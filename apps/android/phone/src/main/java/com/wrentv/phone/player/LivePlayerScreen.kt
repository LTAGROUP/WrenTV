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
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.automirrored.rounded.List
import androidx.compose.material.icons.rounded.KeyboardArrowDown
import androidx.compose.material.icons.rounded.KeyboardArrowUp
import androidx.compose.material.icons.rounded.Pause
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.wrentv.phone.ui.ChannelLogo
import com.wrentv.phone.ui.LiveTarget
import com.wrentv.phone.ui.LocalAppState
import com.wrentv.phone.ui.LocalNavigator
import com.wrentv.phone.ui.Wren
import com.wrentv.tv.data.LiveChannel
import com.wrentv.tv.player.LiveController
import kotlinx.coroutines.delay

/** A live channel, full screen. Tap for the banner; flip channels with the arrows or pick from the list. */
@Composable
fun LivePlayerScreen(target: LiveTarget) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val pick = { all: List<LiveChannel> ->
        when {
            target.ids != null -> target.ids.mapNotNull { id -> all.firstOrNull { it.id == id } }
            target.group != null -> all.filter { it.group == target.group }
            else -> all
        }
    }
    var channels by remember(target) { mutableStateOf(pick(state.liveChannels)) }
    var error by remember(target) { mutableStateOf<String?>(null) }
    LaunchedEffect(target) {
        if (channels.none { it.id == target.channelId }) {
            runCatching { state.loadLiveChannels() }
                .onSuccess { channels = pick(it); if (it.none { c -> c.id == target.channelId }) error = "That channel isn't available any more." }
                .onFailure { error = it.message ?: "Couldn't load channels" }
        }
    }
    Immersive()
    val close = { nav.live = null }
    BackHandler(onBack = close)
    val start = channels.firstOrNull { it.id == target.channelId }
    Box(Modifier.fillMaxSize().background(Color.Black)) {
        when {
            start != null -> LivePlayer(channels, start, close)
            error != null -> Message(error!!, null, close)
            else -> Spinner()
        }
    }
}

@Composable
private fun LivePlayer(channels: List<LiveChannel>, start: LiveChannel, close: () -> Unit) {
    val state = LocalAppState.current
    val context = LocalContext.current
    val c = remember { LiveController(context, state.api, channels, start, state.scope) }
    DisposableEffect(c) {
        c.start()
        onDispose { c.stop() }
    }
    var controls by remember { mutableStateOf(true) }
    var touched by remember { mutableIntStateOf(0) }
    var list by remember { mutableStateOf(false) }
    LaunchedEffect(controls, touched, list) { if (controls && !list) { delay(5000); controls = false } }
    LaunchedEffect(c.channel.id) { controls = true; touched++ }
    fun poke() { controls = true; touched++ }

    Box(Modifier.fillMaxSize().clickable(remember { MutableInteractionSource() }, indication = null) {
        if (list) list = false else if (controls) controls = false else poke()
    }) {
        VideoSurface(c.player, Modifier.fillMaxSize())
        when (val p = c.phase) {
            is LiveController.Phase.Failed -> Message(p.message, { c.retry() }, close)
            LiveController.Phase.Tuning -> Spinner()
            else -> if (c.buffering) Spinner()
        }
        AnimatedVisibility(controls || list, enter = fadeIn(), exit = fadeOut()) {
            Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0f to Color.Black.copy(alpha = 0.6f), 0.3f to Color.Transparent,
                0.6f to Color.Transparent, 1f to Color.Black.copy(alpha = 0.8f)))) {
                Row(Modifier.fillMaxWidth().safeDrawingPadding().padding(8.dp), verticalAlignment = Alignment.CenterVertically) {
                    IconButton(close) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Close", tint = Color.White) }
                    Text("LIVE", Modifier.clip(RoundedCornerShape(4.dp)).background(Wren.ember).padding(horizontal = 6.dp, vertical = 2.dp),
                        color = Color.White, fontSize = 11.sp, fontWeight = FontWeight.Black)
                    Box(Modifier.weight(1f))
                    IconButton({ c.togglePause(); poke() }) { Icon(if (c.paused) Icons.Rounded.PlayArrow else Icons.Rounded.Pause, if (c.paused) "Play" else "Pause", tint = Color.White) }
                    IconButton({ list = !list; poke() }) { Icon(Icons.AutoMirrored.Rounded.List, "Channels", tint = Color.White) }
                }
                Banner(c, Modifier.align(Alignment.BottomStart).safeDrawingPadding().padding(20.dp)) { d -> c.flip(d); poke() }
            }
        }
        if (list) ChannelList(c, Modifier.align(Alignment.CenterEnd)) { ch -> c.tune(ch); list = false }
    }
}

