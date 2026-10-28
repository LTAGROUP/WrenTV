@file:OptIn(UnstableApi::class)

package com.wrentv.tv.player

import android.view.ViewGroup
import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Pause
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onKeyEvent
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
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
import com.wrentv.tv.data.LiveChannel
import com.wrentv.tv.ui.Bar
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.PillButton
import com.wrentv.tv.ui.Spinner
import com.wrentv.tv.ui.UiSounds
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.screens.ChannelLogo
import com.wrentv.tv.ui.screens.inGroup
import com.wrentv.tv.ui.screens.timeOf
import com.wrentv.tv.ui.screens.timeRange
import kotlinx.coroutines.android.awaitFrame
import kotlinx.coroutines.delay

/**
 * Live TV, full screen. Up and down (or channel up and down) flip channels,
 * OK opens the channel list, number keys tune straight to a channel, and
 * Back closes whatever is showing, then leaves.
 */
@Composable
fun LivePlayerScreen(channelId: String, group: String? = null, ids: List<String>? = null) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    // Opened from a group, flipping stays in it, in the order the group page shows.
    val pick = { all: List<LiveChannel> ->
        when {
            ids != null -> ids.mapNotNull { id -> all.firstOrNull { it.id == id } }
            group != null -> all.inGroup(group, System.currentTimeMillis() / 1000)
            else -> all
        }
    }
    var channels by remember { mutableStateOf(pick(state.liveChannels)) }
    var error by remember { mutableStateOf<String?>(null) }
    // Opened without the list loaded (straight from Home, say): fetch it first.
    LaunchedEffect(Unit) {
        if (channels.none { it.id == channelId }) {
            runCatching { state.loadLiveChannels() }
                .onSuccess { channels = pick(it); if (it.none { c -> c.id == channelId }) error = "That channel isn't available any more." }
                .onFailure { error = it.message ?: "Couldn't load channels" }
        }
    }
    val start = channels.firstOrNull { it.id == channelId }
    when {
        start != null -> LivePlayer(channels, start, pick)
        error != null -> Box(Modifier.fillMaxSize().background(Color.Black)) {
            Failed(error!!, retry = null, back = { nav.back() })
        }
        else -> Box(Modifier.fillMaxSize().background(Color.Black), contentAlignment = Alignment.Center) { Spinner() }
    }
}

