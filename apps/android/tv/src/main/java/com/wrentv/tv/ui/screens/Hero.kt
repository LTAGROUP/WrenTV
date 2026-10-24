package com.wrentv.tv.ui.screens

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.Crossfade
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.background
import androidx.compose.foundation.focusGroup
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowForward
import androidx.compose.material.icons.rounded.Info
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import coil3.compose.AsyncImage
import com.wrentv.tv.data.Item
import com.wrentv.tv.data.TitleView
import com.wrentv.tv.ui.Badge
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.LocalNotices
import com.wrentv.tv.ui.PageGutter
import com.wrentv.tv.ui.PillButton
import com.wrentv.tv.ui.RemoteImage
import com.wrentv.tv.ui.Route
import com.wrentv.tv.ui.TopBarSpace
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.formatDuration
import com.wrentv.tv.ui.heroScrim
import com.wrentv.tv.ui.playTarget
import com.wrentv.tv.ui.rememberFocus
import com.wrentv.tv.ui.timeLeft
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** One title in the banner at the top of Home. */
data class HeroEntry(
    val id: String,
    val title: String,
    val backdrop: String?,
    val logo: String?,
    val meta: List<String>,
    val badges: List<String>,
    val overview: String?,
    val item: Item?,
    val tmdb: TitleView? = null,
)

fun Item.heroEntry() = HeroEntry(
    id = id, title = if (kind == "episode") showTitle ?: title else title, backdrop = backdropPath, logo = logoPath,
    meta = listOfNotNull(
        if (kind == "episode") "$episodeLabel · $title" else null,
        year?.toString(),
        if (kind == "show") "Series" else null,
        runtimeMin?.takeIf { it > 0 }?.let { formatDuration(it * 60.0) },
        rating?.takeIf { it > 0 }?.let { "★ %.1f".format(it) },
    ) + (genres ?: emptyList()).take(2),
    badges = listOfNotNull(contentRating?.takeIf { it.isNotEmpty() }),
    overview = overview, item = this,
)

fun TitleView.heroEntry() = HeroEntry(
    id = id, title = title, backdrop = backdropPath, logo = item?.logoPath,
    meta = listOfNotNull(year?.toString(), if (mediaType == "show") "Series" else "Film", rating?.takeIf { it > 0 }?.let { "★ %.1f".format(it) }),
    badges = listOfNotNull(item?.contentRating?.takeIf { it.isNotEmpty() }),
    overview = overview, item = item, tmdb = this,
)

/**
 * The full-bleed banner: a backdrop behind the page with the title, a meta
 * line and Play / More Info. It moves to the next title every few seconds
 * unless its buttons have focus; left and right on the buttons step through.
 */
