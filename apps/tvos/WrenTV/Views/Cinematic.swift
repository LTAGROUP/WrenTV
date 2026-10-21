import SwiftUI

/*
 * Home, Movies, TV Shows, libraries, hubs and collections share one
 * cinematic layout: the focused title's details in a hero at the top, over
 * its artwork, and rows of titles below. The hero stays put; the rows
 * scroll under it so the focused row always settles on the same line.
 */

/// What the hero shows: the focused title, or the page's own details.
struct HeroEntry: Equatable {
    var id: String
    var title: String
    var backdrop: String?
    var logo: String?
    var meta: String
    var overview: String?
    var label: String?
}

/// The focused title and row of a cinematic page, set by its cards.
@MainActor
@Observable
final class Ambient {
    var entry: HeroEntry?
    var row: String?

    func show(_ e: HeroEntry, row: String?) {
        if entry != e { withAnimation(.easeOut(duration: 0.25)) { entry = e } }
        if let row, self.row != row { self.row = row }
    }
}

private struct CinematicRowKey: EnvironmentKey {
    static let defaultValue: String? = nil
}

extension EnvironmentValues {
    /// The cinematic row a card sits in, so focusing it settles that row.
    var cinematicRow: String? {
        get { self[CinematicRowKey.self] }
        set { self[CinematicRowKey.self] = newValue }
    }
}

extension View {
    /// Shows entry in the page's hero while this card has focus.
    func ambient(_ entry: @escaping () -> HeroEntry) -> some View { modifier(AmbientFocus(entry: entry)) }
}

private struct AmbientFocus: ViewModifier {
    @Environment(Ambient.self) private var ambient: Ambient?
    @Environment(\.cinematicRow) private var row
    let entry: () -> HeroEntry
    @FocusState private var focused: Bool

    func body(content: Content) -> some View {
        content
            .focused($focused)
            .onChange(of: focused) { _, on in if on { ambient?.show(entry(), row: row) } }
    }
}

/// Card sizes on cinematic pages: smaller than a grid's, so the hero has room.
/// The hero is the same height on every row, so the page never jumps.
enum Cinematic {
    /// Posters and tiles scale with the Poster Size setting.
    static var poster: CGFloat { 190 * PosterSize.current.scale }
    static var landscape: CGFloat { 360 * PosterSize.current.scale }
    static let heroHeight: CGFloat = 480
    static let gridSpacing: CGFloat = 44
    /// Posters across the screen.
    static var columns: Int { Int((1920 - TV.gutter * 2 + gridSpacing) / (poster + gridSpacing)) }
}

/// A cinematic page. fallback fills the hero until something takes focus.
struct CinematicPage<Rows: View>: View {
    @Environment(AppState.self) private var state
    var fallback: HeroEntry?
    var label: String?
    @ViewBuilder var rows: () -> Rows
    @State private var ambient = Ambient()

    var body: some View {
        let entry = ambient.entry ?? fallback
        ZStack(alignment: .topLeading) {
            backdrop(entry)
            VStack(alignment: .leading, spacing: 0) {
                HeroText(entry: entry, label: label)
                    .frame(height: Cinematic.heroHeight, alignment: .bottomLeading)
                    .padding(.horizontal, TV.gutter)
                ScrollViewReader { proxy in
                    ScrollView {
                        LazyVStack(alignment: .leading, spacing: 0) { rows() }
                            .padding(.top, 10)
                            .padding(.bottom, 500)
                    }
                    .scrollIndicators(.hidden)
                    .onChange(of: ambient.row) { _, row in
                        guard let row else { return }
                        withAnimation(.easeOut(duration: 0.3)) { proxy.scrollTo(row, anchor: .top) }
                    }
                }
            }
        }
        .environment(ambient)
        .ignoresSafeArea()
    }

    private func backdrop(_ entry: HeroEntry?) -> some View {
        ZStack {
            if let path = entry?.backdrop {
                RemoteImage(url: state.api?.imageURL(path, size: "w1280"))
                    .id(path)
                    .transition(.opacity)
            }
            LinearGradient(colors: [Color.wrenInk.opacity(0.92), Color.wrenInk.opacity(0.35), .clear], startPoint: .leading, endPoint: .trailing)
            LinearGradient(stops: [.init(color: .clear, location: 0), .init(color: Color.wrenInk.opacity(0.7), location: 0.45),
                                   .init(color: Color.wrenInk, location: 0.75)], startPoint: .top, endPoint: .bottom)
        }
        .frame(width: 1920, height: 1080)
        .animation(.easeOut(duration: 0.4), value: entry?.backdrop)
    }
}

