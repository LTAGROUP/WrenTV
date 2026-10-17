package com.wrentv.tv.data

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.job
import kotlinx.coroutines.flow.flowOn
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import kotlinx.serialization.serializer
import okhttp3.Call
import okhttp3.Callback
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import java.io.IOException
import java.util.TimeZone
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resumeWithException

class WrenTVException(message: String, val status: Int = 0) : Exception(message)

val json = Json {
    ignoreUnknownKeys = true
    explicitNulls = false
    coerceInputValues = true
    encodeDefaults = true
}

object Http {
    val client: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .build()

    /** For the server's event stream, which stays open. */
    val streaming: OkHttpClient = client.newBuilder().readTimeout(0, TimeUnit.SECONDS).build()
}

suspend fun Call.await(): Response = suspendCancellableCoroutine { cont ->
    enqueue(object : Callback {
        override fun onFailure(call: Call, e: IOException) {
            if (!cont.isCancelled) cont.resumeWithException(e)
        }

        override fun onResponse(call: Call, response: Response) {
            cont.resume(response) { _, _, _ -> response.close() }
        }
    })
    cont.invokeOnCancellation { runCatching { cancel() } }
}

private val jsonType = "application/json".toMediaType()

/** Thin async client for the Wren TV server API. */
class WrenTVApi(val baseUrl: HttpUrl, val auth: TokenSource? = null) {

    fun url(path: String, query: Map<String, String> = emptyMap()): HttpUrl {
        val b = baseUrl.newBuilder()
        val base = baseUrl.encodedPath.trimEnd('/')
        b.encodedPath(base + path.substringBefore('?'))
        query.forEach { (k, v) -> b.addQueryParameter(k, v) }
        return b.build()
    }

    /** Absolute URL for a server-relative path like "/stream/…". */
    fun absolute(path: String): String = baseUrl.resolve(path)?.toString() ?: (baseUrl.toString().trimEnd('/') + path)

    fun imageUrl(path: String?, size: String = "w342"): String? {
        if (path.isNullOrEmpty()) return null
        return url("/api/images/$size/${path.removePrefix("/")}").toString()
    }

    suspend fun send(method: String, path: String, query: Map<String, String> = emptyMap(), body: JsonElement? = null): String {
        val b = Request.Builder().url(url(path, query)).header("X-WrenTV-Client", CLIENT_NAME)
        auth?.let { b.header("Authorization", "Bearer ${it.token()}") }
        val rb = body?.toString()?.toRequestBody(jsonType) ?: if (method == "GET" || method == "DELETE") null else "".toRequestBody(jsonType)
        b.method(method, rb)
        val resp = Http.client.newCall(b.build()).await()
        return resp.use { r ->
            val text = withContext(Dispatchers.IO) { r.body?.string() ?: "" }
            if (r.code >= 400) {
                val msg = runCatching { json.parseToJsonElement(text).jsonObject["error"]?.jsonPrimitive?.content }.getOrNull()
                throw WrenTVException(msg ?: r.message.ifEmpty { "HTTP ${r.code}" }, r.code)
            }
            text
        }
    }

    suspend inline fun <reified T> get(path: String, query: Map<String, String> = emptyMap()): T =
        json.decodeFromString(serializer<T>(), send("GET", path, query))

    suspend inline fun <reified T> post(path: String, body: JsonElement? = null): T =
        json.decodeFromString(serializer<T>(), send("POST", path, body = body))

    private inline fun <reified T> enc(v: T): JsonElement = json.encodeToJsonElement(serializer<T>(), v)

    // Endpoints

    suspend fun serverInfo(): ServerInfo = get("/api/server")

    suspend fun login(username: String, password: String): AuthResponse = post("/api/auth/login", buildJsonObject {
        put("username", username); put("password", password); put("device", deviceName())
    })

    suspend fun setupOwner(username: String, password: String): AuthResponse = post("/api/setup/owner", buildJsonObject {
        put("username", username); put("password", password); put("displayName", username); put("device", deviceName())
    })

    // TV sign-in from a phone or browser
    suspend fun startPair(): PairStart = post("/api/pair", buildJsonObject { put("device", deviceName()) })
    suspend fun pollPair(code: String, secret: String): PairPoll = post("/api/pair/$code/poll", buildJsonObject { put("secret", secret) })

