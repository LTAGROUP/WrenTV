package com.wrentv.phone.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
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
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.automirrored.rounded.ArrowForward
import androidx.compose.material.icons.rounded.Add
import androidx.compose.material.icons.rounded.Apps
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.KeyboardArrowDown
import androidx.compose.material.icons.rounded.KeyboardArrowUp
import androidx.compose.material.icons.rounded.Movie
import androidx.compose.material.icons.rounded.Info
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Tune
import androidx.compose.material.icons.rounded.Visibility
import androidx.compose.material.icons.rounded.VisibilityOff
import androidx.compose.material.icons.rounded.Warning
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.wrentv.tv.data.HubCard
import com.wrentv.tv.data.HubPage
import com.wrentv.tv.data.Item
import com.wrentv.tv.data.LayoutConfig
import com.wrentv.tv.data.LayoutEntry
import com.wrentv.tv.data.LayoutPage
import com.wrentv.tv.data.LayoutRow
import com.wrentv.tv.data.WrenTVException
import com.wrentv.tv.ui.playTarget
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/*
 * Home, Movies, TV Shows, libraries and hubs are built from the server's
 * layout rows, the same rows every Wren TV shows, under a hero of the
 * page's first titles.
 */

/**
 * A page made of the server's layout rows. A tab's root page floats the
 * search bar over the hero; a pushed one gets a back button. Servers from
 * before layouts answer 404 and get [fallback].
 */
@Composable
fun LayoutPageScreen(scope: String, title: String, root: Boolean, browseAll: Route? = null, fallback: @Composable () -> Unit) {
    val state = LocalAppState.current
    var page by remember(scope) { mutableStateOf<LayoutPage?>(null) }
    var error by remember(scope) { mutableStateOf<String?>(null) }
    var unsupported by remember(scope) { mutableStateOf(false) }
    LaunchedEffect(scope, state.refreshTick, state.api) {
        try {
            page = state.api?.layout(scope)
            error = null
        } catch (e: CancellationException) {
            throw e
        } catch (e: WrenTVException) {
            if (e.status == 404) unsupported = true else if (page == null) error = e.message
        } catch (e: Exception) {
            if (page == null) error = e.message
        }
    }
    if (unsupported) return fallback()
    val p = page
    Box(Modifier.fillMaxSize()) {
        LazyColumn(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(28.dp)) {
            when {
                p != null -> {
                    item("hero") { LayoutHero(p.rows) }
                    layoutRows(p.rows)
                    if (p.rows.isEmpty()) item {
                        EmptyState(Icons.Rounded.Movie, "Nothing here yet",
                            if (state.isAdmin) "Add a library in the server's web dashboard." else "Ask the server owner to share a library.")
                    }
                    item("actions") { PageActions(scope, title, browseAll) }
                }
                error != null -> item { EmptyState(Icons.Rounded.Warning, "Couldn't load", error) }
                else -> item { Loading(Modifier.fillMaxWidth().padding(top = 240.dp)) }
            }
        }
        if (root) HomeSearchBar() else FloatingBack()
    }
}

/** The back arrow over a pushed page's hero. */
@Composable
private fun FloatingBack() {
    val nav = LocalNavigator.current
    IconButton({ nav.back() }, Modifier.statusBarsPadding().padding(8.dp).clip(CircleShape).background(Color(0x991C1A21))) {
        Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Back", tint = Color.White)
    }
}

