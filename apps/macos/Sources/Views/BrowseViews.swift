import SwiftUI

/// The home page: a discovery page of everything on TMDB when the server
/// streams, otherwise what's in the libraries.
struct HomeView: View {
    @Environment(AppState.self) private var state

    var body: some View {
        if state.canStream { DiscoverHomeView() } else { LibraryHomeView() }
    }
}

struct LibraryHomeView: View {
    @Environment(AppState.self) private var state
    @Environment(\.openWindow) private var openWindow
    @State private var home: HomeResponse?
    @State private var error: String?
    @State private var trending: TrendingResponse?

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 30) {
                if let home {
                    let picks = featured(home)
                    if !picks.isEmpty { FeatureHero(items: picks, play: play) } else { Color.clear.frame(height: 40) }
                    if !home.continueWatching.isEmpty {
                        Shelf(title: "Continue Watching") {
                            ForEach(home.continueWatching) { item in
                                Button { play(item) } label: { LandscapeCard(item: item) }
                                    .buttonStyle(.plain)
                                    .contextMenu { ItemContextMenu(item: item) }
                            }
                        }
                    }
                    WatchlistShelf(items: home.watchlist)
                    if let t = trending {
                        if t.enabled && !t.items.isEmpty {
                            Shelf(title: "Trending This Week", subtitle: "\(t.items.filter { $0.item != nil }.count) in your library") {
                                ForEach(Array(t.items.enumerated()), id: \.element.id) { i, v in TitleCard(title: v, rank: i + 1) }
                            }
                        }
                    } else {
                        SkeletonShelf(title: "Trending This Week")
                    }
                    ForEach(home.collections ?? [], id: \.collection.id) { row in
                        CollectionShelfView(row: row)
                    }
                    ForEach(home.recentlyAdded, id: \.library.id) { shelf in
                        Shelf(title: "Recently Added in \(shelf.library.name)") {
                            ForEach(shelf.items) { item in
                                NavigationLink(value: item) { PosterCard(item: item) }
                                    .buttonStyle(.plain)
                                    .contextMenu { ItemContextMenu(item: item) }
                            }
                        }
                    }
                    if let top = home.topRated, top.count >= 4 {
                        itemShelf("Top Rated in Your Library", top)
                    }
                    ForEach(home.genres ?? [], id: \.genre) { row in
                        itemShelf(row.genre, row.items)
                    }
                    if home.continueWatching.isEmpty && home.recentlyAdded.isEmpty && (home.watchlist ?? []).isEmpty {
                        ContentUnavailableView("Nothing here yet", systemImage: "film.stack",
                                               description: Text(state.isAdmin ? "Add a library under Server → Libraries." : "Ask the server owner to share a library."))
                            .padding(.top, 120)
                    }
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
        .ambientBackdrop()
        .navigationTitle("Home")
        .toolbarBackground(.hidden, for: .windowToolbar)
        .task(id: state.refreshTick) { await load() }
        .task { trending = (try? await state.api?.trending()) ?? TrendingResponse(enabled: false, items: []) }
    }

    private func itemShelf(_ title: String, _ items: [Item]) -> some View {
        Shelf(title: title) {
            ForEach(items) { item in
                NavigationLink(value: item) { PosterCard(item: item) }
                    .buttonStyle(.plain)
                    .contextMenu { ItemContextMenu(item: item) }
            }
        }
    }

    /// A handful of well-rated and new titles with artwork, a different
    /// set each hour.
    private func featured(_ home: HomeResponse) -> [Item] {
        var seen = Set<String>()
        let pool = ((home.topRated ?? []) + home.recentlyAdded.flatMap(\.items))
            .filter { $0.backdropPath != nil && seen.insert($0.id).inserted }
        guard !pool.isEmpty else { return [] }
        let start = Int(Date().timeIntervalSince1970 / 3600) % pool.count
        return (0..<min(6, pool.count)).map { pool[(start + $0 * 3) % pool.count] }
            .reduce(into: [Item]()) { out, it in if !out.contains(it) { out.append(it) } }
    }

    private func load() async {
        do { home = try await state.api?.home() } catch { self.error = error.localizedDescription }
    }

    private func play(_ item: Item) {
        openWindow(id: "player", value: PlayTarget(itemId: item.id, title: item.showTitle ?? item.title,
                                                   subtitle: item.kind == "episode" ? "\(item.episodeLabel) — \(item.title)" : nil))
    }
}

