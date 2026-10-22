import SwiftUI


// MARK: Collections

/// Every collection, or those for one library.
struct CollectionsView: View {
    @Environment(AppState.self) private var state
    @Environment(\.metrics) private var m
    var libraryId: String?
    var embedded = false
    @State private var collections: [WrenCollection]?
    @State private var error: String?

    var body: some View {
        ScrollView {
            if let collections {
                if collections.isEmpty {
                    ContentUnavailableView("No collections yet", systemImage: "square.stack",
                                           description: Text(state.isAdmin ? "Create collections in the web app: hand-picked, smart rules, or synced from TMDB." : "Collections the server owner makes show up here."))
                        .padding(.top, 100)
                } else if !embedded {
                    // A shelf per kind: the server's own, franchises, lists, charts and smart collections.
                    LazyVStack(alignment: .leading, spacing: 30) {
                        ForEach(grouped(collections), id: \.name) { g in
                            Shelf(title: g.name, subtitle: "\(g.collections.count) collection\(g.collections.count == 1 ? "" : "s")") {
                                ForEach(g.collections) { c in
                                    NavigationLink(value: c) { CollectionTile(collection: c) }
                                        .buttonStyle(.plain)
                                }
                            }
                        }
                    }
                    .padding(.vertical, 20)
                } else {
                    LazyVGrid(columns: m.columns, alignment: .leading, spacing: m.rowSpacing) {
                        ForEach(collections) { c in
                            NavigationLink(value: c) { CollectionCard(collection: c, width: m.compact ? 104 : 150) }
                                .buttonStyle(.plain)
                        }
                    }
                    .padding(m.gutter)
                }
            } else if let error {
                ContentUnavailableView("Couldn't load", systemImage: "exclamationmark.triangle", description: Text(error))
                    .padding(.top, 100)
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 160)
            }
        }
        .modifier(TitleIfStandalone(title: "Collections", standalone: !embedded))
        .task(id: "\(libraryId ?? "")|\(state.refreshTick)") {
            do { collections = try await state.api?.collections(library: libraryId) } catch { self.error = error.localizedDescription }
        }
    }
}

/// In the server's order, which already puts the groups in theirs.
private func grouped(_ list: [WrenCollection]) -> [(name: String, collections: [WrenCollection])] {
    var out: [(name: String, collections: [WrenCollection])] = []
    for c in list {
        let g = c.group ?? "Collections"
        if let i = out.firstIndex(where: { $0.name == g }) { out[i].collections.append(c) } else { out.append((g, [c])) }
    }
    return out
}

/// A collection's wide tile: its artwork with the name over it.
struct CollectionTile: View {
    @Environment(AppState.self) private var state
    @Environment(\.metrics) private var m
    let collection: WrenCollection

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            ZStack(alignment: .bottomLeading) {
                RemoteImage(url: state.api?.imageURL(collection.backdropPath ?? collection.posterPath, size: "w780"))
                LinearGradient(colors: [.clear, .black.opacity(0.8)], startPoint: .center, endPoint: .bottom)
                Text(collection.name).font(.headline.bold()).foregroundStyle(.white).lineLimit(2).padding(12)
            }
            .frame(width: m.landscape, height: m.landscape * 9 / 16)
            .overlay(alignment: .topLeading) {
                if collection.active == false {
                    Text("OUT OF SEASON")
                        .font(.system(size: 9, weight: .bold)).kerning(0.6).foregroundStyle(.white)
                        .padding(.horizontal, 8).padding(.vertical, 4)
                        .background(.black.opacity(0.7), in: Capsule())
                        .padding(8)
                }
            }
            .clipShape(RoundedRectangle(cornerRadius: 10))
            Text("\(collection.itemCount ?? 0) title\(collection.itemCount == 1 ? "" : "s") · \(collection.kindLabel)")
                .font(.caption).foregroundStyle(.secondary)
        }
        .frame(width: m.landscape)
        .contentShape(Rectangle())
    }
}

