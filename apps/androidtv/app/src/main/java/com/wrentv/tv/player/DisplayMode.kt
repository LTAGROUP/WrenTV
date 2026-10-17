package com.wrentv.tv.player

import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.view.Display
import kotlin.math.abs
import kotlin.math.roundToInt

/**
 * Switches the TV to a refresh rate the video plays evenly on (23.976 fps at
 * 23.976 Hz, not 59.94), like Plex and Kodi do. Fitting film to a 60 Hz output
 * makes the player nudge frames against the audio clock every few seconds,
 * which shows as small, regular stutters. The resolution is left as it is,
 * and the rate is never dropped lower than it needs to be.
 */
object DisplayMode {
    /**
     * The mode to use for [fps], or null when the current one is already right
     * (or nothing fits). Of the rates the video divides evenly into, the highest
     * wins: 25 fps goes to 50 Hz and 30 to 60, not down to 25 or 30 Hz. Film has
     * no multiple a TV usually offers below 120 Hz, so it still gets 24 Hz.
     */
    fun pick(display: Display, fps: Float): Display.Mode? {
        val current = display.mode
        fun multiple(rate: Float) = (1..5).any { k -> abs(rate - fps * k) < 0.01f * k }
        val best = display.supportedModes
            .filter { it.physicalWidth == current.physicalWidth && it.physicalHeight == current.physicalHeight }
            .filter { multiple(it.refreshRate) }
            .maxByOrNull { it.refreshRate }
            ?: return null
        if (multiple(current.refreshRate) && current.refreshRate >= best.refreshRate - 0.5f) return null
        return best.takeIf { it.modeId != current.modeId }
    }

    /** Asks for the mode matching [fps]; 0 or less goes back to the system's choice. */
    fun apply(context: Context, fps: Float) {
        val activity = context.findActivity() ?: return
        val window = activity.window
        val attrs = window.attributes
        val id = if (fps > 0) {
            val display = activity.display ?: return
            pick(display, fps)?.modeId ?: return
        } else {
            0
        }
        if (attrs.preferredDisplayModeId == id) return
        attrs.preferredDisplayModeId = id
        window.attributes = attrs
    }

    /** Film, PAL and NTSC rates, snapped so a container's 23.98 counts as 24000/1001. */
    fun normalize(fps: Float): Float {
        val known = floatArrayOf(24000f / 1001, 24f, 25f, 30000f / 1001, 30f, 50f, 60000f / 1001, 60f)
        return known.firstOrNull { abs(it - fps) < 0.02f } ?: (fps * 1000).roundToInt() / 1000f
    }

    private tailrec fun Context.findActivity(): Activity? = when (this) {
        is Activity -> this
        is ContextWrapper -> baseContext.findActivity()
        else -> null
    }
}
