@file:OptIn(UnstableApi::class)

package com.wrentv.phone.ui

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.horizontalScroll
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
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.rounded.Clear
import androidx.compose.material.icons.rounded.LiveTv
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Search
import androidx.compose.material.icons.rounded.Star
import androidx.compose.material.icons.rounded.StarBorder
import androidx.compose.material.icons.rounded.Warning
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.AspectRatioFrameLayout
import androidx.media3.ui.PlayerView
import com.wrentv.tv.data.LiveChannel
import com.wrentv.tv.data.LiveGenres
import com.wrentv.tv.data.LiveProgram
import com.wrentv.tv.data.LiveSearchResult
import com.wrentv.tv.player.LivePreview
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import java.text.DateFormat
import java.util.Date

private fun nowSec() = System.currentTimeMillis() / 1000
private fun clock(sec: Long): String = DateFormat.getTimeInstance(DateFormat.SHORT).format(Date(sec * 1000))
private fun LiveProgram.timeRange() = "${clock(start)} – ${clock(end)}"
private val LiveRed = Color(0xFFD7263D)
private val StarGold = Color(0xFFF5C451)

/**
 * Live TV: the channel last watched playing muted at the top, filters
 * (favorites, recent, kinds of channel, the provider's groups), a search over
 * channels and upcoming shows, and what's on now and next per channel.
 * Tapping a channel watches it; holding it or its info button opens its
 * schedule, where it can be starred.
 */