@Composable
private fun LivePlayer(channels: List<LiveChannel>, start: LiveChannel, pick: (List<LiveChannel>) -> List<LiveChannel>) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val context = LocalContext.current
    val c = remember { LiveController(context, state.api, channels, start, state.scope) }
    DisposableEffect(c) {
        c.start()
        onDispose { c.stop() }
    }
    DisposableEffect(Unit) {
        UiSounds.muted = true
        ThemeMusic.stop()
        onDispose { UiSounds.muted = false }
    }
    // Remember where to start next time, once a channel has actually been landed on.
    LaunchedEffect(c.channel.id, c.phase) {
        if (c.phase != LiveController.Phase.Playing) return@LaunchedEffect
        delay(3_000)
        state.lastChannel = c.channel.id
    }
    // Keep what's on now and next current.
    LaunchedEffect(c) {
        while (true) {
            delay(60_000)
            runCatching { state.loadLiveChannels() }.onSuccess { c.update(pick(it)) }
        }
    }

    var banner by remember { mutableStateOf(true) }
    var guide by remember { mutableStateOf(false) }
    var digits by remember { mutableStateOf("") }
    // Bumped on every key press so the banner stays up while in use.
    var touched by remember { mutableIntStateOf(0) }
    val root = remember { FocusRequester() }

    fun showBanner() { banner = true; touched++ }

    // The banner shows on every channel change and goes after a few seconds.
    LaunchedEffect(c.channel.id) { showBanner() }
    LaunchedEffect(banner, touched, c.paused, c.phase) {
        if (banner && !c.paused && c.phase == LiveController.Phase.Playing) {
            delay(5_000)
            banner = false
        }
    }
    // Number keys: tune once typing pauses.
    LaunchedEffect(digits) {
        if (digits.isEmpty()) return@LaunchedEffect
        delay(1_500)
        c.tuneNumber(digits)
        digits = ""
    }
    LaunchedEffect(guide, c.phase) {
        awaitFrame()
        if (!guide && c.phase !is LiveController.Phase.Failed) runCatching { root.requestFocus() }
    }

    BackHandler {
        when {
            guide -> guide = false
            // The banner is hidden behind the error, so Back leaves rather than dismissing it.
            c.phase is LiveController.Phase.Failed -> nav.back()
            digits.isNotEmpty() -> digits = ""
            banner -> banner = false
            else -> nav.back()
        }
    }

    Box(
        Modifier.fillMaxSize().background(Color.Black)
            .onPreviewKeyEvent { if (it.type == KeyEventType.KeyDown) touched++; false }
            .focusRequester(root)
            .focusable()
            .onKeyEvent { e ->
                if (e.type != KeyEventType.KeyDown || guide) return@onKeyEvent false
                digit(e.key)?.let { d ->
                    if (digits.length < 5) digits += d
                    return@onKeyEvent true
                }
                when (e.key) {
                    Key.DirectionUp, Key.ChannelUp -> { c.flip(1); true }
                    Key.DirectionDown, Key.ChannelDown -> { c.flip(-1); true }
                    Key.DirectionCenter, Key.Enter, Key.NumPadEnter -> {
                        if (digits.isNotEmpty()) { c.tuneNumber(digits); digits = "" } else guide = true
                        true
                    }
                    Key.MediaPlayPause, Key.MediaPlay, Key.MediaPause -> { c.togglePause(); showBanner(); true }
                    Key.DirectionLeft, Key.DirectionRight, Key.Info, Key.Menu -> { showBanner(); true }
                    else -> false
                }
            },
    ) {
        AndroidView(
            factory = { ctx ->
                PlayerView(ctx).apply {
                    layoutParams = ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT)
                    useController = false
                    keepScreenOn = true
                    resizeMode = AspectRatioFrameLayout.RESIZE_MODE_FIT
                    setShowBuffering(PlayerView.SHOW_BUFFERING_NEVER)
                    isFocusable = false
                    player = c.player
                }
            },
            modifier = Modifier.fillMaxSize(),
        )

        when (val phase = c.phase) {
            is LiveController.Phase.Failed -> Failed(phase.message, retry = { c.retry() }, back = { nav.back() })
            LiveController.Phase.Tuning -> Center { Spinner() }
            LiveController.Phase.Playing -> if (c.buffering) Center { Spinner() }
        }

        if (c.paused) {
            Icon(
                Icons.Rounded.Pause, "Paused",
                Modifier.align(Alignment.TopEnd).padding(48.dp).background(Color.Black.copy(alpha = 0.5f), RoundedCornerShape(50)).padding(14.dp),
                tint = Color.White,
            )
        }

        AnimatedVisibility(banner && !guide && c.phase !is LiveController.Phase.Failed, Modifier.align(Alignment.BottomCenter), enter = fadeIn(), exit = fadeOut()) {
            Banner(c.channel)
        }

        if (digits.isNotEmpty()) {
            Text(
                digits, style = MaterialTheme.typography.displayMedium, color = Color.White,
                modifier = Modifier.align(Alignment.TopEnd).padding(48.dp)
                    .background(Color.Black.copy(alpha = 0.6f), RoundedCornerShape(16.dp)).padding(horizontal = 24.dp, vertical = 8.dp),
            )
        }

        AnimatedVisibility(guide, enter = slideInHorizontally { -it } + fadeIn(), exit = slideOutHorizontally { -it } + fadeOut()) {
            ChannelList(c.channels, c.channel.id) { ch ->
                guide = false
                c.tune(ch)
            }
        }
    }
}

/** What's on: the channel, the programme and how far into it, and what's next. */
@Composable
private fun Banner(ch: LiveChannel) {
    val state = LocalAppState.current
    var now by remember { mutableLongStateOf(System.currentTimeMillis() / 1000) }
    LaunchedEffect(Unit) { while (true) { delay(15_000); now = System.currentTimeMillis() / 1000 } }
    Box(
        Modifier.fillMaxWidth().height(260.dp)
            .background(Brush.verticalGradient(listOf(Color.Transparent, Color.Black.copy(alpha = 0.9f)))),
    ) {
        Row(
            Modifier.align(Alignment.BottomStart).fillMaxWidth().padding(horizontal = 56.dp, vertical = 40.dp),
            horizontalArrangement = Arrangement.spacedBy(24.dp),
            verticalAlignment = Alignment.Bottom,
        ) {
            ChannelLogo(ch, state.api, Modifier.width(150.dp).height(84.dp).background(Color.White.copy(alpha = 0.08f), RoundedCornerShape(10.dp)))
            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        "LIVE", style = MaterialTheme.typography.labelMedium, color = Color.White,
                        modifier = Modifier.background(Wren.ember, RoundedCornerShape(4.dp)).padding(horizontal = 6.dp, vertical = 1.dp),
                    )
                    Text(
                        listOfNotNull(ch.number, ch.name).joinToString("  "), style = MaterialTheme.typography.titleMedium, color = Wren.secondary,
                        maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(start = 12.dp),
                    )
                }
                val on = ch.now
                Text(
                    on?.title ?: ch.name, style = MaterialTheme.typography.headlineMedium, color = Wren.text,
                    maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 6.dp),
                )
                if (on != null) {
                    Row(Modifier.padding(top = 10.dp), verticalAlignment = Alignment.CenterVertically) {
                        Text(timeOf(on.start), style = MaterialTheme.typography.labelMedium, color = Wren.secondary)
                        Bar(on.progress(now), Modifier.padding(horizontal = 12.dp).widthIn(max = 520.dp).weight(1f).height(5.dp))
                        Text(timeOf(on.end), style = MaterialTheme.typography.labelMedium, color = Wren.secondary)
                    }
                    on.description?.takeIf { it.isNotBlank() }?.let {
                        Text(it, style = MaterialTheme.typography.bodyMedium, color = Wren.secondary, maxLines = 2, overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.padding(top = 8.dp).widthIn(max = 900.dp))
                    }
                }
                ch.next?.let {
                    Text(
                        "Next  ${timeOf(it.start)}  ${it.title}", style = MaterialTheme.typography.bodySmall, color = Wren.tertiary,
                        maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 6.dp),
                    )
                }
            }
        }
    }
}