@Composable
private fun Banner(c: LiveController, modifier: Modifier, flip: (Int) -> Unit) {
    val ch = c.channel
    val now = System.currentTimeMillis() / 1000
    val program = ch.now?.takeIf { it.start <= now && now < it.end }
    Row(modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(14.dp), verticalAlignment = Alignment.CenterVertically) {
        Column {
            IconButton({ flip(-1) }) { Icon(Icons.Rounded.KeyboardArrowUp, "Previous channel", tint = Color.White) }
            IconButton({ flip(1) }) { Icon(Icons.Rounded.KeyboardArrowDown, "Next channel", tint = Color.White) }
        }
        ChannelLogo(ch.logo, ch.name, Modifier.size(80.dp, 50.dp))
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                ch.number?.let { Text(it, color = Color.White.copy(alpha = 0.7f), fontSize = 14.sp) }
                Text(ch.name, color = Color.White, fontWeight = FontWeight.Bold, fontSize = 18.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            if (program != null) {
                Text(program.title, color = Color.White, maxLines = 1, overflow = TextOverflow.Ellipsis)
                LinearProgressIndicator({ program.progress(now) }, Modifier.width(220.dp), color = Color.White, trackColor = Color.White.copy(alpha = 0.2f), drawStopIndicator = {})
            }
            ch.next?.let { Text("Next: ${it.title}", color = Color.White.copy(alpha = 0.6f), fontSize = 12.sp, maxLines = 1, overflow = TextOverflow.Ellipsis) }
        }
    }
}

@Composable
private fun ChannelList(c: LiveController, modifier: Modifier, tune: (LiveChannel) -> Unit) {
    val index = c.channels.indexOfFirst { it.id == c.channel.id }.coerceAtLeast(0)
    val listState = rememberLazyListState((index - 2).coerceAtLeast(0))
    LazyColumn(
        modifier.fillMaxHeight().width(320.dp).background(Color.Black.copy(alpha = 0.85f)).safeDrawingPadding()
            .clickable(remember { MutableInteractionSource() }, indication = null) {},
        listState,
    ) {
        items(c.channels, { it.id }) { ch ->
            val current = ch.id == c.channel.id
            Row(
                Modifier.fillMaxWidth().background(if (current) Color.White.copy(alpha = 0.12f) else Color.Transparent).clickable { tune(ch) }
                    .padding(horizontal = 14.dp, vertical = 8.dp),
                horizontalArrangement = Arrangement.spacedBy(10.dp), verticalAlignment = Alignment.CenterVertically,
            ) {
                ChannelLogo(ch.logo, ch.name, Modifier.size(52.dp, 32.dp))
                Column(Modifier.weight(1f)) {
                    Text(listOfNotNull(ch.number, ch.name).joinToString("  "), color = Color.White, fontWeight = if (current) FontWeight.Bold else FontWeight.Medium,
                        fontSize = 14.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    ch.now?.let { Text(it.title, color = Color.White.copy(alpha = 0.6f), fontSize = 12.sp, maxLines = 1, overflow = TextOverflow.Ellipsis) }
                }
            }
        }
    }
}
