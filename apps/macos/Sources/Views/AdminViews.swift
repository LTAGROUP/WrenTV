import SwiftUI
import AppKit

/// Streamed titles being saved to the server as files.
struct ServerDownloadsView: View {
    @Environment(AppState.self) private var state
    @State private var downloads: [ServerDownload] = []
    @State private var loaded = false

    var body: some View {
        let active = downloads.filter(\.active)
        let rest = downloads.filter { !$0.active }
        PageScroll(maxWidth: 960) {
            if !loaded {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 160)
            } else if downloads.isEmpty {
                ContentUnavailableView("Nothing downloading", systemImage: "externaldrive.badge.plus",
                                       description: Text("Open a streamed title and choose Stream → Download to Server, or turn on download mode in Settings → Streaming."))
                    .frame(maxWidth: .infinity)
                    .padding(.top, 80)
            }
            if !active.isEmpty {
                CardSection("In progress", subtitle: "\(active.count)") { ForEach(active) { row($0) } }
            }
            if !rest.isEmpty {
                CardSection("Finished") { ForEach(rest) { row($0) } }
            }
        }
        .navigationTitle("Server Downloads")
        .task { await poll() }
    }

    private func row(_ d: ServerDownload) -> some View {
        HStack(spacing: 14) {
            Image(systemName: icon(d.status))
                .font(.title3)
                .foregroundStyle(color(d.status))
                .frame(width: 26)
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    Text(d.title).font(.headline).lineLimit(1)
                    if !d.quality.isEmpty { Badge(text: d.quality) }
                }
                if let e = d.error, !e.isEmpty {
                    Text(e).font(.caption).foregroundStyle(.red).lineLimit(1)
                } else if d.status == "downloading" {
                    ProgressView(value: d.fraction).tint(Color.wrenEmber).frame(maxWidth: 360)
                    Text("\(size(d.bytes)) of \(size(d.total))").font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                } else if d.status == "done", let p = d.path {
                    Text(p).font(.caption.monospaced()).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                } else {
                    Text([d.status.capitalized, d.total > 0 ? size(d.total) : nil].compactMap { $0 }.joined(separator: " · "))
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            Spacer()
            HStack(spacing: 8) {
                if d.status == "failed" || d.status == "canceled" {
                    Button("Retry") { act { try await $0.retryServerDownload(d.id) } }
                }
                Button(d.active ? "Cancel" : "Clear", role: d.active ? .destructive : nil) { act { try await $0.cancelServerDownload(d.id) } }
            }
            .buttonStyle(.softCapsule)
        }
    }

    /// Refreshes every few seconds while something is in flight.
    private func poll() async {
        while !Task.isCancelled {
            if let d = try? await state.api?.serverDownloads() { downloads = d }
            loaded = true
            try? await Task.sleep(for: .seconds(downloads.contains(where: \.active) ? 3 : 15))
        }
    }

    private func act(_ f: @escaping (WrenTVAPI) async throws -> Void) {
        Task {
            guard let api = state.api else { return }
            try? await f(api)
            if let d = try? await api.serverDownloads() { downloads = d }
        }
    }

    private func size(_ b: Int64) -> String { ByteCountFormatter.string(fromByteCount: b, countStyle: .file) }

    private func icon(_ s: String) -> String {
        switch s {
        case "done": "checkmark.circle.fill"
        case "queued", "downloading": "arrow.down.circle"
        case "canceled": "minus.circle"
        default: "xmark.octagon.fill"
        }
    }

    private func color(_ s: String) -> Color {
        switch s {
        case "done": .green
        case "queued", "downloading": .wrenEmber
        case "canceled": .secondary
        default: .red
        }
    }
}

/// Server accounts and their streaming limits.
struct UsersView: View {
    @Environment(AppState.self) private var state
    @State private var users: [ServerUser]?
    @State private var defaults = UserDefaultsSettings()
    @State private var editing: ServerUser?

