import SwiftUI

/// Movies or TV Shows when the server streams: TMDB's whole catalog, loading
/// more as you scroll. Titles the server has open their item; the rest stream.
struct CatalogView: View {
    @Environment(AppState.self) private var state
    let type: String // "movie" or "show"
    @AppStorage("catalogSort") private var sort = "popular"
    @State private var genre: Int?
    @State private var genres: [CatalogPage.Genre] = []
    @State private var titles: [TitleView] = []
    @State private var page = 0
    @State private var totalPages = 1
    @State private var loading = false
    @State private var enabled = true
    @State private var error: String?

    private var label: String { type == "show" ? "TV Shows" : "Movies" }

    var body: some View {
        ScrollView {
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 150, maximum: 180), spacing: 22)], alignment: .leading, spacing: 26) {
                ForEach(titles) { v in
                    TitleCard(title: v)
                        .onAppear { if v.id == titles.last?.id { Task { await loadMore() } } }
                }
            }
            .padding(28)
            if loading {
                ProgressView().frame(maxWidth: .infinity).padding(.vertical, titles.isEmpty ? 200 : 24)
            } else if !enabled {
                ContentUnavailableView("\(label) need TMDB", systemImage: "sparkles.tv",
                                       description: Text(state.isAdmin ? "Add a TMDB API key under Server → Settings." : "The server owner hasn't set up TMDB yet."))
                    .padding(.top, 120)
            } else if let error, titles.isEmpty {
                ContentUnavailableView("Couldn't load", systemImage: "exclamationmark.triangle", description: Text(error))
                    .padding(.top, 120)
            } else if titles.isEmpty {
                ContentUnavailableView("Nothing matches", systemImage: "film.stack").padding(.top, 120)
            }
        }
        .navigationTitle(label)
        .navigationSubtitle(genres.first { $0.id == genre }?.name ?? "")
        .toolbar {
            ToolbarItemGroup {
                Menu {
                    Picker("Genre", selection: $genre) {
                        Text("All genres").tag(Int?.none)
                        Divider()
                        ForEach(genres) { g in Text(g.name).tag(Optional(g.id)) }
                    }
                    .pickerStyle(.inline)
                } label: {
                    Label(genres.first { $0.id == genre }?.name ?? "All genres", systemImage: "tag").labelStyle(.titleAndIcon)
                }
                .help("Genre")
                Menu {
                    Picker("Sort", selection: $sort) {
                        Text("Popular").tag("popular")
                        Text("Top Rated").tag("top_rated")
                        Text("Newest").tag("new")
                    }
                    .pickerStyle(.inline)
                } label: {
                    Label("Sort", systemImage: "arrow.up.arrow.down")
                }
                .help("Sort")
            }
        }
        .task(id: "\(type)|\(sort)|\(genre ?? 0)") { await reload() }
    }

    private func reload() async {
        titles = []
        page = 0
        totalPages = 1
        await loadMore()
    }

    private func loadMore() async {
        guard !loading, page < totalPages, let api = state.api else { return }
        loading = true
        defer { loading = false }
        do {
            let res = try await api.catalog(type: type, sort: sort, genre: genre, page: page + 1)
            enabled = res.enabled
            if !res.genres.isEmpty { genres = res.genres }
            var seen = Set(titles.map(\.id))
            titles += res.items.filter { seen.insert($0.id).inserted }
            page = res.page
            totalPages = res.totalPages
            error = nil
        } catch is CancellationError {
        } catch {
            self.error = error.localizedDescription
        }
    }
}
