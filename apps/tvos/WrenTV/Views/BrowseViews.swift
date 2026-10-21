import SwiftUI

private let sortOptions: [(String, String)] = [
    ("title", "Title"), ("added", "Recently Added"), ("year", "Newest"), ("year_asc", "Oldest"), ("rating", "Rating"),
]

private var posterColumns: [GridItem] { [GridItem(.adaptive(minimum: TV.poster, maximum: TV.poster), spacing: 48, alignment: .top)] }

/// A library's titles as a poster grid, with sort and genre filters.
struct LibraryView: View {
    @Environment(AppState.self) private var state
    let library: Library
    @AppStorage("librarySort") private var sort = "title"
    @State private var genre: String?
    @State private var unwatched = false
    @State private var items: [Item]?
    @State private var genres: [GenreCount] = []
    @State private var error: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 30) {
                HStack(spacing: 24) {
                    Text(library.name).font(.title2.bold())
                    if let items { Text("\(items.count)").foregroundStyle(.secondary) }
                    Spacer()
                    Menu {
                        Picker("Sort", selection: $sort) {
                            ForEach(sortOptions, id: \.0) { Text($0.1).tag($0.0) }
                        }
                    } label: {
                        Label(sortOptions.first { $0.0 == sort }?.1 ?? "Title", systemImage: "arrow.up.arrow.down")
                    }
                    if !genres.isEmpty {
                        Menu {
                            Picker("Genre", selection: $genre) {
                                Text("All Genres").tag(String?.none)
                                ForEach(genres) { Text($0.genre).tag(Optional($0.genre)) }
                            }
                        } label: {
                            Label(genre ?? "All Genres", systemImage: "line.3.horizontal.decrease")
                        }
                    }
                    Button { unwatched.toggle() } label: {
                        Label("Unwatched", systemImage: unwatched ? "checkmark.circle.fill" : "circle")
                    }
                }
                .focusSection()
                if let items {
                    if items.isEmpty {
                        EmptyState(title: genre == nil && !unwatched ? "This library is empty" : "Nothing matches")
                    } else {
                        LazyVGrid(columns: posterColumns, alignment: .leading, spacing: 56) {
                            ForEach(items) { PosterCard(item: $0) }
                        }
                    }
                } else if let error {
                    EmptyState(title: "Couldn't load", message: error, symbol: "wifi.exclamationmark")
                } else {
                    ProgressView().frame(maxWidth: .infinity).padding(.top, 200)
                }
            }
            .padding(.horizontal, TV.gutter)
            .padding(.vertical, 40)
        }
        .task(id: "\(library.id)|\(sort)|\(genre ?? "")|\(unwatched)|\(state.refreshTick)") {
            do {
                items = try await state.api?.items(library: library.id, sort: sort, genre: genre, unwatched: unwatched).items
                error = nil
            } catch {
                if items == nil { self.error = error.localizedDescription }
            }
        }
        .task(id: library.id) {
            genres = (try? await state.api?.genres(library: library.id)) ?? []
        }
    }
}

/// One genre's titles.
struct GenreView: View {
    @Environment(AppState.self) private var state
    let route: GenreRoute
    @State private var items: [Item]?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 30) {
                Text(route.genre).font(.title2.bold())
                if let items {
                    LazyVGrid(columns: posterColumns, alignment: .leading, spacing: 56) {
                        ForEach(items) { PosterCard(item: $0) }
                    }
                } else {
                    ProgressView().frame(maxWidth: .infinity).padding(.top, 200)
                }
            }
            .padding(.horizontal, TV.gutter)
            .padding(.vertical, 40)
        }
        .task(id: route) {
            items = (try? await state.api?.genreItems(route.genre, library: route.libraryId))?.items ?? []
        }
    }
}

/// A title the server doesn't have yet: adds it to the streaming library,
/// then shows it like any other.
struct StreamTitleView: View {
    @Environment(AppState.self) private var state
    let route: StreamTitleRoute
    @State private var item: Item?
    @State private var error: String?

