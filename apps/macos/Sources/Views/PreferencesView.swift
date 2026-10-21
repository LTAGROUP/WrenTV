import SwiftUI

/// The signed-in user's own settings. They're stored on the server, so they
/// follow the user to every device and the web app.
struct PreferencesView: View {
    @Environment(AppState.self) private var state
    @State private var audioLanguage = "en"
    @State private var subtitleLanguage = ""
    @State private var watchedPercent = 90
    @State private var continueAfter = 30
    @State private var loaded = false
    @State private var status: String?
    @AppStorage(PosterSize.key) private var posterSize = PosterSize.medium.rawValue

    /// ISO 639-1 codes the server understands, shown in the user's language.
    private static let languages = ["en", "es", "fr", "de", "it", "pt", "ja", "ko", "zh", "hi", "ru", "ar", "nl", "sv", "no", "da", "fi",
                                    "pl", "tr", "el", "he", "th", "vi", "id", "cs", "hu", "ro", "uk", "ta", "te", "tl", "fa"]

    private var options: [(String, String)] {
        Self.languages
            .map { ($0, Locale.current.localizedString(forLanguageCode: $0) ?? $0) }
            .sorted { $0.1.localizedCaseInsensitiveCompare($1.1) == .orderedAscending }
    }

    var body: some View {
        Form {
            Section {
                Picker("Preferred audio language", selection: $audioLanguage) {
                    Text("File's default track").tag("")
                    Divider()
                    ForEach(options, id: \.0) { Text($0.1).tag($0.0) }
                }
                .disabled(!loaded || state.api == nil)
                Picker("Subtitles", selection: $subtitleLanguage) {
                    Text("Off").tag("")
                    Divider()
                    ForEach(options, id: \.0) { Text($0.1).tag($0.0) }
                }
                .disabled(!loaded || state.api == nil)
            } header: {
                Text("Playback")
            } footer: {
                Text(status ?? "Movies and episodes start with audio and subtitles in these languages when they have them. Saved to your account, so it applies on every device.")
                    .foregroundStyle(.secondary)
            }
            Section {
                Picker("Counts as watched at", selection: $watchedPercent) {
                    ForEach(UserPrefs.watchedPercents, id: \.self) { Text("\($0)%").tag($0) }
                }
                .disabled(!loaded || state.api == nil)
                Picker("Add to Continue Watching after", selection: $continueAfter) {
                    ForEach(UserPrefs.continueAfters, id: \.0) { Text($0.1).tag($0.0) }
                    if !UserPrefs.continueAfters.contains(where: { $0.0 == continueAfter }) {
                        Text("\(continueAfter) seconds").tag(continueAfter)
                    }
                }
                .disabled(!loaded || state.api == nil)
            } header: {
                Text("Watching")
            } footer: {
                Text("How far in a movie or episode counts as watched, and how long you watch before it's on Continue Watching.")
                    .foregroundStyle(.secondary)
            }
            Section {
                Picker("Poster size", selection: $posterSize) {
                    ForEach(PosterSize.allCases) { Text($0.label).tag($0.rawValue) }
                }
            } header: {
                Text("Browsing")
            } footer: {
                Text("How big posters and tiles are on this Mac.").foregroundStyle(.secondary)
            }
            SimklSection()
            LinkTVSection()
        }
        .formStyle(.grouped)
        .frame(width: 460)
        .fixedSize(horizontal: false, vertical: true)
        .task(id: state.user?.id) {
            guard let api = state.api else { return }
            if let p = try? await api.prefs() {
                audioLanguage = p.audioLanguage
                subtitleLanguage = p.subtitleLanguage ?? ""
                watchedPercent = p.watchedPercent ?? 90
                continueAfter = p.continueAfter ?? 30
            }
            loaded = true
        }
        .onChange(of: audioLanguage) { old, new in
            if old != new { save() }
        }
        .onChange(of: subtitleLanguage) { old, new in
            if old != new { save() }
        }
        .onChange(of: watchedPercent) { old, new in
            if old != new { save() }
        }
        .onChange(of: continueAfter) { old, new in
            if old != new { save() }
        }
    }

