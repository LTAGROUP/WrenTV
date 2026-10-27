@file:OptIn(UnstableApi::class)

package com.wrentv.tv.player

import android.content.Context
import android.net.Uri
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableDoubleStateOf
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.media3.common.C
import androidx.media3.common.Format
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.TrackSelectionOverride
import androidx.media3.common.Tracks
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.DefaultLoadControl
import androidx.media3.exoplayer.DefaultRenderersFactory
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.DecoderReuseEvaluation
import androidx.media3.exoplayer.analytics.AnalyticsListener
import androidx.media3.exoplayer.util.EventLogger
import com.wrentv.tv.data.Chapter
import com.wrentv.tv.data.DownloadManager
import com.wrentv.tv.data.Item
import com.wrentv.tv.data.Markers
import com.wrentv.tv.data.MediaVersion
import com.wrentv.tv.data.OnlineSubtitle
import com.wrentv.tv.data.PlayRequest
import com.wrentv.tv.data.PlayResponse
import com.wrentv.tv.data.SeekThumbs
import com.wrentv.tv.data.SubtitleTrack
import com.wrentv.tv.data.TimeRange
import com.wrentv.tv.data.WrenTVApi
import com.wrentv.tv.data.languageName
import kotlin.math.roundToInt
import com.wrentv.tv.ui.PlayTarget
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import java.io.File
import java.util.Locale

/** A track the viewer can pick in the audio or subtitle panel. */
data class TrackOption(val key: String, val label: String)

/** Seconds before an unmarked episode ends that the next-episode countdown starts. */
private const val UP_NEXT_LEAD = 30.0

/**
 * Plays one title: asks the server how (direct file or HLS), drives
 * ExoPlayer, reports progress, offers intro and credit skips and moves on to
 * the next episode. Downloads play from the file on this TV.
 */
