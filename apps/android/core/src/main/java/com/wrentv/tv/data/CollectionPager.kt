package com.wrentv.tv.data

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch

/**
 * A collection's one shelf, loaded a page at a time as it's browsed toward
 * the end. Pagers live while the app runs ([of]), so going back to a
 * collection finds the pages it had.
 */
class CollectionPager private constructor(val id: String, val sort: String) {
    var collection by mutableStateOf<WrenCollection?>(null)
        private set
    /** Loaded so far; only ever appended to, so cards keep their focus. */
    val entries = mutableStateListOf<TitleView>()
    var total by mutableIntStateOf(-1)
        private set
    var countLabel by mutableStateOf("")
        private set
    var loading by mutableStateOf(false)
        private set
    /** Why the last page didn't load; browsing on retries it. */
    var error by mutableStateOf<String?>(null)
        private set
    private var tick = -1

    val done: Boolean get() = total >= 0 && entries.size >= total

    /** Loads the first page, again after [refresh] changes. */
    fun start(api: WrenTVApi, scope: CoroutineScope, refresh: Int) {
        if (tick == refresh && (total >= 0 || loading)) return
        tick = refresh
        entries.clear()
        total = -1
        error = null
        load(api, scope)
    }

    /** Loads the next page when [index] is within [Ahead] of the end. */
    fun near(index: Int, api: WrenTVApi, scope: CoroutineScope) {
        if (index >= entries.size - Ahead && error == null) load(api, scope)
    }

    fun retry(api: WrenTVApi, scope: CoroutineScope) {
        error = null
        load(api, scope)
    }

    private fun load(api: WrenTVApi, scope: CoroutineScope) {
        if (loading || done) return
        loading = true
        val from = entries.size
        scope.launch {
            try {
                val p = api.collectionPage(id, sort, from)
                if (entries.size == from) {
                    collection = p.collection
                    countLabel = p.countLabel
                    val seen = entries.mapTo(HashSet()) { it.key }
                    entries += p.entries.filter { seen.add(it.key) }
                    total = if (p.entries.isEmpty()) entries.size else p.total
                }
                error = null
            } catch (e: Exception) {
                error = e.message ?: "Couldn't load"
            } finally {
                loading = false
            }
        }
    }

    companion object {
        const val PageSize = 40
        const val Ahead = 15
        private val pagers = HashMap<String, CollectionPager>()

        fun of(id: String, sort: String): CollectionPager = pagers.getOrPut("$id:$sort") { CollectionPager(id, sort) }
    }
}

/** A collection entry's identity: the library item, else the TMDB title. */
val TitleView.key: String get() = item?.id ?: id
