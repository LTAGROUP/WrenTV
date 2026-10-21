import AppKit
import Foundation
import Network
import Observation

@MainActor
@Observable
final class AppState {
    var serverURL: URL?
    var api: WrenTVAPI?
    var user: User?
    var server: ServerInfo?
    var libraries: [Library] = []
    /// Bumped when the server reports library changes; views reload on change.
    var refreshTick = 0
    /// The server's live TV channels, kept for flipping through them in the player.
    var liveChannels: [LiveChannel] = []
    var restoring = true
    /// What this user may do: stream, download to this Mac, how many at once.
    var limits: UserLimitsInfo?
    /// Movies and episodes saved on this Mac for offline viewing.
    let downloads = DownloadManager()

    /// Streaming is on for this server and this user may use it.
    var canStream: Bool { (server?.streaming ?? false) && (limits?.canStream ?? true) }

    private var eventsTask: Task<Void, Never>?
    private var pathMonitor: NWPathMonitor?
    private var picking: Task<Void, Never>?

    /// Signed in to an account that can't use this server yet (not shared, or
    /// the server isn't claimed). Shown on the sign-in screen.
    var accessIssue: String?
    var accountEmail: String?

    /// Signed in to a Wren TV account but no server picked yet: the app
    /// lists the account's servers.
    var account: AccountSession?
    var accountServers: [AccountServer]?
    var accountServersError: String?
    private let accountClient = AccountClient(.wrenTV)

    var isAdmin: Bool { user.map { $0.isAdmin || $0.role == "owner" || $0.role == "admin" } ?? false }

    init() {
        Self.adoptLegacyDefaults()
        Task { await restore() }
        watchNetwork()
    }

    /// Copies settings saved under the app's pre-rename bundle id.
    private static func adoptLegacyDefaults() {
        let d = UserDefaults.standard
        guard d.string(forKey: "serverURL") == nil,
              let old = d.persistentDomain(forName: "com.haven.mac") else { return }
        for (k, v) in old where d.object(forKey: k) == nil { d.set(v, forKey: k) }
    }

    private func restore() async {
        defer { restoring = false }
        guard let s = UserDefaults.standard.string(forKey: "serverURL"), let url = URL(string: s) else {
            await restoreAccount()
            return
        }
        serverURL = url
        // The typed address may only work at home: also try every address the
        // server published last time.
        var base = url
        var info = try? await WrenTVAPI(baseURL: url).serverInfo()
        if info == nil, let id = UserDefaults.standard.string(forKey: "serverId"),
           let known = UserDefaults.standard.stringArray(forKey: "serverAddresses"),
           let found = await AddressPicker.best(known.compactMap(URL.init(string:)), id: id) {
            base = found
            info = try? await WrenTVAPI(baseURL: found).serverInfo()
        }
        guard let info else {
            // Server offline: list the account's servers, or show the connect
            // screen with the URL filled in.
            await restoreAccount()
            return
        }
        server = info
        // Account servers can also have local accounts, so fall back to a saved local token.
        if info.usesAccounts, let supa = info.supabase, let session = Keychain.session(for: AccountClient(supa).keychainKey) {
            await finishSignIn(url: base, auth: TokenSource(account: AccountClient(supa), session: session), email: session.email)
        } else if let token = Keychain.get(s) {
            await finishSignIn(url: url, auth: TokenSource(localToken: token), email: nil)
        }
    }

    // MARK: Wren TV account

    /// Picks up a saved account sign-in and lists its servers.
    private func restoreAccount() async {
        guard let session = Keychain.session(for: accountClient.keychainKey) else { return }
        account = session
        accountEmail = session.email
        await loadAccountServers()
        if let servers = accountServers, servers.count == 1 {
            try? await open(servers[0])
        }
    }

