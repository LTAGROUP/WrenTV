import Foundation

/// Gets a server HLS stream ready before AVPlayer opens it.
///
/// AVPlayer gives an init segment ("map") only 6 seconds. On a cold start the
/// server needs longer, because ffmpeg first has to reach into the source file
/// (often a remote debrid stream). AVPlayer then cancels and retries until the
/// item fails, and playback only works on the next try. The server keeps
/// producing segments after a request is cancelled, so asking for the init
/// segment and the first segment here, with a patient timeout, means AVPlayer
/// finds them ready.
enum StreamWarmup {
    private static let session: URLSession = {
        let c = URLSessionConfiguration.default
        c.timeoutIntervalForRequest = 90
        c.requestCachePolicy = .reloadIgnoringLocalCacheData
        return URLSession(configuration: c)
    }()

    /// Fetches every rendition's init segment and the segment holding `start`.
    /// Returns false when the server answered one of them with an error (it
    /// couldn't read the source), so the player can say so instead of waiting.
    /// A request that only fails to connect or times out isn't counted:
    /// AVPlayer then tries on its own, as before.
    static func prepare(_ master: URL, start: Double) async -> Bool {
        guard let text = await fetchText(master) else { return true }
        let playlists = renditions(in: text, base: master)
        return await withTaskGroup(of: Bool.self) { group in
            for playlist in playlists {
                group.addTask {
                    guard let media = await fetchText(playlist) else { return true }
                    let urls = firstFetches(in: media, base: playlist, start: start)
                    return await withTaskGroup(of: Bool.self) { inner in
                        for u in urls {
                            inner.addTask {
                                guard let (_, resp) = try? await session.data(from: u) else { return true }
                                return ((resp as? HTTPURLResponse)?.statusCode ?? 200) < 500
                            }
                        }
                        return await inner.allSatisfy { $0 }
                    }
                }
            }
            return await group.allSatisfy { $0 }
        }
    }

    private static func fetchText(_ url: URL) async -> String? {
        guard let (data, resp) = try? await session.data(from: url),
              (resp as? HTTPURLResponse)?.statusCode == 200 else { return nil }
        return String(decoding: data, as: UTF8.self)
    }

    /// Video and audio media playlists in a master playlist (subtitles are cheap).
    static func renditions(in master: String, base: URL) -> [URL] {
        var out: [URL] = []
        var isMaster = false
        for raw in master.split(whereSeparator: \.isNewline) {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("#EXT-X-STREAM-INF") { isMaster = true }
            if line.hasPrefix("#EXT-X-MEDIA:"), line.contains("TYPE=AUDIO"), let uri = attribute("URI", in: line) {
                out.append(URL(string: uri, relativeTo: base)!.absoluteURL)
            } else if !line.isEmpty, !line.hasPrefix("#") {
                out.append(URL(string: line, relativeTo: base)!.absoluteURL)
            }
        }
        // Already a media playlist: warm it directly.
        return isMaster ? Array(Set(out)) : [base]
    }

    /// The init segment and the media segment that covers `start`.
    static func firstFetches(in media: String, base: URL, start: Double) -> [URL] {
        var out: [URL] = []
        var elapsed = 0.0
        var duration: Double?
        for raw in media.split(whereSeparator: \.isNewline) {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("#EXT-X-MAP:"), let uri = attribute("URI", in: line) {
                out.append(URL(string: uri, relativeTo: base)!.absoluteURL)
            } else if line.hasPrefix("#EXTINF:") {
                duration = Double(line.dropFirst(8).prefix { $0 != "," })
            } else if !line.isEmpty, !line.hasPrefix("#"), let d = duration {
                if start < elapsed + d {
                    out.append(URL(string: line, relativeTo: base)!.absoluteURL)
                    break
                }
                elapsed += d
                duration = nil
            }
        }
        return out
    }

    private static func attribute(_ name: String, in line: String) -> String? {
        guard let r = line.range(of: name + "=\"") else { return nil }
        let rest = line[r.upperBound...]
        guard let end = rest.firstIndex(of: "\"") else { return nil }
        return String(rest[..<end])
    }
}
