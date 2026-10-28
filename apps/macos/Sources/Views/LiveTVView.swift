import SwiftUI

/// Which channel to open in a live player window, and the channels its
/// channel up and down step through.
struct LiveTarget: Codable, Hashable {
    var channelId: String
    var group: String?
    /// The guide's channels in order, as filtered when the window opened.
    var channelIds: [String]? = nil
}

/// Live TV as an overview or a guide. The picked card or programme fills the
/// top with its details and a muted picture of its channel. The overview has
/// shelves of live and upcoming games with the teams' logos, followed shows,
/// favorite and recent channels, and what's on by kind; the guide has filters
/// (favorites, recent, kinds of channel, the provider's groups) and a grid of
/// what's on per channel with a now line. A search looks across channels and
/// shows from either. Clicking picks; clicking what's on again watches it.
struct LiveTVView: View {
    @Environment(AppState.self) private var state
    @Environment(\.openWindow) private var openWindow
    @State private var guide: [String: [LiveProgram]] = [:]
    @State private var guideUntil = Date()
    @State private var loaded = false
    @State private var error: String?
    @State private var query = ""
    @AppStorage("liveFilter") private var filter = "all"
    @State private var pick: Pick?
    @State private var preview = LivePreview()
    /// "overview" or "guide".
    @AppStorage("liveView") private var view = "overview"
    @State private var shelves: [LiveShelf]?
    @State private var overviewError: String?
    @State private var pickTile: LiveTile?
    /// The left edge of the grid; paged half an hour at a time.
    @State private var windowStart = LiveTVView.halfHour(Date())
    @State private var now = Date()

    struct Pick: Equatable {
        var channel: String
        var start: Int64?
    }

    static let span: TimeInterval = 2 * 3600
    static let channelColumn: CGFloat = 240
    static let rowHeight: CGFloat = 64

    static func halfHour(_ d: Date) -> Date {
        let t = d.timeIntervalSince1970
        return Date(timeIntervalSince1970: t - t.truncatingRemainder(dividingBy: 1800))
    }

    private var channels: [LiveChannel] { state.liveChannels }

    @AppStorage("liveProvider") private var provider = ""
    @State private var providers: [LiveProvider] = []
    /// With more than one provider, the one the guide shows; nil for all of them.
    private var chosenProvider: LiveProvider? { providers.count > 1 ? providers.first { $0.id == provider } : nil }
    /// The chosen provider's channels, or every channel.
    private var mine: [LiveChannel] {
        guard let p = chosenProvider else { return channels }
        return channels.filter { $0.source == p.id }
    }

    /// Shows one provider's channels ("" for all). A group belongs to one provider's playlist.
    private func pickProvider(_ id: String) {
        provider = id
        if filter.hasPrefix("group:") { filter = "all" }
    }

    private var groups: [String] {
        var seen = Set<String>(), out: [String] = []
        for c in mine { if let g = c.group, !g.isEmpty, seen.insert(g).inserted { out.append(g) } }
        return out
    }

    private var genres: [LiveGenre] {
        let have = Set(mine.compactMap(\.genre))
        return LiveGenre.allCases.filter { have.contains($0.rawValue) }
    }

    private var searching: Bool { !query.trimmingCharacters(in: .whitespaces).isEmpty }

