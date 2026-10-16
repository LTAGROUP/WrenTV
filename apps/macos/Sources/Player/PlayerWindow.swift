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
        .onKeyPress(.space) { model?.togglePause(); poke(); return model?.engine == "mpv" ? .handled : .ignored }
        .onKeyPress(.leftArrow) { guard model?.engine == "mpv" else { return .ignored }; model?.seek(by: -10); poke(); return .handled }
        .onKeyPress(.rightArrow) { guard model?.engine == "mpv" else { return .ignored }; model?.seek(by: 10); poke(); return .handled }
        .onKeyPress("s") {
            guard let m = model, let k = m.skip else { return .ignored }
            k == .intro ? m.skipIntro() : m.skipCredits()
            return .handled
        }
        .onChange(of: model?.closeRequested) { _, v in if v == true { dismiss() } }
        .onChange(of: model?.menuOpen) { _, open in if open == false { poke() } }
        .onKeyPress(.escape) {
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
            if model?.menuOpen == true { return }
            withAnimation(.easeOut(duration: 0.3)) { controlsVisible = false }
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
                    Button("Try Again") { model.start(at: model.position > 0 ? model.position : nil) }
                    Button("Close", action: close)
                }
            }
        case .playing, .ended:
            ZStack {
                video
                if model.buffering {
                    ProgressView().controlSize(.large)
                }
                VStack {
                    if controlsVisible { topBar.transition(.opacity) }
                    Spacer()
                    if model.engine == "mpv" && controlsVisible { MPVControls(model: model).transition(.opacity) }
                }
                if model.phase == .ended, let next = model.upNext {
                    UpNextCard(next: next, play: model.playNext)
                } else if model.phase == .playing {
                    SkipOverlay(model: model, raised: model.engine == "mpv" && controlsVisible)
                }
            }
        }
    }

    @ViewBuilder
    private var video: some View {
        if model.engine == "mpv", let c = model.mpv {
            MPVVideoView(controller: c) { model.attach($0) }
                .onTapGesture(count: 2) { NSApp.keyWindow?.toggleFullScreen(nil) }
                .onTapGesture { model.togglePause() }
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
            HStack(spacing: 6) {
                if model.versions.count > 1 { VersionMenu(model: model) }
                if !model.chapters.isEmpty { ChapterMenu(model: model) }
                if model.engine == "mpv" || model.audioStreams.count > 1 { TrackMenu(model: model) }
            }
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
                .font(.system(size: 19, weight: .medium))
                .frame(width: 38, height: 34)
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
        .padding(.bottom, raised ? 130 : 96)
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
        let mpv = model.engine == "mpv"
        PlayerMenuButton(model: model, symbol: mpv ? "captions.bubble" : "speaker.wave.2", help: mpv ? "Audio & Subtitles" : "Audio") {
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
                // Subtitles are in AVKit's own menu, next to the video's captions.
                PlayerMenuSection(title: "Audio") {
                    ForEach(model.audioStreams, id: \.typeIndex) { a in
                        PlayerMenuRow(title: a.label, selected: model.audioIndex == a.typeIndex) { model.selectAudio(a.typeIndex) }
                    }
                }
            }
        }
    }
}

private struct MPVControls: View {
    @Bindable var model: PlayerModel
    @State private var scrub: Double?

    var body: some View {
        VStack(spacing: 8) {
            Slider(value: Binding(get: { scrub ?? model.position }, set: { scrub = $0 }),
                   in: 0...max(model.duration, 1)) { editing in
                if !editing, let s = scrub {
                    model.seek(to: s)
                    scrub = nil
                }
            }
            .controlSize(.small)
            .overlay { ChapterTicks(model: model).allowsHitTesting(false) }
            HStack(spacing: 22) {
                Text(formatClock(scrub ?? model.position)).monospacedDigit()
                if let c = model.currentChapter, !c.title.isEmpty {
                    Text(c.title).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer()
                Button { model.seek(by: -10) } label: { Image(systemName: "gobackward.10") }
                Button { model.togglePause() } label: { Image(systemName: model.paused ? "play.fill" : "pause.fill").font(.title) }
                Button { model.seek(by: 10) } label: { Image(systemName: "goforward.10") }
                Spacer()
                Text("-" + formatClock(max(0, model.duration - (scrub ?? model.position)))).monospacedDigit()
            }
            .buttonStyle(.plain)
            .font(.title3)
        }
        .padding(.horizontal, 28)
        .padding(.bottom, 22)
        .padding(.top, 40)
        .background(LinearGradient(colors: [.clear, .black.opacity(0.75)], startPoint: .top, endPoint: .bottom))
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
        v.controlsStyle = .floating
        v.showsFullScreenToggleButton = true
        v.allowsPictureInPicturePlayback = true
        v.videoGravity = .resizeAspect
        return v
    }

    func updateNSView(_ v: AVPlayerView, context: Context) {
        if v.player !== player { v.player = player }
    }
}
