import SwiftUI

/// Which channel to open in a live player window, and the channels its
/// channel up and down step through.
struct LiveTarget: Codable, Hashable {
    var channelId: String
    var group: String?
}

/// Live TV as a guide: a search box, the provider's groups as chips, and a
/// grid of what's on per channel with a now line. Channels airing something
/// come first.
struct LiveTVView: View {
    @Environment(AppState.self) private var state
    @Environment(\.openWindow) private var openWindow
    @State private var guide: [String: [LiveProgram]] = [:]
    @State private var loaded = false
    @State private var error: String?
    @State private var filter = ""
    @State private var group: String?
    /// The left edge of the grid; paged half an hour at a time.
    @State private var windowStart = LiveTVView.halfHour(Date())
    @State private var now = Date()

    static let span: TimeInterval = 2 * 3600
    static let channelColumn: CGFloat = 230
    static let rowHeight: CGFloat = 62

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

    private var searching: Bool { !filter.trimmingCharacters(in: .whitespaces).isEmpty }

    private var shown: [LiveChannel] {
        let q = filter.trimmingCharacters(in: .whitespaces)
        // A search looks across every channel, whatever group is picked.
        let list = channels.filter { c in
            guard !q.isEmpty else { return group == nil || c.group == group }
            return c.name.localizedCaseInsensitiveContains(q) || c.number == q
                || (c.group ?? "").localizedCaseInsensitiveContains(q)
                || (guide[c.id] ?? []).contains { $0.title.localizedCaseInsensitiveContains(q) }
                || (c.now?.title ?? "").localizedCaseInsensitiveContains(q)
        }
        // On-air channels first, otherwise in the provider's order.
        let airing = list.filter { program(on: $0, at: now) != nil }
        return airing + list.filter { program(on: $0, at: now) == nil }
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
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
            } else if shown.isEmpty {
                ContentUnavailableView.search(text: filter).frame(maxHeight: .infinity)
            } else {
                grid
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
    }

    // MARK: Header

    private var header: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 10) {
                HStack(spacing: 6) {
                    Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
                    TextField("Search all channels and shows", text: $filter).textFieldStyle(.plain)
                    if !filter.isEmpty {
                        Button { filter = "" } label: { Image(systemName: "xmark.circle.fill").foregroundStyle(.secondary) }
                            .buttonStyle(.plain)
                    }
                }
                .padding(.horizontal, 10)
                .frame(width: 320, height: 28)
                .background(.white.opacity(0.07), in: Capsule())
                Spacer()
                Button { page(-1) } label: { Image(systemName: "chevron.left") }
                    .help("Earlier")
                    .disabled(windowStart <= Self.halfHour(now))
                Button("Now") { windowStart = Self.halfHour(now) }
                    .disabled(windowStart == Self.halfHour(now))
                Button { page(1) } label: { Image(systemName: "chevron.right") }
                    .help("Later")
            }
            if groups.count > 1 && !searching {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 8) {
                        chip("All", selected: group == nil) { group = nil }
                        ForEach(groups, id: \.self) { g in chip(g, selected: group == g) { group = g } }
                    }
                }
            }
        }
        .padding(.horizontal, 20)
        .padding(.vertical, 12)
    }

    private func chip(_ title: String, selected: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(title).font(.callout)
                .padding(.horizontal, 12).padding(.vertical, 5)
                .foregroundStyle(selected ? Color.black : Color.primary)
                .background(selected ? AnyShapeStyle(Color.white) : AnyShapeStyle(Color.white.opacity(0.08)), in: Capsule())
        }
        .buttonStyle(.plain)
    }

    private func page(_ dir: Int) {
        let next = windowStart.addingTimeInterval(Double(dir) * 1800)
        windowStart = max(Self.halfHour(now), next)
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
                .overlay(alignment: .topLeading) { nowLine(width: width, height: geo.size.height) }
            }
        }
    }

    private func x(_ d: Date, _ width: CGFloat) -> CGFloat {
        CGFloat(d.timeIntervalSince(windowStart) / Self.span) * width
    }

    private func ruler(width: CGFloat) -> some View {
        HStack(spacing: 0) {
            Text(windowStart.formatted(.dateTime.weekday(.wide).month().day()))
                .font(.caption.bold()).foregroundStyle(.secondary)
                .frame(width: Self.channelColumn, alignment: .leading)
                .padding(.leading, 20)
                .offset(x: -10)
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
    private func nowLine(width: CGFloat, height: CGFloat) -> some View {
        let px = x(now, width)
        if px >= 0 && px <= width {
            Rectangle().fill(Color.wrenEmber)
                .frame(width: 2)
                .frame(maxHeight: .infinity)
                .offset(x: Self.channelColumn + px)
                .allowsHitTesting(false)
        }
    }

    private func row(_ ch: LiveChannel, width: CGFloat) -> some View {
        HStack(spacing: 0) {
            Button { play(ch) } label: { channelCell(ch) }
                .buttonStyle(.plain)
                .help("Watch \(ch.name)")
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
    }

    private func channelCell(_ ch: LiveChannel) -> some View {
        HStack(spacing: 10) {
            ChannelLogo(url: state.api?.liveLogoURL(ch.logo), name: ch.name)
                .frame(width: 56, height: 36)
            VStack(alignment: .leading, spacing: 2) {
                Text(ch.name).font(.callout.weight(.semibold)).lineLimit(1)
                if let n = ch.number { Text(n).font(.caption).foregroundStyle(.secondary) }
            }
            Spacer(minLength: 0)
        }
        .padding(.leading, 20)
        .padding(.trailing, 8)
        .frame(width: Self.channelColumn, height: Self.rowHeight)
        .contentShape(Rectangle())
    }

    private func programBlock(_ ch: LiveChannel, _ p: LiveProgram?, x: CGFloat, w: CGFloat) -> some View {
        let airing = p.map { $0.startDate <= now && now < $0.endDate } ?? true
        return Button { play(ch) } label: {
            VStack(alignment: .leading, spacing: 2) {
                Text(p?.title ?? ch.name).font(.callout.weight(airing ? .semibold : .regular)).lineLimit(1)
                if let p {
                    Text(p.episode.map { "\($0) · \(p.timeRange)" } ?? p.timeRange)
                        .font(.caption).foregroundStyle(.secondary).lineLimit(1)
                } else {
                    Text("No guide").font(.caption).foregroundStyle(.secondary)
                }
            }
            .padding(.horizontal, 10)
            .frame(width: max(w - 4, 0), height: Self.rowHeight - 10, alignment: .leading)
            .background(airing ? Color.white.opacity(0.12) : Color.white.opacity(0.05), in: RoundedRectangle(cornerRadius: 7))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(p?.description ?? p?.title ?? ch.name)
        .offset(x: x + 2, y: 5)
    }

    private func visiblePrograms(_ ch: LiveChannel) -> [LiveProgram] {
        let end = windowStart.addingTimeInterval(Self.span)
        var ps = guide[ch.id] ?? [ch.now, ch.next].compactMap { $0 }
        ps = ps.filter { $0.endDate > windowStart && $0.startDate < end }
        return ps
    }

    private func program(on ch: LiveChannel, at t: Date) -> LiveProgram? {
        (guide[ch.id] ?? [ch.now].compactMap { $0 }).first { $0.startDate <= t && t < $0.endDate }
    }

    // MARK: Data

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
        openWindow(id: "live", value: LiveTarget(channelId: ch.id, group: searching ? nil : group))
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
