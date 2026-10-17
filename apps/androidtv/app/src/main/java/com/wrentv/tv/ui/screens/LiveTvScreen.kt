package com.wrentv.tv.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.LiveTv
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.LocalContentColor
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import com.wrentv.tv.data.LiveChannel
import com.wrentv.tv.data.LiveProgram
import com.wrentv.tv.data.WrenTVApi
import com.wrentv.tv.ui.Bar
import com.wrentv.tv.ui.EmptyState
import com.wrentv.tv.ui.Loading
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.PageGutter
import com.wrentv.tv.ui.PageHeader
import com.wrentv.tv.ui.PillButton
import com.wrentv.tv.ui.RemoteImage
import com.wrentv.tv.ui.ReportScroll
import com.wrentv.tv.ui.Route
import com.wrentv.tv.ui.TopBarSpace
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.rememberFocus
import com.wrentv.tv.ui.rememberLoaded
import kotlinx.coroutines.delay
import java.text.DateFormat
import java.util.Date

/** The server's live channels: the provider's groups, or the channels themselves when there's only one. */
@Composable
fun LiveTvScreen() {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val list = rememberLazyListState()
    ReportScroll(list)
    // What's on changes by the minute: reload the guide in place.
    var tick by remember { mutableLongStateOf(0L) }
    LaunchedEffect(Unit) { while (true) { delay(60_000); tick++ } }
    val loaded = rememberLoaded("livetv", refresh = tick) { state.loadLiveChannels() }
    val channels = loaded.value
    val now = System.currentTimeMillis() / 1000
    val last = state.lastChannel?.let { id -> channels?.firstOrNull { it.id == id } }
    val groups = channels?.groupBy { it.groupName }.orEmpty()

    LazyColumn(
        Modifier.fillMaxSize(), state = list,
        contentPadding = PaddingValues(top = TopBarSpace, bottom = 48.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        item("header") {
            val subtitle = channels?.takeIf { it.isNotEmpty() }?.let {
                if (groups.size > 1) "${groups.size} groups · ${it.size} channels" else "${it.size} channels"
            }
            PageHeader("Live TV", subtitle) {
                if (last != null) {
                    PillButton(
                        "Watch ${last.name}", { nav.push(Route.LivePlayer(last.id)) },
                        Modifier.rememberFocus("resume", default = true), icon = Icons.Rounded.PlayArrow, primary = true,
                    )
                }
            }
        }
        when {
            channels == null && loaded.error != null -> item("error") { EmptyState("Couldn't load channels", loaded.error, icon = Icons.Rounded.LiveTv) }
            channels == null -> item("loading") { Loading() }
            channels.isEmpty() -> item("empty") {
                EmptyState("No channels yet", "Your server's admin can add an IPTV playlist in the web dashboard.", icon = Icons.Rounded.LiveTv)
            }
            groups.size == 1 -> {
                val sorted = channels.inGroup(groups.keys.first(), now)
                items(sorted, key = { "ch:${it.id}" }) { ch ->
                    ChannelRow(
                        ch, state.api, now,
                        onClick = { nav.push(Route.LivePlayer(ch.id)) },
                        modifier = Modifier.padding(horizontal = PageGutter)
                            .rememberFocus("ch:${ch.id}", default = last == null && ch === sorted.first()),
                    )
                }
            }
            else -> {
                val names = groups.keys.toList()
                items(names, key = { "group:$it" }) { name ->
                    val inGroup = groups.getValue(name)
                    GroupRow(
                        name, inGroup.size, inGroup.count { it.onAir(now) },
                        onClick = { nav.push(Route.LiveGroup(name)) },
                        modifier = Modifier.padding(horizontal = PageGutter)
                            .rememberFocus("group:$name", default = last == null && name == names.first()),
                    )
                }
            }
        }
    }
}

/** One provider group's channels, the ones with something on right now first. */
@Composable
fun LiveGroupScreen(group: String) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val list = rememberLazyListState()
    ReportScroll(list)
    var tick by remember { mutableLongStateOf(0L) }
    LaunchedEffect(Unit) { while (true) { delay(60_000); tick++ } }
    val loaded = rememberLoaded("livetv", refresh = tick) { state.loadLiveChannels() }
    val now = System.currentTimeMillis() / 1000
    val channels = loaded.value?.inGroup(group, now)

    LazyColumn(
        Modifier.fillMaxSize(), state = list,
        contentPadding = PaddingValues(top = TopBarSpace, bottom = 48.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        item("header") {
            PageHeader(group, channels?.takeIf { it.isNotEmpty() }?.let { onAirLabel(it.size, it.count { c -> c.onAir(now) }) })
        }
        when {
            channels == null && loaded.error != null -> item("error") { EmptyState("Couldn't load channels", loaded.error, icon = Icons.Rounded.LiveTv) }
            channels == null -> item("loading") { Loading() }
            channels.isEmpty() -> item("empty") { EmptyState("No channels in $group", icon = Icons.Rounded.LiveTv) }
            else -> items(channels, key = { "ch:${it.id}" }) { ch ->
                ChannelRow(
                    ch, state.api, now,
                    onClick = { nav.push(Route.LivePlayer(ch.id, group)) },
                    modifier = Modifier.padding(horizontal = PageGutter)
                        .rememberFocus("ch:${ch.id}", default = ch === channels.first()),
                )
            }
        }
    }
}

private val LiveChannel.groupName: String get() = group?.takeIf { it.isNotBlank() } ?: "Channels"

/** The guide has a programme airing on it now. */
fun LiveChannel.onAir(at: Long): Boolean = now?.let { at >= it.start && at < it.end } == true

/** [group]'s channels, those on air first, otherwise in the provider's order. */
fun List<LiveChannel>.inGroup(group: String, now: Long): List<LiveChannel> =
    filter { it.groupName == group }.sortedBy { !it.onAir(now) }

private fun onAirLabel(total: Int, live: Int): String =
    (if (total == 1) "1 channel" else "$total channels") + if (live > 0) " · $live on now" else ""

@Composable
private fun GroupRow(name: String, total: Int, live: Int, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Surface(
        onClick = onClick,
        modifier = modifier.fillMaxWidth(),
        shape = ClickableSurfaceDefaults.shape(RoundedCornerShape(14.dp)),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = Wren.surface, contentColor = Wren.text,
            focusedContainerColor = Color.White, focusedContentColor = Color.Black,
        ),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1.02f),
    ) {
        Row(
            Modifier.fillMaxWidth().height(72.dp).padding(horizontal = 20.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(18.dp),
        ) {
            Text(name, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
            Text(onAirLabel(total, live), style = MaterialTheme.typography.bodyMedium, maxLines = 1, color = LocalContentColor.current.copy(alpha = 0.6f))
        }
    }
}

@Composable
private fun ChannelRow(ch: LiveChannel, api: WrenTVApi?, now: Long, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Surface(
        onClick = onClick,
        modifier = modifier.fillMaxWidth(),
        shape = ClickableSurfaceDefaults.shape(RoundedCornerShape(14.dp)),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = Wren.surface, contentColor = Wren.text,
            focusedContainerColor = Color.White, focusedContentColor = Color.Black,
        ),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1.02f),
    ) {
        Row(
            Modifier.fillMaxWidth().height(84.dp).padding(horizontal = 16.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(18.dp),
        ) {
            Text(
                ch.number.orEmpty(), style = MaterialTheme.typography.titleMedium, maxLines = 1,
                color = LocalContentColor.current.copy(alpha = 0.6f), modifier = Modifier.width(44.dp),
            )
            ChannelLogo(ch, api, Modifier.width(96.dp).height(54.dp))
            Text(ch.name, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.width(260.dp))
            Column(Modifier.weight(1f)) {
                val on = ch.now
                if (on == null) {
                    Text("No guide information", style = MaterialTheme.typography.bodyMedium, color = LocalContentColor.current.copy(alpha = 0.5f))
                } else {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(on.title, style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
                        Text("  " + timeRange(on), style = MaterialTheme.typography.bodySmall, maxLines = 1, color = LocalContentColor.current.copy(alpha = 0.6f))
                    }
                    Bar(on.progress(now), Modifier.padding(top = 6.dp).width(220.dp).height(4.dp))
                    ch.next?.let {
                        Text(
                            "Next: ${it.title}", style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis,
                            color = LocalContentColor.current.copy(alpha = 0.6f), modifier = Modifier.padding(top = 4.dp),
                        )
                    }
                }
            }
        }
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

private val clock: DateFormat = DateFormat.getTimeInstance(DateFormat.SHORT)

fun timeRange(p: LiveProgram): String = "${clock.format(Date(p.start * 1000))} – ${clock.format(Date(p.end * 1000))}"

fun timeOf(seconds: Long): String = clock.format(Date(seconds * 1000))
