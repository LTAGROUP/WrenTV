package com.wrentv.phone.player

import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.Audiotrack
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.Info
import androidx.compose.material.icons.rounded.VideoLibrary
import androidx.compose.material.icons.rounded.Search
import androidx.compose.material.icons.rounded.Check
import androidx.compose.material.icons.rounded.ClosedCaption
import androidx.compose.material.icons.rounded.Forward10
import androidx.compose.material.icons.rounded.HighQuality
import androidx.compose.material.icons.rounded.Pause
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Replay10
import androidx.compose.material.icons.rounded.SkipNext
import androidx.compose.material.icons.rounded.Warning
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.TextButton
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Slider
import androidx.compose.material3.SliderDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.wrentv.phone.ui.LocalAppState
import com.wrentv.phone.ui.LocalNavigator
import com.wrentv.phone.ui.PrimaryButton
import com.wrentv.phone.ui.GlassButton
import com.wrentv.phone.ui.RemoteImage
import com.wrentv.phone.ui.Wren
import com.wrentv.phone.ui.formatClock
import com.wrentv.tv.data.Item
import com.wrentv.tv.data.OnlineSubtitle
import com.wrentv.tv.data.languageName
import com.wrentv.tv.player.PlayerController
import com.wrentv.tv.player.TrackOption
import com.wrentv.tv.ui.PlayTarget
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** A film or episode, full screen and landscape. Tap shows the controls; they fade after a few seconds. */
@Composable
fun PlayerScreen(target: PlayTarget) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val context = LocalContext.current
    val c = remember(target) { PlayerController(context, state.api, state.downloads, target, state.scope) }
    DisposableEffect(c) {
        c.start()
        onDispose { c.stop(); state.refreshTick++ }
    }
    Immersive()
    val close = { nav.playing = null }
    BackHandler(onBack = close)
    LaunchedEffect(c.closeRequested) { if (c.closeRequested) close() }

    var controls by remember { mutableStateOf(true) }
    var touched by remember { mutableIntStateOf(0) }
    var dragging by remember { mutableStateOf<Float?>(null) }
    var menuOpen by remember { mutableStateOf(false) }
    var shelf by remember { mutableStateOf(false) }
    var stats by remember { mutableStateOf(false) }
    var search by remember { mutableStateOf(false) }
    LaunchedEffect(controls, touched, c.paused, dragging, menuOpen, shelf) {
        if (controls && !c.paused && dragging == null && !menuOpen && !shelf) { delay(4000); controls = false }
    }
    fun poke() { controls = true; touched++ }

    Box(
        Modifier.fillMaxSize().background(Color.Black)
            .clickable(remember { MutableInteractionSource() }, indication = null) { if (controls) controls = false else poke() },
    ) {
        VideoSurface(c.player, Modifier.fillMaxSize())
        val phase = c.phase
        when (phase) {
            is PlayerController.Phase.Loading -> Spinner()
            is PlayerController.Phase.Pending -> Message(phase.message, null, close)
            is PlayerController.Phase.Failed -> Message(phase.message, { c.retry() }, close)
            else -> if (c.buffering) Spinner()
        }

        AnimatedVisibility(controls && phase !is PlayerController.Phase.Failed, enter = fadeIn(), exit = fadeOut()) {
            Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0f to Color.Black.copy(alpha = 0.6f), 0.3f to Color.Transparent,
                0.7f to Color.Transparent, 1f to Color.Black.copy(alpha = 0.7f)))) {
                Row(Modifier.fillMaxWidth().safeDrawingPadding().padding(8.dp), verticalAlignment = Alignment.CenterVertically) {
                    IconButton(close) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Close", tint = Color.White) }
                    Column(Modifier.weight(1f)) {
                        Text(c.title, color = Color.White, fontWeight = FontWeight.Bold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        c.subtitle?.let { Text(it, color = Color.White.copy(alpha = 0.7f), fontSize = 13.sp, maxLines = 1, overflow = TextOverflow.Ellipsis) }
                    }
                    if (c.versions.size > 1) TrackMenu(Icons.Rounded.HighQuality, "Version", c.versions.map { TrackOption(it.id, it.label) }, c.fileId, null,
                        { menuOpen = it }) { it?.let(c::selectVersion) }
                    if (c.audioOptions.size > 1) TrackMenu(Icons.Rounded.Audiotrack, "Audio", c.audioOptions, c.audioKey, null, { menuOpen = it }) { it?.let(c::selectAudio) }
                    if (c.subtitleOptions.isNotEmpty() || c.onlineSubtitles) TrackMenu(Icons.Rounded.ClosedCaption, "Subtitles", c.subtitleOptions, c.subtitleKey, "Off",
                        { menuOpen = it }, search = if (c.onlineSubtitles) ({ search = true }) else null) { c.selectSubtitle(it) }
                    if (c.episode != null) IconButton({ shelf = !shelf; poke() }) { Icon(Icons.Rounded.VideoLibrary, "Episodes", tint = if (shelf) Wren.ember else Color.White) }
                    IconButton({ stats = !stats; poke() }) { Icon(Icons.Rounded.Info, "Stats", tint = if (stats) Wren.ember else Color.White) }
                }
                if (phase is PlayerController.Phase.Playing && !shelf) Row(Modifier.align(Alignment.Center), horizontalArrangement = Arrangement.spacedBy(48.dp), verticalAlignment = Alignment.CenterVertically) {
                    RoundButton(Icons.Rounded.Replay10, "Back 10 seconds", 44.dp) { c.seekBy(-10.0); poke() }
                    RoundButton(if (c.paused) Icons.Rounded.PlayArrow else Icons.Rounded.Pause, if (c.paused) "Play" else "Pause", 64.dp) { c.togglePause(); poke() }
                    RoundButton(Icons.Rounded.Forward10, "Forward 10 seconds", 44.dp) { c.seekBy(10.0); poke() }
                }
                if (c.duration > 0) Column(Modifier.align(Alignment.BottomCenter).fillMaxWidth().safeDrawingPadding().padding(horizontal = 16.dp, vertical = 8.dp)) {
                    val fraction = dragging ?: (c.position / c.duration).toFloat().coerceIn(0f, 1f)
                    Slider(
                        fraction, { dragging = it; touched++ }, onValueChangeFinished = { dragging?.let { c.seekTo(it * c.duration) }; dragging = null },
                        colors = SliderDefaults.colors(thumbColor = Color.White, activeTrackColor = Wren.ember, inactiveTrackColor = Color.White.copy(alpha = 0.25f)),
                    )
                    Row {
                        Text(formatClock(fraction * c.duration), color = Color.White, fontSize = 12.sp)
                        Spacer(Modifier.weight(1f))
                        Text("-" + formatClock(c.duration - fraction * c.duration), color = Color.White.copy(alpha = 0.7f), fontSize = 12.sp)
                    }
                    if (shelf) EpisodeShelf(c) { shelf = false }
                }
            }
        }

        if (stats) Stats(c, Modifier.align(Alignment.TopStart).safeDrawingPadding().padding(start = 16.dp, top = 64.dp))
        if (search) OnlineSubtitlesDialog(c) { search = false }

        // Skip intro / credits, and the next episode.
        if (!shelf) Box(Modifier.align(Alignment.BottomEnd).safeDrawingPadding().padding(end = 24.dp, bottom = if (controls) 96.dp else 32.dp)) {
            val next = c.upNext
            when {
                next != null && c.skip == PlayerController.Skip.Credits || next != null && c.countdown != null -> UpNext(c)
                c.skip == PlayerController.Skip.Intro -> GlassButton("Skip Intro", Icons.Rounded.SkipNext, { c.skipIntro() })
                c.skip == PlayerController.Skip.Credits -> GlassButton("Skip Credits", Icons.Rounded.SkipNext, { c.skipCredits() })
            }
        }
    }
}

