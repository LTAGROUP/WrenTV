import AVKit
import SwiftUI

/// The full-screen player, presented over the app.
struct PlayerView: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let target: PlayTarget

    @State private var model: PlayerModel?
    @State private var controlsVisible = true
    @State private var hideTask: Task<Void, Never>?
    /// A player menu (versions, audio, subtitles, chapters) is open. Hiding the
    /// controls would close it, so they stay up until it's dismissed.
    @State private var menuOpen = false
    /// The episode row, stats or subtitle search is open, which keeps the
    /// controls up the same way.
    @State private var panel: PlayerPanel?
    @State private var fill = false
    @State private var pip: AVPictureInPictureController?

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            if let model {
                PlayerContent(model: model, controlsVisible: controlsVisible, fill: $fill, pip: $pip,
                              menuOpen: $menuOpen, panel: $panel, close: { dismiss() }, poke: poke, toggleControls: toggleControls)
            }
        }
        .statusBarHidden(!controlsVisible)
        .persistentSystemOverlays(.hidden)
        .onAppear {
            guard model == nil else { return }
            // Downloads play without a connection; progress still syncs when there is one.
            guard let api = state.api ?? (target.localFile != nil ? WrenTVAPI(baseURL: state.serverURL ?? URL(string: "http://localhost:32500")!) : nil) else { return }
            let m = PlayerModel(target: target, api: api, online: state.api != nil)
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
        .onChange(of: model?.closeRequested) { _, v in if v == true { dismiss() } }
        .onChange(of: model?.paused) { _, p in if p == true { showControls() } else { poke() } }
        .onChange(of: menuOpen) { _, open in if open { showControls() } else { poke() } }
        .onChange(of: panel) { _, p in if p != nil { showControls() } else { poke() } }
    }

    private func showControls() {
        hideTask?.cancel()
        withAnimation(.easeOut(duration: 0.2)) { controlsVisible = true }
    }

    /// Shows the controls, then hides them after a few seconds of playback.
    private func poke() {
        showControls()
        hideTask = Task {
            try? await Task.sleep(for: .seconds(4))
            guard !Task.isCancelled, !menuOpen, panel == nil, model?.paused != true, model?.phase == .playing else { return }
            withAnimation(.easeOut(duration: 0.3)) { controlsVisible = false }
        }
    }

    private func toggleControls() {
        if controlsVisible {
            hideTask?.cancel()
            withAnimation(.easeOut(duration: 0.2)) { controlsVisible = false }
        } else {
            poke()
        }
    }
}

enum PlayerPanel: String, Identifiable {
    case episodes, stats, onlineSubtitles
    var id: String { rawValue }
}

private struct PlayerContent: View {
    @Bindable var model: PlayerModel
    let controlsVisible: Bool
    @Binding var fill: Bool
    @Binding var pip: AVPictureInPictureController?
    @Binding var menuOpen: Bool
    @Binding var panel: PlayerPanel?
    let close: () -> Void
    let poke: () -> Void
    let toggleControls: () -> Void

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
                        .buttonStyle(.borderedProminent)
                    Button("Close", action: close).buttonStyle(.bordered)
                }
            }
            .padding(24)
        case .playing, .ended:
            playing
                .sheet(isPresented: Binding(get: { panel == .onlineSubtitles }, set: { if !$0 { panel = nil } })) {
                    OnlineSubtitlesSheet(model: model)
                }
        }
    }

    @ViewBuilder
    private var playing: some View {
        switch model.phase {
        case .loading, .pending, .error:
            EmptyView()
        case .playing, .ended:
            ZStack {
                if let p = model.avPlayer {
                    VideoLayerView(player: p, fill: fill, pip: $pip)
                        .ignoresSafeArea()
                        .contentShape(Rectangle())
                        .onTapGesture(count: 2) { withAnimation { fill.toggle() } }
                        .onTapGesture(perform: toggleControls)
                }
                if model.buffering {
                    ProgressView().controlSize(.large).tint(.white)
                }
                if let cue = model.sideCue {
                    SideCueView(text: cue, raised: controlsVisible)
                }
                if controlsVisible {
                    Controls(model: model, fill: $fill, pip: pip, menuOpen: $menuOpen, panel: $panel, close: close, poke: poke)
                        .transition(.opacity)
                }
                if panel == .stats {
                    StatsOverlay(model: model) { panel = nil }
                }
                if model.phase == .ended, let next = model.upNext {
                    UpNextCard(next: next, play: model.playNext)
                } else if model.phase == .playing {
                    SkipOverlay(model: model, raised: controlsVisible)
                }
            }
        }
    }
}

