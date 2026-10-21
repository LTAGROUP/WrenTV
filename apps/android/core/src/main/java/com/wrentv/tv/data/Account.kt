package com.wrentv.tv.data

import android.content.Context
import android.content.SharedPreferences
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonObject
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody

/** A signed-in Wren TV account (Supabase Auth session). */
@Serializable
data class AccountSession(val accessToken: String, val refreshToken: String, val expiresAt: Double, val email: String) {
    /** The account's user id, from the access token. */
    val userId: String? get() = runCatching {
        val part = accessToken.split('.')[1]
        val body = String(android.util.Base64.decode(part, android.util.Base64.URL_SAFE or android.util.Base64.NO_PADDING or android.util.Base64.NO_WRAP))
        json.parseToJsonElement(body).jsonObject["sub"]?.jsonPrimitive?.content
    }.getOrNull()
}

/** Where sign-ins and server addresses are kept. The app's private storage is only readable by this app. */
class Store(context: Context) {
    private val prefs: SharedPreferences = context.getSharedPreferences("wrentv", Context.MODE_PRIVATE)

    var serverUrl: String?
        get() = prefs.getString("serverURL", null)
        set(v) = prefs.edit().putString("serverURL", v).apply()

    var serverId: String?
        get() = prefs.getString("serverId", null)
        set(v) = prefs.edit().putString("serverId", v).apply()

    var serverAddresses: List<String>
        get() = prefs.getString("serverAddresses", null)?.split('\n')?.filter { it.isNotEmpty() } ?: emptyList()
        set(v) = prefs.edit().putString("serverAddresses", v.joinToString("\n")).apply()

    /** Local-account tokens and device tokens, by server address. */
    fun token(key: String): String? = prefs.getString("token:$key", null)
    fun setToken(key: String, token: String?) = prefs.edit().apply { if (token == null) remove("token:$key") else putString("token:$key", token) }.apply()

    fun session(key: String): AccountSession? =
        prefs.getString("session:$key", null)?.let { runCatching { json.decodeFromString<AccountSession>(it) }.getOrNull() }

    fun setSession(key: String, s: AccountSession?) = prefs.edit().apply {
        if (s == null) remove("session:$key") else putString("session:$key", json.encodeToString(AccountSession.serializer(), s))
    }.apply()

    fun string(key: String, def: String): String = prefs.getString(key, def) ?: def
    fun setString(key: String, v: String) = prefs.edit().putString(key, v).apply()
    fun int(key: String, def: Int): Int = prefs.getInt(key, def)
    fun setInt(key: String, v: Int) = prefs.edit().putInt(key, v).apply()
    fun bool(key: String, def: Boolean): Boolean = prefs.getBoolean(key, def)
    fun setBool(key: String, v: Boolean) = prefs.edit().putBoolean(key, v).apply()
}

/** The Wren TV account service. The publishable key is public: every account server hands it to its clients in /api/server. */
val WrenAccounts = SupabaseInfo("https://buycndonhmcafzzcvmgu.supabase.co", "sb_publishable_vNiXbucy_8L_RQBFPoBPvA_HNZv1i15")

/** A server this account owns or has been shared, as the account service lists it. */
@Serializable
data class AccountServer(
    val id: String,
    val name: String,
    val owner_id: String,
    val local_urls: List<String>? = null,
    val public_url: String? = null,
    val remote_url: String? = null,
    val last_seen_at: String? = null,
) {
    /** Every address the server published, best first. */
    val candidates: List<HttpUrl> get() = AddressPicker.candidates(null, local_urls ?: emptyList(), public_url, remote_url)

    /** Heard from in the last ten minutes. */
    val online: Boolean get() = runCatching {
        System.currentTimeMillis() - java.time.OffsetDateTime.parse(last_seen_at).toInstant().toEpochMilli() < 10 * 60_000
    }.getOrDefault(false)
}

/** A sign-in code shown on this TV until a signed-in phone or browser approves it. */
@Serializable
data class DeviceLink(val code: String, val device_secret: String, val expires_at: String = "")

/** Talks to the Wren TV account service (Supabase Auth over REST). */
class AccountClient(info: SupabaseInfo) {
    private val url = info.url.trimEnd('/')
    private val key = info.publishableKey

