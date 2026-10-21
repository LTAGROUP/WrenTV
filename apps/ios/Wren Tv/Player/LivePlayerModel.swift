import AVFoundation
import Combine
import MediaPlayer
import Observation

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
                phase = .error("This channel sends a stream this device can't play, and the server can't convert it. Update the server to watch it here.")
                return
            }
            let url = api.absolute(r.url)
            let item = AVPlayerItem(url: url)
            item.preferredForwardBufferDuration = 6
            item.publisher(for: \.status).receive(on: DispatchQueue.main).sink { [weak self] s in
                if s == .failed { self?.ended(url, fallback: item.error?.localizedDescription ?? "The channel didn't play.") }
            }.store(in: &itemSubs)
            NotificationCenter.default.publisher(for: AVPlayerItem.didPlayToEndTimeNotification, object: item)
                .merge(with: NotificationCenter.default.publisher(for: AVPlayerItem.failedToPlayToEndTimeNotification, object: item))
                .receive(on: DispatchQueue.main)
                .sink { [weak self] _ in self?.ended(url, fallback: "The channel stopped sending video.") }
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
