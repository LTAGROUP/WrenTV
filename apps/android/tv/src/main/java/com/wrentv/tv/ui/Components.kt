package com.wrentv.tv.ui

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.LazyListPrefetchStrategy
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.wrapContentSize
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.tv.material3.Border
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.Icon
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import coil3.compose.AsyncImage
import com.wrentv.tv.data.Item
import com.wrentv.tv.data.TitleView
import com.wrentv.tv.data.WatchState
import kotlinx.coroutines.android.awaitFrame
import kotlinx.coroutines.delay
import kotlin.math.roundToInt

// Notices

/** A short message along the bottom of the screen. */
class Notices {
    var text by mutableStateOf<String?>(null)
        private set
    private var serial = 0

    fun show(message: String?) {
        text = message
        serial++
    }

    @Composable
    fun Host() {
        val t = text ?: return
        val s = serial
        LaunchedEffect(s) {
            delay(4500)
            if (serial == s) text = null
        }
        Box(Modifier.fillMaxSize().padding(bottom = 48.dp), contentAlignment = Alignment.BottomCenter) {
            Text(
                t, style = MaterialTheme.typography.titleMedium, color = Wren.text,
                modifier = Modifier.background(Wren.surfaceHigh, RoundedCornerShape(14.dp)).padding(horizontal = 26.dp, vertical = 14.dp),
            )
        }
    }
}

val LocalNotices = compositionLocalOf { Notices() }

// Focus memory

/**
 * Remembers which element of a page was focused, and focuses it again when
 * the page comes back (after Back from a detail page, say). A page's
 * `default` element is focused when nothing was remembered.
 */
@Composable
fun Modifier.rememberFocus(key: String, default: Boolean = false): Modifier {
    val nav = LocalNavigator.current
    val route = LocalRouteKey.current
    val fr = remember { FocusRequester() }
    LaunchedEffect(Unit) {
        val saved = nav.focus[route]
        if (saved == key || (saved == null && default)) {
            awaitFrame()
            runCatching { fr.requestFocus() }
        }
    }
    return this.focusRequester(fr).onFocusChanged { if (it.isFocused) nav.focus[route] = key }
}

// Images

@Composable
fun RemoteImage(url: String?, modifier: Modifier = Modifier, contentScale: ContentScale = ContentScale.Crop, alignment: Alignment = Alignment.Center) {
    Box(modifier.background(Color.White.copy(alpha = 0.06f))) {
        if (url != null) {
            AsyncImage(model = url, contentDescription = null, contentScale = contentScale, alignment = alignment, modifier = Modifier.fillMaxSize())
        }
    }
}

// Buttons

/**
 * The TV app's capsule button: frosted glass at rest, white with dark text
 * when focused. `primary` is white at rest too, like Play.
 */
@Composable
fun PillButton(
    text: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    icon: ImageVector? = null,
    primary: Boolean = false,
    enabled: Boolean = true,
    onLongClick: (() -> Unit)? = null,
) {
    Surface(
        onClick = onClick,
        onLongClick = onLongClick,
        enabled = enabled,
        modifier = modifier.height(48.dp),
        shape = ClickableSurfaceDefaults.shape(CircleShape),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = if (primary) Wren.cream.copy(alpha = 0.92f) else Wren.glass,
            contentColor = if (primary) Color.Black else Wren.text,
            focusedContainerColor = Color.White,
            focusedContentColor = Color.Black,
            pressedContainerColor = Color.White.copy(alpha = 0.8f),
            pressedContentColor = Color.Black,
            disabledContainerColor = Wren.glass.copy(alpha = 0.1f),
            disabledContentColor = Wren.tertiary,
        ),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1.06f),
    ) {
        Row(
            Modifier.fillMaxHeight().padding(horizontal = if (text.isEmpty()) 12.dp else 24.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            if (icon != null) Icon(icon, contentDescription = text.ifEmpty { null }, modifier = Modifier.size(24.dp))
            if (text.isNotEmpty()) Text(text, style = MaterialTheme.typography.labelLarge.copy(fontSize = 17.sp), maxLines = 1)
        }
    }
}

