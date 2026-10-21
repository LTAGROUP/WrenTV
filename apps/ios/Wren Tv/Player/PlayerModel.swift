import AVFoundation
import Combine
import MediaPlayer
import Observation
import UIKit
import VideoToolbox

extension ClientCaps {
    /// What this device can play natively. The server remuxes or converts the rest.
    @MainActor
    static func current() -> ClientCaps {
        var video = ["h264"]
        // The simulator decodes HEVC in software but reports no hardware decoder;
        // claim it anyway so the server remuxes as it would for a real iPhone.
        #if targetEnvironment(simulator)
        video.append("hevc")
        #else
        if VTIsHardwareDecodeSupported(kCMVideoCodecType_HEVC) { video.append("hevc") }
        #endif
        if VTIsHardwareDecodeSupported(kCMVideoCodecType_AV1) { video.append("av1") }
        #if os(tvOS)
        // The TV's HDR formats, as the Apple TV is set to output them.
        let hdr = AVPlayer.eligibleForHDRPlayback
        let dv = AVPlayer.availableHDRModes.contains(.dolbyVision)
        #else
        let screen = UIApplication.shared.connectedScenes.compactMap { ($0 as? UIWindowScene)?.screen }.first
        let hdr = (screen?.potentialEDRHeadroom ?? 1) > 1
        let dv = hdr
        #endif
        // FLAC/Opus are omitted on purpose: CoreMedia rejects them in fMP4 HLS, so the
        // server converts them to lossless ALAC instead.
        return ClientCaps(engines: ["avplayer"], nativeVideo: video,
                          nativeAudio: ["aac", "ac3", "eac3", "alac", "mp3"],
                          nativeContainers: ["mp4", "mov"], dolbyVision: dv, hdr: hdr, maxKbps: 0)
    }
}

@MainActor
@Observable
final class PlayerModel {
    enum Phase: Equatable {
        case loading
        case pending(String)
        case error(String)
        case playing
        case ended
    }

    /// Containers AVPlayer opens from a file on disk.
    static let playableFileTypes: Set<String> = ["mp4", "m4v", "mov"]

    let api: WrenTVAPI
    /// Signed in, so a download the device can't open can stream instead.
    private let online: Bool
    private(set) var itemId: String
    private(set) var title: String
    private(set) var subtitle: String?
    private var fromStart: Bool
    /// Plays a downloaded file instead of asking the server.
    private var localFile: URL?
    private var localPosition: Double?
    /// Saves the spot in a downloaded copy.
    @ObservationIgnored var onLocalProgress: ((String, Double, Double) -> Void)?

    var phase: Phase = .loading
    var methodLabel = ""
    var avPlayer: AVPlayer?

    var position = 0.0
    var duration = 0.0
    var paused = false
    var buffering = false

    var audioStreams: [MediaStream] = []
    var audioIndex: Int?
    /// Text tracks in the stream (the server's subtitles and any captions in the video).
    var legibleOptions: [AVMediaSelectionOption] = []
    var selectedLegible: AVMediaSelectionOption?
    var upNext: Item?
    /// The item's versions, and the one playing.
    var versions: [MediaVersion] = []
    var fileId: String?

    var chapters: [Chapter] = []
    var markers = Markers()
    /// What the skip button offers right now.
    var skip: Skip?
    /// Seconds left before the next episode starts, while the credits roll.
    var countdown: Int?
    /// Set when playback is done and the player should close.
    var closeRequested = false
    /// The episode after this one, looked up when playback starts.
    private(set) var nextEpisode: Item?

    enum Skip: Equatable { case intro, credits }

    /// The chapter playing now.
    var currentChapter: Chapter? { chapters.last(where: { $0.start <= position + 0.01 }) }

    /// The subtitle the viewer had on, kept across stream restarts.
    @ObservationIgnored private var keptSubtitle: String?
    @ObservationIgnored private var sessionId: String?
    @ObservationIgnored private var lastAutoRetry: Date?
    @ObservationIgnored private var timeObserver: Any?
    @ObservationIgnored private var loadTask: Task<Void, Never>?
    @ObservationIgnored private var progressTask: Task<Void, Never>?
    @ObservationIgnored private var subs: Set<AnyCancellable> = []
    @ObservationIgnored private var endObserver: NSObjectProtocol?
    @ObservationIgnored private var stopped = false
    @ObservationIgnored private var dismissed: Set<Skip> = []
    @ObservationIgnored private var countdownTask: Task<Void, Never>?
    @ObservationIgnored private var versionsTask: Task<Void, Never>?
    @ObservationIgnored private var remoteTargets: [Any] = []
    /// Paused by the viewer, as opposed to by the system (a screen recording
    /// starting, Control Center, a call), which shouldn't keep it paused.
    @ObservationIgnored private var userPaused = false
    @ObservationIgnored private var resumeObservers: [NSObjectProtocol] = []

