using System.Globalization;
using System.Text.Json.Serialization;

namespace WrenTV.Core;

// The server's JSON, field for field (camelCase on the wire).

public sealed class WatchState
{
    public double Position { get; set; }
    public double Duration { get; set; }
    public bool Watched { get; set; }
    public double Fraction => Duration > 0 ? Math.Clamp(Position / Duration, 0, 1) : 0;
}

public sealed class Item
{
    public string Id { get; set; } = "";
    public string LibraryId { get; set; } = "";
    public string Kind { get; set; } = "";
    public string? ParentId { get; set; }
    public string? ShowId { get; set; }
    public string Title { get; set; } = "";
    public string? OriginalTitle { get; set; }
    public int? Year { get; set; }
    public string? Overview { get; set; }
    public string? Tagline { get; set; }
    public int? TmdbId { get; set; }
    public int SeasonNumber { get; set; }
    public int EpisodeNumber { get; set; }
    public string? PosterPath { get; set; }
    public string? BackdropPath { get; set; }
    public string? StillPath { get; set; }
    public string? LogoPath { get; set; }
    public double? Rating { get; set; }
    public string? ContentRating { get; set; }
    public List<string>? Genres { get; set; }
    public int? RuntimeMin { get; set; }
    public string? AirDate { get; set; }
    public string MatchState { get; set; } = "";
    public long AddedAt { get; set; }
    public WatchState? Progress { get; set; }
    public string? ShowTitle { get; set; }
    public int? ChildCount { get; set; }
    public int? UnwatchedCount { get; set; }

    public bool IsPlayable => Kind is "movie" or "episode";
    public bool IsWatched => Progress?.Watched ?? false;
    public bool InProgress => Progress is { Watched: false, Position: > 30 };
    /// "S1 · E4" for episodes.
    public string EpisodeLabel => $"S{SeasonNumber} · E{EpisodeNumber}";
    public string DisplayTitle => Kind == "season" ? (SeasonNumber == 0 ? "Specials" : $"Season {SeasonNumber}") : Title;
}

public sealed class Library
{
    public string Id { get; set; } = "";
    public string Name { get; set; } = "";
    public string Type { get; set; } = "movies"; // movies | shows
    public List<string> Paths { get; set; } = [];
    /// "files" (folders on disk) or "stream" (titles streamed through debrid).
    public string? Source { get; set; }
    public long? LastScanAt { get; set; }
    public int? ItemCount { get; set; }
    public bool IsStream => Source == "stream";
    /// Segoe Fluent Icons glyph.
    public string Glyph => Type == "shows" ? "" : "";
}

public sealed class User
{
    public string Id { get; set; } = "";
    public string Username { get; set; } = "";
    public string DisplayName { get; set; } = "";
    public bool IsAdmin { get; set; }
    public string? Role { get; set; }
    /// Marked watched on Simkl while the server doesn't have it.
    public bool Watched { get; set; }
}

public sealed class SupabaseInfo
{
    public string Url { get; set; } = "";
    public string PublishableKey { get; set; } = "";

    /// The Wren TV account service. The publishable key is public: every
    /// account server hands it to its clients in /api/server.
    public static readonly SupabaseInfo WrenTV = new()
    {
        Url = "https://buycndonhmcafzzcvmgu.supabase.co",
        PublishableKey = "sb_publishable_vNiXbucy_8L_RQBFPoBPvA_HNZv1i15",
    };
}

public sealed class ServerInfo
{
    public string Name { get; set; } = "";
    public string MachineId { get; set; } = "";
    public string Version { get; set; } = "";
    public string AuthMode { get; set; } = "";
    public bool SetupRequired { get; set; }
    public bool? Streaming { get; set; }
    public SupabaseInfo? Supabase { get; set; }
    public string? ServerId { get; set; }
    public List<string>? LocalUrls { get; set; }
    public bool? LiveTv { get; set; }
    /// Account servers also take local (username) accounts the admin added.
    public bool? LocalAccounts { get; set; }

    public bool UsesAccounts => AuthMode == "supabase" && Supabase != null;
    /// A local server with no accounts yet: the first sign-in creates the owner.
    public bool NeedsOwner => SetupRequired && !UsesAccounts;
    public string Identity => string.IsNullOrEmpty(ServerId) ? MachineId : ServerId!;
}

