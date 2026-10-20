import SwiftUI

/// Movies and episodes saved on this Mac. Plays without a connection.
struct DownloadsView: View {
    @Environment(AppState.self) private var state
    @Environment(\.openWindow) private var openWindow
    @State private var confirmRemove: OfflineItem?

    private var downloads: DownloadManager { state.downloads }

    var body: some View {
        let movies = downloads.items.filter { !$0.isEpisode }.sorted { $0.title < $1.title }
        let shows = Dictionary(grouping: downloads.items.filter(\.isEpisode)) { $0.showTitle ?? "" }
            .sorted { $0.key < $1.key }
        PageScroll(maxWidth: 960) {
            if downloads.items.isEmpty {
                ContentUnavailableView("No downloads", systemImage: "arrow.down.circle",
                                       description: Text("Use Download on a movie, episode or season to watch it without a connection."))
                    .frame(maxWidth: .infinity)
                    .padding(.top, 80)
            }
            if !movies.isEmpty {
                CardSection("Movies", subtitle: count(movies.count, "movie")) { ForEach(movies) { row($0) } }
            }
            ForEach(shows, id: \.key) { show, eps in
                CardSection(show, subtitle: count(eps.count, "episode")) {
                    ForEach(eps.sorted { ($0.seasonNumber, $0.episodeNumber) < ($1.seasonNumber, $1.episodeNumber) }) { row($0) }
                }
            }
        }
        .navigationTitle("Downloads")
        .navigationSubtitle(downloads.usedBytes > 0 ? ByteCountFormatter.string(fromByteCount: downloads.usedBytes, countStyle: .file) + " on this Mac" : "")
        .toolbar {
            Button { NSWorkspace.shared.open(downloads.folder) } label: { Label("Show in Finder", systemImage: "folder") }
                .help("Open the downloads folder")
        }
        .confirmationDialog("Remove \(confirmRemove?.displayTitle ?? "")?", isPresented: Binding(get: { confirmRemove != nil }, set: { if !$0 { confirmRemove = nil } })) {
            Button("Remove Download", role: .destructive) {
                if let x = confirmRemove { downloads.remove(x.id) }
            }
        } message: {
            Text("The copy on this Mac is deleted. It stays on the server.")
        }
    }

    private func count(_ n: Int, _ noun: String) -> String { "\(n) \(noun)\(n == 1 ? "" : "s")" }

    private func row(_ x: OfflineItem) -> some View {
        HStack(spacing: 14) {
            poster(x)
            VStack(alignment: .leading, spacing: 4) {
                Text(x.isEpisode ? "S\(x.seasonNumber) · E\(x.episodeNumber) — \(x.title)" : x.title).font(.headline).lineLimit(1)
                statusLine(x)
                if x.active || x.status == .paused, x.size > 0 {
                    ProgressView(value: x.fraction).tint(Color.wrenEmber).frame(maxWidth: 320)
                }
            }
            Spacer()
            actions(x)
        }
        .contentShape(Rectangle())
        .onTapGesture(count: 2) { play(x) }
        .contextMenu {
            if x.status == .done {
                Button("Play") { play(x) }
                Button("Play from Beginning") { play(x, fromStart: true) }
                Button("Show in Finder") { downloads.revealInFinder(x) }
                Divider()
            }
            Button("Remove Download", role: .destructive) { confirmRemove = x }
        }
    }

    private func poster(_ x: OfflineItem) -> some View {
        let w: CGFloat = x.isEpisode ? 112 : 46
        return Group {
            if let url = downloads.posterURL(x), let img = NSImage(contentsOf: url) {
                Image(nsImage: img).resizable().aspectRatio(contentMode: .fill)
            } else {
                Rectangle().fill(.white.opacity(0.06))
                    .overlay(Image(systemName: x.isEpisode ? "tv" : "film").foregroundStyle(.secondary))
            }
        }
        .frame(width: w, height: x.isEpisode ? 63 : 69)
        .clipShape(RoundedRectangle(cornerRadius: 6))
    }