    private var shown: [LiveChannel] {
        let q = query.trimmingCharacters(in: .whitespaces)
        // A search looks across every channel, whatever filter is picked.
        if !q.isEmpty {
            return channels.filter { c in
                c.name.localizedCaseInsensitiveContains(q) || c.number == q
                    || (c.group ?? "").localizedCaseInsensitiveContains(q)
                    || programs(c).contains { $0.endDate > now && $0.title.localizedCaseInsensitiveContains(q) }
            }
        }
        switch filter {
        case "favorites": return mine.filter { state.liveFavorites.contains($0.id) }
        case "recent":
            let byId = Dictionary(mine.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
            return state.liveRecents.compactMap { byId[$0] }
        case let f where f.hasPrefix("genre:"): return mine.filter { $0.genre == f.dropFirst(6).description }
        case let f where f.hasPrefix("group:"): return mine.filter { $0.group == f.dropFirst(6).description }
        default: return mine
        }
    }

    /// The overview shows unless the guide is picked or there's a search.
    private var overview: Bool { view == "overview" && !searching }

    private func channel(_ t: LiveTile) -> LiveChannel? { channels.first { $0.id == t.channelId } }

    /// The overview's picked card, or its first.
    private var heroTile: LiveTile? {
        pickTile ?? shelves?.lazy.flatMap(\.tiles).first { channel($0) != nil }
    }

    /// The channel playing muted at the top: a card coming up has no picture yet.
    private var previewing: LiveChannel? {
        guard overview else { return picked?.channel }
        guard let t = heroTile, (t.program?.startDate ?? .distantPast) <= now else { return nil }
        return channel(t)
    }

    private var picked: (channel: LiveChannel, program: LiveProgram?)? {
        guard let pick, let ch = channels.first(where: { $0.id == pick.channel }) else { return nil }
        let p = pick.start.flatMap { s in programs(ch).first { $0.start == s } } ?? program(on: ch, at: now)
        return (ch, p)
    }

    var body: some View {
        VStack(spacing: 0) {
            if !loaded {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if let error, channels.isEmpty {
                ContentUnavailableView("Couldn't load", systemImage: "exclamationmark.triangle", description: Text(error))
                    .frame(maxHeight: .infinity)
            } else if channels.isEmpty {
                ContentUnavailableView("No channels", systemImage: "antenna.radiowaves.left.and.right",
                                       description: Text(state.isAdmin ? "Add an IPTV playlist on the server's web dashboard under Settings → Live TV."
                                                         : "The server owner hasn't added any channels yet."))
                    .frame(maxHeight: .infinity)
            } else if overview {
                if let t = heroTile, let ch = channel(t) { tileHero(t, ch) }
                header
                Divider()
                overviewShelves
            } else {
                hero
                header
                Divider()
                if shown.isEmpty {
                    if searching {
                        ContentUnavailableView.search(text: query).frame(maxHeight: .infinity)
                    } else {
                        ContentUnavailableView(filter == "favorites" ? "No favorites yet" : "No channels here",
                                               systemImage: filter == "favorites" ? "star" : "tv",
                                               description: Text(filter == "favorites" ? "Star a channel in the guide to keep it here." : ""))
                            .frame(maxHeight: .infinity)
                    }
                } else {
                    grid
                }
            }
        }
        .navigationTitle("Live TV")
        .navigationSubtitle(loaded && !channels.isEmpty && !overview ? "\(shown.count) channel\(shown.count == 1 ? "" : "s")" : "")
        .toolbar {
            ToolbarItem {
                Picker("View", selection: $view) {
                    Text("Overview").tag("overview")
                    Text("Guide").tag("guide")
                }
                .pickerStyle(.segmented)
                .help("Show the overview or the guide")
            }
        }
        .task(id: state.refreshTick) { await load() }
        .task(id: "\(view)|\(state.refreshTick)|\(state.liveShows.count)|\(state.liveFavorites.count)|\(state.liveRecents.first ?? "")") {
            guard view == "overview" else { return }
            while !Task.isCancelled {
                await loadOverview()
                try? await Task.sleep(for: .seconds(120))
            }
        }
        .task {
            // Keep the now line moving and the guide fresh.
            var ticks = 0
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(30))
                now = Date()
                if windowStart.addingTimeInterval(Self.span) < now { windowStart = Self.halfHour(now) }
                ticks += 1
                if ticks % 10 == 0 { await load() }
            }
        }
        .onChange(of: previewing?.id) { _, _ in preview.show(previewing, api: state.api) }
        .onDisappear { preview.stop() }
    }

    // MARK: Hero

