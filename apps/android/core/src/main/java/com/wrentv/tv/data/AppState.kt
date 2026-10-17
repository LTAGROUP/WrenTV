package com.wrentv.tv.data

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import kotlinx.coroutines.CancellationException
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
    private val appContext: Context = context.applicationContext

    /** A wrentv:// link from the TV's home screen or a voice search, waiting to be opened. */
    var link by mutableStateOf<android.net.Uri?>(null)
    /** Text for the search page to start with (from a voice search). */
    var searchSeed by mutableStateOf<String?>(null)

    /** Clicks as focus moves, kept on this TV. */
    private var uiSoundsOn by mutableStateOf(store.bool("uiSounds", true))
    var uiSounds: Boolean
        get() = uiSoundsOn
        set(v) { uiSoundsOn = v; store.setBool("uiSounds", v) }
    /** A show's or movie's theme on its details page, kept on this TV. */
    private var themeMusicOn by mutableStateOf(store.bool("themeMusic", true))
    var themeMusic: Boolean
        get() = themeMusicOn
        set(v) { themeMusicOn = v; store.setBool("themeMusic", v) }

    var serverUrl by mutableStateOf<HttpUrl?>(null)
        private set

    /** The server's live TV channels, kept for flipping through them in the player. */
    var liveChannels by mutableStateOf<List<LiveChannel>>(emptyList())
        private set
    val liveTv: Boolean get() = server?.liveTv == true
    /** The channel last watched on this TV, where Live TV starts. */
    var lastChannel: String?
        get() = store.string("lastChannel", "").ifEmpty { null }
        set(v) { store.setString("lastChannel", v ?: "") }

    /** What was searched for on this TV, newest first. */
    var recentSearches: List<String>
        get() = store.string("recentSearches", "").split('\n').filter { it.isNotBlank() }
        set(v) { store.setString("recentSearches", v.joinToString("\n")) }

    fun rememberSearch(q: String) {
        val t = q.trim().takeIf { it.length >= 2 } ?: return
        recentSearches = (listOf(t) + recentSearches.filterNot { it.equals(t, ignoreCase = true) }).take(8)
    }

    /** Fetches the channels and what's on them now. */
    suspend fun loadLiveChannels(): List<LiveChannel> {
        val api = api ?: return liveChannels
        return api.liveChannels().also { liveChannels = it }
    }
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

    /** Signed in to a Wren TV account but no server picked yet: the TV lists the account's servers. */
    var account by mutableStateOf<AccountSession?>(null)
        private set
    var accountServers by mutableStateOf<List<AccountServer>?>(null)
        private set
    var accountServersError by mutableStateOf<String?>(null)
        private set
    private val accounts = AccountClient(WrenAccounts)

    /** Movies and episodes saved on this TV for offline viewing. */
    val downloads = DownloadManager(context, scope)

    /** Streaming is on for this server and this user may use it. */
    val canStream: Boolean get() = (server?.streaming ?: false) && (limits?.canStream ?: true)

    val isAdmin: Boolean get() = user?.let { it.isAdmin || it.role == "owner" || it.role == "admin" } ?: false

    /** Extra clean-up on sign-out, like the TV's Play Next row. */
    var onSignOut: (suspend (Context) -> Unit)? = null

    private var eventsJob: Job? = null
    private var picking: Job? = null

    init {
        scope.launch { restore() }
        watchNetwork(context)
    }

    private suspend fun restore() {
        try {
            val url = store.serverUrl?.toHttpUrlOrNull() ?: return restoreAccount()
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
            // Server offline: list the account's servers, or show the connect screen.
            val i = info ?: return restoreAccount()
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

    // Wren TV account

    /** Picks up a saved account sign-in and lists its servers. */
    private suspend fun restoreAccount() {
        val session = store.session(accounts.storeKey) ?: return
        account = session
        accountEmail = session.email
        loadAccountServers(open = true)
    }

    /** Signs in to a Wren TV account without a server address. Returns false when a new account must confirm its email first. */
    suspend fun signInToAccount(email: String, password: String, name: String?): Boolean {
        val session = if (name != null) accounts.signUp(email, password, name) ?: return false else accounts.signIn(email, password)
        accountSignedIn(session)
        return true
    }

    /** A code for this TV, approved from a phone or browser signed in to the account. */
    suspend fun startAccountLink(): DeviceLink = accounts.startLink(deviceName())

    /** True once approved, and the TV is then signed in; false while waiting. Throws (404) when the code expired. */
    suspend fun pollAccountLink(link: DeviceLink): Boolean {
        val session = accounts.pollLink(link) ?: return false
        accountSignedIn(session)
        return true
    }

    private suspend fun accountSignedIn(session: AccountSession) {
        store.setSession(accounts.storeKey, session)
        account = session
        accountEmail = session.email
        // The sign-in screen goes away now, taking its coroutine with it: connect from the app's own scope.
        scope.launch { loadAccountServers(open = true) }
    }

    /**
     * Lists the servers the account owns or has been shared, accepting any waiting invites first.
     * With [open], goes straight to the server when there's only one, or only one online.
     */
    suspend fun loadAccountServers(open: Boolean = false) {
        var session = account ?: return
        if (session.expiresAt - 60 < System.currentTimeMillis() / 1000.0) {
            val fresh = runCatching { accounts.refresh(session) }.getOrNull()
            if (fresh == null) {
                // The sign-in expired for good: sign in again.
                store.setSession(accounts.storeKey, null)
                account = null
                return
            }
            session = fresh
            account = fresh
            store.setSession(accounts.storeKey, fresh)
        }
        accounts.acceptInvites(session)
        val servers = try {
            accounts.servers(session).also { accountServersError = null }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            accountServersError = e.message
            return
        }
        accountServers = servers
        if (!open) return
        val pick = servers.singleOrNull() ?: servers.filter { it.online }.singleOrNull() ?: return
        try {
            open(pick)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            accountServersError = e.message
        }
    }

    /** Connects to one of the account's servers at whichever published address answers (home network first, then remote). */
    suspend fun open(s: AccountServer) {
        val session = account ?: return
        val url = AddressPicker.best(s.candidates, s.id)
            ?: throw WrenTVException("Couldn't reach ${s.name}. Make sure it's running, and connected to the internet if you're away from home.")
        val info = WrenTVApi(url).serverInfo()
        serverUrl = url
        server = info
        accessIssue = null
        store.serverUrl = url.toString()
        store.serverId = s.id
        store.serverAddresses = s.candidates.map { it.toString() }
        val client = AccountClient(info.supabase ?: WrenAccounts)
        if (client.storeKey != accounts.storeKey) store.setSession(client.storeKey, session)
        finishSignIn(url, TokenSource(client, session, store), session.email)
    }

    /** Leaves this server but stays signed in to the account. */
    fun switchServer() {
        leaveServer()
        serverUrl?.let { store.setToken(key(it), null) }
        serverUrl = null
        server = null
        accessIssue = null
        store.serverUrl = null
        scope.launch { loadAccountServers() }
    }

    private fun leaveServer() {
        eventsJob?.cancel()
        api = null
        user = null
        limits = null
        libraries = emptyList()
        downloads.disconnect()
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
        account?.let { s -> scope.launch { accounts.signOut(s) } }
        store.setSession(accounts.storeKey, null)
        account = null
        accountServers = null
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
        // Account servers are found again through the account, so forget the address too.
        val viaAccount = account != null || server?.usesAccounts == true
        onSignOut?.let { f -> scope.launch { f(appContext) } }
        clearCredentials()
        leaveServer()
        if (viaAccount) {
            serverUrl = null
            server = null
            store.serverUrl = null
        }
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
        runCatching { api.serverInfo() }.getOrNull()?.let { info -> server = server?.copy(streaming = info.streaming, liveTv = info.liveTv) }
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
