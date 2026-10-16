import Foundation
import UIKit

struct WrenTVError: LocalizedError {
    var message: String
    var status: Int = 0
    var errorDescription: String? { message }
}

/// Thin async client for the Wren TV server API.
struct WrenTVAPI: Sendable {
    let baseURL: URL
    var auth: TokenSource?

    private static let session: URLSession = {
        let c = URLSessionConfiguration.default
        c.timeoutIntervalForRequest = 30
        return URLSession(configuration: c)
    }()

    static let decoder = JSONDecoder()
    static let encoder = JSONEncoder()

    func url(_ path: String, query: [URLQueryItem] = []) -> URL {
        var c = URLComponents(url: baseURL, resolvingAgainstBaseURL: false)!
        c.path = (c.path.hasSuffix("/") ? String(c.path.dropLast()) : c.path) + path
        if !query.isEmpty { c.queryItems = query }
        return c.url!
    }

    /// Absolute URL for a server-relative path like "/stream/…".
    func absolute(_ path: String) -> URL {
        URL(string: path, relativeTo: baseURL)!.absoluteURL
    }

    func imageURL(_ path: String?, size: String = "w342") -> URL? {
        guard let path, !path.isEmpty else { return nil }
        let file = path.hasPrefix("/") ? String(path.dropFirst()) : path
        return url("/api/images/\(size)/\(file)")
    }

    /// Shown next to sessions on the server ("Wren TV for iPhone").
    static let clientName = "Wren TV for \(UIDevice.current.userInterfaceIdiom == .pad ? "iPad" : "iPhone")"

    @discardableResult
    func send(_ method: String, _ path: String, query: [URLQueryItem] = [], body: (any Encodable)? = nil, timeout: TimeInterval? = nil) async throws -> (Data, Int) {
        var req = URLRequest(url: url(path, query: query))
        req.httpMethod = method
        if let timeout { req.timeoutInterval = timeout }
        req.setValue(Self.clientName, forHTTPHeaderField: "X-WrenTV-Client")
        if let auth { req.setValue("Bearer \(try await auth.token())", forHTTPHeaderField: "Authorization") }
        if let body {
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            req.httpBody = try Self.encoder.encode(body)
        }
        let (data, resp) = try await Self.session.data(for: req)
        let status = (resp as? HTTPURLResponse)?.statusCode ?? 0
        if status >= 400 {
            let msg = (try? JSONDecoder().decode([String: String].self, from: data))?["error"]
            throw WrenTVError(message: msg ?? HTTPURLResponse.localizedString(forStatusCode: status), status: status)
        }
        return (data, status)
    }

    func get<T: Decodable>(_ path: String, query: [URLQueryItem] = []) async throws -> T {
        let (data, _) = try await send("GET", path, query: query)
        return try Self.decoder.decode(T.self, from: data)
    }

    func post<T: Decodable>(_ path: String, _ body: (any Encodable)? = nil) async throws -> T {
        let (data, _) = try await send("POST", path, body: body)
        return try Self.decoder.decode(T.self, from: data)
    }

    // MARK: Endpoints

    func serverInfo() async throws -> ServerInfo { try await get("/api/server") }

    func login(username: String, password: String) async throws -> AuthResponse {
        try await post("/api/auth/login", ["username": username, "password": password, "device": await MainActor.run { UIDevice.current.name }])
    }

    func setupOwner(username: String, password: String) async throws -> AuthResponse {
        try await post("/api/setup/owner", ["username": username, "password": password, "displayName": username])
    }

    func me() async throws -> MeResponse { try await get("/api/me") }
    func home() async throws -> HomeResponse { try await get("/api/home") }
    func libraries() async throws -> [Library] { try await get("/api/libraries") }

    func items(library: String, sort: String, genre: String? = nil, unwatched: Bool = false) async throws -> ItemPage {
        var q: [URLQueryItem] = [.init(name: "sort", value: sort), .init(name: "limit", value: "5000")]
        if let genre { q.append(.init(name: "genre", value: genre)) }
        if unwatched { q.append(.init(name: "unwatched", value: "1")) }
        return try await get("/api/libraries/\(library)/items", query: q)
    }

