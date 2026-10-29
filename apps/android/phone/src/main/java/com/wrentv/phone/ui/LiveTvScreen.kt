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
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.rounded.Add
import androidx.compose.material.icons.rounded.Check
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
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.AspectRatioFrameLayout
import androidx.media3.ui.PlayerView
import coil3.compose.AsyncImage
import com.wrentv.tv.data.LiveChannel
import com.wrentv.tv.data.LiveGenres
import com.wrentv.tv.data.LiveMatchup
import com.wrentv.tv.data.LiveProgram
import com.wrentv.tv.data.LiveSearchResult
import com.wrentv.tv.data.LiveShelf
import com.wrentv.tv.data.LiveTeam
import com.wrentv.tv.data.LiveTile
import com.wrentv.tv.player.LivePreview
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import java.text.DateFormat
import java.text.SimpleDateFormat
import java.util.Calendar
import java.util.Date
import java.util.Locale

private fun nowSec() = System.currentTimeMillis() / 1000
private fun clock(sec: Long): String = DateFormat.getTimeInstance(DateFormat.SHORT).format(Date(sec * 1000))
private fun LiveProgram.timeRange() = "${clock(start)} – ${clock(end)}"
private val LiveRed = Color(0xFFD7263D)
private val StarGold = Color(0xFFF5C451)
private val weekday = SimpleDateFormat("EEE", Locale.getDefault())

/** "7:00 PM", "Tomorrow 7:00 PM" or "Sat 7:00 PM". */
private fun startsAt(start: Long, now: Long): String {
    val day = { s: Long -> Calendar.getInstance().apply { timeInMillis = s * 1000 }.let { it.get(Calendar.YEAR) * 400 + it.get(Calendar.DAY_OF_YEAR) } }
    return when (day(start) - day(now)) {
        0 -> clock(start)
        1 -> "Tomorrow ${clock(start)}"
        else -> "${weekday.format(Date(start * 1000))} ${clock(start)}"
    }
}

private fun LiveMatchup.title(short: Boolean = false) =
    if (short) "${away.short ?: away.name} at ${home.short ?: home.name}" else "${away.name} at ${home.name}"

