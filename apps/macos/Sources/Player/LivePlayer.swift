import AVFoundation
import AppKit
import Combine
import Observation
import SwiftUI

/// Plays a live channel through libmpv, which reads both the HLS and the
/// plain MPEG-TS streams IPTV providers send. One mpv instance lives as long
/// as the window; changing channel loads the new stream into it.
@MainActor
@Observable
final class LivePlayerModel {
    enum Phase: Equatable { case loading, playing, error(String) }

    let api: WrenTVAPI
    /// The channels up and down step through.
    var channels: [LiveChannel]
    var channel: LiveChannel
    var phase: Phase = .loading
    var buffering = false
    var paused = false
    var muted = false
    let mpv = MPVController()

    @ObservationIgnored private var layer: MPVLayer?
    @ObservationIgnored private var layerReady = false
    @ObservationIgnored private var pendingURL: URL?
    @ObservationIgnored private var streamURL: URL?
    @ObservationIgnored private var sessionId: String?
    @ObservationIgnored private var tuneTask: Task<Void, Never>?
    @ObservationIgnored private var stopped = false

    init(api: WrenTVAPI, channel: LiveChannel, channels: [LiveChannel]) {
        self.api = api
        self.channel = channel
        self.channels = channels.isEmpty ? [channel] : channels
        mpv.set("cache-secs", "20")
        // mpv calls these on the main queue.
        mpv.onPause = { [weak self] in self?.paused = $0 }
        mpv.onBuffering = { [weak self] in self?.buffering = $0 }
        mpv.onEnd = { [weak self] in self?.ended(self?.streamURL, fallback: "The channel stopped sending video.") }
        mpv.onError = { [weak self] in self?.phase = .error($0) }
    }

    func attach(_ layer: MPVLayer) {
        self.layer = layer
        layer.onReady = { [weak self] in
            guard let self else { return }
            self.layerReady = true
            if let url = self.pendingURL {
                self.pendingURL = nil
                self.mpv.load(url, start: 0)
            }
        }
    }

    func tune(_ ch: LiveChannel) {
        channel = ch
        tuneTask?.cancel()
        tuneTask = Task { await run(ch) }
    }

    func retry() { tune(channel) }

    /// Steps through the list: +1 is channel up.
    func step(_ d: Int) {
        guard let i = channels.firstIndex(where: { $0.id == channel.id }), channels.count > 1 else { return }
        tune(channels[(i + d + channels.count) % channels.count])
    }

    private func run(_ ch: LiveChannel) async {
        phase = .loading
        mpv.command("stop")
        if let old = sessionId {
            sessionId = nil
            Task.detached { [api] in await api.stopSession(old) }
        }
        do {
            let r = try await api.playChannel(ch.id, hls: false)
            if Task.isCancelled || stopped {
                await api.stopSession(r.sessionId)
                return
            }
            sessionId = r.sessionId
            let url = api.absolute(r.url)
            streamURL = url
            if layerReady { mpv.load(url, start: 0) } else { pendingURL = url }
            phase = .playing
            UserDefaults.standard.set(ch.id, forKey: "lastLiveChannel")
        } catch {
            if !Task.isCancelled { phase = .error(error.localizedDescription) }
        }
    }

    /// Shows why the channel ended: when the server owner stopped it, the
    /// stream's URL now answers 410 with their message.
    private func ended(_ url: URL?, fallback: String) {
        phase = .error(fallback)
        guard let url else { return }
        Task {
            guard let (data, resp) = try? await URLSession.shared.data(from: url),
                  (resp as? HTTPURLResponse)?.statusCode == 410,
                  let why = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines),
                  why.hasPrefix("The server owner") else { return }
            phase = .error(why)
        }
    }

    func togglePause() { mpv.togglePause() }

    func toggleMute() {
        muted.toggle()
        mpv.set("mute", muted ? "yes" : "no")
    }

    func stop() {
        guard !stopped else { return }
        stopped = true
        tuneTask?.cancel()
        if let sid = sessionId { Task.detached { [api] in await api.stopSession(sid) } }
        layer?.teardown()
        layer = nil
        mpv.destroy()
    }
}

