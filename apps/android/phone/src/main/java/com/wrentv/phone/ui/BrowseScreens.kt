package com.wrentv.phone.ui

import androidx.compose.foundation.border
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.material.icons.rounded.ChevronLeft
import androidx.compose.material3.TextButton
import androidx.compose.ui.unit.sp
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.layout.BoxWithConstraints
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
import androidx.compose.material.icons.rounded.Refresh
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
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.wrentv.tv.data.CatalogPage
import com.wrentv.tv.data.CollectionPager
import com.wrentv.tv.data.key
import com.wrentv.tv.data.GenreCount
import com.wrentv.tv.data.Item
import com.wrentv.tv.data.Library
import com.wrentv.tv.data.TitleView
import com.wrentv.tv.data.WrenCollection
import kotlinx.coroutines.CancellationException
import java.time.format.DateTimeFormatter
import java.time.LocalDate
import com.wrentv.tv.data.CalendarResponse
import com.wrentv.tv.data.CalendarEpisode
import androidx.compose.material.icons.rounded.CalendarMonth
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.layout.width

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
            if (state.libraries.any { it.type == "shows" }) item { NavRow(Icons.Rounded.CalendarMonth, "Calendar") { nav.push(Route.Calendar) } }
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

/** The movies and shows saved for later, newest added first: a banner of them, then a row of each. */
@Composable
fun WatchlistScreen() {
    val state = LocalAppState.current
    val m = metrics()
    var items by remember { mutableStateOf<List<Item>?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var hideWatched by remember { mutableStateOf(state.store.bool("watchlistHideWatched", true)) }
    LaunchedEffect(state.refreshTick, state.api) {
        try { items = state.api?.watchlist().orEmpty(); error = null } catch (e: CancellationException) { throw e } catch (e: Exception) { if (items == null) error = e.message }
    }
    val shown = items?.filter { !hideWatched || !it.isWatched }
    Box(Modifier.fillMaxSize()) {
        LazyColumn(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(24.dp), contentPadding = PaddingValues(bottom = 32.dp)) {
            item("hero") {
                val withArt = shown.orEmpty().filter { it.backdropPath != null }.take(10)
                val count = shown?.let { "${it.size} title${if (it.size == 1) "" else "s"}" } ?: ""
                val slides = withArt.map { Slide(it.id, it.backdropPath, null, it.displayTitle, listOfNotNull(it.year?.toString(), if (it.kind == "movie") "Movie" else "TV Show").joinToString(" · "), it.overview) }
                    .ifEmpty { listOf(Slide("watchlist", null, null, "Watchlist", count, "Movies and shows you saved for later.")) }
                Hero("ON YOUR WATCHLIST", slides) {
                    GlassButton(if (hideWatched) "Show watched" else "Hide watched", if (hideWatched) Icons.Rounded.Visibility else Icons.Rounded.VisibilityOff, {
                        hideWatched = !hideWatched; state.store.setBool("watchlistHideWatched", hideWatched)
                    })
                }
            }
            when {
                shown == null && error != null -> item { EmptyState(Icons.Rounded.Warning, "Couldn't load", error) }
                shown == null -> item { Loading(Modifier.fillMaxWidth().padding(top = 60.dp)) }
                shown.isEmpty() -> item {
                    if (items.isNullOrEmpty()) EmptyState(Icons.Rounded.BookmarkBorder, "Your watchlist is empty", "Add movies and shows from their pages to find them here.", Modifier.fillMaxWidth().padding(top = 60.dp))
                    else EmptyState(Icons.Rounded.BookmarkBorder, "All watched", "Show watched to see them.", Modifier.fillMaxWidth().padding(top = 60.dp))
                }
                else -> listOf("Movies" to shown.filter { it.kind == "movie" }, "TV Shows" to shown.filter { it.kind != "movie" }).forEach { (title, list) ->
                    if (list.isNotEmpty()) item(title) {
                        Shelf(title, "${list.size}") { items(list, { it.id }) { PosterCard(it, width = m.poster, onWatchlist = true) } }
                    }
                }
            }
        }
        FloatingBack()
    }
}

/** Every collection, a shelf per kind: the server's own, franchises, lists, charts and smart collections. */
@Composable
fun CollectionsScreen() {
    val state = LocalAppState.current
    var list by remember { mutableStateOf<List<WrenCollection>?>(null) }
    LaunchedEffect(state.refreshTick, state.api) { list = runCatching { state.api?.collections() }.getOrNull().orEmpty() }
    Page("Collections") {
        val l = list
        if (l == null) Loading()
        else LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(vertical = 16.dp), verticalArrangement = Arrangement.spacedBy(28.dp)) {
            // The server's order already puts the groups in theirs.
            l.groupBy { it.group ?: "Collections" }.forEach { (group, cs) ->
                item(group) {
                    Shelf(group, "${cs.size} collection${if (cs.size == 1) "" else "s"}") {
                        items(cs, { it.id }) { CollectionTile(it) }
                    }
                }
            }
            if (l.isEmpty()) item { EmptyState(Icons.Rounded.CollectionsBookmark, "No collections yet") }
        }
    }
}