@OptIn(ExperimentalFoundationApi::class, ExperimentalMaterial3Api::class)
@Composable
fun LiveTvScreen() {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val m = metrics()
    var guide by remember { mutableStateOf<Map<String, List<LiveProgram>>>(emptyMap()) }
    var loaded by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var query by rememberSaveable { mutableStateOf("") }
    // "all", "favorites", "recent", "genre:<kind>" or "group:<name>".
    var filter by rememberSaveable { mutableStateOf(state.store.string("liveFilter", "all")) }
    LaunchedEffect(filter) { state.store.setString("liveFilter", filter) }
    var now by remember { mutableLongStateOf(nowSec()) }
    var schedule by remember { mutableStateOf<LiveChannel?>(null) }
    var results by remember { mutableStateOf<LiveSearchResult?>(null) }

    suspend fun load() {
        try {
            state.loadLiveChannels()
            runCatching { state.api?.liveGuide(12) }.getOrNull()?.let { g -> guide = g.associate { it.id to it.programs } }
            error = null
        } catch (e: CancellationException) { throw e } catch (e: Exception) { error = e.message }
        now = nowSec()
        loaded = true
    }
    LaunchedEffect(state.refreshTick) { load() }
    LaunchedEffect(Unit) {
        var ticks = 0
        while (true) {
            delay(30_000)
            now = nowSec()
            if (++ticks % 10 == 0) load()
        }
    }

    val channels = state.liveChannels
    fun programs(c: LiveChannel) = guide[c.id] ?: listOfNotNull(c.now, c.next)
    fun onNow(c: LiveChannel) = programs(c).firstOrNull { it.start <= now && now < it.end }
    val groups = channels.mapNotNull { it.group?.takeIf(String::isNotEmpty) }.distinct()
    val genres = channels.mapNotNull { it.genre }.toSet().let { have -> LiveGenres.filter { it.first in have } }
    val q = query.trim()
    val searching = q.isNotEmpty()
    LaunchedEffect(q) {
        if (q.isEmpty()) { results = null; return@LaunchedEffect }
        delay(250)
        runCatching { state.api?.liveSearch(q) }.getOrNull()?.let { results = it }
    }
    val list = when {
        searching -> channels.filter { c ->
            c.name.contains(q, true) || c.number == q || (c.group ?: "").contains(q, true)
        }
        filter == "favorites" -> channels.filter { it.id in state.liveFavorites }
        filter == "recent" -> state.liveRecents.mapNotNull { id -> channels.firstOrNull { it.id == id } }
        filter.startsWith("genre:") -> channels.filter { it.genre == filter.removePrefix("genre:") }
        filter.startsWith("group:") -> channels.filter { it.group == filter.removePrefix("group:") }
        else -> channels
    }
    val shown = if (filter == "recent" && !searching) list else list.filter { onNow(it) != null } + list.filter { onNow(it) == null }
    val spotlight = state.liveRecents.firstNotNullOfOrNull { id -> channels.firstOrNull { it.id == id } } ?: channels.firstOrNull()

    fun play(c: LiveChannel) {
        schedule = null
        state.watchedChannel(c.id)
        nav.live = LiveTarget(c.id, null, shown.map { it.id }.takeIf { !searching && c in shown })
    }

    // One muted picture: the schedule's channel while it's open, otherwise the spotlight's.
    val context = LocalContext.current
    val preview = remember { LivePreview(context, { state.api }, state.scope) }
    DisposableEffect(preview) { onDispose { preview.release() } }
    val previewing = if (nav.live != null) null else schedule ?: spotlight.takeIf { !searching }
    LaunchedEffect(previewing?.id) { preview.show(previewing, after = if (schedule != null) 300 else 900) }

    Page("Live TV", if (loaded && channels.isNotEmpty()) "${shown.size} channel${if (shown.size == 1) "" else "s"}" else null) {
        when {
            !loaded -> Loading()
            error != null && channels.isEmpty() -> EmptyState(Icons.Rounded.Warning, "Couldn't load", error)
            channels.isEmpty() -> EmptyState(Icons.Rounded.LiveTv, "No channels",
                if (state.isAdmin) "Add an IPTV playlist on the server's web dashboard under Settings → Live TV." else "The server owner hasn't added any channels yet.")
            else -> LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 16.dp)) {
                item("search") {
                    TextField(
                        query, { query = it }, Modifier.fillMaxWidth().padding(horizontal = m.gutter, vertical = 6.dp),
                        placeholder = { Text("Search channels and shows") }, singleLine = true,
                        leadingIcon = { Icon(Icons.Rounded.Search, null) },
                        trailingIcon = { if (query.isNotEmpty()) IconButton({ query = "" }) { Icon(Icons.Rounded.Clear, "Clear") } },
                        shape = CircleShape,
                        colors = TextFieldDefaults.colors(
                            focusedContainerColor = Color.White.copy(alpha = 0.1f), unfocusedContainerColor = Color.White.copy(alpha = 0.1f),
                            focusedIndicatorColor = Color.Transparent, unfocusedIndicatorColor = Color.Transparent,
                        ),
                    )
                }
                if (!searching && spotlight != null) item("spotlight") {
                    Spotlight(spotlight, onNow(spotlight), programs(spotlight).firstOrNull { it.start > now }, now, preview,
                        Modifier.padding(horizontal = m.gutter, vertical = 6.dp)) { play(spotlight) }
                }
                if (!searching) item("filters") {
                    Row(
                        Modifier.horizontalScroll(rememberScrollState()).padding(horizontal = m.gutter, vertical = 6.dp),
                        horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Chip("All", filter == "all") { filter = "all" }
                        Chip("★ Favorites", filter == "favorites") { filter = "favorites" }
                        if (state.liveRecents.isNotEmpty()) Chip("Recent", filter == "recent") { filter = "recent" }
                        genres.forEach { (k, name) -> Chip(name, filter == "genre:$k") { filter = "genre:$k" } }
                        if (groups.size > 1) {
                            var open by remember { mutableStateOf(false) }
                            Box {
                                Chip(if (filter.startsWith("group:")) "${filter.removePrefix("group:")} ▾" else "Groups ▾", filter.startsWith("group:")) { open = true }
                                DropdownMenu(open, { open = false }) {
                                    groups.forEach { g -> DropdownMenuItem({ Text(g) }, { filter = "group:$g"; open = false }) }
                                }
                            }
                        }
                    }
                }
                if (searching && shown.isNotEmpty()) item("h-channels") { Header("Channels", m.gutter) }
                if (shown.isEmpty() && !searching) item("none") {
                    EmptyState(
                        if (filter == "favorites") Icons.Rounded.StarBorder else Icons.Rounded.LiveTv,
                        if (filter == "favorites") "No favorites yet" else "No channels here",
                        if (filter == "favorites") "Hold a channel and tap the star to add it." else null,
                        Modifier.fillMaxWidth().padding(top = 60.dp),
                    )
                }
                items(shown, { it.id }) { ch ->
                    val current = onNow(ch)
                    val next = programs(ch).firstOrNull { it.start > now }
                    Row(
                        Modifier.fillMaxWidth().combinedClickable(onClick = { play(ch) }, onLongClick = { schedule = ch })
                            .padding(start = m.gutter, end = 4.dp, top = 8.dp, bottom = 8.dp),
                        horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically,
                    ) {
                        ChannelLogo(ch.logo, ch.name, Modifier.size(64.dp, 40.dp))
                        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
                            Row(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
                                ch.number?.let { Text(it, color = Wren.secondary, fontSize = 12.sp) }
                                Text(ch.name, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyMedium)
                                if (ch.id in state.liveFavorites) Icon(Icons.Rounded.Star, null, Modifier.size(12.dp), tint = StarGold)
                            }
                            if (current != null) {
                                Row(horizontalArrangement = Arrangement.spacedBy(5.dp), verticalAlignment = Alignment.CenterVertically) {
                                    if (current.isLive) Badge("LIVE", LiveRed)
                                    if (current.isNew) Badge("NEW", Color.White.copy(alpha = 0.22f))
                                    Text(current.title, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyMedium)
                                }
                                Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                                    LinearProgressIndicator({ current.progress(now) }, Modifier.widthIn(max = 120.dp).weight(1f, fill = false),
                                        color = Wren.ember, trackColor = Color.White.copy(alpha = 0.15f), drawStopIndicator = {})
                                    Text("${(current.end - now) / 60} min left", color = Wren.secondary, fontSize = 11.sp, maxLines = 1)
                                }
                            } else Text("No guide", color = Wren.secondary, fontSize = 12.sp)
                            if (next != null && (!m.compact || current == null)) {
                                Text("Next: ${next.title} · ${clock(next.start)}", color = Wren.secondary, fontSize = 12.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            }
                        }
                        IconButton({ schedule = ch }) { Icon(Icons.Outlined.Info, "Schedule", tint = Wren.secondary) }
                    }
                    HorizontalDivider(Modifier.padding(start = m.gutter + 76.dp), color = Color.White.copy(alpha = 0.06f))
                }
                val airings = if (searching) results?.airings.orEmpty() else emptyList()
                if (airings.isNotEmpty()) item("h-shows") { Header("Shows", m.gutter) }
                items(airings, { "a:${it.channelId}:${it.program.start}" }) { a ->
                    val ch = channels.firstOrNull { it.id == a.channelId } ?: return@items
                    val p = a.program
                    val airing = p.start <= now && now < p.end
                    Row(
                        Modifier.fillMaxWidth().combinedClickable(onClick = { if (airing) play(ch) else schedule = ch })
                            .padding(horizontal = m.gutter, vertical = 8.dp),
                        horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Art(ch, p, Modifier.size(96.dp, 54.dp).clip(RoundedCornerShape(6.dp)))
                        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
                            Text(p.title, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyMedium)
                            Row(horizontalArrangement = Arrangement.spacedBy(5.dp), verticalAlignment = Alignment.CenterVertically) {
                                if (airing) Badge("ON NOW", Wren.ember)
                                if (p.isNew) Badge("NEW", Color.White.copy(alpha = 0.22f))
                                Text(
                                    listOfNotNull(p.episode, ch.name, if (airing) null else clock(p.start)).joinToString(" · "),
                                    color = Wren.secondary, fontSize = 12.sp, maxLines = 1, overflow = TextOverflow.Ellipsis,
                                )
                            }
                        }
                    }
                }
                if (searching && shown.isEmpty() && airings.isEmpty() && results != null) item("none") {
                    EmptyState(Icons.Rounded.Search, "No results", "Nothing matches “$q”.", Modifier.fillMaxWidth().padding(top = 60.dp))
                }
            }
        }
    }

    schedule?.let { ch ->
        val fav = ch.id in state.liveFavorites
        ModalBottomSheet({ schedule = null }, containerColor = Wren.surface) {
            Column(Modifier.fillMaxWidth()) {
                Row(Modifier.fillMaxWidth().padding(horizontal = 20.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    ChannelLogo(ch.logo, ch.name, Modifier.size(48.dp, 30.dp))
                    Text(ch.name, Modifier.weight(1f), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    IconButton({ state.toggleLiveFavorite(ch.id) }) {
                        Icon(if (fav) Icons.Rounded.Star else Icons.Rounded.StarBorder, if (fav) "Remove from Favorites" else "Add to Favorites",
                            tint = if (fav) StarGold else Wren.secondary)
                    }
                    TextButton({ play(ch) }) { Icon(Icons.Rounded.PlayArrow, null); Text("Watch") }
                }
                val upcoming = programs(ch).filter { it.end > now }
                LazyColumn(contentPadding = PaddingValues(20.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
                    item("picture") {
                        Box(Modifier.fillMaxWidth().aspectRatio(16f / 9f).clip(RoundedCornerShape(12.dp)).combinedClickable(onClick = { play(ch) })) {
                            Picture(ch, onNow(ch), preview, Modifier.fillMaxSize())
                            Badge("LIVE", LiveRed, Modifier.align(Alignment.TopStart).padding(10.dp))
                        }
                    }
                    if (upcoming.isEmpty()) item { Text("This channel has no guide.", color = Wren.secondary) }
                    items(upcoming, { it.start }) { p ->
                        val airing = p.start <= now
                        Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                                Text(clock(p.start), color = Wren.secondary, fontSize = 12.sp)
                                if (airing) Badge("ON NOW", Wren.ember)
                                if (p.isLive) Badge("LIVE", LiveRed)
                                if (p.isNew) Badge("NEW", Color.White.copy(alpha = 0.22f))
                            }
                            Text(p.title, fontWeight = FontWeight.SemiBold)
                            p.episode?.let { Text(it, color = Wren.secondary, fontSize = 12.sp) }
                            p.description?.takeIf { it.isNotEmpty() }?.let { Text(it, color = Wren.secondary, style = MaterialTheme.typography.bodySmall, maxLines = 3, overflow = TextOverflow.Ellipsis) }
                            if (airing) LinearProgressIndicator({ p.progress(now) }, Modifier.fillMaxWidth().clip(CircleShape), color = Wren.ember,
                                trackColor = Color.White.copy(alpha = 0.15f), drawStopIndicator = {})
                        }
                    }
                }
            }
        }
    }
}

