package com.wrentv.phone.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.AutoAwesome
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.History
import androidx.compose.material.icons.rounded.Info
import androidx.compose.material.icons.rounded.Movie
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Search
import androidx.compose.material.icons.rounded.Warning
import androidx.compose.material.icons.rounded.WifiOff
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.wrentv.tv.data.DiscoverHome
import com.wrentv.tv.data.HomeResponse
import com.wrentv.tv.data.Item
import com.wrentv.tv.data.WrenCollection
import com.wrentv.tv.data.TitleView
import com.wrentv.tv.data.TrendingResponse
import com.wrentv.tv.data.WrenTVException
import com.wrentv.tv.ui.playTarget
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.serialization.SerializationException

/** Home: everything on TMDB when the server streams, otherwise what's in the libraries. */
@Composable
fun HomeScreen() {
    val state = LocalAppState.current
    var unsupported by remember { mutableStateOf(false) }
    Box(Modifier.fillMaxSize()) {
        if (state.canStream && !unsupported) DiscoverHomeScreen { unsupported = true } else LibraryHomeScreen()
        HomeSearchBar()
    }
}

/** The search bar floating at the top of Home; opens the search page. */
@Composable
fun HomeSearchBar() {
    val nav = LocalNavigator.current
    Row(
        Modifier.statusBarsPadding().padding(horizontal = 16.dp, vertical = 8.dp).fillMaxWidth().height(46.dp)
            .clip(CircleShape).background(Color(0xCC1C1A21)).clickable { nav.push(Route.Search) }.padding(horizontal = 16.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Icon(Icons.Rounded.Search, null, tint = Wren.secondary)
        Text("Movies and shows", color = Wren.secondary)
    }
}

@Composable
private fun DiscoverHomeScreen(unsupported: () -> Unit) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    var home by remember { mutableStateOf<DiscoverHome?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(state.refreshTick, state.api) {
        try {
            home = state.api?.discoverHome()
            error = null
        } catch (e: CancellationException) {
            throw e
        } catch (e: WrenTVException) {
            if (e.status == 404) unsupported() else if (home == null) error = e.message
        } catch (e: SerializationException) {
            unsupported()
        } catch (e: Exception) {
            if (home == null) error = e.message
        }
    }
    val h = home
    LazyColumn(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(28.dp)) {
        when {
            h != null -> {
                item { if (h.hero.isNotEmpty()) DiscoverHero(h.hero) else Spacer(Modifier.height(90.dp)) }
                if (h.continueWatching.isNotEmpty()) item {
                    Shelf("Continue Watching") { items(h.continueWatching, { it.id }) { LandscapeCard(it, width = metricsLandscape()) } }
                }
                h.watchlist?.takeIf { it.isNotEmpty() }?.let { w -> item { Shelf("My Watchlist") { items(w, { it.id }) { PosterCard(it, width = 112.dp, onWatchlist = true) } } } }
                h.rows.forEachIndexed { i, row ->
                    if (i == 1) recentlyWatched(h)
                    item(row.id) {
                        Shelf(row.title) {
                            itemsIndexed(row.items, { _, v -> v.id }) { n, v -> TitleCard(v, width = 112.dp, rank = if (row.id == "trending" && n < 10) n + 1 else null) }
                        }
                    }
                }
                if (h.rows.size < 2) recentlyWatched(h)
                if (!h.enabled) item {
                    EmptyState(Icons.Rounded.AutoAwesome, "Discovery needs TMDB",
                        if (state.isAdmin) "Add a TMDB API key in the server's web dashboard under Settings." else "The server owner hasn't set up TMDB yet.")
                } else if (h.rows.isEmpty()) item { EmptyState(Icons.Rounded.WifiOff, "Nothing to show", "TMDB didn't answer. Try again in a moment.") }
                item { Spacer(Modifier.height(8.dp)) }
            }
            error != null -> item { EmptyState(Icons.Rounded.Warning, "Couldn't load", error) }
            else -> item { Loading(Modifier.fillMaxWidth().padding(top = 240.dp)) }
        }
    }
}

private fun androidx.compose.foundation.lazy.LazyListScope.recentlyWatched(h: DiscoverHome) {
    if (h.recentlyWatched.isEmpty()) return
    item("recent") { Shelf("Recently Watched") { items(h.recentlyWatched, { it.id }) { PosterCard(it, width = 112.dp) } } }
}

@Composable
private fun metricsLandscape() = metrics().landscape

/** One slide of a hero banner. */
class Slide(
    val key: String,
    val backdrop: String?,
    val logo: String?,
    val title: String,
    val meta: String,
    val overview: String?,
)

/** A banner of titles that take turns every eight seconds, edge to edge under the status bar. */
@Composable
fun Hero(label: String, slides: List<Slide>, actions: @Composable (Int) -> Unit) {
    val state = LocalAppState.current
    val m = metrics()
    val pager = rememberPagerState { slides.size }
    LaunchedEffect(slides.size) {
        while (slides.size > 1) {
            delay(8000)
            if (!pager.isScrollInProgress) pager.animateScrollToPage((pager.currentPage + 1) % slides.size)
        }
    }
    Box(Modifier.fillMaxWidth().height(m.heroHeight)) {
        HorizontalPager(pager, Modifier.fillMaxSize()) { i ->
            val s = slides[i]
            Box(Modifier.fillMaxSize()) {
                RemoteImage(state.api?.imageUrl(s.backdrop, "w1280"), Modifier.fillMaxSize())
                Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0.35f to Color.Transparent, 0.75f to Color.Black.copy(alpha = 0.6f), 1f to Wren.background)))
                Column(Modifier.align(Alignment.BottomStart).padding(horizontal = m.gutter).padding(bottom = 54.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    Text(label, fontSize = 12.sp, fontWeight = FontWeight.Bold, letterSpacing = 1.sp, color = Color.White.copy(alpha = 0.7f))
                    if (s.logo != null) {
                        RemoteImage(state.api?.imageUrl(s.logo, "w500"), Modifier.widthIn(max = 220.dp).fillMaxWidth().heightIn(max = 84.dp).height(84.dp), ContentScale.Fit)
                    } else {
                        Text(s.title, fontSize = 32.sp, fontWeight = FontWeight.Bold, lineHeight = 36.sp)
                    }
                    Text(s.meta, color = Color.White.copy(alpha = 0.75f), style = MaterialTheme.typography.bodyMedium)
                    s.overview?.let { Text(it, maxLines = 2, overflow = TextOverflow.Ellipsis, color = Color.White.copy(alpha = 0.85f), style = MaterialTheme.typography.bodySmall) }
                }
            }
        }
        Column(Modifier.align(Alignment.BottomStart).padding(horizontal = m.gutter), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            actions(pager.currentPage.coerceIn(0, (slides.size - 1).coerceAtLeast(0)))
            if (slides.size > 1) {
                Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    slides.indices.forEach { i ->
                        Box(Modifier.height(7.dp).width(if (i == pager.currentPage) 18.dp else 7.dp).clip(CircleShape)
                            .background(Color.White.copy(alpha = if (i == pager.currentPage) 0.95f else 0.35f)))
                    }
                }
            }
        }
    }
}