    private func save() {
        guard loaded, let api = state.api else { return }
        let prefs = UserPrefs(audioLanguage: audioLanguage, subtitleLanguage: subtitleLanguage,
                              watchedPercent: watchedPercent, continueAfter: continueAfter)
        Task {
            do {
                _ = try await api.setPrefs(prefs)
                status = "Saved."
            } catch {
                status = "Couldn't save: \(error.localizedDescription)"
            }
        }
    }
}

/// Linking Simkl: what's watched here is sent there, and its history
/// imported. Hidden when the server has no Simkl app.
struct SimklSection: View {
    @Environment(AppState.self) private var state
    @Environment(\.openURL) private var openURL
    @State private var st: SimklStatus?
    @State private var error: String?
    @State private var tick = 0

    var body: some View {
        Group {
            if let s = st, s.available || s.linked {
                Section {
                    rows(s)
                } header: {
                    Text("Simkl")
                } footer: {
                    Text(error ?? footer(s)).foregroundStyle(error == nil ? Color.secondary : Color.red)
                }
            }
        }
        .task(id: tick) {
            st = try? await state.api?.simkl()
            // Waiting on approval or an import: check again shortly.
            if st?.busy == true {
                try? await Task.sleep(for: .seconds(3))
                if !Task.isCancelled { tick += 1 }
            }
        }
    }

    @ViewBuilder private func rows(_ s: SimklStatus) -> some View {
        if s.linked, let a = s.account {
            LabeledContent("Linked to", value: a.username ?? "Simkl")
            Toggle("Send what I watch to Simkl", isOn: Binding(get: { a.scrobble }, set: { on in act { try await $0.simklScrobble(on) } }))
            Button("Import history again") { act { try await $0.simklImport() } }
                .disabled(s.importing == true || a.importStatus == "running")
            Button("Disconnect", role: .destructive) { act { try await $0.simklUnlink() } }
        } else if let p = s.pending, (p.error ?? "").isEmpty {
            LabeledContent("Code", value: p.userCode)
                .font(.title3.monospaced())
                .textSelection(.enabled)
            Button("Open \(p.url.replacingOccurrences(of: "https://", with: ""))") {
                if let u = URL(string: p.urlComplete ?? p.url) { openURL(u) }
            }
            Button("Cancel") { act { try await $0.simklCancelLink() } }
        } else {
            Button("Connect Simkl") {
                act { api in
                    try await api.simklLink()
                    if let p = try? await api.simkl().pending, let u = URL(string: p.urlComplete ?? p.url) { openURL(u) }
                }
            }
        }
    }

    private func footer(_ s: SimklStatus) -> String {
        if s.linked, let a = s.account {
            if s.importing == true || a.importStatus == "running" { return "Importing your history…" }
            if a.importStatus == "done" {
                return "Last import: \(a.importMovies ?? 0) movies and \(a.importEpisodes ?? 0) episodes marked watched."
            }
            if let e = a.importStatus, !e.isEmpty { return "Import failed: \(e)" }
            return "What you watch here is marked watched on Simkl."
        }
        if let p = s.pending {
            if let e = p.error, !e.isEmpty { return e }
            return "Open the link and enter the code. Waiting for you to approve it on Simkl…"
        }
        return "Link your Simkl account to import what you've already watched and keep Simkl up to date as you watch here."
    }

    private func act(_ f: @escaping (WrenTVAPI) async throws -> Void) {
        guard let api = state.api else { return }
        Task {
            do {
                try await f(api)
                error = nil
            } catch {
                self.error = error.localizedDescription
            }
            tick += 1
        }
    }
}

