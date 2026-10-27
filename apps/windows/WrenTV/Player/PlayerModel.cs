using WrenTV.Core;

namespace WrenTV.Player;

/// Plays one item (then the next episode) through mpv: asks the server for a
/// stream, reports progress, and offers skip intro / credits and up next.
public sealed class PlayerModel
{
    public enum PhaseKind { Loading, Pending, Error, Playing, Ended }
    public enum SkipKind { Intro, Credits }

    readonly WrenTVApi api;
    readonly Mpv mpv;
    public string ItemId { get; private set; }
    public string Title { get; private set; }
    public string? Subtitle { get; private set; }
    bool fromStart;

    public PhaseKind Phase { get; private set; } = PhaseKind.Loading;
    public string? Message { get; private set; }
    public string MethodLabel { get; private set; } = "";

    public double Position { get; private set; }
    public double Duration { get; private set; }
    public bool Paused { get; private set; }
    public bool Buffering { get; private set; }
    public int Volume { get; private set; } = 100;
    public bool Muted { get; private set; }

    public List<MediaStream> AudioStreams { get; private set; } = [];
    public int? AudioIndex { get; private set; }
    public List<MpvTrack> Tracks { get; private set; } = [];
    public List<MediaVersion> Versions { get; private set; } = [];
    public string? FileId { get; private set; }
    public List<Chapter> Chapters { get; private set; } = [];
    Markers markers = new();
    /// What the skip button offers right now.
    public SkipKind? Skip { get; private set; }
    /// Seconds left before the next episode starts, while the credits roll.
    public int? Countdown { get; private set; }
    public Item? UpNext { get; private set; }
    Item? nextEpisode;
    /// This episode, for the episode list; null for a movie.
    public Item? Episode { get; private set; }
    /// The server can search the subtitle sites for this title.
    public bool OnlineSubtitles { get; private set; }
    /// Subtitles downloaded from the sites for this session, not yet loaded into mpv.
    public List<SubtitleTrack> SideTracks { get; private set; } = [];
    PlayResponse? response;
    /// The next play request asks for a new link: the last one failed.
    bool freshLink;
    /// The version that failed, so the server tries another first.
    string? failedFile;
    /// Direct play: switching audio is mpv's job. Otherwise the server's stream has one audio track.
    bool direct;

    /// Anything shown changed.
    public event Action? Changed;
    /// Only the position moved.
    public event Action? Ticked;
    /// No credits marker: the countdown starts this long before the end of an
    /// episode that has a next one.
    const double UpNextLead = 30;

    /// Playback is done and the window should close.
    public event Action? CloseRequested;

    string? sessionId;
    DateTime? lastAutoRetry;
    CancellationTokenSource? load, progress, countdown, versionsWatch;
    readonly HashSet<SkipKind> dismissed = [];
    bool stopped;

    public PlayerModel(PlayTarget target, WrenTVApi api, Mpv mpv)
    {
        this.api = api;
        this.mpv = mpv;
        ItemId = target.ItemId;
        Title = target.Title;
        Subtitle = target.Subtitle;
        fromStart = target.FromStart;
        FileId = target.FileId;
        mpv.OnTime = (t, d) =>
        {
            Position = t;
            if (d > 0) Duration = d;
            UpdateSkip();
            Ticked?.Invoke();
        };
        mpv.OnPause = p => { Paused = p; Changed?.Invoke(); };
        mpv.OnBuffering = b => { Buffering = b; Changed?.Invoke(); };
        mpv.OnTracks = t => { Tracks = t; Changed?.Invoke(); };
        mpv.OnEnd = () =>
        {
            // A stream that breaks partway ends early instead of failing.
            if (Duration > 0 && Position < Duration - 120) Failed("The stream ended early.");
            else Finished();
        };
        mpv.OnError = Failed;
    }

    void Set(PhaseKind p, string? message = null)
    {
        Phase = p;
        Message = message;
        Changed?.Invoke();
    }

    public void Start(double? at = null)
    {
        load?.Cancel();
        var cts = load = new CancellationTokenSource();
        _ = Run(at, cts.Token);
    }

