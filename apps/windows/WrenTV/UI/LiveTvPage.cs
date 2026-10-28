using System.Globalization;
using System.Text.RegularExpressions;
using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Media;
using Windows.Media.Core;
using Windows.Media.Playback;
using WrenTV.Core;

namespace WrenTV.UI;

/// Live TV, as an overview or a guide, with the picked card or channel's muted
/// live picture and what it's showing up top. The overview has shelves of live
/// and upcoming games with the teams' logos, followed shows, favorite and
/// recent channels, and what's on by kind; the guide has filters (favorites,
/// recent, kinds of channel, the provider's groups) and each channel with
/// what's on now and next. A search looks over channels and upcoming shows
/// from either. Pointing at a card or channel previews it; clicking watches
/// it; right-clicking offers favoriting the channel and following the show.
sealed class LiveTvPage : Page
{
    static readonly (string Key, string Name)[] Genres =
        [("sports", "Sports"), ("news", "News"), ("movies", "Movies"), ("kids", "Kids"), ("music", "Music"), ("entertainment", "Entertainment")];
    static readonly SolidColorBrush LiveRed = new(Windows.UI.Color.FromArgb(255, 0xD7, 0x26, 0x3D));
    static readonly SolidColorBrush StarGold = new(Windows.UI.Color.FromArgb(255, 0xF5, 0xC4, 0x51));

    /// "all", "favorites", "recent", "genre:<kind>" or "group:<name>"; kept while the app runs.
    static string filter = "all";
    /// "overview" or "guide"; kept while the app runs.
    static string view = "overview";
    /// With more than one provider, the id of the one the guide shows ("" for all); kept while the app runs.
    static string provider = "";

