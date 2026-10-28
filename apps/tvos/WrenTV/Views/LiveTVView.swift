import SwiftUI

/// Live TV, as an overview or a guide, with the focused card or channel's
/// muted live picture and what it's showing across the top. The overview's
/// shelves have live and upcoming games with the teams' logos, the viewer's
/// followed shows and channels, and what's on by kind; the guide has the next
/// three hours under filters (favorites, recent, kinds of channel, the
/// provider's groups). Select watches; holding Select offers favoriting the
/// channel and following the show.
struct LiveTVView: View {
    @Environment(AppState.self) private var state
    @AppStorage("liveFilter") private var filter = "all"
    @State private var guide: [String: [LiveProgram]] = [:]
    @State private var loaded = false
    @State private var error: String?
    @State private var now = Date()
    @State private var pick: String?
    @State private var searching = false
    @State private var preview = LivePreview()
    @FocusState private var focus: String?
    /// "overview" or "guide".
    @AppStorage("liveView") private var view = "overview"
    @State private var shelves: [LiveShelf]?
    @State private var overviewError: String?
    @State private var pickTile: LiveTile?
    @State private var optionsTile: LiveTile?

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

    private var picked: LiveChannel? {
        pick.flatMap { id in channels.first { $0.id == id } }
            ?? state.liveRecents.lazy.compactMap { id in channels.first { $0.id == id } }.first
            ?? shown.first
    }

    private var overview: Bool { view == "overview" }

    private func channel(_ t: LiveTile) -> LiveChannel? { channels.first { $0.id == t.channelId } }

    /// The overview's focused card, or its first.
    private var heroTile: LiveTile? {
        pickTile ?? shelves?.lazy.flatMap(\.tiles).first { channel($0) != nil }
    }

    /// The channel playing muted at the top: one coming up has no picture yet.
    private var previewing: LiveChannel? {
        guard overview else { return picked }
        guard let t = heroTile, (t.program?.startDate ?? .distantPast) <= now else { return nil }
        return channel(t)
    }

    /// The guide starts at the half hour before now.
    private var windowStart: Date {
        let t = now.timeIntervalSince1970
        return Date(timeIntervalSince1970: t - t.truncatingRemainder(dividingBy: 1800))
    }