struct CollectionCard: View {
    @Environment(AppState.self) private var state
    let collection: WrenCollection
    var width: CGFloat = 150

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            ZStack {
                // Two offset cards behind the poster read as a stack.
                RoundedRectangle(cornerRadius: 8).fill(.white.opacity(0.05))
                    .frame(width: width - 16, height: width * 1.5)
                    .offset(y: -8)
                RoundedRectangle(cornerRadius: 8).fill(.white.opacity(0.08))
                    .frame(width: width - 8, height: width * 1.5)
                    .offset(y: -4)
                RemoteImage(url: state.api?.imageURL(collection.posterPath))
                    .frame(width: width, height: width * 1.5)
                    .overlay {
                        if collection.posterPath == nil {
                            VStack(spacing: 8) {
                                Image(systemName: "square.stack").font(.title)
                                Text(collection.name).font(.headline).multilineTextAlignment(.center)
                            }
                            .foregroundStyle(.secondary)
                            .padding(8)
                        }
                    }
                    .overlay(alignment: .bottom) {
                        if collection.showOnHome || collection.active == false {
                            Text(collection.active == false ? "OUT OF SEASON" : "ON HOME")
                                .font(.system(size: 9, weight: .bold)).kerning(0.6)
                                .padding(.vertical, 4)
                                .frame(maxWidth: .infinity)
                                .background(.black.opacity(0.7))
                        }
                    }
                    .clipShape(RoundedRectangle(cornerRadius: 8))
                    .hoverLift()
            }
            .padding(.top, 8)
            Text(collection.name).font(.callout).lineLimit(1)
            Text("\(collection.itemCount ?? 0) title\(collection.itemCount == 1 ? "" : "s") · \(collection.kindLabel)")
                .font(.caption).foregroundStyle(.secondary)
        }
        .frame(width: width)
        .contentShape(Rectangle())
    }
}

struct CollectionDetailView: View {
    @Environment(AppState.self) private var state
    @Environment(\.metrics) private var m
    let seed: WrenCollection
    @State private var sort = ""

    /// One shelf in the collection's order, a page at a time.
    private var pager: CollectionPager { CollectionPager.of(seed.id, sort: sort) }
    private var collection: WrenCollection { pager.collection ?? seed }

    var body: some View {
        let pager = pager
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                PageBanner(label: "COLLECTION", title: collection.name, overview: collection.description, meta: meta,
                           backdrop: collection.backdropPath ?? pager.entries.first { $0.backdropPath != nil }?.backdropPath,
                           warning: state.isAdmin ? collection.syncError : nil)
                if !pager.entries.isEmpty {
                    Shelf(title: collection.name, subtitle: pager.total > 0 ? "\(pager.total)" : nil) {
                        ForEach(Array(pager.entries.enumerated()), id: \.element.entryKey) { i, t in
                            card(t).onAppear { if let api = state.api { pager.appeared(i, api) } }
                        }
                        if pager.error != nil, let api = state.api {
                            Button { pager.retry(api) } label: {
                                VStack(spacing: 8) {
                                    Image(systemName: "arrow.clockwise").font(.title2)
                                    Text("Couldn't load more").font(.caption)
                                }
                                .foregroundStyle(.secondary)
                                .frame(width: m.poster, height: m.poster * 1.5)
                                .background(.quaternary, in: RoundedRectangle(cornerRadius: 10))
                            }
                            .buttonStyle(.plain)
                        }
                    }
                    .padding(.vertical, 20)
                } else if let error = pager.error {
                    ContentUnavailableView("Couldn't load", systemImage: "exclamationmark.triangle", description: Text(error))
                } else if pager.done {
                    ContentUnavailableView("Nothing in this collection yet", systemImage: "square.stack")
                } else {
                    ProgressView().frame(maxWidth: .infinity).padding(.top, 60)
                }
            }
        }
        .ignoresSafeArea(edges: .top)
        .navigationTitle(collection.name)
        .navigationBarTitleDisplayMode(.inline)
        .toolbarBackground(.hidden, for: .navigationBar)
        .toolbar {
            Menu {
                Picker("Order", selection: $sort) {
                    Text("Collection order").tag("")
                    Text("Title").tag("title")
                    Text("Year (newest)").tag("year")
                    Text("Year (oldest)").tag("year_asc")
                    Text("Rating").tag("rating")
                    Text("Recently added").tag("added")
                }
                .pickerStyle(.inline)
            } label: {
                Label("Order", systemImage: "arrow.up.arrow.down")
            }
            .help("Order")
        }
        .task(id: "\(seed.id)|\(sort)|\(state.refreshTick)") {
            if let api = state.api { await pager.start(api, refresh: state.refreshTick) }
        }
    }

    private var meta: [String] {
        var meta: [String] = []
        if !pager.countLabel.isEmpty { meta.append(pager.countLabel) }
        meta.append(collection.kindLabel)
        if !collection.visibleFrom.isEmpty { meta.append("Visible \(collection.visibleFrom) – \(collection.visibleUntil)") }
        return meta
    }

    @ViewBuilder
    private func card(_ t: TitleView) -> some View {
        if let item = t.item {
            NavigationLink(value: item) { PosterCard(item: item) }
                .buttonStyle(.plain)
                .contextMenu { menu(item) }
        } else {
            TitleCard(title: t)
        }
    }

    @ViewBuilder
    private func menu(_ item: Item) -> some View {
        ItemContextMenu(item: item)
        if state.isAdmin && collection.kind == "manual" {
            Button("Remove from Collection") { remove(item) }
        }
    }

    private func remove(_ item: Item) {
        Task {
            try? await state.api?.removeFromCollection(seed.id, item: item.id)
            state.refreshTick += 1
        }
    }
}