/**
 * Live TV, as an overview or a guide. The overview leads with a game or show
 * on now playing muted, then shelves: live and upcoming games with the teams'
 * logos, the viewer's followed shows and channels, and what's on by kind.
 * The guide has the channel last watched at the top, filters (favorites,
 * recent, kinds of channel, the provider's groups) and what's on now and next
 * per channel. Search covers channels and upcoming shows. Tapping watches;
 * holding a card or a channel opens its options or schedule, where the
 * channel can be starred and its shows followed.
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
    var options by remember { mutableStateOf<LiveTile?>(null) }
    var results by remember { mutableStateOf<LiveSearchResult?>(null) }
    // "overview" or "guide".
    var view by rememberSaveable { mutableStateOf(state.store.string("liveView", "overview")) }
    LaunchedEffect(view) { state.store.setString("liveView", view) }
    var shelves by remember { mutableStateOf<List<LiveShelf>?>(null) }
    var overviewError by remember { mutableStateOf<String?>(null) }

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
    // The overview, again every couple of minutes and when what it shows changes.
    LaunchedEffect(view, state.refreshTick, state.liveShows, state.liveFavorites, state.liveRecents) {
        if (view != "overview") return@LaunchedEffect
        while (true) {
            try {
                shelves = state.api?.liveOverview()?.filter { it.tiles.isNotEmpty() }
                overviewError = null
            } catch (e: CancellationException) { throw e } catch (e: Exception) {
                overviewError = if (e.message?.contains("404") == true) "Your WrenTV server needs an update for the Live TV overview." else e.message
            }
            delay(120_000)
        }
    }

    // With more than one provider, the guide shows one of them ("" for all).
    var provider by rememberSaveable { mutableStateOf(state.store.string("liveProvider", "")) }
    LaunchedEffect(provider) { state.store.setString("liveProvider", provider) }
    val providers = state.liveProviders
    val chosenProvider = providers.takeIf { it.size > 1 }?.firstOrNull { it.id == provider }
    val everyChannel = state.liveChannels
    val channels = remember(everyChannel, chosenProvider) { chosenProvider?.let { p -> everyChannel.filter { it.source == p.id } } ?: everyChannel }
    val byId = remember(channels) { channels.associateBy { it.id } }
    // The overview and search span every provider, whichever the guide shows.
    val everyById = remember(everyChannel) { everyChannel.associateBy { it.id } }
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
        searching -> everyChannel.filter { c ->
            c.name.contains(q, true) || c.number == q || (c.group ?: "").contains(q, true)
        }
        filter == "favorites" -> channels.filter { it.id in state.liveFavorites }
        filter == "recent" -> state.liveRecents.mapNotNull { byId[it] }
        filter.startsWith("genre:") -> channels.filter { it.genre == filter.removePrefix("genre:") }
        filter.startsWith("group:") -> channels.filter { it.group == filter.removePrefix("group:") }
        else -> channels
    }
    val shown = if (filter == "recent" && !searching) list else list.filter { onNow(it) != null } + list.filter { onNow(it) == null }
    val spotlight = state.liveRecents.firstNotNullOfOrNull { byId[it] } ?: channels.firstOrNull()
    val overview = view == "overview" && !searching
    fun channel(t: LiveTile) = everyById[t.channelId]
    val heroTile = shelves?.firstNotNullOfOrNull { sh -> sh.tiles.firstOrNull { t -> channel(t) != null && t.program?.let { it.start <= now } != false } }

    // Channel up and down go through [ids]: the shelf, or the guide's list.
    fun play(c: LiveChannel, ids: List<String>? = shown.map { it.id }.takeIf { !searching && c in shown }) {
        schedule = null
        options = null
        state.watchedChannel(c.id)
        nav.live = LiveTarget(c.id, null, ids)
    }
    fun open(t: LiveTile, shelf: List<LiveTile>) {
        val ch = channel(t) ?: return
        if ((t.program?.start ?: 0) > now) options = t
        else play(ch, shelf.map { it.channelId }.distinct())
    }

    // One muted picture: the schedule's channel while it's open, otherwise the spotlight's.
    val context = LocalContext.current
    val preview = remember { LivePreview(context, { state.api }, state.scope) }
    DisposableEffect(preview) { onDispose { preview.release() } }
    val previewing = when {
        nav.live != null -> null
        schedule != null -> schedule
        options != null -> options?.let(::channel)?.takeIf { (options?.program?.start ?: 0) <= now }
        searching -> null
        overview -> heroTile?.let(::channel)
        else -> spotlight
    }
    LaunchedEffect(previewing?.id) { preview.show(previewing, after = if (schedule != null) 300 else 900) }

    Page("Live TV", if (loaded && channels.isNotEmpty() && !overview) "${shown.size} channel${if (shown.size == 1) "" else "s"}" else null) {
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
                if (!searching) item("views") {
                    Row(Modifier.padding(horizontal = m.gutter, vertical = 6.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Chip("Overview", view == "overview") { view = "overview" }
                        Chip("Guide", view == "guide") { view = "guide" }
                    }
                }
                if (overview) {
                    val all = shelves
                    when {
                        all == null && overviewError != null -> item("ov-error") {
                            EmptyState(Icons.Rounded.Warning, "No overview", overviewError, Modifier.fillMaxWidth().padding(top = 60.dp))
                        }
                        all == null -> item("ov-loading") { Box(Modifier.fillMaxWidth().padding(top = 60.dp), contentAlignment = Alignment.Center) { Loading() } }
                        all.isEmpty() -> item("ov-empty") {
                            EmptyState(Icons.Rounded.LiveTv, "Nothing to show yet", "Once the guide has loaded, games, your shows and what's on gather here.",
                                Modifier.fillMaxWidth().padding(top = 60.dp))
                        }
                        else -> {
                            val hero = heroTile
                            val heroCh = hero?.let(::channel)
                            if (hero != null && heroCh != null) item("hero") {
                                val shelf = all.first { hero in it.tiles }.tiles
                                Hero(hero, heroCh, now, preview, Modifier.padding(horizontal = m.gutter, vertical = 6.dp),
                                    watch = { open(hero, shelf) }, more = { options = hero })
                            }
                            items(all, { "shelf:${it.id}" }) { sh ->
                                Header(sh.title, m.gutter)
                                LazyRow(contentPadding = PaddingValues(horizontal = m.gutter, vertical = 6.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                                    items(sh.tiles, { "${it.channelId}:${it.program?.start}" }) { t ->
                                        val ch = channel(t) ?: return@items
                                        TileCard(t, ch, now, Modifier.width(if (m.compact) 220.dp else 260.dp), onClick = { open(t, sh.tiles) }, onLongClick = { options = t })
                                    }
                                }
                            }
                        }
                    }
                }
                if (!searching && !overview && spotlight != null) item("spotlight") {
                    Spotlight(spotlight, onNow(spotlight), programs(spotlight).firstOrNull { it.start > now }, now, preview,
                        Modifier.padding(horizontal = m.gutter, vertical = 6.dp)) { play(spotlight) }
                }
                if (!searching && !overview) item("filters") {
                    Row(
                        Modifier.horizontalScroll(rememberScrollState()).padding(horizontal = m.gutter, vertical = 6.dp),
                        horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically,
                    ) {
                        if (providers.size > 1) {
                            var open by remember { mutableStateOf(false) }
                            Box {
                                Chip("${chosenProvider?.name ?: "All providers"} ▾", chosenProvider != null) { open = true }
                                DropdownMenu(open, { open = false }) {
                                    DropdownMenuItem({ Text("All providers") }, { provider = ""; open = false })
                                    providers.forEach { p ->
                                        DropdownMenuItem({ Text(p.name) }, {
                                            provider = p.id
                                            // A group belongs to one provider's playlist.
                                            if (filter.startsWith("group:")) filter = "all"
                                            open = false
                                        })
                                    }
                                }
                            }
                        }
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
                if (shown.isEmpty() && !searching && !overview) item("none") {
                    EmptyState(
                        if (filter == "favorites") Icons.Rounded.StarBorder else Icons.Rounded.LiveTv,
                        if (filter == "favorites") "No favorites yet" else "No channels here",
                        if (filter == "favorites") "Hold a channel and tap the star to add it." else null,
                        Modifier.fillMaxWidth().padding(top = 60.dp),
                    )
                }
                items(if (overview) emptyList() else shown, { it.id }) { ch ->
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
                    val ch = everyById[a.channelId] ?: return@items
                    val p = a.program
                    val airing = p.start <= now && now < p.end
                    Row(
                        Modifier.fillMaxWidth().combinedClickable(onClick = { if (airing) play(ch) else options = LiveTile(ch.id, p) }, onLongClick = { options = LiveTile(ch.id, p) })
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
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                Text(p.title, Modifier.weight(1f), fontWeight = FontWeight.SemiBold)
                                FollowButton(p.title)
                            }
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

    options?.let { t ->
        val ch = channel(t) ?: return@let
        val p = t.program
        val upcoming = p != null && p.start > now
        val fav = ch.id in state.liveFavorites
        ModalBottomSheet({ options = null }, containerColor = Wren.surface) {
            Column(Modifier.fillMaxWidth().padding(start = 20.dp, end = 20.dp, bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Box(Modifier.fillMaxWidth().aspectRatio(16f / 9f).clip(RoundedCornerShape(12.dp))) {
                    if (upcoming) TileArt(t, ch, true, Modifier.fillMaxSize()) else Picture(ch, p, preview, Modifier.fillMaxSize(), t)
                }
                Row(Modifier.padding(top = 10.dp), horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
                    TileBadges(t, now)
                    Text(listOfNotNull(ch.number, ch.name).joinToString(" · "), color = Wren.secondary, fontSize = 12.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
                Text(t.matchup?.title() ?: p?.title ?: ch.name, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                if (p != null) Text(timing(p, now), color = Wren.secondary, fontSize = 13.sp)
                p?.description?.takeIf { it.isNotEmpty() }?.let {
                    Text(it, color = Wren.secondary, style = MaterialTheme.typography.bodySmall, maxLines = 4, overflow = TextOverflow.Ellipsis)
                }
                Spacer(8.dp)
                OptionRow(Icons.Rounded.PlayArrow, if (upcoming) "Watch ${ch.name} now" else "Watch now") { play(ch) }
                OptionRow(if (fav) Icons.Rounded.Star else Icons.Rounded.StarBorder, if (fav) "Remove channel from favorites" else "Add channel to favorites",
                    tint = if (fav) StarGold else Color.White) { state.toggleLiveFavorite(ch.id) }
                if (p != null && t.matchup == null) {
                    val following = state.followsShow(p.title)
                    OptionRow(if (following) Icons.Rounded.Check else Icons.Rounded.Add, if (following) "Stop following “${p.title}”" else "Follow “${p.title}”",
                        detail = "Its airings, on any channel, gather under Your shows on the overview") { state.toggleLiveShow(p.title) }
                }
            }
        }
    }
}

@Composable
private fun Spacer(h: Dp) = Box(Modifier.height(h))

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun OptionRow(icon: androidx.compose.ui.graphics.vector.ImageVector, label: String, detail: String? = null, tint: Color = Color.White, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).combinedClickable(onClick = onClick).padding(vertical = 12.dp, horizontal = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(16.dp), verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(icon, null, tint = tint)
        Column(Modifier.weight(1f)) {
            Text(label, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
            detail?.let { Text(it, color = Wren.secondary, fontSize = 12.sp) }
        }
    }
}

/** Follows a show, or stops; its airings then gather under Your shows. */
@Composable
private fun FollowButton(title: String) {
    val state = LocalAppState.current
    val following = state.followsShow(title)
    IconButton({ state.toggleLiveShow(title) }, Modifier.size(36.dp)) {
        Icon(if (following) Icons.Rounded.Check else Icons.Rounded.Add, if (following) "Stop following" else "Follow show",
            tint = if (following) Wren.ember else Wren.secondary)
    }
}

