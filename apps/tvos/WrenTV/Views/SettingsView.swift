import SwiftUI

/// The account, the server, and playback languages.
struct SettingsView: View {
    @Environment(AppState.self) private var state
    @State private var audioLanguage = "en"
    @State private var subtitleLanguage = ""
    @State private var loaded = false
    @State private var status: String?

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
            }
            // Let the pickers settle before changes count as the viewer's.
            try? await Task.sleep(for: .milliseconds(200))
            loaded = true
        }
        .onChange(of: audioLanguage) { save() }
        .onChange(of: subtitleLanguage) { save() }
    }

    private func save() {
        guard loaded, let api = state.api else { return }
        let prefs = UserPrefs(audioLanguage: audioLanguage, subtitleLanguage: subtitleLanguage)
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