    var body: some View {
        Group {
            if !channels.isEmpty {
                VStack(alignment: .leading, spacing: 0) {
                    if overview {
                        if let t = heroTile, let ch = channel(t) {
                            TileHero(tile: t, channel: ch, now: now, preview: preview, favorite: state.liveFavorites.contains(ch.id))
                                .padding(.horizontal, TV.gutter)
                                .padding(.top, 20)
                        }
                    } else if let ch = picked {
                        LiveHero(channel: ch, programs: programs(ch), now: now, preview: preview,
                                 favorite: state.liveFavorites.contains(ch.id))
                            .padding(.horizontal, TV.gutter)
                            .padding(.top, 20)
                    }
                    ScrollView {
                        VStack(alignment: .leading, spacing: 18) {
                            filters
                            if overview {
                                overviewShelves
                            } else {
                            GuideRuler(start: windowStart, now: now).padding(.horizontal, TV.gutter)
                            LazyVStack(spacing: 12) {
                                ForEach(shown) { ch in
                                    GuideRow(channel: ch, programs: programs(ch), start: windowStart, now: now,
                                             favorite: state.liveFavorites.contains(ch.id)) { play(ch) }
                                        .focused($focus, equals: ch.id)
                                        .contextMenu {
                                            Button("Watch", systemImage: "play.fill") { play(ch) }
                                            let fav = state.liveFavorites.contains(ch.id)
                                            Button(fav ? "Remove from Favorites" : "Add to Favorites",
                                                   systemImage: fav ? "star.slash" : "star") { toggleFavorite(ch) }
                                            if let p = programs(ch).first(where: { $0.startDate <= now && now < $0.endDate }) {
                                                followButton(p.title)
                                            }
                                        }
                                }
                                if shown.isEmpty {
                                    EmptyState(title: filter == "favorites" ? "No favorites yet" : "No channels here",
                                               message: filter == "favorites" ? "Hold Select on a channel to add it." : nil,
                                               symbol: filter == "favorites" ? "star" : "tv")
                                }
                            }
                            .padding(.horizontal, TV.gutter)
                            .focusSection()
                            }
                        }
                        .padding(.vertical, 24)
                    }
                    .scrollClipDisabled()
                }
            } else if let error {
                EmptyState(title: "Couldn't load channels", message: error, symbol: "wifi.exclamationmark")
            } else if loaded {
                EmptyState(title: "No channels", message: "Add an IPTV playlist on the server's web dashboard under Settings → Live TV.",
                           symbol: "dot.radiowaves.left.and.right")
            } else {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .task {
            await load()
            var ticks = 0
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(30))
                now = Date()
                ticks += 1
                if ticks % 10 == 0 { await load() }
            }
        }
        .task(id: "\(view)|\(state.liveShows.count)|\(state.liveFavorites.count)|\(state.liveRecents.first ?? "")") {
            guard overview else { return }
            while !Task.isCancelled {
                await loadOverview()
                try? await Task.sleep(for: .seconds(120))
            }
        }
        .onChange(of: focus) { _, id in
            guard let id else { return }
            if id.hasPrefix("t:") {
                // "t:<shelf>/<tile>"
                let parts = id.dropFirst(2).split(separator: "/", maxSplits: 1).map(String.init)
                if parts.count == 2, let t = shelves?.first(where: { $0.id == parts[0] })?.tiles.first(where: { $0.id == parts[1] }) { pickTile = t }
            } else if !id.hasPrefix("v:") {
                pick = id
            }
        }
        .onChange(of: previewing?.id) { _, _ in preview.show(previewing, api: state.api) }
        .onChange(of: state.playingLive == nil) { _, closed in
            if closed && !searching { preview.show(previewing, api: state.api, after: .milliseconds(600)) }
        }
        .onAppear { preview.show(previewing, api: state.api) }
        .confirmationDialog(optionsTile.map { t in t.matchup?.title() ?? t.program?.title ?? channel(t)?.name ?? "" } ?? "",
                            isPresented: Binding(get: { optionsTile != nil }, set: { if !$0 { optionsTile = nil } }),
                            titleVisibility: .visible, presenting: optionsTile) { t in
            if let ch = channel(t) { tileMenu(t, ch) }
        } message: { t in
            if let p = t.program, let ch = channel(t) { Text("\(ch.name) · \(tvTiming(p, now: now))") }
        }
        .onDisappear { preview.stop() }
        .sheet(isPresented: $searching) {
            LiveSearch(channels: channels) { ch in
                searching = false
                play(ch)
            }
        }
        .onChange(of: searching) { _, open in
            if open { preview.stop() } else if state.playingLive == nil { preview.show(previewing, api: state.api) }
        }
    }

    private var filters: some View {
        ScrollView(.horizontal) {
            HStack(spacing: 20) {
                Button { searching = true } label: { Label("Search", systemImage: "magnifyingglass") }
                chip("Overview", icon: "square.grid.2x2", selected: overview) { view = "overview" }
                    .focused($focus, equals: "v:overview")
                chip("Guide", icon: "list.bullet", selected: !overview) { view = "guide" }
                    .focused($focus, equals: "v:guide")
                if !overview {
                Divider().frame(height: 40)
                if providers.count > 1 {
                    Menu {
                        Button("All providers") { pickProvider("") }
                        ForEach(providers) { p in Button(p.name) { pickProvider(p.id) } }
                    } label: {
                        Label(chosenProvider?.name ?? "All providers", systemImage: "chevron.down")
                            .foregroundStyle(chosenProvider != nil ? Color.wrenEmber : .primary)
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
                        Label(filter.hasPrefix("group:") ? String(filter.dropFirst(6)) : "Groups", systemImage: "chevron.down")
                            .foregroundStyle(filter.hasPrefix("group:") ? Color.wrenEmber : .primary)
                    }
                }
                }
            }
            .padding(.horizontal, TV.gutter)
            .padding(.vertical, 12)
        }
        .scrollClipDisabled()
        .focusSection()
    }