    // Genres & collections
    func genres(library: String? = nil) async throws -> [GenreCount] {
        try await get("/api/genres", query: library.map { [.init(name: "libraryId", value: $0)] } ?? [])
    }

    func genreItems(_ genre: String, library: String? = nil, kind: String? = nil, sort: String = "title", unwatched: Bool = false) async throws -> ItemPage {
        var q: [URLQueryItem] = [.init(name: "genre", value: genre), .init(name: "sort", value: sort)]
        if let library { q.append(.init(name: "libraryId", value: library)) }
        if let kind { q.append(.init(name: "kind", value: kind)) }
        if unwatched { q.append(.init(name: "unwatched", value: "1")) }
        return try await get("/api/genres/items", query: q)
    }

    func collections(library: String? = nil) async throws -> [WrenCollection] {
        try await get("/api/collections", query: library.map { [.init(name: "libraryId", value: $0)] } ?? [])
    }

    func collection(_ id: String, sort: String? = nil) async throws -> CollectionDetail {
        try await get("/api/collections/\(id)", query: sort.map { [.init(name: "sort", value: $0)] } ?? [])
    }

    func addToCollection(_ id: String, items: [String]) async throws {
        try await send("POST", "/api/collections/\(id)/items", body: ["itemIds": items])
    }

    func removeFromCollection(_ id: String, item: String) async throws {
        try await send("DELETE", "/api/collections/\(id)/items/\(item)")
    }

    func itemCollections(_ id: String) async throws -> [String] { try await get("/api/items/\(id)/collections") }

    // Preferences follow the user across devices.
    func prefs() async throws -> UserPrefs { try await get("/api/me/prefs") }
    func setPrefs(_ p: UserPrefs) async throws -> UserPrefs {
        let (data, _) = try await send("PATCH", "/api/me/prefs", body: p)
        return try Self.decoder.decode(UserPrefs.self, from: data)
    }

    func stats(days: Int) async throws -> StatsResponse {
        let tz = TimeZone.current.secondsFromGMT()
        return try await get("/api/admin/stats", query: [.init(name: "days", value: String(days)), .init(name: "tz", value: String(tz))])
    }

    func item(_ id: String) async throws -> ItemDetail { try await get("/api/items/\(id)") }
    func extras(_ id: String) async throws -> Extras { try await get("/api/items/\(id)/extras") }
    func discoverHome() async throws -> DiscoverHome { try await get("/api/discover/home") }
    func catalog(type: String, sort: String, genre: Int?, page: Int) async throws -> CatalogPage {
        try await get("/api/discover/browse", query: [.init(name: "type", value: type), .init(name: "sort", value: sort),
                                                      .init(name: "genre", value: genre.map(String.init) ?? ""), .init(name: "page", value: String(page))])
    }
    func trending() async throws -> TrendingResponse { try await get("/api/discover/trending") }
    func search(_ q: String) async throws -> [Item] { try await get("/api/search", query: [.init(name: "q", value: q)]) }

    /// Returns the play response and whether the source is still pending (HTTP 202).
    func play(_ id: String, _ req: PlayRequest) async throws -> PlayResponse {
        // A first play can probe and index a remote file before answering.
        let (data, _) = try await send("POST", "/api/items/\(id)/play", body: req, timeout: 90)
        return try Self.decoder.decode(PlayResponse.self, from: data)
    }

    func reportProgress(_ id: String, position: Double, duration: Double) async {
        struct P: Encodable { var position: Double; var duration: Double }
        _ = try? await send("POST", "/api/items/\(id)/progress", body: P(position: position, duration: duration))
    }

    func setWatched(_ id: String, _ watched: Bool) async throws {
        try await send("POST", "/api/items/\(id)/watched", body: ["watched": watched])
    }

    // TV sign-in: a TV shows a code (and a QR of it); approving signs it in as this user.
    func pairInfo(_ code: String) async throws -> PairInfo { try await get("/api/pair/\(code)") }
    func approvePair(_ code: String) async throws { try await send("POST", "/api/pair/\(code)/approve") }