    @ViewBuilder
    private var hero: some View {
        if let (ch, p) = picked {
            HStack(alignment: .top, spacing: 24) {
                ZStack {
                    LiveArt(program: p, channel: ch, logo: state.api?.liveLogoURL(ch.logo))
                    if preview.channelId == ch.id {
                        LivePreviewSurface(player: preview.player)
                            .opacity(preview.showing ? 1 : 0)
                            .animation(.easeOut(duration: 0.4), value: preview.showing)
                    }
                    LinearGradient(colors: [.clear, .black.opacity(0.6)], startPoint: .center, endPoint: .bottom)
                    VStack {
                        Spacer()
                        HStack {
                            LiveBadge(text: "LIVE", color: .red)
                            Spacer()
                            Button { play(ch) } label: { Image(systemName: "arrow.up.left.and.arrow.down.right") }
                                .buttonStyle(.iconCircle)
                                .help("Watch")
                        }
                        .padding(10)
                    }
                }
                .aspectRatio(16 / 9, contentMode: .fit)
                .frame(width: 400)
                .clipShape(RoundedRectangle(cornerRadius: 12))
                .shadow(color: .black.opacity(0.4), radius: 14, y: 8)
                .onTapGesture(count: 2) { play(ch) }

                VStack(alignment: .leading, spacing: 8) {
                    HStack(spacing: 10) {
                        ChannelLogo(url: state.api?.liveLogoURL(ch.logo), name: ch.name).frame(width: 52, height: 34)
                        VStack(alignment: .leading, spacing: 0) {
                            Text(ch.name).font(.headline)
                            if let n = ch.number { Text("Channel \(n)").font(.caption).foregroundStyle(.secondary) }
                        }
                    }
                    Text(p?.title ?? ch.name).font(.title.bold()).lineLimit(2)
                    if let p {
                        HStack(spacing: 8) {
                            if p.isLive { LiveBadge(text: "LIVE", color: .red) }
                            if p.isNew { LiveBadge(text: "NEW", color: .white.opacity(0.22)) }
                            Text(timeLine(p)).foregroundStyle(.secondary)
                            if let e = p.episode { Text(e).foregroundStyle(.secondary) }
                            ForEach((p.categories ?? []).prefix(3), id: \.self) { c in
                                Text(c.capitalized).font(.caption)
                                    .padding(.horizontal, 8).padding(.vertical, 2)
                                    .overlay(Capsule().stroke(.white.opacity(0.2)))
                            }
                        }
                        .font(.callout)
                        if p.startDate <= now && now < p.endDate {
                            ProgressView(value: p.progress(at: now)).tint(.wrenEmber).frame(maxWidth: 360)
                        }
                        if let d = p.description {
                            Text(d).foregroundStyle(.secondary).lineLimit(3).frame(maxWidth: 560, alignment: .leading)
                        }
                    }
                    HStack(spacing: 10) {
                        Button { play(ch) } label: { Label("Watch live", systemImage: "play.fill") }
                            .buttonStyle(.borderedProminent).tint(.wrenEmber)
                        favoriteButton(ch)
                        if let p { followButton(p.title) }
                    }
                    .padding(.top, 4)
                    if let p, let next = programs(ch).first(where: { $0.start >= p.end }) {
                        Text("Up next · \(next.startDate.formatted(date: .omitted, time: .shortened))  \(next.title)")
                            .font(.callout).foregroundStyle(.secondary).lineLimit(1)
                    }
                }
                Spacer(minLength: 0)
            }
            .padding(.horizontal, 20)
            .padding(.top, 16)
            .padding(.bottom, 6)
        }
    }

    private func favoriteButton(_ ch: LiveChannel) -> some View {
        let on = state.liveFavorites.contains(ch.id)
        return Button { toggleFavorite(ch) } label: {
            Label(on ? "Favorite" : "Add to favorites", systemImage: on ? "star.fill" : "star")
        }
        .help(on ? "Remove from favorites" : "Add to favorites")
    }

    private func followButton(_ title: String) -> some View {
        let on = state.followsShow(title)
        return Button { state.toggleLiveShow(title) } label: {
            Label(on ? "Following" : "Follow show", systemImage: on ? "checkmark" : "plus")
        }
        .help(on ? "Stop following \(title)" : "Gather \(title) under Your shows")
    }

    private func timeLine(_ p: LiveProgram) -> String {
        if p.startDate <= now && now < p.endDate {
            return "\(p.timeRange) · \(Int(p.endDate.timeIntervalSince(now) / 60)) min left"
        }
        if !Calendar.current.isDateInToday(p.startDate) {
            return "\(p.startDate.formatted(.dateTime.weekday(.wide))) \(p.timeRange)"
        }
        return p.timeRange
    }

    // MARK: Header

