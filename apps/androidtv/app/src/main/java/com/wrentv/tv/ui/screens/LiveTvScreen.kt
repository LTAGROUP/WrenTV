package com.wrentv.tv.ui.screens

import androidx.compose.animation.core.animateDpAsState
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.LiveTv
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Search
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.Icon
import androidx.tv.material3.LocalContentColor
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import com.wrentv.tv.data.LiveChannel
import com.wrentv.tv.data.LiveProgram
import com.wrentv.tv.data.WrenTVApi
import com.wrentv.tv.ui.Bar
import com.wrentv.tv.ui.Chip
import com.wrentv.tv.ui.EmptyState
import com.wrentv.tv.ui.Loading
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.LocalTopBar
import com.wrentv.tv.ui.PageGutter
import com.wrentv.tv.ui.PillButton
import com.wrentv.tv.ui.RemoteImage
import com.wrentv.tv.ui.ReportScroll
import com.wrentv.tv.ui.Route
import com.wrentv.tv.ui.TopBarSpace
import com.wrentv.tv.ui.TvTextField
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.rememberFocus
import com.wrentv.tv.ui.rememberLoaded
import kotlinx.coroutines.delay
import java.text.DateFormat
import java.util.Date

/** How much of the schedule the guide shows at once, and how far one page moves it. */
private const val SPAN = 2 * 3600L
private const val STEP = 1800L
/** How far ahead the guide is loaded. */
private const val GUIDE_HOURS = 6

private val ChannelColumn = 230.dp
private val RowHeight = 78.dp

/**
 * Live TV as a guide, like YouTube TV: what's focused up top, search and the
 * provider's groups as filters, then channels down the side and their
 * programmes across a two-hour window. Left and right move along the
 * schedule, paging it half an hour at a time at the edges.
 */