class PlayerController(
    private val context: Context,
    private val api: WrenTVApi?,
    private val downloads: DownloadManager,
    target: PlayTarget,
    /** Outlives the screen, so the last progress report and session stop get sent. */
    private val appScope: CoroutineScope,
) {
    sealed interface Phase {
        data object Loading : Phase
        data class Pending(val message: String) : Phase
        data class Failed(val message: String) : Phase
        data object Playing : Phase
        data object Ended : Phase
    }

    enum class Skip { Intro, Credits }

    var itemId by mutableStateOf(target.itemId); private set
    var title by mutableStateOf(target.title); private set
    var subtitle by mutableStateOf(target.subtitle); private set
    var phase by mutableStateOf<Phase>(Phase.Loading); private set
    var methodLabel by mutableStateOf(""); private set
    var position by mutableDoubleStateOf(0.0); private set
    var duration by mutableDoubleStateOf(0.0); private set
    var paused by mutableStateOf(false); private set
    var buffering by mutableStateOf(false); private set
    /** The playing video's frame rate, once known; the screen matches the TV's refresh rate to it. */
    var frameRate by mutableFloatStateOf(0f); private set

    var audioOptions by mutableStateOf<List<TrackOption>>(emptyList()); private set
    var audioKey by mutableStateOf<String?>(null); private set
    var subtitleOptions by mutableStateOf<List<TrackOption>>(emptyList()); private set
    var subtitleKey by mutableStateOf<String?>(null); private set
    /** The server can search subtitle sites for this title. */
    var onlineSubtitles by mutableStateOf(false); private set
    /** The playing episode, once looked up, for the episode picker. */
    var episode by mutableStateOf<Item?>(null); private set
    var versions by mutableStateOf<List<MediaVersion>>(emptyList()); private set
    var fileId by mutableStateOf(target.fileId); private set
    var chapters by mutableStateOf<List<Chapter>>(emptyList()); private set
    private var thumbs: SeekThumbs? = null
    /** Where the seek bar is headed while left/right are pressed; the jump happens once they let go. */
    var scrub by mutableStateOf<Double?>(null); private set
    var skip by mutableStateOf<Skip?>(null); private set
    var countdown by mutableStateOf<Int?>(null); private set
    var upNext by mutableStateOf<Item?>(null); private set
    /** Set when playback is over and the screen should close. */
    var closeRequested by mutableStateOf(false); private set

    val player: ExoPlayer = ExoPlayer.Builder(context, DefaultRenderersFactory(context).setEnableDecoderFallback(true))
        .setSeekBackIncrementMs(10_000)
        .setSeekForwardIncrementMs(10_000)
        .setHandleAudioBecomingNoisy(true)
        .setLoadControl(loadControl())
        .build()

    /**
     * The read-ahead buffer lives in the app's memory. ExoPlayer's default allows
     * ~140 MB for video, which a 4K remux fills within minutes and runs a TV out
     * of memory, so cap it at a quarter of what the app may use.
     */
    private fun loadControl(): DefaultLoadControl {
        val cap = (Runtime.getRuntime().maxMemory() / 4).coerceIn(16L shl 20, 96L shl 20).toInt()
        return DefaultLoadControl.Builder()
            .setTargetBufferBytes(cap)
            .setPrioritizeTimeOverSizeThresholds(false)
            .build()
    }

    private val scope = CoroutineScope(appScope.coroutineContext + Job())
    private var fromStart = target.fromStart
    private var localFile: String? = target.localFile
    private val localPosition = target.localPosition
    private var audioIndex: Int? = null
    private var response: PlayResponse? = null
    private var sessionId: String? = null
    private var markers = Markers()
    private var nextEpisode: Item? = null
    private var dismissed = mutableSetOf<Skip>()
    private var loadJob: Job? = null
    private var countdownJob: Job? = null
    private var versionsJob: Job? = null
    private var subtitlePending = false
    private var stopped = false
    /** Audio codecs the server sent that didn't play here; they're converted to AAC instead. */
    private val failedAudio = mutableSetOf<String>()
    private var startedAt = 0.0
    private var lastAutoRetry = 0L
    /** The next play is a retry, and the version that broke partway, if one did. */
    private var freshLink = false
    private var failedFile: String? = null

    val isOffline: Boolean get() = localFile != null
    val currentChapter: Chapter? get() = chapters.lastOrNull { it.start <= position + 0.01 }

    init {
        // Debug builds: dropped frames, audio underruns, buffering and loads in logcat (tag EventLogger).
        if (context.applicationInfo.flags and android.content.pm.ApplicationInfo.FLAG_DEBUGGABLE != 0) player.addAnalyticsListener(EventLogger())
        // Every build: audio timing trouble, which shows as lip sync drifting after a seek.
        player.addAnalyticsListener(object : AnalyticsListener {
            override fun onAudioSinkError(t: AnalyticsListener.EventTime, e: Exception) {
                android.util.Log.w("WrenAudio", "sink at ${t.currentPlaybackPositionMs} ms: $e")
            }
            override fun onAudioUnderrun(t: AnalyticsListener.EventTime, size: Int, ms: Long, since: Long) {
                android.util.Log.w("WrenAudio", "underrun at ${t.currentPlaybackPositionMs} ms")
            }
            override fun onAudioInputFormatChanged(t: AnalyticsListener.EventTime, f: Format, r: DecoderReuseEvaluation?) {
                android.util.Log.i("WrenAudio", "format ${f.sampleMimeType} ${f.channelCount}ch")
            }
            override fun onPositionDiscontinuity(t: AnalyticsListener.EventTime, old: Player.PositionInfo, new: Player.PositionInfo, reason: Int) {
                android.util.Log.i("WrenAudio", "jump ${old.positionMs} -> ${new.positionMs} ms (reason $reason)")
            }
        })
        player.addListener(object : Player.Listener {
            override fun onPlaybackStateChanged(state: Int) {
                buffering = state == Player.STATE_BUFFERING
                if (state == Player.STATE_ENDED) {
                    // A stream that breaks partway can end early instead of failing.
                    if (!isOffline && duration > 0 && position < duration - 120) failed("The stream ended early.") else finished()
                }
            }

            override fun onIsPlayingChanged(isPlaying: Boolean) {
                paused = !player.playWhenReady
            }

            override fun onPlayerError(error: PlaybackException) {
                // The TV took the format but its audio output didn't (passthrough that isn't really there).
                val audioOutput = error.errorCode == PlaybackException.ERROR_CODE_AUDIO_TRACK_INIT_FAILED ||
                    error.errorCode == PlaybackException.ERROR_CODE_AUDIO_TRACK_WRITE_FAILED
                if (audioOutput && fallBackFromAudio()) return
                failed(error.message ?: "Playback failed")
            }

            override fun onTracksChanged(tracks: Tracks) {
                groups(C.TRACK_TYPE_VIDEO).firstOrNull { it.isSelected }?.let { g ->
                    val f = (0 until g.length).firstOrNull { g.isTrackSelected(it) }?.let { g.getTrackFormat(it).frameRate } ?: -1f
                    if (f > 0) frameRate = DisplayMode.normalize(f)
                }
                if (isOffline) {
                    audioOptions = groups(C.TRACK_TYPE_AUDIO).mapIndexed { i, g -> TrackOption("t$i", formatLabel(g, "Track ${i + 1}")) }
                    subtitleOptions = groups(C.TRACK_TYPE_TEXT).mapIndexed { i, g -> TrackOption("t$i", formatLabel(g, "Subtitles ${i + 1}")) }
                    audioKey = groups(C.TRACK_TYPE_AUDIO).indexOfFirst { it.isSelected }.takeIf { it >= 0 }?.let { "t$it" }
                }
                checkAudio(tracks)
                if (subtitlePending && groups(C.TRACK_TYPE_TEXT).isNotEmpty()) {
                    subtitlePending = false
                    applySubtitle()
                }
            }
        })
        scope.launch {
            // The clock: position for the bar, skips and progress reports.
            while (isActive) {
                if (phase == Phase.Playing) {
                    position = player.currentPosition / 1000.0
                    val d = player.duration
                    if (d != C.TIME_UNSET && d > 0) duration = d / 1000.0
                    updateSkip()
                }
                delay(250)
            }
        }
        scope.launch {
            while (isActive) {
                delay(10_000)
                if (phase == Phase.Playing && position > 5) report(position)
            }
        }
    }

    fun start(at: Double? = null) {
        loadJob?.cancel()
        loadJob = scope.launch { run(at) }
    }

    private suspend fun run(startAt: Double?) {
        phase = Phase.Loading
        localFile?.let { path ->
            methodLabel = "Downloaded"
            subtitleKey = null
            subtitlePending = false
            play(MediaItem.fromUri(Uri.fromFile(File(path))), startAt ?: if (fromStart) 0.0 else localPosition ?: 0.0)
            return
        }
        val api = api ?: run { phase = Phase.Failed("Not connected to the server."); return }
        var resp: PlayResponse
        // Only the first ask says it's a retry.
        var retry = freshLink
        var failed = failedFile
        freshLink = false
        failedFile = null
        while (true) {
            if (stopped) return
            resp = try {
                api.play(itemId, PlayRequest(fileId = fileId, audioIndex = audioIndex, caps = DeviceCaps.current(context, failedAudio),
                    retry = retry, failedFileId = failed)).also { retry = false; failed = null }
            } catch (e: Exception) {
                if (e is kotlinx.coroutines.CancellationException) throw e
                phase = Phase.Failed(e.message ?: "Couldn't start playback")
                return
            }
            // A streamed title is still being found: wait here and ask again.
            if (resp.status == "pending") {
                phase = Phase.Pending(resp.message ?: "Finding a stream…")
                delay(3000)
                continue
            }
            break
        }
        val path = resp.url ?: run { phase = Phase.Failed(resp.message ?: "The server didn't return a stream."); return }
        if (stopped) {
            resp.sessionId?.let { sid -> appScope.launch { runCatching { api.stopSession(sid) } } }
            return
        }
        response = resp
        sessionId = resp.sessionId
        fileId = resp.fileId ?: fileId
        versions = resp.versions.orEmpty()
        watchVersions()
        val audio = resp.info?.audio.orEmpty()
        audioOptions = audio.map { TrackOption("a${it.typeIndex}", it.label.ifEmpty { "Track ${it.typeIndex + 1}" }) }
        if (audioIndex == null) audioIndex = resp.decision?.audioIndex
        audioKey = audioIndex?.let { "a$it" }
        subtitleOptions = resp.subtitles.orEmpty().map(::subtitleOption)
        onlineSubtitles = resp.onlineSubtitles
        // The viewer's language setting picks the first time; restarts keep their choice.
        if (!subtitleChosen) {
            subtitleKey = resp.subtitleIndex?.takeIf { it >= 0 }?.let { "s$it" }
        }
        duration = resp.duration ?: 0.0
        chapters = resp.chapters ?: resp.info?.chapters.orEmpty()
        markers = resp.markers ?: Markers()
        thumbs = resp.thumbs
        methodLabel = describe(resp)

        val item = MediaItem.Builder().setUri(api.absolute(path))
        if (resp.method != "direct") item.setMimeType(MimeTypes.APPLICATION_M3U8)
        item.setSubtitleConfigurations(resp.subtitles.orEmpty().mapNotNull { subtitleConfig(api, it) })
        subtitlePending = true
        play(item.build(), startAt ?: if (fromStart) 0.0 else resp.resumePosition ?: 0.0)
        if (nextEpisode == null) nextEpisode = findNextEpisode()
    }

    private fun subtitleOption(s: SubtitleTrack) = TrackOption("s${s.index}",
        listOfNotNull(languageName(s.language), s.title?.takeIf { it.isNotEmpty() }, if (s.forced) "Forced" else null)
            .joinToString(" · ").ifEmpty { "Track ${s.index + 1}" })

    private fun subtitleConfig(api: WrenTVApi, s: SubtitleTrack): MediaItem.SubtitleConfiguration? {
        val url = s.url ?: return null
        return MediaItem.SubtitleConfiguration.Builder(Uri.parse(api.absolute(url)))
            .setMimeType(MimeTypes.TEXT_VTT)
            .setLanguage(s.language)
            .setId("wren-sub-${s.index}")
            .build()
    }

    /** Searches the subtitle sites for this title; throws when the search fails. */
    suspend fun searchOnlineSubtitles(): List<OnlineSubtitle> =
        api?.searchOnlineSubtitles(itemId)?.results.orEmpty()

    /**
     * Downloads a subtitle the search found and shows it. A side-loaded track
     * can only be added by loading the stream again, from the same spot.
     */
    suspend fun addOnlineSubtitle(sub: OnlineSubtitle) {
        val api = api ?: return
        val resp = response ?: return
        val track = api.fetchOnlineSubtitle(itemId, sub, sessionId)
        val subs = resp.subtitles.orEmpty().filter { it.index != track.index } + track
        response = resp.copy(subtitles = subs)
        subtitleOptions = subs.map(::subtitleOption)
        subtitleChosen = true
        subtitleKey = "s${track.index}"
        val current = player.currentMediaItem ?: return
        val item = current.buildUpon().setSubtitleConfigurations(subs.mapNotNull { subtitleConfig(api, it) }).build()
        subtitlePending = true
        play(item, player.currentPosition / 1000.0)
    }

    /** A season's episodes, or a show's seasons. */
    suspend fun children(id: String): List<Item> = api?.item(id)?.children.orEmpty()

    /** What the stats overlay shows: how the title is playing right now. */
    fun stats(): List<Pair<String, String>> {
        val rows = mutableListOf<Pair<String, String>>()
        val r = response
        rows += "Playback" to listOfNotNull(methodLabel.ifEmpty { null }, r?.engine).joinToString(" · ").ifEmpty { if (isOffline) "Downloaded" else "—" }
        versions.firstOrNull { it.id == fileId }?.label?.takeIf { it.isNotEmpty() }?.let { rows += "Version" to it }
        r?.info?.let { info ->
            if (info.container.isNotEmpty()) rows += "Container" to info.container
            info.bitrate?.takeIf { it > 0 }?.let { rows += "Bitrate" to mbps(it) }
        }
        player.videoFormat?.let { f ->
            rows += "Video" to listOfNotNull(f.codecs ?: f.sampleMimeType, if (f.width > 0) "${f.width}×${f.height}" else null,
                f.frameRate.takeIf { it > 0 }?.let { String.format(Locale.ROOT, "%.3g fps", it) },
                f.bitrate.takeIf { it > 0 }?.let { mbps(it.toLong()) }).joinToString(" · ")
        }
        player.audioFormat?.let { f ->
            rows += "Audio" to listOfNotNull(f.codecs ?: f.sampleMimeType, f.channelCount.takeIf { it > 0 }?.let { "${it}ch" },
                f.sampleRate.takeIf { it > 0 }?.let { "${it / 1000.0} kHz" }).joinToString(" · ")
        }
        rows += "Buffered" to String.format(Locale.ROOT, "%.1f s", player.totalBufferedDuration / 1000.0)
        player.videoDecoderCounters?.let { c ->
            c.ensureUpdated()
            rows += "Dropped frames" to "${c.droppedBufferCount} of ${c.renderedOutputBufferCount + c.droppedBufferCount}"
        }
        markers.source?.takeIf { it.isNotEmpty() }?.let { rows += "Skip markers" to it }
        r?.decision?.reasons?.takeIf { it.isNotEmpty() }?.let { rows += "Why" to it.joinToString("; ") }
        return rows
    }

    private fun mbps(bps: Long) = String.format(Locale.ROOT, "%.1f Mbps", bps / 1_000_000.0)

    private fun play(item: MediaItem, start: Double) {
        startedAt = start
        player.setMediaItem(item, (start * 1000).toLong().coerceAtLeast(0))
        player.prepare()
        player.playWhenReady = true
        paused = false
        phase = Phase.Playing
        if (localFile == null) applySubtitle()
    }

    /** A streamed title's lookup goes on after play starts: pick up the
     *  versions it finds while it's still looking. */
    private fun watchVersions() {
        versionsJob?.cancel()
        val id = itemId
        versionsJob = scope.launch {
            delay(5_000)
            repeat(15) {
                val r = runCatching { api?.versions(id) }.getOrNull() ?: return@launch
                if (id != itemId) return@launch
                if (r.versions.isNotEmpty()) versions = r.versions
                if (!r.looking) return@launch
                delay(20_000)
            }
        }
    }

    /** Asks for the versions again, for when the picker opens. */
    fun refreshVersions() {
        val id = itemId
        scope.launch {
            val r = runCatching { api?.versions(id) }.getOrNull() ?: return@launch
            if (id == itemId && r.versions.isNotEmpty()) versions = r.versions
        }
    }

    fun stop() {
        if (stopped) return
        stopped = true
        loadJob?.cancel()
        val pos = position
        if (pos > 5) report(pos)
        sessionId?.let { sid -> api?.let { a -> appScope.launch { runCatching { a.stopSession(sid) } } } }
        sessionId = null
        cancelCountdown()
        scope.coroutineContext[Job]?.cancel()
        player.release()
    }

    private fun report(pos: Double) {
        val id = itemId
        val dur = duration
        if (isOffline) downloads.savePosition(id, pos, dur)
        val held = paused
        api?.let { a ->
            appScope.launch {
                val reason = a.reportProgress(id, pos, dur, held) ?: return@launch
                scope.launch { stoppedByServer(reason) }
            }
        }
    }

    /** The server owner ended this stream from the dashboard: stop and say why. */
    private fun stoppedByServer(message: String) {
        if (stopped || phase != Phase.Playing) return
        sessionId = null
        player.stop()
        phase = Phase.Failed(message)
    }

    // Controls

    fun togglePause() {
        if (phase != Phase.Playing) return
        player.playWhenReady = !player.playWhenReady
        paused = !player.playWhenReady
    }

    fun seekBy(seconds: Double) = seekTo(position + seconds)

    /**
     * Moves the scrub point. A tap is 10 seconds; holding the button picks up
     * speed, the way Plex and YouTube do, so crossing a film doesn't take a minute.
     */
    fun scrubBy(direction: Int, repeat: Int) {
        if (phase != Phase.Playing || duration <= 0) return
        val step = when {
            repeat < 6 -> 10.0
            repeat < 16 -> 30.0
            repeat < 30 -> 60.0
            else -> 120.0
        }
        scrub = ((scrub ?: position) + direction * step).coerceIn(0.0, duration - 1)
    }

    /** Jumps to the scrub point. */
    fun commitScrub() {
        val t = scrub ?: return
        scrub = null
        seekTo(t)
    }

    fun cancelScrub() { scrub = null }

    /** The seek bar frame nearest a time, or null when the server has none. */
    fun thumbUrl(seconds: Double): String? {
        val t = thumbs ?: return null
        val a = api ?: return null
        if (isOffline) return null
        val n = (seconds / t.interval).roundToInt().coerceAtLeast(0)
        return a.absolute(t.url + n + ".jpg")
    }

    fun seekTo(seconds: Double) {
        val t = seconds.coerceIn(0.0, if (duration > 0) duration - 1 else Double.MAX_VALUE)
        player.seekTo((t * 1000).toLong())
        position = t
        updateSkip()
    }

    fun selectAudio(key: String) {
        if (key == audioKey) return
        if (isOffline) {
            val i = key.removePrefix("t").toIntOrNull() ?: return
            val g = groups(C.TRACK_TYPE_AUDIO).getOrNull(i) ?: return
            player.trackSelectionParameters = player.trackSelectionParameters.buildUpon()
                .setOverrideForType(TrackSelectionOverride(g.mediaTrackGroup, 0)).build()
            audioKey = key
            return
        }
        // A different track needs a new stream from the server.
        audioIndex = key.removePrefix("a").toIntOrNull()
        audioKey = key
        restart()
    }

    private var subtitleChosen = false

    fun selectSubtitle(key: String?) {
        subtitleChosen = true
        subtitleKey = key
        applySubtitle()
    }

    /** Plays another version of the title from the same spot. */
    fun selectVersion(id: String) {
        if (id == fileId) return
        fileId = id
        // Track numbers differ between files; let the server pick again.
        audioIndex = null
        subtitleChosen = false
        subtitleKey = null
        restart()
    }

    private fun restart() {
        // Before the clock first ticks, position is still 0: keep where playback began.
        val pos = position.takeIf { it > 0 } ?: startedAt
        val old = sessionId
        sessionId = null
        player.stop()
        old?.let { sid -> api?.let { a -> appScope.launch { runCatching { a.stopSession(sid) } } } }
        start(pos)
    }

    /**
     * A stream that fails partway is tried again from the same spot once:
     * the server drops a post that lost articles and hands out another.
     */
    private fun failed(message: String) {
        if (phase != Phase.Playing) return
        if (!isOffline) failedFile = fileId
        val now = android.os.SystemClock.elapsedRealtime()
        if (!isOffline && (lastAutoRetry == 0L || now - lastAutoRetry > 120_000)) {
            lastAutoRetry = now
            retry()
            return
        }
        phase = Phase.Failed(message)
    }

    /** Starts over after a failure, closing the failed stream on the server first. */
    fun retry() {
        freshLink = true
        val old = sessionId
        sessionId = null
        player.stop()
        old?.let { sid -> api?.let { a -> appScope.launch { runCatching { a.stopSession(sid) } } } }
        start(position.takeIf { it > 0 })
    }

    /**
     * A stream whose audio the player can't use plays silently, so ask the
     * server again with that codec ruled out and it converts the audio.
     */
    private fun checkAudio(tracks: Tracks) {
        if (isOffline || phase != Phase.Playing) return
        // Until the video shows up the tracks aren't known yet.
        if (tracks.groups.none { it.type == C.TRACK_TYPE_VIDEO }) return
        if (tracks.groups.any { it.type == C.TRACK_TYPE_AUDIO && it.isSupported }) return
        fallBackFromAudio()
    }

    private fun fallBackFromAudio(): Boolean {
        val decision = response?.decision ?: return false
        val codec = decision.audioCodec ?: return false
        if ((decision.audioIndex ?: -1) < 0 || codec == "aac" || codec in failedAudio) return false
        failedAudio += codec
        restart()
        return true
    }

    private fun groups(type: Int): List<Tracks.Group> = player.currentTracks.groups.filter { it.type == type }

    /** Shows the chosen subtitle track, or none. */
    private fun applySubtitle() {
        val params = player.trackSelectionParameters.buildUpon().clearOverridesOfType(C.TRACK_TYPE_TEXT)
        val key = subtitleKey
        if (key == null) {
            player.trackSelectionParameters = params.setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true).build()
            return
        }
        val text = groups(C.TRACK_TYPE_TEXT)
        val group = if (isOffline) {
            text.getOrNull(key.removePrefix("t").toIntOrNull() ?: -1)
        } else {
            val index = key.removePrefix("s").toIntOrNull() ?: -1
            val resp = response
            // Text subtitles come from the server as WebVTT; picture ones (PGS) are in the stream.
            text.firstOrNull { g -> g.getTrackFormat(0).id?.endsWith("wren-sub-$index") == true }
                ?: if (resp?.method == "direct") {
                    val embedded = text.filter { g -> g.getTrackFormat(0).id?.contains("wren-sub-") != true }
                    embedded.getOrNull(resp.subtitles.orEmpty().indexOfFirst { it.index == index })
                } else {
                    // The server names stream track 2 "English (3)".
                    text.firstOrNull { g -> g.getTrackFormat(0).label?.endsWith("(${index + 1})") == true }
                }
        }
        if (group == null) {
            // Tracks aren't known until the stream loads.
            subtitlePending = true
            player.trackSelectionParameters = params.setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true).build()
            return
        }
        player.trackSelectionParameters = params.setTrackTypeDisabled(C.TRACK_TYPE_TEXT, false)
            .setOverrideForType(TrackSelectionOverride(group.mediaTrackGroup, 0)).build()
    }

    private fun formatLabel(g: Tracks.Group, fallback: String): String {
        val f = g.getTrackFormat(0)
        return listOfNotNull(languageName(f.language), f.label?.takeIf { it.isNotEmpty() }).distinct().joinToString(" · ").ifEmpty { fallback }
    }

    // Skipping

    private fun updateSkip() {
        val t = position
        val intro = markers.intro
        // An episode with no credits marker counts down over its last
        // seconds instead, so it can still roll on to the next.
        val credits = markers.credits
            ?: if (nextEpisode != null && duration >= 300) TimeRange(duration - UP_NEXT_LEAD, duration + 1) else null
        // Seeking back before a marker offers it again.
        if (intro != null && t < intro.start) dismissed.remove(Skip.Intro)
        if (credits != null && t < credits.start - 1) {
            dismissed.remove(Skip.Credits)
            cancelCountdown()
        }
        val next = when {
            intro != null && t >= intro.start && t < intro.end - 1 && Skip.Intro !in dismissed -> Skip.Intro
            credits != null && t >= credits.start && t < credits.end - 1 && Skip.Credits !in dismissed -> Skip.Credits
            else -> null
        }
        if (next == Skip.Credits && nextEpisode != null && countdown == null && countdownJob == null && !paused) startCountdown()
        if (skip != next) skip = next
    }

    fun skipIntro() {
        val i = markers.intro ?: return
        dismissed += Skip.Intro
        skip = null
        seekTo(i.end)
    }

    /** Skips the credits: on to the next episode when there is one, else the end. */
    fun skipCredits() {
        dismissed += Skip.Credits
        skip = null
        cancelCountdown()
        val next = nextEpisode
        if (next != null) {
            upNext = next
            playNext()
        } else {
            finished()
            closeRequested = true
        }
    }

    /** Keeps watching the credits. */
    fun dismissCredits() {
        dismissed += Skip.Credits
        skip = null
        cancelCountdown()
    }

    private fun startCountdown() {
        countdown = 10
        countdownJob = scope.launch {
            while (isActive) {
                delay(1000)
                val n = countdown ?: return@launch
                if (paused) continue
                if (n <= 1) {
                    countdownJob = null
                    skipCredits()
                    return@launch
                }
                countdown = n - 1
            }
        }
    }

    private fun cancelCountdown() {
        countdownJob?.cancel()
        countdownJob = null
        countdown = null
    }

    /** The episode after this one: next in its season, else the first of the next season. */
    private suspend fun findNextEpisode(): Item? {
        val api = api ?: return null
        return runCatching {
            val ep = api.item(itemId).item
            if (ep.kind != "episode") return null
            episode = ep
            ep.parentId?.let { pid ->
                api.item(pid).children.orEmpty().filter { it.episodeNumber > ep.episodeNumber }.minByOrNull { it.episodeNumber }
            }?.let { return it }
            val showId = ep.showId ?: return null
            val season = api.item(showId).children.orEmpty().filter { it.seasonNumber > ep.seasonNumber }.minByOrNull { it.seasonNumber } ?: return null
            api.item(season.id).children.orEmpty().minByOrNull { it.episodeNumber }
        }.getOrNull()
    }

    // The end

    private fun finished() {
        if (phase != Phase.Playing) return
        phase = Phase.Ended
        val dur = duration
        val id = itemId
        report(dur)
        nextEpisode?.let { upNext = it; return }
        if (isOffline) {
            closeRequested = true
            return
        }
        scope.launch {
            val api = api ?: return@launch
            val next = runCatching {
                val d = api.item(id).item
                if (d.kind == "episode" && d.showId != null) api.item(d.showId).nextEpisode?.takeIf { it.id != id } else null
            }.getOrNull()
            if (next != null) upNext = next else closeRequested = true
        }
    }

    fun playNext() {
        val next = upNext ?: return
        // Moving on counts as finishing this one.
        if (phase == Phase.Playing) report(duration)
        switchTo(next)
    }

    /** Plays another episode picked from the episode list. */
    fun playEpisode(ep: Item) {
        if (ep.id == itemId) return
        if (phase == Phase.Playing && position > 5) report(position)
        switchTo(ep)
    }

    private fun switchTo(next: Item) {
        freshLink = false
        failedFile = null
        localFile = downloads.item(next.id)?.let { x -> downloads.localFile(x)?.absolutePath }
        val old = sessionId
        sessionId = null
        player.stop()
        old?.let { sid -> api?.let { a -> appScope.launch { runCatching { a.stopSession(sid) } } } }
        itemId = next.id
        subtitle = "${next.episodeLabel} — ${next.title}"
        upNext = null
        nextEpisode = null
        episode = null
        cancelCountdown()
        dismissed.clear()
        skip = null
        chapters = emptyList()
        markers = Markers()
        thumbs = null
        scrub = null
        fromStart = false
        fileId = null
        versions = emptyList()
        audioIndex = null
        subtitleChosen = false
        subtitleKey = null
        position = 0.0
        duration = 0.0
        start()
    }

    private fun describe(r: PlayResponse): String {
        val parts = mutableListOf<String>()
        when (r.method) {
            "direct" -> parts += "Direct Play"
            "remux" -> parts += "Direct Stream"
            "transcode" -> parts += "Transcode"
        }
        r.info?.video?.firstOrNull()?.let { v ->
            v.hdr?.takeIf { it.isNotEmpty() }?.let { h -> parts += v.dvProfile?.let { "$h P$it" } ?: h }
        }
        r.info?.audio?.firstOrNull { it.typeIndex == r.decision?.audioIndex }?.let { a ->
            val out = r.decision?.audioCodec
            parts += if (out != null && out != a.codec && r.method != "direct") "${a.codecLabel} → ${out.uppercase(Locale.ROOT)}" else a.codecLabel
        }
        return parts.joinToString(" · ")
    }
}
