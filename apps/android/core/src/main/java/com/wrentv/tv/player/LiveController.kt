@file:OptIn(UnstableApi::class)

package com.wrentv.tv.player

import android.content.Context
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.DefaultLoadControl
import androidx.media3.exoplayer.DefaultRenderersFactory
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.extractor.DefaultExtractorsFactory
import androidx.media3.extractor.ts.DefaultTsPayloadReaderFactory
import com.wrentv.tv.data.LiveChannel
import com.wrentv.tv.data.WrenTVApi
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * Plays live channels. The server hands back a stream for each channel (its
 * own proxy of the provider's, HLS or plain MPEG-TS); flipping channels waits
 * for the remote to settle before tuning, and a dropped stream reconnects on
 * its own, as IPTV streams often drop.
 */
class LiveController(
    private val context: Context,
    private val api: WrenTVApi?,
    channels: List<LiveChannel>,
    start: LiveChannel,
    /** Outlives the screen, so the last session stop gets sent. */
    private val appScope: CoroutineScope,
) {
    sealed interface Phase {
        data object Tuning : Phase
        data object Playing : Phase
        data class Failed(val message: String) : Phase
    }

    var channels by mutableStateOf(channels); private set
    /** The channel showing in the banner: while flipping, the one about to tune. */
    var channel by mutableStateOf(start); private set
    var phase by mutableStateOf<Phase>(Phase.Tuning); private set
    var buffering by mutableStateOf(false); private set
    var paused by mutableStateOf(false); private set

    val player: ExoPlayer = ExoPlayer.Builder(context, DefaultRenderersFactory(context).setEnableDecoderFallback(true))
        .setMediaSourceFactory(DefaultMediaSourceFactory(context, extractors()))
        .setLoadControl(loadControl())
        .setHandleAudioBecomingNoisy(true)
        .build()

    private val scope = CoroutineScope(appScope.coroutineContext + Job())
    private var tuned: String? = null
    private var sessionId: String? = null
    private var tuneJob: Job? = null
    private var retries = 0
    private var stopped = false

    init {
        player.addListener(object : Player.Listener {
            override fun onPlaybackStateChanged(state: Int) {
                buffering = state == Player.STATE_BUFFERING
                when (state) {
                    Player.STATE_READY -> { retries = 0; phase = Phase.Playing }
                    // A live stream doesn't end: the connection dropped.
                    Player.STATE_ENDED -> reconnect("The stream ended.")
                }
            }

            override fun onPlayerError(error: PlaybackException) {
                if (error.errorCode == PlaybackException.ERROR_CODE_BEHIND_LIVE_WINDOW) {
                    player.seekToDefaultPosition()
                    player.prepare()
                    return
                }
                reconnect(describe(error))
            }
        })
    }

    /** ExoPlayer's own messages ("Source error") mean nothing to someone on the couch. */
    private fun describe(error: PlaybackException): String = when (error.errorCode) {
        PlaybackException.ERROR_CODE_IO_BAD_HTTP_STATUS,
        PlaybackException.ERROR_CODE_IO_FILE_NOT_FOUND,
        PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_FAILED,
        PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_TIMEOUT,
        PlaybackException.ERROR_CODE_IO_UNSPECIFIED ->
            "The channel isn't sending anything right now. It may be off the air, or the provider may be busy."
        PlaybackException.ERROR_CODE_PARSING_CONTAINER_MALFORMED,
        PlaybackException.ERROR_CODE_PARSING_MANIFEST_MALFORMED,
        PlaybackException.ERROR_CODE_PARSING_CONTAINER_UNSUPPORTED,
        PlaybackException.ERROR_CODE_PARSING_MANIFEST_UNSUPPORTED ->
            "The channel's stream couldn't be read."
        PlaybackException.ERROR_CODE_DECODER_INIT_FAILED,
        PlaybackException.ERROR_CODE_DECODING_FORMAT_UNSUPPORTED,
        PlaybackException.ERROR_CODE_DECODING_FORMAT_EXCEEDS_CAPABILITIES ->
            "This TV can't play the channel's video format."
        else -> "Something went wrong playing the channel."
    }

    /** Broadcast streams start on any frame and come in odd shapes: start fast, tolerate them. */
    private fun extractors() = DefaultExtractorsFactory().setTsExtractorFlags(
        DefaultTsPayloadReaderFactory.FLAG_ALLOW_NON_IDR_KEYFRAMES or DefaultTsPayloadReaderFactory.FLAG_DETECT_ACCESS_UNITS,
    )

    /** Starts after a second of video rather than ExoPlayer's two and a half, so flipping feels quick. */
    private fun loadControl(): DefaultLoadControl {
        val cap = (Runtime.getRuntime().maxMemory() / 4).coerceIn(16L shl 20, 64L shl 20).toInt()
        return DefaultLoadControl.Builder()
            .setBufferDurationsMs(10_000, 30_000, 1_000, 2_500)
            .setTargetBufferBytes(cap)
            .setPrioritizeTimeOverSizeThresholds(false)
            .build()
    }

    fun start() = tune(channel, settle = 0)

    /** Moves [delta] channels along the list, wrapping around; tunes once the presses stop. */
    fun flip(delta: Int) {
        val list = channels
        if (list.isEmpty()) return
        val i = list.indexOfFirst { it.id == channel.id }.coerceAtLeast(0)
        tune(list[Math.floorMod(i + delta, list.size)], settle = 600)
    }

    fun tune(ch: LiveChannel, settle: Long = 0) {
        channel = ch
        tuneJob?.cancel()
        // Flipped away and back before it tuned: stay on what's playing.
        if (ch.id == tuned && phase !is Phase.Failed) return
        tuneJob = scope.launch {
            if (settle > 0) delay(settle)
            retries = 0
            open(ch)
        }
    }

    /** A channel number typed on the remote. */
    fun tuneNumber(number: String): Boolean {
        val ch = channels.firstOrNull { it.number == number } ?: channels.firstOrNull { it.number?.trimStart('0') == number.trimStart('0') } ?: return false
        tune(ch)
        return true
    }

    private suspend fun open(ch: LiveChannel) {
        val api = api ?: run { phase = Phase.Failed("Not connected to the server."); return }
        phase = Phase.Tuning
        paused = false
        endSession()
        player.stop()
        tuned = ch.id
        val resp = try {
            api.playChannel(ch.id, DeviceCaps.current(context))
        } catch (e: Exception) {
            if (e is CancellationException) throw e
            tuned = null
            phase = Phase.Failed(e.message ?: "Couldn't tune to ${ch.name}")
            return
        }
        val url = resp.url ?: run {
            tuned = null
            phase = Phase.Failed(resp.message ?: "The server didn't return a stream.")
            return
        }
        if (stopped) {
            resp.sessionId?.let { sid -> appScope.launch { runCatching { api.stopSession(sid) } } }
            return
        }
        sessionId = resp.sessionId
        val item = MediaItem.Builder().setUri(api.absolute(url))
        when (resp.type) {
            "hls" -> item.setMimeType(MimeTypes.APPLICATION_M3U8)
            "ts" -> item.setMimeType(MimeTypes.VIDEO_MP2T)
        }
        player.setMediaItem(item.build())
        player.prepare()
        player.playWhenReady = true
    }

    /** Tries the channel again a few times, waiting longer each time, before giving up. */
    private fun reconnect(reason: String) {
        if (stopped) return
        val ch = channels.firstOrNull { it.id == tuned } ?: channel
        if (retries >= 3) {
            tuned = null
            phase = Phase.Failed(reason)
            return
        }
        retries++
        buffering = true
        tuneJob?.cancel()
        tuneJob = scope.launch {
            delay(1_000L * retries)
            open(ch)
        }
    }

    fun retry() {
        retries = 0
        tuned = null
        tune(channel)
    }

    /** Pausing live TV holds the picture; playing again goes back to live. */
    fun togglePause() {
        if (phase != Phase.Playing) return
        if (player.playWhenReady) {
            player.playWhenReady = false
            paused = true
        } else {
            player.seekToDefaultPosition()
            player.playWhenReady = true
            paused = false
        }
    }

    /** Fresh guide data: keeps the list and the showing channel up to date. */
    fun update(fresh: List<LiveChannel>) {
        if (fresh.isEmpty()) return
        channels = fresh
        fresh.firstOrNull { it.id == channel.id }?.let { channel = it }
    }

    private fun endSession() {
        val sid = sessionId ?: return
        sessionId = null
        api?.let { a -> appScope.launch { runCatching { a.stopSession(sid) } } }
    }

    fun stop() {
        if (stopped) return
        stopped = true
        tuneJob?.cancel()
        endSession()
        scope.coroutineContext[Job]?.cancel()
        player.release()
    }
}
