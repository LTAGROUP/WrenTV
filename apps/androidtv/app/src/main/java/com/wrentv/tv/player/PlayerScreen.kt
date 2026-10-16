@file:OptIn(UnstableApi::class)

package com.wrentv.tv.player

import android.view.ViewGroup
import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Audiotrack
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.Layers
import androidx.compose.material.icons.rounded.Pause
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material.icons.rounded.SkipNext
import androidx.compose.material.icons.rounded.Subtitles
import androidx.compose.material.icons.rounded.Toc
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
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
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
import androidx.media3.ui.CaptionStyleCompat
import androidx.media3.ui.PlayerView
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.wrentv.tv.ui.Bar
import com.wrentv.tv.ui.Choice
import com.wrentv.tv.ui.ChoicePanel
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.PillButton
import com.wrentv.tv.ui.PlayTarget
import com.wrentv.tv.ui.RemoteImage
import com.wrentv.tv.ui.RoundButton
import com.wrentv.tv.ui.Spinner
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.formatClock
import kotlinx.coroutines.android.awaitFrame
import kotlinx.coroutines.delay

private enum class PlayerPanel { Audio, Subtitles, Version, Chapters }

/**
 * Full-screen playback. With the controls hidden the remote works the
 * player directly: OK pauses, left and right skip 10 seconds, up and down
 * show the controls. Back hides the controls, then leaves.
 */
@Composable
fun PlayerScreen(target: PlayTarget) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val context = LocalContext.current
    val c = remember(target) { PlayerController(context, state.api, state.downloads, target, state.scope) }
    DisposableEffect(c) {
        c.start()
        onDispose { c.stop() }
    }
    LaunchedEffect(c.closeRequested) { if (c.closeRequested) nav.back() }

    var controls by remember { mutableStateOf(false) }
    var panel by remember { mutableStateOf<PlayerPanel?>(null) }
    // Bumped on every key press so the controls stay up while in use.
    var touched by remember { mutableIntStateOf(0) }
    val root = remember { FocusRequester() }
    val playButton = remember { FocusRequester() }

    fun show() { controls = true; touched++ }

    LaunchedEffect(controls, touched, c.paused, panel) {
        if (controls && panel == null && !c.paused) {
            delay(5000)
            controls = false
        }
    }
    LaunchedEffect(controls, c.phase, c.upNext) {
        awaitFrame()
        if (c.upNext != null || c.phase is PlayerController.Phase.Failed) return@LaunchedEffect
        runCatching { if (controls) playButton.requestFocus() else root.requestFocus() }
    }

    BackHandler {
        when {
            controls -> controls = false
            else -> nav.back()
        }
    }

    Box(
        Modifier.fillMaxSize().background(Color.Black)
            .onPreviewKeyEvent { if (it.type == KeyEventType.KeyDown) touched++; false }
            .focusRequester(root)
            .focusable()
            .onKeyEvent { e ->
                if (e.type != KeyEventType.KeyDown || controls || c.upNext != null) return@onKeyEvent false
                when (e.key) {
                    Key.DirectionCenter, Key.Enter, Key.NumPadEnter -> {
                        when (c.skip) {
                            PlayerController.Skip.Intro -> c.skipIntro()
                            PlayerController.Skip.Credits -> c.skipCredits()
                            null -> { c.togglePause(); show() }
                        }
                        true
                    }
                    Key.MediaPlayPause, Key.MediaPlay, Key.MediaPause -> { c.togglePause(); show(); true }
                    Key.DirectionLeft, Key.MediaRewind -> { c.seekBy(-10.0); true }
                    Key.DirectionRight, Key.MediaFastForward -> { c.seekBy(10.0); true }
                    Key.DirectionUp, Key.DirectionDown, Key.Menu -> { show(); true }
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
                    subtitleView?.setStyle(
                        CaptionStyleCompat(
                            android.graphics.Color.WHITE, android.graphics.Color.TRANSPARENT, android.graphics.Color.TRANSPARENT,
                            CaptionStyleCompat.EDGE_TYPE_DROP_SHADOW, android.graphics.Color.BLACK, null,
                        ),
                    )
                }
            },
            // Captions clear the overscan edge, and rise above the controls when they show.
            update = { it.subtitleView?.setBottomPaddingFraction(if (controls) 0.3f else 0.08f) },
            modifier = Modifier.fillMaxSize(),
        )

        when (val phase = c.phase) {
            PlayerController.Phase.Loading -> Center { Spinner() }
            is PlayerController.Phase.Pending -> Center {
                Spinner()
                Spacer(Modifier.height(20.dp))
                Text(c.title, style = MaterialTheme.typography.headlineSmall, color = Wren.text)
                Text(phase.message, style = MaterialTheme.typography.bodyLarge, color = Wren.secondary)
            }
            is PlayerController.Phase.Failed -> Failed(phase.message, retry = { c.retry() }, back = { nav.back() })
            else -> if (c.buffering) Center { Spinner() }
        }

        // The skip pill shows on its own while the controls are hidden; OK takes it.
        val skip = c.skip
        AnimatedVisibility(skip != null && !controls && c.upNext == null, Modifier.align(Alignment.BottomEnd).padding(48.dp), enter = fadeIn(), exit = fadeOut()) {
            SkipPill(skip, c.countdown, hint = true)
        }

        AnimatedVisibility(controls && c.upNext == null, enter = fadeIn(), exit = fadeOut()) {
            Controls(c, playButton, onPanel = { panel = it }, onSkip = {
                when (c.skip) {
                    PlayerController.Skip.Intro -> c.skipIntro()
                    PlayerController.Skip.Credits -> c.skipCredits()
                    null -> {}
                }
            })
        }

        c.upNext?.let { UpNext(c, back = { nav.back() }) }
    }

    when (panel) {
        null -> {}
        PlayerPanel.Audio -> ChoicePanel("Audio", c.audioOptions.map { Choice(it.key, it.label) }, c.audioKey, { panel = null }) {
            c.selectAudio(it)
        }
        PlayerPanel.Subtitles -> ChoicePanel(
            "Subtitles",
            listOf(Choice<String?>(null, "Off")) + c.subtitleOptions.map { Choice<String?>(it.key, it.label) },
            c.subtitleKey, { panel = null },
        ) { c.selectSubtitle(it) }
        PlayerPanel.Version -> ChoicePanel("Version", c.versions.map { Choice(it.id, it.label.ifEmpty { if (it.height > 0) "${it.height}p" else "Version" }) }, c.fileId, { panel = null }) {
            c.selectVersion(it)
        }
        PlayerPanel.Chapters -> ChoicePanel(
            "Chapters",
            c.chapters.mapIndexed { i, ch -> Choice(i, ch.title.ifEmpty { "Chapter ${i + 1}" }, formatClock(ch.start)) },
            c.chapters.indexOf(c.currentChapter).takeIf { it >= 0 }, { panel = null },
        ) { c.seekTo(c.chapters[it].start) }
    }
}

