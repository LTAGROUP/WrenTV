import Foundation

/// Every address a server publishes, and picking the best one that answers.
///
/// Servers publish their addresses to the Wren TV account service: LAN
/// addresses (`http://192.168.1.20:32500` and, with remote access,
/// `https://192-168-1-20.<id>.wrentv.net:32500`), the owner's own `public_url`,
/// and the automatic `remote_url`. The app tries them all at once and uses the
/// best-ranked one that answers, the same way the web player does.
enum AddressPicker {
    /// How long a better-ranked address gets to answer after a worse one did.
    private static let preferWait: Duration = .milliseconds(1500)

    /// Candidates, best first: secure LAN, plain LAN, the owner's public
    /// address, the automatic remote address, then the address typed in.
    static func candidates(entered: URL?, localUrls: [String], publicUrl: String?, remoteUrl: String?) -> [URL] {
        let lan = localUrls.compactMap(URL.init(string:))
        var out = lan.filter { $0.scheme == "https" } + lan.filter { $0.scheme != "https" }
        out += [publicUrl, remoteUrl].compactMap { $0.flatMap(URL.init(string:)) }
        if let entered { out.append(entered) }
        var seen = Set<String>()
        return out.filter { seen.insert(key($0)).inserted }
    }

    /// The best candidate that answers as this server (matching serverId or,
    /// for local-account servers, machineId), or nil when none does.
    static func best(_ candidates: [URL], id: String) async -> URL? {
        guard !candidates.isEmpty else { return nil }
        return await withTaskGroup(of: (Int, Bool).self) { group in
            for (i, u) in candidates.enumerated() {
                group.addTask { (i, await answers(u, id: id)) }
            }
            var results = [Bool?](repeating: nil, count: candidates.count)
            var deadline: ContinuousClock.Instant?
            func decided() -> URL?? {
                // Best-ranked success whose better-ranked rivals have all finished.
                for (i, r) in results.enumerated() {
                    guard let r else { return nil }
                    if r { return .some(candidates[i]) }
                }
                return .some(nil)
            }
            while let (i, ok) = await group.next() {
                guard i >= 0 else { break } // waited long enough for a better answer
                results[i] = ok
                if let d = decided() { group.cancelAll(); return d }
                if ok, deadline == nil {
                    let d = ContinuousClock.now + preferWait
                    deadline = d
                    group.addTask { try? await Task.sleep(until: d); return (-1, false) }
                }
            }
            group.cancelAll()
            if let i = results.firstIndex(where: { $0 == true }) { return candidates[i] }
            return nil
        }
    }

    private static func answers(_ base: URL, id: String) async -> Bool {
        var req = URLRequest(url: URL(string: "/api/server", relativeTo: base)!.absoluteURL)
        req.timeoutInterval = 5
        guard let (data, resp) = try? await URLSession.shared.data(for: req),
              (resp as? HTTPURLResponse)?.statusCode == 200,
              let info = try? JSONDecoder().decode(ServerInfo.self, from: data) else { return false }
        return (info.serverId?.isEmpty == false ? info.serverId : info.machineId) == id
    }

    static func key(_ u: URL) -> String {
        var s = u.absoluteString
        while s.hasSuffix("/") { s.removeLast() }
        return s.lowercased()
    }
}