    var body: some View {
        PageScroll(maxWidth: 1100) {
            if let users {
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 320), spacing: 16)], alignment: .leading, spacing: 16) {
                    ForEach(users) { card($0) }
                }
                Text("Add people and choose which libraries they see in the web app.")
                    .font(.caption).foregroundStyle(.secondary)
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 160)
            }
        }
        .navigationTitle("Users")
        .navigationSubtitle(users.map { "\($0.count) \($0.count == 1 ? "person" : "people")" } ?? "")
        .sheet(item: $editing) { u in
            UserLimitsSheet(user: u, defaults: defaults) { Task { await load() } }
        }
        .task { await load() }
    }

    private func card(_ u: ServerUser) -> some View {
        let name = u.displayName.isEmpty ? u.username : u.displayName
        return HStack(alignment: .top, spacing: 14) {
            Text(initials(name))
                .font(.system(size: 16, weight: .semibold))
                .foregroundStyle(.white)
                .frame(width: 44, height: 44)
                .background(u.isAdmin ? Color.wrenEmber : Color.white.opacity(0.12), in: Circle())
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 8) {
                    Text(name).font(.headline).lineLimit(1)
                    if u.isAdmin { Badge(text: "Admin") }
                }
                if let e = u.email, !e.isEmpty { Text(e).font(.caption).foregroundStyle(.secondary).lineLimit(1) }
                Text(summary(u.effective)).font(.caption).foregroundStyle(.secondary).padding(.top, 3)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 0)
            Button("Edit") { editing = u }.buttonStyle(.softCapsule)
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .cardBackground()
        .contentShape(RoundedRectangle(cornerRadius: 12))
        .onTapGesture(count: 2) { editing = u }
    }

    private func initials(_ name: String) -> String {
        let parts = name.split(separator: " ").prefix(2)
        let s = parts.compactMap(\.first).map(String.init).joined()
        return (s.isEmpty ? "?" : s).uppercased()
    }

    private func load() async {
        guard let api = state.api else { return }
        users = (try? await api.users()) ?? []
        if let s = try? await api.settings() { defaults = s.userDefaults }
    }

    private func summary(_ e: UserLimitsInfo) -> String {
        var parts: [String] = []
        if !e.canStream && state.server?.streaming == true { parts.append("No streaming") }
        parts.append(e.maxStreams > 0 ? "\(e.maxStreams) stream\(e.maxStreams == 1 ? "" : "s") at once" : "Unlimited streams")
        parts.append(e.canDownload ? (e.maxDownloads > 0 ? "\(e.maxDownloads) download\(e.maxDownloads == 1 ? "" : "s") at once" : "Downloads") : "No downloads")
        return parts.joined(separator: " · ")
    }
}

private struct UserLimitsSheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let user: ServerUser
    let defaults: UserDefaultsSettings
    var onSave: () -> Void
    @State private var allowStreaming = true
    @State private var allowDownloads = -1
    @State private var maxStreams = ""
    @State private var maxDownloads = ""
    @State private var error: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text(user.displayName.isEmpty ? user.username : user.displayName).font(.title2.bold())
            Form {
                Toggle("Can play titles streamed through debrid", isOn: $allowStreaming)
                Picker("Downloads to devices", selection: $allowDownloads) {
                    Text("Server default (\(defaults.deviceDownloads ? "allowed" : "off"))").tag(-1)
                    Text("Allowed").tag(1)
                    Text("Not allowed").tag(0)
                }
                TextField("Streams at once", text: $maxStreams, prompt: Text(defaultText(defaults.maxStreams)))
                TextField("Downloads at once", text: $maxDownloads, prompt: Text(defaultText(defaults.maxDownloads)))
            }
            .formStyle(.grouped)
            Text("Leave a number blank for the server default; 0 means no limit.").font(.caption).foregroundStyle(.secondary)
            if let error { Text(error).foregroundStyle(.red).font(.callout) }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Save", action: save).keyboardShortcut(.defaultAction)
            }
        }
        .padding(22)
        .frame(width: 460)
        .onAppear {
            allowStreaming = user.allowStreaming
            allowDownloads = user.allowDownloads
            maxStreams = user.maxStreams >= 0 ? String(user.maxStreams) : ""
            maxDownloads = user.maxDownloads >= 0 ? String(user.maxDownloads) : ""
        }
    }

    private func defaultText(_ n: Int) -> String { "Default (\(n > 0 ? String(n) : "no limit"))" }

    private func number(_ s: String) -> Int {
        let t = s.trimmingCharacters(in: .whitespaces)
        return t.isEmpty ? -1 : max(0, Int(t) ?? 0)
    }

    private func save() {
        Task {
            do {
                try await state.api?.setUserLimits(user.id, UserLimitsPatch(allowStreaming: allowStreaming, allowDownloads: allowDownloads,
                                                                             maxStreams: number(maxStreams), maxDownloads: number(maxDownloads)))
                onSave()
                dismiss()
            } catch {
                self.error = error.localizedDescription
            }
        }
    }
}

