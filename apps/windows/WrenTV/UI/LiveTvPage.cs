using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Media;
using Windows.Media.Core;
using Windows.Media.Playback;
using WrenTV.Core;

namespace WrenTV.UI;

/// Live TV: the picked channel's muted live picture and what it's showing
/// up top, filters (favorites, recent, kinds of channel, the provider's
/// groups) and a search over channels and upcoming shows, then each channel
/// with what's on now and next. Pointing at a channel previews it; clicking
/// watches it.
sealed class LiveTvPage : Page
{
    static readonly (string Key, string Name)[] Genres =
        [("sports", "Sports"), ("news", "News"), ("movies", "Movies"), ("kids", "Kids"), ("music", "Music"), ("entertainment", "Entertainment")];
    static readonly SolidColorBrush LiveRed = new(Windows.UI.Color.FromArgb(255, 0xD7, 0x26, 0x3D));
    static readonly SolidColorBrush StarGold = new(Windows.UI.Color.FromArgb(255, 0xF5, 0xC4, 0x51));

    /// "all", "favorites", "recent", "genre:<kind>" or "group:<name>"; kept while the app runs.
    static string filter = "all";

    List<LiveChannel> channels = [];
    Dictionary<string, List<LiveProgram>> guide = [];
    HashSet<string> favorites = [];
    List<string> recents = [];
    LiveChannel? picked;
    string query = "";
    int searchGen;
    int ticks;

    readonly LivePreview preview;
    readonly DispatcherTimer clock = new() { Interval = TimeSpan.FromSeconds(30) };
    // Rebuilt in place as the filter, search or clock changes, so the search box keeps its text and focus.
    Grid hero = new();
    StackPanel chips = new();
    StackPanel list = new();
    TextBlock count = new();
    readonly MediaPlayerElement surface = new() { Stretch = Stretch.UniformToFill, AreTransportControlsEnabled = false, Opacity = 0 };

    public override bool ReloadsOnRefresh => false;

    public LiveTvPage()
    {
        preview = new LivePreview(App.State.Api!);
        surface.SetMediaPlayer(preview.Player);
        preview.Started += () => DispatcherQueue.TryEnqueue(() => surface.Opacity = 1);
        clock.Tick += async (_, _) =>
        {
            // What's on changes by the minute: redraw in place, fetch the guide again now and then.
            if (++ticks % 20 == 0) await Fetch();
            if (query.Length == 0 && ticks % 4 == 0) RenderList();
            RenderHero();
        };
        Loaded += (_, _) =>
        {
            clock.Start();
            if (picked != null) ShowPreview(picked);
        };
        Unloaded += (_, _) =>
        {
            clock.Stop();
            preview.Stop();
        };
    }

    protected override async Task<UIElement> Build()
    {
        await Fetch();
        if (channels.Count == 0) return Ui.Empty("", "No channels", "Add an M3U playlist in the server's Live TV settings.");
        picked = recents.Select(Find).FirstOrDefault(c => c != null) ?? channels[0];
        hero = new Grid { ColumnSpacing = 28, Margin = new Thickness(Gutter, 0, Gutter, 0) };
        chips = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8 };
        list = new StackPanel { Spacing = 6, Margin = new Thickness(Gutter, 0, Gutter, 40) };
        count = Ui.Secondary("", 14);

