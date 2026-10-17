package com.wrentv.phone.ui

import android.text.format.Formatter
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Delete
import androidx.compose.material.icons.rounded.Download
import androidx.compose.material.icons.rounded.Pause
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import androidx.compose.ui.layout.ContentScale
import com.wrentv.tv.data.OfflineItem
import com.wrentv.tv.ui.PlayTarget

/** Movies and episodes saved on this phone; they play without a connection. */
@Composable
fun DownloadsScreen() {
    val state = LocalAppState.current
    val context = LocalContext.current
    val dl = state.downloads
    val items = dl.items.sortedByDescending { it.addedAt }
    val used = dl.usedBytes
    Page("Downloads", if (used > 0) "${Formatter.formatShortFileSize(context, used)} on this phone" else null) {
        if (items.isEmpty()) {
            EmptyState(Icons.Rounded.Download, "No downloads", "Download movies and episodes from their pages to watch them offline.")
        } else LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(vertical = 8.dp)) {
            items(items, { it.id }) { DownloadRow(it) }
        }
    }
}

@Composable
private fun DownloadRow(x: OfflineItem) {
    val state = LocalAppState.current
    val nav = LocalNavigator.current
    val context = LocalContext.current
    val m = metrics()
    val dl = state.downloads
    val file = dl.localFile(x)
    Row(
        Modifier.fillMaxWidth().clickable(enabled = file != null) {
            file?.let { nav.play(PlayTarget(x.id, x.displayTitle, x.subtitle, localFile = it.absolutePath, localPosition = x.position)) }
        }.padding(horizontal = m.gutter, vertical = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically,
    ) {
        val poster = dl.posterFile(x)
        val wide = x.isEpisode
        Box(Modifier.width(if (wide) 120.dp else 60.dp).height(if (wide) 68.dp else 90.dp).clip(RoundedCornerShape(6.dp)).background(Wren.surfaceHigh)) {
            if (poster != null) AsyncImage(poster, null, Modifier.fillMaxSize(), contentScale = ContentScale.Crop)
            else RemoteImage(state.api?.imageUrl(x.posterPath, "w300"), Modifier.fillMaxSize())
            if (x.duration > 0 && x.position > 0) ProgressStrip(x.position / x.duration)
        }
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
            Text(x.displayTitle, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
            x.subtitle?.let { Text(it, color = Wren.secondary, style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis) }
            val size = if (x.size > 0) Formatter.formatShortFileSize(context, x.size) else null
            val status = when (x.status) {
                OfflineItem.Status.DONE -> listOfNotNull(size, x.label).joinToString(" · ")
                OfflineItem.Status.WAITING -> "Waiting"
                OfflineItem.Status.PREPARING -> "Preparing…"
                OfflineItem.Status.DOWNLOADING -> if (x.size > 0) "${Formatter.formatShortFileSize(context, x.received)} of $size" else "Downloading…"
                OfflineItem.Status.PAUSED -> "Paused"
                OfflineItem.Status.FAILED -> x.message ?: "Failed"
            }
            Text(status, color = if (x.status == OfflineItem.Status.FAILED) Wren.ember else Wren.secondary, style = MaterialTheme.typography.bodySmall, maxLines = 2)
            if (x.status != OfflineItem.Status.DONE && x.status != OfflineItem.Status.FAILED) {
                LinearProgressIndicator({ x.fraction }, Modifier.fillMaxWidth().padding(top = 2.dp), color = Color.White, trackColor = Color.White.copy(alpha = 0.15f))
            }
        }
        when (x.status) {
            OfflineItem.Status.DONE -> Icon(Icons.Rounded.PlayArrow, "Play", Modifier.size(22.dp))
            OfflineItem.Status.PAUSED, OfflineItem.Status.FAILED ->
                IconButton({ dl.resume(x.id) }) { Icon(Icons.Rounded.Refresh, "Resume") }
            else -> IconButton({ dl.pause(x.id) }) { Icon(Icons.Rounded.Pause, "Pause") }
        }
        IconButton({ dl.remove(x.id) }) { Icon(Icons.Rounded.Delete, "Delete", tint = Wren.secondary) }
    }
}
