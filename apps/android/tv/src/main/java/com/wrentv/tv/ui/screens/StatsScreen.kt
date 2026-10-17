package com.wrentv.tv.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Insights
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.wrentv.tv.data.DayStat
import com.wrentv.tv.data.NowPlaying
import com.wrentv.tv.data.StatCount
import com.wrentv.tv.data.StatsResponse
import com.wrentv.tv.data.TopTitle
import com.wrentv.tv.ui.ArtCard
import com.wrentv.tv.ui.Chip
import com.wrentv.tv.ui.EmptyState
import com.wrentv.tv.ui.Loading
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.PageGutter
import com.wrentv.tv.ui.ProgressStrip
import com.wrentv.tv.ui.RemoteImage
import com.wrentv.tv.ui.Route
import com.wrentv.tv.ui.SectionTitle
import com.wrentv.tv.ui.Shelf
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.formatBytes
import com.wrentv.tv.ui.formatClock
import com.wrentv.tv.ui.rememberFocus
import kotlinx.coroutines.delay
import java.text.DateFormat
import java.util.Date

/** Who's watching now, plays over time, top titles and the library's makeup. */
@Composable
fun StatsScreen() {
    val state = LocalAppState.current
    var days by rememberSaveable { mutableIntStateOf(30) }
    var stats by remember { mutableStateOf<StatsResponse?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(days) {
        // Now playing changes by the minute; keep it fresh while the page is open.
        while (true) {
            try {
                stats = state.api?.stats(days)
                error = null
            } catch (e: Exception) {
                if (stats == null) error = e.message
            }
            delay(20_000)
        }
    }
    val s = stats
    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(top = 40.dp, bottom = 48.dp), verticalArrangement = Arrangement.spacedBy(24.dp)) {
        item("header") {
            Row(Modifier.fillMaxWidth().padding(horizontal = PageGutter), verticalAlignment = Alignment.CenterVertically) {
                Text("Activity", style = MaterialTheme.typography.displaySmall, color = Wren.text, modifier = Modifier.weight(1f))
                Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    listOf(7, 30, 90, 365).forEach { d ->
                        Chip(if (d == 365) "Year" else "$d days", days == d, { days = d }, Modifier.rememberFocus("stats:$d", default = d == 30))
                    }
                }
            }
        }
        when {
            s == null && error != null -> item("error") { EmptyState("Couldn't load", error, icon = Icons.Rounded.Insights) }
            s == null -> item("loading") { Loading() }
            else -> {
                s.nowPlaying?.takeIf { it.isNotEmpty() }?.let { now ->
                    item("now") {
                        Shelf("Now Playing") { items(now, key = { it.sessionId }) { NowPlayingCard(it) } }
                    }
                }
                item("totals") {
                    Row(Modifier.padding(horizontal = PageGutter), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                        Total("Plays", s.stats.plays.toString())
                        Total("Hours watched", "%.0f".format(s.stats.hours))
                        Total("People", s.stats.users.toString())
                        Total("Titles", s.stats.titles.toString())
                    }
                }
                if (s.stats.daily.isNotEmpty()) item("daily") { DailyChart(s.stats.daily) }
                s.stats.topMovies?.takeIf { it.isNotEmpty() }?.let { top -> item("movies") { TopShelf("Top Movies", top) } }
                s.stats.topShows?.takeIf { it.isNotEmpty() }?.let { top -> item("shows") { TopShelf("Top Shows", top) } }
                item("breakdown") {
                    Row(Modifier.padding(horizontal = PageGutter), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                        s.stats.topUsers?.takeIf { it.isNotEmpty() }?.let { users ->
                            Card("Top People", Modifier.weight(1f)) {
                                users.take(6).forEach { u -> StatLine(u.name, "${u.plays} plays · ${"%.1f".format(u.hours)} h") }
                            }
                        }
                        Card("How It Played", Modifier.weight(1f)) {
                            s.stats.byMethod.orEmpty().forEach { StatLine(methodName(it.name), it.count.toString()) }
                            s.stats.byClient.orEmpty().take(4).forEach { StatLine(it.name, it.count.toString()) }
                        }
                    }
                }
                item("library") {
                    val l = s.library
                    Card("Library", Modifier.padding(horizontal = PageGutter).fillMaxWidth()) {
                        Text("${l.movies} movies · ${l.shows} shows · ${l.episodes} episodes · ${l.files} files · ${formatBytes(l.bytes)} · ${"%.0f".format(l.hours)} hours",
                            style = MaterialTheme.typography.bodyLarge, color = Wren.text)
                        Spacer(Modifier.height(10.dp))
                        Row(horizontalArrangement = Arrangement.spacedBy(28.dp)) {
                            Breakdown("Resolution", l.resolutions)
                            Breakdown("HDR", l.hdr)
                            Breakdown("Video", l.videoCodecs)
                            Breakdown("Container", l.containers)
                        }
                    }
                }
                s.recent?.takeIf { it.isNotEmpty() }?.let { recent ->
                    item("recent-title") { SectionTitle("Recent Plays", Modifier.padding(horizontal = PageGutter)) }
                    itemsIndexed(recent.take(25), key = { _, r -> r.id }) { _, r ->
                        val fmt = remember { DateFormat.getDateTimeInstance(DateFormat.MEDIUM, DateFormat.SHORT) }
                        Row(Modifier.padding(horizontal = PageGutter + 8.dp), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                            Text(fmt.format(Date(r.startedAt * 1000)), style = MaterialTheme.typography.bodySmall, color = Wren.tertiary, modifier = Modifier.width(170.dp))
                            Text(listOfNotNull(r.showTitle, r.title).joinToString(" — "), style = MaterialTheme.typography.bodyMedium, color = Wren.text,
                                maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                            Text("${r.userName} · ${methodName(r.method)}${if (r.completed) " · finished" else ""}",
                                style = MaterialTheme.typography.bodySmall, color = Wren.secondary)
                        }
                    }
                }
            }
        }
    }
}