        var g = new Grid { RowSpacing = 18, Padding = new Thickness(0, 24, 0, 0) };
        for (var i = 0; i < 3; i++) g.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        g.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });

        var header = new Grid { Margin = new Thickness(Gutter, 0, Gutter, 0) };
        header.Children.Add(Ui.Column(4, Ui.Title("Live TV", 30), count));
        var search = new TextBox
        {
            PlaceholderText = "Search channels and shows",
            Width = 340,
            HorizontalAlignment = HorizontalAlignment.Right,
            VerticalAlignment = VerticalAlignment.Center,
            CornerRadius = new CornerRadius(18),
            Text = query,
        };
        search.TextChanged += async (_, _) =>
        {
            query = search.Text.Trim();
            var gen = ++searchGen;
            if (query.Length == 0) { RenderList(); return; }
            await Task.Delay(250);
            if (gen != searchGen) return;
            try
            {
                var r = await App.State.Api!.LiveSearch(query);
                if (gen == searchGen) RenderResults(r);
            }
            catch { }
        };
        header.Children.Add(search);
        g.Children.Add(header);

        Grid.SetRow(hero, 1);
        g.Children.Add(hero);

        var chipScroll = new ScrollViewer
        {
            Content = chips,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Hidden,
            HorizontalScrollMode = ScrollMode.Enabled,
            VerticalScrollBarVisibility = ScrollBarVisibility.Disabled,
            VerticalScrollMode = ScrollMode.Disabled,
            Padding = new Thickness(Gutter, 0, Gutter, 0),
        };
        Grid.SetRow(chipScroll, 2);
        g.Children.Add(chipScroll);

        var scroll = new ScrollViewer { Content = list };
        Grid.SetRow(scroll, 3);
        g.Children.Add(scroll);

        RenderHero();
        RenderChips();
        RenderList();
        ShowPreview(picked);
        return g;
    }

    async Task Fetch()
    {
        var api = App.State.Api!;
        var lineup = api.LiveLineup();
        var schedule = api.LiveGuide(12);
        var l = await lineup;
        channels = l.Channels;
        favorites = [.. l.Favorites];
        recents = l.Recents;
        try { guide = (await schedule).ToDictionary(c => c.Id, c => c.Programs); } catch { }
    }

    LiveChannel? Find(string id) => channels.FirstOrDefault(c => c.Id == id);

    List<LiveProgram> Programs(LiveChannel c) =>
        guide.TryGetValue(c.Id, out var p) && p.Count > 0 ? p : new[] { c.Now, c.Next }.OfType<LiveProgram>().ToList();

    LiveProgram? OnNow(LiveChannel c, DateTimeOffset now) => Programs(c).FirstOrDefault(p => p.AiringAt(now));
    LiveProgram? UpNext(LiveChannel c, DateTimeOffset now) => Programs(c).FirstOrDefault(p => p.StartDate > now);

    List<LiveChannel> Shown()
    {
        var now = DateTimeOffset.Now;
        IEnumerable<LiveChannel> l = filter switch
        {
            "favorites" => channels.Where(c => favorites.Contains(c.Id)),
            "recent" => recents.Select(Find).OfType<LiveChannel>(),
            _ when filter.StartsWith("genre:") => channels.Where(c => c.Genre == filter[6..]),
            _ when filter.StartsWith("group:") => channels.Where(c => c.Group == filter[6..]),
            _ => channels,
        };
        // Channels airing something first, except the recent ones, which keep their order.
        return filter == "recent" ? l.ToList() : l.OrderBy(c => OnNow(c, now) == null).ToList();
    }

    // ---------- hero ----------

    void Pick(LiveChannel c)
    {
        if (picked?.Id == c.Id) return;
        picked = c;
        RenderHero();
        ShowPreview(c);
    }

    void ShowPreview(LiveChannel c)
    {
        surface.Opacity = 0;
        preview.Show(c);
    }

    void RenderHero()
    {
        hero.Children.Clear();
        hero.ColumnDefinitions.Clear();
        if (picked is not { } c) return;
        var api = App.State.Api!;
        var now = DateTimeOffset.Now;
        var p = OnNow(c, now);
        var next = UpNext(c, now);
        hero.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(448) });
        hero.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });

        var picture = new Grid { Background = new SolidColorBrush(Theme.Surface) };
        picture.Children.Add(Art(c, p, 448));
        if (surface.Parent is Panel old) old.Children.Remove(surface);
        picture.Children.Add(surface);
        var live = Badge("LIVE", LiveRed);
        live.Margin = new Thickness(12);
        live.HorizontalAlignment = HorizontalAlignment.Left;
        live.VerticalAlignment = VerticalAlignment.Top;
        picture.Children.Add(live);
        var frame = new Border { Width = 448, Height = 252, CornerRadius = new CornerRadius(12), Child = picture };
        var watchFrame = new Button { Content = frame, Padding = new Thickness(0), CornerRadius = new CornerRadius(12), BorderThickness = new Thickness(0) };
        watchFrame.Click += (_, _) => Watch(c);
        ToolTipService.SetToolTip(watchFrame, $"Watch {c.Name}");
        hero.Children.Add(watchFrame);

        var info = Ui.Column(8);
        info.VerticalAlignment = VerticalAlignment.Top;
        var top = Ui.Row(12);
        if (api.LiveLogoUrl(c.Logo) is { } u)
        {
            var logo = Ui.Image(u, 160, Stretch.Uniform);
            logo.Width = 80;
            logo.Height = 40;
            top.Children.Add(logo);
        }
        var name = Ui.Text(string.Join("  ", new[] { c.Number, c.Name }.Where(s => !string.IsNullOrEmpty(s))), 14, FontWeights.SemiBold, Theme.SecondaryText);
        name.VerticalAlignment = VerticalAlignment.Center;
        top.Children.Add(name);
        if (favorites.Contains(c.Id)) top.Children.Add(Ui.Icon("", 14, StarGold));
        info.Children.Add(top);
        info.Children.Add(Ui.Text(p?.Title ?? c.Name, 28, FontWeights.Bold, maxLines: 2));
        if (p != null)
        {
            var meta = Ui.Row(8);
            if (p.IsLive) meta.Children.Add(Badge("LIVE", LiveRed));
            if (p.IsNew) meta.Children.Add(Badge("NEW", Theme.Brush(Theme.Cream, 0.22)));
            var left = Math.Max((int)(p.EndDate - now).TotalMinutes, 0);
            var line = Ui.Secondary(string.Join("  ·  ", new[] { p.Episode, p.TimeRange, left >= 60 ? $"{left / 60} hr {left % 60} min left" : $"{left} min left" }
                .Where(s => !string.IsNullOrEmpty(s))), 14);
            line.VerticalAlignment = VerticalAlignment.Center;
            meta.Children.Add(line);
            info.Children.Add(meta);
            if (p.Categories is { Count: > 0 } cats) info.Children.Add(Ui.Text(string.Join(" · ", cats.Take(3)), 12, color: Theme.TertiaryText));
            var bar = Ui.Progress(p.ProgressAt(now));
            bar.MaxWidth = 520;
            bar.HorizontalAlignment = HorizontalAlignment.Left;
            bar.Width = 520;
            info.Children.Add(bar);
            if (p.Description is { Length: > 0 } d) info.Children.Add(Ui.Secondary(d, 14, 3));
        }
        else info.Children.Add(Ui.Secondary("No guide information for this channel", 14));

        var fav = favorites.Contains(c.Id);
        var actions = Ui.Row(10,
            Ui.Button("Watch live", () => Watch(c), "", accent: true),
            Ui.Button(fav ? "Favorite" : "Add to Favorites", () => ToggleFavorite(c), fav ? "" : ""));
        actions.Margin = new Thickness(0, 6, 0, 0);
        info.Children.Add(actions);
        if (next != null) info.Children.Add(Ui.Secondary($"Up next  {next.StartDate.LocalDateTime.ToShortTimeString()}  {next.Title}", 13));
        Grid.SetColumn(info, 1);
        hero.Children.Add(info);
    }

    /// The programme's artwork, or the channel's logo on a soft backdrop.
    UIElement Art(LiveChannel c, LiveProgram? p, int width)
    {
        if (p?.Icon is { Length: > 0 } icon && Uri.TryCreate(icon, UriKind.Absolute, out var art)) return Ui.Image(art, width);
        var g = new Grid
        {
            Background = new LinearGradientBrush
            {
                StartPoint = new Windows.Foundation.Point(0, 0),
                EndPoint = new Windows.Foundation.Point(1, 1),
                GradientStops =
                {
                    new GradientStop { Color = Windows.UI.Color.FromArgb(255, 0x34, 0x30, 0x3A), Offset = 0 },
                    new GradientStop { Color = Windows.UI.Color.FromArgb(255, 0x12, 0x10, 0x16), Offset = 1 },
                },
            },
        };
        if (App.State.Api!.LiveLogoUrl(c.Logo) is { } u)
        {
            var logo = Ui.Image(u, width / 2, Stretch.Uniform);
            logo.Width = width * 0.36;
            logo.MaxHeight = width * 0.2;
            logo.HorizontalAlignment = HorizontalAlignment.Center;
            logo.VerticalAlignment = VerticalAlignment.Center;
            g.Children.Add(logo);
        }
        else
        {
            var t = Ui.Text(c.Name, 18, FontWeights.SemiBold, Theme.SecondaryText, maxLines: 2);
            t.HorizontalAlignment = HorizontalAlignment.Center;
            t.VerticalAlignment = VerticalAlignment.Center;
            g.Children.Add(t);
        }
        return g;
    }

    // ---------- filters ----------

    void RenderChips()
    {
        chips.Children.Clear();
        void Chip(string key, string label, string? glyph = null)
        {
            var b = new ToggleButton
            {
                Content = glyph == null ? label : Ui.Label(glyph, label),
                IsChecked = filter == key,
                CornerRadius = new CornerRadius(16),
                Padding = new Thickness(14, 6, 14, 6),
            };
            b.Click += (_, _) => { filter = key; RenderChips(); RenderList(); };
            chips.Children.Add(b);
        }
        Chip("all", "All channels");
        Chip("favorites", "Favorites", "");
        if (recents.Count > 0) Chip("recent", "Recent", "");
        var have = channels.Select(c => c.Genre).OfType<string>().ToHashSet();
        foreach (var (k, n) in Genres) if (have.Contains(k)) Chip($"genre:{k}", n);

        var groups = channels.Select(c => c.Group).Where(g => !string.IsNullOrEmpty(g)).Distinct().Cast<string>().ToList();
        if (groups.Count > 1)
        {
            var menu = new MenuFlyout();
            foreach (var x in groups)
            {
                var m = new ToggleMenuFlyoutItem { Text = x, IsChecked = filter == $"group:{x}" };
                m.Click += (_, _) => { filter = $"group:{x}"; RenderChips(); RenderList(); };
                menu.Items.Add(m);
            }
            chips.Children.Add(new DropDownButton
            {
                Content = filter.StartsWith("group:") ? filter[6..] : "Groups",
                Flyout = menu,
                CornerRadius = new CornerRadius(16),
                Padding = new Thickness(14, 6, 14, 6),
            });
        }
    }

    // ---------- channels ----------

    void RenderList()
    {
        list.Children.Clear();
        var shown = Shown();
        count.Text = shown.Count == 1 ? "1 channel" : $"{shown.Count} channels";
        var now = DateTimeOffset.Now;
        foreach (var c in shown) list.Children.Add(Row(c, now));
        if (shown.Count == 0)
        {
            list.Children.Add(filter == "favorites"
                ? Ui.Empty("", "No favorites yet", "Right-click a channel, or use Add to Favorites above, to star it.")
                : Ui.Empty("", "No channels here", "Pick another filter."));
        }
    }

    void RenderResults(LiveSearchResult r)
    {
        list.Children.Clear();
        var now = DateTimeOffset.Now;
        var found = r.Channels.Select(c => Find(c.Id)).OfType<LiveChannel>().ToList();
        count.Text = $"{found.Count + r.Airings.Count} results";
        if (found.Count > 0)
        {
            list.Children.Add(Ui.Text("Channels", 18, FontWeights.SemiBold));
            foreach (var c in found) list.Children.Add(Row(c, now));
        }
        if (r.Airings.Count > 0)
        {
            var h = Ui.Text("Shows", 18, FontWeights.SemiBold);
            h.Margin = new Thickness(0, found.Count > 0 ? 16 : 0, 0, 0);
            list.Children.Add(h);
            foreach (var a in r.Airings)
                if (Find(a.ChannelId) is { } c) list.Children.Add(AiringRow(a.Program, c, now));
        }
        if (found.Count == 0 && r.Airings.Count == 0) list.Children.Add(Ui.Empty("", $"Nothing matches “{query}”", "Try a channel name, number or show."));
    }

    UIElement Row(LiveChannel c, DateTimeOffset now)
    {
        var api = App.State.Api!;
        var g = new Grid { ColumnSpacing = 18, Padding = new Thickness(12) };
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(48) });
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(120) });
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(2, GridUnitType.Star) });
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });

        var number = Ui.Text(c.Number ?? "", 15, FontWeights.SemiBold, Theme.SecondaryText);
        number.VerticalAlignment = VerticalAlignment.Center;
        g.Children.Add(number);

        var logo = new Border
        {
            Width = 120, Height = 64, CornerRadius = new CornerRadius(8), Padding = new Thickness(8),
            Background = new SolidColorBrush(Theme.Surface),
            Child = api.LiveLogoUrl(c.Logo) is { } u
                ? Ui.Image(u, 240, Stretch.Uniform)
                : Ui.Text(c.Name, 12, FontWeights.SemiBold, maxLines: 2),
        };
        Grid.SetColumn(logo, 1);
        g.Children.Add(logo);

        var title = Ui.Row(6, Ui.Text(c.Name, 13, FontWeights.SemiBold, Theme.SecondaryText));
        if (favorites.Contains(c.Id)) title.Children.Add(Ui.Icon("", 11, StarGold));
        var nowCol = Ui.Column(4, title);
        if (OnNow(c, now) is { } p)
        {
            var line = Ui.Row(6);
            if (p.IsLive) line.Children.Add(Badge("LIVE", LiveRed));
            if (p.IsNew) line.Children.Add(Badge("NEW", Theme.Brush(Theme.Cream, 0.22)));
            line.Children.Add(Ui.Text(p.Title, 16, FontWeights.SemiBold, maxLines: 1));
            nowCol.Children.Add(line);
            var bar = Ui.Progress(p.ProgressAt(now));
            bar.Width = 180;
            bar.HorizontalAlignment = HorizontalAlignment.Left;
            bar.VerticalAlignment = VerticalAlignment.Center;
            nowCol.Children.Add(Ui.Row(10, bar, Ui.Secondary($"{Math.Max((int)(p.EndDate - now).TotalMinutes, 0)} min left", 12)));
        }
        else nowCol.Children.Add(Ui.Secondary("No guide information"));
        nowCol.VerticalAlignment = VerticalAlignment.Center;
        Grid.SetColumn(nowCol, 2);
        g.Children.Add(nowCol);

        if (UpNext(c, now) is { } n)
        {
            var next = Ui.Column(2, Ui.Secondary("Next", 12), Ui.Text(n.Title, 14, maxLines: 1), Ui.Secondary(n.StartDate.LocalDateTime.ToShortTimeString(), 12));
            next.VerticalAlignment = VerticalAlignment.Center;
            Grid.SetColumn(next, 3);
            g.Children.Add(next);
        }

        var b = new Button
        {
            Content = g,
            Padding = new Thickness(0),
            HorizontalAlignment = HorizontalAlignment.Stretch,
            HorizontalContentAlignment = HorizontalAlignment.Stretch,
            CornerRadius = new CornerRadius(12),
        };
        b.Click += (_, _) => Watch(c);
        b.PointerEntered += (_, _) => Pick(c);
        b.GotFocus += (_, _) => Pick(c);
        b.ContextFlyout = Menu(c);
        return b;
    }

    UIElement AiringRow(LiveProgram p, LiveChannel c, DateTimeOffset now)
    {
        var airing = p.AiringAt(now);
        var g = new Grid { ColumnSpacing = 18, Padding = new Thickness(12) };
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(128) });
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        g.Children.Add(new Border { Width = 128, Height = 72, CornerRadius = new CornerRadius(8), Child = Art(c, p, 128) });
        var meta = Ui.Row(6);
        if (airing) meta.Children.Add(Badge("ON NOW", Theme.EmberBrush));
        if (p.IsNew) meta.Children.Add(Badge("NEW", Theme.Brush(Theme.Cream, 0.22)));
        var start = p.StartDate.LocalDateTime;
        var when = start.Date == DateTime.Today ? start.ToShortTimeString() : $"{start:ddd} {start.ToShortTimeString()}";
        meta.Children.Add(Ui.Secondary(string.Join(" · ", new[] { p.Episode, c.Name, airing ? null : when }.Where(s => !string.IsNullOrEmpty(s))), 13));
        var col = Ui.Column(4, Ui.Text(p.Title, 16, FontWeights.SemiBold, maxLines: 1), meta);
        col.VerticalAlignment = VerticalAlignment.Center;
        Grid.SetColumn(col, 1);
        g.Children.Add(col);
        var b = new Button
        {
            Content = g,
            Padding = new Thickness(0),
            HorizontalAlignment = HorizontalAlignment.Stretch,
            HorizontalContentAlignment = HorizontalAlignment.Stretch,
            CornerRadius = new CornerRadius(12),
        };
        // Something later can't be watched yet: show its channel.
        b.Click += (_, _) => { if (airing) Watch(c); else Pick(c); };
        b.PointerEntered += (_, _) => Pick(c);
        b.ContextFlyout = Menu(c);
        return b;
    }

    MenuFlyout Menu(LiveChannel c)
    {
        var menu = new MenuFlyout();
        var watch = new MenuFlyoutItem { Text = "Watch", Icon = new FontIcon { Glyph = "" } };
        watch.Click += (_, _) => Watch(c);
        var fav = favorites.Contains(c.Id);
        var star = new MenuFlyoutItem { Text = fav ? "Remove from Favorites" : "Add to Favorites", Icon = new FontIcon { Glyph = fav ? "" : "" } };
        star.Click += (_, _) => ToggleFavorite(c);
        menu.Items.Add(watch);
        menu.Items.Add(star);
        return menu;
    }

    static Border Badge(string text, Brush color) => new()
    {
        Background = color,
        CornerRadius = new CornerRadius(3),
        Padding = new Thickness(5, 1, 5, 1),
        VerticalAlignment = VerticalAlignment.Center,
        Child = Ui.Text(text, 10, FontWeights.Bold, new SolidColorBrush(Microsoft.UI.Colors.White)),
    };

    // ---------- actions ----------

    async void ToggleFavorite(LiveChannel c)
    {
        var on = !favorites.Contains(c.Id);
        if (on) favorites.Add(c.Id); else favorites.Remove(c.Id);
        Redraw();
        try { await App.State.Api!.SetLiveFavorite(c.Id, on); }
        catch
        {
            if (on) favorites.Remove(c.Id); else favorites.Add(c.Id);
            Redraw();
        }
    }

    void Redraw()
    {
        RenderHero();
        if (query.Length == 0) RenderList();
    }

    void Watch(LiveChannel c)
    {
        preview.Stop();
        surface.Opacity = 0;
        recents = [c.Id, .. recents.Where(id => id != c.Id)];
        // Channel up and down step through the guide as it's filtered.
        var shown = Shown();
        LivePlayerWindow.Open(c, query.Length == 0 && shown.Any(x => x.Id == c.Id) ? shown : channels, App.State.Api!);
        RenderChips();
    }
}