struct ItemContextMenu: View {
    @Environment(AppState.self) private var state
    let item: Item
    /// Opened from the watchlist, so the menu can take it off.
    var onWatchlist = false

    var body: some View {
        if item.isPlayable || item.kind == "show" || item.kind == "season" {
            if !item.isWatched {
                Button("Mark as Watched") { set(true) }
            }
            // Part way through counts too: unwatching clears where it left off.
            if item.isWatched || item.inProgress {
                Button("Mark as Unwatched") { set(false) }
            }
        }
        if item.kind == "movie" || item.kind == "show" {
            Button(onWatchlist ? "Remove from Watchlist" : "Add to Watchlist") { watchlist(!onWatchlist) }
        }
    }

    private func watchlist(_ on: Bool) {
        Task {
            try? await state.api?.setWatchlist(item.id, on)
            state.refreshTick += 1
        }
    }

    private func set(_ w: Bool) {
        Task {
            try? await state.api?.setWatched(item.id, w)
            state.refreshTick += 1
        }
    }
}

struct LibraryView: View {
    @Environment(AppState.self) private var state
    let library: Library
    @AppStorage("librarySort") private var sort = "title"
    @State private var tab = "titles"
    @State private var genre: String?
    @State private var unwatched = false
    @State private var genres: [GenreCount] = []
    @State private var items: [Item] = []
    @State private var rows: [CollectionShelf] = []
    @State private var loaded = false
    @State private var filter = ""

    private var shown: [Item] {
        filter.isEmpty ? items : items.filter { $0.title.localizedCaseInsensitiveContains(filter) }
    }

    var body: some View {
        Group {
            switch tab {
            case "collections": CollectionsView(libraryId: library.id, embedded: true)
            case "genres": GenresView(libraryId: library.id, embedded: true)
            default: titles
            }
        }
        .ambientBackdrop()
        .navigationTitle(library.name)
        .navigationSubtitle(tab == "titles" && loaded ? "\(items.count) \(library.type == "shows" ? "shows" : "movies")" : "")
        .toolbar {
            ToolbarItem(placement: .principal) {
                Picker("View", selection: $tab) {
                    Text(library.type == "shows" ? "Shows" : "Movies").tag("titles")
                    Text("Collections").tag("collections")
                    Text("Genres").tag("genres")
                }
                .pickerStyle(.segmented)
                .fixedSize()
            }
            if tab == "titles" {
                ToolbarItemGroup {
                    Menu {
                        Picker("Genre", selection: $genre) {
                            Text("All genres").tag(String?.none)
                            Divider()
                            ForEach(genres) { g in Text("\(g.genre)  (\(g.count))").tag(Optional(g.genre)) }
                        }
                        .pickerStyle(.inline)
                    } label: {
                        Label(genre ?? "All genres", systemImage: "tag").labelStyle(.titleAndIcon)
                    }
                    .help("Genre")
                    Toggle(isOn: $unwatched) { Label("Unwatched", systemImage: "eye.slash") }
                        .help("Only unwatched")
                    SortMenu(sort: $sort)
                }
            }
        }
        .task(id: library.id) {
            genres = (try? await state.api?.genres(library: library.id)) ?? []
        }
    }

