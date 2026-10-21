package com.wrentv.tv.ui.screens

import androidx.compose.foundation.Canvas
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
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Insights
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableDoubleStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.wrentv.tv.data.ActivityResponse
import com.wrentv.tv.data.DayStat
import com.wrentv.tv.data.NowPlaying
import com.wrentv.tv.data.StatCount
import com.wrentv.tv.data.StatsResponse
import com.wrentv.tv.data.SystemResponse
import com.wrentv.tv.data.SystemSample
import com.wrentv.tv.data.TopTitle
import com.wrentv.tv.data.TrackInfo
import com.wrentv.tv.ui.ArtCard
import com.wrentv.tv.ui.Chip
import com.wrentv.tv.ui.Choice
import com.wrentv.tv.ui.ChoicePanel
import com.wrentv.tv.ui.EmptyState
import com.wrentv.tv.ui.Loading
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.PageGutter
import com.wrentv.tv.ui.RemoteImage
import com.wrentv.tv.ui.Route
import com.wrentv.tv.ui.SectionTitle
import com.wrentv.tv.ui.Shelf
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.formatBytes
import com.wrentv.tv.ui.formatClock
import com.wrentv.tv.ui.rememberFocus
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.text.DateFormat
import java.util.Date
import java.util.Locale

private val movieColor = Color(0xFF4F8DF5)
private val tvColor = Color(0xFFE0703A)
private val directColor = Color(0xFF4CC38A)
private val remuxColor = Color(0xFF4F8DF5)
private val transcodeColor = Color(0xFFE0703A)

private fun methodColor(m: String) = when (m) {
    "direct" -> directColor
    "remux" -> remuxColor
    else -> transcodeColor
}

/**
 * The server's dashboard: who's watching now and how each stream is being
 * delivered, the machine's load, plays over time, top titles and people,
 * and the library's makeup.
 */
