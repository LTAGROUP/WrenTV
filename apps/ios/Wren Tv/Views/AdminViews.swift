import SwiftUI

/// Streamed titles being saved to the server as files.
struct ServerDownloadsView: View {
    @Environment(AppState.self) private var state
    @State private var downloads: [ServerDownload] = []
    @State private var loaded = false

    var body: some View {
        List(downloads) { d in
            VStack(alignment: .leading, spacing: 6) {
                HStack(alignment: .firstTextBaseline) {
                    Text(d.title).font(.headline).lineLimit(1)
                    Spacer()
                    Text(d.quality).font(.caption).foregroundStyle(.secondary)
                }
                HStack(spacing: 10) {
                    Label(d.status.capitalized, systemImage: icon(d.status)).foregroundStyle(color(d.status))
                    if d.status != "downloading", d.total > 0 {
                        Text(size(d.total)).foregroundStyle(.secondary)
                    }
                }
                .font(.caption)
                if d.status == "downloading" {
                    ProgressView(value: d.fraction).tint(Color.wrenEmber)
                    Text("\(size(d.bytes)) of \(size(d.total))").font(.caption).foregroundStyle(.secondary)
                }
                if let e = d.error, !e.isEmpty {
                    Text(e).font(.caption).foregroundStyle(.red).lineLimit(2)
                } else if d.status == "done", let p = d.path {
                    Text(p).font(.caption.monospaced()).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                }
            }
            .padding(.vertical, 2)
            .swipeActions {
                Button(d.active ? "Cancel" : "Clear", role: d.active ? .destructive : nil) { act { try await $0.cancelServerDownload(d.id) } }
                if d.status == "failed" || d.status == "canceled" {
                    Button("Retry") { act { try await $0.retryServerDownload(d.id) } }.tint(.blue)
                }
            }
        }
        .overlay {
            if loaded && downloads.isEmpty {
                ContentUnavailableView("Nothing downloading", systemImage: "externaldrive.badge.plus",
                                       description: Text("Open a streamed title and choose Stream → Download to Server, or turn on download mode in Settings → Streaming."))
            }
        }
        .navigationTitle("Server Downloads")
        .task { await poll() }
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
        case "queued", "downloading": .blue
        case "canceled": .secondary
        default: .red
        }
    }
}

/// Server accounts and their streaming limits.
struct UsersView: View {
    @Environment(AppState.self) private var state
    @State private var users: [ServerUser] = []
    @State private var defaults = UserDefaultsSettings()
    @State private var editing: ServerUser?