/// A page's banner: its artwork edge to edge, with what it is over it.
struct PageBanner: View {
    @Environment(AppState.self) private var state
    @Environment(\.metrics) private var m
    let label: String
    let title: String
    var overview: String = ""
    var meta: [String] = []
    var backdrop: String?
    var warning: String?

    var body: some View {
        ZStack(alignment: .bottomLeading) {
            RemoteImage(url: state.api?.imageURL(backdrop, size: "w1280"))
                .frame(height: m.heroHeight)
                .frame(maxWidth: .infinity)
                .clipped()
                .overlay(LinearGradient(stops: [.init(color: .clear, location: 0.35), .init(color: .black.opacity(0.55), location: 0.75),
                                                .init(color: Color.black, location: 1)],
                                        startPoint: .top, endPoint: .bottom))
                .overlay(LinearGradient(colors: [.black.opacity(0.65), .clear], startPoint: .leading, endPoint: .center))
            VStack(alignment: .leading, spacing: 8) {
                Text(label).font(.caption.weight(.bold)).kerning(1.2).foregroundStyle(Color.wrenEmber)
                Text(title).font(.system(size: m.heroTitle, weight: .bold))
                if !overview.isEmpty {
                    Text(overview).foregroundStyle(.secondary).lineLimit(3).frame(maxWidth: 680, alignment: .leading)
                }
                if !meta.isEmpty {
                    Text(meta.joined(separator: "  ·  ")).font(.callout).foregroundStyle(.secondary)
                }
                if let warning, !warning.isEmpty {
                    Label(warning, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(Color.wrenEmber)
                }
            }
            .padding(.horizontal, m.gutter)
        }
    }
}

// MARK: Genres

struct GenresView: View {
    @Environment(AppState.self) private var state
    @Environment(\.metrics) private var m
    var libraryId: String?
    var embedded = false
    @State private var genres: [GenreCount]?

    var body: some View {
        ScrollView {
            if let genres {
                if genres.isEmpty {
                    ContentUnavailableView("No genres yet", systemImage: "tag", description: Text("Genres come from TMDB once titles are matched."))
                        .padding(.top, 100)
                }
                LazyVGrid(columns: [GridItem(.adaptive(minimum: m.compact ? 150 : 230, maximum: 320), spacing: m.compact ? 12 : 18)], alignment: .leading, spacing: 18) {
                    ForEach(genres) { g in
                        NavigationLink(value: GenreRoute(genre: g.genre, libraryId: libraryId)) { GenreTile(genre: g) }
                            .buttonStyle(.plain)
                    }
                }
                .padding(m.gutter)
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 160)
            }
        }
        .modifier(TitleIfStandalone(title: "Genres", standalone: !embedded))
        .task(id: "\(libraryId ?? "")|\(state.refreshTick)") {
            genres = (try? await state.api?.genres(library: libraryId)) ?? []
        }
    }
}

