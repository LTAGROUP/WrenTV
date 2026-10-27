import AVKit
import SwiftUI

/// The full-screen player: Apple TV's own player for the controls, with the
/// server's audio tracks, versions and subtitle-site search in its menus,
/// chapters, the season's episodes and playback stats in its info panel
/// (swipe down), and Skip Intro / Next Episode as its contextual buttons.
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
                             menuKey: menuKey, chapterCount: model.chapters.count, cue: model.sideCue,
                             infoKey: model.episode?.id ?? "")
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
        let side = model.sideTracks.map { "\($0.index)" }.joined(separator: ",")
        return "\(audio)|\(model.audioIndex ?? -1)|\(versions)|\(model.fileId ?? "")|\(model.onlineSubtitles)|\(side)|\(model.sideSubtitle?.track.index ?? -1)"
    }
}

/// AVPlayerViewController, given the model's menus and actions.
private struct SystemPlayer: UIViewControllerRepresentable {
    @Environment(AppState.self) private var state
    let model: PlayerModel
    let player: AVPlayer
    let skip: PlayerModel.Skip?
    let countdown: Int?
    let menuKey: String
    let chapterCount: Int
    /// The subtitle-site download's line showing now.
    let cue: String?
    /// Changes when the info panel's tabs need rebuilding.
    let infoKey: String

    final class Coordinator {
        var menuKey: String?
        var describedItem: AVPlayerItem?
        var actionKey: String?
        var infoKey: String?
        let cueLabel: UILabel = {
            let l = UILabel()
            l.numberOfLines = 0
            l.textAlignment = .center
            l.textColor = .white
            l.font = .systemFont(ofSize: 46, weight: .semibold)
            l.shadowColor = UIColor.black.withAlphaComponent(0.9)
            l.shadowOffset = CGSize(width: 0, height: 2)
            l.translatesAutoresizingMaskIntoConstraints = false
            return l
        }()
    }

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeUIViewController(context: Context) -> AVPlayerViewController {
        let vc = AVPlayerViewController()
        vc.player = player
        if let overlay = vc.contentOverlayView {
            let l = context.coordinator.cueLabel
            overlay.addSubview(l)
            NSLayoutConstraint.activate([
                l.centerXAnchor.constraint(equalTo: overlay.centerXAnchor),
                l.widthAnchor.constraint(lessThanOrEqualTo: overlay.widthAnchor, multiplier: 0.8),
                l.bottomAnchor.constraint(equalTo: overlay.bottomAnchor, constant: -90),
            ])
        }
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
        if c.infoKey != infoKey {
            c.infoKey = infoKey
            vc.customInfoViewControllers = infoPanels(vc)
        }
        if c.cueLabel.text != cue { c.cueLabel.text = cue }
    }

    /// Tabs for the info panel, next to the system's own.
    private func infoPanels(_ vc: AVPlayerViewController) -> [UIViewController] {
        var out: [UIViewController] = []
        if model.episode != nil {
            let host = UIHostingController(rootView: EpisodesInfo(model: model) { [weak vc] in
                // Back to the video with the info panel closed.
                vc?.dismiss(animated: true)
            }.environment(state))
            host.title = "Episodes"
            host.preferredContentSize = CGSize(width: 0, height: 360)
            out.append(host)
        }
        let stats = UIHostingController(rootView: StatsInfo(model: model))
        stats.title = "Stats"
        stats.preferredContentSize = CGSize(width: 0, height: 360)
        out.append(stats)
        return out
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
        if model.onlineSubtitles || !model.sideTracks.isEmpty { out.append(onlineSubtitlesMenu()) }
        return out
    }

