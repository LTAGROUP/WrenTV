import Foundation
import Observation

struct WatchState: Codable, Hashable {
    var position: Double
    var duration: Double
    var watched: Bool

    var fraction: Double { duration > 0 ? min(max(position / duration, 0), 1) : 0 }
}

struct Item: Codable, Hashable, Identifiable {
    let id: String
    let libraryId: String
    let kind: String
    var parentId: String?
    var showId: String?
    var title: String
    var originalTitle: String?
    var year: Int?
    var overview: String?
    var tagline: String?
    var tmdbId: Int?
    var seasonNumber: Int
    var episodeNumber: Int
    var posterPath: String?
    var backdropPath: String?
    var stillPath: String?
    var logoPath: String?
    var rating: Double?
    var contentRating: String?
    var genres: [String]?
    var runtimeMin: Int?
    var airDate: String?
    var matchState: String
    var addedAt: Int64
    var progress: WatchState?
    var showTitle: String?
    var childCount: Int?
    var unwatchedCount: Int?

    var isPlayable: Bool { kind == "movie" || kind == "episode" }
    var isWatched: Bool { progress?.watched ?? false }
    var inProgress: Bool { (progress.map { !$0.watched && $0.position > 30 }) ?? false }

    /// "S1 · E4" for episodes.
    var episodeLabel: String { "S\(seasonNumber) · E\(episodeNumber)" }

    var displayTitle: String {
        if kind == "season" { return seasonNumber == 0 ? "Specials" : "Season \(seasonNumber)" }
        return title
    }
}

struct Library: Codable, Hashable, Identifiable {
    let id: String
    var name: String
    var type: String // movies | shows
    var paths: [String]
    /// "files" (folders on disk) or "stream" (titles streamed through debrid).
    var source: String?
    var lastScanAt: Int64?
    var itemCount: Int?

    var isStream: Bool { source == "stream" }
    var symbol: String { isStream ? (type == "shows" ? "play.tv" : "play.rectangle") : (type == "shows" ? "tv" : "film") }
}

struct User: Codable, Hashable, Identifiable {
    let id: String
    var username: String
    var displayName: String
    var isAdmin: Bool
    var role: String?
}

struct SupabaseInfo: Codable, Hashable, Sendable {
    var url: String
    var publishableKey: String
}

struct ServerInfo: Codable {
    var name: String
    var machineId: String
    var version: String
    var authMode: String
    var setupRequired: Bool
    /// Streaming through the owner's debrid service is on and working.
    var streaming: Bool?
    var supabase: SupabaseInfo?
    /// The server's id in the account service (account servers only).
    var serverId: String?
    var localUrls: [String]?
    /// The owner has set up live TV (IPTV) and it has channels.
    var liveTv: Bool?
    /// Account servers also take local (username) accounts the admin added.
    var localAccounts: Bool?

    /// Signs in with a Wren TV account (email) rather than a server-local username.
    var usesAccounts: Bool { authMode == "supabase" && supabase != nil }
    /// A local server with no accounts yet: the first sign-in creates the owner.
    var needsOwner: Bool { setupRequired && !usesAccounts }
}

struct AuthResponse: Codable {
    var token: String
    var user: User
}

struct MeResponse: Codable {
    var user: User
    var limits: UserLimitsInfo?
}

/// What the signed-in user may do, with server defaults applied.
struct UserLimitsInfo: Codable, Hashable {
    var canStream: Bool
    var canDownload: Bool
    var maxStreams: Int
    var maxDownloads: Int
}

struct HomeResponse: Codable {
    struct Shelf: Codable, Hashable {
        var library: Library
        var items: [Item]
    }
    var continueWatching: [Item]
    var recentlyAdded: [Shelf]
    var topRated: [Item]?
    var genres: [GenreRow]?
    var collections: [CollectionShelf]?
    /// Still to watch, newest added first. Nil on servers from before the watchlist.
    var watchlist: [Item]?
}

/// The streaming home page: rows of TMDB titles.
struct DiscoverHome: Codable {
    struct Row: Codable, Hashable, Identifiable {
        var id: String
        var title: String
        var items: [TitleView]
    }
    var enabled: Bool
    var streaming: Bool?
    var continueWatching: [Item]
    var recentlyWatched: [Item]
    var watchlist: [Item]?
    var hero: [TitleView]
    var rows: [Row]
}

struct CollectionShelf: Codable, Hashable {
    var collection: WrenCollection
    var items: [Item]
    var missing: [TitleView]?
}

/// A page of TMDB's catalog for the Movies and TV Shows pages.
struct CatalogPage: Codable {
    struct Genre: Codable, Hashable, Identifiable {
        var id: Int
        var name: String
    }
    var enabled: Bool
    var items: [TitleView]
    var genres: [Genre]
    var page: Int
    var totalPages: Int
}