    async Task Run(double? startAt, CancellationToken ct)
    {
        Set(PhaseKind.Loading);
        PlayResponse resp;
        while (true)
        {
            if (ct.IsCancellationRequested || stopped) return;
            try
            {
                var req = new PlayRequest { FileId = FileId, AudioIndex = AudioIndex, FailedFileId = failedFile };
                if (freshLink) req.Retry = true;
                freshLink = false;
                failedFile = null;
                resp = await api.Play(ItemId, req);
            }
            catch (Exception e)
            {
                if (!ct.IsCancellationRequested) Set(PhaseKind.Error, e.Message);
                return;
            }
            // A streamed title is still being found: keep the viewer here and retry.
            if (resp.Status == "pending")
            {
                Set(PhaseKind.Pending, resp.Message ?? "Finding a stream…");
                try { await Task.Delay(3000, ct); } catch { return; }
                continue;
            }
            break;
        }
        if (resp.Url is not { } path)
        {
            Set(PhaseKind.Error, "The server didn't return a stream.");
            return;
        }
        if (stopped || ct.IsCancellationRequested)
        {
            if (resp.SessionId is { } sid) await api.StopSession(sid);
            return;
        }
        sessionId = resp.SessionId;
        response = resp;
        OnlineSubtitles = resp.OnlineSubtitles ?? false;
        SideTracks = (resp.Subtitles ?? []).Where(t => t.Index >= 10000 && t.Url != null).ToList();
        FileId = resp.FileId ?? FileId;
        Versions = resp.Versions ?? [];
        WatchVersions();
        AudioStreams = resp.Info?.Audio ?? [];
        AudioIndex ??= resp.Decision?.AudioIndex;
        Duration = resp.Duration ?? 0;
        Chapters = resp.Chapters ?? resp.Info?.Chapters ?? [];
        markers = resp.Markers ?? new Markers();
        MethodLabel = Describe(resp);
        direct = resp.Method == "direct";
        var start = startAt ?? (fromStart ? 0 : resp.ResumePosition ?? 0);

        // mpv picks its own tracks from the file; use the ones the server chose
        // from the viewer's language settings (mpv counts from 1).
        if (direct && (AudioIndex ?? resp.Decision?.AudioIndex) is { } a) mpv.Set("aid", (a + 1).ToString());
        else mpv.Set("aid", "auto");
        mpv.Set("sid", direct && resp.SubtitleIndex is >= 0 and var s ? (s + 1).ToString() : "no");
        mpv.Set("start", "none");
        mpv.Set("pause", "no");
        mpv.Load(api.Absolute(path), start);
        Position = start;
        Set(PhaseKind.Playing);
        StartProgressLoop();
        if (nextEpisode == null) nextEpisode = await FindNextEpisode();
    }

    /// A streamed title's lookup goes on after play starts: pick up the versions it finds.
    void WatchVersions()
    {
        versionsWatch?.Cancel();
        var cts = versionsWatch = new CancellationTokenSource();
        var id = ItemId;
        _ = Task.Run(async () =>
        {
            try
            {
                await Task.Delay(5000, cts.Token);
                for (var i = 0; i < 15 && !cts.IsCancellationRequested; i++)
                {
                    var r = await api.Versions(id);
                    if (r.Versions is { Count: > 0 } v)
                        App.OnUi(() => { if (id == ItemId) { Versions = v; Changed?.Invoke(); } });
                    if (r.Looking != true) return;
                    await Task.Delay(20000, cts.Token);
                }
            }
            catch { }
        });
    }

    public void Stop()
    {
        if (stopped) return;
        stopped = true;
        load?.Cancel();
        versionsWatch?.Cancel();
        progress?.Cancel();
        CancelCountdown();
        var (pos, dur, sid, item) = (Position, Duration, sessionId, ItemId);
        _ = Task.Run(async () =>
        {
            if (pos > 5) await api.ReportProgress(item, pos, dur);
            if (sid != null) await api.StopSession(sid);
        });
        mpv.Command("stop");
    }

    // Controls

