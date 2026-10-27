import AVKit
import SwiftUI

struct PlayerWindow: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let target: PlayTarget

    @State private var model: PlayerModel?
    @State private var controlsVisible = true
    @State private var hideTask: Task<Void, Never>?

    var body: some View {
        ZStack {
            Color.black
            if let model {
                PlayerContent(model: model, controlsVisible: controlsVisible, close: { dismiss() })
            }
        }
        .ignoresSafeArea()
        .navigationTitle(model.map { [$0.title, $0.subtitle].compactMap { $0 }.joined(separator: " — ") } ?? target.title)
        .onAppear {
            guard model == nil else { return }
            // Downloads play without a connection; progress still syncs when there is one.
            guard let api = state.api ?? (target.localFile != nil ? WrenTVAPI(baseURL: state.serverURL ?? URL(string: "http://localhost:32500")!) : nil) else { return }
            let m = PlayerModel(target: target, api: api)
            let downloads = state.downloads
            m.onLocalProgress = { id, pos, dur in downloads.savePosition(id, position: pos, duration: dur) }
            model = m
            m.start()
            poke()
        }
        .onDisappear {
            model?.stop()
            state.refreshTick += 1
        }
        .onContinuousHover { _ in poke() }
        .focusable()
        .focusEffectDisabled()
        .onKeyPress(.space) { model?.togglePause(); poke(); return .handled }
        .onKeyPress(.leftArrow) { model?.seek(by: -10); poke(); return .handled }
        .onKeyPress(.rightArrow) { model?.seek(by: 10); poke(); return .handled }
        .onKeyPress(.upArrow) { model.map { $0.setVolume($0.volume + 0.1) }; poke(); return .handled }
        .onKeyPress(.downArrow) { model.map { $0.setVolume($0.volume - 0.1) }; poke(); return .handled }
        .onKeyPress("m") { model?.toggleMute(); poke(); return .handled }
        .onKeyPress("e") {
            guard let m = model, m.episode != nil else { return .ignored }
            m.shelfOpen.toggle(); poke(); return .handled
        }
        .onKeyPress("i") { model?.statsOpen.toggle(); return .handled }
        .onKeyPress("f") { NSApp.keyWindow?.toggleFullScreen(nil); return .handled }
        .onKeyPress("s") {
            guard let m = model, let k = m.skip else { return .ignored }
            k == .intro ? m.skipIntro() : m.skipCredits()
            return .handled
        }
        .onChange(of: model?.closeRequested) { _, v in if v == true { dismiss() } }
        .onChange(of: model?.menuOpen) { _, open in if open == false { poke() } }
        .onChange(of: model?.shelfOpen) { _, open in if open == false { poke() } }
        .onChange(of: model?.searchOpen) { _, open in if open == false { poke() } }
        .onKeyPress(.escape) {
            if model?.shelfOpen == true { model?.shelfOpen = false; return .handled }
            if NSApp.keyWindow?.styleMask.contains(.fullScreen) == true { NSApp.keyWindow?.toggleFullScreen(nil); return .handled }
            return .ignored
        }
    }

    private func poke() {
        controlsVisible = true
        hideTask?.cancel()
        hideTask = Task {
            try? await Task.sleep(for: .seconds(3))
            guard !Task.isCancelled else { return }
            // Leave the controls up while a menu is open; closing it pokes again.
            if model?.menuOpen == true || model?.shelfOpen == true || model?.searchOpen == true { return }
            withAnimation(.easeOut(duration: 0.3)) { controlsVisible = false }
            // The pointer goes with the controls; moving it brings both back.
            if NSApp.isActive { NSCursor.setHiddenUntilMouseMoves(true) }
        }
    }
}

private struct PlayerContent: View {
    @Bindable var model: PlayerModel
    let controlsVisible: Bool
    let close: () -> Void