fun metaLine(year: Int?, show: Boolean, rating: Double?): String = listOfNotNull(
    year?.toString(), if (show) "Series" else "Film", rating?.takeIf { it > 0 }?.let { "★ %.1f".format(it) },
).joinToString(" · ")

@Composable
fun DiscoverHero(titles: List<TitleView>) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val scope = rememberCoroutineScope()
    var opening by remember { mutableStateOf(false) }
    val slides = remember(titles) {
        titles.map { Slide(it.id, it.backdropPath, it.item?.logoPath, it.title, metaLine(it.year, it.mediaType == "show", it.rating), it.overview) }
    }
    Hero("TRENDING THIS WEEK", slides) { i ->
        val t = titles[i]
        val item = t.item
        fun details() = if (item != null) nav.push(Route.Detail(item.id, item)) else nav.push(Route.StreamTitle(t.tmdbId, t.mediaType, t.title))
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            if (t.mediaType == "movie") {
                PrimaryButton(if (opening) "Opening…" else if (item?.inProgress == true) "Resume" else "Play", Icons.Rounded.PlayArrow, {
                    if (item != null) nav.play(item.playTarget())
                    else {
                        opening = true
                        scope.launch {
                            runCatching { state.api?.addStreamTitle(t.tmdbId, t.mediaType) }.getOrNull()?.let { nav.play(it.playTarget()) }
                            opening = false
                        }
                    }
                }, enabled = !opening)
                GlassButton("Details", Icons.Rounded.Info, ::details)
            } else {
                PrimaryButton("Watch Now", Icons.Rounded.PlayArrow, ::details)
            }
        }
    }
}

