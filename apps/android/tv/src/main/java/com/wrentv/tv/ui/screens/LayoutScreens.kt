package com.wrentv.tv.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowForward
import androidx.compose.material.icons.rounded.Movie
import androidx.compose.material.icons.rounded.WifiOff
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.tv.material3.Icon
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import coil3.compose.AsyncImage
import com.wrentv.tv.data.HubCard
import com.wrentv.tv.data.HubPage
import com.wrentv.tv.data.LayoutPage
import com.wrentv.tv.data.LayoutRow
import com.wrentv.tv.data.WrenTVException
import com.wrentv.tv.ui.ArtCard
import com.wrentv.tv.ui.CinematicBrowse
import com.wrentv.tv.ui.CinematicLandscape
import com.wrentv.tv.ui.CinematicPoster
import com.wrentv.tv.ui.CinematicScope
import com.wrentv.tv.ui.EmptyState
import com.wrentv.tv.ui.LandscapeCard
import com.wrentv.tv.ui.Loading
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.PageCache
import com.wrentv.tv.ui.PosterCard
import com.wrentv.tv.ui.RemoteImage
import com.wrentv.tv.ui.Route
import com.wrentv.tv.ui.Shelf
import com.wrentv.tv.ui.Spinner
import com.wrentv.tv.ui.TitleCard
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.ambient
import com.wrentv.tv.ui.playTarget
import com.wrentv.tv.ui.rememberFocus
import com.wrentv.tv.ui.rememberLoaded
import kotlinx.coroutines.flow.first

/** How many of a page's rows load at a time. */
private const val PageRows = 6

/**
 * A page made of the server's layout rows: Home, Movies, TV Shows or a
 * library. Servers from before layouts answer 404, and get [fallback].
 */
@Composable
fun LayoutPageScreen(scope: String, label: String? = null, fallback: @Composable () -> Unit) {
    val state = LocalAppState.current
    var unsupported by remember(scope) { mutableStateOf(false) }
    if (unsupported) {
        fallback()
        return
    }
    val key = "layout:$scope"
    val page = rememberLoaded<LayoutPage>(key, state.refreshTick) {
        try {
            // Rows come a few at a time as the page scrolls; a reload keeps as many as were showing.
            val showing = (PageCache.values[key] as? LayoutPage)?.rows?.size ?: 0
            state.api?.layout(scope, 0, maxOf(PageRows, showing))
        } catch (e: WrenTVException) {
            if (e.status == 404) unsupported = true
            throw e
        }
    }
    val menu = rememberItemMenu()
    val list = rememberLazyListState()
    val p = page.value
    // Near the last row loaded, the next few.
    LaunchedEffect(key, p?.nextOffset) {
        val next = p?.nextOffset ?: return@LaunchedEffect
        snapshotFlow { list.layoutInfo.let { it.totalItemsCount > 0 && (it.visibleItemsInfo.lastOrNull()?.index ?: 0) >= it.totalItemsCount - 3 } }
            .first { it }
        val more = runCatching { state.api?.layout(scope, next, PageRows) }.getOrNull() ?: return@LaunchedEffect
        val cur = page.value ?: return@LaunchedEffect
        val have = cur.rows.map { it.id }.toHashSet()
        val merged = cur.copy(rows = cur.rows + more.rows.filter { it.id !in have }, nextOffset = more.nextOffset)
        page.value = merged
        PageCache.values[key] = merged
    }
    // Until a title takes focus (the page opens with the top bar focused), the hero shows the first.
    val first = p?.rows?.firstNotNullOfOrNull { r ->
        r.items?.firstOrNull()?.heroEntry() ?: r.titles?.firstOrNull()?.heroEntry() ?: r.hubs?.firstOrNull()?.heroEntry()
    }
    CinematicBrowse(list, fallback = first, label = label) {
        when {
            p != null -> {
                layoutRows(scope, p.rows, menu)
                if (p.nextOffset != null) extra("more") {
                    Box(Modifier.fillMaxWidth().padding(vertical = 32.dp), contentAlignment = Alignment.Center) { Spinner() }
                }
                if (p.rows.isEmpty()) extra("empty") {
                    EmptyState("Nothing here yet",
                        if (state.isAdmin) "Add a library from Settings → Libraries." else "Ask the server owner to share a library.",
                        icon = Icons.Rounded.Movie)
                }
            }
            page.error != null -> extra("error") { EmptyState("Couldn't load", page.error, icon = Icons.Rounded.WifiOff) }
            else -> extra("loading") { Loading() }
        }
    }
}

