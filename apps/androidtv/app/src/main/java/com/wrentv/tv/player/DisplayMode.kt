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
 * which shows as small, regular stutters. The resolution is left as it is.
 */
object DisplayMode {
    /** The mode to use for [fps], or null when the current one is already right (or nothing fits). */
    fun pick(display: Display, fps: Float): Display.Mode? {
        val current = display.mode
        val same = display.supportedModes.filter {
            it.physicalWidth == current.physicalWidth && it.physicalHeight == current.physicalHeight
        }
        fun fits(rate: Float, multiple: Int) = abs(rate - fps * multiple) < 0.01f * multiple
        if ((1..3).any { fits(current.refreshRate, it) }) return null
        val best = same.firstOrNull { fits(it.refreshRate, 1) }
            ?: (2..3).firstNotNullOfOrNull { k -> same.firstOrNull { fits(it.refreshRate, k) } }
            ?: return null
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