public sealed class AuthResponse
{
    public string Token { get; set; } = "";
    public User User { get; set; } = new();
}

public sealed class MeResponse
{
    public User User { get; set; } = new();
    public UserLimitsInfo? Limits { get; set; }
}

public sealed class UserLimitsInfo
{
    public bool CanStream { get; set; }
    public bool CanDownload { get; set; }
    public int MaxStreams { get; set; }
    public int MaxDownloads { get; set; }
}

public sealed class HomeShelf
{
    public Library Library { get; set; } = new();
    public List<Item> Items { get; set; } = [];
}

public sealed class HomeResponse
{
    public List<Item> ContinueWatching { get; set; } = [];
    public List<HomeShelf> RecentlyAdded { get; set; } = [];
    public List<Item>? TopRated { get; set; }
    public List<GenreRow>? Genres { get; set; }
    public List<CollectionShelf>? Collections { get; set; }
    public List<Item>? Watchlist { get; set; }
}

public sealed class DiscoverRow
{
    public string Id { get; set; } = "";
    public string Title { get; set; } = "";
    public List<TitleView> Items { get; set; } = [];
}

public sealed class DiscoverHome
{
    public bool Enabled { get; set; }
    public bool? Streaming { get; set; }
    public List<Item> ContinueWatching { get; set; } = [];
    public List<Item> RecentlyWatched { get; set; } = [];
    public List<Item>? Watchlist { get; set; }
    public List<TitleView> Hero { get; set; } = [];
    public List<DiscoverRow> Rows { get; set; } = [];
}

public sealed class CollectionShelf
{
    public WrenCollection Collection { get; set; } = new();
    public List<Item> Items { get; set; } = [];
    public List<TitleView>? Missing { get; set; }
}

public sealed class WrenCollection
{
    public string Id { get; set; } = "";
    public string Name { get; set; } = "";
    public string Description { get; set; } = "";
    public string Kind { get; set; } = "";
    public string? LibraryId { get; set; }
    public string? PosterPath { get; set; }
    public string? BackdropPath { get; set; }
    public string Sort { get; set; } = "";
    public bool ShowOnHome { get; set; }
    public int? ItemCount { get; set; }
    /// Collections, Franchises, Lists, Charts or Smart Collections.
    public string? Group { get; set; }
}

/// Episodes airing from a week back to a month ahead (GET /api/calendar).
public sealed class CalendarResponse
{
    public string From { get; set; } = "";
    public string To { get; set; } = "";
    public List<CalendarEpisode> Episodes { get; set; } = [];
}

public sealed class CalendarEpisode
{
    public string Date { get; set; } = ""; // YYYY-MM-DD
    public Item Show { get; set; } = new();
    public int SeasonNumber { get; set; }
    public int EpisodeNumber { get; set; }
    public string Title { get; set; } = "";
    public string? Overview { get; set; }
    public string? StillPath { get; set; }
    /// The episode in the library, when it's there.
    public string? ItemId { get; set; }
    public bool Watched { get; set; }
    /// The viewer has watched some of the show; UpNext is the episode they're up to.
    public bool Watching { get; set; }
    public bool UpNext { get; set; }

    public string Code => $"S{SeasonNumber} · E{EpisodeNumber}";
    public string Name => Title.Length > 0 ? Title : $"Episode {EpisodeNumber}";
}

public sealed class CollectionDetail
{
    public WrenCollection Collection { get; set; } = new();
    public List<Item> Items { get; set; } = [];
    public List<TitleView>? Missing { get; set; }
    public List<TitleView>? Titles { get; set; }
    /// A big collection's shelves: indexes into Titles when there are any, else Items.
    public List<CollectionRow>? Rows { get; set; }
}

public sealed class CollectionRow
{
    public string Title { get; set; } = "";
    public List<int> Index { get; set; } = [];
}

public sealed class GenreCount
{
    public string Genre { get; set; } = "";
    public int Count { get; set; }
    public int Movies { get; set; }
    public int Shows { get; set; }
    public string? BackdropPath { get; set; }
}