    public void TogglePause() => mpv.TogglePause();
    public void SeekBy(double d) => SeekTo(Math.Clamp(Position + d, 0, Math.Max(Duration, 0)));

    public void SeekTo(double t)
    {
        Position = t;
        mpv.Seek(t);
        UpdateSkip();
        Ticked?.Invoke();
    }

    public void SetVolume(int v)
    {
        Volume = Math.Clamp(v, 0, 100);
        Muted = false;
        mpv.Set("volume", Volume.ToString());
        mpv.Set("mute", "no");
        Changed?.Invoke();
    }

    public void ToggleMute()
    {
        Muted = !Muted;
        mpv.Set("mute", Muted ? "yes" : "no");
        Changed?.Invoke();
    }

    /// The audio tracks to offer: mpv's when playing the file itself, the
    /// server's otherwise (switching those needs a new stream).
    public bool SwitchesAudioInPlayer => direct;

    public void SelectAudio(int typeIndex)
    {
        if (typeIndex == AudioIndex) return;
        AudioIndex = typeIndex;
        RestartStream(Position);
    }

    public void SelectTrack(MpvTrack? t)
    {
        if (t != null) mpv.SelectTrack(t); else mpv.DisableSubtitles();
        var type = t?.Type ?? "sub";
        Tracks = Tracks.Select(x => x.Type == type ? x with { Selected = x.Id == t?.Id } : x).ToList();
        Changed?.Invoke();
    }

    /// Plays another version of the item from the same spot.
    public void SelectVersion(string id)
    {
        if (id == FileId) return;
        FileId = id;
        AudioIndex = null; // track numbers differ between files
        RestartStream(Position);
    }

    /// Starts over after a failure. The failed stream is closed on the server first.
    public void Retry()
    {
        freshLink = true;
        var old = sessionId;
        sessionId = null;
        if (old != null) _ = api.StopSession(old);
        Start(Position > 0 ? Position : null);
    }

    /// A stream that fails partway is tried again from the same spot once.
    void Failed(string message)
    {
        if (Phase != PhaseKind.Playing) return;
        // Reported with the next play, whether it's the automatic retry or Try Again.
        failedFile = FileId;
        if (lastAutoRetry is not { } at || DateTime.UtcNow - at > TimeSpan.FromMinutes(2))
        {
            lastAutoRetry = DateTime.UtcNow;
            Retry();
            return;
        }
        progress?.Cancel();
        Set(PhaseKind.Error, message);
    }

    void RestartStream(double pos)
    {
        var old = sessionId;
        sessionId = null;
        if (old != null) _ = api.StopSession(old);
        Start(pos);
    }

    // Chapters & skipping

    public Chapter? CurrentChapter => Chapters.LastOrDefault(c => c.Start <= Position + 0.01);

    void UpdateSkip()
    {
        var t = Position;
        if (markers.Intro is { } i && t < i.Start) dismissed.Remove(SkipKind.Intro);
        // No credits marker: count down over the last seconds of an episode
        // that has a next one, so it still moves on before the end.
        var credits = markers.Credits ?? (nextEpisode != null && Duration >= 300
            ? new TimeRange { Start = Duration - UpNextLead, End = Duration + 1 } : null);
        if (credits is { } c0 && t < c0.Start - 1) { dismissed.Remove(SkipKind.Credits); CancelCountdown(); }

        SkipKind? next = null;
        if (markers.Intro is { } intro && t >= intro.Start && t < intro.End - 1 && !dismissed.Contains(SkipKind.Intro)) next = SkipKind.Intro;
        else if (credits is { } c && t >= c.Start && t < c.End - 1 && !dismissed.Contains(SkipKind.Credits)) next = SkipKind.Credits;
        if (next == SkipKind.Credits && nextEpisode != null && Countdown == null && countdown == null && !Paused) StartCountdown();
        if (Skip != next)
        {
            Skip = next;
            Changed?.Invoke();
        }
    }

    public void SkipIntro()
    {
        if (markers.Intro is not { } i) return;
        dismissed.Add(SkipKind.Intro);
        Skip = null;
        SeekTo(i.End);
        Changed?.Invoke();
    }