    var body: some View {
        switch model.phase {
        case .loading:
            WaitingView(title: model.title, message: "Preparing…", close: close)
        case .pending(let msg):
            WaitingView(title: model.title, message: msg, close: close)
        case .error(let msg):
            VStack(spacing: 14) {
                Image(systemName: "exclamationmark.triangle").font(.system(size: 40)).foregroundStyle(Color.wrenEmber)
                Text("Can't play this right now").font(.title2.bold())
                Text(msg).foregroundStyle(.secondary).multilineTextAlignment(.center).frame(maxWidth: 480)
                HStack {
                    Button("Try Again") { model.retry() }
                    Button("Close", action: close)
                }
            }
        case .playing, .ended:
            ZStack {
                video
                // Both players share these controls, so clicks go to us, not the video view.
                Color.clear
                    .contentShape(Rectangle())
                    .onTapGesture(count: 2) { NSApp.keyWindow?.toggleFullScreen(nil) }
                    .onTapGesture { model.togglePause() }
                if model.buffering {
                    ProgressView().controlSize(.large)
                }
                if let cue = model.sideCue {
                    SideCueView(text: cue, raised: controlsVisible)
                }
                VStack {
                    if controlsVisible { topBar.transition(.opacity) }
                    Spacer()
                    if controlsVisible {
                        VStack(spacing: 0) {
                            PlayerControls(model: model)
                            if model.shelfOpen {
                                EpisodeShelf(model: model)
                                    .transition(.move(edge: .bottom).combined(with: .opacity))
                            }
                        }
                        .transition(.opacity)
                    }
                }
                .animation(.easeOut(duration: 0.25), value: model.shelfOpen)
                if model.statsOpen {
                    StatsPanel(model: model)
                }
                if model.phase == .ended, let next = model.upNext {
                    UpNextCard(next: next, play: model.playNext)
                } else if model.phase == .playing, !model.shelfOpen {
                    SkipOverlay(model: model, raised: controlsVisible)
                }
            }
            .sheet(isPresented: $model.searchOpen) { OnlineSubtitlesSheet(model: model) }
        }
    }

    @ViewBuilder
    private var video: some View {
        if model.engine == "mpv", let c = model.mpv {
            MPVVideoView(controller: c) { model.attach($0) }
        } else if let p = model.avPlayer {
            AVPlayerViewRep(player: p)
        }
    }

    private var topBar: some View {
        HStack(alignment: .top) {
            VStack(alignment: .leading, spacing: 2) {
                Text(model.title).font(.title3.bold())
                if let s = model.subtitle { Text(s).foregroundStyle(.secondary) }
                if !model.methodLabel.isEmpty {
                    Text(model.methodLabel).font(.caption).foregroundStyle(.secondary)
                }
            }
            Spacer()
        }
        .padding(.horizontal, 24)
        .padding(.top, 34)
        .padding(.bottom, 30)
        .background(LinearGradient(colors: [.black.opacity(0.7), .clear], startPoint: .top, endPoint: .bottom))
    }
}

private struct WaitingView: View {
    let title: String
    let message: String
    let close: () -> Void

    var body: some View {
        VStack(spacing: 16) {
            ProgressView().controlSize(.large)
            Text(title).font(.title2.bold())
            Text(message).foregroundStyle(.secondary)
            Button("Cancel", action: close).padding(.top, 8)
        }
    }
}

private struct ChapterMenu: View {
    @Bindable var model: PlayerModel

    var body: some View {
        PlayerMenuButton(model: model, symbol: "list.bullet.rectangle", help: "Chapters") {
            PlayerMenuSection(title: "Chapters") {
                ForEach(Array(model.chapters.enumerated()), id: \.offset) { i, c in
                    PlayerMenuRow(title: c.title.isEmpty ? "Chapter \(i + 1)" : c.title,
                                  detail: formatClock(c.start),
                                  selected: model.currentChapter == c) { model.seek(toChapter: c) }
                }
            }
        }
    }
}

private struct VersionMenu: View {
    @Bindable var model: PlayerModel

    var body: some View {
        PlayerMenuButton(model: model, symbol: "rectangle.stack", help: "Version") {
            PlayerMenuSection(title: "Version") {
                ForEach(model.versions) { v in
                    PlayerMenuRow(title: v.label, selected: model.fileId == v.id) { model.selectVersion(v.id) }
                }
            }
        }
    }
}

/// An icon in the player's top bar that opens a dark panel of choices.
private struct PlayerMenuButton<Panel: View>: View {
    @Bindable var model: PlayerModel
    let symbol: String
    let help: String
    @ViewBuilder var panel: () -> Panel
    @State private var open = false
    @State private var hover = false