@Composable
fun LiveTvScreen() {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val bar = LocalTopBar.current
    val list = rememberLazyListState()
    ReportScroll(list)
    // What's on changes by the minute: reload the listing in place, the full guide less often.
    var tick by remember { mutableLongStateOf(0L) }
    LaunchedEffect(Unit) { while (true) { delay(60_000); tick++ } }
    val loaded = rememberLoaded("livetv", refresh = tick) { state.loadLiveChannels() }
    val guide = rememberLoaded("livetv-guide", refresh = tick / 10) {
        state.api?.liveGuide(GUIDE_HOURS)?.associate { it.id to it.programs }
    }.value.orEmpty()
    val channels = loaded.value
    val now = remember(tick) { System.currentTimeMillis() / 1000 }
    val base = now - now % STEP
    var paged by rememberSaveable { mutableLongStateOf(base) }
    // Once time passes the window's start, the window follows.
    val windowStart = maxOf(paged, base)
    val lastStart = base + GUIDE_HOURS * 3600L - SPAN

    var query by rememberSaveable { mutableStateOf("") }
    var group by rememberSaveable { mutableStateOf<String?>(null) }
    var focused by remember { mutableStateOf<Pair<LiveChannel, LiveProgram?>?>(null) }
    val last = state.lastChannel?.let { id -> channels?.firstOrNull { it.id == id } }
    val groups = remember(channels) { channels?.map { it.groupName }?.distinct().orEmpty() }
    val programs = { ch: LiveChannel -> guide[ch.id]?.takeIf { it.isNotEmpty() } ?: listOfNotNull(ch.now, ch.next) }
    val shown = remember(channels, guide, query, group, now) {
        val q = query.trim().lowercase()
        channels.orEmpty()
            .filter { group == null || it.groupName == group }
            .filter { ch ->
                q.isEmpty() || ch.name.lowercase().contains(q) || ch.number == q || ch.groupName.lowercase().contains(q) ||
                    programs(ch).any { it.title.lowercase().contains(q) }
            }
            .sortedBy { !it.onAir(now) }
    }

    val top by animateDpAsState(if (bar.atTop || bar.focused) TopBarSpace else 28.dp, label = "guide-top")
    Column(Modifier.fillMaxSize().padding(top = top)) {
        Row(
            Modifier.fillMaxWidth().padding(horizontal = PageGutter),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Text("Live TV", style = MaterialTheme.typography.headlineLarge)
            Spacer(Modifier.weight(1f))
            Icon(Icons.Rounded.Search, null, Modifier.size(26.dp), tint = Wren.secondary)
            TvTextField(query, { query = it }, "", Modifier.rememberFocus("search"), placeholder = "Search channels and shows", width = 380.dp)
            if (last != null) {
                PillButton(
                    "Watch ${last.name}", { nav.push(Route.LivePlayer(last.id)) },
                    Modifier.rememberFocus("resume", default = true), icon = Icons.Rounded.PlayArrow, primary = true,
                )
            }
        }

        Details(focused ?: shown.firstOrNull()?.let { it to it.current(programs(it), now) }, state.api, now)

        if (groups.size > 1) {
            LazyRow(
                contentPadding = PaddingValues(horizontal = PageGutter),
                horizontalArrangement = Arrangement.spacedBy(10.dp),
                modifier = Modifier.padding(bottom = 12.dp),
            ) {
                item("all") { Chip("All", group == null, { group = null }, Modifier.rememberFocus("chip:all")) }
                items(groups, key = { "chip:$it" }) { g -> Chip(g, group == g, { group = g }, Modifier.rememberFocus("chip:$g")) }
            }
        }

        when {
            channels == null && loaded.error != null -> EmptyState("Couldn't load channels", loaded.error, icon = Icons.Rounded.LiveTv)
            channels == null -> Loading()
            channels.isEmpty() -> EmptyState("No channels yet", "Your server's admin can add an IPTV playlist in the web dashboard.", icon = Icons.Rounded.LiveTv)
            shown.isEmpty() -> EmptyState("Nothing matches “${query.trim()}”", "Try a channel name, number or show.", icon = Icons.Rounded.Search)
            else -> BoxWithConstraints(Modifier.fillMaxSize().padding(horizontal = PageGutter)) {
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
                                first = ch === shown.first() && last == null,
                                onFocus = { p -> focused = ch to p },
                                onPage = { delta ->
                                    val next = (windowStart + delta).coerceIn(base, maxOf(base, lastStart))
                                    if (next != windowStart) { paged = next; true } else false
                                },
                                onClick = { nav.push(Route.LivePlayer(ch.id, group)) },
                            )
                        }
                    }
                }
            }
        }
    }
}

/** The focused programme: the guide's preview, like YouTube TV's top panel. */
@Composable
private fun Details(sel: Pair<LiveChannel, LiveProgram?>?, api: WrenTVApi?, now: Long) {
    Row(
        Modifier.fillMaxWidth().height(156.dp).padding(horizontal = PageGutter, vertical = 14.dp),
        horizontalArrangement = Arrangement.spacedBy(22.dp),
    ) {
        if (sel == null) return@Row
        val (ch, p) = sel
        ChannelLogo(ch, api, Modifier.width(170.dp).fillMaxHeight().background(Wren.surface, RoundedCornerShape(12.dp)))
        Column(Modifier.weight(1f)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                if (p != null && p.airing(now)) LiveBadge()
                Text(
                    listOfNotNull(ch.number?.takeIf { it.isNotEmpty() }, ch.name).joinToString("  "),
                    style = MaterialTheme.typography.labelLarge, color = Wren.secondary, maxLines = 1, overflow = TextOverflow.Ellipsis,
                )
            }
            Text(
                p?.title ?: ch.name, style = MaterialTheme.typography.headlineMedium, maxLines = 1, overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(top = 4.dp),
            )
            if (p != null) {
                val left = (p.end - now) / 60
                val timing = when {
                    p.airing(now) -> "${timeRange(p)} · ${if (left >= 60) "${left / 60} hr ${left % 60} min" else "$left min"} left"
                    p.start > now -> "Starts ${timeOf(p.start)} · ${(p.end - p.start) / 60} min"
                    else -> timeRange(p)
                }
                Text(
                    listOfNotNull(timing, p.episode?.takeIf { it.isNotEmpty() }).joinToString(" · "),
                    style = MaterialTheme.typography.bodyMedium, color = Wren.secondary, maxLines = 1, modifier = Modifier.padding(top = 2.dp),
                )
                p.description?.takeIf { it.isNotEmpty() }?.let {
                    Text(it, style = MaterialTheme.typography.bodyMedium, color = Wren.tertiary, maxLines = 2, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 4.dp))
                }
            } else {
                Text("No guide information", style = MaterialTheme.typography.bodyMedium, color = Wren.secondary, modifier = Modifier.padding(top = 2.dp))
            }
        }
    }
}

