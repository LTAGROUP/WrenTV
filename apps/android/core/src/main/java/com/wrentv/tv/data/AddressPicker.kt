package com.wrentv.tv.data

import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.selects.select
import kotlinx.coroutines.withTimeoutOrNull
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.Request
import java.util.concurrent.TimeUnit

/**
 * Every address a server publishes, and picking the best one that answers.
 *
 * Servers publish their addresses to the Wren TV account service: LAN
 * addresses (http and, with remote access, https), the owner's own public
 * URL and the automatic remote URL. The app tries them all at once and uses
 * the best-ranked one that answers, the same way the Mac app does.
 */
object AddressPicker {
    /** How long a better-ranked address gets to answer after a worse one did. */
    private const val PREFER_WAIT_MS = 1500L

    private val probe = Http.client.newBuilder().callTimeout(5, TimeUnit.SECONDS).build()

    /** Candidates, best first: secure LAN, plain LAN, the owner's public address, the remote address, then the address typed in. */
    fun candidates(entered: HttpUrl?, localUrls: List<String>, publicUrl: String?, remoteUrl: String?): List<HttpUrl> {
        val lan = localUrls.mapNotNull { it.toHttpUrlOrNull() }
        val out = lan.filter { it.isHttps } + lan.filterNot { it.isHttps } +
            listOfNotNull(publicUrl, remoteUrl).mapNotNull { it.toHttpUrlOrNull() } + listOfNotNull(entered)
        val seen = HashSet<String>()
        return out.filter { seen.add(key(it)) }
    }

    /** The best candidate that answers as this server, or null when none does. */
    suspend fun best(candidates: List<HttpUrl>, id: String): HttpUrl? = coroutineScope {
        if (candidates.isEmpty()) return@coroutineScope null
        val jobs = candidates.map { u -> async(Dispatchers.IO) { answers(u, id) } }
        val results = arrayOfNulls<Boolean>(candidates.size)
        var deadline: Long? = null
        fun decided(): Pair<Boolean, HttpUrl?> {
            for ((i, r) in results.withIndex()) {
                if (r == null) return false to null
                if (r) return true to candidates[i]
            }
            return true to null
        }
        while (true) {
            val pending = jobs.withIndex().filter { results[it.index] == null }
            if (pending.isEmpty()) break
            val wait = deadline?.let { it - System.currentTimeMillis() }
            val got = withTimeoutOrNull(wait ?: Long.MAX_VALUE) {
                select<Pair<Int, Boolean>> { pending.forEach { (i, j) -> j.onAwait { i to it } } }
            } ?: break // waited long enough for a better answer
            results[got.first] = got.second
            val (done, url) = decided()
            if (done) {
                jobs.forEach { it.cancel() }
                return@coroutineScope url
            }
            if (got.second && deadline == null) deadline = System.currentTimeMillis() + PREFER_WAIT_MS
        }
        jobs.forEach { it.cancel() }
        results.indexOfFirst { it == true }.takeIf { it >= 0 }?.let { candidates[it] }
    }

    private suspend fun answers(base: HttpUrl, id: String): Boolean = runCatching {
        val u = base.resolve("/api/server") ?: return false
        probe.newCall(Request.Builder().url(u).build()).await().use { r ->
            if (r.code != 200) return false
            val info = json.decodeFromString<ServerInfo>(r.body!!.string())
            info.addressId == id
        }
    }.getOrDefault(false)

    fun key(u: HttpUrl): String = u.toString().trimEnd('/').lowercase()
}
