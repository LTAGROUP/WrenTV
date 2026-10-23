package com.wrentv.tv.ui.screens

import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Movie
import androidx.compose.material.icons.rounded.WifiOff
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Modifier
import com.wrentv.tv.data.Company
import com.wrentv.tv.data.StudioRow
import com.wrentv.tv.data.TitleView
import com.wrentv.tv.data.WrenTVApi
import com.wrentv.tv.ui.CinematicBrowse
import com.wrentv.tv.ui.EmptyState
import com.wrentv.tv.ui.Loading
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.PageCache
import com.wrentv.tv.ui.Shelf
import com.wrentv.tv.ui.TitleCard
import com.wrentv.tv.ui.rememberFocus
import com.wrentv.tv.ui.rememberLoaded
import kotlinx.coroutines.CancellationException

/**
 * A studio's titles over their artwork: popular, top rated and recent movies,
 * then popular and top rated shows. Each shelf loads more as it nears its end.
 */
@Composable
fun StudioScreen(id: Int, name: String) {
    val state = LocalAppState.current
    val page = rememberLoaded<Company>("studio:$id") { state.api?.company(id) }
    val list = rememberLazyListState()
    val c = page.value
    val hero = HeroEntry(
        id = "studio:$id", title = c?.name ?: name, backdrop = c?.backdropPath, logo = null,
        meta = listOfNotNull(c?.headquarters?.takeIf { it.isNotBlank() }, c?.originCountry?.takeIf { it.isNotBlank() }),
        badges = emptyList(), overview = c?.description?.takeIf { it.isNotBlank() }, item = null,
    )
    CinematicBrowse(list, fallback = hero, label = "Studio") {
        when {
            c != null -> {
                c.rows.forEach { r -> row("row:${r.id}") { StudioShelf(id, r) } }
                if (c.rows.isEmpty()) extra("empty") { EmptyState("No movies or shows listed", null, icon = Icons.Rounded.Movie) }
            }
            page.error != null -> extra("error") { EmptyState("Couldn't load", page.error, icon = Icons.Rounded.WifiOff) }
            else -> extra("loading") { Loading() }
        }
    }
}

/** A shelf's titles so far. Kept with the page, so Back returns to the same card. */
private class ShelfPages(first: StudioRow) {
    val items = mutableStateListOf<TitleView>().apply { addAll(first.items.distinctBy { it.id }) }
    var page by mutableIntStateOf(1)
    var total by mutableIntStateOf(first.totalPages)
    var loading by mutableStateOf(false)

    suspend fun more(api: WrenTVApi, studio: Int, row: StudioRow) {
        if (loading || page >= total) return
        loading = true
        try {
            val res = api.catalog(row.mediaType, row.sort, null, page + 1, company = studio)
            val have = items.mapTo(HashSet()) { it.id }
            items += res.items.filter { have.add(it.id) }
            page = res.page
            total = res.totalPages
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            // Tried again when the shelf next moves.
        } finally {
            loading = false
        }
    }
}

@Composable
private fun StudioShelf(studio: Int, row: StudioRow) {
    val api = LocalAppState.current.api
    val key = "studio:$studio:${row.id}"
    val pages = remember(key) { (PageCache.values[key] as? ShelfPages) ?: ShelfPages(row).also { PageCache.values[key] = it } }
    val shelf = rememberLazyListState()
    // A couple of screenfuls before the end, the next page comes in.
    LaunchedEffect(pages) {
        snapshotFlow { (shelf.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: 0) to pages.items.size }
            .collect { (last, size) -> if (api != null && last >= size - 12) pages.more(api, studio, row) }
    }
    Shelf(row.title, state = shelf) {
        items(pages.items, key = { it.id }) { t -> TitleCard(t, Modifier.rememberFocus("$key:${t.id}")) }
    }
}
