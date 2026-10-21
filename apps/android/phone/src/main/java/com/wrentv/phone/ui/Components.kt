package com.wrentv.phone.ui

import android.content.Intent
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.net.toUri
import coil3.compose.AsyncImage
import com.wrentv.tv.data.Item
import com.wrentv.tv.data.TitleView
import com.wrentv.tv.data.WatchState
import com.wrentv.tv.ui.playTarget
import kotlinx.coroutines.launch

/** Sizes that change between phones and tablets. */
class Metrics(val compact: Boolean, private val scale: Float = 1f) {
    val gutter: Dp get() = if (compact) 16.dp else 28.dp
    /** Posters and tiles scale with the Poster Size setting. */
    val poster: Dp get() = (if (compact) 112.dp else 150.dp) * scale
    val landscape: Dp get() = (if (compact) 250.dp else 280.dp) * scale
    val heroHeight: Dp get() = if (compact) 500.dp else 560.dp
    val cardSpacing: Dp get() = if (compact) 12.dp else 16.dp
    val gridMin: Dp get() = (if (compact) 100.dp else 150.dp) * scale
}

@Composable
fun metrics(): Metrics = Metrics(
    LocalConfiguration.current.screenWidthDp < 600,
    when (LocalAppState.current.posterSize) {
        "small" -> 0.85f
        "large" -> 1.25f
        else -> 1f
    },
)

@Composable
fun RemoteImage(url: String?, modifier: Modifier = Modifier, contentScale: ContentScale = ContentScale.Crop) {
    Box(modifier.background(if (contentScale == ContentScale.Crop) Color.White.copy(alpha = 0.06f) else Color.Transparent)) {
        if (url != null) AsyncImage(url, null, Modifier.fillMaxSize(), contentScale = contentScale)
    }
}

/** Playback progress as a thin bar floating over the bottom of artwork. */
@Composable
fun BoxScope.ProgressStrip(fraction: Double) {
    Box(
        Modifier.align(Alignment.BottomCenter).padding(start = 10.dp, end = 10.dp, bottom = 9.dp).fillMaxWidth().height(4.dp)
            .clip(CircleShape).background(Color.White.copy(alpha = 0.3f)),
    ) {
        Box(Modifier.fillMaxWidth(fraction.toFloat().coerceIn(0.03f, 1f)).height(4.dp).clip(CircleShape).background(Color.White))
    }
}

/** "52m left" for something partly watched. */
fun timeLeft(p: WatchState): String? {
    val left = p.duration - p.position
    if (p.duration <= 0 || left <= 60 || p.watched) return null
    return "${formatDuration(left)} left"
}