/** A collection's wide tile: its artwork with the name over it. */
@Composable
fun CollectionTile(c: WrenCollection) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val m = metrics()
    Column(Modifier.width(m.landscape).clickable { nav.push(Route.Collection(c.id, c.name)) }, verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Box(Modifier.fillMaxWidth().aspectRatio(16f / 9f).clip(RoundedCornerShape(10.dp))) {
            RemoteImage(state.api?.imageUrl(c.backdropPath ?: c.posterPath, "w780"), Modifier.fillMaxSize())
            Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0.4f to Color.Transparent, 1f to Color.Black.copy(alpha = 0.8f))))
            Text(c.name, Modifier.align(Alignment.BottomStart).padding(12.dp), fontWeight = FontWeight.Bold, maxLines = 2,
                style = MaterialTheme.typography.titleMedium)
            if (c.active == false) Badge("OUT OF SEASON", Color.Black.copy(alpha = 0.7f), Modifier.align(Alignment.TopStart))
        }
        Text("${c.itemCount ?: 0} title${if (c.itemCount == 1) "" else "s"}", color = Wren.secondary, style = MaterialTheme.typography.bodySmall)
    }
}

@Composable
private fun Badge(text: String, color: Color, modifier: Modifier = Modifier) {
    Text(text, modifier.padding(8.dp).clip(RoundedCornerShape(6.dp)).background(color).padding(horizontal = 8.dp, vertical = 3.dp),
        fontWeight = FontWeight.Bold, style = MaterialTheme.typography.labelSmall, color = Color.White)
}

/**
 * When episodes air, a week at a time: a strip of the week's days (tap one
 * to see just that day, again for the whole week; swipe or use the arrows
 * for other weeks), then a shelf a day. The library's shows still running
 * and the ones being watched.
 */
