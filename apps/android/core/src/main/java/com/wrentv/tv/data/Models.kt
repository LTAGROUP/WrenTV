package com.wrentv.tv.data

import kotlinx.serialization.Serializable
import java.util.Locale

@Serializable
data class WatchState(val position: Double = 0.0, val duration: Double = 0.0, val watched: Boolean = false, val updatedAt: Long = 0) {
    val fraction: Double get() = if (duration > 0) (position / duration).coerceIn(0.0, 1.0) else 0.0
}

@Serializable
data class Item(
    val id: String,
    val libraryId: String = "",
    val kind: String = "movie",
    val parentId: String? = null,
    val showId: String? = null,
    val title: String = "",
    val originalTitle: String? = null,
    val year: Int? = null,
    val overview: String? = null,
    val tagline: String? = null,
    val tmdbId: Int? = null,
    val seasonNumber: Int = 0,
    val episodeNumber: Int = 0,
    val posterPath: String? = null,
    val backdropPath: String? = null,
    val stillPath: String? = null,
    val logoPath: String? = null,
    val rating: Double? = null,
    val contentRating: String? = null,
    val genres: List<String>? = null,
    val runtimeMin: Int? = null,
    val airDate: String? = null,
    val matchState: String = "",
    val addedAt: Long = 0,
    val progress: WatchState? = null,
    val showTitle: String? = null,
    val childCount: Int? = null,
    val unwatchedCount: Int? = null,
) {
    val isPlayable: Boolean get() = kind == "movie" || kind == "episode"
    val isWatched: Boolean get() = progress?.watched ?: false
    val inProgress: Boolean get() = progress?.let { !it.watched && it.position > 30 } ?: false

    /** "S1 · E4" for episodes. */
    val episodeLabel: String get() = "S$seasonNumber · E$episodeNumber"

    val displayTitle: String
        get() = if (kind == "season") (if (seasonNumber == 0) "Specials" else "Season $seasonNumber") else title
}

@Serializable
data class Library(
    val id: String,
    val name: String = "",
    val type: String = "movies",
    val paths: List<String> = emptyList(),
    /** "files" (folders on disk) or "stream" (titles streamed through debrid). */
    val source: String? = null,
    val lastScanAt: Long? = null,
    val itemCount: Int? = null,
) {
    val isStream: Boolean get() = source == "stream"
}

@Serializable
data class User(
    val id: String,
    val username: String = "",
    val displayName: String = "",
    val isAdmin: Boolean = false,
    val role: String? = null,
)

@Serializable
data class SupabaseInfo(val url: String, val publishableKey: String)

@Serializable
data class ServerInfo(
    val name: String = "",
    val machineId: String = "",
    val version: String = "",
    val authMode: String = "local",
    val setupRequired: Boolean = false,
    /** Streaming through the owner's debrid service is on and working. */
    val streaming: Boolean? = null,
    val supabase: SupabaseInfo? = null,
    /** The server's id in the account service (account servers only). */
    val serverId: String? = null,
    val localUrls: List<String>? = null,
    /** The admin has set up live TV (IPTV) and it has channels. */
    val liveTv: Boolean? = null,
    /** Account servers also take local (username) accounts the admin added. */
    val localAccounts: Boolean? = null,
) {
    /** Signs in with a Wren TV account (email) rather than a server-local username. */
    val usesAccounts: Boolean get() = authMode == "supabase" && supabase != null
    /** A local server with no accounts yet: the first sign-in creates the owner. */
    val needsOwner: Boolean get() = setupRequired && !usesAccounts
    val addressId: String get() = serverId?.takeIf { it.isNotEmpty() } ?: machineId
}

@Serializable
data class AuthResponse(val token: String, val user: User)

@Serializable
data class MeResponse(val user: User, val limits: UserLimitsInfo? = null)

/** What the signed-in user may do, with server defaults applied. */
@Serializable
data class UserLimitsInfo(
    val canStream: Boolean = true,
    val canDownload: Boolean = false,
    val maxStreams: Int = 0,
    val maxDownloads: Int = 0,
)

/** A TV sign-in code waiting to be approved from a phone or browser. */
@Serializable
data class PairStart(val code: String, val secret: String, val expiresIn: Int = 600, val path: String = "")

@Serializable
data class PairPoll(val status: String, val token: String? = null, val user: User? = null)

@Serializable
data class HomeResponse(
    val continueWatching: List<Item> = emptyList(),
    val recentlyAdded: List<Shelf> = emptyList(),
    val topRated: List<Item>? = null,
    val genres: List<GenreRow>? = null,
    val collections: List<CollectionShelf>? = null,
    val watchlist: List<Item>? = null,
) {
    @Serializable
    data class Shelf(val library: Library, val items: List<Item> = emptyList())
}

