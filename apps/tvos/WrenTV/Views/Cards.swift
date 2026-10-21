import SwiftUI

/// Pushes pages in the current tab, for actions that aren't links (menus).
@MainActor
@Observable
final class Router {
    var path = NavigationPath()
    func push<V: Hashable>(_ v: V) { path.append(v) }
}

/// A titled row of cards that scrolls sideways.
struct Shelf<Content: View>: View {
    let title: String
    var subtitle: String?
    @ViewBuilder var content: () -> Content

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            ShelfTitle(title: title, subtitle: subtitle)
            ScrollView(.horizontal) {
                LazyHStack(alignment: .top, spacing: TV.shelfSpacing) { content() }
                    .padding(.horizontal, TV.gutter)
                    .padding(.vertical, 34)
            }
            .scrollClipDisabled()
            .scrollIndicators(.hidden)
        }
        .focusSection()
    }
}

/// A shelf's name, and a line about it.
struct ShelfTitle: View {
    let title: String
    var subtitle: String?

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 18) {
            Text(title).font(.title3.bold())
            if let subtitle { Text(subtitle).font(.callout).foregroundStyle(.secondary) }
        }
        .padding(.horizontal, TV.gutter)
    }
}

/// A movie, show or season poster that opens its page.
struct PosterCard: View {
    @Environment(AppState.self) private var state
    @Environment(Router.self) private var router
    let item: Item
    var width: CGFloat = TV.poster

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            NavigationLink(value: ItemRoute(id: item.id, title: item.title)) {
                RemoteImage(url: state.api?.imageURL(item.posterPath ?? item.stillPath, size: "w342"))
                    .frame(width: width, height: width * 1.5)
                    .overlay(alignment: .bottom) {
                        if item.inProgress, let p = item.progress { ProgressStrip(fraction: p.fraction) }
                    }
                    .overlay(alignment: .topTrailing) { WatchedMark(item: item) }
            }
            .buttonStyle(.card)
            .contextMenu { ItemMenu(item: item) }
            .ambient { item.heroEntry }
            Text(item.displayTitle).font(.caption).lineLimit(1)
            if let y = item.year, item.kind != "season" {
                Text(String(y)).font(.caption2).foregroundStyle(.secondary)
            }
        }
        .frame(width: width)
    }
}

/// A watched check, or how many episodes are left.
struct WatchedMark: View {
    let item: Item
    var body: some View {
        if let n = item.unwatchedCount, n > 0, item.kind != "movie" {
            Text("\(n)")
                .font(.caption2.bold())
                .padding(.horizontal, 10).padding(.vertical, 4)
                .background(Color.wrenEmber, in: Capsule())
                .padding(12)
        } else if item.isWatched {
            Image(systemName: "checkmark.circle.fill")
                .font(.title3)
                .foregroundStyle(.white, Color.wrenEmber)
                .padding(12)
        }
    }
}

/// A wide card that plays straight away: Continue Watching and episodes.
struct LandscapeCard: View {
    @Environment(AppState.self) private var state
    let item: Item
    var width: CGFloat = TV.landscape
    /// Episodes in a season list show their number and name; elsewhere the show's name leads.
    var episodeList = false

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Button { state.play(item.playTarget) } label: {
                RemoteImage(url: state.api?.imageURL(item.stillPath ?? item.backdropPath ?? item.posterPath, size: "w780"))
                    .frame(width: width, height: width * 9 / 16)
                    .overlay(alignment: .bottom) {
                        if item.inProgress, let p = item.progress { ProgressStrip(fraction: p.fraction) }
                    }
                    .overlay(alignment: .topTrailing) { WatchedMark(item: item) }
            }
            .buttonStyle(.card)
            .contextMenu { ItemMenu(item: item, showDetails: true) }
            .ambient { item.heroEntry }
            Text(episodeList ? "\(item.episodeNumber). \(item.title)" : item.displayTitle(inShow: false))
                .font(.caption).lineLimit(1)
            Text(caption).font(.caption2).foregroundStyle(.secondary).lineLimit(episodeList ? 3 : 1)
        }
        .frame(width: width)
    }

    private var caption: String {
        if episodeList {
            var parts: [String] = []
            if let r = item.runtimeMin, r > 0 { parts.append(formatDuration(Double(r * 60))) }
            if let p = item.progress, let left = timeLeft(p) { parts.append(left) }
            let head = parts.joined(separator: " · ")
            guard let o = item.overview, !o.isEmpty else { return head }
            return head.isEmpty ? o : head + "\n" + o
        }
        var parts: [String] = []
        if item.kind == "episode" { parts.append("\(item.episodeLabel) — \(item.title)") }
        if let p = item.progress, let left = timeLeft(p) { parts.append(left) }
        return parts.joined(separator: " · ")
    }
}

extension Item {
    /// The show's name for an episode outside its show.
    func displayTitle(inShow: Bool) -> String {
        kind == "episode" && !inShow ? (showTitle ?? title) : displayTitle
    }
}

/// A TMDB title: its library page when the server has it, else the streaming page.
struct TitleCard: View {
    @Environment(AppState.self) private var state
    let title: TitleView
    var rank: Int?
    var width: CGFloat = TV.poster

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Group {
                if let item = title.item {
                    NavigationLink(value: ItemRoute(id: item.id, title: item.title)) { poster }
                } else if state.canStream {
                    NavigationLink(value: StreamTitleRoute(tmdbId: title.tmdbId, mediaType: title.mediaType, title: title.title)) { poster }
                } else {
                    Button {} label: { poster }
                }
            }
            .buttonStyle(.card)
            .ambient { title.heroEntry }
            Text(title.title).font(.caption).lineLimit(1)
            Text(title.item != nil ? "In your library" : (title.year.map(String.init) ?? " "))
                .font(.caption2).foregroundStyle(title.item != nil ? Color.wrenEmber : .secondary)
        }
        .frame(width: width)
    }

    private var poster: some View {
        RemoteImage(url: state.api?.imageURL(title.posterPath, size: "w342"))
            .frame(width: width, height: width * 1.5)
            .overlay(alignment: .bottomLeading) {
                if let rank {
                    Text("\(rank)")
                        .font(.system(size: 72, weight: .heavy))
                        .foregroundStyle(.white)
                        .shadow(color: .black.opacity(0.7), radius: 8)
                        .padding(.leading, 12)
                }
            }
    }
}

/// Long-press actions for an item.
struct ItemMenu: View {
    @Environment(AppState.self) private var state
    @Environment(Router.self) private var router
    let item: Item
    var showDetails = false

    var body: some View {
        if item.isPlayable {
            Button { state.play(item.playTarget) } label: { Label(item.inProgress ? "Resume" : "Play", systemImage: "play.fill") }
            if item.inProgress {
                Button {
                    var t = item.playTarget
                    t.fromStart = true
                    state.play(t)
                } label: { Label("Play from Beginning", systemImage: "backward.end.fill") }
            }
        }
        if showDetails {
            Button { router.push(ItemRoute(id: item.id, title: item.title)) } label: { Label("Details", systemImage: "info.circle") }
            if item.kind == "episode", let show = item.showId {
                Button { router.push(ItemRoute(id: show, title: item.showTitle ?? "")) } label: { Label("Go to Show", systemImage: "tv") }
            }
        }
        if item.kind != "season" {
            Button {
                Task {
                    try? await state.api?.setWatched(item.id, !item.isWatched)
                    state.refreshTick += 1
                }
            } label: {
                Label(item.isWatched ? "Mark as Unwatched" : "Mark as Watched", systemImage: item.isWatched ? "eye.slash" : "checkmark")
            }
        }
    }
}
