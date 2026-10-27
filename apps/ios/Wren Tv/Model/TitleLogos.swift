import Foundation
import Observation

/// Clear logos for TMDB titles the server doesn't have yet, so a hero can
/// show one as soon as its title comes up. Cards ask as they appear; asks
/// made together go to the server in one request.
@MainActor
@Observable
final class TitleLogos {
    /// A title's logo path, "" when it has none; missing while unknown.
    private var known: [String: String] = [:]
    @ObservationIgnored private var asked: Set<String> = []
    @ObservationIgnored private var pending: [String] = []
    @ObservationIgnored private var sending = false
    @ObservationIgnored var api: () -> WrenTVAPI? = { nil }

    /// The title's logo once known: its library copy's, else TMDB's.
    subscript(t: TitleView) -> String? { t.item?.logoPath ?? self[t.id] }

    /// The logo for a title id ("movie:<tmdbId>"), once known.
    subscript(id: String) -> String? { known[id].flatMap { $0.isEmpty ? nil : $0 } }

    func want(_ t: TitleView) {
        guard t.item == nil, known[t.id] == nil, asked.insert(t.id).inserted else { return }
        pending.append(t.id)
        guard !sending else { return }
        sending = true
        Task {
            // Let the rest of the row's cards ask before sending.
            try? await Task.sleep(for: .milliseconds(60))
            while !pending.isEmpty {
                let batch = Array(pending.prefix(30))
                pending.removeFirst(batch.count)
                guard let api = api(), let got = try? await api.titleLogos(batch) else {
                    asked.subtract(batch) // asked again next time it's shown
                    continue
                }
                known.merge(got) { _, new in new }
                // The server couldn't look these up just now.
                asked.subtract(batch.filter { got[$0] == nil })
                for path in got.values where !path.isEmpty {
                    // Ready the image too, so the hero has it the moment it's needed.
                    if let url = api.imageURL(path, size: "w500") { URLSession.shared.dataTask(with: url).resume() }
                }
            }
            sending = false
        }
    }

    @ObservationIgnored private var warmed: Set<String> = []
    @ObservationIgnored private var settling: Task<Void, Never>?

    /// A card the viewer has settled on: once focus or the pointer rests on it
    /// a moment, the server readies its detail page, so opening it doesn't
    /// wait on TMDB. Moving on quickly asks for nothing.
    func focused(_ t: TitleView) {
        settling?.cancel()
        guard t.item == nil, !warmed.contains(t.id) else { return }
        settling = Task {
            try? await Task.sleep(for: .milliseconds(500))
            guard !Task.isCancelled, let api = api() else { return }
            warmed.insert(t.id)
            if (try? await api.warmTitles([t.id])) == nil { warmed.remove(t.id) }
        }
    }

    /// Focus moved on before the card settled.
    func unfocused() { settling?.cancel() }
}
