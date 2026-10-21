import SwiftUI

/*
 * Home, Movies, TV Shows, libraries and hubs are built from the server's
 * layout rows, the same rows every Wren TV shows, under a hero of the
 * page's first titles.
 */

/// Opens Movies' or TV Shows' whole catalog.
struct CatalogRoute: Hashable {
    var type: String
}

/// A page made of the server's layout rows. Servers from before layouts answer 404, and get fallback.
struct LayoutPageView<Fallback: View>: View {
    @Environment(AppState.self) private var state
    @Environment(\.metrics) private var m
    let scope: String
    let title: String
    /// A tab's first page: no navigation bar, the search bar over the hero.
    var root = false
    /// Where "Browse All" goes, if anywhere.
    var browseAll: (any Hashable)?
    @ViewBuilder var fallback: () -> Fallback
    @State private var page: LayoutPage?
    @State private var error: String?
    @State private var unsupported = false
    /// Rows per request; the rest load as the page scrolls.
    private let pageRows = 6

    var body: some View {
        if unsupported {
            fallback()
        } else {
            content
        }
    }

    private var content: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 30) {
                if let p = page {
                    LayoutHero(rows: p.rows)
                    LayoutRows(rows: p.rows)
                    if p.nextOffset != nil {
                        ProgressView().frame(maxWidth: .infinity).padding(.vertical, 20)
                            .task(id: p.nextOffset) { await loadMore() }
                    }
                    if p.rows.isEmpty {
                        ContentUnavailableView("Nothing here yet", systemImage: "film.stack",
                                               description: Text(state.isAdmin ? "Add a library under More → Libraries." : "Ask the server owner to share a library."))
                            .padding(.top, 120)
                    }
                    PageActions(scope: scope, title: title, browseAll: browseAll)
                } else if let error {
                    ContentUnavailableView("Couldn't load", systemImage: "exclamationmark.triangle", description: Text(error))
                        .padding(.top, 120)
                } else {
                    ProgressView().frame(maxWidth: .infinity).padding(.top, 200)
                }
            }
            .padding(.bottom, 24)
        }
        .ignoresSafeArea(edges: .top)
        .navigationTitle(title)
        .modifier(RootChrome(root: root))
        .refreshable { await load() }
        .task(id: "\(scope)|\(state.refreshTick)") { await load() }
    }

    private func load() async {
        guard let api = state.api else { return }
        do {
            // A refresh keeps as many rows as were showing, so the page doesn't jump.
            page = try await api.layout(scope, limit: max(pageRows, page?.rows.count ?? 0))
            error = nil
        } catch let e as WrenTVError where e.status == 404 {
            unsupported = true
        } catch is CancellationError {
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

/// A tab's first page hides the navigation bar and floats the search bar
/// over the hero; pushed pages keep the bar and its back button, see-through.
private struct RootChrome: ViewModifier {
    let root: Bool

    func body(content: Content) -> some View {
        if root {
            content
                .toolbar(.hidden, for: .navigationBar)
                .homeSearchBar()
        } else {
            content
                .navigationBarTitleDisplayMode(.inline)
                .toolbarBackground(.hidden, for: .navigationBar)
        }
    }
}

/// "Browse All" and "Customize" under a page's rows.
private struct PageActions: View {
    @Environment(\.metrics) private var m
    let scope: String
    let title: String
    let browseAll: (any Hashable)?

    var body: some View {
        HStack(spacing: 10) {
            if let browseAll {
                NavigationLink(value: browseAll) { Label("Browse All", systemImage: "square.grid.3x3") }
            }
            NavigationLink(value: LayoutEditorRoute(scope: scope, title: title)) { Label("Customize", systemImage: "slider.horizontal.3") }
        }
        .buttonStyle(.bordered)
        .tint(.secondary)
        .font(.callout)
        .padding(.horizontal, m.gutter)
        .padding(.top, 8)
    }
}

/// The page's banner: its first titles with artwork, taking turns.
private struct LayoutHero: View {
    @Environment(AppState.self) private var state
    let rows: [LayoutRow]

    var body: some View {
        let titles = heroTitles
        if !titles.isEmpty {
            DiscoverHero(titles: titles, play: play)
        } else if !heroItems.isEmpty {
            FeatureHero(items: heroItems, play: play)
        } else {
            Color.clear.frame(height: 110)
        }
    }

    /// Titles from the first row of titles (trending, popular) with artwork.
    private var heroTitles: [TitleView] {
        let row = rows.first { $0.kind == "titles" && !($0.titles ?? []).isEmpty }
        return Array((row?.titles ?? []).uniqued().filter { $0.backdropPath != nil }.prefix(6))
    }

    /// Library titles with artwork, for servers that don't stream.
    private var heroItems: [Item] {
        let pool = rows.filter { $0.kind == "items" || $0.kind == "collection" }.flatMap { $0.items ?? [] }
        return Array(pool.uniqued().filter { $0.backdropPath != nil && $0.kind != "episode" }.prefix(6))
    }

    private func play(_ item: Item) {
        state.play(PlayTarget(itemId: item.id, title: item.showTitle ?? item.title,
                              subtitle: item.kind == "episode" ? "\(item.episodeLabel) — \(item.title)" : nil))
    }
}

/// A hub: a genre, a streaming service or an admin's own, as shelves.
struct HubView: View {
    @Environment(AppState.self) private var state
    @Environment(\.metrics) private var m
    let route: HubRoute
    @State private var page: HubPage?
    @State private var error: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 30) {
                header
                if let p = page {
                    LayoutRows(rows: p.rows)
                    if p.rows.isEmpty {
                        ContentUnavailableView("Nothing here yet", systemImage: "film.stack",
                                               description: Text("None of this hub's shelves found any titles."))
                    }
                } else if let error {
                    ContentUnavailableView("Couldn't load", systemImage: "exclamationmark.triangle", description: Text(error))
                } else {
                    ProgressView().frame(maxWidth: .infinity).padding(.top, 60)
                }
            }
            .padding(.bottom, 24)
        }
        .ignoresSafeArea(edges: .top)
        .navigationTitle(route.name)
        .navigationBarTitleDisplayMode(.inline)
        .toolbarBackground(.hidden, for: .navigationBar)
        .task(id: "\(route.id)|\(state.refreshTick)") {
            do {
                page = try await state.api?.hub(route.id)
                error = nil
            } catch is CancellationError {
            } catch {
                if page == nil { self.error = error.localizedDescription }
            }
        }
    }

    private var header: some View {
        ZStack(alignment: .bottomLeading) {
            RemoteImage(url: state.api?.imageURL(page?.hub.backdropPath, size: "w1280"))
                .frame(height: m.compact ? 300 : 380)
                .frame(maxWidth: .infinity)
                .clipped()
                .overlay(LinearGradient(colors: [.clear, .black.opacity(0.4), Color(uiColor: .systemBackground)], startPoint: .top, endPoint: .bottom))
            VStack(alignment: .leading, spacing: 6) {
                if let hub = page?.hub {
                    Text(hub.kindLabel.uppercased()).font(.caption.weight(.bold)).kerning(1.2).foregroundStyle(Color.wrenEmber)
                }
                Text(route.name).font(.system(size: m.heroTitle, weight: .bold))
                if let t = page?.hub.tagline, !t.isEmpty {
                    Text(t).font(.callout).foregroundStyle(.secondary).lineLimit(3)
                }
            }
            .padding(.horizontal, m.gutter)
        }
    }
}