/// A collection: hand-picked, smart (rules) or synced from TMDB.
struct WrenCollection: Codable, Hashable, Identifiable {
    let id: String
    var name: String
    var description: String
    var kind: String // manual | smart | tmdb
    var libraryId: String?
    var posterPath: String?
    var backdropPath: String?
    var sort: String
    var showOnHome: Bool
    var showMissing: Bool
    var visibleFrom: String
    var visibleUntil: String
    var syncError: String?
    var itemCount: Int?
    var active: Bool?
    /// The shelf it sits on: Collections, Franchises, Lists, Charts or Smart Collections.
    var group: String?

    var kindLabel: String {
        switch kind {
        case "smart": "Smart"
        case "tmdb": "From TMDB"
        default: "Hand-picked"
        }
    }
}

/// A page of a collection's one shelf, in its order (offset of total).
struct CollectionPage: Codable {
    var collection: WrenCollection
    var entries: [TitleView]
    var total: Int
    var offset: Int
    /// "12 in your library", "40 titles", "8 in your library · 4 not".
    var countLabel: String

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        collection = try c.decode(WrenCollection.self, forKey: .collection)
        entries = try c.decodeIfPresent([TitleView].self, forKey: .entries) ?? []
        total = try c.decodeIfPresent(Int.self, forKey: .total) ?? 0
        offset = try c.decodeIfPresent(Int.self, forKey: .offset) ?? 0
        countLabel = try c.decodeIfPresent(String.self, forKey: .countLabel) ?? ""
    }
}

extension TitleView {
    /// A collection entry's identity: the library item, else the TMDB title.
    var entryKey: String { item?.id ?? id }
}

struct CollectionDetail: Codable {
    var collection: WrenCollection
    var items: [Item]
    var missing: [TitleView]?
    /// When streaming, a TMDB collection's every title, in its order.
    var titles: [TitleView]?
    /// A big collection's shelves, unsorted: indexes into titles when there are any, else items.
    var rows: [CollectionRow]?
}

struct CollectionRow: Codable, Hashable {
    var title: String
    var index: [Int]
}

/// Episodes airing around now, from the library's shows still running and the ones being watched.
struct CalendarResponse: Codable {
    var from: String
    var to: String
    var episodes: [CalendarEpisode]
}

struct CalendarEpisode: Codable, Hashable, Identifiable {
    var date: String // YYYY-MM-DD
    var show: Item
    var seasonNumber: Int
    var episodeNumber: Int
    var title: String
    var overview: String?
    var stillPath: String?
    /// The episode in the library, when it's there.
    var itemId: String?
    var watched: Bool
    /// The viewer has watched some of the show; upNext is the episode they're up to.
    var watching: Bool
    var upNext: Bool

    var id: String { "\(show.id):\(seasonNumber):\(episodeNumber)" }
    var code: String { "S\(seasonNumber) E\(episodeNumber)" }
    var name: String { title.isEmpty ? "Episode \(episodeNumber)" : title }
    var day: Date? { CalendarEpisode.parser.date(from: date) }

    static let parser: DateFormatter = {
        let f = DateFormatter()
        f.calendar = Calendar(identifier: .gregorian)
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateFormat = "yyyy-MM-dd"
        return f
    }()

    /// "Today", "Tomorrow", "Yesterday", or the weekday and date.
    static func dayName(_ date: String) -> String {
        guard let d = parser.date(from: date) else { return date }
        let cal = Calendar.current
        if cal.isDateInToday(d) { return "Today" }
        if cal.isDateInTomorrow(d) { return "Tomorrow" }
        if cal.isDateInYesterday(d) { return "Yesterday" }
        return d.formatted(.dateTime.weekday(.wide).month(.abbreviated).day())
    }

    /// "Airs tomorrow", "Aired Oct 12, 2026".
    var airs: String {
        guard let d = day else { return date }
        let cal = Calendar.current
        let name = Self.dayName(date)
        let when = ["Today", "Tomorrow", "Yesterday"].contains(name) ? name.lowercased() : d.formatted(date: .abbreviated, time: .omitted)
        return cal.isDateInToday(d) || d > Date() ? "Airs \(when)" : "Aired \(when)"
    }

    /// Episodes grouped by day, in order.
    static func days(_ eps: [CalendarEpisode]) -> [(date: String, episodes: [CalendarEpisode])] {
        var out: [(date: String, episodes: [CalendarEpisode])] = []
        for e in eps {
            if out.last?.date == e.date { out[out.count - 1].episodes.append(e) } else { out.append((e.date, [e])) }
        }
        return out
    }

    /// Today's date the way the server writes it, for "what's next".
    static var today: String { parser.string(from: Date()) }

    /// The seven days (YYYY-MM-DD) of the week containing date, starting on
    /// the locale's first weekday.
    static func week(of date: String) -> [String] {
        let cal = Calendar.current
        guard let d = parser.date(from: date), let start = cal.dateInterval(of: .weekOfYear, for: d)?.start else { return [date] }
        return (0..<7).compactMap { cal.date(byAdding: .day, value: $0, to: start) }.map { parser.string(from: $0) }
    }

    /// date moved by n days.
    static func adding(_ n: Int, to date: String) -> String {
        guard let d = parser.date(from: date), let t = Calendar.current.date(byAdding: .day, value: n, to: d) else { return date }
        return parser.string(from: t)
    }
}