    var body: some View {
        Button { open.toggle() } label: {
            Image(systemName: symbol)
                .font(.system(size: 15, weight: .medium))
                .frame(width: 30, height: 26)
                .background(open || hover ? Color.white.opacity(0.14) : .clear, in: RoundedRectangle(cornerRadius: 8))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hover = $0 }
        .help(help)
        .popover(isPresented: $open, arrowEdge: .bottom) {
            ScrollView {
                VStack(alignment: .leading, spacing: 10) { panel() }
                    .padding(8)
                    // Picking something closes the panel.
                    .environment(\.playerMenuDismiss, { open = false })
            }
            .frame(width: 320)
            .frame(maxHeight: 460)
            .fixedSize(horizontal: false, vertical: true)
            .preferredColorScheme(.dark)
        }
        .onChange(of: open) { _, v in model.menuOpen = v }
    }
}

private struct PlayerMenuDismissKey: EnvironmentKey {
    static let defaultValue: () -> Void = {}
}

private extension EnvironmentValues {
    var playerMenuDismiss: () -> Void {
        get { self[PlayerMenuDismissKey.self] }
        set { self[PlayerMenuDismissKey.self] = newValue }
    }
}

private struct PlayerMenuSection<Rows: View>: View {
    let title: String
    @ViewBuilder var rows: () -> Rows

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(title.uppercased())
                .font(.system(size: 10.5, weight: .bold))
                .kerning(0.6)
                .foregroundStyle(.secondary)
                .padding(.horizontal, 10)
                .padding(.bottom, 4)
            rows()
        }
    }
}

private struct PlayerMenuRow: View {
    @Environment(\.playerMenuDismiss) private var dismiss
    let title: String
    var detail: String?
    var selected = false
    var disabled = false
    let action: () -> Void
    @State private var hover = false

    var body: some View {
        Button {
            action()
            dismiss()
        } label: {
            HStack(spacing: 8) {
                Image(systemName: "checkmark")
                    .font(.system(size: 11, weight: .bold))
                    .foregroundStyle(Color.wrenEmber)
                    .opacity(selected ? 1 : 0)
                    .frame(width: 14)
                Text(title)
                    .fontWeight(selected ? .semibold : .regular)
                    .lineLimit(2)
                    .frame(maxWidth: .infinity, alignment: .leading)
                if let detail {
                    Text(detail).font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                }
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 6)
            .background(hover && !disabled ? Color.white.opacity(0.1) : .clear, in: RoundedRectangle(cornerRadius: 6))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(disabled)
        .opacity(disabled ? 0.45 : 1)
        .onHover { hover = $0 }
    }
}

/// Skip Intro / Skip Credits, and the next-episode countdown during credits.
private struct SkipOverlay: View {
    @Environment(AppState.self) private var state
    @Bindable var model: PlayerModel
    let raised: Bool

    var body: some View {
        VStack {
            Spacer()
            HStack {
                Spacer()
                content
            }
        }
        .padding(.trailing, 36)
        .padding(.bottom, raised ? 150 : 96)
        .animation(.easeOut(duration: 0.25), value: model.skip)
        .animation(.easeOut(duration: 0.25), value: model.countdown == nil)
    }

    @ViewBuilder
    private var content: some View {
        if model.skip == .credits, let n = model.countdown, let next = model.nextEpisode {
            HStack(spacing: 14) {
                RemoteImage(url: state.api?.imageURL(next.stillPath, size: "w300"))
                    .frame(width: 150, height: 84)
                    .clipShape(RoundedRectangle(cornerRadius: 6))
                VStack(alignment: .leading, spacing: 6) {
                    Text("Next episode in \(n)").font(.caption.bold()).foregroundStyle(Color.wrenEmber)
                    Text("\(next.episodeLabel) — \(next.title)").font(.headline).lineLimit(2)
                    ProgressView(value: Double(10 - n), total: 10).tint(Color.wrenEmber).frame(width: 200)
                    HStack {
                        Button("Next Episode") { model.skipCredits() }
                            .buttonStyle(.borderedProminent).tint(Color.wrenEmber)
                        Button("Watch Credits") { model.dismissCredits() }
                    }
                }
            }
            .padding(14)
            .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 12))
            .transition(.move(edge: .trailing).combined(with: .opacity))
        } else if let k = model.skip {
            Button {
                k == .intro ? model.skipIntro() : model.skipCredits()
            } label: {
                Text(k == .intro ? "Skip Intro" : model.nextEpisode != nil ? "Next Episode" : "Skip Credits")
                    .font(.headline)
                    .padding(.horizontal, 18).padding(.vertical, 10)
            }
            .buttonStyle(SkipButtonStyle())
            .help("Skip (S)")
            .transition(.opacity)
        }
    }
}