/** The streaming home page: rows of TMDB titles. */
@Serializable
data class DiscoverHome(
    val enabled: Boolean = false,
    val streaming: Boolean? = null,
    val continueWatching: List<Item> = emptyList(),
    val recentlyWatched: List<Item> = emptyList(),
    val watchlist: List<Item>? = null,
    val hero: List<TitleView> = emptyList(),
    val rows: List<Row> = emptyList(),
) {
    @Serializable
    data class Row(val id: String, val title: String, val items: List<TitleView> = emptyList())
}

@Serializable
data class CollectionShelf(val collection: WrenCollection, val items: List<Item> = emptyList(), val missing: List<TitleView>? = null)

/** A page of TMDB's catalog for the Movies and TV Shows pages. */
@Serializable
data class CatalogPage(
    val enabled: Boolean = false,
    val items: List<TitleView> = emptyList(),
    val genres: List<Genre> = emptyList(),
    val page: Int = 1,
    val totalPages: Int = 1,
) {
    @Serializable
    data class Genre(val id: Int, val name: String)
}

/** A collection: hand-picked, smart (rules) or synced from TMDB. */
@Serializable
data class WrenCollection(
    val id: String,
    val name: String = "",
    val description: String = "",
    val kind: String = "manual",
    val libraryId: String? = null,
    val posterPath: String? = null,
    val backdropPath: String? = null,
    val sort: String = "",
    val showOnHome: Boolean = false,
    val showMissing: Boolean = false,
    val visibleFrom: String = "",
    val visibleUntil: String = "",
    val syncError: String? = null,
    val itemCount: Int? = null,
    val active: Boolean? = null,
    /** The shelf it sits on with the rest: "Franchises", "Lists", "Charts"… */
    val group: String? = null,
) {
    val kindLabel: String
        get() = when (kind) {
            "smart" -> "Smart"
            "tmdb" -> "TMDB"
            else -> "Hand-picked"
        }
}

@Serializable
data class CollectionDetail(
    val collection: WrenCollection,
    val items: List<Item> = emptyList(),
    val missing: List<TitleView>? = null,
    /** When streaming, a TMDB collection's every title, in its order. */
    val titles: List<TitleView>? = null,
    /** A big collection as rows, like Home: indexes into [titles] (or [items]). */
    val rows: List<CollectionRowIndex>? = null,
)

/** A page of a collection's one shelf, in its order ([offset] of [total]). */
@Serializable
data class CollectionPage(
    val collection: WrenCollection,
    val entries: List<TitleView> = emptyList(),
    val total: Int = 0,
    val offset: Int = 0,
    /** "12 in your library", "40 titles", "8 in your library · 4 not". */
    val countLabel: String = "",
)

@Serializable
data class CollectionRowIndex(val title: String, val index: List<Int> = emptyList())

@Serializable
data class GenreCount(
    val genre: String,
    val count: Int = 0,
    val movies: Int = 0,
    val shows: Int = 0,
    val backdropPath: String? = null,
)

@Serializable
data class UserPrefs(
    val audioLanguage: String = "",
    /** Subtitles turn on in this language when a file has them; empty is off. */
    val subtitleLanguage: String? = null,
    /** With subtitles off, forced tracks (foreign-language lines only) still show. */
    val forcedSubtitles: Boolean = true,
    /** Subtitles in the audio language turn on when a title plays in another language. */
    val foreignAudioSubtitles: Boolean = true,
    /** How far in (50–98%) a movie or episode counts as watched. */
    val watchedPercent: Int = 90,
    /** Seconds (10–1800) watched before a title is on Continue Watching. */
    val continueAfter: Int = 30,
)

/** The choices the settings offer, as on the web. */
val WatchedPercents = listOf(50, 60, 70, 75, 80, 85, 90, 95, 98)
val ContinueAfters = listOf(10 to "10 seconds", 30 to "30 seconds", 60 to "1 minute", 120 to "2 minutes", 300 to "5 minutes", 600 to "10 minutes")

fun continueAfterLabel(s: Int): String = ContinueAfters.firstOrNull { it.first == s }?.second ?: if (s % 60 == 0) "${s / 60} minutes" else "$s seconds"

/** The caller's Simkl link, from /api/me/simkl. */
@Serializable
data class SimklStatus(
    val available: Boolean = false,
    val linked: Boolean = false,
    val account: SimklAccount? = null,
    val pending: SimklPending? = null,
    val importing: Boolean = false,
) {
    /** Worth checking again soon: waiting on approval or an import. */
    val busy: Boolean get() = (pending != null && pending.error.isNullOrEmpty()) || importing || account?.importStatus == "running"
}

