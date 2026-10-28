import SwiftUI

/// Live TV, as an overview or a guide. The overview leads with a game or
/// show on now playing muted, then shelves: live and upcoming games with the
/// teams' logos, the viewer's followed shows and channels, and what's on by
/// kind. The guide has a spotlight on the channel last watched, filters
/// (favorites, recent, kinds of channel, the provider's groups) and what's on
/// now and next per channel. Search covers channels and upcoming shows.
/// Tapping watches; holding a card opens its options (favorite the channel,
/// follow the show), and a channel's info button its schedule.
struct LiveTVView: View {
    @Environment(AppState.self) private var state
    @Environment(\.metrics) private var m
    @State private var guide: [String: [LiveProgram]] = [:]
    @State private var loaded = false
    @State private var error: String?
    @State private var query = ""
    @AppStorage("liveFilter") private var filter = "all"
    @State private var now = Date()
    @State private var schedule: LiveChannel?
    @State private var options: LiveTile?
    @State private var results: LiveSearchResult?
    @State private var preview = LivePreview()
    /// "overview" or "guide".
    @AppStorage("liveView") private var view = "overview"
    @State private var shelves: [LiveShelf]?
    @State private var overviewError: String?

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

    /// The channel the top of the page shows.
    private var spotlight: LiveChannel? {
        state.liveRecents.lazy.compactMap { id in channels.first { $0.id == id } }.first ?? channels.first
    }

    private var overview: Bool { view == "overview" }

    private func channel(_ t: LiveTile) -> LiveChannel? { channels.first { $0.id == t.channelId } }

    /// The overview's lead: its first card that's on now.
    private var heroTile: LiveTile? {
        shelves?.lazy.flatMap(\.tiles).first { t in channel(t) != nil && (t.program.map { $0.startDate <= now } ?? true) }
    }

    /// The channel playing muted: the open sheet's, else the top of the page's.
    private var previewing: LiveChannel? {
        if state.playingLive != nil { return nil }
        if let schedule { return schedule }
        if let t = options { return (t.program?.startDate ?? .distantPast) <= now ? channel(t) : nil }
        if searching { return nil }
        return overview ? heroTile.flatMap(channel) : spotlight
    }

