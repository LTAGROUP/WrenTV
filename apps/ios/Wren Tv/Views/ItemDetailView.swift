import SwiftUI

struct ItemDetailView: View {
    @Environment(AppState.self) private var state
    @Environment(\.metrics) private var m
    let itemId: String
    var seed: Item?

    @State private var detail: ItemDetail?
    @State private var seasonId: String?
    @State private var episodes: [Item] = []
    @State private var error: String?
    @State private var extras: Extras?
    @State private var extrasLoaded = false
    @State private var showTrailer = false
    /// The file to play; nil lets the server pick the best fit.
    @State private var versionId: String?
    @State private var prefs: UserPrefs?

    private var item: Item? { detail?.item ?? seed }

    var body: some View {
        ScrollView {
            if let item {
                VStack(alignment: .leading, spacing: 0) {
                    hero(item)
                    VStack(alignment: .leading, spacing: 28) {
                        if item.kind == "show", let seasons = detail?.children, !seasons.isEmpty {
                            seasonSection(seasons)
                        } else if item.kind == "season" {
                            episodeList(detail?.children ?? [])
                        }
                    }
                    .padding(.horizontal, m.gutter)
                    .padding(.top, 24)
                    discoverSection(item)
                    if let files = detail?.files, !files.isEmpty {
                        fileSection(files)
                            .padding(.horizontal, m.gutter)
                            .padding(.vertical, 24)
                    }
                }
            } else if let error {
                ContentUnavailableView("Couldn't load", systemImage: "exclamationmark.triangle", description: Text(error))
            }
        }
        .ignoresSafeArea(edges: .top)
        .navigationTitle(item?.displayTitle ?? "")
        .navigationBarTitleDisplayMode(.inline)
        .toolbarBackground(.hidden, for: .navigationBar)
        .task(id: "\(itemId)|\(state.refreshTick)") { await load() }
        .sheet(isPresented: $showTrailer) {
            if let t = extras?.trailer { TrailerSheet(trailer: t, title: item?.title ?? "") }
        }
        .task(id: itemId) {
            versionId = nil
            extras = nil
            extrasLoaded = false
            extras = try? await state.api?.extras(itemId)
            extrasLoaded = true
            if prefs == nil { prefs = try? await state.api?.prefs() }
        }
    }

    // MARK: Hero

    @ViewBuilder
    private func hero(_ item: Item) -> some View {
        let backdrop = item.backdropPath ?? detail?.show?.backdropPath ?? item.stillPath
        if m.compact {
            // Phones: the artwork on top, the details below it.
            VStack(alignment: .leading, spacing: 0) {
                RemoteImage(url: state.api?.imageURL(backdrop, size: "w780"))
                    .frame(height: 330)
                    .frame(maxWidth: .infinity)
                    .clipped()
                    .overlay(LinearGradient(colors: [.clear, .black.opacity(0.4), .black], startPoint: .center, endPoint: .bottom))
                heroInfo(item)
                    .padding(.horizontal, m.gutter)
                    .padding(.top, -64)
            }
        } else {
            ZStack(alignment: .bottomLeading) {
                RemoteImage(url: state.api?.imageURL(backdrop, size: "w1280"))
                    .frame(height: 520)
                    .frame(maxWidth: .infinity)
                    .clipped()
                    .overlay(LinearGradient(colors: [.clear, .black.opacity(0.55), .black], startPoint: .top, endPoint: .bottom))
                    .overlay(LinearGradient(colors: [.black.opacity(0.75), .clear], startPoint: .leading, endPoint: .center))
                heroInfo(item)
                    .padding(.horizontal, m.gutter)
                    .padding(.bottom, 12)
            }
        }
    }

    private func heroInfo(_ item: Item) -> some View {
            VStack(alignment: .leading, spacing: 14) {
                titleBlock(item)
                metaLine(item)
                if let r = extras?.ratings, item.kind == "movie" || item.kind == "show" {
                    scrollsOnPhone { RatingsRow(ratings: r) }
                }
                if let tagline = item.tagline, !tagline.isEmpty {
                    Text(tagline).font(.title3).italic().foregroundStyle(.secondary)
                }
                if let overview = item.overview, !overview.isEmpty {
                    Text(overview).font(m.compact ? .callout : .body).lineLimit(m.compact ? 6 : 5).frame(maxWidth: 680, alignment: .leading)
                }
                if item.isPlayable, let files = detail?.files, !files.isEmpty {
                    scrollsOnPhone { trackRow(files) }
                }
                scrollsOnPhone { actions(item) }
            }
    }

