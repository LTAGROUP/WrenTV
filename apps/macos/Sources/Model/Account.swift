import Foundation

/// A signed-in Wren TV account (Supabase Auth session).
struct AccountSession: Codable, Sendable {
    var accessToken: String
    var refreshToken: String
    var expiresAt: Double
    var email: String
}

/// Talks to the Wren TV account service (Supabase Auth over REST).
struct AccountClient: Sendable {
    let url: URL
    let publishableKey: String

    init(_ info: SupabaseInfo) {
        url = URL(string: info.url)!
        publishableKey = info.publishableKey
    }

    /// Keychain account under which the session is stored; shared by every
    /// server that uses the same account service.
    var keychainKey: String { "account:" + url.absoluteString }

    private struct TokenResponse: Decodable {
        var access_token: String?
        var refresh_token: String?
        var expires_in: Double?
        var expires_at: Double?
        var user: U?
        struct U: Decodable { var email: String? }
    }

    private struct ErrorResponse: Decodable {
        var msg: String?
        var message: String?
        var error_description: String?
        var error: String?
    }

    private func request(_ path: String, body: [String: Any], bearer: String? = nil) async throws -> Data {
        var req = URLRequest(url: URL(string: path, relativeTo: url)!)
        req.httpMethod = "POST"
        req.setValue(publishableKey, forHTTPHeaderField: "apikey")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        if let bearer { req.setValue("Bearer \(bearer)", forHTTPHeaderField: "Authorization") }
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        let (data, resp) = try await URLSession.shared.data(for: req)
        let status = (resp as? HTTPURLResponse)?.statusCode ?? 0
        if status >= 400 {
            let e = try? JSONDecoder().decode(ErrorResponse.self, from: data)
            let msg = e?.msg ?? e?.error_description ?? e?.message ?? e?.error ?? HTTPURLResponse.localizedString(forStatusCode: status)
            throw WrenTVError(message: msg.prefix(1).uppercased() + msg.dropFirst(), status: status)
        }
        return data
    }

    private func session(from data: Data, email fallback: String) throws -> AccountSession? {
        let t = try JSONDecoder().decode(TokenResponse.self, from: data)
        guard let access = t.access_token, let refresh = t.refresh_token else { return nil }
        let expires = t.expires_at ?? Date().timeIntervalSince1970 + (t.expires_in ?? 3600)
        return AccountSession(accessToken: access, refreshToken: refresh, expiresAt: expires, email: t.user?.email ?? fallback)
    }

    func signIn(email: String, password: String) async throws -> AccountSession {
        let data = try await request("/auth/v1/token?grant_type=password", body: ["email": email, "password": password])
        guard let s = try session(from: data, email: email) else { throw WrenTVError(message: "Sign-in failed") }
        await acceptInvites(s)
        return s
    }

    /// Returns nil when the account must confirm its email before signing in.
    func signUp(email: String, password: String, name: String) async throws -> AccountSession? {
        let data = try await request("/auth/v1/signup", body: ["email": email, "password": password, "data": ["display_name": name]])
        guard let s = try session(from: data, email: email) else { return nil }
        await acceptInvites(s)
        return s
    }

    func refresh(_ s: AccountSession) async throws -> AccountSession {
        let data = try await request("/auth/v1/token?grant_type=refresh_token", body: ["refresh_token": s.refreshToken])
        guard let n = try session(from: data, email: s.email) else { throw WrenTVError(message: "Your sign-in expired", status: 401) }
        return n
    }

    func signOut(_ s: AccountSession) async {
        _ = try? await request("/auth/v1/logout", body: [:], bearer: s.accessToken)
    }

    struct ServerAddresses: Decodable {
        var local_urls: [String]?
        var public_url: String?
        var remote_url: String?
    }

    /// The addresses a server published in its heartbeat (visible to its
    /// owner and the accounts it's shared with).
    func addresses(server id: String, _ s: AccountSession) async -> ServerAddresses? {
        guard var c = URLComponents(url: URL(string: "/rest/v1/servers", relativeTo: url)!, resolvingAgainstBaseURL: true) else { return nil }
        c.queryItems = [.init(name: "id", value: "eq." + id), .init(name: "select", value: "local_urls,public_url,remote_url")]
        guard let u = c.url else { return nil }
        var req = URLRequest(url: u)
        req.setValue(publishableKey, forHTTPHeaderField: "apikey")
        req.setValue("Bearer \(s.accessToken)", forHTTPHeaderField: "Authorization")
        guard let (data, resp) = try? await URLSession.shared.data(for: req),
              (resp as? HTTPURLResponse)?.statusCode == 200 else { return nil }
        return (try? JSONDecoder().decode([ServerAddresses].self, from: data))?.first
    }

    /// Turns pending share invites for this email into access.
    func acceptInvites(_ s: AccountSession) async {
        _ = try? await request("/rest/v1/rpc/accept_invites", body: [:], bearer: s.accessToken)
    }
}

/// Supplies the bearer token for server requests, refreshing account sessions
/// before they expire.
actor TokenSource {
    private enum Kind {
        case local(String)
        case account(AccountClient, AccountSession)
    }

    private var kind: Kind
    private var refreshing: Task<AccountSession, Error>?
    /// The last failed refresh. A rejected refresh token is never retried, and
    /// other failures wait a while, so a broken sign-in can't hammer the
    /// account service (which rate-limits every device on the same network).
    private var failure: (error: Error, at: Date, final: Bool)?

    init(localToken: String) { kind = .local(localToken) }
    init(account: AccountClient, session: AccountSession) { kind = .account(account, session) }

    var email: String? {
        if case .account(_, let s) = kind { return s.email }
        return nil
    }

    var accountSession: AccountSession? {
        if case .account(_, let s) = kind { return s }
        return nil
    }

    func token() async throws -> String {
        switch kind {
        case .local(let t):
            return t
        case .account(let client, var s):
            if s.expiresAt - 60 > Date().timeIntervalSince1970 { return s.accessToken }
            // Another part of the app may have refreshed already; its refresh
            // token replaced ours, which is now spent.
            if let saved = Keychain.session(for: client.keychainKey), saved.refreshToken != s.refreshToken, saved.expiresAt > s.expiresAt {
                s = saved
                kind = .account(client, saved)
                failure = nil
                if s.expiresAt - 60 > Date().timeIntervalSince1970 { return s.accessToken }
            }
            if let f = failure {
                if f.final { throw WrenTVError(message: "Your sign-in expired", status: 401) }
                if Date().timeIntervalSince(f.at) < 30 { throw f.error }
            }
            let task = refreshing ?? Task { [s] in try await client.refresh(s) }
            refreshing = task
            defer { refreshing = nil }
            do {
                let fresh = try await task.value
                kind = .account(client, fresh)
                failure = nil
                Keychain.setSession(fresh, for: client.keychainKey)
                return fresh.accessToken
            } catch {
                let status = (error as? WrenTVError)?.status ?? 0
                let final = status == 400 || status == 401 || status == 403
                failure = (error, Date(), final)
                if final { throw WrenTVError(message: "Your sign-in expired", status: 401) }
                throw error
            }
        }
    }
}

extension Keychain {
    static func session(for key: String) -> AccountSession? {
        guard let s = get(key) else { return nil }
        return try? JSONDecoder().decode(AccountSession.self, from: Data(s.utf8))
    }

    static func setSession(_ s: AccountSession?, for key: String) {
        set(s.flatMap { (try? JSONEncoder().encode($0)).flatMap { String(data: $0, encoding: .utf8) } }, for: key)
    }
}
