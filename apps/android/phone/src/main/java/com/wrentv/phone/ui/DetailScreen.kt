package com.wrentv.phone.ui

import android.content.Intent
import android.text.format.Formatter
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.ArrowCircleDown
import androidx.compose.material.icons.rounded.Bookmark
import androidx.compose.material.icons.rounded.BookmarkBorder
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.CheckCircleOutline
import androidx.compose.material.icons.rounded.Download
import androidx.compose.material.icons.rounded.DownloadDone
import androidx.compose.material.icons.rounded.ExpandMore
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Replay
import androidx.compose.material.icons.rounded.SmartDisplay
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
import androidx.compose.ui.layout.layout
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.net.toUri
import com.wrentv.tv.data.CastMember
import com.wrentv.tv.data.Extras
import com.wrentv.tv.data.Item
import com.wrentv.tv.data.ItemDetail
import com.wrentv.tv.data.MediaFileView
import com.wrentv.tv.data.OfflineItem
import com.wrentv.tv.data.Ratings
import com.wrentv.tv.data.UserPrefs
import com.wrentv.tv.data.languageName
import com.wrentv.tv.ui.PlayTarget
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/** A movie, show, season or episode: artwork on top, details, actions, episodes, cast and more like this. */
@Composable
fun DetailScreen(itemId: String, seed: Item?) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val context = LocalContext.current
    val m = metrics()
    var detail by remember(itemId) { mutableStateOf<ItemDetail?>(null) }
    var error by remember(itemId) { mutableStateOf<String?>(null) }
    var extras by remember(itemId) { mutableStateOf<Extras?>(null) }
    var extrasLoaded by remember(itemId) { mutableStateOf(false) }
    var prefs by remember { mutableStateOf<UserPrefs?>(null) }
    var versionId by remember(itemId) { mutableStateOf<String?>(null) }
    var seasonId by remember(itemId) { mutableStateOf<String?>(null) }
    var episodes by remember(itemId) { mutableStateOf<List<Item>>(emptyList()) }
    var reload by remember { mutableStateOf(0) }

    LaunchedEffect(itemId, state.refreshTick, reload) {
        try { detail = state.api?.item(itemId) } catch (e: CancellationException) { throw e } catch (e: Exception) { error = e.message }
    }
    LaunchedEffect(itemId) {
        extras = runCatching { state.api?.extras(itemId) }.getOrNull()
        extrasLoaded = true
        if (prefs == null) prefs = runCatching { state.api?.prefs() }.getOrNull()
    }
    val d = detail
    val seasons = d?.children?.takeIf { d.item.kind == "show" }.orEmpty()
    LaunchedEffect(seasonId, seasons.map { it.id }, state.refreshTick) {
        if (seasons.isEmpty()) return@LaunchedEffect
        val target = seasons.firstOrNull { it.id == seasonId }
            ?: d?.nextEpisode?.let { n -> seasons.firstOrNull { it.id == n.parentId } }
            ?: seasons.firstOrNull { it.seasonNumber > 0 } ?: seasons.first()
        if (seasonId == null) seasonId = target.id
        runCatching { state.api?.item(target.id) }.getOrNull()?.let { episodes = it.children.orEmpty() }
    }

    val item = d?.item ?: seed
    fun play(it: Item, fromStart: Boolean) {
        val showTitle = if (it.kind == "episode") d?.show?.title ?: d?.item?.title?.takeIf { d.item.kind == "show" } ?: it.showTitle ?: "" else null
        nav.play(PlayTarget(it.id, showTitle ?: it.title, if (it.kind == "episode") "${it.episodeLabel} — ${it.title}" else null, fromStart,
            fileId = if (it.id == itemId) versionId else null))
    }

    Box(Modifier.fillMaxSize()) {
        if (item == null) {
            if (error != null) EmptyState(Icons.Rounded.Warning, "Couldn't load", error) else Loading()
        } else LazyColumn(Modifier.fillMaxSize()) {
            item("hero") {
                val backdrop = item.backdropPath ?: d?.show?.backdropPath ?: item.stillPath
                Box(Modifier.fillMaxWidth().height(330.dp)) {
                    RemoteImage(state.api?.imageUrl(backdrop, "w780"), Modifier.fillMaxSize())
                    Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0.5f to Color.Transparent, 0.75f to Color.Black.copy(alpha = 0.4f), 1f to Wren.background)))
                }
            }
            item("info") {
                Column(Modifier.pullUp(64.dp).padding(horizontal = m.gutter), verticalArrangement = Arrangement.spacedBy(14.dp)) {
                    TitleBlock(item, d)
                    MetaLine(item, d, versionId)
                    val r = extras?.ratings
                    if (r != null && (item.kind == "movie" || item.kind == "show")) Sideways(m.gutter) { RatingsRow(r) }
                    item.tagline?.takeIf { it.isNotEmpty() }?.let { Text(it, fontStyle = FontStyle.Italic, color = Wren.secondary, style = MaterialTheme.typography.titleMedium) }
                    item.overview?.takeIf { it.isNotEmpty() }?.let { Text(it, maxLines = 6, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyMedium) }
                    val files = d?.files.orEmpty()
                    if (item.isPlayable && files.isNotEmpty()) Sideways(m.gutter) { TrackRow(files, versionId, prefs) { versionId = it } }
                    Sideways(m.gutter) {
                        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                            if (item.isPlayable) {
                                val resume = item.inProgress
                                val left = item.progress?.let(::timeLeft)
                                PrimaryButton(if (resume) left?.let { "Resume · $it" } ?: "Resume" else "Play", Icons.Rounded.PlayArrow, { play(item, false) })
                                if (resume) GlassButton("Start Over", Icons.Rounded.Replay, { play(item, true) })
                            } else if (item.kind == "show") d?.nextEpisode?.let { next ->
                                PrimaryButton("${if (next.inProgress) "Resume" else "Play"} ${next.episodeLabel}", Icons.Rounded.PlayArrow, { play(next, false) })
                            }
                            extras?.trailer?.let { t ->
                                GlassButton("Trailer", Icons.Rounded.SmartDisplay, {
                                    runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, "https://www.youtube.com/watch?v=${t.youtubeKey}".toUri())) }
                                })
                            }
                            d?.onWatchlist?.let { listed -> WatchlistButton(item, listed) }
                            val scope = rememberCoroutineScope()
                            GlassButton(if (item.isWatched) "Watched" else "Mark Watched", if (item.isWatched) Icons.Rounded.CheckCircle else Icons.Rounded.CheckCircleOutline, {
                                scope.launch { runCatching { state.api?.setWatched(item.id, !item.isWatched) }; reload++ }
                            })
                            if (item.isPlayable && d?.canDownload == true && (!d.files.isNullOrEmpty() || d.streaming == true)) {
                                DownloadButton(item, d.show, versionId)
                            }
                        }
                    }
                }
            }
            if (seasons.isNotEmpty()) {
                item("seasons") {
                    SeasonHeader(seasons, seasonId, { seasonId = it }, if (d?.canDownload == true) episodes else emptyList(), d?.item)
                }
                items(episodes, { "e" + it.id }) { ep -> EpisodeRow(ep, state.downloads.item(ep.id)) { play(ep, false) } }
            } else if (item.kind == "season") {
                items(d?.children.orEmpty(), { "e" + it.id }) { ep -> EpisodeRow(ep, state.downloads.item(ep.id)) { play(ep, false) } }
            }
            if (item.kind == "movie" || item.kind == "show") {
                val ex = extras
                if (ex != null) {
                    if (ex.cast.isNotEmpty()) item("cast") { Box(Modifier.padding(top = 20.dp)) { CastShelf(ex.cast) } }
                    if (ex.moreLikeThis.isNotEmpty()) item("more") {
                        Box(Modifier.padding(top = 28.dp)) { Shelf("More Like This") { items(ex.moreLikeThis, { it.id }) { TitleCard(it, width = m.poster) } } }
                    }
                } else if (!extrasLoaded) item("skeleton") { Box(Modifier.padding(top = 20.dp)) { SkeletonShelf("More Like This") } }
            }
            d?.files?.takeIf { it.isNotEmpty() }?.let { files -> item("files") { FileSection(files, state.isAdmin) } }
            item("end") { Spacer(Modifier.height(32.dp)) }
        }
        // The back arrow floats over the artwork.
        IconButton({ nav.back() }, Modifier.statusBarsPadding().padding(8.dp).clip(CircleShape).background(Color.Black.copy(alpha = 0.4f))) {
            Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Back")
        }
    }
}