/// Signs in a TV showing a code: to this account, or to this server as you.
/// A TV that doesn't know a server yet shows an account code; one connected
/// by address to a server with its own usernames shows that server's.
struct LinkTVSection: View {
    @Environment(AppState.self) private var state
    @State private var code = ""
    @State private var pending: Pending?
    @State private var linked: String?
    @State private var error: String?
    @State private var busy = false

    var body: some View {
        Section {
            if let linked {
                Label("\(linked) is signed in as \(state.user?.displayName ?? "you").", systemImage: "checkmark.circle.fill")
                    .foregroundStyle(.green)
                Button("Sign In Another TV") { self.linked = nil; code = "" }
            } else {
                HStack {
                    TextField("Code", text: $code, prompt: Text("ABCD-EFGH"))
                        .font(.body.monospaced())
                        .onSubmit { lookUp() }
                    Button("Continue") { lookUp() }
                        .disabled(Self.normalize(code).count != 8 || busy || state.api == nil)
                }
            }
        } header: {
            Text("Sign in a TV")
        } footer: {
            Text(error ?? "On the TV, choose Sign in with your phone, then type the code it shows.")
                .foregroundStyle(error == nil ? Color.secondary : Color.red)
        }
        .alert("Sign in this TV?", isPresented: Binding(get: { pending != nil }, set: { if !$0 { pending = nil } }), presenting: pending) { p in
            Button("Sign In") { approve(p) }
            Button("Cancel", role: .cancel) {}
        } message: { p in
            if p.account {
                Text("\(p.device) will be signed in to your Wren TV account (\(state.accountEmail ?? "this account")) and open your servers.")
            } else {
                Text("\(p.device) will be signed in to \(state.server?.name ?? "this server") as \(state.user?.displayName ?? "you").")
            }
        }
    }

    /// Codes are 8 characters; the TV shows them as ABCD-EFGH. A pasted
    /// link's code is used, never its address.
    static func normalize(_ s: String) -> String {
        var s = s
        if let c = URLComponents(string: s), let v = c.queryItems?.first(where: { $0.name == "code" })?.value { s = v }
        let alpha = Set("ABCDEFGHJKLMNPQRSTUVWXYZ23456789")
        return String(s.uppercased().filter { alpha.contains($0) })
    }

    struct Pending {
        var code: String
        var device: String
        /// An account sign-in code (the TV doesn't know a server yet), not this server's own.
        var account: Bool
    }

    /// The account's access token, when signed in with a Wren TV account.
    private func accountToken() async -> String? {
        guard let auth = state.api?.auth, await auth.accountSession != nil else { return nil }
        return try? await auth.token()
    }

    private func lookUp() {
        let c = Self.normalize(code)
        guard c.count == 8, let api = state.api else { return }
        error = nil
        busy = true
        Task {
            defer { busy = false }
            var accountError: Error?
            if let token = await accountToken() {
                do {
                    let d = try await AccountClient(.wrenTV).linkDevice(code: c, check: true, accessToken: token)
                    pending = Pending(code: c, device: d.deviceName.isEmpty ? "TV" : d.deviceName, account: true)
                    return
                } catch {
                    accountError = error
                }
            }
            // Not an account code: one from this server's own sign-in.
            do {
                let info = try await api.pairInfo(c)
                if info.approved {
                    error = "This code was already used. Get a new one on the TV."
                } else {
                    pending = Pending(code: c, device: info.device, account: false)
                }
            } catch {
                self.error = (accountError ?? error).localizedDescription
            }
        }
    }

    private func approve(_ p: Pending) {
        guard let api = state.api else { return }
        Task {
            do {
                if p.account {
                    guard let token = await accountToken() else { return }
                    _ = try await AccountClient(.wrenTV).linkDevice(code: p.code, check: false, accessToken: token)
                } else {
                    try await api.approvePair(p.code)
                }
                linked = p.device
                code = ""
            } catch {
                self.error = error.localizedDescription
            }
        }
    }
}
