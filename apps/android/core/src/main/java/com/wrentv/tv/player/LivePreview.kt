@file:OptIn(UnstableApi::class)

package com.wrentv.tv.player

import android.content.Context
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import com.wrentv.tv.data.LiveChannel
import com.wrentv.tv.data.WrenTVApi
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * The guide's muted live picture of the channel in focus. It starts once
 * focus has rested on a channel for a moment, so scrolling past channels
 * doesn't tune each one, and the server doesn't count it as watching.
 */
class LivePreview(
    context: Context,
    private val api: () -> WrenTVApi?,
    /** Outlives the screen, so the last session stop gets sent. */
    private val appScope: CoroutineScope,
) {
    val player: ExoPlayer = ExoPlayer.Builder(context).build().apply { volume = 0f }
    /** The picture is moving (until then the guide shows the channel's art). */
    var showing by mutableStateOf(false); private set
    var channelId by mutableStateOf<String?>(null); private set

    private val caps = DeviceCaps.current(context)
    private var sessionId: String? = null
    private var job: Job? = null
    private var released = false

    init {
        player.addListener(object : Player.Listener {
            override fun onIsPlayingChanged(isPlaying: Boolean) { if (isPlaying) showing = true }
            // A preview that fails just stays as art.
            override fun onPlayerError(error: androidx.media3.common.PlaybackException) { showing = false }
        })
    }

    fun show(ch: LiveChannel?, after: Long = 900) {
        if (released || ch?.id == channelId) return
        stop()
        channelId = ch?.id
        val api = api() ?: return
        ch ?: return
        job = appScope.launch {
            delay(after)
            val r = runCatching { api.playChannel(ch.id, caps, preview = true) }.getOrNull() ?: return@launch
            if (released || channelId != ch.id || r.url == null) {
                r.sessionId?.let { api.stopSession(it) }
                return@launch
            }
            sessionId = r.sessionId
            val item = MediaItem.Builder().setUri(api.absolute(r.url))
            when (r.type) {
                "hls" -> item.setMimeType(MimeTypes.APPLICATION_M3U8)
                "ts" -> item.setMimeType(MimeTypes.VIDEO_MP2T)
            }
            player.setMediaItem(item.build())
            player.prepare()
            player.playWhenReady = true
        }
    }

    fun stop() {
        job?.cancel()
        job = null
        channelId = null
        showing = false
        player.stop()
        player.clearMediaItems()
        val sid = sessionId ?: return
        sessionId = null
        api()?.let { a -> appScope.launch { a.stopSession(sid) } }
    }

    fun release() {
        stop()
        released = true
        player.release()
    }
}
