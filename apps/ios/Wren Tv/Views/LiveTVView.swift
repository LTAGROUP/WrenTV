import SwiftUI

/// Which channel the live player opens on, and the channels its channel up
/// and down step through.
struct LiveTarget: Hashable, Identifiable {
    var channelId: String
    var group: String?
    var id: String { channelId }
}

/// Live TV: the provider's groups as chips, a search over channels and
/// shows, and what's on now and next per channel. Channels airing
/// something come first.
struct LiveTVView: View {
    @Environment(AppState.self) private var state
    @Environment(\.metrics) private var m
    @State private var guide: [String: [LiveProgram]] = [:]
    @State private var loaded = false
    @State private var error: String?
    @State private var filter = ""
    @State private var group: String?
    @State private var now = Date()
    @State private var schedule: LiveChannel?

    private var channels: [LiveChannel] { state.liveChannels }

    private var groups: [String] {
        var seen = Set<String>(), out: [String] = []
        for c in channels { if let g = c.group, !g.isEmpty, seen.insert(g).inserted { out.append(g) } }
        return out
    }

    private var searching: Bool { !filter.trimmingCharacters(in: .whitespaces).isEmpty }

    private var shown: [LiveChannel] {
        let q = filter.trimmingCharacters(in: .whitespaces)
        // A search looks across every channel, whatever group is picked.
        let list = channels.filter { c in
            guard !q.isEmpty else { return group == nil || c.group == group }
            return c.name.localizedCaseInsensitiveContains(q) || c.number == q
                || (c.group ?? "").localizedCaseInsensitiveContains(q)
                || programs(c).contains { $0.title.localizedCaseInsensitiveContains(q) }
        }
        let airing = list.filter { onNow($0) != nil }
        return airing + list.filter { onNow($0) == nil }
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
            } else {
                list
            }
        }
        .navigationTitle("Live TV")
        .searchable(text: $filter, placement: .navigationBarDrawer(displayMode: .always), prompt: "Search all channels and shows")
        .refreshable { await load() }
        .task(id: state.refreshTick) { await load() }
        .task {
            var ticks = 0
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(30))
                now = Date()
                ticks += 1
                if ticks % 10 == 0 { await load() }
            }
        }
        .sheet(item: $schedule) { ch in
            ScheduleSheet(channel: ch, programs: programs(ch), now: now) {
                schedule = nil
                play(ch)
            }
            .presentationDetents([.medium, .large])
        }
    }

    private var list: some View {
        List {
            if groups.count > 1 && !searching {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 8) {
                        chip("All", selected: group == nil) { group = nil }
                        ForEach(groups, id: \.self) { g in chip(g, selected: group == g) { group = g } }
                    }
                    .padding(.horizontal, m.gutter)
                }
                .listRowInsets(EdgeInsets(top: 4, leading: 0, bottom: 8, trailing: 0))
                .listRowSeparator(.hidden)
                .listRowBackground(Color.clear)
            }
            ForEach(shown) { ch in
                Button { play(ch) } label: { row(ch) }
                    .buttonStyle(.plain)
                    .swipeActions(edge: .trailing) {
                        Button { schedule = ch } label: { Label("Schedule", systemImage: "list.bullet.rectangle") }
                    }
                    .contextMenu {
                        Button("Watch", systemImage: "play.fill") { play(ch) }
                        Button("Schedule", systemImage: "list.bullet.rectangle") { schedule = ch }
                    }
            }
            if shown.isEmpty && !filter.isEmpty {
                ContentUnavailableView.search(text: filter).listRowBackground(Color.clear)
            }
        }
        .listStyle(.plain)
    }

    private func chip(_ title: String, selected: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(title).font(.subheadline)
                .padding(.horizontal, 12).padding(.vertical, 6)
                .foregroundStyle(selected ? Color.black : Color.primary)
                .background(selected ? AnyShapeStyle(Color.white) : AnyShapeStyle(Color.white.opacity(0.1)), in: Capsule())
        }
        .buttonStyle(.plain)
    }

    private func row(_ ch: LiveChannel) -> some View {
        let current = onNow(ch)
        let next = programs(ch).first { $0.startDate > now }
        return HStack(spacing: 12) {
            ChannelLogo(url: state.api?.liveLogoURL(ch.logo), name: ch.name)
                .frame(width: 64, height: 40)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    if let n = ch.number { Text(n).font(.caption.monospacedDigit()).foregroundStyle(.secondary) }
                    Text(ch.name).font(.subheadline.weight(.semibold)).lineLimit(1)
                }
                if let current {
                    Text(current.title).font(.subheadline).lineLimit(1)
                    HStack(spacing: 8) {
                        ProgressView(value: current.progress(at: now)).tint(.white).frame(maxWidth: 120)
                        Text(current.timeRange).font(.caption2).foregroundStyle(.secondary).lineLimit(1)
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

    private func programs(_ ch: LiveChannel) -> [LiveProgram] {
        guide[ch.id] ?? [ch.now, ch.next].compactMap { $0 }
    }

    private func onNow(_ ch: LiveChannel) -> LiveProgram? {
        programs(ch).first { $0.startDate <= now && now < $0.endDate }
    }

    private func load() async {
        guard let api = state.api else { return }
        do {
            async let chs = api.liveChannels()
            async let g = api.liveGuide(hours: 12)
            state.liveChannels = try await chs
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

    private func play(_ ch: LiveChannel) {
        state.playingLive = LiveTarget(channelId: ch.id, group: searching ? nil : group)
    }
}

/// What a channel is showing over the next hours.
private struct ScheduleSheet: View {
    @Environment(AppState.self) private var state
    let channel: LiveChannel
    let programs: [LiveProgram]
    let now: Date
    let watch: () -> Void

    var body: some View {
        NavigationStack {
            List {
                if programs.isEmpty {
                    Text("This channel has no guide.").foregroundStyle(.secondary)
                }
                ForEach(programs.filter { $0.endDate > now }, id: \.start) { p in
                    let airing = p.startDate <= now
                    VStack(alignment: .leading, spacing: 4) {
                        HStack {
                            Text(p.startDate.formatted(date: .omitted, time: .shortened))
                                .font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                            if airing {
                                Text("ON NOW").font(.caption2.bold()).foregroundStyle(Color.wrenEmber)
                            }
                        }
                        Text(p.title).font(.headline)
                        if let e = p.episode { Text(e).font(.caption).foregroundStyle(.secondary) }
                        if let d = p.description, !d.isEmpty {
                            Text(d).font(.subheadline).foregroundStyle(.secondary).lineLimit(3)
                        }
                        if airing { ProgressView(value: p.progress(at: now)).tint(.white) }
                    }
                    .padding(.vertical, 2)
                }
            }
            .navigationTitle(channel.name)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    ChannelLogo(url: state.api?.liveLogoURL(channel.logo), name: channel.name).frame(width: 48, height: 30)
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Watch", systemImage: "play.fill", action: watch)
                }
            }
        }
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