private struct SkipButtonStyle: ButtonStyle {
    @State private var hover = false

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .foregroundStyle(.white)
            .background(hover ? Color.wrenEmber : Color.black.opacity(0.6), in: RoundedRectangle(cornerRadius: 9))
            .overlay(RoundedRectangle(cornerRadius: 9).stroke(.white.opacity(hover ? 0 : 0.4)))
            .scaleEffect(configuration.isPressed ? 0.97 : 1)
            .onHover { hover = $0 }
            .animation(.easeOut(duration: 0.15), value: hover)
    }
}

private struct TrackMenu: View {
    @Bindable var model: PlayerModel

    var body: some View {
        PlayerMenuButton(model: model, symbol: "captions.bubble", help: "Audio & Subtitles") {
            if model.engine == "mpv" {
                let audio = model.mpvTracks.filter { $0.type == "audio" }
                let subs = model.mpvTracks.filter { $0.type == "sub" }
                PlayerMenuSection(title: "Audio") {
                    ForEach(audio) { t in
                        PlayerMenuRow(title: t.title, selected: t.selected) { model.selectMPVTrack(t) }
                    }
                }
                Divider().padding(.horizontal, 6)
                PlayerMenuSection(title: "Subtitles") {
                    PlayerMenuRow(title: "Off", selected: !subs.contains(where: \.selected)) { model.selectMPVTrack(nil) }
                    ForEach(subs) { t in
                        PlayerMenuRow(title: t.title, selected: t.selected) { model.selectMPVTrack(t) }
                    }
                }
            } else {
                if model.audioStreams.count > 1 {
                    PlayerMenuSection(title: "Audio") {
                        ForEach(model.audioStreams, id: \.typeIndex) { a in
                            PlayerMenuRow(title: a.label, selected: model.audioIndex == a.typeIndex) { model.selectAudio(a.typeIndex) }
                        }
                    }
                    Divider().padding(.horizontal, 6)
                }
                PlayerMenuSection(title: "Subtitles") {
                    PlayerMenuRow(title: "Off", selected: model.avSubtitle == nil && model.sideSubtitle == nil) { model.selectAVSubtitle(nil) }
                    ForEach(model.avSubtitles, id: \.self) { o in
                        PlayerMenuRow(title: o.displayName, selected: model.sideSubtitle == nil && model.avSubtitle == o) { model.selectAVSubtitle(o) }
                    }
                }
            }
            if model.onlineSubtitles || !model.sideTracks.isEmpty {
                Divider().padding(.horizontal, 6)
                PlayerMenuSection(title: "Online") {
                    // mpv lists the ones it has loaded with its own tracks.
                    ForEach(model.sideTracks.filter { t in
                        model.engine != "mpv" || !model.mpvTracks.contains { $0.type == "sub" && $0.title.contains(t.title ?? t.language ?? "Online") }
                    }, id: \.index) { t in
                        PlayerMenuRow(title: t.title ?? t.language ?? "Subtitles", selected: model.sideSubtitle?.track.index == t.index) {
                            Task { try? await model.showSideSubtitle(t) }
                        }
                    }
                    if model.onlineSubtitles {
                        PlayerMenuRow(title: "Search Online…") { model.searchOpen = true }
                    }
                }
            }
        }
    }
}

