import SwiftUI

/*
 * Home, Movies, TV Shows, libraries and hubs are built from the server's
 * layout rows, the same rows every Wren TV shows. The hovered title's name
 * and details sit in a hero at the top, over its artwork.
 */

/// What the hero shows: the hovered title, or the page's own details.
struct HeroEntry: Equatable {
    var id: String
    var title: String
    var logo: String?
    var meta: String
    var overview: String?
}

/// Opens Movies' or TV Shows' whole catalog.
struct CatalogRoute: Hashable {
    var type: String
}

/// A page made of the server's layout rows. Servers from before layouts answer 404, and get fallback.
struct LayoutPageView<Fallback: View>: View {
    @Environment(AppState.self) private var state
    let scope: String
    let title: String
    var label: String?
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
            CinematicPage(fallback: first, fallbackBackdrop: firstBackdrop, label: label) {
                if let p = page {
                    LayoutRows(rows: p.rows)
                    if p.nextOffset != nil {
                        ProgressView().frame(maxWidth: .infinity).padding(.vertical, 20)
                            .task(id: p.nextOffset) { await loadMore() }
                    }
                    if p.rows.isEmpty {
                        ContentUnavailableView("Nothing here yet", systemImage: "film.stack",
                                               description: Text(state.isAdmin ? "Add a library under Server → Libraries." : "Ask the server owner to share a library."))
                            .padding(.top, 60)
                    }
                } else if let error {
                    ContentUnavailableView("Couldn't load", systemImage: "exclamationmark.triangle", description: Text(error)).padding(.top, 60)
                } else {
                    ProgressView().frame(maxWidth: .infinity).padding(.top, 80)
                }
            }
            .navigationTitle(title)
            .toolbar {
                ToolbarItemGroup {
                    if let browseAll {
                        NavigationLink(value: browseAll) { Label("Browse All", systemImage: "square.grid.3x3") }
                            .help("Browse All")
                    }
                    NavigationLink(value: LayoutEditorRoute(scope: scope, title: title)) { Label("Customize", systemImage: "slider.horizontal.3") }
                        .help("Choose this page's rows")
                }
            }
            .task(id: "\(scope)|\(state.refreshTick)") { await load() }
        }
    }

    /// Until something is hovered, the hero shows the first title.
    private var first: HeroEntry? {
        page?.rows.lazy.compactMap { r in
            r.items?.first?.heroEntry ?? r.titles?.first?.heroEntry ?? r.hubs?.first?.heroEntry
        }.first
    }

    private var firstBackdrop: String? {
        page?.rows.lazy.compactMap { r in
            r.items?.first?.backdropPath ?? r.titles?.first?.backdropPath ?? r.hubs?.first?.backdropPath
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

/// A hub: a genre, a streaming service or an admin's own, as shelves.
struct HubView: View {
    @Environment(AppState.self) private var state
    let route: HubRoute
    @State private var page: HubPage?
    @State private var error: String?

    var body: some View {
        let hub = page?.hub ?? HubCard(id: route.id, name: route.name, kind: "custom")
        CinematicPage(fallback: hub.heroEntry, fallbackBackdrop: hub.backdropPath, label: hub.kindLabel) {
            if let p = page {
                LayoutRows(rows: p.rows)
                if p.rows.isEmpty {
                    ContentUnavailableView("Nothing here yet", systemImage: "film.stack",
                                           description: Text("None of this hub's shelves found any titles.")).padding(.top, 60)
                }
            } else if let error {
                ContentUnavailableView("Couldn't load", systemImage: "exclamationmark.triangle", description: Text(error)).padding(.top, 60)
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 80)
            }
        }
        .navigationTitle(route.name)
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
}

/// The hovered title's details at the top, the page's rows scrolling below.
struct CinematicPage<Rows: View>: View {
    var fallback: HeroEntry?
    var fallbackBackdrop: String?
    var label: String?
    @ViewBuilder var rows: () -> Rows

    var body: some View {
        CinematicBody(fallback: fallback, fallbackBackdrop: fallbackBackdrop, label: label, rows: rows)
            .ambientBackdrop()
    }
}

private struct CinematicBody<Rows: View>: View {
    @Environment(\.ambient) private var ambient
    var fallback: HeroEntry?
    var fallbackBackdrop: String?
    var label: String?
    var rows: () -> Rows

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HeroText(entry: ambient?.entry ?? fallback, label: label)
                .frame(height: 250, alignment: .bottomLeading)
                .padding(.horizontal, 28)
                .padding(.bottom, 10)
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 30) { rows() }
                    .padding(.top, 14)
                    .padding(.bottom, 28)
            }
            .mask(LinearGradient(stops: [.init(color: .clear, location: 0), .init(color: .black, location: 0.03)], startPoint: .top, endPoint: .bottom))
        }
        .onAppear { if ambient?.path == nil { ambient?.path = fallbackBackdrop } }
        .onChange(of: fallbackBackdrop) { if ambient?.path == nil { ambient?.path = fallbackBackdrop } }
    }
}

