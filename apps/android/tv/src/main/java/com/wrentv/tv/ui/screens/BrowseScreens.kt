package com.wrentv.tv.ui.screens

import com.wrentv.tv.data.DiscoverHome
import androidx.compose.material.icons.rounded.History
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyGridScope
import androidx.compose.foundation.lazy.grid.LazyGridState
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.grid.rememberLazyGridState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.Sort
import androidx.compose.material.icons.rounded.AutoAwesome
import androidx.compose.material.icons.rounded.FilterList
import androidx.compose.material.icons.rounded.Label
import androidx.compose.material.icons.rounded.Search
import androidx.compose.material.icons.rounded.VisibilityOff
import androidx.compose.material.icons.rounded.ViewCarousel
import androidx.compose.material.icons.rounded.WifiOff
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.Icon
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import com.wrentv.tv.data.CatalogPage
import com.wrentv.tv.data.CollectionDetail
import com.wrentv.tv.data.CollectionShelf
import com.wrentv.tv.data.GenreCount
import com.wrentv.tv.data.Item
import com.wrentv.tv.data.TitleView
import com.wrentv.tv.data.WrenCollection
import com.wrentv.tv.ui.ArtCard
import com.wrentv.tv.ui.Choice
import com.wrentv.tv.ui.ChoicePanel
import com.wrentv.tv.ui.Chip
import com.wrentv.tv.ui.EmptyState
import com.wrentv.tv.ui.LandscapeCard
import com.wrentv.tv.ui.Loading
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.Shelf
import com.wrentv.tv.ui.ambient
import com.wrentv.tv.ui.PageGutter
import com.wrentv.tv.ui.PosterCard
import com.wrentv.tv.ui.RemoteImage
import com.wrentv.tv.ui.ReportScroll
import com.wrentv.tv.ui.Route
import com.wrentv.tv.ui.SectionTitle
import com.wrentv.tv.ui.Spinner
import com.wrentv.tv.ui.TitleCard
import com.wrentv.tv.ui.TopBarSpace
import com.wrentv.tv.ui.TvTextField
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.heroScrim
import com.wrentv.tv.ui.rememberFocus
import com.wrentv.tv.ui.rememberLoaded
import kotlinx.coroutines.android.awaitFrame
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

private const val Columns = 6
private val CardWidth = 132.dp

val SortOptions = listOf(
    Choice("title", "Title"), Choice("added", "Recently Added"), Choice("year", "Year (newest)"),
    Choice("year_asc", "Year (oldest)"), Choice("rating", "Rating"),
)

/** Settings kept for the session, like the Mac app's remembered sort. */
private object BrowsePrefs {
    var librarySort by mutableStateOf("title")
    var catalogSort by mutableStateOf("popular")
}

/** A grid of posters under a header, with the top bar's space above. */
@Composable
private fun PosterGrid(
    grid: LazyGridState = rememberLazyGridState(),
    modifier: Modifier = Modifier,
    content: LazyGridScope.() -> Unit,
) {
    ReportScroll(grid)
    LazyVerticalGrid(
        columns = GridCells.Fixed(Columns),
        state = grid,
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = PageGutter, end = PageGutter, bottom = 48.dp),
        horizontalArrangement = Arrangement.spacedBy(20.dp),
        verticalArrangement = Arrangement.spacedBy(28.dp),
        content = content,
    )
}

private fun LazyGridScope.fullWidth(key: String, content: @Composable () -> Unit) {
    item(key = key, span = { GridItemSpan(maxLineSpan) }) { content() }
}

/** The page's title and its filter chips, under the top bar. */
private fun LazyGridScope.header(title: String, subtitle: String? = null, chips: @Composable () -> Unit = {}) {
    fullWidth("header") {
        Column(Modifier.padding(top = TopBarSpace)) {
            Text(title, style = MaterialTheme.typography.displaySmall, color = Wren.text)
            if (!subtitle.isNullOrEmpty()) Text(subtitle, style = MaterialTheme.typography.bodyLarge, color = Wren.secondary)
            Spacer(Modifier.height(16.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp), verticalAlignment = Alignment.CenterVertically) { chips() }
        }
    }
}

