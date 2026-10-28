import SwiftUI

/// Live TV: the focused channel's muted live picture and what it's showing
/// across the top, then a guide of the next three hours under filters
/// (favorites, recent, kinds of channel, the provider's groups) and search.
/// Select watches a channel; holding it adds the channel to favorites.
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

    private var shown: [LiveChannel] {
        switch filter {
        case "favorites": return channels.filter { state.liveFavorites.contains($0.id) }
        case "recent": return state.liveRecents.compactMap { id in channels.first { $0.id == id } }
        case let f where f.hasPrefix("genre:"): return channels.filter { $0.genre == f.dropFirst(6).description }
        case let f where f.hasPrefix("group:"): return channels.filter { $0.group == f.dropFirst(6).description }
        default: return channels
        }
    }

    private var picked: LiveChannel? {
        pick.flatMap { id in channels.first { $0.id == id } }
            ?? state.liveRecents.lazy.compactMap { id in channels.first { $0.id == id } }.first
            ?? shown.first
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
                    if let ch = picked {
                        LiveHero(channel: ch, programs: programs(ch), now: now, preview: preview,
                                 favorite: state.liveFavorites.contains(ch.id))
                            .padding(.horizontal, TV.gutter)
                            .padding(.top, 20)
                    }
                    ScrollView {
                        VStack(alignment: .leading, spacing: 18) {
                            filters
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
        .onChange(of: focus) { _, id in
            guard let id else { return }
            pick = id
        }
        .onChange(of: picked?.id) { _, _ in preview.show(picked, api: state.api) }
        .onChange(of: state.playingLive == nil) { _, closed in
            if closed && !searching { preview.show(picked, api: state.api, after: .milliseconds(600)) }
        }
        .onAppear { preview.show(picked, api: state.api) }
        .onDisappear { preview.stop() }
        .sheet(isPresented: $searching) {
            LiveSearch(channels: channels) { ch in
                searching = false
                play(ch)
            }
        }
        .onChange(of: searching) { _, open in
            if open { preview.stop() } else if state.playingLive == nil { preview.show(picked, api: state.api) }
        }
    }

    private var filters: some View {
        ScrollView(.horizontal) {
            HStack(spacing: 20) {
                Button { searching = true } label: { Label("Search", systemImage: "magnifyingglass") }
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
            .padding(.horizontal, TV.gutter)
            .padding(.vertical, 12)
        }
        .scrollClipDisabled()
        .focusSection()
    }

    private func chip(_ title: String, icon: String? = nil, selected: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 10) {
                if let icon { Image(systemName: icon).foregroundStyle(.yellow) }
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
            state.liveFavorites = Set(l.favorites ?? [])
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
        pick = ch.id
        state.liveRecents = [ch.id] + state.liveRecents.filter { $0 != ch.id }
        let ids = shown.map(\.id)
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
