package com.wrentv.tv.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.AutoAwesome
import androidx.compose.material.icons.rounded.Movie
import androidx.compose.material.icons.rounded.WifiOff
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.wrentv.tv.data.DiscoverHome
import com.wrentv.tv.data.HomeResponse
import com.wrentv.tv.data.Item
import com.wrentv.tv.data.TitleView
import com.wrentv.tv.data.TrendingResponse
import com.wrentv.tv.data.WrenTVException
import com.wrentv.tv.ui.EmptyState
import com.wrentv.tv.ui.LandscapeCard
import com.wrentv.tv.ui.Loading
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.PosterCard
import com.wrentv.tv.ui.ReportScroll
import com.wrentv.tv.ui.Route
import com.wrentv.tv.ui.Shelf
import com.wrentv.tv.ui.TitleCard
import com.wrentv.tv.ui.TopBarSpace
import com.wrentv.tv.ui.playTarget
import com.wrentv.tv.ui.rememberFocus
import com.wrentv.tv.ui.rememberLoaded

@Composable
fun HomeScreen() {
    val state = LocalAppState.current
    // Servers from before discovery don't have its endpoint.
    var unsupported by remember { mutableStateOf(false) }
    if (state.canStream && !unsupported) DiscoverHomeScreen { unsupported = true } else LibraryHomeScreen()
}