    List<LiveChannel> channels = [];
    Dictionary<string, LiveChannel> byId = [];
    List<LiveProvider> providers = [];
    Dictionary<string, List<LiveProgram>> guide = [];
    HashSet<string> favorites = [];
    List<string> recents = [];
    /// Followed shows, by ShowKey.
    HashSet<string> shows = [];
    List<LiveShelf>? shelves;
    string? overviewError;
    LiveTile? pickTile;
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
    StackPanel overview = new();
    ScrollViewer body = new();
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
            if (query.Length == 0 && ticks % 4 == 0)
            {
                if (Overview) await LoadOverview(); else RenderList();
            }
            RenderHero();
        };
        Loaded += (_, _) =>
        {
            clock.Start();
            UpdatePreview();
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
        overview = new StackPanel { Spacing = 28, Margin = new Thickness(0, 0, 0, 40) };
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
            if (query.Length == 0) { Repaint(); return; }
            if (body.Content != list) Repaint();
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

        body = new ScrollViewer();
        Grid.SetRow(body, 3);
        g.Children.Add(body);

        Repaint();
        if (view == "overview") _ = LoadOverview();
        return g;
    }

    async Task Fetch()
    {
        var api = App.State.Api!;
        var lineup = api.LiveLineup();
        var schedule = api.LiveGuide(12);
        var l = await lineup;
        channels = l.Channels;
        byId = channels.GroupBy(c => c.Id).ToDictionary(g => g.Key, g => g.First());
        providers = l.Providers;
        favorites = [.. l.Favorites];
        recents = l.Recents;
        shows = [.. l.Shows.Select(ShowKey)];
        try { guide = (await schedule).ToDictionary(c => c.Id, c => c.Programs); } catch { }
    }

    LiveChannel? Find(string id) => byId.GetValueOrDefault(id);

    LiveProvider? ChosenProvider => providers.Count > 1 ? providers.FirstOrDefault(p => p.Id == provider) : null;

    /// The chosen provider's channels, or every channel.
    IEnumerable<LiveChannel> Mine() => ChosenProvider is { } p ? channels.Where(c => c.Source == p.Id) : channels;

    List<LiveProgram> Programs(LiveChannel c) =>
        guide.TryGetValue(c.Id, out var p) && p.Count > 0 ? p : new[] { c.Now, c.Next }.OfType<LiveProgram>().ToList();

    LiveProgram? OnNow(LiveChannel c, DateTimeOffset now) => Programs(c).FirstOrDefault(p => p.AiringAt(now));
    LiveProgram? UpNext(LiveChannel c, DateTimeOffset now) => Programs(c).FirstOrDefault(p => p.StartDate > now);

    List<LiveChannel> Shown()
    {
        var now = DateTimeOffset.Now;
        IEnumerable<LiveChannel> l = filter switch
        {
            "favorites" => Mine().Where(c => favorites.Contains(c.Id)),
            "recent" => recents.Select(Find).OfType<LiveChannel>().Where(c => ChosenProvider == null || c.Source == ChosenProvider.Id),
            _ when filter.StartsWith("genre:") => Mine().Where(c => c.Genre == filter[6..]),
            _ when filter.StartsWith("group:") => Mine().Where(c => c.Group == filter[6..]),
            _ => Mine(),
        };
        // Channels airing something first, except the recent ones, which keep their order.
        return filter == "recent" ? l.ToList() : l.OrderBy(c => OnNow(c, now) == null).ToList();
    }

    /// The overview shows unless the guide is picked or there's a search.
    bool Overview => view == "overview" && query.Length == 0;

    /// The overview's picked card, or its first.
    LiveTile? HeroTile() => pickTile ?? shelves?.SelectMany(s => s.Tiles).FirstOrDefault(t => Find(t.ChannelId) != null);

    /// Lays the page out again for the view, filter or search.
    void Repaint()
    {
        RenderChips();
        body.Content = Overview ? overview : list;
        if (Overview) RenderOverview(); else if (query.Length == 0) RenderList();
        RenderHero();
        UpdatePreview();
    }

    // ---------- hero ----------

    void Pick(LiveChannel c)
    {
        if (picked?.Id == c.Id) return;
        picked = c;
        RenderHero();
        ShowPreview(c);
    }

    void PickTile(LiveTile t)
    {
        if (pickTile?.Id == t.Id) return;
        pickTile = t;
        RenderHero();
        UpdatePreview();
    }

    /// Previews the picked channel, or the overview's picked card once it's on.
    void UpdatePreview()
    {
        if (!Overview)
        {
            if (picked != null) ShowPreview(picked);
            return;
        }
        if (HeroTile() is { } t && Find(t.ChannelId) is { } c && !(t.Program?.StartDate > DateTimeOffset.Now)) ShowPreview(c);
        else
        {
            preview.Stop();
            surface.Opacity = 0;
        }
    }

    void ShowPreview(LiveChannel c)
    {
        if (preview.ChannelId == c.Id) return;
        surface.Opacity = 0;
        preview.Show(c);
    }

    void RenderHero()
    {
        hero.Children.Clear();
        hero.ColumnDefinitions.Clear();
        if (Overview)
        {
            if (HeroTile() is { } t && Find(t.ChannelId) is { } tc) RenderTileHero(t, tc);
            return;
        }
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
        if (p != null) actions.Children.Add(FollowButton(p.Title));
        actions.Margin = new Thickness(0, 6, 0, 0);
        info.Children.Add(actions);
        if (next != null) info.Children.Add(Ui.Secondary($"Up next  {next.StartDate.LocalDateTime.ToShortTimeString()}  {next.Title}", 13));
        Grid.SetColumn(info, 1);
        hero.Children.Add(info);
    }

    void RenderTileHero(LiveTile t, LiveChannel c)
    {
        var api = App.State.Api!;
        var now = DateTimeOffset.Now;
        var p = t.Program;
        var upcoming = p?.StartDate > now;
        hero.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(448) });
        hero.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });

        var picture = new Grid { Background = new SolidColorBrush(Theme.Surface) };
        picture.Children.Add(TileArt(t, c, 448, true));
        if (surface.Parent is Panel old) old.Children.Remove(surface);
        // Something coming up has no picture yet.
        if (!upcoming) picture.Children.Add(surface);
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
        if (favorites.Contains(c.Id)) top.Children.Add(Ui.Icon("\uE735", 14, StarGold));
        info.Children.Add(top);
        info.Children.Add(Ui.Text(t.Matchup?.Title() ?? p?.Title ?? c.Name, 28, FontWeights.Bold, maxLines: 2));
        if (p != null)
        {
            var meta = Tags(t, now);
            var line = Ui.Secondary(string.Join("  ·  ", new[] { p.Episode, Timing(p, now) }.Where(s => !string.IsNullOrEmpty(s))), 14);
            line.VerticalAlignment = VerticalAlignment.Center;
            meta.Children.Add(line);
            info.Children.Add(meta);
            if (!upcoming)
            {
                var bar = Ui.Progress(p.ProgressAt(now));
                bar.Width = 520;
                bar.HorizontalAlignment = HorizontalAlignment.Left;
                info.Children.Add(bar);
            }
            if (p.Description is { Length: > 0 } d) info.Children.Add(Ui.Secondary(d, 14, 3));
        }

        var fav = favorites.Contains(c.Id);
        var actions = Ui.Row(10,
            Ui.Button(upcoming ? $"Watch {c.Name} now" : "Watch live", () => Watch(c), "\uE768", accent: true),
            Ui.Button(fav ? "Favorite" : "Add to Favorites", () => ToggleFavorite(c), fav ? "\uE735" : "\uE734"));
        if (p != null && t.Matchup == null) actions.Children.Add(FollowButton(p.Title));
        actions.Margin = new Thickness(0, 6, 0, 0);
        info.Children.Add(actions);
        Grid.SetColumn(info, 1);
        hero.Children.Add(info);
    }

    Button FollowButton(string title)
    {
        var on = shows.Contains(ShowKey(title));
        var b = Ui.Button(on ? "Following" : "Follow show", () => ToggleShow(title), on ? "\uE73E" : "\uE710");
        ToolTipService.SetToolTip(b, on ? $"Stop following {title}" : $"Gather {title} under Your shows");
        return b;
    }

    /// When a programme is on, said for where it is in time.
    static string Timing(LiveProgram p, DateTimeOffset now)
    {
        if (p.StartDate > now) return $"{StartsAt(p.StartDate)} – {p.EndDate.LocalDateTime.ToShortTimeString()}";
        var left = Math.Max((int)(p.EndDate - now).TotalMinutes, 0);
        return $"{p.TimeRange}  ·  " + (left >= 60 ? $"{left / 60} hr {left % 60} min left" : $"{left} min left");
    }

    /// "7:00 PM", "Tomorrow 7:00 PM" or "Sat 7:00 PM".
    static string StartsAt(DateTimeOffset d)
    {
        var l = d.LocalDateTime;
        var t = l.ToShortTimeString();
        if (l.Date == DateTime.Today) return t;
        if (l.Date == DateTime.Today.AddDays(1)) return $"Tomorrow {t}";
        return $"{l:ddd} {t}";
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
        void ViewChip(string key, string label, string glyph)
        {
            var b = new ToggleButton
            {
                Content = Ui.Label(glyph, label),
                IsChecked = view == key,
                CornerRadius = new CornerRadius(16),
                Padding = new Thickness(14, 6, 14, 6),
            };
            b.Click += (_, _) =>
            {
                view = key;
                Repaint();
                if (key == "overview" && shelves == null) _ = LoadOverview();
            };
            chips.Children.Add(b);
        }
        ViewChip("overview", "Overview", "\uF0E2");
        ViewChip("guide", "Guide", "\uE8FD");
        if (view == "overview") return;
        chips.Children.Add(new AppBarSeparator());
        if (providers.Count > 1)
        {
            var menu = new MenuFlyout();
            void Pick(string id, string label)
            {
                var m = new ToggleMenuFlyoutItem { Text = label, IsChecked = (ChosenProvider?.Id ?? "") == id };
                m.Click += (_, _) =>
                {
                    provider = id;
                    // A group belongs to one provider's playlist.
                    if (filter.StartsWith("group:")) filter = "all";
                    RenderChips();
                    RenderList();
                };
                menu.Items.Add(m);
            }
            Pick("", "All providers");
            foreach (var p in providers) Pick(p.Id, p.Name);
            chips.Children.Add(new DropDownButton
            {
                Content = ChosenProvider?.Name ?? "All providers",
                Flyout = menu,
                CornerRadius = new CornerRadius(16),
                Padding = new Thickness(14, 6, 14, 6),
            });
        }
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
        var have = Mine().Select(c => c.Genre).OfType<string>().ToHashSet();
        foreach (var (k, n) in Genres) if (have.Contains(k)) Chip($"genre:{k}", n);

        var groups = Mine().Select(c => c.Group).Where(g => !string.IsNullOrEmpty(g)).Distinct().Cast<string>().ToList();
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

    // ---------- overview ----------

    async Task LoadOverview()
    {
        try
        {
            shelves = await App.State.Api!.LiveOverview();
            overviewError = null;
            // A refresh keeps the picked card when it's still there.
            if (pickTile is { } t) pickTile = shelves.SelectMany(s => s.Tiles).FirstOrDefault(x => x.Id == t.Id);
        }
        catch (WrenTVException e) when (shelves == null)
        {
            overviewError = e.Status == 404 ? "Your WrenTV server needs an update for the Live TV overview." : e.Message;
        }
        catch { }
        if (!Overview) return;
        RenderOverview();
        RenderHero();
        UpdatePreview();
    }

    void RenderOverview()
    {
        overview.Children.Clear();
        count.Text = channels.Count == 1 ? "1 channel" : $"{channels.Count} channels";
        if (shelves == null)
        {
            overview.Children.Add(overviewError != null ? Ui.Empty("\uE783", "No overview", overviewError) : Ui.Spinner());
            return;
        }
        if (shelves.Count == 0)
        {
            overview.Children.Add(Ui.Empty("\uE7F4", "Nothing to show yet", "Once the guide has loaded, games, your shows and what's on gather here."));
            return;
        }
        var now = DateTimeOffset.Now;
        foreach (var sh in shelves)
        {
            // Channel up and down step through the shelf's channels.
            var shelf = sh.Tiles.Select(t => Find(t.ChannelId)).OfType<LiveChannel>().DistinctBy(c => c.Id).ToList();
            var cards = sh.Tiles.Select(t => Find(t.ChannelId) is { } c ? Card(t, c, shelf, now) : null).OfType<UIElement>();
            overview.Children.Add(Cards.Shelf(sh.Title, cards));
        }
    }

    UIElement Card(LiveTile t, LiveChannel c, List<LiveChannel> shelf, DateTimeOffset now)
    {
        const double w = 300, h = 169;
        var p = t.Program;
        var upcoming = p?.StartDate > now;
        var art = new Grid { Width = w, Height = h, Background = new SolidColorBrush(Theme.Surface) };
        art.Children.Add(TileArt(t, c, w, false));
        var tags = Tags(t, now);
        tags.Margin = new Thickness(10);
        tags.HorizontalAlignment = HorizontalAlignment.Left;
        tags.VerticalAlignment = VerticalAlignment.Top;
        art.Children.Add(tags);
        if (p != null && p.AiringAt(now))
        {
            var bar = Ui.Progress(p.ProgressAt(now));
            bar.VerticalAlignment = VerticalAlignment.Bottom;
            bar.CornerRadius = new CornerRadius(0);
            art.Children.Add(bar);
        }
        var sub = p == null ? c.Group ?? ""
            : upcoming ? c.Name
            : $"{c.Name} · {Math.Max((int)(p.EndDate - now).TotalMinutes, 0)} min left";
        var col = Ui.Column(8,
            new Border { Width = w, Height = h, CornerRadius = new CornerRadius(10), Child = art },
            Ui.Column(2, Ui.Text(t.Matchup?.Title(true) ?? p?.Title ?? c.Name, 15, FontWeights.SemiBold, maxLines: 1), Ui.Secondary(sub, 12)));
        col.Width = w;
        var b = Cards.Plain(col);
        var menu = Menu(c, t.Matchup == null ? p?.Title : null, shelf);
        b.ContextFlyout = menu;
        // Something coming up can't be watched yet: offer its options.
        b.Click += (_, _) => { if (upcoming) menu.ShowAt(b); else Watch(c, shelf); };
        b.PointerEntered += (_, _) => PickTile(t);
        b.GotFocus += (_, _) => PickTile(t);
        if (p?.Description is { Length: > 0 } d) ToolTipService.SetToolTip(b, d);
        return b;
    }

    /// A tile's picture: the matchup, the programme's art over its channel's logo, or the logo alone.
    UIElement TileArt(LiveTile t, LiveChannel c, double width, bool big)
    {
        var api = App.State.Api!;
        var g = new Grid();
        Uri? art = null;
        if (t.Matchup is { } m) g.Children.Add(MatchupArt(m, big));
        else
        {
            g.Children.Add(Art(c, null, (int)width));
            art = api.LiveLogoUrl(t.Art ?? t.Program?.Icon);
            if (art != null) g.Children.Add(Ui.Image(art, (int)width));
        }
        if ((t.Matchup != null || art != null) && api.LiveLogoUrl(c.Logo) is { } logo)
        {
            // Which channel it's on, like a network's bug.
            g.Children.Add(new Border
            {
                Width = big ? 96 : 64,
                Height = big ? 50 : 34,
                Padding = new Thickness(5),
                Margin = new Thickness(10),
                CornerRadius = new CornerRadius(6),
                Background = new SolidColorBrush(Windows.UI.Color.FromArgb(0x99, 0, 0, 0)),
                HorizontalAlignment = HorizontalAlignment.Right,
                VerticalAlignment = VerticalAlignment.Bottom,
                Child = Ui.Image(logo, 192, Stretch.Uniform),
            });
        }
        return g;
    }

    /// Two teams over their colours, like a scoreboard.
    static UIElement MatchupArt(LiveMatchup m, bool big)
    {
        var g = new Grid
        {
            Background = new LinearGradientBrush
            {
                StartPoint = new Windows.Foundation.Point(0, 0),
                EndPoint = new Windows.Foundation.Point(1, 1),
                GradientStops =
                {
                    new GradientStop { Color = Hex(m.Away.Color) ?? Windows.UI.Color.FromArgb(255, 0x2B, 0x29, 0x33), Offset = 0 },
                    new GradientStop { Color = Hex(m.Home.Color) ?? Windows.UI.Color.FromArgb(255, 0x3A, 0x35, 0x42), Offset = 1 },
                },
            },
        };
        // Darkened, so white logos and names read on any team's colour.
        g.Children.Add(new Border { Background = new SolidColorBrush(Windows.UI.Color.FromArgb(0x52, 0, 0, 0)) });
        var row = new Grid { VerticalAlignment = VerticalAlignment.Center };
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        var size = big ? 96 : 64;
        row.Children.Add(Team(m.Away, size));
        var at = Ui.Text("@", big ? 26 : 18, FontWeights.SemiBold, new SolidColorBrush(Windows.UI.Color.FromArgb(0xCC, 0xFF, 0xFF, 0xFF)));
        at.VerticalAlignment = VerticalAlignment.Center;
        Grid.SetColumn(at, 1);
        row.Children.Add(at);
        var home = Team(m.Home, size);
        Grid.SetColumn(home, 2);
        row.Children.Add(home);
        g.Children.Add(row);
        return g;
    }

    static FrameworkElement Team(LiveTeam t, double size)
    {
        var white = new SolidColorBrush(Microsoft.UI.Colors.White);
        FrameworkElement mark;
        if (t.Logo is { Length: > 0 } l && Uri.TryCreate(l, UriKind.Absolute, out var u))
        {
            var img = Ui.Image(u, (int)size * 2, Stretch.Uniform);
            img.Width = size;
            img.Height = size;
            mark = img;
        }
        else
        {
            var initials = Ui.Text(string.Concat(t.Name.Split(' ', StringSplitOptions.RemoveEmptyEntries).Take(2).Select(w => w[0])).ToUpperInvariant(),
                size / 3, FontWeights.Bold, white);
            initials.HorizontalAlignment = HorizontalAlignment.Center;
            initials.VerticalAlignment = VerticalAlignment.Center;
            mark = new Border
            {
                Width = size, Height = size, CornerRadius = new CornerRadius(size / 2),
                Background = new SolidColorBrush(Windows.UI.Color.FromArgb(0x29, 0xFF, 0xFF, 0xFF)),
                Child = initials,
            };
        }
        mark.HorizontalAlignment = HorizontalAlignment.Center;
        var name = Ui.Text(t.Short ?? t.Name, 12, FontWeights.Bold, white, maxLines: 1);
        name.HorizontalAlignment = HorizontalAlignment.Center;
        var col = Ui.Column(6, mark, name);
        col.HorizontalAlignment = HorizontalAlignment.Center;
        return col;
    }

    /// "1D428A" as a colour; null when it isn't one.
    static Windows.UI.Color? Hex(string? s)
    {
        s = s?.TrimStart('#');
        if (s is not { Length: 6 } || !uint.TryParse(s, NumberStyles.HexNumber, null, out var v)) return null;
        return Windows.UI.Color.FromArgb(255, (byte)(v >> 16), (byte)(v >> 8), (byte)v);
    }

    /// A tile's tags: its start time when it's coming up, else LIVE and NEW; and the league.
    StackPanel Tags(LiveTile t, DateTimeOffset now)
    {
        var r = Ui.Row(6);
        var dark = new SolidColorBrush(Windows.UI.Color.FromArgb(0xBF, 0, 0, 0));
        if (t.Program is { } p)
        {
            if (p.StartDate > now) r.Children.Add(Badge(StartsAt(p.StartDate), dark));
            else
            {
                if (p.IsLive) r.Children.Add(Badge("LIVE", LiveRed));
                if (p.IsNew) r.Children.Add(Badge("NEW", Theme.Brush(Theme.Cream, 0.22)));
            }
        }
        if (t.Matchup?.League is { Length: > 0 } league) r.Children.Add(Badge(league.ToUpperInvariant(), dark));
        return r;
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
        b.ContextFlyout = Menu(c, OnNow(c, now)?.Title);
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
        b.ContextFlyout = Menu(c, p.Title);
        return b;
    }

    /// Watch, favorite the channel and, given a show, follow it.
    MenuFlyout Menu(LiveChannel c, string? show = null, List<LiveChannel>? shelf = null)
    {
        var menu = new MenuFlyout();
        var watch = new MenuFlyoutItem { Text = "Watch", Icon = new FontIcon { Glyph = "" } };
        watch.Click += (_, _) => Watch(c, shelf);
        var fav = favorites.Contains(c.Id);
        var star = new MenuFlyoutItem { Text = fav ? "Remove from Favorites" : "Add to Favorites", Icon = new FontIcon { Glyph = fav ? "" : "" } };
        star.Click += (_, _) => ToggleFavorite(c);
        menu.Items.Add(watch);
        menu.Items.Add(star);
        if (show is { Length: > 0 })
        {
            var on = shows.Contains(ShowKey(show));
            var follow = new MenuFlyoutItem { Text = on ? $"Stop Following “{show}”" : $"Follow “{show}”", Icon = new FontIcon { Glyph = on ? "\uE73E" : "\uE710" } };
            follow.Click += (_, _) => ToggleShow(show);
            menu.Items.Add(follow);
        }
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
        try
        {
            await App.State.Api!.SetLiveFavorite(c.Id, on);
            if (shelves != null) await LoadOverview();
        }
        catch
        {
            if (on) favorites.Remove(c.Id); else favorites.Add(c.Id);
            Redraw();
        }
    }

    async void ToggleShow(string title)
    {
        var key = ShowKey(title);
        var on = !shows.Contains(key);
        if (on) shows.Add(key); else shows.Remove(key);
        Redraw();
        try
        {
            await App.State.Api!.SetLiveShow(title, on);
            if (shelves != null) await LoadOverview();
        }
        catch
        {
            if (on) shows.Remove(key); else shows.Add(key);
            Redraw();
        }
    }

    /// A show's title as the server keys followed shows: lower case, words only.
    static string ShowKey(string title) =>
        string.Join(' ', Regex.Split(title.ToLowerInvariant(), @"[^\p{L}\p{N}]+").Where(w => w.Length > 0));

    void Redraw()
    {
        RenderHero();
        if (query.Length > 0) return;
        if (Overview) RenderOverview(); else RenderList();
    }

    void Watch(LiveChannel c, List<LiveChannel>? shelf = null)
    {
        preview.Stop();
        surface.Opacity = 0;
        recents = [c.Id, .. recents.Where(id => id != c.Id)];
        // Channel up and down step through the guide as it's filtered.
        var shown = shelf ?? Shown();
        LivePlayerWindow.Open(c, (shelf != null || query.Length == 0) && shown.Any(x => x.Id == c.Id) ? shown : channels, App.State.Api!);
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
    /// The channel showing or about to.
    public string? ChannelId => channelId;
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
