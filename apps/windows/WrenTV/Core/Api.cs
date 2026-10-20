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

    public Task<CollectionDetail> Collection(string id) => Get<CollectionDetail>($"/api/collections/{id}");

    public Task<UserPrefs> Prefs() => Get<UserPrefs>("/api/me/prefs");
    public async Task<UserPrefs> SetPrefs(UserPrefs p) => Json.Parse<UserPrefs>((await Send(HttpMethod.Patch, "/api/me/prefs", body: p)).Body);

    public Task<ItemDetail> Item(string id) => Get<ItemDetail>($"/api/items/{id}");
    public Task<Extras> Extras(string id) => Get<Extras>($"/api/items/{id}/extras");
    public Task<DiscoverHome> DiscoverHome() => Get<DiscoverHome>("/api/discover/home");
    public Task<TrendingResponse> Trending() => Get<TrendingResponse>("/api/discover/trending");

    /// TMDB's whole catalog of movies or shows ("movie" | "show"), a page at a time.
    public Task<CatalogResult> Catalog(string type, string sort, int? genre, int page) =>
        Get<CatalogResult>("/api/discover/browse", [("type", type), ("sort", sort), ("genre", genre?.ToString() ?? ""), ("page", page.ToString())]);
    public Task<List<Item>> Search(string q) => Get<List<Item>>("/api/search", [("q", q)]);

    public Task<PlayResponse> Play(string id, PlayRequest req) =>
        Post<PlayResponse>($"/api/items/{id}/play", req, TimeSpan.FromSeconds(90));

    public Task<VersionsResponse> Versions(string id) => Get<VersionsResponse>($"/api/items/{id}/versions");

    public async Task ReportProgress(string id, double position, double duration)
    {
        try { await Send(HttpMethod.Post, $"/api/items/{id}/progress", body: new { position, duration }); } catch { }
    }

    public Task SetWatched(string id, bool watched) => Send(HttpMethod.Post, $"/api/items/{id}/watched", body: new { watched });

    public Task<List<Item>> Watchlist() => Get<List<Item>>("/api/watchlist");
    public Task SetWatchlist(string id, bool on) => Send(on ? HttpMethod.Put : HttpMethod.Delete, $"/api/items/{id}/watchlist");

    // Live TV
    public async Task<List<LiveChannel>> LiveChannels() => (await Get<LiveChannelsResponse>("/api/livetv/channels")).Channels;
    public Task<LivePlayResponse> PlayChannel(string id) => Post<LivePlayResponse>($"/api/livetv/channels/{id}/play", new { hls = false });

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
    public Task<Item> AddStreamTitle(int tmdbId, string mediaType) => Post<Item>("/api/streaming/titles", new { tmdbId, mediaType });

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