/// The guide's muted live picture of the picked channel, in Windows' own
/// player (the full player is mpv in its own window). It starts once the
/// pointer rests on a channel for a moment, and the server doesn't count
/// it as watching.
sealed class LivePreview(WrenTVApi api)
{
    public readonly MediaPlayer Player = MakePlayer();
    /// The picture started moving; raised off the UI thread.
    public event Action? Started;
    string? channelId;
    string? sessionId;
    int gen;

    static MediaPlayer MakePlayer() => new() { IsMuted = true, AutoPlay = true };

    bool hooked;

    public async void Show(LiveChannel c, int delayMs = 900)
    {
        if (!hooked)
        {
            hooked = true;
            Player.PlaybackSession.PlaybackStateChanged += (s, _) =>
            {
                if (s.PlaybackState == MediaPlaybackState.Playing) Started?.Invoke();
            };
        }
        if (c.Id == channelId) return;
        Stop();
        channelId = c.Id;
        var my = gen;
        await Task.Delay(delayMs);
        if (my != gen) return;
        LivePlayResponse r;
        try { r = await api.PreviewChannel(c.Id); }
        catch { return; }
        if (my != gen || r.Type != "hls")
        {
            _ = api.StopSession(r.SessionId);
            return;
        }
        sessionId = r.SessionId;
        Player.Source = MediaSource.CreateFromUri(api.Absolute(r.Url));
        Player.Play();
    }

    public void Stop()
    {
        gen++;
        channelId = null;
        Player.Source = null;
        if (sessionId is { } sid) _ = api.StopSession(sid);
        sessionId = null;
    }
}
