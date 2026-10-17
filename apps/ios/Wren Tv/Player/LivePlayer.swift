import AVKit
import Combine
import MediaPlayer
import SwiftUI

/// Plays a live channel. AVPlayer can't read a plain MPEG-TS stream, so the
/// app asks the server for HLS; the server repackages TS channels.
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
    let player = AVPlayer()

    @ObservationIgnored private var sessionId: String?
    @ObservationIgnored private var tuneTask: Task<Void, Never>?
    @ObservationIgnored private var subs: Set<AnyCancellable> = []
    @ObservationIgnored private var itemSubs: Set<AnyCancellable> = []
    @ObservationIgnored private var remoteTargets: [Any] = []
    @ObservationIgnored private var stopped = false

    init(api: WrenTVAPI, channel: LiveChannel, channels: [LiveChannel]) {
        self.api = api
        self.channel = channel
        self.channels = channels.isEmpty ? [channel] : channels
        player.publisher(for: \.timeControlStatus).receive(on: DispatchQueue.main).sink { [weak self] s in
            self?.buffering = s == .waitingToPlayAtSpecifiedRate
            self?.paused = s == .paused
        }.store(in: &subs)
    }

    func tune(_ ch: LiveChannel) {
        channel = ch
        tuneTask?.cancel()
        tuneTask = Task { await run(ch) }
    }

    func retry() { tune(channel) }

    /// +1 is the next channel down the list.
    func step(_ d: Int) {
        guard let i = channels.firstIndex(where: { $0.id == channel.id }), channels.count > 1 else { return }
        tune(channels[(i + d + channels.count) % channels.count])
    }

    private func run(_ ch: LiveChannel) async {
        phase = .loading
        player.replaceCurrentItem(with: nil)
        itemSubs = []
        if let old = sessionId {
            sessionId = nil
            Task.detached { [api] in await api.stopSession(old) }
        }
        try? AVAudioSession.sharedInstance().setActive(true)
        setUpRemoteCommands()
        do {
            let r = try await api.playChannel(ch.id, hls: true)
            if Task.isCancelled || stopped {
                await api.stopSession(r.sessionId)
                return
            }
            sessionId = r.sessionId
            guard r.type == "hls" else {
                phase = .error("This channel sends a stream iPhone can't play, and the server can't convert it. Update the server to watch it here.")
                return
            }
            let item = AVPlayerItem(url: api.absolute(r.url))
            item.preferredForwardBufferDuration = 6
            item.publisher(for: \.status).receive(on: DispatchQueue.main).sink { [weak self] s in
                if s == .failed { self?.phase = .error(item.error?.localizedDescription ?? "The channel didn't play.") }
            }.store(in: &itemSubs)
            NotificationCenter.default.publisher(for: AVPlayerItem.didPlayToEndTimeNotification, object: item)
                .merge(with: NotificationCenter.default.publisher(for: AVPlayerItem.failedToPlayToEndTimeNotification, object: item))
                .receive(on: DispatchQueue.main)
                .sink { [weak self] _ in self?.phase = .error("The channel stopped sending video.") }
                .store(in: &itemSubs)
            player.replaceCurrentItem(with: item)
            player.play()
            phase = .playing
            UserDefaults.standard.set(ch.id, forKey: "lastLiveChannel")
            updateNowPlaying()
        } catch {
            if !Task.isCancelled { phase = .error(error.localizedDescription) }
        }
    }

    func togglePause() {
        if player.timeControlStatus == .paused {
            // Back to the live edge rather than where it paused.
            if let item = player.currentItem, let edge = item.seekableTimeRanges.last?.timeRangeValue.end {
                player.seek(to: edge)
            }
            player.play()
        } else {
            player.pause()
        }
    }

    func stop() {
        guard !stopped else { return }
        stopped = true
        tuneTask?.cancel()
        player.pause()
        player.replaceCurrentItem(with: nil)
        if let sid = sessionId { Task.detached { [api] in await api.stopSession(sid) } }
        let c = MPRemoteCommandCenter.shared()
        for cmd in [c.playCommand, c.pauseCommand, c.togglePlayPauseCommand, c.nextTrackCommand, c.previousTrackCommand] as [MPRemoteCommand] {
            cmd.removeTarget(nil)
        }
        remoteTargets = []
        MPNowPlayingInfoCenter.default().nowPlayingInfo = nil
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
    }

    private func setUpRemoteCommands() {
        guard remoteTargets.isEmpty else { return }
        let c = MPRemoteCommandCenter.shared()
        remoteTargets = [
            c.playCommand.addTarget { [weak self] _ in self?.player.play(); return .success },
            c.pauseCommand.addTarget { [weak self] _ in self?.player.pause(); return .success },
            c.togglePlayPauseCommand.addTarget { [weak self] _ in self?.togglePause(); return .success },
            c.nextTrackCommand.addTarget { [weak self] _ in self?.step(1); return .success },
            c.previousTrackCommand.addTarget { [weak self] _ in self?.step(-1); return .success },
        ]
    }

    private func updateNowPlaying() {
        var info: [String: Any] = [
            MPNowPlayingInfoPropertyIsLiveStream: true,
            MPMediaItemPropertyTitle: channel.now?.title ?? channel.name,
            MPMediaItemPropertyArtist: channel.name,
            MPNowPlayingInfoPropertyMediaType: MPNowPlayingInfoMediaType.video.rawValue,
        ]
        info[MPNowPlayingInfoPropertyPlaybackRate] = 1.0
        MPNowPlayingInfoCenter.default().nowPlayingInfo = info
    }
}