@Composable
fun StatsScreen() {
    val state = LocalAppState.current
    val api = state.api
    var days by rememberSaveable { mutableIntStateOf(30) }
    var stats by remember { mutableStateOf<StatsResponse?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var activity by remember { mutableStateOf<ActivityResponse?>(null) }
    var system by remember { mutableStateOf<SystemResponse?>(null) }
    // Server time minus ours, so positions move on from the server's clock.
    var skew by remember { mutableDoubleStateOf(0.0) }
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    var stopping by remember { mutableStateOf<NowPlaying?>(null) }
    val scope = rememberCoroutineScope()

    LaunchedEffect(days) {
        while (true) {
            try {
                stats = api?.stats(days)
                error = null
            } catch (e: Exception) {
                if (stats == null) error = e.message
            }
            delay(60_000)
        }
    }
    LaunchedEffect(Unit) {
        var n = 0
        while (true) {
            runCatching { api?.activity() }.getOrNull()?.let {
                activity = it
                skew = it.serverTime - System.currentTimeMillis() / 1000.0
            }
            if (n % 20 == 0) runCatching { api?.system("1h") }.getOrNull()?.let { system = it }
            n++
            delay(3_000)
        }
    }
    LaunchedEffect(Unit) {
        while (true) {
            now = System.currentTimeMillis()
            delay(1_000)
        }
    }

    val serverNow = now / 1000.0 + skew
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
        activity?.let { a ->
            item("live") { LiveTotals(a, system) }
            if (a.sessions.isNotEmpty()) {
                item("now") {
                    Shelf("Now Playing") {
                        items(a.sessions, key = { it.sessionId }) { NowPlayingCard(it, serverNow, now) { stopping = it } }
                    }
                }
            }
            val samples = system?.samples.orEmpty().let { list ->
                // The latest sample from activity, so the line keeps up between history fetches.
                val last = a.system
                if (last != null && (list.isEmpty() || last.t > list.last().t)) list + last else list
            }
            if (samples.size > 1) item("system") { SystemCharts(samples, a, system) }
        }
        when {
            s == null && error != null -> item("error") { EmptyState("Couldn't load", error, icon = Icons.Rounded.Insights) }
            s == null -> item("loading") { Loading() }
            else -> {
                item("totals") {
                    Row(Modifier.padding(horizontal = PageGutter), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                        Total("Plays", s.stats.plays.toString(), Modifier.weight(1f))
                        Total("Hours watched", hours(s.stats.hours), Modifier.weight(1f))
                        Total("People", s.stats.users.toString(), Modifier.weight(1f))
                        Total("Titles", s.stats.titles.toString(), Modifier.weight(1f))
                    }
                }
                if (s.stats.daily.isNotEmpty() && s.stats.plays > 0) {
                    item("daily") {
                        Row(Modifier.padding(horizontal = PageGutter), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                            DailyChart("Plays per day by type", s.stats.daily, listOf("Movies" to movieColor, "TV" to tvColor), Modifier.weight(1f)) {
                                listOf(it.movies ?: it.plays, it.episodes ?: 0)
                            }
                            DailyChart("Plays per day by stream type", s.stats.daily,
                                listOf("Direct play" to directColor, "Direct stream" to remuxColor, "Transcode" to transcodeColor), Modifier.weight(1f)) {
                                listOf(it.direct ?: 0, it.remux ?: 0, it.transcode ?: 0)
                            }
                        }
                    }
                }
                if (s.stats.byHour.any { it > 0 }) {
                    item("when") {
                        Row(Modifier.padding(horizontal = PageGutter), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                            Bars("By time of day", s.stats.byHour, listOf("12a", "6a", "12p", "6p"), Modifier.weight(1f))
                            Bars("By day of week", s.stats.byWeekday, listOf("Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"), Modifier.weight(1f))
                        }
                    }
                }
                s.stats.topMovies?.takeIf { it.isNotEmpty() }?.let { top -> item("movies") { TopShelf("Top Movies", top) } }
                s.stats.topShows?.takeIf { it.isNotEmpty() }?.let { top -> item("shows") { TopShelf("Top Shows", top) } }
                item("breakdown") {
                    Row(Modifier.padding(horizontal = PageGutter), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                        s.stats.topUsers?.takeIf { it.isNotEmpty() }?.let { users ->
                            Card("Top People", Modifier.weight(1f)) {
                                users.take(6).forEach { u -> StatLine(u.name, "${u.plays} plays · ${hours(u.hours)} h") }
                            }
                        }
                        Card("How It Played", Modifier.weight(1f)) {
                            val methods = s.stats.byMethod.orEmpty()
                            val total = methods.sumOf { it.count }.coerceAtLeast(1)
                            methods.forEach { Meter(methodName(it.name), it.count, total, methodColor(it.name)) }
                        }
                        Card("Players", Modifier.weight(1f)) {
                            val clients = s.stats.byClient.orEmpty().take(6)
                            val total = clients.sumOf { it.count }.coerceAtLeast(1)
                            clients.forEach { Meter(it.name.ifEmpty { "Unknown" }, it.count, total, Wren.ember) }
                        }
                    }
                }
                item("library") {
                    val l = s.library
                    Card("Library", Modifier.padding(horizontal = PageGutter).fillMaxWidth()) {
                        Text("${l.movies} movies · ${l.shows} shows · ${l.episodes} episodes · ${l.files} files · ${formatBytes(l.bytes)} · ${hours(l.hours)} hours",
                            style = MaterialTheme.typography.bodyLarge, color = Wren.text)
                        Spacer(Modifier.height(10.dp))
                        Row(horizontalArrangement = Arrangement.spacedBy(28.dp)) {
                            Breakdown("Resolution", l.resolutions, Modifier.weight(1f))
                            Breakdown("HDR", l.hdr, Modifier.weight(1f))
                            Breakdown("Video", l.videoCodecs, Modifier.weight(1f))
                            Breakdown("Container", l.containers, Modifier.weight(1f))
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
                            Text("${r.userName} · ${r.client.ifEmpty { "Unknown player" }} · ${methodName(r.method)}${if (r.completed) " · finished" else ""}",
                                style = MaterialTheme.typography.bodySmall, color = Wren.secondary)
                        }
                    }
                }
            }
        }
    }

    stopping?.let { n ->
        ChoicePanel(
            "Stop ${n.userName.ifEmpty { "this" }}'s stream?",
            listOf(
                Choice("", "Stop", "Their player says the server owner stopped it."),
                Choice("The server is going down for maintenance.", "Stop: maintenance"),
                Choice("Too many people are watching right now.", "Stop: too many streams"),
                Choice("Please play this in a player that can direct play it.", "Stop: transcoding"),
            ),
            selected = null,
            onDismiss = { stopping = null },
        ) { message ->
            scope.launch {
                runCatching { api?.killSession(n.sessionId, message) }
                activity = activity?.let { a -> a.copy(sessions = a.sessions.filter { it.sessionId != n.sessionId }) }
                runCatching { api?.activity() }.getOrNull()?.let { activity = it }
            }
        }
    }
}