@Serializable
data class SimklAccount(
    val username: String = "",
    val scrobble: Boolean = false,
    val importStatus: String = "",
    val importAt: Long = 0,
    val importMovies: Int = 0,
    val importEpisodes: Int = 0,
)

@Serializable
data class SimklPending(val userCode: String = "", val url: String = "", val urlComplete: String? = null, val error: String? = null)

@Serializable
data class PairInfo(val device: String = "TV", val approved: Boolean = false)

@Serializable
data class GenreRow(val genre: String, val items: List<Item> = emptyList())

/** A TMDB title, linked to the library item when the server has it. */
@Serializable
data class TitleView(
    val tmdbId: Int,
    val mediaType: String = "movie",
    val title: String = "",
    val year: Int? = null,
    val overview: String? = null,
    val posterPath: String? = null,
    val backdropPath: String? = null,
    val rating: Double? = null,
    val item: Item? = null,
    /** On a person's page, who they play or what they did. */
    val role: String? = null,
    /** Marked watched on Simkl while the server doesn't have it. */
    val watched: Boolean = false,
) {
    val id: String get() = "$mediaType:$tmdbId"
}

/** An actor or crew member, with rows of what they're in (Known for, In your library, Movies, TV Shows). */
@Serializable
data class Person(
    val id: Int,
    val name: String = "",
    val biography: String? = null,
    val birthday: String? = null,
    val deathday: String? = null,
    val placeOfBirth: String? = null,
    val profilePath: String? = null,
    val knownFor: String? = null,
    val imdbId: String? = null,
    val backdropPath: String? = null,
    val rows: List<CreditRow> = emptyList(),
) {
    /** "Born March 3, 1970 (age 56)", "Died …", "Los Angeles, California, USA". */
    val facts: List<String>
        get() {
            val out = mutableListOf<String>()
            val b = birthday?.let { runCatching { java.time.LocalDate.parse(it) }.getOrNull() }
            val d = deathday?.let { runCatching { java.time.LocalDate.parse(it) }.getOrNull() }
            val fmt = java.time.format.DateTimeFormatter.ofLocalizedDate(java.time.format.FormatStyle.LONG)
            if (b != null) {
                val age = java.time.Period.between(b, d ?: java.time.LocalDate.now()).years
                out += if (d == null) "Born ${b.format(fmt)} (age $age)" else "Born ${b.format(fmt)}"
            }
            if (d != null) out += "Died ${d.format(fmt)}" + (if (b != null) " (aged ${java.time.Period.between(b, d).years})" else "")
            placeOfBirth?.takeIf { it.isNotBlank() }?.let { out += it }
            return out
        }
}

@Serializable
data class CreditRow(val title: String, val credits: List<TitleView> = emptyList())

/** A title's line under its poster on a person's page: "Role · 2019". */
val TitleView.roleLine: String? get() = role?.let { r -> listOfNotNull(r.ifBlank { null }, year?.toString()).joinToString(" · ") }

/** Just the poster size from the account's preferences, so changing it doesn't resend the rest. */
@Serializable
data class PosterPref(val posterSize: String = "medium")

@Serializable
data class TrendingResponse(val enabled: Boolean = false, val items: List<TitleView> = emptyList())

@Serializable
data class Ratings(
    val tmdb: Double? = null,
    val tmdbVotes: Int? = null,
    val imdb: Double? = null,
    val imdbVotes: Int? = null,
    val rottenTomatoes: Int? = null,
    val metacritic: Int? = null,
)

@Serializable
/** [id] is the TMDB person, for their page. */
data class CastMember(val name: String, val character: String? = null, val profilePath: String? = null, val id: Int? = null)

@Serializable
data class Trailer(val youtubeKey: String, val name: String = "")

@Serializable
data class Extras(
    val ratings: Ratings = Ratings(),
    /** Whether IMDb / Rotten Tomatoes / Metacritic ratings are configured. */
    val omdb: Boolean = false,
    val tmdb: Boolean = false,
    val cast: List<CastMember> = emptyList(),
    val trailer: Trailer? = null,
    val moreLikeThis: List<TitleView> = emptyList(),
    /** The movie series it's part of, in release order. */
    val franchise: Franchise? = null,
    /** The studios that made it; each has a page. */
    val studios: List<Studio> = emptyList(),
)

@Serializable
data class Franchise(val id: Int, val name: String = "", val backdropPath: String? = null, val titles: List<TitleView> = emptyList())

@Serializable
data class Studio(val id: Int, val name: String = "", val logoPath: String? = null)

