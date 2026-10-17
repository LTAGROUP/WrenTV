package com.wrentv.tv.ui

import androidx.activity.compose.BackHandler
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.grid.LazyGridState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Search
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.foundation.focusGroup
import kotlinx.coroutines.delay
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.dp
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.Icon
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import com.wrentv.tv.R
import com.wrentv.tv.data.AppState
import com.wrentv.tv.player.LivePlayerScreen
import com.wrentv.tv.player.PlayerScreen
import com.wrentv.tv.ui.screens.CatalogScreen
import com.wrentv.tv.ui.screens.CollectionDetailScreen
import com.wrentv.tv.ui.screens.CollectionsScreen
import com.wrentv.tv.ui.screens.ConnectScreen
import com.wrentv.tv.ui.screens.DetailScreen
import com.wrentv.tv.ui.screens.DownloadsScreen
import com.wrentv.tv.ui.screens.GenreItemsScreen
import com.wrentv.tv.ui.screens.GenresScreen
import com.wrentv.tv.ui.screens.HomeScreen
import com.wrentv.tv.ui.screens.LibraryScreen
import com.wrentv.tv.ui.screens.LiveGroupScreen
import com.wrentv.tv.ui.screens.LiveTvScreen
import com.wrentv.tv.ui.screens.ManageLibrariesScreen
import com.wrentv.tv.ui.screens.SearchScreen
import com.wrentv.tv.ui.screens.ServerDownloadsScreen
import com.wrentv.tv.ui.screens.ServerSettingsScreen
import com.wrentv.tv.ui.screens.SettingsScreen
import com.wrentv.tv.ui.screens.StatsScreen
import com.wrentv.tv.ui.screens.StreamTitleScreen
import com.wrentv.tv.ui.screens.TrailerScreen
import com.wrentv.tv.ui.screens.UsersScreen

/** Whether the top bar shows: pages hide it once scrolled down. */
class TopBarState {
    var atTop by mutableStateOf(true)
    /** Focus is in the bar; pages scroll back to the top to meet it. */
    var focused by mutableStateOf(false)
}

val LocalTopBar = compositionLocalOf { TopBarState() }

/** Pages report their list's scroll so the top bar can step aside. */
@Composable
fun ReportScroll(list: LazyListState) {
    val bar = LocalTopBar.current
    LaunchedEffect(list) {
        snapshotFlow { list.firstVisibleItemIndex == 0 && list.firstVisibleItemScrollOffset < 40 }.collect { bar.atTop = it }
    }
    LaunchedEffect(list) { snapshotFlow { bar.focused }.collect { if (it) list.animateScrollToItem(0) } }
}

@Composable
fun ReportScroll(grid: LazyGridState) {
    val bar = LocalTopBar.current
    LaunchedEffect(grid) {
        snapshotFlow { grid.firstVisibleItemIndex == 0 && grid.firstVisibleItemScrollOffset < 40 }.collect { bar.atTop = it }
    }
    LaunchedEffect(grid) { snapshotFlow { bar.focused }.collect { if (it) grid.animateScrollToItem(0) } }
}

/** Room at the top of a page for the floating bar. */
val TopBarSpace = 96.dp

@Composable
fun WrenApp(state: AppState) {
    val nav = remember { Navigator() }
    val notices = remember { Notices() }
    WrenTheme {
        CompositionLocalProvider(LocalAppState provides state, LocalNavigator provides nav, LocalNotices provides notices) {
            Box(Modifier.fillMaxSize().background(Wren.background)) {
                when {
                    state.restoring -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { Spinner() }
                    state.user == null -> SignedOut(nav)
                    else -> Main(state, nav)
                }
                notices.Host()
            }
        }
    }
}

/** Before signing in, downloads still play. */
@Composable
private fun SignedOut(nav: Navigator) {
    val route = nav.stack.lastOrNull()
    BackHandler(enabled = route != null) { nav.back() }
    CompositionLocalProvider(LocalRouteKey provides (route?.key ?: "connect")) {
        when (route) {
            is Route.Player -> PlayerScreen(route.target)
            Route.Downloads -> DownloadsScreen()
            else -> ConnectScreen()
        }
    }
}

@Composable
private fun Main(state: AppState, nav: Navigator) {
    val holder = rememberSaveableStateHolder()
    val bar = remember { TopBarState() }
    val route = nav.current
    BackHandler(enabled = nav.stack.isNotEmpty() || nav.tab != Route.Home) { nav.back() }
    // A new page starts with the bar showing.
    LaunchedEffect(route) { bar.atTop = true }
    // Pages without a default focus (or still loading) would leave the first
    // remote press to land on the top bar; move focus into the page instead.
    val page = remember { FocusRequester() }
    var anyFocus by remember { mutableStateOf(false) }
    LaunchedEffect(route, anyFocus) {
        while (!anyFocus) {
            delay(400)
            runCatching { page.requestFocus() }
        }
    }
    CompositionLocalProvider(LocalTopBar provides bar, LocalRouteKey provides route.key) {
        Box(Modifier.fillMaxSize().onFocusChanged { anyFocus = it.hasFocus }) {
            Box(Modifier.fillMaxSize().focusRequester(page).focusGroup()) {
                holder.SaveableStateProvider(route.key) { Page(route) }
            }
            if (nav.atTop && route !is Route.Player && route !is Route.LivePlayer) {
                // Scrolled pages fade the bar out but keep it focusable, so Up
                // from the first row still reaches it (and brings it back).
                val shown by animateFloatAsState(if (bar.atTop || bar.focused) 1f else 0f, label = "topbar")
                Box(
                    Modifier.onFocusChanged { bar.focused = it.hasFocus }
                        .graphicsLayer { alpha = shown; translationY = -(1 - shown) * size.height / 2 },
                ) { TopBar(state, nav, page) }
            }
        }
    }
}