/// An actor or crew member, with rows of what they're in (Known for, In
/// your library, Movies, TV Shows).
struct Person: Codable, Hashable {
    var id: Int
    var name: String
    var biography: String?
    var birthday: String?
    var deathday: String?
    var placeOfBirth: String?
    var profilePath: String?
    /// TMDB's department for them: "Acting", "Directing"…
    var knownFor: String?
    var imdbId: String?
    var backdropPath: String?
    var rows: [CreditRow]

    /// "Born February 3, 1970 (age 56)", "Died …", where they were born.
    var facts: [String] {
        var out: [String] = []
        let d = CalendarEpisode.parser
        func long(_ s: String) -> String { d.date(from: s)?.formatted(date: .long, time: .omitted) ?? s }
        func age(_ from: String, _ to: String?) -> Int? {
            guard let b = d.date(from: from) else { return nil }
            let e = to.flatMap { d.date(from: $0) } ?? Date()
            return Calendar.current.dateComponents([.year], from: b, to: e).year
        }
        if let k = knownFor, !k.isEmpty { out.append("Known for \(k.lowercased())") }
        if let b = birthday, !b.isEmpty {
            out.append("Born \(long(b))" + (deathday == nil ? age(b, nil).map { " (age \($0))" } ?? "" : ""))
        }
        if let x = deathday, !x.isEmpty {
            out.append("Died \(long(x))" + (birthday.flatMap { age($0, x) }.map { " (aged \($0))" } ?? ""))
        }
        if let p = placeOfBirth, !p.isEmpty { out.append(p) }
        return out
    }
}

struct CreditRow: Codable, Hashable {
    var title: String
    var credits: [TitleView]
}

/// Opens a person's page from a cast list.
struct PersonRoute: Hashable {
    var id: Int
    var name: String
}

struct StudioRoute: Hashable {
    var id: Int
    var name: String
}

/// A studio shelf's titles so far; `more()` fetches the next page when the end comes near.
@MainActor @Observable
final class StudioShelf: Identifiable {
    let row: StudioRow
    let studio: Int
    var items: [TitleView]
    private var page = 1
    private var total: Int
    private var loading = false
    nonisolated var id: String { row.id }

    init(row: StudioRow, studio: Int) {
        self.row = row
        self.studio = studio
        items = row.items.uniqued()
        total = row.totalPages
    }

    /// Called as each card appears; within a dozen of the end, the next page comes in.
    func appeared(_ t: TitleView, api: WrenTVAPI?) {
        guard let api, !loading, page < total,
              let i = items.firstIndex(where: { $0.id == t.id }), i >= items.count - 12 else { return }
        loading = true
        Task {
            defer { loading = false }
            guard let res = try? await api.catalog(type: row.mediaType, sort: row.sort, genre: nil, page: page + 1, company: studio) else { return }
            let have = Set(items.map(\.id))
            items += res.items.filter { !have.contains($0.id) }.uniqued()
            page = res.page
            total = res.totalPages
        }
    }
}

/// Poster sizes the browsing pages can use. The choice is saved to the
/// account (UserPrefs.posterSize) and cached here for the first frame.
enum PosterSize: String, CaseIterable, Identifiable {
    case small, medium, large
    static let key = "posterSize"
    var id: String { rawValue }
    var label: String { rawValue.capitalized }
    var scale: CGFloat {
        switch self {
        case .small: 0.85
        case .medium: 1
        case .large: 1.25
        }
    }
    static var current: PosterSize { PosterSize(rawValue: UserDefaults.standard.string(forKey: key) ?? "") ?? .medium }
}

struct GenreCount: Codable, Hashable, Identifiable {
    var genre: String
    var count: Int
    var movies: Int
    var shows: Int
    var backdropPath: String?
    var id: String { genre }
}

/// Opens an item known only by its id, as from Stats.
struct ItemIDRoute: Hashable {
    var id: String
}

/// Opens a genre's titles, optionally within one library.
struct GenreRoute: Hashable {
    var genre: String
    var libraryId: String?
}

/// A TV waiting to be signed in with a code.
struct PairInfo: Decodable {
    var device: String
    var approved: Bool
}

struct UserPrefs: Codable, Hashable {
    var audioLanguage: String
    /// Subtitles turn on in this language when a file has them; empty is off.
    var subtitleLanguage: String?
    /// With subtitles off, forced tracks (foreign-language lines only) still show.
    var forcedSubtitles: Bool?
    /// Subtitles in the audio language turn on when a title plays in another language.
    var foreignAudioSubtitles: Bool?
    /// How far in (percent) a title counts as watched.
    var watchedPercent: Int?
    /// Seconds watched before a title is on Continue Watching.
    var continueAfter: Int?
    /// Poster size in every app: small, medium or large.
    var posterSize: String?

    static let watchedPercents = [50, 60, 70, 75, 80, 85, 90, 95, 98]
    static let continueAfters: [(Int, String)] = [(10, "10 seconds"), (30, "30 seconds"), (60, "1 minute"), (120, "2 minutes"),
                                                  (300, "5 minutes"), (600, "10 minutes")]
}