@Composable
private fun UpNext(c: PlayerController) {
    val state = LocalAppState.current
    val next = c.upNext ?: return
    Row(Modifier.clip(RoundedCornerShape(12.dp)).background(Color.Black.copy(alpha = 0.75f)).padding(10.dp), horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalAlignment = Alignment.CenterVertically) {
        RemoteImage(state.api?.imageUrl(next.stillPath, "w300"), Modifier.width(120.dp).size(120.dp, 68.dp).clip(RoundedCornerShape(6.dp)))
        Column(Modifier.width(180.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text("UP NEXT", color = Color.White.copy(alpha = 0.6f), fontSize = 11.sp, fontWeight = FontWeight.Bold)
            Text("${next.episodeLabel} — ${next.title}", color = Color.White, fontWeight = FontWeight.SemiBold, maxLines = 2, overflow = TextOverflow.Ellipsis, fontSize = 14.sp)
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                PrimaryButton(c.countdown?.let { "Play in $it" } ?: "Play", Icons.Rounded.PlayArrow, { c.playNext() })
                if (c.skip == PlayerController.Skip.Credits) Text("Watch Credits", Modifier.clip(CircleShape).clickable { c.dismissCredits() }.padding(8.dp),
                    color = Color.White.copy(alpha = 0.8f), fontSize = 13.sp)
            }
        }
    }
}