/** Draws the content higher, over the artwork, without leaving a gap below it. */
private fun Modifier.pullUp(by: androidx.compose.ui.unit.Dp) = layout { measurable, constraints ->
    val p = measurable.measure(constraints)
    val dy = by.roundToPx()
    layout(p.width, (p.height - dy).coerceAtLeast(0)) { p.place(0, -dy) }
}

/** Rows that would overflow a phone scroll sideways, edge to edge. */
@Composable
private fun Sideways(gutter: androidx.compose.ui.unit.Dp, content: @Composable () -> Unit) {
    Box(Modifier.padding(end = 0.dp).horizontalScroll(rememberScrollState()).padding(end = gutter)) { content() }
}

@Composable
private fun TitleBlock(item: Item, d: ItemDetail?) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val logo = item.logoPath ?: d?.show?.logoPath
    if (item.kind == "episode" || item.kind == "season") {
        d?.show?.let { show ->
            Text(show.title, Modifier.clickable { nav.push(Route.Detail(show.id, show)) }, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.SemiBold, color = Wren.secondary)
        }
        Text(if (item.kind == "episode") "${item.episodeLabel} — ${item.title}" else item.displayTitle, fontSize = 26.sp, fontWeight = FontWeight.Bold, lineHeight = 30.sp)
    } else if (logo != null) {
        RemoteImage(state.api?.imageUrl(logo, "w500"), Modifier.widthIn(max = 280.dp).fillMaxWidth().height(110.dp), ContentScale.Fit)
    } else {
        Text(item.title, fontSize = 30.sp, fontWeight = FontWeight.Bold, lineHeight = 34.sp)
    }
}