/// The season's episodes in a row under the controls, with the show's other
/// seasons above it.
private struct EpisodeShelf: View {
    @Environment(AppState.self) private var state
    @Bindable var model: PlayerModel
    @State private var seasons: [Item] = []
    @State private var seasonId: String?
    @State private var episodes: [Item]?

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if seasons.count > 1 {
                HStack(spacing: 6) {
                    ForEach(seasons) { s in
                        Button { seasonId = s.id } label: {
                            Text(s.displayTitle).font(.caption.weight(.semibold))
                                .padding(.horizontal, 12).padding(.vertical, 5)
                                .background(s.id == seasonId ? Color.wrenEmber : Color.white.opacity(0.12), in: Capsule())
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
            ScrollViewReader { proxy in
                ScrollView(.horizontal, showsIndicators: false) {
                    LazyHStack(alignment: .top, spacing: 14) {
                        if let episodes {
                            ForEach(episodes) { e in EpisodeCard(model: model, episode: e).id(e.id) }
                        } else {
                            ProgressView().frame(height: 110)
                        }
                    }
                }
                .onChange(of: episodes) { _, eps in
                    if eps?.contains(where: { $0.id == model.itemId }) == true {
                        proxy.scrollTo(model.itemId, anchor: .leading)
                    }
                }
            }
        }
        .padding(.horizontal, 40)
        .padding(.bottom, 24)
        .environment(\.colorScheme, .dark)
        .task {
            guard let ep = model.episode else { return }
            if seasonId == nil { seasonId = ep.parentId }
            if let show = ep.showId { seasons = await model.children(show).sorted { $0.seasonNumber < $1.seasonNumber } }
        }
        .task(id: seasonId) {
            guard let seasonId else { return }
            episodes = nil
            episodes = await model.children(seasonId).sorted { $0.episodeNumber < $1.episodeNumber }
        }
    }
}

private struct EpisodeCard: View {
    @Environment(AppState.self) private var state
    @Bindable var model: PlayerModel
    let episode: Item
    @State private var hover = false

    var body: some View {
        let e = episode
        let playing = e.id == model.itemId
        Button {
            model.shelfOpen = false
            model.playEpisode(e)
        } label: {
            VStack(alignment: .leading, spacing: 5) {
                RemoteImage(url: state.api?.imageURL(e.stillPath ?? e.backdropPath, size: "w300"))
                    .frame(width: 200, height: 112)
                    .clipShape(RoundedRectangle(cornerRadius: 7))
                    .overlay(alignment: .bottom) {
                        if let p = e.progress, p.position > 0, !p.watched, !playing {
                            ProgressStrip(fraction: p.fraction).clipShape(RoundedRectangle(cornerRadius: 7))
                        }
                    }
                    .overlay(alignment: .topLeading) {
                        if playing {
                            Text("Playing").font(.caption2.bold()).padding(.horizontal, 6).padding(.vertical, 2)
                                .background(Color.wrenEmber, in: RoundedRectangle(cornerRadius: 4)).padding(6)
                        }
                    }
                    .overlay(alignment: .topTrailing) {
                        if e.isWatched, !playing {
                            Image(systemName: "checkmark.circle.fill").foregroundStyle(.white, Color.wrenEmber).padding(6)
                        }
                    }
                    .overlay {
                        RoundedRectangle(cornerRadius: 7)
                            .stroke(playing ? Color.wrenEmber : .white.opacity(hover ? 0.8 : 0), lineWidth: 2)
                    }
                Text("\(e.episodeNumber). \(e.title)").font(.caption).lineLimit(1)
                if let m = e.runtimeMin, m > 0 { Text("\(m) min").font(.caption2).foregroundStyle(.secondary) }
            }
            .frame(width: 200, alignment: .leading)
            .scaleEffect(hover ? 1.03 : 1)
            .animation(.easeOut(duration: 0.15), value: hover)
        }
        .buttonStyle(.plain)
        .disabled(playing)
        .onHover { hover = $0 }
    }
}

/// How the title is playing, refreshed every second.
private struct StatsPanel: View {
    @Bindable var model: PlayerModel
    @State private var rows: [(String, String)] = []

    var body: some View {
        VStack {
            HStack {
                VStack(alignment: .leading, spacing: 8) {
                    HStack {
                        Text("Stats").font(.headline)
                        Spacer()
                        Button { model.statsOpen = false } label: { Image(systemName: "xmark").font(.caption.bold()) }
                            .buttonStyle(.plain)
                            .help("Close (I)")
                    }
                    Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 3) {
                        ForEach(Array(rows.enumerated()), id: \.offset) { _, r in
                            GridRow {
                                Text(r.0).foregroundStyle(.secondary)
                                Text(r.1).textSelection(.enabled)
                            }
                        }
                    }
                    .font(.caption.monospacedDigit())
                }
                .padding(14)
                .frame(width: 380)
                .background(.black.opacity(0.75), in: RoundedRectangle(cornerRadius: 10))
                .environment(\.colorScheme, .dark)
                Spacer()
            }
            Spacer()
        }
        .padding(.horizontal, 24)
        .padding(.top, 110)
        .task {
            while !Task.isCancelled {
                rows = model.stats()
                try? await Task.sleep(for: .seconds(1))
            }
        }
    }
}

/// Searches the subtitle sites for this title and downloads the one picked.
private struct OnlineSubtitlesSheet: View {
    let model: PlayerModel
    @Environment(\.dismiss) private var dismiss
    @State private var results: [OnlineSubtitle]?
    @State private var error: String?
    @State private var adding: OnlineSubtitle?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Online Subtitles").font(.headline)
                Spacer()
                Button("Close") { dismiss() }.keyboardShortcut(.cancelAction)
            }
            Group {
                if let error {
                    ContentUnavailableView("Couldn't search", systemImage: "exclamationmark.triangle", description: Text(error))
                } else if let results {
                    if results.isEmpty {
                        ContentUnavailableView("Nothing found", systemImage: "captions.bubble")
                    } else {
                        List(results) { r in
                            Button { add(r) } label: {
                                HStack {
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(r.name.isEmpty ? r.provider : r.name).lineLimit(2)
                                        Text(r.detail).font(.caption).foregroundStyle(.secondary)
                                    }
                                    Spacer()
                                    if adding == r { ProgressView().controlSize(.small) }
                                }
                                .contentShape(Rectangle())
                            }
                            .buttonStyle(.plain)
                            .disabled(adding != nil)
                        }
                    }
                } else {
                    ProgressView("Searching…").frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            .frame(maxHeight: .infinity)
        }
        .padding(18)
        .frame(width: 520, height: 460)
        .task {
            do { results = try await model.searchOnlineSubtitles() } catch { self.error = error.localizedDescription }
        }
    }

    private func add(_ r: OnlineSubtitle) {
        adding = r
        Task {
            do {
                try await model.addOnlineSubtitle(r)
                dismiss()
            } catch {
                self.error = error.localizedDescription
            }
            adding = nil
        }
    }
}

