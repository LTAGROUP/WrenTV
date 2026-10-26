package com.wrentv.tv.ui

import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.wrentv.tv.data.AppState
import com.wrentv.tv.data.Item
import com.wrentv.tv.data.LiveEvent
import com.wrentv.tv.data.TitleView
import com.wrentv.tv.data.WrenCollection

sealed interface Route {
    /** Identifies the page for remembering scroll and focus. */
    val key: String

    // Top-level pages, picked from the bar at the top.
    data object Home : Route { override val key = "home" }
    data object Movies : Route { override val key = "movies" }
    data object Shows : Route { override val key = "shows" }
    data object Collections : Route { override val key = "collections" }
    data object Calendar : Route { override val key = "calendar" }
    data object Search : Route { override val key = "search" }
    data object LiveTv : Route { override val key = "livetv" }
    data object Downloads : Route { override val key = "downloads" }
    data object Settings : Route { override val key = "settings" }
    data class Library(val id: String) : Route { override val key = "library:$id" }

    // Server management (admins), opened from Settings.
    data object Stats : Route { override val key = "stats" }
    data object ServerDownloads : Route { override val key = "server-downloads" }
    data object Users : Route { override val key = "users" }
    data object ManageLibraries : Route { override val key = "manage-libraries" }
    data object ServerSettings : Route { override val key = "server-settings" }
    data object Layouts : Route { override val key = "layouts" }
    data class LayoutEditor(val scope: String, val title: String) : Route { override val key = "layout-editor:$scope" }

    // Pushed pages.
    data class Detail(val itemId: String, val seed: Item? = null) : Route { override val key = "item:$itemId" }
    data class Collection(val collection: WrenCollection) : Route { override val key = "collection:${collection.id}" }
    data object Watchlist : Route { override val key = "watchlist" }
    data class GenreItems(val genre: String, val libraryId: String? = null) : Route { override val key = "genre:$genre:$libraryId" }
    data class Hub(val id: String, val name: String) : Route { override val key = "hub:$id" }
    data class Person(val id: Int, val name: String) : Route { override val key = "person:$id" }
    data class Studio(val id: Int, val name: String) : Route { override val key = "studio:$id" }
    /** Every movie or show TMDB has, as a grid, opened from the Movies and TV Shows pages. */
    data class StreamTitle(val tmdbId: Int, val mediaType: String, val title: String, val seed: TitleView? = null) : Route { override val key = "stream:$mediaType:$tmdbId" }
    data class Trailer(val youtubeKey: String, val title: String) : Route { override val key = "trailer:$youtubeKey" }
    data class Player(val target: PlayTarget) : Route { override val key = "player:${target.itemId}" }
    /**
     * Live TV, full screen, starting on a channel; up and down change it, within
     * [group] when set. [event] is the Stremio addon entry the player was opened
     * on, when it is an event rather than a channel.
     */
    data class LivePlayer(val channelId: String, val group: String? = null, val event: LiveEvent? = null) : Route { override val key = "live-player" }
}

class Navigator {
    var tab by mutableStateOf<Route>(Route.Home)
        private set
    val stack = mutableStateListOf<Route>()

    val current: Route get() = stack.lastOrNull() ?: tab
    val atTop: Boolean get() = stack.isEmpty()

    /** The page each route's focus was on, so coming back lands where you were. */
    val focus = mutableMapOf<String, String>()

    fun select(route: Route) {
        stack.clear()
        tab = route
    }

    fun push(route: Route) {
        if (stack.lastOrNull() != route) stack.add(route)
    }

    fun play(target: PlayTarget) = push(Route.Player(target))

    /** Returns false when there's nowhere to go back to. */
    fun back(): Boolean {
        if (stack.isNotEmpty()) {
            stack.removeAt(stack.lastIndex)
            return true
        }
        if (tab != Route.Home) {
            tab = Route.Home
            return true
        }
        return false
    }
}

val LocalNavigator = compositionLocalOf<Navigator> { error("no navigator") }
val LocalAppState = compositionLocalOf<AppState> { error("no app state") }
val LocalRouteKey = compositionLocalOf { "" }