    suspend fun me(): MeResponse = get("/api/me")
    suspend fun home(): HomeResponse = get("/api/home")
    suspend fun libraries(): List<Library> = get("/api/libraries")

    suspend fun items(library: String, sort: String, genre: String? = null, unwatched: Boolean = false): ItemPage {
        val q = mutableMapOf("sort" to sort, "limit" to "5000")
        genre?.let { q["genre"] = it }
        if (unwatched) q["unwatched"] = "1"
        return get("/api/libraries/$library/items", q)
    }

    suspend fun genres(library: String? = null): List<GenreCount> =
        get("/api/genres", library?.let { mapOf("libraryId" to it) } ?: emptyMap())

    suspend fun genreItems(genre: String, library: String? = null, kind: String? = null, sort: String = "title", unwatched: Boolean = false): ItemPage {
        val q = mutableMapOf("genre" to genre, "sort" to sort)
        library?.let { q["libraryId"] = it }
        kind?.let { q["kind"] = it }
        if (unwatched) q["unwatched"] = "1"
        return get("/api/genres/items", q)
    }

    suspend fun collections(library: String? = null): List<WrenCollection> =
        get("/api/collections", library?.let { mapOf("libraryId" to it) } ?: emptyMap())

    suspend fun collection(id: String, sort: String? = null): CollectionDetail =
        get("/api/collections/$id", sort?.let { mapOf("sort" to it) } ?: emptyMap())

    suspend fun addToCollection(id: String, items: List<String>) {
        send("POST", "/api/collections/$id/items", body = buildJsonObject { put("itemIds", enc(items)) })
    }

    suspend fun removeFromCollection(id: String, item: String) {
        send("DELETE", "/api/collections/$id/items/$item")
    }

    suspend fun itemCollections(id: String): List<String> = get("/api/items/$id/collections")

    // Preferences follow the user across devices.
    suspend fun prefs(): UserPrefs = get("/api/me/prefs")
    suspend fun setPrefs(p: UserPrefs): UserPrefs = json.decodeFromString(send("PATCH", "/api/me/prefs", body = enc(p)))

    suspend fun stats(days: Int): StatsResponse {
        val tz = TimeZone.getDefault().getOffset(System.currentTimeMillis()) / 1000
        return get("/api/admin/stats", mapOf("days" to days.toString(), "tz" to tz.toString()))
    }

    suspend fun item(id: String): ItemDetail = get("/api/items/$id")
    suspend fun extras(id: String): Extras = get("/api/items/$id/extras")

    /** The theme for a movie or show (a season or episode gets its show's), saved to [into]; null when it has none. */
    suspend fun theme(id: String, into: java.io.File): java.io.File? {
        val b = Request.Builder().url(url("/api/items/$id/theme")).header("X-WrenTV-Client", CLIENT_NAME)
        auth?.let { b.header("Authorization", "Bearer ${it.token()}") }
        val resp = Http.client.newCall(b.build()).await()
        return resp.use { r ->
            if (!r.isSuccessful) return@use null
            withContext(Dispatchers.IO) {
                into.parentFile?.mkdirs()
                val tmp = java.io.File(into.path + ".part")
                r.body?.byteStream()?.use { input -> tmp.outputStream().use { input.copyTo(it) } } ?: return@withContext null
                if (tmp.renameTo(into)) into else null
            }
        }
    }
    suspend fun discoverHome(): DiscoverHome = get("/api/discover/home")
    suspend fun catalog(type: String, sort: String, genre: Int?, page: Int): CatalogPage =
        get("/api/discover/browse", mapOf("type" to type, "sort" to sort, "genre" to (genre?.toString() ?: ""), "page" to page.toString()))

    suspend fun trending(): TrendingResponse = get("/api/discover/trending")
    suspend fun search(q: String): List<Item> = get("/api/search", mapOf("q" to q))

    suspend fun play(id: String, req: PlayRequest): PlayResponse = post("/api/items/$id/play", enc(req))

    suspend fun reportProgress(id: String, position: Double, duration: Double) {
        runCatching {
            send("POST", "/api/items/$id/progress", body = buildJsonObject { put("position", position); put("duration", duration) })
        }
    }