/** A studio's page: who they are and shelves of what they made. */
@Serializable
data class Company(
    val id: Int,
    val name: String = "",
    val logoPath: String? = null,
    val description: String? = null,
    val headquarters: String? = null,
    val originCountry: String? = null,
    val backdropPath: String? = null,
    val rows: List<StudioRow> = emptyList(),
)

/** One shelf of a studio's titles; [WrenTVApi.catalog] with its type, sort and the studio pages through the rest. */
@Serializable
data class StudioRow(
    val id: String,
    val title: String = "",
    val mediaType: String = "movie",
    val sort: String = "popular",
    val items: List<TitleView> = emptyList(),
    val totalPages: Int = 1,
)

@Serializable
data class ItemPage(val items: List<Item> = emptyList(), val total: Int = 0)

fun languageName(code: String?): String? {
    if (code.isNullOrEmpty() || code == "und") return null
    val name = Locale.forLanguageTag(code).getDisplayLanguage(Locale.getDefault())
    return name.ifEmpty { code }.replaceFirstChar { it.titlecase(Locale.getDefault()) }
}

@Serializable
data class MediaStream(
    val index: Int = 0,
    val typeIndex: Int = 0,
    val codec: String = "",
    val profile: String? = null,
    val language: String? = null,
    val title: String? = null,
    val default: Boolean = false,
    val forced: Boolean = false,
    val width: Int? = null,
    val height: Int? = null,
    val hdr: String? = null,
    val dvProfile: Int? = null,
    val channels: Int? = null,
    val channelLayout: String? = null,
) {
    val label: String
        get() = listOfNotNull(languageName(language), title?.takeIf { it.isNotEmpty() }, codecLabel).joinToString(" · ")

    val codecLabel: String
        get() {
            var c = when (codec) {
                "eac3" -> if (profile?.contains("Atmos") == true) "Dolby Atmos" else "Dolby Digital+"
                "truehd" -> if (profile?.contains("Atmos") == true) "TrueHD Atmos" else "TrueHD"
                "ac3" -> "Dolby Digital"
                "dts" -> profile ?: "DTS"
                "subrip" -> "SRT"
                "hdmv_pgs_subtitle" -> "PGS"
                "ass" -> "ASS"
                else -> codec.uppercase()
            }
            val ch = channels
            if (ch != null && ch > 0) {
                c += " " + when (ch) {
                    8 -> "7.1"
                    6 -> "5.1"
                    2 -> "Stereo"
                    else -> "${ch}ch"
                }
            }
            return c
        }
}

@Serializable
data class MediaInfo(
    val container: String = "",
    val duration: Double = 0.0,
    val bitrate: Long? = null,
    val video: List<MediaStream>? = null,
    val audio: List<MediaStream>? = null,
    val subtitles: List<MediaStream>? = null,
    val chapters: List<Chapter>? = null,
)

@Serializable
data class MediaFileView(
    val id: String,
    val size: Long = 0,
    val container: String = "",
    val duration: Double = 0.0,
    val videoCodec: String = "",
    val width: Int = 0,
    val height: Int = 0,
    val hdr: String? = null,
    val dvProfile: Int? = null,
    /** Set when the file is a saved debrid stream rather than a file on disk. */
    val streamId: String? = null,
    /** The release a stream comes from. */
    val release: String? = null,
    val fileName: String = "",
    val label: String? = null,
    val info: MediaInfo? = null,
) {
    /** Badges like "4K", "Dolby Vision", "Atmos". */
    val badges: List<String>
        get() {
            val b = mutableListOf<String>()
            when {
                width >= 3200 -> b += "4K"
                width >= 1800 -> b += "1080p"
                width >= 1200 -> b += "720p"
            }
            when (hdr) {
                "DV", "DV+HDR10" -> b += "Dolby Vision"
                "HDR10" -> b += "HDR10"
                "HLG" -> b += "HLG"
            }
            val a = info?.audio?.firstOrNull { it.default } ?: info?.audio?.firstOrNull()
            if (a != null) {
                if (a.profile?.contains("Atmos") == true) b += "Atmos"
                else if ((a.channels ?: 0) >= 6) b += if (a.channels == 8) "7.1" else "5.1"
            }
            return b
        }
}

@Serializable
data class ItemDetail(
    val item: Item,
    val children: List<Item>? = null,
    val nextEpisode: Item? = null,
    val show: Item? = null,
    val files: List<MediaFileView>? = null,
    /** The item lives in a streaming library. */
    val streaming: Boolean? = null,
    val canDownload: Boolean? = null,
    /** Null on servers from before the watchlist. */
    val onWatchlist: Boolean? = null,
)

