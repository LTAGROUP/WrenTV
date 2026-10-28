@file:OptIn(UnstableApi::class)

package com.wrentv.tv.ui.screens

import androidx.compose.animation.core.animateDpAsState
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.BookmarkAdd
import androidx.compose.material.icons.rounded.BookmarkAdded
import androidx.compose.material.icons.rounded.Dashboard
import androidx.compose.material.icons.rounded.FilterList
import androidx.compose.material.icons.rounded.History
import androidx.compose.material.icons.rounded.LiveTv
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Search
import androidx.compose.material.icons.rounded.Star
import androidx.compose.material.icons.rounded.StarBorder
import androidx.compose.material.icons.rounded.ViewList
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
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.AspectRatioFrameLayout
import androidx.media3.ui.PlayerView
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.Icon
import androidx.tv.material3.LocalContentColor
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import coil3.compose.AsyncImage
import com.wrentv.tv.data.LiveChannel
import com.wrentv.tv.data.LiveGenres
import com.wrentv.tv.data.LiveMatchup
import com.wrentv.tv.data.LiveProgram
import com.wrentv.tv.data.LiveSearchResult
import com.wrentv.tv.data.LiveTeam
import com.wrentv.tv.data.LiveTile
import com.wrentv.tv.data.WrenTVApi
import com.wrentv.tv.player.LivePreview
import com.wrentv.tv.ui.ArtCard
import com.wrentv.tv.ui.Bar
import com.wrentv.tv.ui.CardCaption
import com.wrentv.tv.ui.Chip
import com.wrentv.tv.ui.Choice
import com.wrentv.tv.ui.ChoicePanel
import com.wrentv.tv.ui.EmptyState
import com.wrentv.tv.ui.ListRow
import com.wrentv.tv.ui.Loading
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.LocalTopBar
import com.wrentv.tv.ui.PageGutter
import com.wrentv.tv.ui.Panel
import com.wrentv.tv.ui.PillButton
import com.wrentv.tv.ui.ReportScroll
import com.wrentv.tv.ui.Route
import com.wrentv.tv.ui.Shelf
import com.wrentv.tv.ui.TopBarSpace
import com.wrentv.tv.ui.TvTextField
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.rememberFocus
import com.wrentv.tv.ui.rememberLoaded
import kotlinx.coroutines.android.awaitFrame
import kotlinx.coroutines.delay
import java.text.DateFormat
import java.text.SimpleDateFormat
import java.util.Calendar
import java.util.Date
import java.util.Locale

/** How much of the schedule the guide shows at once, and how far one page moves it. */
private const val SPAN = 2 * 3600L
private const val STEP = 1800L
/** How far ahead the guide is loaded. */
private const val GUIDE_HOURS = 6

private val ChannelColumn = 260.dp
private val StarGold = Color(0xFFF5C451)
private val LiveRed = Color(0xFFD7263D)
private val RowHeight = 66.dp
private val CardWidth = 248.dp

/** A channel and programme the options panel is open for. [ids] are the channels the player flips through. */
private data class Pick(val ch: LiveChannel, val program: LiveProgram?, val ids: List<String>?, val follow: Boolean = true)

/**
 * Live TV, like YouTube TV: an overview of games, the viewer's channels and
 * shows, and what's on by kind; and a guide of channels down the side and
 * their programmes across a two-hour window. Holding OK on anything opens
 * its options: favorite the channel, follow the show.
 */