@Composable
private fun MetaLine(item: Item, d: ItemDetail?, versionId: String?) {
    val nav = LocalNavigator.current
    val parts = listOfNotNull(
        item.year?.toString(), item.contentRating?.takeIf { it.isNotEmpty() },
        item.runtimeMin?.takeIf { it > 0 }?.let { formatDuration(it * 60.0) }, item.rating?.takeIf { it > 0 }?.let { "★ %.1f".format(it) },
    )
    val genres = item.genres.orEmpty().take(3)
    val file = d?.files?.firstOrNull { it.id == versionId } ?: d?.files?.firstOrNull()
    Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(10.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(parts.joinToString("  ·  "), color = Wren.secondary)
        if (item.kind == "movie" || item.kind == "show") genres.forEach { g ->
            Text(g, Modifier.clip(CircleShape).background(Color.White.copy(alpha = 0.08f)).clickable { nav.push(Route.Genre(g)) }.padding(horizontal = 9.dp, vertical = 3.dp),
                style = MaterialTheme.typography.bodyMedium)
        } else if (genres.isNotEmpty()) Text(genres.joinToString(", "), color = Wren.secondary)
        file?.badges?.forEach { Badge(it) }
    }
}

/** Rotten Tomatoes, IMDb, Metacritic and TMDB scores. */
@Composable
private fun RatingsRow(r: Ratings) {
    @Composable
    fun pill(content: @Composable () -> Unit) = Row(
        Modifier.clip(RoundedCornerShape(7.dp)).background(Color.White.copy(alpha = 0.08f)).border(1.dp, Color.White.copy(alpha = 0.12f), RoundedCornerShape(7.dp))
            .padding(horizontal = 9.dp, vertical = 5.dp),
        horizontalArrangement = Arrangement.spacedBy(5.dp), verticalAlignment = Alignment.CenterVertically,
    ) { content() }
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        r.rottenTomatoes?.takeIf { it > 0 }?.let { rt ->
            pill { Text(if (rt >= 60) "🍅" else "🟢", fontSize = 12.sp); Text("$rt%", fontWeight = FontWeight.Bold) }
        }
        r.imdb?.takeIf { it > 0 }?.let { v ->
            pill {
                Text("IMDb", fontSize = 11.sp, fontWeight = FontWeight.Black, color = Color.Black,
                    modifier = Modifier.clip(RoundedCornerShape(3.dp)).background(Color(0xFFF5C518)).padding(horizontal = 4.dp, vertical = 1.dp))
                Text("%.1f".format(v), fontWeight = FontWeight.Bold)
            }
        }
        r.metacritic?.takeIf { it > 0 }?.let { mc -> pill { Text("MC", fontSize = 11.sp, fontWeight = FontWeight.Black); Text("$mc", fontWeight = FontWeight.Bold) } }
        r.tmdb?.takeIf { it > 0 }?.let { t ->
            pill { Text("TMDB", fontSize = 11.sp, fontWeight = FontWeight.Black, color = Color(0xFF00D1D9)); Text("${Math.round(t * 10)}%", fontWeight = FontWeight.Bold) }
        }
    }
}

