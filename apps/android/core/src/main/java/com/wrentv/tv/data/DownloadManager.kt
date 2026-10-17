package com.wrentv.tv.data

import android.content.Context
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import okhttp3.Request
import java.io.File
import java.io.FileOutputStream

/** A movie or episode saved on this TV for offline viewing. */
@Serializable
data class OfflineItem(
    val id: String, // the server's item id
    val server: String = "",
    val kind: String = "movie",
    val title: String = "",
    val showId: String? = null,
    val showTitle: String? = null,
    val seasonNumber: Int = 0,
    val episodeNumber: Int = 0,
    val year: Int? = null,
    val overview: String? = null,
    val runtimeMin: Int? = null,
    val posterPath: String? = null,
    val fileId: String? = null,
    val label: String? = null,
    val fileName: String? = null,
    val size: Long = 0,
    val received: Long = 0,
    val status: Status = Status.WAITING,
    val message: String? = null,
    /** The video's name in the downloads folder once complete. */
    val file: String? = null,
    /** The poster's name in the downloads folder. */
    val poster: String? = null,
    val position: Double = 0.0,
    val duration: Double = 0.0,
    val addedAt: Long = System.currentTimeMillis(),
) {
    @Serializable
    enum class Status { WAITING, PREPARING, DOWNLOADING, PAUSED, DONE, FAILED }

    val isEpisode: Boolean get() = kind == "episode"
    val displayTitle: String get() = if (isEpisode) showTitle ?: title else title
    val subtitle: String? get() = if (isEpisode) "S$seasonNumber · E$episodeNumber — $title" else null
    val fraction: Float get() = if (size > 0) (received.toFloat() / size).coerceAtMost(1f) else 0f
    val active: Boolean get() = status == Status.PREPARING || status == Status.DOWNLOADING
}

/**
 * Saves the original file of a movie or episode on this TV through a
 * short-lived link from the server, resuming interrupted transfers with HTTP
 * ranges, and keeps an index so downloads play without a connection.
 */
class DownloadManager(context: Context, private val scope: CoroutineScope) {
    var items by mutableStateOf<List<OfflineItem>>(emptyList())
        private set

    private var api: WrenTVApi? = null
    private var maxConcurrent = 2
    private val jobs = mutableMapOf<String, Job>()
    private var lastSave = 0L

    val folder: File = (context.getExternalFilesDir("Downloads") ?: File(context.filesDir, "Downloads")).apply { mkdirs() }
    private val index = File(folder, "downloads.json")

    init {
        runCatching { json.decodeFromString<List<OfflineItem>>(index.readText()) }.getOrNull()?.let { saved ->
            // Transfers don't survive the app closing; they resume once signed in.
            items = saved.map { if (it.active) it.copy(status = OfflineItem.Status.WAITING, message = null) else it }
        }
    }

    fun connect(api: WrenTVApi, limits: UserLimitsInfo?) {
        this.api = api
        val m = limits?.maxDownloads ?: 2
        maxConcurrent = if (m > 0) minOf(m, 4) else 3
        pump()
    }

    fun disconnect() {
        api = null
        jobs.values.forEach { it.cancel() }
        jobs.clear()
        items = items.map { if (it.active) it.copy(status = OfflineItem.Status.WAITING) else it }
    }

    fun item(id: String): OfflineItem? = items.firstOrNull { it.id == id }

    fun localFile(x: OfflineItem): File? {
        if (x.status != OfflineItem.Status.DONE) return null
        return x.file?.let { File(folder, it) }?.takeIf { it.exists() }
    }

    fun posterFile(x: OfflineItem): File? = x.poster?.let { File(folder, it) }?.takeIf { it.exists() }

    val usedBytes: Long get() = items.filter { it.status == OfflineItem.Status.DONE }.sumOf { it.size }

    /** Queues a movie or episode. `show` names the show for episodes. */
    fun add(it: Item, show: Item?, fileId: String? = null, server: String) {
        if (!it.isPlayable) return
        item(it.id)?.let { x ->
            if (x.status == OfflineItem.Status.FAILED || x.status == OfflineItem.Status.PAUSED) resume(it.id)
            return
        }
        items = items + OfflineItem(
            id = it.id, server = server, kind = it.kind, title = it.title, showId = it.showId ?: show?.id,
            showTitle = show?.title ?: it.showTitle, seasonNumber = it.seasonNumber, episodeNumber = it.episodeNumber,
            year = it.year ?: show?.year, overview = it.overview, runtimeMin = it.runtimeMin,
            posterPath = if (it.kind == "episode") it.stillPath ?: show?.posterPath else it.posterPath, fileId = fileId,
        )
        save()
        pump()
    }

    fun pause(id: String) {
        jobs.remove(id)?.cancel()
        update(id) { it.copy(status = OfflineItem.Status.PAUSED, message = null) }
        save()
        pump()
    }

    fun resume(id: String) {
        update(id) { it.copy(status = OfflineItem.Status.WAITING, message = null) }
        pump()
    }

    fun remove(id: String) {
        jobs.remove(id)?.cancel()
        item(id)?.let { x -> listOfNotNull(x.file, x.poster, partName(x)).forEach { File(folder, it).delete() } }
        items = items.filterNot { it.id == id }
        save()
        pump()
    }

    fun savePosition(id: String, position: Double, duration: Double) {
        update(id) { it.copy(position = position, duration = if (duration > 0) duration else it.duration) }
        save()
    }