    // MARK: Overview

    private var overviewShelves: some View {
        Group {
            if let shelves {
                LazyVStack(alignment: .leading, spacing: 30) {
                    ForEach(shelves) { sh in
                        VStack(alignment: .leading, spacing: 8) {
                            Text(sh.title).font(.headline).padding(.horizontal, TV.gutter)
                            ScrollView(.horizontal) {
                                LazyHStack(alignment: .top, spacing: 40) {
                                    ForEach(sh.tiles) { t in
                                        if let ch = channel(t) {
                                            TVTileCard(tile: t, channel: ch, now: now) { open(t, in: sh.tiles) }
                                                .focused($focus, equals: "t:\(sh.id)/\(t.id)")
                                                .contextMenu { tileMenu(t, ch) }
                                        }
                                    }
                                }
                                .padding(.horizontal, TV.gutter)
                                .padding(.vertical, 20)
                            }
                            .scrollClipDisabled()
                        }
                        .focusSection()
                    }
                    if shelves.isEmpty {
                        EmptyState(title: "Nothing to show yet", message: "Once the guide has loaded, games, your shows and what's on gather here.",
                                   symbol: "sportscourt")
                    }
                }
            } else if let overviewError {
                EmptyState(title: "No overview", message: overviewError, symbol: "exclamationmark.triangle")
            } else {
                ProgressView().frame(maxWidth: .infinity).padding(.top, 60)
            }
        }
    }

    @ViewBuilder private func tileMenu(_ t: LiveTile, _ ch: LiveChannel) -> some View {
        let upcoming = t.program.map { $0.startDate > now } ?? false
        Button(upcoming ? "Watch \(ch.name) Now" : "Watch Now", systemImage: "play.fill") { play(ch) }
        let fav = state.liveFavorites.contains(ch.id)
        Button(fav ? "Remove Channel from Favorites" : "Add Channel to Favorites", systemImage: fav ? "star.slash" : "star") { toggleFavorite(ch) }
        if let p = t.program, t.matchup == nil { followButton(p.title) }
    }

    private func followButton(_ title: String) -> some View {
        let following = state.followsShow(title)
        return Button(following ? "Stop Following “\(title)”" : "Follow “\(title)”", systemImage: following ? "checkmark" : "plus") {
            state.toggleLiveShow(title)
        }
    }

    /// A card plays when it's on now; one coming up offers its options.
    private func open(_ t: LiveTile, in shelf: [LiveTile]) {
        guard let ch = channel(t) else { return }
        if let p = t.program, p.startDate > now { optionsTile = t; return }
        var ids: [String] = []
        for x in shelf where !ids.contains(x.channelId) { ids.append(x.channelId) }
        play(ch, channelIds: ids)
    }

    private func loadOverview() async {
        guard let api = state.api else { return }
        do {
            let s = try await api.liveOverview()
            shelves = s
            overviewError = nil
            // A refresh keeps the focused card when it's still there.
            if let t = pickTile { pickTile = s.lazy.flatMap(\.tiles).first { $0.id == t.id } }
        } catch is CancellationError {
        } catch {
            if shelves == nil {
                overviewError = (error as? WrenTVError)?.status == 404 ? "Your WrenTV server needs an update for the Live TV overview." : error.localizedDescription
            }
        }
    }