private struct WaitingView: View {
    let title: String
    let message: String
    let close: () -> Void

    var body: some View {
        VStack(spacing: 16) {
            ProgressView().controlSize(.large)
            Text(title).font(.title2.bold()).multilineTextAlignment(.center)
            Text(message).foregroundStyle(.secondary).multilineTextAlignment(.center)
            Button("Cancel", action: close).buttonStyle(.bordered).padding(.top, 8)
        }
        .padding(24)
    }
}

/// Everything drawn over the video: the top bar, the transport buttons and
/// the timeline.
private struct Controls: View {
    @Bindable var model: PlayerModel
    @Binding var fill: Bool
    let pip: AVPictureInPictureController?
    @Binding var menuOpen: Bool
    @Binding var panel: PlayerPanel?
    let close: () -> Void
    let poke: () -> Void
    @State private var scrub: Double?

    var body: some View {
        ZStack {
            Color.black.opacity(0.35).ignoresSafeArea().allowsHitTesting(false)
            VStack(spacing: 0) {
                topBar
                Spacer()
                timeline
                if panel == .episodes {
                    EpisodeShelf(model: model) { panel = nil }
                        .transition(.move(edge: .bottom).combined(with: .opacity))
                }
            }
            if panel != .episodes { transport }
        }
        .foregroundStyle(.white)
        .animation(.easeOut(duration: 0.25), value: panel)
    }

    private func toggle(_ p: PlayerPanel) {
        panel = panel == p ? nil : p
    }

    private var topBar: some View {
        HStack(alignment: .top, spacing: 14) {
            Button(action: close) {
                Image(systemName: "xmark").font(.system(size: 17, weight: .semibold)).frame(width: 40, height: 40)
            }
            .background(.ultraThinMaterial, in: Circle())
            VStack(alignment: .leading, spacing: 2) {
                Text(model.title).font(.headline).lineLimit(1)
                if let s = model.subtitle { Text(s).font(.subheadline).foregroundStyle(.white.opacity(0.75)).lineLimit(1) }
                if !model.methodLabel.isEmpty {
                    Text(model.methodLabel).font(.caption2).foregroundStyle(.white.opacity(0.6)).lineLimit(1)
                }
            }
            Spacer(minLength: 0)
            HStack(spacing: 4) {
                if let pip, AVPictureInPictureController.isPictureInPictureSupported() {
                    Button { pip.startPictureInPicture() } label: { barIcon("pip.enter") }
                }
                AirPlayButton().frame(width: 40, height: 40)
                if model.episode != nil {
                    Button { toggle(.episodes) } label: { barIcon("rectangle.stack") }
                        .foregroundStyle(panel == .episodes ? Color.wrenEmber : .white)
                }
                if !model.chapters.isEmpty { chapterMenu }
                if model.versions.count > 1 || model.audioStreams.count > 1 || !model.legibleOptions.isEmpty
                    || model.onlineSubtitles || !model.sideTracks.isEmpty { trackMenu }
                Button { toggle(.stats) } label: { barIcon("info.circle") }
                    .foregroundStyle(panel == .stats ? Color.wrenEmber : .white)
            }
        }
        .padding(.horizontal, 16)
        .padding(.top, 8)
    }

    private func barIcon(_ symbol: String) -> some View {
        Image(systemName: symbol).font(.system(size: 18, weight: .medium)).frame(width: 40, height: 40).contentShape(Rectangle())
    }

    private var transport: some View {
        HStack(spacing: 56) {
            Button { model.seek(by: -10); poke() } label: { Image(systemName: "gobackward.10").font(.system(size: 30)) }
            Button { model.togglePause(); poke() } label: {
                Image(systemName: model.paused ? "play.fill" : "pause.fill").font(.system(size: 46)).frame(width: 60)
            }
            Button { model.seek(by: 10); poke() } label: { Image(systemName: "goforward.10").font(.system(size: 30)) }
        }
        .buttonStyle(.plain)
        .shadow(color: .black.opacity(0.4), radius: 8)
    }