    /// Signs in to a Wren TV account without a server address. Returns false
    /// when a new account must confirm its email first.
    @discardableResult
    func signInToAccount(email: String, password: String, name: String?) async throws -> Bool {
        let session: AccountSession
        if let name {
            guard let s = try await accountClient.signUp(email: email, password: password, name: name) else { return false }
            session = s
        } else {
            session = try await accountClient.signIn(email: email, password: password)
        }
        Keychain.setSession(session, for: accountClient.keychainKey)
        account = session
        accountEmail = session.email
        await loadAccountServers()
        // One server: go straight to it.
        if let servers = accountServers, servers.count == 1 {
            try? await open(servers[0])
        }
        return true
    }

    /// Lists the servers the account owns or has been shared, accepting any
    /// waiting invites first.
    func loadAccountServers() async {
        guard var session = account else { return }
        if session.expiresAt - 60 < Date().timeIntervalSince1970 {
            guard let fresh = try? await accountClient.refresh(session) else {
                // The sign-in expired for good: sign in again.
                Keychain.setSession(nil, for: accountClient.keychainKey)
                account = nil
                return
            }
            session = fresh
            account = fresh
            Keychain.setSession(fresh, for: accountClient.keychainKey)
        }
        await accountClient.acceptInvites(session)
        do {
            accountServers = try await accountClient.servers(session)
            accountServersError = nil
        } catch {
            accountServersError = error.localizedDescription
        }
    }

    /// Connects to one of the account's servers at whichever published address
    /// answers (home network first, then the remote addresses).
    func open(_ server: AccountServer) async throws {
        guard let session = account else { return }
        guard let url = await AddressPicker.best(server.candidates, id: server.id) else {
            throw WrenTVError(message: "Couldn't reach \(server.name). Make sure it's running, and connected to the internet if you're away from home.")
        }
        let info = try await WrenTVAPI(baseURL: url).serverInfo()
        serverURL = url
        self.server = info
        accessIssue = nil
        let d = UserDefaults.standard
        d.set(url.absoluteString, forKey: "serverURL")
        d.set(server.id, forKey: "serverId")
        d.set(server.candidates.map(\.absoluteString), forKey: "serverAddresses")
        let client = AccountClient(info.supabase ?? .wrenTV)
        if client.keychainKey != accountClient.keychainKey { Keychain.setSession(session, for: client.keychainKey) }
        await finishSignIn(url: url, auth: TokenSource(account: client, session: session), email: session.email)
    }

    /// Leaves this server but stays signed in to the account.
    func switchServer() {
        eventsTask?.cancel()
        api = nil
        user = nil
        limits = nil
        libraries = []
        accessIssue = nil
        downloads.disconnect()
        forgetServer()
        Task { await loadAccountServers() }
    }

    private func forgetServer() {
        serverURL = nil
        server = nil
        UserDefaults.standard.removeObject(forKey: "serverURL")
    }

    func connect(_ raw: String) async throws -> ServerInfo {
        var s = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if !s.contains("://") { s = "http://" + s }
        if URL(string: s)?.port == nil, !s.dropFirst(8).contains(":"), !s.hasPrefix("https://") { s += ":32500" }
        guard let url = URL(string: s) else { throw WrenTVError(message: "Invalid server address") }
        let info = try await WrenTVAPI(baseURL: url).serverInfo()
        serverURL = url
        server = info
        accessIssue = nil
        UserDefaults.standard.set(url.absoluteString, forKey: "serverURL")
        // Already signed in to this server's account service (e.g. from another server)?
        if info.usesAccounts, let supa = info.supabase {
            let client = AccountClient(supa)
            if let session = Keychain.session(for: client.keychainKey) {
                await finishSignIn(url: url, auth: TokenSource(account: client, session: session), email: session.email)
            }
        }
        return info
    }