struct ManageLibrariesView: View {
    @Environment(AppState.self) private var state
    @State private var adding = false
    @State private var confirmDelete: Library?
    /// Libraries a scan was just started for, to show it took.
    @State private var scanning: Set<String> = []

    var body: some View {
        PageScroll(maxWidth: 960) {
            if state.libraries.isEmpty {
                ContentUnavailableView {
                    Label("No libraries", systemImage: "folder")
                } description: {
                    Text("Point the server at a folder of movies or shows.")
                } actions: {
                    Button("Add Library") { adding = true }.buttonStyle(.primaryCapsule)
                }
                .frame(maxWidth: .infinity)
                .padding(.top, 80)
            } else {
                VStack(spacing: 14) {
                    ForEach(state.libraries) { card($0) }
                }
            }
        }
        .navigationTitle("Libraries")
        .toolbar {
            Button { adding = true } label: { Label("Add Library", systemImage: "plus") }
        }
        .sheet(isPresented: $adding) { AddLibrarySheet() }
        .confirmationDialog("Remove \(confirmDelete?.name ?? "")?", isPresented: Binding(get: { confirmDelete != nil }, set: { if !$0 { confirmDelete = nil } })) {
            Button("Remove Library", role: .destructive) {
                if let lib = confirmDelete {
                    Task {
                        try? await state.api?.deleteLibrary(lib.id)
                        await state.reloadLibraries()
                    }
                }
            }
        } message: {
            Text(confirmDelete?.isStream == true ? "Its titles and saved streams are removed. It comes back empty the next time someone streams." : "Files on disk are not touched.")
        }
    }

    private func card(_ lib: Library) -> some View {
        HStack(spacing: 16) {
            Image(systemName: lib.symbol)
                .font(.system(size: 20, weight: .medium))
                .foregroundStyle(Color.wrenEmber)
                .frame(width: 48, height: 48)
                .background(Color.wrenEmber.opacity(0.14), in: RoundedRectangle(cornerRadius: 11))
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    Text(lib.name).font(.headline)
                    Badge(text: lib.type == "shows" ? "TV" : "Movies")
                    if lib.isStream { Badge(text: "Streamed") }
                }
                if lib.isStream {
                    Text("Streamed through the server's debrid service").font(.caption).foregroundStyle(.secondary)
                } else {
                    Text(lib.paths.joined(separator: ", ")).font(.caption.monospaced()).foregroundStyle(.secondary)
                        .lineLimit(2).truncationMode(.middle)
                }
                Text(counts(lib)).font(.caption).foregroundStyle(.secondary)
            }
            Spacer(minLength: 12)
            if !lib.isStream {
                Button(scanning.contains(lib.id) ? "Scanning…" : "Scan") { scan(lib) }
                    .buttonStyle(.softCapsule)
                    .disabled(scanning.contains(lib.id))
            }
            Button(role: .destructive) { confirmDelete = lib } label: { Image(systemName: "trash") }
                .buttonStyle(.iconCircle)
                .help("Remove library")
        }
        .padding(16)
        .cardBackground()
    }

    private func counts(_ lib: Library) -> String {
        let n = lib.itemCount ?? 0
        var s = "\(n) item\(n == 1 ? "" : "s")"
        if let t = lib.lastScanAt, t > 0 {
            s += " · scanned " + Date(timeIntervalSince1970: TimeInterval(t)).formatted(.relative(presentation: .named))
        }
        return s
    }

    private func scan(_ lib: Library) {
        scanning.insert(lib.id)
        Task {
            try? await state.api?.scanLibrary(lib.id)
            try? await Task.sleep(for: .seconds(4))
            await state.reloadLibraries()
            scanning.remove(lib.id)
        }
    }
}