/** A round icon button, for secondary actions next to Play. Its label shows underneath while it has focus. */
@Composable
fun RoundButton(icon: ImageVector, label: String, onClick: () -> Unit, modifier: Modifier = Modifier, tint: Color? = null) {
    var focused by remember { mutableStateOf(false) }
    val shown by animateFloatAsState(if (focused) 1f else 0f, label = "roundLabel")
    Box(Modifier.size(48.dp)) {
        Surface(
            onClick = onClick,
            modifier = modifier.size(48.dp).onFocusChanged { focused = it.isFocused },
            shape = ClickableSurfaceDefaults.shape(CircleShape),
            colors = ClickableSurfaceDefaults.colors(
                containerColor = Wren.glass, contentColor = tint ?: Wren.text,
                focusedContainerColor = Color.White, focusedContentColor = Color.Black,
            ),
            scale = ClickableSurfaceDefaults.scale(focusedScale = 1.1f),
        ) {
            Icon(icon, contentDescription = label, modifier = Modifier.align(Alignment.Center).size(24.dp))
        }
        // Takes no room in the row, so the buttons stay evenly spaced.
        Text(
            label, style = MaterialTheme.typography.labelMedium, color = Wren.text, maxLines = 1,
            modifier = Modifier.align(Alignment.BottomCenter).wrapContentSize(unbounded = true).offset(y = 32.dp).alpha(shown),
        )
    }
}

/** A filter or tab chip. */
@Composable
fun Chip(text: String, selected: Boolean, onClick: () -> Unit, modifier: Modifier = Modifier, icon: ImageVector? = null) {
    Surface(
        onClick = onClick,
        modifier = modifier.height(38.dp),
        shape = ClickableSurfaceDefaults.shape(CircleShape),
        colors = ClickableSurfaceDefaults.colors(
            // The chosen filter is ember so it reads apart from the focused one, which is white.
            containerColor = if (selected) Wren.ember.copy(alpha = 0.85f) else Color.White.copy(alpha = 0.07f),
            contentColor = if (selected) Color.White else Wren.secondary,
            focusedContainerColor = Color.White,
            focusedContentColor = Color.Black,
        ),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1.05f),
    ) {
        Row(Modifier.fillMaxHeight().padding(horizontal = 18.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            if (icon != null) Icon(icon, null, Modifier.size(18.dp))
            Text(text, style = MaterialTheme.typography.labelLarge, maxLines = 1)
        }
    }
}

// Cards

private val cardShape = RoundedCornerShape(10.dp)

/** Artwork that lifts and gets a white ring when focused, like the TV app. */
@Composable
fun ArtCard(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    onLongClick: (() -> Unit)? = null,
    shape: RoundedCornerShape = cardShape,
    content: @Composable BoxScope.() -> Unit,
) {
    Surface(
        onClick = onClick,
        onLongClick = onLongClick,
        modifier = modifier,
        shape = ClickableSurfaceDefaults.shape(shape),
        colors = ClickableSurfaceDefaults.colors(containerColor = Wren.surface, focusedContainerColor = Wren.surface),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1.07f),
        border = ClickableSurfaceDefaults.border(focusedBorder = Border(BorderStroke(3.dp, Color.White), inset = 0.dp, shape = shape)),
        content = content,
    )
}

/** Card text that brightens with focus. */
@Composable
private fun CardCaption(title: String, subtitle: String?, focused: Boolean, width: Dp) {
    val a by animateFloatAsState(if (focused) 1f else 0.75f, label = "caption")
    Column(Modifier.width(width).padding(top = 10.dp).alpha(a)) {
        Text(title, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis, color = Wren.text)
        if (!subtitle.isNullOrEmpty()) {
            Text(subtitle, style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis, color = Wren.secondary)
        }
    }
}