    private func chip(_ title: String, icon: String? = nil, selected: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 10) {
                if let icon { Image(systemName: icon).foregroundStyle(title == "Favorites" ? Color.yellow : selected ? Color.wrenEmber : Color.secondary) }
                Text(title).fontWeight(selected ? .bold : .regular)
            }
            .foregroundStyle(selected ? Color.wrenEmber : .primary)
        }
    }

    private func programs(_ ch: LiveChannel) -> [LiveProgram] {
        guide[ch.id] ?? [ch.now, ch.next].compactMap { $0 }
    }

    private func load() async {
        guard let api = state.api else { return }
        do {
            async let lineup = api.liveLineup()
            async let g = api.liveGuide(from: nil, hours: 6)
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
            if state.liveChannels.isEmpty { self.error = error.localizedDescription }
        }
        now = Date()
        loaded = true
    }

    private func toggleFavorite(_ ch: LiveChannel) { state.toggleLiveFavorite(ch.id) }

    private func play(_ ch: LiveChannel, channelIds: [String]? = nil) {
        preview.stop()
        optionsTile = nil
        if !overview { pick = ch.id }
        state.liveRecents = [ch.id] + state.liveRecents.filter { $0 != ch.id }
        let ids = channelIds ?? shown.map(\.id)
        state.playingLive = LiveTarget(channelId: ch.id, group: nil, channelIds: ids.contains(ch.id) ? ids : nil)
    }
}

/// The focused channel: its muted live picture beside what's on.
private struct LiveHero: View {
    @Environment(AppState.self) private var state
    let channel: LiveChannel
    let programs: [LiveProgram]
    let now: Date
    let preview: LivePreview
    let favorite: Bool

    private var current: LiveProgram? { programs.first { $0.startDate <= now && now < $0.endDate } }
    private var next: LiveProgram? { programs.first { $0.startDate > now } }

    var body: some View {
        HStack(alignment: .top, spacing: 50) {
            ZStack(alignment: .topLeading) {
                LiveArt(program: current, channel: channel, logo: state.api?.liveLogoURL(channel.logo))
                if preview.channelId == channel.id {
                    LivePreviewSurface(player: preview.player)
                        .opacity(preview.showing ? 1 : 0)
                        .animation(.easeOut(duration: 0.4), value: preview.showing)
                }
                LiveBadge(text: "LIVE", color: .red).padding(16)
            }
            .frame(width: 640, height: 360)
            .clipShape(RoundedRectangle(cornerRadius: 18))
            .shadow(color: .black.opacity(0.5), radius: 20, y: 10)

            VStack(alignment: .leading, spacing: 12) {
                HStack(spacing: 18) {
                    RemoteImage(url: state.api?.liveLogoURL(channel.logo), contentMode: .fit).frame(width: 110, height: 56)
                    Text([channel.number, channel.name].compactMap { $0 }.joined(separator: "  "))
                        .font(.callout.weight(.semibold)).foregroundStyle(.secondary)
                    if favorite { Image(systemName: "star.fill").foregroundStyle(.yellow) }
                }
                Text(current?.title ?? channel.name).font(.title2.bold()).lineLimit(2)
                if let p = current {
                    HStack(spacing: 12) {
                        if p.isLive { LiveBadge(text: "LIVE", color: .red) }
                        if p.isNew { LiveBadge(text: "NEW", color: .white.opacity(0.25)) }
                        Text([p.episode, p.timeRange, "\(max(Int(p.endDate.timeIntervalSince(now) / 60), 0)) min left"]
                            .compactMap { $0 }.joined(separator: "  ·  "))
                            .font(.callout).foregroundStyle(.secondary).lineLimit(1)
                    }
                    if let cats = p.categories, !cats.isEmpty {
                        Text(cats.prefix(3).joined(separator: " · ")).font(.caption).foregroundStyle(.tertiary)
                    }
                    ProgressView(value: p.progress(at: now)).tint(Color.wrenEmber).frame(maxWidth: 700)
                    if let d = p.description, !d.isEmpty {
                        Text(d).font(.callout).foregroundStyle(.secondary).lineLimit(3).frame(maxWidth: 900, alignment: .leading)
                    }
                } else {
                    Text("No guide for this channel").foregroundStyle(.secondary)
                }
                Spacer(minLength: 0)
                if let n = next {
                    Text("Up next  \(n.startDate.formatted(date: .omitted, time: .shortened))  \(n.title)")
                        .font(.callout).foregroundStyle(.secondary).lineLimit(1)
                }
            }
            .frame(maxWidth: .infinity, maxHeight: 360, alignment: .topLeading)
        }
        .frame(height: 360)
    }
}