@Composable
fun Hero(entries: List<HeroEntry>, label: String? = null) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val notices = LocalNotices.current
    val scope = rememberCoroutineScope()
    var index by remember { mutableIntStateOf(0) }
    var focused by remember { mutableStateOf(false) }
    var opening by remember { mutableStateOf(false) }
    val entry = entries[index.coerceIn(0, entries.lastIndex)]
    LaunchedEffect(entries.map { it.id }) { entries.forEach { e -> e.tmdb?.let { state.logos.want(it) } } }

    LaunchedEffect(entries.map { it.id }, focused) {
        while (entries.size > 1 && !focused) {
            delay(9000)
            index = (index + 1) % entries.size
        }
    }

    fun details(e: HeroEntry) {
        val item = e.item
        val t = e.tmdb
        when {
            item != null -> nav.push(Route.Detail(item.id, item))
            t != null -> nav.push(Route.StreamTitle(t.tmdbId, t.mediaType, t.title, t))
        }
    }

    fun play(e: HeroEntry) {
        val item = e.item
        val t = e.tmdb
        when {
            item != null && item.isPlayable -> nav.play(item.playTarget())
            item != null -> nav.push(Route.Detail(item.id, item))
            t != null && t.mediaType == "movie" -> {
                // Adds the movie to the streaming library, then plays it.
                opening = true
                scope.launch {
                    try {
                        state.api?.addStreamTitle(t.tmdbId, t.mediaType)?.let { nav.play(it.playTarget()) }
                    } catch (ex: Exception) {
                        notices.show(ex.message)
                    } finally {
                        opening = false
                    }
                }
            }
            t != null -> nav.push(Route.StreamTitle(t.tmdbId, t.mediaType, t.title, t))
        }
    }

    Box(Modifier.fillMaxWidth().height(540.dp)) {
        Crossfade(entry.backdrop, label = "backdrop") { b ->
            RemoteImage(state.api?.imageUrl(b, "w1280"), Modifier.fillMaxSize(), alignment = Alignment.TopCenter)
        }
        Box(Modifier.fillMaxSize().heroScrim())
        Column(
            Modifier.align(Alignment.BottomStart).padding(start = PageGutter, bottom = 20.dp, top = TopBarSpace).widthIn(max = 720.dp),
        ) {
            if (label != null) {
                Text(label.uppercase(), style = MaterialTheme.typography.labelMedium.copy(letterSpacing = 1.5.sp), color = Color.White.copy(alpha = 0.7f))
                Spacer(Modifier.height(10.dp))
            }
            AnimatedContent(entry, transitionSpec = { fadeIn() togetherWith fadeOut() }, label = "hero") { e ->
                Column {
                    val logo = e.logo ?: e.tmdb?.let { state.logos[it] }
                    if (logo != null) {
                        AsyncImage(
                            state.api?.imageUrl(logo, "w500"), e.title,
                            Modifier.width(340.dp).height(104.dp),
                            contentScale = ContentScale.Fit, alignment = Alignment.BottomStart,
                        )
                    } else {
                        Text(
                            e.title, style = MaterialTheme.typography.displayMedium.copy(fontWeight = FontWeight.Bold),
                            color = Wren.text, maxLines = 2, overflow = TextOverflow.Ellipsis,
                        )
                    }
                    Spacer(Modifier.height(14.dp))
                    MetaLine(e.meta, e.badges)
                    e.overview?.takeIf { it.isNotEmpty() }?.let {
                        Spacer(Modifier.height(10.dp))
                        Text(it, style = MaterialTheme.typography.bodyMedium, color = Color.White.copy(alpha = 0.85f), maxLines = 3, overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.widthIn(max = 620.dp))
                    }
                }
            }
            Spacer(Modifier.height(22.dp))
            Row(
                Modifier.focusGroup().onFocusChanged { focused = it.hasFocus }.onPreviewKeyEvent { ev ->
                    // The remote's fast-forward and rewind keys step through the banner.
                    if (ev.type != KeyEventType.KeyDown || entries.size < 2) return@onPreviewKeyEvent false
                    when (ev.key) {
                        Key.MediaFastForward -> { index = (index + 1) % entries.size; true }
                        Key.MediaRewind -> { index = (index - 1 + entries.size) % entries.size; true }
                        else -> false
                    }
                },
                horizontalArrangement = Arrangement.spacedBy(14.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                val item = entry.item
                val playLabel = when {
                    opening -> "Opening…"
                    item != null && item.inProgress -> item.progress?.let(::timeLeft)?.let { "Resume · $it" } ?: "Resume"
                    item != null && !item.isPlayable -> "Watch Now"
                    entry.tmdb?.mediaType == "show" -> "Watch Now"
                    else -> "Play"
                }
                PillButton(playLabel, { play(entry) }, Modifier.rememberFocus("hero:play", default = true), icon = Icons.Rounded.PlayArrow, primary = true, enabled = !opening)
                PillButton("More Info", { details(entry) }, Modifier.rememberFocus("hero:info"), icon = Icons.Rounded.Info)
                if (entries.size > 1) {
                    PillButton("", { index = (index + 1) % entries.size }, Modifier.rememberFocus("hero:next"), icon = Icons.AutoMirrored.Rounded.ArrowForward)
                    Spacer(Modifier.width(6.dp))
                    Dots(entries.size, index)
                }
            }
        }
    }
}

@Composable
private fun Dots(count: Int, index: Int) {
    Row(horizontalArrangement = Arrangement.spacedBy(7.dp), verticalAlignment = Alignment.CenterVertically) {
        repeat(count) { i ->
            Box(
                Modifier.height(7.dp).width(if (i == index) 18.dp else 7.dp)
                    .background(Color.White.copy(alpha = if (i == index) 0.95f else 0.35f), CircleShape),
            )
        }
    }
}

/** "2023 · 2h 6m · ★ 7.8" followed by tags. */
@Composable
fun MetaLine(parts: List<String>, badges: List<String> = emptyList()) {
    Row(horizontalArrangement = Arrangement.spacedBy(10.dp), verticalAlignment = Alignment.CenterVertically) {
        badges.forEach { Badge(it) }
        if (parts.isNotEmpty()) {
            Text(parts.joinToString("  ·  "), style = MaterialTheme.typography.titleSmall, color = Color.White.copy(alpha = 0.8f), maxLines = 1)
        }
    }
}