/** When a programme is on, said for where it is in time. */
private fun timing(p: LiveProgram, now: Long): String = when {
    p.start > now -> "${startsAt(p.start, now)} – ${clock(p.end)}"
    else -> "${p.timeRange()} · ${(p.end - now) / 60} min left"
}

@Composable
private fun TileBadges(t: LiveTile, now: Long) {
    val p = t.program ?: return
    when {
        p.start > now -> Badge(startsAt(p.start, now), Color.Black.copy(alpha = 0.75f))
        else -> {
            if (p.isLive) Badge("LIVE", LiveRed)
            if (p.isNew) Badge("NEW", Color.White.copy(alpha = 0.22f))
        }
    }
    t.matchup?.league?.takeIf { it.isNotEmpty() }?.let { Badge(it.uppercase(), Color.White.copy(alpha = 0.22f)) }
}

/** The overview's lead: a game or show on now, playing muted. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun Hero(t: LiveTile, ch: LiveChannel, now: Long, preview: LivePreview, modifier: Modifier, watch: () -> Unit, more: () -> Unit) {
    val p = t.program
    Box(modifier.fillMaxWidth().aspectRatio(16f / 9f).clip(RoundedCornerShape(14.dp)).combinedClickable(onClick = watch, onLongClick = more)) {
        Picture(ch, p, preview, Modifier.fillMaxSize(), t)
        Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0.45f to Color.Transparent, 1f to Color.Black.copy(alpha = 0.85f))))
        IconButton(more, Modifier.align(Alignment.TopEnd)) { Icon(Icons.Outlined.Info, "Options", tint = Color.White) }
        Column(Modifier.align(Alignment.BottomStart).padding(14.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Row(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
                TileBadges(t, now)
                Text(listOfNotNull(ch.number, ch.name).joinToString(" · "), color = Color.White.copy(alpha = 0.85f), fontSize = 12.sp, fontWeight = FontWeight.SemiBold)
            }
            Text(t.matchup?.title() ?: p?.title ?: ch.name, color = Color.White, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold,
                maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (p != null) {
                LinearProgressIndicator({ p.progress(now) }, Modifier.fillMaxWidth().clip(CircleShape), color = Wren.ember,
                    trackColor = Color.White.copy(alpha = 0.2f), drawStopIndicator = {})
                Text(timing(p, now), color = Color.White.copy(alpha = 0.7f), fontSize = 12.sp, maxLines = 1)
            }
        }
    }
}

/** A card on an overview shelf. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun TileCard(t: LiveTile, ch: LiveChannel, now: Long, modifier: Modifier, onClick: () -> Unit, onLongClick: () -> Unit) {
    val p = t.program
    val airing = p != null && p.start <= now && now < p.end
    Column(modifier.combinedClickable(onClick = onClick, onLongClick = onLongClick), verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Box(Modifier.fillMaxWidth().aspectRatio(16f / 9f).clip(RoundedCornerShape(10.dp))) {
            TileArt(t, ch, false, Modifier.fillMaxSize())
            Row(Modifier.align(Alignment.TopStart).padding(8.dp), horizontalArrangement = Arrangement.spacedBy(4.dp)) { TileBadges(t, now) }
            if (airing) LinearProgressIndicator({ p!!.progress(now) }, Modifier.align(Alignment.BottomCenter).fillMaxWidth().height(3.dp),
                color = Wren.ember, trackColor = Color.White.copy(alpha = 0.2f), drawStopIndicator = {}, gapSize = 0.dp)
        }
        Text(t.matchup?.title(short = true) ?: p?.title ?: ch.name, Modifier.padding(top = 6.dp), fontWeight = FontWeight.SemiBold,
            maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyMedium)
        Text(
            when {
                airing -> "${ch.name} · ${(p!!.end - now) / 60} min left"
                p != null -> ch.name
                else -> ch.group.orEmpty()
            },
            color = Wren.secondary, fontSize = 12.sp, maxLines = 1, overflow = TextOverflow.Ellipsis,
        )
    }
}

/** A tile's picture: the matchup, the programme's art over its channel's logo, or the logo alone. */
@Composable
private fun TileArt(t: LiveTile, ch: LiveChannel, big: Boolean, modifier: Modifier) {
    val api = LocalAppState.current.api
    val m = t.matchup
    val art = (t.art ?: t.program?.icon)?.takeIf { it.isNotEmpty() }?.let { if (it.startsWith("http")) it else api?.absolute(it) }
    Box(modifier.background(Brush.radialGradient(listOf(Color(0xFF333333), Color(0xFF101010)))), contentAlignment = Alignment.Center) {
        if (m != null) MatchupArt(m, big, Modifier.fillMaxSize())
        else {
            // Under the art, so a picture that doesn't load leaves the channel.
            ChannelLogo(ch.logo, ch.name, Modifier.fillMaxSize(0.42f))
            if (art != null) AsyncImage(art, null, Modifier.fillMaxSize(), contentScale = ContentScale.Crop)
        }
        if (m != null || art != null) {
            // Which channel it's on, like a network's bug.
            Box(Modifier.align(Alignment.BottomEnd).padding(8.dp).background(Color.Black.copy(alpha = 0.6f), RoundedCornerShape(6.dp))) {
                ChannelLogo(ch.logo, ch.name, Modifier.size(if (big) 60.dp else 46.dp, if (big) 34.dp else 26.dp))
            }
        }
    }
}