// Live

@Composable
private fun LiveTotals(a: ActivityResponse, system: SystemResponse?) {
    val t = a.totals
    val info = a.systemInfo ?: system?.info
    val sample = a.system
    Row(Modifier.padding(horizontal = PageGutter), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
        Total("Streams", t.streams.toString(), Modifier.weight(1f),
            if (t.streams == 0) "Nobody is watching" else listOfNotNull(
                t.direct.takeIf { it > 0 }?.let { "$it direct" },
                t.remux.takeIf { it > 0 }?.let { "$it direct stream" },
                t.transcode.takeIf { it > 0 }?.let { "$it transcode" },
            ).joinToString(" · "))
        Total("Bandwidth", rate(t.kbps), Modifier.weight(1f),
            if (t.kbps == 0) "Nothing streaming" else "LAN ${rate(t.lanKbps)} · WAN ${rate(t.wanKbps)}")
        Total("CPU", sample?.let { "${it.cpu.toInt()}%" } ?: "—", Modifier.weight(1f),
            sample?.takeIf { info?.procCpuAvailable != false }?.let { "${info?.procLabel ?: "Wren TV"} ${"%.1f".format(it.procCpu)}%" } ?: "")
        Total("Memory", sample?.let { formatBytes(it.mem) } ?: "—", Modifier.weight(1f),
            sample?.let { "${info?.procLabel ?: "Wren TV"} ${formatBytes(it.procMem)}${info?.memTotal?.takeIf { m -> m > 0 }?.let { m -> " of ${formatBytes(m)}" } ?: ""}" } ?: "")
    }
}