    private var timeline: some View {
        VStack(spacing: 4) {
            Slider(value: Binding(get: { scrub ?? model.position }, set: { scrub = $0; poke() }),
                   in: 0...max(model.duration, 1)) { editing in
                if !editing, let s = scrub {
                    model.seek(to: s)
                    scrub = nil
                }
            }
            .tint(Color.wrenEmber)
            .overlay { ChapterTicks(model: model).allowsHitTesting(false) }
            HStack {
                Text(formatClock(scrub ?? model.position)).monospacedDigit()
                if let c = model.currentChapter, !c.title.isEmpty {
                    Text(c.title).foregroundStyle(.white.opacity(0.7)).lineLimit(1)
                }
                Spacer()
                Button { withAnimation { fill.toggle() } } label: {
                    Image(systemName: fill ? "arrow.down.right.and.arrow.up.left" : "arrow.up.left.and.arrow.down.right")
                }
                .padding(.horizontal, 10)
                Text("-" + formatClock(max(0, model.duration - (scrub ?? model.position)))).monospacedDigit()
            }
            .font(.caption)
        }
        .padding(.horizontal, 20)
        .padding(.bottom, 8)
    }

    private var chapterMenu: some View {
        Menu {
            Section("Chapters") {
                ForEach(Array(model.chapters.enumerated()), id: \.offset) { i, c in
                    Button { model.seek(toChapter: c) } label: {
                        CheckLabel(title: "\(c.title.isEmpty ? "Chapter \(i + 1)" : c.title)  \(formatClock(c.start))",
                                   on: model.currentChapter == c)
                    }
                }
            }
            .tracksMenuOpen($menuOpen)
        } label: { barIcon("list.bullet.rectangle") }
    }

    private var trackMenu: some View {
        Menu {
            Group {
                if model.versions.count > 1 {
                    Section("Version") {
                        ForEach(model.versions) { v in
                            Button { model.selectVersion(v.id) } label: {
                                CheckLabel(title: v.label, on: model.fileId == v.id)
                            }
                        }
                    }
                }
                if model.audioStreams.count > 1 {
                    Section("Audio") {
                        ForEach(model.audioStreams, id: \.typeIndex) { a in
                            Button { model.selectAudio(a.typeIndex) } label: {
                                CheckLabel(title: a.label, on: model.audioIndex == a.typeIndex)
                            }
                        }
                    }
                }
                if !model.legibleOptions.isEmpty || !model.sideTracks.isEmpty || model.onlineSubtitles {
                    Section("Subtitles") {
                        Button { model.selectSubtitle(nil) } label: {
                            CheckLabel(title: "Off", on: model.selectedLegible == nil && model.sideSubtitle == nil)
                        }
                        ForEach(model.legibleOptions, id: \.self) { o in
                            Button { model.selectSubtitle(o) } label: {
                                CheckLabel(title: o.displayName, on: model.sideSubtitle == nil && model.selectedLegible == o)
                            }
                        }
                        ForEach(model.sideTracks, id: \.index) { t in
                            Button { Task { try? await model.showSideSubtitle(t) } } label: {
                                CheckLabel(title: t.title ?? t.language ?? "Subtitles", on: model.sideSubtitle?.track.index == t.index)
                            }
                        }
                        if model.onlineSubtitles {
                            Button { panel = .onlineSubtitles } label: {
                                Label("Search Online…", systemImage: "magnifyingglass")
                            }
                        }
                    }
                }
            }
            .tracksMenuOpen($menuOpen)
        } label: { barIcon("captions.bubble") }
    }
}

private extension View {
    /// Inside a Menu's content: its rows appear when the menu opens and go
    /// when it closes, which is the only open/closed signal a Menu gives.
    func tracksMenuOpen(_ open: Binding<Bool>) -> some View {
        onAppear { open.wrappedValue = true }
            .onDisappear { open.wrappedValue = false }
    }
}