@Composable
private fun Controls(c: PlayerController, playButton: FocusRequester, onPanel: (PlayerPanel) -> Unit, onSkip: () -> Unit) {
    Box(Modifier.fillMaxSize()) {
        // Scrims keep the text readable over bright scenes.
        Box(Modifier.fillMaxWidth().height(200.dp).background(Brush.verticalGradient(listOf(Color.Black.copy(alpha = 0.75f), Color.Transparent))))
        Box(
            Modifier.align(Alignment.BottomCenter).fillMaxWidth().height(300.dp)
                .background(Brush.verticalGradient(listOf(Color.Transparent, Color.Black.copy(alpha = 0.85f)))),
        )
        Column(Modifier.padding(horizontal = 56.dp, vertical = 36.dp)) {
            Text(c.title, style = MaterialTheme.typography.headlineMedium, color = Wren.text, maxLines = 1, overflow = TextOverflow.Ellipsis)
            c.subtitle?.let { Text(it, style = MaterialTheme.typography.titleMedium, color = Wren.secondary, maxLines = 1) }
            if (c.methodLabel.isNotEmpty()) {
                Text(c.methodLabel, style = MaterialTheme.typography.bodySmall, color = Wren.tertiary, modifier = Modifier.padding(top = 6.dp))
            }
        }
        Column(Modifier.align(Alignment.BottomCenter).fillMaxWidth().padding(horizontal = 56.dp, vertical = 36.dp)) {
            if (c.skip != null) {
                Box(Modifier.fillMaxWidth().padding(bottom = 18.dp), contentAlignment = Alignment.CenterEnd) {
                    PillButton(skipText(c.skip, c.countdown), onSkip, icon = Icons.Rounded.SkipNext, primary = true)
                }
            }
            c.currentChapter?.title?.takeIf { it.isNotEmpty() && c.chapters.size > 1 }?.let {
                Text(it, style = MaterialTheme.typography.labelLarge, color = Wren.secondary, modifier = Modifier.padding(bottom = 8.dp))
            }
            SeekBar(c)
            Row(Modifier.fillMaxWidth().padding(top = 6.dp)) {
                Text(formatClock(c.position), style = MaterialTheme.typography.labelMedium, color = Wren.secondary)
                Spacer(Modifier.weight(1f))
                Text(if (c.duration > 0) "-" + formatClock(c.duration - c.position) else "", style = MaterialTheme.typography.labelMedium, color = Wren.secondary)
            }
            Row(Modifier.padding(top = 18.dp), horizontalArrangement = Arrangement.spacedBy(14.dp), verticalAlignment = Alignment.CenterVertically) {
                RoundButton(if (c.paused) Icons.Rounded.PlayArrow else Icons.Rounded.Pause, if (c.paused) "Play" else "Pause", { c.togglePause() }, Modifier.focusRequester(playButton))
                if (c.audioOptions.size > 1) PillButton("Audio", { onPanel(PlayerPanel.Audio) }, icon = Icons.Rounded.Audiotrack)
                if (c.subtitleOptions.isNotEmpty()) PillButton("Subtitles", { onPanel(PlayerPanel.Subtitles) }, icon = Icons.Rounded.Subtitles)
                if (c.versions.size > 1) PillButton("Version", { onPanel(PlayerPanel.Version) }, icon = Icons.Rounded.Layers)
                if (c.chapters.size > 1) PillButton("Chapters", { onPanel(PlayerPanel.Chapters) }, icon = Icons.Rounded.Toc)
            }
        }
    }
}