/// "7:00 PM", "Tomorrow 7:00 PM" or "Sat 7:00 PM".
private func tvStartsAt(_ d: Date) -> String {
    let t = d.formatted(date: .omitted, time: .shortened)
    if Calendar.current.isDateInToday(d) { return t }
    if Calendar.current.isDateInTomorrow(d) { return "Tomorrow \(t)" }
    return "\(d.formatted(.dateTime.weekday(.abbreviated))) \(t)"
}

/// When a programme is on, said for where it is in time.
private func tvTiming(_ p: LiveProgram, now: Date) -> String {
    if p.startDate > now { return "\(tvStartsAt(p.startDate)) – \(p.endDate.formatted(date: .omitted, time: .shortened))" }
    return "\(p.timeRange)  ·  \(max(0, Int(p.endDate.timeIntervalSince(now) / 60))) min left"
}

/// A tile's tags: its start time when it's coming up, else LIVE and NEW; and the league.
private struct TileBadges: View {
    let tile: LiveTile
    let now: Date

    var body: some View {
        HStack(spacing: 8) {
            if let p = tile.program {
                if p.startDate > now {
                    LiveBadge(text: tvStartsAt(p.startDate), color: .black.opacity(0.75))
                } else {
                    if p.isLive { LiveBadge(text: "LIVE", color: .red) }
                    if p.isNew { LiveBadge(text: "NEW", color: .white.opacity(0.25)) }
                }
            }
            if let l = tile.matchup?.league, !l.isEmpty { LiveBadge(text: l.uppercased(), color: .white.opacity(0.25)) }
        }
    }
}

/// The focused card: its picture (live, once it's on) beside what it is.
private struct TileHero: View {
    @Environment(AppState.self) private var state
    let tile: LiveTile
    let channel: LiveChannel
    let now: Date
    let preview: LivePreview
    let favorite: Bool

    var body: some View {
        let p = tile.program
        HStack(alignment: .top, spacing: 50) {
            ZStack(alignment: .topLeading) {
                TVTileArt(tile: tile, channel: channel, big: true)
                if preview.channelId == channel.id {
                    LivePreviewSurface(player: preview.player)
                        .opacity(preview.showing ? 1 : 0)
                        .animation(.easeOut(duration: 0.4), value: preview.showing)
                }
            }
            .frame(width: 640, height: 360)
            .clipShape(RoundedRectangle(cornerRadius: 18))
            .shadow(color: .black.opacity(0.5), radius: 20, y: 10)

            VStack(alignment: .leading, spacing: 12) {
                HStack(spacing: 18) {
                    TileBadges(tile: tile, now: now)
                    Text([channel.number, channel.name].compactMap { $0 }.joined(separator: "  "))
                        .font(.callout.weight(.semibold)).foregroundStyle(.secondary)
                    if favorite { Image(systemName: "star.fill").foregroundStyle(.yellow) }
                }
                Text(tile.matchup?.title() ?? p?.title ?? channel.name).font(.title2.bold()).lineLimit(2)
                if let p {
                    Text([p.episode, tvTiming(p, now: now)].compactMap { $0 }.joined(separator: "  ·  "))
                        .font(.callout).foregroundStyle(.secondary).lineLimit(1)
                    if p.startDate <= now { ProgressView(value: p.progress(at: now)).tint(Color.wrenEmber).frame(maxWidth: 700) }
                    if let d = p.description, !d.isEmpty {
                        Text(d).font(.callout).foregroundStyle(.secondary).lineLimit(3).frame(maxWidth: 900, alignment: .leading)
                    }
                }
                Spacer(minLength: 0)
                Text(p.map { $0.startDate > now } ?? false ? "Select for options" : "Select to watch  ·  Hold Select for options")
                    .font(.caption).foregroundStyle(.tertiary)
            }
            .frame(maxWidth: .infinity, maxHeight: 360, alignment: .topLeading)
        }
        .frame(height: 360)
    }
}

/// A card on an overview shelf.
private struct TVTileCard: View {
    let tile: LiveTile
    let channel: LiveChannel
    let now: Date
    let action: () -> Void