/** "Browse All" and "Customize" under a page's rows. */
@Composable
private fun PageActions(scope: String, title: String, browseAll: Route?) {
    val nav = LocalNavigator.current
    Row(Modifier.padding(horizontal = metrics().gutter).padding(bottom = 16.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
        if (browseAll != null) GlassButton("Browse All", Icons.Rounded.Apps, { nav.push(browseAll) })
        GlassButton("Customize", Icons.Rounded.Tune, { nav.push(Route.LayoutEditor(scope, title)) })
    }
}

/** The page's banner: its first titles with artwork, taking turns. */
@Composable
private fun LayoutHero(rows: List<LayoutRow>) {
    val titles = remember(rows) {
        rows.firstOrNull { it.kind == "titles" && !it.titles.isNullOrEmpty() }?.titles.orEmpty()
            .distinctBy { it.id }.filter { it.backdropPath != null }.take(6)
    }
    val items = remember(rows) {
        rows.filter { it.kind == "items" || it.kind == "collection" }.flatMap { it.items.orEmpty() }
            .distinctBy { it.id }.filter { it.backdropPath != null && it.kind != "episode" }.take(6)
    }
    when {
        titles.isNotEmpty() -> DiscoverHero(titles)
        items.isNotEmpty() -> ItemHero(items)
        else -> Spacer(Modifier.height(90.dp))
    }
}

/** Library titles taking turns, for servers that don't stream. */
@Composable
private fun ItemHero(picks: List<Item>) {
    val nav = LocalNavigator.current
    val slides = remember(picks) { picks.map { Slide(it.id, it.backdropPath, it.logoPath, it.title, metaLine(it.year, it.kind == "show", it.rating), it.overview) } }
    Hero("FEATURED", slides) { i ->
        val it = picks[i]
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            if (it.isPlayable) PrimaryButton(if (it.inProgress) "Resume" else "Play", Icons.Rounded.PlayArrow, { nav.play(it.playTarget()) })
            GlassButton("Details", Icons.Rounded.Info, { nav.push(Route.Detail(it.id, it)) })
        }
    }
}

/** The server's rows, each drawn the way its kind asks. */
fun LazyListScope.layoutRows(rows: List<LayoutRow>) {
    rows.forEach { row ->
        item("row:" + row.id) {
            val m = metrics()
            Shelf(row.title, row.subtitle) {
                when (row.kind) {
                    "landscape" -> items(row.items.orEmpty().distinctBy { it.id }, { it.id }) { LandscapeCard(it, width = m.landscape) }
                    "hubs" -> items(row.hubs.orEmpty().distinctBy { it.id }, { it.id }) { HubTile(it, row.style == "logos") }
                    "titles" -> itemsIndexed(row.titles.orEmpty().distinctBy { it.id }, { _, t -> t.id }) { i, t ->
                        TitleCard(t, width = m.poster, rank = if (row.ranked && i < 10) i + 1 else null)
                    }
                    else -> {
                        items(row.items.orEmpty().distinctBy { it.id }, { it.id }) { PosterCard(it, width = m.poster, onWatchlist = row.id == "watchlist") }
                        items(row.missing.orEmpty().distinctBy { it.id }, { "m" + it.id }) { TitleCard(it, width = m.poster) }
                    }
                }
                // A collection's or hub's row leads to the rest of it.
                row.collection?.let { c -> item("more") { MoreCard("View All", Route.Collection(c.id, c.name)) } }
                    ?: row.hubId?.let { h -> item("more") { MoreCard("Open ${row.title}", Route.Hub(h, row.title)) } }
            }
        }
    }
}

/** The card at the end of a row that opens the whole of it. */
@Composable
private fun MoreCard(label: String, route: Route) {
    val nav = LocalNavigator.current
    val m = metrics()
    Column(
        Modifier.width(m.poster).aspectRatio(2f / 3f).clip(RoundedCornerShape(10.dp)).background(Color.White.copy(alpha = 0.08f))
            .clickable { nav.push(route) }.padding(10.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp, Alignment.CenterVertically), horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Icon(Icons.AutoMirrored.Rounded.ArrowForward, null)
        Text(label, maxLines = 2, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyMedium)
    }
}

/** A hub's tile: its artwork with the name over it, or a service's logo on its colour. */
@Composable
fun HubTile(hub: HubCard, logos: Boolean) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val m = metrics()
    val tint = hubColor(hub.color)
    Box(Modifier.width(m.landscape).aspectRatio(16f / 9f).clip(RoundedCornerShape(10.dp)).clickable { nav.push(Route.Hub(hub.id, hub.name)) }) {
        if (logos && hub.logoPath != null) {
            Box(Modifier.fillMaxSize().background(tint ?: Color.White.copy(alpha = 0.08f)))
            RemoteImage(state.api?.imageUrl(hub.logoPath, "w300"), Modifier.fillMaxSize().padding(26.dp), ContentScale.Fit)
        } else {
            RemoteImage(state.api?.imageUrl(hub.backdropPath, "w780"), Modifier.fillMaxSize())
            Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0.4f to Color.Transparent, 1f to (tint ?: Color.Black).copy(alpha = 0.85f))))
            Text(hub.name, Modifier.align(Alignment.BottomStart).padding(12.dp), fontWeight = FontWeight.Bold, maxLines = 2, color = Color.White)
        }
    }
}