    var body: some View {
        Group {
            if !loaded {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if let error, channels.isEmpty {
                ContentUnavailableView("Couldn't load", systemImage: "exclamationmark.triangle", description: Text(error))
            } else if channels.isEmpty {
                ContentUnavailableView("No channels", systemImage: "antenna.radiowaves.left.and.right",
                                       description: Text(state.isAdmin ? "Add an IPTV playlist on the server's web dashboard under Settings → Live TV."
                                                         : "The server owner hasn't added any channels yet."))
            } else if searching {
                searchList
            } else if overview {
                overviewList
            } else {
                list
            }
        }
        .navigationTitle("Live TV")
        .searchable(text: $query, placement: .navigationBarDrawer(displayMode: .always), prompt: "Search channels and shows")
        .refreshable { await load() }
        .task(id: state.refreshTick) { await load() }
        .task(id: query) { await search() }
        .task {
            var ticks = 0
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(30))
                now = Date()
                ticks += 1
                if ticks % 10 == 0 { await load() }
            }
        }
        .task(id: "\(view)|\(state.refreshTick)|\(state.liveShows.count)|\(state.liveFavorites.count)|\(state.liveRecents.first ?? "")") {
            guard overview else { return }
            while !Task.isCancelled {
                await loadOverview()
                try? await Task.sleep(for: .seconds(120))
            }
        }
        // A sheet borrows the preview; the top of the page takes it back.
        .onChange(of: previewing?.id) { _, _ in
            preview.show(previewing, api: state.api, after: .milliseconds(schedule != nil || options != nil ? 300 : 700))
        }
        .onAppear { preview.show(previewing, api: state.api) }
        .onDisappear { preview.stop() }
        .sheet(item: $schedule) { ch in
            ScheduleSheet(channel: ch, programs: programs(ch), now: now, preview: preview,
                          favorite: state.liveFavorites.contains(ch.id), toggleFavorite: { toggleFavorite(ch) }) {
                schedule = nil
                play(ch)
            }
            .presentationDetents([.medium, .large])
        }
        .sheet(item: $options) { t in
            if let ch = channel(t) {
                TileOptionsSheet(tile: t, channel: ch, now: now, preview: preview,
                                 favorite: state.liveFavorites.contains(ch.id), toggleFavorite: { toggleFavorite(ch) }) {
                    options = nil
                    play(ch)
                }
                .presentationDetents([.medium, .large])
            }
        }
    }

    private var viewPicker: some View {
        Picker("View", selection: $view) {
            Text("Overview").tag("overview")
            Text("Guide").tag("guide")
        }
        .pickerStyle(.segmented)
        .frame(maxWidth: 360)
    }

    // MARK: Overview

    private var overviewList: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 18) {
                viewPicker.padding(.horizontal, m.gutter)
                if let shelves {
                    if let t = heroTile, let ch = channel(t) {
                        OverviewHero(tile: t, channel: ch, now: now, preview: preview, more: { options = t }) {
                            open(t, in: shelves.first { $0.tiles.contains(t) }?.tiles ?? [t])
                        }
                        .padding(.horizontal, m.gutter)
                    }
                    ForEach(shelves) { sh in
                        VStack(alignment: .leading, spacing: 8) {
                            Text(sh.title).font(.title3.bold()).padding(.horizontal, m.gutter)
                            ScrollView(.horizontal, showsIndicators: false) {
                                LazyHStack(alignment: .top, spacing: 12) {
                                    ForEach(sh.tiles) { t in
                                        if let ch = channel(t) {
                                            TileCard(tile: t, channel: ch, now: now)
                                                .frame(width: m.compact ? 220 : 260)
                                                .onTapGesture { open(t, in: sh.tiles) }
                                                .contextMenu { tileMenu(t, ch) }
                                        }
                                    }
                                }
                                .padding(.horizontal, m.gutter)
                            }
                        }
                    }
                    if shelves.isEmpty {
                        ContentUnavailableView("Nothing to show yet", systemImage: "sportscourt",
                                               description: Text("Once the guide has loaded, games, your shows and what's on gather here."))
                    }
                } else if let overviewError {
                    ContentUnavailableView("No overview", systemImage: "exclamationmark.triangle", description: Text(overviewError))
                } else {
                    ProgressView().frame(maxWidth: .infinity).padding(.top, 60)
                }
            }
            .padding(.vertical, 8)
        }
    }

    @ViewBuilder private func tileMenu(_ t: LiveTile, _ ch: LiveChannel) -> some View {
        let fav = state.liveFavorites.contains(ch.id)
        Button("Watch \(ch.name)", systemImage: "play.fill") { play(ch) }
        Button(fav ? "Remove Channel from Favorites" : "Add Channel to Favorites", systemImage: fav ? "star.slash" : "star") { toggleFavorite(ch) }
        if let p = t.program, t.matchup == nil {
            let following = state.followsShow(p.title)
            Button(following ? "Stop Following “\(p.title)”" : "Follow “\(p.title)”", systemImage: following ? "checkmark" : "plus") {
                state.toggleLiveShow(p.title)
            }
        }
        Button("Details", systemImage: "info.circle") { options = t }
    }

    /// A card plays when it's on now; one coming up shows its options.
    private func open(_ t: LiveTile, in shelf: [LiveTile]) {
        guard let ch = channel(t) else { return }
        if let p = t.program, p.startDate > now { options = t; return }
        var ids: [String] = []
        for x in shelf where !ids.contains(x.channelId) { ids.append(x.channelId) }
        play(ch, channelIds: ids)
    }

    private func loadOverview() async {
        guard let api = state.api else { return }
        do {
            shelves = try await api.liveOverview()
            overviewError = nil
        } catch is CancellationError {
        } catch {
            if shelves == nil {
                overviewError = (error as? WrenTVError)?.status == 404 ? "Your WrenTV server needs an update for the Live TV overview." : error.localizedDescription
            }
        }
    }

    // MARK: Guide

    private var list: some View {
        List {
            viewPicker
                .listRowInsets(EdgeInsets(top: 4, leading: m.gutter, bottom: 6, trailing: m.gutter))
                .listRowSeparator(.hidden)
                .listRowBackground(Color.clear)
            if let ch = spotlight {
                Spotlight(channel: ch, program: onNow(ch), next: next(ch), now: now, preview: preview) { play(ch) }
                    .listRowInsets(EdgeInsets(top: 4, leading: m.gutter, bottom: 10, trailing: m.gutter))
                    .listRowSeparator(.hidden)
                    .listRowBackground(Color.clear)
            }
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 8) {
                    if providers.count > 1 {
                        Menu {
                            Button("All providers") { pickProvider("") }
                            ForEach(providers) { p in Button(p.name) { pickProvider(p.id) } }
                        } label: {
                            chipLabel(chosenProvider?.name ?? "All providers", icon: "chevron.down", selected: chosenProvider != nil)
                        }
                    }
                    chip("All", selected: filter == "all") { filter = "all" }
                    chip("Favorites", icon: "star.fill", selected: filter == "favorites") { filter = "favorites" }
                    if !state.liveRecents.isEmpty {
                        chip("Recent", selected: filter == "recent") { filter = "recent" }
                    }
                    ForEach(genres) { g in chip(g.name, selected: filter == "genre:\(g.rawValue)") { filter = "genre:\(g.rawValue)" } }
                    if groups.count > 1 {
                        Menu {
                            ForEach(groups, id: \.self) { g in Button(g) { filter = "group:\(g)" } }
                        } label: {
                            chipLabel(filter.hasPrefix("group:") ? String(filter.dropFirst(6)) : "Groups", icon: "chevron.down",
                                      selected: filter.hasPrefix("group:"))
                        }
                    }
                }
                .padding(.horizontal, m.gutter)
            }
            .listRowInsets(EdgeInsets(top: 4, leading: 0, bottom: 8, trailing: 0))
            .listRowSeparator(.hidden)
            .listRowBackground(Color.clear)
            ForEach(shown) { ch in channelRow(ch) }
            if shown.isEmpty {
                ContentUnavailableView(filter == "favorites" ? "No favorites yet" : "No channels here",
                                       systemImage: filter == "favorites" ? "star" : "tv",
                                       description: Text(filter == "favorites" ? "Swipe right on a channel to add it." : ""))
                    .listRowBackground(Color.clear)
            }
        }
        .listStyle(.plain)
    }

    private func channelRow(_ ch: LiveChannel) -> some View {
        let fav = state.liveFavorites.contains(ch.id)
        return Button { play(ch) } label: { row(ch) }
            .buttonStyle(.plain)
            .swipeActions(edge: .leading) {
                Button { toggleFavorite(ch) } label: { Label(fav ? "Unfavorite" : "Favorite", systemImage: fav ? "star.slash" : "star.fill") }
                    .tint(.yellow)
            }
            .swipeActions(edge: .trailing) {
                Button { schedule = ch } label: { Label("Schedule", systemImage: "list.bullet.rectangle") }
            }
            .contextMenu {
                Button("Watch", systemImage: "play.fill") { play(ch) }
                Button("Schedule", systemImage: "list.bullet.rectangle") { schedule = ch }
                Button(fav ? "Remove from Favorites" : "Add to Favorites", systemImage: fav ? "star.slash" : "star") { toggleFavorite(ch) }
            }
    }

    private func chip(_ title: String, icon: String? = nil, selected: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) { chipLabel(title, icon: icon, selected: selected) }
            .buttonStyle(.plain)
    }

    private func chipLabel(_ title: String, icon: String?, selected: Bool) -> some View {
        HStack(spacing: 5) {
            if let icon { Image(systemName: icon).font(.caption2).foregroundStyle(selected ? Color.black : Color.yellow) }
            Text(title)
        }
        .font(.subheadline)
        .padding(.horizontal, 12).padding(.vertical, 6)
        .foregroundStyle(selected ? Color.black : Color.primary)
        .background(selected ? AnyShapeStyle(Color.white) : AnyShapeStyle(Color.white.opacity(0.1)), in: Capsule())
    }

    private func row(_ ch: LiveChannel) -> some View {
        let current = onNow(ch)
        let next = next(ch)
        return HStack(spacing: 12) {
            ChannelLogo(url: state.api?.liveLogoURL(ch.logo), name: ch.name)
                .frame(width: 64, height: 40)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    if let n = ch.number { Text(n).font(.caption.monospacedDigit()).foregroundStyle(.secondary) }
                    Text(ch.name).font(.subheadline.weight(.semibold)).lineLimit(1)
                    if state.liveFavorites.contains(ch.id) {
                        Image(systemName: "star.fill").font(.caption2).foregroundStyle(.yellow)
                    }
                }
                if let current {
                    HStack(spacing: 5) {
                        if current.isLive { LiveBadge(text: "LIVE", color: .red) }
                        if current.isNew { LiveBadge(text: "NEW", color: .white.opacity(0.22)) }
                        Text(current.title).font(.subheadline).lineLimit(1)
                    }
                    HStack(spacing: 8) {
                        ProgressView(value: current.progress(at: now)).tint(.wrenEmber).frame(maxWidth: 120)
                        Text("\(Int(current.endDate.timeIntervalSince(now) / 60)) min left").font(.caption2).foregroundStyle(.secondary).lineLimit(1)
                    }
                } else {
                    Text("No guide").font(.caption).foregroundStyle(.secondary)
                }
                if let next, !m.compact || current == nil {
                    Text("Next: \(next.title) · \(next.startDate.formatted(date: .omitted, time: .shortened))")
                        .font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
            }
            Spacer(minLength: 0)
            Button { schedule = ch } label: {
                Image(systemName: "info.circle").font(.title3).foregroundStyle(.secondary)
            }
            .buttonStyle(.borderless)
        }
        .padding(.vertical, 4)
        .contentShape(Rectangle())
    }

    // MARK: Search

    private var searchList: some View {
        List {
            if let r = results {
                if !r.channels.isEmpty {
                    Section("Channels") {
                        ForEach(r.channels.compactMap { c in channels.first { $0.id == c.id } }) { ch in channelRow(ch) }
                    }
                }
                if !r.airings.isEmpty {
                    Section("Shows") {
                        ForEach(r.airings, id: \.self) { a in
                            if let ch = channels.first(where: { $0.id == a.channelId }) { airingRow(a.program, ch) }
                        }
                    }
                }
                if r.channels.isEmpty && r.airings.isEmpty {
                    ContentUnavailableView.search(text: query).listRowBackground(Color.clear)
                }
            } else {
                ProgressView().frame(maxWidth: .infinity).listRowBackground(Color.clear)
            }
        }
        .listStyle(.plain)
    }

    private func airingRow(_ p: LiveProgram, _ ch: LiveChannel) -> some View {
        let airing = p.startDate <= now && now < p.endDate
        return Button { airing ? play(ch) : (options = LiveTile(channelId: ch.id, program: p)) } label: {
            HStack(spacing: 12) {
                LiveArt(program: p, channel: ch, logo: state.api?.liveLogoURL(ch.logo))
                    .frame(width: 96, height: 54)
                    .clipShape(RoundedRectangle(cornerRadius: 6))
                VStack(alignment: .leading, spacing: 3) {
                    Text(p.title).font(.subheadline.weight(.semibold)).lineLimit(1)
                    HStack(spacing: 5) {
                        if airing { LiveBadge(text: "ON NOW", color: .wrenEmber) }
                        if p.isNew { LiveBadge(text: "NEW", color: .white.opacity(0.22)) }
                        Text([p.episode, ch.name, airing ? nil : when(p)].compactMap { $0 }.joined(separator: " · "))
                            .font(.caption).foregroundStyle(.secondary).lineLimit(1)
                    }
                }
                Spacer(minLength: 0)
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    private func when(_ p: LiveProgram) -> String {
        Calendar.current.isDateInToday(p.startDate) ? p.startDate.formatted(date: .omitted, time: .shortened)
            : p.startDate.formatted(.dateTime.weekday(.abbreviated).hour().minute())
    }

    // MARK: Data

    private func programs(_ ch: LiveChannel) -> [LiveProgram] {
        guide[ch.id] ?? [ch.now, ch.next].compactMap { $0 }
    }

    private func onNow(_ ch: LiveChannel) -> LiveProgram? {
        programs(ch).first { $0.startDate <= now && now < $0.endDate }
    }

    private func next(_ ch: LiveChannel) -> LiveProgram? {
        programs(ch).first { $0.startDate > now }
    }

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
            }
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
        now = Date()
        loaded = true
    }

    private func search() async {
        let q = query.trimmingCharacters(in: .whitespaces)
        guard !q.isEmpty, let api = state.api else { results = nil; return }
        try? await Task.sleep(for: .milliseconds(250))
        guard !Task.isCancelled, let r = try? await api.liveSearch(q), !Task.isCancelled else { return }
        results = r
    }

    private func toggleFavorite(_ ch: LiveChannel) { state.toggleLiveFavorite(ch.id) }

    private func play(_ ch: LiveChannel, channelIds: [String]? = nil) {
        preview.stop()
        schedule = nil
        options = nil
        state.liveRecents = [ch.id] + state.liveRecents.filter { $0 != ch.id }
        state.playingLive = LiveTarget(channelId: ch.id, group: nil, channelIds: channelIds ?? (searching ? nil : shown.map(\.id)))
    }
}