/// The caller's Simkl link, from /api/me/simkl.
struct SimklStatus: Decodable {
    var available: Bool
    var linked: Bool
    var account: Account?
    var pending: Pending?
    var importing: Bool?

    struct Account: Decodable {
        var username: String?
        var scrobble: Bool
        var importStatus: String?
        var importAt: Int64?
        var importMovies: Int?
        var importEpisodes: Int?
    }

    struct Pending: Decodable {
        var userCode: String
        var url: String
        var urlComplete: String?
        var error: String?
    }

    /// Worth checking again soon: waiting on approval or an import.
    var busy: Bool {
        (pending != nil && (pending?.error ?? "").isEmpty) || importing == true || account?.importStatus == "running"
    }
}

struct GenreRow: Codable, Hashable {
    var genre: String
    var items: [Item]
}

/// A TMDB title (trending, more like this), linked to the library item when
/// the server has it.
struct TitleView: Codable, Hashable, Identifiable {
    var tmdbId: Int
    var mediaType: String // movie | show
    var title: String
    var year: Int?
    var overview: String?
    var posterPath: String?
    var backdropPath: String?
    var rating: Double?
    var item: Item?
    /// On a person's page: who they played, or their job.
    var role: String?
    /// Marked watched on Simkl while the server doesn't have it.
    var watched: Bool?

    var id: String { "\(mediaType):\(tmdbId)" }
    var tmdbURL: URL { URL(string: "https://www.themoviedb.org/\(mediaType == "show" ? "tv" : "movie")/\(tmdbId)")! }
}

struct TrendingResponse: Codable {
    var enabled: Bool
    var items: [TitleView]
}

struct Ratings: Codable, Hashable {
    var tmdb: Double?
    var tmdbVotes: Int?
    var imdb: Double?
    var imdbVotes: Int?
    var rottenTomatoes: Int?
    var metacritic: Int?
}

struct CastMember: Codable, Hashable {
    /// The TMDB person, for their page.
    var id: Int?
    var name: String
    var character: String?
    var profilePath: String?
}

struct Trailer: Codable, Hashable {
    var youtubeKey: String
    var name: String
    var url: URL { URL(string: "https://www.youtube.com/watch?v=\(youtubeKey)")! }
}

struct Extras: Codable {
    var ratings: Ratings
    /// Whether IMDb / Rotten Tomatoes / Metacritic ratings are configured.
    var omdb: Bool
    var tmdb: Bool
    var cast: [CastMember]
    var trailer: Trailer?
    var moreLikeThis: [TitleView]
    /// The movie series it's part of, in release order.
    var franchise: Franchise?
    /// The studios that made it; each has a page.
    var studios: [Studio]?
}

struct Franchise: Codable, Hashable {
    var id: Int
    var name: String
    var backdropPath: String?
    var titles: [TitleView]
}

struct Studio: Codable, Hashable, Identifiable {
    var id: Int
    var name: String
    var logoPath: String?
}

/// A studio's page: who they are and shelves of what they made.
struct Company: Codable, Hashable {
    var id: Int
    var name: String
    var logoPath: String?
    var description: String?
    var headquarters: String?
    var originCountry: String?
    var backdropPath: String?
    var rows: [StudioRow]
}

/// One shelf of a studio's titles; `catalog` with its type, sort and the studio pages through the rest.
struct StudioRow: Codable, Hashable, Identifiable {
    var id: String
    var title: String
    var mediaType: String
    var sort: String
    var items: [TitleView]
    var totalPages: Int
}

struct ItemPage: Codable {
    var items: [Item]
    var total: Int
}

struct MediaStream: Codable, Hashable {
    var index: Int
    var typeIndex: Int
    var codec: String
    var profile: String?
    var language: String?
    var title: String?
    var `default`: Bool
    var forced: Bool
    var width: Int?
    var height: Int?
    var hdr: String?
    var dvProfile: Int?
    var channels: Int?
    var channelLayout: String?

    var label: String {
        var parts: [String] = []
        if let language, !language.isEmpty, language != "und" {
            parts.append(Locale.current.localizedString(forLanguageCode: language) ?? language)
        }
        if let title, !title.isEmpty { parts.append(title) }
        parts.append(codecLabel)
        return parts.joined(separator: " · ")
    }

    var codecLabel: String {
        var c = codec.uppercased()
        switch codec {
        case "eac3": c = profile?.contains("Atmos") == true ? "Dolby Atmos" : "Dolby Digital+"
        case "truehd": c = profile?.contains("Atmos") == true ? "TrueHD Atmos" : "TrueHD"
        case "ac3": c = "Dolby Digital"
        case "dts": c = profile ?? "DTS"
        case "subrip": c = "SRT"
        case "hdmv_pgs_subtitle": c = "PGS"
        case "ass": c = "ASS"
        default: break
        }
        if let channels, channels > 0 {
            let layout = channels == 8 ? "7.1" : channels == 6 ? "5.1" : channels == 2 ? "Stereo" : "\(channels)ch"
            c += " \(layout)"
        }
        return c
    }
}