/// The focused title's name, details and summary, low on the hero so it sits just above the rows.
private struct HeroText: View {
    @Environment(AppState.self) private var state
    let entry: HeroEntry?
    let label: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            if let e = entry {
                if let label { Text(label.uppercased()).font(.caption.weight(.bold)).foregroundStyle(Color.wrenEmber) }
                if let logo = e.logo {
                    RemoteImage(url: state.api?.imageURL(logo, size: "w500"), contentMode: .fit)
                        .frame(maxWidth: 520, maxHeight: 130, alignment: .bottomLeading)
                } else {
                    Text(e.title).font(.system(size: 58, weight: .bold)).lineLimit(1)
                }
                if !e.meta.isEmpty { Text(e.meta).font(.callout).foregroundStyle(.secondary) }
                if let o = e.overview, !o.isEmpty {
                    Text(o).font(.callout).lineLimit(3).frame(maxWidth: 980, alignment: .leading)
                }
            }
        }
        .padding(.bottom, 24)
        .id(entry?.id)
        .transition(.opacity)
        .animation(.easeOut(duration: 0.2), value: entry?.id)
    }
}

/// A row of a cinematic page: focusing anything in it settles it under the hero.
struct CinematicRow<Content: View>: View {
    let id: String
    @ViewBuilder var content: () -> Content

    var body: some View {
        content()
            .environment(\.cinematicRow, id)
            .id(id)
    }
}

/// Lines of posters, each a row of the page, for more titles than one row can hold.
struct PosterLines<T: Identifiable, Card: View>: View {
    let id: String
    let title: String
    var subtitle: String?
    let entries: [T]
    @ViewBuilder var card: (T) -> Card

    var body: some View {
        let lines = stride(from: 0, to: entries.count, by: Cinematic.columns).map { Array(entries[$0..<min($0 + Cinematic.columns, entries.count)]) }
        ForEach(Array(lines.enumerated()), id: \.offset) { i, line in
            CinematicRow(id: "\(id):\(i)") {
                VStack(alignment: .leading, spacing: 4) {
                    if i == 0 { ShelfTitle(title: title, subtitle: subtitle) }
                    HStack(alignment: .top, spacing: Cinematic.gridSpacing) {
                        ForEach(line) { card($0) }
                    }
                    .padding(.horizontal, TV.gutter)
                    .padding(.top, i == 0 ? 30 : 0)
                    .padding(.bottom, 30)
                }
                .focusSection()
            }
        }
    }
}

// MARK: Layout pages

/// A page made of the server's layout rows: Home, Movies, TV Shows or a
/// library. Servers from before layouts answer 404, and get fallback.
struct LayoutPageView<Fallback: View>: View {
    @Environment(AppState.self) private var state
    let scope: String
    var label: String?
    @ViewBuilder var fallback: () -> Fallback
    @State private var page: LayoutPage?
    @State private var error: String?
    @State private var unsupported = false
    /// Rows per request; the rest load as the page scrolls.
    private let pageRows = 6

    var body: some View {
        Group {
            if unsupported {
                fallback()
            } else {
                CinematicPage(fallback: first, label: label) {
                    if let p = page {
                        LayoutRows(rows: p.rows)
                        if p.nextOffset != nil {
                            ProgressView().frame(maxWidth: .infinity).padding(.vertical, 30)
                                .task(id: p.nextOffset) { await loadMore() }
                        }
                        if p.rows.isEmpty {
                            EmptyState(title: "Nothing here yet",
                                       message: state.isAdmin ? "Add a library from the Wren TV app on a computer or in a web browser." : "Ask the server owner to share a library.")
                        }
                    } else if let error {
                        EmptyState(title: "Couldn't load", message: error, symbol: "wifi.exclamationmark")
                    } else {
                        ProgressView().frame(maxWidth: .infinity).padding(.top, 120)
                    }
                }
            }
        }
        .task(id: "\(scope)|\(state.refreshTick)") { await load() }
    }

    /// Until a title takes focus, the hero shows the first.
    private var first: HeroEntry? {
        page?.rows.lazy.compactMap { r in
            r.items?.first?.heroEntry ?? r.titles?.first?.heroEntry ?? r.hubs?.first?.heroEntry
        }.first
    }

