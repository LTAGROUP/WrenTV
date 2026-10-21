package com.wrentv.phone.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.ArrowCircleDown
import androidx.compose.material.icons.rounded.Home
import androidx.compose.material.icons.rounded.LiveTv
import androidx.compose.material.icons.rounded.Menu
import androidx.compose.material.icons.rounded.Movie
import androidx.compose.material.icons.rounded.Tv
import androidx.compose.material.icons.rounded.VideoLibrary
import androidx.compose.material3.Icon
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.NavigationBarItemDefaults
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import com.wrentv.phone.player.LivePlayerScreen
import com.wrentv.phone.player.PlayerScreen
import com.wrentv.tv.data.AppState

@Composable
fun WrenApp(state: AppState) {
    val nav = remember { Navigator() }
    WrenTheme {
        CompositionLocalProvider(LocalAppState provides state, LocalNavigator provides nav) {
            Box(Modifier.fillMaxSize().background(Wren.background)) {
                when {
                    state.restoring -> Loading()
                    state.user == null -> ConnectScreen()
                    else -> MainScreen()
                }
                nav.playing?.let { PlayerScreen(it) }
                nav.live?.let { LivePlayerScreen(it) }
            }
            // Signing out or switching servers starts over at Home.
            LaunchedEffect(state.user?.id, state.server?.machineId) { if (state.user == null) nav.reset() }
        }
    }
}

/** The tabs this user gets, like the iPhone app: streaming servers browse all of TMDB. */
@Composable
private fun tabs(state: AppState): List<Tab> {
    val list = mutableListOf(Tab.Home)
    if (state.canStream) list += listOf(Tab.Movies, Tab.Shows) else list += Tab.Library
    if (state.liveTv) list += Tab.Live
    if (state.downloads.items.isNotEmpty() || state.limits?.canDownload == true) list += Tab.Downloads
    list += Tab.More
    // A phone's bar holds five; Downloads is under More too.
    if (list.size > 5) list -= Tab.Downloads
    return list
}

private fun icon(t: Tab): ImageVector = when (t) {
    Tab.Home -> Icons.Rounded.Home
    Tab.Movies -> Icons.Rounded.Movie
    Tab.Shows -> Icons.Rounded.Tv
    Tab.Library -> Icons.Rounded.VideoLibrary
    Tab.Live -> Icons.Rounded.LiveTv
    Tab.Downloads -> Icons.Rounded.ArrowCircleDown
    Tab.More -> Icons.Rounded.Menu
}

@Composable
private fun MainScreen() {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val tabs = tabs(state)
    LaunchedEffect(tabs) { if (nav.tab !in tabs) nav.tab = Tab.Home }
    val saver = rememberSaveableStateHolder()

    BackHandler(nav.playing == null && nav.live == null && (nav.stack().isNotEmpty() || nav.tab != Tab.Home)) {
        if (!nav.back()) nav.tab = Tab.Home
    }

    Scaffold(
        containerColor = Wren.background,
        bottomBar = {
            NavigationBar(containerColor = Wren.surface) {
                tabs.forEach { t ->
                    NavigationBarItem(
                        selected = nav.tab == t,
                        onClick = { nav.select(t) },
                        icon = { Icon(icon(t), null) },
                        label = { Text(t.label, maxLines = 1) },
                        colors = NavigationBarItemDefaults.colors(
                            selectedIconColor = Color.White, selectedTextColor = Color.White,
                            indicatorColor = Wren.ember.copy(alpha = 0.35f),
                            unselectedIconColor = Wren.secondary, unselectedTextColor = Wren.secondary,
                        ),
                    )
                }
            }
        },
    ) { pad ->
        Box(Modifier.fillMaxSize().padding(bottom = pad.calculateBottomPadding())) {
            val top = nav.stack().lastOrNull()
            val key = "${nav.tab}/${nav.stack().size}"
            saver.SaveableStateProvider(key) {
                if (top != null) RouteScreen(top) else TabRoot(nav.tab)
            }
        }
    }
}

@Composable
private fun TabRoot(t: Tab) {
    when (t) {
        Tab.Home -> LayoutPageScreen("home", "Home", root = true) { HomeScreen() }
        Tab.Movies -> LayoutPageScreen("movies", "Movies", root = true, browseAll = Route.Catalog("movie")) { CatalogScreen("movie") }
        Tab.Shows -> LayoutPageScreen("shows", "TV Shows", root = true, browseAll = Route.Catalog("show")) { CatalogScreen("show") }
        Tab.Library -> LibrariesScreen()
        Tab.Live -> LiveTvScreen()
        Tab.Downloads -> DownloadsScreen()
        Tab.More -> MoreScreen()
    }
}

@Composable
fun RouteScreen(r: Route) {
    when (r) {
        is Route.Detail -> DetailScreen(r.id, r.seed)
        is Route.StreamTitle -> StreamTitleScreen(r)
        is Route.LibraryItems -> LibraryScreen(r.library)
        is Route.Collection -> CollectionScreen(r.id, r.name)
        is Route.Genre -> GenreScreen(r.genre, r.libraryId)
        Route.Collections -> CollectionsScreen()
        Route.Calendar -> CalendarScreen()
        is Route.Genres -> GenresScreen(r.libraryId)
        Route.Watchlist -> WatchlistScreen()
        Route.Downloads -> DownloadsScreen()
        Route.LiveTv -> LiveTvScreen()
        Route.Preferences -> PreferencesScreen()
        Route.LinkTv -> LinkTvScreen()
        Route.Search -> SearchScreen()
        is Route.LibraryPage -> LayoutPageScreen("library:${r.library.id}", r.library.name, root = false, browseAll = Route.LibraryItems(r.library)) { LibraryScreen(r.library) }
        is Route.Catalog -> CatalogScreen(r.type)
        is Route.Hub -> HubScreen(r.id, r.name)
        is Route.Person -> PersonScreen(r.id, r.name)
        Route.Layouts -> LayoutsScreen()
        is Route.LayoutEditor -> LayoutEditorScreen(r.scope, r.title)
    }
}