struct AddLibrarySheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var type = "movies"
    @State private var paths = ""
    @State private var error: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Add Library").font(.title2.bold())
            Form {
                TextField("Name", text: $name)
                Picker("Type", selection: $type) {
                    Text("Movies").tag("movies")
                    Text("TV Shows").tag("shows")
                }
                .pickerStyle(.segmented)
                LabeledContent("Folders") {
                    VStack(alignment: .leading) {
                        TextField("One path per line, as seen by the server", text: $paths, axis: .vertical)
                            .lineLimit(3...6)
                            .font(.body.monospaced())
                        Button("Browse…", action: browse).controlSize(.small)
                    }
                }
            }
            if let error { Text(error).foregroundStyle(.red).font(.callout) }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Add") { add() }.keyboardShortcut(.defaultAction).disabled(name.isEmpty || paths.isEmpty)
            }
        }
        .padding(22)
        .frame(width: 520)
    }

    /// Only meaningful when the server runs on this Mac.
    private func browse() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = true
        if panel.runModal() == .OK {
            let picked = panel.urls.map(\.path).joined(separator: "\n")
            paths = paths.isEmpty ? picked : paths + "\n" + picked
            if name.isEmpty, let first = panel.urls.first { name = first.lastPathComponent }
        }
    }

    private func add() {
        let list = paths.split(whereSeparator: \.isNewline).map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
        Task {
            do {
                _ = try await state.api?.createLibrary(name: name, type: type, paths: list)
                await state.reloadLibraries()
                dismiss()
            } catch {
                self.error = error.localizedDescription
            }
        }
    }
}

struct ServerSettingsView: View {
    private enum Tab: String, CaseIterable { case general = "General", streaming = "Streaming", users = "Users", integrations = "Integrations" }

    @Environment(AppState.self) private var state
    @State private var tab = Tab.general
    @State private var settings: ServerSettings?
    @State private var serverName = ""
    @State private var publicUrl = ""
    @State private var hwaccel = "auto"
    @State private var tmdbKey = ""
    @State private var omdbKey = ""
    @State private var fingerprinting = true
    @State private var webhooks: [WebhookSetting] = []
    // Streaming
    @State private var streaming: StreamingSettings?
    @State private var keyEdits: [String: String] = [:]
    /// Services whose key is removed on save.
    @State private var disconnects: Set<String> = []
    @State private var accounts: [String: String] = [:]
    @State private var newAddon = ""
    @State private var addonStatus: [String: String] = [:]
    @State private var addonError: String?
    @State private var userDefaults = UserDefaultsSettings()
    @State private var status: String?
    @State private var saving = false

    private static let qualities = [("2160p", "4K"), ("1080p", "1080p"), ("720p", "720p")]