    private func load() async {
        guard let api = state.api else { return }
        do {
            // A refresh keeps as many rows as were showing, so the page doesn't jump.
            page = try await api.layout(scope, limit: max(pageRows, page?.rows.count ?? 0))
            error = nil
        } catch let e as WrenTVError where e.status == 404 {
            unsupported = true
        } catch {
            if page == nil { self.error = error.localizedDescription }
        }
    }

    /// The next rows, once the last ones scroll into view.
    private func loadMore() async {
        guard let api = state.api, let next = page?.nextOffset else { return }
        guard let more = try? await api.layout(scope, offset: next, limit: pageRows), var cur = page, cur.nextOffset == next else { return }
        let have = Set(cur.rows.map(\.id))
        cur.rows += more.rows.filter { !have.contains($0.id) }
        cur.nextOffset = more.nextOffset
        page = cur
    }
}

/// A hub: a genre, a streaming service or an admin's own, as shelves.
struct HubView: View {
    @Environment(AppState.self) private var state
    let route: HubRoute
    @State private var page: HubPage?
    @State private var error: String?

    var body: some View {
        let hub = page?.hub ?? HubCard(id: route.id, name: route.name, kind: "custom")
        CinematicPage(fallback: hub.heroEntry, label: hub.kindLabel) {
            if let p = page {
                LayoutRows(rows: p.rows)
                if p.rows.isEmpty { EmptyState(title: "Nothing here yet", message: "None of this hub's shelves found any titles.") }
            } else if let error {
                EmptyState(title: "Couldn't load", message: error, symbol: "wifi.exclamationmark")
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 120)
            }
        }
        .task(id: "\(route.id)|\(state.refreshTick)") {
            do {
                page = try await state.api?.hub(route.id)
                error = nil
            } catch {
                if page == nil { self.error = error.localizedDescription }
            }
        }
    }
}

/// The server's rows, each drawn the way its kind asks.
struct LayoutRows: View {
    let rows: [LayoutRow]

    var body: some View {
        ForEach(rows) { row in
            CinematicRow(id: row.id) {
                Shelf(title: row.title, subtitle: row.subtitle) {
                    switch row.kind {
                    case "landscape":
                        ForEach((row.items ?? []).uniqued()) { LandscapeCard(item: $0, width: Cinematic.landscape) }
                    case "hubs":
                        ForEach((row.hubs ?? []).uniqued()) { HubTile(hub: $0, logos: row.style == "logos") }
                    case "titles":
                        ForEach(Array((row.titles ?? []).uniqued().enumerated()), id: \.element.id) { i, t in
                            TitleCard(title: t, rank: row.ranked == true && i < 10 ? i + 1 : nil, width: Cinematic.poster)
                        }
                    default:
                        ForEach((row.items ?? []).uniqued()) { PosterCard(item: $0, width: Cinematic.poster) }
                        ForEach((row.missing ?? []).uniqued()) { TitleCard(title: $0, width: Cinematic.poster) }
                    }
                    // A collection's or hub's row leads to the rest of it.
                    if let c = row.collection {
                        // When the shelf already shows everything the card still opens the page, without "View All".
                        MoreCard(label: row.more == true ? "View All" : "Open Collection", value: c)
                    } else if let hub = row.hubId {
                        MoreCard(label: "Open \(row.title)", value: HubRoute(id: hub, name: row.title))
                    }
                }
            }
        }
    }
}

/// A hub's tile: its artwork with the name over it, or a service's logo on its colour.
struct HubTile: View {
    @Environment(AppState.self) private var state
    let hub: HubCard
    var logos = false

    var body: some View {
        NavigationLink(value: HubRoute(id: hub.id, name: hub.name)) {
            ZStack(alignment: .bottomLeading) {
                if logos, let logo = hub.logoPath {
                    (Color(hex: hub.color) ?? Color.wrenSurface)
                    RemoteImage(url: state.api?.imageURL(logo, size: "w300"), contentMode: .fit)
                        .padding(44)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    RemoteImage(url: state.api?.imageURL(hub.backdropPath, size: "w780"))
                    LinearGradient(colors: [.clear, (Color(hex: hub.color) ?? .black).opacity(0.85)], startPoint: .center, endPoint: .bottom)
                    Text(hub.name).font(.headline.bold()).lineLimit(2).padding(20)
                }
            }
            .frame(width: Cinematic.landscape, height: Cinematic.landscape * 9 / 16)
        }
        .buttonStyle(.card)
        .ambient { hub.heroEntry }
    }
}