/** One stream: what's playing, who's watching on what, and how it's being sent. */
@Composable
private fun NowPlayingCard(n: NowPlaying, serverNow: Double, now: Long, onStop: () -> Unit) {
    val api = LocalAppState.current.api
    val nav = LocalNavigator.current
    val method = if (n.audioOnly) "audio transcode" else n.method
    val color = methodColor(n.method)
    val pos = n.positionAt(serverNow)
    ArtCard(onStop, Modifier.width(560.dp).rememberFocus("now:${n.sessionId}"),
        onLongClick = { if (n.itemId.isNotEmpty() && !n.isLive) nav.push(Route.Detail(n.itemId)) }) {
        Box(Modifier.fillMaxWidth().background(Wren.surface)) {
            RemoteImage(api?.imageUrl(n.backdropPath, "w780"), Modifier.fillMaxWidth().height(170.dp))
            Box(Modifier.fillMaxWidth().height(170.dp).background(Brush.verticalGradient(listOf(Wren.surface.copy(alpha = 0.55f), Wren.surface))))
            Column(Modifier.padding(18.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Row(horizontalArrangement = Arrangement.spacedBy(14.dp)) {
                    if (n.isLive) {
                        val logo = n.channelLogo?.takeIf { it.isNotEmpty() }?.let { if (it.startsWith("http")) it else api?.absolute(it) }
                        RemoteImage(logo, Modifier.size(72.dp).clip(RoundedCornerShape(8.dp)), contentScale = ContentScale.Fit)
                    } else {
                        RemoteImage(api?.imageUrl(n.posterPath, "w185"), Modifier.width(72.dp).aspectRatio(2f / 3f).clip(RoundedCornerShape(8.dp)))
                    }
                    Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
                        Text(n.title, style = MaterialTheme.typography.titleLarge, color = Wren.text, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        val sub = n.subtitle?.takeIf { it.isNotEmpty() } ?: n.year?.toString()
                        if (sub != null) Text(sub, style = MaterialTheme.typography.bodySmall, color = Wren.secondary, maxLines = 1)
                        Text("${n.userName} · ${n.client.ifEmpty { "Unknown player" }}", style = MaterialTheme.typography.bodyMedium, color = Wren.text, maxLines = 1,
                            overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 4.dp))
                        n.ip?.takeIf { it.isNotEmpty() }?.let {
                            Text("$it · ${if (n.local == true) "Local" else "Remote"}", style = MaterialTheme.typography.bodySmall, color = Wren.secondary, maxLines = 1)
                        }
                        Row(Modifier.padding(top = 4.dp), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                            Tag(methodName(method), color)
                            n.stream?.takeIf { !n.isLive && it.height != null }?.let { Tag(res(it.width, it.height)) }
                            n.hw?.takeIf { n.method == "transcode" && n.videoCopy != true && it.isNotEmpty() }?.let { Tag(hwName(it)) }
                            n.library?.takeIf { it.isNotEmpty() }?.let { Tag(it) }
                        }
                    }
                }
                Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    detailRows(n, method).forEach { (label, value) ->
                        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                            Text(label, style = MaterialTheme.typography.bodySmall, color = Wren.tertiary, modifier = Modifier.width(90.dp))
                            Text(value, style = MaterialTheme.typography.bodySmall, color = Wren.text, maxLines = 2, overflow = TextOverflow.Ellipsis)
                        }
                    }
                }
                val fraction = if (n.isLive) 1f else if (n.duration > 0) (pos / n.duration).toFloat().coerceIn(0f, 1f) else 0f
                Box(Modifier.fillMaxWidth().height(5.dp).clip(CircleShape).background(Wren.line)) {
                    Box(Modifier.fillMaxHeight().fillMaxWidth(fraction).background(color))
                }
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    Text(if (n.isPaused) "Paused" else "Playing", style = MaterialTheme.typography.bodySmall, color = if (n.isPaused) Wren.secondary else Wren.text)
                    Text(if (n.isLive) "Watching for ${formatClock(pos)}" else "${formatClock(pos)} / ${formatClock(n.duration)}",
                        style = MaterialTheme.typography.bodySmall, color = Wren.text)
                    if (!n.isLive && n.duration > 0 && !n.isPaused) {
                        val ends = DateFormat.getTimeInstance(DateFormat.SHORT).format(Date(now + ((n.duration - pos) * 1000).toLong()))
                        Text("ends $ends", style = MaterialTheme.typography.bodySmall, color = Wren.secondary)
                    }
                    Spacer(Modifier.weight(1f))
                    Text("OK to stop", style = MaterialTheme.typography.labelSmall, color = Wren.tertiary)
                }
            }
        }
    }
}

