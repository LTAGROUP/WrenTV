import AVKit
import SwiftUI

/// A live channel in Apple TV's player, with the channel list and channel
/// up/down in its menus.
struct LivePlayerScreen: View {
    @Environment(AppState.self) private var state
    let target: LiveTarget
    @State private var model: LivePlayerModel?

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            if let model {
                if model.phase == .playing {
                    LiveSystemPlayer(model: model, channelId: model.channel.id)
                        .ignoresSafeArea()
                }
                switch model.phase {
                case .loading:
                    VStack(spacing: 24) {
                        ProgressView()
                        Text(model.channel.name).font(.title2.bold())
                        if let now = model.channel.now { Text(now.title).foregroundStyle(.secondary) }
                    }
                case .error(let message):
                    VStack(spacing: 30) {
                        Image(systemName: "exclamationmark.triangle").font(.system(size: 64)).foregroundStyle(Color.wrenEmber)
                        Text(model.channel.name).font(.title2.bold())
                        Text(message).foregroundStyle(.secondary).multilineTextAlignment(.center).frame(maxWidth: 1100)
                        HStack(spacing: 30) {
                            Button("Try Again") { model.retry() }
                            Button("Next Channel") { model.step(1) }
                            Button("Close", action: close)
                        }
                    }
                case .playing:
                    EmptyView()
                }
            }
        }
        .onExitCommand(perform: close)
        .onAppear {
            guard model == nil, let api = state.api,
                  let ch = state.liveChannels.first(where: { $0.id == target.channelId }) else { return }
            let list = target.channelIds.map { ids in ids.compactMap { id in state.liveChannels.first { $0.id == id } } }
                ?? target.group.map { g in state.liveChannels.filter { $0.group == g } } ?? state.liveChannels
            let m = LivePlayerModel(api: api, channel: ch, channels: list)
            model = m
            m.tune(ch)
        }
        .onDisappear { model?.stop() }
    }

    private func close() {
        model?.stop()
        state.playingLive = nil
    }
}

private struct LiveSystemPlayer: UIViewControllerRepresentable {
    let model: LivePlayerModel
    let channelId: String

    final class Coordinator {
        var channelId: String?
        var describedItem: AVPlayerItem?
    }

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeUIViewController(context: Context) -> AVPlayerViewController {
        let vc = AVPlayerViewController()
        vc.player = model.player
        apply(vc, context.coordinator)
        return vc
    }

    func updateUIViewController(_ vc: AVPlayerViewController, context: Context) {
        apply(vc, context.coordinator)
    }

    private func apply(_ vc: AVPlayerViewController, _ c: Coordinator) {
        if let item = model.player.currentItem, c.describedItem !== item {
            c.describedItem = item
            let ch = model.channel
            var meta = [metadata(.commonIdentifierTitle, ch.now?.title ?? ch.name)]
            meta.append(metadata(.iTunesMetadataTrackSubTitle, [ch.number, ch.name].compactMap { $0 }.joined(separator: " · ")))
            if let d = ch.now?.description { meta.append(metadata(.commonIdentifierDescription, d)) }
            item.externalMetadata = meta
        }
        guard c.channelId != channelId else { return }
        c.channelId = channelId
        let model = self.model
        var items: [UIMenuElement] = [
            UIAction(title: "Previous Channel", image: UIImage(systemName: "chevron.up")) { _ in MainActor.assumeIsolated { model.step(-1) } },
            UIAction(title: "Next Channel", image: UIImage(systemName: "chevron.down")) { _ in MainActor.assumeIsolated { model.step(1) } },
        ]
        if model.channels.count > 1 {
            let list = model.channels.map { ch in
                UIAction(title: [ch.number, ch.name].compactMap { $0 }.joined(separator: "  "), subtitle: ch.now?.title,
                         state: ch.id == channelId ? .on : .off) { _ in MainActor.assumeIsolated { model.tune(ch) } }
            }
            items.append(UIMenu(title: "Channels", image: UIImage(systemName: "list.bullet"), options: .singleSelection, children: list))
        }
        vc.transportBarCustomMenuItems = items
    }

    private func metadata(_ id: AVMetadataIdentifier, _ value: String) -> AVMetadataItem {
        let m = AVMutableMetadataItem()
        m.identifier = id
        m.value = value as NSString
        m.extendedLanguageTag = "und"
        return m.copy() as! AVMetadataItem
    }
}
