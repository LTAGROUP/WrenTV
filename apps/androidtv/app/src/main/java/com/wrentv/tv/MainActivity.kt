package com.wrentv.tv

import android.app.Application
import android.app.SearchManager
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.view.KeyEvent
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.lifecycle.lifecycleScope
import coil3.ImageLoader
import coil3.SingletonImageLoader
import coil3.disk.DiskCache
import coil3.memory.MemoryCache
import coil3.request.crossfade
import com.wrentv.tv.data.AppState
import com.wrentv.tv.player.ThemeMusic
import com.wrentv.tv.ui.Idle
import com.wrentv.tv.ui.UiSounds
import com.wrentv.tv.ui.WrenApp
import kotlinx.coroutines.launch
import okio.Path.Companion.toOkioPath

class WrenTVApplication : Application(), SingletonImageLoader.Factory {
    /** One per process, so playback, downloads and sign-in outlive the activity. */
    val state: AppState by lazy { AppState(this) }

    override fun newImageLoader(context: android.content.Context): ImageLoader = ImageLoader.Builder(context)
        .crossfade(true)
        .memoryCache { MemoryCache.Builder().maxSizePercent(context, 0.25).build() }
        .diskCache { DiskCache.Builder().directory(cacheDir.resolve("images").toOkioPath()).maxSizeBytes(512L shl 20).build() }
        .build()
}

class MainActivity : ComponentActivity() {
    private val state get() = (application as WrenTVApplication).state

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        UiSounds.load(this)
        setContent { WrenApp(state) }
        if (savedInstanceState == null) handle(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handle(intent)
    }

    /** Cards on the TV's Play Next row, its search results, and "search WrenTV for …". */
    private fun handle(intent: Intent?) {
        intent ?: return
        when (intent.action) {
            Intent.ACTION_VIEW -> intent.data?.takeIf { it.scheme == "wrentv" }?.let { state.link = it }
            Intent.ACTION_SEARCH, "com.google.android.gms.actions.SEARCH_ACTION" -> {
                // A suggestion picked from the system's search carries its own link.
                intent.data?.takeIf { it.scheme == "wrentv" }?.let { state.link = it; return }
                val q = intent.getStringExtra(SearchManager.QUERY) ?: intent.getStringExtra("query")
                if (!q.isNullOrBlank()) state.link = Uri.parse("wrentv://search").buildUpon().appendQueryParameter("q", q).build()
            }
        }
    }

    /** Clicks for keys the app acted on: no sound when focus is already at an edge. */
    override fun dispatchKeyEvent(event: KeyEvent): Boolean {
        if (Idle.onKey(event.action == KeyEvent.ACTION_DOWN)) return true
        val handled = super.dispatchKeyEvent(event)
        if (!handled) return false
        UiSounds.enabled = state.uiSounds
        when (event.keyCode) {
            KeyEvent.KEYCODE_DPAD_UP, KeyEvent.KEYCODE_DPAD_DOWN, KeyEvent.KEYCODE_DPAD_LEFT, KeyEvent.KEYCODE_DPAD_RIGHT ->
                if (event.action == KeyEvent.ACTION_DOWN) UiSounds.play(UiSounds.Kind.Move)
            KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_NUMPAD_ENTER ->
                if (event.action == KeyEvent.ACTION_DOWN && event.repeatCount == 0) UiSounds.play(UiSounds.Kind.Select)
            KeyEvent.KEYCODE_BACK ->
                if (event.action == KeyEvent.ACTION_UP && !event.isCanceled) UiSounds.play(UiSounds.Kind.Back)
        }
        return true
    }

    override fun onStop() {
        super.onStop()
        ThemeMusic.stop()
    }

    override fun onResume() {
        super.onResume()
        // Streaming or this user's limits may have changed while away.
        lifecycleScope.launch { state.refreshStatus() }
    }
}