/** The channels down the left, over the picture; the playing one is focused. */
@Composable
private fun ChannelList(channels: List<LiveChannel>, current: String, onPick: (LiveChannel) -> Unit) {
    val state = LocalAppState.current
    val start = channels.indexOfFirst { it.id == current }.coerceAtLeast(0)
    val list = rememberLazyListState(initialFirstVisibleItemIndex = (start - 3).coerceAtLeast(0))
    val first = remember { FocusRequester() }
    LaunchedEffect(Unit) { awaitFrame(); runCatching { first.requestFocus() } }
    Row(Modifier.fillMaxSize()) {
        Box(Modifier.width(560.dp).fillMaxHeight().background(Color.Black.copy(alpha = 0.88f))) {
            LazyColumn(
                state = list,
                contentPadding = PaddingValues(horizontal = 24.dp, vertical = 40.dp),
                verticalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                itemsIndexed(channels, key = { _, ch -> ch.id }) { i, ch ->
                    Surface(
                        onClick = { onPick(ch) },
                        modifier = Modifier.fillMaxWidth().then(if (i == start) Modifier.focusRequester(first) else Modifier),
                        shape = ClickableSurfaceDefaults.shape(RoundedCornerShape(12.dp)),
                        colors = ClickableSurfaceDefaults.colors(
                            containerColor = if (ch.id == current) Color.White.copy(alpha = 0.12f) else Color.Transparent,
                            contentColor = Wren.text, focusedContainerColor = Color.White, focusedContentColor = Color.Black,
                        ),
                        scale = ClickableSurfaceDefaults.scale(focusedScale = 1.02f),
                    ) {
                        Row(
                            Modifier.fillMaxWidth().height(68.dp).padding(horizontal = 12.dp),
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(14.dp),
                        ) {
                            Text(ch.number.orEmpty(), style = MaterialTheme.typography.labelLarge, maxLines = 1,
                                color = LocalContentColor.current.copy(alpha = 0.6f), modifier = Modifier.width(40.dp))
                            ChannelLogo(ch, state.api, Modifier.width(72.dp).height(40.dp))
                            Column(Modifier.weight(1f)) {
                                Text(ch.name, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                ch.now?.let {
                                    Text("${it.title} · ${timeRange(it)}", style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis,
                                        color = LocalContentColor.current.copy(alpha = 0.65f))
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

private fun digit(key: Key): Char? = when (key) {
    Key.Zero, Key.NumPad0 -> '0'
    Key.One, Key.NumPad1 -> '1'
    Key.Two, Key.NumPad2 -> '2'
    Key.Three, Key.NumPad3 -> '3'
    Key.Four, Key.NumPad4 -> '4'
    Key.Five, Key.NumPad5 -> '5'
    Key.Six, Key.NumPad6 -> '6'
    Key.Seven, Key.NumPad7 -> '7'
    Key.Eight, Key.NumPad8 -> '8'
    Key.Nine, Key.NumPad9 -> '9'
    else -> null
}

@Composable
private fun Failed(message: String, retry: (() -> Unit)?, back: () -> Unit) {
    val fr = remember { FocusRequester() }
    LaunchedEffect(message) { awaitFrame(); runCatching { fr.requestFocus() } }
    Center {
        Text("Couldn't play this channel", style = MaterialTheme.typography.headlineSmall, color = Wren.text)
        Text(message, style = MaterialTheme.typography.bodyLarge, color = Wren.secondary, modifier = Modifier.widthIn(max = 640.dp).padding(top = 8.dp))
        Row(Modifier.padding(top = 24.dp), horizontalArrangement = Arrangement.spacedBy(14.dp)) {
            if (retry != null) PillButton("Try Again", retry, Modifier.focusRequester(fr), icon = Icons.Rounded.Refresh, primary = true)
            PillButton("Back", back, if (retry == null) Modifier.focusRequester(fr) else Modifier)
        }
    }
}

@Composable
private fun Center(content: @Composable () -> Unit) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) { content() }
    }
}