    private var header: some View {
        HStack(spacing: 10) {
            if overview {
                Spacer()
            } else {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 8) {
                    if providers.count > 1 {
                        Menu {
                            Button("All providers") { pickProvider("") }
                            ForEach(providers) { p in Button(p.name) { pickProvider(p.id) } }
                        } label: {
                            Text(chosenProvider?.name ?? "All providers")
                        }
                        .menuStyle(.borderlessButton)
                        .fixedSize()
                    }
                    Chip("All channels", selected: filter == "all" && !searching) { set("all") }
                    Chip("★ Favorites", selected: filter == "favorites" && !searching) { set("favorites") }
                    if !state.liveRecents.isEmpty {
                        Chip("Recent", selected: filter == "recent" && !searching) { set("recent") }
                    }
                    ForEach(genres) { g in Chip(g.name, selected: filter == "genre:\(g.rawValue)" && !searching) { set("genre:\(g.rawValue)") } }
                    if groups.count > 1 {
                        Menu {
                            ForEach(groups, id: \.self) { g in Button(g) { set("group:\(g)") } }
                        } label: {
                            Text(filter.hasPrefix("group:") ? String(filter.dropFirst(6)) : "Groups")
                        }
                        .menuStyle(.borderlessButton)
                        .fixedSize()
                    }
                }
            }
            }
            HStack(spacing: 6) {
                Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
                TextField("Search channels and shows", text: $query).textFieldStyle(.plain)
                if !query.isEmpty {
                    Button { query = "" } label: { Image(systemName: "xmark.circle.fill").foregroundStyle(.secondary) }
                        .buttonStyle(.plain)
                }
            }
            .padding(.horizontal, 10)
            .frame(width: 260, height: 28)
            .background(.white.opacity(0.07), in: Capsule())
            if !overview {
            Button { page(-1) } label: { Image(systemName: "chevron.left") }
                .help("Earlier")
                .disabled(windowStart <= Self.halfHour(now))
            Button("Now") { windowStart = Self.halfHour(now) }
                .disabled(windowStart == Self.halfHour(now))
            Button { page(1) } label: { Image(systemName: "chevron.right") }
                .help("Later")
            }
        }
        .padding(.horizontal, 20)
        .padding(.vertical, 10)
    }

    private func set(_ f: String) {
        query = ""
        filter = f
    }

    private func page(_ dir: Int) {
        let next = windowStart.addingTimeInterval(Double(dir) * 1800)
        windowStart = max(Self.halfHour(now), next)
        if windowStart.addingTimeInterval(Self.span) > guideUntil { Task { await loadMore() } }
    }

    // MARK: Grid

    private var grid: some View {
        GeometryReader { geo in
            let width = max(geo.size.width - Self.channelColumn, 200)
            ScrollView {
                LazyVStack(spacing: 0, pinnedViews: [.sectionHeaders]) {
                    Section {
                        ForEach(shown) { ch in
                            row(ch, width: width)
                            Divider().opacity(0.4)
                        }
                    } header: {
                        ruler(width: width)
                    }
                }
                .overlay(alignment: .topLeading) { nowLine(width: width) }
            }
        }
    }

    private func x(_ d: Date, _ width: CGFloat) -> CGFloat {
        CGFloat(d.timeIntervalSince(windowStart) / Self.span) * width
    }

    private func ruler(width: CGFloat) -> some View {
        HStack(spacing: 0) {
            Text(Calendar.current.isDateInToday(windowStart) ? "Today" : windowStart.formatted(.dateTime.weekday(.wide).month().day()))
                .font(.caption.bold()).foregroundStyle(.secondary)
                .frame(width: Self.channelColumn - 20, alignment: .leading)
                .padding(.leading, 20)
            ZStack(alignment: .topLeading) {
                ForEach(0..<4, id: \.self) { i in
                    let t = windowStart.addingTimeInterval(Double(i) * 1800)
                    Text(t.formatted(date: .omitted, time: .shortened))
                        .font(.caption).foregroundStyle(.secondary)
                        .offset(x: x(t, width) + 8)
                }
            }
            .frame(width: width, alignment: .leading)
        }
        .frame(height: 30)
        .background(.bar)
    }

    @ViewBuilder
    private func nowLine(width: CGFloat) -> some View {
        let px = x(now, width)
        if px >= 0 && px <= width {
            Rectangle().fill(Color.wrenEmber)
                .frame(width: 2)
                .frame(maxHeight: .infinity)
                .shadow(color: .wrenEmber.opacity(0.7), radius: 4)
                .offset(x: Self.channelColumn + px)
                .allowsHitTesting(false)
        }
    }

    private func row(_ ch: LiveChannel, width: CGFloat) -> some View {
        HStack(spacing: 0) {
            channelCell(ch)
            ZStack(alignment: .topLeading) {
                let programs = visiblePrograms(ch)
                if programs.isEmpty {
                    programBlock(ch, nil, x: 0, w: width)
                } else {
                    ForEach(programs, id: \.start) { p in
                        let x0 = max(0, x(p.startDate, width))
                        let x1 = min(width, x(p.endDate, width))
                        programBlock(ch, p, x: x0, w: max(x1 - x0, 1))
                    }
                }
            }
            .frame(width: width, height: Self.rowHeight, alignment: .topLeading)
            .clipped()
        }
        .frame(height: Self.rowHeight)
        .background(pick?.channel == ch.id ? Color.white.opacity(0.04) : .clear)
        .contextMenu {
            Button("Watch \(ch.name)") { play(ch) }
            Button(state.liveFavorites.contains(ch.id) ? "Remove from Favorites" : "Add to Favorites") { toggleFavorite(ch) }
            if let p = program(on: ch, at: now) {
                Button(state.followsShow(p.title) ? "Stop Following “\(p.title)”" : "Follow “\(p.title)”") { state.toggleLiveShow(p.title) }
            }
        }
    }

    private func channelCell(_ ch: LiveChannel) -> some View {
        let fav = state.liveFavorites.contains(ch.id)
        return HStack(spacing: 10) {
            Button { play(ch) } label: {
                HStack(spacing: 10) {
                    ChannelLogo(url: state.api?.liveLogoURL(ch.logo), name: ch.name)
                        .frame(width: 56, height: 36)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(ch.name).font(.callout.weight(.semibold)).lineLimit(1)
                        if let n = ch.number { Text(n).font(.caption).foregroundStyle(.secondary) }
                    }
                    Spacer(minLength: 0)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("Watch \(ch.name)")
            Button { toggleFavorite(ch) } label: {
                Image(systemName: fav ? "star.fill" : "star")
                    .foregroundStyle(fav ? Color.yellow : Color.secondary.opacity(0.5))
            }
            .buttonStyle(.plain)
            .help(fav ? "Remove from favorites" : "Add to favorites")
        }
        .padding(.leading, 20)
        .padding(.trailing, 12)
        .frame(width: Self.channelColumn, height: Self.rowHeight)
    }

    private func programBlock(_ ch: LiveChannel, _ p: LiveProgram?, x: CGFloat, w: CGFloat) -> some View {
        let airing = p.map { $0.startDate <= now && now < $0.endDate } ?? true
        let picked = pick?.channel == ch.id && (p == nil || pick?.start == p?.start
            || (pick?.start == nil && airing))
        return Button { select(ch, p, airing: airing, picked: picked) } label: {
            ZStack(alignment: .leading) {
                if let p, airing {
                    GeometryReader { g in
                        Rectangle().fill(.white.opacity(0.06))
                            .frame(width: g.size.width * p.progress(at: now))
                    }
                }
                VStack(alignment: .leading, spacing: 2) {
                    Text(p?.title ?? ch.name).font(.callout.weight(airing ? .semibold : .regular)).lineLimit(1)
                    HStack(spacing: 5) {
                        if let p {
                            if p.isLive { LiveBadge(text: "LIVE", color: .red, small: true) }
                            if p.isNew { LiveBadge(text: "NEW", color: .white.opacity(0.22), small: true) }
                            Text(airing ? "\(Int(p.endDate.timeIntervalSince(now) / 60)) min left"
                                 : p.episode.map { "\($0) · \(p.timeRange)" } ?? p.timeRange)
                        } else {
                            Text("No guide")
                        }
                    }
                    .font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                .padding(.horizontal, 10)
            }
            .frame(width: max(w - 4, 0), height: Self.rowHeight - 10, alignment: .leading)
            .background(airing ? Color.white.opacity(0.11) : Color.white.opacity(0.05))
            .clipShape(RoundedRectangle(cornerRadius: 7))
            .overlay(RoundedRectangle(cornerRadius: 7).stroke(picked ? Color.wrenEmber : .clear, lineWidth: 1.5))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(p?.description ?? p?.title ?? ch.name)
        .offset(x: x + 2, y: 5)
    }

    private func programs(_ ch: LiveChannel) -> [LiveProgram] {
        guide[ch.id] ?? [ch.now, ch.next].compactMap { $0 }
    }

    private func visiblePrograms(_ ch: LiveChannel) -> [LiveProgram] {
        let end = windowStart.addingTimeInterval(Self.span)
        return programs(ch).filter { $0.endDate > windowStart && $0.startDate < end }
    }

    private func program(on ch: LiveChannel, at t: Date) -> LiveProgram? {
        programs(ch).first { $0.startDate <= t && t < $0.endDate }
    }

    // MARK: Actions

    /// Picks a programme; picking what's on again watches it.
    private func select(_ ch: LiveChannel, _ p: LiveProgram?, airing: Bool, picked: Bool) {
        if picked && airing { return play(ch) }
        pick = Pick(channel: ch.id, start: airing ? nil : p?.start)
    }

    private func toggleFavorite(_ ch: LiveChannel) { state.toggleLiveFavorite(ch.id) }

    // MARK: Data

    private func load() async {
        guard let api = state.api else { return }
        do {
            async let lineup = api.liveLineup()
            async let g = api.liveGuide(from: nil, hours: 12)
            let l = try await lineup
            state.liveChannels = l.channels
            providers = l.providers ?? []
            state.liveFavorites = Set(l.favorites ?? [])
            state.liveShows = Set((l.shows ?? []).map(liveShowKey))
            state.liveRecents = l.recents ?? []
            if let g = try? await g {
                guide = Dictionary(g.map { ($0.id, $0.programs) }, uniquingKeysWith: { a, _ in a })
                guideUntil = Date().addingTimeInterval(12 * 3600)
            }
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
        now = Date()
        loaded = true
        if pick == nil, let first = state.liveRecents.first.flatMap({ id in channels.first { $0.id == id } }) ?? shown.first {
            pick = Pick(channel: first.id, start: nil)
        }
    }

    private func loadOverview() async {
        guard let api = state.api else { return }
        do {
            let s = try await api.liveOverview()
            shelves = s
            overviewError = nil
            // A refresh keeps the picked card when it's still there.
            if let t = pickTile { pickTile = s.lazy.flatMap(\.tiles).first { $0.id == t.id } }
        } catch is CancellationError {
        } catch {
            if shelves == nil {
                overviewError = (error as? WrenTVError)?.status == 404 ? "Your WrenTV server needs an update for the Live TV overview." : error.localizedDescription
            }
        }
    }

    /// Fetches the next few hours when the grid pages past what's loaded.
    private func loadMore() async {
        guard let api = state.api, let g = try? await api.liveGuide(from: guideUntil, hours: 6) else { return }
        for c in g {
            let have = guide[c.id] ?? []
            let starts = Set(have.map(\.start))
            guide[c.id] = (have + c.programs.filter { !starts.contains($0.start) }).sorted { $0.start < $1.start }
        }
        guideUntil = guideUntil.addingTimeInterval(6 * 3600)
    }

    private func play(_ ch: LiveChannel, channelIds: [String]? = nil) {
        preview.stop()
        state.liveRecents = [ch.id] + state.liveRecents.filter { $0 != ch.id }
        openWindow(id: "live", value: LiveTarget(channelId: ch.id, group: nil, channelIds: channelIds ?? (searching ? nil : shown.map(\.id))))
    }

    /// Plays a card's channel, with channel up and down stepping through its shelf.
    private func play(_ t: LiveTile, in shelf: [LiveTile]) {
        guard let ch = channel(t) else { return }
        var ids: [String] = []
        for x in shelf where !ids.contains(x.channelId) { ids.append(x.channelId) }
        play(ch, channelIds: ids)
    }

    // MARK: Overview

    private func tileHero(_ t: LiveTile, _ ch: LiveChannel) -> some View {
        let p = t.program
        let upcoming = p.map { $0.startDate > now } ?? false
        return HStack(alignment: .top, spacing: 24) {
            ZStack {
                LiveTileArt(tile: t, channel: ch, big: true)
                if preview.channelId == ch.id {
                    LivePreviewSurface(player: preview.player)
                        .opacity(preview.showing ? 1 : 0)
                        .animation(.easeOut(duration: 0.4), value: preview.showing)
                }
            }
            .aspectRatio(16 / 9, contentMode: .fit)
            .frame(width: 400)
            .clipShape(RoundedRectangle(cornerRadius: 12))
            .shadow(color: .black.opacity(0.4), radius: 14, y: 8)
            .onTapGesture(count: 2) { play(ch) }

            VStack(alignment: .leading, spacing: 8) {
                HStack(spacing: 10) {
                    ChannelLogo(url: state.api?.liveLogoURL(ch.logo), name: ch.name).frame(width: 52, height: 34)
                    VStack(alignment: .leading, spacing: 0) {
                        Text(ch.name).font(.headline)
                        if let n = ch.number { Text("Channel \(n)").font(.caption).foregroundStyle(.secondary) }
                    }
                }
                Text(t.matchup?.title() ?? p?.title ?? ch.name).font(.title.bold()).lineLimit(2)
                if let p {
                    HStack(spacing: 8) {
                        TileBadges(tile: t, now: now)
                        Text(liveTiming(p, now: now)).foregroundStyle(.secondary)
                        if let e = p.episode { Text(e).foregroundStyle(.secondary) }
                    }
                    .font(.callout)
                    if !upcoming { ProgressView(value: p.progress(at: now)).tint(.wrenEmber).frame(maxWidth: 360) }
                    if let d = p.description {
                        Text(d).foregroundStyle(.secondary).lineLimit(3).frame(maxWidth: 560, alignment: .leading)
                    }
                }
                HStack(spacing: 10) {
                    Button { play(ch) } label: { Label(upcoming ? "Watch \(ch.name) now" : "Watch live", systemImage: "play.fill") }
                        .buttonStyle(.borderedProminent).tint(.wrenEmber)
                    favoriteButton(ch)
                    if let p, t.matchup == nil { followButton(p.title) }
                }
                .padding(.top, 4)
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 20)
        .padding(.top, 16)
        .padding(.bottom, 6)
    }

    @ViewBuilder
    private var overviewShelves: some View {
        if let shelves {
            if shelves.isEmpty {
                ContentUnavailableView("Nothing to show yet", systemImage: "sportscourt",
                                       description: Text("Once the guide has loaded, games, your shows and what's on gather here."))
                    .frame(maxHeight: .infinity)
            } else {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 22) {
                        ForEach(shelves) { sh in
                            VStack(alignment: .leading, spacing: 8) {
                                Text(sh.title).font(.title3.bold()).padding(.horizontal, 20)
                                ScrollView(.horizontal, showsIndicators: false) {
                                    LazyHStack(alignment: .top, spacing: 14) {
                                        ForEach(sh.tiles) { t in
                                            if let ch = channel(t) { tileCard(t, ch, shelf: sh.tiles) }
                                        }
                                    }
                                    .padding(.horizontal, 20)
                                }
                            }
                        }
                    }
                    .padding(.vertical, 16)
                }
            }
        } else if let overviewError {
            ContentUnavailableView("No overview", systemImage: "exclamationmark.triangle", description: Text(overviewError))
                .frame(maxHeight: .infinity)
        } else {
            ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    /// A card on a shelf: clicking picks it into the top, clicking it again
    /// (or double-clicking) watches it once it's on.
    private func tileCard(_ t: LiveTile, _ ch: LiveChannel, shelf: [LiveTile]) -> some View {
        let p = t.program
        let airing = p.map { $0.startDate <= now && now < $0.endDate } ?? false
        let selected = heroTile?.id == t.id
        return VStack(alignment: .leading, spacing: 4) {
            LiveTileArt(tile: t, channel: ch, big: false)
                .frame(width: 256, height: 144)
                .overlay(alignment: .topLeading) { TileBadges(tile: t, now: now).padding(8) }
                .overlay(alignment: .bottom) {
                    if airing, let p {
                        GeometryReader { g in
                            ZStack(alignment: .leading) {
                                Rectangle().fill(.white.opacity(0.2))
                                Rectangle().fill(Color.wrenEmber).frame(width: g.size.width * p.progress(at: now))
                            }
                        }
                        .frame(height: 3)
                    }
                }
                .clipShape(RoundedRectangle(cornerRadius: 10))
                .overlay(RoundedRectangle(cornerRadius: 10).stroke(selected ? Color.wrenEmber : .white.opacity(0.08), lineWidth: selected ? 2 : 1))
            Text(t.matchup?.title(short: true) ?? p?.title ?? ch.name).font(.callout.weight(.semibold)).lineLimit(1)
            Text(airing ? "\(ch.name) · \(Int(p!.endDate.timeIntervalSince(now) / 60)) min left" : p != nil ? ch.name : ch.group ?? "")
                .font(.caption).foregroundStyle(.secondary).lineLimit(1)
        }
        .frame(width: 256)
        .contentShape(Rectangle())
        .onTapGesture(count: 2) { if airing { play(t, in: shelf) } }
        .onTapGesture {
            if selected && airing { play(t, in: shelf) } else { pickTile = t }
        }
        .help(p?.description ?? p?.title ?? ch.name)
        .contextMenu {
            Button(airing ? "Watch" : "Watch \(ch.name) Now") { play(t, in: shelf) }
            Button(state.liveFavorites.contains(ch.id) ? "Remove Channel from Favorites" : "Add Channel to Favorites") { toggleFavorite(ch) }
            if let p, t.matchup == nil {
                Button(state.followsShow(p.title) ? "Stop Following “\(p.title)”" : "Follow “\(p.title)”") { state.toggleLiveShow(p.title) }
            }
        }
    }
}

/// "7:00 PM", "Tomorrow 7:00 PM" or "Sat 7:00 PM".
func liveStartsAt(_ d: Date) -> String {
    let t = d.formatted(date: .omitted, time: .shortened)
    if Calendar.current.isDateInToday(d) { return t }
    if Calendar.current.isDateInTomorrow(d) { return "Tomorrow \(t)" }
    return "\(d.formatted(.dateTime.weekday(.abbreviated))) \(t)"
}

/// When a programme is on, said for where it is in time.
func liveTiming(_ p: LiveProgram, now: Date) -> String {
    if p.startDate > now { return "\(liveStartsAt(p.startDate)) – \(p.endDate.formatted(date: .omitted, time: .shortened))" }
    return "\(p.timeRange) · \(max(0, Int(p.endDate.timeIntervalSince(now) / 60))) min left"
}

/// A tile's tags: its start time when it's coming up, else LIVE and NEW; and the league.
struct TileBadges: View {
    let tile: LiveTile
    let now: Date

    var body: some View {
        HStack(spacing: 5) {
            if let p = tile.program {
                if p.startDate > now {
                    LiveBadge(text: liveStartsAt(p.startDate), color: .black.opacity(0.75))
                } else {
                    if p.isLive { LiveBadge(text: "LIVE", color: .red) }
                    if p.isNew { LiveBadge(text: "NEW", color: .white.opacity(0.22)) }
                }
            }
            if let l = tile.matchup?.league, !l.isEmpty { LiveBadge(text: l.uppercased(), color: .black.opacity(0.55)) }
        }
    }
}

/// A tile's picture: the matchup, the programme's art over its channel's
/// logo, or the logo alone.
struct LiveTileArt: View {
    @Environment(AppState.self) private var state
    let tile: LiveTile
    let channel: LiveChannel
    let big: Bool

    var body: some View {
        let logo = state.api?.liveLogoURL(channel.logo)
        let art = state.api?.liveLogoURL(tile.art ?? tile.program?.icon)
        ZStack {
            if let m = tile.matchup {
                MatchupArt(matchup: m, big: big)
            } else {
                LiveArt(program: nil, channel: channel, logo: logo)
                if let art {
                    AsyncImage(url: art) { phase in
                        if let image = phase.image { image.resizable().aspectRatio(contentMode: .fill) }
                    }
                }
            }
        }
        .clipped()
        .overlay(alignment: .bottomTrailing) {
            if tile.matchup != nil || art != nil {
                // Which channel it's on, like a network's bug.
                ChannelLogo(url: logo, name: channel.name)
                    .frame(width: big ? 64 : 44, height: big ? 34 : 24)
                    .padding(4)
                    .background(.black.opacity(0.6), in: RoundedRectangle(cornerRadius: 6))
                    .padding(8)
            }
        }
    }
}

/// Two teams over their colours, like a scoreboard.
struct MatchupArt: View {
    let matchup: LiveMatchup
    let big: Bool

    var body: some View {
        ZStack {
            LinearGradient(colors: [Color(hex: matchup.away.color) ?? Color(red: 0.17, green: 0.16, blue: 0.2),
                                    Color(hex: matchup.home.color) ?? Color(red: 0.23, green: 0.21, blue: 0.26)],
                           startPoint: .topLeading, endPoint: .bottomTrailing)
            // Darkened, so white logos and names read on any team's colour.
            Color.black.opacity(0.32)
            HStack {
                Spacer(minLength: 0)
                TeamMark(team: matchup.away, size: big ? 84 : 52)
                Spacer(minLength: 0)
                Text("@").font(big ? .title2 : .headline).foregroundStyle(.white.opacity(0.8))
                Spacer(minLength: 0)
                TeamMark(team: matchup.home, size: big ? 84 : 52)
                Spacer(minLength: 0)
            }
        }
    }
}

private struct TeamMark: View {
    let team: LiveTeam
    let size: CGFloat

    var body: some View {
        VStack(spacing: 4) {
            if let logo = team.logo, let url = URL(string: logo) {
                AsyncImage(url: url) { phase in
                    if let image = phase.image { image.resizable().aspectRatio(contentMode: .fit) } else { initials }
                }
                .frame(width: size, height: size)
            } else {
                initials
            }
            Text(team.short ?? team.name).font(.caption.bold()).foregroundStyle(.white).lineLimit(1)
        }
        .frame(width: size * 1.7)
    }

    private var initials: some View {
        Text(String(team.name.split(separator: " ").prefix(2).compactMap(\.first)).uppercased())
            .font(.headline).foregroundStyle(.white)
            .frame(width: size, height: size)
            .background(.white.opacity(0.16), in: Circle())
    }
}

/// The programme's artwork, or the channel's logo large on a soft backdrop.
struct LiveArt: View {
    let program: LiveProgram?
    let channel: LiveChannel
    let logo: URL?

    var body: some View {
        ZStack {
            RadialGradient(colors: [Color(white: 0.2), Color(white: 0.06)], center: .topLeading, startRadius: 10, endRadius: 500)
            if let icon = program?.icon, let url = URL(string: icon) {
                AsyncImage(url: url) { phase in
                    if let image = phase.image { image.resizable().aspectRatio(contentMode: .fill) } else { logoView }
                }
            } else {
                logoView
            }
        }
        .clipped()
    }

    private var logoView: some View {
        ChannelLogo(url: logo, name: channel.name)
            .frame(width: 140, height: 80)
    }
}

/// A small filled tag: LIVE, NEW.
struct LiveBadge: View {
    let text: String
    let color: Color
    var small = false

    var body: some View {
        Text(text)
            .font(.system(size: small ? 9 : 10, weight: .heavy))
            .tracking(0.6)
            .foregroundStyle(.white)
            .padding(.horizontal, small ? 4 : 6)
            .padding(.vertical, 1)
            .background(color, in: RoundedRectangle(cornerRadius: 3))
    }
}

/// A channel's logo, or its initials when it has none.
struct ChannelLogo: View {
    let url: URL?
    let name: String

    var body: some View {
        AsyncImage(url: url) { phase in
            if let image = phase.image {
                image.resizable().aspectRatio(contentMode: .fit)
            } else {
                Text(initials).font(.caption.bold()).foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(.white.opacity(0.06), in: RoundedRectangle(cornerRadius: 6))
            }
        }
    }

    private var initials: String {
        String(name.split(separator: " ").prefix(2).compactMap(\.first)).uppercased()
    }
}
