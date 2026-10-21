import AppKit
import SwiftUI

/// The hovered title's artwork, shown behind Home, Collections, Movies, TV
/// Shows and library pages like Plex's cinematic backdrop. Each page keeps
/// its own; cards report their backdrop when the pointer is over them.
@Observable final class AmbientState {
    var path: String?
    /// The hovered title's details, for pages with a hero.
    var entry: HeroEntry?
}

extension EnvironmentValues {
    @Entry var ambient: AmbientState?
}

extension View {
    /// Makes a card's backdrop the page's while the pointer is over it.
    func ambient(_ backdrop: String?, entry: (() -> HeroEntry)? = nil) -> some View {
        modifier(AmbientSource(backdrop: backdrop, entry: entry))
    }

    /// Gives a page the hovered title's artwork as its background.
    func ambientBackdrop() -> some View { modifier(AmbientPage()) }
}

private struct AmbientSource: ViewModifier {
    @Environment(\.ambient) private var ambient
    let backdrop: String?
    let entry: (() -> HeroEntry)?

    func body(content: Content) -> some View {
        content.onHover { inside in
            guard inside else { return }
            if let backdrop, !backdrop.isEmpty { ambient?.path = backdrop }
            if let entry, let ambient {
                let e = entry()
                if ambient.entry != e { ambient.entry = e }
            }
        }
    }
}

private struct AmbientPage: ViewModifier {
    @State private var ambient = AmbientState()

    func body(content: Content) -> some View {
        content
            .environment(\.ambient, ambient)
            .background { AmbientBackdrop(ambient: ambient).ignoresSafeArea() }
    }
}

/// The art at its own colours in the top right, fading (not darkening) into
/// a background tinted from the art, so rows and titles stay readable.
private struct AmbientBackdrop: View {
    @Environment(AppState.self) private var state
    let ambient: AmbientState
    @State private var shown: URL?
    @State private var tint: Color?

    private static let base = Color(red: 0x12 / 255, green: 0x10 / 255, blue: 0x16 / 255)

    var body: some View {
        GeometryReader { g in
            ZStack(alignment: .topTrailing) {
                (tint.map { Self.base.mix(with: $0, by: 0.42) } ?? Self.base)
                if let shown {
                    RemoteImage(url: shown)
                        .frame(width: g.size.width * 0.74, height: g.size.height * 0.8)
                        .clipped()
                        // Fade the left and bottom edges out into the tint.
                        .mask(LinearGradient(stops: [.init(color: .clear, location: 0), .init(color: .black, location: 0.45)],
                                             startPoint: .leading, endPoint: .trailing))
                        .mask(LinearGradient(stops: [.init(color: .black, location: 0.5), .init(color: .clear, location: 1)],
                                             startPoint: .top, endPoint: .bottom))
                        .id(shown)
                        .transition(.opacity)
                }
            }
            .animation(.easeInOut(duration: 0.65), value: shown)
            .animation(.easeInOut(duration: 0.7), value: tint)
        }
        .allowsHitTesting(false)
        // Skimming along a row shouldn't flash every poster's art.
        .task(id: ambient.path) {
            guard let url = state.api?.imageURL(ambient.path, size: "w1280") else { return }
            try? await Task.sleep(for: .milliseconds(260))
            guard !Task.isCancelled else { return }
            shown = url
            if let c = await dominantColor(url), !Task.isCancelled { tint = c }
        }
    }
}

/// The art's main colour, favouring saturated pixels, kept dark enough for white text.
private func dominantColor(_ url: URL) async -> Color? {
    guard let (data, _) = try? await URLSession.shared.data(from: url),
          let src = NSImage(data: data)?.cgImage(forProposedRect: nil, context: nil, hints: nil) else { return nil }
    let w = 32, h = 18
    var px = [UInt8](repeating: 0, count: w * h * 4)
    guard let ctx = CGContext(data: &px, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                              space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)
    else { return nil }
    ctx.draw(src, in: CGRect(x: 0, y: 0, width: w, height: h))
    var r = 0.0, g = 0.0, b = 0.0, sum = 0.0
    for i in stride(from: 0, to: px.count, by: 4) {
        let pr = Double(px[i]) / 255, pg = Double(px[i + 1]) / 255, pb = Double(px[i + 2]) / 255
        let mx = max(pr, pg, pb), mn = min(pr, pg, pb)
        let sat = mx == 0 ? 0 : (mx - mn) / mx
        let k = sat * mx + 0.04
        r += pr * k; g += pg * k; b += pb * k; sum += k
    }
    guard sum > 0 else { return nil }
    var hue: CGFloat = 0, s: CGFloat = 0, v: CGFloat = 0
    NSColor(red: r / sum, green: g / sum, blue: b / sum, alpha: 1).getHue(&hue, saturation: &s, brightness: &v, alpha: nil)
    return Color(hue: hue, saturation: s, brightness: min(v, 0.62))
}
