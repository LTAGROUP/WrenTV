package com.wrentv.tv

import android.app.Application
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.lifecycle.lifecycleScope
import coil3.ImageLoader
import coil3.SingletonImageLoader
import coil3.disk.DiskCache
import coil3.memory.MemoryCache
import coil3.request.crossfade
import com.wrentv.tv.data.AppState
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
        setContent { WrenApp(state) }
    }

    override fun onResume() {
        super.onResume()
        // Streaming or this user's limits may have changed while away.
        lifecycleScope.launch { state.refreshStatus() }
    }
}