private fun hubColor(hex: String?): Color? =
    hex?.let { runCatching { Color(android.graphics.Color.parseColor(it)) }.getOrNull() }

private val HubCard.kindLabel: String get() = when (kind) {
    "genre" -> "GENRE"
    "service" -> "STREAMING SERVICE"
    else -> "HUB"
}

/** A hub: a genre, a streaming service or an admin's own, as shelves. */
@Composable
fun HubScreen(id: String, name: String) {
    val state = LocalAppState.current
    val m = metrics()
    var page by remember(id) { mutableStateOf<HubPage?>(null) }
    var error by remember(id) { mutableStateOf<String?>(null) }
    LaunchedEffect(id, state.refreshTick, state.api) {
        try { page = state.api?.hub(id); error = null } catch (e: CancellationException) { throw e } catch (e: Exception) { if (page == null) error = e.message }
    }
    val p = page
    Box(Modifier.fillMaxSize()) {
        LazyColumn(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(28.dp), contentPadding = PaddingValues(bottom = 16.dp)) {
            item("header") {
                Box(Modifier.fillMaxWidth().height(if (m.compact) 300.dp else 380.dp)) {
                    RemoteImage(state.api?.imageUrl(p?.hub?.backdropPath, "w1280"), Modifier.fillMaxSize())
                    Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0.3f to Color.Transparent, 1f to Wren.background)))
                    Column(Modifier.align(Alignment.BottomStart).padding(horizontal = m.gutter), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                        p?.hub?.let { Text(it.kindLabel, fontSize = 12.sp, fontWeight = FontWeight.Bold, letterSpacing = 1.sp, color = Wren.ember) }
                        Text(name, fontSize = 30.sp, fontWeight = FontWeight.Bold, lineHeight = 34.sp)
                        p?.hub?.tagline?.takeIf { it.isNotEmpty() }?.let { Text(it, color = Wren.secondary, maxLines = 3, style = MaterialTheme.typography.bodyMedium) }
                    }
                }
            }
            when {
                p != null -> {
                    layoutRows(p.rows)
                    if (p.rows.isEmpty()) item { EmptyState(Icons.Rounded.Movie, "Nothing here yet", "None of this hub's shelves found any titles.", Modifier.fillMaxWidth().padding(top = 40.dp)) }
                }
                error != null -> item { EmptyState(Icons.Rounded.Warning, "Couldn't load", error, Modifier.fillMaxWidth().padding(top = 40.dp)) }
                else -> item { Loading(Modifier.fillMaxWidth().padding(top = 60.dp)) }
            }
        }
        FloatingBack()
    }
}

// Layout editor

/** The pages whose rows can be chosen. */
@Composable
fun LayoutsScreen() {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    Page("Layouts", "Choose which rows each page shows, and in what order.") {
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(vertical = 8.dp)) {
            item { NavRow(null, "Home") { nav.push(Route.LayoutEditor("home", "Home")) } }
            if (state.canStream) {
                item { NavRow(null, "Movies") { nav.push(Route.LayoutEditor("movies", "Movies")) } }
                item { NavRow(null, "TV Shows") { nav.push(Route.LayoutEditor("shows", "TV Shows")) } }
            } else if (state.libraries.isNotEmpty()) {
                item { SectionHeader("Libraries") }
                items(state.libraries, { it.id }) { lib -> NavRow(null, lib.name) { nav.push(Route.LayoutEditor("library:${lib.id}", lib.name)) } }
            }
        }
    }
}

/**
 * One page's rows: move, hide, remove, add. Admins also choose whether
 * they're editing their own layout or everyone's default.
 */