/// The server's rows, each drawn the way its kind asks.
struct LayoutRows: View {
    @Environment(AppState.self) private var state
    let rows: [LayoutRow]

    var body: some View {
        ForEach(rows) { row in
            // A collection's title opens it, so the shelf needs no "View All" when it shows everything.
            Shelf(title: row.title, subtitle: row.subtitle, link: row.collection) {
                switch row.kind {
                case "landscape":
                    ForEach((row.items ?? []).uniqued()) { item in
                        Button { play(item) } label: { LandscapeCard(item: item) }
                            .buttonStyle(.plain)
                            .contextMenu { ItemContextMenu(item: item) }
                    }
                case "hubs":
                    ForEach((row.hubs ?? []).uniqued()) { HubTile(hub: $0, logos: row.style == "logos") }
                case "titles":
                    ForEach(Array((row.titles ?? []).uniqued().enumerated()), id: \.element.id) { i, t in
                        TitleCard(title: t, rank: row.ranked == true && i < 10 ? i + 1 : nil)
                    }
                default:
                    ForEach((row.items ?? []).uniqued()) { item in
                        NavigationLink(value: item) { PosterCard(item: item) }
                            .buttonStyle(.plain)
                            .contextMenu { ItemContextMenu(item: item, onWatchlist: row.id == "watchlist") }
                    }
                    ForEach((row.missing ?? []).uniqued()) { TitleCard(title: $0) }
                }
                // A collection's or hub's row leads to the rest of it.
                if let c = row.collection, row.more == true {
                    MoreCard(label: "View All", value: c)
                } else if let hub = row.hubId {
                    MoreCard(label: "Open \(row.title)", value: HubRoute(id: hub, name: row.title))
                }
            }
        }
    }