/** What will play: VERSION · AUDIO · SUBTITLES, each from the viewer's settings unless they pick something else. */
@Composable
private fun TrackRow(files: List<MediaFileView>, versionId: String?, prefs: UserPrefs?, pick: (String?) -> Unit) {
    val context = LocalContext.current
    val picked = files.firstOrNull { it.id == versionId }
    val best = picked ?: files.maxByOrNull { it.width }
    val quality = best?.badges?.firstOrNull() ?: ""
    fun name(f: MediaFileView) = f.label ?: ((f.badges.firstOrNull()?.let { "$it · " } ?: "") + Formatter.formatShortFileSize(context, f.size))
    var open by remember { mutableStateOf(false) }
    Row(horizontalArrangement = Arrangement.spacedBy(22.dp), verticalAlignment = Alignment.CenterVertically) {
        if (files.size > 1) {
            Box {
                TrackLabel("Version", picked?.let(::name) ?: if (quality.isEmpty()) "Auto" else "Auto · $quality", menu = true, Modifier.clickable { open = true })
                DropdownMenu(open, { open = false }) {
                    DropdownMenuItem({ Text("Automatic") }, { open = false; pick(null) })
                    files.forEach { f -> DropdownMenuItem({ Text(name(f)) }, { open = false; pick(f.id) }) }
                }
            }
        } else TrackLabel("Version", quality.ifEmpty { "Original" })
        TrackLabel("Audio", languageName(prefs?.audioLanguage) ?: "Default")
        TrackLabel("Subtitles", languageName(prefs?.subtitleLanguage) ?: "Off")
    }
}

@Composable
private fun TrackLabel(title: String, value: String, menu: Boolean = false, modifier: Modifier = Modifier) {
    Row(modifier, horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(title.uppercase(), fontSize = 10.sp, fontWeight = FontWeight.Bold, letterSpacing = 0.6.sp, color = Color.White.copy(alpha = 0.55f))
        Text(value, color = Color.White.copy(alpha = 0.9f), style = MaterialTheme.typography.bodyMedium)
        if (menu) Icon(Icons.Rounded.ExpandMore, null, Modifier.size(14.dp), tint = Color.White.copy(alpha = 0.6f))
    }
}

/** Adds a movie or show to the watchlist, or takes it off. Flips at once; the server catches up behind it. */
@Composable
private fun WatchlistButton(item: Item, listed: Boolean) {
    val state = LocalAppState.current
    val scope = rememberCoroutineScope()
    var on by remember(listed) { mutableStateOf(listed) }
    GlassButton(if (on) "On Watchlist" else "Watchlist", if (on) Icons.Rounded.Bookmark else Icons.Rounded.BookmarkBorder, {
        val next = !on
        on = next
        scope.launch {
            try { state.api?.setWatchlist(item.id, next); state.refreshTick++ } catch (e: CancellationException) { throw e } catch (e: Exception) { on = !next }
        }
    })
}

/** Saves the title to this phone, or shows how far along it is. */
@Composable
private fun DownloadButton(item: Item, show: Item?, fileId: String?) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val x = state.downloads.item(item.id)
    when {
        x == null -> GlassButton("Download", Icons.Rounded.Download, { state.downloads.add(item, show, fileId, state.server?.name ?: "") })
        x.status == OfflineItem.Status.DONE -> GlassButton("Downloaded", Icons.Rounded.DownloadDone, { nav.push(Route.Downloads) })
        x.status == OfflineItem.Status.FAILED -> GlassButton("Retry Download", Icons.Rounded.Download, { state.downloads.resume(x.id) })
        else -> GlassButton(if (x.size > 0) "Downloading ${(x.fraction * 100).toInt()}%" else "Downloading…", Icons.Rounded.ArrowCircleDown, { nav.push(Route.Downloads) })
    }
}

