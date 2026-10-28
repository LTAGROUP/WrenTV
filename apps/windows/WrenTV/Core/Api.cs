using System.Net;
using System.Net.Http.Headers;
using System.Runtime.CompilerServices;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace WrenTV.Core;

public sealed class WrenTVException(string message, int status = 0) : Exception(message)
{
    public int Status { get; } = status;
}

static class Json
{
    public static readonly JsonSerializerOptions Options = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.CamelCase,
        PropertyNameCaseInsensitive = true,
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
        NumberHandling = JsonNumberHandling.AllowReadingFromString,
    };

    public static T Parse<T>(string s) => JsonSerializer.Deserialize<T>(s, Options)!;
    public static string Write<T>(T v) => JsonSerializer.Serialize(v, Options);
}

/// Thin async client for the Wren TV server API.
public sealed class WrenTVApi
{
    public Uri BaseUrl { get; }
    public TokenSource? Auth { get; }

    internal static readonly HttpClient Http = new(new SocketsHttpHandler
    {
        PooledConnectionLifetime = TimeSpan.FromMinutes(5),
        AutomaticDecompression = DecompressionMethods.All,
    })
    { Timeout = Timeout.InfiniteTimeSpan };

    public const string ClientName = "Wren TV for Windows";

    public WrenTVApi(Uri baseUrl, TokenSource? auth = null)
    {
        BaseUrl = baseUrl;
        Auth = auth;
    }

    public Uri Url(string path, IEnumerable<(string, string)>? query = null)
    {
        var b = new UriBuilder(BaseUrl);
        b.Path = b.Path.TrimEnd('/') + path;
        if (query != null)
            b.Query = string.Join("&", query.Select(q => $"{Uri.EscapeDataString(q.Item1)}={Uri.EscapeDataString(q.Item2)}"));
        return b.Uri;
    }

    /// Absolute URL for a server-relative path like "/stream/…".
    public Uri Absolute(string path) => new(BaseUrl, path);

    public Uri? ImageUrl(string? path, string size = "w342")
    {
        if (string.IsNullOrEmpty(path)) return null;
        return Url($"/api/images/{size}/{path.TrimStart('/')}");
    }

    public async Task<(string Body, int Status)> Send(HttpMethod method, string path, IEnumerable<(string, string)>? query = null,
                                                       object? body = null, TimeSpan? timeout = null)
    {
        using var req = new HttpRequestMessage(method, Url(path, query));
        req.Headers.Add("X-WrenTV-Client", ClientName);
        if (Auth != null) req.Headers.Authorization = new AuthenticationHeaderValue("Bearer", await Auth.Token());
        if (body != null) req.Content = new StringContent(JsonSerializer.Serialize(body, body.GetType(), Json.Options), Encoding.UTF8, "application/json");
        using var cts = new CancellationTokenSource(timeout ?? TimeSpan.FromSeconds(30));
        HttpResponseMessage resp;
        try { resp = await Http.SendAsync(req, cts.Token); }
        catch (OperationCanceledException) { throw new WrenTVException("The server took too long to answer."); }
        catch (HttpRequestException e) { throw new WrenTVException(e.Message); }
        using (resp)
        {
            var text = await resp.Content.ReadAsStringAsync();
            var status = (int)resp.StatusCode;
            if (status >= 400)
            {
                string? msg = null;
                try { msg = JsonDocument.Parse(text).RootElement.GetProperty("error").GetString(); } catch { }
                throw new WrenTVException(msg ?? resp.ReasonPhrase ?? $"Error {status}", status);
            }
            return (text, status);
        }
    }

    public async Task<T> Get<T>(string path, IEnumerable<(string, string)>? query = null) =>
        Json.Parse<T>((await Send(HttpMethod.Get, path, query)).Body);

    public async Task<T> Post<T>(string path, object? body = null, TimeSpan? timeout = null) =>
        Json.Parse<T>((await Send(HttpMethod.Post, path, body: body, timeout: timeout)).Body);

    // Endpoints

    public Task<ServerInfo> ServerInfo() => Get<ServerInfo>("/api/server");

    public Task<AuthResponse> Login(string username, string password) =>
        Post<AuthResponse>("/api/auth/login", new { username, password, device = Environment.MachineName });

    public Task<AuthResponse> SetupOwner(string username, string password) =>
        Post<AuthResponse>("/api/setup/owner", new { username, password, displayName = username });