@Composable
fun LiveTvScreen() {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val bar = LocalTopBar.current
    // What's on changes by the minute: reload in place.
    var tick by remember { mutableLongStateOf(0L) }
    LaunchedEffect(Unit) { while (true) { delay(60_000); tick++ } }
    val loaded = rememberLoaded("livetv", refresh = tick) { state.loadLiveChannels() }
    val channels = loaded.value
    val byId = remember(channels) { channels.orEmpty().associateBy { it.id } }
    val now = remember(tick) { System.currentTimeMillis() / 1000 }

    // "overview" or "guide".
    var view by rememberSaveable { mutableStateOf(state.store.string("liveView", "overview")) }
    LaunchedEffect(view) { state.store.setString("liveView", view) }
    // "all", "favorites", "recent", "genre:<kind>" or "group:<name>".
    var filter by rememberSaveable { mutableStateOf(state.store.string("liveFilter", "all")) }
    LaunchedEffect(filter) { state.store.setString("liveFilter", filter) }
    var options by remember { mutableStateOf<Pick?>(null) }
    var searching by remember { mutableStateOf(false) }
    var pickingGroup by remember { mutableStateOf(false) }

    val groups = remember(channels) { channels?.map { it.groupName }?.distinct().orEmpty() }
    val genres = remember(channels) { channels?.mapNotNull { it.genre }?.toSet().orEmpty().let { have -> LiveGenres.filter { it.first in have } } }
    val chips = buildList {
        add(Triple("all", "All", null))
        add(Triple("favorites", "Favorites", Icons.Rounded.Star))
        if (state.liveRecents.isNotEmpty()) add(Triple("recent", "Recent", Icons.Rounded.History))
        genres.forEach { (k, name) -> add(Triple("genre:$k", name, null)) }
        // Providers have hundreds of groups: they're picked from a list, not a chip each.
        if (groups.size > 1) add(Triple("groups", if (filter.startsWith("group:")) filter.removePrefix("group:") else "Groups", Icons.Rounded.FilterList))
    }
    val shown = remember(channels, filter, now, state.liveFavorites, state.liveRecents) {
        val all = channels.orEmpty()
        when {
            filter == "favorites" -> all.filter { it.id in state.liveFavorites }
            filter == "recent" -> state.liveRecents.mapNotNull { id -> byId[id] }
            filter.startsWith("genre:") -> all.filter { it.genre == filter.removePrefix("genre:") }.sortedBy { !it.onAir(now) }
            filter.startsWith("group:") -> all.inGroup(filter.removePrefix("group:"), now)
            else -> all.sortedBy { !it.onAir(now) }
        }
    }
    val watch = { ch: LiveChannel, ids: List<String>? ->
        state.watchedChannel(ch.id)
        nav.push(Route.LivePlayer(ch.id, ids = ids?.takeIf { ch.id in it }))
    }

    // The focused channel plays muted up top once focus rests on it.
    val context = LocalContext.current
    val preview = remember { LivePreview(context, { state.api }, state.scope) }
    DisposableEffect(preview) { onDispose { preview.release() } }

    val top by animateDpAsState(if (bar.atTop || bar.focused) TopBarSpace else 24.dp, label = "live-top")
    Column(Modifier.fillMaxSize().padding(top = top)) {
        Row(
            Modifier.fillMaxWidth().padding(horizontal = PageGutter),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Chip("Overview", view == "overview", { view = "overview" }, Modifier.rememberFocus("tab:overview"), icon = Icons.Rounded.Dashboard)
            Chip("Guide", view == "guide", { view = "guide" }, Modifier.rememberFocus("tab:guide"), icon = Icons.Rounded.ViewList)
            if (view == "guide" && channels.orEmpty().isNotEmpty()) {
                Box(Modifier.padding(horizontal = 8.dp).width(1.dp).height(26.dp).background(Wren.line))
                // Coming into the row lands on the chosen filter, kept in view, not whichever chip is nearest.
                val chipList = rememberLazyListState()
                val chosen = remember { FocusRequester() }
                val chosenKey = if (filter.startsWith("group:")) "groups" else filter
                val chosenIndex = chips.indexOfFirst { it.first == chosenKey }.coerceAtLeast(0)
                LaunchedEffect(chips.size, chosenIndex) {
                    val info = chipList.layoutInfo
                    val item = info.visibleItemsInfo.firstOrNull { it.index == chosenIndex }
                    // Wholly in view, not cut off by the row's end.
                    if (item == null || item.offset + item.size > info.viewportEndOffset) chipList.scrollToItem(chosenIndex)
                }
                LazyRow(
                    state = chipList,
                    horizontalArrangement = Arrangement.spacedBy(10.dp),
                    modifier = Modifier.weight(1f).focusProperties { onEnter = { runCatching { chosen.requestFocus() } } },
                ) {
                    items(chips, key = { "chip:${it.first}" }) { (key, label, icon) ->
                        Chip(
                            label, key == chosenKey, { if (key == "groups") pickingGroup = true else filter = key },
                            (if (key == chips[chosenIndex].first) Modifier.focusRequester(chosen) else Modifier).rememberFocus("chip:$key"),
                            icon = icon,
                        )
                    }
                }
            } else {
                Spacer(Modifier.weight(1f))
            }
            PillButton("Search", { searching = true }, Modifier.rememberFocus("search"), icon = Icons.Rounded.Search)
        }

        when {
            channels == null && loaded.error != null -> EmptyState("Couldn't load channels", loaded.error, icon = Icons.Rounded.LiveTv)
            channels == null -> Loading()
            channels.isEmpty() -> EmptyState("No channels yet", "Your server's admin can add an IPTV playlist in the web dashboard.", icon = Icons.Rounded.LiveTv)
            view == "guide" -> when {
                shown.isEmpty() && filter == "favorites" -> EmptyState("No favorites yet", "Hold OK on a channel and choose Add to favorites.", icon = Icons.Rounded.Star)
                shown.isEmpty() -> EmptyState("No channels here", null, icon = Icons.Rounded.LiveTv)
                else -> Guide(
                    shown, now, tick, preview,
                    onWatch = { ch -> watch(ch, shown.map { it.id }) },
                    onOptions = { ch, p -> options = Pick(ch, p, shown.map { it.id }) },
                )
            }
            else -> Overview(
                byId, now, tick, preview,
                onWatch = watch,
                onOptions = { options = it },
            )
        }
    }

    options?.let { pick ->
        OptionsPanel(pick, now, onDismiss = { options = null }, onWatch = { watch(pick.ch, pick.ids) })
    }
    if (searching) {
        SearchPanel(
            byId, now, onDismiss = { searching = false },
            onWatch = { ch -> searching = false; watch(ch, null) },
            onOptions = { ch, p -> searching = false; options = Pick(ch, p, null) },
        )
    }
    if (pickingGroup) {
        ChoicePanel(
            "Channel groups",
            groups.map { g -> Choice("group:$g", g, "${channels.orEmpty().count { it.groupName == g }} channels") },
            filter, onDismiss = { pickingGroup = false },
        ) { filter = it }
    }
}

// Overview

/**
 * Shelves of what's on, like YouTube TV's home: games with the teams'
 * logos, the viewer's shows and channels, then movies, news and the rest.
 * The focused card fills the panel up top, its channel playing muted.
 */