/// The card at the end of a row that opens the whole of it.
private struct MoreCard<V: Hashable>: View {
    let label: String
    let value: V

    var body: some View {
        NavigationLink(value: value) {
            VStack(spacing: 14) {
                Image(systemName: "arrow.right").font(.title2)
                Text(label).font(.caption).multilineTextAlignment(.center).lineLimit(2)
            }
            .padding(16)
            .frame(width: Cinematic.poster, height: Cinematic.poster * 1.5)
            .background(Color.wrenSurface)
        }
        .buttonStyle(.card)
    }
}

// MARK: Collections

/// Every collection the viewer can see, a shelf per kind: the server's own,
/// franchises, lists, charts and smart collections.
struct CollectionsView: View {
    @Environment(AppState.self) private var state
    @State private var collections: [WrenCollection]?
    @State private var error: String?

    var body: some View {
        let groups = grouped
        CinematicPage(fallback: collections?.first?.heroEntry(count: nil), label: "Collections") {
            if let list = collections {
                if list.isEmpty {
                    EmptyState(title: "No collections yet",
                               message: state.isAdmin ? "Create collections in the web app: hand-picked, smart rules, or synced from TMDB."
                                                      : "Collections the server owner makes show up here.",
                               symbol: "rectangle.stack")
                }
                ForEach(groups, id: \.name) { g in
                    CinematicRow(id: "group:\(g.name)") {
                        Shelf(title: g.name, subtitle: "\(g.collections.count) collection\(g.collections.count == 1 ? "" : "s")") {
                            ForEach(g.collections) { CollectionTile(collection: $0) }
                        }
                    }
                }
            } else if let error {
                EmptyState(title: "Couldn't load", message: error, symbol: "wifi.exclamationmark")
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 120)
            }
        }
        .task(id: state.refreshTick) {
            do {
                collections = try await state.api?.collections()
                error = nil
            } catch {
                if collections == nil { self.error = error.localizedDescription }
            }
        }
    }

    /// In the server's order, which already puts the groups in theirs.
    private var grouped: [(name: String, collections: [WrenCollection])] {
        var out: [(name: String, collections: [WrenCollection])] = []
        for c in collections ?? [] {
            let g = c.group ?? "Collections"
            if let i = out.firstIndex(where: { $0.name == g }) { out[i].collections.append(c) } else { out.append((g, [c])) }
        }
        return out
    }
}

/// A collection's wide tile: its artwork with the name over it.
struct CollectionTile: View {
    @Environment(AppState.self) private var state
    let collection: WrenCollection

    var body: some View {
        let w = Cinematic.landscape
        VStack(alignment: .leading, spacing: 10) {
            NavigationLink(value: collection) {
                ZStack(alignment: .bottomLeading) {
                    RemoteImage(url: state.api?.imageURL(collection.backdropPath ?? collection.posterPath, size: "w780"))
                    LinearGradient(colors: [.clear, .black.opacity(0.8)], startPoint: .center, endPoint: .bottom)
                    Text(collection.name).font(.headline.bold()).lineLimit(2).padding(20)
                    if collection.active == false {
                        Text("OUT OF SEASON").font(.caption2.bold()).padding(.horizontal, 10).padding(.vertical, 4)
                            .background(.black.opacity(0.7), in: .capsule)
                            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading).padding(14)
                    }
                }
                .frame(width: w, height: w * 9 / 16)
            }
            .buttonStyle(.card)
            .ambient { collection.heroEntry(count: collection.itemCount.map { "\($0) titles" }) }
            Text(collection.itemCount.map { "\($0) titles · \(collection.kindLabel)" } ?? collection.kindLabel)
                .font(.caption2).foregroundStyle(.secondary).lineLimit(1)
        }
        .frame(width: w)
    }
}

// MARK: Calendar

/// When episodes air, a shelf a day: the library's shows still running and the ones being watched.
struct CalendarView: View {
    @Environment(AppState.self) private var state
    @State private var calendar: CalendarResponse?
    @State private var error: String?
    @State private var watchingOnly = false