/** The card's detail lines, like the server's own dashboard. */
private fun detailRows(n: NowPlaying, method: String): List<Pair<String, String>> {
    val r = mutableListOf<Pair<String, String>>()
    r += "Stream" to methodName(method) + if (n.engine == "mpv") " · mpv" else ""
    if (!n.isLive) {
        val s = n.source ?: TrackInfo()
        val o = n.stream ?: TrackInfo()
        val from = container(s.container)
        val to = container(o.container)
        r += "Container" to if (s.container != null && o.container != null && from != to) "$from → $to" else from
        if (s.videoCodec != null) {
            val src = listOf(codec(s.videoCodec) + (s.videoProfile?.let { " $it" } ?: ""), res(s.width, s.height), s.hdr?.ifEmpty { null } ?: "SDR",
                s.frameRate?.let { "%.3g fps".format(Locale.US, it) } ?: "").filter { it.isNotEmpty() }.joinToString(" · ")
            r += "Video" to when (o.video) {
                "transcode" -> "$src → " + listOf(codec(o.videoCodec), res(o.width, o.height), o.kbps?.let(::rate) ?: "",
                    if (n.tonemap == true) "tone-mapped to SDR" else "").filter { it.isNotEmpty() }.joinToString(" · ")
                "copy" -> "$src · " + if (n.stripDolbyVision == true) "Copied, Dolby Vision removed" else "Copied"
                else -> "$src · Direct"
            }
        }
        if (s.audioCodec != null) {
            val src = listOf(codec(s.audioCodec), channels(s.audioChannels, s.audioLayout), language(s.audioLanguage)).filter { it.isNotEmpty() }.joinToString(" · ")
            r += "Audio" to when (o.audio) {
                "transcode" -> "$src → " + listOf(codec(o.audioCodec), channels(o.audioChannels, o.audioLayout)).filter { it.isNotEmpty() }.joinToString(" · ")
                "copy" -> "$src · Copied"
                else -> "$src · Direct"
            }
        }
        if (n.quality.isNotEmpty()) r += "Version" to n.quality
    }
    val sending = n.stream?.kbps ?: n.delivery?.kbps
    r += "Bitrate" to rate(sending) + (n.source?.kbps?.takeIf { it != sending }?.let { " · file ${rate(it)}" } ?: "")
    n.delivery?.let { d -> d.bytes?.takeIf { it > 0 }?.let { b -> r += "Sent" to formatBytes(b) + (d.kbps?.let { " · ${rate(it)} now" } ?: "") } }
    if (n.method == "transcode") {
        n.delivery?.let { d ->
            val parts = mutableListOf<String>()
            d.speed?.takeIf { it > 0 }?.let { parts += "%.1f× speed".format(it) }
            if (d.encodeDone == true) parts += "Finished encoding" else d.bufferSeconds?.takeIf { it > 0 }?.let { parts += "${it.toInt()} s ahead" }
            val hw = n.hw
            if (!hw.isNullOrEmpty() && n.videoCopy != true) parts += hwName(hw) + if (n.hwDecode == true) " decode + encode" else " encode"
            else if (n.videoCopy == true) parts += "Audio only"
            r += "Transcoder" to parts.ifEmpty { listOf("Starting") }.joinToString(" · ")
        }
    }
    n.reasons?.takeIf { it.isNotEmpty() && n.method != "direct" }?.let { r += "Why" to it.joinToString("; ") }
    return r
}