/// The season's episodes in a row under the timeline, with the show's other
/// seasons above it.
private struct EpisodeShelf: View {
    @Environment(AppState.self) private var state
    let model: PlayerModel
    let close: () -> Void
    @State private var seasons: [Item] = []
    @State private var seasonId: String?
    @State private var episodes: [Item]?

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if seasons.count > 1 {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 8) {
                        ForEach(seasons) { s in
                            Button { seasonId = s.id } label: {
                                Text(s.displayTitle).font(.caption.weight(.semibold))
                                    .padding(.horizontal, 12).padding(.vertical, 6)
                                    .background(s.id == seasonId ? Color.wrenEmber : Color.white.opacity(0.15), in: Capsule())
                            }
                            .buttonStyle(.plain)
                        }
                    }
                    .padding(.horizontal, 20)
                }
            }
            ScrollViewReader { proxy in
                ScrollView(.horizontal, showsIndicators: false) {
                    LazyHStack(alignment: .top, spacing: 12) {
                        if let episodes {
                            ForEach(episodes) { e in card(e).id(e.id) }
                        } else {
                            ProgressView().tint(.white).frame(height: 90)
                        }
                    }
                    .padding(.horizontal, 20)
                }
                .onChange(of: episodes) { _, eps in
                    if eps?.contains(where: { $0.id == model.itemId }) == true {
                        proxy.scrollTo(model.itemId, anchor: .leading)
                    }
                }
            }
        }
        .padding(.bottom, 12)
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

    private func card(_ e: Item) -> some View {
        let playing = e.id == model.itemId
        return Button {
            if !playing { model.playEpisode(e) }
            close()
        } label: {
            VStack(alignment: .leading, spacing: 4) {
                RemoteImage(url: state.api?.imageURL(e.stillPath ?? e.backdropPath, size: "w300"))
                    .frame(width: 160, height: 90)
                    .clipShape(RoundedRectangle(cornerRadius: 6))
                    .overlay(alignment: .bottom) {
                        if let p = e.progress, p.position > 0, !p.watched, !playing {
                            ProgressStrip(fraction: p.fraction).clipShape(RoundedRectangle(cornerRadius: 6))
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
                        if playing { RoundedRectangle(cornerRadius: 6).stroke(Color.wrenEmber, lineWidth: 2) }
                    }
                Text("\(e.episodeNumber). \(e.title)").font(.caption).lineLimit(1)
            }
            .frame(width: 160, alignment: .leading)
        }
        .buttonStyle(.plain)
    }
}

/// How the title is playing, refreshed every second.
private struct StatsOverlay: View {
    let model: PlayerModel
    let close: () -> Void
    @State private var rows: [(String, String)] = []

    var body: some View {
        VStack {
            HStack {
                VStack(alignment: .leading, spacing: 8) {
                    HStack {
                        Text("Stats").font(.headline)
                        Spacer()
                        Button(action: close) { Image(systemName: "xmark").font(.caption.bold()) }
                    }
                    Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 3) {
                        ForEach(Array(rows.enumerated()), id: \.offset) { _, r in
                            GridRow {
                                Text(r.0).foregroundStyle(.white.opacity(0.6))
                                Text(r.1)
                            }
                        }
                    }
                    .font(.caption.monospacedDigit())
                }
                .padding(14)
                .frame(maxWidth: 360)
                .background(.black.opacity(0.75), in: RoundedRectangle(cornerRadius: 10))
                Spacer()
            }
            Spacer()
        }
        .foregroundStyle(.white)
        .padding(.horizontal, 16)
        .padding(.top, 64)
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
        NavigationStack {
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
                                    if adding == r { ProgressView() }
                                }
                            }
                            .disabled(adding != nil)
                        }
                    }
                } else {
                    ProgressView("Searching…")
                }
            }
            .navigationTitle("Online Subtitles")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Close") { dismiss() } } }
        }
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

/// A subtitle-site download's current line, drawn over the video.
private struct SideCueView: View {
    let text: String
    let raised: Bool

    var body: some View {
        VStack {
            Spacer()
            Text(text)
                .font(.system(size: 20, weight: .semibold))
                .multilineTextAlignment(.center)
                .foregroundStyle(.white)
                .shadow(color: .black, radius: 2, y: 1)
                .padding(.horizontal, 10).padding(.vertical, 4)
                .background(.black.opacity(0.35), in: RoundedRectangle(cornerRadius: 4))
                .padding(.horizontal, 40)
                .padding(.bottom, raised ? 90 : 30)
        }
        .allowsHitTesting(false)
        .animation(.easeOut(duration: 0.2), value: raised)
    }
}