struct MediaInfo: Codable, Hashable {
    var container: String
    var duration: Double
    var bitrate: Int64?
    var video: [MediaStream]?
    var audio: [MediaStream]?
    var subtitles: [MediaStream]?
    var chapters: [Chapter]?
}

struct MediaFileView: Codable, Hashable, Identifiable {
    let id: String
    var size: Int64
    var container: String
    var duration: Double
    var videoCodec: String
    var width: Int
    var height: Int
    var hdr: String?
    var dvProfile: Int?
    /// Set when the file is a saved debrid stream rather than a file on disk.
    var streamId: String?
    /// The release a stream comes from.
    var release: String?
    var fileName: String
    var label: String?
    var info: MediaInfo?

    /// Badges like "4K", "Dolby Vision", "Atmos".
    var badges: [String] {
        var b: [String] = []
        if width >= 3200 { b.append("4K") } else if width >= 1800 { b.append("1080p") } else if width >= 1200 { b.append("720p") }
        switch hdr ?? "" {
        case "DV", "DV+HDR10": b.append("Dolby Vision")
        case "HDR10": b.append("HDR10")
        case "HLG": b.append("HLG")
        default: break
        }
        if let a = info?.audio?.first(where: { $0.default }) ?? info?.audio?.first {
            if a.profile?.contains("Atmos") == true { b.append("Atmos") }
            else if let ch = a.channels, ch >= 6 { b.append(ch == 8 ? "7.1" : "5.1") }
        }
        return b
    }
}

struct ItemDetail: Codable {
    var item: Item
    var children: [Item]?
    var nextEpisode: Item?
    var show: Item?
    var files: [MediaFileView]?
    /// The item lives in a streaming library.
    var streaming: Bool?
    var canDownload: Bool?
    /// Movies and shows only; nil on servers from before the watchlist.
    var onWatchlist: Bool?
}

/// A device download link: the original file (or stream) for this device.
struct DeviceDownload: Codable {
    var status: String
    var message: String?
    var url: String?
    var fileName: String?
    var fileId: String?
    var size: Int64?
    var label: String?
    var expiresAt: Int64?
}

/// Search results from TMDB, linked to library items when the server has them.
struct DiscoverSearch: Codable {
    var streaming: Bool
    var items: [TitleView]
}

/// Opens a TMDB title in the streaming library (added on first use).
struct StreamTitleRoute: Hashable {
    var tmdbId: Int
    var mediaType: String
    var title: String
}

struct ClientCaps: Codable {
    var engines: [String]
    var nativeVideo: [String]
    var nativeAudio: [String]
    var nativeContainers: [String]
    var dolbyVision: Bool
    var hdr: Bool
    var maxKbps: Int
    var forceEngine: String?
    var forceTranscode: Bool?
}

struct PlayRequest: Codable {
    var fileId: String?
    var audioIndex: Int?
    var caps: ClientCaps
    var maxWidth: Int?
    /// Starting again after a failure: the server resolves a fresh link.
    var retry: Bool?
    /// The version that broke partway, for the server to skip.
    var failedFileId: String?
}

struct Decision: Codable {
    var engine: String
    var method: String
    var audioCodec: String?
    var audioIndex: Int?
    var reasons: [String]?
}

struct SubtitleTrack: Codable, Hashable, Identifiable {
    var index: Int
    var language: String?
    var title: String?
    var codec: String
    var forced: Bool
    var `default`: Bool
    var url: String?
    var id: Int { index }

    var label: String {
        var parts: [String] = []
        if let language, !language.isEmpty, language != "und" {
            parts.append(Locale.current.localizedString(forLanguageCode: language) ?? language)
        }
        if let title, !title.isEmpty { parts.append(title) }
        if forced { parts.append("Forced") }
        return parts.isEmpty ? "Track \(index + 1)" : parts.joined(separator: " · ")
    }
}

struct PlayResponse: Codable {
    var status: String
    var message: String?
    var sessionId: String?
    var engine: String?
    var method: String?
    var url: String?
    var decision: Decision?
    var duration: Double?
    var resumePosition: Double?
    var info: MediaInfo?
    var subtitles: [SubtitleTrack]?
    /// The subtitle track the viewer's language setting picks, or -1 for off.
    var subtitleIndex: Int?
    var fileId: String?
    var chapters: [Chapter]?
    var markers: Markers?
    /// The item's files, when it has more than one, for a version picker.
    var versions: [MediaVersion]?
    /// The server can search subtitle sites for this title.
    var onlineSubtitles: Bool?
}

/// A subtitle found on a subtitle site, not yet downloaded.
struct OnlineSubtitle: Codable, Hashable, Identifiable {
    var provider: String
    var id: String
    var name: String
    var language: String
    var downloads: Int?
    var hearingImpaired: Bool?
    var machineTranslated: Bool?
    var matchesFile: Bool?

    var detail: String {
        var parts = matchesFile == true ? ["Matches your file"] : []
        parts += [Locale.current.localizedString(forLanguageCode: language) ?? language.uppercased(), provider]
        if hearingImpaired == true { parts.append("SDH") }
        if machineTranslated == true { parts.append("Machine translated") }
        if let n = downloads, n > 0 { parts.append("\(n.formatted()) downloads") }
        return parts.joined(separator: " · ")
    }
}

