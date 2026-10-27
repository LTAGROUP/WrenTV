@file:OptIn(UnstableApi::class)

package com.wrentv.tv.player

import android.view.ViewGroup
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.tv.material3.Surface
import androidx.tv.material3.Icon
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.draw.alpha
import androidx.compose.material.icons.rounded.Forward10
import androidx.compose.material.icons.rounded.Replay10
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.size
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.layout.layout
import androidx.compose.ui.unit.Constraints
import coil3.compose.AsyncImage
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
import androidx.compose.material.icons.rounded.Info
import androidx.compose.material.icons.rounded.KeyboardArrowDown
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.animation.animateContentSize
import androidx.compose.animation.core.animateDpAsState
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.lazy.rememberLazyListState
import com.wrentv.tv.ui.ArtCard
import com.wrentv.tv.ui.ProgressStrip
import androidx.compose.material.icons.rounded.Layers
import androidx.compose.material.icons.rounded.Pause
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material.icons.rounded.SkipNext
import androidx.compose.material.icons.rounded.Subtitles
import androidx.compose.material.icons.rounded.Toc
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import com.wrentv.tv.ui.UiSounds
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
import com.wrentv.tv.ui.Choice
import com.wrentv.tv.ui.ChoicePanel
import com.wrentv.tv.ui.ListRow
import com.wrentv.tv.ui.Panel
import com.wrentv.tv.data.Item
import com.wrentv.tv.data.OnlineSubtitle
import com.wrentv.tv.data.languageName
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.runtime.rememberCoroutineScope
import kotlinx.coroutines.launch
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.PillButton
import com.wrentv.tv.ui.PlayTarget
import com.wrentv.tv.ui.RemoteImage
import com.wrentv.tv.ui.Spinner
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.formatClock
import kotlinx.coroutines.android.awaitFrame
import kotlinx.coroutines.delay

private enum class PlayerPanel { Audio, Subtitles, OnlineSubtitles, Version, Chapters }

/** The Subtitles panel's "Search online" row. */
private const val SEARCH_ONLINE = "online"

