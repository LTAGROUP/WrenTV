import ImageIO
import SwiftUI

/// Sizes that change between iPhone (compact width) and iPad.
struct Metrics {
    var compact: Bool

    /// Space between the screen edge and content.
    var gutter: CGFloat { compact ? 16 : 28 }
    /// Posters and tiles scale with the Poster Size setting.
    var poster: CGFloat { (compact ? 112 : 150) * PosterSize.current.scale }
    var landscape: CGFloat { (compact ? 250 : 280) * PosterSize.current.scale }
    var heroHeight: CGFloat { compact ? 500 : 560 }
    var heroTitle: CGFloat { compact ? 32 : 44 }
    var cardSpacing: CGFloat { compact ? 12 : 16 }
    /// Columns for grids of posters.
    var columns: [GridItem] {
        let k = PosterSize.current.scale
        return compact ? [GridItem(.adaptive(minimum: 100 * k, maximum: 160 * k), spacing: 12)]
                       : [GridItem(.adaptive(minimum: 150 * k, maximum: 180 * k), spacing: 22)]
    }
    var rowSpacing: CGFloat { compact ? 18 : 26 }
}

extension EnvironmentValues {
    var metrics: Metrics { Metrics(compact: horizontalSizeClass == .compact) }
}

struct RemoteImage: View {
    let url: URL?
    var contentMode: ContentMode = .fill
    @State private var loaded: (url: URL, image: CGImage)?

    var body: some View {
        if contentMode == .fill {
            // Takes exactly the space it's offered and crops; a filled image
            // left to size itself grows wider than the screen.
            Color.clear
                .overlay { image }
                .clipped()
        } else {
            image
        }
    }

    private var image: some View {
        let image = url.flatMap { u in loaded?.url == u ? loaded?.image : ArtworkCache.shared.cached(u) }
        return ZStack {
            if let image {
                Image(decorative: image, scale: 1).resizable().aspectRatio(contentMode: contentMode)
            } else {
                Rectangle().fill(Color.white.opacity(0.06))
            }
        }
        .task(id: url) {
            guard let url, ArtworkCache.shared.cached(url) == nil else { return }
            if let img = await ArtworkCache.shared.image(url), !Task.isCancelled {
                withAnimation(.easeOut(duration: 0.2)) { loaded = (url, img) }
            }
        }
    }
}

/// Decoded artwork by URL. AsyncImage keeps nothing, so every card scrolling
/// back into view fetched and decoded its art again; here images are fetched
/// and decoded off the main thread, once each however many cards ask.
final class ArtworkCache: @unchecked Sendable {
    static let shared = ArtworkCache()
    private let cache = NSCache<NSURL, CGImage>()
    private let lock = NSLock()
    private var loads: [URL: Task<CGImage?, Never>] = [:]

    init() { cache.totalCostLimit = 200 << 20 }

    func cached(_ url: URL) -> CGImage? { cache.object(forKey: url as NSURL) }

    func image(_ url: URL) async -> CGImage? {
        if let img = cached(url) { return img }
        let task = lock.withLock {
            if let t = loads[url] { return t }
            let t = Task.detached(priority: .userInitiated) { [self] () -> CGImage? in
                defer { _ = lock.withLock { loads.removeValue(forKey: url) } }
                guard let (data, resp) = try? await URLSession.shared.data(from: url),
                      (resp as? HTTPURLResponse)?.statusCode ?? 200 < 400,
                      let img = Self.decode(data) else { return nil }
                cache.setObject(img, forKey: url as NSURL, cost: img.bytesPerRow * img.height)
                return img
            }
            loads[url] = t
            return t
        }
        return await task.value
    }

    /// Decodes now rather than when first drawn, which would be on the main thread.
    private static func decode(_ data: Data) -> CGImage? {
        guard let src = CGImageSourceCreateWithData(data as CFData, [kCGImageSourceShouldCache: false] as CFDictionary) else { return nil }
        return CGImageSourceCreateImageAtIndex(src, 0, [kCGImageSourceShouldCacheImmediately: true] as CFDictionary)
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
    @Environment(\.metrics) private var m
    let item: Item
    var width: CGFloat?
    /// Fills its grid column instead of a fixed width.
    var fill = false

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            ZStack(alignment: .topTrailing) {
                Color.clear
                    .aspectRatio(2 / 3, contentMode: .fit)
                    .overlay { RemoteImage(url: state.api?.imageURL(item.posterPath)) }
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
        .frame(width: fill ? nil : (width ?? m.poster))
        .frame(maxWidth: fill ? .infinity : nil, alignment: .leading)
        .contentShape(Rectangle())
    }
}

struct LandscapeCard: View {
    @Environment(AppState.self) private var state
    @Environment(\.metrics) private var m
    let item: Item
    var width: CGFloat?
    var fill = false

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Color.clear
                .aspectRatio(16 / 9, contentMode: .fit)
                .overlay { RemoteImage(url: state.api?.imageURL(item.stillPath ?? item.backdropPath, size: "w500")) }
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
        .frame(width: fill ? nil : (width ?? m.landscape))
        .frame(maxWidth: fill ? .infinity : nil, alignment: .leading)
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
    @Environment(\.metrics) private var m
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
            .padding(.horizontal, m.gutter)
            // Vertical room inside the scroll view so hover rings and lifted
            // cards aren't clipped by its bounds; pulled back out so rows keep
            // their spacing.
            ScrollView(.horizontal, showsIndicators: false) {
                LazyHStack(alignment: .top, spacing: m.cardSpacing) { content }
                    .padding(.horizontal, m.gutter)
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

/// Modifiers that only exist on newer iOS versions. Older versions skip them.
extension View {
    /// The subtitle under a large navigation title (iOS 26+).
    @ViewBuilder func navigationSubtitleIfAvailable(_ subtitle: String) -> some View {
        if #available(iOS 26, *) { navigationSubtitle(subtitle) } else { self }
    }

    /// Shrinks the tab bar while scrolling down (iOS 26+).
    @ViewBuilder func minimizesTabBarOnScroll() -> some View {
        if #available(iOS 26, *) { tabBarMinimizeBehavior(.onScrollDown) } else { self }
    }
}