struct OnlineSubtitleSearch: Codable {
    var enabled: Bool
    var results: [OnlineSubtitle]?
}

struct VersionsResponse: Codable {
    var versions: [MediaVersion]?
    var looking: Bool?
}

struct MediaVersion: Codable, Hashable, Identifiable {
    var id: String
    var label: String
    var height: Int
}

struct Chapter: Codable, Hashable {
    var start: Double
    var end: Double
    var title: String
}

struct TimeRange: Codable, Hashable {
    var start: Double
    var end: Double
}

/// The skippable parts of a file, from chapter names or audio fingerprinting.
struct Markers: Codable, Hashable {
    var intro: TimeRange?
    var credits: TimeRange?
    var source: String?
}

/// A streamed title being saved to the server as a file.
struct ServerDownload: Codable, Hashable, Identifiable {
    let id: String
    var itemId: String?
    var title: String
    var quality: String
    var status: String // queued, downloading, done, failed, canceled
    var path: String?
    var bytes: Int64
    var total: Int64
    var error: String?
    var createdAt: Int64

    var active: Bool { status == "queued" || status == "downloading" }
    var fraction: Double { total > 0 ? min(1, Double(bytes) / Double(total)) : 0 }
}

struct WebhookSetting: Codable, Hashable {
    var url: String
    var secret: String?
    var events: [String]?
}

struct ServerSettings: Codable {
    var serverName: String
    var publicUrl: String
    var hwaccel: String
    var tmdbKeySet: Bool
    var omdbKeySet: Bool?
    var markerFingerprinting: Bool?
    var markerCommunity: Bool?
    var streaming: StreamingSettings
    var userDefaults: UserDefaultsSettings
    var webhooks: [WebhookSetting]?
    var integrationKey: String
    var restartRequired: Bool?
}

struct StreamingSettings: Codable, Hashable {
    var enabled: Bool
    var active: Bool
    var debrid: [DebridService]
    var addons: [AddonSetting]
    var qualities: [String]
    var maxSizeGB: [String: Double]?
    var prefetch: Bool
    var lookupOnOpen: Bool?
    var mode: String
    var movieLibrary: String
    var showLibrary: String
    var downloadQuality: String
    var limits: StreamingLimits
}

struct DebridService: Codable, Hashable, Identifiable {
    var provider: String
    var name: String
    var keySet: Bool
    var keyHint: String?
    var id: String { provider }
}

struct AddonSetting: Codable, Hashable {
    var name: String
    var url: String
    var enabled: Bool
}

struct StreamingLimits: Codable, Hashable {
    var maxStreams = 0
    var bandwidthMbps = 0.0
    var perStreamMbps = 0.0
    var downloadMbps = 0.0
    var maxDownloads = 0
    var prefetchWorkers = 0
    var downloadQuotaGB = 0.0
    var minFreeGB = 0.0
}

struct UserDefaultsSettings: Codable, Hashable {
    var maxStreams = 0
    var deviceDownloads = true
    var maxDownloads = 0
}

/// Result of checking a debrid API key.
struct DebridAccount: Codable {
    var provider: String
    var ok: Bool
    var user: String?
    var premium: Bool
    var expires: String?
    var error: String?
}

/// A server account with its streaming limits (-1 = server default, 0 = no limit).
struct ServerUser: Codable, Hashable, Identifiable {
    let id: String
    var username: String
    var displayName: String
    var email: String?
    var isAdmin: Bool
    var allowStreaming: Bool
    var allowDownloads: Int
    var maxStreams: Int
    var maxDownloads: Int
    var effective: UserLimitsInfo
}

struct UserLimitsPatch: Codable {
    var allowStreaming: Bool
    var allowDownloads: Int
    var maxStreams: Int
    var maxDownloads: Int
}

struct ServerEvent: Codable {
    var type: String
    var libraryId: String?
    var itemId: String?
    var message: String?
}

// MARK: Stats

struct StatCount: Codable, Hashable {
    var name: String
    var count: Int
}

struct DayStat: Codable, Hashable {
    var date: String
    var plays: Int
    var hours: Double
    var movies: Int?
    var episodes: Int?
    var direct: Int?
    var remux: Int?
    var transcode: Int?
    var peak: Int?
}

struct MonthStat: Codable, Hashable {
    var month: String // 2026-10
    var plays: Int
    var movies: Int
    var episodes: Int
    var hours: Double
}

struct TopTitle: Codable, Hashable {
    var itemId: String
    var title: String
    var year: Int?
    var posterPath: String?
    var plays: Int
    var users: Int
    var hours: Double
}

struct TopUser: Codable, Hashable {
    var userId: String
    var name: String
    var plays: Int
    var hours: Double
    var lastSeen: Int64
    var lastPlayed: String
}

/// Plays by Direct Play, Direct Stream and Transcode for one player or viewer.
struct MethodSplit: Codable, Hashable {
    var name: String
    var direct: Int
    var remux: Int
    var transcode: Int
    var total: Int { direct + remux + transcode }
}

