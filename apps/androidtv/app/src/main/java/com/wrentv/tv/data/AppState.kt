package com.wrentv.tv.data

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/** Everything the app knows about the server and who's signed in. Lives as long as the process. */
class AppState(context: Context) {
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    val store = Store(context)

    var serverUrl by mutableStateOf<HttpUrl?>(null)
        private set
    var api by mutableStateOf<WrenTVApi?>(null)
        private set
    var user by mutableStateOf<User?>(null)
        private set
    var server by mutableStateOf<ServerInfo?>(null)
        private set
    var libraries by mutableStateOf<List<Library>>(emptyList())
        private set
    /** Bumped when the server reports library changes; screens reload on change. */
    var refreshTick by mutableIntStateOf(0)
    var restoring by mutableStateOf(true)
        private set
    /** What this user may do: stream, download to this TV, how many at once. */
    var limits by mutableStateOf<UserLimitsInfo?>(null)
        private set

    /** Signed in to an account that can't use this server yet (not shared, or the server isn't claimed). */
    var accessIssue by mutableStateOf<String?>(null)
    var accountEmail by mutableStateOf<String?>(null)
        private set

    /** Movies and episodes saved on this TV for offline viewing. */
    val downloads = DownloadManager(context, scope)

    /** Streaming is on for this server and this user may use it. */
    val canStream: Boolean get() = (server?.streaming ?: false) && (limits?.canStream ?: true)

    val isAdmin: Boolean get() = user?.let { it.isAdmin || it.role == "owner" || it.role == "admin" } ?: false

    private var eventsJob: Job? = null
    private var picking: Job? = null

    init {
        scope.launch { restore() }
        watchNetwork(context)
    }

    private suspend fun restore() {
        try {
            val url = store.serverUrl?.toHttpUrlOrNull() ?: return
            serverUrl = url
            // The typed address may only work at home: also try every address the server published last time.
            var base = url
            var info = runCatching { WrenTVApi(url).serverInfo() }.getOrNull()
            val id = store.serverId
            if (info == null && id != null) {
                AddressPicker.best(store.serverAddresses.mapNotNull { it.toHttpUrlOrNull() }, id)?.let { found ->
                    base = found
                    info = runCatching { WrenTVApi(found).serverInfo() }.getOrNull()
                }
            }
            val i = info ?: return // Server offline: show the connect screen with the address filled in.
            server = i
            // A TV linked from a phone keeps its device token, whatever the auth mode.
            store.token(key(url))?.let { return finishSignIn(base, TokenSource(it), null) }
            val supa = i.supabase
            if (i.usesAccounts && supa != null) {
                val client = AccountClient(supa)
                val session = store.session(client.storeKey) ?: return
                finishSignIn(base, TokenSource(client, session, store), session.email)
            }
        } finally {
            restoring = false
        }
    }

    suspend fun connect(raw: String): ServerInfo {
        var s = raw.trim()
        if (!s.contains("://")) s = "http://$s"
        val parsed = s.toHttpUrlOrNull() ?: throw WrenTVException("Invalid server address")
        // No port typed: the server's default, unless it's https behind a proxy.
        val hasPort = Regex("^[a-z]+://[^/]+:\\d+").containsMatchIn(s)
        val url = if (!hasPort && parsed.scheme == "http") parsed.newBuilder().port(32500).build() else parsed
        val info = WrenTVApi(url).serverInfo()
        serverUrl = url
        server = info
        accessIssue = null
        store.serverUrl = url.toString()
        // Already signed in to this server's account service (e.g. from another server)?
        val supa = info.supabase
        if (info.usesAccounts && supa != null) {
            val client = AccountClient(supa)
            store.session(client.storeKey)?.let { finishSignIn(url, TokenSource(client, it, store), it.email) }
        }
        return info
    }

    /** Local-account servers: username and password stored on the server. */
    suspend fun login(username: String, password: String, createOwner: Boolean) {
        val url = serverUrl ?: return
        val anon = WrenTVApi(url)
        val resp = if (createOwner) anon.setupOwner(username, password) else anon.login(username, password)
        store.setToken(key(url), resp.token)
        val api = WrenTVApi(url, TokenSource(resp.token))
        limits = runCatching { api.me() }.getOrNull()?.limits
        signedIn(api, resp.user)
    }

    /** Wren TV account servers. Returns false when the new account must confirm its email before it can sign in. */
    suspend fun accountSignIn(email: String, password: String, name: String?): Boolean {
        val url = serverUrl ?: return false
        val supa = server?.supabase ?: return false
        val client = AccountClient(supa)
        val session = if (name != null) client.signUp(email, password, name) ?: return false else client.signIn(email, password)
        store.setSession(client.storeKey, session)
        finishSignIn(url, TokenSource(client, session, store), session.email)
        return true
    }

    /** Starts a sign-in approved from a phone or browser: the TV shows the code and polls. */
    suspend fun startPairing(): PairStart {
        val url = serverUrl ?: throw WrenTVException("Not connected")
        return WrenTVApi(url).startPair()
    }

    /** The page a phone opens to approve the code (what the QR code holds). */
    fun pairingLink(p: PairStart): String {
        val base = serverUrl ?: return ""
        return base.resolve(p.path.ifEmpty { "/link?code=${p.code}" })?.toString() ?: ""
    }