/** The timeline. Focused, left and right skip 10 seconds. */
@Composable
private fun SeekBar(c: PlayerController) {
    var focused by remember { mutableStateOf(false) }
    val fraction = if (c.duration > 0) (c.position / c.duration).toFloat() else 0f
    Box(
        Modifier.fillMaxWidth().height(24.dp)
            .onFocusChanged { focused = it.isFocused }
            .onKeyEvent { e ->
                if (e.type != KeyEventType.KeyDown) return@onKeyEvent false
                when (e.key) {
                    Key.DirectionLeft -> { c.seekBy(-10.0); true }
                    Key.DirectionRight -> { c.seekBy(10.0); true }
                    Key.DirectionCenter, Key.Enter -> { c.togglePause(); true }
                    else -> false
                }
            }
            .focusable(),
        contentAlignment = Alignment.Center,
    ) {
        Bar(fraction, Modifier.fillMaxWidth().height(if (focused) 10.dp else 5.dp), color = if (focused) Color.White else Wren.ember)
    }
}

private fun skipText(skip: PlayerController.Skip?, countdown: Int?): String = when (skip) {
    PlayerController.Skip.Intro -> "Skip Intro"
    PlayerController.Skip.Credits -> if (countdown != null) "Next Episode in $countdown" else "Skip Credits"
    null -> ""
}

@Composable
private fun SkipPill(skip: PlayerController.Skip?, countdown: Int?, hint: Boolean) {
    Row(
        Modifier.background(Color.White, RoundedCornerShape(50)).padding(horizontal = 24.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(skipText(skip, countdown), style = MaterialTheme.typography.titleMedium, color = Color.Black)
        if (hint) Text("  ·  OK", style = MaterialTheme.typography.labelLarge, color = Color.Black.copy(alpha = 0.55f))
    }
}

@Composable
private fun UpNext(c: PlayerController, back: () -> Unit) {
    val next = c.upNext ?: return
    val state = LocalAppState.current
    val play = remember { FocusRequester() }
    LaunchedEffect(next.id) { awaitFrame(); runCatching { play.requestFocus() } }
    Box(Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.7f))) {
        Row(
            Modifier.align(Alignment.BottomStart).padding(56.dp),
            horizontalArrangement = Arrangement.spacedBy(28.dp),
            verticalAlignment = Alignment.Bottom,
        ) {
            RemoteImage(
                state.api?.imageUrl(next.stillPath ?: next.backdropPath, "w780"),
                Modifier.width(360.dp).height(202.dp).background(Wren.surface, RoundedCornerShape(14.dp)),
            )
            Column(Modifier.widthIn(max = 480.dp)) {
                Text("UP NEXT", style = MaterialTheme.typography.labelLarge, color = Wren.ember)
                Text(next.episodeLabel, style = MaterialTheme.typography.titleMedium, color = Wren.secondary, modifier = Modifier.padding(top = 6.dp))
                Text(next.title, style = MaterialTheme.typography.headlineSmall, color = Wren.text, maxLines = 2, overflow = TextOverflow.Ellipsis)
                next.overview?.let { Text(it, style = MaterialTheme.typography.bodyMedium, color = Wren.secondary, maxLines = 3, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 6.dp)) }
                Row(Modifier.padding(top = 20.dp), horizontalArrangement = Arrangement.spacedBy(14.dp)) {
                    PillButton("Play Next", { c.playNext() }, Modifier.focusRequester(play), icon = Icons.Rounded.PlayArrow, primary = true)
                    PillButton("Close", back, icon = Icons.Rounded.Close)
                }
            }
        }
    }
}

@Composable
private fun Failed(message: String, retry: () -> Unit, back: () -> Unit) {
    val fr = remember { FocusRequester() }
    LaunchedEffect(message) { awaitFrame(); runCatching { fr.requestFocus() } }
    Center {
        Text("Couldn't play this", style = MaterialTheme.typography.headlineSmall, color = Wren.text)
        Text(message, style = MaterialTheme.typography.bodyLarge, color = Wren.secondary, modifier = Modifier.widthIn(max = 640.dp).padding(top = 8.dp))
        Row(Modifier.padding(top = 24.dp), horizontalArrangement = Arrangement.spacedBy(14.dp)) {
            PillButton("Try Again", retry, Modifier.focusRequester(fr), icon = Icons.Rounded.Refresh, primary = true)
            PillButton("Back", back)
        }
    }
}

@Composable
private fun Center(content: @Composable () -> Unit) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) { content() }
    }
}