struct LivePlayerWindow: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let target: LiveTarget

    @State private var model: LivePlayerModel?
    @State private var controlsVisible = true
    @State private var showList = false
    @State private var hideTask: Task<Void, Never>?
    @State private var now = Date()

    var body: some View {
        ZStack {
            Color.black
            if let model {
                content(model)
            } else {
                ProgressView().controlSize(.large)
            }
        }
        .ignoresSafeArea()
        .navigationTitle(model.map { [$0.channel.number, $0.channel.name].compactMap { $0 }.joined(separator: "  ") } ?? "Live TV")
        .task {
            guard model == nil, let api = state.api else { return }
            var channels = state.liveChannels
            if channels.isEmpty { channels = (try? await api.liveChannels()) ?? [] }
            state.liveChannels = channels
            guard let ch = channels.first(where: { $0.id == target.channelId }) else { return }
            // Up and down follow the guide as it was filtered.
            let list = target.channelIds.map { ids in
                // By id, not a search per channel: a big provider's guide has thousands.
                let byId = Dictionary(channels.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
                return ids.compactMap { byId[$0] }
            }
                ?? target.group.map { g in channels.filter { $0.group == g } } ?? channels
            let m = LivePlayerModel(api: api, channel: ch, channels: list)
            model = m
            m.tune(ch)
            poke()
        }
        .task {
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(30))
                now = Date()
            }
        }
        .onDisappear { model?.stop() }
        .onContinuousHover { _ in poke() }
        .focusable()
        .focusEffectDisabled()
        .onKeyPress(.space) { model?.togglePause(); poke(); return .handled }
        .onKeyPress(.upArrow) { model?.step(-1); poke(); return .handled }
        .onKeyPress(.downArrow) { model?.step(1); poke(); return .handled }
        .onKeyPress("m") { model?.toggleMute(); poke(); return .handled }
        .onKeyPress("f") { NSApp.keyWindow?.toggleFullScreen(nil); return .handled }
        .onKeyPress("l") { withAnimation { showList.toggle() }; poke(); return .handled }
        .onKeyPress(.escape) {
            if showList { withAnimation { showList = false }; return .handled }
            if NSApp.keyWindow?.styleMask.contains(.fullScreen) == true { NSApp.keyWindow?.toggleFullScreen(nil); return .handled }
            return .ignored
        }
    }

    @ViewBuilder
    private func content(_ model: LivePlayerModel) -> some View {
        ZStack {
            MPVVideoView(controller: model.mpv) { model.attach($0) }
                .onTapGesture(count: 2) { NSApp.keyWindow?.toggleFullScreen(nil) }
                .onTapGesture { model.togglePause() }
            switch model.phase {
            case .loading:
                VStack(spacing: 12) {
                    ProgressView().controlSize(.large)
                    Text("Tuning to \(model.channel.name)…").foregroundStyle(.secondary)
                }
            case .error(let msg):
                VStack(spacing: 14) {
                    Image(systemName: "exclamationmark.triangle").font(.system(size: 40)).foregroundStyle(Color.wrenEmber)
                    Text("Can't play \(model.channel.name)").font(.title2.bold())
                    Text(msg).foregroundStyle(.secondary).multilineTextAlignment(.center).frame(maxWidth: 480)
                    HStack {
                        Button("Try Again") { model.retry() }
                        if model.channels.count > 1 { Button("Next Channel") { model.step(1) } }
                        Button("Close") { dismiss() }
                    }
                }
                .padding(30)
                .background(.black.opacity(0.6), in: RoundedRectangle(cornerRadius: 16))
            case .playing:
                if model.buffering { ProgressView().controlSize(.large) }
            }
            if controlsVisible || model.phase != .playing {
                VStack {
                    topBar(model)
                    Spacer()
                    bottomBar(model)
                }
                .transition(.opacity)
            }
            if showList {
                HStack {
                    Spacer()
                    ChannelList(model: model, now: now) { withAnimation { showList = false } }
                        .frame(width: 340)
                        .transition(.move(edge: .trailing))
                }
            }
        }
    }

    private func topBar(_ model: LivePlayerModel) -> some View {
        HStack(alignment: .top, spacing: 14) {
            ChannelLogo(url: state.api?.liveLogoURL(model.channel.logo), name: model.channel.name)
                .frame(width: 72, height: 46)
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    if let n = model.channel.number { Text(n).font(.headline.monospacedDigit()).foregroundStyle(.secondary) }
                    Text(model.channel.name).font(.headline)
                    Text("LIVE").font(.caption2.bold()).padding(.horizontal, 6).padding(.vertical, 2)
                        .background(Color.wrenEmber, in: RoundedRectangle(cornerRadius: 4))
                }
                if let p = airing(model.channel) {
                    Text(p.title).font(.title3.bold())
                    HStack(spacing: 8) {
                        Text(p.timeRange).font(.caption).foregroundStyle(.secondary)
                        ProgressView(value: p.progress(at: now)).frame(width: 140).tint(.white)
                    }
                }
                if let n = model.channel.next {
                    Text("Next: \(n.title) at \(n.startDate.formatted(date: .omitted, time: .shortened))")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            Spacer()
        }
        .padding(.horizontal, 24)
        .padding(.top, 40)
        .padding(.bottom, 30)
        .background(LinearGradient(colors: [.black.opacity(0.75), .clear], startPoint: .top, endPoint: .bottom))
    }

    private func bottomBar(_ model: LivePlayerModel) -> some View {
        HStack(spacing: 22) {
            Button { model.step(-1) } label: { Image(systemName: "chevron.up") }
                .help("Previous channel (↑)")
            Button { model.togglePause() } label: { Image(systemName: model.paused ? "play.fill" : "pause.fill") }
                .help(model.paused ? "Play" : "Pause")
            Button { model.step(1) } label: { Image(systemName: "chevron.down") }
                .help("Next channel (↓)")
            Spacer()
            Button { model.toggleMute() } label: { Image(systemName: model.muted ? "speaker.slash.fill" : "speaker.wave.2.fill") }
                .help(model.muted ? "Unmute (M)" : "Mute (M)")
            Button { withAnimation { showList.toggle() } } label: { Image(systemName: "list.bullet") }
                .help("Channels (L)")
            Button { NSApp.keyWindow?.toggleFullScreen(nil) } label: { Image(systemName: "arrow.up.left.and.arrow.down.right") }
                .help("Full Screen (F)")
        }
        .font(.title2)
        .buttonStyle(.plain)
        .padding(.horizontal, 28)
        .padding(.top, 40)
        .padding(.bottom, 22)
        .background(LinearGradient(colors: [.clear, .black.opacity(0.75)], startPoint: .top, endPoint: .bottom))
    }

    private func airing(_ ch: LiveChannel) -> LiveProgram? {
        [ch.now, ch.next].compactMap { $0 }.first { $0.startDate <= now && now < $0.endDate }
    }

    private func poke() {
        controlsVisible = true
        hideTask?.cancel()
        hideTask = Task {
            try? await Task.sleep(for: .seconds(3))
            guard !Task.isCancelled, !showList else { return }
            withAnimation(.easeOut(duration: 0.3)) { controlsVisible = false }
            // The pointer goes with the controls; moving it brings both back.
            if NSApp.isActive { NSCursor.setHiddenUntilMouseMoves(true) }
        }
    }
}