    var body: some View {
        Group {
            if let item {
                DetailView(route: ItemRoute(id: item.id, title: item.title))
            } else if let error {
                EmptyState(title: "Couldn't open \(route.title)", message: error, symbol: "exclamationmark.triangle")
            } else {
                VStack(spacing: 24) {
                    ProgressView()
                    Text("Opening \(route.title)…").foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .task(id: route) {
            do {
                item = try await state.api?.addStreamTitle(tmdbId: route.tmdbId, mediaType: route.mediaType)
                await state.reloadLibraries()
            } catch {
                self.error = error.localizedDescription
            }
        }
    }
}

/// Streaming: trending and popular titles from TMDB, playable whether or
/// not the server has them yet.
struct DiscoverView: View {
    @Environment(AppState.self) private var state
    @State private var home: DiscoverHome?
    @State private var error: String?

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 20) {
                if let h = home {
                    if let first = h.hero.first { DiscoverHero(title: first) }
                    if !h.continueWatching.isEmpty {
                        Shelf(title: "Continue Watching") { ForEach(h.continueWatching) { LandscapeCard(item: $0) } }
                    }
                    if let w = h.watchlist, !w.isEmpty {
                        Shelf(title: "My Watchlist") { ForEach(w) { PosterCard(item: $0) } }
                    }
                    ForEach(h.rows) { row in
                        Shelf(title: row.title) {
                            ForEach(Array(row.items.enumerated()), id: \.element.id) { i, t in
                                TitleCard(title: t, rank: row.id == "trending" ? i + 1 : nil)
                            }
                        }
                    }
                    if !h.enabled {
                        EmptyState(title: "Discovery needs TMDB",
                                   message: state.isAdmin ? "Add a TMDB API key in the server's settings." : "The server owner hasn't set up TMDB yet.",
                                   symbol: "sparkles")
                    }
                } else if let error {
                    EmptyState(title: "Couldn't load", message: error, symbol: "wifi.exclamationmark")
                } else {
                    ProgressView().frame(maxWidth: .infinity).padding(.top, 300)
                }
            }
            .padding(.bottom, 80)
        }
        .ignoresSafeArea(edges: .horizontal)
        .task(id: state.refreshTick) {
            do {
                home = try await state.api?.discoverHome()
            } catch {
                if home == nil { self.error = error.localizedDescription }
            }
        }
    }
}

private struct DiscoverHero: View {
    @Environment(AppState.self) private var state
    let title: TitleView

    var body: some View {
        ZStack(alignment: .bottomLeading) {
            RemoteImage(url: state.api?.imageURL(title.backdropPath, size: "w1280"))
                .frame(height: 700)
                .overlay { LinearGradient(colors: [.clear, Color.wrenInk], startPoint: .center, endPoint: .bottom) }
                .overlay { LinearGradient(colors: [Color.wrenInk.opacity(0.85), .clear], startPoint: .leading, endPoint: .trailing) }
            VStack(alignment: .leading, spacing: 18) {
                Text("Trending this week").font(.callout.weight(.semibold)).foregroundStyle(Color.wrenEmber)
                Text(title.title).font(.system(size: 64, weight: .bold)).lineLimit(2)
                if let o = title.overview { Text(o).font(.callout).lineLimit(3).frame(maxWidth: 900, alignment: .leading) }
                Group {
                    if let item = title.item {
                        NavigationLink(value: ItemRoute(id: item.id, title: item.title)) { Label("More Info", systemImage: "info.circle") }
                    } else {
                        NavigationLink(value: StreamTitleRoute(tmdbId: title.tmdbId, mediaType: title.mediaType, title: title.title)) {
                            Label("Watch", systemImage: "play.fill")
                        }
                    }
                }
                .padding(.top, 10)
            }
            .padding(.horizontal, TV.gutter)
            .padding(.bottom, 40)
        }
        .frame(height: 700)
        .focusSection()
    }
}

/// Search the libraries, and TMDB too when streaming is on.
struct SearchView: View {
    @Environment(AppState.self) private var state
    @State private var query = ""
    @State private var items: [Item] = []
    @State private var titles: [TitleView] = []
    @State private var searching = false

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 20) {
                if !items.isEmpty {
                    Shelf(title: "In Your Library") { ForEach(items) { PosterCard(item: $0) } }
                }
                let more = titles.filter { $0.item == nil }
                if !more.isEmpty {
                    Shelf(title: "Stream") { ForEach(more) { TitleCard(title: $0) } }
                }
                if !query.isEmpty && !searching && items.isEmpty && more.isEmpty {
                    EmptyState(title: "No results for “\(query)”", symbol: "magnifyingglass")
                }
            }
            .padding(.vertical, 40)
        }
        .searchable(text: $query, prompt: "Movies, shows and episodes")
        .task(id: query) {
            let q = query.trimmingCharacters(in: .whitespaces)
            guard !q.isEmpty else { items = []; titles = []; return }
            try? await Task.sleep(for: .milliseconds(350))
            guard !Task.isCancelled, let api = state.api else { return }
            searching = true
            defer { searching = false }
            items = (try? await api.search(q)) ?? []
            titles = state.canStream ? ((try? await api.discoverSearch(q))?.items ?? []) : []
        }
    }
}