    /// Rows that fit side by side on iPad scroll sideways on a phone.
    @ViewBuilder
    private func scrollsOnPhone<C: View>(@ViewBuilder _ content: () -> C) -> some View {
        if m.compact {
            ScrollView(.horizontal, showsIndicators: false) {
                content().padding(.horizontal, m.gutter).padding(.vertical, 2)
            }
            .padding(.horizontal, -m.gutter)
        } else {
            content()
        }
    }

    @ViewBuilder
    private func titleBlock(_ item: Item) -> some View {
        let logo = item.logoPath ?? detail?.show?.logoPath
        if item.kind == "episode" || item.kind == "season" {
            if let show = detail?.show {
                NavigationLink(value: show) {
                    Text(show.title).font(.title2.weight(.semibold)).foregroundStyle(.secondary)
                }
                .buttonStyle(.plain)
            }
            Text(item.kind == "episode" ? "\(item.episodeLabel) — \(item.title)" : item.displayTitle)
                .font(.system(size: m.compact ? 26 : 34, weight: .bold))
        } else if let logo {
            RemoteImage(url: state.api?.imageURL(logo, size: "w500"), contentMode: .fit)
                .frame(maxWidth: m.compact ? 280 : 420, maxHeight: m.compact ? 110 : 150, alignment: .bottomLeading)
        } else {
            Text(item.title).font(.system(size: m.compact ? 30 : 40, weight: .bold))
        }
    }

    private func metaLine(_ item: Item) -> some View {
        var parts: [String] = []
        if let y = item.year { parts.append(String(y)) }
        if let c = item.contentRating, !c.isEmpty { parts.append(c) }
        if let r = item.runtimeMin, r > 0 { parts.append(formatDuration(Double(r * 60))) }
        if let r = item.rating, r > 0 { parts.append(String(format: "★ %.1f", r)) }
        let genres = Array((item.genres ?? []).prefix(3))
        let file = detail?.files?.first(where: { $0.id == versionId }) ?? detail?.files?.first
        return scrollsOnPhone { HStack(spacing: 10) {
            Text(parts.joined(separator: "  ·  ")).foregroundStyle(.secondary)
            if !genres.isEmpty && (item.kind == "movie" || item.kind == "show") {
                ForEach(genres, id: \.self) { g in
                    NavigationLink(value: GenreRoute(genre: g)) {
                        Text(g).font(.callout)
                            .padding(.horizontal, 9).padding(.vertical, 3)
                            .background(.white.opacity(0.08), in: Capsule())
                    }
                    .buttonStyle(.plain)
                    .help("More \(g)")
                }
            } else if !genres.isEmpty {
                Text(genres.joined(separator: ", ")).foregroundStyle(.secondary)
            }
            ForEach(file?.badges ?? [], id: \.self) { Badge(text: $0) }
        } }
    }

    @ViewBuilder
    private func actions(_ item: Item) -> some View {
        HStack(spacing: 12) {
            if item.isPlayable {
                let resume = item.inProgress ? item.progress?.position : nil
                let left = item.progress.flatMap(timeLeft)
                Button { play(item, fromStart: false) } label: {
                    Label(resume != nil ? (left.map { "Resume · \($0)" } ?? "Resume") : "Play", systemImage: "play.fill")
                }
                .buttonStyle(.primaryCapsule)
                .help(resume.map { "Resume from \(formatClock($0))" } ?? "Play")
                if resume != nil {
                    Button { play(item, fromStart: true) } label: { Label("Start Over", systemImage: "arrow.counterclockwise") }
                        .buttonStyle(.glassCapsule)
                }
            } else if item.kind == "show", let next = detail?.nextEpisode {
                Button { play(next, fromStart: false) } label: {
                    Label("\(next.inProgress ? "Resume" : "Play") \(next.episodeLabel)", systemImage: "play.fill")
                }
                .buttonStyle(.primaryCapsule)
            }
            if let t = extras?.trailer {
                Button { showTrailer = true } label: { Label("Trailer", systemImage: "play.rectangle") }
                    .buttonStyle(.glassCapsule)
                    .help(t.name)
            }
            if let listed = detail?.onWatchlist {
                WatchlistButton(item: item, listed: listed)
            }
            Button {
                Task {
                    try? await state.api?.setWatched(item.id, !item.isWatched)
                    await load()
                }
            } label: {
                Label(item.isWatched ? "Watched" : "Mark Watched", systemImage: item.isWatched ? "checkmark.circle.fill" : "checkmark.circle")
            }
            .buttonStyle(.glassCapsule)
            if item.isPlayable, detail?.canDownload == true, !(detail?.files ?? []).isEmpty || detail?.streaming == true {
                DownloadButton(item: item, show: detail?.show, fileId: versionId)
            }
            if state.isAdmin && (item.kind == "movie" || item.kind == "show") {
                AddToCollectionMenu(item: item)
            }
            if state.isAdmin, detail?.streaming == true {
                StreamAdminMenu(item: item) { Task { await load() } }
            }
        }
    }