/** The top of the page: a channel's muted live picture and what's on. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun Spotlight(ch: LiveChannel, p: LiveProgram?, next: LiveProgram?, now: Long, preview: LivePreview, modifier: Modifier, watch: () -> Unit) {
    Box(modifier.fillMaxWidth().aspectRatio(16f / 9f).clip(RoundedCornerShape(14.dp)).combinedClickable(onClick = watch)) {
        Picture(ch, p, preview, Modifier.fillMaxSize())
        Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0.45f to Color.Transparent, 1f to Color.Black.copy(alpha = 0.85f))))
        Column(Modifier.align(Alignment.BottomStart).padding(14.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Row(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
                Badge("LIVE", LiveRed)
                Text(listOfNotNull(ch.number, ch.name).joinToString(" · "), color = Color.White.copy(alpha = 0.85f), fontSize = 12.sp, fontWeight = FontWeight.SemiBold)
            }
            Text(p?.title ?: ch.name, color = Color.White, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (p != null) {
                LinearProgressIndicator({ p.progress(now) }, Modifier.fillMaxWidth().clip(CircleShape), color = Wren.ember,
                    trackColor = Color.White.copy(alpha = 0.2f), drawStopIndicator = {})
            }
            if (next != null) {
                Text("Up next · ${clock(next.start)}  ${next.title}", color = Color.White.copy(alpha = 0.7f), fontSize = 12.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
    }
}

/** A channel's muted live picture over the programme's art until it starts. */
@Composable
private fun Picture(ch: LiveChannel, p: LiveProgram?, preview: LivePreview, modifier: Modifier) {
    Box(modifier) {
        Art(ch, p, Modifier.fillMaxSize())
        if (preview.channelId == ch.id) {
            val alpha by animateFloatAsState(if (preview.showing) 1f else 0f, label = "preview")
            AndroidView(
                factory = { ctx ->
                    PlayerView(ctx).apply {
                        useController = false
                        resizeMode = AspectRatioFrameLayout.RESIZE_MODE_ZOOM
                        setShowBuffering(PlayerView.SHOW_BUFFERING_NEVER)
                        player = preview.player
                    }
                },
                modifier = Modifier.fillMaxSize().alpha(alpha),
            )
        }
    }
}

/** The programme's artwork, or the channel's logo large on a soft backdrop. */
@Composable
private fun Art(ch: LiveChannel, p: LiveProgram?, modifier: Modifier) {
    Box(modifier.background(Brush.radialGradient(listOf(Color(0xFF333333), Color(0xFF101010)))), contentAlignment = Alignment.Center) {
        val icon = p?.icon?.takeIf { it.isNotEmpty() }
        if (icon != null) RemoteImage(icon, Modifier.fillMaxSize())
        else ChannelLogo(ch.logo, ch.name, Modifier.fillMaxSize(0.4f))
    }
}

@Composable
private fun Badge(text: String, color: Color, modifier: Modifier = Modifier) {
    Text(
        text, color = Color.White, fontSize = 9.sp, fontWeight = FontWeight.ExtraBold, letterSpacing = 0.5.sp, maxLines = 1,
        modifier = modifier.background(color, RoundedCornerShape(3.dp)).padding(horizontal = 5.dp, vertical = 1.dp),
    )
}

@Composable
private fun Header(text: String, gutter: androidx.compose.ui.unit.Dp) {
    Text(text, Modifier.padding(start = gutter, top = 14.dp, bottom = 4.dp), style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.Bold)
}