/// The channels beside the picture, for flipping without leaving it.
private struct ChannelList: View {
    @Environment(AppState.self) private var state
    let model: LivePlayerModel
    let now: Date
    let close: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("Channels").font(.headline)
                Spacer()
                Button(action: close) { Image(systemName: "xmark") }.buttonStyle(.plain)
            }
            .padding(14)
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(spacing: 2) {
                        ForEach(model.channels) { ch in
                            Button { model.tune(ch) } label: {
                                HStack(spacing: 10) {
                                    ChannelLogo(url: state.api?.liveLogoURL(ch.logo), name: ch.name)
                                        .frame(width: 44, height: 28)
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text([ch.number, ch.name].compactMap { $0 }.joined(separator: "  "))
                                            .font(.callout.weight(.semibold)).lineLimit(1)
                                        if let p = ch.now, p.endDate > now {
                                            Text(p.title).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                                        }
                                    }
                                    Spacer(minLength: 0)
                                }
                                .padding(.horizontal, 12).padding(.vertical, 7)
                                .background(ch.id == model.channel.id ? Color.white.opacity(0.14) : .clear, in: RoundedRectangle(cornerRadius: 8))
                                .contentShape(Rectangle())
                            }
                            .buttonStyle(.plain)
                            .id(ch.id)
                        }
                    }
                    .padding(.horizontal, 8)
                }
                .onAppear { proxy.scrollTo(model.channel.id, anchor: .center) }
            }
        }
        .padding(.top, 28)
        .background(.ultraThinMaterial)
    }
}

