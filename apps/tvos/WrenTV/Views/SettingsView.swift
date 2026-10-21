import SwiftUI

/// The account, the server, and playback languages.
struct SettingsView: View {
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
        HStack(alignment: .top, spacing: 100) {
            VStack(alignment: .leading, spacing: 24) {
                Image("Lockup").resizable().scaledToFit().frame(height: 64)
                Text("Version \(Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "")")
                    .font(.caption).foregroundStyle(.secondary)
                Spacer()
            }
            .frame(width: 520, alignment: .leading)

            Form {
                Section("Account") {
                    LabeledContent("Signed in as", value: state.user?.displayName ?? "")
                    if let email = state.accountEmail { LabeledContent("Email", value: email) }
                    LabeledContent("Server", value: state.server?.name ?? "")
                    if let v = state.server?.version { LabeledContent("Server version", value: v) }
                    if state.account != nil {
                        Button("Switch Server") { state.switchServer() }
                    }
                    Button("Sign Out", role: .destructive) { state.signOut() }
                }
                Section {
                    NavigationLink("Layouts", value: LayoutsRoute())
                    Picker("Poster size", selection: $posterSize) {
                        ForEach(PosterSize.allCases) { Text($0.label).tag($0.rawValue) }
                    }
                } header: {
                    Text("Pages")
                } footer: {
                    Text("Choose which rows Home, Movies, TV Shows and each library show, and in what order. Layouts and poster size are saved to your account, so every app you sign in to uses them.")
                }
                Section {
                    Picker("Audio language", selection: $audioLanguage) {
                        Text("File's default track").tag("")
                        ForEach(options, id: \.0) { Text($0.1).tag($0.0) }
                    }
                    Picker("Subtitles", selection: $subtitleLanguage) {
                        Text("Off").tag("")
                        ForEach(options, id: \.0) { Text($0.1).tag($0.0) }
                    }
                } header: {
                    Text("Playback")
                } footer: {
                    Text(status ?? "Movies and episodes start with audio and subtitles in these languages when they have them. Saved to your account, so it applies on every device.")
                }
                .disabled(!loaded)
                Section {
                    Picker("Counts as watched at", selection: $watchedPercent) {
                        ForEach(UserPrefs.watchedPercents, id: \.self) { Text("\($0)%").tag($0) }
                    }
                    Picker("Add to Continue Watching after", selection: $continueAfter) {
                        ForEach(UserPrefs.continueAfters, id: \.0) { Text($0.1).tag($0.0) }
                        if !UserPrefs.continueAfters.contains(where: { $0.0 == continueAfter }) {
                            Text("\(continueAfter) seconds").tag(continueAfter)
                        }
                    }
                } header: {
                    Text("Watching")
                } footer: {
                    Text("How far in a movie or episode counts as watched, and how long you watch before it's on Continue Watching.")
                }
                .disabled(!loaded)
                SimklSection()
                if state.isAdmin {
                    Section {
                        ForEach(state.libraries) { lib in
                            Button { scan(lib) } label: {
                                LabeledContent(lib.name, value: lib.itemCount.map { "\($0) titles · Scan" } ?? "Scan")
                            }
                        }
                    } header: {
                        Text("Libraries")
                    } footer: {
                        Text("Server settings, users and libraries are managed in the Wren TV app on a computer or in a web browser.")
                    }
                }
            }
        }
        .padding(.horizontal, TV.gutter)
        .padding(.vertical, 40)
        .task(id: state.user?.id) {
            guard let api = state.api else { return }
            if let p = try? await api.prefs() {
                audioLanguage = p.audioLanguage
                subtitleLanguage = p.subtitleLanguage ?? ""
                watchedPercent = p.watchedPercent ?? 90
                continueAfter = p.continueAfter ?? 30
                if let size = p.posterSize, PosterSize(rawValue: size) != nil { posterSize = size }
            }
            // Let the pickers settle before changes count as the viewer's.
            try? await Task.sleep(for: .milliseconds(200))
            loaded = true
        }
        .onChange(of: audioLanguage) { save() }
        .onChange(of: subtitleLanguage) { save() }
        .onChange(of: watchedPercent) { save() }
        .onChange(of: continueAfter) { save() }
        .onChange(of: posterSize) { save() }
    }

    private func save() {
        guard loaded, let api = state.api else { return }
        let prefs = UserPrefs(audioLanguage: audioLanguage, subtitleLanguage: subtitleLanguage,
                              watchedPercent: watchedPercent, continueAfter: continueAfter, posterSize: posterSize)
        Task {
            do {
                _ = try await api.setPrefs(prefs)
                status = "Saved."
            } catch {
                status = "Couldn't save: \(error.localizedDescription)"
            }
        }
    }

    private func scan(_ lib: Library) {
        Task {
            do {
                try await state.api?.scanLibrary(lib.id)
                status = "Scanning \(lib.name)…"
            } catch {
                status = error.localizedDescription
            }
        }
    }
}

/// Linking Simkl: what's watched here is sent there, and its history
/// imported. The code is entered on a phone. Hidden when the server has no
/// Simkl app.
struct SimklSection: View {
    @Environment(AppState.self) private var state
    @State private var st: SimklStatus?
    @State private var error: String?
    @State private var tick = 0

    var body: some View {
        Group {
            if let s = st, s.available || s.linked {
                Section {
                    if s.linked, let a = s.account {
                        LabeledContent("Linked to", value: a.username ?? "Simkl")
                        Button { act { try await $0.simklScrobble(!a.scrobble) } } label: {
                            LabeledContent("Send what I watch to Simkl", value: a.scrobble ? "On" : "Off")
                        }
                        Button("Import history again") { act { try await $0.simklImport() } }
                            .disabled(s.importing == true || a.importStatus == "running")
                        Button("Disconnect", role: .destructive) { act { try await $0.simklUnlink() } }
                    } else if let p = s.pending, (p.error ?? "").isEmpty {
                        HStack(spacing: 40) {
                            QRCode(text: p.urlComplete ?? p.url).frame(width: 200, height: 200)
                            VStack(alignment: .leading, spacing: 8) {
                                Text("On your phone, open \(p.url.replacingOccurrences(of: "https://", with: "")) and enter")
                                    .foregroundStyle(.secondary)
                                Text(p.userCode).font(.largeTitle.monospaced().bold())
                            }
                        }
                        .padding(.vertical, 12)
                        Button("Cancel") { act { try await $0.simklCancelLink() } }
                    } else {
                        Button("Connect Simkl") { act { try await $0.simklLink() } }
                    }
                } header: {
                    Text("Simkl")
                } footer: {
                    Text(error ?? footer(s))
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
            return "Waiting for you to approve it on Simkl…"
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
