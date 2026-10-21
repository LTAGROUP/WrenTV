package com.wrentv.tv.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.CloudOff
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.wrentv.tv.data.Item
import com.wrentv.tv.player.TrailerWebView
import com.wrentv.tv.ui.EmptyState
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.PillButton
import com.wrentv.tv.ui.Route
import com.wrentv.tv.ui.Spinner
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.rememberFocus
import com.wrentv.tv.ui.rememberLoaded

/**
 * A TMDB title that isn't on the server yet: adding it to the streaming
 * library gives it an item, and the page becomes that item's detail page.
 */
@Composable
fun StreamTitleScreen(route: Route.StreamTitle) {
    val state = LocalAppState.current
    var attempt by remember { mutableStateOf(0) }
    val added = rememberLoaded<Item>("stream:${route.mediaType}:${route.tmdbId}", attempt) {
        state.api?.addStreamTitle(route.tmdbId, route.mediaType)
    }
    val item = added.value
    LaunchedEffect(item?.id) {
        // The title may have created the streaming library.
        if (item != null && state.libraries.none { it.id == item.libraryId }) runCatching { state.reloadLibraries() }
    }
    when {
        item != null -> DetailScreen(item.id, item)
        added.error != null -> Column(Modifier.fillMaxSize(), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
            EmptyState("Couldn't open ${route.title}", added.error, icon = Icons.Rounded.CloudOff)
            PillButton("Try Again", { added.error = null; attempt++ }, Modifier.padding(top = 20.dp).rememberFocus("stream:retry", default = true), primary = true)
        }
        else -> Column(Modifier.fillMaxSize(), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
            Spinner()
            Text("Opening ${route.title}…", style = MaterialTheme.typography.titleMedium, color = Wren.secondary, modifier = Modifier.padding(top = 16.dp))
        }
    }
}

/** Plays a YouTube trailer inside the app; Back returns to the title. */
@Composable
fun TrailerScreen(youtubeKey: String, title: String) {
    val state = LocalAppState.current
    val server = state.api?.baseUrl
    var view by remember { mutableStateOf<TrailerWebView?>(null) }
    DisposableEffect(youtubeKey) { onDispose { view?.release() } }
    Box(Modifier.fillMaxSize().background(Color.Black)) {
        AndroidView(
            factory = { ctx -> TrailerWebView(ctx, youtubeKey, server).also { it.contentDescription = title; view = it; it.requestFocus() } },
            modifier = Modifier.fillMaxSize(),
        )
    }
}