    init(target: PlayTarget, api: WrenTVAPI, online: Bool) {
        self.api = api
        self.online = online
        itemId = target.itemId
        title = target.title
        subtitle = target.subtitle
        fromStart = target.fromStart
        fileId = target.fileId
        localFile = target.localFile.map { URL(fileURLWithPath: $0) }
        localPosition = target.localPosition
    }

    var isOffline: Bool { localFile != nil }

    // MARK: Lifecycle

    func start(at override: Double? = nil) {
        loadTask?.cancel()
        loadTask = Task { await run(startAt: override) }
    }

    private func run(startAt: Double?) async {
        phase = .loading
        try? AVAudioSession.sharedInstance().setActive(true)
        setUpRemoteCommands()
        if let file = localFile {
            if Self.playableFileTypes.contains(file.pathExtension.lowercased()) {
                methodLabel = "Downloaded"
                startAV(file, start: startAt ?? (fromStart ? 0 : (localPosition ?? 0)), subtitle: nil, direct: true)
                phase = .playing
                startProgressLoop()
                return
            }
            // AVPlayer can't open this container from disk; stream it while connected.
            guard online else {
                phase = .error("This download is a .\(file.pathExtension) file, which \(UIDevice.current.model) can't play offline. Connect to your server to stream it instead.")
                return
            }
            localFile = nil
            fromStart = fromStart || (localPosition ?? 0) < 1
        }
        var resp: PlayResponse
        while true {
            if Task.isCancelled || stopped { return }
            do {
                resp = try await api.play(itemId, PlayRequest(fileId: fileId, audioIndex: audioIndex, caps: .current()))
            } catch {
                phase = .error(error.localizedDescription)
                return
            }
            // A streamed title is still being found: keep the user here and retry.
            if resp.status == "pending" {
                phase = .pending(resp.message ?? "Finding a stream…")
                try? await Task.sleep(for: .seconds(3))
                continue
            }
            break
        }
        guard let path = resp.url else {
            phase = .error("The server didn't return a stream.")
            return
        }
        if stopped {
            if let sid = resp.sessionId { await api.stopSession(sid) }
            return
        }
        sessionId = resp.sessionId
        // Keep the same file when restarting for an audio change.
        fileId = resp.fileId ?? fileId
        versions = resp.versions ?? []
        watchVersions()
        audioStreams = resp.info?.audio ?? []
        if audioIndex == nil { audioIndex = resp.decision?.audioIndex }
        duration = resp.duration ?? 0
        chapters = resp.chapters ?? resp.info?.chapters ?? []
        markers = resp.markers ?? Markers()
        methodLabel = describe(resp)
        let start = startAt ?? (fromStart ? 0 : (resp.resumePosition ?? localPosition ?? 0))
        if resp.method != "direct" {
            // Wait for the server's first segments here: AVPlayer gives up on them after 6 seconds.
            phase = .pending("Starting the stream…")
            let ready = await StreamWarmup.prepare(api.absolute(path), start: start)
            if Task.isCancelled || stopped { return }
            guard ready else {
                phase = .error("The server couldn't read this stream from its source. Try again, or pick another version.")
                return
            }
        }
        startAV(api.absolute(path), start: start, subtitle: resp.subtitleIndex.flatMap { $0 >= 0 ? $0 : nil }, direct: resp.method == "direct")
        phase = .playing
        startProgressLoop()
        if nextEpisode == nil { Task { nextEpisode = await findNextEpisode() } }
    }