@Composable
fun CalendarScreen() {
    val state = LocalAppState.current
    val today = remember { LocalDate.now() }
    val thisWeek = remember { weekStart(today) }
    var weekText by rememberSaveable { mutableStateOf(thisWeek.toString()) }
    val week = LocalDate.parse(weekText)
    var picked by rememberSaveable { mutableStateOf<String?>(null) }
    var cal by remember { mutableStateOf<CalendarResponse?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var watchingOnly by rememberSaveable { mutableStateOf(false) }
    LaunchedEffect(week, state.refreshTick, state.api) {
        runCatching { state.api?.calendar(week.toString(), week.plusDays(6).toString()) }
            .onSuccess { if (it?.from == week.toString()) cal = it; error = null }
            .onFailure { if (it !is CancellationException && cal == null) error = it.message }
    }
    fun step(n: Long) { picked = null; weekText = week.plusWeeks(n).toString() }
    Page("Calendar", actions = {
        if (week != thisWeek) TextButton({ picked = null; weekText = thisWeek.toString() }) { Text("This week", color = Wren.ember) }
        IconButton({ watchingOnly = !watchingOnly }) {
            Icon(if (watchingOnly) Icons.Rounded.Visibility else Icons.Rounded.VisibilityOff,
                if (watchingOnly) "Showing shows you're watching" else "Showing all shows", tint = if (watchingOnly) Wren.ember else Color.White)
        }
    }) {
        val c = cal
        val eps = c?.episodes.orEmpty().filter { !watchingOnly || it.watching }
        val days = eps.groupBy { it.date }
        val shown = days.filterKeys { picked == null || it == picked }.toList()
        val list = rememberLazyListState()
        // Open this week on today, with what aired earlier in the week above it.
        LaunchedEffect(c?.from) {
            val i = shown.indexOfFirst { it.first >= today.toString() }
            if (picked == null && i > 0) list.scrollToItem(i + 1)
        }
        LazyColumn(Modifier.fillMaxSize(), list, contentPadding = PaddingValues(vertical = 16.dp), verticalArrangement = Arrangement.spacedBy(28.dp)) {
            item("week") {
                WeekStrip(week, today, picked, days.mapValues { it.value.size }, { d -> picked = if (picked == d) null else d }, ::step)
            }
            when {
                c == null -> item { if (error != null) EmptyState(Icons.Rounded.Warning, "Couldn't load", error) else Loading(Modifier.fillMaxWidth().padding(top = 60.dp)) }
                shown.isEmpty() -> item {
                    EmptyState(Icons.Rounded.Tv,
                        when {
                            picked != null -> "Nothing airing ${dayName(picked!!).let { if (it == "Today") "today" else "on $it" }}"
                            watchingOnly -> "Nothing this week for your shows"
                            else -> "Nothing airing this week"
                        },
                        "Episodes show here for shows in the library that are still running. Use the arrows to see other weeks.",
                        Modifier.fillMaxWidth().padding(top = 40.dp))
                }
                else -> items(shown, { it.first }) { (date, dayEps) ->
                    Shelf(dayName(date), "${dayEps.size} episode${if (dayEps.size == 1) "" else "s"}") {
                        items(dayEps, { it.id }) { CalendarCard(it) }
                    }
                }
            }
        }
    }
}

/** The first day of date's week, by the locale's first weekday. */
private fun weekStart(date: LocalDate): LocalDate =
    date.with(java.time.temporal.TemporalAdjusters.previousOrSame(java.time.temporal.WeekFields.of(java.util.Locale.getDefault()).firstDayOfWeek))

/** The seven days of a week with how many episodes air each, between previous and next week buttons. */
@Composable
private fun WeekStrip(week: LocalDate, today: LocalDate, picked: String?, counts: Map<String, Int>, pick: (String) -> Unit, step: (Long) -> Unit) {
    val m = metrics()
    var drag by remember { mutableStateOf(0f) }
    Column(
        Modifier.padding(horizontal = m.gutter).pointerInput(week) {
            detectHorizontalDragGestures(onDragEnd = { if (kotlin.math.abs(drag) > 80) step(if (drag < 0) 1 else -1); drag = 0f }) { _, d -> drag += d }
        },
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Text("${week.format(DateTimeFormatter.ofPattern("MMM d"))} – ${week.plusDays(6).format(DateTimeFormatter.ofPattern("MMM d, yyyy"))}",
            color = Wren.secondary, style = MaterialTheme.typography.labelLarge)
        Row(horizontalArrangement = Arrangement.spacedBy(4.dp), verticalAlignment = Alignment.CenterVertically) {
            IconButton({ step(-1) }, Modifier.width(32.dp)) { Icon(Icons.Rounded.ChevronLeft, "Previous week") }
            for (i in 0L until 7L) {
                val d = week.plusDays(i)
                val key = d.toString()
                val on = picked == key
                val isToday = d == today
                val n = counts[key] ?: 0
                Column(
                    Modifier.weight(1f).clip(RoundedCornerShape(10.dp))
                        .background(if (on) Wren.ember else Color.White.copy(alpha = 0.05f))
                        .border(1.dp, if (isToday && !on) Wren.ember else Color.White.copy(alpha = 0.1f), RoundedCornerShape(10.dp))
                        .clickable { pick(key) }.padding(vertical = 8.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                ) {
                    Text(d.format(DateTimeFormatter.ofPattern("EEE")).uppercase(), fontSize = 10.sp, fontWeight = FontWeight.Bold,
                        color = if (on) Color.White else if (isToday) Wren.ember else Wren.secondary)
                    Text("${d.dayOfMonth}", fontSize = 18.sp, fontWeight = FontWeight.Bold, color = Color.White.copy(alpha = if (n == 0 && !on) 0.55f else 1f))
                    Text(if (n > 0) "$n" else " ", fontSize = 11.sp, color = if (on) Color.White else Wren.secondary)
                }
            }
            IconButton({ step(1) }, Modifier.width(32.dp)) { Icon(Icons.Rounded.ChevronRight, "Next week") }
        }
    }
}

/** "Today", "Tomorrow", "Yesterday", or the weekday and date. */
private fun dayName(date: String): String {
    val d = runCatching { LocalDate.parse(date) }.getOrNull() ?: return date
    val today = LocalDate.now()
    return when (d) {
        today -> "Today"
        today.plusDays(1) -> "Tomorrow"
        today.minusDays(1) -> "Yesterday"
        else -> d.format(DateTimeFormatter.ofPattern("EEEE, MMM d"))
    }
}

@Composable
private fun CalendarCard(e: CalendarEpisode) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val m = metrics()
    Column(Modifier.width(m.landscape).clickable { nav.push(Route.Detail(e.itemId ?: e.show.id, if (e.itemId == null) e.show else null)) },
        verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Box(Modifier.fillMaxWidth().aspectRatio(16f / 9f).clip(RoundedCornerShape(10.dp))) {
            RemoteImage(state.api?.imageUrl(e.stillPath ?: e.show.backdropPath, "w500"), Modifier.fillMaxSize())
            if (e.upNext) Badge("UP NEXT", Wren.ember, Modifier.align(Alignment.TopStart))
            else if (e.watched) Badge("WATCHED", Color.Black.copy(alpha = 0.7f), Modifier.align(Alignment.TopStart))
        }
        Text(e.show.title, maxLines = 1, fontWeight = FontWeight.Medium, style = MaterialTheme.typography.bodyMedium)
        Text("${e.code} · ${e.title.ifEmpty { "Episode ${e.episodeNumber}" }}", maxLines = 1, color = Wren.secondary, style = MaterialTheme.typography.bodySmall)
    }
}

/**
 * A collection under its artwork, like the other pages: one shelf in its
 * order, loaded a page at a time as it scrolls toward the end.
 */
@Composable
fun CollectionScreen(id: String, name: String) {
    val state = LocalAppState.current
    val m = metrics()
    var sort by rememberSaveable { mutableStateOf("") }
    val pager = remember(id, sort) { CollectionPager.of(id, sort) }
    val api = state.api
    LaunchedEffect(pager, state.refreshTick, api) { api?.let { pager.start(it, state.scope, state.refreshTick) } }
    val shelf = rememberLazyListState()
    LaunchedEffect(pager, api) {
        snapshotFlow { shelf.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: -1 }.collect { last ->
            if (last >= 0 && api != null) pager.near(last, api, state.scope)
        }
    }
    val c = pager.collection
    val entries = pager.entries
    Box(Modifier.fillMaxSize()) {
        LazyColumn(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(24.dp), contentPadding = PaddingValues(bottom = 32.dp)) {
            item("hero") {
                val meta = listOfNotNull(pager.countLabel.ifEmpty { null }, c?.kindLabel).joinToString(" · ")
                val about = c?.syncError?.takeIf { it.isNotEmpty() && state.isAdmin } ?: c?.description?.ifEmpty { null }
                val slides = listOf(Slide(id, c?.backdropPath ?: entries.firstOrNull { it.backdropPath != null }?.backdropPath, null, c?.name ?: name, meta, about))
                Hero("COLLECTION", slides) { SortButton(CollectionSorts, sort) { sort = it } }
            }
            when {
                entries.isEmpty() && pager.error != null -> item { EmptyState(Icons.Rounded.Warning, "Couldn't load", pager.error) }
                entries.isEmpty() && !pager.done -> item { Loading(Modifier.fillMaxWidth().padding(top = 60.dp)) }
                entries.isEmpty() -> item { EmptyState(Icons.Rounded.Warning, "Nothing in this collection yet") }
                else -> item("shelf:$sort") {
                    Shelf(c?.name ?: name, pager.total.takeIf { it > 0 }?.toString(), state = shelf) {
                        items(entries, { it.key }) { t -> TitleCard(t, width = m.poster) }
                        if (pager.error != null && api != null) item("retry") {
                            Column(Modifier.width(m.poster).clickable { pager.retry(api, state.scope) }) {
                                Box(Modifier.fillMaxWidth().aspectRatio(2f / 3f).clip(RoundedCornerShape(10.dp)).background(Wren.surface), contentAlignment = Alignment.Center) {
                                    Icon(Icons.Rounded.Refresh, "Retry", tint = Wren.secondary)
                                }
                                Text("Couldn't load more", color = Wren.secondary, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(top = 6.dp))
                            }
                        }
                    }
                }
            }
        }
        FloatingBack()
    }
}

private val CollectionSorts = listOf(
    "" to "Collection order", "title" to "Title", "year" to "Year (newest)",
    "year_asc" to "Year (oldest)", "rating" to "Rating", "added" to "Recently added",
)

/** The hero's button for how a collection is shown. */
@Composable
private fun SortButton(sorts: List<Pair<String, String>>, sort: String, pick: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Box {
        GlassButton(sorts.firstOrNull { it.first == sort }?.second ?: "Collection order", Icons.AutoMirrored.Rounded.Sort, { open = true })
        DropdownMenu(open, { open = false }) {
            sorts.forEach { (v, l) ->
                DropdownMenuItem({ Text(l) }, { open = false; pick(v) }, trailingIcon = { if (v == sort) Icon(Icons.Rounded.Check, null) })
            }
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