public sealed class GenreRow
{
    public string Genre { get; set; } = "";
    public List<Item> Items { get; set; } = [];
}

/// The account's poster size: small, medium or large, the same on every device.
public sealed class PosterPref
{
    public string? PosterSize { get; set; }
}

public sealed class UserPrefs
{
    public string AudioLanguage { get; set; } = "";
    /// Subtitles turn on in this language when a file has them; empty is off.
    public string? SubtitleLanguage { get; set; }
    /// How far in (percent) a title counts as watched.
    public int? WatchedPercent { get; set; }
    /// Seconds watched before a title is on Continue Watching.
    public int? ContinueAfter { get; set; }

    public static readonly int[] WatchedPercents = [50, 60, 70, 75, 80, 85, 90, 95, 98];
    public static readonly (int Seconds, string Label)[] ContinueAfters =
        [(10, "10 seconds"), (30, "30 seconds"), (60, "1 minute"), (120, "2 minutes"), (300, "5 minutes"), (600, "10 minutes")];
}

/// The caller's Simkl link, from /api/me/simkl.
public sealed class SimklStatus
{
    public bool Available { get; set; }
    public bool Linked { get; set; }
    public SimklAccount? Account { get; set; }
    public SimklPending? Pending { get; set; }
    public bool Importing { get; set; }

    /// Worth checking again soon: waiting on approval or an import.
    public bool Busy => (Pending != null && string.IsNullOrEmpty(Pending.Error)) || Importing || Account?.ImportStatus == "running";
}

public sealed class SimklAccount
{
    public string? Username { get; set; }
    public bool Scrobble { get; set; }
    public string? ImportStatus { get; set; }
    public long? ImportAt { get; set; }
    public int ImportMovies { get; set; }
    public int ImportEpisodes { get; set; }
}

public sealed class SimklPending
{
    public string UserCode { get; set; } = "";
    public string Url { get; set; } = "";
    public string? UrlComplete { get; set; }
    public string? Error { get; set; }
}

/// A TMDB title (trending, more like this), linked to the library item when the server has it.
public sealed class TitleView
{
    public int TmdbId { get; set; }
    public string MediaType { get; set; } = "movie"; // movie | show
    public string Title { get; set; } = "";
    public int? Year { get; set; }
    public string? Overview { get; set; }
    public string? PosterPath { get; set; }
    public string? BackdropPath { get; set; }
    public double? Rating { get; set; }
    public Item? Item { get; set; }
    /// On a person's page, who they play or what they did.
    public string? Role { get; set; }

    /// "Role · 2019" on a person's page, else the year.
    public string Caption => Role is { } r ? string.Join(" · ", new[] { r, Year?.ToString() }.Where(x => !string.IsNullOrEmpty(x))) : Year?.ToString() ?? "";
}

/// An actor or crew member, with rows of what they're in (Known for, In your library, Movies, TV Shows).
public sealed class Person
{
    public int Id { get; set; }
    public string Name { get; set; } = "";
    public string? Biography { get; set; }
    public string? Birthday { get; set; }
    public string? Deathday { get; set; }
    public string? PlaceOfBirth { get; set; }
    public string? ProfilePath { get; set; }
    public string? KnownFor { get; set; }
    public string? ImdbId { get; set; }
    public string? BackdropPath { get; set; }
    public List<CreditRow> Rows { get; set; } = [];

    /// "Born March 3, 1970 (age 56)", "Died …", the place they were born.
    public List<string> Facts
    {
        get
        {
            var out_ = new List<string>();
            DateTime? b = DateTime.TryParse(Birthday, out var bd) ? bd : null;
            DateTime? d = DateTime.TryParse(Deathday, out var dd) ? dd : null;
            static int Years(DateTime from, DateTime to) => to.Year - from.Year - (to.DayOfYear < from.DayOfYear ? 1 : 0);
            if (b is { } born) out_.Add(d == null ? $"Born {born:MMMM d, yyyy} (age {Years(born, DateTime.Today)})" : $"Born {born:MMMM d, yyyy}");
            if (d is { } died) out_.Add(b is { } bb ? $"Died {died:MMMM d, yyyy} (aged {Years(bb, died)})" : $"Died {died:MMMM d, yyyy}");
            if (!string.IsNullOrWhiteSpace(PlaceOfBirth)) out_.Add(PlaceOfBirth);
            return out_;
        }
    }
}

