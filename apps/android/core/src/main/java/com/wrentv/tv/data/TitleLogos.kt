package com.wrentv.tv.data

import androidx.compose.runtime.mutableStateMapOf
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * Clear logos for TMDB titles the server doesn't have yet, so a hero can show
 * one the moment its card takes focus. Cards ask as they're laid out; asks
 * made together go to the server in one request.
 */
class TitleLogos(private val scope: CoroutineScope, private val api: () -> WrenTVApi?) {
    /** A title's logo path, "" when it has none; missing while unknown. */
    private val known = mutableStateMapOf<String, String>()
    private val asked = mutableSetOf<String>()
    private val pending = LinkedHashSet<String>()
    private var sending = false

    /** The title's logo once known: its library copy's, else TMDB's. Reading it recomposes when it arrives. */
    operator fun get(t: TitleView): String? = t.item?.logoPath ?: known[t.id]?.ifEmpty { null }

    fun want(t: TitleView) {
        if (t.item != null || t.id in known || !asked.add(t.id)) return
        pending += t.id
        if (sending) return
        sending = true
        scope.launch {
            // Let the rest of the row's cards ask before sending.
            delay(60)
            while (pending.isNotEmpty()) {
                val batch = pending.take(30)
                pending.removeAll(batch.toSet())
                val got = runCatching { api()?.titleLogos(batch) }.getOrNull()
                if (got == null) asked.removeAll(batch.toSet()) // asked again next time it's shown
                else {
                    known.putAll(got)
                    // The server couldn't look these up just now.
                    asked.removeAll(batch.filterNot { it in got }.toSet())
                }
            }
            sending = false
        }
    }
}
