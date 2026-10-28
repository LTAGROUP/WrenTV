import SwiftUI

/// The signed-in user's own settings. They're stored on the server, so they
/// follow the user to every device and the web app.
struct PreferencesView: View {
    @Environment(AppState.self) private var state
    @State private var audioLanguage = "en"
    @State private var subtitleLanguage = ""
    @State private var forcedSubtitles = true
    @State private var foreignAudioSubtitles = true
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
                Toggle("Forced subtitles", isOn: $forcedSubtitles)
                    .disabled(!loaded || state.api == nil)
                Toggle("Subtitles with foreign audio", isOn: $foreignAudioSubtitles)
                    .disabled(!loaded || state.api == nil)
            } header: {
                Text("Playback")
            } footer: {
                Text(status ?? "Movies and episodes start with audio and subtitles in these languages when they have them. Forced subtitles cover only foreign-language lines and signs, and show even with subtitles off. With foreign audio, subtitles in your audio language turn on. Saved to your account, so it applies on every device.")
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
                Text("How big posters and tiles are. Saved to your account, so every app you sign in to uses it.").foregroundStyle(.secondary)
            }
            ForEach(Tracker.allCases) { TrackerSection(tracker: $0) }
        }
        .navigationTitle("Settings")
        .task(id: state.user?.id) {
            guard let api = state.api else { return }
            if let p = try? await api.prefs() {
                audioLanguage = p.audioLanguage
                subtitleLanguage = p.subtitleLanguage ?? ""
                forcedSubtitles = p.forcedSubtitles ?? true
                foreignAudioSubtitles = p.foreignAudioSubtitles ?? true
                watchedPercent = p.watchedPercent ?? 90
                continueAfter = p.continueAfter ?? 30
                if let size = p.posterSize, PosterSize(rawValue: size) != nil { posterSize = size }
            }
            loaded = true
        }
        .onChange(of: audioLanguage) { old, new in
            if old != new { save() }
        }
        .onChange(of: subtitleLanguage) { old, new in
            if old != new { save() }
        }
        .onChange(of: forcedSubtitles) { old, new in
            if old != new { save() }
        }
        .onChange(of: foreignAudioSubtitles) { old, new in
            if old != new { save() }
        }
        .onChange(of: watchedPercent) { old, new in
            if old != new { save() }
        }
        .onChange(of: continueAfter) { old, new in
            if old != new { save() }
        }
        .onChange(of: posterSize) { old, new in
            if old != new { save() }
        }
    }

    private func save() {
        guard loaded, let api = state.api else { return }
        let prefs = UserPrefs(audioLanguage: audioLanguage, subtitleLanguage: subtitleLanguage,
                              forcedSubtitles: forcedSubtitles, foreignAudioSubtitles: foreignAudioSubtitles,
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
}

/// Linking Simkl or Trakt: what's watched here is sent there, and its history
/// imported. Hidden when the server has no app for the tracker.
struct TrackerSection: View {
    let tracker: Tracker
    @Environment(AppState.self) private var state
    @Environment(\.openURL) private var openURL
    @State private var st: TrackerStatus?
    @State private var error: String?
    @State private var tick = 0

    var body: some View {
        Group {
            if let s = st, s.available || s.linked {
                Section {
                    rows(s)
                } header: {
                    Text(tracker.name)
                } footer: {
                    Text(error ?? footer(s)).foregroundStyle(error == nil ? Color.secondary : Color.red)
                }
            }
        }
        .task(id: tick) {
            st = try? await state.api?.tracker(tracker)
            // Waiting on approval or an import: check again shortly.
            if st?.busy == true {
                try? await Task.sleep(for: .seconds(3))
                if !Task.isCancelled { tick += 1 }
            }
        }
    }

    @ViewBuilder private func rows(_ s: TrackerStatus) -> some View {
        if s.linked, let a = s.account {
            LabeledContent("Linked to", value: a.username ?? tracker.name)
            Toggle("Send what I watch to \(tracker.name)", isOn: Binding(get: { a.scrobble }, set: { on in act { try await $0.trackerScrobble(tracker, on) } }))
            Button("Import history again") { act { try await $0.trackerImport(tracker) } }
                .disabled(s.importing == true || a.importStatus == "running")
            Button("Disconnect", role: .destructive) { act { try await $0.trackerUnlink(tracker) } }
        } else if let p = s.pending, (p.error ?? "").isEmpty {
            LabeledContent("Code", value: p.userCode)
                .font(.title3.monospaced())
                .textSelection(.enabled)
            Button("Open \(p.url.replacingOccurrences(of: "https://", with: ""))") {
                if let u = URL(string: p.urlComplete ?? p.url) { openURL(u) }
            }
            Button("Cancel") { act { try await $0.trackerCancelLink(tracker) } }
        } else {
            Button("Connect \(tracker.name)") {
                act { api in
                    try await api.trackerLink(tracker)
                    if let p = try? await api.tracker(tracker).pending, let u = URL(string: p.urlComplete ?? p.url) { openURL(u) }
                }
            }
        }
    }

    private func footer(_ s: TrackerStatus) -> String {
        if s.linked, let a = s.account {
            if s.importing == true || a.importStatus == "running" { return "Importing your history…" }
            if a.importStatus == "done" {
                return "Last import: \(a.importMovies ?? 0) movies and \(a.importEpisodes ?? 0) episodes marked watched."
            }
            if let e = a.importStatus, !e.isEmpty { return "Import failed: \(e)" }
            return "What you watch here is marked watched on \(tracker.name)."
        }
        if let p = s.pending {
            if let e = p.error, !e.isEmpty { return e }
            return "Open the link and enter the code. Waiting for you to approve it on \(tracker.name)…"
        }
        return "Link your \(tracker.name) account to import what you've already watched and keep \(tracker.name) up to date as you watch here."
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