/// A subtitle-site download's current line, drawn over the native player.
private struct SideCueView: View {
    let text: String
    let raised: Bool

    var body: some View {
        VStack {
            Spacer()
            Text(text)
                .font(.system(size: 26, weight: .semibold))
                .multilineTextAlignment(.center)
                .foregroundStyle(.white)
                .shadow(color: .black, radius: 2, y: 1)
                .padding(.horizontal, 12).padding(.vertical, 4)
                .background(.black.opacity(0.35), in: RoundedRectangle(cornerRadius: 4))
                .padding(.horizontal, 60)
                .padding(.bottom, raised ? 170 : 50)
        }
        .allowsHitTesting(false)
        .animation(.easeOut(duration: 0.2), value: raised)
    }
}

/// The transport bar, laid out like AVKit's floating controls, for both players.
private struct PlayerControls: View {
    @Bindable var model: PlayerModel
    @State private var scrub: Double?

    var body: some View {
        VStack(spacing: 10) {
            ZStack {
                HStack(spacing: 8) {
                    Button { model.toggleMute() } label: { Image(systemName: volumeSymbol).frame(width: 22) }
                        .help(model.muted ? "Unmute (M)" : "Mute (M)")
                    Slider(value: Binding(get: { Double(model.muted ? 0 : model.volume) }, set: { model.setVolume(Float($0)) }), in: 0...1)
                        .controlSize(.mini)
                        .frame(width: 84)
                    Spacer()
                    if model.versions.count > 1 { VersionMenu(model: model) }
                    if !model.chapters.isEmpty { ChapterMenu(model: model) }
                    if model.engine == "mpv" || model.audioStreams.count > 1 || !model.avSubtitles.isEmpty
                        || model.onlineSubtitles || !model.sideTracks.isEmpty { TrackMenu(model: model) }
                    if model.episode != nil {
                        Button { model.shelfOpen.toggle() } label: {
                            Image(systemName: "rectangle.grid.1x2").frame(width: 30, height: 26)
                                .foregroundStyle(model.shelfOpen ? Color.wrenEmber : .primary)
                        }
                        .help("Episodes (E)")
                    }
                    Button { model.statsOpen.toggle() } label: {
                        Image(systemName: "info.circle").frame(width: 30, height: 26)
                            .foregroundStyle(model.statsOpen ? Color.wrenEmber : .primary)
                    }
                    .help("Stats (I)")
                    Button { NSApp.keyWindow?.toggleFullScreen(nil) } label: {
                        Image(systemName: "arrow.up.left.and.arrow.down.right").frame(width: 30, height: 26)
                    }
                    .help("Full Screen (F)")
                }
                HStack(spacing: 26) {
                    Button { model.seek(by: -10) } label: { Image(systemName: "gobackward.10") }.help("Back 10 seconds")
                    Button { model.togglePause() } label: {
                        Image(systemName: model.paused ? "play.fill" : "pause.fill")
                            .font(.system(size: 24))
                            .frame(width: 30)
                    }
                    .help(model.paused ? "Play (Space)" : "Pause (Space)")
                    Button { model.seek(by: 10) } label: { Image(systemName: "goforward.10") }.help("Forward 10 seconds")
                }
                .font(.system(size: 18, weight: .medium))
            }
            HStack(spacing: 10) {
                Text(formatClock(scrub ?? model.position)).monospacedDigit()
                Slider(value: Binding(get: { scrub ?? model.position }, set: { scrub = $0 }),
                       in: 0...max(model.duration, 1)) { editing in
                    if !editing, let s = scrub {
                        model.seek(to: s)
                        scrub = nil
                    }
                }
                .controlSize(.mini)
                .overlay { ChapterTicks(model: model).allowsHitTesting(false) }
                Text("-" + formatClock(max(0, model.duration - (scrub ?? model.position)))).monospacedDigit()
            }
            .font(.system(size: 11))
            .foregroundStyle(.secondary)
            if let c = model.currentChapter, !c.title.isEmpty {
                Text(c.title).font(.caption).foregroundStyle(.secondary).lineLimit(1).padding(.top, -6)
            }
        }
        .buttonStyle(.plain)
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        .frame(maxWidth: 640)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 16, style: .continuous).stroke(.white.opacity(0.08)))
        .environment(\.colorScheme, .dark)
        .padding(.horizontal, 40)
        .padding(.bottom, 24)
    }

    private var volumeSymbol: String {
        if model.muted || model.volume == 0 { return "speaker.slash.fill" }
        return model.volume < 0.34 ? "speaker.wave.1.fill" : model.volume < 0.67 ? "speaker.wave.2.fill" : "speaker.wave.3.fill"
    }
}