@Composable
private fun Overview(
    byId: Map<String, LiveChannel>,
    now: Long,
    tick: Long,
    preview: LivePreview,
    onWatch: (LiveChannel, List<String>?) -> Unit,
    onOptions: (Pick) -> Unit,
) {
    val state = LocalAppState.current
    val list = rememberLazyListState()
    ReportScroll(list)
    // Following a show or starring a channel changes the shelves.
    val loaded = rememberLoaded("livetv-overview", refresh = listOf(tick / 2, state.liveShows, state.liveFavorites)) { state.api?.liveOverview() }
    val shelves = loaded.value?.map { sh -> sh to sh.tiles.filter { it.channelId in byId } }?.filter { it.second.isNotEmpty() }
    var focused by remember { mutableStateOf<LiveTile?>(null) }
    val hero = focused ?: shelves?.firstOrNull()?.second?.firstOrNull()
    val heroCh = hero?.let { byId[it.channelId] }
    // Upcoming programmes show their art; only what's on now plays.
    val onNow = hero?.program?.let { it.start <= now } ?: true
    LaunchedEffect(heroCh?.id, onNow) { preview.show(heroCh?.takeIf { onNow }) }

    when {
        shelves == null && loaded.error != null -> EmptyState(
            "Couldn't load the overview",
            // An older server has no overview.
            (if (loaded.error == "not found") "Your WrenTV server needs an update for it." else loaded.error) + " The guide still works: it's the tab next to this one.",
            icon = Icons.Rounded.LiveTv,
        )
        shelves == null -> Loading()
        shelves.isEmpty() -> EmptyState("Nothing on right now", "Your provider's guide has no programmes for now. Try the guide.", icon = Icons.Rounded.LiveTv)
        else -> Column(Modifier.fillMaxSize()) {
            if (hero != null && heroCh != null) Hero(hero, heroCh, state.api, now, preview, heroCh.id in state.liveFavorites)
            LazyColumn(state = list, contentPadding = PaddingValues(bottom = 48.dp)) {
                itemsIndexed(shelves, key = { _, (sh, _) -> "shelf:${sh.id}" }) { si, (sh, tiles) ->
                    val ids = tiles.map { it.channelId }.distinct()
                    Shelf(sh.title) {
                        itemsIndexed(tiles, key = { _, t -> "${t.channelId}:${t.program?.start}" }) { i, t ->
                            val ch = byId.getValue(t.channelId)
                            val upcoming = (t.program?.start ?: 0) > now
                            val pick = Pick(ch, t.program, ids, follow = t.matchup == null)
                            TileCard(
                                t, ch, state.api, now,
                                onFocus = { focused = t },
                                onClick = { if (upcoming) onOptions(pick) else onWatch(ch, ids) },
                                onLongClick = { onOptions(pick) },
                                modifier = Modifier.rememberFocus("ov:${sh.id}:${t.channelId}:${t.program?.start}", default = si == 0 && i == 0),
                            )
                        }
                    }
                }
            }
        }
    }
}

/** The focused card's picture, live when it's on now, and what it is. */
@Composable
private fun Hero(tile: LiveTile, ch: LiveChannel, api: WrenTVApi?, now: Long, preview: LivePreview, favorite: Boolean) {
    val p = tile.program
    val m = tile.matchup
    Row(
        Modifier.fillMaxWidth().height(206.dp).padding(start = PageGutter, end = PageGutter, top = 14.dp, bottom = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(26.dp),
    ) {
        Box(Modifier.fillMaxHeight().aspectRatio(16f / 9f).clip(RoundedCornerShape(14.dp))) {
            TileArt(tile, ch, api, big = true, modifier = Modifier.fillMaxSize())
            LivePicture(preview, ch)
            if (p == null || p.start <= now) LiveBadge(Modifier.align(Alignment.TopStart).padding(10.dp))
        }
        Column(Modifier.weight(1f).padding(top = 4.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                if (p != null && p.isLive && p.start <= now) LiveBadge()
                if (p != null && p.isNew) LiveBadge("NEW", Color.White.copy(alpha = 0.22f))
                m?.league?.let { LiveBadge(it.uppercase(), Color.White.copy(alpha = 0.22f)) }
                if (favorite) Icon(Icons.Rounded.Star, null, Modifier.size(16.dp), tint = StarGold)
                Text(
                    listOfNotNull(ch.number?.takeIf { it.isNotEmpty() }, ch.name).joinToString("  "),
                    style = MaterialTheme.typography.labelLarge, color = Wren.secondary, maxLines = 1, overflow = TextOverflow.Ellipsis,
                )
            }
            Text(
                m?.let { "${it.away.name} at ${it.home.name}" } ?: p?.title ?: ch.name,
                style = MaterialTheme.typography.headlineMedium, maxLines = 2, overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(top = 6.dp),
            )
            if (p != null) {
                Text(
                    listOfNotNull(timing(p, now), p.episode?.takeIf { it.isNotEmpty() }).joinToString(" · "),
                    style = MaterialTheme.typography.bodyMedium, color = Wren.secondary, maxLines = 1, modifier = Modifier.padding(top = 2.dp),
                )
                if (p.airing(now)) Bar(p.progress(now), Modifier.padding(top = 8.dp).width(320.dp).height(3.dp))
                p.description?.takeIf { it.isNotEmpty() }?.let {
                    Text(it, style = MaterialTheme.typography.bodyMedium, color = Wren.tertiary, maxLines = 2, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 6.dp))
                }
            }
            Spacer(Modifier.weight(1f))
            Text(
                if (p != null && p.start > now) "OK for options" else "OK to watch · Hold OK for options",
                style = MaterialTheme.typography.labelMedium, color = Wren.tertiary,
            )
        }
    }
}