    var body: some View {
        let p = tile.program
        let airing = p.map { $0.startDate <= now && now < $0.endDate } ?? false
        VStack(alignment: .leading, spacing: 6) {
            Button(action: action) {
                TVTileArt(tile: tile, channel: channel, big: false)
                    .frame(width: 400, height: 225)
                    .overlay(alignment: .topLeading) { TileBadges(tile: tile, now: now).padding(12) }
                    .overlay(alignment: .bottom) {
                        if airing, let p {
                            ZStack(alignment: .leading) {
                                Rectangle().fill(.white.opacity(0.2))
                                Rectangle().fill(Color.wrenEmber).frame(width: 400 * p.progress(at: now))
                            }
                            .frame(height: 5)
                        }
                    }
            }
            .buttonStyle(.card)
            Text(tile.matchup?.title(short: true) ?? p?.title ?? channel.name)
                .font(.callout.weight(.semibold)).lineLimit(1).padding(.top, 8)
            Text(airing ? "\(channel.name) · \(Int(p!.endDate.timeIntervalSince(now) / 60)) min left" : p != nil ? channel.name : channel.group ?? "")
                .font(.caption).foregroundStyle(.secondary).lineLimit(1)
        }
        .frame(width: 400)
    }
}

/// A tile's picture: the matchup, the programme's art over its channel's logo, or the logo alone.
private struct TVTileArt: View {
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
                if let art { RemoteImage(url: art) }
            }
        }
        .clipped()
        .overlay(alignment: .bottomTrailing) {
            if tile.matchup != nil || art != nil {
                // Which channel it's on, like a network's bug.
                RemoteImage(url: logo, contentMode: .fit)
                    .frame(width: big ? 100 : 72, height: big ? 52 : 38)
                    .padding(6)
                    .background(.black.opacity(0.6), in: RoundedRectangle(cornerRadius: 8))
                    .padding(12)
            }
        }
    }
}

/// Two teams over their colours, like a scoreboard.
private struct MatchupArt: View {
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
                TeamMark(team: matchup.away, size: big ? 130 : 84)
                Spacer(minLength: 0)
                Text("@").font(big ? .title2 : .headline).foregroundStyle(.white.opacity(0.8))
                Spacer(minLength: 0)
                TeamMark(team: matchup.home, size: big ? 130 : 84)
                Spacer(minLength: 0)
            }
        }
    }
}

private struct TeamMark: View {
    let team: LiveTeam
    let size: CGFloat

    var body: some View {
        VStack(spacing: 8) {
            if let logo = team.logo, let url = URL(string: logo) {
                RemoteImage(url: url, contentMode: .fit).frame(width: size, height: size)
            } else {
                Text(String(team.name.split(separator: " ").prefix(2).compactMap(\.first)).uppercased())
                    .font(.title3.bold()).foregroundStyle(.white)
                    .frame(width: size, height: size)
                    .background(.white.opacity(0.16), in: Circle())
            }
            Text(team.short ?? team.name).font(.caption.bold()).foregroundStyle(.white).lineLimit(1)
        }
        .frame(width: size * 1.6)
    }
}

/// Times along the top of the guide, every half hour.
private struct GuideRuler: View {
    let start: Date
    let now: Date

    var body: some View {
        HStack(spacing: 0) {
            Color.clear.frame(width: GuideRow.channelWidth + GuideRow.gap)
            GeometryReader { g in
                let perHour = g.size.width / GuideRow.hours
                ZStack(alignment: .leading) {
                    ForEach(0..<Int(GuideRow.hours * 2), id: \.self) { i in
                        Text(start.addingTimeInterval(Double(i) * 1800).formatted(date: .omitted, time: .shortened))
                            .font(.caption).foregroundStyle(.secondary)
                            .offset(x: CGFloat(i) * perHour / 2)
                    }
                    Capsule().fill(Color.wrenEmber).frame(width: 4, height: 30)
                        .offset(x: perHour * now.timeIntervalSince(start) / 3600 - 2)
                }
            }
            .frame(height: 30)
        }
    }
}