    public Task<PairStart> StartPair(string device) => Post<PairStart>("/api/pair", new { device });
    public Task<PairPoll> PollPair(string code, string secret) => Post<PairPoll>($"/api/pair/{code}/poll", new { secret });

    public Task<MeResponse> Me() => Get<MeResponse>("/api/me");
    public Task<HomeResponse> Home() => Get<HomeResponse>("/api/home");
    public Task<List<Library>> Libraries() => Get<List<Library>>("/api/libraries");

    public Task<ItemPage> Items(string library, string sort, string? genre = null, bool unwatched = false)
    {
        var q = new List<(string, string)> { ("sort", sort), ("limit", "5000") };
        if (genre != null) q.Add(("genre", genre));
        if (unwatched) q.Add(("unwatched", "1"));
        return Get<ItemPage>($"/api/libraries/{library}/items", q);
    }

    public Task<List<GenreCount>> Genres(string? library = null) =>
        Get<List<GenreCount>>("/api/genres", library == null ? null : [("libraryId", library)]);

    public Task<ItemPage> GenreItems(string genre, string? library = null, string sort = "title")
    {
        var q = new List<(string, string)> { ("genre", genre), ("sort", sort) };
        if (library != null) q.Add(("libraryId", library));
        return Get<ItemPage>("/api/genres/items", q);
    }

    public Task<List<WrenCollection>> Collections(string? library = null) =>
        Get<List<WrenCollection>>("/api/collections", library == null ? null : [("libraryId", library)]);

    /// from and to are YYYY-MM-DD; a week at a time.
    public Task<CalendarResponse> Calendar(string from, string to) => Get<CalendarResponse>("/api/calendar", [("from", from), ("to", to)]);

    public Task<Person> Person(int id) => Get<Person>($"/api/people/{id}");

    /// Collections whose names match, a franchise's among them.
    public Task<List<WrenCollection>> SearchCollections(string q) => Get<List<WrenCollection>>("/api/collections", [("q", q)]);

    /// sort is null for the collection's own order (and a big one's rows).
    public Task<CollectionDetail> Collection(string id, string? sort = null) =>
        Get<CollectionDetail>($"/api/collections/{id}", sort == null ? null : [("sort", sort)]);

    /// A page of the collection in order (or by sort), limit titles from offset.
    public Task<CollectionPageData> CollectionPage(string id, string? sort, int offset, int limit = 40)
    {
        var q = new List<(string, string)> { ("limit", limit.ToString()), ("offset", offset.ToString()) };
        if (sort != null) q.Add(("sort", sort));
        return Get<CollectionPageData>($"/api/collections/{id}", q.ToArray());
    }

    public Task<UserPrefs> Prefs() => Get<UserPrefs>("/api/me/prefs");
    public async Task<UserPrefs> SetPrefs(UserPrefs p) => Json.Parse<UserPrefs>((await Send(HttpMethod.Patch, "/api/me/prefs", body: p)).Body);
    /// Just the poster size, so changing it doesn't resend the rest.
    public async Task SetPosterSize(string size) => await Send(HttpMethod.Patch, "/api/me/prefs", body: new PosterPref { PosterSize = size });
    public async Task<string?> PosterSize() => (await Get<PosterPref>("/api/me/prefs")).PosterSize;

    // Simkl and Trakt: what's watched here is sent there, and its history imported.
    public Task<TrackerStatus> TrackerState(Tracker t) => Get<TrackerStatus>($"/api/me/{t.Id()}");
    public Task TrackerLink(Tracker t) => Send(HttpMethod.Post, $"/api/me/{t.Id()}/link");
    public Task TrackerCancelLink(Tracker t) => Send(HttpMethod.Delete, $"/api/me/{t.Id()}/link");
    public Task TrackerUnlink(Tracker t) => Send(HttpMethod.Delete, $"/api/me/{t.Id()}");
    public Task TrackerImport(Tracker t) => Send(HttpMethod.Post, $"/api/me/{t.Id()}/import");
    public Task TrackerScrobble(Tracker t, bool on) => Send(HttpMethod.Patch, $"/api/me/{t.Id()}", body: new { scrobble = on });

    /// A TV waiting to be signed in with a code, and signing it in as the caller.
    public Task<PairInfo> PairInfo(string code) => Get<PairInfo>($"/api/pair/{code}");
    public Task ApprovePair(string code) => Send(HttpMethod.Post, $"/api/pair/{code}/approve");