    /// Skips the credits: the next episode when there is one, else the end.
    public void SkipCredits()
    {
        dismissed.Add(SkipKind.Credits);
        Skip = null;
        CancelCountdown();
        if (nextEpisode != null)
        {
            UpNext = nextEpisode;
            PlayNext();
        }
        else
        {
            Finished();
            CloseRequested?.Invoke();
        }
    }

    /// Keeps watching the credits.
    public void DismissCredits()
    {
        dismissed.Add(SkipKind.Credits);
        Skip = null;
        CancelCountdown();
        Changed?.Invoke();
    }

    void StartCountdown()
    {
        Countdown = 10;
        var cts = countdown = new CancellationTokenSource();
        App.OnUi(async () =>
        {
            while (!cts.IsCancellationRequested)
            {
                try { await Task.Delay(1000, cts.Token); } catch { return; }
                if (Countdown is not { } n) return;
                if (Paused) continue;
                if (n <= 1)
                {
                    countdown = null;
                    SkipCredits();
                    return;
                }
                Countdown = n - 1;
                Changed?.Invoke();
            }
        });
    }

    void CancelCountdown()
    {
        countdown?.Cancel();
        countdown = null;
        Countdown = null;
    }

    /// The episode after this one: the next in its season, else the first of the next season.
    async Task<Item?> FindNextEpisode()
    {
        try
        {
            var d = await api.Item(ItemId);
            if (d.Item.Kind != "episode") return null;
            var ep = d.Item;
            Episode = ep;
            Changed?.Invoke();
            if (ep.ParentId is { } pid)
            {
                var season = await api.Item(pid);
                var n = (season.Children ?? []).Where(x => x.EpisodeNumber > ep.EpisodeNumber).MinBy(x => x.EpisodeNumber);
                if (n != null) return n;
            }
            if (ep.ShowId is not { } showId) return null;
            var show = await api.Item(showId);
            var s = (show.Children ?? []).Where(x => x.SeasonNumber > ep.SeasonNumber).MinBy(x => x.SeasonNumber);
            if (s == null) return null;
            var sd = await api.Item(s.Id);
            return (sd.Children ?? []).MinBy(x => x.EpisodeNumber);
        }
        catch { return null; }
    }

    // Progress & up next

    void StartProgressLoop()
    {
        progress?.Cancel();
        var cts = progress = new CancellationTokenSource();
        App.OnUi(async () =>
        {
            while (!cts.IsCancellationRequested)
            {
                try { await Task.Delay(10000, cts.Token); } catch { return; }
                if (Position <= 5 || await api.ReportProgress(ItemId, Position, Duration, Paused) is not { } reason) continue;
                StoppedByServer(reason);
                return;
            }
        });
    }

    /// The server owner ended this stream from the dashboard: stop and say why.
    void StoppedByServer(string message)
    {
        if (Phase != PhaseKind.Playing) return;
        progress?.Cancel();
        sessionId = null;
        // Stopped on purpose: the file didn't fail.
        failedFile = null;
        mpv.Command("stop");
        Set(PhaseKind.Error, message);
    }

    void Finished()
    {
        if (Phase != PhaseKind.Playing) return;
        progress?.Cancel();
        var (id, dur) = (ItemId, Duration);
        if (nextEpisode != null) UpNext = nextEpisode;
        Set(PhaseKind.Ended);
        App.OnUi(async () =>
        {
            await api.ReportProgress(id, dur, dur);
            if (UpNext != null) return;
            try
            {
                var d = await api.Item(id);
                if (d.Item.Kind == "episode" && d.Item.ShowId is { } showId &&
                    (await api.Item(showId)).NextEpisode is { } next && next.Id != id)
                {
                    UpNext = next;
                    Changed?.Invoke();
                }
            }
            catch { }
        });
    }

    public void PlayNext()
    {
        if (UpNext is not { } next) return;
        // Moving on counts as finishing this one.
        var (id, dur) = (ItemId, Duration);
        if (Phase == PhaseKind.Playing) _ = api.ReportProgress(id, dur, dur);
        SwitchTo(next);
    }