/**
 * Full-screen playback. With the controls hidden the remote works the
 * player directly: OK shows the controls on Play (so a second OK pauses),
 * left and right skip 10 seconds, up and down show the controls. Down from the timeline opens the season's episodes
 * beneath it. Back hides the controls, then leaves.
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
    // Play film at a matching refresh rate; give the TV back its own mode on the way out.
    LaunchedEffect(c.frameRate) { DisplayMode.apply(context, c.frameRate) }
    DisposableEffect(Unit) { onDispose { DisplayMode.apply(context, 0f) } }
    DisposableEffect(Unit) {
        UiSounds.muted = true
        ThemeMusic.stop()
        onDispose { UiSounds.muted = false }
    }
    LaunchedEffect(c.closeRequested) { if (c.closeRequested) nav.back() }

    var controls by remember { mutableStateOf(false) }
    var panel by remember { mutableStateOf<PlayerPanel?>(null) }
    var stats by remember { mutableStateOf(false) }
    // The row of episodes under the timeline.
    var shelf by remember { mutableStateOf(false) }
    // Bumped on every key press so the controls stay up while in use.
    var touched by remember { mutableIntStateOf(0) }
    val root = remember { FocusRequester() }
    val playButton = remember { FocusRequester() }
    val seekBar = remember { FocusRequester() }
    // Controls opened by left/right land on the seek bar instead of Play.
    var toSeek by remember { mutableStateOf(false) }
    // The OK that opened the controls: its release would click whatever
    // button focus just moved to (TV buttons click on release), so it's eaten.
    var eatOkUp by remember { mutableStateOf(false) }

    fun show() { controls = true; touched++ }

    // The jump happens a moment after the last press, so holding left or right
    // moves the preview rather than stuttering through every point on the way.
    LaunchedEffect(c.scrub) {
        if (c.scrub == null) return@LaunchedEffect
        delay(800)
        c.commitScrub()
    }

    // Resuming hides the controls sooner; any later press goes back to the usual wait.
    // The press clears it before the resume sets it, so these two stay in this order.
    var resumed by remember { mutableStateOf(false) }
    var wasPaused by remember { mutableStateOf(c.paused) }
    LaunchedEffect(touched) { resumed = false }
    LaunchedEffect(c.paused) {
        if (wasPaused && !c.paused) resumed = true
        wasPaused = c.paused
    }

    LaunchedEffect(controls, touched, c.paused, panel, shelf, resumed) {
        if (controls && panel == null && !shelf && !c.paused) {
            delay(if (resumed) 3000 else 5000)
            controls = false
        }
    }
    LaunchedEffect(controls) { if (!controls) shelf = false }
    // Closing the row hands focus back to the timeline.
    LaunchedEffect(shelf) {
        if (shelf || !controls) return@LaunchedEffect
        awaitFrame()
        runCatching { seekBar.requestFocus() }
    }
    LaunchedEffect(controls, c.phase, c.upNext) {
        awaitFrame()
        if (c.upNext != null || c.phase is PlayerController.Phase.Failed) return@LaunchedEffect
        runCatching { if (controls) (if (toSeek) seekBar else playButton).requestFocus() else root.requestFocus() }
        toSeek = false
    }

    BackHandler {
        when {
            c.scrub != null -> c.cancelScrub()
            shelf -> shelf = false
            controls -> controls = false
            else -> nav.back()
        }
    }

    Box(
        Modifier.fillMaxSize().background(Color.Black)
            .onPreviewKeyEvent { e ->
                if (e.type == KeyEventType.KeyUp && e.key.isOk()) {
                    val eat = eatOkUp
                    eatOkUp = false
                    return@onPreviewKeyEvent eat
                }
                if (e.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                touched++
                // The remote's play/pause works whatever has focus, controls up or not.
                val media = when (e.key) {
                    Key.MediaPlayPause -> true
                    Key.MediaPlay -> c.paused
                    Key.MediaPause -> !c.paused
                    else -> return@onPreviewKeyEvent false
                }
                if (c.upNext != null) return@onPreviewKeyEvent false
                if (media) c.togglePause()
                show()
                true
            }
            .focusRequester(root)
            .focusable()
            .onKeyEvent { e ->
                if (e.type != KeyEventType.KeyDown || controls || c.upNext != null) return@onKeyEvent false
                when (e.key) {
                    Key.DirectionCenter, Key.Enter, Key.NumPadEnter -> {
                        eatOkUp = true
                        when (c.skip) {
                            PlayerController.Skip.Intro -> c.skipIntro()
                            PlayerController.Skip.Credits -> c.skipCredits()
                            // Opens the controls on Play; a second OK pauses.
                            null -> show()
                        }
                        true
                    }
                    Key.DirectionLeft, Key.MediaRewind, Key.DirectionRight, Key.MediaFastForward -> {
                        val dir = if (e.key == Key.DirectionLeft || e.key == Key.MediaRewind) -1 else 1
                        c.scrubBy(dir, e.nativeKeyEvent.repeatCount)
                        toSeek = true
                        show()
                        true
                    }
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
            update = {
                it.subtitleView?.setBottomPaddingFraction(if (controls) 0.3f else 0.08f)
                // A subtitle-site download is drawn here; clear it once when it goes.
                if (c.sideShowing || it.tag == true) {
                    it.subtitleView?.setCues(SideSubtitle.cues(c.sideCue))
                    it.tag = c.sideShowing
                }
            },
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
            Controls(c, playButton, seekBar, shelf, onShelf = { shelf = it && c.episode != null }, onPanel = { panel = it }, onStats = { stats = !stats }, onSkip = {
                when (c.skip) {
                    PlayerController.Skip.Intro -> c.skipIntro()
                    PlayerController.Skip.Credits -> c.skipCredits()
                    null -> {}
                }
            })
        }

        if (stats) Stats(c, Modifier.align(Alignment.TopEnd).padding(top = 32.dp, end = 48.dp))

        c.upNext?.let { UpNext(c, back = { nav.back() }) }
    }

    when (panel) {
        null -> {}
        PlayerPanel.Audio -> ChoicePanel("Audio", c.audioOptions.map { Choice(it.key, it.label) }, c.audioKey, { panel = null }) {
            c.selectAudio(it)
        }
        PlayerPanel.Subtitles -> ChoicePanel(
            "Subtitles",
            listOf(Choice<String?>(null, "Off")) + c.subtitleOptions.map { Choice<String?>(it.key, it.label) } +
                (if (c.onlineSubtitles) listOf(Choice<String?>(SEARCH_ONLINE, "Search online…", "OpenSubtitles and other sites")) else emptyList()),
            c.subtitleKey,
            // Picking "Search online" opens the next panel before this one closes.
            { if (panel == PlayerPanel.Subtitles) panel = null },
        ) { if (it == SEARCH_ONLINE) panel = PlayerPanel.OnlineSubtitles else c.selectSubtitle(it) }
        PlayerPanel.OnlineSubtitles -> OnlineSubtitlesPanel(c) { panel = null }
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
private fun Controls(
    c: PlayerController, playButton: FocusRequester, seekBar: FocusRequester, shelf: Boolean, onShelf: (Boolean) -> Unit,
    onPanel: (PlayerPanel) -> Unit, onStats: () -> Unit, onSkip: () -> Unit,
) {
    Box(Modifier.fillMaxSize()) {
        // Light scrims: just enough for the title and panel over bright scenes.
        Box(Modifier.fillMaxWidth().height(160.dp).background(Brush.verticalGradient(listOf(Color.Black.copy(alpha = 0.6f), Color.Transparent))))
        val scrim by animateDpAsState(if (shelf) 480.dp else 220.dp, label = "scrim")
        Box(
            Modifier.align(Alignment.BottomCenter).fillMaxWidth().height(scrim)
                .background(Brush.verticalGradient(listOf(Color.Transparent, Color.Black.copy(alpha = 0.55f)))),
        )
        Column(Modifier.padding(horizontal = 56.dp, vertical = 32.dp)) {
            Text(c.title, style = MaterialTheme.typography.titleLarge, color = Wren.text, maxLines = 1, overflow = TextOverflow.Ellipsis)
            c.subtitle?.let { Text(it, style = MaterialTheme.typography.bodyLarge, color = Wren.secondary, maxLines = 1) }
            if (c.methodLabel.isNotEmpty()) {
                Text(c.methodLabel, style = MaterialTheme.typography.labelSmall, color = Wren.tertiary, modifier = Modifier.padding(top = 4.dp))
            }
        }
        // animateContentSize clips, so the top padding inside it leaves room for the button tips above the panel.
        Column(Modifier.align(Alignment.BottomCenter).fillMaxWidth().padding(bottom = 28.dp).animateContentSize().padding(top = 36.dp), horizontalAlignment = Alignment.CenterHorizontally) {
            if (c.skip != null) {
                Box(Modifier.width(PanelWidth).padding(bottom = 14.dp), contentAlignment = Alignment.CenterEnd) {
                    PillButton(skipText(c.skip, c.countdown), onSkip, icon = Icons.Rounded.SkipNext, primary = true)
                }
            }
            // One floating panel, like the Mac player's: the buttons, then the timeline.
            Column(
                Modifier.width(PanelWidth)
                    .background(Color(0xFF141418).copy(alpha = 0.68f), RoundedCornerShape(20.dp))
                    .border(1.dp, Color.White.copy(alpha = 0.08f), RoundedCornerShape(20.dp))
                    .padding(start = 20.dp, end = 20.dp, top = PanelTop, bottom = 14.dp),
            ) {
                Box(Modifier.fillMaxWidth().height(ButtonRow)) {
                    c.currentChapter?.title?.takeIf { it.isNotEmpty() && c.chapters.size > 1 }?.let {
                        Text(it, style = MaterialTheme.typography.labelMedium, color = Wren.secondary, maxLines = 1, overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.align(Alignment.CenterStart).widthIn(max = 190.dp))
                    }
                    Row(Modifier.align(Alignment.Center), horizontalArrangement = Arrangement.spacedBy(18.dp), verticalAlignment = Alignment.CenterVertically) {
                        PlayerButton(Icons.Rounded.Replay10, "Back 10 seconds", { c.seekBy(-10.0) })
                        PlayerButton(if (c.paused) Icons.Rounded.PlayArrow else Icons.Rounded.Pause, if (c.paused) "Play" else "Pause", { c.togglePause() },
                            Modifier.focusRequester(playButton), big = true)
                        PlayerButton(Icons.Rounded.Forward10, "Forward 10 seconds", { c.seekBy(10.0) })
                    }
                    // Smaller than the transport, so it stays the centre of the panel.
                    Row(Modifier.align(Alignment.CenterEnd), horizontalArrangement = Arrangement.spacedBy(4.dp), verticalAlignment = Alignment.CenterVertically) {
                        if (c.audioOptions.size > 1) PlayerButton(Icons.Rounded.Audiotrack, "Audio", { onPanel(PlayerPanel.Audio) }, small = true)
                        if (c.subtitleOptions.isNotEmpty() || c.onlineSubtitles) PlayerButton(Icons.Rounded.Subtitles, "Subtitles", { onPanel(PlayerPanel.Subtitles) }, small = true)
                        if (c.versions.size > 1) PlayerButton(Icons.Rounded.Layers, "Version", { c.refreshVersions(); onPanel(PlayerPanel.Version) }, small = true)
                        if (c.chapters.size > 1) PlayerButton(Icons.Rounded.Toc, "Chapters", { onPanel(PlayerPanel.Chapters) }, small = true)
                        PlayerButton(Icons.Rounded.Info, "Stats", onStats, small = true)
                    }
                }
                Row(Modifier.fillMaxWidth().padding(top = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                    val at = c.scrub
                    Text(formatClock(at ?: c.position), style = MaterialTheme.typography.labelMedium, color = if (at != null) Wren.text else Wren.secondary,
                        modifier = Modifier.widthIn(min = 56.dp))
                    Box(Modifier.weight(1f).padding(horizontal = 14.dp)) { SeekBar(c, Modifier.focusRequester(seekBar), onDown = { onShelf(true) }) }
                    Text(if (c.duration > 0) "-" + formatClock(c.duration - (at ?: c.position)) else "", style = MaterialTheme.typography.labelMedium, color = Wren.secondary,
                        textAlign = TextAlign.End, modifier = Modifier.widthIn(min = 56.dp))
                }
            }
            if (c.episode != null) {
                if (shelf) {
                    EpisodeShelf(c, onClose = { onShelf(false) })
                } else {
                    Row(Modifier.padding(top = 10.dp).alpha(0.7f), verticalAlignment = Alignment.CenterVertically) {
                        Icon(Icons.Rounded.KeyboardArrowDown, null, Modifier.size(18.dp), tint = Wren.secondary)
                        Text("Episodes", style = MaterialTheme.typography.labelMedium, color = Wren.secondary)
                    }
                }
            }
        }
    }
}

private val PanelWidth = 720.dp

/** The panel's button row and the padding above it, which place the button tips. */
private val ButtonRow = 54.dp
private val PanelTop = 10.dp