    public Task<ItemDetail> Item(string id) => Get<ItemDetail>($"/api/items/{id}");
    public Task<Extras> Extras(string id) => Get<Extras>($"/api/items/{id}/extras");
    public Task<DiscoverHome> DiscoverHome() => Get<DiscoverHome>("/api/discover/home");
    public Task<TrendingResponse> Trending() => Get<TrendingResponse>("/api/discover/trending");
    /// Logo paths by title id ("movie:<tmdbId>"), "" for a title without one.
    public Task<Dictionary<string, string>> TitleLogos(IEnumerable<string> ids) =>
        Get<Dictionary<string, string>>("/api/discover/logos", [("ids", string.Join(",", ids))]);

    /// TMDB's whole catalog of movies or shows ("movie" | "show"), a page at a time.
    public Task<CatalogResult> Catalog(string type, string sort, int? genre, int page, int? company = null) =>
        Get<CatalogResult>("/api/discover/browse", [("type", type), ("sort", sort), ("genre", genre?.ToString() ?? ""), ("page", page.ToString()), ("company", company?.ToString() ?? "")]);
    public Task<Company> Company(int id) => Get<Company>($"/api/companies/{id}");
    public Task<List<Item>> Search(string q) => Get<List<Item>>("/api/search", [("q", q)]);

    public Task<PlayResponse> Play(string id, PlayRequest req) =>
        Post<PlayResponse>($"/api/items/{id}/play", req, TimeSpan.FromSeconds(90));

    public Task<VersionsResponse> Versions(string id) => Get<VersionsResponse>($"/api/items/{id}/versions");

    /// Searches the subtitle sites for the item.
    public async Task<OnlineSubtitleSearch> SearchOnlineSubtitles(string id) =>
        Json.Parse<OnlineSubtitleSearch>((await Send(HttpMethod.Get, $"/api/items/{id}/subtitles/online", timeout: TimeSpan.FromSeconds(40))).Body);

    /// Downloads a search result to the server; it comes back as a track of the session.
    public Task<SubtitleTrack> FetchOnlineSubtitle(string id, OnlineSubtitle sub, string? sessionId) =>
        Post<SubtitleTrack>($"/api/items/{id}/subtitles/online",
            new { provider = sub.Provider, id = sub.Id, language = sub.Language, name = sub.Name, sessionId }, TimeSpan.FromSeconds(60));

    /// Null, or why the server owner stopped this stream.
    public async Task<string?> ReportProgress(string id, double position, double duration, bool? paused = null)
    {
        try { await Send(HttpMethod.Post, $"/api/items/{id}/progress", body: new { position, duration, state = paused switch { true => "paused", false => "playing", null => null } }); }
        catch (WrenTVException e) when (e.Status == 410) { return e.Message; }
        catch { }
        return null;
    }

    public Task SetWatched(string id, bool watched) => Send(HttpMethod.Post, $"/api/items/{id}/watched", body: new { watched });

    public Task<List<Item>> Watchlist() => Get<List<Item>>("/api/watchlist");
    public Task SetWatchlist(string id, bool on) => Send(on ? HttpMethod.Put : HttpMethod.Delete, $"/api/items/{id}/watchlist");

    // Live TV
    public async Task<List<LiveChannel>> LiveChannels() => (await LiveLineup()).Channels;
    /// The channels with the viewer's favorites and recently watched.
    public Task<LiveChannelsResponse> LiveLineup() => Get<LiveChannelsResponse>("/api/livetv/channels");
    /// Every channel's programmes for a few hours, from a Unix time or now.
    public async Task<List<LiveGuideChannel>> LiveGuide(int hours, long? from = null)
    {
        var q = new List<(string, string)> { ("hours", hours.ToString()) };
        if (from is { } f) q.Add(("from", f.ToString()));
        return (await Get<LiveGuideResponse>("/api/livetv/guide", q)).Channels;
    }
    public Task<LiveSearchResult> LiveSearch(string q) => Get<LiveSearchResult>("/api/livetv/search", [("q", q)]);
    public Task SetLiveFavorite(string id, bool on) => Send(on ? HttpMethod.Put : HttpMethod.Delete, $"/api/livetv/favorites/{Uri.EscapeDataString(id)}");
    public Task<LivePlayResponse> PlayChannel(string id) => Post<LivePlayResponse>($"/api/livetv/channels/{id}/play", new { hls = false });
    /// The guide's muted picture: HLS for Windows' own player, not counted as watching.
    public Task<LivePlayResponse> PreviewChannel(string id) => Post<LivePlayResponse>($"/api/livetv/channels/{id}/play", new { hls = true, preview = true });

