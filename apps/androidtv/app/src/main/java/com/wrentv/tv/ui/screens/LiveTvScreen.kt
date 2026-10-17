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
import com.wrentv.tv.ui.SectionTitle
import com.wrentv.tv.ui.TopBarSpace
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.rememberFocus
import com.wrentv.tv.ui.rememberLoaded
import kotlinx.coroutines.delay
import java.text.DateFormat
import java.util.Date

/** The server's live channels, in the provider's groups, with what's on now. */
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

    LazyColumn(
        Modifier.fillMaxSize(), state = list,
        contentPadding = PaddingValues(top = TopBarSpace, bottom = 48.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        item("header") {
            PageHeader("Live TV", channels?.takeIf { it.isNotEmpty() }?.let { "${it.size} channels" }) {
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
            else -> {
                val groups = channels.groupBy { it.group?.takeIf { g -> g.isNotBlank() } ?: "Channels" }
                groups.entries.forEachIndexed { gi, (group, inGroup) ->
                    if (groups.size > 1) item("group:$group") { SectionTitle(group, Modifier.padding(start = PageGutter, top = if (gi == 0) 8.dp else 24.dp, bottom = 6.dp)) }
                    items(inGroup, key = { "ch:${it.id}" }) { ch ->
                        ChannelRow(
                            ch, state.api, now,
                            onClick = { nav.push(Route.LivePlayer(ch.id)) },
                            modifier = Modifier.padding(horizontal = PageGutter)
                                .rememberFocus("ch:${ch.id}", default = last == null && ch === channels.first()),
                        )
                    }
                }
            }
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