private struct GenreTile: View {
    @Environment(AppState.self) private var state
    let genre: GenreCount
    @State private var hover = false

    var body: some View {
        ZStack(alignment: .bottomLeading) {
            RemoteImage(url: state.api?.imageURL(genre.backdropPath, size: "w780"))
                .frame(height: 130)
                .frame(maxWidth: .infinity)
                .clipped()
                .overlay(LinearGradient(colors: [.black.opacity(0.1), .black.opacity(0.8)], startPoint: .top, endPoint: .bottom))
            VStack(alignment: .leading, spacing: 2) {
                Text(genre.genre).font(.title3.bold())
                Text(countLabel).font(.caption).foregroundStyle(.secondary)
            }
            .padding(14)
        }
        .clipShape(RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(hover ? Color.wrenEmber : .white.opacity(0.08), lineWidth: hover ? 2 : 1))
        .onHover { hover = $0 }
        .animation(.easeOut(duration: 0.15), value: hover)
    }

    private var countLabel: String {
        var parts: [String] = []
        if genre.movies > 0 { parts.append("\(genre.movies) movie\(genre.movies == 1 ? "" : "s")") }
        if genre.shows > 0 { parts.append("\(genre.shows) show\(genre.shows == 1 ? "" : "s")") }
        return parts.joined(separator: " · ")
    }
}

struct GenreItemsView: View {
    @Environment(AppState.self) private var state
    @Environment(\.metrics) private var m
    let route: GenreRoute
    @AppStorage("genreSort") private var sort = "title"
    @State private var kind = ""
    @State private var unwatched = false
    @State private var items: [Item]?

    var body: some View {
        ScrollView {
            if let items {
                LazyVGrid(columns: m.columns, alignment: .leading, spacing: m.rowSpacing) {
                    ForEach(items) { item in
                        NavigationLink(value: item) { PosterCard(item: item, fill: true) }
                            .buttonStyle(.plain)
                            .contextMenu { ItemContextMenu(item: item) }
                    }
                }
                .padding(m.gutter)
                if items.isEmpty {
                    ContentUnavailableView("Nothing matches", systemImage: "line.3.horizontal.decrease.circle")
                }
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 160)
            }
        }
        .navigationTitle(route.genre)
        .navigationSubtitleIfAvailable(items.map { "\($0.count) title\($0.count == 1 ? "" : "s")" } ?? "")
        .toolbar {
            Picker("Show", selection: $kind) {
                Text("All").tag("")
                Text("Movies").tag("movie")
                Text("Shows").tag("show")
            }
            .pickerStyle(.segmented)
            .help("Movies or shows")
            Toggle(isOn: $unwatched) { Label("Unwatched", systemImage: "eye.slash") }
                .help("Only unwatched")
            SortMenu(sort: $sort)
        }
        .task(id: "\(route.genre)|\(route.libraryId ?? "")|\(kind)|\(sort)|\(unwatched)|\(state.refreshTick)") {
            items = (try? await state.api?.genreItems(route.genre, library: route.libraryId, kind: kind.isEmpty ? nil : kind,
                                                      sort: sort, unwatched: unwatched))?.items ?? []
        }
    }
}

/// A toolbar menu for ordering titles.
struct SortMenu: View {
    @Binding var sort: String

    static let options: [(String, String)] = [
        ("title", "Title"), ("added", "Recently Added"), ("year", "Year (newest)"), ("year_asc", "Year (oldest)"), ("rating", "Rating"),
    ]

    var body: some View {
        Menu {
            Picker("Sort", selection: $sort) {
                ForEach(Self.options, id: \.0) { Text($0.1).tag($0.0) }
            }
            .pickerStyle(.inline)
        } label: {
            Label("Sort: \(Self.options.first { $0.0 == sort }?.1 ?? "Title")", systemImage: "arrow.up.arrow.down")
                .labelStyle(.titleAndIcon)
        }
        .help("Sort")
    }
}

/// Sets the navigation title only when the view isn't a tab inside another page.
private struct TitleIfStandalone: ViewModifier {
    let title: String
    let standalone: Bool

    func body(content: Content) -> some View {
        if standalone { content.navigationTitle(title) } else { content }
    }
}