    @ViewBuilder
    private func statusLine(_ x: OfflineItem) -> some View {
        let size = x.size > 0 ? ByteCountFormatter.string(fromByteCount: x.size, countStyle: .file) : nil
        Group {
            switch x.status {
            case .done:
                Text([x.label, size, x.position > 30 && x.duration > 0 ? "\(Int(x.position / x.duration * 100))% watched" : nil]
                    .compactMap { $0 }.joined(separator: " · "))
            case .downloading:
                Text("\(ByteCountFormatter.string(fromByteCount: x.received, countStyle: .file))\(size.map { " of \($0)" } ?? "")")
            case .preparing:
                Text(x.message ?? "Preparing…")
            case .waiting:
                Text(state.api == nil ? "Waiting for the server" : "Queued")
            case .paused:
                Text(x.message ?? "Paused")
            case .failed:
                Text(x.message ?? "Failed").foregroundStyle(.red)
            }
        }
        .font(.caption)
        .foregroundStyle(.secondary)
        .lineLimit(1)
    }

    @ViewBuilder
    private func actions(_ x: OfflineItem) -> some View {
        HStack(spacing: 8) {
            switch x.status {
            case .done:
                Button { play(x) } label: { Label("Play", systemImage: "play.fill") }
                    .buttonStyle(.primaryCapsule)
            case .downloading, .preparing, .waiting:
                Button { downloads.pause(x.id) } label: { Image(systemName: "pause.fill") }
                    .buttonStyle(.iconCircle)
                    .help("Pause")
            case .paused, .failed:
                Button { downloads.resume(x.id) } label: { Image(systemName: x.status == .failed ? "arrow.clockwise" : "arrow.down") }
                    .buttonStyle(.iconCircle)
                    .help(x.status == .failed ? "Retry" : "Resume")
                    .disabled(state.api == nil)
            }
            Button(role: .destructive) { confirmRemove = x } label: { Image(systemName: "trash") }
                .buttonStyle(.iconCircle)
                .help("Remove download")
        }
    }

    private func play(_ x: OfflineItem, fromStart: Bool = false) {
        guard let url = downloads.localURL(x) else { return }
        openWindow(id: "player", value: PlayTarget(itemId: x.id, title: x.displayTitle, subtitle: x.subtitle, fromStart: fromStart,
                                                    localFile: url.path, localPosition: x.position))
    }
}

/// Download controls for a movie or episode page.
struct DownloadButton: View {
    @Environment(AppState.self) private var state
    @Environment(\.openWindow) private var openWindow
    let item: Item
    var show: Item?
    var fileId: String?

    var body: some View {
        let x = state.downloads.item(item.id)
        Group {
            if let x, x.status == .done {
                Menu {
                    Button("Play Downloaded Copy") { playLocal(x) }
                    Button("Show in Finder") { state.downloads.revealInFinder(x) }
                    Divider()
                    Button("Remove Download", role: .destructive) { state.downloads.remove(x.id) }
                } label: {
                    Label("Downloaded", systemImage: "checkmark.circle.fill")
                }
                .menuStyle(.button)
                .fixedSize()
            } else if let x, x.active || x.status == .waiting {
                Button { state.downloads.pause(x.id) } label: {
                    HStack(spacing: 8) {
                        ProgressView(value: x.status == .downloading ? x.fraction : nil)
                            .progressViewStyle(.circular).controlSize(.small)
                        Text(x.status == .downloading ? "\(Int(x.fraction * 100))%" : (x.message ?? "Preparing…")).monospacedDigit()
                    }
                }
                .buttonStyle(.glassCapsule)
                .help("Downloading to this Mac — click to pause")
            } else {
                Button {
                    state.downloads.add(item, show: show, fileId: fileId, server: state.server?.name ?? "")
                } label: {
                    Label(x?.status == .failed ? "Retry Download" : x?.status == .paused ? "Resume Download" : "Download",
                          systemImage: "arrow.down.circle")
                }
                .buttonStyle(.glassCapsule)
                .help(x?.message ?? "Save to this Mac to watch without a connection")
            }
        }
    }

    private func playLocal(_ x: OfflineItem) {
        guard let url = state.downloads.localURL(x) else { return }
        openWindow(id: "player", value: PlayTarget(itemId: x.id, title: x.displayTitle, subtitle: x.subtitle, localFile: url.path, localPosition: x.position))
    }
}
