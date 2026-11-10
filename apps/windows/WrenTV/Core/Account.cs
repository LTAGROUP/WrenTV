using System.Net.Http.Headers;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace WrenTV.Core;

/// A signed-in Wren TV account (Supabase Auth session).
public sealed class AccountSession
{
    public string AccessToken { get; set; } = "";
    public string RefreshToken { get; set; } = "";
    public double ExpiresAt { get; set; }
    public string Email { get; set; } = "";
}

/// A server this account owns or has been shared, as the account service lists it.
public sealed class AccountServer
{
    [JsonPropertyName("id")] public string Id { get; set; } = "";
    [JsonPropertyName("name")] public string Name { get; set; } = "";
    [JsonPropertyName("owner_id")] public string OwnerId { get; set; } = "";
    [JsonPropertyName("local_urls")] public List<string>? LocalUrls { get; set; }
    [JsonPropertyName("public_url")] public string? PublicUrl { get; set; }
    [JsonPropertyName("remote_url")] public string? RemoteUrl { get; set; }
    [JsonPropertyName("last_seen_at")] public DateTimeOffset? LastSeenAt { get; set; }

    /// Every address the server published, best first.
    public List<Uri> Candidates => AddressPicker.Candidates(null, LocalUrls ?? [], PublicUrl, RemoteUrl);
}

/// Talks to the Wren TV account service (Supabase Auth over REST).
public sealed class AccountClient(SupabaseInfo info)
{
    readonly Uri url = new(info.Url);
    readonly string key = info.PublishableKey;

    /// Where the session is saved; shared by every server on the same account service.
    public string StoreKey => "account:" + url.AbsoluteUri;

    static double Now => DateTimeOffset.UtcNow.ToUnixTimeMilliseconds() / 1000.0;

    async Task<string> Request(string path, object body, string? bearer = null)
    {
        using var req = new HttpRequestMessage(HttpMethod.Post, new Uri(url, path));
        req.Headers.Add("apikey", key);
        if (bearer != null) req.Headers.Authorization = new AuthenticationHeaderValue("Bearer", bearer);
        req.Content = new StringContent(JsonSerializer.Serialize(body), Encoding.UTF8, "application/json");
        using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(30));
        HttpResponseMessage resp;
        try { resp = await WrenTVApi.Http.SendAsync(req, cts.Token); }
        catch (Exception e) when (e is HttpRequestException or OperationCanceledException)
        { throw new WrenTVException("Couldn't reach the Wren TV account service."); }
        using (resp)
        {
            var text = await resp.Content.ReadAsStringAsync();
            var status = (int)resp.StatusCode;
            if (status >= 400)
            {
                string? msg = null;
                try
                {
                    var root = JsonDocument.Parse(text).RootElement;
                    foreach (var k in new[] { "msg", "error_description", "message", "error" })
                        if (root.TryGetProperty(k, out var v) && v.ValueKind == JsonValueKind.String) { msg = v.GetString(); break; }
                }
                catch { }
                msg ??= resp.ReasonPhrase ?? $"Error {status}";
                throw new WrenTVException(char.ToUpperInvariant(msg[0]) + msg[1..], status);
            }
            return text;
        }
    }

    static AccountSession? SessionFrom(string text, string fallbackEmail)
    {
        var root = JsonDocument.Parse(text).RootElement;
        string? Str(JsonElement e, string k) => e.TryGetProperty(k, out var v) && v.ValueKind == JsonValueKind.String ? v.GetString() : null;
        double? Num(string k) => root.TryGetProperty(k, out var v) && v.ValueKind == JsonValueKind.Number ? v.GetDouble() : null;
        var access = Str(root, "access_token");
        var refresh = Str(root, "refresh_token");
        if (access == null || refresh == null) return null;
        var email = root.TryGetProperty("user", out var u) && u.ValueKind == JsonValueKind.Object ? Str(u, "email") : null;
        return new AccountSession
        {
            AccessToken = access,
            RefreshToken = refresh,
            ExpiresAt = Num("expires_at") ?? Now + (Num("expires_in") ?? 3600),
            Email = email ?? fallbackEmail,
        };
    }

    public async Task<AccountSession> SignIn(string email, string password)
    {
        var text = await Request("/auth/v1/token?grant_type=password", new { email, password });
        var s = SessionFrom(text, email) ?? throw new WrenTVException("Sign-in failed");
        await AcceptInvites(s);
        return s;
    }

    /// Null when the account must confirm its email before signing in.
    public async Task<AccountSession?> SignUp(string email, string password, string name)
    {
        var text = await Request("/auth/v1/signup", new { email, password, data = new { display_name = name } });
        var s = SessionFrom(text, email);
        if (s != null) await AcceptInvites(s);
        return s;
    }

    public async Task<AccountSession> Refresh(AccountSession s)
    {
        var text = await Request("/auth/v1/token?grant_type=refresh_token", new { refresh_token = s.RefreshToken });
        return SessionFrom(text, s.Email) ?? throw new WrenTVException("Your sign-in expired", 401);
    }

    public async Task SignOut(AccountSession s)
    {
        try { await Request("/auth/v1/logout", new { }, s.AccessToken); } catch { }
    }

    /// A ticket that signs this account in to one media server. The server
    /// redeems it with the account service to learn who is signing in; it
    /// works once, for that server only, for two minutes. This is what a
    /// media server is given instead of the account's own token, which would
    /// work everywhere.
    public async Task<string> ServerTicket(string serverId, string accessToken)
    {
        var text = await Request("/rest/v1/rpc/issue_server_ticket", new { p_server_id = serverId }, accessToken);
        string? ticket = null;
        try { ticket = JsonSerializer.Deserialize<string>(text); } catch { }
        if (string.IsNullOrEmpty(ticket)) throw new WrenTVException("Couldn't sign in to the server");
        return ticket;
    }

    /// Turns pending share invites for this email into access.
    public async Task AcceptInvites(AccountSession s)
    {
        try { await Request("/rest/v1/rpc/accept_invites", new { }, s.AccessToken); } catch { }
    }

    async Task<string> Rest(string pathAndQuery, AccountSession s)
    {
        using var req = new HttpRequestMessage(HttpMethod.Get, new Uri(url, pathAndQuery));
        req.Headers.Add("apikey", key);
        req.Headers.Authorization = new AuthenticationHeaderValue("Bearer", s.AccessToken);
        using var resp = await WrenTVApi.Http.SendAsync(req);
        if (!resp.IsSuccessStatusCode) throw new WrenTVException("Couldn't load your servers", (int)resp.StatusCode);
        return await resp.Content.ReadAsStringAsync();
    }

    /// The servers this account owns or has been shared, most recently seen first.
    /// Signs in a TV showing an account code; with check, only looks it up.
    public async Task<string> LinkDevice(string code, bool check, string accessToken)
    {
        var text = await Request("/functions/v1/link-device", new { code, check }, accessToken);
        try { return JsonDocument.Parse(text).RootElement.GetProperty("deviceName").GetString() ?? ""; } catch { return ""; }
    }

    public async Task<List<AccountServer>> Servers(AccountSession s) =>
        JsonSerializer.Deserialize<List<AccountServer>>(await Rest(
            "/rest/v1/servers?select=id,name,owner_id,local_urls,public_url,remote_url,last_seen_at&order=last_seen_at.desc.nullslast", s)) ?? [];

    /// The addresses a server published in its heartbeat.
    public async Task<AccountServer?> Addresses(string serverId, AccountSession s)
    {
        try
        {
            var list = JsonSerializer.Deserialize<List<AccountServer>>(await Rest(
                $"/rest/v1/servers?id=eq.{Uri.EscapeDataString(serverId)}&select=id,name,owner_id,local_urls,public_url,remote_url", s));
            return list?.FirstOrDefault();
        }
        catch { return null; }
    }
}