@Composable
fun PosterCard(item: Item, onClick: () -> Unit, modifier: Modifier = Modifier, width: Dp = 150.dp, onLongClick: (() -> Unit)? = null) {
    val api = LocalAppState.current.api
    var focused by remember { mutableStateOf(false) }
    Column(modifier.width(width)) {
        ArtCard(onClick, Modifier.width(width).aspectRatio(2f / 3f).onFocusChanged { focused = it.hasFocus }, onLongClick) {
            RemoteImage(api?.imageUrl(item.posterPath), Modifier.fillMaxSize())
            if (item.posterPath == null) PlaceholderTitle(item.displayTitle)
            if (item.inProgress) item.progress?.let { ProgressStrip(it.fraction, Modifier.align(Alignment.BottomCenter)) }
            if (item.isWatched) WatchedMark(Modifier.align(Alignment.TopEnd))
            else item.unwatchedCount?.takeIf { it > 0 }?.let { CountBadge(it, Modifier.align(Alignment.TopEnd)) }
        }
        val sub = when {
            item.kind == "show" && (item.childCount ?: 0) > 0 -> "${item.childCount} episode${if (item.childCount == 1) "" else "s"}"
            item.kind != "season" -> item.year?.toString()
            else -> null
        }
        CardCaption(item.displayTitle, sub, focused, width)
    }
}

@Composable
fun LandscapeCard(item: Item, onClick: () -> Unit, modifier: Modifier = Modifier, width: Dp = 300.dp, onLongClick: (() -> Unit)? = null) {
    val api = LocalAppState.current.api
    var focused by remember { mutableStateOf(false) }
    Column(modifier.width(width)) {
        ArtCard(onClick, Modifier.width(width).aspectRatio(16f / 9f).onFocusChanged { focused = it.hasFocus }, onLongClick) {
            RemoteImage(api?.imageUrl(item.stillPath ?: item.backdropPath, "w780"), Modifier.fillMaxSize())
            item.progress?.takeIf { it.position > 0 && !it.watched }?.let { ProgressStrip(it.fraction, Modifier.align(Alignment.BottomCenter)) }
        }
        val left = item.progress?.let(::timeLeft)
        if (item.kind == "episode") {
            CardCaption(item.showTitle ?: item.title, listOfNotNull("${item.episodeLabel} · ${item.title}", left).joinToString(" · "), focused, width)
        } else {
            CardCaption(item.title, listOfNotNull(item.year?.toString(), left).joinToString(" · "), focused, width)
        }
    }
}

/** A TMDB title: opens the library item when the server has it; with streaming on, anything opens to play. */
@Composable
fun TitleCard(title: TitleView, modifier: Modifier = Modifier, rank: Int? = null, width: Dp = 150.dp) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val notices = LocalNotices.current
    var focused by remember { mutableStateOf(false) }
    val missing = title.item == null && !state.canStream
    Column(modifier.width(width)) {
        ArtCard(
            onClick = {
                val item = title.item
                when {
                    item != null -> nav.push(Route.Detail(item.id, item))
                    state.canStream -> nav.push(Route.StreamTitle(title.tmdbId, title.mediaType, title.title))
                    else -> notices.show("${title.title} isn't on this server.")
                }
            },
            modifier = Modifier.width(width).aspectRatio(2f / 3f).onFocusChanged { focused = it.hasFocus },
        ) {
            RemoteImage(state.api?.imageUrl(title.posterPath), Modifier.fillMaxSize().alpha(if (missing) 0.6f else 1f))
            if (title.posterPath == null) PlaceholderTitle(title.title)
            if (rank != null) {
                Text(
                    "$rank", style = MaterialTheme.typography.labelLarge, color = Color.White,
                    modifier = Modifier.padding(8.dp).background(Color.Black.copy(alpha = 0.7f), RoundedCornerShape(6.dp)).padding(horizontal = 8.dp, vertical = 2.dp),
                )
            }
            if (missing) {
                Text(
                    "NOT IN LIBRARY", style = MaterialTheme.typography.labelSmall, color = Color.White, textAlign = TextAlign.Center,
                    modifier = Modifier.align(Alignment.BottomCenter).fillMaxWidth().background(Color.Black.copy(alpha = 0.7f)).padding(vertical = 5.dp),
                )
            } else {
                val p = title.item?.progress
                if (p != null && title.item?.inProgress == true) ProgressStrip(p.fraction, Modifier.align(Alignment.BottomCenter))
            }
        }
        CardCaption(title.title, title.year?.toString(), focused, width)
    }
}

