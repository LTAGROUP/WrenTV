package com.wrentv.tv.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.CalendarMonth
import androidx.compose.material.icons.rounded.Visibility
import androidx.compose.material.icons.rounded.WifiOff
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.wrentv.tv.data.CalendarEpisode
import com.wrentv.tv.data.CalendarResponse
import com.wrentv.tv.ui.ArtCard
import com.wrentv.tv.ui.CardCaption
import com.wrentv.tv.ui.Chip
import com.wrentv.tv.ui.CinematicBrowse
import com.wrentv.tv.ui.CinematicLandscape
import com.wrentv.tv.ui.EmptyState
import com.wrentv.tv.ui.Loading
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.RemoteImage
import com.wrentv.tv.ui.Route
import com.wrentv.tv.ui.Shelf
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.ambient
import com.wrentv.tv.ui.rememberFocus
import com.wrentv.tv.ui.rememberLoaded
import java.time.LocalDate
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle

/**
 * When episodes air: a week back to a month ahead, a shelf a day, from the
 * library's shows that are still running and the ones being watched.
 */
@Composable
fun CalendarScreen() {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val cal = rememberLoaded<CalendarResponse>("calendar", state.refreshTick) { state.api?.calendar() }
    var watchingOnly by rememberSaveable { mutableStateOf(false) }
    val list = rememberLazyListState()
    val today = remember { LocalDate.now() }
    val episodes = cal.value?.episodes.orEmpty().filter { !watchingOnly || it.watching }
    val days = episodes.groupBy { it.date }
    // The page opens on what's next to air, with what just aired above it.
    val next = episodes.firstOrNull { it.date >= today.toString() } ?: episodes.lastOrNull()
    CinematicBrowse(list, fallback = next?.heroEntry(today), label = "Calendar", pills = {
        Chip("Shows I'm Watching", watchingOnly, { watchingOnly = !watchingOnly }, Modifier.rememberFocus("chip:watching"), icon = Icons.Rounded.Visibility)
    }) {
        val c = cal.value
        when {
            c == null && cal.error != null -> extra("error") { EmptyState("Couldn't load", cal.error, icon = Icons.Rounded.WifiOff) }
            c == null -> extra("loading") { Loading() }
            episodes.isEmpty() -> extra("none") {
                EmptyState(
                    if (watchingOnly) "Nothing coming up for your shows" else "Nothing airing",
                    "Episodes from a week back to a month ahead show here, for shows in the library that are still running.",
                    icon = Icons.Rounded.CalendarMonth,
                )
            }
            else -> days.forEach { (date, eps) ->
                row("day:$date") {
                    Shelf(dayName(date, today), subtitle = "${eps.size} episode${if (eps.size == 1) "" else "s"}") {
                        items(eps, key = { it.id }) { e ->
                            CalendarCard(e, today, Modifier.rememberFocus("cal:${e.id}", default = e == next)) {
                                val id = e.itemId
                                if (id != null) nav.push(Route.Detail(id)) else nav.push(Route.Detail(e.show.id, e.show))
                            }
                        }
                    }
                }
            }
        }
    }
}

/** "Today", "Tomorrow", "Yesterday", or the weekday and date. */
private fun dayName(date: String, today: LocalDate): String {
    val d = runCatching { LocalDate.parse(date) }.getOrNull() ?: return date
    return when (d) {
        today -> "Today"
        today.plusDays(1) -> "Tomorrow"
        today.minusDays(1) -> "Yesterday"
        else -> d.format(DateTimeFormatter.ofPattern("EEEE, MMM d"))
    }
}

private fun airs(date: String, today: LocalDate): String {
    val d = runCatching { LocalDate.parse(date) }.getOrNull() ?: return date
    val day = dayName(date, today).let { if (it == "Today" || it == "Tomorrow" || it == "Yesterday") it.lowercase() else d.format(DateTimeFormatter.ofLocalizedDate(FormatStyle.MEDIUM)) }
    return if (d.isAfter(today)) "Airs $day" else if (d == today) "Airs today" else "Aired $day"
}

private fun CalendarEpisode.heroEntry(today: LocalDate) = HeroEntry(
    id = "cal:$id", title = show.title, backdrop = show.backdropPath, logo = show.logoPath,
    meta = listOfNotNull("$code · ${title.ifEmpty { "Episode $episodeNumber" }}", airs(date, today),
        if (upNext) "Up next" else if (watched) "Watched" else null, if (itemId == null && date <= today.toString()) "Not in the library yet" else null),
    badges = emptyList(), overview = overview?.takeIf { it.isNotEmpty() } ?: show.overview, item = null,
)

@Composable
private fun CalendarCard(e: CalendarEpisode, today: LocalDate, modifier: Modifier = Modifier, onClick: () -> Unit) {
    val api = LocalAppState.current.api
    var focused by remember { mutableStateOf(false) }
    val width = CinematicLandscape
    Column(modifier.width(width)) {
        ArtCard(onClick, Modifier.width(width).aspectRatio(16f / 9f).onFocusChanged { focused = it.hasFocus }.ambient(e.show.backdropPath) { e.heroEntry(today) }) {
            RemoteImage(api?.imageUrl(e.stillPath ?: e.show.backdropPath, "w780"), Modifier.fillMaxSize())
            val tag = when {
                e.upNext -> "UP NEXT"
                e.watched -> "WATCHED"
                else -> null
            }
            if (tag != null) {
                Box(Modifier.align(Alignment.TopStart).padding(10.dp)
                    .background(if (e.upNext) Wren.ember else Color.Black.copy(alpha = 0.7f), RoundedCornerShape(6.dp))
                    .padding(horizontal = 8.dp, vertical = 3.dp)) {
                    Text(tag, style = MaterialTheme.typography.labelSmall, color = Color.White)
                }
            }
        }
        CardCaption(e.show.title, "${e.code} · ${e.title.ifEmpty { "Episode ${e.episodeNumber}" }}", focused, width)
    }
}
