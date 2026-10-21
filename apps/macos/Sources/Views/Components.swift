import ImageIO
import SwiftUI

/// Artwork, drawn from memory when it has been seen before. AsyncImage keeps
/// nothing, so every card scrolling back into view fetched and decoded its
/// art again on the way to the screen.
struct RemoteImage: View {
    let url: URL?
    var contentMode: ContentMode = .fill
    @State private var loaded: (url: URL, image: CGImage)?

    var body: some View {
        let image = url.flatMap { u in loaded?.url == u ? loaded?.image : ArtworkCache.shared.cached(u) }
        ZStack {
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

/// Decoded artwork by URL. Images are fetched and decoded off the main
/// thread, once each however many cards ask at the same time.
final class ArtworkCache: @unchecked Sendable {
    static let shared = ArtworkCache()
    private let cache = NSCache<NSURL, CGImage>()
    private let lock = NSLock()
    private var loads: [URL: Task<CGImage?, Never>] = [:]

    init() { cache.totalCostLimit = 320 << 20 }

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

/// Small actions inside a card ("Scan", "Edit", "Check"): a faint capsule,
/// red text when destructive.
struct SoftCapsuleStyle: ButtonStyle {
    @Environment(\.isEnabled) private var enabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.callout.weight(.medium))
            .foregroundStyle(configuration.role == .destructive ? Color.red : Color.primary)
            .padding(.horizontal, 12).padding(.vertical, 5)
            .background(.white.opacity(configuration.isPressed ? 0.16 : 0.08), in: Capsule())
            .opacity(enabled ? 1 : 0.4)
            .contentShape(Capsule())
    }
}

/// An icon-only action in a card (pause, remove, reorder): a faint circle.
struct IconCircleStyle: ButtonStyle {
    @Environment(\.isEnabled) private var enabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.callout.weight(.medium))
            .foregroundStyle(configuration.role == .destructive ? Color.red.opacity(0.9) : Color.primary)
            .frame(width: 28, height: 28)
            .background(.white.opacity(configuration.isPressed ? 0.16 : 0.07), in: Circle())
            .opacity(enabled ? 1 : 0.35)
            .contentShape(Circle())
    }
}

extension ButtonStyle where Self == PrimaryCapsuleStyle {
    static var primaryCapsule: PrimaryCapsuleStyle { PrimaryCapsuleStyle() }
}

extension ButtonStyle where Self == GlassCapsuleStyle {
    static var glassCapsule: GlassCapsuleStyle { GlassCapsuleStyle() }
}

extension ButtonStyle where Self == SoftCapsuleStyle {
    static var softCapsule: SoftCapsuleStyle { SoftCapsuleStyle() }
}

extension ButtonStyle where Self == IconCircleStyle {
    static var iconCircle: IconCircleStyle { IconCircleStyle() }
}

/// A filter or tab pill: white when chosen, faint otherwise.
struct Chip: View {
    let title: String
    let selected: Bool
    let action: () -> Void

    init(_ title: String, selected: Bool, action: @escaping () -> Void) {
        self.title = title
        self.selected = selected
        self.action = action
    }

    var body: some View {
        Button(action: action) {
            Text(title).font(.callout)
                .padding(.horizontal, 12).padding(.vertical, 5)
                .foregroundStyle(selected ? Color.black : Color.primary)
                .background(selected ? AnyShapeStyle(Color.white) : AnyShapeStyle(Color.white.opacity(0.08)), in: Capsule())
                .contentShape(Capsule())
        }
        .buttonStyle(.plain)
    }
}

extension View {
    /// The faint fill and hairline every card in the app sits on.
    func cardBackground(cornerRadius: CGFloat = 12) -> some View {
        background(.white.opacity(0.04), in: RoundedRectangle(cornerRadius: cornerRadius))
            .overlay(RoundedRectangle(cornerRadius: cornerRadius).stroke(.white.opacity(0.08)))
    }

