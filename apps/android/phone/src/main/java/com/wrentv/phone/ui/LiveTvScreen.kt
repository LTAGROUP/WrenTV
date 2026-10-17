package com.wrentv.phone.ui

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.rounded.Clear
import androidx.compose.material.icons.rounded.LiveTv
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Search
import androidx.compose.material.icons.rounded.Warning
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.wrentv.tv.data.LiveChannel
import com.wrentv.tv.data.LiveProgram
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import java.text.DateFormat
import java.util.Date

private fun nowSec() = System.currentTimeMillis() / 1000
private fun clock(sec: Long): String = DateFormat.getTimeInstance(DateFormat.SHORT).format(Date(sec * 1000))
private fun LiveProgram.timeRange() = "${clock(start)} – ${clock(end)}"

/**
 * Live TV: the provider's groups as chips, a search over channels and shows,
 * and what's on now and next per channel. Channels airing something come first.
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
    var filter by rememberSaveable { mutableStateOf("") }
    var group by rememberSaveable { mutableStateOf<String?>(null) }
    var now by remember { mutableLongStateOf(nowSec()) }
    var schedule by remember { mutableStateOf<LiveChannel?>(null) }

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
    val q = filter.trim()
    val searching = q.isNotEmpty()
    val list = channels.filter { c ->
        if (!searching) group == null || c.group == group
        else c.name.contains(q, true) || c.number == q || (c.group ?: "").contains(q, true) || programs(c).any { it.title.contains(q, true) }
    }
    val shown = list.filter { onNow(it) != null } + list.filter { onNow(it) == null }
    fun play(c: LiveChannel) { nav.live = LiveTarget(c.id, if (searching) null else group) }

    Page("Live TV", if (loaded && channels.isNotEmpty()) "${shown.size} channel${if (shown.size == 1) "" else "s"}" else null) {
        when {
            !loaded -> Loading()
            error != null && channels.isEmpty() -> EmptyState(Icons.Rounded.Warning, "Couldn't load", error)
            channels.isEmpty() -> EmptyState(Icons.Rounded.LiveTv, "No channels",
                if (state.isAdmin) "Add an IPTV playlist on the server's web dashboard under Settings → Live TV." else "The server owner hasn't added any channels yet.")
            else -> LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 16.dp)) {
                item("search") {
                    TextField(
                        filter, { filter = it }, Modifier.fillMaxWidth().padding(horizontal = m.gutter, vertical = 6.dp),
                        placeholder = { Text("Search all channels and shows") }, singleLine = true,
                        leadingIcon = { Icon(Icons.Rounded.Search, null) },
                        trailingIcon = { if (filter.isNotEmpty()) IconButton({ filter = "" }) { Icon(Icons.Rounded.Clear, "Clear") } },
                        shape = CircleShape,
                        colors = TextFieldDefaults.colors(
                            focusedContainerColor = Color.White.copy(alpha = 0.1f), unfocusedContainerColor = Color.White.copy(alpha = 0.1f),
                            focusedIndicatorColor = Color.Transparent, unfocusedIndicatorColor = Color.Transparent,
                        ),
                    )
                }
                if (groups.size > 1 && !searching) item("groups") {
                    Row(Modifier.horizontalScroll(rememberScrollState()).padding(horizontal = m.gutter, vertical = 6.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Chip("All", group == null) { group = null }
                        groups.forEach { g -> Chip(g, group == g) { group = g } }
                    }
                }
                if (shown.isEmpty()) item("none") { EmptyState(Icons.Rounded.Search, "No results", "Nothing matches “$q”.", Modifier.fillMaxWidth().padding(top = 60.dp)) }
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
                            }
                            if (current != null) {
                                Text(current.title, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyMedium)
                                Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                                    LinearProgressIndicator({ current.progress(now) }, Modifier.widthIn(max = 120.dp).weight(1f, fill = false),
                                        color = Color.White, trackColor = Color.White.copy(alpha = 0.15f), drawStopIndicator = {})
                                    Text(current.timeRange(), color = Wren.secondary, fontSize = 11.sp, maxLines = 1)
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
            }
        }
    }

    schedule?.let { ch ->
        ModalBottomSheet({ schedule = null }, containerColor = Wren.surface) {
            Column(Modifier.fillMaxWidth()) {
                Row(Modifier.fillMaxWidth().padding(horizontal = 20.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    ChannelLogo(ch.logo, ch.name, Modifier.size(48.dp, 30.dp))
                    Text(ch.name, Modifier.weight(1f), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    TextButton({ schedule = null; play(ch) }) { Icon(Icons.Rounded.PlayArrow, null); Text("Watch") }
                }
                val upcoming = programs(ch).filter { it.end > now }
                LazyColumn(contentPadding = PaddingValues(20.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
                    if (upcoming.isEmpty()) item { Text("This channel has no guide.", color = Wren.secondary) }
                    items(upcoming, { it.start }) { p ->
                        val airing = p.start <= now
                        Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                Text(clock(p.start), color = Wren.secondary, fontSize = 12.sp)
                                if (airing) Text("ON NOW", color = Wren.ember, fontSize = 11.sp, fontWeight = FontWeight.Bold)
                            }
                            Text(p.title, fontWeight = FontWeight.SemiBold)
                            p.episode?.let { Text(it, color = Wren.secondary, fontSize = 12.sp) }
                            p.description?.takeIf { it.isNotEmpty() }?.let { Text(it, color = Wren.secondary, style = MaterialTheme.typography.bodySmall, maxLines = 3, overflow = TextOverflow.Ellipsis) }
                            if (airing) LinearProgressIndicator({ p.progress(now) }, Modifier.fillMaxWidth().clip(CircleShape), color = Color.White,
                                trackColor = Color.White.copy(alpha = 0.15f), drawStopIndicator = {})
                        }
                    }
                }
            }
        }
    }
}
