package com.wrentv.tv.data

import android.annotation.SuppressLint
import android.content.Context
import android.net.Uri
import android.util.Log
import androidx.tvprovider.media.tv.TvContractCompat
import androidx.tvprovider.media.tv.TvContractCompat.WatchNextPrograms
import androidx.tvprovider.media.tv.WatchNextProgram
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext

/**
 * Keeps the TV's own "Play Next" row in step with Continue Watching, so a
 * show can be picked up from the home screen without opening the app first.
 * Each card opens wrentv://play/<id>.
 */
object WatchNext {
    private const val TAG = "WatchNext"
    private val lock = Mutex()

    /** Titles the viewer took off the row themselves, and how far along they were then. */
    private const val PREFS = "watch-next"

    suspend fun sync(context: Context, api: WrenTVApi, items: List<Item>) = withContext(Dispatchers.IO) {
        lock.withLock { runCatching { write(context.applicationContext, api, items.take(20)) }.onFailure { Log.w(TAG, "sync", it) } }
    }

    /** Signing out empties the row: it belongs to whoever is signed in. */
    suspend fun clear(context: Context) = withContext(Dispatchers.IO) {
        lock.withLock {
            runCatching {
                existing(context).values.forEach { context.contentResolver.delete(TvContractCompat.buildWatchNextProgramUri(it.id), null, null) }
            }
        }
    }

    private data class Row(val id: Long, val browsable: Boolean, val position: Long)

    @SuppressLint("RestrictedApi")
    private fun existing(context: Context): Map<String, Row> {
        val out = mutableMapOf<String, Row>()
        context.contentResolver.query(WatchNextPrograms.CONTENT_URI, WatchNextProgram.PROJECTION, null, null, null)?.use { c ->
            while (c.moveToNext()) {
                val p = WatchNextProgram.fromCursor(c)
                val key = p.internalProviderId ?: continue
                out[key] = Row(p.id, p.isBrowsable, p.lastPlaybackPositionMillis.toLong())
            }
        }
        return out
    }

    @SuppressLint("RestrictedApi")
    private fun write(context: Context, api: WrenTVApi, items: List<Item>) {
        val resolver = context.contentResolver
        val have = existing(context)
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val wanted = items.map { it.id }.toSet()
        // Gone from Continue Watching: finished, or removed in the app.
        for ((key, row) in have) {
            if (key !in wanted) {
                resolver.delete(TvContractCompat.buildWatchNextProgramUri(row.id), null, null)
                prefs.edit().remove(key).apply()
            }
        }
        val now = System.currentTimeMillis()
        items.forEachIndexed { i, it ->
            val positionMs = ((it.progress?.position ?: 0.0) * 1000).toLong()
            val old = have[it.id]
            if (old != null && !old.browsable) {
                // Removed from the row on the TV: stays off until it's watched further.
                val removedAt = prefs.getLong(it.id, -1)
                if (removedAt < 0) prefs.edit().putLong(it.id, old.position).apply()
                if (positionMs <= maxOf(removedAt, old.position) + 60_000) return@forEachIndexed
                resolver.delete(TvContractCompat.buildWatchNextProgramUri(old.id), null, null)
                prefs.edit().remove(it.id).apply()
            }
            val program = build(api, it, positionMs,
                // Newest first; items without a time keep the server's order.
                (it.progress?.updatedAt?.takeIf { t -> t > 0 }?.times(1000)) ?: (now - i * 60_000L))
            if (old != null && old.browsable) {
                resolver.update(TvContractCompat.buildWatchNextProgramUri(old.id), program.toContentValues(), null, null)
            } else {
                resolver.insert(WatchNextPrograms.CONTENT_URI, program.toContentValues())
            }
        }
    }

    @SuppressLint("RestrictedApi")
    private fun build(api: WrenTVApi, it: Item, positionMs: Long, engaged: Long): WatchNextProgram {
        val episode = it.kind == "episode"
        val durationMs = ((it.progress?.duration?.takeIf { d -> d > 0 } ?: ((it.runtimeMin ?: 0) * 60.0)) * 1000).toLong()
        val art = api.imageUrl(it.stillPath ?: it.backdropPath ?: it.posterPath, "w780")
        val b = WatchNextProgram.Builder()
            .setType(if (episode) TvContractCompat.PreviewPrograms.TYPE_TV_EPISODE else TvContractCompat.PreviewPrograms.TYPE_MOVIE)
            .setWatchNextType(if (it.inProgress) WatchNextPrograms.WATCH_NEXT_TYPE_CONTINUE else WatchNextPrograms.WATCH_NEXT_TYPE_NEXT)
            .setLastEngagementTimeUtcMillis(engaged)
            .setTitle(if (episode) it.showTitle ?: it.title else it.title)
            .setDescription(it.overview ?: "")
            .setPosterArtAspectRatio(TvContractCompat.PreviewPrograms.ASPECT_RATIO_16_9)
            .setInternalProviderId(it.id)
            .setIntentUri(Uri.parse("wrentv://play/${it.id}"))
        if (art != null) b.setPosterArtUri(Uri.parse(art))
        if (episode) {
            b.setEpisodeTitle(it.title)
            if (it.seasonNumber > 0) b.setSeasonNumber(it.seasonNumber)
            if (it.episodeNumber > 0) b.setEpisodeNumber(it.episodeNumber)
        } else {
            it.year?.let { y -> b.setReleaseDate(y.toString()) }
        }
        if (durationMs > 0) b.setDurationMillis(durationMs.toInt())
        if (it.inProgress && positionMs > 0) b.setLastPlaybackPositionMillis(positionMs.toInt())
        return b.build()
    }
}
