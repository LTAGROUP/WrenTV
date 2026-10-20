import SwiftUI

/// Live TV: the provider's groups across the top, then each channel with
/// what's on now and next. Channels airing something come first.
struct LiveTVView: View {
    @Environment(AppState.self) private var state
    @State private var group: String?
    @State private var loaded = false
    @State private var error: String?
    @State private var now = Date()

    private var groups: [String] {
        var seen = Set<String>(), out: [String] = []
        for c in state.liveChannels { if let g = c.group, !g.isEmpty, seen.insert(g).inserted { out.append(g) } }
        return out
    }

    private var shown: [LiveChannel] {
        let list = group.map { g in state.liveChannels.filter { $0.group == g } } ?? state.liveChannels
        return list.filter { $0.now != nil } + list.filter { $0.now == nil }
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 30) {
                Text("Live TV").font(.title2.bold()).padding(.horizontal, TV.gutter)
                if groups.count > 1 {
                    ScrollView(.horizontal) {
                        HStack(spacing: 20) {
                            chip("All", selected: group == nil) { group = nil }
                            ForEach(groups, id: \.self) { g in chip(g, selected: group == g) { group = g } }
                        }
                        .padding(.horizontal, TV.gutter)
                        .padding(.vertical, 16)
                    }
                    .scrollClipDisabled()
                    .focusSection()
                }
                if !shown.isEmpty {
                    LazyVStack(spacing: 18) {
                        ForEach(shown) { ChannelRow(channel: $0, now: now, group: group) }
                    }
                    .padding(.horizontal, TV.gutter)
                    .focusSection()
                } else if let error {
                    EmptyState(title: "Couldn't load channels", message: error, symbol: "wifi.exclamationmark")
                } else if loaded {
                    EmptyState(title: "No channels", symbol: "dot.radiowaves.left.and.right")
                } else {
                    ProgressView().frame(maxWidth: .infinity).padding(.top, 200)
                }
            }
            .padding(.vertical, 40)
        }
        .task {
            await load()
            // Keep what's on now current.
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(60))
                now = Date()
                if state.liveChannels.contains(where: { ($0.now?.endDate ?? .distantFuture) < now }) { await load() }
            }
        }
    }

    private func chip(_ title: String, selected: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(title).fontWeight(selected ? .bold : .regular).foregroundStyle(selected ? Color.wrenEmber : .primary)
        }
        .buttonStyle(.bordered)
    }

    private func load() async {
        guard let api = state.api else { return }
        do {
            state.liveChannels = try await api.liveChannels()
            error = nil
        } catch {
            if state.liveChannels.isEmpty { self.error = error.localizedDescription }
        }
        loaded = true
    }
}

private struct ChannelRow: View {
    @Environment(AppState.self) private var state
    let channel: LiveChannel
    let now: Date
    let group: String?

    var body: some View {
        Button { state.playingLive = LiveTarget(channelId: channel.id, group: group) } label: {
            HStack(spacing: 30) {
                RemoteImage(url: state.api?.liveLogoURL(channel.logo), contentMode: .fit)
                    .frame(width: 140, height: 80)
                    .background(Color.white.opacity(0.05), in: RoundedRectangle(cornerRadius: 10))
                VStack(alignment: .leading, spacing: 6) {
                    HStack(spacing: 14) {
                        if let n = channel.number { Text(n).foregroundStyle(.secondary) }
                        Text(channel.name).font(.headline).lineLimit(1)
                    }
                    if let p = channel.now {
                        Text(p.title).font(.callout).lineLimit(1)
                        ProgressView(value: p.progress(at: now)).tint(Color.wrenEmber).frame(maxWidth: 500)
                    }
                }
                Spacer()
                VStack(alignment: .trailing, spacing: 6) {
                    if let p = channel.now { Text(p.timeRange).font(.caption).foregroundStyle(.secondary) }
                    if let n = channel.next { Text("Next: \(n.title)").font(.caption).foregroundStyle(.secondary).lineLimit(1) }
                }
                .frame(maxWidth: 520, alignment: .trailing)
            }
            .padding(.vertical, 8)
        }
    }
}
