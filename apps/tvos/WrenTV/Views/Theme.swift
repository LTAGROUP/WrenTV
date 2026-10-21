import CoreImage.CIFilterBuiltins
import SwiftUI
import UIKit

extension Color {
    /// The Wren palette.
    static let wrenEmber = Color(red: 0xE0 / 255, green: 0x4A / 255, blue: 0x2A / 255)
    static let wrenInk = Color(red: 0x1C / 255, green: 0x1A / 255, blue: 0x21 / 255)
    static let wrenCream = Color(red: 0xF7 / 255, green: 0xF1 / 255, blue: 0xE8 / 255)
    static let wrenSurface = Color.white.opacity(0.07)
    static let wrenBad = Color(red: 1, green: 0.45, blue: 0.4)
}

/// Sizes for the 1920×1080 point TV screen.
enum TV {
    static let gutter: CGFloat = 80
    static var poster: CGFloat { 220 * PosterSize.current.scale }
    static var landscape: CGFloat { 400 * PosterSize.current.scale }
    static let shelfSpacing: CGFloat = 40
}

/// The app's dark background with a faint ember glow in the corner.
struct WrenBackground: View {
    var body: some View {
        ZStack {
            Color.wrenInk
            RadialGradient(colors: [Color.wrenEmber.opacity(0.16), .clear], center: .topLeading, startRadius: 0, endRadius: 1400)
        }
        .ignoresSafeArea()
    }
}

struct RemoteImage: View {
    let url: URL?
    var contentMode: ContentMode = .fill

    var body: some View {
        if contentMode == .fill {
            Color.clear.overlay { image }.clipped()
        } else {
            image
        }
    }

    private var image: some View {
        AsyncImage(url: url, transaction: Transaction(animation: .easeOut(duration: 0.2))) { phase in
            if let image = phase.image {
                image.resizable().aspectRatio(contentMode: contentMode)
            } else if contentMode == .fill {
                Rectangle().fill(Color.white.opacity(0.06))
            }
        }
    }
}

/// Playback progress as a thin bar over the bottom of artwork.
struct ProgressStrip: View {
    let fraction: Double
    var body: some View {
        GeometryReader { g in
            ZStack(alignment: .leading) {
                Capsule().fill(.white.opacity(0.3))
                Capsule().fill(Color.wrenEmber).frame(width: max(g.size.width * fraction, 6))
            }
        }
        .frame(height: 6)
        .padding(.horizontal, 14)
        .padding(.bottom, 12)
        .shadow(color: .black.opacity(0.4), radius: 3)
    }
}

/// A small rounded label like "4K" or "Dolby Vision".
struct Badge: View {
    let text: String
    var body: some View {
        Text(text)
            .font(.caption2.weight(.semibold))
            .padding(.horizontal, 10)
            .padding(.vertical, 4)
            .background(.white.opacity(0.14), in: RoundedRectangle(cornerRadius: 6))
    }
}

/// A centered message for empty or failed pages.
struct EmptyState: View {
    let title: String
    var message: String?
    var symbol = "film.stack"

    var body: some View {
        VStack(spacing: 18) {
            Image(systemName: symbol).font(.system(size: 64)).foregroundStyle(.secondary)
            Text(title).font(.title3.bold())
            if let message { Text(message).foregroundStyle(.secondary).multilineTextAlignment(.center).frame(maxWidth: 900) }
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 120)
    }
}

/// A QR code for a link, drawn crisp at any size.
struct QRCode: View {
    let text: String

    var body: some View {
        if let image = Self.image(text) {
            Image(uiImage: image)
                .interpolation(.none)
                .resizable()
                .scaledToFit()
        }
    }

    static func image(_ text: String) -> UIImage? {
        let f = CIFilter.qrCodeGenerator()
        f.message = Data(text.utf8)
        f.correctionLevel = "M"
        guard let out = f.outputImage,
              let cg = CIContext().createCGImage(out, from: out.extent) else { return nil }
        return UIImage(cgImage: cg)
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

/// "52m left" for something partly watched.
func timeLeft(_ p: WatchState) -> String? {
    let left = p.duration - p.position
    guard p.duration > 0, left > 60, !p.watched else { return nil }
    return "\(formatDuration(left)) left"
}

extension Item {
    /// What the player shows for this item.
    var playTarget: PlayTarget {
        PlayTarget(itemId: id, title: kind == "episode" ? (showTitle ?? title) : title,
                   subtitle: kind == "episode" ? "\(episodeLabel) — \(title)" : nil)
    }

    /// "2019 · 2h 4m · PG-13"
    var metaLine: String {
        var parts: [String] = []
        if kind == "episode" { parts.append(episodeLabel) }
        if let year { parts.append(String(year)) }
        if let r = runtimeMin, r > 0 { parts.append(formatDuration(Double(r * 60))) }
        if let c = contentRating, !c.isEmpty { parts.append(c) }
        if let r = rating, r > 0 { parts.append(String(format: "★ %.1f", r)) }
        return parts.joined(separator: "  ·  ")
    }
}

/// Opens a library item's page.
struct ItemRoute: Hashable {
    var id: String
    var title: String
}