    /// Subtitles from the subtitle sites: the ones already downloaded for this
    /// title, then a search, run when the menu opens.
    private func onlineSubtitlesMenu() -> UIMenu {
        let model = self.model
        let on = model.sideSubtitle?.track.index
        var kept: [UIMenuElement] = []
        if on != nil {
            kept.append(UIAction(title: "Off") { _ in MainActor.assumeIsolated { model.clearSideSubtitle() } })
        }
        kept += model.sideTracks.map { t in
            UIAction(title: t.title ?? t.language ?? "Subtitles", state: t.index == on ? .on : .off) { _ in
                MainActor.assumeIsolated { Task { try? await model.showSideSubtitle(t) } }
            }
        }
        var children: [UIMenuElement] = []
        if !kept.isEmpty { children.append(UIMenu(title: "", options: .displayInline, children: kept)) }
        if model.onlineSubtitles {
            let search = UIDeferredMenuElement.uncached { done in
                Task { @MainActor in
                    do {
                        let found = try await model.searchOnlineSubtitles()
                        if found.isEmpty {
                            done([UIAction(title: "Nothing found", attributes: .disabled) { _ in }])
                            return
                        }
                        done(found.prefix(40).map { r in
                            UIAction(title: r.name.isEmpty ? r.provider : r.name, subtitle: r.detail) { _ in
                                MainActor.assumeIsolated { Task { try? await model.addOnlineSubtitle(r) } }
                            }
                        })
                    } catch {
                        done([UIAction(title: error.localizedDescription, attributes: .disabled) { _ in }])
                    }
                }
            }
            children.append(UIMenu(title: "Search", options: .displayInline, children: [search]))
        }
        return UIMenu(title: "Online Subtitles", image: UIImage(systemName: "captions.bubble"), children: children)
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

/// The info panel's Episodes tab: the season's episodes in a row, with the
/// show's other seasons above it.
private struct EpisodesInfo: View {
    @Environment(AppState.self) private var state
    let model: PlayerModel
    let close: () -> Void
    @State private var seasons: [Item] = []
    @State private var seasonId: String?
    @State private var episodes: [Item]?
    @FocusState private var focus: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            if seasons.count > 1 {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 16) {
                        ForEach(seasons) { s in
                            Button { seasonId = s.id } label: {
                                Text(s.displayTitle).font(.callout.weight(s.id == seasonId ? .bold : .regular))
                            }
                            .buttonStyle(.bordered)
                            .tint(s.id == seasonId ? Color.wrenEmber : nil)
                        }
                    }
                    .padding(.horizontal, TV.gutter)
                }
            }
            if let eps = episodes {
                ScrollViewReader { proxy in
                    ScrollView(.horizontal, showsIndicators: false) {
                        LazyHStack(spacing: 40) {
                            ForEach(eps) { e in card(e).id(e.id) }
                        }
                        .padding(.horizontal, TV.gutter)
                        .padding(.vertical, 20)
                    }
                    .onAppear {
                        if let cur = eps.first(where: { $0.id == model.itemId }) {
                            proxy.scrollTo(cur.id, anchor: .leading)
                            focus = cur.id
                        }
                    }
                }
            } else {
                ProgressView().frame(maxWidth: .infinity, minHeight: 200)
            }
        }
        .task {
            guard let ep = model.episode else { return }
            if seasonId == nil { seasonId = ep.parentId }
            if let show = ep.showId { seasons = await model.children(show).sorted { $0.seasonNumber < $1.seasonNumber } }
        }
        .task(id: seasonId) {
            guard let seasonId else { return }
            let eps = await model.children(seasonId).sorted { $0.episodeNumber < $1.episodeNumber }
            episodes = eps
        }
    }

    private func card(_ e: Item) -> some View {
        let playing = e.id == model.itemId
        return VStack(alignment: .leading, spacing: 10) {
            Button {
                if !playing { model.playEpisode(e) }
                close()
            } label: {
                RemoteImage(url: state.api?.imageURL(e.stillPath ?? e.backdropPath, size: "w500"))
                    .frame(width: 340, height: 191)
                    .overlay(alignment: .bottom) {
                        if let p = e.progress, p.position > 0, !p.watched, !playing { ProgressStrip(fraction: p.fraction) }
                    }
                    .overlay(alignment: .topLeading) {
                        if playing {
                            Text("Playing").font(.caption2.bold()).padding(.horizontal, 10).padding(.vertical, 4)
                                .background(Color.wrenEmber, in: RoundedRectangle(cornerRadius: 6)).padding(12)
                        }
                    }
                    .overlay(alignment: .topTrailing) {
                        if e.isWatched, !playing {
                            Image(systemName: "checkmark.circle.fill").font(.title3).foregroundStyle(.white, Color.wrenEmber).padding(12)
                        }
                    }
            }
            .buttonStyle(.card)
            .focused($focus, equals: e.id)
            Text("\(e.episodeNumber). \(e.title)").font(.caption).lineLimit(1)
            if let m = e.runtimeMin, m > 0 { Text("\(m) min").font(.caption2).foregroundStyle(.secondary) }
        }
        .frame(width: 340)
    }
}

/// The info panel's Stats tab: how the title is playing, refreshed every second.
private struct StatsInfo: View {
    let model: PlayerModel
    @State private var rows: [(String, String)] = []

    var body: some View {
        ScrollView {
            Grid(alignment: .leading, horizontalSpacing: 40, verticalSpacing: 8) {
                ForEach(Array(rows.enumerated()), id: \.offset) { _, r in
                    GridRow {
                        Text(r.0).foregroundStyle(.secondary)
                        Text(r.1)
                    }
                    .font(.callout)
                }
            }
            .padding(.horizontal, TV.gutter)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task {
            while !Task.isCancelled {
                rows = model.stats()
                try? await Task.sleep(for: .seconds(1))
            }
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