    /// A text field drawn like the Live TV search box rather than the
    /// system's bezel.
    func wrenField() -> some View {
        textFieldStyle(.plain)
            .padding(.horizontal, 10)
            .frame(height: 28)
            .background(.white.opacity(0.07), in: RoundedRectangle(cornerRadius: 7))
    }
}

/// A titled card, as on Stats.
struct Panel<Content: View>: View {
    let title: String
    @ViewBuilder var content: Content

    init(_ title: String, @ViewBuilder content: () -> Content) {
        self.title = title
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(title).font(.headline)
            content
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(16)
        .cardBackground()
    }
}

/// A heading like a Home row's over a card of rows split by hairlines, with
/// an optional note under it. Settings, libraries and downloads all use it.
struct CardSection<Content: View>: View {
    let title: String?
    var subtitle: String?
    var footer: String?
    @ViewBuilder var content: Content

    init(_ title: String?, subtitle: String? = nil, footer: String? = nil, @ViewBuilder content: () -> Content) {
        self.title = title
        self.subtitle = subtitle
        self.footer = footer
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if let title {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text(title).font(.title3.bold())
                    if let subtitle { Text(subtitle).font(.callout).foregroundStyle(.secondary) }
                }
            }
            Group(subviews: content) { rows in
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(rows) { row in
                        if row.id != rows.first?.id { Divider().opacity(0.5) }
                        row.padding(.vertical, 11)
                    }
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 3)
                .frame(maxWidth: .infinity, alignment: .leading)
                .cardBackground()
            }
            if let footer {
                Text(footer).font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.horizontal, 4)
            }
        }
    }
}

/// One setting: its name, and a line about it, on the left; the control on
/// the right.
struct SettingRow<Control: View>: View {
    let title: String
    var detail: String?
    @ViewBuilder var control: Control

    init(_ title: String, detail: String? = nil, @ViewBuilder control: () -> Control) {
        self.title = title
        self.detail = detail
        self.control = control()
    }

    var body: some View {
        HStack(spacing: 16) {
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                if let detail { Text(detail).font(.caption).foregroundStyle(.secondary) }
            }
            Spacer(minLength: 12)
            control
        }
    }
}

/// A setting that's on or off, as an ember switch.
struct SettingToggle: View {
    let title: String
    var detail: String?
    @Binding var isOn: Bool

    init(_ title: String, detail: String? = nil, isOn: Binding<Bool>) {
        self.title = title
        self.detail = detail
        self._isOn = isOn
    }

    var body: some View {
        SettingRow(title, detail: detail) {
            Toggle(title, isOn: $isOn).toggleStyle(.switch).labelsHidden().tint(Color.wrenEmber)
        }
    }
}

/// The body of an admin page: scrolls, padded like every other page, and
/// kept to a readable width.
struct PageScroll<Content: View>: View {
    var maxWidth: CGFloat = 820
    @ViewBuilder var content: Content

    init(maxWidth: CGFloat = 820, @ViewBuilder content: () -> Content) {
        self.maxWidth = maxWidth
        self.content = content()
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 28) { content }
                .frame(maxWidth: maxWidth, alignment: .leading)
                .padding(28)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}

struct PosterCard: View {
    @Environment(AppState.self) private var state
    let item: Item
    var width: CGFloat = PosterSize.width(150)

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
                    .ambient(item.backdropPath) { item.heroEntry }
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
    var width: CGFloat = PosterSize.width(280)

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            RemoteImage(url: state.api?.imageURL(item.stillPath ?? item.backdropPath, size: "w500"))
                .frame(width: width, height: width * 9 / 16)
                .overlay(alignment: .bottom) {
                    if let p = item.progress, p.position > 0, !p.watched { ProgressStrip(fraction: p.fraction) }
                }
                .clipShape(RoundedRectangle(cornerRadius: 10))
                .hoverLift(cornerRadius: 10)
                .ambient(item.backdropPath) { item.heroEntry }
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