/** A programme on a shelf: the game, its art or its channel, with what's live or when it starts. */
@Composable
private fun TileCard(
    tile: LiveTile,
    ch: LiveChannel,
    api: WrenTVApi?,
    now: Long,
    onFocus: () -> Unit,
    onClick: () -> Unit,
    onLongClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val p = tile.program
    var focused by remember { mutableStateOf(false) }
    Column {
        ArtCard(
            onClick,
            modifier.width(CardWidth).aspectRatio(16f / 9f).onFocusChanged {
                focused = it.isFocused
                if (it.isFocused) onFocus()
            },
            onLongClick = onLongClick,
        ) {
            TileArt(tile, ch, api, big = false, modifier = Modifier.fillMaxSize())
            Row(Modifier.align(Alignment.TopStart).padding(8.dp), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                when {
                    p == null -> {}
                    p.start > now -> LiveBadge(startsAt(p.start, now), Color.Black.copy(alpha = 0.7f))
                    p.isLive -> LiveBadge()
                }
                if (p != null && p.isNew) LiveBadge("NEW", Wren.ember)
            }
            if (p != null && p.airing(now)) Bar(p.progress(now), Modifier.align(Alignment.BottomStart).fillMaxWidth().height(4.dp))
        }
        val m = tile.matchup
        CardCaption(
            m?.let { "${it.away.short ?: it.away.name} at ${it.home.short ?: it.home.name}" } ?: p?.title ?: ch.name,
            when {
                p == null -> ch.groupName
                p.start > now -> ch.name
                else -> "${ch.name} · ${minutesLeft(p, now)}"
            },
            focused, CardWidth,
        )
    }
}

/** A tile's picture: the matchup, the programme's art over its channel's logo, or the logo alone. */
@Composable
private fun TileArt(tile: LiveTile, ch: LiveChannel, api: WrenTVApi?, big: Boolean, modifier: Modifier = Modifier) {
    val m = tile.matchup
    val art = (tile.art ?: tile.program?.icon)?.takeIf { it.isNotEmpty() }?.let { if (it.startsWith("http")) it else api?.absolute(it) }
    Box(modifier.background(Brush.linearGradient(listOf(Wren.surfaceHigh, Wren.surface)))) {
        if (m != null) {
            MatchupArt(m, big, Modifier.fillMaxSize())
        } else {
            // Under the art, so a picture that doesn't load leaves the channel.
            ChannelLogo(ch, api, Modifier.fillMaxSize().padding(horizontal = if (big) 64.dp else 48.dp, vertical = if (big) 36.dp else 28.dp))
            if (art != null) AsyncImage(model = art, contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize())
        }
        if (m != null || art != null) {
            // Which channel it's on, like the network bug on YouTube TV's cards.
            ChannelLogo(
                ch, api,
                Modifier.align(Alignment.BottomEnd).padding(8.dp).width(if (big) 72.dp else 54.dp).height(if (big) 38.dp else 28.dp)
                    .background(Color.Black.copy(alpha = 0.6f), RoundedCornerShape(6.dp)),
            )
        }
    }
}

/** Two teams over their colours, like a scoreboard. */
@Composable
private fun MatchupArt(m: LiveMatchup, big: Boolean, modifier: Modifier = Modifier) {
    val away = teamColor(m.away) ?: Color(0xFF2B2833)
    val home = teamColor(m.home) ?: Color(0xFF3A3542)
    Box(modifier.background(Brush.linearGradient(listOf(away, home), start = Offset.Zero, end = Offset.Infinite))) {
        // Darkened, so white logos and names read on any team's colour.
        Box(Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.32f)))
        Row(
            Modifier.fillMaxSize().padding(horizontal = 12.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.SpaceEvenly,
        ) {
            TeamMark(m.away, if (big) 76.dp else 54.dp)
            Text("@", style = if (big) MaterialTheme.typography.headlineMedium else MaterialTheme.typography.titleMedium, color = Color.White.copy(alpha = 0.8f))
            TeamMark(m.home, if (big) 76.dp else 54.dp)
        }
    }
}

@Composable
private fun TeamMark(t: LiveTeam, size: Dp) {
    Column(Modifier.width(size * 1.5f), horizontalAlignment = Alignment.CenterHorizontally) {
        val logo = t.logo?.takeIf { it.isNotEmpty() }
        if (logo != null) {
            AsyncImage(model = logo, contentDescription = t.name, contentScale = ContentScale.Fit, modifier = Modifier.size(size))
        } else {
            Box(Modifier.size(size).background(Color.White.copy(alpha = 0.16f), CircleShape), contentAlignment = Alignment.Center) {
                Text(
                    t.name.split(' ').filter { it.isNotEmpty() }.take(2).joinToString("") { it.take(1) }.uppercase(),
                    style = MaterialTheme.typography.titleLarge, color = Color.White,
                )
            }
        }
        Text(
            t.short ?: t.name, style = MaterialTheme.typography.labelMedium, color = Color.White, maxLines = 1, overflow = TextOverflow.Ellipsis,
            modifier = Modifier.padding(top = 4.dp),
        )
    }
}

