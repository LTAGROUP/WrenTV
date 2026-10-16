import SwiftUI

/// The signed-in user's own settings. They're stored on the server, so they
/// follow the user to every device and the web app.
struct PreferencesView: View {
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
        }
        .navigationTitle("Playback Settings")
        .task(id: state.user?.id) {
            guard let api = state.api else { return }
            if let p = try? await api.prefs() {
                audioLanguage = p.audioLanguage
                subtitleLanguage = p.subtitleLanguage ?? ""
            }
            loaded = true
        }
        .onChange(of: audioLanguage) { old, new in
            if old != new { save() }
        }
        .onChange(of: subtitleLanguage) { old, new in
            if old != new { save() }
        }
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
}