    /** Starts waiting downloads up to the per-user limit. */
    private fun pump() {
        if (api == null) return
        var running = items.count { it.active }
        for (x in items.filter { it.status == OfflineItem.Status.WAITING }) {
            if (running >= maxConcurrent) break
            running++
            start(x.id)
        }
    }

    private fun partName(x: OfflineItem) = "${x.id}.part"

    private fun start(id: String) {
        val api = api ?: return
        val x = item(id) ?: return
        update(id) { it.copy(status = OfflineItem.Status.PREPARING, message = null) }
        jobs[id] = scope.launch {
            try {
                var link: DeviceDownload
                var tries = 0
                while (true) {
                    link = api.deviceDownload(id, x.fileId)
                    if (link.status != "pending") break
                    if (++tries > 40) throw WrenTVException("No stream found yet. Try again later.")
                    update(id) { it.copy(message = link.message ?: "Finding a stream…") }
                    delay(3000)
                }
                val path = link.url ?: throw WrenTVException("The server didn't return a link.")
                update(id) {
                    it.copy(
                        fileName = link.fileName, fileId = link.fileId ?: it.fileId, label = link.label,
                        size = link.size?.takeIf { s -> s > 0 } ?: it.size, status = OfflineItem.Status.DOWNLOADING, message = null,
                    )
                }
                save()
                launch { fetchPoster(id, api) }
                transfer(id, api.absolute(path))
                jobs.remove(id)
                save()
                pump()
            } catch (e: CancellationException) {
                throw e
            } catch (e: WrenTVException) {
                if (e.status == 429) {
                    // Over this user's download limit; wait for another to finish.
                    update(id) { it.copy(status = OfflineItem.Status.PAUSED, message = e.message) }
                    jobs.remove(id)
                    save()
                    delay(30_000)
                    if (item(id)?.status == OfflineItem.Status.PAUSED) resume(id)
                } else fail(id, e.message ?: "Failed")
            } catch (e: Exception) {
                fail(id, e.message ?: "Failed")
            }
        }
    }

    /** Downloads into a .part file, continuing from what's there. */
    private suspend fun transfer(id: String, url: String) = withContext(Dispatchers.IO) {
        val x = item(id) ?: return@withContext
        val part = File(folder, partName(x))
        val have = if (part.exists()) part.length() else 0L
        val req = Request.Builder().url(url).apply { if (have > 0) header("Range", "bytes=$have-") }.build()
        Http.streaming.newCall(req).await().use { r ->
            if (r.code >= 400) {
                val msg = runCatching { json.decodeFromString<Map<String, String>>(r.body!!.string())["error"] }.getOrNull()
                throw WrenTVException(msg ?: "HTTP ${r.code}", r.code)
            }
            val append = r.code == 206
            val start = if (append) have else 0L
            val total = r.body!!.contentLength().let { if (it > 0) it + start else 0L }
            if (total > 0) withContext(Dispatchers.Main) { update(id) { it.copy(size = total) } }
            val buf = ByteArray(256 * 1024)
            var received = start
            var lastUi = 0L
            r.body!!.byteStream().use { input ->
                FileOutputStream(part, append).use { out ->
                    while (true) {
                        ensureActive()
                        val n = input.read(buf)
                        if (n < 0) break
                        out.write(buf, 0, n)
                        received += n
                        val now = System.currentTimeMillis()
                        if (now - lastUi > 500) {
                            lastUi = now
                            val got = received
                            withContext(Dispatchers.Main) {
                                update(id) { it.copy(received = got) }
                                if (now - lastSave > 5000) save()
                            }
                        }
                    }
                }
            }
            val ext = x.fileName?.substringAfterLast('.', "")?.takeIf { it.isNotEmpty() && it.length <= 4 } ?: "mkv"
            val name = "$id.$ext"
            val dest = File(folder, name)
            dest.delete()
            if (!part.renameTo(dest)) throw WrenTVException("Couldn't save the file")
            withContext(Dispatchers.Main) {
                update(id) { it.copy(file = name, status = OfflineItem.Status.DONE, message = null, size = dest.length(), received = dest.length()) }
            }
        }
    }

    private suspend fun fetchPoster(id: String, api: WrenTVApi) {
        val x = item(id) ?: return
        if (x.poster != null) return
        val url = api.imageUrl(x.posterPath, if (x.isEpisode) "w300" else "w342") ?: return
        val name = "$id.jpg"
        val ok = withContext(Dispatchers.IO) {
            runCatching {
                Http.client.newCall(Request.Builder().url(url).build()).await().use { r ->
                    if (!r.isSuccessful) return@use false
                    File(folder, name).writeBytes(r.body!!.bytes())
                    true
                }
            }.getOrDefault(false)
        }
        if (ok) {
            update(id) { it.copy(poster = name) }
            save()
        }
    }

    private fun fail(id: String, message: String) {
        jobs.remove(id)
        update(id) { it.copy(status = OfflineItem.Status.FAILED, message = message) }
        save()
        pump()
    }

    private fun update(id: String, f: (OfflineItem) -> OfflineItem) {
        items = items.map { if (it.id == id) f(it) else it }
    }

    private fun save() {
        lastSave = System.currentTimeMillis()
        val text = json.encodeToString(kotlinx.serialization.builtins.ListSerializer(OfflineItem.serializer()), items)
        scope.launch(Dispatchers.IO) { runCatching { index.writeText(text) } }
    }
}