/** A device download link: the original file (or stream) for this device. */
@Serializable
data class DeviceDownload(
    val status: String = "",
    val message: String? = null,
    val url: String? = null,
    val fileName: String? = null,
    val fileId: String? = null,
    val size: Long? = null,
    val label: String? = null,
    val expiresAt: Long? = null,
)

/** Search results from TMDB, linked to library items when the server has them. */
@Serializable
data class DiscoverSearch(val streaming: Boolean = false, val items: List<TitleView> = emptyList())

@Serializable
data class ClientCaps(
    val engines: List<String>,
    val nativeVideo: List<String>,
    val nativeAudio: List<String>,
    val nativeContainers: List<String>,
    val dolbyVision: Boolean,
    val hdr: Boolean,
    /** The display can't show HDR and nothing here tone-maps it: the server sends SDR. */
    val needsSdr: Boolean = false,
    val maxKbps: Int = 0,
    val audioFallback: String? = null,
)

@Serializable
data class PlayRequest(
    val fileId: String? = null,
    val audioIndex: Int? = null,
    val caps: ClientCaps,
    val maxWidth: Int? = null,
    /** Started again after a failure: the server resolves a fresh link. */
    val retry: Boolean = false,
    /** The version that broke while playing; the server plays another if it has one. */
    val failedFileId: String? = null,
)

@Serializable
data class Decision(
    val engine: String = "",
    val method: String = "",
    val audioCodec: String? = null,
    val audioIndex: Int? = null,
    val reasons: List<String>? = null,
)

@Serializable
data class SubtitleTrack(
    val index: Int,
    val language: String? = null,
    val title: String? = null,
    val codec: String = "",
    val forced: Boolean = false,
    val default: Boolean = false,
    val url: String? = null,
)

@Serializable
data class PlayResponse(
    val status: String = "",
    val message: String? = null,
    val sessionId: String? = null,
    val engine: String? = null,
    val method: String? = null,
    val url: String? = null,
    val decision: Decision? = null,
    val duration: Double? = null,
    val resumePosition: Double? = null,
    val info: MediaInfo? = null,
    val subtitles: List<SubtitleTrack>? = null,
    /** The subtitle track the viewer's language setting picks, or -1 for off. */
    val subtitleIndex: Int? = null,
    val fileId: String? = null,
    val chapters: List<Chapter>? = null,
    val markers: Markers? = null,
    /** The item's files, when it has more than one, for a version picker. */
    val versions: List<MediaVersion>? = null,
    /** Seek bar frames, when the server makes them. */
    val thumbs: SeekThumbs? = null,
    /** The server can search subtitle sites for this title. */
    val onlineSubtitles: Boolean = false,
)

/** A subtitle a subtitle site has for the title. */
@Serializable
data class OnlineSubtitle(
    val provider: String,
    val id: String,
    val name: String = "",
    val language: String = "",
    val downloads: Int = 0,
    val hearingImpaired: Boolean = false,
    val machineTranslated: Boolean = false,
)

@Serializable
data class OnlineSubtitleSearch(val enabled: Boolean = false, val results: List<OnlineSubtitle> = emptyList())

/** Frame n (url + n + ".jpg") is the picture at n × interval seconds. */
@Serializable
data class SeekThumbs(val url: String, val interval: Double, val width: Int = 320)

@Serializable
data class MediaVersion(val id: String, val label: String = "", val height: Int = 0)

@Serializable
data class VersionsResponse(val versions: List<MediaVersion> = emptyList(), val looking: Boolean = false)

@Serializable
data class Chapter(val start: Double = 0.0, val end: Double = 0.0, val title: String = "")

@Serializable
data class TimeRange(val start: Double, val end: Double)

/** The skippable parts of a file, from chapter names or audio fingerprinting. */
@Serializable
data class Markers(val intro: TimeRange? = null, val credits: TimeRange? = null, val source: String? = null)

/** A streamed title being saved to the server as a file. */
@Serializable
data class ServerDownload(
    val id: String,
    val itemId: String? = null,
    val title: String = "",
    val quality: String = "",
    val status: String = "",
    val path: String? = null,
    val bytes: Long = 0,
    val total: Long = 0,
    val error: String? = null,
    val createdAt: Long = 0,
) {
    val active: Boolean get() = status == "queued" || status == "downloading"
    val fraction: Float get() = if (total > 0) (bytes.toFloat() / total).coerceAtMost(1f) else 0f
}

@Serializable
data class WebhookSetting(val url: String = "", val secret: String? = null, val events: List<String>? = null)