private struct HeroText: View {
    @Environment(AppState.self) private var state
    let entry: HeroEntry?
    let label: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if let e = entry {
                if let label { Text(label.uppercased()).font(.caption.weight(.bold)).kerning(1.2).foregroundStyle(Color.wrenEmber) }
                if let logo = e.logo {
                    RemoteImage(url: state.api?.imageURL(logo, size: "w500"), contentMode: .fit)
                        .frame(maxWidth: 310, maxHeight: 78, alignment: .bottomLeading)
                } else {
                    Text(e.title).font(.system(size: 38, weight: .bold)).lineLimit(1)
                }
                if !e.meta.isEmpty { Text(e.meta).font(.callout).foregroundStyle(.white.opacity(0.75)) }
                if let o = e.overview, !o.isEmpty {
                    Text(o).font(.subheadline).foregroundStyle(.white.opacity(0.85)).lineLimit(3).frame(maxWidth: 580, alignment: .leading)
                }
            }
        }
        .shadow(color: .black.opacity(0.5), radius: 8)
        .animation(.easeOut(duration: 0.2), value: entry)
    }
}

/// The server's rows, each drawn the way its kind asks.
struct LayoutRows: View {
    @Environment(\.openWindow) private var openWindow
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
        openWindow(id: "player", value: PlayTarget(itemId: item.id, title: item.showTitle ?? item.title,
                                                   subtitle: item.kind == "episode" ? "\(item.episodeLabel) — \(item.title)" : nil))
    }
}

/// A hub's tile: its artwork with the name over it, or a service's logo on its colour.
struct HubTile: View {
    @Environment(AppState.self) private var state
    let hub: HubCard
    var logos = false
    var width: CGFloat = PosterSize.width(280)

    var body: some View {
        NavigationLink(value: HubRoute(id: hub.id, name: hub.name)) {
            ZStack(alignment: .bottomLeading) {
                if logos, let logo = hub.logoPath {
                    (Color(hex: hub.color) ?? Color.white.opacity(0.08))
                    RemoteImage(url: state.api?.imageURL(logo, size: "w300"), contentMode: .fit)
                        .padding(30)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    RemoteImage(url: state.api?.imageURL(hub.backdropPath, size: "w780"))
                    LinearGradient(colors: [.clear, (Color(hex: hub.color) ?? .black).opacity(0.85)], startPoint: .center, endPoint: .bottom)
                    Text(hub.name).font(.title3.bold()).lineLimit(2).padding(14)
                }
            }
            .frame(width: width, height: width * 9 / 16)
            .clipShape(RoundedRectangle(cornerRadius: 10))
            .hoverLift(cornerRadius: 10)
            .ambient(hub.backdropPath) { hub.heroEntry }
        }
        .buttonStyle(.plain)
    }
}

/// The card at the end of a row that opens the whole of it.
private struct MoreCard<V: Hashable>: View {
    let label: String
    let value: V

    var body: some View {
        NavigationLink(value: value) {
            VStack(spacing: 10) {
                Image(systemName: "arrow.right").font(.title2)
                Text(label).font(.callout).multilineTextAlignment(.center).lineLimit(2)
            }
            .padding(12)
            .frame(width: 150, height: 225)
            .background(.white.opacity(0.08), in: RoundedRectangle(cornerRadius: 10))
            .hoverLift(cornerRadius: 10)
        }
        .buttonStyle(.plain)
    }
}

// MARK: Hero entries

extension Item {
    var heroEntry: HeroEntry {
        var parts: [String] = []
        if let y = year { parts.append(String(y)) }
        if let c = contentRating, !c.isEmpty { parts.append(c) }
        if let g = genres?.first { parts.append(g) }
        if let r = runtimeMin, r > 0 { parts.append(formatDuration(Double(r * 60))) }
        if let r = rating, r > 0 { parts.append(String(format: "★ %.1f", r)) }
        if kind == "episode" { parts.insert(episodeLabel + " · " + title, at: 0) }
        return HeroEntry(id: id, title: kind == "episode" ? (showTitle ?? title) : title,
                         logo: kind == "episode" ? nil : logoPath, meta: parts.joined(separator: " · "), overview: overview)
    }
}

extension TitleView {
    var heroEntry: HeroEntry {
        if let item { return item.heroEntry }
        var parts: [String] = []
        if let year { parts.append(String(year)) }
        parts.append(mediaType == "show" ? "Series" : "Movie")
        if let r = rating, r > 0 { parts.append(String(format: "★ %.1f", r)) }
        return HeroEntry(id: id, title: title, meta: parts.joined(separator: " · "), overview: overview)
    }
}