@Composable
private fun BoxScope.PlaceholderTitle(title: String) {
    Text(
        title, style = MaterialTheme.typography.titleMedium, color = Wren.secondary, textAlign = TextAlign.Center,
        modifier = Modifier.align(Alignment.Center).padding(12.dp),
    )
}

@Composable
private fun WatchedMark(modifier: Modifier) {
    Icon(
        Icons.Rounded.CheckCircle, contentDescription = "Watched", tint = Color.White,
        modifier = modifier.padding(8.dp).size(26.dp).background(Wren.ember, CircleShape),
    )
}

@Composable
private fun CountBadge(n: Int, modifier: Modifier) {
    Text(
        "$n", style = MaterialTheme.typography.labelMedium, color = Color.White,
        modifier = modifier.padding(8.dp).background(Wren.ember, CircleShape).padding(horizontal = 8.dp, vertical = 2.dp),
    )
}

/** Playback progress as a thin bar floating over the bottom of artwork. */
@Composable
fun ProgressStrip(fraction: Double, modifier: Modifier = Modifier) {
    // A dark track and ember fill, so it still shows on bright artwork (white on white disappeared).
    Box(modifier.fillMaxWidth().padding(horizontal = 10.dp, vertical = 9.dp).height(5.dp).clip(CircleShape).background(Color.Black.copy(alpha = 0.55f))) {
        Box(Modifier.fillMaxHeight().fillMaxWidth(fraction.toFloat().coerceIn(0.03f, 1f)).background(Wren.ember))
    }
}

/** A small quality or rating tag ("4K", "DOLBY VISION", "PG-13"). */
@Composable
fun Badge(text: String, modifier: Modifier = Modifier) {
    Text(
        text.uppercase(), style = MaterialTheme.typography.labelSmall, color = Wren.text.copy(alpha = 0.92f),
        modifier = modifier.background(Color.White.copy(alpha = 0.16f), RoundedCornerShape(4.dp)).padding(horizontal = 7.dp, vertical = 3.dp),
    )
}

// Rows

val PageGutter = 56.dp

/** A titled horizontal row of cards. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun Shelf(title: String, modifier: Modifier = Modifier, subtitle: String? = null, content: LazyListScope.() -> Unit) {
    Column(modifier.fillMaxWidth()) {
        Row(Modifier.padding(horizontal = PageGutter), verticalAlignment = Alignment.Bottom, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Text(title, style = MaterialTheme.typography.headlineSmall, color = Wren.text)
            if (subtitle != null) Text(subtitle, style = MaterialTheme.typography.bodyMedium, color = Wren.secondary)
        }
        LazyRow(
            // When the page scrolls a shelf into view, its first screenful of
            // cards is composed ahead of time instead of all in one frame.
            state = rememberLazyListState(prefetchStrategy = remember { LazyListPrefetchStrategy(nestedPrefetchItemCount = 8) }),
            contentPadding = PaddingValues(horizontal = PageGutter, vertical = 16.dp),
            horizontalArrangement = Arrangement.spacedBy(20.dp),
            content = content,
        )
    }
}

/** A page heading with optional controls on the right. */
@Composable
fun PageHeader(title: String, subtitle: String? = null, actions: @Composable RowScope.() -> Unit = {}) {
    Row(
        Modifier.fillMaxWidth().padding(start = PageGutter, end = PageGutter, top = 28.dp, bottom = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.displaySmall, color = Wren.text)
            if (!subtitle.isNullOrEmpty()) Text(subtitle, style = MaterialTheme.typography.bodyLarge, color = Wren.secondary)
        }
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically, content = actions)
    }
}