    private func play(_ item: Item) {
        state.play(PlayTarget(itemId: item.id, title: item.showTitle ?? item.title,
                              subtitle: item.kind == "episode" ? "\(item.episodeLabel) — \(item.title)" : nil))
    }
}

/// A hub's tile: its artwork with the name over it, or a service's logo on its colour.
struct HubTile: View {
    @Environment(AppState.self) private var state
    @Environment(\.metrics) private var m
    let hub: HubCard
    var logos = false

    var body: some View {
        NavigationLink(value: HubRoute(id: hub.id, name: hub.name)) {
            ZStack(alignment: .bottomLeading) {
                if logos, let logo = hub.logoPath {
                    (Color(hex: hub.color) ?? Color.white.opacity(0.08))
                    RemoteImage(url: state.api?.imageURL(logo, size: "w300"), contentMode: .fit)
                        .padding(m.compact ? 22 : 30)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    RemoteImage(url: state.api?.imageURL(hub.backdropPath, size: "w780"))
                    LinearGradient(colors: [.clear, (Color(hex: hub.color) ?? .black).opacity(0.85)], startPoint: .center, endPoint: .bottom)
                    Text(hub.name).font(.headline.bold()).foregroundStyle(.white).lineLimit(2).padding(12)
                }
            }
            .frame(width: m.landscape, height: m.landscape * 9 / 16)
            .clipShape(RoundedRectangle(cornerRadius: 10))
        }
        .buttonStyle(.plain)
    }
}

/// The card at the end of a row that opens the whole of it.
private struct MoreCard<V: Hashable>: View {
    @Environment(\.metrics) private var m
    let label: String
    let value: V

