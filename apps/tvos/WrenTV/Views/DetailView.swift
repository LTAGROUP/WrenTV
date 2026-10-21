import SwiftUI

/// A movie, show, season or episode: artwork behind, what it is, Play, and
/// for shows the seasons and their episodes.
struct DetailView: View {
    @Environment(AppState.self) private var state
    let route: ItemRoute
    @State private var detail: ItemDetail?
    @State private var extras: Extras?
    @State private var error: String?
    /// The season whose episodes are showing (shows).
    @State private var seasonId: String?
    @State private var episodes: [Item]?
    @State private var onWatchlist = false

    var body: some View {
        ZStack(alignment: .topLeading) {
            WrenBackground()
            if let d = detail {
                backdrop(d)
                ScrollView {
                    VStack(alignment: .leading, spacing: 30) {
                        header(d)
                        if d.item.kind == "show" { seasons(d) }
                        if d.item.kind == "season" { episodeShelf(d.children ?? [], title: "Episodes") }
                        if let cast = extras?.cast, !cast.isEmpty { castShelf(cast) }
                        if let more = extras?.moreLikeThis, !more.isEmpty {
                            Shelf(title: "More Like This") { ForEach(more) { TitleCard(title: $0) } }
                        }
                    }
                    .padding(.bottom, 80)
                }
            } else if let error {
                EmptyState(title: "Couldn't load", message: error, symbol: "wifi.exclamationmark")
            } else {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .ignoresSafeArea(edges: .horizontal)
        .task(id: "\(route.id)|\(state.refreshTick)") { await load() }
    }

    private func backdrop(_ d: ItemDetail) -> some View {
        let path = d.item.backdropPath ?? d.show?.backdropPath ?? d.item.stillPath
        return RemoteImage(url: state.api?.imageURL(path, size: "w1280"))
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .overlay { LinearGradient(colors: [Color.wrenInk.opacity(0.95), Color.wrenInk.opacity(0.5), .clear], startPoint: .leading, endPoint: .trailing) }
            .overlay { LinearGradient(colors: [.clear, Color.wrenInk], startPoint: .center, endPoint: .bottom) }
            .ignoresSafeArea()
    }

    // MARK: Header

    @ViewBuilder private func header(_ d: ItemDetail) -> some View {
        let item = d.item
        VStack(alignment: .leading, spacing: 22) {
            if item.kind == "episode" || item.kind == "season", let show = d.show {
                Text(show.title).font(.headline).foregroundStyle(.secondary)
            }
            if let logo = item.logoPath ?? (item.kind == "season" ? d.show?.logoPath : nil) {
                RemoteImage(url: state.api?.imageURL(logo, size: "w500"), contentMode: .fit)
                    .frame(maxWidth: 620, maxHeight: 200, alignment: .leading)
            } else {
                Text(item.displayTitle).font(.system(size: 60, weight: .bold)).lineLimit(2)
            }
            HStack(spacing: 14) {
                Text(item.metaLine).foregroundStyle(.secondary)
                ForEach(d.files?.first?.badges ?? [], id: \.self) { Badge(text: $0) }
            }
            .font(.callout)
            ratings
            if let g = item.genres, !g.isEmpty {
                Text(g.prefix(4).joined(separator: " · ")).font(.callout).foregroundStyle(.secondary)
            }
            if let tagline = item.tagline, !tagline.isEmpty {
                Text(tagline).font(.callout.italic())
            }
            if let o = item.overview, !o.isEmpty {
                Text(o).font(.callout).lineLimit(5).frame(maxWidth: 1000, alignment: .leading)
            }
            actions(d).padding(.top, 14)
        }
        .padding(.horizontal, TV.gutter)
        .padding(.top, 140)
        .frame(minHeight: 820, alignment: .bottomLeading)
        .focusSection()
    }

    @ViewBuilder private var ratings: some View {
        if let r = extras?.ratings {
            HStack(spacing: 28) {
                if let v = r.imdb { Text("IMDb \(String(format: "%.1f", v))") }
                if let v = r.rottenTomatoes { Text("🍅 \(v)%") }
                if let v = r.metacritic { Text("Metacritic \(v)") }
            }
            .font(.caption)
            .foregroundStyle(.secondary)
        }
    }

    @ViewBuilder private func actions(_ d: ItemDetail) -> some View {
        let item = d.item
        HStack(spacing: 24) {
            if item.isPlayable {
                Button { state.play(item.playTarget) } label: {
                    Label(item.inProgress ? "Resume" : "Play", systemImage: "play.fill")
                }
                if item.inProgress {
                    Button {
                        var t = item.playTarget
                        t.fromStart = true
                        state.play(t)
                    } label: { Label("From Beginning", systemImage: "backward.end.fill") }
                }
                if let files = d.files, files.count > 1 {
                    Menu {
                        ForEach(files) { f in
                            Button(f.label ?? f.badges.joined(separator: " · ")) {
                                var t = item.playTarget
                                t.fileId = f.id
                                state.play(t)
                            }
                        }
                    } label: { Label("Versions", systemImage: "square.stack") }
                }
            } else if item.kind == "show" || item.kind == "season", let next = d.nextEpisode {
                Button { state.play(next.playTarget) } label: {
                    Label(next.inProgress ? "Resume \(next.episodeLabel)" : "Play \(next.episodeLabel)", systemImage: "play.fill")
                }
            }
            if item.kind == "movie" || item.kind == "show", d.onWatchlist != nil {
                Button {
                    let on = !onWatchlist
                    onWatchlist = on
                    Task { try? await state.api?.setWatchlist(item.id, on) }
                } label: {
                    Label(onWatchlist ? "On Watchlist" : "Watchlist", systemImage: onWatchlist ? "checkmark" : "plus")
                }
            }
            Button {
                Task {
                    try? await state.api?.setWatched(item.id, !item.isWatched)
                    await load()
                    state.refreshTick += 1
                }
            } label: {
                Label(item.isWatched ? "Unwatched" : "Watched", systemImage: item.isWatched ? "eye.slash" : "checkmark.circle")
            }
            if let trailer = extras?.trailer {
                // Apple TV has no web browser; the trailer opens in the YouTube app when it's installed.
                Button { UIApplication.shared.open(URL(string: "youtube://watch/\(trailer.youtubeKey)")!) } label: {
                    Label("Trailer", systemImage: "film")
                }
            }
        }
    }

    // MARK: Seasons & episodes

    @ViewBuilder private func seasons(_ d: ItemDetail) -> some View {
        let seasons = d.children ?? []
        if !seasons.isEmpty {
            VStack(alignment: .leading, spacing: 0) {
                ScrollView(.horizontal) {
                    HStack(spacing: 20) {
                        ForEach(seasons) { s in
                            Button { selectSeason(s.id) } label: {
                                Text(s.displayTitle)
                                    .fontWeight(s.id == seasonId ? .bold : .regular)
                                    .foregroundStyle(s.id == seasonId ? Color.wrenEmber : .primary)
                            }
                            .buttonStyle(.bordered)
                        }
                    }
                    .padding(.horizontal, TV.gutter)
                    .padding(.vertical, 20)
                }
                .scrollClipDisabled()
                .focusSection()
                if let episodes {
                    episodeShelf(episodes, title: nil)
                } else {
                    ProgressView().padding(.horizontal, TV.gutter).padding(.vertical, 60)
                }
            }
        }
    }

    @ViewBuilder private func episodeShelf(_ items: [Item], title: String?) -> some View {
        if title == nil {
            ScrollView(.horizontal) {
                LazyHStack(alignment: .top, spacing: TV.shelfSpacing) {
                    ForEach(items) { LandscapeCard(item: $0, episodeList: true) }
                }
                .padding(.horizontal, TV.gutter)
                .padding(.vertical, 30)
            }
            .scrollClipDisabled()
            .scrollIndicators(.hidden)
            .focusSection()
        } else if !items.isEmpty {
            Shelf(title: title ?? "") { ForEach(items) { LandscapeCard(item: $0, episodeList: true) } }
        }
    }

    private func castShelf(_ cast: [CastMember]) -> some View {
        Shelf(title: "Cast") {
            ForEach(Array(cast.prefix(20).enumerated()), id: \.offset) { _, c in
                VStack(spacing: 12) {
                    NavigationLink(value: PersonRoute(id: c.id ?? 0, name: c.name)) {
                        RemoteImage(url: state.api?.imageURL(c.profilePath, size: "w185"))
                            .frame(width: 160, height: 160)
                            .overlay {
                                if c.profilePath == nil {
                                    Text(String(c.name.prefix(1))).font(.title.bold()).foregroundStyle(.secondary)
                                }
                            }
                            .clipShape(Circle())
                    }
                    .buttonStyle(.borderless)
                    .disabled(c.id == nil)
                    Text(c.name).font(.caption2).lineLimit(1)
                    if let ch = c.character { Text(ch).font(.caption2).foregroundStyle(.secondary).lineLimit(1) }
                }
                .frame(width: 200)
            }
        }
    }

    // MARK: Loading

    private func load() async {
        guard let api = state.api else { return }
        do {
            let d = try await api.item(route.id)
            detail = d
            onWatchlist = d.onWatchlist ?? false
            error = nil
            if d.item.kind == "show" {
                let seasons = d.children ?? []
                let pick = seasonId.flatMap { id in seasons.first { $0.id == id } }
                    ?? d.nextEpisode.flatMap { n in seasons.first { $0.id == n.parentId } }
                    ?? seasons.first { $0.seasonNumber > 0 } ?? seasons.first
                if let pick { await loadSeason(pick.id) }
            }
        } catch {
            if detail == nil { self.error = error.localizedDescription }
        }
        if extras == nil, let d = detail, d.item.kind == "movie" || d.item.kind == "show" {
            extras = try? await api.extras(route.id)
        }
    }

    private func selectSeason(_ id: String) {
        guard id != seasonId else { return }
        Task { await loadSeason(id) }
    }

    private func loadSeason(_ id: String) async {
        if seasonId != id { episodes = nil }
        seasonId = id
        if let s = try? await state.api?.item(id), seasonId == id { episodes = s.children ?? [] }
    }
}