// Movies & TV Shows (streaming)

/** TMDB's whole catalog when the server streams, loading more as you go. */
@Composable
fun CatalogScreen(type: String) {
    val state = LocalAppState.current
    val label = if (type == "show") "TV Shows" else "Movies"
    var genre by rememberSaveable { mutableStateOf<Int?>(null) }
    val sort = BrowsePrefs.catalogSort
    val titles = remember(type, sort, genre) { mutableStateListOf<TitleView>() }
    var genres by remember { mutableStateOf<List<CatalogPage.Genre>>(emptyList()) }
    var page by remember(type, sort, genre) { mutableIntStateOf(0) }
    var total by remember(type, sort, genre) { mutableIntStateOf(1) }
    var loading by remember { mutableStateOf(false) }
    var enabled by remember { mutableStateOf(true) }
    var error by remember { mutableStateOf<String?>(null) }
    var picker by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val grid = rememberLazyGridState()

    fun loadMore() {
        if (loading || page >= total) return
        val api = state.api ?: return
        loading = true
        scope.launch {
            try {
                val res = api.catalog(type, sort, genre, page + 1)
                enabled = res.enabled
                if (res.genres.isNotEmpty()) genres = res.genres
                val seen = titles.map { it.id }.toHashSet()
                titles += res.items.filter { seen.add(it.id) }
                page = res.page
                total = res.totalPages
                error = null
            } catch (e: Exception) {
                error = e.message
            } finally {
                loading = false
            }
        }
    }

    LaunchedEffect(type, sort, genre) { loadMore() }
    // Load the next page as the last rows come into view.
    LaunchedEffect(grid, type, sort, genre) {
        snapshotFlow { grid.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: 0 }.collect { last ->
            if (titles.isNotEmpty() && last >= titles.size - Columns * 2) loadMore()
        }
    }

    PosterGrid(grid) {
        header(label, genres.firstOrNull { it.id == genre }?.name) {
            Chip(genres.firstOrNull { it.id == genre }?.name ?: "All Genres", genre != null, { picker = "genre" },
                Modifier.rememberFocus("chip:genre"), icon = Icons.Rounded.Label)
            Chip(when (sort) { "top_rated" -> "Top Rated"; "new" -> "Newest"; else -> "Popular" }, false, { picker = "sort" },
                Modifier.rememberFocus("chip:sort"), icon = Icons.AutoMirrored.Rounded.Sort)
        }
        items(titles, key = { it.id }) { t ->
            TitleCard(t, Modifier.rememberFocus("t:${t.id}", default = t == titles.first()), width = CardWidth)
        }
        when {
            loading -> fullWidth("loading") { Loading(Modifier.padding(vertical = if (titles.isEmpty()) 60.dp else 0.dp)) }
            !enabled -> fullWidth("empty") {
                EmptyState("$label need TMDB", if (state.isAdmin) "Add a TMDB API key in Settings → Server Settings." else "The server owner hasn't set up TMDB yet.", icon = Icons.Rounded.AutoAwesome)
            }
            error != null && titles.isEmpty() -> fullWidth("error") { EmptyState("Couldn't load", error, icon = Icons.Rounded.WifiOff) }
            titles.isEmpty() -> fullWidth("none") { EmptyState("Nothing matches") }
        }
    }

    when (picker) {
        "genre" -> ChoicePanel("Genre", listOf(Choice<Int?>(null, "All Genres")) + genres.map { Choice<Int?>(it.id, it.name) }, genre, { picker = null }) { genre = it }
        "sort" -> ChoicePanel("Sort", listOf(Choice("popular", "Popular"), Choice("top_rated", "Top Rated"), Choice("new", "Newest")), sort, { picker = null }) {
            BrowsePrefs.catalogSort = it
        }
    }
}

// Libraries