    private var titles: some View {
        ScrollViewReader { proxy in
        ScrollView {
            // The owner's collection rows, above everything, when nothing is filtered.
            if showRows {
                VStack(alignment: .leading, spacing: 30) {
                    ForEach(rows, id: \.collection.id) { CollectionShelfView(row: $0) }
                    Text("All \(library.type == "shows" ? "Shows" : "Movies")").font(.title3.bold()).padding(.horizontal, 28)
                }
                .padding(.top, 24)
            }
            LazyVGrid(columns: PosterSize.columns, alignment: .leading, spacing: 26) {
                ForEach(shown) { item in
                    NavigationLink(value: item) { PosterCard(item: item) }
                        .buttonStyle(.plain)
                        .contextMenu { ItemContextMenu(item: item) }
                        .id(item.id)
                }
            }
            .padding(28)
            .padding(.top, showRows ? -14 : 0)
            .padding(.trailing, sort == "title" ? 14 : 0)
            if loaded && items.isEmpty {
                if genre != nil || unwatched {
                    ContentUnavailableView("Nothing matches", systemImage: "line.3.horizontal.decrease.circle",
                                           description: Text("Try a different genre or show watched titles too."))
                } else {
                    ContentUnavailableView("Empty library", systemImage: library.symbol, description: Text("New files appear here automatically."))
                }
            }
        }
        .overlay(alignment: .trailing) {
            if sort == "title" && shown.count > 30 {
                LetterBar(items: shown) { id in
                    withAnimation(.easeInOut(duration: 0.25)) { proxy.scrollTo(id, anchor: .top) }
                }
                .padding(.trailing, 6)
            }
        }
        }
        .searchable(text: $filter, placement: .toolbar, prompt: "Filter")
        .task(id: "\(library.id)|\(sort)|\(genre ?? "")|\(unwatched)|\(state.refreshTick)") {
            if let page = try? await state.api?.items(library: library.id, sort: sort, genre: genre, unwatched: unwatched) {
                items = page.items
            }
            loaded = true
        }
        .task(id: "\(library.id)|\(state.refreshTick)") {
            rows = (try? await state.api?.libraryRows(library.id)) ?? []
        }
    }

    private var showRows: Bool { !rows.isEmpty && genre == nil && !unwatched && filter.isEmpty && sort == "title" }
}

/// A–Z down the edge of a title-sorted grid; a letter jumps to its first title.
private struct LetterBar: View {
    let items: [Item]
    let jump: (String) -> Void
    @State private var hover: String?

    private static let letters = ["#"] + "ABCDEFGHIJKLMNOPQRSTUVWXYZ".map(String.init)

    /// The first item for each letter, keyed the way titles sort ("The Bear" under B).
    private var firsts: [String: String] {
        var out: [String: String] = [:]
        for it in items {
            let key = Self.key(it.title)
            if out[key] == nil { out[key] = it.id }
        }
        return out
    }

    static func key(_ title: String) -> String {
        var t = title.uppercased()
        for a in ["THE ", "A ", "AN "] where t.hasPrefix(a) { t.removeFirst(a.count); break }
        guard let c = t.first, c.isLetter, c.isASCII else { return "#" }
        return String(c)
    }

    var body: some View {
        let f = firsts
        VStack(spacing: 1) {
            ForEach(Self.letters, id: \.self) { l in
                Button { if let id = f[l] { jump(id) } } label: {
                    Text(l)
                        .font(.system(size: 10, weight: .semibold))
                        .frame(width: 18, height: 15)
                        .foregroundStyle(f[l] == nil ? Color.secondary.opacity(0.35) : (hover == l ? Color.white : Color.secondary))
                        .background(hover == l && f[l] != nil ? Color.white.opacity(0.15) : .clear, in: RoundedRectangle(cornerRadius: 4))
                }
                .buttonStyle(.plain)
                .disabled(f[l] == nil)
                .onHover { hover = $0 ? l : (hover == l ? nil : hover) }
            }
        }
        .padding(.vertical, 6)
        .background(.ultraThinMaterial.opacity(0.6), in: Capsule())
    }
}

