using System.Net.NetworkInformation;

namespace WrenTV.Core;

/// Who's signed in, to which server, and what they may do. Lives on the UI
/// thread; Changed fires whenever the screen should be rebuilt.
public sealed class AppState
{
    public Uri? ServerUrl { get; private set; }
    public WrenTVApi? Api { get; private set; }
    public User? User { get; private set; }
    public ServerInfo? Server { get; private set; }
    public List<Library> Libraries { get; private set; } = [];
    public UserLimitsInfo? Limits { get; private set; }
    public bool Restoring { get; private set; } = true;

    /// Signed in to an account that can't use this server yet. Shown on the sign-in screen.
    public string? AccessIssue { get; private set; }
    public string? AccountEmail { get; private set; }

    /// Signed in to a Wren TV account but no server picked yet: the app lists the account's servers.
    public AccountSession? Account { get; private set; }
    public List<AccountServer>? AccountServers { get; private set; }
    public string? AccountServersError { get; private set; }
    readonly AccountClient accountClient = new(SupabaseInfo.WrenTV);

    /// Sign-in state changed: rebuild the window.
    public event Action? Changed;
    /// Library contents changed on the server: pages reload.
    public event Action? Refreshed;

    public bool CanStream => (Server?.Streaming ?? false) && (Limits?.CanStream ?? true);
    public bool HasLiveTv => Server?.LiveTv ?? false;
    public bool IsAdmin => User is { } u && (u.IsAdmin || u.Role is "owner" or "admin");

    CancellationTokenSource? events;
    CancellationTokenSource? picking;

    public AppState()
    {
        NetworkChange.NetworkAddressChanged += (_, _) => App.OnUi(async () =>
        {
            await Task.Delay(2000); // let the new network settle
            PickAddress();
        });
    }

    void Notify() => Changed?.Invoke();

    public async Task Restore()
    {
        try
        {
            var s = Store.GetString("serverURL");
            if (s == null || !Uri.TryCreate(s, UriKind.Absolute, out var url))
            {
                await RestoreAccount();
                return;
            }
            ServerUrl = url;
            // The saved address may only work at home: also try every address the server published.
            var baseUrl = url;
            ServerInfo? info = null;
            try { info = await new WrenTVApi(url).ServerInfo(); } catch { }
            if (info == null && Store.GetString("serverId") is { } id && Store.Get<List<string>>("serverAddresses") is { } known)
            {
                var found = await AddressPicker.Best(known.Select(k => new Uri(k)).ToList(), id);
                if (found != null)
                {
                    baseUrl = found;
                    try { info = await new WrenTVApi(found).ServerInfo(); } catch { }
                }
            }
            if (info == null)
            {
                // Server offline: list the account's servers instead.
                await RestoreAccount();
                return;
            }
            Server = info;
            // Account servers can also have local accounts, so a saved local token comes first.
            if (Store.GetSecret(url.AbsoluteUri) is { } token)
            {
                await FinishSignIn(url, new TokenSource(token), null);
            }
            else if (info.UsesAccounts)
            {
                if (Store.Session(accountClient.StoreKey) is { } session)
                    await ServerSignIn(baseUrl, Store.GetString("serverId") ?? info.ServerId, session);
                else
                    await RestoreAccount();
            }
        }
        finally
        {
            Restoring = false;
            Notify();
        }
    }

    // Wren TV account

    async Task RestoreAccount()
    {
        if (Store.Session(accountClient.StoreKey) is not { } session) return;
        Account = session;
        AccountEmail = session.Email;
        await LoadAccountServers();
    }

    /// Signs in to a Wren TV account. Returns false when a new account must confirm its email first.
    public async Task<bool> SignInToAccount(string email, string password, string? name)
    {
        AccountSession session;
        if (name != null)
        {
            var s = await accountClient.SignUp(email, password, name);
            if (s == null) return false;
            session = s;
        }
        else session = await accountClient.SignIn(email, password);
        Store.SetSession(accountClient.StoreKey, session);
        Account = session;
        AccountEmail = session.Email;
        await LoadAccountServers();
        // One server: go straight to it.
        if (AccountServers is [var only])
        {
            try { await Open(only); } catch { }
        }
        Notify();
        return true;
    }