private fun teamColor(t: LiveTeam): Color? =
    t.color?.takeIf { it.length == 6 }?.toLongOrNull(16)?.let { Color(0xFF000000 or it) }

// Guide

/**
 * Channels down the side and their programmes across a two-hour window, the
 * focused one up top. Left and right move along the schedule, paging it
 * half an hour at a time at the edges.
 */
@Composable
private fun Guide(
    shown: List<LiveChannel>,
    now: Long,
    tick: Long,
    preview: LivePreview,
    onWatch: (LiveChannel) -> Unit,
    onOptions: (LiveChannel, LiveProgram?) -> Unit,
) {
    val state = LocalAppState.current
    val list = rememberLazyListState()
    ReportScroll(list)
    val guide = rememberLoaded("livetv-guide", refresh = tick / 10) {
        state.api?.liveGuide(GUIDE_HOURS)?.associate { it.id to it.programs }
    }.value.orEmpty()
    val base = now - now % STEP
    var paged by rememberSaveable { mutableLongStateOf(base) }
    // Once time passes the window's start, the window follows.
    val windowStart = maxOf(paged, base)
    val lastStart = base + GUIDE_HOURS * 3600L - SPAN
    var focused by remember { mutableStateOf<Pair<LiveChannel, LiveProgram?>?>(null) }
    val programs = { ch: LiveChannel -> guide[ch.id]?.takeIf { it.isNotEmpty() } ?: listOfNotNull(ch.now, ch.next) }
    val previewing = focused?.first ?: state.lastChannel?.let { id -> shown.firstOrNull { it.id == id } } ?: shown.firstOrNull()
    LaunchedEffect(previewing?.id) { preview.show(previewing) }

    Column(Modifier.fillMaxSize()) {
        Details(
            focused ?: previewing?.let { it to it.current(programs(it), now) }, state.api, now, preview,
            state.liveFavorites,
        )
        BoxWithConstraints(Modifier.fillMaxSize().padding(horizontal = PageGutter)) {
            val timeline = maxWidth - ChannelColumn - 8.dp
            Column {
                Ruler(windowStart, now, timeline)
                LazyColumn(
                    state = list,
                    contentPadding = PaddingValues(bottom = 48.dp),
                    verticalArrangement = Arrangement.spacedBy(4.dp),
                ) {
                    items(shown, key = { "row:${it.id}" }) { ch ->
                        GuideRow(
                            ch, programs(ch), state.api, now, windowStart, timeline,
                            first = ch === shown.first(),
                            favorite = ch.id in state.liveFavorites,
                            onFocus = { p -> focused = ch to p },
                            onPage = { delta ->
                                val next = (windowStart + delta).coerceIn(base, maxOf(base, lastStart))
                                if (next != windowStart) { paged = next; true } else false
                            },
                            onClick = { onWatch(ch) },
                            onLongClick = { p -> onOptions(ch, p) },
                        )
                    }
                }
            }
        }
    }
}

/** The focused programme and its channel's muted live picture, like YouTube TV's top panel. */
@Composable
private fun Details(
    sel: Pair<LiveChannel, LiveProgram?>?,
    api: WrenTVApi?,
    now: Long,
    preview: LivePreview,
    favorites: Set<String>,
) {
    Row(
        Modifier.fillMaxWidth().height(164.dp).padding(horizontal = PageGutter, vertical = 12.dp),
        horizontalArrangement = Arrangement.spacedBy(22.dp),
    ) {
        if (sel == null) return@Row
        val (ch, p) = sel
        Box(Modifier.fillMaxHeight().aspectRatio(16f / 9f).clip(RoundedCornerShape(12.dp)).background(Wren.surfaceHigh)) {
            val art = p?.icon?.takeIf { it.isNotEmpty() }
            ChannelLogo(ch, api, Modifier.fillMaxSize().padding(horizontal = 50.dp, vertical = 30.dp))
            if (art != null) AsyncImage(model = art, contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize())
            LivePicture(preview, ch)
            LiveBadge(Modifier.align(Alignment.TopStart).padding(8.dp))
        }
        Column(Modifier.weight(1f)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                if (p != null && p.isLive) LiveBadge()
                if (p != null && p.isNew) LiveBadge("NEW", Color.White.copy(alpha = 0.22f))
                if (ch.id in favorites) Icon(Icons.Rounded.Star, null, Modifier.size(16.dp), tint = StarGold)
                Text(
                    // With no programme the channel's name is the title below: say where it lives instead.
                    listOfNotNull(ch.number?.takeIf { it.isNotEmpty() }, if (p == null) ch.groupName else ch.name).joinToString("  "),
                    style = MaterialTheme.typography.labelLarge, color = Wren.secondary, maxLines = 1, overflow = TextOverflow.Ellipsis,
                )
            }
            Text(
                p?.title ?: ch.name, style = MaterialTheme.typography.headlineMedium, maxLines = 1, overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(top = 4.dp),
            )
            if (p != null) {
                Text(
                    listOfNotNull(timing(p, now), p.episode?.takeIf { it.isNotEmpty() }).joinToString(" · "),
                    style = MaterialTheme.typography.bodyMedium, color = Wren.secondary, maxLines = 1, modifier = Modifier.padding(top = 2.dp),
                )
                if (p.airing(now)) Bar(p.progress(now), Modifier.padding(top = 6.dp).width(320.dp).height(3.dp))
                p.description?.takeIf { it.isNotEmpty() }?.let {
                    Text(it, style = MaterialTheme.typography.bodyMedium, color = Wren.tertiary, maxLines = 2, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 4.dp))
                }
            } else {
                Text("No guide information", style = MaterialTheme.typography.bodyMedium, color = Wren.secondary, modifier = Modifier.padding(top = 2.dp))
            }
            Spacer(Modifier.weight(1f))
            Text("Hold OK for options: favorite the channel, follow the show", style = MaterialTheme.typography.labelMedium, color = Wren.tertiary, maxLines = 1)
        }
    }
}