@Composable
private fun SeasonHeader(seasons: List<Item>, seasonId: String?, pick: (String) -> Unit, downloadable: List<Item>, show: Item?) {
    val state = LocalAppState.current
    val m = metrics()
    var open by remember { mutableStateOf(false) }
    val current = seasons.firstOrNull { it.id == seasonId } ?: seasons.first()
    Row(Modifier.fillMaxWidth().padding(horizontal = m.gutter).padding(bottom = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        Box(Modifier.weight(1f)) {
            Row(Modifier.clip(RoundedCornerShape(8.dp)).clickable { open = true }.padding(vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(current.displayTitle, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                Icon(Icons.Rounded.ExpandMore, null)
            }
            DropdownMenu(open, { open = false }) {
                seasons.forEach { s -> DropdownMenuItem({ Text(s.displayTitle) }, { open = false; pick(s.id) }) }
            }
        }
        if (downloadable.isNotEmpty()) {
            val missing = downloadable.filter { state.downloads.item(it.id) == null }
            TextButton({ missing.forEach { state.downloads.add(it, show, server = state.server?.name ?: "") } }, enabled = missing.isNotEmpty()) {
                Text(if (missing.isEmpty()) "Season Downloaded" else "Download Season")
            }
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun EpisodeRow(ep: Item, offline: OfflineItem?, onPlay: () -> Unit) {
    val state = LocalAppState.current
    val m = metrics()
    var menu by remember { mutableStateOf(false) }
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp))
            .combinedClickable(onClick = onPlay, onLongClick = { menu = true })
            .padding(horizontal = m.gutter, vertical = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Box(Modifier.width(136.dp).height(77.dp).clip(RoundedCornerShape(6.dp))) {
            RemoteImage(state.api?.imageUrl(ep.stillPath, "w300"), Modifier.fillMaxSize())
            ep.progress?.let { p -> if (p.position > 0 && !p.watched) ProgressStrip(p.fraction) }
            ItemMenu(ep, menu, { menu = false })
        }
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                Text("${ep.episodeNumber}. ${ep.title}", Modifier.weight(1f, fill = false), fontWeight = FontWeight.Bold, maxLines = 2, overflow = TextOverflow.Ellipsis,
                    style = MaterialTheme.typography.bodyMedium)
                if (ep.isWatched) Icon(Icons.Rounded.CheckCircle, "Watched", Modifier.size(16.dp), tint = Wren.ember)
                if (offline != null) Icon(if (offline.status == OfflineItem.Status.DONE) Icons.Rounded.DownloadDone else Icons.Rounded.ArrowCircleDown, "Downloaded",
                    Modifier.size(16.dp), tint = Wren.secondary)
            }
            Text(listOfNotNull(ep.airDate?.takeIf { it.isNotEmpty() }, ep.runtimeMin?.takeIf { it > 0 }?.let { formatDuration(it * 60.0) }).joinToString("  "),
                color = Wren.secondary, style = MaterialTheme.typography.bodySmall)
            ep.overview?.let { Text(it, maxLines = 2, overflow = TextOverflow.Ellipsis, color = Wren.secondary, style = MaterialTheme.typography.bodySmall) }
        }
    }
}

@Composable
private fun CastShelf(cast: List<CastMember>) {
    val state = LocalAppState.current
    Shelf("Cast") {
        items(cast.size) { i ->
            val c = cast[i]
            Column(Modifier.width(100.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Box(Modifier.size(88.dp).clip(CircleShape).border(1.dp, Color.White.copy(alpha = 0.12f), CircleShape), contentAlignment = Alignment.Center) {
                    RemoteImage(state.api?.imageUrl(c.profilePath, "w185"), Modifier.fillMaxSize())
                    if (c.profilePath == null) Text(c.name.split(" ").take(2).mapNotNull { it.firstOrNull() }.joinToString(""), fontWeight = FontWeight.Bold, color = Wren.secondary)
                }
                Text(c.name, maxLines = 1, overflow = TextOverflow.Ellipsis, fontWeight = FontWeight.Medium, style = MaterialTheme.typography.bodyMedium)
                c.character?.takeIf { it.isNotEmpty() }?.let { Text(it, maxLines = 1, overflow = TextOverflow.Ellipsis, color = Wren.secondary, style = MaterialTheme.typography.bodySmall) }
            }
        }
    }
}

@Composable
private fun FileSection(files: List<MediaFileView>, admin: Boolean) {
    val context = LocalContext.current
    val m = metrics()
    Column(Modifier.padding(horizontal = m.gutter, vertical = 24.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Text("Media", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
        files.forEach { f ->
            Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(f.fileName, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodySmall.copy(fontFamily = androidx.compose.ui.text.font.FontFamily.Monospace))
                val bits = listOfNotNull(
                    f.info?.video?.firstOrNull()?.let { v -> "${v.codec.uppercase()} ${v.width ?: 0}×${v.height ?: 0}${v.hdr?.let { " $it" } ?: ""}" },
                    (f.info?.audio?.firstOrNull { it.default } ?: f.info?.audio?.firstOrNull())?.codecLabel,
                    f.size.takeIf { it > 0 }?.let { Formatter.formatShortFileSize(context, it) },
                    if (f.streamId != null && admin) f.release?.let { "Stream · $it" } ?: "Stream" else null,
                )
                Text(bits.joinToString("  ·  "), color = Wren.secondary, style = MaterialTheme.typography.bodySmall, maxLines = 2)
            }
        }
    }
}