@Composable
fun EmptyState(title: String, message: String? = null, modifier: Modifier = Modifier, icon: ImageVector? = null) {
    Column(modifier.fillMaxWidth().padding(vertical = 80.dp, horizontal = PageGutter), horizontalAlignment = Alignment.CenterHorizontally) {
        if (icon != null) Icon(icon, null, Modifier.size(54.dp), tint = Wren.tertiary)
        Spacer(Modifier.height(16.dp))
        Text(title, style = MaterialTheme.typography.headlineMedium, color = Wren.text, textAlign = TextAlign.Center)
        if (message != null) {
            Spacer(Modifier.height(8.dp))
            Text(message, style = MaterialTheme.typography.bodyLarge, color = Wren.secondary, textAlign = TextAlign.Center, modifier = Modifier.width(620.dp))
        }
    }
}

@Composable
fun Loading(modifier: Modifier = Modifier) {
    Box(modifier.fillMaxWidth().padding(vertical = 140.dp), contentAlignment = Alignment.Center) {
        Spinner()
    }
}

/** A ring that spins, in the brand color. */
@Composable
fun Spinner(size: Dp = 44.dp) {
    androidx.compose.material3.CircularProgressIndicator(
        color = Wren.ember, trackColor = Color.White.copy(alpha = 0.1f), strokeWidth = 4.dp, modifier = Modifier.size(size),
    )
}

/** Darkens the bottom and left of a backdrop so text over it reads. */
fun Modifier.heroScrim(bottomTo: Color = Wren.background): Modifier = this
    .background(Brush.verticalGradient(0.35f to Color.Transparent, 0.75f to Color.Black.copy(alpha = 0.55f), 1f to bottomTo))
    .background(Brush.horizontalGradient(0f to Color.Black.copy(alpha = 0.8f), 0.55f to Color.Transparent))

// Formatting

fun formatDuration(seconds: Double): String {
    val s = seconds.roundToInt()
    val h = s / 3600
    val m = (s % 3600) / 60
    return if (h > 0) "${h}h ${m}m" else "${m}m"
}

fun formatClock(seconds: Double): String {
    if (!seconds.isFinite() || seconds < 0) return "0:00"
    val s = seconds.toInt()
    val h = s / 3600
    val m = (s % 3600) / 60
    val sec = s % 60
    return if (h > 0) "%d:%02d:%02d".format(h, m, sec) else "%d:%02d".format(m, sec)
}

/** "52m left" for something partly watched. */
fun timeLeft(p: WatchState): String? {
    val left = p.duration - p.position
    if (p.duration <= 0 || left <= 60 || p.watched) return null
    return "${formatDuration(left)} left"
}

fun formatBytes(b: Long): String {
    if (b < 1024) return "$b B"
    val units = listOf("KB", "MB", "GB", "TB")
    var v = b / 1024.0
    var i = 0
    while (v >= 1000 && i < units.lastIndex) { v /= 1024; i++ }
    return if (v >= 100) "%.0f %s".format(v, units[i]) else "%.1f %s".format(v, units[i])
}

/** Bold headline text for section titles inside pages. */
@Composable
fun SectionTitle(text: String, modifier: Modifier = Modifier) {
    Text(text, style = MaterialTheme.typography.headlineSmall.copy(fontWeight = FontWeight.SemiBold), color = Wren.text, modifier = modifier)
}