/** Movies or TV Shows when the server streams: its layout rows. */
@Composable
fun KindScreen(type: String) = LayoutPageScreen(if (type == "show") "shows" else "movies") { CatalogScreen(type) }

/** A hub: a genre, a streaming service or an admin's own, as shelves. */
@Composable
fun HubScreen(id: String, name: String) {
    val state = LocalAppState.current
    val page = rememberLoaded<HubPage>("hub:$id", state.refreshTick) { state.api?.hub(id) }
    val menu = rememberItemMenu()
    val list = rememberLazyListState()
    val p = page.value
    val seed = p?.hub ?: HubCard(id = id, name = name)
    CinematicBrowse(list, fallback = seed.heroEntry(), label = hubLabel(seed.kind)) {
        when {
            p != null -> {
                layoutRows("hub:$id", p.rows, menu)
                if (p.rows.isEmpty()) extra("empty") { EmptyState("Nothing here yet", "None of this hub's shelves found any titles.") }
            }
            page.error != null -> extra("error") { EmptyState("Couldn't load", page.error, icon = Icons.Rounded.WifiOff) }
            else -> extra("loading") { Loading() }
        }
    }
}

private fun hubLabel(kind: String) = when (kind) {
    "genre" -> "Genre"
    "service" -> "Streaming service"
    else -> "Hub"
}

fun HubCard.heroEntry() = HeroEntry(
    id = "hub:$id", title = name, backdrop = backdropPath, logo = null,
    meta = listOf(hubLabel(kind)), badges = emptyList(), overview = tagline, item = null,
)

/** The server's rows, each drawn the way its kind asks. */
fun CinematicScope.layoutRows(page: String, rows: List<LayoutRow>, menu: ItemMenuState) {
    rows.forEach { row ->
        val key = "$page:${row.id}"
        row(row.id) {
            val nav = LocalNavigator.current
            Shelf(row.title, subtitle = row.subtitle) {
                when (row.kind) {
                    "landscape" -> items(row.items.orEmpty(), key = { it.id }) { item ->
                        LandscapeCard(item, { nav.play(item.playTarget()) }, Modifier.rememberFocus("$key:${item.id}"), width = CinematicLandscape,
                            onLongClick = { menu.open(item) })
                    }
                    "hubs" -> items(row.hubs.orEmpty(), key = { it.id }) { hub ->
                        HubTile(hub, logos = row.style == "logos", modifier = Modifier.rememberFocus("$key:${hub.id}")) {
                            nav.push(Route.Hub(hub.id, hub.name))
                        }
                    }
                    "titles" -> itemsIndexed(row.titles.orEmpty(), key = { _, t -> t.id }) { i, t ->
                        TitleCard(t, Modifier.rememberFocus("$key:${t.id}"), rank = if (row.ranked && i < 10) i + 1 else null, width = CinematicPoster)
                    }
                    else -> {
                        items(row.items.orEmpty(), key = { it.id }) { item ->
                            PosterCard(item, { nav.push(Route.Detail(item.id, item)) }, Modifier.rememberFocus("$key:${item.id}"), width = CinematicPoster,
                                onLongClick = { menu.open(item, onWatchlist = row.id == "watchlist") })
                        }
                        items(row.missing.orEmpty(), key = { "m:${it.id}" }) { t -> TitleCard(t, Modifier.rememberFocus("$key:m:${t.id}"), width = CinematicPoster) }
                    }
                }
                // A collection's or hub's row leads to the rest of it: all of
                // it when the row is cut short, otherwise the collection's page.
                val c = row.collection
                val hub = row.hubId
                if (c != null) item("more") {
                    MoreCard(if (row.more) "View all" else "Open collection", Modifier.rememberFocus("$key:more")) { nav.push(Route.Collection(c)) }
                }
                else if (hub != null) item("more") { MoreCard("Open ${row.title}", Modifier.rememberFocus("$key:more")) { nav.push(Route.Hub(hub, row.title)) } }
            }
        }
    }
}