/// A channel and its next three hours of programmes.
private struct GuideRow: View {
    static let channelWidth: CGFloat = 330
    static let gap: CGFloat = 16
    static let hours: CGFloat = 3

    @Environment(AppState.self) private var state
    let channel: LiveChannel
    let programs: [LiveProgram]
    let start: Date
    let now: Date
    let favorite: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: Self.gap) {
                HStack(spacing: 16) {
                    RemoteImage(url: state.api?.liveLogoURL(channel.logo), contentMode: .fit)
                        .frame(width: 120, height: 64)
                        .background(Color.white.opacity(0.05), in: RoundedRectangle(cornerRadius: 10))
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 8) {
                            if let n = channel.number { Text(n).font(.caption.monospacedDigit()).foregroundStyle(.secondary) }
                            if favorite { Image(systemName: "star.fill").font(.caption).foregroundStyle(.yellow) }
                        }
                        Text(channel.name).font(.callout.weight(.semibold)).lineLimit(2)
                    }
                    Spacer(minLength: 0)
                }
                .frame(width: Self.channelWidth)
                timeline
            }
            .padding(10)
        }
        .buttonStyle(GuideRowStyle())
    }

    private var timeline: some View {
        GeometryReader { g in
            let perSecond = g.size.width / (Self.hours * 3600)
            let end = start.addingTimeInterval(Self.hours * 3600)
            let visible = programs.filter { $0.endDate > start && $0.startDate < end }
            ZStack(alignment: .leading) {
                if visible.isEmpty {
                    Text("No guide").font(.caption).foregroundStyle(.tertiary).padding(.leading, 16)
                }
                ForEach(visible, id: \.start) { p in
                    let from = max(p.startDate, start), to = min(p.endDate, end)
                    block(p)
                        .frame(width: max(CGFloat(to.timeIntervalSince(from)) * perSecond - 6, 6), height: g.size.height)
                        .offset(x: CGFloat(from.timeIntervalSince(start)) * perSecond)
                }
            }
        }
        .frame(height: 80)
    }

    private func block(_ p: LiveProgram) -> some View {
        let airing = p.startDate <= now && now < p.endDate
        return ZStack(alignment: .leading) {
            RoundedRectangle(cornerRadius: 10).fill(Color.white.opacity(airing ? 0.1 : 0.05))
            if airing {
                GeometryReader { g in
                    Rectangle().fill(Color.wrenEmber.opacity(0.28)).frame(width: g.size.width * p.progress(at: now))
                }
                .clipShape(RoundedRectangle(cornerRadius: 10))
            }
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    if p.isLive { LiveBadge(text: "LIVE", color: .red) }
                    if p.isNew { LiveBadge(text: "NEW", color: .white.opacity(0.25)) }
                    Text(p.title).font(.caption.weight(airing ? .semibold : .regular)).lineLimit(1)
                }
                Text(p.startDate.formatted(date: .omitted, time: .shortened))
                    .font(.caption2).foregroundStyle(.secondary).lineLimit(1)
            }
            .padding(.horizontal, 14)
        }
    }
}

/// A guide row lights up as a whole while it has focus.
private struct GuideRowStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View { RowBody(configuration: configuration) }

    private struct RowBody: View {
        let configuration: Configuration
        @Environment(\.isFocused) private var focused

        var body: some View {
            configuration.label
                .background(focused ? Color.white.opacity(0.14) : Color.white.opacity(0.03), in: RoundedRectangle(cornerRadius: 16))
                .overlay(RoundedRectangle(cornerRadius: 16).stroke(focused ? Color.white.opacity(0.85) : .clear, lineWidth: 3))
                .scaleEffect(configuration.isPressed ? 0.98 : focused ? 1.01 : 1)
                .animation(.easeOut(duration: 0.15), value: focused)
        }
    }
}