struct LibPlays: Codable, Hashable {
    var id: String
    var name: String
    var type: String
    var plays: Int
    var users: Int
    var hours: Double
}

struct PlayStats: Codable {
    var plays: Int
    var hours: Double
    var users: Int
    var titles: Int
    var daily: [DayStat]
    var byHour: [Int]
    var byWeekday: [Int] // Sunday first
    var byMethod: [StatCount]?
    var byClient: [StatCount]?
    var topMovies: [TopTitle]?
    var topShows: [TopTitle]?
    var topUsers: [TopUser]?
    var movieHours: Double?
    var episodeHours: Double?
    var popularMovies: [TopTitle]?
    var popularShows: [TopTitle]?
    var peakStreams: Int?
    var peakAt: Int64?
    var sourceResolutions: [StatCount]?
    var streamResolutions: [StatCount]?
    var byLibrary: [LibPlays]?
    var clientMethods: [MethodSplit]?
    var userMethods: [MethodSplit]?
    var monthly: [MonthStat]?
}

struct LibraryStats: Codable {
    struct Lib: Codable, Hashable {
        var id: String
        var name: String
        var type: String
        var items: Int
        var episodes: Int?
        var bytes: Int64
        var plays: Int?
        var hours: Double?
        var lastPlayed: String?
        var lastPlayedAt: Int64?
    }
    var movies: Int
    var shows: Int
    var episodes: Int
    var files: Int
    var bytes: Int64
    var hours: Double
    var resolutions: [StatCount]?
    var hdr: [StatCount]?
    var videoCodecs: [StatCount]?
    var containers: [StatCount]?
    var libraries: [Lib]?
}

/// A file's or a stream's tracks, as the dashboard describes them.
struct TrackInfo: Codable, Hashable {
    var container: String?
    var kbps: Int?
    var videoCodec: String?
    var videoProfile: String?
    var width: Int?
    var height: Int?
    var frameRate: Double?
    var hdr: String?
    var bitDepth: Int?
    var audioCodec: String?
    var audioChannels: Int?
    var audioLayout: String?
    var audioLanguage: String?
    var audioTitle: String?
    /// For a stream: "direct", "copy" or "transcode".
    var video: String?
    var audio: String?
}

/// What the server has sent a stream's player, and how its encode is going.
struct StreamDelivery: Codable, Hashable {
    var bytes: Int64?
    /// The send rate over the last ten seconds.
    var kbps: Int?
    var bufferSeconds: Double?
    var speed: Double?
    var encodeDone: Bool?
}

struct NowPlaying: Codable, Hashable, Identifiable {
    var sessionId: String
    var userId: String?
    var userName: String
    var itemId: String
    var kind: String? // movie, episode or live
    var title: String
    var subtitle: String?
    var year: Int?
    var posterPath: String?
    var backdropPath: String?
    var library: String?
    var state: String?
    var position: Double
    /// When position was reported, in server time.
    var positionAt: Double?
    var duration: Double
    var started: Int64?
    var client: String
    var ip: String?
    var local: Bool?
    var method: String
    var engine: String?
    var videoCopy: Bool?
    var reasons: [String]?
    var quality: String
    var source: TrackInfo?
    var stream: TrackInfo?
    var delivery: StreamDelivery?
    var hw: String?
    var hwDecode: Bool?
    var tonemap: Bool?
    var stripDolbyVision: Bool?
    var channelLogo: String?
    var id: String { sessionId }

    var isLive: Bool { kind == "live" }
    var isPaused: Bool { state == "paused" }
    /// Transcoding the audio while the video is copied.
    var audioOnly: Bool { method == "transcode" && videoCopy == true }
    /// Where playback is now, moved on from the last report.
    func position(at serverNow: Double) -> Double {
        var p = position + (isPaused ? 0 : max(0, serverNow - (positionAt ?? serverNow)))
        if duration > 0 { p = min(p, duration) }
        return p
    }
}

struct ActivityTotals: Codable, Hashable {
    var streams: Int
    var direct: Int
    var remux: Int
    var transcode: Int
    var kbps: Int
    var lanKbps: Int
    var wanKbps: Int
}

struct SystemSample: Codable, Hashable {
    var t: Int64
    var cpu: Double
    var mem: Int64
    var procCpu: Double
    var procMem: Int64
}

struct SystemInfo: Codable, Hashable {
    var os: String
    var arch: String
    var cpus: Int
    var memTotal: Int64
    /// False when the server can only see its own container.
    var host: Bool?
    /// "Container" or "Wren TV": what the second series measures.
    var procLabel: String?
    var procCpuAvailable: Bool?
    var memLimit: Int64?
    var cpuLimit: Double?
}

struct ActivityResponse: Codable {
    var serverTime: Double
    var sessions: [NowPlaying]
    var totals: ActivityTotals
    var system: SystemSample?
    var systemInfo: SystemInfo?
}

struct HardwareInfo: Codable, Hashable {
    var kind: String
    var h264Encoder: String?
    var hwDecode: Bool?
    var tonemap: String?
    var dovi: Bool?
}

struct DiskInfo: Codable, Hashable {
    var path: String
    var total: Int64
    var free: Int64
    var label: String
}

