package com.wrentv.tv.ui

import android.content.Context
import android.graphics.Color as AColor
import androidx.compose.animation.Crossfade
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.BlendMode
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.CompositingStrategy
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import coil3.SingletonImageLoader
import coil3.compose.AsyncImage
import coil3.request.ImageRequest
import coil3.request.SuccessResult
import coil3.request.allowHardware
import coil3.toBitmap
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.collectLatest
import com.wrentv.tv.ui.screens.HeroEntry

/**
 * The focused title's artwork, shown behind Home, Collections and library
 * pages like Plex's cinematic backdrop. Cards report their backdrop when
 * they take focus; the page shows it for the route it came from.
 */
class AmbientState {
    var route by mutableStateOf<String?>(null)
        private set
    var path by mutableStateOf<String?>(null)
        private set

    /** The focused title, for a cinematic page's hero. Unlike the art, it changes at once. */
    var entry by mutableStateOf<HeroEntry?>(null)
        private set

    /** An image the page takes only its colour from, while no title's art is showing. */
    var wash by mutableStateOf<String?>(null)
        private set

    fun show(route: String, backdrop: String?, entry: HeroEntry? = null) {
        if (route != this.route) wash = null
        if (entry != null) {
            this.route = route
            this.entry = entry
        }
        if (backdrop.isNullOrEmpty()) return
        this.route = route
        path = backdrop
    }

    /** No artwork: just a soft colour taken from [wash], for a page's own top. */
    fun clear(route: String, entry: HeroEntry?, wash: String?) {
        this.route = route
        this.entry = entry
        this.wash = wash
        path = null
    }
}

val LocalAmbient = staticCompositionLocalOf { AmbientState() }

/** Makes a card's backdrop the page's while it has focus. */
@Composable
fun Modifier.ambient(backdrop: String?, entry: (() -> HeroEntry)? = null): Modifier {
    val ambient = LocalAmbient.current
    val route = LocalRouteKey.current
    return onFocusChanged { if (it.hasFocus) ambient.show(route, backdrop, entry?.invoke()) }
}

/**
 * The backdrop behind a page: the art at its own colours in the top right,
 * fading (not darkening) into a background tinted from the art, so rows
 * and titles on the left and bottom stay readable.
 */
@Composable
fun AmbientBackdrop(routeKey: String) {
    val ambient = LocalAmbient.current
    val api = LocalAppState.current.api
    val context = LocalContext.current
    var shown by remember(routeKey) { mutableStateOf<String?>(null) }
    // Skimming along a row shouldn't flash every poster's art.
    LaunchedEffect(routeKey) {
        snapshotFlow { if (ambient.route == routeKey) ambient.path else null }.collectLatest {
            if (it != null) {
                // Start fetching now, so the art is ready when the pause ends.
                api?.imageUrl(it, "w1280")?.let { u -> SingletonImageLoader.get(context).enqueue(ImageRequest.Builder(context).data(u).build()) }
                delay(260)
            }
            shown = it
        }
    }
    val url = shown?.let { api?.imageUrl(it, "w1280") }
    val wash = ambient.wash.takeIf { ambient.route == routeKey && url == null }?.let { api?.imageUrl(it, "w185") }
    var tint by remember { mutableStateOf<Color?>(null) }
    LaunchedEffect(url, wash) { tint = (url ?: wash)?.let { dominantColor(context, it) } ?: tint.takeIf { url != null || wash != null } }
    val base = Wren.background
    // A wash on its own stays fainter than art's: just a hint of colour.
    val bg by animateColorAsState(tint?.let { lerp(base, it, if (url != null) 0.42f else 0.26f) } ?: base, tween(700), label = "ambient")
    Box(Modifier.fillMaxSize().background(bg)) {
        Crossfade(url, animationSpec = tween(650), label = "backdrop") { u ->
            if (u == null) return@Crossfade
            Box(Modifier.fillMaxSize()) {
                AsyncImage(
                    model = u, contentDescription = null, contentScale = ContentScale.Crop, alignment = Alignment.TopEnd,
                    modifier = Modifier.align(Alignment.TopEnd).fillMaxWidth(0.74f).fillMaxHeight(0.8f)
                        .graphicsLayer { compositingStrategy = CompositingStrategy.Offscreen }
                        .drawWithContent {
                            drawContent()
                            // Fade the left and bottom edges out into the tint.
                            drawRect(Brush.horizontalGradient(0f to Color.Transparent, 0.45f to Color.Black), blendMode = BlendMode.DstIn)
                            drawRect(Brush.verticalGradient(0.5f to Color.Black, 1f to Color.Transparent), blendMode = BlendMode.DstIn)
                        },
                )
            }
        }
    }
}

private val tints = object : android.util.LruCache<String, Color>(200) {}

/** The art's main colour, favouring saturated pixels, kept dark enough for white text. */
private suspend fun dominantColor(context: Context, url: String): Color? = tints.get(url) ?: extractColor(context, url)?.also { tints.put(url, it) }

private suspend fun extractColor(context: Context, url: String): Color? = runCatching {
    val req = ImageRequest.Builder(context).data(url).size(32, 18).allowHardware(false).build()
    val bmp = (SingletonImageLoader.get(context).execute(req) as? SuccessResult)?.image?.toBitmap() ?: return null
    var r = 0.0; var g = 0.0; var b = 0.0; var w = 0.0
    val hsv = FloatArray(3)
    for (y in 0 until bmp.height) for (x in 0 until bmp.width) {
        val p = bmp.getPixel(x, y)
        AColor.colorToHSV(p, hsv)
        val k = hsv[1] * hsv[2] + 0.04
        r += AColor.red(p) * k; g += AColor.green(p) * k; b += AColor.blue(p) * k; w += k
    }
    if (w == 0.0) return null
    AColor.colorToHSV(AColor.rgb((r / w).toInt(), (g / w).toInt(), (b / w).toInt()), hsv)
    hsv[2] = hsv[2].coerceAtMost(0.62f)
    Color(AColor.HSVToColor(hsv))
}.getOrNull()