/// The top of the guide: a channel's muted live picture and what's on.
private struct Spotlight: View {
    @Environment(AppState.self) private var state
    let channel: LiveChannel
    let program: LiveProgram?
    let next: LiveProgram?
    let now: Date
    let preview: LivePreview
    let watch: () -> Void

    var body: some View {
        Button(action: watch) {
            ZStack(alignment: .bottomLeading) {
                LivePicture(channel: channel, program: program, preview: preview)
                LinearGradient(colors: [.clear, .black.opacity(0.85)], startPoint: .center, endPoint: .bottom)
                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 6) {
                        LiveBadge(text: "LIVE", color: .red)
                        Text([channel.number, channel.name].compactMap { $0 }.joined(separator: " · "))
                            .font(.caption.weight(.semibold)).foregroundStyle(.white.opacity(0.85))
                    }
                    Text(program?.title ?? channel.name).font(.title3.bold()).foregroundStyle(.white).lineLimit(1)
                    if let program {
                        ProgressView(value: program.progress(at: now)).tint(.wrenEmber)
                        if let next {
                            Text("Up next · \(next.startDate.formatted(date: .omitted, time: .shortened))  \(next.title)")
                                .font(.caption).foregroundStyle(.white.opacity(0.7)).lineLimit(1)
                        }
                    }
                }
                .padding(14)
            }
            .aspectRatio(16 / 9, contentMode: .fit)
            .clipShape(RoundedRectangle(cornerRadius: 14))
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Watch \(channel.name)")
    }
}

