import Foundation

/// A hub as a tile: a genre, a streaming service or an admin's own.
struct HubCard: Codable, Hashable, Identifiable {
    var id: String
    var name: String
    var kind: String // genre | service | custom
    var tagline: String?
    var backdropPath: String?
    var logoPath: String?
    /// "#rrggbb" behind a service's logo.
    var color: String?

    var kindLabel: String {
        switch kind {
        case "genre": "Genre"
        case "service": "Streaming service"
        default: "Hub"
        }
    }
}

/// One row of a page or hub, resolved by the server.
struct LayoutRow: Codable, Hashable, Identifiable {
    var id: String
    /// "landscape", "items", "titles", "hubs" or "collection".
    var kind: String
    var title: String
    var subtitle: String?
    var items: [Item]?
    var titles: [TitleView]?
    var hubs: [HubCard]?
    /// A hub row's "tiles" or "logos".
    var style: String?
    var collection: WrenCollection?
    var missing: [TitleView]?
    var ranked: Bool?
    /// Set when the row is a single hub's first shelf.
    var hubId: String?
    /// Whether there's more than the row shows, so a "View all" is worth having.
    var more: Bool?
}

/// A page's rows: "home", "movies", "shows" or "library:<id>".
struct LayoutPage: Codable {
    var rows: [LayoutRow]
    var custom: Bool?
    /// Where the next page of rows starts, while more remain.
    var nextOffset: Int?
}

struct HubPage: Codable {
    var hub: HubCard
    var rows: [LayoutRow]
}

/// A row as the layout editor sees it.
struct LayoutEntry: Codable, Hashable, Identifiable {
    var type: String
    var ref: String
    var hidden: Bool?
    var title: String?
    var detail: String?

    var id: String { "\(type)|\(ref)" }
    var isHidden: Bool { hidden ?? false }
}

struct LayoutConfig: Codable {
    var entries: [LayoutEntry]
    /// Whether this is a saved layout of its own rather than the inherited one.
    var custom: Bool?
    var hasDefault: Bool?
    var options: [LayoutEntry]?
}

/// Opens a hub's page.
struct HubRoute: Hashable {
    var id: String
    var name: String
}

/// Opens the layout editor for one page.
struct LayoutEditorRoute: Hashable {
    var scope: String
    var title: String
}

extension WrenTVAPI {
    /// A page's rows; servers from before layouts answer 404.
    func layout(_ scope: String, offset: Int = 0, limit: Int? = nil) async throws -> LayoutPage {
        var q: [URLQueryItem] = []
        if offset > 0 { q.append(.init(name: "offset", value: String(offset))) }
        if let limit { q.append(.init(name: "limit", value: String(limit))) }
        return try await get("/api/layouts/\(scope)", query: q)
    }

    func hub(_ id: String) async throws -> HubPage {
        try await get("/api/hubs/\(id.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(["/"])) ?? id)")
    }

    /// everyone edits the server's default (admins); otherwise the user's own layout.
    func layoutConfig(_ scope: String, everyone: Bool) async throws -> LayoutConfig {
        try await get("/api/layouts/\(scope)/config", query: [.init(name: "target", value: everyone ? "default" : "me")])
    }

    func setLayout(_ scope: String, everyone: Bool, entries: [LayoutEntry]) async throws {
        struct E: Encodable { var type: String; var ref: String; var hidden: Bool }
        struct B: Encodable { var entries: [E] }
        try await send("PUT", "/api/layouts/\(scope)", query: [.init(name: "target", value: everyone ? "default" : "me")],
                       body: B(entries: entries.map { E(type: $0.type, ref: $0.ref, hidden: $0.isHidden) }))
    }

    func resetLayout(_ scope: String, everyone: Bool) async throws {
        try await send("DELETE", "/api/layouts/\(scope)", query: [.init(name: "target", value: everyone ? "default" : "me")])
    }
}

extension Array where Element: Identifiable {
    /// Lists can name a title twice; it's shown once.
    func uniqued() -> [Element] {
        var seen = Set<Element.ID>()
        return filter { seen.insert($0.id).inserted }
    }
}