    /// Local-account servers: username and password stored on the server.
    func login(username: String, password: String, createOwner: Bool) async throws {
        guard let url = serverURL else { return }
        let anon = WrenTVAPI(baseURL: url)
        let resp = createOwner
            ? try await anon.setupOwner(username: username, password: password)
            : try await anon.login(username: username, password: password)
        Keychain.set(resp.token, for: url.absoluteString)
        let api = WrenTVAPI(baseURL: url, auth: TokenSource(localToken: resp.token))
        limits = (try? await api.me())?.limits
        signedIn(api: api, user: resp.user)
    }

    /// Wren TV account servers. Returns false when the new account must confirm
    /// its email before it can sign in.
    @discardableResult
    func accountSignIn(email: String, password: String, name: String?) async throws -> Bool {
        guard let url = serverURL, let supa = server?.supabase else { return false }
        let client = AccountClient(supa)
        let session: AccountSession
        if let name {
            guard let s = try await client.signUp(email: email, password: password, name: name) else { return false }
            session = s
        } else {
            session = try await client.signIn(email: email, password: password)
        }
        Keychain.setSession(session, for: client.keychainKey)
        await finishSignIn(url: url, auth: TokenSource(account: client, session: session), email: session.email)
        return true
    }

    /// Re-checks access after the owner shares the server.
    func retryAccess() async {
        guard let url = serverURL, let supa = server?.supabase else { return }
        let client = AccountClient(supa)
        guard let session = Keychain.session(for: client.keychainKey) else { return }
        await client.acceptInvites(session)
        server = (try? await WrenTVAPI(baseURL: url).serverInfo()) ?? server
        await finishSignIn(url: url, auth: TokenSource(account: client, session: session), email: session.email)
    }

    private func finishSignIn(url: URL, auth: TokenSource, email: String?) async {
        let api = WrenTVAPI(baseURL: url, auth: auth)
        accountEmail = email
        if let session = await auth.accountSession { account = session }
        do {
            let me = try await api.me()
            accessIssue = nil
            limits = me.limits
            signedIn(api: api, user: me.user)
            pickAddress()
        } catch let e as WrenTVError where e.status == 401 {
            clearCredentials()
        } catch let e as WrenTVError where e.status == 403 {
            accessIssue = "\(email ?? "This account") doesn't have access to \(server?.name ?? "this server"). Ask the owner to share it with this email address."
        } catch let e as WrenTVError where e.status == 409 {
            accessIssue = "\(server?.name ?? "This server") hasn't been set up yet. Open it in a web browser to claim it with your account."
        } catch {
            accessIssue = error.localizedDescription
        }
    }

    private func signedIn(api: WrenTVAPI, user: User) {
        self.api = api
        self.user = user
        downloads.connect(api: api, limits: limits)
        Task { await reloadLibraries() }
        // Poster size follows the account to every device.
        Task {
            if let size = try? await api.prefs().posterSize, PosterSize(rawValue: size) != nil {
                UserDefaults.standard.set(size, forKey: PosterSize.key)
            }
        }
        listen()
    }

    private func clearCredentials() {
        if let url = serverURL { Keychain.set(nil, for: url.absoluteString) }
        if let supa = server?.supabase {
            let client = AccountClient(supa)
            if let s = Keychain.session(for: client.keychainKey) { Task { await client.signOut(s) } }
            Keychain.setSession(nil, for: client.keychainKey)
        }
        if let s = account ?? Keychain.session(for: accountClient.keychainKey) {
            Task { [accountClient] in await accountClient.signOut(s) }
        }
        Keychain.setSession(nil, for: accountClient.keychainKey)
        account = nil
        accountServers = nil
        accountEmail = nil
        accessIssue = nil
    }

    // MARK: Addresses