public sealed class CreditRow
{
    public string Title { get; set; } = "";
    public List<TitleView> Credits { get; set; } = [];
}

public sealed class CatalogResult
{
    public sealed class Genre
    {
        public int Id { get; set; }
        public string Name { get; set; } = "";
    }

    public bool Enabled { get; set; }
    public List<TitleView> Items { get; set; } = [];
    public List<Genre> Genres { get; set; } = [];
    public int Page { get; set; }
    public int TotalPages { get; set; }
}

public sealed class TrendingResponse
{
    public bool Enabled { get; set; }
    public List<TitleView> Items { get; set; } = [];
}

public sealed class Ratings
{
    public double? Tmdb { get; set; }
    public double? Imdb { get; set; }
    public int? RottenTomatoes { get; set; }
    public int? Metacritic { get; set; }
}

public sealed class CastMember
{
    public string Name { get; set; } = "";
    public string? Character { get; set; }
    public string? ProfilePath { get; set; }
    /// The TMDB person, for their page.
    public int? Id { get; set; }
}

public sealed class Trailer
{
    public string YoutubeKey { get; set; } = "";
    public string Name { get; set; } = "";
    public Uri Url => new($"https://www.youtube.com/watch?v={YoutubeKey}");
}

public sealed class Extras
{
    public Ratings Ratings { get; set; } = new();
    public bool Omdb { get; set; }
    public bool Tmdb { get; set; }
    public List<CastMember> Cast { get; set; } = [];
    public Trailer? Trailer { get; set; }
    public List<TitleView> MoreLikeThis { get; set; } = [];
}

public sealed class ItemPage
{
    public List<Item> Items { get; set; } = [];
    public int Total { get; set; }
}

public sealed class MediaStream
{
    public int Index { get; set; }
    public int TypeIndex { get; set; }
    public string Codec { get; set; } = "";
    public string? Profile { get; set; }
    public string? Language { get; set; }
    public string? Title { get; set; }
    public bool Default { get; set; }
    public bool Forced { get; set; }
    public int? Width { get; set; }
    public int? Height { get; set; }
    public string? Hdr { get; set; }
    public int? DvProfile { get; set; }
    public int? Channels { get; set; }

    public string Label
    {
        get
        {
            var parts = new List<string>();
            if (!string.IsNullOrEmpty(Language) && Language != "und") parts.Add(Languages.Name(Language));
            if (!string.IsNullOrEmpty(Title)) parts.Add(Title);
            parts.Add(CodecLabel);
            return string.Join(" · ", parts);
        }
    }

    public string CodecLabel
    {
        get
        {
            var atmos = Profile?.Contains("Atmos") == true;
            var c = Codec switch
            {
                "eac3" => atmos ? "Dolby Atmos" : "Dolby Digital+",
                "truehd" => atmos ? "TrueHD Atmos" : "TrueHD",
                "ac3" => "Dolby Digital",
                "dts" => Profile ?? "DTS",
                "subrip" => "SRT",
                "hdmv_pgs_subtitle" => "PGS",
                "ass" => "ASS",
                _ => Codec.ToUpperInvariant(),
            };
            if (Channels is > 0 and var n)
                c += " " + (n == 8 ? "7.1" : n == 6 ? "5.1" : n == 2 ? "Stereo" : $"{n}ch");
            return c;
        }
    }
}

public sealed class MediaInfo
{
    public string Container { get; set; } = "";
    public double Duration { get; set; }
    public long? Bitrate { get; set; }
    public List<MediaStream>? Video { get; set; }
    public List<MediaStream>? Audio { get; set; }
    public List<MediaStream>? Subtitles { get; set; }
    public List<Chapter>? Chapters { get; set; }
}