/** [ch]'s muted live picture, faded in once it's moving. */
@Composable
private fun BoxScope.LivePicture(preview: LivePreview, ch: LiveChannel) {
    if (preview.channelId != ch.id) return
    val alpha by animateFloatAsState(if (preview.showing) 1f else 0f, label = "preview")
    AndroidView(
        factory = { ctx ->
            PlayerView(ctx).apply {
                useController = false
                resizeMode = AspectRatioFrameLayout.RESIZE_MODE_ZOOM
                setShowBuffering(PlayerView.SHOW_BUFFERING_NEVER)
                isFocusable = false
                player = preview.player
            }
        },
        modifier = Modifier.matchParentSize().alpha(alpha),
    )
}

@Composable
private fun LiveBadge(modifier: Modifier = Modifier) = LiveBadge("LIVE", LiveRed, modifier)

@Composable
private fun LiveBadge(text: String, color: Color, modifier: Modifier = Modifier) {
    Text(
        text, style = MaterialTheme.typography.labelSmall, color = Color.White, maxLines = 1,
        modifier = modifier.background(color, RoundedCornerShape(4.dp)).padding(horizontal = 6.dp, vertical = 2.dp),
    )
}

/** Half-hour marks over the timeline, and where "now" is. */
@Composable
private fun Ruler(start: Long, now: Long, width: Dp) {
    Row(Modifier.fillMaxWidth().height(30.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(
            if (start <= now) "Today" else timeOf(start), style = MaterialTheme.typography.labelMedium, color = Wren.secondary,
            modifier = Modifier.width(ChannelColumn + 8.dp).padding(start = 4.dp),
        )
        Box(Modifier.width(width).fillMaxHeight()) {
            var t = start
            while (t < start + SPAN) {
                Text(
                    timeOf(t), style = MaterialTheme.typography.labelMedium, color = Wren.secondary,
                    modifier = Modifier.offset(x = width * ((t - start).toFloat() / SPAN)).align(Alignment.CenterStart).padding(start = 6.dp),
                )
                t += STEP
            }
            if (now in start until start + SPAN) {
                Box(
                    Modifier.offset(x = width * ((now - start).toFloat() / SPAN) - 4.dp).align(Alignment.BottomStart)
                        .size(8.dp).background(Wren.ember, CircleShape),
                )
                Box(
                    Modifier.offset(x = width * ((now - start).toFloat() / SPAN) - 1.dp).align(Alignment.BottomStart)
                        .width(2.dp).height(6.dp).background(Wren.ember),
                )
            }
        }
    }
}

@Composable
private fun GuideRow(
    ch: LiveChannel,
    programs: List<LiveProgram>,
    api: WrenTVApi?,
    now: Long,
    start: Long,
    width: Dp,
    first: Boolean,
    favorite: Boolean,
    onFocus: (LiveProgram?) -> Unit,
    /** Moves the window by the given seconds; false when it can't go further. */
    onPage: (Long) -> Boolean,
    onClick: () -> Unit,
    /** Holding OK opens the options for the channel and the programme held on. */
    onLongClick: (LiveProgram?) -> Unit,
) {
    val end = start + SPAN
    val visible = programs.filter { it.end > start && it.start < end }
    Row(Modifier.fillMaxWidth().height(RowHeight), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        GuideCell(
            onClick, Modifier.width(ChannelColumn).onFocusChanged { if (it.isFocused) onFocus(ch.current(programs, now)) }
                .rememberFocus("ch:${ch.id}"),
            onLongClick = { onLongClick(ch.current(programs, now)) },
        ) {
            Row(Modifier.fillMaxSize().padding(horizontal = 12.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    ChannelLogo(ch, api, Modifier.width(60.dp).height(34.dp))
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                        if (favorite) Icon(Icons.Rounded.Star, null, Modifier.size(11.dp), tint = StarGold)
                        ch.number?.takeIf { it.isNotEmpty() }?.let {
                            Text(it, style = MaterialTheme.typography.labelSmall, maxLines = 1, color = LocalContentColor.current.copy(alpha = 0.6f))
                        }
                    }
                }
                // Two lines: provider names are long ("F1 TV 01: Racing …") and the end is what tells them apart.
                Text(ch.name, style = MaterialTheme.typography.titleSmall, maxLines = 2, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
            }
        }
        Box(Modifier.width(width).fillMaxHeight()) {
            if (visible.isEmpty()) {
                GuideCell(
                    onClick, Modifier.fillMaxSize().onFocusChanged { if (it.isFocused) onFocus(null) }
                        .rememberFocus("p:${ch.id}:none", default = first),
                    onLongClick = { onLongClick(null) },
                ) {
                    Text(
                        "No guide information", style = MaterialTheme.typography.bodyMedium, maxLines = 1,
                        color = LocalContentColor.current.copy(alpha = 0.6f), modifier = Modifier.align(Alignment.CenterStart).padding(horizontal = 14.dp),
                    )
                }
            }
            visible.forEachIndexed { i, p ->
                val from = maxOf(p.start, start)
                val to = minOf(p.end, end)
                val airing = p.airing(now)
                GuideCell(
                    onClick,
                    Modifier.offset(x = width * ((from - start).toFloat() / SPAN))
                        .width(width * ((to - from).toFloat() / SPAN) - 4.dp)
                        .fillMaxHeight()
                        .onFocusChanged { if (it.isFocused) onFocus(p) }
                        .onPreviewKeyEvent { e ->
                            if (e.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                            when {
                                e.key == Key.DirectionRight && p.end >= end -> onPage(STEP)
                                e.key == Key.DirectionLeft && p.start <= start -> onPage(-STEP)
                                else -> false
                            }
                        }
                        .rememberFocus("p:${ch.id}:${p.start}", default = first && i == 0),
                    airing = airing,
                    onLongClick = { onLongClick(p) },
                ) {
                    Column(Modifier.fillMaxSize().padding(horizontal = 14.dp), verticalArrangement = Arrangement.Center) {
                        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                            if (p.isLive) LiveBadge()
                            if (p.isNew) LiveBadge("NEW", Wren.ember)
                            Text(p.title, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        }
                        Text(
                            if (airing) "${timeOf(p.start)} – ${timeOf(p.end)}" else timeOf(p.start),
                            style = MaterialTheme.typography.bodySmall, maxLines = 1, color = LocalContentColor.current.copy(alpha = 0.6f),
                        )
                    }
                    if (airing) Bar(p.progress(now), Modifier.align(Alignment.BottomStart).fillMaxWidth().height(3.dp))
                }
            }
        }
    }
}

@Composable
private fun GuideCell(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    airing: Boolean = false,
    onLongClick: (() -> Unit)? = null,
    content: @Composable BoxScope.() -> Unit,
) {
    Surface(
        onClick = onClick,
        onLongClick = onLongClick,
        modifier = modifier,
        shape = ClickableSurfaceDefaults.shape(RoundedCornerShape(10.dp)),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = if (airing) Wren.surfaceHigh else Wren.surface, contentColor = Wren.text,
            focusedContainerColor = Color.White, focusedContentColor = Color.Black,
        ),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1f),
    ) {
        Box(Modifier.fillMaxSize().clip(RoundedCornerShape(10.dp)), content = content)
    }
}