/** Two teams over their colours, like a scoreboard. */
@Composable
private fun MatchupArt(m: LiveMatchup, big: Boolean, modifier: Modifier) {
    val away = teamColor(m.away) ?: Color(0xFF2B2833)
    val home = teamColor(m.home) ?: Color(0xFF3A3542)
    Box(modifier.background(Brush.linearGradient(listOf(away, home), start = Offset.Zero, end = Offset.Infinite))) {
        // Darkened, so white logos and names read on any team's colour.
        Box(Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.32f)))
        Row(Modifier.fillMaxSize().padding(horizontal = 8.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.SpaceEvenly) {
            TeamMark(m.away, if (big) 64.dp else 44.dp)
            Text("@", style = if (big) MaterialTheme.typography.headlineSmall else MaterialTheme.typography.titleMedium, color = Color.White.copy(alpha = 0.8f))
            TeamMark(m.home, if (big) 64.dp else 44.dp)
        }
    }
}

@Composable
private fun TeamMark(t: LiveTeam, size: Dp) {
    Column(Modifier.width(size * 1.6f), horizontalAlignment = Alignment.CenterHorizontally) {
        val logo = t.logo?.takeIf { it.isNotEmpty() }
        if (logo != null) AsyncImage(logo, t.name, Modifier.size(size), contentScale = ContentScale.Fit)
        else Box(Modifier.size(size).background(Color.White.copy(alpha = 0.16f), CircleShape), contentAlignment = Alignment.Center) {
            Text(t.name.split(' ').filter { it.isNotEmpty() }.take(2).joinToString("") { it.take(1) }.uppercase(), color = Color.White, fontWeight = FontWeight.Bold)
        }
        Text(t.short ?: t.name, Modifier.padding(top = 4.dp), color = Color.White, fontSize = 12.sp, fontWeight = FontWeight.Bold,
            maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

private fun teamColor(t: LiveTeam): Color? =
    t.color?.takeIf { it.length == 6 }?.toLongOrNull(16)?.let { Color(0xFF000000 or it) }

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

/** A channel's muted live picture over the programme's (or tile's) art until it starts. */
@androidx.annotation.OptIn(UnstableApi::class)
@Composable
private fun Picture(ch: LiveChannel, p: LiveProgram?, preview: LivePreview, modifier: Modifier, tile: LiveTile? = null) {
    Box(modifier) {
        if (tile != null) TileArt(tile, ch, true, Modifier.fillMaxSize()) else Art(ch, p, Modifier.fillMaxSize())
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
