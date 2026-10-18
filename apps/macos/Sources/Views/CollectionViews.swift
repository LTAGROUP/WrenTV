import SwiftUI

private let gridColumns = [GridItem(.adaptive(minimum: 150, maximum: 180), spacing: 22)]

// MARK: Collections

/// Every collection, or those for one library.
struct CollectionsView: View {
    @Environment(AppState.self) private var state
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
                } else {
                    LazyVGrid(columns: gridColumns, alignment: .leading, spacing: 26) {
                        ForEach(collections) { c in
                            NavigationLink(value: c) { CollectionCard(collection: c) }
                                .buttonStyle(.plain)
                        }
                    }
                    .padding(28)
                }
            } else if let error {
                ContentUnavailableView("Couldn't load", systemImage: "exclamationmark.triangle", description: Text(error))
                    .padding(.top, 100)
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 160)
            }
        }
        .modifier(TitleIfStandalone(title: "Collections", standalone: !embedded))
        // A library's page already has one.
        .modifier(AmbientIfStandalone(standalone: !embedded))
        .task(id: "\(libraryId ?? "")|\(state.refreshTick)") {
            do { collections = try await state.api?.collections(library: libraryId) } catch { self.error = error.localizedDescription }
        }
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
                    .ambient(collection.backdropPath)
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
    let seed: WrenCollection
    @State private var detail: CollectionDetail?
    @State private var sort = ""
    @State private var error: String?

    private var collection: WrenCollection { detail?.collection ?? seed }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                header
                if let detail, let titles = detail.titles {
                    LazyVGrid(columns: gridColumns, alignment: .leading, spacing: 26) {
                        ForEach(sorted(titles)) { TitleCard(title: $0) }
                    }
                    .padding(.horizontal, 28)
                    .padding(.vertical, 20)
                    if titles.isEmpty {
                        ContentUnavailableView("Nothing in this collection yet", systemImage: "square.stack")
                    }
                } else if let detail {
                    LazyVGrid(columns: gridColumns, alignment: .leading, spacing: 26) {
                        ForEach(detail.items) { item in
                            NavigationLink(value: item) { PosterCard(item: item) }
                                .buttonStyle(.plain)
                                .contextMenu {
                                    ItemContextMenu(item: item)
                                    if state.isAdmin && collection.kind == "manual" {
                                        Button("Remove from Collection") { remove(item) }
                                    }
                                }
                        }
                    }
                    .padding(.horizontal, 28)
                    .padding(.vertical, 20)
                    if detail.items.isEmpty {
                        ContentUnavailableView("Nothing in this collection yet", systemImage: "square.stack")
                    }
                    if let missing = detail.missing, !missing.isEmpty {
                        Text("Not in your library").font(.title3.bold()).padding(.horizontal, 28).padding(.top, 10)
                        LazyVGrid(columns: gridColumns, alignment: .leading, spacing: 26) {
                            ForEach(missing) { TitleCard(title: $0) }
                        }
                        .padding(28)
                    }
                } else if let error {
                    ContentUnavailableView("Couldn't load", systemImage: "exclamationmark.triangle", description: Text(error))
                } else {
                    ProgressView().frame(maxWidth: .infinity).padding(.top, 60)
                }
            }
        }
        .ignoresSafeArea(edges: .top)
        .navigationTitle(collection.name)
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
        .task(id: "\(seed.id)|\(sort)|\(state.refreshTick)") { await load() }
    }

    private var header: some View {
        ZStack(alignment: .bottomLeading) {
            RemoteImage(url: state.api?.imageURL(collection.backdropPath, size: "w1280"))
                .frame(height: 300)
                .frame(maxWidth: .infinity)
                .clipped()
                .overlay(LinearGradient(colors: [.clear, .black.opacity(0.5), Color(nsColor: .windowBackgroundColor)], startPoint: .top, endPoint: .bottom))
            VStack(alignment: .leading, spacing: 8) {
                Text("COLLECTION").font(.caption.weight(.bold)).kerning(1.2).foregroundStyle(Color.wrenEmber)
                Text(collection.name).font(.system(size: 36, weight: .bold))
                if !collection.description.isEmpty {
                    Text(collection.description).foregroundStyle(.secondary).lineLimit(3).frame(maxWidth: 680, alignment: .leading)
                }
                HStack(spacing: 8) {
                    if let titles = detail?.titles {
                        Text("\(titles.count) title\(titles.count == 1 ? "" : "s")")
                    } else {
                        Text("\(detail?.items.count ?? collection.itemCount ?? 0) in your library")
                    }
                    Text("·")
                    Text(collection.kindLabel)
                    if !collection.visibleFrom.isEmpty {
                        Text("·")
                        Text("Visible \(collection.visibleFrom) – \(collection.visibleUntil)")
                    }
                }
                .font(.callout)
                .foregroundStyle(.secondary)
                if let e = collection.syncError, !e.isEmpty, state.isAdmin {
                    Label(e, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(Color.wrenEmber)
                }
            }
            .padding(.horizontal, 28)
        }
    }

    /// The server orders library items; full TMDB title lists are ordered here.
    private func sorted(_ titles: [TitleView]) -> [TitleView] {
        switch sort {
        case "title": return titles.sorted { $0.title.localizedStandardCompare($1.title) == .orderedAscending }
        case "year": return titles.sorted { ($0.year ?? 0) > ($1.year ?? 0) }
        case "year_asc": return titles.sorted { ($0.year ?? 9999) < ($1.year ?? 9999) }
        case "rating": return titles.sorted { ($0.item?.rating ?? $0.rating ?? 0) > ($1.item?.rating ?? $1.rating ?? 0) }
        default: return titles
        }
    }

    private func load() async {
        do { detail = try await state.api?.collection(seed.id, sort: sort.isEmpty ? nil : sort) } catch { self.error = error.localizedDescription }
    }

    private func remove(_ item: Item) {
        Task {
            try? await state.api?.removeFromCollection(seed.id, item: item.id)
            await load()
        }
    }
}

// MARK: Genres

struct GenresView: View {
    @Environment(AppState.self) private var state
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
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 230, maximum: 320), spacing: 18)], alignment: .leading, spacing: 18) {
                    ForEach(genres) { g in
                        NavigationLink(value: GenreRoute(genre: g.genre, libraryId: libraryId)) { GenreTile(genre: g) }
                            .buttonStyle(.plain)
                    }
                }
                .padding(28)
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
    let route: GenreRoute
    @AppStorage("genreSort") private var sort = "title"
    @State private var kind = ""
    @State private var unwatched = false
    @State private var items: [Item]?

    var body: some View {
        ScrollView {
            if let items {
                LazyVGrid(columns: gridColumns, alignment: .leading, spacing: 26) {
                    ForEach(items) { item in
                        NavigationLink(value: item) { PosterCard(item: item) }
                            .buttonStyle(.plain)
                            .contextMenu { ItemContextMenu(item: item) }
                    }
                }
                .padding(28)
                if items.isEmpty {
                    ContentUnavailableView("Nothing matches", systemImage: "line.3.horizontal.decrease.circle")
                }
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 160)
            }
        }
        .navigationTitle(route.genre)
        .navigationSubtitle(items.map { "\($0.count) title\($0.count == 1 ? "" : "s")" } ?? "")
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

private struct AmbientIfStandalone: ViewModifier {
    let standalone: Bool
    func body(content: Content) -> some View {
        if standalone { content.ambientBackdrop() } else { content }
    }
}