private fun methodName(m: String) = when (m) {
    "direct" -> "Direct play"
    "remux" -> "Direct stream"
    "transcode" -> "Transcode"
    else -> m.replaceFirstChar { it.uppercase() }
}

@Composable
private fun NowPlayingCard(n: NowPlaying) {
    val api = LocalAppState.current.api
    val nav = LocalNavigator.current
    Column(Modifier.width(320.dp)) {
        ArtCard({ if (n.itemId.isNotEmpty()) nav.push(Route.Detail(n.itemId)) }, Modifier.width(320.dp).aspectRatio(16f / 9f).rememberFocus("now:${n.sessionId}")) {
            RemoteImage(api?.imageUrl(n.backdropPath ?: n.posterPath, "w780"), Modifier.fillMaxSize())
            if (n.duration > 0) ProgressStrip(n.position / n.duration, Modifier.align(Alignment.BottomCenter))
        }
        Text(n.title, style = MaterialTheme.typography.titleSmall, color = Wren.text, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 10.dp))
        Text(listOfNotNull(n.userName, n.client.ifEmpty { null }, methodName(n.method), n.quality.ifEmpty { null },
            if (n.duration > 0) "${formatClock(n.position)} / ${formatClock(n.duration)}" else null).joinToString(" · "),
            style = MaterialTheme.typography.bodySmall, color = Wren.secondary, maxLines = 2)
    }
}

@Composable
private fun Total(label: String, value: String) {
    Column(Modifier.width(200.dp).background(Wren.surface, RoundedCornerShape(16.dp)).padding(20.dp)) {
        Text(value, style = MaterialTheme.typography.displaySmall, color = Wren.text)
        Text(label, style = MaterialTheme.typography.bodyMedium, color = Wren.secondary)
    }
}

@Composable
private fun Card(title: String, modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    Column(modifier.background(Wren.surface, RoundedCornerShape(16.dp)).padding(20.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Text(title.uppercase(), style = MaterialTheme.typography.labelSmall, color = Wren.secondary)
        Spacer(Modifier.height(4.dp))
        content()
    }
}

@Composable
private fun StatLine(name: String, value: String) {
    Row(Modifier.fillMaxWidth()) {
        Text(name, style = MaterialTheme.typography.bodyMedium, color = Wren.text, modifier = Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
        Text(value, style = MaterialTheme.typography.bodyMedium, color = Wren.secondary)
    }
}

@Composable
private fun Breakdown(title: String, counts: List<StatCount>?) {
    if (counts.isNullOrEmpty()) return
    Column {
        Text(title, style = MaterialTheme.typography.labelMedium, color = Wren.tertiary)
        counts.take(5).forEach { Text("${it.name.ifEmpty { "Other" }}  ${it.count}", style = MaterialTheme.typography.bodySmall, color = Wren.secondary) }
    }
}

/** Plays per day as bars. */
@Composable
private fun DailyChart(daily: List<DayStat>) {
    val max = daily.maxOf { it.plays }.coerceAtLeast(1)
    Card("Plays per day", Modifier.padding(horizontal = PageGutter).fillMaxWidth()) {
        Row(Modifier.fillMaxWidth().height(120.dp), horizontalArrangement = Arrangement.spacedBy(2.dp), verticalAlignment = Alignment.Bottom) {
            daily.forEach { d ->
                Box(Modifier.weight(1f).fillMaxHeight(), contentAlignment = Alignment.BottomCenter) {
                    Box(
                        Modifier.fillMaxWidth().fillMaxHeight((d.plays.toFloat() / max).coerceAtLeast(0.02f))
                            .background(if (d.plays > 0) Wren.ember else Wren.line, RoundedCornerShape(topStart = 3.dp, topEnd = 3.dp)),
                    )
                }
            }
        }
        Row(Modifier.fillMaxWidth()) {
            Text(daily.first().date, style = MaterialTheme.typography.bodySmall, color = Wren.tertiary, modifier = Modifier.weight(1f))
            Text(daily.last().date, style = MaterialTheme.typography.bodySmall, color = Wren.tertiary)
        }
    }
}

@Composable
private fun TopShelf(title: String, top: List<TopTitle>) {
    val api = LocalAppState.current.api
    val nav = LocalNavigator.current
    Shelf(title) {
        items(top.take(15), key = { it.itemId }) { t ->
            Column(Modifier.width(140.dp)) {
                ArtCard({ nav.push(Route.Detail(t.itemId)) }, Modifier.width(140.dp).aspectRatio(2f / 3f).rememberFocus("top:$title:${t.itemId}")) {
                    RemoteImage(api?.imageUrl(t.posterPath), Modifier.fillMaxSize())
                }
                Text(t.title, style = MaterialTheme.typography.titleSmall, color = Wren.text, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 8.dp))
                Text("${t.plays} plays · ${t.users} ${if (t.users == 1) "person" else "people"}", style = MaterialTheme.typography.bodySmall, color = Wren.secondary)
            }
        }
    }
}