@Composable
private fun LibraryHomeScreen() {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    var home by remember { mutableStateOf<HomeResponse?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var trending by remember { mutableStateOf<TrendingResponse?>(null) }
    LaunchedEffect(state.refreshTick, state.api) {
        try { home = state.api?.home(); error = null } catch (e: CancellationException) { throw e } catch (e: Exception) { if (home == null) error = e.message }
    }
    LaunchedEffect(state.api) { trending = runCatching { state.api?.trending() }.getOrNull() ?: TrendingResponse() }
    val h = home
    LazyColumn(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(28.dp)) {
        when {
            h != null -> {
                val picks = featured(h)
                item {
                    if (picks.isEmpty()) Spacer(Modifier.height(90.dp))
                    else {
                        val slides = remember(picks) { picks.map { Slide(it.id, it.backdropPath, it.logoPath, it.title, metaLine(it.year, it.kind == "show", it.rating), it.overview) } }
                        Hero("FEATURED", slides) { i ->
                            val it = picks[i]
                            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                                if (it.isPlayable) PrimaryButton(if (it.inProgress) "Resume" else "Play", Icons.Rounded.PlayArrow, { nav.play(it.playTarget()) })
                                GlassButton("Details", Icons.Rounded.Info, { nav.push(Route.Detail(it.id, it)) })
                            }
                        }
                    }
                }
                if (h.continueWatching.isNotEmpty()) item {
                    Shelf("Continue Watching") { items(h.continueWatching, { it.id }) { LandscapeCard(it, width = metricsLandscape()) } }
                }
                h.watchlist?.takeIf { it.isNotEmpty() }?.let { w -> item { Shelf("My Watchlist") { items(w, { it.id }) { PosterCard(it, width = 112.dp, onWatchlist = true) } } } }
                item {
                    val t = trending
                    if (t == null) SkeletonShelf("Trending This Week")
                    else if (t.enabled && t.items.isNotEmpty()) {
                        Shelf("Trending This Week", "${t.items.count { it.item != null }} in your library") {
                            itemsIndexed(t.items, { _, v -> v.id }) { i, v -> TitleCard(v, width = 112.dp, rank = i + 1) }
                        }
                    }
                }
                h.collections.orEmpty().forEach { row ->
                    item("c" + row.collection.id) { Shelf(row.collection.name) { items(row.items, { it.id }) { PosterCard(it, width = 112.dp) } } }
                }
                h.recentlyAdded.forEach { shelf ->
                    item("r" + shelf.library.id) { Shelf("Recently Added in ${shelf.library.name}") { items(shelf.items, { it.id }) { PosterCard(it, width = 112.dp) } } }
                }
                h.topRated?.takeIf { it.size >= 4 }?.let { top -> item { Shelf("Top Rated in Your Library") { items(top, { it.id }) { PosterCard(it, width = 112.dp) } } } }
                h.genres.orEmpty().forEach { row -> item("g" + row.genre) { Shelf(row.genre) { items(row.items, { it.id }) { PosterCard(it, width = 112.dp) } } } }
                if (h.continueWatching.isEmpty() && h.recentlyAdded.isEmpty() && h.watchlist.isNullOrEmpty()) item {
                    EmptyState(Icons.Rounded.Movie, "Nothing here yet",
                        if (state.isAdmin) "Add a library in the server's web dashboard." else "Ask the server owner to share a library.")
                }
                item { Spacer(Modifier.height(8.dp)) }
            }
            error != null -> item { EmptyState(Icons.Rounded.Warning, "Couldn't load", error) }
            else -> item { Loading(Modifier.fillMaxWidth().padding(top = 240.dp)) }
        }
    }
}

/** A handful of well-rated and new titles with artwork, a different set each hour. */
private fun featured(home: HomeResponse): List<Item> {
    val seen = HashSet<String>()
    val pool = (home.topRated.orEmpty() + home.recentlyAdded.flatMap { it.items }).filter { it.backdropPath != null && seen.add(it.id) }
    if (pool.isEmpty()) return emptyList()
    val start = (System.currentTimeMillis() / 3_600_000 % pool.size).toInt()
    return (0 until minOf(6, pool.size)).map { pool[(start + it * 3) % pool.size] }.distinctBy { it.id }
}

