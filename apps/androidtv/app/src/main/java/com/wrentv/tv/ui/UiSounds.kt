package com.wrentv.tv.ui

import android.content.Context
import android.media.AudioAttributes
import android.media.SoundPool
import android.os.SystemClock
import com.wrentv.tv.R

/**
 * Light ticks as focus moves, for selecting and for going back, like the
 * sounds Plex and the Apple TV make. Quiet, and silent while a video plays.
 */
object UiSounds {
    enum class Kind { Move, Select, Back }

    private var pool: SoundPool? = null
    private val ids = IntArray(Kind.entries.size)
    private var lastMove = 0L

    /** Off in Settings. */
    var enabled = true
    /** Held while the player is showing. */
    var muted = false

    fun load(context: Context) {
        if (pool != null) return
        val attrs = AudioAttributes.Builder()
            .setUsage(AudioAttributes.USAGE_ASSISTANCE_SONIFICATION)
            .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
            .build()
        val p = SoundPool.Builder().setMaxStreams(3).setAudioAttributes(attrs).build()
        ids[Kind.Move.ordinal] = p.load(context, R.raw.ui_move, 1)
        ids[Kind.Select.ordinal] = p.load(context, R.raw.ui_select, 1)
        ids[Kind.Back.ordinal] = p.load(context, R.raw.ui_back, 1)
        pool = p
    }

    fun play(kind: Kind) {
        if (!enabled || muted) return
        val p = pool ?: return
        if (kind == Kind.Move) {
            // Holding a direction repeats fast; a click on every step turns into a buzz.
            val now = SystemClock.uptimeMillis()
            if (now - lastMove < 60) return
            lastMove = now
        }
        val volume = when (kind) {
            Kind.Move -> 0.25f
            Kind.Select -> 0.4f
            Kind.Back -> 0.34f
        }
        p.play(ids[kind.ordinal], volume, volume, 1, 0, 1f)
    }
}