    suspend fun setWatched(id: String, watched: Boolean) {
        send("POST", "/api/items/$id/watched", body = buildJsonObject { put("watched", watched) })
    }

    suspend fun stopSession(sid: String) {
        runCatching { send("DELETE", "/api/sessions/$sid") }
    }

    // Streaming
    suspend fun discoverSearch(q: String): DiscoverSearch = get("/api/discover/search", mapOf("q" to q))

    suspend fun addStreamTitle(tmdbId: Int, mediaType: String): Item = post("/api/streaming/titles", buildJsonObject {
        put("tmdbId", tmdbId); put("mediaType", mediaType)
    })

    /** A link to save the item's file on this device; status "pending" while a streamed title is still being found. */
    suspend fun deviceDownload(id: String, fileId: String?): DeviceDownload =
        post("/api/items/$id/download", buildJsonObject { put("fileId", fileId ?: "") })

    // Admin
    suspend fun serverDownload(id: String): Int {
        val r: Map<String, Int> = post("/api/items/$id/server-download")
        return r["queued"] ?: 0
    }

    suspend fun forgetStreams(id: String) { send("DELETE", "/api/items/$id/streams") }
    suspend fun serverDownloads(): List<ServerDownload> = get("/api/admin/downloads")
    suspend fun cancelServerDownload(id: String) { send("DELETE", "/api/admin/downloads/$id") }
    suspend fun retryServerDownload(id: String) { send("POST", "/api/admin/downloads/$id/retry") }

    suspend fun checkDebrid(provider: String, key: String): DebridAccount =
        post("/api/admin/streaming/check-debrid", buildJsonObject { put("provider", provider); put("key", key) })

    suspend fun checkAddon(url: String): AddonSetting {
        val r: Map<String, String> = post("/api/admin/streaming/check-addon", buildJsonObject { put("url", url) })
        return AddonSetting(name = r["name"] ?: "", url = r["url"] ?: url, enabled = true)
    }

    suspend fun users(): List<ServerUser> = get("/api/users")

    suspend fun setUserLimits(id: String, l: UserLimitsPatch) {
        send("PATCH", "/api/users/$id", body = buildJsonObject { put("limits", enc(l)) })
    }

    suspend fun settings(): ServerSettings = get("/api/admin/settings")

    suspend fun patchSettings(patch: JsonObject): ServerSettings =
        json.decodeFromString(send("PATCH", "/api/admin/settings", body = patch))

    suspend fun createLibrary(name: String, type: String, paths: List<String>): Library = post("/api/libraries", buildJsonObject {
        put("name", name); put("type", type); put("paths", enc(paths))
    })

    suspend fun deleteLibrary(id: String) { send("DELETE", "/api/libraries/$id") }
    suspend fun scanLibrary(id: String) { send("POST", "/api/libraries/$id/scan") }

    /** Server-sent events: library changes and scans. Completes when the connection drops. */
    fun events(): Flow<ServerEvent> = flow {
        val b = Request.Builder().url(url("/api/events")).header("Accept", "text/event-stream")
        auth?.let { b.header("Authorization", "Bearer ${it.token()}") }
        val call = Http.streaming.newCall(b.build())
        // Reads block, so cancelling the collector cancels the call.
        val handle = currentCoroutineContext().job.invokeOnCompletion { call.cancel() }
        try {
            call.execute().use { r ->
                if (!r.isSuccessful) throw WrenTVException("events: HTTP ${r.code}", r.code)
                val source = r.body!!.source()
                while (true) {
                    val line = source.readUtf8Line() ?: break
                    if (!line.startsWith("data:")) continue
                    runCatching { json.decodeFromString<ServerEvent>(line.removePrefix("data:").trim()) }.getOrNull()?.let { emit(it) }
                }
            }
        } finally {
            handle.dispose()
        }
    }.flowOn(Dispatchers.IO)

    companion object {
        const val CLIENT_NAME = "Wren TV for Android TV"

        fun parseBase(s: String): HttpUrl = s.toHttpUrl()
    }
}

fun deviceName(): String {
    val model = android.os.Build.MODEL ?: "Android TV"
    return if (model.contains("SHIELD", ignoreCase = true)) "NVIDIA SHIELD" else model
}