    /** Key under which the session is stored; shared by every server that uses the same account service. */
    val storeKey: String get() = "account:$url"

    @Serializable
    private data class TokenResponse(
        val access_token: String? = null,
        val refresh_token: String? = null,
        val expires_in: Double? = null,
        val expires_at: Double? = null,
        val user: U? = null,
    ) {
        @Serializable
        data class U(val email: String? = null)
    }

    @Serializable
    private data class ErrorResponse(val msg: String? = null, val message: String? = null, val error_description: String? = null, val error: String? = null)

    private suspend fun request(path: String, body: JsonElement, bearer: String? = null): String {
        val b = Request.Builder().url("$url$path").header("apikey", key)
            .post(body.toString().toRequestBody("application/json".toMediaType()))
        bearer?.let { b.header("Authorization", "Bearer $it") }
        return Http.client.newCall(b.build()).await().use { r ->
            val text = withContext(Dispatchers.IO) { r.body?.string() ?: "" }
            if (r.code >= 400) {
                val e = runCatching { json.decodeFromString<ErrorResponse>(text) }.getOrNull()
                val msg = e?.msg ?: e?.error_description ?: e?.message ?: e?.error ?: "HTTP ${r.code}"
                throw WrenTVException(msg.replaceFirstChar { it.uppercase() }, r.code)
            }
            text
        }
    }

    private fun session(text: String, fallback: String): AccountSession? {
        val t = json.decodeFromString<TokenResponse>(text)
        val access = t.access_token ?: return null
        val refresh = t.refresh_token ?: return null
        val expires = t.expires_at ?: (System.currentTimeMillis() / 1000.0 + (t.expires_in ?: 3600.0))
        return AccountSession(access, refresh, expires, t.user?.email ?: fallback)
    }

    suspend fun signIn(email: String, password: String): AccountSession {
        val text = request("/auth/v1/token?grant_type=password", buildJsonObject { put("email", email); put("password", password) })
        val s = session(text, email) ?: throw WrenTVException("Sign-in failed")
        acceptInvites(s)
        return s
    }

    /** Returns null when the account must confirm its email before signing in. */
    suspend fun signUp(email: String, password: String, name: String): AccountSession? {
        val text = request("/auth/v1/signup", buildJsonObject {
            put("email", email); put("password", password)
            putJsonObject("data") { put("display_name", name) }
        })
        val s = session(text, email) ?: return null
        acceptInvites(s)
        return s
    }

    suspend fun refresh(s: AccountSession): AccountSession {
        val text = request("/auth/v1/token?grant_type=refresh_token", buildJsonObject { put("refresh_token", s.refreshToken) })
        return session(text, s.email) ?: throw WrenTVException("Your sign-in expired", 401)
    }

    suspend fun signOut(s: AccountSession) {
        runCatching { request("/auth/v1/logout", buildJsonObject {}, s.accessToken) }
    }

    @Serializable
    data class ServerAddresses(val local_urls: List<String>? = null, val public_url: String? = null, val remote_url: String? = null)

    /** The addresses a server published in its heartbeat (visible to its owner and the accounts it's shared with). */
    suspend fun addresses(serverId: String, s: AccountSession): ServerAddresses? = runCatching {
        val u = "$url/rest/v1/servers".toHttpUrl().newBuilder()
            .addQueryParameter("id", "eq.$serverId")
            .addQueryParameter("select", "local_urls,public_url,remote_url").build()
        val req = Request.Builder().url(u).header("apikey", key).header("Authorization", "Bearer ${s.accessToken}").build()
        Http.client.newCall(req).await().use { r ->
            if (r.code != 200) return null
            json.decodeFromString<List<ServerAddresses>>(withContext(Dispatchers.IO) { r.body!!.string() }).firstOrNull()
        }
    }.getOrNull()

    private suspend fun get(path: String, bearer: String): String {
        val req = Request.Builder().url("$url$path").header("apikey", key).header("Authorization", "Bearer $bearer").build()
        return Http.client.newCall(req).await().use { r ->
            val text = withContext(Dispatchers.IO) { r.body?.string() ?: "" }
            if (r.code >= 400) throw WrenTVException("Couldn't load your servers", r.code)
            text
        }
    }

