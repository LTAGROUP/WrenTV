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

    public bool UsesAccounts => AuthMode == "supabase" && Supabase != null;
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
}

public sealed class CollectionDetail
{
    public WrenCollection Collection { get; set; } = new();
    public List<Item> Items { get; set; } = [];
    public List<TitleView>? Missing { get; set; }
    public List<TitleView>? Titles { get; set; }
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

public sealed class UserPrefs
{
    public string AudioLanguage { get; set; } = "";
    /// Subtitles turn on in this language when a file has them; empty is off.
    public string? SubtitleLanguage { get; set; }
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