@Composable
private fun LiveBadge() {
    Text(
        "LIVE", style = MaterialTheme.typography.labelSmall, color = Color.White,
        modifier = Modifier.background(Wren.ember, RoundedCornerShape(4.dp)).padding(horizontal = 6.dp, vertical = 2.dp),
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
    onFocus: (LiveProgram?) -> Unit,
    /** Moves the window by the given seconds; false when it can't go further. */
    onPage: (Long) -> Boolean,
    onClick: () -> Unit,
) {
    val end = start + SPAN
    val visible = programs.filter { it.end > start && it.start < end }
    Row(Modifier.fillMaxWidth().height(RowHeight), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        GuideCell(
            onClick, Modifier.width(ChannelColumn).onFocusChanged { if (it.isFocused) onFocus(ch.current(programs, now)) }
                .rememberFocus("ch:${ch.id}"),
        ) {
            Row(Modifier.fillMaxSize().padding(horizontal = 12.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                ChannelLogo(ch, api, Modifier.width(72.dp).height(44.dp))
                Column(Modifier.weight(1f)) {
                    Text(ch.name, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    ch.number?.takeIf { it.isNotEmpty() }?.let {
                        Text(it, style = MaterialTheme.typography.bodySmall, maxLines = 1, color = LocalContentColor.current.copy(alpha = 0.6f))
                    }
                }
            }
        }
        Box(Modifier.width(width).fillMaxHeight()) {
            if (visible.isEmpty()) {
                GuideCell(
                    onClick, Modifier.fillMaxSize().onFocusChanged { if (it.isFocused) onFocus(null) }
                        .rememberFocus("p:${ch.id}:none", default = first),
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
                ) {
                    Column(Modifier.fillMaxSize().padding(horizontal = 14.dp), verticalArrangement = Arrangement.Center) {
                        Text(p.title, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(
                            if (airing) "${timeOf(p.start)} – ${timeOf(p.end)}" else timeOf(p.start),
                            style = MaterialTheme.typography.bodySmall, maxLines = 1, color = LocalContentColor.current.copy(alpha = 0.6f),
                        )
                    }
                    if (airing) Bar(p.progress(now), Modifier.align(Alignment.BottomStart).fillMaxWidth().height(3.dp))
                }
            }
            if (now in start until end) {
                Box(Modifier.offset(x = width * ((now - start).toFloat() / SPAN) - 1.dp).width(2.dp).fillMaxHeight().background(Wren.ember.copy(alpha = 0.7f)))
            }
        }
    }
}

@Composable
private fun GuideCell(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    airing: Boolean = false,
    content: @Composable androidx.compose.foundation.layout.BoxScope.() -> Unit,
) {
    Surface(
        onClick = onClick,
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

/** A channel's logo on a dark tile, or its name when there's no logo. */
@Composable
fun ChannelLogo(ch: LiveChannel, api: WrenTVApi?, modifier: Modifier = Modifier) {
    val url = ch.logo?.takeIf { it.isNotEmpty() }?.let { if (it.startsWith("http")) it else api?.absolute(it) }
    Box(modifier.clip(RoundedCornerShape(8.dp)), contentAlignment = Alignment.Center) {
        if (url != null) {
            RemoteImage(url, Modifier.fillMaxSize().padding(6.dp), contentScale = ContentScale.Fit)
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

fun timeRange(p: LiveProgram): String = "${clock.format(Date(p.start * 1000))} – ${clock.format(Date(p.end * 1000))}"

fun timeOf(seconds: Long): String = clock.format(Date(seconds * 1000))
