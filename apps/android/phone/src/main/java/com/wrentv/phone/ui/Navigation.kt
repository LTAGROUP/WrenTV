package com.wrentv.phone.ui

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import com.wrentv.tv.data.AppState
import com.wrentv.tv.data.Item
import com.wrentv.tv.data.Library
import com.wrentv.tv.ui.PlayTarget

/** A page pushed onto a tab. */
sealed interface Route {
    data class Detail(val id: String, val seed: Item? = null) : Route
    /** A TMDB title the server doesn't have yet: added to the streaming library, then shown. */
    data class StreamTitle(val tmdbId: Int, val mediaType: String, val title: String) : Route
    data class LibraryItems(val library: Library) : Route
    data class Collection(val id: String, val name: String) : Route
    data class Genre(val genre: String, val libraryId: String? = null) : Route
    data object Collections : Route
    data object Calendar : Route
    data class Genres(val libraryId: String? = null) : Route
    data object Watchlist : Route
    data object Downloads : Route
    data object LiveTv : Route
    data object Preferences : Route
    data object LinkTv : Route
    data object Search : Route
    /** A library's layout page; its Browse All is [LibraryItems]. */
    data class LibraryPage(val library: Library) : Route
    /** Movies' or TV Shows' whole catalog. */
    data class Catalog(val type: String) : Route
    data class Hub(val id: String, val name: String) : Route
    data class Person(val id: Int, val name: String) : Route
    data object Layouts : Route
    data class LayoutEditor(val scope: String, val title: String) : Route
}

enum class Tab(val label: String) { Home("Home"), Movies("Movies"), Shows("TV Shows"), Library("Library"), Live("Live TV"), Downloads("Downloads"), More("More") }

/** Which channel the live player opens on, and the channels channel up and down step through. */
data class LiveTarget(val channelId: String, val group: String?)

/** A stack of pages per tab, like the iPhone app, plus what's playing full screen. */
class Navigator {
    var tab by mutableStateOf(Tab.Home)
    private val stacks = mutableStateMapOf<Tab, List<Route>>()
    var playing by mutableStateOf<PlayTarget?>(null)
    var live by mutableStateOf<LiveTarget?>(null)

    fun stack(t: Tab = tab): List<Route> = stacks[t].orEmpty()

    fun push(r: Route) { stacks[tab] = stack() + r }

    /** Pops the current tab's top page; false when already at its root. */
    fun back(): Boolean {
        val s = stack()
        if (s.isEmpty()) return false
        stacks[tab] = s.dropLast(1)
        return true
    }

    /** Tapping the selected tab again goes back to its root. */
    fun select(t: Tab) {
        if (t == tab) stacks[t] = emptyList() else tab = t
    }

    fun reset() {
        stacks.clear()
        tab = Tab.Home
        playing = null
        live = null
    }

    fun play(t: PlayTarget) { live = null; playing = t }
}

val LocalAppState = staticCompositionLocalOf<AppState> { error("no AppState") }
val LocalNavigator = staticCompositionLocalOf<Navigator> { error("no Navigator") }
