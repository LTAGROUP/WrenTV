import SwiftUI

/// Which channel to open in a live player window, and the channels its
/// channel up and down step through.
struct LiveTarget: Codable, Hashable {
    var channelId: String
    var group: String?
    /// The guide's channels in order, as filtered when the window opened.
    var channelIds: [String]? = nil
}

/// Live TV as a guide. The picked programme fills the top with its details
/// and a muted picture of its channel; below are filters (favorites, recent,
/// kinds of channel, the provider's groups), a search across channels and
/// shows, and a grid of what's on per channel with a now line. Clicking a
/// programme picks it; clicking what's on again, or a channel, watches it.
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
        case "favorites": return channels.filter { state.liveFavorites.contains($0.id) }
        case "recent": return state.liveRecents.compactMap { id in channels.first { $0.id == id } }
        case let f where f.hasPrefix("genre:"): return channels.filter { $0.genre == f.dropFirst(6).description }
        case let f where f.hasPrefix("group:"): return channels.filter { $0.group == f.dropFirst(6).description }
        default: return channels
        }
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
        .navigationSubtitle(loaded && !channels.isEmpty ? "\(shown.count) channel\(shown.count == 1 ? "" : "s")" : "")
        .task(id: state.refreshTick) { await load() }
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
        .onChange(of: pick?.channel) { _, id in
            preview.show(id.flatMap { id in channels.first { $0.id == id } }, api: state.api)
        }
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
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 8) {
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
            Button { page(-1) } label: { Image(systemName: "chevron.left") }
                .help("Earlier")
                .disabled(windowStart <= Self.halfHour(now))
            Button("Now") { windowStart = Self.halfHour(now) }
                .disabled(windowStart == Self.halfHour(now))
            Button { page(1) } label: { Image(systemName: "chevron.right") }
                .help("Later")
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

    // MARK: Data

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

    private func play(_ ch: LiveChannel) {
        preview.stop()
        state.liveRecents = [ch.id] + state.liveRecents.filter { $0 != ch.id }
        openWindow(id: "live", value: LiveTarget(channelId: ch.id, group: nil, channelIds: searching ? nil : shown.map(\.id)))
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