@Serializable
data class ServerSettings(
    val serverName: String = "",
    val publicUrl: String = "",
    val hwaccel: String = "",
    val tmdbKeySet: Boolean = false,
    val omdbKeySet: Boolean? = null,
    val markerFingerprinting: Boolean? = null,
    val markerCommunity: Boolean? = null,
    val streaming: StreamingSettings = StreamingSettings(),
    val userDefaults: UserDefaultsSettings = UserDefaultsSettings(),
    val webhooks: List<WebhookSetting>? = null,
    val integrationKey: String = "",
    val restartRequired: Boolean? = null,
)

@Serializable
data class StreamingSettings(
    val enabled: Boolean = false,
    val active: Boolean = false,
    val debrid: List<DebridService> = emptyList(),
    val addons: List<AddonSetting> = emptyList(),
    val qualities: List<String> = emptyList(),
    val maxSizeGB: Map<String, Double>? = null,
    val prefetch: Boolean = false,
    val lookupOnOpen: Boolean = true,
    val mode: String = "stream",
    val movieLibrary: String = "",
    val showLibrary: String = "",
    val downloadQuality: String = "1080p",
    val limits: StreamingLimits = StreamingLimits(),
)

@Serializable
data class DebridService(val provider: String, val name: String = "", val keySet: Boolean = false, val keyHint: String? = null)

@Serializable
data class AddonSetting(val name: String = "", val url: String = "", val enabled: Boolean = true)

@Serializable
data class StreamingLimits(
    val maxStreams: Int = 0,
    val bandwidthMbps: Double = 0.0,
    val perStreamMbps: Double = 0.0,
    val downloadMbps: Double = 0.0,
    val maxDownloads: Int = 0,
    val prefetchWorkers: Int = 0,
    val downloadQuotaGB: Double = 0.0,
    val minFreeGB: Double = 0.0,
)

@Serializable
data class UserDefaultsSettings(val maxStreams: Int = 0, val deviceDownloads: Boolean = true, val maxDownloads: Int = 0)

/** Result of checking a debrid API key. */
@Serializable
data class DebridAccount(
    val provider: String = "",
    val ok: Boolean = false,
    val user: String? = null,
    val premium: Boolean = false,
    val expires: String? = null,
    val error: String? = null,
)

/** A server account with its streaming limits (-1 = server default, 0 = no limit). */
@Serializable
data class ServerUser(
    val id: String,
    val username: String = "",
    val displayName: String = "",
    val email: String? = null,
    val isAdmin: Boolean = false,
    val allowStreaming: Boolean = true,
    val allowDownloads: Int = -1,
    val maxStreams: Int = -1,
    val maxDownloads: Int = -1,
    val effective: UserLimitsInfo = UserLimitsInfo(),
)

@Serializable
data class UserLimitsPatch(val allowStreaming: Boolean, val allowDownloads: Int, val maxStreams: Int, val maxDownloads: Int)

@Serializable
data class ServerEvent(val type: String = "", val libraryId: String? = null, val itemId: String? = null, val message: String? = null)

// Stats

@Serializable
data class StatCount(val name: String, val count: Int)

@Serializable
data class DayStat(
    val date: String,
    val plays: Int,
    val hours: Double,
    val movies: Int? = null,
    val episodes: Int? = null,
    val direct: Int? = null,
    val remux: Int? = null,
    val transcode: Int? = null,
    /** The most streams at once that day. */
    val peak: Int? = null,
)

@Serializable
data class TopTitle(
    val itemId: String,
    val title: String,
    val year: Int? = null,
    val posterPath: String? = null,
    val plays: Int = 0,
    val users: Int = 0,
    val hours: Double = 0.0,
)

@Serializable
data class TopUser(
    val userId: String,
    val name: String,
    val plays: Int = 0,
    val hours: Double = 0.0,
    val lastSeen: Long = 0,
    val lastPlayed: String = "",
)

@Serializable
data class PlayStats(
    val plays: Int = 0,
    val hours: Double = 0.0,
    val users: Int = 0,
    val titles: Int = 0,
    val daily: List<DayStat> = emptyList(),
    val byHour: List<Int> = emptyList(),
    val byWeekday: List<Int> = emptyList(),
    val byMethod: List<StatCount>? = null,
    val byClient: List<StatCount>? = null,
    val topMovies: List<TopTitle>? = null,
    val topShows: List<TopTitle>? = null,
    val topUsers: List<TopUser>? = null,
)

@Serializable
data class LibraryStats(
    val movies: Int = 0,
    val shows: Int = 0,
    val episodes: Int = 0,
    val files: Int = 0,
    val bytes: Long = 0,
    val hours: Double = 0.0,
    val resolutions: List<StatCount>? = null,
    val hdr: List<StatCount>? = null,
    val videoCodecs: List<StatCount>? = null,
    val containers: List<StatCount>? = null,
)