    /// Lists the servers the account owns or has been shared, accepting any waiting invites first.
    public async Task LoadAccountServers()
    {
        if (Account is not { } session) return;
        if (session.ExpiresAt - 60 < DateTimeOffset.UtcNow.ToUnixTimeSeconds())
        {
            try
            {
                session = await accountClient.Refresh(session);
                Account = session;
                Store.SetSession(accountClient.StoreKey, session);
            }
            catch
            {
                // The sign-in expired for good: sign in again.
                Store.SetSession(accountClient.StoreKey, null);
                Account = null;
                Notify();
                return;
            }
        }
        await accountClient.AcceptInvites(session);
        try
        {
            AccountServers = await accountClient.Servers(session);
            AccountServersError = null;
        }
        catch (Exception e) { AccountServersError = e.Message; }
        Notify();
    }

    /// Connects to one of the account's servers at whichever published address answers.
    public async Task Open(AccountServer server)
    {
        if (Account is not { } session) return;
        var url = await AddressPicker.Best(server.Candidates, server.Id)
            ?? throw new WrenTVException($"Couldn't reach {server.Name}. Make sure it's running, and connected to the internet if you're away from home.");
        var info = await new WrenTVApi(url).ServerInfo();
        ServerUrl = url;
        Server = info;
        AccessIssue = null;
        Store.Set("serverURL", url.AbsoluteUri);
        Store.Set("serverId", server.Id);
        Store.Set("serverAddresses", server.Candidates.Select(c => c.AbsoluteUri).ToList());
        await ServerSignIn(url, server.Id, session);
        Notify();
    }

    /// Leaves this server but stays signed in to the account.
    public void SwitchServer()
    {
        events?.Cancel();
        Api = null;
        User = null;
        Limits = null;
        Libraries = [];
        AccessIssue = null;
        ForgetServer();
        if (Account == null && Store.Session(accountClient.StoreKey) is { } s)
        {
            Account = s;
            AccountEmail = s.Email;
        }
        Notify();
        _ = LoadAccountServers();
    }

    void ForgetServer()
    {
        ServerUrl = null;
        Server = null;
        Store.Set<string>("serverURL", null);
    }

    // Servers with their own accounts (by address)

    public async Task<ServerInfo> Connect(string raw)
    {
        var s = raw.Trim();
        if (!s.Contains("://")) s = "http://" + s;
        if (!Uri.TryCreate(s, UriKind.Absolute, out var url)) throw new WrenTVException("Invalid server address");
        if (url.IsDefaultPort && url.Scheme == "http" && !raw.Contains(":80")) url = new UriBuilder(url) { Port = 32500 }.Uri;
        var info = await new WrenTVApi(url).ServerInfo();
        ServerUrl = url;
        Server = info;
        AccessIssue = null;
        Store.Set("serverURL", url.AbsoluteUri);
        // Already signed in to this server's account service?
        if (info.UsesAccounts)
        {
            if (Store.Session(accountClient.StoreKey) is { } session)
            {
                await ServerSignIn(url, info.ServerId, session);
                Notify();
            }
        }
        return info;
    }

    /// Local-account servers: username and password stored on the server.
    public async Task Login(string username, string password, bool createOwner)
    {
        if (ServerUrl is not { } url) return;
        var anon = new WrenTVApi(url);
        var resp = createOwner ? await anon.SetupOwner(username, password) : await anon.Login(username, password);
        Store.SetSecret(url.AbsoluteUri, resp.Token);
        var api = new WrenTVApi(url, new TokenSource(resp.Token));
        try { Limits = (await api.Me()).Limits; } catch { }
        SignedIn(api, resp.User);
        Notify();
    }

    /// Account servers reached by address. False when the new account must confirm its email.
    public async Task<bool> AccountSignIn(string email, string password, string? name)
    {
        // The account service's address is the app's own, never one a server names.
        if (ServerUrl is not { } url || Server?.UsesAccounts != true) return false;
        var client = accountClient;
        AccountSession session;
        if (name != null)
        {
            var s = await client.SignUp(email, password, name);
            if (s == null) return false;
            session = s;
        }
        else session = await client.SignIn(email, password);
        Store.SetSession(client.StoreKey, session);
        await ServerSignIn(url, Server?.ServerId, session);
        Notify();
        return true;
    }