    /// Plays another episode picked from the episode list.
    public void PlayEpisode(Item ep)
    {
        if (ep.Id == ItemId) return;
        var (id, pos, dur) = (ItemId, Position, Duration);
        if (Phase == PhaseKind.Playing && pos > 5) _ = api.ReportProgress(id, pos, dur);
        SwitchTo(ep);
    }

    void SwitchTo(Item next)
    {
        freshLink = false;
        failedFile = null;
        Episode = null;
        SideTracks = [];
        CancelCountdown();
        var old = sessionId;
        sessionId = null;
        if (old != null) _ = api.StopSession(old);
        ItemId = next.Id;
        Subtitle = $"{next.EpisodeLabel} — {next.Title}";
        UpNext = null;
        nextEpisode = null;
        dismissed.Clear();
        Skip = null;
        Chapters = [];
        markers = new Markers();
        fromStart = false;
        FileId = null;
        Versions = [];
        AudioIndex = null;
        Position = 0;
        Start();
    }

    // Episodes

    /// A season's episodes, or a show's seasons.
    public async Task<List<Item>> Children(string id)
    {
        try { return (await api.Item(id)).Children ?? []; } catch { return []; }
    }

    // Subtitle sites

    /// Searches the subtitle sites for this title; throws when the search fails.
    public async Task<List<OnlineSubtitle>> SearchOnlineSubtitles() =>
        (await api.SearchOnlineSubtitles(ItemId)).Results ?? [];

    /// Downloads a subtitle the search found and turns it on.
    public async Task AddOnlineSubtitle(OnlineSubtitle sub)
    {
        var track = await api.FetchOnlineSubtitle(ItemId, sub, sessionId);
        ShowSideTrack(track);
    }

    /// Loads a subtitle-site download into mpv, which lists it with its own tracks.
    public void ShowSideTrack(SubtitleTrack track)
    {
        if (track.Url is not { } url) return;
        mpv.Command("sub-add", api.Absolute(url).ToString(), "select", track.Title ?? track.Language ?? "Online");
        SideTracks = SideTracks.Where(t => t.Index != track.Index).ToList();
        Changed?.Invoke();
    }

    // Stats

    /// What the stats panel shows: how the title is playing right now.
    public List<(string, string)> Stats()
    {
        var rows = new List<(string, string)>();
        var r = response;
        rows.Add(("Playback", MethodLabel is { Length: > 0 } m ? m : "—"));
        if (Versions.FirstOrDefault(v => v.Id == FileId) is { Label.Length: > 0 } ver) rows.Add(("Version", ver.Label));
        if (r?.Info is { } info)
        {
            if (info.Container.Length > 0) rows.Add(("Container", info.Container));
            if (info.Bitrate is long b && b > 0) rows.Add(("Source bitrate", Mbps(b)));
            if (info.Video?.FirstOrDefault() is { } v)
                rows.Add(("Video", string.Join(" · ", new[] { v.Codec, v.Width is { } w ? $"{w}×{v.Height}" : null, v.Hdr }.Where(x => !string.IsNullOrEmpty(x)))));
            if ((info.Audio?.FirstOrDefault(a => a.TypeIndex == (AudioIndex ?? r.Decision?.AudioIndex)) ?? info.Audio?.FirstOrDefault()) is { } au)
            {
                var s = au.CodecLabel;
                if (r.Decision?.AudioCodec is { Length: > 0 } c && c != au.Codec) s += " → " + c.ToUpperInvariant();
                rows.Add(("Audio", s));
            }
        }
        if (mpv.Get("width") is { } pw && mpv.Get("height") is { } ph) rows.Add(("Showing", $"{pw}×{ph}"));
        if (mpv.Get("hwdec-current") is { Length: > 0 } hw) rows.Add(("Decoder", hw == "no" ? "Software" : $"Hardware ({hw})"));
        var inv = System.Globalization.CultureInfo.InvariantCulture;
        if (double.TryParse(mpv.Get("video-bitrate"), inv, out var vb) && vb > 0) rows.Add(("Stream bitrate", Mbps(vb)));
        if (double.TryParse(mpv.Get("demuxer-cache-duration"), inv, out var cache)) rows.Add(("Buffer ahead", $"{cache:0.0} s"));
        if (mpv.Get("frame-drop-count") is { } drops) rows.Add(("Dropped frames", drops));
        return rows;
    }