/// Supplies the bearer token for media-server requests.
///
/// A server only ever gets a token it issued itself: a local sign-in, or the
/// token it gave in exchange for a server ticket. The Wren TV account's own
/// token works on the account service and on every server the account can
/// use, so it goes to the account service alone (AccountToken), never to a
/// media server.
public sealed class TokenSource
{
    /// The media server's own token.
    volatile string serverToken;
    readonly AccountClient? client;
    AccountSession? session;
    readonly SemaphoreSlim gate = new(1, 1);
    /// The last failed refresh. A rejected refresh token is never retried, and
    /// other failures wait a while, so a broken sign-in can't hammer the
    /// account service.
    (Exception Error, DateTime At, bool Final)? failure;

    public TokenSource(string localToken) => serverToken = localToken;

    /// serverToken is the server's own token for this account ("" until it's exchanged).
    public TokenSource(AccountClient client, AccountSession session, string serverToken)
    {
        this.client = client;
        this.session = session;
        this.serverToken = serverToken;
    }

    public AccountSession? AccountSession => session;
    public string? Email => session?.Email;

    static double Now => DateTimeOffset.UtcNow.ToUnixTimeMilliseconds() / 1000.0;

    /// The bearer for the media server.
    public Task<string> Token() => Task.FromResult(serverToken);

    public void SetServerToken(string token) => serverToken = token;

    /// The account's own token, refreshed when it is about to expire. Only
    /// for requests to the account service.
    public async Task<string> AccountToken()
    {
        if (client == null || session == null) throw new WrenTVException("Not signed in to a Wren TV account", 401);
        if (session.ExpiresAt - 60 > Now) return session.AccessToken;
        await gate.WaitAsync();
        try
        {
            if (session.ExpiresAt - 60 > Now) return session.AccessToken;
            if (failure is { } f)
            {
                if (f.Final) throw new WrenTVException("Your sign-in expired", 401);
                if (DateTime.UtcNow - f.At < TimeSpan.FromSeconds(30)) throw f.Error;
            }
            try
            {
                var fresh = await client.Refresh(session);
                session = fresh;
                failure = null;
                Store.SetSession(client.StoreKey, fresh);
                return fresh.AccessToken;
            }
            catch (Exception e)
            {
                var status = (e as WrenTVException)?.Status ?? 0;
                var final = status is 400 or 401 or 403;
                failure = (e, DateTime.UtcNow, final);
                if (final) throw new WrenTVException("Your sign-in expired", 401);
                throw;
            }
        }
        finally { gate.Release(); }
    }
}