    /** True once approved, and the TV is then signed in; false while waiting. Throws when the code expired. */
    suspend fun pollPairing(p: PairStart): Boolean {
        val url = serverUrl ?: return false
        val r = WrenTVApi(url).pollPair(p.code, p.secret)
        val token = r.token
        if (r.status != "approved" || token == null) return false
        store.setToken(key(url), token)
        finishSignIn(url, TokenSource(token), null)
        return true
    }

    /** Re-checks access after the owner shares the server. */
    suspend fun retryAccess() {
        val url = serverUrl ?: return
        val supa = server?.supabase ?: return
        val client = AccountClient(supa)
        val session = store.session(client.storeKey) ?: return
        client.acceptInvites(session)
        server = runCatching { WrenTVApi(url).serverInfo() }.getOrNull() ?: server
        finishSignIn(url, TokenSource(client, session, store), session.email)
    }

    private suspend fun finishSignIn(url: HttpUrl, auth: TokenSource, email: String?) {
        val api = WrenTVApi(url, auth)
        accountEmail = email
        try {
            val me = api.me()
            accessIssue = null
            limits = me.limits
            signedIn(api, me.user)
            pickAddress()
        } catch (e: WrenTVException) {
            val name = server?.name ?: "this server"
            when (e.status) {
                401 -> clearCredentials()
                403 -> accessIssue = "${email ?: "This account"} doesn't have access to $name. Ask the owner to share it with this email address."
                409 -> accessIssue = "$name hasn't been set up yet. Open it in a web browser to claim it with your account."
                else -> accessIssue = e.message
            }
        } catch (e: Exception) {
            accessIssue = e.message ?: "Couldn't reach the server"
        }
    }

    private fun signedIn(api: WrenTVApi, user: User) {
        this.api = api
        this.user = user
        downloads.connect(api, limits)
        scope.launch { reloadLibraries() }
        listen()
    }

    private fun clearCredentials() {
        serverUrl?.let { store.setToken(key(it), null) }
        server?.supabase?.let { supa ->
            val client = AccountClient(supa)
            store.session(client.storeKey)?.let { s -> scope.launch { client.signOut(s) } }
            store.setSession(client.storeKey, null)
        }
        accountEmail = null
        accessIssue = null
    }

    // Addresses

    /**
     * Switches to the best address the server published (secure LAN at home,
     * the public or automatic remote address elsewhere). Runs after sign-in
     * and whenever the network changes.
     */
    fun pickAddress() {
        picking?.cancel()
        picking = scope.launch {
            val api = api ?: return@launch
            val entered = serverUrl ?: return@launch
            val info = server ?: return@launch
            val id = info.addressId
            var local = info.localUrls ?: emptyList()
            var publicUrl: String? = null
            var remoteUrl: String? = null
            val session = api.auth?.accountSession
            val supa = info.supabase
            if (session != null && supa != null) {
                AccountClient(supa).addresses(id, session)?.let { row ->
                    local = row.local_urls ?: local
                    publicUrl = row.public_url
                    remoteUrl = row.remote_url
                }
            }
            val candidates = AddressPicker.candidates(entered, local, publicUrl, remoteUrl)
            store.serverId = id
            store.serverAddresses = candidates.map { it.toString() }
            val best = AddressPicker.best(candidates, id) ?: return@launch
            val current = this@AppState.api ?: return@launch
            if (AddressPicker.key(best) == AddressPicker.key(current.baseUrl)) return@launch
            val next = WrenTVApi(best, current.auth)
            this@AppState.api = next
            downloads.connect(next, limits)
            listen()
        }
    }

    private fun watchNetwork(context: Context) {
        val cm = context.getSystemService(ConnectivityManager::class.java) ?: return
        var first = true
        cm.registerDefaultNetworkCallback(object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                if (first) { first = false; return }
                scope.launch {
                    delay(2000) // let the new network settle
                    pickAddress()
                }
            }
        })
    }

    fun signOut() {
        clearCredentials()
        eventsJob?.cancel()
        api = null
        user = null
        limits = null
        libraries = emptyList()
        downloads.disconnect()
    }

    suspend fun reloadLibraries() {
        val api = api ?: return
        runCatching { api.libraries() }.getOrNull()?.let { libraries = it }
        refreshStatus()
    }

    /**
     * Streaming can be turned on or off (or lose its debrid key) at any time,
     * and the user's limits can change, so this runs on every reconnect and
     * whenever the app comes back to the front.
     */
    suspend fun refreshStatus() {
        val api = api ?: return
        runCatching { api.serverInfo() }.getOrNull()?.let { info -> server = server?.copy(streaming = info.streaming) }
        runCatching { api.me() }.getOrNull()?.let { me ->
            limits = me.limits
            downloads.connect(api, me.limits)
        }
    }

    private fun listen() {
        eventsJob?.cancel()
        eventsJob = scope.launch {
            var first = true
            while (isActive) {
                val api = api ?: return@launch
                if (!first) refreshStatus()
                first = false
                runCatching { api.events().collect { handle(it) } }
                delay(3000)
            }
        }
    }

    private var pendingRefresh: Job? = null

    private fun handle(ev: ServerEvent) {
        when (ev.type) {
            "library.updated", "item.added", "item.updated", "scan.finished" -> scheduleRefresh()
        }
    }

    /** Coalesce bursts of events (a scan emits many) into one refresh. */
    private fun scheduleRefresh() {
        pendingRefresh?.cancel()
        pendingRefresh = scope.launch {
            delay(800)
            refreshTick++
            reloadLibraries()
        }
    }

    private fun key(url: HttpUrl) = AddressPicker.key(url)
}
