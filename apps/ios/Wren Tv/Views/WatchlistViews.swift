import SwiftUI

/// Movies and shows saved for later, newest added first.
struct WatchlistView: View {
    @Environment(AppState.self) private var state
    @Environment(\.metrics) private var m
    @State private var items: [Item]?
    @State private var error: String?
    @AppStorage("watchlistHideWatched") private var hideWatched = true

    private var shown: [Item] {
        (items ?? []).filter { !hideWatched || !$0.isWatched }
    }

    var body: some View {
        ScrollView {
            if items != nil {
                LazyVGrid(columns: m.columns, alignment: .leading, spacing: m.rowSpacing) {
                    ForEach(shown) { item in
                        NavigationLink(value: item) { PosterCard(item: item, fill: true) }
                            .buttonStyle(.plain)
                            .contextMenu { ItemContextMenu(item: item, onWatchlist: true) }
                    }
                }
                .padding(m.gutter)
                if shown.isEmpty {
                    ContentUnavailableView("Your watchlist is empty", systemImage: "bookmark",
                                           description: Text("Add movies and shows from their pages to find them here."))
                        .padding(.top, 60)
                }
            } else if let error {
                ContentUnavailableView("Couldn't load", systemImage: "exclamationmark.triangle", description: Text(error))
                    .padding(.top, 120)
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 160)
            }
        }
        .navigationTitle("Watchlist")
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Toggle(isOn: $hideWatched) { Label("Hide Watched", systemImage: "eye.slash") }
            }
        }
        .refreshable { await load() }
        .task(id: state.refreshTick) { await load() }
    }

    private func load() async {
        do {
            items = try await state.api?.watchlist() ?? []
            error = nil
        } catch {
            if items == nil { self.error = error.localizedDescription }
        }
    }
}

/// The home page row of the watchlist.
struct WatchlistShelf: View {
    let items: [Item]?

    var body: some View {
        if let items, !items.isEmpty {
            Shelf(title: "My Watchlist") {
                ForEach(items) { item in
                    NavigationLink(value: item) { PosterCard(item: item) }
                        .buttonStyle(.plain)
                        .contextMenu { ItemContextMenu(item: item, onWatchlist: true) }
                }
            }
        }
    }
}

/// Adds a movie or show to the watchlist, or takes it off. Flips at once;
/// the server catches up behind it.
struct WatchlistButton: View {
    @Environment(AppState.self) private var state
    let item: Item
    let listed: Bool
    @State private var on: Bool?
    @State private var notice: String?

    var body: some View {
        let isOn = on ?? listed
        Button {
            let next = !isOn
            on = next
            UIImpactFeedbackGenerator(style: .light).impactOccurred()
            Task {
                do {
                    try await state.api?.setWatchlist(item.id, next)
                    state.refreshTick += 1
                } catch {
                    on = !next
                    notice = error.localizedDescription
                }
            }
        } label: {
            Label(isOn ? "On Watchlist" : "Watchlist", systemImage: isOn ? "bookmark.fill" : "bookmark")
        }
        .buttonStyle(.glassCapsule)
        .onChange(of: listed) { on = nil }
        .alert(notice ?? "", isPresented: Binding(get: { notice != nil }, set: { if !$0 { notice = nil } })) { Button("OK") {} }
    }
}