    /// Re-checks access after the owner shares the server.
    public async Task RetryAccess()
    {
        if (ServerUrl is not { } url || Server?.UsesAccounts != true) return;
        var client = accountClient;
        if (Store.Session(client.StoreKey) is not { } session) return;
        await client.AcceptInvites(session);
        try { Server = await new WrenTVApi(url).ServerInfo(); } catch { }
        await ServerSignIn(url, Server?.ServerId, session);
        Notify();
    }

    static string ServerTokenKey(string serverId) => "server-token:" + serverId;

    /// Signs in to a media server with the Wren TV account, without showing
    /// the server the account's token (which would work on the account
    /// service and on every other server too). The app asks the account
    /// service for a ticket for this one server and hands that over; the
    /// server answers with a token of its own, kept for next time.
    async Task ServerSignIn(Uri url, string? serverId, AccountSession session, bool retried = false)
    {
        AccountEmail = session.Email;
        Account = session;
        var name = Server?.Name ?? "This server";
        if (string.IsNullOrEmpty(serverId))
        {
            AccessIssue = $"{name} hasn't been set up yet. Open it in a web browser to claim it with your account.";
            return;
        }
        var key = ServerTokenKey(serverId);
        var saved = Store.GetSecret(key);
        var auth = new TokenSource(accountClient, session, saved ?? "");
        if (saved == null)
        {
            try
            {
                var ticket = await accountClient.ServerTicket(serverId, await auth.AccountToken());
                var r = await new WrenTVApi(url).Exchange(ticket);
                Store.SetSecret(key, r.Token);
                auth.SetServerToken(r.Token);
            }
            catch (WrenTVException e) when (e.Status is 404 or 405)
            {
                AccessIssue = $"{name} needs to be updated before this version of the app can sign in to it.";
                return;
            }
            catch (WrenTVException e) when (e.Status == 401 && e.Message == "Your sign-in expired")
            {
                ClearCredentials();
                return;
            }
            catch (WrenTVException e) when (e.Status == 403 || e.Message.Contains("access", StringComparison.OrdinalIgnoreCase))
            {
                AccessIssue = $"{session.Email} doesn't have access to {name}. Ask the owner to share it with this email address.";
                return;
            }
            catch (WrenTVException e) when (e.Status == 409)
            {
                AccessIssue = $"{name} hasn't been set up yet. Open it in a web browser to claim it with your account.";
                return;
            }
            catch (Exception e)
            {
                AccessIssue = e.Message;
                return;
            }
        }
        var rejected = await FinishSignIn(url, auth, session.Email, keepAccountOn401: !retried);
        if (rejected && !retried)
        {
            // The server no longer takes its old token (signed out there, or
            // it was reset): ask for a new one, once.
            Store.SetSecret(key, null);
            await ServerSignIn(url, serverId, session, retried: true);
        }
    }

    /// Returns true when the server turned the token down and
    /// keepAccountOn401 left the sign-in alone for the caller to retry.
    async Task<bool> FinishSignIn(Uri url, TokenSource auth, string? email, bool keepAccountOn401 = false)
    {
        var api = new WrenTVApi(url, auth);
        AccountEmail = email;
        try
        {
            var me = await api.Me();
            AccessIssue = null;
            Limits = me.Limits;
            SignedIn(api, me.User);
            PickAddress();
        }
        catch (WrenTVException e) when (e.Status == 401)
        {
            if (keepAccountOn401) return true;
            ClearCredentials();
        }
        catch (WrenTVException e) when (e.Status == 403)
        {
            AccessIssue = $"{email ?? "This account"} doesn't have access to {Server?.Name ?? "this server"}. Ask the owner to share it with this email address.";
        }
        catch (WrenTVException e) when (e.Status == 409)
        {
            AccessIssue = $"{Server?.Name ?? "This server"} hasn't been set up yet. Open it in a web browser to claim it with your account.";
        }
        catch (Exception e) { AccessIssue = e.Message; }
        return false;
    }

    void SignedIn(WrenTVApi api, User user)
    {
        Api = api;
        User = user;
        _ = ReloadLibraries();
        _ = LoadPosterSize(api);
        Listen();
    }

    /// Poster size is saved to the account; the copy here is so the first page opens at the right size.
    async Task LoadPosterSize(WrenTVApi api)
    {
        try
        {
            var size = await api.PosterSize();
            if (size is "small" or "medium" or "large") Store.Set("posterSize", size == "medium" ? null : size);
        }
        catch { }
    }