    var body: some View {
        let eps = (calendar?.episodes ?? []).filter { !watchingOnly || $0.watching }
        let next = eps.first { $0.date >= CalendarEpisode.today } ?? eps.last
        CinematicPage(fallback: next?.heroEntry, label: "Calendar") {
            CinematicRow(id: "filter") {
                Picker("Shows", selection: $watchingOnly) {
                    Text("All Shows").tag(false)
                    Text("Shows I'm Watching").tag(true)
                }
                .pickerStyle(.segmented)
                .frame(width: 720)
                .padding(.horizontal, TV.gutter)
                .padding(.vertical, 20)
                .focusSection()
            }
            if calendar != nil {
                if eps.isEmpty {
                    EmptyState(title: watchingOnly ? "Nothing coming up for your shows" : "Nothing airing",
                               message: "Episodes from a week back to a month ahead show here, for shows in the library that are still running.",
                               symbol: "calendar")
                }
                ForEach(CalendarEpisode.days(eps), id: \.date) { day in
                    CinematicRow(id: "day:\(day.date)") {
                        Shelf(title: CalendarEpisode.dayName(day.date), subtitle: "\(day.episodes.count) episode\(day.episodes.count == 1 ? "" : "s")") {
                            ForEach(day.episodes) { CalendarCard(episode: $0) }
                        }
                    }
                }
            } else if let error {
                EmptyState(title: "Couldn't load", message: error, symbol: "wifi.exclamationmark")
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 120)
            }
        }
        .task(id: state.refreshTick) {
            do {
                calendar = try await state.api?.calendar()
                error = nil
            } catch {
                if calendar == nil { self.error = error.localizedDescription }
            }
        }
    }
}

private struct CalendarCard: View {
    @Environment(AppState.self) private var state
    let episode: CalendarEpisode

    var body: some View {
        let e = episode, w = Cinematic.landscape
        VStack(alignment: .leading, spacing: 10) {
            NavigationLink(value: ItemRoute(id: e.itemId ?? e.show.id, title: e.itemId == nil ? e.show.title : e.name)) {
                RemoteImage(url: state.api?.imageURL(e.stillPath ?? e.show.backdropPath, size: "w780"))
                    .frame(width: w, height: w * 9 / 16)
                    .overlay(alignment: .topLeading) {
                        if e.upNext || e.watched {
                            Text(e.upNext ? "UP NEXT" : "WATCHED").font(.caption2.bold()).padding(.horizontal, 10).padding(.vertical, 4)
                                .background(e.upNext ? Color.wrenEmber : .black.opacity(0.7), in: .capsule)
                                .padding(14)
                        }
                    }
            }
            .buttonStyle(.card)
            .ambient { e.heroEntry }
            Text(e.show.title).font(.caption).lineLimit(1)
            Text("\(e.code) · \(e.name)").font(.caption2).foregroundStyle(.secondary).lineLimit(1)
        }
        .frame(width: w)
    }
}

private let collectionSorts: [(String, String)] = [
    ("", "Collection order"), ("title", "Title"), ("year", "Year (newest)"), ("year_asc", "Year (oldest)"),
    ("rating", "Rating"), ("added", "Recently added"),
]

/// A collection's titles in lines of posters, and (when streaming) the ones the server doesn't have yet.
struct CollectionView: View {
    @Environment(AppState.self) private var state
    let collection: WrenCollection
    @State private var detail: CollectionDetail?
    @State private var sort = ""
    /// Sorted answers come without rows, so whether there are any is remembered.
    @State private var rowed = false

    /// A big collection shows as rows, like Home; "order" is its own order as lines of posters.
    private var sorts: [(String, String)] {
        rowed ? [("", "Rows"), ("order", "Collection order")] + collectionSorts.dropFirst() : collectionSorts
    }