@Composable
private fun Tag(text: String, color: Color? = null) {
    Row(
        Modifier.background(Wren.glass.copy(alpha = 0.12f), RoundedCornerShape(6.dp)).padding(horizontal = 8.dp, vertical = 3.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        if (color != null) Box(Modifier.size(7.dp).background(color, CircleShape))
        Text(text, style = MaterialTheme.typography.labelSmall, color = Wren.text, maxLines = 1)
    }
}

/** CPU and memory over the last hour: the whole machine and the server's own share. */
@Composable
private fun SystemCharts(samples: List<SystemSample>, a: ActivityResponse, system: SystemResponse?) {
    val info = a.systemInfo ?: system?.info
    val proc = info?.procLabel ?: "Wren TV"
    val host = if (info?.host == false) "Container" else "Host"
    Row(Modifier.padding(horizontal = PageGutter), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
        Card("CPU · last hour", Modifier.weight(1f)) {
            Legend(listOf(host to movieColor, proc to tvColor))
            LineChart(listOf(samples.map { it.cpu } to movieColor, samples.map { it.procCpu } to tvColor), 100.0, "100%")
        }
        Card("Memory · last hour", Modifier.weight(1f)) {
            Legend(listOf(host to movieColor, proc to tvColor))
            val top = (info?.memTotal?.toDouble() ?: 0.0).takeIf { it > 0 } ?: samples.maxOf { it.mem }.toDouble()
            LineChart(listOf(samples.map { it.mem.toDouble() } to movieColor, samples.map { it.procMem.toDouble() } to tvColor), top, formatBytes(top.toLong()))
        }
        val disks = system?.disks.orEmpty()
        if (disks.isNotEmpty() || system?.uptime != null) {
            Card("Server", Modifier.weight(0.8f)) {
                info?.let { StatLine("System", "${osName(it.os)} · ${it.arch} · ${it.cpus} cores") }
                system?.version?.let { StatLine("Wren TV", it) }
                system?.uptime?.let { StatLine("Up for", uptime(it)) }
                disks.take(3).forEach { d -> Meter(d.label.ifEmpty { d.path }, (d.total - d.free).coerceAtLeast(0), d.total.coerceAtLeast(1), Wren.ember,
                    "${formatBytes(d.free)} free of ${formatBytes(d.total)}") }
            }
        }
    }
}

@Composable
private fun Legend(items: List<Pair<String, Color>>) {
    Row(horizontalArrangement = Arrangement.spacedBy(14.dp), verticalAlignment = Alignment.CenterVertically) {
        items.forEach { (name, color) ->
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                Box(Modifier.size(9.dp).background(color, RoundedCornerShape(2.dp)))
                Text(name, style = MaterialTheme.typography.labelSmall, color = Wren.secondary)
            }
        }
    }
}