public sealed class MediaFileView
{
    public string Id { get; set; } = "";
    public long Size { get; set; }
    public string Container { get; set; } = "";
    public double Duration { get; set; }
    public string VideoCodec { get; set; } = "";
    public int Width { get; set; }
    public int Height { get; set; }
    public string? Hdr { get; set; }
    public int? DvProfile { get; set; }
    public string? StreamId { get; set; }
    public string? Release { get; set; }
    public string FileName { get; set; } = "";
    public string? Label { get; set; }
    public MediaInfo? Info { get; set; }

    /// Badges like "4K", "Dolby Vision", "Atmos".
    public List<string> Badges
    {
        get
        {
            var b = new List<string>();
            if (Width >= 3200) b.Add("4K"); else if (Width >= 1800) b.Add("1080p"); else if (Width >= 1200) b.Add("720p");
            switch (Hdr ?? "")
            {
                case "DV" or "DV+HDR10": b.Add("Dolby Vision"); break;
                case "HDR10": b.Add("HDR10"); break;
                case "HLG": b.Add("HLG"); break;
            }
            var a = Info?.Audio?.FirstOrDefault(x => x.Default) ?? Info?.Audio?.FirstOrDefault();
            if (a != null)
            {
                if (a.Profile?.Contains("Atmos") == true) b.Add("Atmos");
                else if (a.Channels is >= 6 and var ch) b.Add(ch == 8 ? "7.1" : "5.1");
            }
            return b;
        }
    }
}

public sealed class ItemDetail
{
    public Item Item { get; set; } = new();
    public List<Item>? Children { get; set; }
    public Item? NextEpisode { get; set; }
    public Item? Show { get; set; }
    public List<MediaFileView>? Files { get; set; }
    public bool? Streaming { get; set; }
    public bool? CanDownload { get; set; }
    public bool? OnWatchlist { get; set; }
}

public sealed class DiscoverSearch
{
    public bool Streaming { get; set; }
    public List<TitleView> Items { get; set; } = [];
}

public sealed class ClientCaps
{
    public List<string> Engines { get; set; } = [];
    public List<string> NativeVideo { get; set; } = [];
    public List<string> NativeAudio { get; set; } = [];
    public List<string> NativeContainers { get; set; } = [];
    public bool DolbyVision { get; set; }
    public bool Hdr { get; set; }
    public int MaxKbps { get; set; }

    /// mpv plays every container and codec, so the server direct-plays everything.
    public static ClientCaps Current() => new()
    {
        Engines = ["mpv"],
        NativeVideo = ["h264", "hevc", "av1", "vp9", "mpeg2video", "vc1"],
        NativeAudio = ["aac", "ac3", "eac3", "truehd", "dts", "flac", "opus", "mp3", "alac", "pcm_s16le", "pcm_s24le"],
        NativeContainers = ["mkv", "mp4", "mov", "webm", "ts", "avi"],
        DolbyVision = false,
        Hdr = true,
        MaxKbps = 0,
    };
}

public sealed class PlayRequest
{
    public string? FileId { get; set; }
    public int? AudioIndex { get; set; }
    public ClientCaps Caps { get; set; } = ClientCaps.Current();
}

public sealed class Decision
{
    public string Engine { get; set; } = "";
    public string Method { get; set; } = "";
    public string? AudioCodec { get; set; }
    public int? AudioIndex { get; set; }
}

public sealed class SubtitleTrack
{
    public int Index { get; set; }
    public string? Language { get; set; }
    public string? Title { get; set; }
    public string Codec { get; set; } = "";
    public bool Forced { get; set; }
    public bool Default { get; set; }
    public string? Url { get; set; }
}

public sealed class PlayResponse
{
    public string Status { get; set; } = "";
    public string? Message { get; set; }
    public string? SessionId { get; set; }
    public string? Engine { get; set; }
    public string? Method { get; set; }
    public string? Url { get; set; }
    public Decision? Decision { get; set; }
    public double? Duration { get; set; }
    public double? ResumePosition { get; set; }
    public MediaInfo? Info { get; set; }
    public List<SubtitleTrack>? Subtitles { get; set; }
    /// The subtitle track the viewer's language setting picks, or -1 for off.
    public int? SubtitleIndex { get; set; }
    public string? FileId { get; set; }
    public List<Chapter>? Chapters { get; set; }
    public Markers? Markers { get; set; }
    public List<MediaVersion>? Versions { get; set; }
}

