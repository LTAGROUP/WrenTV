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
    @State private var fill = false
    @State private var pip: AVPictureInPictureController?

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            if let model {
                PlayerContent(model: model, controlsVisible: controlsVisible, fill: $fill, pip: $pip,
                              close: { dismiss() }, poke: poke, toggleControls: toggleControls)
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
            guard !Task.isCancelled, model?.paused != true, model?.phase == .playing else { return }
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

private struct PlayerContent: View {
    @Bindable var model: PlayerModel
    let controlsVisible: Bool
    @Binding var fill: Bool
    @Binding var pip: AVPictureInPictureController?
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
                if controlsVisible {
                    Controls(model: model, fill: $fill, pip: pip, close: close, poke: poke)
                        .transition(.opacity)
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
            }
            transport
        }
        .foregroundStyle(.white)
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
                if !model.chapters.isEmpty { chapterMenu }
                if model.versions.count > 1 || model.audioStreams.count > 1 || !model.legibleOptions.isEmpty { trackMenu }
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
        } label: { barIcon("list.bullet.rectangle") }
    }

    private var trackMenu: some View {
        Menu {
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
            if !model.legibleOptions.isEmpty {
                Section("Subtitles") {
                    Button { model.selectSubtitle(nil) } label: {
                        CheckLabel(title: "Off", on: model.selectedLegible == nil)
                    }
                    ForEach(model.legibleOptions, id: \.self) { o in
                        Button { model.selectSubtitle(o) } label: {
                            CheckLabel(title: o.displayName, on: model.selectedLegible == o)
                        }
                    }
                }
            }
        } label: { barIcon("captions.bubble") }
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
private struct VideoLayerView: UIViewRepresentable {
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

private struct AirPlayButton: UIViewRepresentable {
    func makeUIView(context: Context) -> AVRoutePickerView {
        let v = AVRoutePickerView()
        v.tintColor = .white
        v.activeTintColor = UIColor(Color.wrenEmber)
        v.prioritizesVideoDevices = true
        return v
    }

    func updateUIView(_ v: AVRoutePickerView, context: Context) {}
}