@Composable
fun LibraryScreen(id: String) {
    val state = LocalAppState.current
    val library = state.libraries.firstOrNull { it.id == id }
    var chosen by rememberSaveable { mutableStateOf<String?>(null) }
    val rows = rememberLoaded<List<CollectionShelf>>("rows:$id", state.refreshTick) { state.api?.libraryRows(id) }
    val hasRows = !rows.value.isNullOrEmpty()
    // Like Plex, a library with rows opens on them.
    val tab = chosen ?: if (hasRows) "rows" else "titles"
    var genre by rememberSaveable { mutableStateOf<String?>(null) }
    var unwatched by rememberSaveable { mutableStateOf(false) }
    var picker by remember { mutableStateOf<String?>(null) }
    val sort = BrowsePrefs.librarySort
    val genres = rememberLoaded<List<GenreCount>>("genres:$id") { state.api?.genres(id) }
    val items = rememberLoaded<List<Item>>("library:$id:$sort:$genre:$unwatched", state.refreshTick) {
        state.api?.items(id, sort, genre, unwatched)?.items
    }
    val menu = rememberItemMenu()
    val grid = rememberLazyGridState()
    val scope = rememberCoroutineScope()
    val navigator = LocalNavigator.current
    val name = library?.name ?: "Library"
    val noun = if (library?.type == "shows") "Shows" else "Movies"

    @Composable
    fun tabs() {
        if (hasRows) Chip("Recommended", tab == "rows", { chosen = "rows" }, Modifier.rememberFocus("tab:rows", default = tab == "rows"))
        Chip(noun, tab == "titles", { chosen = "titles" }, Modifier.rememberFocus("tab:titles", default = tab == "titles"))
        Chip("Collections", tab == "collections", { chosen = "collections" }, Modifier.rememberFocus("tab:collections"))
        Chip("Genres", tab == "genres", { chosen = "genres" }, Modifier.rememberFocus("tab:genres"))
    }

    when (tab) {
        "rows" -> LibraryRows(name, rows.value.orEmpty(), menu) { tabs() }
        "collections" -> CollectionsGrid(id, name) { tabs() }
        "genres" -> GenresGrid(id, name) { tabs() }
        else -> Row(Modifier.fillMaxSize()) {
            val list = items.value
            PosterGrid(grid, Modifier.weight(1f)) {
                header(name, list?.let { "${it.size} ${noun.lowercase()}" }) {
                    tabs()
                    Spacer(Modifier.width(18.dp))
                    Chip(genre ?: "All Genres", genre != null, { picker = "genre" }, Modifier.rememberFocus("chip:genre"), icon = Icons.Rounded.Label)
                    Chip("Unwatched", unwatched, { unwatched = !unwatched }, Modifier.rememberFocus("chip:unwatched"), icon = Icons.Rounded.VisibilityOff)
                    Chip(SortOptions.firstOrNull { it.value == sort }?.label ?: "Title", false, { picker = "sort" },
                        Modifier.rememberFocus("chip:sort"), icon = Icons.AutoMirrored.Rounded.Sort)
                }
                when {
                    list == null && items.error != null -> fullWidth("error") { EmptyState("Couldn't load", items.error, icon = Icons.Rounded.WifiOff) }
                    list == null -> fullWidth("loading") { Loading() }
                    list.isEmpty() && (genre != null || unwatched) -> fullWidth("none") {
                        EmptyState("Nothing matches", "Try a different genre or show watched titles too.", icon = Icons.Rounded.FilterList)
                    }
                    list.isEmpty() -> fullWidth("none") { EmptyState("Empty library", "New files appear here automatically.") }
                    else -> items(list, key = { it.id }) { item ->
                        PosterCard(item, { navigator.push(Route.Detail(item.id, item)) }, Modifier.rememberFocus("i:${item.id}"), width = CardWidth, onLongClick = { menu.open(item) })
                    }
                }
            }
            if (sort == "title" && (list?.size ?: 0) > 30) {
                LetterRail(list!!) { index -> scope.launch { grid.animateScrollToItem(index + 1) } }
            }
        }
    }

    when (picker) {
        "genre" -> ChoicePanel("Genre", listOf(Choice<String?>(null, "All Genres")) + genres.value.orEmpty().map { Choice<String?>(it.genre, it.genre, "${it.count}") },
            genre, { picker = null }) { genre = it }
        "sort" -> ChoicePanel("Sort", SortOptions, sort, { picker = null }) { BrowsePrefs.librarySort = it }
    }
}

