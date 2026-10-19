import AVFoundation
import AppKit
import Combine
import Observation
import VideoToolbox

extension ClientCaps {
    /// What this Mac can play natively. Anything else goes to libmpv or the server.
    static func current() -> ClientCaps {
        var video = ["h264"]
        if VTIsHardwareDecodeSupported(kCMVideoCodecType_HEVC) { video.append("hevc") }
        if VTIsHardwareDecodeSupported(kCMVideoCodecType_AV1) { video.append("av1") }
        let edr = NSScreen.screens.map(\.maximumPotentialExtendedDynamicRangeColorComponentValue).max() ?? 1
        let hdr = edr > 1.0
        // FLAC/Opus are omitted on purpose: CoreMedia rejects them in fMP4 HLS, so the
        // server converts them to lossless ALAC instead.
        return ClientCaps(engines: ["avplayer", "mpv"], nativeVideo: video,
                          nativeAudio: ["aac", "ac3", "eac3", "alac", "mp3"],
                          nativeContainers: ["mp4", "mov"], dolbyVision: hdr, hdr: hdr, maxKbps: 0)
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

    let api: WrenTVAPI
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
    var engine = "avplayer"
    var methodLabel = ""
    var avPlayer: AVPlayer?
    var mpv: MPVController?

    var position = 0.0
    var duration = 0.0
    var paused = false
    var buffering = false
    var volume: Float = 1
    var muted = false

    var audioStreams: [MediaStream] = []
    var audioIndex: Int?
    var subtitles: [SubtitleTrack] = []
    var mpvTracks: [MPVTrack] = []
    /// AVPlayer's subtitle choices: the text tracks the server lists in the
    /// stream, and any captions in the video.
    var avSubtitles: [AVMediaSelectionOption] = []
    var avSubtitle: AVMediaSelectionOption?
    var upNext: Item?
    /// The item's versions, and the one playing.
    var versions: [MediaVersion] = []
    var fileId: String?
    /// True while a player menu is open, so the controls stay up.
    var menuOpen = false

    var chapters: [Chapter] = []
    var markers = Markers()
    /// What the skip button offers right now.
    var skip: Skip?
    /// Seconds left before the next episode starts, while the credits roll.
    var countdown: Int?
    /// Set when playback is done and the window should close.
    var closeRequested = false
    /// The episode after this one, looked up when playback starts.
    private(set) var nextEpisode: Item?

    enum Skip: Equatable { case intro, credits }

    /// The chapter playing now.
    var currentChapter: Chapter? { chapters.last(where: { $0.start <= position + 0.01 }) }

    /// The AVKit subtitle the viewer had on, kept across stream restarts.
    @ObservationIgnored private var keptSubtitle: String?
    @ObservationIgnored private var sessionId: String?
    @ObservationIgnored private var lastAutoRetry: Date?
    @ObservationIgnored private var timeObserver: Any?
    @ObservationIgnored private var loadTask: Task<Void, Never>?
    @ObservationIgnored private var progressTask: Task<Void, Never>?
    @ObservationIgnored private var statusSub: AnyCancellable?
    @ObservationIgnored private var endObserver: NSObjectProtocol?
    @ObservationIgnored private var avSubtitleGroup: AVMediaSelectionGroup?
    @ObservationIgnored private var mpvLayer: MPVLayer?
    @ObservationIgnored private var pendingMPVLoad: (URL, Double)?
    @ObservationIgnored private var stopped = false
    @ObservationIgnored private var dismissed: Set<Skip> = []
    @ObservationIgnored private var countdownTask: Task<Void, Never>?
    @ObservationIgnored private var versionsTask: Task<Void, Never>?

    init(target: PlayTarget, api: WrenTVAPI) {
        self.api = api
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
        if let localFile {
            // mpv plays any container, so downloads never need the server.
            engine = "mpv"
            methodLabel = "Downloaded"
            startMPV(localFile, start: startAt ?? (fromStart ? 0 : (localPosition ?? 0)), audioTrack: nil, subtitleTrack: nil)
            phase = .playing
            startProgressLoop()
            return
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
        subtitles = resp.subtitles ?? []
        duration = resp.duration ?? 0
        chapters = resp.chapters ?? resp.info?.chapters ?? []
        markers = resp.markers ?? Markers()
        engine = resp.engine ?? "avplayer"
        methodLabel = describe(resp)
        let start = startAt ?? (fromStart ? 0 : (resp.resumePosition ?? 0))
        let url = api.absolute(path)

        if engine == "mpv" {
            // mpv picks its own audio track from the file; use the one the server
            // chose from the user's language preference (mpv counts from 1).
            let aid = resp.method == "direct" ? (audioIndex ?? resp.decision?.audioIndex).map { $0 + 1 } : nil
            // Likewise subtitles: off unless the viewer's language setting picks a track.
            let sid = resp.method == "direct" ? resp.subtitleIndex.flatMap { $0 >= 0 ? $0 + 1 : nil } : nil
            startMPV(url, start: start, audioTrack: aid, subtitleTrack: sid)
        } else {
            startAV(url, start: start, subtitle: resp.subtitleIndex.flatMap { $0 >= 0 ? $0 : nil }, direct: resp.method == "direct")
        }
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
        teardownEngines()
    }

    private func teardownEngines() {
        progressTask?.cancel()
        cancelCountdown()
        statusSub = nil
        if let endObserver { NotificationCenter.default.removeObserver(endObserver) }
        endObserver = nil
        if let avPlayer {
            avPlayer.pause()
            if let timeObserver { avPlayer.removeTimeObserver(timeObserver) }
            avPlayer.replaceCurrentItem(with: nil)
        }
        timeObserver = nil
        avPlayer = nil
        avSubtitles = []
        avSubtitle = nil
        avSubtitleGroup = nil
        mpvLayer?.teardown()
        mpvLayer = nil
        mpv?.destroy()
        mpv = nil
    }

    // MARK: AVPlayer

    private func startAV(_ url: URL, start: Double, subtitle: Int?, direct: Bool) {
        let item = AVPlayerItem(url: url)
        let player = AVPlayer(playerItem: item)
        player.appliesMediaSelectionCriteriaAutomatically = false
        if start > 1 {
            player.seek(to: CMTime(seconds: start, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .positiveInfinity)
        }
        avPlayer = player
        applyVolume()
        timeObserver = player.addPeriodicTimeObserver(forInterval: CMTime(value: 1, timescale: 4), queue: .main) { [weak self] t in
            MainActor.assumeIsolated { self?.tick(t.seconds) }
        }
        statusSub = item.publisher(for: \.status).receive(on: DispatchQueue.main).sink { [weak self] status in
            if status == .failed {
                self?.failed(item.error?.localizedDescription ?? "Playback failed")
            }
        }
        endObserver = NotificationCenter.default.addObserver(forName: AVPlayerItem.didPlayToEndTimeNotification, object: item, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.finished() }
        }
        player.play()
        // The server lists its text subtitle tracks in the stream, next to any
        // captions in the video. They start off
        // unless the viewer's language setting picks one (or one was on
        // before a stream restart).
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
            self.avSubtitleGroup = group
            self.avSubtitles = group.options
            self.avSubtitle = pick
        }
    }

    private func tick(_ t: Double) {
        guard t.isFinite else { return }
        position = t
        if let d = avPlayer?.currentItem?.duration.seconds, d.isFinite, d > 0 { duration = d }
        paused = (avPlayer?.rate ?? 0) == 0
        buffering = avPlayer?.timeControlStatus == .waitingToPlayAtSpecifiedRate
        updateSkip()
    }

    // MARK: mpv

    private func startMPV(_ url: URL, start: Double, audioTrack: Int?, subtitleTrack: Int?) {
        let c = MPVController()
        c.onTime = { [weak self] t, d in
            self?.position = t
            if d > 0 { self?.duration = d }
            self?.updateSkip()
        }
        c.onPause = { [weak self] in self?.paused = $0 }
        c.onBuffering = { [weak self] in self?.buffering = $0 }
        c.onTracks = { [weak self] in self?.mpvTracks = $0 }
        c.onEnd = { [weak self] in
            guard let self else { return }
            // A stream that breaks partway ends early instead of failing.
            if self.duration > 0, self.position < self.duration - 120, !self.isOffline {
                self.failed("The stream ended early.")
            } else {
                self.finished()
            }
        }
        c.onError = { [weak self] in self?.failed($0) }
        if let audioTrack { c.set("aid", String(audioTrack)) }
        c.set("sid", subtitleTrack.map(String.init) ?? "no")
        mpv = c
        applyVolume()
        pendingMPVLoad = (url, start)
    }

    /// Called by the video view once its layer exists.
    func attach(_ layer: MPVLayer) {
        mpvLayer = layer
        layer.onReady = { [weak self] in
            guard let self, let (url, start) = self.pendingMPVLoad else { return }
            self.pendingMPVLoad = nil
            self.mpv?.load(url, start: start)
        }
    }

    // MARK: Controls

    func togglePause() {
        if let mpv { mpv.togglePause(); return }
        guard let p = avPlayer else { return }
        p.rate == 0 ? p.play() : p.pause()
    }

    func seek(by d: Double) { seek(to: min(max(0, position + d), max(duration, 0))) }

    func setVolume(_ v: Float) {
        volume = min(max(v, 0), 1)
        muted = false
        applyVolume()
    }

    func toggleMute() {
        muted.toggle()
        applyVolume()
    }

    private func applyVolume() {
        avPlayer?.volume = volume
        avPlayer?.isMuted = muted
        mpv?.set("volume", String(Int(volume * 100)))
        mpv?.set("mute", muted ? "yes" : "no")
    }

    func seek(to t: Double) {
        if let mpv { mpv.seek(to: t); return }
        avPlayer?.seek(to: CMTime(seconds: t, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero)
    }

    /// Switching audio on the native path needs a new stream from the server.
    func selectAudio(_ typeIndex: Int) {
        guard typeIndex != audioIndex else { return }
        audioIndex = typeIndex
        restartStream(at: position)
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

    func selectAVSubtitle(_ o: AVMediaSelectionOption?) {
        guard let item = avPlayer?.currentItem, let avSubtitleGroup else { return }
        item.select(o, in: avSubtitleGroup)
        avSubtitle = o
    }

    func selectMPVTrack(_ t: MPVTrack?) {
        if let t { mpv?.selectTrack(t) } else { mpv?.disableSubtitles() }
        // Refresh selection state.
        mpvTracks = mpvTracks.map { var x = $0; if x.type == (t?.type ?? "sub") { x.selected = x.id == t?.id }; return x }
    }

    /// Starts over after a failure. The failed stream is closed on the server
    /// first, or it keeps reading the source (and holding a stream slot).
    func retry() {
        let old = sessionId
        sessionId = nil
        teardownEngines()
        if let old { Task { await api.stopSession(old) } }
        start(at: position > 0 ? position : nil)
    }

    /// A stream that fails partway is tried again from the same spot once:
    /// the server drops a post that lost articles and hands out another.
    private func failed(_ message: String) {
        guard phase == .playing else { return }
        if !isOffline, lastAutoRetry.map({ Date().timeIntervalSince($0) > 120 }) ?? true {
            lastAutoRetry = Date()
            retry()
            return
        }
        phase = .error(message)
    }

    private func restartStream(at pos: Double) {
        if avPlayer != nil { keptSubtitle = avSubtitle?.displayName }
        let old = sessionId
        sessionId = nil
        teardownEngines()
        if let old { Task { await api.stopSession(old) } }
        start(at: pos)
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
                if self.position > 5, !self.paused {
                    if self.isOffline { self.onLocalProgress?(self.itemId, self.position, self.duration) }
                    await self.api.reportProgress(self.itemId, position: self.position, duration: self.duration)
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
        let old = sessionId
        sessionId = nil
        teardownEngines()
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
        case "direct": parts.append(r.engine == "mpv" ? "Direct Play (mpv)" : "Direct Play")
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