@Composable
private fun TrackMenu(
    icon: ImageVector, label: String, options: List<TrackOption>, selected: String?, none: String?, opened: (Boolean) -> Unit,
    search: (() -> Unit)? = null, pick: (String?) -> Unit,
) {
    var open by remember { mutableStateOf(false) }
    Box {
        IconButton({ open = true; opened(true) }) { Icon(icon, label, tint = Color.White) }
        DropdownMenu(open, { open = false; opened(false) }) {
            Text(label.uppercase(), Modifier.padding(horizontal = 16.dp, vertical = 6.dp), color = Wren.secondary, fontSize = 11.sp, fontWeight = FontWeight.Bold)
            if (none != null) DropdownMenuItem({ Text(none) }, { open = false; opened(false); pick(null) },
                trailingIcon = { if (selected == null) Icon(Icons.Rounded.Check, null) })
            options.forEach { o ->
                DropdownMenuItem({ Text(o.label) }, { open = false; opened(false); pick(o.key) }, trailingIcon = { if (selected == o.key) Icon(Icons.Rounded.Check, null) })
            }
            if (search != null) {
                HorizontalDivider()
                DropdownMenuItem({ Text("Search online…") }, { open = false; opened(false); search() }, leadingIcon = { Icon(Icons.Rounded.Search, null) })
            }
        }
    }
}