    static string Mbps(double bps) => $"{bps / 1_000_000:0.0} Mbps";

    static string Describe(PlayResponse r)
    {
        var parts = new List<string>();
        switch (r.Method)
        {
            case "direct": parts.Add("Direct Play"); break;
            case "remux": parts.Add("Direct Stream"); break;
            case "transcode": parts.Add("Transcode"); break;
        }
        if (r.Info?.Video?.FirstOrDefault() is { Hdr: { Length: > 0 } h } v) parts.Add(v.DvProfile is { } p ? $"{h} P{p}" : h);
        if (r.Info?.Audio?.FirstOrDefault(a => a.TypeIndex == r.Decision?.AudioIndex) is { } au) parts.Add(au.CodecLabel);
        return string.Join(" · ", parts);
    }
}

/// A live channel through mpv, with channel up/down.
public sealed class LivePlayerModel
{
    readonly WrenTVApi api;
    readonly Mpv mpv;
    public LiveChannel Channel { get; private set; }
    public List<LiveChannel> Channels { get; }
    public bool Loading { get; private set; } = true;
    public string? Error { get; private set; }
    public bool Paused { get; private set; }
    public bool Muted { get; private set; }
    public int Volume { get; private set; } = 100;
    public event Action? Changed;
    string? sessionId;
    CancellationTokenSource? tuning;

    public LivePlayerModel(WrenTVApi api, Mpv mpv, LiveChannel channel, List<LiveChannel> channels)
    {
        this.api = api;
        this.mpv = mpv;
        Channel = channel;
        Channels = channels;
        mpv.OnPause = p => { Paused = p; Changed?.Invoke(); };
        mpv.OnError = m => { Error = m; Changed?.Invoke(); };
        mpv.OnEnd = () => { Error = "The channel stopped."; Changed?.Invoke(); };
    }

    public void Tune(LiveChannel ch)
    {
        tuning?.Cancel();
        var cts = tuning = new CancellationTokenSource();
        Channel = ch;
        Loading = true;
        Error = null;
        Changed?.Invoke();
        App.OnUi(async () =>
        {
            var old = sessionId;
            sessionId = null;
            if (old != null) _ = api.StopSession(old);
            try
            {
                var r = await api.PlayChannel(ch.Id);
                if (cts.IsCancellationRequested) { _ = api.StopSession(r.SessionId); return; }
                sessionId = r.SessionId;
                mpv.Set("start", "none");
                mpv.Set("pause", "no");
                mpv.Load(api.Absolute(r.Url), 0);
                Loading = false;
            }
            catch (Exception e)
            {
                if (cts.IsCancellationRequested) return;
                Loading = false;
                Error = e.Message;
            }
            Changed?.Invoke();
        });
    }

    public void Retry() => Tune(Channel);

    public void Step(int d)
    {
        if (Channels.Count == 0) return;
        var i = Channels.FindIndex(c => c.Id == Channel.Id);
        Tune(Channels[((i < 0 ? 0 : i) + d + Channels.Count) % Channels.Count]);
    }

    public void TogglePause() => mpv.TogglePause();

    public void ToggleMute()
    {
        Muted = !Muted;
        mpv.Set("mute", Muted ? "yes" : "no");
        Changed?.Invoke();
    }

    public void SetVolume(int v)
    {
        Volume = Math.Clamp(v, 0, 100);
        Muted = false;
        mpv.Set("volume", Volume.ToString());
        mpv.Set("mute", "no");
        Changed?.Invoke();
    }

    public void Stop()
    {
        tuning?.Cancel();
        if (sessionId is { } sid) _ = api.StopSession(sid);
        sessionId = null;
        mpv.Command("stop");
    }
}
