@file:OptIn(androidx.compose.foundation.ExperimentalFoundationApi::class)

package com.wrentv.tv.ui

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.gestures.BringIntoViewSpec
import androidx.compose.foundation.gestures.LocalBringIntoViewSpec
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.MutableIntState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import coil3.compose.AsyncImage
import com.wrentv.tv.ui.screens.HeroEntry
import com.wrentv.tv.ui.screens.MetaLine

/*
 * Home, libraries, hubs and collections share one cinematic layout: the
 * focused title's details in a hero at the top, over its artwork, and rows
 * of titles below. The hero is not part of the list; the list scrolls so
 * the focused row always settles on the same line, just under the hero,
 * which stays the same size whichever row is focused.
 */

/** The hero's height, as a share of the screen. */
private const val HeroHeight = 0.48f

/** From a shelf's top to its cards: the title line and the row's own padding. */
private val ShelfHeader = 48.dp

/** How much bigger or smaller than usual posters are, from the Poster Size setting. */
val posterScale: Float
    @Composable @ReadOnlyComposable get() = when (LocalAppState.current.posterSize) {
        "small" -> 0.85f
        "large" -> 1.25f
        else -> 1f
    }

/** Card sizes on cinematic pages: smaller than a grid's, so the hero has room. */
val CinematicPoster: Dp @Composable @ReadOnlyComposable get() = 130.dp * posterScale
val CinematicLandscape: Dp @Composable @ReadOnlyComposable get() = 240.dp * posterScale
private val PosterGap = 20.dp

/**
 * Scrolls a focused card so its row's top sits on the focus line. With
 * the first row (or the page's pills) focused, the page goes back to the top.
 */
private class FocusLine(val focusedRow: MutableIntState, val list: LazyListState, val headerPx: Float) : BringIntoViewSpec {
    override fun calculateScrollDistance(offset: Float, size: Float, containerSize: Float): Float {
        if (focusedRow.intValue <= 0) {
            return if (list.firstVisibleItemIndex == 0) -list.firstVisibleItemScrollOffset.toFloat() else -containerSize * 4
        }
        return offset - (HeroHeight * containerSize + headerPx)
    }
}

class CinematicScope internal constructor(
    val lazy: LazyListScope, private val focusedRow: MutableIntState, private val rowSpec: BringIntoViewSpec,
    /** How many posters fit across the page. */
    val posterColumns: Int,
) {
    private var next = 0

    /** A row of the page. Rows above the focused one fade away as they pass behind the hero. */
    fun row(key: Any, content: @Composable () -> Unit) {
        val index = next++
        lazy.item(key) {
            val alpha by animateFloatAsState(if (index < focusedRow.intValue) 0f else 1f, tween(280), label = "row")
            // Within a row, cards scroll the usual way.
            CompositionLocalProvider(LocalBringIntoViewSpec provides rowSpec) {
                Box(Modifier.graphicsLayer { this.alpha = alpha }.onFocusChanged { if (it.hasFocus) focusedRow.intValue = index }) { content() }
            }
        }
    }

    /**
     * Too many titles for one row to browse: a titled grid of posters,
     * each line a row of the page, so it scrolls and settles like the rest.
     */
    fun <T> grid(key: Any, title: String, subtitle: String?, entries: List<T>, id: (T) -> Any, card: @Composable (T) -> Unit) {
        entries.chunked(posterColumns).forEachIndexed { i, line ->
            row("$key:$i") {
                Column(Modifier.fillMaxWidth()) {
                    if (i == 0) ShelfTitle(title, subtitle)
                    Row(Modifier.padding(start = PageGutter, end = PageGutter, top = if (i == 0) 16.dp else 0.dp, bottom = 16.dp),
                        horizontalArrangement = Arrangement.spacedBy(PosterGap)) {
                        line.forEach { androidx.compose.runtime.key(id(it)) { card(it) } }
                    }
                }
            }
        }
    }

    /** Anything else on the page: loading, an empty state. */
    fun extra(key: Any, content: @Composable () -> Unit) = lazy.item(key) { content() }
}

/**
 * A cinematic page. [fallback] fills the hero until something takes focus
 * (a hub's or collection's own details); [pills] sit under the hero, above
 * the first row, for the page's own choices (All Movies, sort).
 */
