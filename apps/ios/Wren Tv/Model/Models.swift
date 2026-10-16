import Foundation

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

    /// Signs in with a Wren TV account (email) rather than a server-local username.
    var usesAccounts: Bool { authMode == "supabase" && supabase != nil }
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

    var kindLabel: String {
        switch kind {
        case "smart": "Smart"
        case "tmdb": "From TMDB"
        default: "Hand-picked"
        }
    }
}

struct CollectionDetail: Codable {
    var collection: WrenCollection
    var items: [Item]
    var missing: [TitleView]?
    /// When streaming, a TMDB collection's every title, in its order.
    var titles: [TitleView]?
}

struct GenreCount: Codable, Hashable, Identifiable {
    var genre: String
    var count: Int
    var movies: Int
    var shows: Int
    var backdropPath: String?
    var id: String { genre }
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

struct PlayStats: Codable {
    var plays: Int
    var hours: Double
    var users: Int
    var titles: Int
    var daily: [DayStat]
    var byHour: [Int]
    var byWeekday: [Int]
    var byMethod: [StatCount]?
    var byClient: [StatCount]?
    var topMovies: [TopTitle]?
    var topShows: [TopTitle]?
    var topUsers: [TopUser]?
}

struct LibraryStats: Codable {
    struct Lib: Codable, Hashable {
        var id: String
        var name: String
        var type: String
        var items: Int
        var bytes: Int64
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

struct NowPlaying: Codable, Hashable, Identifiable {
    var sessionId: String
    var userName: String
    var itemId: String
    var title: String
    var subtitle: String?
    var posterPath: String?
    var backdropPath: String?
    var method: String
    var quality: String
    var client: String
    var position: Double
    var duration: Double
    var id: String { sessionId }
}

struct HistoryRow: Codable, Hashable, Identifiable {
    var id: String
    var userName: String
    var itemId: String?
    var kind: String
    var title: String
    var showTitle: String?
    var method: String
    var client: String
    var startedAt: Int64
    var playedSeconds: Double
    var completed: Bool
}

struct StatsResponse: Codable {
    var days: Int
    var stats: PlayStats
    var library: LibraryStats
    var nowPlaying: [NowPlaying]?
    var recent: [HistoryRow]?
}
