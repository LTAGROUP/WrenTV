package com.wrentv.phone.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Movie
import androidx.compose.material.icons.rounded.Warning
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.wrentv.tv.data.Company
import com.wrentv.tv.data.Studio
import com.wrentv.tv.data.StudioRow
import com.wrentv.tv.data.TitleView
import com.wrentv.tv.data.WrenTVApi
import kotlinx.coroutines.CancellationException

/** Studio pages and their shelves so far, kept so Back returns to the same place. */
private val studioCache = HashMap<String, Any>()

/** The studios behind a title, as logo tiles that open each studio's page. */
@Composable
fun StudioTiles(studios: List<Studio>) {
    val nav = LocalNavigator.current
    Shelf("Production") {
        items(studios, { it.id }) { s -> StudioLogo(s, 150.dp, Modifier.clickable { nav.push(Route.Studio(s.id, s.name)) }) }
    }
}

/** A studio's logo on a light tile, or its name where there's no logo. */
@Composable
fun StudioLogo(studio: Studio, width: Dp, modifier: Modifier = Modifier) {
    val api = LocalAppState.current.api
    Box(modifier.size(width, width / 2).clip(RoundedCornerShape(10.dp)).background(Color(0xFFF2F2F4)), contentAlignment = Alignment.Center) {
        if (studio.logoPath != null) RemoteImage(api?.imageUrl(studio.logoPath, "w300"), Modifier.fillMaxSize().padding(width * 0.14f), ContentScale.Fit)
        else Text(studio.name, Modifier.padding(8.dp), color = Color(0xFF1C1C1E), fontWeight = FontWeight.SemiBold, textAlign = TextAlign.Center, maxLines = 2)
    }
}

/**
 * A studio over its artwork: popular, top rated and recent movies, then
 * popular and top rated shows. Each shelf loads more as it nears its end.
 */
@Composable
fun StudioScreen(id: Int, name: String) {
    val state = LocalAppState.current
    val m = metrics()
    var company by remember(id) { mutableStateOf(studioCache["studio:$id"] as? Company) }
    var error by remember(id) { mutableStateOf<String?>(null) }
    LaunchedEffect(id, state.api) {
        if (company != null) return@LaunchedEffect
        try {
            company = state.api?.company(id)?.also { studioCache["studio:$id"] = it }
            error = null
        } catch (e: CancellationException) { throw e } catch (e: Exception) { error = e.message }
    }
    val c = company
    Box(Modifier.fillMaxSize()) {
        LazyColumn(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(28.dp), contentPadding = PaddingValues(bottom = 16.dp)) {
            item("header") {
                Column {
                    Box(Modifier.fillMaxWidth().height(if (m.compact) 320.dp else 400.dp)) {
                        RemoteImage(state.api?.imageUrl(c?.backdropPath, "w1280"), Modifier.fillMaxSize())
                        Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0.2f to Color.Transparent, 1f to Wren.background)))
                        Row(Modifier.align(Alignment.BottomStart).padding(horizontal = m.gutter), horizontalArrangement = Arrangement.spacedBy(16.dp), verticalAlignment = Alignment.Bottom) {
                            StudioLogo(Studio(id, c?.name ?: name, c?.logoPath), 140.dp)
                            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                                Text("STUDIO", fontSize = 12.sp, fontWeight = FontWeight.Bold, letterSpacing = 1.sp, color = Wren.ember)
                                Text(c?.name ?: name, fontSize = 28.sp, fontWeight = FontWeight.Bold, lineHeight = 32.sp)
                                listOfNotNull(c?.headquarters, c?.originCountry).filter { it.isNotBlank() }.takeIf { it.isNotEmpty() }?.let {
                                    Text(it.joinToString(" · "), color = Wren.secondary, style = MaterialTheme.typography.bodySmall)
                                }
                            }
                        }
                    }
                    c?.description?.takeIf { it.isNotBlank() }?.let {
                        Text(it, Modifier.padding(horizontal = m.gutter).padding(top = 16.dp), style = MaterialTheme.typography.bodyMedium, color = Wren.text)
                    }
                }
            }
            when {
                c != null -> {
                    items(c.rows, { it.id }) { r -> StudioShelf(id, r) }
                    if (c.rows.isEmpty()) item { EmptyState(Icons.Rounded.Movie, "No movies or shows listed", null, Modifier.fillMaxWidth().padding(top = 40.dp)) }
                }
                error != null -> item { EmptyState(Icons.Rounded.Warning, "Couldn't load", error, Modifier.fillMaxWidth().padding(top = 40.dp)) }
                else -> item { Loading(Modifier.fillMaxWidth().padding(top = 60.dp)) }
            }
        }
        FloatingBack()
    }
}

/** A shelf's titles so far. */
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
    val m = metrics()
    val key = "studio:$studio:${row.id}"
    val pages = remember(key) { (studioCache[key] as? ShelfPages) ?: ShelfPages(row).also { studioCache[key] = it } }
    val shelf = rememberLazyListState()
    // A couple of screenfuls before the end, the next page comes in.
    LaunchedEffect(pages) {
        snapshotFlow { (shelf.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: 0) to pages.items.size }
            .collect { (last, size) -> if (api != null && last >= size - 12) pages.more(api, studio, row) }
    }
    Shelf(row.title, state = shelf) {
        items(pages.items, { it.id }) { TitleCard(it, width = m.poster) }
    }
}
