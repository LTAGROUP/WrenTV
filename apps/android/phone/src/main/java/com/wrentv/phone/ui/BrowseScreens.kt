package com.wrentv.phone.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyGridScope
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.Sort
import androidx.compose.material.icons.rounded.AutoAwesome
import androidx.compose.material.icons.rounded.Bookmark
import androidx.compose.material.icons.rounded.BookmarkBorder
import androidx.compose.material.icons.rounded.Check
import androidx.compose.material.icons.rounded.ChevronRight
import androidx.compose.material.icons.rounded.CollectionsBookmark
import androidx.compose.material.icons.rounded.FilterList
import androidx.compose.material.icons.rounded.Folder
import androidx.compose.material.icons.rounded.Movie
import androidx.compose.material.icons.rounded.Sell
import androidx.compose.material.icons.rounded.Tv
import androidx.compose.material.icons.rounded.Visibility
import androidx.compose.material.icons.rounded.VisibilityOff
import androidx.compose.material.icons.rounded.Warning
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.wrentv.tv.data.CatalogPage
import com.wrentv.tv.data.CollectionDetail
import com.wrentv.tv.data.GenreCount
import com.wrentv.tv.data.Item
import com.wrentv.tv.data.Library
import com.wrentv.tv.data.TitleView
import com.wrentv.tv.data.WrenCollection
import kotlinx.coroutines.CancellationException

/** A grid of cards filling the width, iPhone-style. */
@Composable
fun CardGrid(content: LazyGridScope.() -> Unit) {
    val m = metrics()
    LazyVerticalGrid(
        GridCells.Adaptive(m.gridMin), Modifier.fillMaxSize(), rememberLazyGridState(),
        contentPadding = PaddingValues(m.gutter),
        horizontalArrangement = Arrangement.spacedBy(12.dp), verticalArrangement = Arrangement.spacedBy(18.dp),
        content = content,
    )
}

fun LazyGridScope.fullWidth(content: @Composable () -> Unit) = item(span = { GridItemSpan(maxLineSpan) }) { content() }

/** A dropdown of choices behind an icon in the top bar. */
@Composable
fun <T> PickerMenu(icon: ImageVector, label: String, options: List<Pair<T, String>>, selected: T, pick: (T) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Box {
        IconButton({ open = true }) { Icon(icon, label) }
        DropdownMenu(open, { open = false }) {
            options.forEach { (v, name) ->
                DropdownMenuItem({ Text(name) }, { open = false; pick(v) }, trailingIcon = { if (v == selected) Icon(Icons.Rounded.Check, null) })
            }
        }
    }
}