    void ClearCredentials()
    {
        if (ServerUrl is { } url) Store.SetSecret(url.AbsoluteUri, null);
        foreach (var id in new[] { Server?.ServerId, Store.GetString("serverId") })
            if (!string.IsNullOrEmpty(id)) Store.SetSecret(ServerTokenKey(id), null);
        if (Store.Session(accountClient.StoreKey) is { } session) _ = accountClient.SignOut(session);
        Store.SetSession(accountClient.StoreKey, null);
        Account = null;
        AccountServers = null;
        AccountEmail = null;
        AccessIssue = null;
    }

    public void SignOut()
    {
        ClearCredentials();
        events?.Cancel();
        Api = null;
        User = null;
        Limits = null;
        Libraries = [];
        ForgetServer();
        Notify();
    }

    /// Leaves an address that stopped answering for the account's server list.
    public void ForgetUnreachable()
    {
        AccessIssue = null;
        ForgetServer();
        Notify();
    }

    // Addresses

    /// Switches to the best address the server published (secure LAN at home,
    /// the public or remote address elsewhere). Runs after sign-in and on network changes.
    public void PickAddress()
    {
        picking?.Cancel();
        var cts = picking = new CancellationTokenSource();
        App.OnUi(async () =>
        {
            if (Api is not { } api || ServerUrl is not { } entered || Server is not { } info) return;
            var id = info.Identity;
            var local = info.LocalUrls ?? [];
            string? publicUrl = null, remoteUrl = null;
            if (api.Auth?.AccountSession is { } session &&
                await accountClient.Addresses(id, session) is { } row)
            {
                local = row.LocalUrls ?? local;
                publicUrl = row.PublicUrl;
                remoteUrl = row.RemoteUrl;
            }
            var candidates = AddressPicker.Candidates(entered, local, publicUrl, remoteUrl);
            Store.Set("serverId", id);
            Store.Set("serverAddresses", candidates.Select(c => c.AbsoluteUri).ToList());
            var best = await AddressPicker.Best(candidates, id);
            if (best == null || cts.IsCancellationRequested || Api is not { } current) return;
            if (AddressPicker.Key(best) == AddressPicker.Key(current.BaseUrl)) return;
            Api = new WrenTVApi(best, current.Auth);
            Listen();
        });
    }

    public async Task ReloadLibraries()
    {
        if (Api is not { } api) return;
        try { Libraries = await api.Libraries(); } catch { }
        await RefreshStatus();
        Notify();
    }

    /// Streaming and live TV can be turned on or off at any time, and the
    /// user's limits can change, so this runs on every reconnect.
    public async Task RefreshStatus()
    {
        if (Api is not { } api) return;
        try
        {
            var info = await api.ServerInfo();
            if (Server != null)
            {
                Server.Streaming = info.Streaming;
                Server.LiveTv = info.LiveTv;
            }
        }
        catch { }
        try { Limits = (await api.Me()).Limits; } catch { }
    }

    void Listen()
    {
        events?.Cancel();
        var cts = events = new CancellationTokenSource();
        App.OnUi(async () =>
        {
            var first = true;
            while (!cts.IsCancellationRequested)
            {
                if (Api is not { } api) return;
                if (!first) await RefreshStatus();
                first = false;
                try
                {
                    await foreach (var ev in api.Events(cts.Token))
                        if (ev.Type is "library.updated" or "item.added" or "item.updated" or "scan.finished") ScheduleRefresh();
                }
                catch { }
                try { await Task.Delay(3000, cts.Token); } catch { return; }
            }
        });
    }

    /// Something was watched or changed here (e.g. the player closed): pages reload.
    public void Bump() => Refreshed?.Invoke();

    CancellationTokenSource? pendingRefresh;

    /// Coalesce bursts of events (a scan emits many) into one refresh.
    void ScheduleRefresh()
    {
        pendingRefresh?.Cancel();
        var cts = pendingRefresh = new CancellationTokenSource();
        App.OnUi(async () =>
        {
            try { await Task.Delay(800, cts.Token); } catch { return; }
            Refreshed?.Invoke();
            if (Api is { } api)
                try { Libraries = await api.Libraries(); } catch { }
        });
    }
}