/// Channels and upcoming shows matching what's typed.
private struct LiveSearch: View {
    @Environment(AppState.self) private var state
    let channels: [LiveChannel]
    let watch: (LiveChannel) -> Void
    @State private var query = ""
    @State private var results: LiveSearchResult?

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 30) {
                    if let r = results {
                        if !r.channels.isEmpty {
                            Text("Channels").font(.headline)
                            ForEach(r.channels.compactMap { c in channels.first { $0.id == c.id } }) { ch in
                                Button { watch(ch) } label: {
                                    HStack(spacing: 24) {
                                        RemoteImage(url: state.api?.liveLogoURL(ch.logo), contentMode: .fit).frame(width: 120, height: 64)
                                        Text([ch.number, ch.name].compactMap { $0 }.joined(separator: "  ")).font(.headline)
                                        Spacer()
                                        if let p = ch.now { Text(p.title).foregroundStyle(.secondary).lineLimit(1) }
                                    }
                                }
                            }
                        }
                        if !r.airings.isEmpty {
                            Text("Shows").font(.headline)
                            ForEach(r.airings, id: \.self) { a in
                                if let ch = channels.first(where: { $0.id == a.channelId }) { airing(a.program, ch) }
                            }
                        }
                        if r.channels.isEmpty && r.airings.isEmpty {
                            EmptyState(title: "Nothing matches “\(query)”", symbol: "magnifyingglass")
                        }
                    }
                }
                .padding(.horizontal, TV.gutter)
            }
            .searchable(text: $query, prompt: "Channels and shows")
            .task(id: query) {
                let q = query.trimmingCharacters(in: .whitespaces)
                guard !q.isEmpty, let api = state.api else { results = nil; return }
                try? await Task.sleep(for: .milliseconds(300))
                guard !Task.isCancelled, let r = try? await api.liveSearch(q), !Task.isCancelled else { return }
                results = r
            }
        }
    }

    private func airing(_ p: LiveProgram, _ ch: LiveChannel) -> some View {
        let on = p.startDate <= Date() && Date() < p.endDate
        return Button { watch(ch) } label: {
            HStack(spacing: 24) {
                LiveArt(program: p, channel: ch, logo: state.api?.liveLogoURL(ch.logo))
                    .frame(width: 192, height: 108)
                    .clipShape(RoundedRectangle(cornerRadius: 10))
                VStack(alignment: .leading, spacing: 6) {
                    Text(p.title).font(.headline).lineLimit(1)
                    HStack(spacing: 10) {
                        if on { LiveBadge(text: "ON NOW", color: .wrenEmber) }
                        if p.isNew { LiveBadge(text: "NEW", color: .white.opacity(0.25)) }
                        Text([p.episode, ch.name, on ? nil : p.startDate.formatted(.dateTime.weekday(.abbreviated).hour().minute())]
                            .compactMap { $0 }.joined(separator: " · "))
                            .font(.callout).foregroundStyle(.secondary).lineLimit(1)
                    }
                }
                Spacer()
            }
        }
        .disabled(!on)
    }
}

/// The programme's artwork, or the channel's logo large on a soft backdrop.
private struct LiveArt: View {
    let program: LiveProgram?
    let channel: LiveChannel
    let logo: URL?

    var body: some View {
        ZStack {
            RadialGradient(colors: [Color(white: 0.2), Color(white: 0.06)], center: .topLeading, startRadius: 10, endRadius: 700)
            if let icon = program?.icon, let url = URL(string: icon) {
                RemoteImage(url: url)
            } else {
                GeometryReader { g in
                    RemoteImage(url: logo, contentMode: .fit)
                        .frame(width: g.size.width * 0.4, height: g.size.height * 0.4)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
        }
        .clipped()
    }
}

/// A small filled tag: LIVE, NEW, ON NOW.
private struct LiveBadge: View {
    let text: String
    let color: Color

    var body: some View {
        Text(text)
            .font(.system(size: 18, weight: .heavy))
            .tracking(0.8)
            .foregroundStyle(.white)
            .padding(.horizontal, 8)
            .padding(.vertical, 2)
            .background(color, in: RoundedRectangle(cornerRadius: 5))
    }
}