/** How the title is playing, refreshed every second while it shows. */
@Composable
private fun Stats(c: PlayerController, modifier: Modifier = Modifier) {
    var rows by remember { mutableStateOf(c.stats()) }
    LaunchedEffect(c) {
        while (true) {
            rows = c.stats()
            delay(1000)
        }
    }
    Column(
        modifier.widthIn(max = 520.dp).background(Color.Black.copy(alpha = 0.72f), RoundedCornerShape(14.dp)).padding(horizontal = 18.dp, vertical = 12.dp),
        verticalArrangement = Arrangement.spacedBy(3.dp),
    ) {
        rows.forEach { (k, v) ->
            Row {
                Text(k, style = MaterialTheme.typography.labelMedium, color = Wren.tertiary, modifier = Modifier.width(120.dp))
                Text(v, style = MaterialTheme.typography.labelMedium, color = Wren.text)
            }
        }
    }
}

/** Searches the subtitle sites and adds the one picked to what's playing. */
@Composable
private fun OnlineSubtitlesPanel(c: PlayerController, onDismiss: () -> Unit) {
    val scope = rememberCoroutineScope()
    var results by remember { mutableStateOf<List<OnlineSubtitle>?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var adding by remember { mutableStateOf<OnlineSubtitle?>(null) }
    val first = remember { FocusRequester() }
    LaunchedEffect(Unit) {
        results = try {
            c.searchOnlineSubtitles()
        } catch (e: Exception) {
            if (e is kotlinx.coroutines.CancellationException) throw e
            error = e.message ?: "The search failed."
            emptyList()
        }
        awaitFrame()
        runCatching { first.requestFocus() }
    }
    Panel(onDismiss, "Search online", width = 720.dp) {
        val rs = results
        when {
            rs == null || adding != null -> Box(Modifier.fillMaxWidth().padding(vertical = 40.dp), contentAlignment = Alignment.Center) { Spinner() }
            error != null && rs.isEmpty() -> Text(error ?: "", style = MaterialTheme.typography.bodyLarge, color = Wren.secondary)
            rs.isEmpty() -> Text("No subtitles found for this title.", style = MaterialTheme.typography.bodyLarge, color = Wren.secondary)
            else -> {
                error?.let { Text(it, style = MaterialTheme.typography.bodyMedium, color = Wren.ember, modifier = Modifier.padding(bottom = 10.dp)) }
                LazyColumn(verticalArrangement = Arrangement.spacedBy(4.dp), modifier = Modifier.fillMaxWidth()) {
                    itemsIndexed(rs) { i, r ->
                        ListRow(
                            title = r.name.ifEmpty { r.provider },
                            detail = listOfNotNull(if (r.matchesFile) "Matches your file" else null, languageName(r.language) ?: r.language.uppercase(), r.provider,
                                if (r.hearingImpaired) "SDH" else null, if (r.machineTranslated) "Machine translated" else null,
                                if (r.downloads > 0) "${r.downloads} downloads" else null).joinToString(" · "),
                            onClick = {
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
                            },
                            modifier = if (i == 0) Modifier.focusRequester(first) else Modifier,
                        )
                    }
                }
            }
        }
    }
}

/**
 * The season's episodes in a row under the timeline, opened with down. Up
 * goes to the seasons, then back to the timeline, which closes the row.
 */
@Composable
private fun EpisodeShelf(c: PlayerController, onClose: () -> Unit) {
    val ep = c.episode ?: return
    val api = LocalAppState.current.api
    var seasons by remember { mutableStateOf<List<Item>>(emptyList()) }
    var seasonId by remember { mutableStateOf(ep.parentId) }
    var episodes by remember { mutableStateOf<List<Item>?>(null) }
    val current = remember { FocusRequester() }
    val list = rememberLazyListState()
    // Closes once focus has been in the row and left it.
    var entered by remember { mutableStateOf(false) }
    LaunchedEffect(ep.showId) {
        seasons = ep.showId?.let { runCatching { c.children(it) }.getOrNull() }.orEmpty().sortedBy { it.seasonNumber }
    }
    LaunchedEffect(seasonId) {
        val fresh = seasonId?.let { runCatching { c.children(it) }.getOrNull() }.orEmpty().sortedBy { it.episodeNumber }
        val first = episodes == null
        episodes = fresh
        // Opening lands on what's playing; switching season stays on the season chips.
        if (first) {
            list.scrollToItem(fresh.indexOfFirst { it.id == c.itemId }.coerceAtLeast(0))
            awaitFrame()
            runCatching { current.requestFocus() }
        }
    }
    Column(
        Modifier.fillMaxWidth().padding(top = 18.dp)
            .onFocusChanged { if (it.hasFocus) entered = true else if (entered) onClose() },
    ) {
        if (seasons.size > 1) {
            LazyRow(horizontalArrangement = Arrangement.spacedBy(10.dp), contentPadding = PaddingValues(horizontal = 56.dp), modifier = Modifier.padding(bottom = 14.dp)) {
                items(seasons, key = { it.id }) { s -> PillButton(s.displayTitle, { seasonId = s.id }, primary = s.id == seasonId) }
            }
        }
        val eps = episodes
        if (eps == null) {
            Box(Modifier.fillMaxWidth().height(EpisodeCardHeight), contentAlignment = Alignment.Center) { Spinner() }
            return@Column
        }
        val focusAt = eps.indexOfFirst { it.id == c.itemId }.coerceAtLeast(0)
        LazyRow(state = list, horizontalArrangement = Arrangement.spacedBy(18.dp), contentPadding = PaddingValues(horizontal = 56.dp, vertical = 8.dp)) {
            itemsIndexed(eps, key = { _, e -> e.id }) { i, e ->
                ShelfEpisode(api?.imageUrl(e.stillPath ?: e.backdropPath, "w300"), e, playing = e.id == c.itemId,
                    onClick = { if (e.id != c.itemId) c.playEpisode(e); onClose() },
                    modifier = if (i == focusAt) Modifier.focusRequester(current) else Modifier)
            }
        }
    }
}

private val EpisodeCardWidth = 260.dp
private val EpisodeCardHeight = 210.dp

@Composable
private fun ShelfEpisode(image: String?, e: Item, playing: Boolean, onClick: () -> Unit, modifier: Modifier = Modifier) {
    var focused by remember { mutableStateOf(false) }
    Column(Modifier.width(EpisodeCardWidth)) {
        ArtCard(onClick, modifier.width(EpisodeCardWidth).aspectRatio(16f / 9f).onFocusChanged { focused = it.hasFocus }) {
            RemoteImage(image, Modifier.fillMaxSize())
            e.progress?.takeIf { it.position > 0 && !it.watched && !playing }?.let { ProgressStrip(it.fraction, Modifier.align(Alignment.BottomCenter)) }
            if (playing) {
                Text("Playing", style = MaterialTheme.typography.labelSmall, color = Color.White,
                    modifier = Modifier.align(Alignment.TopStart).padding(8.dp).background(Wren.ember, RoundedCornerShape(6.dp)).padding(horizontal = 8.dp, vertical = 2.dp))
            } else if (e.isWatched) {
                Icon(Icons.Rounded.CheckCircle, "Watched", Modifier.align(Alignment.TopEnd).padding(8.dp).size(24.dp).background(Wren.ember, CircleShape), tint = Color.White)
            }
        }
        Column(Modifier.padding(top = 8.dp).alpha(if (focused) 1f else 0.75f)) {
            Text("${e.episodeNumber}. ${e.title}", style = MaterialTheme.typography.titleSmall, color = Wren.text, maxLines = 1, overflow = TextOverflow.Ellipsis)
            e.runtimeMin?.takeIf { it > 0 }?.let { Text("$it min", style = MaterialTheme.typography.bodySmall, color = Wren.secondary, maxLines = 1) }
        }
    }
}

/** A plain icon on the panel, white when focused, with its name in a tip above the panel. */
@Composable
private fun PlayerButton(icon: ImageVector, label: String, onClick: () -> Unit, modifier: Modifier = Modifier, big: Boolean = false, small: Boolean = false) {
    var focused by remember { mutableStateOf(false) }
    val tip by animateFloatAsState(if (focused) 1f else 0f, label = "tip")
    val size = if (big) 54.dp else if (small) 38.dp else 44.dp
    Box(Modifier.size(size)) {
        Surface(
            onClick = onClick,
            modifier = modifier.size(size).onFocusChanged { focused = it.isFocused },
            shape = ClickableSurfaceDefaults.shape(CircleShape),
            colors = ClickableSurfaceDefaults.colors(
                containerColor = Color.Transparent, contentColor = Wren.text,
                focusedContainerColor = Color.White, focusedContentColor = Color.Black,
            ),
            scale = ClickableSurfaceDefaults.scale(focusedScale = 1.08f),
        ) {
            Icon(icon, contentDescription = label, modifier = Modifier.align(Alignment.Center).size(if (big) 32.dp else if (small) 21.dp else 27.dp))
        }
        // Takes no room, so the buttons stay evenly spaced. Every tip's bottom sits the same
        // distance above the panel, whatever the button's size.
        val lift = (ButtonRow - size) / 2 + PanelTop + 8.dp
        Text(
            label, style = MaterialTheme.typography.labelMedium, color = Wren.text, maxLines = 1,
            modifier = Modifier
                .layout { m, c ->
                    val p = m.measure(Constraints())
                    layout(c.maxWidth, 0) { p.place((c.maxWidth - p.width) / 2, -lift.roundToPx() - p.height) }
                }
                .alpha(tip)
                .background(Color.Black.copy(alpha = 0.7f), RoundedCornerShape(8.dp)).padding(horizontal = 10.dp, vertical = 3.dp),
        )
    }
}

/**
 * The timeline. Focused, left and right move a preview of where you'll land
 * (10 seconds a tap, faster while held); OK jumps there at once.
 */
@Composable
private fun SeekBar(c: PlayerController, modifier: Modifier = Modifier, onDown: () -> Unit = {}) {
    var focused by remember { mutableStateOf(false) }
    val at = c.scrub
    val fraction = if (c.duration > 0) ((at ?: c.position) / c.duration).toFloat() else 0f
    BoxWithConstraints(Modifier.fillMaxWidth()) {
        val barWidth = maxWidth
        Box(
            modifier.fillMaxWidth().height(24.dp)
                .onFocusChanged { focused = it.isFocused }
                .onKeyEvent { e ->
                    if (e.type != KeyEventType.KeyDown) return@onKeyEvent false
                    when (e.key) {
                        Key.DirectionLeft, Key.MediaRewind -> { c.scrubBy(-1, e.nativeKeyEvent.repeatCount); true }
                        Key.DirectionRight, Key.MediaFastForward -> { c.scrubBy(1, e.nativeKeyEvent.repeatCount); true }
                        Key.DirectionCenter, Key.Enter -> { if (c.scrub != null) c.commitScrub() else c.togglePause(); true }
                        Key.DirectionDown -> { onDown(); true }
                        else -> false
                    }
                }
                .focusable(),
            contentAlignment = Alignment.Center,
        ) {
            // The track shows its full length, so the start of a film doesn't read as an empty gap.
            Box(Modifier.fillMaxWidth().height(if (focused) 6.dp else 4.dp).background(Color.White.copy(alpha = 0.22f), CircleShape)) {
                Box(Modifier.fillMaxHeight().fillMaxWidth(fraction.coerceIn(0f, 1f)).background(if (focused) Color.White else Wren.ember, CircleShape))
            }
            if (focused) {
                val x = (barWidth * fraction.coerceIn(0f, 1f) - 7.dp).coerceIn(0.dp, (barWidth - 14.dp).coerceAtLeast(0.dp))
                Box(Modifier.align(Alignment.CenterStart).offset(x = x).size(14.dp).background(Color.White, CircleShape))
            }
        }
        if (at != null) {
            // Rides above the bar at the scrub point, kept on screen at the ends.
            val w = PreviewWidth
            val x = (barWidth * fraction - w / 2).coerceIn(0.dp, (barWidth - w).coerceAtLeast(0.dp))
            // Takes no height, so the panel keeps its size: it hangs above the bar instead.
            Box(Modifier.offset(x = x).height(0.dp).wrapContentHeight(Alignment.Bottom, unbounded = true)) {
                // Clear of the panel above the bar (its top padding, the buttons and the gap), not over the buttons.
                ScrubPreview(c.thumbUrl(at), at, Modifier.padding(bottom = 10.dp + 54.dp + 6.dp + 12.dp))
            }
        }
    }
}

private val PreviewWidth = 256.dp
private val PreviewHeight = 144.dp

/** The frame at the scrub point. The last frame stays up until the next arrives, so holding a direction doesn't flicker. */
@Composable
private fun ScrubPreview(url: String?, seconds: Double, modifier: Modifier = Modifier) {
    var shown by remember { mutableStateOf<String?>(null) }
    Column(modifier.width(PreviewWidth), horizontalAlignment = Alignment.CenterHorizontally) {
        if (url != null) {
            Box(Modifier.size(PreviewWidth, PreviewHeight).clip(RoundedCornerShape(10.dp)).background(Color.Black)
                .border(2.dp, Color.White.copy(alpha = 0.9f), RoundedCornerShape(10.dp))) {
                AsyncImage(model = shown, contentDescription = null, contentScale = ContentScale.Fit, modifier = Modifier.fillMaxSize())
                AsyncImage(model = url, contentDescription = null, contentScale = ContentScale.Fit, modifier = Modifier.fillMaxSize(),
                    onSuccess = { shown = url })
            }
        }
        Text(formatClock(seconds), style = MaterialTheme.typography.titleMedium, color = Wren.text,
            modifier = Modifier.padding(top = 8.dp).background(Color.Black.copy(alpha = 0.6f), RoundedCornerShape(8.dp)).padding(horizontal = 10.dp, vertical = 3.dp))
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

private fun Key.isOk() = this == Key.DirectionCenter || this == Key.Enter || this == Key.NumPadEnter
