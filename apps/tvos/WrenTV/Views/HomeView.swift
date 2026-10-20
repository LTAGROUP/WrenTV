import SwiftUI

/// A featured title across the top, then Continue Watching, the watchlist,
/// trending, collections, what's new in each library and genres.
struct HomeView: View {
    @Environment(AppState.self) private var state
    @State private var home: HomeResponse?
    @State private var trending: TrendingResponse?
    @State private var error: String?

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 20) {
                if let h = home {
                    let picks = Self.featured(h)
                    if !picks.isEmpty { Hero(items: picks) } else { Spacer().frame(height: 40) }
                    if !h.continueWatching.isEmpty {
                        Shelf(title: "Continue Watching") {
                            ForEach(h.continueWatching) { LandscapeCard(item: $0) }
                        }
                    }
                    if let w = h.watchlist, !w.isEmpty {
                        Shelf(title: "My Watchlist") { ForEach(w) { PosterCard(item: $0) } }
                    }
                    if let t = trending, t.enabled, !t.items.isEmpty {
                        Shelf(title: "Trending This Week", subtitle: "\(t.items.count { $0.item != nil }) in your library") {
                            ForEach(Array(t.items.enumerated()), id: \.element.id) { i, title in TitleCard(title: title, rank: i + 1) }
                        }
                    }
                    ForEach(h.collections ?? [], id: \.collection.id) { row in
                        Shelf(title: row.collection.name) {
                            ForEach(row.items) { PosterCard(item: $0) }
                            ForEach(row.missing ?? []) { TitleCard(title: $0) }
                        }
                    }
                    ForEach(h.recentlyAdded, id: \.library.id) { shelf in
                        Shelf(title: "Recently Added in \(shelf.library.name)") { ForEach(shelf.items) { PosterCard(item: $0) } }
                    }
                    if let top = h.topRated, top.count >= 4 {
                        Shelf(title: "Top Rated in Your Library") { ForEach(top) { PosterCard(item: $0) } }
                    }
                    ForEach(h.genres ?? [], id: \.genre) { row in
                        Shelf(title: row.genre) { ForEach(row.items) { PosterCard(item: $0) } }
                    }
                    if h.continueWatching.isEmpty && h.recentlyAdded.isEmpty {
                        EmptyState(title: "Nothing here yet",
                                   message: state.isAdmin ? "Add a library from the Wren TV app on a computer or in a web browser." : "Ask the server owner to share a library.")
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
        .task(id: state.refreshTick) { await load() }
    }

    private func load() async {
        guard let api = state.api else { return }
        do {
            home = try await api.home()
            error = nil
        } catch {
            if home == nil { self.error = error.localizedDescription }
        }
        trending = try? await api.trending()
    }

    /// A few titles with artwork, changing every hour.
    static func featured(_ h: HomeResponse) -> [Item] {
        var seen = Set<String>()
        let pool = ((h.topRated ?? []) + h.recentlyAdded.flatMap(\.items))
            .filter { $0.backdropPath != nil && seen.insert($0.id).inserted }
        guard !pool.isEmpty else { return [] }
        let start = Int(Date().timeIntervalSince1970 / 3600) % pool.count
        var out: [Item] = [], ids = Set<String>()
        for i in 0..<min(6, pool.count) {
            let it = pool[(start + i * 3) % pool.count]
            if ids.insert(it.id).inserted { out.append(it) }
        }
        return out
    }
}

/// The big featured title: artwork, its logo, a line about it and Play.
/// Moves on to the next pick every few seconds unless a button has focus.
struct Hero: View {
    @Environment(AppState.self) private var state
    let items: [Item]
    @State private var index = 0
    @FocusState private var focus: Int?

    var body: some View {
        let item = items[min(index, items.count - 1)]
        ZStack(alignment: .bottomLeading) {
            RemoteImage(url: state.api?.imageURL(item.backdropPath, size: "w1280"))
                .frame(height: 760)
                .overlay {
                    LinearGradient(colors: [.clear, Color.wrenInk.opacity(0.6), Color.wrenInk], startPoint: .center, endPoint: .bottom)
                }
                .overlay {
                    LinearGradient(colors: [Color.wrenInk.opacity(0.85), .clear], startPoint: .leading, endPoint: .trailing)
                }
                .id(item.id)
                .transition(.opacity)
            VStack(alignment: .leading, spacing: 20) {
                if let logo = item.logoPath {
                    RemoteImage(url: state.api?.imageURL(logo, size: "w500"), contentMode: .fit)
                        .frame(maxWidth: 560, maxHeight: 180, alignment: .bottomLeading)
                } else {
                    Text(item.title).font(.system(size: 64, weight: .bold)).lineLimit(2)
                }
                Text(item.metaLine).font(.callout).foregroundStyle(.secondary)
                if let o = item.overview {
                    Text(o).font(.callout).lineLimit(3).frame(maxWidth: 900, alignment: .leading)
                }
                HStack(spacing: 24) {
                    if item.isPlayable {
                        Button { state.play(item.playTarget) } label: {
                            Label(item.inProgress ? "Resume" : "Play", systemImage: "play.fill")
                        }
                        .focused($focus, equals: 0)
                    }
                    NavigationLink(value: ItemRoute(id: item.id, title: item.title)) {
                        Label("More Info", systemImage: "info.circle")
                    }
                    .focused($focus, equals: 1)
                    if items.count > 1 {
                        HStack(spacing: 10) {
                            ForEach(items.indices, id: \.self) { i in
                                Circle().fill(i == index ? Color.white : Color.white.opacity(0.3)).frame(width: 10, height: 10)
                            }
                        }
                        .padding(.leading, 20)
                    }
                }
                .padding(.top, 10)
            }
            .padding(.horizontal, TV.gutter)
            .padding(.bottom, 40)
        }
        .frame(height: 760)
        .focusSection()
        .task(id: items.map(\.id)) {
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(10))
                if focus == nil, items.count > 1 {
                    withAnimation(.easeInOut(duration: 0.6)) { index = (index + 1) % items.count }
                }
            }
        }
    }
}
