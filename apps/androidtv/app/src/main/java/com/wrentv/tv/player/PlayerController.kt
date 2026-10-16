@file:OptIn(UnstableApi::class)

package com.wrentv.tv.player

import android.content.Context
import android.net.Uri
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableDoubleStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.TrackSelectionOverride
import androidx.media3.common.Tracks
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.DefaultRenderersFactory
import androidx.media3.exoplayer.ExoPlayer
import com.wrentv.tv.data.Chapter
import com.wrentv.tv.data.DownloadManager
import com.wrentv.tv.data.Item
import com.wrentv.tv.data.Markers
import com.wrentv.tv.data.MediaVersion
import com.wrentv.tv.data.PlayRequest
import com.wrentv.tv.data.PlayResponse
import com.wrentv.tv.data.WrenTVApi
import com.wrentv.tv.data.languageName
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

    var audioOptions by mutableStateOf<List<TrackOption>>(emptyList()); private set
    var audioKey by mutableStateOf<String?>(null); private set
    var subtitleOptions by mutableStateOf<List<TrackOption>>(emptyList()); private set
    var subtitleKey by mutableStateOf<String?>(null); private set
    var versions by mutableStateOf<List<MediaVersion>>(emptyList()); private set
    var fileId by mutableStateOf(target.fileId); private set
    var chapters by mutableStateOf<List<Chapter>>(emptyList()); private set
    var skip by mutableStateOf<Skip?>(null); private set
    var countdown by mutableStateOf<Int?>(null); private set
    var upNext by mutableStateOf<Item?>(null); private set
    /** Set when playback is over and the screen should close. */
    var closeRequested by mutableStateOf(false); private set

    val player: ExoPlayer = ExoPlayer.Builder(context, DefaultRenderersFactory(context).setEnableDecoderFallback(true))
        .setSeekBackIncrementMs(10_000)
        .setSeekForwardIncrementMs(10_000)
        .setHandleAudioBecomingNoisy(true)
        .build()

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
    private var subtitlePending = false
    private var stopped = false

    val isOffline: Boolean get() = localFile != null
    val currentChapter: Chapter? get() = chapters.lastOrNull { it.start <= position + 0.01 }

    init {
        player.addListener(object : Player.Listener {
            override fun onPlaybackStateChanged(state: Int) {
                buffering = state == Player.STATE_BUFFERING
                if (state == Player.STATE_ENDED) finished()
            }

            override fun onIsPlayingChanged(isPlaying: Boolean) {
                paused = !player.playWhenReady
            }

            override fun onPlayerError(error: PlaybackException) {
                phase = Phase.Failed(error.message ?: "Playback failed")
            }

            override fun onTracksChanged(tracks: Tracks) {
                if (isOffline) {
                    audioOptions = groups(C.TRACK_TYPE_AUDIO).mapIndexed { i, g -> TrackOption("t$i", formatLabel(g, "Track ${i + 1}")) }
                    subtitleOptions = groups(C.TRACK_TYPE_TEXT).mapIndexed { i, g -> TrackOption("t$i", formatLabel(g, "Subtitles ${i + 1}")) }
                    audioKey = groups(C.TRACK_TYPE_AUDIO).indexOfFirst { it.isSelected }.takeIf { it >= 0 }?.let { "t$it" }
                }
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
                if (phase == Phase.Playing && position > 5 && !paused) report(position)
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
        while (true) {
            if (stopped) return
            resp = try {
                api.play(itemId, PlayRequest(fileId = fileId, audioIndex = audioIndex, caps = DeviceCaps.current(context)))
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
        val audio = resp.info?.audio.orEmpty()
        audioOptions = audio.map { TrackOption("a${it.typeIndex}", it.label.ifEmpty { "Track ${it.typeIndex + 1}" }) }
        if (audioIndex == null) audioIndex = resp.decision?.audioIndex
        audioKey = audioIndex?.let { "a$it" }
        subtitleOptions = resp.subtitles.orEmpty().map { s ->
            TrackOption("s${s.index}", listOfNotNull(languageName(s.language), s.title?.takeIf { it.isNotEmpty() }, if (s.forced) "Forced" else null)
                .joinToString(" · ").ifEmpty { "Track ${s.index + 1}" })
        }
        // The viewer's language setting picks the first time; restarts keep their choice.
        if (!subtitleChosen) {
            subtitleKey = resp.subtitleIndex?.takeIf { it >= 0 }?.let { "s$it" }
        }
        duration = resp.duration ?: 0.0
        chapters = resp.chapters ?: resp.info?.chapters.orEmpty()
        markers = resp.markers ?: Markers()
        methodLabel = describe(resp)

        val item = MediaItem.Builder().setUri(api.absolute(path))
        if (resp.method != "direct") item.setMimeType(MimeTypes.APPLICATION_M3U8)
        item.setSubtitleConfigurations(resp.subtitles.orEmpty().mapNotNull { s ->
            val url = s.url ?: return@mapNotNull null
            MediaItem.SubtitleConfiguration.Builder(Uri.parse(api.absolute(url)))
                .setMimeType(MimeTypes.TEXT_VTT)
                .setLanguage(s.language)
                .setId("wren-sub-${s.index}")
                .build()
        })
        subtitlePending = true
        play(item.build(), startAt ?: if (fromStart) 0.0 else resp.resumePosition ?: 0.0)
        if (nextEpisode == null) nextEpisode = findNextEpisode()
    }

    private fun play(item: MediaItem, start: Double) {
        player.setMediaItem(item, (start * 1000).toLong().coerceAtLeast(0))
        player.prepare()
        player.playWhenReady = true
        paused = false
        phase = Phase.Playing
        if (localFile == null) applySubtitle()
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
        api?.let { a -> appScope.launch { runCatching { a.reportProgress(id, pos, dur) } } }
    }

    // Controls

    fun togglePause() {
        if (phase != Phase.Playing) return
        player.playWhenReady = !player.playWhenReady
        paused = !player.playWhenReady
    }

    fun seekBy(seconds: Double) = seekTo(position + seconds)

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
        val pos = position
        val old = sessionId
        sessionId = null
        player.stop()
        old?.let { sid -> api?.let { a -> appScope.launch { runCatching { a.stopSession(sid) } } } }
        start(pos)
    }

    fun retry() {
        start(position.takeIf { it > 0 })
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
        val credits = markers.credits
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
        localFile = downloads.item(next.id)?.let { x -> downloads.localFile(x)?.absolutePath }
        val old = sessionId
        sessionId = null
        player.stop()
        old?.let { sid -> api?.let { a -> appScope.launch { runCatching { a.stopSession(sid) } } } }
        itemId = next.id
        subtitle = "${next.episodeLabel} — ${next.title}"
        upNext = null
        nextEpisode = null
        dismissed.clear()
        skip = null
        chapters = emptyList()
        markers = Markers()
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