// Panels

/** What can be done with a channel and the programme on it: watch, favorite, follow. */
@Composable
private fun OptionsPanel(pick: Pick, now: Long, onDismiss: () -> Unit, onWatch: () -> Unit) {
    val state = LocalAppState.current
    val ch = pick.ch
    val p = pick.program?.takeIf { it.title.isNotBlank() }
    Panel(onDismiss, p?.title ?: ch.name) {
        val first = remember { FocusRequester() }
        LaunchedEffect(Unit) { awaitFrame(); runCatching { first.requestFocus() } }
        Text(
            listOfNotNull(ch.name, p?.let { timing(it, now) }).joinToString(" · "),
            style = MaterialTheme.typography.bodyMedium, color = Wren.secondary, maxLines = 1, overflow = TextOverflow.Ellipsis,
        )
        Spacer(Modifier.height(16.dp))
        Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
            ListRow(
                if (p != null && p.start > now) "Watch ${ch.name} now" else "Watch now",
                { onDismiss(); onWatch() },
                Modifier.focusRequester(first),
                detail = if (p != null && p.start > now) ch.now?.title?.let { "On now: $it" } else null,
                leading = { Icon(Icons.Rounded.PlayArrow, null, Modifier.size(24.dp)) },
            )
            val favorite = ch.id in state.liveFavorites
            ListRow(
                if (favorite) "Remove from favorites" else "Add channel to favorites",
                { state.toggleLiveFavorite(ch.id) },
                detail = if (favorite) "${ch.name} is in your favorites" else "${ch.name} goes first in the guide and on the overview",
                leading = { Icon(if (favorite) Icons.Rounded.Star else Icons.Rounded.StarBorder, null, Modifier.size(24.dp)) },
            )
            if (p != null && pick.follow) {
                val following = state.followsShow(p.title)
                ListRow(
                    if (following) "Stop following “${p.title}”" else "Follow “${p.title}”",
                    { state.toggleLiveShow(p.title) },
                    detail = if (following) "Its airings are under Your shows on the overview" else "Its airings, on any channel, gather under Your shows on the overview",
                    leading = { Icon(if (following) Icons.Rounded.BookmarkAdded else Icons.Rounded.BookmarkAdd, null, Modifier.size(24.dp)) },
                )
            }
        }
    }
}