extension HubCard {
    var heroEntry: HeroEntry { HeroEntry(id: "hub:\(id)", title: name, meta: kindLabel, overview: tagline) }
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

/// The pages whose rows can be chosen, laid out like the other settings.
struct LayoutsView: View {
    @Environment(AppState.self) private var state

    var body: some View {
        PageScroll(maxWidth: 780) {
            CardSection("Pages", footer: state.isAdmin ? "Choose the rows each page shows, and their order. You can change your own layout or the default for everyone."
                                                       : "Choose the rows each page shows, and their order. Saved to your account, so it applies on every device.") {
                page("Home", "house", "home")
                if state.canStream {
                    page("Movies", "film", "movies")
                    page("TV Shows", "tv", "shows")
                }
            }
            if !state.libraries.isEmpty {
                CardSection("Libraries") {
                    ForEach(state.libraries) { lib in page(lib.name, lib.symbol, "library:\(lib.id)") }
                }
            }
        }
        .navigationTitle("Layouts")
    }

    private func page(_ title: String, _ symbol: String, _ scope: String) -> some View {
        NavigationLink(value: LayoutEditorRoute(scope: scope, title: title)) {
            HStack(spacing: 12) {
                Image(systemName: symbol).foregroundStyle(Color.wrenEmber).frame(width: 22)
                Text(title)
                Spacer(minLength: 12)
                Image(systemName: "chevron.right").font(.caption.bold()).foregroundStyle(.tertiary)
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}

/// One page's rows: reorder, hide, remove, add more. Admins also choose
/// whether they're editing their own layout or everyone's default.
struct LayoutEditorView: View {
    @Environment(AppState.self) private var state
    let route: LayoutEditorRoute
    @State private var everyone = false
    @State private var config: LayoutConfig?
    @State private var error: String?

    var body: some View {
        PageScroll(maxWidth: 780) {
            if state.isAdmin {
                HStack(spacing: 8) {
                    Chip("My layout", selected: !everyone) { everyone = false }
                    Chip("Default for everyone", selected: everyone) { everyone = true }
                }
            }
            if let c = config {
                CardSection("Rows", subtitle: "\(c.entries.count)", footer: footer(c)) {
                    if c.entries.isEmpty {
                        Text("No rows. Add one to fill the page.").foregroundStyle(.secondary)
                    }
                    ForEach(Array(c.entries.enumerated()), id: \.element.id) { i, e in
                        row(i, e, last: i == c.entries.count - 1)
                    }
                }
                if let error { Text(error).font(.caption).foregroundStyle(.red) }
            } else if let error {
                ContentUnavailableView("Couldn't load", systemImage: "exclamationmark.triangle", description: Text(error))
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 120)
            }
        }
        .navigationTitle("\(route.title) Layout")
        .toolbar {
            if config?.custom == true {
                Button(!everyone && config?.hasDefault == true ? "Use the Default Layout" : "Back to the Built-in Layout") { reset() }
            }
            if let options = available, !options.isEmpty {
                Menu {
                    ForEach(options) { o in
                        Button { add(o) } label: { Text([o.title ?? o.ref, o.detail].compactMap { $0 }.joined(separator: " — ")) }
                    }
                } label: {
                    Label("Add a Row", systemImage: "plus")
                }
                .help("Add a Row")
            }
        }
        .task(id: "\(route.scope)|\(everyone)") { await load() }
    }

    private func footer(_ c: LayoutConfig) -> String {
        if c.custom == true { return everyone ? "The default for everyone without their own layout." : "Your own layout, on every device." }
        return !everyone && c.hasDefault == true ? "Showing the server's default. Changing anything makes it your own."
                                                 : "Showing Wren TV's built-in layout. Changing anything makes it your own."
    }

    private func row(_ i: Int, _ e: LayoutEntry, last: Bool) -> some View {
        SettingRow(e.title ?? e.ref, detail: e.detail) {
            HStack(spacing: 14) {
                Button { move(i, to: i - 1) } label: { Image(systemName: "chevron.up") }
                    .disabled(i == 0).help("Move Up")
                Button { move(i, to: i + 1) } label: { Image(systemName: "chevron.down") }
                    .disabled(last).help("Move Down")
                Button { toggle(i) } label: { Image(systemName: e.isHidden ? "eye.slash" : "eye") }
                    .help(e.isHidden ? "Show" : "Hide")
                Button { remove(i) } label: { Image(systemName: "minus.circle") }
                    .help("Remove")
            }
            .buttonStyle(.borderless)
        }
        .opacity(e.isHidden ? 0.55 : 1)
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