fun formatDuration(seconds: Double): String {
    val s = Math.round(seconds).toInt()
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

/** The main action: a white capsule with dark text. */
@Composable
fun PrimaryButton(text: String, icon: ImageVector?, onClick: () -> Unit, modifier: Modifier = Modifier, enabled: Boolean = true) {
    Capsule(text, icon, onClick, modifier, Color.White.copy(alpha = if (enabled) 1f else 0.6f), Color.Black, enabled)
}

/** Secondary actions over artwork. */
@Composable
fun GlassButton(text: String, icon: ImageVector?, onClick: () -> Unit, modifier: Modifier = Modifier, enabled: Boolean = true) {
    Capsule(text, icon, onClick, modifier, Color.White.copy(alpha = 0.14f), Color.White, enabled)
}

@Composable
private fun Capsule(text: String, icon: ImageVector?, onClick: () -> Unit, modifier: Modifier, bg: Color, fg: Color, enabled: Boolean) {
    Row(
        modifier.clip(CircleShape).background(bg).combinedClickable(enabled = enabled, onClick = onClick)
            .padding(horizontal = 18.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        if (icon != null) Icon(icon, null, Modifier.size(18.dp), tint = fg)
        Text(text, color = fg, fontWeight = FontWeight.SemiBold, fontSize = 15.sp, maxLines = 1)
    }
}

/** A small quality or rating tag ("4K", "DOLBY VISION"). */
@Composable
fun Badge(text: String) {
    Text(
        text.uppercase(), fontSize = 10.sp, fontWeight = FontWeight.Bold, color = Color.White.copy(alpha = 0.9f),
        modifier = Modifier.clip(RoundedCornerShape(4.dp)).background(Color.White.copy(alpha = 0.16f)).padding(horizontal = 6.dp, vertical = 3.dp),
    )
}

@Composable
fun Loading(modifier: Modifier = Modifier.fillMaxSize()) {
    Box(modifier, contentAlignment = Alignment.Center) { CircularProgressIndicator(color = Color.White) }
}

/** Like ContentUnavailableView: an icon, a title and a line of help. */
@Composable
fun EmptyState(icon: ImageVector, title: String, message: String? = null, modifier: Modifier = Modifier.fillMaxWidth().padding(top = 120.dp)) {
    Column(modifier.padding(horizontal = 32.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Icon(icon, null, Modifier.size(44.dp), tint = Wren.secondary)
        Text(title, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold, textAlign = TextAlign.Center)
        if (message != null) Text(message, color = Wren.secondary, textAlign = TextAlign.Center)
    }
}

/** A titled page with a back arrow when it was pushed. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun Page(title: String, subtitle: String? = null, actions: @Composable RowScope.() -> Unit = {}, content: @Composable () -> Unit) {
    val nav = LocalNavigator.current
    Column(Modifier.fillMaxSize()) {
        TopAppBar(
            title = {
                Column {
                    Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis, fontWeight = FontWeight.Bold)
                    if (!subtitle.isNullOrEmpty()) Text(subtitle, style = MaterialTheme.typography.bodySmall, color = Wren.secondary)
                }
            },
            navigationIcon = {
                if (nav.stack().isNotEmpty()) IconButton({ nav.back() }) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Back") }
            },
            actions = actions,
            colors = TopAppBarDefaults.topAppBarColors(containerColor = Wren.background),
        )
        Box(Modifier.weight(1f)) { content() }
    }
}

/** A titled row of cards that scrolls sideways. */
@Composable
fun Shelf(title: String, subtitle: String? = null, content: LazyListScope.() -> Unit) {
    val m = metrics()
    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Row(Modifier.padding(horizontal = m.gutter), verticalAlignment = Alignment.Bottom, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(title, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
            if (subtitle != null) Text(subtitle, color = Wren.secondary, style = MaterialTheme.typography.bodyMedium)
        }
        LazyRow(contentPadding = PaddingValues(horizontal = m.gutter), horizontalArrangement = Arrangement.spacedBy(m.cardSpacing), content = content)
    }
}

/** A poster that opens its page, with a long-press menu. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun PosterCard(item: Item, modifier: Modifier = Modifier, width: Dp? = null, onWatchlist: Boolean = false) {
    val nav = LocalNavigator.current
    var menu by remember { mutableStateOf(false) }
    Column(
        (if (width != null) modifier.width(width) else modifier.fillMaxWidth())
            .combinedClickable(onClick = { nav.push(Route.Detail(item.id, item)) }, onLongClick = { menu = true }),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        Box(Modifier.fillMaxWidth().aspectRatio(2f / 3f).clip(RoundedCornerShape(10.dp))) {
            RemoteImage(LocalAppState.current.api?.imageUrl(item.posterPath), Modifier.fillMaxSize())
            if (item.posterPath == null) {
                Text(item.title, Modifier.align(Alignment.Center).padding(8.dp), fontWeight = FontWeight.SemiBold, textAlign = TextAlign.Center)
            }
            if (item.inProgress) item.progress?.let { ProgressStrip(it.fraction) }
            if (item.isWatched) {
                Icon(Icons.Rounded.CheckCircle, "Watched", Modifier.align(Alignment.TopEnd).padding(6.dp).size(22.dp).clip(CircleShape).background(Color.White), tint = Wren.ember)
            } else if ((item.unwatchedCount ?: 0) > 0) {
                Text(
                    "${item.unwatchedCount}", fontSize = 12.sp, fontWeight = FontWeight.Bold,
                    modifier = Modifier.align(Alignment.TopEnd).padding(6.dp).clip(CircleShape).background(Wren.ember).padding(horizontal = 6.dp, vertical = 2.dp),
                )
            }
            ItemMenu(item, menu, { menu = false }, onWatchlist)
        }
        Text(item.displayTitle, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyMedium)
        val n = item.childCount
        val sub = when {
            item.kind == "show" && n != null && n > 0 -> "$n episode${if (n == 1) "" else "s"}"
            item.kind != "season" -> item.year?.toString()
            else -> null
        }
        if (sub != null) Text(sub, color = Wren.secondary, style = MaterialTheme.typography.bodySmall)
    }
}

/** A wide still, for Continue Watching and episodes. Tapping plays. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun LandscapeCard(item: Item, modifier: Modifier = Modifier, width: Dp? = null, onClick: (() -> Unit)? = null) {
    val nav = LocalNavigator.current
    val state = LocalAppState.current
    var menu by remember { mutableStateOf(false) }
    Column(
        (if (width != null) modifier.width(width) else modifier.fillMaxWidth())
            .combinedClickable(onClick = onClick ?: { nav.play(item.playTarget()) }, onLongClick = { menu = true }),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        Box(Modifier.fillMaxWidth().aspectRatio(16f / 9f).clip(RoundedCornerShape(10.dp))) {
            RemoteImage(state.api?.imageUrl(item.stillPath ?: item.backdropPath, "w500"), Modifier.fillMaxSize())
            item.progress?.let { p -> if (p.position > 0 && !p.watched) ProgressStrip(p.fraction) }
            ItemMenu(item, menu, { menu = false })
        }
        val left = item.progress?.let(::timeLeft)
        if (item.kind == "episode") {
            Text(item.showTitle ?: item.title, maxLines = 1, overflow = TextOverflow.Ellipsis, fontWeight = FontWeight.Medium, style = MaterialTheme.typography.bodyMedium)
            Text(listOfNotNull("${item.episodeLabel} · ${item.title}", left).joinToString(" · "), maxLines = 1, overflow = TextOverflow.Ellipsis,
                color = Wren.secondary, style = MaterialTheme.typography.bodySmall)
        } else {
            Text(item.title, maxLines = 1, overflow = TextOverflow.Ellipsis, fontWeight = FontWeight.Medium, style = MaterialTheme.typography.bodyMedium)
            Text(listOfNotNull(item.year?.toString(), left).joinToString(" · "), color = Wren.secondary, style = MaterialTheme.typography.bodySmall)
        }
    }
}

/** A TMDB title: opens the library item when the server has it; otherwise streams it, or links to TMDB. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun TitleCard(title: TitleView, modifier: Modifier = Modifier, width: Dp? = null, rank: Int? = null) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val context = LocalContext.current
    var menu by remember { mutableStateOf(false) }
    val item = title.item
    val dim = item == null && !state.canStream
    Column(
        (if (width != null) modifier.width(width) else modifier.fillMaxWidth()).combinedClickable(
            onClick = {
                when {
                    item != null -> nav.push(Route.Detail(item.id, item))
                    state.canStream -> nav.push(Route.StreamTitle(title.tmdbId, title.mediaType, title.title))
                    else -> context.startActivity(Intent(Intent.ACTION_VIEW, tmdbUrl(title).toUri()))
                }
            },
            onLongClick = { if (item != null) menu = true },
        ),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        Box(Modifier.fillMaxWidth().aspectRatio(2f / 3f).clip(RoundedCornerShape(8.dp))) {
            RemoteImage(state.api?.imageUrl(title.posterPath), Modifier.fillMaxSize())
            if (title.posterPath == null) Text(title.title, Modifier.align(Alignment.Center).padding(8.dp), fontWeight = FontWeight.SemiBold, textAlign = TextAlign.Center)
            if (rank != null) {
                Text(
                    "$rank", fontSize = 12.sp, fontWeight = FontWeight.Bold,
                    modifier = Modifier.padding(6.dp).clip(RoundedCornerShape(5.dp)).background(Color.Black.copy(alpha = 0.7f)).padding(horizontal = 7.dp, vertical = 3.dp),
                )
            }
            if (dim) {
                Text(
                    "NOT IN LIBRARY", fontSize = 9.sp, fontWeight = FontWeight.Bold, textAlign = TextAlign.Center,
                    modifier = Modifier.align(Alignment.BottomCenter).fillMaxWidth().background(Color.Black.copy(alpha = 0.7f)).padding(vertical = 4.dp),
                )
            } else if (item?.inProgress == true) {
                item.progress?.let { ProgressStrip(it.fraction) }
            }
            if (item != null) ItemMenu(item, menu, { menu = false })
        }
        Text(title.title, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyMedium)
        title.year?.let { Text("$it", color = Wren.secondary, style = MaterialTheme.typography.bodySmall) }
    }
}

fun tmdbUrl(t: TitleView) = "https://www.themoviedb.org/${if (t.mediaType == "show") "tv" else "movie"}/${t.tmdbId}"

/** The long-press menu on a movie, show or episode. */
@Composable
fun ItemMenu(item: Item, expanded: Boolean, dismiss: () -> Unit, onWatchlist: Boolean = false) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val scope = rememberCoroutineScope()
    DropdownMenu(expanded, dismiss) {
        if (item.isPlayable) {
            DropdownMenuItem({ Text(if (item.inProgress) "Resume" else "Play") }, { dismiss(); nav.play(item.playTarget()) })
            if (item.inProgress) DropdownMenuItem({ Text("Play from Beginning") }, { dismiss(); nav.play(item.playTarget(fromStart = true)) })
        }
        DropdownMenuItem({ Text("Details") }, { dismiss(); nav.push(Route.Detail(item.id, item)) })
        if (item.kind == "episode" && item.showId != null) {
            DropdownMenuItem({ Text("Go to Show") }, { dismiss(); nav.push(Route.Detail(item.showId!!)) })
        }
        DropdownMenuItem({ Text(if (item.isWatched) "Mark Unwatched" else "Mark Watched") }, {
            dismiss()
            scope.launch { runCatching { state.api?.setWatched(item.id, !item.isWatched) }; state.refreshTick++ }
        })
        if (item.kind == "movie" || item.kind == "show") {
            DropdownMenuItem({ Text(if (onWatchlist) "Remove from Watchlist" else "Add to Watchlist") }, {
                dismiss()
                scope.launch { runCatching { state.api?.setWatchlist(item.id, !onWatchlist) }; state.refreshTick++ }
            })
        }
    }
}

/** Placeholder shelf while a row loads. */
@Composable
fun SkeletonShelf(title: String) {
    val m = metrics()
    Shelf(title) {
        items(7) {
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Box(Modifier.width(m.poster).aspectRatio(2f / 3f).clip(RoundedCornerShape(8.dp)).background(Color.White.copy(alpha = 0.06f)))
                Box(Modifier.width(m.poster * 0.7f).height(10.dp).clip(RoundedCornerShape(3.dp)).background(Color.White.copy(alpha = 0.06f)))
            }
        }
    }
}

/** A channel's logo, or its initials when it has none. */
@Composable
fun ChannelLogo(logo: String?, name: String, modifier: Modifier) {
    val api = LocalAppState.current.api
    val url = logo?.takeIf { it.isNotEmpty() }?.let { if (it.startsWith("http")) it else api?.absolute(it) }
    Box(modifier.clip(RoundedCornerShape(6.dp)).background(Color.White.copy(alpha = 0.08f)), contentAlignment = Alignment.Center) {
        if (url != null) AsyncImage(url, null, Modifier.fillMaxSize().padding(3.dp), contentScale = ContentScale.Fit)
        else Text(name.split(" ").take(2).mapNotNull { it.firstOrNull() }.joinToString("").uppercase(), color = Wren.secondary, fontWeight = FontWeight.Bold, fontSize = 12.sp)
    }
}

/** A chip like the iPhone app's group filters. */
@Composable
fun Chip(text: String, selected: Boolean, onClick: () -> Unit) {
    Text(
        text, color = if (selected) Color.Black else Color.White, style = MaterialTheme.typography.bodyMedium,
        modifier = Modifier.clip(CircleShape).background(if (selected) Color.White else Color.White.copy(alpha = 0.1f))
            .combinedClickable(onClick = onClick).padding(horizontal = 12.dp, vertical = 6.dp),
    )
}

@Composable
fun Gap(h: Dp) = Spacer(Modifier.height(h))