@Composable
private fun Page(route: Route) {
    when (route) {
        Route.Home -> HomeScreen()
        Route.Movies -> CatalogScreen("movie")
        Route.Shows -> CatalogScreen("show")
        Route.Collections -> CollectionsScreen()
        Route.Genres -> GenresScreen()
        Route.Search -> SearchScreen()
        Route.LiveTv -> LiveTvScreen()
        Route.Downloads -> DownloadsScreen()
        Route.Settings -> SettingsScreen()
        is Route.Library -> LibraryScreen(route.id)
        Route.Stats -> StatsScreen()
        Route.ServerDownloads -> ServerDownloadsScreen()
        Route.Users -> UsersScreen()
        Route.ManageLibraries -> ManageLibrariesScreen()
        Route.ServerSettings -> ServerSettingsScreen()
        is Route.Detail -> DetailScreen(route.itemId, route.seed)
        is Route.Collection -> CollectionDetailScreen(route.collection)
        is Route.GenreItems -> GenreItemsScreen(route.genre, route.libraryId)
        is Route.StreamTitle -> StreamTitleScreen(route)
        is Route.Trailer -> TrailerScreen(route.youtubeKey, route.title)
        is Route.Player -> PlayerScreen(route.target)
        is Route.LiveGroup -> LiveGroupScreen(route.group)
        is Route.LivePlayer -> LivePlayerScreen(route.channelId, route.group)
    }
}

/** The floating bar of pages along the top, like the TV app's. */
@Composable
private fun TopBar(state: AppState, nav: Navigator, page: FocusRequester) {
    val tabs = buildList {
        add(Route.Home to "Home")
        if (state.liveTv) add(Route.LiveTv to "Live TV")
        if (state.canStream) {
            // Everything plays, so there are no libraries to pick from: just movies and shows.
            add(Route.Movies to "Movies")
            add(Route.Shows to "TV Shows")
            add(Route.Collections to "Collections")
        } else {
            state.libraries.forEach { add(Route.Library(it.id) to it.name) }
            add(Route.Collections to "Collections")
            add(Route.Genres to "Genres")
        }
        if (state.downloads.items.isNotEmpty() || state.limits?.canDownload == true) add(Route.Downloads to "Downloads")
    }
    // Coming up from a page lands on the current tab, not whichever is nearest.
    val current = remember { FocusRequester() }
    fun mark(r: Route) = if (nav.tab == r) Modifier.focusRequester(current) else Modifier
    Box(
        Modifier.fillMaxWidth().height(TopBarSpace)
            .background(Brush.verticalGradient(0f to Color.Black.copy(alpha = 0.65f), 1f to Color.Transparent))
            .focusProperties { onEnter = { runCatching { current.requestFocus() } } }
            // The page sits behind the bar, so a plain search for something
            // below finds nothing: Down hands focus to the page outright.
            .onPreviewKeyEvent {
                it.type == KeyEventType.KeyDown && it.key == Key.DirectionDown && runCatching { page.requestFocus() }.getOrDefault(false)
            }
            .focusGroup(),
    ) {
        Image(
            painterResource(R.drawable.lockup), contentDescription = "Wren TV",
            modifier = Modifier.align(Alignment.CenterStart).padding(start = PageGutter).height(30.dp),
        )
        Row(
            Modifier.align(Alignment.Center).background(Color.White.copy(alpha = 0.08f), CircleShape).padding(5.dp),
            horizontalArrangement = Arrangement.spacedBy(4.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            TabItem(selected = nav.tab == Route.Search, onClick = { nav.select(Route.Search) }, key = "tab:search", modifier = mark(Route.Search)) {
                Icon(Icons.Rounded.Search, "Search", Modifier.size(22.dp))
            }
            tabs.forEach { (r, label) ->
                TabItem(selected = nav.tab == r, onClick = { nav.select(r) }, key = "tab:${r.key}", modifier = mark(r)) {
                    Text(label, style = MaterialTheme.typography.labelLarge, maxLines = 1)
                }
            }
        }
        val name = state.user?.displayName?.ifEmpty { state.user?.username } ?: ""
        TabItem(
            selected = nav.tab == Route.Settings, onClick = { nav.select(Route.Settings) }, key = "tab:settings",
            modifier = Modifier.align(Alignment.CenterEnd).padding(end = PageGutter).then(mark(Route.Settings)), circle = true,
        ) {
            Text(name.take(1).uppercase().ifEmpty { "?" }, style = MaterialTheme.typography.titleMedium)
        }
    }
}

@Composable
private fun TabItem(
    selected: Boolean,
    onClick: () -> Unit,
    key: String,
    modifier: Modifier = Modifier,
    circle: Boolean = false,
    content: @Composable () -> Unit,
) {
    val nav = LocalNavigator.current
    Surface(
        onClick = onClick,
        modifier = modifier.height(42.dp).then(if (circle) Modifier.width(42.dp) else Modifier)
            .onFocusChanged { if (it.isFocused) nav.focus["topbar"] = key },
        shape = ClickableSurfaceDefaults.shape(CircleShape),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = if (selected) Color.White.copy(alpha = 0.18f) else if (circle) Wren.ember else Color.Transparent,
            contentColor = if (selected || circle) Wren.text else Wren.secondary,
            focusedContainerColor = Color.White,
            focusedContentColor = Color.Black,
        ),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1.06f),
    ) {
        Box(Modifier.fillMaxHeight().then(if (circle) Modifier.fillMaxWidth() else Modifier.padding(horizontal = 18.dp)), contentAlignment = Alignment.Center) { content() }
    }
}
