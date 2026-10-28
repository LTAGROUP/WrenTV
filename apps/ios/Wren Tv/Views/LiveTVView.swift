import SwiftUI

/// Live TV: a spotlight on the channel last watched (or the first one) with
/// a muted live picture, filters (favorites, recent, kinds of channel, the
/// provider's groups), a search over channels and upcoming shows, and what's
/// on now and next per channel. Tapping a channel watches it; its info
/// button opens its schedule with a preview.
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
    @State private var results: LiveSearchResult?
    @State private var preview = LivePreview()

    private var channels: [LiveChannel] { state.liveChannels }

    private var groups: [String] {
        var seen = Set<String>(), out: [String] = []
        for c in channels { if let g = c.group, !g.isEmpty, seen.insert(g).inserted { out.append(g) } }
        return out
    }

    private var genres: [LiveGenre] {
        let have = Set(channels.compactMap(\.genre))
        return LiveGenre.allCases.filter { have.contains($0.rawValue) }
    }

    private var searching: Bool { !query.trimmingCharacters(in: .whitespaces).isEmpty }

    private var shown: [LiveChannel] {
        switch filter {
        case "favorites": return channels.filter { state.liveFavorites.contains($0.id) }
        case "recent": return state.liveRecents.compactMap { id in channels.first { $0.id == id } }
        case let f where f.hasPrefix("genre:"): return channels.filter { $0.genre == f.dropFirst(6).description }
        case let f where f.hasPrefix("group:"): return channels.filter { $0.group == f.dropFirst(6).description }
        default: return channels
        }
    }

    /// The channel the top of the page shows.
    private var spotlight: LiveChannel? {
        state.liveRecents.lazy.compactMap { id in channels.first { $0.id == id } }.first ?? channels.first
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
        .onChange(of: schedule == nil) { _, closed in
            // The sheet borrows the preview; the spotlight takes it back.
            preview.show(closed ? spotlight : schedule, api: state.api, after: .milliseconds(closed ? 600 : 300))
        }
        .onChange(of: spotlight?.id) { _, _ in if schedule == nil { preview.show(spotlight, api: state.api) } }
        .onChange(of: state.playingLive == nil) { _, closed in
            if closed && schedule == nil { preview.show(spotlight, api: state.api, after: .milliseconds(600)) }
        }
        .onAppear { if schedule == nil { preview.show(spotlight, api: state.api) } }
        .onDisappear { preview.stop() }
        .sheet(item: $schedule) { ch in
            ScheduleSheet(channel: ch, programs: programs(ch), now: now, preview: preview,
                          favorite: state.liveFavorites.contains(ch.id), toggleFavorite: { toggleFavorite(ch) }) {
                schedule = nil
                play(ch)
            }
            .presentationDetents([.medium, .large])
        }
    }

    // MARK: Guide

    private var list: some View {
        List {
            if let ch = spotlight {
                Spotlight(channel: ch, program: onNow(ch), next: next(ch), now: now, preview: preview) { play(ch) }
                    .listRowInsets(EdgeInsets(top: 4, leading: m.gutter, bottom: 10, trailing: m.gutter))
                    .listRowSeparator(.hidden)
                    .listRowBackground(Color.clear)
            }
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 8) {
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
        return Button { airing ? play(ch) : (schedule = ch) } label: {
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
            state.liveFavorites = Set(l.favorites ?? [])
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

    private func toggleFavorite(_ ch: LiveChannel) {
        let on = !state.liveFavorites.contains(ch.id)
        if on { state.liveFavorites.insert(ch.id) } else { state.liveFavorites.remove(ch.id) }
        Task {
            do {
                try await state.api?.setLiveFavorite(ch.id, on)
            } catch {
                if on { state.liveFavorites.remove(ch.id) } else { state.liveFavorites.insert(ch.id) }
            }
        }
    }

    private func play(_ ch: LiveChannel) {
        preview.stop()
        schedule = nil
        state.liveRecents = [ch.id] + state.liveRecents.filter { $0 != ch.id }
        state.playingLive = LiveTarget(channelId: ch.id, group: nil, channelIds: searching ? nil : shown.map(\.id))
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

    var body: some View {
        ZStack {
            LiveArt(program: program, channel: channel, logo: state.api?.liveLogoURL(channel.logo))
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
                        Text(p.title).font(.headline)
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