/// A collection as a Home row.
struct CollectionShelfView: View {
    let row: CollectionShelf

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Shelf(title: row.collection.name, subtitle: row.collection.description.isEmpty ? nil : row.collection.description) {
                ForEach(row.items) { item in
                    NavigationLink(value: item) { PosterCard(item: item) }
                        .buttonStyle(.plain)
                        .contextMenu { ItemContextMenu(item: item) }
                }
                ForEach(row.missing ?? []) { TitleCard(title: $0) }
                NavigationLink(value: row.collection) {
                    VStack(spacing: 8) {
                        Image(systemName: "chevron.right.circle").font(.largeTitle)
                        Text("See All").font(.callout)
                    }
                    .foregroundStyle(.secondary)
                    .frame(width: 120, height: 225)
                    .background(.white.opacity(0.04), in: RoundedRectangle(cornerRadius: 8))
                    .hoverLift()
                }
                .buttonStyle(.plain)
            }
        }
    }
}

/// Results for the search field at the top of every page.
struct SearchView: View {
    @Environment(AppState.self) private var state
    let query: String
    @State private var results: [Item] = []
    /// Titles from TMDB the server doesn't have (playable when streaming is on).
    @State private var more: [TitleView] = []
    /// Collections whose names match, a franchise's among them.
    @State private var collections: [WrenCollection] = []
    @State private var searched = ""

    private var columns: [GridItem] { PosterSize.columns }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                if !collections.isEmpty {
                    Text("Collections").font(.title3.bold())
                    ScrollView(.horizontal, showsIndicators: false) {
                        LazyHStack(alignment: .top, spacing: 16) {
                            ForEach(collections) { c in
                                NavigationLink(value: c) { CollectionTile(collection: c) }.buttonStyle(.plain)
                            }
                        }
                        .padding(.horizontal, 28)
                        .padding(.vertical, 8)
                    }
                    .padding(.horizontal, -28)
                    .padding(.vertical, -8)
                    if !results.isEmpty || !more.isEmpty { Text("Titles").font(.title3.bold()).padding(.top, 10) }
                }
                if !results.isEmpty {
                    if !more.isEmpty && !state.canStream { Text("On This Server").font(.title3.bold()) }
                    LazyVGrid(columns: columns, alignment: .leading, spacing: 26) {
                        ForEach(results) { item in
                            NavigationLink(value: item) {
                                if item.kind == "episode" { LandscapeCard(item: item, width: 170) } else { PosterCard(item: item) }
                            }
                            .buttonStyle(.plain)
                        }
                    }
                }
                if !more.isEmpty {
                    // When streaming everything plays, so it's one list.
                    if !state.canStream {
                        Text("Not on This Server").font(.title3.bold())
                            .padding(.top, results.isEmpty ? 0 : 10)
                    }
                    LazyVGrid(columns: columns, alignment: .leading, spacing: 26) {
                        ForEach(more) { TitleCard(title: $0) }
                    }
                }
            }
            .padding(28)
            if !query.isEmpty && searched == query && results.isEmpty && more.isEmpty && collections.isEmpty {
                ContentUnavailableView.search(text: query)
            }
        }
        .navigationTitle("Search")
        .task(id: query) {
            guard query.count >= 2 else { results = []; more = []; collections = []; return }
            try? await Task.sleep(for: .milliseconds(300))
            guard !Task.isCancelled, let api = state.api else { return }
            async let lib = api.search(query)
            async let tmdb = api.discoverSearch(query)
            async let colls = api.searchCollections(query)
            let found = (try? await lib) ?? []
            let titles = (try? await tmdb)?.items ?? []
            let cs = (try? await colls) ?? []
            guard !Task.isCancelled else { return }
            collections = cs
            // Library matches TMDB didn't return (episodes, local-only files) stay first.
            let ids = Set(found.map(\.id))
            results = found + titles.compactMap(\.item).filter { !ids.contains($0.id) }
            more = titles.filter { $0.item == nil }
            searched = query
        }
    }
}

/// The large backdrop at the top of Home: a few titles that take turns,
/// edge to edge under the toolbar.
private struct FeatureHero: View {
    @Environment(AppState.self) private var state
    let items: [Item]
    let play: (Item) -> Void
    @State private var index = 0
    @State private var hovering = false

    private var item: Item { items[min(index, items.count - 1)] }