/** A hub's tile: its artwork with the name over it, or a service's icon and name. */
@Composable
fun HubTile(hub: HubCard, logos: Boolean, modifier: Modifier = Modifier, width: Dp = CinematicLandscape, onClick: () -> Unit) {
    val api = LocalAppState.current.api
    val color = hub.color?.let { runCatching { Color(android.graphics.Color.parseColor(it)) }.getOrNull() }
    ArtCard(onClick, modifier.width(width).aspectRatio(16f / 9f).ambient(hub.backdropPath) { hub.heroEntry() }) {
        if (logos && hub.logoPath != null) {
            ServiceFace(hub.name, color, api?.imageUrl(hub.logoPath, "w300"), 15.sp)
        } else {
            RemoteImage(api?.imageUrl(hub.backdropPath, "w780"), Modifier.fillMaxSize())
            Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0.3f to Color.Transparent, 1f to (color ?: Color.Black).copy(alpha = 0.85f))))
            Text(
                hub.name, style = MaterialTheme.typography.titleLarge.copy(fontWeight = FontWeight.Bold), color = Color.White,
                textAlign = TextAlign.Start, maxLines = 2,
                modifier = Modifier.align(Alignment.BottomStart).padding(14.dp),
            )
        }
    }
}

/** The card at the end of a row that opens the whole of it. */
@Composable
private fun MoreCard(label: String, modifier: Modifier = Modifier, onClick: () -> Unit) {
    Column(modifier.width(CinematicPoster)) {
        ArtCard(onClick, Modifier.width(CinematicPoster).aspectRatio(2f / 3f)) {
            Column(Modifier.fillMaxSize().padding(12.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = androidx.compose.foundation.layout.Arrangement.Center) {
                Icon(Icons.AutoMirrored.Rounded.ArrowForward, null, Modifier.width(32.dp).height(32.dp), tint = Wren.text)
                Spacer(Modifier.height(10.dp))
                Text(label, style = MaterialTheme.typography.titleSmall, color = Wren.text, textAlign = TextAlign.Center, maxLines = 2)
            }
        }
    }
}

/**
 * A streaming service's tile face: its icon, ringed so it stands off any
 * colour, over its name on a dark wash of its colour. The same for every
 * service, on every app.
 */
@Composable
private fun ServiceFace(name: String, tint: Color?, logo: String?, fontSize: TextUnit) {
    val ink = Color(0xFF0C0B10)
    androidx.compose.foundation.layout.BoxWithConstraints(
        Modifier.fillMaxSize()
            .background(tint ?: Color(0xFF3A3640))
            .background(Brush.linearGradient(listOf(ink.copy(alpha = 0.45f), ink.copy(alpha = 0.84f))))
            .background(Brush.verticalGradient(0f to Color.White.copy(alpha = 0.08f), 0.6f to Color.Transparent)),
        contentAlignment = Alignment.Center,
    ) {
        val side = maxHeight * 0.46f
        val gap = maxHeight * 0.07f
        val shape = RoundedCornerShape(side * 0.22f)
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            RemoteImage(
                logo,
                Modifier.size(side).shadow(side * 0.12f, shape).clip(shape).border(1.dp, Color.White.copy(alpha = 0.22f), shape),
                ContentScale.Crop,
            )
            Spacer(Modifier.height(gap))
            Text(name, color = Color.White.copy(alpha = 0.94f), fontSize = fontSize, fontWeight = FontWeight.SemiBold, maxLines = 1,
                modifier = Modifier.padding(horizontal = 10.dp))
        }
    }
}