/** The season's episodes in a row under the timeline, with the seasons as chips above. */
@Composable
private fun EpisodeShelf(c: PlayerController, onClose: () -> Unit) {
    val ep = c.episode ?: return
    val api = LocalAppState.current.api
    var seasons by remember { mutableStateOf<List<Item>>(emptyList()) }
    var seasonId by remember { mutableStateOf(ep.parentId) }
    var episodes by remember { mutableStateOf<List<Item>?>(null) }
    val list = rememberLazyListState()
    LaunchedEffect(ep.showId) {
        seasons = ep.showId?.let { runCatching { c.children(it) }.getOrNull() }.orEmpty().sortedBy { it.seasonNumber }
    }
    LaunchedEffect(seasonId) {
        val first = episodes == null
        val fresh = seasonId?.let { runCatching { c.children(it) }.getOrNull() }.orEmpty().sortedBy { it.episodeNumber }
        episodes = fresh
        if (first) list.scrollToItem(fresh.indexOfFirst { it.id == c.itemId }.coerceAtLeast(0))
    }
    Column(Modifier.fillMaxWidth().padding(top = 8.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        if (seasons.size > 1) LazyRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            items(seasons, key = { it.id }) { s ->
                val on = s.id == seasonId
                Text(s.displayTitle, Modifier.clip(CircleShape).background(if (on) Color.White else Color.White.copy(alpha = 0.15f))
                    .clickable { seasonId = s.id }.padding(horizontal = 14.dp, vertical = 6.dp),
                    color = if (on) Color.Black else Color.White, fontSize = 13.sp, fontWeight = FontWeight.SemiBold)
            }
        }
        val eps = episodes
        if (eps == null) Box(Modifier.fillMaxWidth().heightIn(min = 120.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator(color = Color.White) }
        else LazyRow(state = list, horizontalArrangement = Arrangement.spacedBy(12.dp), contentPadding = PaddingValues(bottom = 4.dp)) {
            items(eps, key = { it.id }) { e ->
                val playing = e.id == c.itemId
                Column(Modifier.width(180.dp).clip(RoundedCornerShape(8.dp)).clickable { if (!playing) c.playEpisode(e); onClose() }) {
                    Box(Modifier.fillMaxWidth().aspectRatio(16f / 9f).clip(RoundedCornerShape(8.dp)).background(Color.White.copy(alpha = 0.08f))) {
                        RemoteImage(api?.imageUrl(e.stillPath ?: e.backdropPath, "w300"), Modifier.fillMaxSize())
                        if (playing) Text("Playing", Modifier.align(Alignment.TopStart).padding(6.dp).background(Wren.ember, RoundedCornerShape(6.dp))
                            .padding(horizontal = 6.dp, vertical = 2.dp), color = Color.White, fontSize = 11.sp, fontWeight = FontWeight.Bold)
                        else if (e.isWatched) Icon(Icons.Rounded.CheckCircle, "Watched", Modifier.align(Alignment.TopEnd).padding(6.dp).size(20.dp), tint = Wren.ember)
                        e.progress?.takeIf { it.position > 0 && !it.watched && !playing }?.let {
                            Box(Modifier.align(Alignment.BottomStart).fillMaxWidth(it.fraction.toFloat()).height(3.dp).background(Wren.ember))
                        }
                    }
                    Text("${e.episodeNumber}. ${e.title}", Modifier.padding(top = 6.dp), color = Color.White, fontSize = 13.sp, fontWeight = FontWeight.SemiBold,
                        maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            }
        }
    }
}

/** How the title is playing, refreshed every second while it shows. */
@Composable
private fun Stats(c: PlayerController, modifier: Modifier = Modifier) {
    var rows by remember { mutableStateOf(c.stats()) }
    LaunchedEffect(c) {
        while (true) { rows = c.stats(); delay(1000) }
    }
    Column(modifier.widthIn(max = 360.dp).background(Color.Black.copy(alpha = 0.72f), RoundedCornerShape(12.dp)).padding(horizontal = 14.dp, vertical = 10.dp),
        verticalArrangement = Arrangement.spacedBy(2.dp)) {
        rows.forEach { (k, v) ->
            Row {
                Text(k, Modifier.width(100.dp), color = Color.White.copy(alpha = 0.55f), fontSize = 12.sp)
                Text(v, color = Color.White, fontSize = 12.sp)
            }
        }
    }
}

/** Searches the subtitle sites and adds the one picked to what's playing. */
@Composable
private fun OnlineSubtitlesDialog(c: PlayerController, onDismiss: () -> Unit) {
    val scope = rememberCoroutineScope()
    var results by remember { mutableStateOf<List<OnlineSubtitle>?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var adding by remember { mutableStateOf<OnlineSubtitle?>(null) }
    LaunchedEffect(Unit) {
        results = try {
            c.searchOnlineSubtitles()
        } catch (e: Exception) {
            if (e is kotlinx.coroutines.CancellationException) throw e
            error = e.message ?: "The search failed."
            emptyList()
        }
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        confirmButton = { TextButton(onDismiss) { Text("Close") } },
        title = { Text("Search online") },
        text = {
            val rs = results
            when {
                rs == null || adding != null -> Box(Modifier.fillMaxWidth().padding(vertical = 32.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
                error != null && rs.isEmpty() -> Text(error ?: "")
                rs.isEmpty() -> Text("No subtitles found for this title.")
                else -> Column {
                    error?.let { Text(it, color = Wren.ember, fontSize = 13.sp, modifier = Modifier.padding(bottom = 8.dp)) }
                    LazyColumn(Modifier.heightIn(max = 360.dp)) {
                        items(rs) { r ->
                            Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)).clickable {
                                adding = r
                                scope.launch {
                                    try {
                                        c.addOnlineSubtitle(r)
                                        onDismiss()
                                    } catch (e: Exception) {
                                        if (e is kotlinx.coroutines.CancellationException) throw e
                                        error = e.message ?: "Couldn't download that subtitle."
                                        adding = null
                                    }
                                }
                            }.padding(vertical = 8.dp, horizontal = 4.dp)) {
                                Text(r.name.ifEmpty { r.provider }, fontWeight = FontWeight.SemiBold, maxLines = 2, overflow = TextOverflow.Ellipsis)
                                Text(listOfNotNull(languageName(r.language) ?: r.language.uppercase(), r.provider,
                                    if (r.hearingImpaired) "SDH" else null, if (r.machineTranslated) "Machine translated" else null,
                                    if (r.downloads > 0) "${r.downloads} downloads" else null).joinToString(" · "), fontSize = 12.sp, color = Wren.secondary)
                            }
                        }
                    }
                }
            }
        },
    )
}

@Composable
fun RoundButton(icon: ImageVector, label: String, size: androidx.compose.ui.unit.Dp, onClick: () -> Unit) {
    Box(Modifier.size(size + 16.dp).clip(CircleShape).background(Color.Black.copy(alpha = 0.35f)).clickable(onClick = onClick), contentAlignment = Alignment.Center) {
        Icon(icon, label, Modifier.size(size), tint = Color.White)
    }
}

@Composable
fun Spinner() {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator(color = Color.White) }
}

@Composable
fun Message(text: String, retry: (() -> Unit)?, close: () -> Unit) {
    Column(Modifier.fillMaxSize().padding(32.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically)) {
        Icon(Icons.Rounded.Warning, null, Modifier.size(40.dp), tint = Wren.ember)
        Text(text, color = Color.White, textAlign = TextAlign.Center)
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            if (retry != null) PrimaryButton("Try Again", null, retry)
            GlassButton("Close", null, close)
        }
    }
}