/// A channel's muted live picture over the programme's art until it starts.
private struct LivePicture: View {
    @Environment(AppState.self) private var state
    let channel: LiveChannel
    let program: LiveProgram?
    let preview: LivePreview
    var tile: LiveTile? = nil

    var body: some View {
        ZStack {
            if let tile {
                LiveTileArt(tile: tile, channel: channel, big: true)
            } else {
                LiveArt(program: program, channel: channel, logo: state.api?.liveLogoURL(channel.logo))
            }
            if preview.channelId == channel.id {
                LivePreviewSurface(player: preview.player)
                    .opacity(preview.showing ? 1 : 0)
                    .animation(.easeOut(duration: 0.4), value: preview.showing)
            }
        }
    }
}

/// What a channel is showing over the next hours.
private struct ScheduleSheet: View {
    @Environment(AppState.self) private var state
    let channel: LiveChannel
    let programs: [LiveProgram]
    let now: Date
    let preview: LivePreview
    let favorite: Bool
    let toggleFavorite: () -> Void
    let watch: () -> Void

    var body: some View {
        NavigationStack {
            List {
                Button(action: watch) {
                    ZStack(alignment: .bottomLeading) {
                        LivePicture(channel: channel, program: programs.first { $0.startDate <= now && now < $0.endDate }, preview: preview)
                        HStack {
                            LiveBadge(text: "LIVE", color: .red)
                            Spacer()
                            Image(systemName: "play.fill").font(.title3).foregroundStyle(.white)
                                .padding(10).background(.black.opacity(0.5), in: Circle())
                        }
                        .padding(10)
                    }
                    .aspectRatio(16 / 9, contentMode: .fit)
                    .clipShape(RoundedRectangle(cornerRadius: 12))
                }
                .buttonStyle(.plain)
                .listRowSeparator(.hidden)
                if programs.isEmpty {
                    Text("This channel has no guide.").foregroundStyle(.secondary)
                }
                ForEach(programs.filter { $0.endDate > now }, id: \.start) { p in
                    let airing = p.startDate <= now
                    VStack(alignment: .leading, spacing: 4) {
                        HStack(spacing: 6) {
                            Text(p.startDate.formatted(date: .omitted, time: .shortened))
                                .font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                            if airing { LiveBadge(text: "ON NOW", color: .wrenEmber) }
                            if p.isLive { LiveBadge(text: "LIVE", color: .red) }
                            if p.isNew { LiveBadge(text: "NEW", color: .white.opacity(0.22)) }
                        }
                        HStack {
                            Text(p.title).font(.headline)
                            Spacer(minLength: 8)
                            let following = state.followsShow(p.title)
                            Button(following ? "Following" : "Follow", systemImage: following ? "checkmark" : "plus") { state.toggleLiveShow(p.title) }
                                .font(.caption.weight(.semibold)).buttonStyle(.bordered).buttonBorderShape(.capsule)
                                .tint(following ? .wrenEmber : .secondary)
                        }
                        if let e = p.episode { Text(e).font(.caption).foregroundStyle(.secondary) }
                        if let d = p.description, !d.isEmpty {
                            Text(d).font(.subheadline).foregroundStyle(.secondary).lineLimit(3)
                        }
                        if airing { ProgressView(value: p.progress(at: now)).tint(.wrenEmber) }
                    }
                    .padding(.vertical, 2)
                }
            }
            .listStyle(.plain)
            .navigationTitle(channel.name)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    ChannelLogo(url: state.api?.liveLogoURL(channel.logo), name: channel.name).frame(width: 48, height: 30)
                }
                ToolbarItemGroup(placement: .topBarTrailing) {
                    Button(favorite ? "Remove from Favorites" : "Add to Favorites", systemImage: favorite ? "star.fill" : "star", action: toggleFavorite)
                        .tint(.yellow)
                    Button("Watch", systemImage: "play.fill", action: watch)
                }
            }
        }
    }
}

