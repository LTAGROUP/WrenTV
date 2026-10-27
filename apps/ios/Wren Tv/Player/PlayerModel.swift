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

/// Seconds before an unmarked episode ends that the next-episode countdown starts.
private let upNextLead = 30.0

/// A subtitle file drawn over the video by the app, for the subtitle-site
/// downloads: AVPlayer can't add a track to a stream it's already playing.
struct SideSubtitle: Equatable {
    struct Cue: Equatable { var start: Double; var end: Double; var text: String }
    var track: SubtitleTrack
    var cues: [Cue]

    /// Reads WebVTT, which is what the server hands out.
    static func parse(_ vtt: String) -> [Cue] {
        var cues: [Cue] = []
        for block in vtt.replacingOccurrences(of: "\r\n", with: "\n").components(separatedBy: "\n\n") {
            let lines = block.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
            guard let i = lines.firstIndex(where: { $0.contains("-->") }) else { continue }
            let parts = lines[i].components(separatedBy: "-->")
            guard parts.count == 2, let a = clock(parts[0]), let b = clock(parts[1].split(separator: " ").first.map(String.init) ?? "") else { continue }
            let text = lines[(i + 1)...].joined(separator: "\n")
                .replacingOccurrences(of: "<[^>]+>", with: "", options: .regularExpression)
                .replacingOccurrences(of: "&amp;", with: "&").replacingOccurrences(of: "&lt;", with: "<").replacingOccurrences(of: "&gt;", with: ">")
                .trimmingCharacters(in: .whitespacesAndNewlines)
            if !text.isEmpty { cues.append(Cue(start: a, end: b, text: text)) }
        }
        return cues.sorted { $0.start < $1.start }
    }

    private static func clock(_ s: String) -> Double? {
        let f = s.trimmingCharacters(in: .whitespaces).replacingOccurrences(of: ",", with: ".").split(separator: ":").compactMap { Double($0) }
        guard !f.isEmpty else { return nil }
        return f.reduce(0) { $0 * 60 + $1 }
    }