    /// Switches to the best address the server published (secure LAN at home,
    /// the public or automatic remote address elsewhere). Runs after sign-in
    /// and whenever the Mac's network changes.
    func pickAddress() {
        picking?.cancel()
        picking = Task { [weak self] in
            guard let self, let api = self.api, let entered = self.serverURL, let info = self.server else { return }
            let id = (info.serverId?.isEmpty == false ? info.serverId : info.machineId) ?? info.machineId
            var local = info.localUrls ?? []
            var publicUrl: String?, remoteUrl: String?
            if let auth = api.auth, let session = await auth.accountSession, let supa = info.supabase,
               let row = await AccountClient(supa).addresses(server: id, session) {
                local = row.local_urls ?? local
                publicUrl = row.public_url
                remoteUrl = row.remote_url
            }
            let candidates = AddressPicker.candidates(entered: entered, localUrls: local, publicUrl: publicUrl, remoteUrl: remoteUrl)
            UserDefaults.standard.set(id, forKey: "serverId")
            UserDefaults.standard.set(candidates.map(\.absoluteString), forKey: "serverAddresses")
            guard let best = await AddressPicker.best(candidates, id: id), !Task.isCancelled,
                  let current = self.api, AddressPicker.key(best) != AddressPicker.key(current.baseURL) else { return }
            self.api = WrenTVAPI(baseURL: best, auth: current.auth)
            self.downloads.connect(api: self.api!, limits: self.limits)
            self.listen()
        }
    }

    private func watchNetwork() {
        let m = NWPathMonitor()
        var first = true
        m.pathUpdateHandler = { [weak self] path in
            guard path.status == .satisfied else { return }
            if first { first = false; return }
            Task { @MainActor [weak self] in
                try? await Task.sleep(for: .seconds(2)) // let the new network settle
                self?.pickAddress()
            }
        }
        m.start(queue: .main)
        pathMonitor = m
    }

    func signOut() {
        // Account servers are found through the account next time, not by address.
        if server?.usesAccounts == true || account != nil { forgetServer() }
        clearCredentials()
        eventsTask?.cancel()
        api = nil
        user = nil
        limits = nil
        libraries = []
        downloads.disconnect()
    }

    func reloadLibraries() async {
        guard let api else { return }
        if let libs = try? await api.libraries() { libraries = libs }
        await refreshStatus()
    }

    /// Streaming can be turned on or off (or lose its debrid key) at any
    /// time, and the user's limits can change, so this runs on every
    /// reconnect and whenever the app comes to the front.
    func refreshStatus() async {
        guard let api else { return }
        if let info = try? await api.serverInfo() {
            server?.streaming = info.streaming
            server?.liveTv = info.liveTv
        }
        if let me = try? await api.me() {
            limits = me.limits
            downloads.connect(api: api, limits: me.limits)
        }
    }

    @ObservationIgnored private var activeObserver: NSObjectProtocol?

    private func listen() {
        eventsTask?.cancel()
        if activeObserver == nil {
            activeObserver = NotificationCenter.default.addObserver(forName: NSApplication.didBecomeActiveNotification, object: nil, queue: .main) { [weak self] _ in
                Task { @MainActor in await self?.refreshStatus() }
            }
        }
        eventsTask = Task { [weak self] in
            var first = true
            while !Task.isCancelled {
                guard let api = self?.api else { return }
                if !first { await self?.refreshStatus() }
                first = false
                do {
                    for try await ev in api.events() {
                        self?.handle(ev)
                    }
                } catch {}
                try? await Task.sleep(for: .seconds(3))
            }
        }
    }

    private var pendingRefresh: Task<Void, Never>?

    private func handle(_ ev: ServerEvent) {
        switch ev.type {
        case "library.updated", "item.added", "item.updated", "scan.finished":
            scheduleRefresh()
        default:
            break
        }
    }

    /// Coalesce bursts of events (a scan emits many) into one refresh.
    private func scheduleRefresh() {
        pendingRefresh?.cancel()
        pendingRefresh = Task {
            try? await Task.sleep(for: .milliseconds(800))
            guard !Task.isCancelled else { return }
            refreshTick += 1
            await reloadLibraries()
        }
    }
}