    var body: some View {
        let c = detail?.collection ?? collection
        CinematicPage(fallback: c.heroEntry(count: count), label: "Collection") {
            CinematicRow(id: "sort") {
                HStack {
                    Menu {
                        Picker("Order", selection: $sort) {
                            ForEach(sorts, id: \.0) { Text($0.1).tag($0.0) }
                        }
                    } label: {
                        Label(sorts.first { $0.0 == sort }?.1 ?? "Collection order", systemImage: "arrow.up.arrow.down")
                    }
                }
                .padding(.horizontal, TV.gutter)
                .padding(.vertical, 20)
                .focusSection()
            }
            if let d = detail {
                if let rows = d.rows, !rows.isEmpty, sort.isEmpty {
                    ForEach(Array(rows.enumerated()), id: \.offset) { n, r in
                        CinematicRow(id: "row:\(n):\(r.title)") {
                            Shelf(title: r.title) {
                                if let titles = d.titles {
                                    let ts = r.index.compactMap { titles.indices.contains($0) ? titles[$0] : nil }.uniqued()
                                    ForEach(Array(ts.enumerated()), id: \.element.id) { i, t in
                                        TitleCard(title: t, rank: r.title == "Top 20" && i < 10 ? i + 1 : nil, width: Cinematic.poster)
                                    }
                                } else {
                                    ForEach(r.index.compactMap { d.items.indices.contains($0) ? d.items[$0] : nil }.uniqued()) {
                                        PosterCard(item: $0, width: Cinematic.poster)
                                    }
                                }
                            }
                        }
                    }
                } else if let titles = d.titles {
                    let list = titles.uniqued()
                    PosterLines(id: "titles", title: "All Titles", subtitle: "\(list.count { $0.item != nil }) in your library", entries: list) {
                        TitleCard(title: $0, width: Cinematic.poster)
                    }
                } else {
                    let items = d.items.uniqued(), missing = (d.missing ?? []).uniqued()
                    if !items.isEmpty {
                        PosterLines(id: "items", title: "In Your Library", entries: items) { PosterCard(item: $0, width: Cinematic.poster) }
                    }
                    if !missing.isEmpty {
                        PosterLines(id: "missing", title: "Not in Your Library", entries: missing) { TitleCard(title: $0, width: Cinematic.poster) }
                    }
                    if items.isEmpty && missing.isEmpty { EmptyState(title: "Nothing in this collection yet") }
                }
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 120)
            }
        }
        .task(id: "\(collection.id)|\(sort)|\(state.refreshTick)") {
            let server = sort.isEmpty || sort == "order" ? nil : sort
            if let d = try? await state.api?.collection(collection.id, sort: server) {
                detail = d
                if !(d.rows ?? []).isEmpty { rowed = true }
            }
        }
    }

    private var count: String? {
        guard let d = detail else { return nil }
        if let t = d.titles { return "\(t.uniqued().count) titles" }
        return "\(d.items.uniqued().count) in your library"
    }
}

// MARK: Hero entries

extension Item {
    var heroEntry: HeroEntry {
        HeroEntry(id: id, title: kind == "episode" ? (showTitle ?? title) : title, backdrop: backdropPath ?? stillPath,
                  logo: kind == "episode" ? nil : logoPath, meta: metaLine, overview: overview)
    }
}

extension TitleView {
    var heroEntry: HeroEntry {
        if let item { return item.heroEntry }
        var parts: [String] = []
        if let year { parts.append(String(year)) }
        parts.append(mediaType == "show" ? "Series" : "Movie")
        if let r = rating, r > 0 { parts.append(String(format: "★ %.1f", r)) }
        return HeroEntry(id: id, title: title, backdrop: backdropPath, meta: parts.joined(separator: "  ·  "), overview: overview)
    }
}

extension HubCard {
    var heroEntry: HeroEntry {
        HeroEntry(id: "hub:\(id)", title: name, backdrop: backdropPath, meta: kindLabel, overview: tagline)
    }
}

extension WrenCollection {
    func heroEntry(count: String?) -> HeroEntry {
        HeroEntry(id: "collection:\(id)", title: name, backdrop: backdropPath,
                  meta: [count, kindLabel].compactMap { $0 }.joined(separator: "  ·  "),
                  overview: description.isEmpty ? nil : description)
    }
}

extension CalendarEpisode {
    var heroEntry: HeroEntry {
        var meta = ["\(code) · \(name)", airs]
        if upNext { meta.append("Up next") } else if watched { meta.append("Watched") }
        if itemId == nil && date <= CalendarEpisode.today { meta.append("Not in the library yet") }
        return HeroEntry(id: "cal:\(id)", title: show.title, backdrop: show.backdropPath, logo: show.logoPath,
                         meta: meta.joined(separator: "  ·  "), overview: overview?.isEmpty == false ? overview : show.overview)
    }
}

extension Color {
    /// "#rrggbb", or nil.
    init?(hex: String?) {
        guard var s = hex?.trimmingCharacters(in: .whitespaces), !s.isEmpty else { return nil }
        if s.hasPrefix("#") { s.removeFirst() }
        guard s.count == 6, let v = Int(s, radix: 16) else { return nil }
        self.init(red: Double((v >> 16) & 0xFF) / 255, green: Double((v >> 8) & 0xFF) / 255, blue: Double(v & 0xFF) / 255)
    }
}
