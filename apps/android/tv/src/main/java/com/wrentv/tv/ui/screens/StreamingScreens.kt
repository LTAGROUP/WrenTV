package com.wrentv.tv.ui.screens

import android.annotation.SuppressLint
import android.content.ActivityNotFoundException
import android.content.Intent
import android.net.Uri
import android.webkit.WebChromeClient
import android.webkit.WebView
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.wrentv.tv.data.Item
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

/**
 * Plays a YouTube trailer in the YouTube app when the TV has it, otherwise
 * in an embedded player.
 */
@Composable
fun TrailerScreen(youtubeKey: String, title: String) {
    val context = LocalContext.current
    val nav = LocalNavigator.current
    var embedded by remember { mutableStateOf(false) }
    LaunchedEffect(youtubeKey) {
        try {
            context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("vnd.youtube:$youtubeKey")).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            // The YouTube app took over; coming back returns to the title.
            nav.back()
        } catch (_: ActivityNotFoundException) {
            embedded = true
        }
    }
    Box(Modifier.fillMaxSize().background(Color.Black)) {
        if (embedded) YouTubeEmbed(youtubeKey, title)
    }
}

@SuppressLint("SetJavaScriptEnabled")
@Composable
private fun YouTubeEmbed(key: String, title: String) {
    val html = """
        <html><head><meta name="viewport" content="width=device-width, initial-scale=1">
        <style>html,body{margin:0;height:100%;background:#000}iframe{border:0;width:100%;height:100%}</style></head>
        <body><iframe title="${title.replace("\"", "&quot;")}" src="https://www.youtube-nocookie.com/embed/$key?autoplay=1&playsinline=1&rel=0"
        allow="autoplay; encrypted-media; fullscreen" allowfullscreen></iframe></body></html>
    """.trimIndent()
    var view by remember { mutableStateOf<WebView?>(null) }
    DisposableEffect(Unit) { onDispose { view?.destroy() } }
    AndroidView(
        factory = { ctx ->
            WebView(ctx).apply {
                settings.javaScriptEnabled = true
                settings.mediaPlaybackRequiresUserGesture = false
                webChromeClient = WebChromeClient()
                setBackgroundColor(android.graphics.Color.BLACK)
                loadDataWithBaseURL("https://www.youtube-nocookie.com", html, "text/html", "utf-8", null)
                requestFocus()
                view = this
            }
        },
        modifier = Modifier.fillMaxSize(),
    )
}