    public Uri? LiveLogoUrl(string? logo)
    {
        if (string.IsNullOrEmpty(logo)) return null;
        if (logo.StartsWith("http://") || logo.StartsWith("https://")) return Uri.TryCreate(logo, UriKind.Absolute, out var u) ? u : null;
        return Absolute(logo);
    }

    public async Task StopSession(string sid)
    {
        try { await Send(HttpMethod.Delete, $"/api/sessions/{sid}"); } catch { }
    }

    // Streaming
    public Task<DiscoverSearch> DiscoverSearch(string q) => Get<DiscoverSearch>("/api/discover/search", [("q", q)]);
    /// Adds a TMDB title to the server's streaming catalog. A slow TMDB answer
    /// can fail the first try, so it goes again once, except when the server said no.
    public async Task<Item> AddStreamTitle(int tmdbId, string mediaType)
    {
        try { return await Post<Item>("/api/streaming/titles", new { tmdbId, mediaType }); }
        catch (WrenTVException e) when (e.Status is < 400 or > 499)
        {
            return await Post<Item>("/api/streaming/titles", new { tmdbId, mediaType });
        }
    }

    /// Readies titles' detail pages on the server, for a card the viewer has settled on.
    public Task WarmTitles(IEnumerable<string> ids) =>
        Send(HttpMethod.Post, "/api/discover/warm", [("ids", string.Join(",", ids))]);

    // Layouts. Servers from before layouts answer 404.
    public Task<LayoutResponse> Layout(string scope, int offset = 0, int? limit = null)
    {
        var q = new List<(string, string)>();
        if (offset > 0) q.Add(("offset", offset.ToString()));
        if (limit != null) q.Add(("limit", limit.Value.ToString()));
        return Get<LayoutResponse>($"/api/layouts/{scope}", q);
    }
    public Task<HubResponse> Hub(string id) => Get<HubResponse>($"/api/hubs/{Uri.EscapeDataString(id)}");

    /// everyone edits the server's default (admins); otherwise the user's own layout.
    public Task<LayoutConfig> LayoutConfig(string scope, bool everyone) =>
        Get<LayoutConfig>($"/api/layouts/{scope}/config", [("target", everyone ? "default" : "me")]);

    public Task SetLayout(string scope, bool everyone, IEnumerable<LayoutEntry> entries) =>
        Send(HttpMethod.Put, $"/api/layouts/{scope}", [("target", everyone ? "default" : "me")],
            new { entries = entries.Select(e => new { type = e.Type, @ref = e.Ref, hidden = e.Hidden }).ToList() });

    public Task ResetLayout(string scope, bool everyone) =>
        Send(HttpMethod.Delete, $"/api/layouts/{scope}", [("target", everyone ? "default" : "me")]);

    // Admin
    public Task<List<ServerDownload>> ServerDownloads() => Get<List<ServerDownload>>("/api/admin/downloads");
    public Task CancelServerDownload(string id) => Send(HttpMethod.Delete, $"/api/admin/downloads/{id}");
    public Task RetryServerDownload(string id) => Send(HttpMethod.Post, $"/api/admin/downloads/{id}/retry");
    public Task<List<ServerUser>> Users() => Get<List<ServerUser>>("/api/users");
    public Task SetUserLimits(string id, UserLimitsPatch l) => Send(HttpMethod.Patch, $"/api/users/{id}", body: new { limits = l });
    public Task ScanLibrary(string id) => Send(HttpMethod.Post, $"/api/libraries/{id}/scan");

    /// Server-sent events: library changes and scans.
    public async IAsyncEnumerable<ServerEvent> Events([EnumeratorCancellation] CancellationToken ct)
    {
        using var req = new HttpRequestMessage(HttpMethod.Get, Url("/api/events"));
        if (Auth != null) req.Headers.Authorization = new AuthenticationHeaderValue("Bearer", await Auth.Token());
        using var resp = await Http.SendAsync(req, HttpCompletionOption.ResponseHeadersRead, ct);
        resp.EnsureSuccessStatusCode();
        using var reader = new StreamReader(await resp.Content.ReadAsStreamAsync(ct));
        while (!ct.IsCancellationRequested)
        {
            var line = await reader.ReadLineAsync(ct);
            if (line == null) yield break;
            if (!line.StartsWith("data:")) continue;
            ServerEvent? ev = null;
            try { ev = Json.Parse<ServerEvent>(line[5..].Trim()); } catch { }
            if (ev != null) yield return ev;
        }
    }
}