/** Library matches first, then TMDB's (playable when the server streams). */
@Composable
fun SearchScreen() {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    var query by remember { mutableStateOf("") }
    var results by remember { mutableStateOf<List<Item>>(emptyList()) }
    var more by remember { mutableStateOf<List<TitleView>>(emptyList()) }
    var collections by remember { mutableStateOf<List<WrenCollection>>(emptyList()) }
    var searched by remember { mutableStateOf("") }
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }
    LaunchedEffect(query) {
        val q = query.trim()
        if (q.length < 2) { results = emptyList(); more = emptyList(); collections = emptyList(); return@LaunchedEffect }
        delay(300)
        val api = state.api ?: return@LaunchedEffect
        val found = runCatching { api.search(q) }.getOrNull().orEmpty()
        val titles = runCatching { api.discoverSearch(q) }.getOrNull()?.items.orEmpty()
        val ids = found.map { it.id }.toSet()
        results = found + titles.mapNotNull { it.item }.filter { it.id !in ids }
        more = titles.filter { it.item == null }
        collections = runCatching { api.searchCollections(q) }.getOrNull().orEmpty()
        searched = q
        state.rememberSearch(q)
    }
    val m = metrics()
    Column(Modifier.fillMaxSize().statusBarsPadding()) {
        Row(Modifier.padding(horizontal = 8.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            IconButton({ nav.back() }) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Back") }
            TextField(
                query, { query = it }, Modifier.weight(1f).focusRequester(focus), singleLine = true,
                placeholder = { Text("Movies and shows") },
                leadingIcon = { Icon(Icons.Rounded.Search, null) },
                trailingIcon = { if (query.isNotEmpty()) IconButton({ query = "" }) { Icon(Icons.Rounded.Close, "Clear") } },
                shape = CircleShape,
                colors = TextFieldDefaults.colors(
                    focusedContainerColor = Wren.surfaceHigh, unfocusedContainerColor = Wren.surfaceHigh,
                    focusedIndicatorColor = Color.Transparent, unfocusedIndicatorColor = Color.Transparent, cursorColor = Color.White,
                ),
            )
        }
        val q = query.trim()
        if (q.isEmpty()) {
            val recent = state.recentSearches
            if (recent.isEmpty()) EmptyState(Icons.Rounded.Search, "Search Wren TV", "Find movies and shows by title.")
            else LazyColumn(Modifier.fillMaxSize()) {
                item { Text("Recent", Modifier.padding(horizontal = m.gutter, vertical = 8.dp), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold) }
                items(recent) { r ->
                    Row(Modifier.fillMaxWidth().clickable { query = r }.padding(horizontal = m.gutter, vertical = 12.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                        Icon(Icons.Rounded.History, null, tint = Wren.secondary)
                        Text(r)
                    }
                }
            }
        } else {
            androidx.compose.foundation.lazy.grid.LazyVerticalGrid(
                androidx.compose.foundation.lazy.grid.GridCells.Adaptive(m.gridMin), Modifier.fillMaxSize(),
                contentPadding = androidx.compose.foundation.layout.PaddingValues(m.gutter),
                horizontalArrangement = Arrangement.spacedBy(12.dp), verticalArrangement = Arrangement.spacedBy(18.dp),
            ) {
                val full = { s: androidx.compose.foundation.lazy.grid.LazyGridItemSpanScope -> androidx.compose.foundation.lazy.grid.GridItemSpan(s.maxLineSpan) }
                if (collections.isNotEmpty()) {
                    item("colls", span = full) {
                        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            Text("Collections", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                            LazyRow(horizontalArrangement = Arrangement.spacedBy(12.dp)) { items(collections, { it.id }) { CollectionTile(it) } }
                        }
                    }
                    if (results.isNotEmpty() || more.isNotEmpty()) item(span = full) { Text("Titles", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold) }
                }
                if (results.isNotEmpty() && more.isNotEmpty() && !state.canStream) item(span = full) { Text("On This Server", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold) }
                results.forEach { item -> item(item.id) { PosterCard(item) } }
                if (more.isNotEmpty() && !state.canStream) item(span = full) { Text("Not on This Server", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold) }
                more.forEach { t -> item(t.id) { TitleCard(t) } }
                if (searched == q && results.isEmpty() && more.isEmpty() && collections.isEmpty()) item(span = full) { EmptyState(Icons.Rounded.Search, "No results for “$q”", "Check the spelling or try a new search.") }
            }
        }
    }
}