    var body: some View {
        NavigationLink(value: value) {
            VStack(spacing: 10) {
                Image(systemName: "arrow.right").font(.title2)
                Text(label).font(.callout).multilineTextAlignment(.center).lineLimit(2)
            }
            .padding(10)
            .frame(width: m.poster, height: m.poster * 1.5)
            .background(.white.opacity(0.08), in: RoundedRectangle(cornerRadius: 10))
        }
        .buttonStyle(.plain)
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

// MARK: Layout editor

/// The pages whose rows can be chosen.
struct LayoutsView: View {
    @Environment(AppState.self) private var state

    var body: some View {
        List {
            Section {
                NavigationLink(value: LayoutEditorRoute(scope: "home", title: "Home")) { Label("Home", systemImage: "house") }
                if state.canStream {
                    NavigationLink(value: LayoutEditorRoute(scope: "movies", title: "Movies")) { Label("Movies", systemImage: "film") }
                    NavigationLink(value: LayoutEditorRoute(scope: "shows", title: "TV Shows")) { Label("TV Shows", systemImage: "tv") }
                }
            } footer: {
                Text("Choose which rows each page shows, and in what order.")
            }
            if !state.canStream && !state.libraries.isEmpty {
                Section("Libraries") {
                    ForEach(state.libraries) { lib in
                        NavigationLink(value: LayoutEditorRoute(scope: "library:\(lib.id)", title: lib.name)) { Label(lib.name, systemImage: lib.symbol) }
                    }
                }
            }
        }
        .navigationTitle("Layouts")
    }
}

/// One page's rows: drag to reorder, swipe to hide or remove, add more.
/// Admins also choose whether they're editing their own layout or everyone's default.
struct LayoutEditorView: View {
    @Environment(AppState.self) private var state
    let route: LayoutEditorRoute
    @State private var everyone = false
    @State private var config: LayoutConfig?
    @State private var error: String?

    var body: some View {
        List {
            if state.isAdmin {
                Picker("Editing", selection: $everyone) {
                    Text("My layout").tag(false)
                    Text("Default for everyone").tag(true)
                }
                .pickerStyle(.segmented)
                .listRowBackground(Color.clear)
                .listRowInsets(EdgeInsets())
            }
            if let c = config {
                Section {
                    ForEach(Array(c.entries.enumerated()), id: \.element.id) { i, e in
                        HStack {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(e.title ?? e.ref).foregroundStyle(e.isHidden ? .secondary : .primary)
                                if let d = e.detail { Text(d).font(.caption).foregroundStyle(.secondary) }
                            }
                            Spacer()
                            Button { toggle(i) } label: { Image(systemName: e.isHidden ? "eye.slash" : "eye") }
                                .buttonStyle(.borderless)
                                .foregroundStyle(e.isHidden ? .secondary : Color.wrenEmber)
                                .accessibilityLabel(e.isHidden ? "Show" : "Hide")
                        }
                        .swipeActions {
                            Button(role: .destructive) { remove(i) } label: { Label("Remove", systemImage: "trash") }
                        }
                        .contextMenu {
                            if i > 0 { Button { move(i, to: 0) } label: { Label("Move to Top", systemImage: "arrow.up.to.line") } }
                            Button { toggle(i) } label: { Label(e.isHidden ? "Show" : "Hide", systemImage: e.isHidden ? "eye" : "eye.slash") }
                            Button(role: .destructive) { remove(i) } label: { Label("Remove", systemImage: "trash") }
                        }
                    }
                    .onMove { from, to in
                        guard var e = config?.entries else { return }
                        e.move(fromOffsets: from, toOffset: to)
                        save(e)
                    }
                    if let options = available, !options.isEmpty {
                        Menu {
                            ForEach(options) { o in
                                Button { add(o) } label: { Text([o.title ?? o.ref, o.detail].compactMap { $0 }.joined(separator: " — ")) }
                            }
                        } label: {
                            Label("Add a Row…", systemImage: "plus")
                        }
                    }
                } header: {
                    Text("Rows")
                } footer: {
                    if c.custom != true {
                        Text(!everyone && c.hasDefault == true ? "Showing the server's default. Changing anything makes it your own."
                                                               : "Showing Wren TV's built-in layout.")
                    }
                }
                if c.custom == true {
                    Section {
                        Button(!everyone && c.hasDefault == true ? "Use the Default Layout" : "Back to the Built-in Layout", role: .destructive) { reset() }
                    }
                }
            } else if let error {
                Text(error).foregroundStyle(.red)
            } else {
                ProgressView().frame(maxWidth: .infinity)
            }
        }
        .environment(\.editMode, .constant(.active))
        .navigationTitle(route.title)
        .task(id: "\(route.scope)|\(everyone)") { await load() }
    }

    /// Options not already on the page.
    private var available: [LayoutEntry]? {
        guard let c = config else { return nil }
        let have = Set(c.entries.map(\.id))
        return c.options?.filter { !have.contains($0.id) }
    }

    private func load() async {
        do {
            config = try await state.api?.layoutConfig(route.scope, everyone: everyone)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func move(_ i: Int, to j: Int) {
        guard var e = config?.entries else { return }
        e.insert(e.remove(at: i), at: j)
        save(e)
    }

    private func toggle(_ i: Int) {
        guard var e = config?.entries else { return }
        e[i].hidden = !e[i].isHidden
        save(e)
    }

    private func remove(_ i: Int) {
        guard var e = config?.entries else { return }
        e.remove(at: i)
        save(e)
    }

    private func add(_ o: LayoutEntry) {
        guard var e = config?.entries else { return }
        e.append(o)
        save(e)
    }

    private func save(_ entries: [LayoutEntry]) {
        config?.entries = entries
        config?.custom = true
        Task {
            do {
                try await state.api?.setLayout(route.scope, everyone: everyone, entries: entries)
                state.refreshTick += 1
            } catch {
                self.error = error.localizedDescription
                await load()
            }
        }
    }

    private func reset() {
        Task {
            do {
                try await state.api?.resetLayout(route.scope, everyone: everyone)
                state.refreshTick += 1
                await load()
            } catch {
                self.error = error.localizedDescription
            }
        }
    }
}
