import SwiftUI
import WebKit

/// Plays a YouTube trailer inside the app.
///
/// YouTube refuses embeds that arrive without a referrer ("Error 153"), so the
/// player page is loaded with the server's URL as its origin and an explicit
/// referrer policy, the same way the web client embeds it.
struct TrailerSheet: View {
    @Environment(AppState.self) private var state
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    let trailer: Trailer
    let title: String

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 12) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(title).font(.headline)
                    if !trailer.name.isEmpty {
                        Text(trailer.name).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                    }
                }
                Spacer()
                Button { openURL(trailer.url) } label: { Label("Open on YouTube", systemImage: "arrow.up.right.square") }
                    .buttonStyle(.borderless)
                    .foregroundStyle(.secondary)
                Button("Done") { dismiss() }
                    .bold()
                    .keyboardShortcut(.cancelAction)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
            YouTubePlayer(key: trailer.youtubeKey, origin: state.api?.baseURL)
                .aspectRatio(16 / 9, contentMode: .fit)
                .background(.black)
            Spacer(minLength: 0)
        }
        .background(Color.wrenInk)
        .presentationDetents([.medium, .large])
    }
}

struct YouTubePlayer: UIViewRepresentable {
    let key: String
    var origin: URL?

    func makeUIView(context: Context) -> WKWebView {
        let config = WKWebViewConfiguration()
        config.mediaTypesRequiringUserActionForPlayback = []
        config.allowsInlineMediaPlayback = true
        config.allowsPictureInPictureMediaPlayback = true
        config.preferences.isElementFullscreenEnabled = true
        let web = WKWebView(frame: .zero, configuration: config)
        web.isOpaque = false
        web.backgroundColor = .black
        web.scrollView.isScrollEnabled = false
        web.underPageBackgroundColor = .black
        load(web)
        return web
    }

    func updateUIView(_ web: WKWebView, context: Context) {
        if context.coordinator.key != key { load(web) }
        context.coordinator.key = key
    }

    func makeCoordinator() -> Coordinator { Coordinator(key: key) }

    final class Coordinator {
        var key: String
        init(key: String) { self.key = key }
    }

    static func dismantleUIView(_ web: WKWebView, coordinator: Coordinator) {
        // Stops audio straight away when the sheet closes.
        web.loadHTMLString("", baseURL: nil)
    }

    private func load(_ web: WKWebView) {
        // A page origin must be http(s) for YouTube to accept it.
        let base = origin.flatMap { u -> URL? in
            guard let scheme = u.scheme, scheme.hasPrefix("http"), let host = u.host else { return nil }
            var c = URLComponents()
            c.scheme = scheme
            c.host = host
            c.port = u.port
            c.path = "/"
            return c.url
        } ?? URL(string: "http://localhost/")!
        let originString = base.absoluteString.hasSuffix("/") ? String(base.absoluteString.dropLast()) : base.absoluteString
        let allowed = CharacterSet.alphanumerics.union(CharacterSet(charactersIn: "-_"))
        let safeKey = key.unicodeScalars.filter { allowed.contains($0) }.map(String.init).joined()
        let src = "https://www.youtube.com/embed/\(safeKey)?autoplay=1&playsinline=1&rel=0&modestbranding=1&origin=\(originString.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? "")"
        let html = """
        <!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1">
        <meta name="referrer" content="strict-origin-when-cross-origin">
        <style>html,body{margin:0;height:100%;background:#000;overflow:hidden}iframe{position:fixed;inset:0;width:100%;height:100%;border:0}</style>
        </head><body><iframe src="\(src)" referrerpolicy="strict-origin-when-cross-origin"
        allow="autoplay; encrypted-media; picture-in-picture; fullscreen" allowfullscreen></iframe></body></html>
        """
        web.loadHTMLString(html, baseURL: base)
    }
}
