import AVKit
import SwiftUI

/// The full-screen player: Apple TV's own player for the controls, with the
/// server's audio tracks and versions in its menus, chapters in its info
/// panel, and Skip Intro / Next Episode as its contextual buttons.
struct PlayerScreen: View {
    @Environment(AppState.self) private var state
    let target: PlayTarget
    @State private var model: PlayerModel?

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            if let model { PlayerBody(model: model, close: close) }
        }
        .onAppear {
            guard model == nil, let api = state.api else { return }
            let m = PlayerModel(target: target, api: api, online: true)
            model = m
            m.start()
        }
        .onDisappear {
            model?.stop()
            state.refreshTick += 1 // progress changed
        }
    }

    private func close() {
        model?.stop()
        state.playing = nil
    }
}

private struct PlayerBody: View {
    let model: PlayerModel
    let close: () -> Void

    var body: some View {
        ZStack {
            if let player = model.avPlayer, model.phase == .playing {
                SystemPlayer(model: model, player: player, skip: model.skip, countdown: model.countdown,
                             menuKey: menuKey, chapterCount: model.chapters.count)
                    .ignoresSafeArea()
            }
            switch model.phase {
            case .loading:
                StatusView(title: model.title, subtitle: model.subtitle, message: nil)
            case .pending(let message):
                StatusView(title: model.title, subtitle: model.subtitle, message: message)
            case .error(let message):
                ErrorView(message: message, retry: { model.retry() }, close: close)
            case .ended:
                if let next = model.upNext {
                    UpNextView(model: model, next: next, close: close)
                } else {
                    Color.black.ignoresSafeArea().onAppear(perform: close)
                }
            case .playing:
                EmptyView()
            }
        }
        .onChange(of: model.closeRequested) { _, done in if done { close() } }
        .onExitCommand(perform: close)
    }

    /// Changes when the transport bar's menus need rebuilding.
    private var menuKey: String {
        let audio = model.audioStreams.map { "\($0.typeIndex)" }.joined(separator: ",")
        let versions = model.versions.map(\.id).joined(separator: ",")
        return "\(audio)|\(model.audioIndex ?? -1)|\(versions)|\(model.fileId ?? "")"
    }
}

/// AVPlayerViewController, given the model's menus and actions.
private struct SystemPlayer: UIViewControllerRepresentable {
    let model: PlayerModel
    let player: AVPlayer
    let skip: PlayerModel.Skip?
    let countdown: Int?
    let menuKey: String
    let chapterCount: Int

    final class Coordinator {
        var menuKey: String?
        var describedItem: AVPlayerItem?
        var actionKey: String?
    }

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeUIViewController(context: Context) -> AVPlayerViewController {
        let vc = AVPlayerViewController()
        vc.player = player
        apply(vc, context.coordinator)
        return vc
    }

    func updateUIViewController(_ vc: AVPlayerViewController, context: Context) {
        if vc.player !== player {
            vc.player = player
            context.coordinator.menuKey = nil
        }
        apply(vc, context.coordinator)
    }

    private func apply(_ vc: AVPlayerViewController, _ c: Coordinator) {
        if let item = player.currentItem, c.describedItem !== item {
            c.describedItem = item
            describe(item)
        }
        if c.menuKey != menuKey {
            c.menuKey = menuKey
            vc.transportBarCustomMenuItems = menus()
        }
        let actionKey = "\(String(describing: skip))|\(countdown ?? -1)"
        if c.actionKey != actionKey {
            c.actionKey = actionKey
            vc.contextualActions = actions()
        }
    }

    /// The title in the info panel, and the chapters to jump between.
    private func describe(_ item: AVPlayerItem) {
        var meta = [metadata(.commonIdentifierTitle, model.title)]
        if let s = model.subtitle { meta.append(metadata(.iTunesMetadataTrackSubTitle, s)) }
        item.externalMetadata = meta
        let chapters = model.chapters.filter { $0.end > $0.start }
        if chapters.count > 1 {
            let markers = chapters.map { ch in
                AVTimedMetadataGroup(items: [metadata(.commonIdentifierTitle, ch.title)],
                                     timeRange: CMTimeRange(start: CMTime(seconds: ch.start, preferredTimescale: 600),
                                                            end: CMTime(seconds: ch.end, preferredTimescale: 600)))
            }
            item.navigationMarkerGroups = [AVNavigationMarkersGroup(title: nil, timedNavigationMarkers: markers)]
        }
    }

    private func metadata(_ id: AVMetadataIdentifier, _ value: String) -> AVMetadataItem {
        let m = AVMutableMetadataItem()
        m.identifier = id
        m.value = value as NSString
        m.extendedLanguageTag = "und"
        return m.copy() as! AVMetadataItem
    }