    func text(at t: Double) -> String? {
        // Cues are few enough that a scan from the nearest start is plenty.
        var lo = 0, hi = cues.count
        while lo < hi { let m = (lo + hi) / 2; if cues[m].start <= t { lo = m + 1 } else { hi = m } }
        let on = cues[max(0, lo - 8)..<lo].filter { $0.end > t }.map(\.text)
        return on.isEmpty ? nil : on.joined(separator: "\n")
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
    /// The playing episode, once looked up, for the episode list.
    private(set) var episode: Item?
    /// The server can search subtitle sites for this title.
    private(set) var onlineSubtitles = false
    /// Subtitles downloaded from subtitle sites, which the app draws itself.
    private(set) var sideTracks: [SubtitleTrack] = []
    private(set) var sideSubtitle: SideSubtitle?
    /// The side subtitle's line showing now.
    private(set) var sideCue: String?
    /// The last play response, for the stats overlay.
    private(set) var response: PlayResponse?

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
    @ObservationIgnored private var cueObserver: Any?
    /// The next play is a retry, and the version that broke partway, if one did.
    @ObservationIgnored private var freshLink = false
    @ObservationIgnored private var failedFile: String?

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
        // Only the first ask says it's a retry.
        var retry = freshLink ? true : nil
        var failed = failedFile
        freshLink = false
        failedFile = nil
        while true {
            if Task.isCancelled || stopped { return }
            do {
                resp = try await api.play(itemId, PlayRequest(fileId: fileId, audioIndex: audioIndex, caps: .current(), retry: retry, failedFileId: failed))
                retry = nil
                failed = nil
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
        response = resp
        onlineSubtitles = resp.onlineSubtitles ?? false
        // Subtitle-site downloads aren't in the stream; the app draws them.
        sideTracks = (resp.subtitles ?? []).filter { $0.index >= 10000 && $0.url != nil }
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
        if let cueObserver { avPlayer?.removeTimeObserver(cueObserver) }
        cueObserver = nil
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
        if sideSubtitle != nil { watchCues() }
        player.play()
        // The server lists its text tracks in the stream, next to any captions
        // in the video. They start off unless the viewer's language setting
        // picks one (or one was on before a stream restart).
        let kept = sideSubtitle == nil ? keptSubtitle : nil
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
                    // Streams name track index 2 "English (3)"; AVFoundation may add
                    // the language after it ("English (3) - English").
                    let tag = "(\(subtitle + 1))"
                    pick = group.options.first { $0.displayName.hasSuffix(tag) || $0.displayName.contains(tag + " - ") }
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
        clearSideSubtitle()
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
        freshLink = true
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
        if !isOffline { failedFile = fileId }
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
        // An episode with no credits marker counts down over its last
        // seconds instead, so it can still roll on to the next.
        let credits = markers.credits ?? (nextEpisode != nil && duration >= 300 ? TimeRange(start: duration - upNextLead, end: duration + 1) : nil)
        // Seeking back before a marker offers it again.
        if let i = markers.intro, t < i.start { dismissed.remove(.intro) }
        if let c = credits, t < c.start - 1 { dismissed.remove(.credits); cancelCountdown() }

        var next: Skip?
        if let i = markers.intro, t >= i.start, t < i.end - 1, !dismissed.contains(.intro) {
            next = .intro
        } else if let c = credits, t >= c.start, t < c.end - 1, !dismissed.contains(.credits) {
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
        if ep.id == itemId { episode = ep }
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
        switchTo(next)
    }

    /// Plays another episode picked from the episode list.
    func playEpisode(_ ep: Item) {
        guard ep.id != itemId else { return }
        let (api, id, pos, dur) = (api, itemId, position, duration)
        if phase == .playing, pos > 5 { Task { await api.reportProgress(id, position: pos, duration: dur) } }
        switchTo(ep)
    }

    private func switchTo(_ next: Item) {
        freshLink = false
        failedFile = nil
        episode = nil
        sideTracks = []
        sideSubtitle = nil
        sideCue = nil
        cancelCountdown()
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

    // MARK: Episodes

    /// A season's episodes, or a show's seasons.
    func children(_ id: String) async -> [Item] {
        (try? await api.item(id).children) ?? []
    }

    // MARK: Subtitle sites

    /// Searches the subtitle sites for this title; throws when the search fails.
    func searchOnlineSubtitles(languages: String? = nil) async throws -> [OnlineSubtitle] {
        try await api.searchOnlineSubtitles(itemId, languages: languages).results ?? []
    }

    /// Downloads a subtitle the search found and shows it.
    func addOnlineSubtitle(_ sub: OnlineSubtitle) async throws {
        let track = try await api.fetchOnlineSubtitle(itemId, sub, sessionId: sessionId)
        sideTracks.removeAll { $0.index == track.index }
        sideTracks.append(track)
        try await showSideSubtitle(track)
    }

    /// Turns on a subtitle-site download, in place of the stream's subtitles.
    func showSideSubtitle(_ track: SubtitleTrack) async throws {
        guard let url = track.url else { return }
        let (data, _) = try await api.send("GET", url)
        let cues = SideSubtitle.parse(String(decoding: data, as: UTF8.self))
        guard !cues.isEmpty else { throw WrenTVError(message: "That subtitle file is empty.") }
        if let item = avPlayer?.currentItem, let group = try? await item.asset.loadMediaSelectionGroup(for: .legible) {
            item.select(nil, in: group)
            selectedLegible = nil
        }
        sideSubtitle = SideSubtitle(track: track, cues: cues)
        watchCues()
    }

    func clearSideSubtitle() {
        sideSubtitle = nil
        sideCue = nil
        if let cueObserver { avPlayer?.removeTimeObserver(cueObserver) }
        cueObserver = nil
    }

    private func watchCues() {
        guard let player = avPlayer, cueObserver == nil else { return }
        cueObserver = player.addPeriodicTimeObserver(forInterval: CMTime(value: 1, timescale: 10), queue: .main) { [weak self] t in
            MainActor.assumeIsolated {
                guard let self, let side = self.sideSubtitle else { return }
                let line = side.text(at: t.seconds)
                if line != self.sideCue { self.sideCue = line }
            }
        }
    }

    // MARK: Stats

    /// What the stats overlay shows: how the title is playing right now.
    func stats() -> [(String, String)] {
        var rows: [(String, String)] = []
        let r = response
        rows.append(("Playback", [methodLabel, r?.engine ?? ""].filter { !$0.isEmpty }.joined(separator: " · ").nonEmpty ?? (isOffline ? "Downloaded" : "—")))
        if let v = versions.first(where: { $0.id == fileId }), !v.label.isEmpty { rows.append(("Version", v.label)) }
        if let info = r?.info {
            if !info.container.isEmpty { rows.append(("Container", info.container)) }
            if let b = info.bitrate, b > 0 { rows.append(("Source bitrate", Self.mbps(Double(b)))) }
            if let v = info.video?.first {
                let size = v.width.map { "\($0)×\(v.height ?? 0)" }
                rows.append(("Video", [v.codec, size, v.hdr].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")))
            }
            if let a = info.audio?.first(where: { $0.typeIndex == (audioIndex ?? r?.decision?.audioIndex) }) ?? info.audio?.first {
                var s = a.codecLabel
                if let c = r?.decision?.audioCodec, !c.isEmpty, c != a.codec { s += " → " + c.uppercased() }
                rows.append(("Audio", s))
            }
        }
        if let item = avPlayer?.currentItem {
            let size = item.presentationSize
            if size.width > 0 { rows.append(("Showing", "\(Int(size.width))×\(Int(size.height))")) }
            let now = item.currentTime().seconds
            let ahead = item.loadedTimeRanges.map(\.timeRangeValue).first { $0.start.seconds <= now + 0.5 && $0.end.seconds > now }
                .map { $0.end.seconds - now } ?? 0
            rows.append(("Buffer ahead", String(format: "%.1f s", ahead)))
            if let e = item.accessLog()?.events.last {
                if e.indicatedBitrate > 0 { rows.append(("Stream bitrate", Self.mbps(e.indicatedBitrate))) }
                if e.observedBitrate > 0 { rows.append(("Connection", Self.mbps(e.observedBitrate))) }
                if e.numberOfDroppedVideoFrames >= 0 { rows.append(("Dropped frames", "\(e.numberOfDroppedVideoFrames)")) }
                if e.numberOfStalls > 0 { rows.append(("Stalls", "\(e.numberOfStalls)")) }
            }
        }
        rows.append(("Skip markers", markers.source?.nonEmpty ?? "none"))
        if let why = r?.decision?.reasons, !why.isEmpty { rows.append(("Why", why.joined(separator: "; "))) }
        return rows
    }

    private static func mbps(_ bps: Double) -> String { String(format: "%.1f Mbps", bps / 1_000_000) }

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

private extension String {
    var nonEmpty: String? { isEmpty ? nil : self }
}