/** Lines on one scale from zero to [top]. */
@Composable
private fun LineChart(series: List<Pair<List<Double>, Color>>, top: Double, topLabel: String) {
    Row(Modifier.fillMaxWidth().height(130.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Canvas(Modifier.weight(1f).fillMaxHeight()) {
            val max = maxOf(top, series.maxOf { s -> s.first.maxOrNull() ?: 0.0 }, 1.0)
            listOf(0f, 0.5f, 1f).forEach { f -> drawLine(Wren.line, Offset(0f, size.height * f), Offset(size.width, size.height * f), 1f) }
            series.forEach { (values, color) ->
                if (values.size < 2) return@forEach
                val path = Path()
                values.forEachIndexed { i, v ->
                    val x = size.width * i / (values.size - 1)
                    val y = size.height * (1 - (v / max).toFloat())
                    if (i == 0) path.moveTo(x, y) else path.lineTo(x, y)
                }
                drawPath(path, color, style = Stroke(width = 2.dp.toPx(), cap = StrokeCap.Round, join = StrokeJoin.Round))
            }
        }
        Column(Modifier.fillMaxHeight(), verticalArrangement = Arrangement.SpaceBetween) {
            Text(topLabel, style = MaterialTheme.typography.labelSmall, color = Wren.tertiary)
            Text("0", style = MaterialTheme.typography.labelSmall, color = Wren.tertiary)
        }
    }
}

// History

private val chartHeight = 130.dp

/** Plays per day, stacked by what [split] picks out of each day. */
@Composable
private fun DailyChart(title: String, daily: List<DayStat>, series: List<Pair<String, Color>>, modifier: Modifier, split: (DayStat) -> List<Int>) {
    val values = daily.map(split)
    val max = values.maxOf { it.sum() }.coerceAtLeast(1)
    Card(title, modifier) {
        Legend(series)
        Row(Modifier.fillMaxWidth().height(chartHeight), horizontalArrangement = Arrangement.spacedBy(2.dp), verticalAlignment = Alignment.Bottom) {
            values.forEach { day ->
                Column(Modifier.weight(1f).fillMaxHeight(), verticalArrangement = Arrangement.Bottom) {
                    if (day.sum() == 0) Box(Modifier.fillMaxWidth().height(2.dp).background(Wren.line))
                    day.withIndex().reversed().forEach { (i, v) ->
                        if (v > 0) Box(Modifier.fillMaxWidth().height(chartHeight * v / max).background(series[i].second))
                    }
                }
            }
        }
        Row(Modifier.fillMaxWidth()) {
            Text(shortDay(daily.first().date), style = MaterialTheme.typography.bodySmall, color = Wren.tertiary, modifier = Modifier.weight(1f))
            Text(shortDay(daily.last().date), style = MaterialTheme.typography.bodySmall, color = Wren.tertiary)
        }
    }
}

/** Plays in each slot, with a few evenly spaced [labels] under them. */
@Composable
private fun Bars(title: String, values: List<Int>, labels: List<String>, modifier: Modifier) {
    val max = (values.maxOrNull() ?: 0).coerceAtLeast(1)
    Card(title, modifier) {
        Row(Modifier.fillMaxWidth().height(100.dp), horizontalArrangement = Arrangement.spacedBy(3.dp), verticalAlignment = Alignment.Bottom) {
            values.forEach { v ->
                Box(Modifier.weight(1f).fillMaxHeight((v.toFloat() / max).coerceAtLeast(0.02f))
                    .background(if (v > 0) Wren.ember else Wren.line, RoundedCornerShape(topStart = 3.dp, topEnd = 3.dp)))
            }
        }
        Row(Modifier.fillMaxWidth()) {
            labels.forEach { Text(it, style = MaterialTheme.typography.bodySmall, color = Wren.tertiary, modifier = Modifier.weight(1f)) }
        }
    }
}

@Composable
private fun Total(label: String, value: String, modifier: Modifier = Modifier, detail: String = "") {
    Column(modifier.background(Wren.surface, RoundedCornerShape(16.dp)).padding(20.dp)) {
        Text(value, style = MaterialTheme.typography.displaySmall, color = Wren.text, maxLines = 1)
        Text(label, style = MaterialTheme.typography.bodyMedium, color = Wren.secondary)
        if (detail.isNotEmpty()) Text(detail, style = MaterialTheme.typography.bodySmall, color = Wren.tertiary, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

@Composable
private fun Card(title: String, modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    Column(modifier.background(Wren.surface, RoundedCornerShape(16.dp)).padding(20.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(title.uppercase(), style = MaterialTheme.typography.labelSmall, color = Wren.secondary)
        content()
    }
}

@Composable
private fun StatLine(name: String, value: String) {
    Row(Modifier.fillMaxWidth()) {
        Text(name, style = MaterialTheme.typography.bodyMedium, color = Wren.text, modifier = Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
        Text(value, style = MaterialTheme.typography.bodyMedium, color = Wren.secondary, maxLines = 1)
    }
}

/** A name, its count and a bar for its share of [total]. */
@Composable
private fun Meter(name: String, value: Long, total: Long, color: Color, detail: String? = null) {
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        StatLine(name, detail ?: value.toString())
        Box(Modifier.fillMaxWidth().height(5.dp).clip(CircleShape).background(Wren.line)) {
            Box(Modifier.fillMaxHeight().fillMaxWidth((value.toFloat() / total).coerceIn(0.02f, 1f)).background(color))
        }
    }
}

@Composable
private fun Meter(name: String, value: Int, total: Int, color: Color) = Meter(name, value.toLong(), total.toLong(), color)

@Composable
private fun Breakdown(title: String, counts: List<StatCount>?, modifier: Modifier) {
    if (counts.isNullOrEmpty()) return
    val total = counts.sumOf { it.count }.coerceAtLeast(1)
    Column(modifier, verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Text(title, style = MaterialTheme.typography.labelMedium, color = Wren.tertiary)
        counts.take(5).forEach { Meter(it.name.ifEmpty { "Other" }, it.count, total, Wren.ember) }
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

// Formatting, matching the server's dashboard.

private fun methodName(m: String) = when (m) {
    "direct" -> "Direct play"
    "remux" -> "Direct stream"
    "transcode" -> "Transcode"
    "audio transcode" -> "Audio transcode"
    else -> m.replaceFirstChar { it.uppercase() }
}

private fun hours(h: Double) = if (h >= 10) "%,.0f".format(h) else "%.1f".format(h)

private fun rate(kbps: Int?): String {
    val k = kbps ?: 0
    if (k <= 0) return "0 Mbps"
    return if (k >= 1000) (if (k >= 10_000) "%.0f Mbps" else "%.1f Mbps").format(k / 1000.0) else "$k kbps"
}

private fun res(w: Int?, h: Int?): String {
    val width = w ?: 0
    val height = h ?: 0
    return when {
        width == 0 && height == 0 -> ""
        width >= 3200 || height >= 1600 -> "4K"
        width >= 1700 || height >= 1000 -> "1080p"
        width >= 1100 || height >= 700 -> "720p"
        height >= 560 -> "576p"
        height >= 470 -> "480p"
        height > 0 -> "${height}p"
        else -> "SD"
    }
}

private val codecs = mapOf(
    "hevc" to "HEVC", "h265" to "HEVC", "h264" to "H.264", "avc" to "H.264", "av1" to "AV1", "vp9" to "VP9", "mpeg2video" to "MPEG-2",
    "mpeg4" to "MPEG-4", "vc1" to "VC-1", "aac" to "AAC", "ac3" to "Dolby Digital", "eac3" to "Dolby Digital+", "truehd" to "TrueHD",
    "dts" to "DTS", "flac" to "FLAC", "opus" to "Opus", "mp3" to "MP3", "pcm_s16le" to "PCM", "pcm_s24le" to "PCM",
)

private fun codec(c: String?) = (c ?: "").let { codecs[it.lowercase()] ?: it.uppercase() }

private fun container(c: String?): String {
    val v = (c ?: "").lowercase()
    return when {
        v.isEmpty() -> "—"
        "matroska" in v -> "MKV"
        "mp4" in v || "mov" in v -> "MP4"
        v == "mpegts" -> "MPEG-TS"
        else -> v.substringBefore(',').uppercase()
    }
}

private fun channels(n: Int?, layout: String?): String {
    Regex("""^\d\.\d""").find(layout ?: "")?.let { return it.value }
    return when (n ?: 0) {
        0 -> ""
        1 -> "Mono"
        2 -> "Stereo"
        6 -> "5.1"
        8 -> "7.1"
        else -> "$n ch"
    }
}

private fun language(code: String?): String {
    if (code.isNullOrEmpty() || code == "und") return ""
    val name = Locale.forLanguageTag(code).getDisplayLanguage(Locale.getDefault())
    return name.ifEmpty { code }
}

private fun hwName(k: String) = mapOf(
    "videotoolbox" to "VideoToolbox", "nvenc" to "NVIDIA NVENC", "qsv" to "Intel Quick Sync", "vaapi" to "VA-API", "amf" to "AMD AMF", "software" to "Software",
)[k] ?: k

private fun osName(o: String) = mapOf("linux" to "Linux", "darwin" to "macOS", "windows" to "Windows", "freebsd" to "FreeBSD")[o] ?: o

private fun uptime(s: Long): String {
    val d = s / 86400
    val h = (s % 86400) / 3600
    val m = (s % 3600) / 60
    return if (d > 0) "$d d $h h" else if (h > 0) "$h h $m min" else "$m min"
}

private fun shortDay(date: String): String = runCatching {
    val d = java.text.SimpleDateFormat("yyyy-MM-dd", Locale.US).parse(date)!!
    java.text.SimpleDateFormat("MMM d", Locale.getDefault()).format(d)
}.getOrDefault(date)