/// A menu row with a checkmark when it is the current choice.
private struct CheckLabel: View {
    let title: String
    let on: Bool

    var body: some View {
        if on { Label(title, systemImage: "checkmark") } else { Text(title) }
    }
}

/// Chapter starts, and intro/credits ranges, drawn over the timeline.
private struct ChapterTicks: View {
    let model: PlayerModel

    var body: some View {
        GeometryReader { g in
            let d = max(model.duration, 1)
            // The slider track is inset by about the knob radius.
            let inset: CGFloat = 12
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
        .padding(.trailing, 20)
        .padding(.bottom, raised ? 76 : 28)
        .animation(.easeOut(duration: 0.25), value: model.skip)
        .animation(.easeOut(duration: 0.25), value: model.countdown == nil)
    }

    @ViewBuilder
    private var content: some View {
        if model.skip == .credits, let n = model.countdown, let next = model.nextEpisode {
            HStack(spacing: 12) {
                RemoteImage(url: state.api?.imageURL(next.stillPath, size: "w300"))
                    .frame(width: 120, height: 68)
                    .clipShape(RoundedRectangle(cornerRadius: 6))
                VStack(alignment: .leading, spacing: 6) {
                    Text("Next episode in \(n)").font(.caption.bold()).foregroundStyle(Color.wrenEmber)
                    Text("\(next.episodeLabel) — \(next.title)").font(.subheadline.bold()).lineLimit(1)
                    HStack {
                        Button("Next Episode") { model.skipCredits() }
                            .buttonStyle(.borderedProminent).tint(Color.wrenEmber)
                        Button("Credits") { model.dismissCredits() }
                            .buttonStyle(.bordered)
                    }
                    .controlSize(.small)
                }
            }
            .padding(12)
            .frame(maxWidth: 380)
            .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 12))
            .transition(.move(edge: .trailing).combined(with: .opacity))
        } else if let k = model.skip {
            Button {
                k == .intro ? model.skipIntro() : model.skipCredits()
            } label: {
                Text(k == .intro ? "Skip Intro" : model.nextEpisode != nil ? "Next Episode" : "Skip Credits")
                    .font(.headline)
                    .padding(.horizontal, 18).padding(.vertical, 10)
                    .foregroundStyle(.white)
                    .background(Color.black.opacity(0.6), in: RoundedRectangle(cornerRadius: 9))
                    .overlay(RoundedRectangle(cornerRadius: 9).stroke(.white.opacity(0.4)))
            }
            .buttonStyle(.plain)
            .transition(.opacity)
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
                        .frame(width: 140, height: 79)
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
                .padding(24)
            }
        }
    }
}

/// The video itself, on an AVPlayerLayer so Picture in Picture works with
/// the app's own controls.
struct VideoLayerView: UIViewRepresentable {
    let player: AVPlayer
    let fill: Bool
    @Binding var pip: AVPictureInPictureController?

    final class LayerView: UIView {
        override class var layerClass: AnyClass { AVPlayerLayer.self }
        var playerLayer: AVPlayerLayer { layer as! AVPlayerLayer }
    }

    func makeUIView(context: Context) -> LayerView {
        let v = LayerView()
        v.backgroundColor = .black
        v.playerLayer.player = player
        v.playerLayer.videoGravity = fill ? .resizeAspectFill : .resizeAspect
        let controller = AVPictureInPictureController(playerLayer: v.playerLayer)
        controller?.canStartPictureInPictureAutomaticallyFromInline = true
        DispatchQueue.main.async { pip = controller }
        return v
    }

    func updateUIView(_ v: LayerView, context: Context) {
        if v.playerLayer.player !== player { v.playerLayer.player = player }
        let gravity: AVLayerVideoGravity = fill ? .resizeAspectFill : .resizeAspect
        if v.playerLayer.videoGravity != gravity { v.playerLayer.videoGravity = gravity }
    }
}

struct AirPlayButton: UIViewRepresentable {
    func makeUIView(context: Context) -> AVRoutePickerView {
        let v = AVRoutePickerView()
        v.tintColor = .white
        v.activeTintColor = UIColor(Color.wrenEmber)
        v.prioritizesVideoDevices = true
        return v
    }

    func updateUIView(_ v: AVRoutePickerView, context: Context) {}
}