/// The guide's muted picture of the picked channel. It starts a moment after
/// the pick settles, so clicking down the guide doesn't open a stream per
/// channel, and stops the last one before the next. AVPlayer reads the HLS
/// the server repackages channels into.
@MainActor
@Observable
final class LivePreview {
    let player = AVPlayer()
    /// The picture is moving (until then the guide shows the programme's art).
    private(set) var showing = false
    private(set) var channelId: String?

    @ObservationIgnored private var api: WrenTVAPI?
    @ObservationIgnored private var sessionId: String?
    @ObservationIgnored private var task: Task<Void, Never>?
    @ObservationIgnored private var subs: Set<AnyCancellable> = []

    init() {
        player.isMuted = true
        player.publisher(for: \.timeControlStatus).receive(on: DispatchQueue.main).sink { [weak self] s in
            guard let self, self.player.currentItem != nil else { return }
            if s == .playing { self.showing = true }
        }.store(in: &subs)
    }

    func show(_ ch: LiveChannel?, api: WrenTVAPI?, after delay: Duration = .milliseconds(900)) {
        guard ch?.id != channelId else { return }
        stop()
        channelId = ch?.id
        guard let ch, let api else { return }
        self.api = api
        task = Task { [weak self] in
            try? await Task.sleep(for: delay)
            guard !Task.isCancelled else { return }
            guard let r = try? await api.playChannel(ch.id, hls: true, preview: true) else { return }
            guard let self, !Task.isCancelled, self.channelId == ch.id, r.type == "hls" else {
                await api.stopSession(r.sessionId)
                return
            }
            self.sessionId = r.sessionId
            self.player.replaceCurrentItem(with: AVPlayerItem(url: api.absolute(r.url)))
            self.player.play()
        }
    }

    func stop() {
        task?.cancel()
        task = nil
        channelId = nil
        showing = false
        player.replaceCurrentItem(with: nil)
        if let sid = sessionId, let api {
            Task.detached { await api.stopSession(sid) }
        }
        sessionId = nil
    }
}

/// A player's picture with no controls, filling its frame.
struct LivePreviewSurface: NSViewRepresentable {
    let player: AVPlayer

    func makeNSView(context: Context) -> NSView {
        let v = NSView()
        v.wantsLayer = true
        let layer = AVPlayerLayer(player: player)
        layer.videoGravity = .resizeAspectFill
        v.layer = layer
        return v
    }

    func updateNSView(_ v: NSView, context: Context) {
        if let layer = v.layer as? AVPlayerLayer, layer.player !== player { layer.player = player }
    }
}