/** Channels and programmes by name, across the whole guide. */
@Composable
private fun SearchPanel(
    byId: Map<String, LiveChannel>,
    now: Long,
    onDismiss: () -> Unit,
    onWatch: (LiveChannel) -> Unit,
    onOptions: (LiveChannel, LiveProgram) -> Unit,
) {
    val state = LocalAppState.current
    var query by rememberSaveable { mutableStateOf("") }
    var result by remember { mutableStateOf<LiveSearchResult?>(null) }
    LaunchedEffect(query) {
        val q = query.trim()
        if (q.length < 2) { result = null; return@LaunchedEffect }
        delay(300)
        result = runCatching { state.api?.liveSearch(q) }.getOrNull()
    }
    Panel(onDismiss, "Search Live TV", width = 820.dp) {
        val field = remember { FocusRequester() }
        LaunchedEffect(Unit) { awaitFrame(); runCatching { field.requestFocus() } }
        TvTextField(query, { query = it }, "", Modifier.focusRequester(field), placeholder = "Channels, shows and games", width = 764.dp)
        Spacer(Modifier.height(12.dp))
        val r = result
        val airings = r?.airings.orEmpty().mapNotNull { a -> byId[a.channelId]?.let { it to a.program } }
        when {
            query.trim().length < 2 -> Text("Press OK to type.", style = MaterialTheme.typography.bodyMedium, color = Wren.tertiary)
            r == null -> Loading(Modifier.height(120.dp))
            r.channels.isEmpty() && airings.isEmpty() -> Text("Nothing matches “${query.trim()}”.", style = MaterialTheme.typography.bodyMedium, color = Wren.secondary)
            else -> LazyColumn(Modifier.heightIn(max = 420.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                items(r.channels, key = { "c:${it.id}" }) { ch ->
                    ListRow(
                        ch.name, { onWatch(ch) },
                        detail = ch.now?.title?.let { "On now: $it" } ?: ch.groupName,
                        leading = { ChannelLogo(ch, state.api, Modifier.width(64.dp).height(36.dp)) },
                    )
                }
                items(airings, key = { (ch, p) -> "a:${ch.id}:${p.start}" }) { (ch, p) ->
                    ListRow(
                        p.title, { if (p.start <= now) onWatch(ch) else onOptions(ch, p) },
                        detail = "${ch.name} · ${timing(p, now)}",
                        leading = { ChannelLogo(ch, state.api, Modifier.width(64.dp).height(36.dp)) },
                    )
                }
            }
        }
    }
}

/** A channel's logo, or its name when there's no logo. */
@Composable
fun ChannelLogo(ch: LiveChannel, api: WrenTVApi?, modifier: Modifier = Modifier) {
    val url = ch.logo?.takeIf { it.isNotEmpty() }?.let { if (it.startsWith("http")) it else api?.absolute(it) }
    Box(modifier.clip(RoundedCornerShape(8.dp)), contentAlignment = Alignment.Center) {
        if (url != null) {
            // No backdrop: logos are drawn for dark backgrounds, and a grey box around them looks broken.
            AsyncImage(model = url, contentDescription = ch.name, contentScale = ContentScale.Fit, modifier = Modifier.fillMaxSize().padding(4.dp))
        } else {
            Text(
                ch.name.take(4).uppercase(), style = MaterialTheme.typography.labelLarge, maxLines = 1,
                color = Wren.secondary, modifier = Modifier.padding(4.dp),
            )
        }
    }
}

private val LiveChannel.groupName: String get() = group?.takeIf { it.isNotBlank() } ?: "Channels"

private fun LiveProgram.airing(at: Long): Boolean = at >= start && at < end

/** The guide has a programme airing on it now. */
fun LiveChannel.onAir(at: Long): Boolean = now?.airing(at) == true

private fun LiveChannel.current(programs: List<LiveProgram>, at: Long): LiveProgram? =
    programs.firstOrNull { it.airing(at) } ?: now

/** [group]'s channels, those on air first, otherwise in the provider's order. */
fun List<LiveChannel>.inGroup(group: String, now: Long): List<LiveChannel> =
    filter { it.groupName == group }.sortedBy { !it.onAir(now) }

private val clock: DateFormat = DateFormat.getTimeInstance(DateFormat.SHORT)
private val weekday = SimpleDateFormat("EEE", Locale.getDefault())

fun timeRange(p: LiveProgram): String = "${clock.format(Date(p.start * 1000))} – ${clock.format(Date(p.end * 1000))}"

fun timeOf(seconds: Long): String = clock.format(Date(seconds * 1000))

/** "7:00 PM", "Tomorrow 7:00 PM" or "Sat 7:00 PM". */
private fun startsAt(start: Long, now: Long): String {
    val day = { s: Long -> Calendar.getInstance().apply { timeInMillis = s * 1000 }.let { it.get(Calendar.YEAR) * 400 + it.get(Calendar.DAY_OF_YEAR) } }
    return when (day(start) - day(now)) {
        0 -> timeOf(start)
        1 -> "Tomorrow ${timeOf(start)}"
        else -> "${weekday.format(Date(start * 1000))} ${timeOf(start)}"
    }
}

private fun minutesLeft(p: LiveProgram, now: Long): String {
    val left = (p.end - now) / 60
    return if (left >= 60) "${left / 60} hr ${left % 60} min left" else "$left min left"
}

/** When a programme is on, said for where it is in time. */
private fun timing(p: LiveProgram, now: Long): String = when {
    p.airing(now) -> "${timeRange(p)} · ${minutesLeft(p, now)}"
    p.start > now -> "Starts ${startsAt(p.start, now)} · ${(p.end - p.start) / 60} min"
    else -> timeRange(p)
}