// MARK: Overview pieces

/// "7:00 PM", "Tomorrow 7:00 PM" or "Sat 7:00 PM".
func liveStartsAt(_ d: Date) -> String {
    let t = d.formatted(date: .omitted, time: .shortened)
    let cal = Calendar.current
    if cal.isDateInToday(d) { return t }
    if cal.isDateInTomorrow(d) { return "Tomorrow \(t)" }
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
        HStack(spacing: 4) {
            if let p = tile.program {
                if p.startDate > now {
                    LiveBadge(text: liveStartsAt(p.startDate), color: .black.opacity(0.75))
                } else {
                    if p.isLive { LiveBadge(text: "LIVE", color: .red) }
                    if p.isNew { LiveBadge(text: "NEW", color: .white.opacity(0.22)) }
                }
            }
            if let l = tile.matchup?.league, !l.isEmpty { LiveBadge(text: l.uppercased(), color: .white.opacity(0.22)) }
        }
    }
}

/// The overview's lead: a game or show on now, playing muted.
private struct OverviewHero: View {
    let tile: LiveTile
    let channel: LiveChannel
    let now: Date
    let preview: LivePreview
    let more: () -> Void
    let watch: () -> Void

    var body: some View {
        Button(action: watch) {
            ZStack(alignment: .bottomLeading) {
                LivePicture(channel: channel, program: tile.program, preview: preview, tile: tile)
                LinearGradient(colors: [.clear, .black.opacity(0.85)], startPoint: .center, endPoint: .bottom)
                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 6) {
                        TileBadges(tile: tile, now: now)
                        Text([channel.number, channel.name].compactMap { $0 }.joined(separator: " · "))
                            .font(.caption.weight(.semibold)).foregroundStyle(.white.opacity(0.85))
                    }
                    Text(tile.matchup?.title() ?? tile.program?.title ?? channel.name).font(.title3.bold()).foregroundStyle(.white).lineLimit(1)
                    if let p = tile.program {
                        ProgressView(value: p.progress(at: now)).tint(.wrenEmber)
                        Text(liveTiming(p, now: now)).font(.caption).foregroundStyle(.white.opacity(0.7)).lineLimit(1)
                    }
                }
                .padding(14)
            }
            .aspectRatio(16 / 9, contentMode: .fit)
            .clipShape(RoundedRectangle(cornerRadius: 14))
            .overlay(alignment: .topTrailing) {
                Button(action: more) {
                    Image(systemName: "ellipsis").font(.headline).foregroundStyle(.white)
                        .frame(width: 34, height: 34).background(.black.opacity(0.5), in: Circle())
                }
                .buttonStyle(.plain)
                .padding(10)
                .accessibilityLabel("Options")
            }
        }
        .buttonStyle(.plain)
    }
}

