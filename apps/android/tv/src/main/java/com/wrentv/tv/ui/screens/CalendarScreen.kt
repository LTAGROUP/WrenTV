package com.wrentv.tv.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.focusGroup
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.height
import androidx.compose.material.icons.rounded.ChevronLeft
import androidx.compose.material.icons.rounded.ChevronRight
import androidx.compose.material.icons.rounded.Today
import androidx.compose.ui.draw.alpha
import androidx.tv.material3.Border
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.Icon
import androidx.tv.material3.Surface
import java.time.temporal.TemporalAdjusters
import java.time.temporal.WeekFields
import java.util.Locale
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
 * When episodes air, a week at a time: a strip of the week's days (select
 * one to see just that day, select it again for the whole week) between
 * previous and next week buttons, then a shelf a day, from the library's
 * shows that are still running and the ones being watched.
 */
@Composable
fun CalendarScreen() {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val today = remember { LocalDate.now() }
    val thisWeek = remember { weekStart(today) }
    var weekText by rememberSaveable { mutableStateOf(thisWeek.toString()) }
    val week = LocalDate.parse(weekText)
    var picked by rememberSaveable { mutableStateOf<String?>(null) }
    val cal = rememberLoaded<CalendarResponse>("calendar:$week", state.refreshTick) {
        state.api?.calendar(week.toString(), week.plusDays(6).toString())
    }
    var watchingOnly by rememberSaveable { mutableStateOf(false) }
    val list = rememberLazyListState()
    val episodes = cal.value?.episodes.orEmpty().filter { !watchingOnly || it.watching }
    val days = episodes.groupBy { it.date }
    val shown = days.filterKeys { picked == null || it == picked }
    val next = episodes.firstOrNull { it.date >= today.toString() } ?: episodes.lastOrNull()
    fun step(n: Long) { picked = null; weekText = week.plusWeeks(n).toString() }
    CinematicBrowse(list, fallback = next?.heroEntry(today), label = "Calendar", pills = {
        Chip("Shows I'm Watching", watchingOnly, { watchingOnly = !watchingOnly }, Modifier.rememberFocus("chip:watching"), icon = Icons.Rounded.Visibility)
        if (week != thisWeek) Chip("This Week", false, { picked = null; weekText = thisWeek.toString() }, Modifier.rememberFocus("chip:thisweek"), icon = Icons.Rounded.Today)
    }) {
        row("week") {
            WeekStrip(week, today, picked, days.mapValues { it.value.size }, { d -> picked = if (picked == d) null else d }, ::step)
        }
        val c = cal.value
        when {
            c == null && cal.error != null -> extra("error") { EmptyState("Couldn't load", cal.error, icon = Icons.Rounded.WifiOff) }
            c == null -> extra("loading") { Loading() }
            shown.isEmpty() -> extra("none") {
                EmptyState(
                    when {
                        picked != null -> "Nothing airing ${dayName(picked!!, today).let { if (it == "Today") "today" else "on $it" }}"
                        watchingOnly -> "Nothing this week for your shows"
                        else -> "Nothing airing this week"
                    },
                    "Episodes show here for shows in the library that are still running. Use the arrows to see other weeks.",
                    icon = Icons.Rounded.CalendarMonth,
                )
            }
            else -> shown.forEach { (date, eps) ->
                row("day:$date") {
                    Shelf(dayName(date, today), subtitle = "${eps.size} episode${if (eps.size == 1) "" else "s"}") {
                        items(eps, key = { it.id }) { e ->
                            CalendarCard(e, today, Modifier.rememberFocus("cal:${e.id}")) {
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

/** The first day of date's week, by the locale's first weekday. */
private fun weekStart(date: LocalDate): LocalDate =
    date.with(TemporalAdjusters.previousOrSame(WeekFields.of(Locale.getDefault()).firstDayOfWeek))

/** The seven days of a week with how many episodes air each, between previous and next week buttons. */
@Composable
private fun WeekStrip(week: LocalDate, today: LocalDate, picked: String?, counts: Map<String, Int>, pick: (String) -> Unit, step: (Long) -> Unit) {
    val end = week.plusDays(6)
    Column(Modifier.padding(horizontal = 48.dp, vertical = 8.dp)) {
        Text("${week.format(DateTimeFormatter.ofPattern("MMM d"))} – ${end.format(DateTimeFormatter.ofPattern("MMM d, yyyy"))}",
            style = MaterialTheme.typography.labelLarge, color = Wren.secondary)
        Row(Modifier.padding(top = 10.dp).focusGroup(), horizontalArrangement = Arrangement.spacedBy(10.dp), verticalAlignment = Alignment.CenterVertically) {
            DayCell({ step(-1) }, false, false, Modifier.width(44.dp).rememberFocus("week:prev")) { Icon(Icons.Rounded.ChevronLeft, "Previous week") }
            for (i in 0L until 7L) {
                val d = week.plusDays(i)
                val key = d.toString()
                val n = counts[key] ?: 0
                DayCell({ pick(key) }, picked == key, d == today, Modifier.width(92.dp).rememberFocus("week:$i", default = d == today)) {
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        Text(d.format(DateTimeFormatter.ofPattern("EEE")).uppercase(), style = MaterialTheme.typography.labelSmall)
                        Text(d.dayOfMonth.toString(), style = MaterialTheme.typography.titleLarge, modifier = Modifier.alpha(if (n == 0 && picked != key) 0.55f else 1f))
                        Text(if (n > 0) "$n" else " ", style = MaterialTheme.typography.labelSmall)
                    }
                }
            }
            DayCell({ step(1) }, false, false, Modifier.width(44.dp).rememberFocus("week:next")) { Icon(Icons.Rounded.ChevronRight, "Next week") }
        }
    }
}

/** A day in the week strip: ember when picked, outlined when today, white when focused. */
@Composable
private fun DayCell(onClick: () -> Unit, on: Boolean, today: Boolean, modifier: Modifier, content: @Composable () -> Unit) {
    val shape = RoundedCornerShape(12.dp)
    Surface(
        onClick = onClick,
        modifier = modifier.height(76.dp),
        shape = ClickableSurfaceDefaults.shape(shape),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = if (on) Wren.ember.copy(alpha = 0.85f) else Color.White.copy(alpha = 0.07f),
            contentColor = if (on) Color.White else if (today) Wren.ember else Color.White,
            focusedContainerColor = Color.White,
            focusedContentColor = Color.Black,
        ),
        border = ClickableSurfaceDefaults.border(
            border = if (today && !on) Border(BorderStroke(2.dp, Wren.ember), shape = shape) else Border.None,
        ),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1.08f),
    ) {
        Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { content() }
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
