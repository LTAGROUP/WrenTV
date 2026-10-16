import SwiftUI

struct RemoteImage: View {
    let url: URL?
    var contentMode: ContentMode = .fill

    var body: some View {
        AsyncImage(url: url, transaction: Transaction(animation: .easeOut(duration: 0.2))) { phase in
            if let image = phase.image {
                image.resizable().aspectRatio(contentMode: contentMode)
            } else {
                Rectangle().fill(Color.white.opacity(0.06))
            }
        }
    }
}

/// Playback progress as a thin bar floating over the bottom of artwork, the
/// way the TV app shows it.
struct ProgressStrip: View {
    let fraction: Double
    var body: some View {
        GeometryReader { g in
            ZStack(alignment: .leading) {
                Capsule().fill(.white.opacity(0.3))
                Capsule().fill(.white).frame(width: max(g.size.width * fraction, 4))
            }
        }
        .frame(height: 4)
        .padding(.horizontal, 10)
        .padding(.bottom, 9)
        .shadow(color: .black.opacity(0.4), radius: 3)
    }
}

/// "52m left" for something partly watched.
func timeLeft(_ p: WatchState) -> String? {
    let left = p.duration - p.position
    guard p.duration > 0, left > 60, !p.watched else { return nil }
    return "\(formatDuration(left)) left"
}

/// The main action: a white capsule with dark text, like Play in the TV app.
struct PrimaryCapsuleStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.body.weight(.semibold))
            .foregroundStyle(.black)
            .padding(.horizontal, 22).padding(.vertical, 9)
            .background(.white.opacity(configuration.isPressed ? 0.75 : 1), in: Capsule())
            .contentShape(Capsule())
    }
}

/// Secondary actions over artwork: frosted glass capsules.
struct GlassCapsuleStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.body.weight(.medium))
            .foregroundStyle(.white)
            .padding(.horizontal, 16).padding(.vertical, 9)
            .background(.ultraThinMaterial, in: Capsule())
            .overlay(Capsule().fill(.white.opacity(configuration.isPressed ? 0.15 : 0)))
            .environment(\.colorScheme, .dark)
            .contentShape(Capsule())
    }
}

extension ButtonStyle where Self == PrimaryCapsuleStyle {
    static var primaryCapsule: PrimaryCapsuleStyle { PrimaryCapsuleStyle() }
}

extension ButtonStyle where Self == GlassCapsuleStyle {
    static var glassCapsule: GlassCapsuleStyle { GlassCapsuleStyle() }
}

struct PosterCard: View {
    @Environment(AppState.self) private var state
    let item: Item
    var width: CGFloat = 150

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            ZStack(alignment: .topTrailing) {
                RemoteImage(url: state.api?.imageURL(item.posterPath))
                    .frame(width: width, height: width * 1.5)
                    .overlay {
                        if item.posterPath == nil {
                            Text(item.title).font(.headline).multilineTextAlignment(.center).padding(8)
                        }
                    }
                    .overlay(alignment: .bottom) {
                        if item.inProgress, let p = item.progress { ProgressStrip(fraction: p.fraction) }
                    }
                    .clipShape(RoundedRectangle(cornerRadius: 10))
                    .hoverLift(cornerRadius: 10)
                if item.isWatched {
                    Image(systemName: "checkmark.circle.fill")
                        .symbolRenderingMode(.palette)
                        .foregroundStyle(.white, Color.wrenEmber)
                        .padding(6)
                } else if let n = item.unwatchedCount, n > 0 {
                    Text("\(n)")
                        .font(.caption.bold())
                        .padding(.horizontal, 6).padding(.vertical, 2)
                        .background(Color.wrenEmber, in: Capsule())
                        .padding(6)
                }
            }
            Text(item.displayTitle).font(.callout).lineLimit(1)
            if item.kind == "show", let n = item.childCount, n > 0 {
                Text("\(n) episode\(n == 1 ? "" : "s")").font(.caption).foregroundStyle(.secondary)
            } else if let y = item.year, item.kind != "season" {
                Text(String(y)).font(.caption).foregroundStyle(.secondary)
            }
        }
        .frame(width: width)
        .contentShape(Rectangle())
    }
}

struct LandscapeCard: View {
    @Environment(AppState.self) private var state
    let item: Item
    var width: CGFloat = 280

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            RemoteImage(url: state.api?.imageURL(item.stillPath ?? item.backdropPath, size: "w500"))
                .frame(width: width, height: width * 9 / 16)
                .overlay(alignment: .bottom) {
                    if let p = item.progress, p.position > 0, !p.watched { ProgressStrip(fraction: p.fraction) }
                }
                .clipShape(RoundedRectangle(cornerRadius: 10))
                .hoverLift(cornerRadius: 10)
            let left = item.progress.flatMap(timeLeft)
            if item.kind == "episode" {
                Text(item.showTitle ?? item.title).font(.callout.weight(.medium)).lineLimit(1)
                Text([item.episodeLabel + " · " + item.title, left].compactMap { $0 }.joined(separator: " · "))
                    .font(.caption).foregroundStyle(.secondary).lineLimit(1)
            } else {
                Text(item.title).font(.callout.weight(.medium)).lineLimit(1)
                Text([item.year.map { String($0) }, left].compactMap { $0 }.joined(separator: " · "))
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
        .frame(width: width)
        .contentShape(Rectangle())
    }
}

/// A small quality or rating tag ("4K", "DOLBY VISION", "PG-13").
struct Badge: View {
    let text: String
    var body: some View {
        Text(text.uppercased())
            .font(.system(size: 10, weight: .bold))
            .kerning(0.4)
            .foregroundStyle(.white.opacity(0.9))
            .padding(.horizontal, 6).padding(.vertical, 3)
            .background(.white.opacity(0.16), in: RoundedRectangle(cornerRadius: 4))
    }
}

struct Shelf<Content: View>: View {
    let title: String
    var subtitle: String?
    @ViewBuilder let content: Content

    init(title: String, subtitle: String? = nil, @ViewBuilder content: () -> Content) {
        self.title = title
        self.subtitle = subtitle
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(title).font(.title3.bold())
                if let subtitle { Text(subtitle).font(.callout).foregroundStyle(.secondary) }
            }
            .padding(.horizontal, 28)
            // Vertical room inside the scroll view so hover rings and lifted
            // cards aren't clipped by its bounds; pulled back out so rows keep
            // their spacing.
            ScrollView(.horizontal, showsIndicators: false) {
                LazyHStack(alignment: .top, spacing: 16) { content }
                    .padding(.horizontal, 28)
                    .padding(.vertical, 14)
            }
            .padding(.vertical, -14)
        }
    }
}

func formatDuration(_ seconds: Double) -> String {
    let s = Int(seconds.rounded())
    let h = s / 3600, m = (s % 3600) / 60
    return h > 0 ? "\(h)h \(m)m" : "\(m)m"
}

func formatClock(_ seconds: Double) -> String {
    guard seconds.isFinite, seconds >= 0 else { return "0:00" }
    let s = Int(seconds)
    let h = s / 3600, m = (s % 3600) / 60, sec = s % 60
    return h > 0 ? String(format: "%d:%02d:%02d", h, m, sec) : String(format: "%d:%02d", m, sec)
}

extension Color {
    /// The Wren palette.
    static let wrenEmber = Color(red: 0xE0 / 255, green: 0x4A / 255, blue: 0x2A / 255)
    static let wrenInk = Color(red: 0x1C / 255, green: 0x1A / 255, blue: 0x21 / 255)
    static let wrenCream = Color(red: 0xF7 / 255, green: 0xF1 / 255, blue: 0xE8 / 255)
}