/// A card on an overview shelf.
private struct TileCard: View {
    let tile: LiveTile
    let channel: LiveChannel
    let now: Date

    var body: some View {
        let p = tile.program
        let airing = p.map { $0.startDate <= now && now < $0.endDate } ?? false
        VStack(alignment: .leading, spacing: 2) {
            LiveTileArt(tile: tile, channel: channel, big: false)
                .aspectRatio(16 / 9, contentMode: .fit)
                .overlay(alignment: .topLeading) { TileBadges(tile: tile, now: now).padding(8) }
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
            Text(tile.matchup?.title(short: true) ?? p?.title ?? channel.name)
                .font(.subheadline.weight(.semibold)).lineLimit(1).padding(.top, 6)
            Text(airing ? "\(channel.name) · \(Int(p!.endDate.timeIntervalSince(now) / 60)) min left" : p != nil ? channel.name : channel.group ?? "")
                .font(.caption).foregroundStyle(.secondary).lineLimit(1)
        }
        .contentShape(Rectangle())
    }
}

/// A tile's picture: the matchup, the programme's art over its channel's logo, or the logo alone.
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
                    .frame(width: big ? 60 : 44, height: big ? 34 : 26)
                    .padding(3)
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
                TeamMark(team: matchup.away, size: big ? 64 : 44)
                Spacer(minLength: 0)
                Text("@").font(big ? .title2 : .headline).foregroundStyle(.white.opacity(0.8))
                Spacer(minLength: 0)
                TeamMark(team: matchup.home, size: big ? 64 : 44)
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
            Group {
                if let logo = team.logo, let url = URL(string: logo) {
                    AsyncImage(url: url) { phase in
                        if let image = phase.image { image.resizable().aspectRatio(contentMode: .fit) } else { initials }
                    }
                } else {
                    initials
                }
            }
            .frame(width: size, height: size)
            Text(team.short ?? team.name).font(.caption.bold()).foregroundStyle(.white).lineLimit(1)
        }
        .frame(width: size * 1.6)
    }

    private var initials: some View {
        Text(String(team.name.split(separator: " ").prefix(2).compactMap(\.first)).uppercased())
            .font(.headline).foregroundStyle(.white)
            .frame(width: size, height: size)
            .background(.white.opacity(0.16), in: Circle())
    }
}