    var body: some View {
        List {
            Section {
                ForEach(users) { u in
                    Button { editing = u } label: {
                        VStack(alignment: .leading, spacing: 3) {
                            HStack {
                                Text(u.displayName.isEmpty ? u.username : u.displayName).foregroundStyle(.primary)
                                Spacer()
                                Text(u.isAdmin ? "Admin" : "Member").font(.caption).foregroundStyle(.secondary)
                            }
                            if let e = u.email, !e.isEmpty { Text(e).font(.caption).foregroundStyle(.secondary) }
                            Text(summary(u.effective)).font(.caption).foregroundStyle(.secondary)
                        }
                    }
                }
            } footer: {
                Text("Add people and choose libraries in the web app.")
            }
        }
        .navigationTitle("Users")
        .sheet(item: $editing) { u in
            UserLimitsSheet(user: u, defaults: defaults) { Task { await load() } }
        }
        .task { await load() }
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
        NavigationStack {
            Form {
                Section {
                    Toggle("Can play titles streamed through debrid", isOn: $allowStreaming)
                    Picker("Downloads to devices", selection: $allowDownloads) {
                        Text("Server default (\(defaults.deviceDownloads ? "allowed" : "off"))").tag(-1)
                        Text("Allowed").tag(1)
                        Text("Not allowed").tag(0)
                    }
                    LabeledContent("Streams at once") {
                        TextField("", text: $maxStreams, prompt: Text(defaultText(defaults.maxStreams)))
                            .keyboardType(.numberPad).multilineTextAlignment(.trailing)
                    }
                    LabeledContent("Downloads at once") {
                        TextField("", text: $maxDownloads, prompt: Text(defaultText(defaults.maxDownloads)))
                            .keyboardType(.numberPad).multilineTextAlignment(.trailing)
                    }
                } footer: {
                    Text("Leave a number blank for the server default; 0 means no limit.")
                }
                if let error { Text(error).foregroundStyle(.red).font(.callout) }
            }
            .navigationTitle(user.displayName.isEmpty ? user.username : user.displayName)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) { Button("Save", action: save) }
            }
        }
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

    var body: some View {
        List {
            ForEach(state.libraries) { lib in
                HStack(spacing: 14) {
                    Image(systemName: lib.symbol).font(.title2).frame(width: 32)
                    VStack(alignment: .leading, spacing: 3) {
                        Text(lib.name).font(.headline)
                        if lib.isStream {
                            Text("Streamed through the server's debrid service").font(.caption).foregroundStyle(.secondary)
                        } else {
                            Text(lib.paths.joined(separator: ", ")).font(.caption.monospaced()).foregroundStyle(.secondary)
                        }
                        Text("\(lib.itemCount ?? 0) items\(lib.lastScanAt.flatMap { $0 > 0 ? " · scanned \(Date(timeIntervalSince1970: TimeInterval($0)).formatted(.relative(presentation: .named)))" : nil } ?? "")")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                    if !lib.isStream {
                        Button("Scan") { Task { try? await state.api?.scanLibrary(lib.id) } }
                            .buttonStyle(.bordered)
                            .controlSize(.small)
                    }
                }
                .padding(.vertical, 6)
                .swipeActions {
                    Button(role: .destructive) { confirmDelete = lib } label: { Label("Remove", systemImage: "trash") }
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
}

struct AddLibrarySheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var type = "movies"
    @State private var paths = ""
    @State private var error: String?

    var body: some View {
        NavigationStack {
            Form {
                TextField("Name", text: $name)
                Picker("Type", selection: $type) {
                    Text("Movies").tag("movies")
                    Text("TV Shows").tag("shows")
                }
                .pickerStyle(.segmented)
                Section {
                    TextField("/media/movies", text: $paths, axis: .vertical)
                        .lineLimit(3...6)
                        .font(.body.monospaced())
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                } header: {
                    Text("Folders")
                } footer: {
                    Text("One path per line, as seen by the server.")
                }
                if let error { Text(error).foregroundStyle(.red).font(.callout) }
            }
            .navigationTitle("Add Library")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) { Button("Add") { add() }.disabled(name.isEmpty || paths.isEmpty) }
            }
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
    @State private var community = true
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
        Form {
            Section {
                Picker("", selection: $tab) {
                    ForEach(Tab.allCases, id: \.self) { Text($0.rawValue).tag($0) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .listRowBackground(Color.clear)
                .listRowInsets(EdgeInsets())
            } footer: {
                if let status { Text(status) }
            }
            switch tab {
            case .general: general
            case .streaming: streamingTab
            case .users: usersTab
            case .integrations: integrations
            }
        }
        .navigationTitle("Server Settings")
        .navigationBarTitleDisplayMode(.inline)
        .scrollDismissesKeyboard(.interactively)
        .toolbar {
            Button("Save", action: save).keyboardShortcut("s").disabled(settings == nil || saving)
        }
        .task { await load() }
    }

    // MARK: General

    @ViewBuilder
    private var general: some View {
        Section("Server") {
            TextField("Name", text: $serverName)
            TextField("Public URL", text: $publicUrl, prompt: Text("https://wrentv.example.com"))
        }
        Section {
            SecureField("TMDB API key", text: $tmdbKey, prompt: Text(settings?.tmdbKeySet == true ? "Saved — enter to replace" : "Required for artwork, metadata and streaming"))
            SecureField("OMDb API key", text: $omdbKey, prompt: Text(settings?.omdbKeySet == true ? "Saved — enter to replace" : "Optional"))
        } header: {
            Text("Metadata")
        } footer: {
            Text("OMDb adds Rotten Tomatoes, IMDb and Metacritic scores; get a free key at omdbapi.com.").font(.caption).foregroundStyle(.secondary)
        }
        Section {
            Picker("Hardware transcoding", selection: $hwaccel) {
                ForEach(["auto", "videotoolbox", "nvenc", "qsv", "vaapi", "amf", "none"], id: \.self) { Text($0).tag($0) }
            }
            Toggle("Detect intros and credits from audio", isOn: $fingerprinting)
            Toggle("Use community timestamps from IntroDB", isOn: $community)
        } header: {
            Text("Playback")
        } footer: {
            Text("Audio detection compares episodes of a season to find shared intros and credits. Chapters are always used. Community timestamps fill in what's still missing, sending the title's IMDb id to IntroDB.")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    // MARK: Streaming

    @ViewBuilder
    private var streamingTab: some View {
        if let binding = Binding($streaming) {
            Section {
                Toggle("Enable streaming", isOn: binding.enabled)
                LabeledContent("Status") { statusText(binding.wrappedValue) }
                Toggle("Find streams for the rest of the season in the background", isOn: binding.prefetch)
                Toggle("Find streams when a details page opens", isOn: Binding(get: { binding.wrappedValue.lookupOnOpen ?? true }, set: { binding.wrappedValue.lookupOnOpen = $0 }))
            } header: {
                Text("Streaming")
            } footer: {
                Text("Search shows every movie and show. Pressing play finds a cached release through your debrid service and streams it through this server. Only the link is saved, never the video, unless downloads are on.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Section {
                ForEach(Array(binding.wrappedValue.debrid.enumerated()), id: \.element.id) { i, d in
                    debridRow(d, index: i, count: binding.wrappedValue.debrid.count)
                }
            } header: {
                Text("Debrid services")
            } footer: {
                Text("Your keys are used for everyone, tried top to bottom. If every key is removed, streamed titles disappear until one is added back.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Section {
                ForEach(binding.addons.indices, id: \.self) { i in
                    addonRow(binding.addons[i], index: i)
                }
                HStack {
                    TextField("Add addon", text: $newAddon, prompt: Text("https://…/manifest.json"))
                        .onSubmit(addAddon)
                    Button("Add", action: addAddon).disabled(newAddon.trimmingCharacters(in: .whitespaces).isEmpty)
                }
                if let addonError { Text(addonError).font(.caption).foregroundStyle(.red) }
            } header: {
                Text("Addons")
            } footer: {
                Text("Stremio addons that list torrents for a title. Results from every enabled addon are merged.").font(.caption).foregroundStyle(.secondary)
            }
            Section {
                ForEach(Self.qualities, id: \.0) { q, label in
                    HStack {
                        Toggle(label, isOn: Binding(
                            get: { binding.wrappedValue.qualities.contains(q) },
                            set: { on in
                                var qs = binding.wrappedValue.qualities.filter { $0 != q }
                                if on { qs.append(q) }
                                binding.wrappedValue.qualities = Self.qualities.map(\.0).filter(qs.contains)
                            }))
                        Spacer()
                        TextField("", value: Binding(
                            get: { binding.wrappedValue.maxSizeGB?[q] ?? 0 },
                            set: { binding.wrappedValue.maxSizeGB = (binding.wrappedValue.maxSizeGB ?? [:]).merging([q: max(0, $0)]) { $1 } }),
                                  format: .number, prompt: Text("Any size"))
                            .frame(width: 80)
                            .multilineTextAlignment(.trailing)
                        Text("GB max").foregroundStyle(.secondary)
                    }
                }
            } header: {
                Text("Quality")
            } footer: {
                Text("A stream is saved for each quality. Size limits apply to the file that plays (one episode of a season pack, not the whole pack); 0 is any size.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Section {
                Picker("Mode", selection: binding.mode) {
                    Text("Stream only").tag("stream")
                    Text("Download what's watched").tag("download")
                }
                .pickerStyle(.inline)
                Picker("Movies go to", selection: binding.movieLibrary) {
                    Text("None").tag("")
                    ForEach(state.libraries.filter { !$0.isStream && $0.type == "movies" }) { Text($0.name).tag($0.id) }
                }
                Picker("Shows go to", selection: binding.showLibrary) {
                    Text("None").tag("")
                    ForEach(state.libraries.filter { !$0.isStream && $0.type == "shows" }) { Text($0.name).tag($0.id) }
                }
                Picker("Download quality", selection: binding.downloadQuality) {
                    ForEach(Self.qualities, id: \.0) { Text($0.1).tag($0.0) }
                }
            } header: {
                Text("Saving to the server")
            } footer: {
                Text("Stream only writes nothing to your media folders; admins can still download single titles. Download mode saves each streamed title as a regular file, which then replaces the stream.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Section {
                limitField("All debrid traffic", binding.limits.bandwidthMbps, unit: "Mbps")
                limitField("Each stream", binding.limits.perStreamMbps, unit: "Mbps")
                limitField("Server downloads", binding.limits.downloadMbps, unit: "Mbps")
                limitField("Streams at once (all users)", binding.limits.maxStreams)
                limitField("Server downloads at once", binding.limits.maxDownloads)
                limitField("Background lookups at once", binding.limits.prefetchWorkers)
                limitField("Total downloaded", binding.limits.downloadQuotaGB, unit: "GB")
                limitField("Keep this much disk free", binding.limits.minFreeGB, unit: "GB")
            } header: {
                Text("Limits")
            } footer: {
                Text("0 means no limit.").font(.caption).foregroundStyle(.secondary)
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
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(d.name).font(.headline)
                    Text(accounts[d.provider] ?? (disconnects.contains(d.provider) ? "Disconnects when you save"
                        : d.keySet ? "Key ending \(d.keyHint ?? "••••")" : "Not connected"))
                        .font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                Button { move(index, by: -1) } label: { Image(systemName: "chevron.up") }
                    .buttonStyle(.borderless).disabled(index == 0).help("Try earlier")
                Button { move(index, by: 1) } label: { Image(systemName: "chevron.down") }
                    .buttonStyle(.borderless).disabled(index == count - 1).help("Try later")
            }
            HStack {
                SecureField("API key", text: Binding(get: { keyEdits[d.provider] ?? "" }, set: { keyEdits[d.provider] = $0; disconnects.remove(d.provider) }),
                            prompt: Text(d.keySet ? "Paste a new key to replace" : "API key"))
                Button("Check") { check(d.provider) }
                if d.keySet && !disconnects.contains(d.provider) {
                    Button("Disconnect", role: .destructive) { keyEdits[d.provider] = nil; disconnects.insert(d.provider) }
                }
            }
        }
        .padding(.vertical, 2)
    }

    private func addonRow(_ addon: Binding<AddonSetting>, index: Int) -> some View {
        HStack {
            Toggle(isOn: addon.enabled) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(addon.wrappedValue.name)
                    Text(addonStatus[addon.wrappedValue.url] ?? addon.wrappedValue.url)
                        .font(.caption.monospaced()).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                }
            }
            Spacer()
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
            Button(role: .destructive) { streaming?.addons.remove(at: index) } label: { Image(systemName: "trash") }
                .buttonStyle(.borderless)
        }
    }

    private func limitField(_ title: String, _ value: Binding<Int>, unit: String = "") -> some View {
        LabeledContent(title) {
            HStack(spacing: 6) {
                TextField("", value: value, format: .number, prompt: Text("No limit")).frame(width: 90).multilineTextAlignment(.trailing)
                if !unit.isEmpty { Text(unit).foregroundStyle(.secondary).frame(width: 42, alignment: .leading) }
            }
        }
    }

    private func limitField(_ title: String, _ value: Binding<Double>, unit: String) -> some View {
        LabeledContent(title) {
            HStack(spacing: 6) {
                TextField("", value: value, format: .number, prompt: Text("No limit")).frame(width: 90).multilineTextAlignment(.trailing)
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
        Section {
            Toggle("Let people download movies and episodes to their devices", isOn: $userDefaults.deviceDownloads)
            limitField("Streams at once per person", $userDefaults.maxStreams)
            limitField("Device downloads at once per person", $userDefaults.maxDownloads)
        } header: {
            Text("Defaults for everyone")
        } footer: {
            Text("Applies unless a person's own settings say otherwise (Server → Users). 0 means no limit. Playing the same title on two screens counts once, and a stream left paused for two minutes makes room for a new one.")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    // MARK: Integrations

    @ViewBuilder
    private var integrations: some View {
        Section {
            ForEach(webhooks.indices, id: \.self) { i in
                HStack {
                    TextField("URL", text: $webhooks[i].url)
                    SecureField("Secret", text: Binding(get: { webhooks[i].secret ?? "" }, set: { webhooks[i].secret = $0 }))
                        .frame(width: 140)
                    Button(role: .destructive) { webhooks.remove(at: i) } label: { Image(systemName: "minus.circle") }
                        .buttonStyle(.borderless)
                }
            }
            Button("Add Webhook") { webhooks.append(WebhookSetting(url: "", secret: "", events: [])) }
        } header: {
            Text("Webhooks")
        } footer: {
            Text("Receives server events such as stream.unavailable, download.completed and download.failed.").font(.caption).foregroundStyle(.secondary)
        }
        if let key = settings?.integrationKey {
            Section("Integration key") {
                HStack {
                    Text(key).font(.callout.monospaced()).textSelection(.enabled)
                    Spacer()
                    Button("Copy") {
                        UIPasteboard.general.string = key
                    }
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
        community = s.markerCommunity ?? true
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
            "lookupOnOpen": AnyEncodable(st.lookupOnOpen ?? true),
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
            "markerCommunity": AnyEncodable(community),
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