    /// What will play: VERSION · AUDIO · SUBTITLES, each from the viewer's
    /// settings unless they pick something else.
    private func trackRow(_ files: [MediaFileView]) -> some View {
        let picked = files.first { $0.id == versionId }
        let best = picked ?? files.max { $0.width < $1.width }
        let quality = best.flatMap { $0.badges.first } ?? ""
        return HStack(spacing: 22) {
            if files.count > 1 {
                Menu {
                    Picker("Version", selection: $versionId) {
                        Text("Automatic").tag(String?.none)
                        Divider()
                        ForEach(files) { f in Text(versionName(f)).tag(Optional(f.id)) }
                    }
                    .pickerStyle(.inline)
                    .labelsHidden()
                } label: {
                    trackLabel("Version", picked.map(versionName) ?? (quality.isEmpty ? "Auto" : "Auto · \(quality)"), menu: true)
                }
                .buttonStyle(.plain)
                .fixedSize()
                .help("Choose a version to play")
            } else {
                trackLabel("Version", quality.isEmpty ? "Original" : quality)
            }
            trackLabel("Audio", languageName(prefs?.audioLanguage) ?? "Default")
            trackLabel("Subtitles", languageName(prefs?.subtitleLanguage) ?? "Off")
        }
        .help("Audio and subtitles follow your settings; change them while watching.")
    }

    private func trackLabel(_ title: String, _ value: String, menu: Bool = false) -> some View {
        HStack(spacing: 6) {
            Text(title.uppercased()).font(.system(size: 10, weight: .bold)).kerning(0.6).foregroundStyle(.white.opacity(0.55))
            Text(value).font(.callout).foregroundStyle(.white.opacity(0.9))
            if menu { Image(systemName: "chevron.down").font(.system(size: 9, weight: .bold)).foregroundStyle(.white.opacity(0.6)) }
        }
        .contentShape(Rectangle())
    }

    private func versionName(_ f: MediaFileView) -> String {
        f.label ?? (f.badges.first.map { "\($0) · " } ?? "") + ByteCountFormatter.string(fromByteCount: f.size, countStyle: .file)
    }

    private func languageName(_ code: String?) -> String? {
        guard let code, !code.isEmpty else { return nil }
        return Locale.current.localizedString(forLanguageCode: code) ?? code
    }

    // MARK: Cast & more like this

    @ViewBuilder
    private func discoverSection(_ item: Item) -> some View {
        if item.kind == "movie" || item.kind == "show" {
            VStack(alignment: .leading, spacing: 28) {
                if let ex = extras {
                    if !ex.cast.isEmpty { CastShelf(cast: ex.cast) }
                    if !ex.moreLikeThis.isEmpty {
                        Shelf(title: "More Like This") {
                            ForEach(ex.moreLikeThis) { TitleCard(title: $0) }
                        }
                    }
                    if !ex.omdb && state.isAdmin {
                        Text("Add an OMDb key in Server → Settings to show Rotten Tomatoes and IMDb ratings.")
                            .font(.caption).foregroundStyle(.secondary)
                            .padding(.horizontal, m.gutter)
                    }
                } else if !extrasLoaded {
                    SkeletonShelf(title: "More Like This")
                }
            }
            .padding(.top, 28)
            .padding(.horizontal, m.compact ? 0 : 12)
        }
    }

