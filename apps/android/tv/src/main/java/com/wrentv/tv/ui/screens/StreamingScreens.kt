package com.wrentv.tv.ui.screens

import androidx.compose.foundation.background
import com.wrentv.tv.ui.RemoteImage
import com.wrentv.tv.ui.PageGutter
import com.wrentv.tv.data.TitleView
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.graphics.Brush
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.Row
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
import com.wrentv.tv.data.WrenTVException
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
        val api = state.api
        // One quiet retry first: a timeout or a server hiccup usually clears,
        // while a refusal (can't stream, unknown title) is shown as it is.
        try {
            api?.addStreamTitle(route.tmdbId, route.mediaType)
        } catch (e: WrenTVException) {
            if (e.status in 400..499) throw e
            api?.addStreamTitle(route.tmdbId, route.mediaType)
        } catch (e: java.io.IOException) {
            api?.addStreamTitle(route.tmdbId, route.mediaType)
        }
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
        route.seed != null -> OpeningPreview(route.seed)
        else -> Column(Modifier.fillMaxSize(), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
            Spinner()
            Text("Opening ${route.title}…", style = MaterialTheme.typography.titleMedium, color = Wren.secondary, modifier = Modifier.padding(top = 16.dp))
        }
    }
}

/**
 * What the title's card already knew, laid out like its detail page, while
 * the server adds it; the full page takes over as soon as it's there.
 */
@Composable
private fun OpeningPreview(t: TitleView) {
    val state = LocalAppState.current
    Box(Modifier.fillMaxSize()) {
        RemoteImage(state.api?.imageUrl(t.backdropPath, "w1280"), Modifier.fillMaxSize())
        Box(
            Modifier.fillMaxSize()
                .background(Brush.verticalGradient(0f to Color.Transparent, 0.45f to Color.Black.copy(alpha = 0.55f), 0.8f to Wren.background))
                .background(Brush.horizontalGradient(0f to Color.Black.copy(alpha = 0.85f), 0.6f to Color.Transparent)),
        )
        Column(Modifier.padding(start = PageGutter, end = PageGutter, top = 150.dp).width(820.dp)) {
            Text(t.title, style = MaterialTheme.typography.displaySmall.copy(fontWeight = FontWeight.Bold), color = Wren.text,
                maxLines = 2, overflow = TextOverflow.Ellipsis)
            Spacer(Modifier.height(14.dp))
            MetaLine(listOfNotNull(t.year?.toString(), if (t.mediaType == "show") "Series" else "Film", t.rating?.takeIf { it > 0 }?.let { "★ %.1f".format(it) }))
            t.overview?.takeIf { it.isNotBlank() }?.let {
                Text(it, style = MaterialTheme.typography.bodyLarge, color = Color.White.copy(alpha = 0.85f), maxLines = 4,
                    overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 14.dp).widthIn(max = 720.dp))
            }
            Row(Modifier.padding(top = 24.dp), verticalAlignment = Alignment.CenterVertically) {
                Spinner(22.dp)
                Text("Getting it ready…", style = MaterialTheme.typography.bodyMedium, color = Wren.secondary, modifier = Modifier.padding(start = 12.dp))
            }
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