/// Chapter starts, and intro/credits ranges, drawn over the timeline.
private struct ChapterTicks: View {
    let model: PlayerModel

    var body: some View {
        GeometryReader { g in
            let d = max(model.duration, 1)
            // The slider track is inset by about the knob radius.
            let inset: CGFloat = 8
            let w = g.size.width - inset * 2
            let x = { (t: Double) in inset + w * CGFloat(min(max(t / d, 0), 1)) }
            ZStack(alignment: .leading) {
                ForEach([model.markers.intro, model.markers.credits].compactMap { $0 }, id: \.start) { r in
                    Capsule().fill(Color.wrenEmber.opacity(0.45))
                        .frame(width: max(2, x(r.end) - x(r.start)), height: 4)
                        .offset(x: x(r.start))
                }
                ForEach(model.chapters.filter { $0.start > 0.5 }, id: \.start) { c in
                    Rectangle().fill(.black.opacity(0.8)).frame(width: 2, height: 6).offset(x: x(c.start) - 1)
                }
            }
            .frame(height: g.size.height)
        }
    }
}

private struct UpNextCard: View {
    @Environment(AppState.self) private var state
    let next: Item
    let play: () -> Void

    var body: some View {
        VStack {
            Spacer()
            HStack {
                Spacer()
                HStack(spacing: 14) {
                    RemoteImage(url: state.api?.imageURL(next.stillPath, size: "w300"))
                        .frame(width: 160, height: 90)
                        .clipShape(RoundedRectangle(cornerRadius: 6))
                    VStack(alignment: .leading, spacing: 4) {
                        Text("Up Next").font(.caption.bold()).foregroundStyle(Color.wrenEmber)
                        Text("\(next.episodeLabel) — \(next.title)").font(.headline).lineLimit(2)
                        Button(action: play) { Label("Play", systemImage: "play.fill") }
                            .buttonStyle(.borderedProminent)
                            .tint(Color.wrenEmber)
                    }
                }
                .padding(14)
                .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 12))
                .padding(40)
            }
        }
    }
}

struct AVPlayerViewRep: NSViewRepresentable {
    let player: AVPlayer

    func makeNSView(context: Context) -> AVPlayerView {
        let v = AVPlayerView()
        v.player = player
        // PlayerControls draws the controls, the same for both players.
        v.controlsStyle = .none
        v.videoGravity = .resizeAspect
        return v
    }

    func updateNSView(_ v: AVPlayerView, context: Context) {
        if v.player !== player { v.player = player }
    }
}
