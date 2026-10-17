package com.wrentv.tv.ui

import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.wrentv.tv.data.AppState
import com.wrentv.tv.data.Item
import com.wrentv.tv.data.WrenCollection

/** What the player opens. */
data class PlayTarget(
    val itemId: String,
    val title: String,
    val subtitle: String? = null,
    val fromStart: Boolean = false,
    /** A specific version (file) to play; null lets the server choose. */
    val fileId: String? = null,
    /** A downloaded copy on this TV, played without the server. */
    val localFile: String? = null,
    val localPosition: Double? = null,
)

fun Item.playTarget(showTitle: String? = null, fromStart: Boolean = false, fileId: String? = null) = PlayTarget(
    itemId = id,
    title = if (kind == "episode") showTitle ?: this.showTitle ?: title else title,
    subtitle = if (kind == "episode") "$episodeLabel — $title" else null,
    fromStart = fromStart,
    fileId = fileId,
)

sealed interface Route {
    /** Identifies the page for remembering scroll and focus. */
    val key: String

    // Top-level pages, picked from the bar at the top.
    data object Home : Route { override val key = "home" }
    data object Movies : Route { override val key = "movies" }
    data object Shows : Route { override val key = "shows" }
    data object Collections : Route { override val key = "collections" }
    data object Genres : Route { override val key = "genres" }
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

    // Pushed pages.
    data class Detail(val itemId: String, val seed: Item? = null) : Route { override val key = "item:$itemId" }
    data class Collection(val collection: WrenCollection) : Route { override val key = "collection:${collection.id}" }
    data class GenreItems(val genre: String, val libraryId: String? = null) : Route { override val key = "genre:$genre:$libraryId" }
    data class StreamTitle(val tmdbId: Int, val mediaType: String, val title: String) : Route { override val key = "stream:$mediaType:$tmdbId" }
    data class Trailer(val youtubeKey: String, val title: String) : Route { override val key = "trailer:$youtubeKey" }
    data class Player(val target: PlayTarget) : Route { override val key = "player:${target.itemId}" }
    /** Live TV, full screen, starting on a channel; up and down change it. */
    data class LivePlayer(val channelId: String) : Route { override val key = "live-player" }
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