/** A file's tracks, or what a stream sends of them. */
@Serializable
data class TrackInfo(
    val container: String? = null,
    val kbps: Int? = null,
    val videoCodec: String? = null,
    val videoProfile: String? = null,
    val width: Int? = null,
    val height: Int? = null,
    val frameRate: Double? = null,
    val hdr: String? = null,
    val audioCodec: String? = null,
    val audioChannels: Int? = null,
    val audioLayout: String? = null,
    val audioLanguage: String? = null,
    /** For a stream: "direct", "copy" or "transcode". */
    val video: String? = null,
    val audio: String? = null,
)

/** What the server has sent a stream's player, and how its encode is going. */
@Serializable
data class StreamDelivery(
    val bytes: Long? = null,
    /** The send rate over the last ten seconds. */
    val kbps: Int? = null,
    val bufferSeconds: Double? = null,
    val speed: Double? = null,
    val encodeDone: Boolean? = null,
)

@Serializable
data class NowPlaying(
    val sessionId: String,
    val userName: String = "",
    val itemId: String = "",
    /** movie, episode or live */
    val kind: String? = null,
    val title: String = "",
    val subtitle: String? = null,
    val year: Int? = null,
    val posterPath: String? = null,
    val backdropPath: String? = null,
    val library: String? = null,
    val state: String? = null,
    val method: String = "",
    val engine: String? = null,
    val videoCopy: Boolean? = null,
    val reasons: List<String>? = null,
    val quality: String = "",
    val client: String = "",
    val ip: String? = null,
    val local: Boolean? = null,
    val position: Double = 0.0,
    /** When position was reported, in server time. */
    val positionAt: Double? = null,
    val duration: Double = 0.0,
    val source: TrackInfo? = null,
    val stream: TrackInfo? = null,
    val delivery: StreamDelivery? = null,
    val hw: String? = null,
    val hwDecode: Boolean? = null,
    val tonemap: Boolean? = null,
    val stripDolbyVision: Boolean? = null,
    val channelLogo: String? = null,
) {
    val isLive get() = kind == "live"
    val isPaused get() = state == "paused"
    /** Transcoding the audio while the video is copied. */
    val audioOnly get() = method == "transcode" && videoCopy == true

    /** Where playback is now, moved on from the last report. */
    fun positionAt(serverNow: Double): Double {
        var p = position + if (isPaused) 0.0 else maxOf(0.0, serverNow - (positionAt ?: serverNow))
        if (duration > 0) p = minOf(p, duration)
        return p
    }
}

@Serializable
data class ActivityTotals(
    val streams: Int = 0,
    val direct: Int = 0,
    val remux: Int = 0,
    val transcode: Int = 0,
    val kbps: Int = 0,
    val lanKbps: Int = 0,
    val wanKbps: Int = 0,
)

/** The machine's and the server's own CPU (percent) and memory (bytes) at a moment. */
@Serializable
data class SystemSample(val t: Long = 0, val cpu: Double = 0.0, val mem: Long = 0, val procCpu: Double = 0.0, val procMem: Long = 0)

@Serializable
data class SystemInfo(
    val os: String = "",
    val arch: String = "",
    val cpus: Int = 0,
    val memTotal: Long = 0,
    /** False when the server can only see its own container. */
    val host: Boolean? = null,
    /** "Container" or "Wren TV": what the second series measures. */
    val procLabel: String? = null,
    val procCpuAvailable: Boolean? = null,
)

@Serializable
data class ActivityResponse(
    val serverTime: Double = 0.0,
    val sessions: List<NowPlaying> = emptyList(),
    val totals: ActivityTotals = ActivityTotals(),
    val system: SystemSample? = null,
    val systemInfo: SystemInfo? = null,
)

@Serializable
data class DiskInfo(val path: String = "", val total: Long = 0, val free: Long = 0, val label: String = "")

@Serializable
data class SystemResponse(
    val version: String? = null,
    val disks: List<DiskInfo>? = null,
    val samples: List<SystemSample>? = null,
    val info: SystemInfo? = null,
    val uptime: Long? = null,
)

@Serializable
data class HistoryRow(
    val id: String,
    val userName: String = "",
    val itemId: String? = null,
    val kind: String = "",
    val title: String = "",
    val showTitle: String? = null,
    val method: String = "",
    val client: String = "",
    val startedAt: Long = 0,
    val playedSeconds: Double = 0.0,
    val completed: Boolean = false,
)