@Composable
fun CinematicBrowse(
    list: LazyListState,
    fallback: HeroEntry? = null,
    label: String? = null,
    pills: (@Composable () -> Unit)? = null,
    content: CinematicScope.() -> Unit,
) {
    val ambient = LocalAmbient.current
    val routeKey = LocalRouteKey.current
    val nav = LocalNavigator.current
    val bar = LocalTopBar.current
    val focusedRow = rememberSaveable(saver = androidx.compose.runtime.saveable.Saver<MutableIntState, Int>({ it.intValue }, { mutableIntStateOf(it) })) { mutableIntStateOf(0) }
    val rowSpec = LocalBringIntoViewSpec.current
    val headerPx = with(LocalDensity.current) { ShelfHeader.toPx() }
    val spec = remember(list) { FocusLine(focusedRow, list, headerPx) }
    ReportScroll(list)
    // Up into the top bar takes the page back to its top.
    LaunchedEffect(bar) { snapshotFlow { bar.focused }.collect { if (it) focusedRow.intValue = 0 } }
    LaunchedEffect(fallback?.id) {
        if (fallback != null && ambient.route != routeKey) ambient.show(routeKey, fallback.backdrop, fallback)
    }
    val entry = ambient.entry.takeIf { ambient.route == routeKey } ?: fallback

    val poster = CinematicPoster
    BoxWithConstraints(
        Modifier.fillMaxSize().onPreviewKeyEvent { ev ->
            // Play on the remote plays the focused title.
            val item = entry?.item
            if (ev.type == KeyEventType.KeyDown && (ev.key == Key.MediaPlay || ev.key == Key.MediaPlayPause) && item != null && item.isPlayable) {
                nav.play(item.playTarget())
                true
            } else false
        },
    ) {
        val screen = maxHeight
        // The pills sit under the hero, above the first row.
        CinematicHero(entry, label, screen * HeroHeight - if (pills != null) 56.dp else 0.dp)
        CompositionLocalProvider(LocalBringIntoViewSpec provides spec) {
            LazyColumn(
                Modifier.fillMaxSize(), state = list,
                contentPadding = PaddingValues(bottom = screen * 0.4f),
                verticalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                item("cinematic:top") {
                    Box(Modifier.fillMaxWidth().height(screen * HeroHeight).onFocusChanged { if (it.hasFocus) focusedRow.intValue = 0 }) {
                        if (pills != null) {
                            val alpha by animateFloatAsState(if (focusedRow.intValue > 0) 0f else 1f, tween(280), label = "pills")
                            Row(
                                Modifier.align(Alignment.BottomStart).padding(start = PageGutter, bottom = 8.dp).graphicsLayer { this.alpha = alpha },
                                horizontalArrangement = Arrangement.spacedBy(10.dp), verticalAlignment = Alignment.CenterVertically,
                            ) {
                                CompositionLocalProvider(LocalBringIntoViewSpec provides rowSpec) { pills() }
                            }
                        }
                    }
                }
                val columns = ((maxWidth - PageGutter * 2 + PosterGap) / (poster + PosterGap)).toInt().coerceAtLeast(1)
                CinematicScope(this, focusedRow, rowSpec, columns).content()
            }
        }
    }
}

/** The focused title's name, details and summary, low on the hero so it sits just above the rows. */
@Composable
private fun CinematicHero(entry: HeroEntry?, label: String?, height: Dp) {
    val api = LocalAppState.current.api
    Box(Modifier.fillMaxWidth().height(height)) {
        AnimatedContent(entry, transitionSpec = { fadeIn(tween(160)) togetherWith fadeOut(tween(120)) }, contentKey = { it?.id }, label = "hero",
            modifier = Modifier.align(Alignment.BottomStart).padding(start = PageGutter, bottom = 10.dp).widthIn(max = 640.dp)) { e ->
            if (e == null) return@AnimatedContent
            Column {
                if (label != null) {
                    Text(label.uppercase(), style = MaterialTheme.typography.labelMedium, color = Wren.ember)
                    Spacer(Modifier.height(6.dp))
                }
                if (e.logo != null) {
                    AsyncImage(api?.imageUrl(e.logo, "w500"), e.title, Modifier.width(320.dp).height(84.dp),
                        contentScale = ContentScale.Fit, alignment = Alignment.BottomStart)
                } else {
                    Text(e.title, style = MaterialTheme.typography.headlineLarge.copy(fontWeight = FontWeight.Bold), color = Wren.text,
                        maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
                Spacer(Modifier.height(8.dp))
                MetaLine(e.meta, e.badges)
                e.overview?.takeIf { it.isNotEmpty() }?.let {
                    Spacer(Modifier.height(8.dp))
                    Text(it, style = MaterialTheme.typography.bodyMedium, color = Color.White.copy(alpha = 0.85f),
                        maxLines = 3, overflow = TextOverflow.Ellipsis)
                }
            }
        }
    }
}