struct SystemResponse: Codable {
    var version: String?
    var hardware: HardwareInfo?
    var disks: [DiskInfo]?
    var samples: [SystemSample]?
    var info: SystemInfo?
    var uptime: Int64?
}

struct HistoryRow: Codable, Hashable, Identifiable {
    var id: String
    var userId: String?
    var userName: String
    var itemId: String?
    var kind: String
    var title: String
    var showTitle: String?
    var year: Int?
    var posterPath: String?
    var method: String
    var videoCopy: Bool?
    var height: Int?
    var client: String
    var startedAt: Int64
    var lastSeenAt: Int64?
    var position: Double?
    var duration: Double?
    var playedSeconds: Double
    var completed: Bool
    var ip: String?
    var quality: String?
    var streamHeight: Int?
    var reasons: String?
}

struct HistoryPage: Codable {
    var total: Int
    var rows: [HistoryRow]
}

struct Viewer: Codable, Hashable, Identifiable {
    var userId: String
    var name: String
    var admin: Bool
    var joined: Int64
    var plays: Int
    var hours: Double
    var lastSeen: Int64?
    var lastPlayed: String?
    var lastClient: String?
    var lastIp: String?
    var id: String { userId }
}

struct ViewerDetail: Codable {
    struct Totals: Codable, Hashable {
        var label: String
        var plays: Int
        var hours: Double
    }
    struct UsedFrom: Codable, Hashable {
        var name: String
        var plays: Int
        var hours: Double
        var lastSeen: Int64
        var detail: String?
    }
    var userId: String
    var name: String
    var admin: Bool
    var joined: Int64
    var plays: Int
    var hours: Double
    var lastSeen: Int64?
    var totals: [Totals]
    var players: [UsedFrom]
    var addresses: [UsedFrom]
    var topTitles: [TopTitle]
    var methods: MethodSplit
    var recent: [HistoryRow]
}

struct RecentlyAdded: Codable, Hashable {
    var itemId: String
    var kind: String
    var title: String
    var showId: String?
    var showTitle: String?
    var season: Int?
    var episode: Int?
    var year: Int?
    var posterPath: String?
    var addedAt: Int64
}

struct StatsResponse: Codable {
    var days: Int
    var stats: PlayStats
    var library: LibraryStats
    var nowPlaying: [NowPlaying]?
    var recent: [HistoryRow]?
    var recentlyAdded: [RecentlyAdded]?
}

// MARK: Live TV

/// A programme in the guide. Times are Unix seconds.
struct LiveProgram: Codable, Hashable {
    var title: String
    var start: Int64
    var end: Int64
    var description: String?
    var episode: String?

    var startDate: Date { Date(timeIntervalSince1970: TimeInterval(start)) }
    var endDate: Date { Date(timeIntervalSince1970: TimeInterval(end)) }

    func progress(at now: Date) -> Double {
        guard end > start else { return 0 }
        return min(max((now.timeIntervalSince1970 - TimeInterval(start)) / TimeInterval(end - start), 0), 1)
    }

    /// "8:00 – 9:30 PM"
    var timeRange: String {
        let f = Date.FormatStyle(date: .omitted, time: .shortened)
        return "\(startDate.formatted(f)) – \(endDate.formatted(f))"
    }
}

struct LiveChannel: Codable, Hashable, Identifiable {
    var id: String
    var name: String
    var number: String?
    /// Absolute, or a path on the server.
    var logo: String?
    var group: String?
    var now: LiveProgram?
    var next: LiveProgram?
}

struct LiveChannelsResponse: Codable {
    var channels: [LiveChannel]
}

/// One channel's programmes over the next few hours.
struct LiveGuideChannel: Codable {
    var id: String
    var programs: [LiveProgram]
}

struct LiveGuideResponse: Codable {
    var channels: [LiveGuideChannel]
}

/// How to play a channel: the server proxies the provider's stream, so the
/// URL is the server's, session-scoped like a film's.
struct LivePlayResponse: Codable {
    var url: String
    /// "hls" or "ts" (a plain MPEG-TS stream).
    var type: String
    var sessionId: String
}

/// What the full-screen player plays.
struct PlayTarget: Codable, Hashable, Identifiable {
    var itemId: String
    var title: String
    var subtitle: String?
    var fromStart = false
    /// A specific version (file) to play; nil lets the server choose.
    var fileId: String?
    /// A downloaded copy on this device, played without the server.
    var localFile: String?
    var localPosition: Double?

    var id: String { [itemId, fileId ?? "", localFile ?? "", fromStart ? "start" : ""].joined(separator: "|") }
}

/// Which channel the live player opens on, and the channels its channel up
/// and down step through.
struct LiveTarget: Hashable, Identifiable {
    var channelId: String
    var group: String?
    var id: String { channelId }
}

/// A sign-in code a TV shows for a server with its own usernames.
struct PairStart: Decodable, Sendable {
    var code: String
    var secret: String
    var expiresIn: Int?
    var path: String?
}

struct PairPoll: Decodable, Sendable {
    var status: String
    var token: String?
}
