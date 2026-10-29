package com.wrentv.phone.player

import android.app.PendingIntent
import android.app.PictureInPictureParams
import android.app.RemoteAction
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.graphics.drawable.Icon
import android.os.Build
import android.util.Rational
import androidx.activity.ComponentActivity
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.platform.LocalContext
import androidx.core.app.PictureInPictureModeChangedInfo
import androidx.core.content.ContextCompat
import androidx.core.util.Consumer
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.media3.common.Player
import androidx.media3.common.VideoSize

/**
 * Picture in picture for the player that's up: leaving the app while it plays
 * (home, the recents gesture, or the button in the controls) carries on in a
 * floating window with a play/pause button, and expanding it comes back to the
 * same player where it was.
 */
object Pip {
    private const val TOGGLE = "com.wrentv.phone.PIP_TOGGLE"

    /** What to enter with; set while a player is up. */
    internal var params: PictureInPictureParams? = null

    /** Leaving the app enters the window only while something is playing. */
    internal var playing = false

    /** Where the video sits on screen, so the window grows out of the picture. */
    internal var surface by mutableStateOf<Rect?>(null)

    fun supported(context: Context) = context.packageManager.hasSystemFeature(PackageManager.FEATURE_PICTURE_IN_PICTURE)

    /** Before Android 12 there's no auto-enter: the activity enters as the user leaves. */
    fun onUserLeaveHint(activity: ComponentActivity) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S || !playing) return
        params?.let { runCatching { activity.enterPictureInPictureMode(it) } }
    }

    fun enter(activity: ComponentActivity) {
        params?.let { runCatching { activity.enterPictureInPictureMode(it) } }
    }

    internal fun build(context: Context, size: VideoSize, paused: Boolean, autoEnter: Boolean): PictureInPictureParams {
        val b = PictureInPictureParams.Builder()
        val ratio = aspect(size)
        ratio?.let(b::setAspectRatio)
        surface?.let { hint(it, ratio) }?.let { b.setSourceRectHint(it) }
        val intent = PendingIntent.getBroadcast(
            context, 0, Intent(TOGGLE).setPackage(context.packageName),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val icon = Icon.createWithResource(context, if (paused) android.R.drawable.ic_media_play else android.R.drawable.ic_media_pause)
        val label = if (paused) "Play" else "Pause"
        b.setActions(listOf(RemoteAction(icon, label, label, intent)))
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            b.setAutoEnterEnabled(autoEnter)
            // Video: a resize redraws the frame rather than cross-fading it.
            b.setSeamlessResizeEnabled(false)
        }
        return b.build()
    }

    /** The video's shape, inside the range the window allows (about 1:2.39 to 2.39:1). */
    private fun aspect(size: VideoSize): Rational? {
        if (size.width <= 0 || size.height <= 0) return null
        val w = (size.width * size.pixelWidthHeightRatio).toInt().coerceAtLeast(1)
        val ratio = (w.toFloat() / size.height).coerceIn(1 / 2.39f, 2.39f)
        return Rational((ratio * 1000).toInt(), 1000)
    }

    /** The picture inside [area], letterboxed the way the player fits it. */
    private fun hint(area: Rect, ratio: Rational?): android.graphics.Rect? {
        if (area.width <= 0f || area.height <= 0f) return null
        val r = ratio?.toFloat() ?: (area.width / area.height)
        val w = minOf(area.width, area.height * r)
        val h = w / r
        val left = area.left + (area.width - w) / 2
        val top = area.top + (area.height - h) / 2
        return android.graphics.Rect(left.toInt(), top.toInt(), (left + w).toInt(), (top + h).toInt())
    }

    internal fun receiver(context: Context, onToggle: () -> Unit): () -> Unit {
        val r = object : BroadcastReceiver() {
            override fun onReceive(context: Context, intent: Intent) = onToggle()
        }
        ContextCompat.registerReceiver(context, r, IntentFilter(TOGGLE), ContextCompat.RECEIVER_NOT_EXPORTED)
        return { context.unregisterReceiver(r) }
    }
}

/**
 * Offers picture in picture while this player is up and returns whether it's
 * showing in the floating window, so the controls can step aside. [onDismissed]
 * runs when the user closes the window rather than expanding it.
 */
@Composable
fun rememberPictureInPicture(player: Player, paused: Boolean, togglePause: () -> Unit, onDismissed: () -> Unit): Boolean {
    val activity = LocalContext.current.activity() as? ComponentActivity ?: return false
    var inPip by remember { mutableStateOf(activity.isInPictureInPictureMode) }
    if (!remember { Pip.supported(activity) }) return false
    val toggle by rememberUpdatedState(togglePause)
    val dismissed by rememberUpdatedState(onDismissed)
    var size by remember { mutableStateOf(player.videoSize) }

    DisposableEffect(player) {
        val listener = object : Player.Listener {
            override fun onVideoSizeChanged(videoSize: VideoSize) { size = videoSize }
        }
        player.addListener(listener)
        onDispose { player.removeListener(listener) }
    }

    // Kept current with the video's shape and play/pause; auto-enter only while playing.
    LaunchedEffect(size, paused, Pip.surface) {
        val p = Pip.build(activity, size, paused, autoEnter = !paused)
        Pip.params = p
        Pip.playing = !paused
        runCatching { activity.setPictureInPictureParams(p) }
    }
    DisposableEffect(Unit) {
        onDispose {
            Pip.params = null
            Pip.playing = false
            Pip.surface = null
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                runCatching { activity.setPictureInPictureParams(PictureInPictureParams.Builder().setAutoEnterEnabled(false).build()) }
            }
        }
    }

    DisposableEffect(activity) {
        val unregister = Pip.receiver(activity) { toggle() }
        // In the window the activity stays started; it only stops once the window
        // is closed. Expanding resumes it instead.
        var wasPip = activity.isInPictureInPictureMode
        val modes = Consumer<PictureInPictureModeChangedInfo> {
            inPip = it.isInPictureInPictureMode
            if (it.isInPictureInPictureMode) wasPip = true
        }
        val life = LifecycleEventObserver { _, e ->
            when (e) {
                Lifecycle.Event.ON_RESUME -> wasPip = false
                Lifecycle.Event.ON_STOP -> if (wasPip) { wasPip = false; dismissed() }
                else -> {}
            }
        }
        activity.addOnPictureInPictureModeChangedListener(modes)
        activity.lifecycle.addObserver(life)
        onDispose {
            unregister()
            activity.removeOnPictureInPictureModeChangedListener(modes)
            activity.lifecycle.removeObserver(life)
        }
    }
    return inPip
}