@Composable
private fun DiscoverHomeScreen(onUnsupported: () -> Unit) {
    val state = LocalAppState.current
    val home = rememberLoaded<DiscoverHome>("home:discover", state.refreshTick) {
        try {
            state.api?.discoverHome()
        } catch (e: WrenTVException) {
            if (e.status == 404) onUnsupported()
            throw e
        }
    }
    val menu = rememberItemMenu()
    val list = rememberLazyListState()
    ReportScroll(list)
    val h = home.value
    LazyColumn(Modifier.fillMaxSize(), state = list, contentPadding = PaddingValues(bottom = 48.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        when {
            h != null -> {
                if (h.hero.isNotEmpty()) item("hero") { Hero(h.hero.map { it.heroEntry() }, label = "Trending this week") }
                else item("space") { Spacer(Modifier.height(TopBarSpace)) }
                continueWatching(h.continueWatching, menu)
                h.rows.forEachIndexed { i, row ->
                    if (i == 1) itemShelf("recent", "Recently Watched", h.recentlyWatched, menu)
                    titleShelf(row.id, row.title, row.items, ranked = row.id == "trending")
                }
                if (h.rows.size < 2) itemShelf("recent", "Recently Watched", h.recentlyWatched, menu)
                if (!h.enabled) {
                    item("empty") {
                        EmptyState("Discovery needs TMDB",
                            if (state.isAdmin) "Add a TMDB API key in Settings → Server Settings." else "The server owner hasn't set up TMDB yet.",
                            icon = Icons.Rounded.AutoAwesome)
                    }
                } else if (h.rows.isEmpty()) {
                    item("empty") { EmptyState("Nothing to show", "TMDB didn't answer. Try again in a moment.", icon = Icons.Rounded.WifiOff) }
                }
            }
            home.error != null -> item("error") { Spacer(Modifier.height(TopBarSpace)); EmptyState("Couldn't load", home.error, icon = Icons.Rounded.WifiOff) }
            else -> item("loading") { Loading() }
        }
    }
}

@Composable
private fun LibraryHomeScreen() {
    val state = LocalAppState.current
    val home = rememberLoaded<HomeResponse>("home:library", state.refreshTick) { state.api?.home() }
    val trending = rememberLoaded<TrendingResponse>("home:trending") { state.api?.trending() }
    val menu = rememberItemMenu()
    val list = rememberLazyListState()
    ReportScroll(list)
    val h = home.value
    LazyColumn(Modifier.fillMaxSize(), state = list, contentPadding = PaddingValues(bottom = 48.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        when {
            h != null -> {
                val picks = featured(h)
                if (picks.isNotEmpty()) item("hero") { Hero(picks.map { it.heroEntry() }) }
                else item("space") { Spacer(Modifier.height(TopBarSpace)) }
                continueWatching(h.continueWatching, menu)
                trending.value?.takeIf { it.enabled && it.items.isNotEmpty() }?.let { t ->
                    titleShelf("trending", "Trending This Week", t.items, ranked = true,
                        subtitle = "${t.items.count { it.item != null }} in your library")
                }
                h.collections.orEmpty().forEach { row ->
                    item("collection:${row.collection.id}") {
                        val nav = LocalNavigator.current
                        Shelf(row.collection.name) {
                            items(row.items, key = { it.id }) { item ->
                                PosterCard(item, { nav.push(Route.Detail(item.id, item)) },
                                    Modifier.rememberFocus("c:${row.collection.id}:${item.id}"), onLongClick = { menu.open(item) })
                            }
                            row.missing.orEmpty().forEach { t -> item(key = t.id) { TitleCard(t) } }
                        }
                    }
                }
                h.recentlyAdded.forEach { shelf ->
                    itemShelf("recent:${shelf.library.id}", "Recently Added in ${shelf.library.name}", shelf.items, menu)
                }
                h.topRated?.takeIf { it.size >= 4 }?.let { itemShelf("top", "Top Rated in Your Library", it, menu) }
                h.genres.orEmpty().forEach { row -> itemShelf("genre:${row.genre}", row.genre, row.items, menu) }
                if (h.continueWatching.isEmpty() && h.recentlyAdded.isEmpty()) {
                    item("empty") {
                        EmptyState("Nothing here yet",
                            if (state.isAdmin) "Add a library from Settings → Libraries." else "Ask the server owner to share a library.",
                            icon = Icons.Rounded.Movie)
                    }
                }
            }
            home.error != null -> item("error") { Spacer(Modifier.height(TopBarSpace)); EmptyState("Couldn't load", home.error, icon = Icons.Rounded.WifiOff) }
            else -> item("loading") { Loading() }
        }
    }
}

/** A handful of well-rated and new titles with artwork, a different set each hour. */
private fun featured(home: HomeResponse): List<Item> {
    val pool = ((home.topRated ?: emptyList()) + home.recentlyAdded.flatMap { it.items })
        .filter { it.backdropPath != null }.distinctBy { it.id }
    if (pool.isEmpty()) return emptyList()
    val start = (System.currentTimeMillis() / 3_600_000 % pool.size).toInt()
    return (0 until minOf(6, pool.size)).map { pool[(start + it * 3) % pool.size] }.distinctBy { it.id }
}

private fun LazyListScope.continueWatching(items: List<Item>, menu: ItemMenuState) {
    if (items.isEmpty()) return
    item("continue") {
        val nav = LocalNavigator.current
        Shelf("Continue Watching") {
            items(items, key = { it.id }) { item ->
                LandscapeCard(item, { nav.play(item.playTarget()) }, Modifier.rememberFocus("cw:${item.id}"), onLongClick = { menu.open(item) })
            }
        }
    }
}

private fun LazyListScope.itemShelf(key: String, title: String, items: List<Item>, menu: ItemMenuState) {
    if (items.isEmpty()) return
    item(key) {
        val nav = LocalNavigator.current
        Shelf(title) {
            items(items, key = { it.id }) { item ->
                PosterCard(item, { nav.push(Route.Detail(item.id, item)) }, Modifier.rememberFocus("$key:${item.id}"), onLongClick = { menu.open(item) })
            }
        }
    }
}

private fun LazyListScope.titleShelf(key: String, title: String, items: List<TitleView>, ranked: Boolean = false, subtitle: String? = null) {
    if (items.isEmpty()) return
    item(key) {
        Shelf(title, subtitle = subtitle) {
            itemsIndexed(items, key = { _, t -> t.id }) { i, t ->
                TitleCard(t, Modifier.rememberFocus("$key:${t.id}"), rank = if (ranked && i < 10) i + 1 else null)
            }
        }
    }
}