@Composable
fun LayoutEditorScreen(scope: String, title: String) {
    val state = LocalAppState.current
    val co = rememberCoroutineScope()
    var everyone by remember { mutableStateOf(false) }
    var config by remember { mutableStateOf<LayoutConfig?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var adding by remember { mutableStateOf(false) }
    var reload by remember { mutableStateOf(0) }
    LaunchedEffect(scope, everyone, reload) {
        try { config = state.api?.layoutConfig(scope, everyone); error = null } catch (e: CancellationException) { throw e } catch (e: Exception) { error = e.message }
    }
    fun save(entries: List<LayoutEntry>) {
        config = config?.copy(entries = entries, custom = true)
        co.launch {
            try {
                state.api?.setLayout(scope, everyone, entries)
                state.refreshTick++
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                error = e.message
                reload++
            }
        }
    }
    Page(title, "Layout") {
        val c = config
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 24.dp)) {
            if (state.isAdmin) item {
                Row(Modifier.padding(horizontal = 20.dp, vertical = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Chip("My layout", !everyone) { everyone = false }
                    Chip("Default for everyone", everyone) { everyone = true }
                }
            }
            error?.let { e -> item { Text(e, Modifier.padding(horizontal = 20.dp, vertical = 8.dp), color = Wren.ember) } }
            when {
                c != null -> {
                    item { SectionHeader("Rows") }
                    itemsIndexed(c.entries, { _, e -> e.key }) { i, e ->
                        Row(Modifier.fillMaxWidth().padding(start = 20.dp, end = 4.dp, top = 4.dp, bottom = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                            Column(Modifier.weight(1f)) {
                                Text(e.title.ifEmpty { e.ref }, color = if (e.hidden) Wren.secondary else Color.White, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                e.detail?.let { Text(it, color = Wren.secondary, style = MaterialTheme.typography.bodySmall, maxLines = 1) }
                            }
                            IconButton({ save(c.entries.toMutableList().apply { add(i - 1, removeAt(i)) }) }, enabled = i > 0) { Icon(Icons.Rounded.KeyboardArrowUp, "Move up") }
                            IconButton({ save(c.entries.toMutableList().apply { add(i + 1, removeAt(i)) }) }, enabled = i < c.entries.size - 1) { Icon(Icons.Rounded.KeyboardArrowDown, "Move down") }
                            IconButton({ save(c.entries.toMutableList().apply { set(i, e.copy(hidden = !e.hidden)) }) }) {
                                Icon(if (e.hidden) Icons.Rounded.VisibilityOff else Icons.Rounded.Visibility, if (e.hidden) "Show" else "Hide", tint = if (e.hidden) Wren.secondary else Wren.ember)
                            }
                            IconButton({ save(c.entries.toMutableList().apply { removeAt(i) }) }) { Icon(Icons.Rounded.Close, "Remove", tint = Wren.secondary) }
                        }
                    }
                    val have = c.entries.map { it.key }.toSet()
                    val options = c.options.filter { it.key !in have }
                    if (options.isNotEmpty()) item {
                        Box {
                            NavRow(Icons.Rounded.Add, "Add a Row…") { adding = true }
                            DropdownMenu(adding, { adding = false }, containerColor = Wren.surface) {
                                options.forEach { o ->
                                    DropdownMenuItem(
                                        text = { Text(listOfNotNull(o.title.ifEmpty { o.ref }, o.detail).joinToString(" — ")) },
                                        onClick = { adding = false; save(c.entries + o) },
                                    )
                                }
                            }
                        }
                    }
                    if (!c.custom) item {
                        Text(if (!everyone && c.hasDefault) "Showing the server's default. Changing anything makes it your own." else "Showing Wren TV's built-in layout.",
                            Modifier.padding(horizontal = 20.dp, vertical = 12.dp), color = Wren.secondary, style = MaterialTheme.typography.bodySmall)
                    } else item {
                        TextButton({
                            co.launch {
                                try { state.api?.resetLayout(scope, everyone); state.refreshTick++; reload++ } catch (e: CancellationException) { throw e } catch (e: Exception) { error = e.message }
                            }
                        }, Modifier.padding(horizontal = 8.dp, vertical = 8.dp)) {
                            Text(if (!everyone && c.hasDefault) "Use the Default Layout" else "Back to the Built-in Layout", color = Wren.ember)
                        }
                    }
                }
                error == null -> item { Loading(Modifier.fillMaxWidth().padding(top = 120.dp)) }
            }
        }
    }
}
