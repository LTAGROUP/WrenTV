package com.wrentv.tv.player

import android.content.Context
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.exoplayer.ExoPlayer
import com.wrentv.tv.data.WrenTVApi
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.io.File

/**
 * A show's or movie's theme behind its details page, like Plex. It keeps
 * playing from a show to one of its seasons and fades out on leaving, and
 * doesn't start over when coming back from watching the same title.
 */
object ThemeMusic {
    private const val VOLUME = 0.35f

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private var player: ExoPlayer? = null
    private var playing: String? = null
    private var job: Job? = null
    private var stopping: Job? = null
    /** Played to the end or cut off by playback: not again until another title's theme. */
    private var done: String? = null

    /** Plays the theme for [key] (a movie's or show's id), fetched by way of [id]. */
    fun play(context: Context, api: WrenTVApi, key: String, id: String) {
        stopping?.cancel()
        stopping = null
        if (key == playing || key == done) return
        fadeOut()
        playing = key
        job = scope.launch {
            val file = File(context.cacheDir, "themes/$key.audio")
            val theme = if (file.exists() && System.currentTimeMillis() - file.lastModified() < 7L * 86_400_000) file
                else runCatching { api.theme(id, file) }.getOrNull()
            if (theme == null || playing != key) return@launch
            val p = ExoPlayer.Builder(context).build().apply {
                setAudioAttributes(AudioAttributes.Builder().setUsage(C.USAGE_MEDIA).setContentType(C.AUDIO_CONTENT_TYPE_MUSIC).build(), false)
                volume = 0f
                setMediaItem(MediaItem.fromUri(theme.toURI().toString()))
                addListener(object : Player.Listener {
                    override fun onPlaybackStateChanged(state: Int) {
                        if (state == Player.STATE_ENDED && player === this@apply) {
                            done = playing
                            playing = null
                            release(this@apply)
                        }
                    }
                })
                prepare()
                play()
            }
            player = p
            // Fade in over a second and a half.
            for (i in 1..30) {
                delay(50)
                if (player !== p) return@launch
                p.volume = VOLUME * i / 30
            }
        }
    }

    /** The details page closed: fade out, unless another page for the same title opens straight away. */
    fun leave() {
        stopping?.cancel()
        stopping = scope.launch {
            delay(400)
            playing = null
            fadeOut()
        }
    }

    /** A video is starting: stop now, and don't play this theme again on the way back. */
    fun stop() {
        stopping?.cancel()
        stopping = null
        playing?.let { done = it }
        playing = null
        fadeOut()
    }

    private fun fadeOut() {
        job?.cancel()
        job = null
        val p = player ?: return
        player = null
        scope.launch {
            val start = p.volume
            for (i in 9 downTo 0) {
                p.volume = start * i / 10
                delay(40)
            }
            release(p)
        }
    }

    private fun release(p: ExoPlayer) {
        if (player === p) player = null
        p.stop()
        p.release()
    }
}