public sealed class VersionsResponse
{
    public List<MediaVersion>? Versions { get; set; }
    public bool? Looking { get; set; }
}

public sealed class MediaVersion
{
    public string Id { get; set; } = "";
    public string Label { get; set; } = "";
    public int Height { get; set; }
}

public sealed class Chapter
{
    public double Start { get; set; }
    public double End { get; set; }
    public string Title { get; set; } = "";
}

public sealed class TimeRange
{
    public double Start { get; set; }
    public double End { get; set; }
}

/// The skippable parts of a file, from chapter names or audio fingerprinting.
public sealed class Markers
{
    public TimeRange? Intro { get; set; }
    public TimeRange? Credits { get; set; }
}

public sealed class ServerDownload
{
    public string Id { get; set; } = "";
    public string? ItemId { get; set; }
    public string Title { get; set; } = "";
    public string Quality { get; set; } = "";
    public string Status { get; set; } = "";
    public long Bytes { get; set; }
    public long Total { get; set; }
    public string? Error { get; set; }
    public long CreatedAt { get; set; }
    public bool Active => Status is "queued" or "downloading";
    public double Fraction => Total > 0 ? Math.Min(1, (double)Bytes / Total) : 0;
}

/// A server account with its streaming limits (-1 = server default, 0 = no limit).
public sealed class ServerUser
{
    public string Id { get; set; } = "";
    public string Username { get; set; } = "";
    public string DisplayName { get; set; } = "";
    public string? Email { get; set; }
    public bool IsAdmin { get; set; }
    public bool AllowStreaming { get; set; }
    public int AllowDownloads { get; set; }
    public int MaxStreams { get; set; }
    public int MaxDownloads { get; set; }
    public UserLimitsInfo Effective { get; set; } = new();
}

public sealed class UserLimitsPatch
{
    public bool AllowStreaming { get; set; }
    public int AllowDownloads { get; set; }
    public int MaxStreams { get; set; }
    public int MaxDownloads { get; set; }
}

public sealed class ServerEvent
{
    public string Type { get; set; } = "";
    public string? LibraryId { get; set; }
    public string? ItemId { get; set; }
    public string? Message { get; set; }
}

// Live TV

/// A programme in the guide. Times are Unix seconds.
public sealed class LiveProgram
{
    public string Title { get; set; } = "";
    public long Start { get; set; }
    public long End { get; set; }
    public string? Description { get; set; }
    public string? Episode { get; set; }

    public DateTimeOffset StartDate => DateTimeOffset.FromUnixTimeSeconds(Start).ToLocalTime();
    public DateTimeOffset EndDate => DateTimeOffset.FromUnixTimeSeconds(End).ToLocalTime();
    public double ProgressAt(DateTimeOffset now) =>
        End > Start ? Math.Clamp((now.ToUnixTimeSeconds() - Start) / (double)(End - Start), 0, 1) : 0;
    /// "8:00 – 9:30 PM"
    public string TimeRange => $"{StartDate.LocalDateTime.ToShortTimeString()} – {EndDate.LocalDateTime.ToShortTimeString()}";
}

public sealed class LiveChannel
{
    public string Id { get; set; } = "";
    public string Name { get; set; } = "";
    public string? Number { get; set; }
    public string? Logo { get; set; }
    public string? Group { get; set; }
    public LiveProgram? Now { get; set; }
    public LiveProgram? Next { get; set; }
}

public sealed class LiveChannelsResponse
{
    public List<LiveChannel> Channels { get; set; } = [];
}

public sealed class LivePlayResponse
{
    public string Url { get; set; } = "";
    public string Type { get; set; } = "";
    public string SessionId { get; set; } = "";
}

// Sign-in codes

/// A TV waiting to be signed in with a code.
public sealed class PairInfo
{
    public string Device { get; set; } = "TV";
    public bool Approved { get; set; }
}

public sealed class PairStart
{
    public string Code { get; set; } = "";
    public string Secret { get; set; } = "";
    public int? ExpiresIn { get; set; }
    public string? Path { get; set; }
}