/// A card's details and what can be done with it: watch, favorite the channel, follow the show.
private struct TileOptionsSheet: View {
    @Environment(AppState.self) private var state
    let tile: LiveTile
    let channel: LiveChannel
    let now: Date
    let preview: LivePreview
    let favorite: Bool
    let toggleFavorite: () -> Void
    let watch: () -> Void

    var body: some View {
        let p = tile.program
        let upcoming = p.map { $0.startDate > now } ?? false
        NavigationStack {
            List {
                Group {
                    if upcoming {
                        LiveTileArt(tile: tile, channel: channel, big: true)
                    } else {
                        LivePicture(channel: channel, program: p, preview: preview, tile: tile)
                    }
                }
                .aspectRatio(16 / 9, contentMode: .fit)
                .clipShape(RoundedRectangle(cornerRadius: 12))
                .listRowSeparator(.hidden)
                VStack(alignment: .leading, spacing: 6) {
                    TileBadges(tile: tile, now: now)
                    Text(tile.matchup?.title() ?? p?.title ?? channel.name).font(.title3.bold())
                    if let p { Text(liveTiming(p, now: now)).font(.subheadline).foregroundStyle(.secondary) }
                    if let d = p?.description, !d.isEmpty { Text(d).font(.subheadline).foregroundStyle(.secondary).lineLimit(4) }
                }
                .listRowSeparator(.hidden)
                Section {
                    Button(upcoming ? "Watch \(channel.name) Now" : "Watch Now", systemImage: "play.fill", action: watch)
                    Button(favorite ? "Remove Channel from Favorites" : "Add Channel to Favorites", systemImage: favorite ? "star.fill" : "star", action: toggleFavorite)
                    if let p, tile.matchup == nil {
                        let following = state.followsShow(p.title)
                        Button(action: { state.toggleLiveShow(p.title) }) {
                            Label {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(following ? "Stop Following “\(p.title)”" : "Follow “\(p.title)”")
                                    Text("Its airings, on any channel, gather under Your shows on the overview").font(.caption).foregroundStyle(.secondary)
                                }
                            } icon: { Image(systemName: following ? "checkmark" : "plus") }
                        }
                    }
                }
            }
            .listStyle(.insetGrouped)
            .navigationTitle([channel.number, channel.name].compactMap { $0 }.joined(separator: " · "))
            .navigationBarTitleDisplayMode(.inline)
        }
    }
}

/// The programme's artwork, or the channel's logo large on a soft backdrop.
struct LiveArt: View {
    let program: LiveProgram?
    let channel: LiveChannel
    let logo: URL?

    var body: some View {
        ZStack {
            RadialGradient(colors: [Color(white: 0.2), Color(white: 0.06)], center: .topLeading, startRadius: 10, endRadius: 400)
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
        GeometryReader { g in
            ChannelLogo(url: logo, name: channel.name)
                .frame(width: g.size.width * 0.36, height: g.size.height * 0.4)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }
}

/// A small filled tag: LIVE, NEW, ON NOW.
struct LiveBadge: View {
    let text: String
    let color: Color

    var body: some View {
        Text(text)
            .font(.system(size: 9, weight: .heavy))
            .tracking(0.5)
            .foregroundStyle(.white)
            .padding(.horizontal, 5)
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
                    .background(.white.opacity(0.08), in: RoundedRectangle(cornerRadius: 6))
            }
        }
    }

    private var initials: String {
        String(name.split(separator: " ").prefix(2).compactMap(\.first)).uppercased()
    }
}
