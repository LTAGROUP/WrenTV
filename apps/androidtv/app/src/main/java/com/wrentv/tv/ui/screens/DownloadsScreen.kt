package com.wrentv.tv.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Download
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import androidx.compose.ui.layout.ContentScale
import com.wrentv.tv.data.OfflineItem
import com.wrentv.tv.ui.Bar
import com.wrentv.tv.ui.EmptyState
import com.wrentv.tv.ui.ListRow
import com.wrentv.tv.ui.LocalAppState
import com.wrentv.tv.ui.LocalNavigator
import com.wrentv.tv.ui.PageGutter
import com.wrentv.tv.ui.PageHeader
import com.wrentv.tv.ui.Panel
import com.wrentv.tv.ui.PlayTarget
import com.wrentv.tv.ui.ReportScroll
import com.wrentv.tv.ui.TopBarSpace
import com.wrentv.tv.ui.Wren
import com.wrentv.tv.ui.formatBytes
import com.wrentv.tv.ui.formatDuration
import com.wrentv.tv.ui.rememberFocus

/** Movies and episodes saved on this TV. Works without signing in. */
@Composable
fun DownloadsScreen() {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val downloads = state.downloads
    val list = rememberLazyListState()
    ReportScroll(list)
    var open by remember { mutableStateOf<OfflineItem?>(null) }
    val items = downloads.items.sortedWith(compareBy<OfflineItem> { it.status == OfflineItem.Status.DONE }.thenByDescending { it.addedAt })

    fun play(x: OfflineItem) {
        val f = downloads.localFile(x) ?: return
        nav.play(PlayTarget(x.id, x.displayTitle, x.subtitle, localFile = f.absolutePath, localPosition = x.position))
    }

    LazyColumn(
        Modifier.fillMaxSize(), state = list,
        contentPadding = PaddingValues(top = TopBarSpace, bottom = 48.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        item("header") {
            val used = downloads.items.sumOf { if (it.status == OfflineItem.Status.DONE) it.size else it.received }
            PageHeader("Downloads", if (items.isEmpty()) null else "${items.size} saved on this TV · ${formatBytes(used)}")
        }
        if (items.isEmpty()) {
            item("empty") {
                EmptyState("No downloads", "Open a movie or episode and choose Download to watch it without a connection.", icon = Icons.Rounded.Download)
            }
        }
        items(items, key = { it.id }) { x ->
            val done = x.status == OfflineItem.Status.DONE
            ListRow(
                title = x.displayTitle,
                detail = listOfNotNull(x.subtitle, status(x)).joinToString(" · "),
                onClick = { open = x },
                modifier = Modifier.padding(horizontal = PageGutter).width(900.dp).rememberFocus("dl:${x.id}", default = x == items.first()),
                leading = {
                    Box(Modifier.width(54.dp).height(80.dp).clip(RoundedCornerShape(6.dp)).background(Wren.surfaceHigh)) {
                        downloads.posterFile(x)?.let { AsyncImage(it, null, Modifier.fillMaxSize(), contentScale = ContentScale.Crop) }
                    }
                },
                value = if (done) formatBytes(x.size) else null,
            )
            if (x.active || x.status == OfflineItem.Status.PAUSED) {
                Bar(x.fraction, Modifier.padding(start = PageGutter + 86.dp, bottom = 6.dp).width(800.dp))
            }
        }
    }

    open?.let { x -> DownloadActions(x, { open = null }, ::play) }
}

private fun status(x: OfflineItem): String? = when (x.status) {
    OfflineItem.Status.DONE -> when {
        x.duration > 0 && x.position > 30 && x.position < x.duration - 60 -> "${formatDuration(x.duration - x.position)} left"
        else -> x.runtimeMin?.takeIf { it > 0 }?.let { formatDuration(it * 60.0) }
    }
    OfflineItem.Status.DOWNLOADING -> if (x.size > 0) "${formatBytes(x.received)} of ${formatBytes(x.size)}" else "Downloading…"
    OfflineItem.Status.PREPARING -> x.message ?: "Preparing…"
    OfflineItem.Status.WAITING -> x.message ?: "Waiting"
    OfflineItem.Status.PAUSED -> "Paused"
    OfflineItem.Status.FAILED -> x.message ?: "Failed"
}

@Composable
private fun DownloadActions(x: OfflineItem, close: () -> Unit, play: (OfflineItem) -> Unit) {
    val downloads = LocalAppState.current.downloads
    // The panel follows the live entry so progress and status stay current.
    val live = downloads.item(x.id) ?: return
    Panel(close, live.displayTitle) {
        Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
            live.subtitle?.let { androidx.tv.material3.Text(it, color = Wren.secondary, modifier = Modifier.padding(bottom = 8.dp)) }
            when (live.status) {
                OfflineItem.Status.DONE -> ListRow("Play", { close(); play(live) }, Modifier.rememberFocus("dla:play", default = true))
                OfflineItem.Status.PAUSED, OfflineItem.Status.FAILED ->
                    ListRow(if (live.status == OfflineItem.Status.FAILED) "Retry" else "Resume", { close(); downloads.resume(live.id) },
                        Modifier.rememberFocus("dla:resume", default = true), detail = live.message)
                else -> ListRow("Pause", { close(); downloads.pause(live.id) }, Modifier.rememberFocus("dla:pause", default = true), detail = status(live))
            }
            ListRow(if (live.status == OfflineItem.Status.DONE) "Remove Download" else "Cancel Download", { close(); downloads.remove(live.id) })
        }
    }
}