/** Movies or TV Shows when the server streams: TMDB's whole catalog, loading more as you scroll. */
@Composable
fun CatalogScreen(type: String) {
    val state = LocalAppState.current
    val label = if (type == "show") "TV Shows" else "Movies"
    var sort by rememberSaveable { mutableStateOf(state.store.string("catalogSort", "popular")) }
    var genre by rememberSaveable(type) { mutableStateOf<Int?>(null) }
    var genres by remember { mutableStateOf<List<CatalogPage.Genre>>(emptyList()) }
    var titles by remember(type, sort, genre) { mutableStateOf<List<TitleView>>(emptyList()) }
    var page by remember(type, sort, genre) { mutableStateOf(0) }
    var total by remember(type, sort, genre) { mutableStateOf(1) }
    var loading by remember { mutableStateOf(false) }
    var enabled by remember { mutableStateOf(true) }
    var error by remember { mutableStateOf<String?>(null) }
    var want by remember(type, sort, genre) { mutableStateOf(1) }

    LaunchedEffect(type, sort, genre, want, state.api) {
        val api = state.api ?: return@LaunchedEffect
        if (page >= total || page >= want) return@LaunchedEffect
        loading = true
        try {
            val res = api.catalog(type, sort, genre, page + 1)
            enabled = res.enabled
            if (res.genres.isNotEmpty()) genres = res.genres
            val seen = titles.map { it.id }.toHashSet()
            titles = titles + res.items.filter { seen.add(it.id) }
            page = res.page
            total = res.totalPages
            error = null
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            error = e.message
        }
        loading = false
    }

    Page(label, genres.firstOrNull { it.id == genre }?.name, actions = {
        PickerMenu(Icons.Rounded.Sell, "Genre", listOf<Pair<Int?, String>>(null to "All genres") + genres.map { it.id to it.name }, genre) { genre = it }
        PickerMenu(Icons.AutoMirrored.Rounded.Sort, "Sort", listOf("popular" to "Popular", "top_rated" to "Top Rated", "new" to "Newest"), sort) {
            sort = it; state.store.setString("catalogSort", it)
        }
    }) {
        CardGrid {
            items(titles, { it.id }) { v ->
                TitleCard(v)
                if (v.id == titles.lastOrNull()?.id) LaunchedEffect(v.id) { want = page + 1 }
            }
            when {
                loading -> fullWidth { Loading(Modifier.fillMaxWidth().padding(vertical = if (titles.isEmpty()) 200.dp else 24.dp)) }
                !enabled -> fullWidth {
                    EmptyState(Icons.Rounded.AutoAwesome, "$label need TMDB",
                        if (state.isAdmin) "Add a TMDB API key in the server's web dashboard under Settings." else "The server owner hasn't set up TMDB yet.")
                }
                error != null && titles.isEmpty() -> fullWidth { EmptyState(Icons.Rounded.Warning, "Couldn't load", error) }
                titles.isEmpty() -> fullWidth { EmptyState(Icons.Rounded.Movie, "Nothing matches") }
            }
        }
    }
}

/** The Library tab on servers without streaming: Watchlist, Collections, then the libraries. */
@Composable
fun LibrariesScreen() {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    Page("Library") {
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(vertical = 8.dp)) {
            item { NavRow(Icons.Rounded.Bookmark, "Watchlist") { nav.push(Route.Watchlist) } }
            item { NavRow(Icons.Rounded.CollectionsBookmark, "Collections") { nav.push(Route.Collections) } }
            item { SectionHeader("Libraries") }
            items(state.libraries, { it.id }) { lib ->
                NavRow(if (lib.type == "shows") Icons.Rounded.Tv else Icons.Rounded.Movie, lib.name, lib.itemCount?.toString()) { nav.push(Route.LibraryPage(lib)) }
            }
            if (state.libraries.isEmpty()) item { EmptyState(Icons.Rounded.Folder, "No libraries", "Ask the server owner to share a library.") }
        }
    }
}

@Composable
fun SectionHeader(text: String) {
    Text(text.uppercase(), Modifier.padding(start = 20.dp, end = 20.dp, top = 20.dp, bottom = 6.dp), color = Wren.secondary, style = MaterialTheme.typography.labelMedium)
}