    private func menus() -> [UIMenuElement] {
        var out: [UIMenuElement] = []
        let model = self.model
        // Changing audio needs a new stream from the server, so the server's tracks are listed here.
        if model.audioStreams.count > 1 {
            let tracks = model.audioStreams.map { s in
                UIAction(title: s.label, state: s.typeIndex == model.audioIndex ? .on : .off) { _ in
                    MainActor.assumeIsolated { model.selectAudio(s.typeIndex) }
                }
            }
            out.append(UIMenu(title: "Audio Track", image: UIImage(systemName: "waveform"), options: .singleSelection, children: tracks))
        }
        if model.versions.count > 1 {
            let versions = model.versions.map { v in
                UIAction(title: v.label, state: v.id == model.fileId ? .on : .off) { _ in
                    MainActor.assumeIsolated { model.selectVersion(v.id) }
                }
            }
            out.append(UIMenu(title: "Version", image: UIImage(systemName: "square.stack"), options: .singleSelection, children: versions))
        }
        return out
    }

    private func actions() -> [UIAction] {
        let model = self.model
        switch skip {
        case .intro:
            return [UIAction(title: "Skip Intro", image: UIImage(systemName: "forward.fill")) { _ in
                MainActor.assumeIsolated { model.skipIntro() }
            }]
        case .credits:
            let next = UIAction(title: countdown.map { "Next Episode in \($0)" } ?? "Skip Credits", image: UIImage(systemName: "forward.end.fill")) { _ in
                MainActor.assumeIsolated { model.skipCredits() }
            }
            guard countdown != nil else { return [next] }
            let stay = UIAction(title: "Watch Credits") { _ in MainActor.assumeIsolated { model.dismissCredits() } }
            return [stay, next]
        case nil:
            return []
        }
    }
}

/// Starting: the title and what the server is doing.
private struct StatusView: View {
    let title: String
    let subtitle: String?
    let message: String?

    var body: some View {
        VStack(spacing: 24) {
            ProgressView()
            if !title.isEmpty { Text(title).font(.title2.bold()) }
            if let subtitle { Text(subtitle).foregroundStyle(.secondary) }
            if let message { Text(message).font(.callout).foregroundStyle(.secondary).multilineTextAlignment(.center).frame(maxWidth: 1000) }
        }
    }
}

private struct ErrorView: View {
    let message: String
    let retry: () -> Void
    let close: () -> Void

    var body: some View {
        VStack(spacing: 30) {
            Image(systemName: "exclamationmark.triangle").font(.system(size: 64)).foregroundStyle(Color.wrenEmber)
            Text("Couldn't play this").font(.title2.bold())
            Text(message).foregroundStyle(.secondary).multilineTextAlignment(.center).frame(maxWidth: 1100)
            HStack(spacing: 30) {
                Button("Try Again", action: retry)
                Button("Close", action: close)
            }
        }
    }
}

/// The next episode, starting by itself after a few seconds.
private struct UpNextView: View {
    @Environment(AppState.self) private var state
    let model: PlayerModel
    let next: Item
    let close: () -> Void
    @State private var left = 10

    var body: some View {
        ZStack(alignment: .bottomLeading) {
            RemoteImage(url: state.api?.imageURL(next.stillPath ?? next.backdropPath, size: "w1280"))
                .overlay { LinearGradient(colors: [.clear, .black.opacity(0.9)], startPoint: .top, endPoint: .bottom) }
                .ignoresSafeArea()
            VStack(alignment: .leading, spacing: 18) {
                Text("Up Next").font(.callout.weight(.semibold)).foregroundStyle(Color.wrenEmber)
                Text(next.showTitle ?? "").font(.headline).foregroundStyle(.secondary)
                Text("\(next.episodeLabel) — \(next.title)").font(.title.bold())
                if let o = next.overview { Text(o).lineLimit(3).frame(maxWidth: 1100, alignment: .leading) }
                HStack(spacing: 30) {
                    Button { model.playNext() } label: { Label("Play in \(left)", systemImage: "play.fill") }
                        .prefersDefaultFocus(true, in: ns)
                    Button("Close", action: close)
                }
                .padding(.top, 12)
            }
            .padding(TV.gutter)
        }
        .focusScope(ns)
        .task {
            while left > 0, !Task.isCancelled {
                try? await Task.sleep(for: .seconds(1))
                left -= 1
            }
            if !Task.isCancelled { model.playNext() }
        }
    }

    @Namespace private var ns
}
