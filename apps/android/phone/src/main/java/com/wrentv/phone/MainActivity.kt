package com.wrentv.phone

import android.app.Application
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.lifecycle.lifecycleScope
import coil3.ImageLoader
import coil3.SingletonImageLoader
import coil3.disk.DiskCache
import coil3.memory.MemoryCache
import coil3.request.crossfade
import com.wrentv.phone.ui.WrenApp
import com.wrentv.tv.data.AppState
import com.wrentv.tv.data.Client
import kotlinx.coroutines.launch
import okio.Path.Companion.toOkioPath

class WrenApplication : Application(), SingletonImageLoader.Factory {
    /** One per process, so playback, downloads and sign-in outlive the activity. */
    val state: AppState by lazy { AppState(this) }

    override fun onCreate() {
        super.onCreate()
        Client.name = "Wren TV for Android"
        Client.platform = "android"
        Client.fallbackDevice = "Android"
    }

    override fun newImageLoader(context: android.content.Context): ImageLoader = ImageLoader.Builder(context)
        .crossfade(true)
        .memoryCache { MemoryCache.Builder().maxSizePercent(context, 0.25).build() }
        .diskCache { DiskCache.Builder().directory(cacheDir.resolve("images").toOkioPath()).maxSizeBytes(256L shl 20).build() }
        .build()
}

class MainActivity : ComponentActivity() {
    private val state get() = (application as WrenApplication).state

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.dark(android.graphics.Color.TRANSPARENT),
            navigationBarStyle = SystemBarStyle.dark(android.graphics.Color.TRANSPARENT),
        )
        super.onCreate(savedInstanceState)
        setContent { WrenApp(state) }
    }

    override fun onResume() {
        super.onResume()
        // Streaming or this user's limits may have changed while away.
        lifecycleScope.launch { state.refreshStatus() }
    }
}