@Composable
fun NavRow(icon: ImageVector?, title: String, value: String? = null, tint: Color = Color.White, onClick: (() -> Unit)?) {
    Row(
        Modifier.fillMaxWidth().clickable(enabled = onClick != null) { onClick?.invoke() }.padding(horizontal = 20.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        if (icon != null) Icon(icon, null, tint = if (tint == Color.White) Wren.ember else tint)
        Text(title, Modifier.weight(1f), color = tint, style = MaterialTheme.typography.bodyLarge)
        if (value != null) Text(value, color = Wren.secondary)
        if (tint == Color.White && onClick != null) Icon(Icons.Rounded.ChevronRight, null, tint = Wren.secondary)
    }
}

private val SortOptions = listOf("title" to "Title", "added" to "Recently Added", "year" to "Year (newest)", "year_asc" to "Year (oldest)", "rating" to "Rating")

/** One library's titles, with sort, genre and unwatched filters. */
@Composable
fun LibraryScreen(library: Library) {
    val state = LocalAppState.current
    var sort by rememberSaveable { mutableStateOf(state.store.string("librarySort", "title")) }
    var genre by rememberSaveable { mutableStateOf<String?>(null) }
    var unwatched by rememberSaveable { mutableStateOf(false) }
    var genres by remember { mutableStateOf<List<GenreCount>>(emptyList()) }
    var items by remember { mutableStateOf<List<Item>?>(null) }
    LaunchedEffect(library.id) { genres = runCatching { state.api?.genres(library.id) }.getOrNull().orEmpty() }
    LaunchedEffect(library.id, sort, genre, unwatched, state.refreshTick) {
        runCatching { state.api?.items(library.id, sort, genre, unwatched) }.getOrNull()?.let { items = it.items }
    }
    val list = items
    Page(library.name, list?.let { "${it.size} ${if (library.type == "shows") "shows" else "movies"}" }, actions = {
        IconButton({ unwatched = !unwatched }) { Icon(if (unwatched) Icons.Rounded.VisibilityOff else Icons.Rounded.Visibility, "Unwatched only") }
        PickerMenu(Icons.Rounded.FilterList, "Genre", listOf<Pair<String?, String>>(null to "All genres") + genres.map { it.genre to it.genre }, genre) { genre = it }
        PickerMenu(Icons.AutoMirrored.Rounded.Sort, "Sort", SortOptions, sort) { sort = it; state.store.setString("librarySort", it) }
    }) {
        if (list == null) Loading()
        else CardGrid {
            items(list, { it.id }) { PosterCard(it) }
            if (list.isEmpty()) fullWidth { EmptyState(Icons.Rounded.Movie, "Nothing here", if (unwatched) "Everything here has been watched." else null) }
        }
    }
}

@Composable
fun WatchlistScreen() {
    val state = LocalAppState.current
    var items by remember { mutableStateOf<List<Item>?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var hideWatched by remember { mutableStateOf(state.store.bool("watchlistHideWatched", true)) }
    LaunchedEffect(state.refreshTick, state.api) {
        try { items = state.api?.watchlist().orEmpty(); error = null } catch (e: CancellationException) { throw e } catch (e: Exception) { if (items == null) error = e.message }
    }
    Page("Watchlist", actions = {
        IconButton({ hideWatched = !hideWatched; state.store.setBool("watchlistHideWatched", hideWatched) }) {
            Icon(if (hideWatched) Icons.Rounded.VisibilityOff else Icons.Rounded.Visibility, "Hide watched")
        }
    }) {
        val list = items
        when {
            list != null -> {
                val shown = list.filter { !hideWatched || !it.isWatched }
                CardGrid {
                    items(shown, { it.id }) { PosterCard(it, onWatchlist = true) }
                    if (shown.isEmpty()) fullWidth { EmptyState(Icons.Rounded.BookmarkBorder, "Your watchlist is empty", "Add movies and shows from their pages to find them here.", Modifier.fillMaxWidth().padding(top = 60.dp)) }
                }
            }
            error != null -> EmptyState(Icons.Rounded.Warning, "Couldn't load", error)
            else -> Loading()
        }
    }
}

@Composable
fun CollectionsScreen() {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    var list by remember { mutableStateOf<List<WrenCollection>?>(null) }
    LaunchedEffect(state.refreshTick, state.api) { list = runCatching { state.api?.collections() }.getOrNull().orEmpty() }
    Page("Collections") {
        val l = list
        if (l == null) Loading()
        else CardGrid {
            items(l, { it.id }) { c ->
                Column(Modifier.clickable { nav.push(Route.Collection(c.id, c.name)) }, verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    RemoteImage(state.api?.imageUrl(c.posterPath), Modifier.fillMaxWidth().aspectRatio(2f / 3f).clip(RoundedCornerShape(10.dp)))
                    Text(c.name, maxLines = 1, style = MaterialTheme.typography.bodyMedium)
                    c.itemCount?.let { Text("$it titles", color = Wren.secondary, style = MaterialTheme.typography.bodySmall) }
                }
            }
            if (l.isEmpty()) fullWidth { EmptyState(Icons.Rounded.CollectionsBookmark, "No collections yet") }
        }
    }
}

@Composable
fun CollectionScreen(id: String, name: String) {
    val state = LocalAppState.current
    var detail by remember { mutableStateOf<CollectionDetail?>(null) }
    LaunchedEffect(id, state.refreshTick) { detail = runCatching { state.api?.collection(id) }.getOrNull() }
    Page(name, detail?.collection?.description?.takeIf { it.isNotEmpty() }) {
        val d = detail
        if (d == null) Loading()
        else CardGrid {
            val titles = d.titles
            if (titles != null && state.canStream) items(titles.distinctBy { it.id }, { it.id }) { TitleCard(it) }
            else {
                items(d.items.distinctBy { it.id }, { it.id }) { PosterCard(it) }
                d.missing?.let { missing -> items(missing.distinctBy { it.id }, { "m" + it.id }) { TitleCard(it) } }
            }
            d.collection.syncError?.takeIf { it.isNotEmpty() }?.let { fullWidth { Text(it, color = Wren.ember) } }
        }
    }
}

@Composable
fun GenresScreen(libraryId: String?) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    var list by remember { mutableStateOf<List<GenreCount>?>(null) }
    LaunchedEffect(libraryId, state.api) { list = runCatching { state.api?.genres(libraryId) }.getOrNull().orEmpty() }
    Page("Genres") {
        val l = list
        if (l == null) Loading()
        else LazyVerticalGrid(GridCells.Adaptive(160.dp), Modifier.fillMaxSize(), contentPadding = PaddingValues(16.dp),
            horizontalArrangement = Arrangement.spacedBy(12.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            items(l, { it.genre }) { g ->
                Box(Modifier.aspectRatio(16f / 9f).clip(RoundedCornerShape(10.dp)).clickable { nav.push(Route.Genre(g.genre, libraryId)) }) {
                    RemoteImage(state.api?.imageUrl(g.backdropPath, "w500"), Modifier.fillMaxSize())
                    Box(Modifier.fillMaxSize().background(Brush.verticalGradient(listOf(Color.Transparent, Color.Black.copy(alpha = 0.75f)))))
                    Column(Modifier.align(Alignment.BottomStart).padding(10.dp)) {
                        Text(g.genre, fontWeight = FontWeight.Bold)
                        Text("${g.count} titles", color = Color.White.copy(alpha = 0.7f), style = MaterialTheme.typography.bodySmall)
                    }
                }
            }
        }
    }
}

@Composable
fun GenreScreen(genre: String, libraryId: String?) {
    val state = LocalAppState.current
    var sort by rememberSaveable { mutableStateOf("title") }
    var items by remember { mutableStateOf<List<Item>?>(null) }
    LaunchedEffect(genre, libraryId, sort, state.refreshTick) { items = runCatching { state.api?.genreItems(genre, libraryId, sort = sort) }.getOrNull()?.items.orEmpty() }
    Page(genre, actions = { PickerMenu(Icons.AutoMirrored.Rounded.Sort, "Sort", SortOptions, sort) { sort = it } }) {
        val l = items
        if (l == null) Loading() else CardGrid { items(l, { it.id }) { PosterCard(it) } }
    }
}

/** Opens a title the server doesn't have yet: adds it to the streaming library, then shows it. */
@Composable
fun StreamTitleScreen(r: Route.StreamTitle) {
    val state = LocalAppState.current
    var item by remember { mutableStateOf<Item?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(r) {
        try {
            item = state.api?.addStreamTitle(r.tmdbId, r.mediaType)
            state.reloadLibraries()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            error = e.message
        }
    }
    val it = item
    when {
        it != null -> DetailScreen(it.id, it)
        error != null -> Page(r.title) { EmptyState(Icons.Rounded.Warning, "Couldn't open ${r.title}", error) }
        else -> Page(r.title) {
            Column(Modifier.fillMaxSize(), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
                Loading(Modifier.size(60.dp))
                Text("Opening ${r.title}…", color = Wren.secondary)
            }
        }
    }
}

