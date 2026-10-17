package com.wrentv.tv.ui

import android.os.SystemClock
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.Crossfade
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.animation.core.Animatable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import coil3.compose.AsyncImage
import com.wrentv.tv.data.DiscoverHome
import com.wrentv.tv.data.HomeResponse
import com.wrentv.tv.data.TrendingResponse
import com.wrentv.tv.ui.screens.HeroEntry
import com.wrentv.tv.ui.screens.heroEntry
import kotlinx.coroutines.delay
import java.text.DateFormat
import java.util.Date

/** When the remote was last touched, and whether the screensaver is up. */
object Idle {
    const val AFTER_MS = 4 * 60_000L
    var lastKey by mutableLongStateOf(SystemClock.uptimeMillis())
    var showing by mutableStateOf(false)
    /** The key that woke the screensaver isn't passed on, nor is its release. */
    private var swallowUp = false

    /** True when the key was used to wake up and should go no further. */
    fun onKey(down: Boolean): Boolean {
        lastKey = SystemClock.uptimeMillis()
        if (showing) {
            showing = false
            swallowUp = true
            return true
        }
        if (!down && swallowUp) {
            swallowUp = false
            return true
        }
        if (down) swallowUp = false
        return false
    }
}

/**
 * After a few idle minutes away from playback, the library's backdrops drift
 * past one at a time. Any key brings the page back as it was.
 */
@Composable
fun Screensaver(allowed: Boolean) {
    LaunchedEffect(allowed) {
        if (!allowed) { Idle.showing = false; return@LaunchedEffect }
        while (true) {
            delay(5_000)
            if (!Idle.showing && SystemClock.uptimeMillis() - Idle.lastKey > Idle.AFTER_MS) Idle.showing = true
        }
    }
    AnimatedVisibility(Idle.showing, enter = fadeIn(tween(1500)), exit = fadeOut(tween(300))) { Slides() }
}

@Composable
private fun Slides() {
    val state = LocalAppState.current
    val slides = remember { pool() }
    var index by remember { mutableIntStateOf(0) }
    LaunchedEffect(slides) {
        if (slides.size < 2) return@LaunchedEffect
        while (true) {
            delay(14_000)
            index = (index + 1) % slides.size
        }
    }
    Box(Modifier.fillMaxSize().background(Color.Black)) {
        Crossfade(slides.getOrNull(index), animationSpec = tween(2500), label = "screensaver") { e ->
            if (e == null) return@Crossfade
            // A slow push in, so the picture isn't still.
            val zoom = remember { Animatable(1f) }
            LaunchedEffect(Unit) { zoom.animateTo(1.08f, tween(17_000, easing = LinearEasing)) }
            Box(Modifier.fillMaxSize()) {
                AsyncImage(
                    state.api?.imageUrl(e.backdrop, "w1280"), null,
                    Modifier.fillMaxSize().graphicsLayer { scaleX = zoom.value; scaleY = zoom.value },
                    contentScale = ContentScale.Crop,
                )
                Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0.55f to Color.Transparent, 1f to Color.Black.copy(alpha = 0.75f))))
                Column(Modifier.align(Alignment.BottomStart).padding(start = 64.dp, bottom = 56.dp)) {
                    if (e.logo != null) {
                        AsyncImage(state.api?.imageUrl(e.logo, "w500"), e.title, Modifier.height(72.dp).widthIn(max = 360.dp), alignment = Alignment.BottomStart)
                    } else {
                        Text(e.title, style = MaterialTheme.typography.headlineMedium, color = Color.White)
                    }
                    if (e.meta.isNotEmpty()) {
                        Text(e.meta.take(3).joinToString("  ·  "), Modifier.padding(top = 8.dp), color = Color.White.copy(alpha = 0.7f), fontSize = 14.sp)
                    }
                }
            }
        }
        Clock(Modifier.align(Alignment.BottomEnd).padding(end = 64.dp, bottom = 56.dp))
    }
}

@Composable
private fun Clock(modifier: Modifier) {
    var now by remember { mutableStateOf(Date()) }
    LaunchedEffect(Unit) {
        while (true) {
            now = Date()
            delay(60_000 - System.currentTimeMillis() % 60_000)
        }
    }
    Column(modifier, horizontalAlignment = Alignment.End, verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Text(DateFormat.getTimeInstance(DateFormat.SHORT).format(now), color = Color.White, fontSize = 40.sp)
        Text(DateFormat.getDateInstance(DateFormat.FULL).format(now), color = Color.White.copy(alpha = 0.6f), fontSize = 14.sp)
    }
}

/** Whatever the home pages have already loaded, with artwork, in a new order each time. */
private fun pool(): List<HeroEntry> {
    val out = mutableListOf<HeroEntry>()
    for (v in PageCache.values.values) {
        when (v) {
            is DiscoverHome -> {
                out += v.hero.map { it.heroEntry() }
                out += v.continueWatching.filter { it.kind != "episode" }.map { it.heroEntry() }
                out += v.rows.flatMap { r -> r.items.map { it.heroEntry() } }
            }
            is HomeResponse -> {
                out += v.topRated.orEmpty().map { it.heroEntry() }
                out += v.recentlyAdded.flatMap { s -> s.items.map { it.heroEntry() } }
            }
            is TrendingResponse -> out += v.items.map { it.heroEntry() }
        }
    }
    return out.filter { it.backdrop != null }.distinctBy { it.id }.shuffled().take(40)
}