/// The full-screen live player.
struct LivePlayerView: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    let target: LiveTarget

    @State private var model: LivePlayerModel?
    @State private var controlsVisible = true
    @State private var hideTask: Task<Void, Never>?
    @State private var pip: AVPictureInPictureController?
    @State private var fill = false
    @State private var showList = false
    @State private var now = Date()

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            if let model {
                content(model)
            } else {
                ProgressView().controlSize(.large).tint(.white)
            }
        }
        .statusBarHidden(!controlsVisible)
        .persistentSystemOverlays(.hidden)
        .task {
            guard model == nil, let api = state.api else { return }
            var channels = state.liveChannels
            if channels.isEmpty { channels = (try? await api.liveChannels()) ?? [] }
            state.liveChannels = channels
            guard let ch = channels.first(where: { $0.id == target.channelId }) else { dismiss(); return }
            let list = target.group.map { g in channels.filter { $0.group == g } } ?? channels
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
        .sheet(isPresented: $showList) {
            if let model {
                ChannelPicker(model: model, now: now) { showList = false }
                    .presentationDetents([.medium, .large])
            }
        }
    }

    @ViewBuilder
    private func content(_ model: LivePlayerModel) -> some View {
        ZStack {
            VideoLayerView(player: model.player, fill: fill, pip: $pip)
                .ignoresSafeArea()
                .onTapGesture(count: 2) { withAnimation { fill.toggle() } }
                .onTapGesture { toggleControls() }
                .gesture(DragGesture(minimumDistance: 40).onEnded { v in
                    // Swipe up for the next channel, down for the one before.
                    guard abs(v.translation.height) > abs(v.translation.width) else { return }
                    model.step(v.translation.height < 0 ? 1 : -1)
                    poke()
                })
            switch model.phase {
            case .loading:
                VStack(spacing: 12) {
                    ProgressView().controlSize(.large).tint(.white)
                    Text("Tuning to \(model.channel.name)…").foregroundStyle(.white.opacity(0.7))
                }
            case .error(let msg):
                VStack(spacing: 14) {
                    Image(systemName: "exclamationmark.triangle").font(.system(size: 36)).foregroundStyle(Color.wrenEmber)
                    Text("Can't play \(model.channel.name)").font(.title3.bold())
                    Text(msg).font(.subheadline).foregroundStyle(.secondary).multilineTextAlignment(.center)
                    HStack(spacing: 12) {
                        Button("Try Again") { model.retry() }.buttonStyle(.glassCapsule)
                        if model.channels.count > 1 {
                            Button("Next Channel") { model.step(1) }.buttonStyle(.glassCapsule)
                        }
                    }
                }
                .padding(24)
                .frame(maxWidth: 420)
                .background(.black.opacity(0.6), in: RoundedRectangle(cornerRadius: 18))
                .padding()
            case .playing:
                if model.buffering { ProgressView().controlSize(.large).tint(.white) }
            }
            if controlsVisible || model.phase != .playing {
                controls(model).transition(.opacity)
            }
        }
        .foregroundStyle(.white)
    }

    private func controls(_ model: LivePlayerModel) -> some View {
        ZStack {
            Color.black.opacity(0.35).ignoresSafeArea().allowsHitTesting(false)
            VStack(spacing: 0) {
                HStack(alignment: .center, spacing: 12) {
                    Button { dismiss() } label: {
                        Image(systemName: "xmark").font(.system(size: 17, weight: .semibold)).frame(width: 40, height: 40)
                    }
                    .background(.ultraThinMaterial, in: Circle())
                    ChannelLogo(url: state.api?.liveLogoURL(model.channel.logo), name: model.channel.name)
                        .frame(width: 54, height: 34)
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 6) {
                            if let n = model.channel.number { Text(n).font(.subheadline.monospacedDigit()).foregroundStyle(.white.opacity(0.7)) }
                            Text(model.channel.name).font(.headline).lineLimit(1)
                        }
                        if let p = airing(model.channel) {
                            Text(p.title).font(.subheadline).foregroundStyle(.white.opacity(0.8)).lineLimit(1)
                        }
                    }
                    Spacer(minLength: 0)
                    if let pip, AVPictureInPictureController.isPictureInPictureSupported() {
                        Button { pip.startPictureInPicture() } label: { icon("pip.enter") }
                    }
                    AirPlayButton().frame(width: 40, height: 40)
                }
                .padding(.horizontal, 16)
                .padding(.top, 8)
                Spacer()
                HStack(spacing: 12) {
                    Text("LIVE").font(.caption2.bold()).padding(.horizontal, 6).padding(.vertical, 3)
                        .background(Color.wrenEmber, in: RoundedRectangle(cornerRadius: 4))
                    if let p = airing(model.channel) {
                        ProgressView(value: p.progress(at: now)).tint(.white)
                        Text(p.timeRange).font(.caption.monospacedDigit()).foregroundStyle(.white.opacity(0.75)).lineLimit(1)
                    } else {
                        Spacer()
                    }
                    Button { showList = true } label: { icon("list.bullet") }
                }
                .padding(.horizontal, 20)
                .padding(.bottom, 12)
            }
            HStack(spacing: 56) {
                Button { model.step(-1); poke() } label: { Image(systemName: "chevron.up").font(.system(size: 28, weight: .semibold)) }
                    .disabled(model.channels.count < 2)
                Button { model.togglePause(); poke() } label: {
                    Image(systemName: model.paused ? "play.fill" : "pause.fill").font(.system(size: 46)).frame(width: 60)
                }
                Button { model.step(1); poke() } label: { Image(systemName: "chevron.down").font(.system(size: 28, weight: .semibold)) }
                    .disabled(model.channels.count < 2)
            }
            .opacity(model.phase == .playing ? 1 : 0)
        }
    }

    private func icon(_ symbol: String) -> some View {
        Image(systemName: symbol).font(.system(size: 18, weight: .medium)).frame(width: 40, height: 40).contentShape(Rectangle())
    }

    private func airing(_ ch: LiveChannel) -> LiveProgram? {
        [ch.now, ch.next].compactMap { $0 }.first { $0.startDate <= now && now < $0.endDate }
    }

    private func poke() {
        withAnimation(.easeOut(duration: 0.2)) { controlsVisible = true }
        hideTask?.cancel()
        hideTask = Task {
            try? await Task.sleep(for: .seconds(4))
            guard !Task.isCancelled, model?.phase == .playing, model?.paused != true else { return }
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

/// The channel list over the picture, for flipping without leaving it.
private struct ChannelPicker: View {
    @Environment(AppState.self) private var state
    let model: LivePlayerModel
    let now: Date
    let close: () -> Void

    var body: some View {
        NavigationStack {
            ScrollViewReader { proxy in
                List(model.channels) { ch in
                    Button {
                        model.tune(ch)
                        close()
                    } label: {
                        HStack(spacing: 12) {
                            ChannelLogo(url: state.api?.liveLogoURL(ch.logo), name: ch.name).frame(width: 52, height: 32)
                            VStack(alignment: .leading, spacing: 2) {
                                Text([ch.number, ch.name].compactMap { $0 }.joined(separator: "  "))
                                    .font(.subheadline.weight(.semibold)).lineLimit(1)
                                if let p = ch.now, p.endDate > now {
                                    Text(p.title).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                                }
                            }
                            Spacer(minLength: 0)
                            if ch.id == model.channel.id {
                                Image(systemName: "speaker.wave.2.fill").foregroundStyle(Color.wrenEmber)
                            }
                        }
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .id(ch.id)
                }
                .listStyle(.plain)
                .onAppear { proxy.scrollTo(model.channel.id, anchor: .center) }
            }
            .navigationTitle("Channels")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) { Button("Done", action: close) }
            }
        }
        .preferredColorScheme(.dark)
    }
}
