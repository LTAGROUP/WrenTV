package com.wrentv.tv.player

import android.text.Html
import androidx.media3.common.text.Cue

/**
 * A subtitle file the app draws over the video itself: the subtitle-site
 * downloads, which the server keeps lining up with the file while it plays.
 * A side-loaded ExoPlayer track would hold playback until the file is whole.
 */
class SideSubtitle(private val cues: List<Line>) {
    data class Line(val start: Double, val end: Double, val text: String)

    /** The lines showing at t seconds, joined; null when none is. */
    fun at(t: Double): String? {
        var lo = 0
        var hi = cues.size
        while (lo < hi) {
            val m = (lo + hi) / 2
            if (cues[m].start <= t) lo = m + 1 else hi = m
        }
        // A long line (a sign) can still be showing behind later ones.
        val on = (maxOf(0, lo - 20) until lo).map { cues[it] }.filter { it.end > t }
        return on.takeIf { it.isNotEmpty() }?.joinToString("\n") { it.text }
    }

    companion object {
        /** Reads WebVTT, which is what the server hands out. */
        fun parse(vtt: String): SideSubtitle {
            val out = mutableListOf<Line>()
            for (block in vtt.replace("\r", "").split("\n\n")) {
                val lines = block.trim('\n').split("\n")
                val i = lines.indexOfFirst { "-->" in it }
                if (i < 0) continue
                val (a, z) = lines[i].split("-->", limit = 2)
                val start = time(a) ?: continue
                val end = time(z.trim().substringBefore(' ')) ?: continue
                val text = lines.drop(i + 1).joinToString("\n").trim()
                if (text.isNotEmpty() && end > start) out += Line(start, end, text)
            }
            return SideSubtitle(out.sortedBy { it.start })
        }

        private fun time(s: String): Double? {
            val parts = s.trim().split(":")
            if (parts.size !in 2..3) return null
            var t = 0.0
            for (p in parts) t = t * 60 + (p.replace(',', '.').toDoubleOrNull() ?: return null)
            return t
        }

        /** A line as cues for the player's subtitle view, its <i> and <b> kept. */
        fun cues(text: String?): List<Cue> =
            if (text == null) emptyList()
            else listOf(Cue.Builder().setText(Html.fromHtml(text.replace("\n", "<br>"), Html.FROM_HTML_MODE_LEGACY)).build())
    }
}