    var body: some View {
        PageScroll(maxWidth: 780) {
            HStack(spacing: 8) {
                ForEach(Tab.allCases, id: \.self) { t in Chip(t.rawValue, selected: tab == t) { tab = t } }
            }
            if settings == nil {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 120)
            } else {
                switch tab {
                case .general: general
                case .streaming: streamingTab
                case .users: usersTab
                case .integrations: integrations
                }
            }
        }
        .navigationTitle("Settings")
        .toolbar {
            if let status { Text(status).foregroundStyle(.secondary).lineLimit(1) }
            Button("Save", action: save).keyboardShortcut("s").disabled(settings == nil || saving)
        }
        .task { await load() }
    }

    // MARK: General

    @ViewBuilder
    private var general: some View {
        CardSection("Server") {
            SettingRow("Name") { TextField("Name", text: $serverName).wrenField().frame(width: 320) }
            SettingRow("Public URL", detail: "Where people reach the server from outside your network") {
                TextField("Public URL", text: $publicUrl, prompt: Text("https://wrentv.example.com")).wrenField().frame(width: 320)
            }
        }
        CardSection("Metadata", footer: "OMDb adds Rotten Tomatoes, IMDb and Metacritic scores; get a free key at omdbapi.com.") {
            SettingRow("TMDB API key", detail: "Required for artwork, metadata and streaming") {
                SecureField("TMDB API key", text: $tmdbKey, prompt: Text(settings?.tmdbKeySet == true ? "Saved — enter to replace" : "API key"))
                    .wrenField().frame(width: 320)
            }
            SettingRow("OMDb API key", detail: "Optional") {
                SecureField("OMDb API key", text: $omdbKey, prompt: Text(settings?.omdbKeySet == true ? "Saved — enter to replace" : "API key"))
                    .wrenField().frame(width: 320)
            }
        }
        CardSection("Playback", footer: "Audio detection compares episodes of a season to find shared intros and credits. Chapters are always used.") {
            SettingRow("Hardware transcoding") {
                Picker("Hardware transcoding", selection: $hwaccel) {
                    ForEach(["auto", "videotoolbox", "nvenc", "qsv", "vaapi", "amf", "none"], id: \.self) { Text($0).tag($0) }
                }
                .labelsHidden().fixedSize()
            }
            SettingToggle("Detect intros and credits from audio", isOn: $fingerprinting)
        }
    }

    // MARK: Streaming

    @ViewBuilder
    private var streamingTab: some View {
        if let binding = Binding($streaming) {
            CardSection("Streaming", footer: "Search shows every movie and show. Pressing play finds a cached release through your debrid service and streams it through this server. Only the link is saved, never the video, unless downloads are on.") {
                SettingToggle("Enable streaming", isOn: binding.enabled)
                SettingRow("Status") { statusText(binding.wrappedValue) }
                SettingToggle("Find streams for the rest of the season in the background", isOn: binding.prefetch)
            }
            CardSection("Debrid services", footer: "Your keys are used for everyone, tried top to bottom. If every key is removed, streamed titles disappear until one is added back.") {
                ForEach(Array(binding.wrappedValue.debrid.enumerated()), id: \.element.id) { i, d in
                    debridRow(d, index: i, count: binding.wrappedValue.debrid.count)
                }
            }
            CardSection("Addons", footer: "Stremio addons that list torrents for a title. Results from every enabled addon are merged.") {
                ForEach(binding.addons.indices, id: \.self) { i in
                    addonRow(binding.addons[i], index: i)
                }
                VStack(alignment: .leading, spacing: 6) {
                    HStack {
                        TextField("Add addon", text: $newAddon, prompt: Text("https://…/manifest.json"))
                            .wrenField()
                            .onSubmit(addAddon)
                        Button("Add", action: addAddon)
                            .buttonStyle(.softCapsule)
                            .disabled(newAddon.trimmingCharacters(in: .whitespaces).isEmpty)
                    }
                    if let addonError { Text(addonError).font(.caption).foregroundStyle(.red) }
                }
            }
            CardSection("Quality", footer: "A stream is saved for each quality. Size limits apply to the file that plays (one episode of a season pack, not the whole pack); 0 is any size.") {
                ForEach(Self.qualities, id: \.0) { q, label in
                    let on = binding.wrappedValue.qualities.contains(q)
                    SettingRow(label) {
                        HStack(spacing: 6) {
                            TextField("", value: Binding(
                                get: { binding.wrappedValue.maxSizeGB?[q] ?? 0 },
                                set: { binding.wrappedValue.maxSizeGB = (binding.wrappedValue.maxSizeGB ?? [:]).merging([q: max(0, $0)]) { $1 } }),
                                      format: .number, prompt: Text("Any size"))
                                .multilineTextAlignment(.trailing)
                                .wrenField()
                                .frame(width: 90)
                            Text("GB max").foregroundStyle(.secondary).frame(width: 52, alignment: .leading)
                        }
                        .disabled(!on)
                        .opacity(on ? 1 : 0.4)
                        Toggle(label, isOn: Binding(
                            get: { on },
                            set: { on in
                                var qs = binding.wrappedValue.qualities.filter { $0 != q }
                                if on { qs.append(q) }
                                binding.wrappedValue.qualities = Self.qualities.map(\.0).filter(qs.contains)
                            }))
                            .toggleStyle(.switch).labelsHidden().tint(Color.wrenEmber)
                    }
                }
            }
            CardSection("Saving to the server", footer: "Stream only writes nothing to your media folders; admins can still download single titles. Download mode saves each streamed title as a regular file, which then replaces the stream.") {
                SettingRow("Mode") {
                    Picker("Mode", selection: binding.mode) {
                        Text("Stream only").tag("stream")
                        Text("Download what's watched").tag("download")
                    }
                    .pickerStyle(.segmented).labelsHidden().fixedSize()
                }
                SettingRow("Movies go to") {
                    Picker("Movies go to", selection: binding.movieLibrary) {
                        Text("None").tag("")
                        ForEach(state.libraries.filter { !$0.isStream && $0.type == "movies" }) { Text($0.name).tag($0.id) }
                    }
                    .labelsHidden().fixedSize()
                }
                SettingRow("Shows go to") {
                    Picker("Shows go to", selection: binding.showLibrary) {
                        Text("None").tag("")
                        ForEach(state.libraries.filter { !$0.isStream && $0.type == "shows" }) { Text($0.name).tag($0.id) }
                    }
                    .labelsHidden().fixedSize()
                }
                SettingRow("Download quality") {
                    Picker("Download quality", selection: binding.downloadQuality) {
                        ForEach(Self.qualities, id: \.0) { Text($0.1).tag($0.0) }
                    }
                    .labelsHidden().fixedSize()
                }
            }
            CardSection("Limits", footer: "0 means no limit.") {
                limitField("All debrid traffic", binding.limits.bandwidthMbps, unit: "Mbps")
                limitField("Each stream", binding.limits.perStreamMbps, unit: "Mbps")
                limitField("Server downloads", binding.limits.downloadMbps, unit: "Mbps")
                limitField("Streams at once (all users)", binding.limits.maxStreams)
                limitField("Server downloads at once", binding.limits.maxDownloads)
                limitField("Background lookups at once", binding.limits.prefetchWorkers)
                limitField("Total downloaded", binding.limits.downloadQuotaGB, unit: "GB")
                limitField("Keep this much disk free", binding.limits.minFreeGB, unit: "GB")
            }
        }
    }

    private func statusText(_ st: StreamingSettings) -> some View {
        let (text, color): (String, Color) =
            !st.enabled ? ("Off", .secondary)
            : st.active ? ("On", .green)
            : settings?.tmdbKeySet != true ? ("Needs a TMDB API key", .red)
            : !st.debrid.contains(where: \.keySet) ? ("Needs a debrid API key", .red)
            : !st.addons.contains(where: \.enabled) ? ("Needs an addon", .red)
            : ("Starting…", .secondary)
        return Text(text).foregroundStyle(color)
    }

    private func debridRow(_ d: DebridService, index: Int, count: Int) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 8) {
                Circle()
                    .fill(d.keySet && !disconnects.contains(d.provider) ? Color.green : Color.white.opacity(0.2))
                    .frame(width: 8, height: 8)
                VStack(alignment: .leading, spacing: 2) {
                    Text(d.name).font(.headline)
                    Text(accounts[d.provider] ?? (disconnects.contains(d.provider) ? "Disconnects when you save"
                        : d.keySet ? "Key ending \(d.keyHint ?? "••••")" : "Not connected"))
                        .font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                Button { move(index, by: -1) } label: { Image(systemName: "chevron.up") }
                    .buttonStyle(.iconCircle).disabled(index == 0).help("Try earlier")
                Button { move(index, by: 1) } label: { Image(systemName: "chevron.down") }
                    .buttonStyle(.iconCircle).disabled(index == count - 1).help("Try later")
            }
            HStack(spacing: 8) {
                SecureField("API key", text: Binding(get: { keyEdits[d.provider] ?? "" }, set: { keyEdits[d.provider] = $0; disconnects.remove(d.provider) }),
                            prompt: Text(d.keySet ? "Paste a new key to replace" : "API key"))
                    .wrenField()
                Button("Check") { check(d.provider) }.buttonStyle(.softCapsule)
                if d.keySet && !disconnects.contains(d.provider) {
                    Button("Disconnect", role: .destructive) { keyEdits[d.provider] = nil; disconnects.insert(d.provider) }
                        .buttonStyle(.softCapsule)
                }
            }
        }
    }

    private func addonRow(_ addon: Binding<AddonSetting>, index: Int) -> some View {
        HStack(spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                Text(addon.wrappedValue.name)
                Text(addonStatus[addon.wrappedValue.url] ?? addon.wrappedValue.url)
                    .font(.caption.monospaced()).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
            }
            Spacer(minLength: 12)
            Button("Check") {
                let url = addon.wrappedValue.url
                addonStatus[url] = "Checking…"
                Task {
                    do {
                        let a = try await state.api?.checkAddon(url)
                        addonStatus[url] = "Working · \(a?.name ?? "")"
                    } catch {
                        addonStatus[url] = error.localizedDescription
                    }
                }
            }
            .buttonStyle(.softCapsule)
            Button(role: .destructive) { streaming?.addons.remove(at: index) } label: { Image(systemName: "trash") }
                .buttonStyle(.iconCircle)
                .help("Remove addon")
            Toggle(addon.wrappedValue.name, isOn: addon.enabled).toggleStyle(.switch).labelsHidden().tint(Color.wrenEmber)
        }
    }

    private func limitField(_ title: String, _ value: Binding<Int>, unit: String = "") -> some View {
        SettingRow(title) {
            HStack(spacing: 6) {
                TextField("", value: value, format: .number, prompt: Text("No limit"))
                    .multilineTextAlignment(.trailing).wrenField().frame(width: 90)
                Text(unit).foregroundStyle(.secondary).frame(width: 42, alignment: .leading)
            }
        }
    }

    private func limitField(_ title: String, _ value: Binding<Double>, unit: String) -> some View {
        SettingRow(title) {
            HStack(spacing: 6) {
                TextField("", value: value, format: .number, prompt: Text("No limit"))
                    .multilineTextAlignment(.trailing).wrenField().frame(width: 90)
                Text(unit).foregroundStyle(.secondary).frame(width: 42, alignment: .leading)
            }
        }
    }

    private func move(_ i: Int, by d: Int) {
        guard var st = streaming, st.debrid.indices.contains(i + d) else { return }
        st.debrid.swapAt(i, i + d)
        streaming = st
    }

    private func check(_ provider: String) {
        accounts[provider] = "Checking…"
        let key = keyEdits[provider] ?? ""
        Task {
            do {
                guard let a = try await state.api?.checkDebrid(provider: provider, key: key) else { return }
                if a.ok {
                    let until = a.expires.flatMap { ISO8601DateFormatter().date(from: $0) }.map { " · until \($0.formatted(date: .abbreviated, time: .omitted))" } ?? ""
                    accounts[provider] = "\(a.premium ? "Premium" : "Not premium")\(a.user.map { " · \($0)" } ?? "")\(until)"
                } else {
                    accounts[provider] = a.error ?? "Key rejected"
                }
            } catch {
                accounts[provider] = error.localizedDescription
            }
        }
    }

    private func addAddon() {
        let url = newAddon.trimmingCharacters(in: .whitespaces)
        guard !url.isEmpty else { return }
        addonError = nil
        Task {
            do {
                guard var a = try await state.api?.checkAddon(url) else { return }
                if streaming?.addons.contains(where: { $0.url == a.url }) == true { throw WrenTVError(message: "That addon is already in the list") }
                a.name = a.name.isEmpty ? (URL(string: a.url)?.host ?? "addon") : a.name.lowercased()
                streaming?.addons.append(a)
                newAddon = ""
            } catch {
                addonError = error.localizedDescription
            }
        }
    }

    // MARK: Users

    @ViewBuilder
    private var usersTab: some View {
        CardSection("Defaults for everyone", footer: "Applies unless a person's own settings say otherwise (Server → Users). 0 means no limit. Playing the same title on two screens counts once, and a stream left paused for two minutes makes room for a new one.") {
            SettingToggle("Let people download movies and episodes to their devices", isOn: $userDefaults.deviceDownloads)
            limitField("Streams at once per person", $userDefaults.maxStreams)
            limitField("Device downloads at once per person", $userDefaults.maxDownloads)
        }
    }

    // MARK: Integrations

    @ViewBuilder
    private var integrations: some View {
        CardSection("Webhooks", footer: "Receives server events such as stream.unavailable, download.completed and download.failed.") {
            ForEach(webhooks.indices, id: \.self) { i in
                HStack(spacing: 8) {
                    TextField("URL", text: $webhooks[i].url, prompt: Text("https://…")).wrenField()
                    SecureField("Secret", text: Binding(get: { webhooks[i].secret ?? "" }, set: { webhooks[i].secret = $0 }), prompt: Text("Secret"))
                        .wrenField()
                        .frame(width: 160)
                    Button(role: .destructive) { webhooks.remove(at: i) } label: { Image(systemName: "minus") }
                        .buttonStyle(.iconCircle)
                        .help("Remove webhook")
                }
            }
            Button { webhooks.append(WebhookSetting(url: "", secret: "", events: [])) } label: { Label("Add Webhook", systemImage: "plus") }
                .buttonStyle(.softCapsule)
        }
        if let key = settings?.integrationKey {
            CardSection("Integration key") {
                HStack {
                    Text(key).font(.callout.monospaced()).textSelection(.enabled).lineLimit(1).truncationMode(.middle)
                    Spacer()
                    Button("Copy") {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(key, forType: .string)
                    }
                    .buttonStyle(.softCapsule)
                }
            }
        }
    }

    // MARK: Load & save

    private func apply(_ s: ServerSettings) {
        settings = s
        serverName = s.serverName
        publicUrl = s.publicUrl
        hwaccel = s.hwaccel.isEmpty ? "auto" : s.hwaccel
        webhooks = s.webhooks ?? []
        tmdbKey = ""
        omdbKey = ""
        fingerprinting = s.markerFingerprinting ?? true
        streaming = s.streaming
        userDefaults = s.userDefaults
        keyEdits = [:]
        disconnects = []
        accounts = [:]
    }

    private func load() async {
        if let s = try? await state.api?.settings() { apply(s) }
    }

    private func save() {
        guard let st = streaming else { return }
        var stream: [String: AnyEncodable] = [
            "enabled": AnyEncodable(st.enabled),
            "prefetch": AnyEncodable(st.prefetch),
            "providerOrder": AnyEncodable(st.debrid.map(\.provider)),
            "addons": AnyEncodable(st.addons),
            "qualities": AnyEncodable(st.qualities),
            "maxSizeGB": AnyEncodable(st.maxSizeGB ?? [:]),
            "mode": AnyEncodable(st.mode),
            "movieLibrary": AnyEncodable(st.movieLibrary),
            "showLibrary": AnyEncodable(st.showLibrary),
            "downloadQuality": AnyEncodable(st.downloadQuality),
            "limits": AnyEncodable(st.limits),
        ]
        for (provider, key) in keyEdits {
            let k = key.trimmingCharacters(in: .whitespacesAndNewlines)
            if !k.isEmpty { stream[provider + "Key"] = AnyEncodable(k) }
        }
        for provider in disconnects { stream[provider + "Key"] = AnyEncodable("") }
        var patch: [String: AnyEncodable] = [
            "serverName": AnyEncodable(serverName),
            "publicUrl": AnyEncodable(publicUrl),
            "hwaccel": AnyEncodable(hwaccel),
            "webhooks": AnyEncodable(webhooks.filter { !$0.url.isEmpty }),
            "markerFingerprinting": AnyEncodable(fingerprinting),
            "streaming": AnyEncodable(stream),
            "userDefaults": AnyEncodable(userDefaults),
        ]
        if !omdbKey.isEmpty { patch["omdbApiKey"] = AnyEncodable(omdbKey) }
        if !tmdbKey.isEmpty { patch["tmdbApiKey"] = AnyEncodable(tmdbKey) }
        saving = true
        Task {
            defer { saving = false }
            do {
                if let s = try await state.api?.patchSettings(patch) {
                    apply(s)
                    status = s.restartRequired == true ? "Saved — restart the server to apply" : "Saved"
                    await state.reloadLibraries()
                }
            } catch {
                status = error.localizedDescription
            }
        }
    }
}