/** A library's collection rows, each a carousel of posters. */
@Composable
private fun LibraryRows(name: String, rows: List<CollectionShelf>, menu: ItemMenuState, tabs: @Composable () -> Unit) {
    val list = rememberLazyListState()
    ReportScroll(list)
    val nav = LocalNavigator.current
    LazyColumn(Modifier.fillMaxSize(), state = list, contentPadding = PaddingValues(bottom = 48.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        item("header") {
            Column(Modifier.padding(start = PageGutter, end = PageGutter, top = TopBarSpace)) {
                Text(name, style = MaterialTheme.typography.displaySmall, color = Wren.text)
                Spacer(Modifier.height(16.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(10.dp), verticalAlignment = Alignment.CenterVertically) { tabs() }
            }
        }
        rows.forEach { row ->
            item("row:${row.collection.id}") {
                Shelf(row.collection.name) {
                    items(row.items, key = { it.id }) { item ->
                        PosterCard(item, { nav.push(Route.Detail(item.id, item)) },
                            Modifier.rememberFocus("r:${row.collection.id}:${item.id}"), onLongClick = { menu.open(item) })
                    }
                    items(row.missing.orEmpty(), key = { it.id }) { t -> TitleCard(t, Modifier.rememberFocus("r:${row.collection.id}:${t.id}")) }
                }
            }
        }
    }
}

/** The letter a title files under: "The Bear" under B, digits under #. */
private fun letterOf(title: String): String {
    var t = title.uppercase()
    for (a in listOf("THE ", "A ", "AN ")) if (t.startsWith(a)) { t = t.removePrefix(a); break }
    val c = t.firstOrNull() ?: return "#"
    return if (c in 'A'..'Z') c.toString() else "#"
}

/** A–Z down the right edge of a title-sorted grid; a letter jumps to its first title. */
@Composable
private fun LetterRail(items: List<Item>, jump: (Int) -> Unit) {
    val firsts = remember(items) {
        val out = LinkedHashMap<String, Int>()
        items.forEachIndexed { i, it -> out.putIfAbsent(letterOf(it.title), i) }
        out
    }
    val letters = listOf("#") + ('A'..'Z').map { it.toString() }
    LazyColumn(
        Modifier.padding(top = TopBarSpace, end = 18.dp, bottom = 24.dp).width(34.dp),
        verticalArrangement = Arrangement.spacedBy(1.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        items(letters.filter { firsts.containsKey(it) }) { l ->
            Surface(
                onClick = { firsts[l]?.let(jump) },
                modifier = Modifier.size(28.dp),
                shape = ClickableSurfaceDefaults.shape(CircleShape),
                colors = ClickableSurfaceDefaults.colors(
                    containerColor = Color.Transparent, contentColor = Wren.secondary,
                    focusedContainerColor = Color.White, focusedContentColor = Color.Black,
                ),
            ) {
                Text(l, style = MaterialTheme.typography.labelMedium, modifier = Modifier.align(Alignment.Center))
            }
        }
    }
}

// Collections

@Composable
fun CollectionsScreen() = CollectionsGrid(null, "Collections")

@Composable
private fun CollectionsGrid(libraryId: String?, title: String, chips: @Composable () -> Unit = {}) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val collections = rememberLoaded<List<WrenCollection>>("collections:$libraryId", state.refreshTick) { state.api?.collections(libraryId) }
    PosterGrid {
        header(title, collections.value?.let { "${it.size} collection${if (it.size == 1) "" else "s"}" }, chips)
        val list = collections.value
        when {
            list == null && collections.error != null -> fullWidth("error") { EmptyState("Couldn't load", collections.error, icon = Icons.Rounded.WifiOff) }
            list == null -> fullWidth("loading") { Loading() }
            list.isEmpty() -> fullWidth("none") {
                EmptyState("No collections yet",
                    if (state.isAdmin) "Create collections in the web app: hand-picked, smart rules, or synced from TMDB." else "Collections the server owner makes show up here.",
                    icon = Icons.Rounded.ViewCarousel)
            }
            else -> items(list, key = { it.id }) { c ->
                CollectionCard(c, { nav.push(Route.Collection(c)) }, Modifier.rememberFocus("c:${c.id}", default = c == list.first()))
            }
        }
    }
}

@Composable
private fun CollectionCard(c: WrenCollection, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val api = LocalAppState.current.api
    var focused by remember { mutableStateOf(false) }
    Column(modifier.width(CardWidth)) {
        // Two offset cards behind the poster read as a stack.
        Box(Modifier.padding(top = 8.dp)) {
            Box(Modifier.align(Alignment.TopCenter).offset(y = (-8).dp).width(CardWidth - 16.dp).aspectRatio(2f / 3f)
                .background(Color.White.copy(alpha = 0.05f), RoundedCornerShape(10.dp)))
            Box(Modifier.align(Alignment.TopCenter).offset(y = (-4).dp).width(CardWidth - 8.dp).aspectRatio(2f / 3f)
                .background(Color.White.copy(alpha = 0.09f), RoundedCornerShape(10.dp)))
            ArtCard(onClick, Modifier.width(CardWidth).aspectRatio(2f / 3f).onFocusChanged { focused = it.hasFocus }.ambient(c.backdropPath)) {
                RemoteImage(api?.imageUrl(c.posterPath), Modifier.fillMaxSize())
                if (c.posterPath == null) {
                    Text(c.name, style = MaterialTheme.typography.titleMedium, color = Wren.secondary, textAlign = TextAlign.Center,
                        modifier = Modifier.align(Alignment.Center).padding(12.dp))
                }
                if (c.showOnHome || c.active == false) {
                    Text(
                        if (c.active == false) "OUT OF SEASON" else "ON HOME", style = MaterialTheme.typography.labelSmall, color = Color.White,
                        textAlign = TextAlign.Center,
                        modifier = Modifier.align(Alignment.BottomCenter).fillMaxWidth().background(Color.Black.copy(alpha = 0.7f)).padding(vertical = 4.dp),
                    )
                }
            }
        }
        Column(Modifier.padding(top = 10.dp).alpha(if (focused) 1f else 0.75f)) {
            Text(c.name, style = MaterialTheme.typography.titleSmall, color = Wren.text, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text("${c.itemCount ?: 0} title${if (c.itemCount == 1) "" else "s"} · ${c.kindLabel}", style = MaterialTheme.typography.bodySmall, color = Wren.secondary, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

@Composable
fun CollectionDetailScreen(seed: WrenCollection) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    var sort by rememberSaveable { mutableStateOf("") }
    var picker by remember { mutableStateOf(false) }
    var removing by remember { mutableStateOf<Item?>(null) }
    val detail = rememberLoaded<CollectionDetail>("collection:${seed.id}:$sort", state.refreshTick) {
        state.api?.collection(seed.id, sort.ifEmpty { null })
    }
    val c = detail.value?.collection ?: seed
    val d = detail.value
    PosterGrid {
        fullWidth("header") {
            Box(Modifier.fillMaxWidth().height(360.dp)) {
                RemoteImage(state.api?.imageUrl(c.backdropPath, "w1280"), Modifier.fillMaxSize())
                Box(Modifier.fillMaxSize().heroScrim())
                Column(Modifier.align(Alignment.BottomStart).padding(bottom = 12.dp).width(760.dp)) {
                    Text("COLLECTION", style = MaterialTheme.typography.labelMedium.copy(letterSpacing = 1.5.sp, fontWeight = FontWeight.Bold), color = Wren.ember)
                    Text(c.name, style = MaterialTheme.typography.displaySmall, color = Wren.text)
                    if (c.description.isNotEmpty()) Text(c.description, style = MaterialTheme.typography.bodyLarge, color = Wren.secondary, maxLines = 3)
                    Spacer(Modifier.height(6.dp))
                    val count = d?.titles?.let { "${it.size} title${if (it.size == 1) "" else "s"}" } ?: "${d?.items?.size ?: c.itemCount ?: 0} in your library"
                    Text(listOfNotNull(count, c.kindLabel, c.visibleFrom.takeIf { it.isNotEmpty() }?.let { "Visible $it – ${c.visibleUntil}" }).joinToString("  ·  "),
                        style = MaterialTheme.typography.bodyMedium, color = Wren.secondary)
                    c.syncError?.takeIf { it.isNotEmpty() && state.isAdmin }?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = Wren.ember) }
                    Spacer(Modifier.height(14.dp))
                    Chip(collectionSorts.firstOrNull { it.value == sort }?.label ?: "Collection order", false, { picker = true },
                        Modifier.rememberFocus("chip:sort"), icon = Icons.AutoMirrored.Rounded.Sort)
                }
            }
        }
        when {
            d == null && detail.error != null -> fullWidth("error") { EmptyState("Couldn't load", detail.error, icon = Icons.Rounded.WifiOff) }
            d == null -> fullWidth("loading") { Loading() }
            d.titles != null -> {
                val titles = sortedTitles(d.titles.orEmpty(), sort)
                items(titles, key = { it.id }) { t -> TitleCard(t, Modifier.rememberFocus("t:${t.id}", default = t == titles.first()), width = CardWidth) }
                if (titles.isEmpty()) fullWidth("none") { EmptyState("Nothing in this collection yet") }
            }
            else -> {
                items(d.items, key = { it.id }) { item ->
                    PosterCard(item, { nav.push(Route.Detail(item.id, item)) }, Modifier.rememberFocus("i:${item.id}", default = item == d.items.first()), width = CardWidth,
                        onLongClick = if (state.isAdmin && c.kind == "manual") ({ removing = item }) else null)
                }
                if (d.items.isEmpty()) fullWidth("none") { EmptyState("Nothing in this collection yet") }
                val missing = d.missing.orEmpty()
                if (missing.isNotEmpty()) {
                    fullWidth("missing") { SectionTitle("Not in your library", Modifier.padding(top = 12.dp)) }
                    items(missing, key = { "m:${it.id}" }) { t -> TitleCard(t, Modifier.rememberFocus("m:${t.id}"), width = CardWidth) }
                }
            }
        }
    }
    if (picker) ChoicePanel("Order", collectionSorts, sort, { picker = false }) { sort = it }
    removing?.let { item ->
        com.wrentv.tv.ui.ConfirmPanel("Remove from collection?", "${item.displayTitle} is removed from ${c.name}. The title stays in your library.", "Remove",
            { removing = null }, destructive = true) {
            state.scope.launch {
                runCatching { state.api?.removeFromCollection(seed.id, item.id) }
                state.refreshTick++
            }
        }
    }
}

private val collectionSorts = listOf(
    Choice("", "Collection order"), Choice("title", "Title"), Choice("year", "Year (newest)"),
    Choice("year_asc", "Year (oldest)"), Choice("rating", "Rating"), Choice("added", "Recently added"),
)

/** The server orders library items; full TMDB title lists are ordered here. */
private fun sortedTitles(titles: List<TitleView>, sort: String): List<TitleView> = when (sort) {
    "title" -> titles.sortedBy { it.title.lowercase() }
    "year" -> titles.sortedByDescending { it.year ?: 0 }
    "year_asc" -> titles.sortedBy { it.year ?: 9999 }
    "rating" -> titles.sortedByDescending { it.item?.rating ?: it.rating ?: 0.0 }
    else -> titles
}

// Genres

@Composable
fun GenresScreen() = GenresGrid(null, "Genres")

@Composable
private fun GenresGrid(libraryId: String?, title: String, chips: @Composable () -> Unit = {}) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val genres = rememberLoaded<List<GenreCount>>("genres:$libraryId", state.refreshTick) { state.api?.genres(libraryId) }
    val grid = rememberLazyGridState()
    ReportScroll(grid)
    LazyVerticalGrid(
        columns = GridCells.Fixed(4), state = grid, modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = PageGutter, end = PageGutter, bottom = 48.dp),
        horizontalArrangement = Arrangement.spacedBy(20.dp), verticalArrangement = Arrangement.spacedBy(20.dp),
    ) {
        header(title, null, chips)
        val list = genres.value
        when {
            list == null -> fullWidth("loading") { Loading() }
            list.isEmpty() -> fullWidth("none") { EmptyState("No genres yet", "Genres come from TMDB once titles are matched.", icon = Icons.Rounded.Label) }
            else -> items(list, key = { it.genre }) { g ->
                ArtCard({ nav.push(Route.GenreItems(g.genre, libraryId)) }, Modifier.fillMaxWidth().height(120.dp).rememberFocus("g:${g.genre}", default = g == list.first())) {
                    RemoteImage(state.api?.imageUrl(g.backdropPath, "w780"), Modifier.fillMaxSize())
                    Box(Modifier.fillMaxSize().background(Brush.verticalGradient(listOf(Color.Black.copy(alpha = 0.1f), Color.Black.copy(alpha = 0.85f)))))
                    Column(Modifier.align(Alignment.BottomStart).padding(14.dp)) {
                        Text(g.genre, style = MaterialTheme.typography.titleLarge, color = Color.White)
                        Text(genreCount(g), style = MaterialTheme.typography.bodySmall, color = Color.White.copy(alpha = 0.7f))
                    }
                }
            }
        }
    }
}

private fun genreCount(g: GenreCount): String = when {
    g.movies > 0 && g.shows > 0 -> "${g.movies} movie${if (g.movies == 1) "" else "s"} · ${g.shows} show${if (g.shows == 1) "" else "s"}"
    g.shows > 0 -> "${g.shows} show${if (g.shows == 1) "" else "s"}"
    else -> "${g.count} title${if (g.count == 1) "" else "s"}"
}

@Composable
fun GenreItemsScreen(genre: String, libraryId: String?) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    var kind by rememberSaveable { mutableStateOf("") }
    var unwatched by rememberSaveable { mutableStateOf(false) }
    var picker by remember { mutableStateOf(false) }
    val sort = BrowsePrefs.librarySort
    val items = rememberLoaded<List<Item>>("genre:$genre:$libraryId:$kind:$sort:$unwatched", state.refreshTick) {
        state.api?.genreItems(genre, libraryId, kind.ifEmpty { null }, sort, unwatched)?.items
    }
    val menu = rememberItemMenu()
    PosterGrid {
        header(genre, items.value?.let { "${it.size} title${if (it.size == 1) "" else "s"}" }) {
            Chip("All", kind == "", { kind = "" }, Modifier.rememberFocus("k:all", default = true))
            Chip("Movies", kind == "movie", { kind = "movie" }, Modifier.rememberFocus("k:movie"))
            Chip("Shows", kind == "show", { kind = "show" }, Modifier.rememberFocus("k:show"))
            Spacer(Modifier.width(18.dp))
            Chip("Unwatched", unwatched, { unwatched = !unwatched }, Modifier.rememberFocus("chip:unwatched"), icon = Icons.Rounded.VisibilityOff)
            Chip(SortOptions.firstOrNull { it.value == sort }?.label ?: "Title", false, { picker = true }, Modifier.rememberFocus("chip:sort"), icon = Icons.AutoMirrored.Rounded.Sort)
        }
        val list = items.value
        when {
            list == null -> fullWidth("loading") { Loading() }
            list.isEmpty() -> fullWidth("none") { EmptyState("Nothing matches", icon = Icons.Rounded.FilterList) }
            else -> items(list, key = { it.id }) { item ->
                PosterCard(item, { nav.push(Route.Detail(item.id, item)) }, Modifier.rememberFocus("i:${item.id}"), width = CardWidth, onLongClick = { menu.open(item) })
            }
        }
    }
    if (picker) ChoicePanel("Sort", SortOptions, sort, { picker = false }) { BrowsePrefs.librarySort = it }
}

// Search

@Composable
fun SearchScreen() {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    // A voice search arrives with its words already in.
    var query by rememberSaveable { mutableStateOf(state.searchSeed?.also { state.searchSeed = null } ?: "") }
    var results by remember { mutableStateOf<List<Item>>(emptyList()) }
    var more by remember { mutableStateOf<List<TitleView>>(emptyList()) }
    var searched by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    val field = remember { FocusRequester() }
    var recent by remember { mutableStateOf(state.recentSearches) }
    // Before anything is typed: recent searches and what's popular, rather than a blank page.
    val popular = if (state.canStream) rememberLoaded<DiscoverHome>("home:discover") { state.api?.discoverHome() }.value
        ?.rows?.firstOrNull()?.items.orEmpty().take(18) else emptyList()
    fun open(push: () -> Unit) { state.rememberSearch(query); recent = state.recentSearches; push() }

    LaunchedEffect(Unit) {
        if (query.isEmpty()) { awaitFrame(); runCatching { field.requestFocus() } }
    }
    LaunchedEffect(state.searchSeed) {
        state.searchSeed?.let { query = it; state.searchSeed = null }
    }
    LaunchedEffect(query) {
        val q = query.trim()
        if (q.length < 2) { results = emptyList(); more = emptyList(); searched = ""; return@LaunchedEffect }
        delay(350)
        val api = state.api ?: return@LaunchedEffect
        busy = true
        val found = runCatching { api.search(q) }.getOrDefault(emptyList())
        val titles = runCatching { api.discoverSearch(q).items }.getOrDefault(emptyList())
        // Library matches TMDB didn't return (episodes, local-only files) stay first.
        val ids = found.map { it.id }.toHashSet()
        results = found + titles.mapNotNull { it.item }.filter { it.id !in ids }
        more = titles.filter { it.item == null }
        searched = q
        busy = false
    }

    PosterGrid {
        fullWidth("header") {
            Column(Modifier.padding(top = TopBarSpace)) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                    Icon(Icons.Rounded.Search, null, Modifier.size(34.dp), tint = Wren.secondary)
                    TvTextField(query, { query = it }, "", Modifier.focusRequester(field).rememberFocus("field"),
                        placeholder = "Movies, shows, episodes…", width = 640.dp)
                    if (busy) Spinner(30.dp)
                }
                Spacer(Modifier.height(8.dp))
            }
        }
        if (query.isBlank()) {
            if (recent.isNotEmpty()) {
                fullWidth("recent") {
                    Column {
                        SectionTitle("Recent Searches")
                        Row(Modifier.padding(top = 4.dp, bottom = 8.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                            recent.take(6).forEach { q -> Chip(q, false, { query = q; runCatching { field.requestFocus() } }, Modifier.rememberFocus("recent:$q"), icon = Icons.Rounded.History) }
                        }
                    }
                }
            }
            if (popular.isNotEmpty()) {
                fullWidth("popular") { SectionTitle("Popular Right Now") }
                items(popular, key = { "p:${it.id}" }) { t -> TitleCard(t, Modifier.rememberFocus("p:${t.id}"), width = CardWidth) }
            }
        }
        val episodes = results.filter { it.kind == "episode" }
        val titles = results.filter { it.kind != "episode" }
        if (titles.isNotEmpty()) {
            if (more.isNotEmpty() && !state.canStream) fullWidth("on") { SectionTitle("On This Server") }
            items(titles, key = { it.id }) { item ->
                PosterCard(item, { open { nav.push(Route.Detail(item.id, item)) } }, Modifier.rememberFocus("r:${item.id}"), width = CardWidth)
            }
        }
        if (episodes.isNotEmpty()) {
            fullWidth("eps") { SectionTitle("Episodes", Modifier.padding(top = 8.dp)) }
            items(episodes, key = { it.id }, span = { GridItemSpan(2) }) { item ->
                LandscapeCard(item, { open { nav.push(Route.Detail(item.id, item)) } }, Modifier.rememberFocus("r:${item.id}"), width = CardWidth * 2 + 20.dp)
            }
        }
        if (more.isNotEmpty()) {
            // When streaming everything plays, so it's one list.
            if (!state.canStream) fullWidth("off") { SectionTitle("Not on This Server", Modifier.padding(top = 8.dp)) }
            items(more, key = { "m:${it.id}" }) { t ->
                TitleCard(t, Modifier.rememberFocus("m:${t.id}").onFocusChanged { if (it.isFocused) { state.rememberSearch(query); recent = state.recentSearches } }, width = CardWidth)
            }
        }
        if (searched.isNotEmpty() && searched == query.trim() && results.isEmpty() && more.isEmpty()) {
            fullWidth("none") { EmptyState("No results for “$searched”", "Check the spelling or try a different title.", icon = Icons.Rounded.Search) }
        }
    }
}