    /// A streamed title's lookup goes on after play starts: pick up the
    /// versions it finds while it's still looking.
    private func watchVersions() {
        versionsTask?.cancel()
        let id = itemId
        versionsTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(5))
            for _ in 0..<15 {
                guard let self, !Task.isCancelled, let r = try? await self.api.versions(id), id == self.itemId else { return }
                if let v = r.versions, !v.isEmpty { self.versions = v }
                if r.looking != true { return }
                try? await Task.sleep(for: .seconds(20))
            }
        }
    }

    /// Asks for the versions again, for when the picker opens.
    func refreshVersions() {
        let id = itemId
        Task { [weak self] in
            guard let self, let r = try? await self.api.versions(id), id == self.itemId, let v = r.versions, !v.isEmpty else { return }
            self.versions = v
        }
    }

    func stop() {
        guard !stopped else { return }
        stopped = true
        loadTask?.cancel()
        versionsTask?.cancel()
        let (pos, dur, sid, item) = (position, duration, sessionId, itemId)
        let api = self.api
        if isOffline { onLocalProgress?(item, pos, dur) }
        Task.detached {
            if pos > 5 { await api.reportProgress(item, position: pos, duration: dur) }
            if let sid { await api.stopSession(sid) }
        }
        teardown()
        tearDownRemoteCommands()
        MPNowPlayingInfoCenter.default().nowPlayingInfo = nil
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
    }

    private func teardown() {
        progressTask?.cancel()
        cancelCountdown()
        subs = []
        if let endObserver { NotificationCenter.default.removeObserver(endObserver) }
        endObserver = nil
        resumeObservers.forEach(NotificationCenter.default.removeObserver)
        resumeObservers = []
        if let avPlayer {
            avPlayer.pause()
            if let timeObserver { avPlayer.removeTimeObserver(timeObserver) }
            avPlayer.replaceCurrentItem(with: nil)
        }
        timeObserver = nil
        avPlayer = nil
        legibleOptions = []
        selectedLegible = nil
    }

    // MARK: AVPlayer

    private func startAV(_ url: URL, start: Double, subtitle: Int?, direct: Bool) {
        let item = AVPlayerItem(url: url)
        let player = AVPlayer(playerItem: item)
        player.appliesMediaSelectionCriteriaAutomatically = false
        player.allowsExternalPlayback = true
        if start > 1 {
            player.seek(to: CMTime(seconds: start, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .positiveInfinity)
        }
        avPlayer = player
        timeObserver = player.addPeriodicTimeObserver(forInterval: CMTime(value: 1, timescale: 4), queue: .main) { [weak self] t in
            MainActor.assumeIsolated { self?.tick(t.seconds) }
        }
        item.publisher(for: \.status).receive(on: DispatchQueue.main).sink { [weak self] status in
            if status == .failed {
                self?.failed(item.error?.localizedDescription ?? "Playback failed")
            }
        }.store(in: &subs)
        NotificationCenter.default.publisher(for: AVPlayerItem.failedToPlayToEndTimeNotification, object: item)
            .receive(on: DispatchQueue.main).sink { [weak self] n in
                let err = n.userInfo?[AVPlayerItemFailedToPlayToEndTimeErrorKey] as? Error
                self?.failed(err?.localizedDescription ?? "Playback failed")
            }.store(in: &subs)
        player.publisher(for: \.timeControlStatus).receive(on: DispatchQueue.main).sink { [weak self] s in
            self?.buffering = s == .waitingToPlayAtSpecifiedRate
            self?.paused = s == .paused
            self?.updateNowPlaying()
        }.store(in: &subs)
        endObserver = NotificationCenter.default.addObserver(forName: AVPlayerItem.didPlayToEndTimeNotification, object: item, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.finished() }
        }
        userPaused = false
        observeInterruptions()
        player.play()
        // The server lists its text tracks in the stream, next to any captions
        // in the video. They start off unless the viewer's language setting
        // picks one (or one was on before a stream restart).
        let kept = keptSubtitle
        keptSubtitle = nil
        Task { [weak self] in
            guard let group = try? await item.asset.loadMediaSelectionGroup(for: .legible) else { return }
            let pick: AVMediaSelectionOption?
            if let kept {
                pick = group.options.first { $0.displayName == kept }
            } else if let subtitle {
                if direct {
                    // A file played directly lists its subtitle tracks in order.
                    let subs = group.options.filter { $0.mediaType != .closedCaption }
                    pick = subtitle < subs.count ? subs[subtitle] : nil
                } else {
                    // Streams name track index 2 "English (3)".
                    pick = group.options.first { $0.displayName.hasSuffix("(\(subtitle + 1))") }
                }
            } else {
                pick = nil
            }
            guard let self, self.avPlayer?.currentItem === item else { return }
            item.select(pick, in: group)
            self.legibleOptions = group.options
            self.selectedLegible = pick
        }
    }

    private func tick(_ t: Double) {
        guard t.isFinite else { return }
        position = t
        if let d = avPlayer?.currentItem?.duration.seconds, d.isFinite, d > 0 { duration = d }
        updateSkip()
    }

    // MARK: Controls

    func togglePause() {
        guard let p = avPlayer else { return }
        p.rate == 0 ? play() : pause()
    }

    private func play() {
        userPaused = false
        avPlayer?.play()
    }

    private func pause() {
        userPaused = true
        avPlayer?.pause()
    }

    /// Starting a screen recording (and opening Control Center to do it)
    /// interrupts the audio session, and AVPlayer stays paused afterwards.
    /// Pick up again once the app is back, unless the viewer paused.
    private func observeInterruptions() {
        let nc = NotificationCenter.default
        resumeObservers = [
            // Pauses from the PiP window or AirPlay controls count as the viewer's.
            nc.addObserver(forName: AVPlayer.rateDidChangeNotification, object: avPlayer, queue: .main) { [weak self] n in
                guard let p = n.object as? AVPlayer else { return }
                let reason = n.userInfo?[AVPlayer.rateDidChangeReasonKey] as? AVPlayer.RateDidChangeReason
                MainActor.assumeIsolated {
                    if p.rate > 0 { self?.userPaused = false } else if reason == .setRateCalled, self?.stopped == false { self?.userPaused = true }
                }
            },
            nc.addObserver(forName: AVAudioSession.interruptionNotification, object: nil, queue: .main) { [weak self] n in
                guard let raw = n.userInfo?[AVAudioSessionInterruptionTypeKey] as? UInt,
                      AVAudioSession.InterruptionType(rawValue: raw) == .ended else { return }
                let opts = (n.userInfo?[AVAudioSessionInterruptionOptionKey] as? UInt).map(AVAudioSession.InterruptionOptions.init) ?? []
                guard opts.contains(.shouldResume) else { return }
                MainActor.assumeIsolated { self?.resumeIfSystemPaused() }
            },
            nc.addObserver(forName: UIApplication.didBecomeActiveNotification, object: nil, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.resumeIfSystemPaused() }
            },
            nc.addObserver(forName: UIScreen.capturedDidChangeNotification, object: nil, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.resumeIfSystemPaused() }
            },
        ]
    }

    private func resumeIfSystemPaused() {
        guard !userPaused, !stopped, phase == .playing, let p = avPlayer, p.rate == 0 else { return }
        try? AVAudioSession.sharedInstance().setActive(true)
        p.play()
    }

    func seek(by d: Double) { seek(to: max(0, position + d)) }

    func seek(to t: Double) {
        position = t
        avPlayer?.seek(to: CMTime(seconds: t, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero) { [weak self] _ in
            MainActor.assumeIsolated { self?.updateNowPlaying() }
        }
    }

    /// Switching audio needs a new stream from the server.
    func selectAudio(_ typeIndex: Int) {
        guard typeIndex != audioIndex else { return }
        audioIndex = typeIndex
        restartStream(at: position)
    }

    func selectSubtitle(_ option: AVMediaSelectionOption?) {
        guard let item = avPlayer?.currentItem else { return }
        Task {
            guard let group = try? await item.asset.loadMediaSelectionGroup(for: .legible) else { return }
            item.select(option, in: group)
            selectedLegible = option
        }
    }

    /// Plays another version of the item from the same spot.
    func selectVersion(_ id: String) {
        guard id != fileId else { return }
        fileId = id
        // Track numbers differ between files; let the server pick again.
        audioIndex = nil
        keptSubtitle = nil
        restartStream(at: position)
    }

    /// Starts over after a failure. The failed stream is closed on the server
    /// first, or it keeps reading the source (and holding a stream slot).
    func retry() {
        let old = sessionId
        sessionId = nil
        teardown()
        if let old { Task { await api.stopSession(old) } }
        start(at: position > 0 ? position : nil)
    }

    /// A stream that fails partway is tried again from the same spot once:
    /// the server drops a post that lost articles and hands out another.
    private func failed(_ message: String) {
        guard phase == .playing else { return }
        if !isOffline, lastAutoRetry.map({ Date().timeIntervalSince($0) > 120 }) ?? true {
            lastAutoRetry = Date()
            // The server owner may have stopped it: say so rather than starting again.
            Task {
                if let msg = await api.reportProgress(itemId, position: position, duration: duration, paused: true) {
                    stoppedByServer(msg)
                } else {
                    retry()
                }
            }
            return
        }
        phase = .error(message)
    }

    /// The server owner stopped this stream from the dashboard.
    private func stoppedByServer(_ message: String) {
        guard phase == .playing else { return }
        sessionId = nil
        teardown()
        phase = .error(message)
    }

    private func restartStream(at pos: Double) {
        keptSubtitle = selectedLegible?.displayName
        let old = sessionId
        sessionId = nil
        teardown()
        if let old { Task { await api.stopSession(old) } }
        start(at: pos)
    }

    // MARK: Lock screen & Control Center

    private func setUpRemoteCommands() {
        guard remoteTargets.isEmpty else { return }
        let c = MPRemoteCommandCenter.shared()
        remoteTargets = [
            c.playCommand.addTarget { [weak self] _ in self?.play(); return .success },
            c.pauseCommand.addTarget { [weak self] _ in self?.pause(); return .success },
            c.togglePlayPauseCommand.addTarget { [weak self] _ in self?.togglePause(); return .success },
            c.skipForwardCommand.addTarget { [weak self] _ in self?.seek(by: 10); return .success },
            c.skipBackwardCommand.addTarget { [weak self] _ in self?.seek(by: -10); return .success },
            c.changePlaybackPositionCommand.addTarget { [weak self] e in
                guard let e = e as? MPChangePlaybackPositionCommandEvent else { return .commandFailed }
                self?.seek(to: e.positionTime)
                return .success
            },
        ]
        c.skipForwardCommand.preferredIntervals = [10]
        c.skipBackwardCommand.preferredIntervals = [10]
    }

    private func tearDownRemoteCommands() {
        let c = MPRemoteCommandCenter.shared()
        for cmd in [c.playCommand, c.pauseCommand, c.togglePlayPauseCommand, c.skipForwardCommand,
                    c.skipBackwardCommand, c.changePlaybackPositionCommand] as [MPRemoteCommand] {
            cmd.removeTarget(nil)
        }
        remoteTargets = []
    }

    private func updateNowPlaying() {
        guard avPlayer != nil else { return }
        var info: [String: Any] = [
            MPMediaItemPropertyTitle: subtitle ?? title,
            MPNowPlayingInfoPropertyElapsedPlaybackTime: position,
            MPNowPlayingInfoPropertyPlaybackRate: paused ? 0.0 : 1.0,
            MPNowPlayingInfoPropertyMediaType: MPNowPlayingInfoMediaType.video.rawValue,
        ]
        if subtitle != nil { info[MPMediaItemPropertyAlbumTitle] = title }
        if duration > 0 { info[MPMediaItemPropertyPlaybackDuration] = duration }
        MPNowPlayingInfoCenter.default().nowPlayingInfo = info
    }

    // MARK: Chapters & skipping

    func seek(toChapter c: Chapter) { seek(to: c.start) }

    private func updateSkip() {
        let t = position
        // Seeking back before a marker offers it again.
        if let i = markers.intro, t < i.start { dismissed.remove(.intro) }
        if let c = markers.credits, t < c.start - 1 { dismissed.remove(.credits); cancelCountdown() }

        var next: Skip?
        if let i = markers.intro, t >= i.start, t < i.end - 1, !dismissed.contains(.intro) {
            next = .intro
        } else if let c = markers.credits, t >= c.start, t < c.end - 1, !dismissed.contains(.credits) {
            next = .credits
        }
        if next == .credits, nextEpisode != nil, countdown == nil, countdownTask == nil, !paused {
            startCountdown()
        }
        if skip != next { skip = next }
    }

    func skipIntro() {
        guard let i = markers.intro else { return }
        dismissed.insert(.intro)
        skip = nil
        seek(to: i.end)
    }

    /// Skips the credits: the next episode when there is one, else the end.
    func skipCredits() {
        dismissed.insert(.credits)
        skip = nil
        cancelCountdown()
        if nextEpisode != nil {
            upNext = nextEpisode
            playNext()
        } else {
            finished()
            closeRequested = true
        }
    }

    /// Keeps watching the credits.
    func dismissCredits() {
        dismissed.insert(.credits)
        skip = nil
        cancelCountdown()
    }

    private func startCountdown() {
        countdown = 10
        countdownTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(1))
                guard let self, !Task.isCancelled, let n = self.countdown else { return }
                if self.paused { continue }
                if n <= 1 {
                    self.countdownTask = nil
                    self.skipCredits()
                    return
                }
                self.countdown = n - 1
            }
        }
    }

    private func cancelCountdown() {
        countdownTask?.cancel()
        countdownTask = nil
        countdown = nil
    }

    /// The episode after this one: the next in its season, else the first of
    /// the next season.
    private func findNextEpisode() async -> Item? {
        guard let d = try? await api.item(itemId), d.item.kind == "episode" else { return nil }
        let ep = d.item
        if let pid = ep.parentId, let season = try? await api.item(pid),
           let n = (season.children ?? []).filter({ $0.episodeNumber > ep.episodeNumber }).min(by: { $0.episodeNumber < $1.episodeNumber }) {
            return n
        }
        guard let sid = ep.showId, let show = try? await api.item(sid),
              let s = (show.children ?? []).filter({ $0.seasonNumber > ep.seasonNumber }).min(by: { $0.seasonNumber < $1.seasonNumber }),
              let sd = try? await api.item(s.id) else { return nil }
        return (sd.children ?? []).min(by: { $0.episodeNumber < $1.episodeNumber })
    }

    // MARK: Progress & up next

    private func startProgressLoop() {
        progressTask?.cancel()
        progressTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(10))
                guard let self, !Task.isCancelled else { return }
                guard self.position > 5 else { continue }
                if self.isOffline {
                    if !self.paused { self.onLocalProgress?(self.itemId, self.position, self.duration) }
                    continue
                }
                // Reported while paused too, so the dashboard shows it paused.
                if let msg = await self.api.reportProgress(self.itemId, position: self.position, duration: self.duration, paused: self.paused) {
                    self.stoppedByServer(msg)
                }
            }
        }
    }

    private func finished() {
        guard phase == .playing else { return }
        phase = .ended
        let (api, id, dur) = (api, itemId, duration)
        if let n = nextEpisode { upNext = n }
        Task {
            await api.reportProgress(id, position: dur, duration: dur)
            if upNext != nil { return }
            if let d = try? await api.item(id), d.item.kind == "episode", let showId = d.item.showId,
               let show = try? await api.item(showId), let next = show.nextEpisode, next.id != id {
                upNext = next
            }
        }
    }

    func playNext() {
        guard let next = upNext else { return }
        // Moving on counts as finishing this one.
        let (api, id, dur) = (api, itemId, duration)
        if phase == .playing { Task { await api.reportProgress(id, position: dur, duration: dur) } }
        localFile = nil // the next episode streams from the server
        localPosition = nil
        let old = sessionId
        sessionId = nil
        teardown()
        if let old { Task { await api.stopSession(old) } }
        itemId = next.id
        subtitle = "\(next.episodeLabel) — \(next.title)"
        upNext = nil
        nextEpisode = nil
        dismissed = []
        skip = nil
        chapters = []
        markers = Markers()
        fromStart = false
        fileId = nil
        versions = []
        audioIndex = nil
        keptSubtitle = nil
        position = 0
        start()
    }

    private func describe(_ r: PlayResponse) -> String {
        var parts: [String] = []
        switch r.method {
        case "direct": parts.append("Direct Play")
        case "remux": parts.append("Direct Stream")
        case "transcode": parts.append("Transcode")
        default: break
        }
        if let v = r.info?.video?.first {
            if let h = v.hdr, !h.isEmpty { parts.append(v.dvProfile.map { "\(h) P\($0)" } ?? h) }
        }
        if let a = r.info?.audio?.first(where: { $0.typeIndex == r.decision?.audioIndex }) {
            parts.append(r.decision?.audioCodec == "alac" && a.codec != "alac" ? "\(a.codecLabel) → ALAC" : a.codecLabel)
        }
        return parts.joined(separator: " · ")
    }
}