    var body: some View {
        ZStack(alignment: .bottomLeading) {
            ZStack {
                ForEach(Array(items.enumerated()), id: \.element.id) { i, it in
                    RemoteImage(url: state.api?.imageURL(it.backdropPath, size: "w1280"))
                        .opacity(i == index ? 1 : 0)
                }
            }
            .frame(height: 560)
            .frame(maxWidth: .infinity)
            .clipped()
            .overlay(LinearGradient(stops: [.init(color: .clear, location: 0.35), .init(color: .black.opacity(0.55), location: 0.75),
                                            .init(color: Color(nsColor: .windowBackgroundColor), location: 1)],
                                    startPoint: .top, endPoint: .bottom))
            .overlay(LinearGradient(stops: [.init(color: .black.opacity(0.58), location: 0), .init(color: .black.opacity(0.24), location: 0.28),
                                            .init(color: .clear, location: 0.48)], startPoint: .leading, endPoint: .trailing))

            VStack(alignment: .leading, spacing: 12) {
                if let logo = item.logoPath {
                    RemoteImage(url: state.api?.imageURL(logo, size: "w500"), contentMode: .fit)
                        .frame(maxWidth: 330, maxHeight: 106, alignment: .bottomLeading)
                        .id(item.id)
                } else {
                    Text(item.title).font(.system(size: 44, weight: .bold)).id(item.id)
                }
                HStack(spacing: 8) {
                    if let c = item.contentRating, !c.isEmpty { Badge(text: c) }
                    Text(metaLine).foregroundStyle(.white.opacity(0.75))
                }
                .font(.callout)
                if let o = item.overview {
                    Text(o).font(.subheadline).lineLimit(2).foregroundStyle(.white.opacity(0.85)).frame(maxWidth: 520, alignment: .leading)
                        .shadow(color: .black.opacity(0.45), radius: 8)
                }
                HStack(spacing: 10) {
                    if item.isPlayable {
                        Button { play(item) } label: {
                            Label(item.inProgress ? "Resume" : "Play", systemImage: "play.fill")
                        }
                        .buttonStyle(.primaryCapsule)
                    }
                    NavigationLink(value: item) {
                        if item.isPlayable {
                            Label("Details", systemImage: "info.circle")
                        } else {
                            Label("Watch Now", systemImage: "play.fill")
                        }
                    }
                    .buttonStyle(item.isPlayable ? AnyButtonStyle(.glassCapsule) : AnyButtonStyle(.primaryCapsule))
                }
                .padding(.top, 4)
                if items.count > 1 { dots }
            }
            .animation(.easeInOut(duration: 0.35), value: index)
            .padding(.horizontal, 28)
            .padding(.bottom, 6)
        }
        .cardHover { hovering = $0 }
        .task(id: items.map(\.id)) {
            // Take turns every few seconds, pausing while the pointer is over it.
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(8))
                if !hovering, items.count > 1 {
                    withAnimation(.easeInOut(duration: 0.8)) { index = (index + 1) % items.count }
                }
            }
        }
    }

    private var metaLine: String {
        var parts: [String] = []
        if let y = item.year { parts.append(String(y)) }
        if let g = item.genres?.first { parts.append(g) }
        if let r = item.runtimeMin, r > 0 { parts.append(formatDuration(Double(r * 60))) }
        if let p = item.progress, let left = timeLeft(p) { parts.append(left) }
        return parts.joined(separator: " · ")
    }

    private var dots: some View {
        HStack(spacing: 7) {
            ForEach(items.indices, id: \.self) { i in
                Button {
                    withAnimation(.easeInOut(duration: 0.6)) { index = i }
                } label: {
                    Capsule()
                        .fill(.white.opacity(i == index ? 0.95 : 0.35))
                        .frame(width: i == index ? 18 : 7, height: 7)
                }
                .buttonStyle(.plain)
                .help(items[i].title)
            }
        }
        .padding(.top, 10)
    }
}

/// Picks a button style at run time.
struct AnyButtonStyle: ButtonStyle {
    private let make: (Configuration) -> AnyView
    init<S: ButtonStyle>(_ style: S) { make = { AnyView(style.makeBody(configuration: $0)) } }
    func makeBody(configuration: Configuration) -> some View { make(configuration) }
}
