import SwiftUI

/// The home page when the server streams: everything on TMDB in rows, not
/// just what the libraries hold. Any title plays.
struct DiscoverHomeView: View {
    @Environment(AppState.self) private var state
    @Environment(\.openWindow) private var openWindow
    @State private var home: DiscoverHome?
    @State private var error: String?
    /// Servers older than discovery don't have the endpoint.
    @State private var unsupported = false

    var body: some View {
        if unsupported { LibraryHomeView() } else { discover }
    }

    private var discover: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 30) {
                if let home {
                    if !home.hero.isEmpty { DiscoverHero(titles: home.hero, play: play) } else { Color.clear.frame(height: 40) }
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
                    ForEach(Array(home.rows.enumerated()), id: \.element.id) { i, row in
                        if i == 1 { recentlyWatched(home) }
                        Shelf(title: row.title) {
                            ForEach(Array(row.items.enumerated()), id: \.element.id) { n, v in
                                TitleCard(title: v, rank: row.id == "trending" && n < 10 ? n + 1 : nil)
                            }
                        }
                    }
                    if home.rows.count < 2 { recentlyWatched(home) }
                    if !home.enabled {
                        ContentUnavailableView("Discovery needs TMDB", systemImage: "sparkles.tv",
                                               description: Text(state.isAdmin ? "Add a TMDB API key under Server → Settings." : "The server owner hasn't set up TMDB yet."))
                            .padding(.top, 120)
                    } else if home.rows.isEmpty {
                        ContentUnavailableView("Nothing to show", systemImage: "wifi.exclamationmark",
                                               description: Text("TMDB didn't answer. Try again in a moment."))
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
        .navigationTitle("Home")
        .toolbarBackground(.hidden, for: .windowToolbar)
        .task(id: state.refreshTick) {
            do {
                home = try await state.api?.discoverHome()
                error = nil
            } catch let e as WrenTVError where e.status == 404 {
                unsupported = true
            } catch is DecodingError {
                unsupported = true
            } catch {
                if home == nil { self.error = error.localizedDescription }
            }
        }
    }

    @ViewBuilder
    private func recentlyWatched(_ home: DiscoverHome) -> some View {
        if !home.recentlyWatched.isEmpty {
            Shelf(title: "Recently Watched") {
                ForEach(home.recentlyWatched) { item in
                    NavigationLink(value: item) { PosterCard(item: item) }
                        .buttonStyle(.plain)
                        .contextMenu { ItemContextMenu(item: item) }
                }
            }
        }
    }

    private func play(_ item: Item) {
        openWindow(id: "player", value: PlayTarget(itemId: item.id, title: item.showTitle ?? item.title,
                                                   subtitle: item.kind == "episode" ? "\(item.episodeLabel) — \(item.title)" : nil))
    }
}

/// The rotating banner of this week's trending titles.
private struct DiscoverHero: View {
    @Environment(AppState.self) private var state
    let titles: [TitleView]
    let play: (Item) -> Void
    @State private var index = 0
    @State private var hovering = false
    @State private var opening = false
    @State private var notice: String?

    private var title: TitleView { titles[min(index, titles.count - 1)] }

    var body: some View {
        ZStack(alignment: .bottomLeading) {
            ZStack {
                ForEach(Array(titles.enumerated()), id: \.element.id) { i, t in
                    RemoteImage(url: state.api?.imageURL(t.backdropPath, size: "w1280"))
                        .opacity(i == index ? 1 : 0)
                }
            }
            .frame(height: 560)
            .frame(maxWidth: .infinity)
            .clipped()
            .overlay(LinearGradient(stops: [.init(color: .clear, location: 0.35), .init(color: .black.opacity(0.55), location: 0.75),
                                            .init(color: Color(nsColor: .windowBackgroundColor), location: 1)],
                                    startPoint: .top, endPoint: .bottom))
            .overlay(LinearGradient(colors: [.black.opacity(0.65), .clear], startPoint: .leading, endPoint: .center))

            VStack(alignment: .leading, spacing: 12) {
                Text("TRENDING THIS WEEK").font(.caption.bold()).kerning(1).foregroundStyle(.white.opacity(0.7))
                if let logo = title.item?.logoPath {
                    RemoteImage(url: state.api?.imageURL(logo, size: "w500"), contentMode: .fit)
                        .frame(maxWidth: 400, maxHeight: 130, alignment: .bottomLeading)
                        .id(title.id)
                } else {
                    Text(title.title).font(.system(size: 44, weight: .bold)).id(title.id)
                }
                Text(metaLine).font(.callout).foregroundStyle(.white.opacity(0.75))
                if let o = title.overview {
                    Text(o).font(.callout).lineLimit(2).foregroundStyle(.white.opacity(0.85)).frame(maxWidth: 560, alignment: .leading)
                }
                HStack(spacing: 10) {
                    if title.mediaType == "movie" {
                        Button { Task { await playTitle() } } label: {
                            if opening {
                                HStack(spacing: 6) { ProgressView().controlSize(.small); Text("Opening…") }
                            } else {
                                Label(title.item?.inProgress == true ? "Resume" : "Play", systemImage: "play.fill")
                            }
                        }
                        .buttonStyle(.primaryCapsule)
                        .disabled(opening)
                    }
                    details
                }
                .padding(.top, 4)
                if titles.count > 1 { dots }
            }
            .animation(.easeInOut(duration: 0.35), value: index)
            .padding(.horizontal, 28)
            .padding(.bottom, 6)
        }
        .onHover { hovering = $0 }
        .alert(notice ?? "", isPresented: Binding(get: { notice != nil }, set: { if !$0 { notice = nil } })) { Button("OK") {} }
        .task(id: titles.map(\.id)) {
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(8))
                if !hovering, !opening, titles.count > 1 {
                    withAnimation(.easeInOut(duration: 0.8)) { index = (index + 1) % titles.count }
                }
            }
        }
    }

    @ViewBuilder
    private var details: some View {
        let label = title.mediaType == "movie" ? Label("Details", systemImage: "info.circle") : Label("Watch Now", systemImage: "play.fill")
        let style = title.mediaType == "movie" ? AnyButtonStyle(.glassCapsule) : AnyButtonStyle(.primaryCapsule)
        if let item = title.item {
            NavigationLink(value: item) { label }.buttonStyle(style)
        } else {
            NavigationLink(value: StreamTitleRoute(tmdbId: title.tmdbId, mediaType: title.mediaType, title: title.title)) { label }
                .buttonStyle(style)
        }
    }

    /// Adds the movie to the streaming library if needed, then plays it.
    private func playTitle() async {
        if let item = title.item { return play(item) }
        opening = true
        defer { opening = false }
        do {
            if let item = try await state.api?.addStreamTitle(tmdbId: title.tmdbId, mediaType: title.mediaType) {
                play(item)
            }
        } catch {
            notice = error.localizedDescription
        }
    }

    private var metaLine: String {
        var parts: [String] = []
        if let y = title.year { parts.append(String(y)) }
        parts.append(title.mediaType == "show" ? "Series" : "Film")
        if let r = title.rating, r > 0 { parts.append(String(format: "★ %.1f", r)) }
        return parts.joined(separator: " · ")
    }

    private var dots: some View {
        HStack(spacing: 7) {
            ForEach(titles.indices, id: \.self) { i in
                Button {
                    withAnimation(.easeInOut(duration: 0.6)) { index = i }
                } label: {
                    Capsule()
                        .fill(.white.opacity(i == index ? 0.95 : 0.35))
                        .frame(width: i == index ? 18 : 7, height: 7)
                }
                .buttonStyle(.plain)
                .help(titles[i].title)
            }
        }
        .padding(.top, 10)
    }
}