public sealed class PairPoll
{
    public string Status { get; set; } = "";
    public string? Token { get; set; }
}

/// What the player plays: an item, optionally from the start or in a given version.
public sealed record PlayTarget(string ItemId, string Title, string? Subtitle, bool FromStart = false, string? FileId = null);

static class Languages
{
    /// "English" for "en" or "eng"; the code itself when unknown.
    public static string Name(string code)
    {
        try
        {
            var c = CultureInfo.GetCultures(CultureTypes.NeutralCultures)
                .FirstOrDefault(x => x.TwoLetterISOLanguageName == code || x.ThreeLetterISOLanguageName == code);
            return c?.EnglishName ?? code;
        }
        catch { return code; }
    }

    /// The choices the language pickers offer: (code, name).
    public static readonly (string Code, string Name)[] Common =
    [
        ("en", "English"), ("es", "Spanish"), ("fr", "French"), ("de", "German"), ("it", "Italian"),
        ("pt", "Portuguese"), ("nl", "Dutch"), ("sv", "Swedish"), ("no", "Norwegian"), ("da", "Danish"),
        ("fi", "Finnish"), ("pl", "Polish"), ("ru", "Russian"), ("uk", "Ukrainian"), ("tr", "Turkish"),
        ("ar", "Arabic"), ("he", "Hebrew"), ("hi", "Hindi"), ("ja", "Japanese"), ("ko", "Korean"),
        ("zh", "Chinese"), ("th", "Thai"), ("vi", "Vietnamese"), ("id", "Indonesian"), ("el", "Greek"),
        ("cs", "Czech"), ("hu", "Hungarian"), ("ro", "Romanian"),
    ];
}

/// A hub as a tile: a genre, a streaming service or an admin's own.
public sealed class HubCard
{
    public string Id { get; set; } = "";
    public string Name { get; set; } = "";
    public string Kind { get; set; } = "custom"; // genre | service | custom
    public string? Tagline { get; set; }
    public string? BackdropPath { get; set; }
    public string? LogoPath { get; set; }
    /// "#rrggbb" behind a service's logo.
    public string? Color { get; set; }

    public string KindLabel => Kind switch { "genre" => "Genre", "service" => "Streaming service", _ => "Hub" };
}

/// One row of a page or hub, resolved by the server.
public sealed class LayoutRow
{
    public string Id { get; set; } = "";
    /// "landscape", "items", "titles", "hubs" or "collection".
    public string Kind { get; set; } = "items";
    public string Title { get; set; } = "";
    public string? Subtitle { get; set; }
    public List<Item>? Items { get; set; }
    public List<TitleView>? Titles { get; set; }
    public List<HubCard>? Hubs { get; set; }
    /// A hub row's "tiles" or "logos".
    public string? Style { get; set; }
    public WrenCollection? Collection { get; set; }
    public List<TitleView>? Missing { get; set; }
    public bool Ranked { get; set; }
    /// Set when the row is a single hub's first shelf.
    public string? HubId { get; set; }
    /// Whether there's more than the row shows, so a "View all" is worth having.
    public bool More { get; set; }
}

/// A page's rows: "home", "movies", "shows" or "library:<id>".
public sealed class LayoutResponse
{
    public List<LayoutRow> Rows { get; set; } = [];
    public bool Custom { get; set; }
    /// Where the next page of rows starts, while more remain.
    public int? NextOffset { get; set; }
}

public sealed class HubResponse
{
    public HubCard Hub { get; set; } = new();
    public List<LayoutRow> Rows { get; set; } = [];
}

/// A row as the layout editor sees it.
public sealed class LayoutEntry
{
    public string Type { get; set; } = "";
    public string Ref { get; set; } = "";
    public bool Hidden { get; set; }
    public string? Title { get; set; }
    public string? Detail { get; set; }

    public string Key => $"{Type}|{Ref}";
}

public sealed class LayoutConfig
{
    public List<LayoutEntry> Entries { get; set; } = [];
    /// Whether this is a saved layout of its own rather than the inherited one.
    public bool Custom { get; set; }
    public bool HasDefault { get; set; }
    public List<LayoutEntry> Options { get; set; } = [];
}