    /** The servers this account owns or has been shared, most recently seen first. */
    suspend fun servers(s: AccountSession): List<AccountServer> =
        json.decodeFromString(get("/rest/v1/servers?select=id,name,owner_id,local_urls,public_url,remote_url,last_seen_at&order=last_seen_at.desc.nullslast", s.accessToken))

    /** Asks for a code to show on this TV; anyone signed in on a phone or browser can approve it. */
    suspend fun startLink(deviceName: String): DeviceLink {
        val text = request("/rest/v1/rpc/create_device_link", buildJsonObject { put("p_device_name", deviceName); put("p_platform", Client.platform) })
        return json.decodeFromString<List<DeviceLink>>(text).first()
    }

    @Serializable
    private data class LinkPoll(val status: String, val token_hash: String? = null)

    /**
     * The TV's own session once the code is approved; null while waiting.
     * Throws (404) once the code has expired.
     */
    suspend fun pollLink(link: DeviceLink): AccountSession? {
        val text = request("/rest/v1/rpc/poll_device_link", buildJsonObject { put("p_code", link.code); put("p_device_secret", link.device_secret) })
        val p = json.decodeFromString<List<LinkPoll>>(text).firstOrNull() ?: return null
        when (p.status) {
            "expired" -> throw WrenTVException("This code has expired", 404)
            "approved" -> {}
            else -> return null
        }
        val hash = p.token_hash ?: return null
        val s = session(request("/auth/v1/verify", buildJsonObject { put("type", "magiclink"); put("token_hash", hash) }), "")
            ?: throw WrenTVException("Sign-in failed")
        acceptInvites(s)
        return s
    }

    @Serializable
    data class LinkedDevice(val deviceName: String = "")

    /** Looks up ([check]) or approves a sign-in code a TV shows before it knows any server; approving signs the TV in to this account. */
    suspend fun linkDevice(code: String, check: Boolean, accessToken: String): LinkedDevice {
        val text = request("/functions/v1/link-device", buildJsonObject { put("code", code); put("check", check) }, accessToken)
        return json.decodeFromString(text)
    }

    /** Turns pending share invites for this email into access. */
    suspend fun acceptInvites(s: AccountSession) {
        runCatching { request("/rest/v1/rpc/accept_invites", buildJsonObject {}, s.accessToken) }
    }
}

/**
 * Supplies the bearer token for server requests, refreshing account sessions
 * before they expire. Local-account and linked-device tokens never change.
 */
class TokenSource private constructor(
    private var local: String?,
    private val client: AccountClient?,
    private var session: AccountSession?,
    private val store: Store?,
) {
    constructor(localToken: String) : this(localToken, null, null, null)
    constructor(client: AccountClient, session: AccountSession, store: Store) : this(null, client, session, store)

    private val mutex = Mutex()
    /**
     * The last failed refresh. A rejected refresh token is never retried, and
     * other failures wait a while, so a broken sign-in can't hammer the account
     * service (which rate-limits every device on the same network).
     */
    private var failure: Exception? = null
    private var failedAt = 0L
    private var spent = false

    val accountSession: AccountSession? get() = session
    val email: String? get() = session?.email

    suspend fun token(): String {
        local?.let { return it }
        return mutex.withLock {
            val s = session!!
            if (s.expiresAt - 60 > System.currentTimeMillis() / 1000.0) return@withLock s.accessToken
            if (spent) throw WrenTVException("Your sign-in expired", 401)
            failure?.let { if (System.currentTimeMillis() - failedAt < 30_000) throw it }
            val fresh = try {
                client!!.refresh(s)
            } catch (e: WrenTVException) {
                failure = e
                failedAt = System.currentTimeMillis()
                if (e.status == 400 || e.status == 401 || e.status == 403) {
                    spent = true
                    throw WrenTVException("Your sign-in expired", 401)
                }
                throw e
            }
            failure = null
            session = fresh
            store?.setSession(client.storeKey, fresh)
            fresh.accessToken
        }
    }
}