@Serializable
data class StatsResponse(
    val days: Int = 30,
    val stats: PlayStats = PlayStats(),
    val library: LibraryStats = LibraryStats(),
    val nowPlaying: List<NowPlaying>? = null,
    val recent: List<HistoryRow>? = null,
)

// Live TV

/** A programme in the guide. Times are Unix seconds. */
@Serializable
data class LiveProgram(
    val title: String = "",
    val start: Long = 0,
    val end: Long = 0,
    val description: String? = null,
    val episode: String? = null,
) {
    fun progress(now: Long): Float = if (end > start) ((now - start).toFloat() / (end - start)).coerceIn(0f, 1f) else 0f
}

@Serializable
data class LiveChannel(
    val id: String,
    val name: String = "",
    val number: String? = null,
    /** Absolute, or a path on the server. */
    val logo: String? = null,
    val group: String? = null,
    val now: LiveProgram? = null,
    val next: LiveProgram? = null,
)

@Serializable
data class LiveChannelsResponse(val channels: List<LiveChannel> = emptyList())

/** One channel's programmes over the next few hours. */
@Serializable
data class LiveGuideChannel(val id: String, val programs: List<LiveProgram> = emptyList())

@Serializable
data class LiveGuideResponse(val channels: List<LiveGuideChannel> = emptyList())

@Serializable
data class LivePlayRequest(val caps: ClientCaps)

/**
 * How to play a channel. The server proxies (or remuxes) the provider's
 * stream, so the URL is the server's, session-scoped like a film's.
 */
@Serializable
data class LivePlayResponse(
    val url: String? = null,
    /** "hls" or "ts" (a plain MPEG-TS stream). */
    val type: String? = null,
    val sessionId: String? = null,
    val message: String? = null,
)

/** A hub as a tile: a genre, a streaming service or an admin's own. */
@Serializable
data class HubCard(
    val id: String,
    val name: String = "",
    val kind: String = "custom",
    val tagline: String? = null,
    val backdropPath: String? = null,
    val logoPath: String? = null,
    /** "#rrggbb" behind a service's logo. */
    val color: String? = null,
)

/** One row of a page or hub, resolved by the server. */
@Serializable
data class LayoutRow(
    val id: String,
    /** "landscape", "items", "titles", "hubs" or "collection". */
    val kind: String = "items",
    val title: String = "",
    val subtitle: String? = null,
    val items: List<Item>? = null,
    val titles: List<TitleView>? = null,
    val hubs: List<HubCard>? = null,
    /** A hub row's "tiles" or "logos". */
    val style: String? = null,
    val collection: WrenCollection? = null,
    val missing: List<TitleView>? = null,
    val ranked: Boolean = false,
    /** A collection row has more than it shows. */
    val more: Boolean = false,
    /** Set when the row is a single hub's first shelf. */
    val hubId: String? = null,
)

@Serializable
data class LayoutPage(
    val rows: List<LayoutRow> = emptyList(),
    val custom: Boolean = false,
    /** Where the next page of rows starts; null once there are no more. */
    val nextOffset: Int? = null,
)

@Serializable
data class HubPage(val hub: HubCard, val rows: List<LayoutRow> = emptyList())

/** A row as the layout editor sees it. */
@Serializable
data class LayoutEntry(
    val type: String,
    val ref: String,
    val hidden: Boolean = false,
    val title: String = "",
    val detail: String? = null,
) {
    val key: String get() = "$type|$ref"
}

@Serializable
data class LayoutConfig(
    val entries: List<LayoutEntry> = emptyList(),
    /** Whether this is a saved layout of its own rather than the inherited one. */
    val custom: Boolean = false,
    val hasDefault: Boolean = false,
    val options: List<LayoutEntry> = emptyList(),
)

/** Episodes airing over a stretch of days, from shows in the library and ones being watched. */
@Serializable
data class CalendarResponse(val from: String, val to: String, val episodes: List<CalendarEpisode> = emptyList())

@Serializable
data class CalendarEpisode(
    /** YYYY-MM-DD */
    val date: String,
    val show: Item,
    val seasonNumber: Int,
    val episodeNumber: Int,
    val title: String = "",
    val overview: String? = null,
    val stillPath: String? = null,
    /** The episode in the library, when it's there. */
    val itemId: String? = null,
    val watched: Boolean = false,
    /** Some of the show has been watched; [upNext] is the episode they're up to. */
    val watching: Boolean = false,
    val upNext: Boolean = false,
) {
    val id: String get() = "${show.id}:$seasonNumber:$episodeNumber"
    val code: String get() = "S$seasonNumber E$episodeNumber"
}