    // MARK: Seasons & episodes

    private func seasonSection(_ seasons: [Item]) -> some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack {
                Picker("", selection: Binding(get: { seasonId ?? seasons.first?.id }, set: { seasonId = $0 })) {
                    ForEach(seasons) { s in
                        Text(s.displayTitle).tag(Optional(s.id))
                    }
                }
                .pickerStyle(.menu)
                .fixedSize()
                .font(.title3.bold())
                Spacer()
                if detail?.canDownload == true, !episodes.isEmpty {
                    let missing = episodes.filter { state.downloads.item($0.id) == nil }
                    Button {
                        for ep in missing { state.downloads.add(ep, show: detail?.item, server: state.server?.name ?? "") }
                    } label: {
                        Label(missing.isEmpty ? "Season Downloaded" : "Download Season", systemImage: missing.isEmpty ? "checkmark.circle" : "arrow.down.circle")
                    }
                    .disabled(missing.isEmpty)
                    .help("Save every episode of this season to this device")
                }
            }
            episodeList(episodes)
        }
        .task(id: "\(seasonId ?? seasons.first?.id ?? "")|\(state.refreshTick)") {
            let target = seasons.first(where: { $0.id == seasonId }) ?? defaultSeason(seasons)
            if seasonId == nil { seasonId = target?.id }
            if let target, let d = try? await state.api?.item(target.id) {
                episodes = d.children ?? []
            }
        }
    }

    /// The season containing the next episode to watch, else the first regular season.
    private func defaultSeason(_ seasons: [Item]) -> Item? {
        if let next = detail?.nextEpisode, let s = seasons.first(where: { $0.id == next.parentId }) { return s }
        return seasons.first(where: { $0.seasonNumber > 0 }) ?? seasons.first
    }

    private func episodeList(_ eps: [Item]) -> some View {
        LazyVStack(alignment: .leading, spacing: 4) {
            ForEach(eps) { ep in
                EpisodeRow(episode: ep, offline: state.downloads.item(ep.id)) { play(ep, fromStart: false) }
                    .contextMenu {
                        NavigationLink("Episode Details", value: ep)
                        if detail?.canDownload == true {
                            if let x = state.downloads.item(ep.id) {
                                Button("Remove Download") { state.downloads.remove(x.id) }
                            } else {
                                Button("Download") { state.downloads.add(ep, show: detail?.show ?? (item?.kind == "show" ? item : nil), server: state.server?.name ?? "") }
                            }
                        }
                        ItemContextMenu(item: ep)
                    }
            }
        }
    }

    // MARK: Files

    private func fileSection(_ files: [MediaFileView]) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Media").font(.title3.bold())
            if let n = detail?.files?.first?.info?.chapters?.count, n > 0 {
                Label("\(n) chapters", systemImage: "list.bullet.rectangle").font(.caption).foregroundStyle(.secondary)
            }
            ForEach(files) { f in
                VStack(alignment: .leading, spacing: 4) {
                    Text(f.fileName).font(.callout.monospaced()).lineLimit(1).truncationMode(.middle)
                    HStack(spacing: 14) {
                        if let v = f.info?.video?.first {
                            Label("\(v.codec.uppercased()) \(v.width ?? 0)×\(v.height ?? 0)\(v.hdr.map { " \($0)" } ?? "")\(v.dvProfile.map { " P\($0)" } ?? "")", systemImage: "film")
                        }
                        if let a = f.info?.audio?.first(where: { $0.default }) ?? f.info?.audio?.first {
                            Label(a.codecLabel, systemImage: "speaker.wave.3")
                        }
                        if f.size > 0 {
                            Label(ByteCountFormatter.string(fromByteCount: f.size, countStyle: .file), systemImage: "internaldrive")
                        }
                        if f.streamId != nil, state.isAdmin {
                            Label(f.release.map { "Stream · \($0)" } ?? "Stream", systemImage: "dot.radiowaves.left.and.right")
                                .lineLimit(1)
                        }
                    }
                    .font(.caption)
                    .foregroundStyle(.secondary)
                }
            }
        }
    }

    // MARK: Actions

    private func load() async {
        do {
            detail = try await state.api?.item(itemId)
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func play(_ item: Item, fromStart: Bool) {
        let showTitle = item.kind == "episode" ? (detail?.show?.title ?? detail?.item.title ?? item.showTitle ?? "") : nil
        state.play(PlayTarget(
            itemId: item.id,
            title: showTitle ?? item.title,
            subtitle: item.kind == "episode" ? "\(item.episodeLabel) — \(item.title)" : nil,
            fromStart: fromStart,
            fileId: item.id == itemId ? versionId : nil))
    }
}

struct EpisodeRow: View {
    @Environment(AppState.self) private var state
    @Environment(\.metrics) private var m
    let episode: Item
    var offline: OfflineItem?
    let onPlay: () -> Void
    @State private var hover = false

    var body: some View {
        Button(action: onPlay) {
            HStack(alignment: .top, spacing: m.compact ? 12 : 16) {
                RemoteImage(url: state.api?.imageURL(episode.stillPath, size: "w300"))
                    .frame(width: m.compact ? 136 : 220, height: m.compact ? 77 : 124)
                    .overlay {
                        if hover {
                            Image(systemName: "play.circle.fill").font(.system(size: 38)).shadow(radius: 6)
                        }
                    }
                    .overlay(alignment: .bottom) {
                        if let p = episode.progress, p.position > 0, !p.watched { ProgressStrip(fraction: p.fraction) }
                    }
                    .clipShape(RoundedRectangle(cornerRadius: 6))
                VStack(alignment: .leading, spacing: 6) {
                    HStack {
                        Text("\(episode.episodeNumber). \(episode.title)").font(m.compact ? .subheadline.bold() : .headline).lineLimit(2)
                        if episode.isWatched {
                            Image(systemName: "checkmark.circle.fill").foregroundStyle(Color.wrenEmber)
                        }
                        if let offline {
                            Image(systemName: offline.status == .done ? "arrow.down.circle.fill" : "arrow.down.circle")
                                .foregroundStyle(.secondary)
                                .help(offline.status == .done ? "Downloaded to this device" : "Downloading")
                        }
                    }
                    HStack(spacing: 8) {
                        if let d = episode.airDate, !d.isEmpty { Text(d) }
                        if let r = episode.runtimeMin, r > 0 { Text(formatDuration(Double(r * 60))) }
                    }
                    .font(.caption).foregroundStyle(.secondary)
                    if let o = episode.overview {
                        Text(o).font(m.compact ? .caption : .callout).foregroundStyle(.secondary).lineLimit(m.compact ? 2 : 3)
                    }
                }
                Spacer(minLength: 0)
            }
            .padding(8)
            .background(hover ? Color.white.opacity(0.06) : .clear, in: RoundedRectangle(cornerRadius: 8))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hover = $0 }
    }
}

/// Picks which file to play when an item has several (4K and 1080p, say).
/// "Automatic" lets the server choose the best one that plays without converting.
/// Adds or removes the item from hand-picked collections (admins).
private struct AddToCollectionMenu: View {
    @Environment(AppState.self) private var state
    let item: Item
    @State private var collections: [WrenCollection] = []
    @State private var member: Set<String> = []

    var body: some View {
        Menu {
            let manual = collections.filter { $0.kind == "manual" }
            if manual.isEmpty {
                Text("No hand-picked collections yet")
            }
            ForEach(manual) { c in
                Toggle(c.name, isOn: Binding(get: { member.contains(c.id) }, set: { on in toggle(c, on) }))
            }
            Divider()
            Text("Create collections in the web app under Collections.")
        } label: {
            Label("Collection", systemImage: "plus.rectangle.on.rectangle")
        }
        .controlSize(.large)
        .fixedSize()
        .task(id: item.id) { await load() }
    }

    private func load() async {
        guard let api = state.api else { return }
        collections = (try? await api.collections()) ?? []
        member = Set((try? await api.itemCollections(item.id)) ?? [])
    }

    private func toggle(_ c: WrenCollection, _ on: Bool) {
        Task {
            if on { try? await state.api?.addToCollection(c.id, items: [item.id]) }
            else { try? await state.api?.removeFromCollection(c.id, item: item.id) }
            await load()
            state.refreshTick += 1
        }
    }
}