    func stopSession(_ sid: String) async {
        _ = try? await send("DELETE", "/api/sessions/\(sid)")
    }

    // Streaming
    func discoverSearch(_ q: String) async throws -> DiscoverSearch {
        try await get("/api/discover/search", query: [.init(name: "q", value: q)])
    }
    func addStreamTitle(tmdbId: Int, mediaType: String) async throws -> Item {
        struct B: Encodable { var tmdbId: Int; var mediaType: String }
        return try await post("/api/streaming/titles", B(tmdbId: tmdbId, mediaType: mediaType))
    }
    /// A link to save the item's file on this device; status "pending" while
    /// a streamed title is still being found.
    func deviceDownload(_ id: String, fileId: String?) async throws -> DeviceDownload {
        try await post("/api/items/\(id)/download", ["fileId": fileId ?? ""])
    }

    // Admin
    func serverDownload(_ id: String) async throws -> Int {
        let r: [String: Int] = try await post("/api/items/\(id)/server-download")
        return r["queued"] ?? 0
    }
    func forgetStreams(_ id: String) async throws { try await send("DELETE", "/api/items/\(id)/streams") }
    func serverDownloads() async throws -> [ServerDownload] { try await get("/api/admin/downloads") }
    func cancelServerDownload(_ id: String) async throws { try await send("DELETE", "/api/admin/downloads/\(id)") }
    func retryServerDownload(_ id: String) async throws { try await send("POST", "/api/admin/downloads/\(id)/retry") }
    func checkDebrid(provider: String, key: String) async throws -> DebridAccount {
        try await post("/api/admin/streaming/check-debrid", ["provider": provider, "key": key])
    }
    func checkAddon(_ url: String) async throws -> AddonSetting {
        let r: [String: String] = try await post("/api/admin/streaming/check-addon", ["url": url])
        return AddonSetting(name: r["name"] ?? "", url: r["url"] ?? url, enabled: true)
    }
    func users() async throws -> [ServerUser] { try await get("/api/users") }
    func setUserLimits(_ id: String, _ l: UserLimitsPatch) async throws {
        struct B: Encodable { var limits: UserLimitsPatch }
        try await send("PATCH", "/api/users/\(id)", body: B(limits: l))
    }
    func settings() async throws -> ServerSettings { try await get("/api/admin/settings") }
    func patchSettings(_ patch: [String: AnyEncodable]) async throws -> ServerSettings {
        let (data, _) = try await send("PATCH", "/api/admin/settings", body: patch)
        return try Self.decoder.decode(ServerSettings.self, from: data)
    }
    func createLibrary(name: String, type: String, paths: [String]) async throws -> Library {
        struct B: Encodable { var name: String; var type: String; var paths: [String] }
        return try await post("/api/libraries", B(name: name, type: type, paths: paths))
    }
    func deleteLibrary(_ id: String) async throws { try await send("DELETE", "/api/libraries/\(id)") }
    func scanLibrary(_ id: String) async throws { try await send("POST", "/api/libraries/\(id)/scan") }

    /// Server-sent events: library changes and scans.
    func events() -> AsyncThrowingStream<ServerEvent, Error> {
        AsyncThrowingStream { cont in
            let task = Task {
                var req = URLRequest(url: url("/api/events"))
                req.timeoutInterval = .infinity
                do {
                    if let auth { req.setValue("Bearer \(try await auth.token())", forHTTPHeaderField: "Authorization") }
                    let (bytes, _) = try await URLSession.shared.bytes(for: req)
                    for try await line in bytes.lines where line.hasPrefix("data:") {
                        let json = line.dropFirst(5).trimmingCharacters(in: .whitespaces)
                        if let ev = try? Self.decoder.decode(ServerEvent.self, from: Data(json.utf8)) {
                            cont.yield(ev)
                        }
                    }
                    cont.finish()
                } catch {
                    cont.finish(throwing: error)
                }
            }
            cont.onTermination = { _ in task.cancel() }
        }
    }
}

struct AnyEncodable: Encodable {
    let value: any Encodable
    init(_ value: any Encodable) { self.value = value }
    func encode(to encoder: Encoder) throws { try value.encode(to: encoder) }
}
