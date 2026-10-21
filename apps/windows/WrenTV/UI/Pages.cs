using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Media;
using WrenTV.Core;

namespace WrenTV.UI;

/// A big backdrop with the title, overview and Play / More Info.
static class Hero
{
    public static UIElement Make(string title, string? overview, string? backdrop, string meta, IEnumerable<Button> actions)
    {
        var api = App.State.Api;
        var g = new Grid { Height = 440, Margin = new Thickness(Page.Gutter, 0, Page.Gutter, 0), CornerRadius = new CornerRadius(16) };
        g.Background = new SolidColorBrush(Theme.Surface);
        g.Children.Add(Ui.Image(api?.ImageUrl(backdrop, "w1280"), 1600));
        g.Children.Add(new Border
        {
            Background = new LinearGradientBrush
            {
                StartPoint = new Windows.Foundation.Point(0, 0.5),
                EndPoint = new Windows.Foundation.Point(1, 0.5),
                GradientStops =
                {
                    new GradientStop { Color = Windows.UI.Color.FromArgb(0xF0, 0x12, 0x10, 0x16), Offset = 0 },
                    new GradientStop { Color = Windows.UI.Color.FromArgb(0x90, 0x12, 0x10, 0x16), Offset = 0.45 },
                    new GradientStop { Color = Windows.UI.Color.FromArgb(0x00, 0x12, 0x10, 0x16), Offset = 0.8 },
                },
            },
        });
        var text = Ui.Column(12, Ui.Text(title, 40, FontWeights.Bold, maxLines: 2));
        if (meta.Length > 0) text.Children.Add(Ui.Secondary(meta, 14));
        if (!string.IsNullOrEmpty(overview)) text.Children.Add(Ui.Secondary(overview, 15, 3));
        var row = Ui.Row(10);
        foreach (var a in actions) row.Children.Add(a);
        row.Margin = new Thickness(0, 8, 0, 0);
        text.Children.Add(row);
        text.MaxWidth = 560;
        text.Margin = new Thickness(40);
        text.VerticalAlignment = VerticalAlignment.Bottom;
        text.HorizontalAlignment = HorizontalAlignment.Left;
        g.Children.Add(text);
        return g;
    }
}

/// Home. A streaming server shows the Mac app's discover layout: a rotating
/// banner of what's trending, then rows from all of TMDB. Otherwise, or on a
/// server too old for it, the libraries' own shelves.
sealed class HomePage : Page
{
    protected override bool HasAmbient => true;

    protected override async Task<UIElement> Build()
    {
        var api = App.State.Api!;
        if (App.State.CanStream)
        {
            try
            {
                return Discover(await api.DiscoverHome());
            }
            catch (WrenTVException e) when (e.Status == 404) { }
            catch (System.Text.Json.JsonException) { }
        }
        return LibraryHome(await api.Home());
    }

    UIElement Discover(DiscoverHome d)
    {
        var sections = new List<UIElement>();
        if (d.Hero.Count > 0) sections.Add(new DiscoverHero(d.Hero));
        if (d.ContinueWatching.Count > 0)
            sections.Add(Cards.Shelf("Continue Watching", d.ContinueWatching.Select(i => (UIElement)Cards.Wide(i))));
        if (d.Watchlist is { Count: > 0 } wl)
            sections.Add(Cards.Shelf("My Watchlist", wl.Select(i => (UIElement)Cards.Poster(i))));
        UIElement? recent = d.RecentlyWatched.Count > 0
            ? Cards.Shelf("Recently Watched", d.RecentlyWatched.Select(i => (UIElement)Cards.Poster(i)))
            : null;
        var rows = d.Rows.Where(r => r.Items.Count > 0).ToList();
        for (var n = 0; n < rows.Count; n++)
        {
            if (n == 1 && recent != null) sections.Add(recent);
            var row = rows[n];
            sections.Add(Cards.Shelf(row.Title, row.Items.Select((t, k) => (UIElement)Cards.Title(t, rank: row.Id == "trending" && k < 10 ? k + 1 : null))));
        }
        if (rows.Count < 2 && recent != null) sections.Add(recent);
        if (!d.Enabled)
            sections.Add(Ui.Empty("\uE7C5", "Discovery needs TMDB",
                App.State.IsAdmin ? "Add a TMDB API key under Server → Settings." : "The server owner hasn't set up TMDB yet."));
        else if (rows.Count == 0)
            sections.Add(Ui.Empty("\uE701", "Nothing to show", "TMDB didn't answer. Try again in a moment."));
        var scroll = Scroll(sections.ToArray());
        // The banner runs to the top edge.
        if (d.Hero.Count > 0) ((StackPanel)scroll.Content).Padding = new Thickness(0, 0, 0, 48);
        return scroll;
    }

    static UIElement LibraryHome(HomeResponse home)
    {
        var sections = new List<UIElement>();
        var featured = home.ContinueWatching.FirstOrDefault() ?? home.RecentlyAdded.SelectMany(s => s.Items).FirstOrDefault(i => i.BackdropPath != null);
        if (featured != null)
        {
            var target = featured;
            var heroTitle = target.Kind == "episode" ? target.ShowTitle ?? target.Title : target.Title;
            var meta = target.Kind == "episode" ? $"{target.EpisodeLabel} · {target.Title}" : Format.Meta(target);
            if (target.InProgress && target.Progress is { } p) meta += " · " + Format.TimeLeft(p);
            sections.Add(Hero.Make(heroTitle, target.Overview, target.BackdropPath ?? target.StillPath, meta,
            [
                Ui.Button(target.InProgress ? "Resume" : "Play", () => Nav.Play(target), "\uE768", accent: true),
                Ui.Button("More Info", () => Nav.Item(target.Kind == "episode" && target.ShowId is { } sid ? sid : target.Id), "\uE946"),
            ]));
        }
        if (home.ContinueWatching.Count > 0)
            sections.Add(Cards.Shelf("Continue Watching", home.ContinueWatching.Select(i => (UIElement)Cards.Wide(i))));
        if (home.Watchlist is { Count: > 0 } wl)
            sections.Add(Cards.Shelf("My Watchlist", wl.Select(i => (UIElement)Cards.Poster(i))));
        foreach (var shelf in home.RecentlyAdded.Where(s => s.Items.Count > 0))
        {
            var lib = shelf.Library;
            sections.Add(Cards.Shelf($"Recently Added in {lib.Name}",
                shelf.Items.Select(i => (UIElement)(i.Kind == "episode" ? Cards.Wide(i) : Cards.Poster(i))),
                () => App.Shell?.OpenLibrary(lib)));
        }
        if (home.TopRated is { Count: > 0 } top)
            sections.Add(Cards.Shelf("Top Rated", top.Select(i => (UIElement)Cards.Poster(i))));
        foreach (var c in home.Collections ?? [])
        {
            if (c.Items.Count == 0) continue;
            var col = c.Collection;
            sections.Add(Cards.Shelf(col.Name, c.Items.Select(i => (UIElement)Cards.Poster(i)),
                () => App.Shell?.Push(new CollectionPage(col.Id, col.Name)),
                string.IsNullOrEmpty(col.Description) ? null : col.Description));
        }
        foreach (var g in home.Genres ?? [])
        {
            if (g.Items.Count == 0) continue;
            var genre = g.Genre;
            sections.Add(Cards.Shelf(genre, g.Items.Select(i => (UIElement)Cards.Poster(i)), () => App.Shell?.Push(new GenrePage(genre, null))));
        }
        if (sections.Count == 0)
        {
            return Ui.Empty("\uE8F1", "Nothing here yet",
                App.State.IsAdmin ? "Add a library on the Server page, or in the server's web app, and it'll show up here." : "The server's libraries are empty. Check back once the owner adds something.");
        }
        return Scroll(sections.ToArray());
    }
}

/// The rotating banner of this week's trending titles, edge to edge.
/// Moves on every 8 seconds unless the pointer is on it.
sealed class DiscoverHero : Grid
{
    readonly List<TitleView> titles;
    readonly List<Image> images = [];
    readonly StackPanel text = Ui.Column(12);
    readonly DispatcherTimer timer = new() { Interval = TimeSpan.FromSeconds(8) };
    int index;
    bool hovering, opening;

    public DiscoverHero(List<TitleView> titles)
    {
        this.titles = titles;
        Height = 560;
        var api = App.State.Api;
        foreach (var t in titles)
        {
            var img = Ui.Image(api?.ImageUrl(t.BackdropPath, "w1280"), 1920);
            img.Opacity = 0;
            img.OpacityTransition = new ScalarTransition { Duration = TimeSpan.FromMilliseconds(800) };
            images.Add(img);
            Children.Add(img);
        }
        Children.Add(new Border
        {
            Background = new LinearGradientBrush
            {
                StartPoint = new Windows.Foundation.Point(0.5, 0),
                EndPoint = new Windows.Foundation.Point(0.5, 1),
                GradientStops =
                {
                    new GradientStop { Color = Windows.UI.Color.FromArgb(0, 0, 0, 0), Offset = 0.35 },
                    new GradientStop { Color = Windows.UI.Color.FromArgb(0x8C, 0, 0, 0), Offset = 0.75 },
                    new GradientStop { Color = Theme.Base, Offset = 1 },
                },
            },
        });
        Children.Add(new Border
        {
            Background = new LinearGradientBrush
            {
                StartPoint = new Windows.Foundation.Point(0, 0.5),
                EndPoint = new Windows.Foundation.Point(0.5, 0.5),
                GradientStops =
                {
                    new GradientStop { Color = Windows.UI.Color.FromArgb(0xA6, 0, 0, 0), Offset = 0 },
                    new GradientStop { Color = Windows.UI.Color.FromArgb(0, 0, 0, 0), Offset = 1 },
                },
            },
        });
        text.MaxWidth = 620;
        text.Margin = new Thickness(Page.Gutter, 0, Page.Gutter, 12);
        text.VerticalAlignment = VerticalAlignment.Bottom;
        text.HorizontalAlignment = HorizontalAlignment.Left;
        Children.Add(text);

        PointerEntered += (_, _) => hovering = true;
        PointerExited += (_, _) => hovering = false;
        timer.Tick += (_, _) =>
        {
            if (!hovering && !opening && titles.Count > 1) Show((index + 1) % titles.Count);
        };
        Loaded += (_, _) => timer.Start();
        Unloaded += (_, _) => timer.Stop();
        Show(0);
    }

    void Show(int i)
    {
        index = i;
        for (var k = 0; k < images.Count; k++) images[k].Opacity = k == i ? 1 : 0;
        var t = titles[i];
        var api = App.State.Api;
        text.Children.Clear();
        var kicker = Ui.Text("TRENDING THIS WEEK", 12, FontWeights.Bold, Theme.SecondaryText);
        kicker.CharacterSpacing = 80;
        text.Children.Add(kicker);
        if (t.Item?.LogoPath is { } logo)
        {
            var img = Ui.Image(api?.ImageUrl(logo, "w500"), 800, Stretch.Uniform);
            img.MaxWidth = 400;
            img.MaxHeight = 130;
            img.HorizontalAlignment = HorizontalAlignment.Left;
            text.Children.Add(img);
        }
        else text.Children.Add(Ui.Text(t.Title, 44, FontWeights.Bold, maxLines: 2));
        var meta = new List<string>();
        if (t.Year is { } y) meta.Add(y.ToString());
        meta.Add(t.MediaType == "show" ? "Series" : "Film");
        if (t.Rating is > 0 and var r) meta.Add($"★ {r:0.0}");
        text.Children.Add(Ui.Secondary(string.Join(" · ", meta), 14));
        if (!string.IsNullOrEmpty(t.Overview)) text.Children.Add(Ui.Text(t.Overview, 14, maxLines: 2, wrap: true));

        var row = Ui.Row(10);
        row.Margin = new Thickness(0, 4, 0, 0);
        if (t.MediaType == "movie")
        {
            row.Children.Add(Ui.Button(t.Item?.InProgress == true ? "Resume" : "Play", () => PlayTitle(t), "\uE768", accent: true));
            row.Children.Add(Ui.Button("Details", () => Nav.Title(t), "\uE946"));
        }
        else row.Children.Add(Ui.Button("Watch Now", () => Nav.Title(t), "\uE768", accent: true));
        text.Children.Add(row);

        if (titles.Count > 1)
        {
            var dots = Ui.Row(6);
            dots.Margin = new Thickness(0, 6, 0, 0);
            for (var k = 0; k < titles.Count; k++)
            {
                var n = k;
                var dot = new Button
                {
                    Width = n == i ? 22 : 8, Height = 8, MinWidth = 0, MinHeight = 0, Padding = new Thickness(0),
                    CornerRadius = new CornerRadius(4), BorderThickness = new Thickness(0),
                    Background = n == i ? new SolidColorBrush(Theme.Cream) : new SolidColorBrush(Windows.UI.Color.FromArgb(0x60, 0xFF, 0xFF, 0xFF)),
                };
                ToolTipService.SetToolTip(dot, titles[n].Title);
                dot.Click += (_, _) => Show(n);
                dots.Children.Add(dot);
            }
            text.Children.Add(dots);
        }
    }

    /// Adds the movie to the streaming library if needed, then plays it.
    async void PlayTitle(TitleView t)
    {
        if (t.Item is { } item) { Nav.Play(item); return; }
        if (App.State.Api is not { } api || opening) return;
        opening = true;
        try { Nav.Play(await api.AddStreamTitle(t.TmdbId, t.MediaType)); }
        catch (Exception e) { App.Shell?.ShowError(e.Message); }
        finally { opening = false; }
    }
}

/// Movies or TV Shows when the server streams: TMDB's whole catalog, loading
/// more as you scroll. Titles the server has open their item; the rest stream.
sealed class BrowsePage(string type) : Page
{
    static readonly (string Key, string Name)[] Sorts = [("popular", "Popular"), ("top_rated", "Top Rated"), ("new", "Newest")];

    protected override bool HasAmbient => true;
    public override bool ReloadsOnRefresh => false;

    string sort = Store.GetString("catalogSort") ?? "popular";
    int? genre;
    List<CatalogResult.Genre> genres = [];
    readonly System.Collections.ObjectModel.ObservableCollection<TitleView> titles = [];
    readonly HashSet<string> seen = [];
    int page, totalPages = 1, generation;
    bool loading;

    string Label => type == "show" ? "TV Shows" : "Movies";

    protected override async Task<UIElement> Build()
    {
        var gen = ++generation;
        titles.Clear();
        seen.Clear();
        page = 0;
        totalPages = 1;
        loading = false;
        var first = await App.State.Api!.Catalog(type, sort, genre, 1);
        if (gen != generation) return new Grid();
        Add(first);

        var g = new Grid { RowSpacing = 16, Padding = new Thickness(0, 24, 0, 0) };
        g.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        g.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        var header = new Grid { Margin = new Thickness(Gutter, 0, Gutter, 0) };
        header.Children.Add(PageTitle(Label, genres.FirstOrDefault(x => x.Id == genre)?.Name));
        ((FrameworkElement)header.Children[0]).Margin = new Thickness(0);
        var tools = Ui.Row(10);
        tools.HorizontalAlignment = HorizontalAlignment.Right;
        tools.VerticalAlignment = VerticalAlignment.Center;
        tools.Children.Add(GenreMenu());
        tools.Children.Add(SortMenu());
        header.Children.Add(tools);
        g.Children.Add(header);

        UIElement content;
        if (!first.Enabled)
            content = Ui.Empty("\uE7C5", $"{Label} need TMDB",
                App.State.IsAdmin ? "Add a TMDB API key under Server → Settings." : "The server owner hasn't set up TMDB yet.");
        else if (titles.Count == 0)
            content = Ui.Empty("\uE8B2", "Nothing matches", "Try a different genre.");
        else
            content = Cards.TitleGrid(titles, () => _ = LoadMore(gen));
        Grid.SetRow((FrameworkElement)content, 1);
        g.Children.Add(content);
        return g;
    }

    void Add(CatalogResult r)
    {
        if (r.Genres.Count > 0) genres = r.Genres;
        foreach (var t in r.Items)
            if (seen.Add($"{t.MediaType}:{t.TmdbId}")) titles.Add(t);
        page = r.Page;
        totalPages = r.TotalPages;
    }

    async Task LoadMore(int gen)
    {
        if (loading || page >= totalPages || App.State.Api is not { } api) return;
        loading = true;
        try
        {
            var r = await api.Catalog(type, sort, genre, page + 1);
            if (gen == generation) Add(r);
        }
        catch (Exception e) { if (gen == generation) App.Shell?.ShowError(e.Message); }
        finally { if (gen == generation) loading = false; }
    }

    DropDownButton GenreMenu()
    {
        var menu = new MenuFlyout();
        void Option(int? id, string name)
        {
            var m = new ToggleMenuFlyoutItem { Text = name, IsChecked = id == genre };
            m.Click += (_, _) => { genre = id; Reload(quiet: true); };
            menu.Items.Add(m);
        }
        Option(null, "All genres");
        menu.Items.Add(new MenuFlyoutSeparator());
        foreach (var x in genres) Option(x.Id, x.Name);
        return new DropDownButton
        {
            Content = Ui.Label("\uE8EC", genres.FirstOrDefault(x => x.Id == genre)?.Name ?? "All genres"),
            Flyout = menu,
            CornerRadius = new CornerRadius(8),
        };
    }

    DropDownButton SortMenu()
    {
        var menu = new MenuFlyout();
        foreach (var (key, name) in Sorts)
        {
            var m = new ToggleMenuFlyoutItem { Text = name, IsChecked = key == sort };
            m.Click += (_, _) =>
            {
                sort = key;
                Store.Set("catalogSort", key);
                Reload(quiet: true);
            };
            menu.Items.Add(m);
        }
        return new DropDownButton
        {
            Content = Ui.Label("\uE8CB", Sorts.FirstOrDefault(s => s.Key == sort).Name ?? "Popular"),
            Flyout = menu,
            CornerRadius = new CornerRadius(8),
        };
    }
}

/// Movies and shows saved for later, newest added first.
sealed class WatchlistPage : Page
{
    bool hideWatched = true;
    List<Item> items = [];

    List<Item> Shown => hideWatched ? items.Where(i => !i.IsWatched).ToList() : items;

    protected override async Task<UIElement> Build()
    {
        items = await App.State.Api!.Watchlist();
        var g = new Grid { RowSpacing = 20, Padding = new Thickness(0, 24, 0, 0) };
        g.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        g.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        var shown = Shown;
        var header = new Grid { Margin = new Thickness(Gutter, 0, Gutter, 0) };
        var title = PageTitle("Watchlist", shown.Count == 1 ? "1 title" : $"{shown.Count} titles");
        title.Margin = new Thickness(0);
        header.Children.Add(title);
        var toggle = new ToggleButton
        {
            Content = Ui.Label("\uED1A", "Hide Watched"),
            IsChecked = hideWatched,
            CornerRadius = new CornerRadius(8),
            HorizontalAlignment = HorizontalAlignment.Right,
            VerticalAlignment = VerticalAlignment.Center,
        };
        ToolTipService.SetToolTip(toggle, "Hide what you've watched");
        toggle.Click += (_, _) => { hideWatched = toggle.IsChecked == true; Reload(quiet: true); };
        header.Children.Add(toggle);
        g.Children.Add(header);
        UIElement content = shown.Count == 0
            ? Ui.Empty("\uE728", "Your watchlist is empty", "Add movies and shows from their pages to find them here.")
            : Cards.PosterGrid(shown);
        Grid.SetRow((FrameworkElement)content, 1);
        g.Children.Add(content);
        return g;
    }
}

/// Every collection on the server, a shelf per kind: the server's own,
/// franchises, lists, charts and smart collections.
sealed class CollectionsPage : Page
{
    protected override bool HasAmbient => true;

    protected override async Task<UIElement> Build()
    {
        var cols = await App.State.Api!.Collections();
        if (cols.Count == 0) return Titled("Collections", "", Ui.Empty("\uE8F1", "No collections", "Collections from TMDB and ones you make show up here."));
        // The server's order already puts the groups in theirs.
        var shelves = cols.GroupBy(c => c.Group ?? "Collections").Select(g => Cards.Shelf(g.Key, g.Select(Tile),
            subtitle: g.Count() == 1 ? "1 collection" : $"{g.Count()} collections"));
        return Scroll("Collections", shelves.ToArray());
    }

    /// A collection's wide tile: its artwork with the name over it.
    internal static UIElement Tile(WrenCollection c) =>
        Cards.Tile(c.Name, c.ItemCount is { } n ? $"{n} titles" : "", c.BackdropPath ?? c.PosterPath,
            () => App.Shell?.Push(new CollectionPage(c.Id, c.Name)), margin: 0);

    internal static UIElement Titled(string title, string subtitle, UIElement content)
    {
        var g = new Grid { RowSpacing = 20, Padding = new Thickness(0, 24, 0, 0) };
        g.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        g.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        g.Children.Add(PageTitle(title, subtitle));
        Grid.SetRow((FrameworkElement)content, 1);
        g.Children.Add(content);
        return g;
    }
}

/// When episodes air: a shelf a day from a week back to a month ahead, of the
/// library's shows that are still running and the ones being watched.
sealed class CalendarPage : Page
{
    protected override bool HasAmbient => true;
    bool watchingOnly;

    protected override async Task<UIElement> Build()
    {
        var cal = await App.State.Api!.Calendar();
        var eps = cal.Episodes.Where(e => !watchingOnly || e.Watching).ToList();
        var today = DateTime.Today.ToString("yyyy-MM-dd");
        var toggle = new ToggleButton { Content = "Shows I'm Watching", IsChecked = watchingOnly, Margin = new Thickness(Gutter, 0, Gutter, 0) };
        toggle.Click += (_, _) => { watchingOnly = toggle.IsChecked == true; Reload(quiet: true); };
        var sections = new List<UIElement> { toggle };
        UIElement? next = null;
        foreach (var day in eps.GroupBy(e => e.Date))
        {
            var shelf = Cards.Shelf(DayName(day.Key), day.Select(e => (UIElement)Card(e)),
                subtitle: day.Count() == 1 ? "1 episode" : $"{day.Count()} episodes");
            if (next == null && string.CompareOrdinal(day.Key, today) >= 0) next = shelf;
            sections.Add(shelf);
        }
        if (eps.Count == 0)
            sections.Add(Ui.Empty("\uE787", watchingOnly ? "Nothing coming up for your shows" : "Nothing airing",
                "Episodes from a week back to a month ahead show here, for shows in the library that are still running."));
        var page = Scroll("Calendar", sections.ToArray());
        // Open on what's next to air, with what just aired above it.
        if (next is FrameworkElement target && sections.IndexOf(target) > 1)
            page.Loaded += (_, _) => target.StartBringIntoView(new BringIntoViewOptions { VerticalAlignmentRatio = 0, AnimationDesired = false });
        return page;
    }

    /// "Today", "Tomorrow", "Yesterday", or the weekday and date.
    static string DayName(string date)
    {
        if (!DateTime.TryParse(date, out var d)) return date;
        return (d.Date - DateTime.Today).Days switch
        {
            0 => "Today",
            1 => "Tomorrow",
            -1 => "Yesterday",
            _ => d.ToString("dddd, MMM d"),
        };
    }

    static Button Card(CalendarEpisode e)
    {
        var flag = e.UpNext ? "Up next" : e.Watched ? "Watched" : null;
        var b = Cards.Tile(e.Show.Title, $"{e.Code} · {e.Name}", e.StillPath ?? e.Show.BackdropPath,
            () => Nav.Item(e.ItemId ?? e.Show.Id), margin: 0, flag: flag, accent: e.UpNext);
        b.PointerEntered += (_, _) => Ambient.Hover(e.Show.BackdropPath);
        if (!string.IsNullOrEmpty(e.Overview)) ToolTipService.SetToolTip(b, e.Overview);
        return b;
    }
}

/// Every genre across the libraries.
sealed class GenresPage : Page
{
    protected override bool HasAmbient => true;

    protected override async Task<UIElement> Build()
    {
        var genres = await App.State.Api!.Genres();
        return CollectionsPage.Titled("Genres", genres.Count == 1 ? "1 genre" : $"{genres.Count} genres", genres.Count == 0
            ? Ui.Empty("\uE8EC", "No genres", "Genres appear once titles are matched.")
            : Cards.Tiles(genres.Select(x => Cards.Tile(x.Genre, $"{x.Count} titles", x.BackdropPath,
                () => App.Shell?.Push(new GenrePage(x.Genre, null))))));
    }
}

sealed class SearchPage(string query) : Page
{
    public override bool ReloadsOnRefresh => false;

    protected override async Task<UIElement> Build()
    {
        var api = App.State.Api!;
        var local = api.Search(query);
        Task<DiscoverSearch>? remote = App.State.CanStream ? api.DiscoverSearch(query) : null;
        var colls = api.SearchCollections(query);
        var items = await local;
        var sections = new List<UIElement>();
        // Collections whose names match, a franchise's among them.
        try
        {
            var cs = await colls;
            if (cs.Count > 0) sections.Add(Cards.Shelf("Collections", cs.Select(CollectionsPage.Tile)));
        }
        catch { }
        var movies = items.Where(i => i.Kind == "movie").ToList();
        var shows = items.Where(i => i.Kind == "show").ToList();
        var episodes = items.Where(i => i.Kind == "episode").ToList();
        if (movies.Count > 0) sections.Add(Cards.Shelf("Movies", movies.Select(i => (UIElement)Cards.Poster(i))));
        if (shows.Count > 0) sections.Add(Cards.Shelf("Shows", shows.Select(i => (UIElement)Cards.Poster(i))));
        if (episodes.Count > 0) sections.Add(Cards.Shelf("Episodes", episodes.Select(i => (UIElement)Cards.Wide(i))));
        if (remote != null)
        {
            try
            {
                var r = await remote;
                var have = items.Where(i => i.TmdbId != null).Select(i => i.TmdbId).ToHashSet();
                var more = r.Items.Where(t => t.Item == null || !have.Contains(t.TmdbId)).ToList();
                if (more.Count > 0) sections.Add(Cards.Shelf("Stream", more.Select(t => (UIElement)Cards.Title(t)), subtitle: "Titles you can add and watch"));
            }
            catch { }
        }
        if (sections.Count == 0) return Ui.Empty("", "No results", $"Nothing matched “{query}”.");
        return Scroll($"Results for “{query}”", sections.ToArray());
    }
}

sealed class CollectionPage(string id, string name) : Page
{
    protected override bool HasAmbient => true;

    /// A big collection shows as rows, like Home; this shows it as one grid instead.
    bool asGrid;

    protected override async Task<UIElement> Build()
    {
        var c = await App.State.Api!.Collection(id);
        // Lists can name a title twice; it's shown once.
        c.Items = c.Items.DistinctBy(i => i.Id).ToList();
        if (c.Collection.BackdropPath != null) Backdrop?.Show(c.Collection.BackdropPath);
        var rows = asGrid ? null : c.Rows is { Count: > 0 } r ? r : null;

        var head = Ui.Column(6);
        var kicker = Ui.Text("COLLECTION", 12, FontWeights.Bold, Theme.EmberBrush);
        kicker.CharacterSpacing = 80;
        head.Children.Add(kicker);
        head.Children.Add(Ui.Title(c.Collection.Name.Length > 0 ? c.Collection.Name : name, 40));
        if (!string.IsNullOrEmpty(c.Collection.Description)) head.Children.Add(Ui.Secondary(c.Collection.Description, 15, 3));
        var count = c.Titles is { } ts ? $"{ts.DistinctBy(t => t.MediaType + t.TmdbId).Count()} titles" : $"{c.Items.Count} in your library";
        head.Children.Add(Ui.Secondary(count, 14));
        if (c.Rows is { Count: > 0 })
        {
            var toggle = Ui.Button(asGrid ? "Show as Rows" : "Show All in Order", () => { asGrid = !asGrid; Reload(quiet: true); }, asGrid ? "" : "");
            toggle.HorizontalAlignment = HorizontalAlignment.Left;
            toggle.Margin = new Thickness(0, 8, 0, 0);
            head.Children.Add(toggle);
        }
        head.MaxWidth = 640;
        head.HorizontalAlignment = HorizontalAlignment.Left;
        head.Margin = new Thickness(Gutter, 120, Gutter, 0);

        if (rows != null)
        {
            var sections = new List<UIElement> { head };
            foreach (var row in rows)
            {
                var cards = c.Titles is { } titles
                    ? row.Index.Where(i => i >= 0 && i < titles.Count).Select(i => titles[i]).DistinctBy(t => t.MediaType + t.TmdbId)
                        .Select((t, k) => (UIElement)Cards.Title(t, rank: row.Title == "Top 20" && k < 10 ? k + 1 : null)).ToList()
                    : row.Index.Where(i => i >= 0 && i < c.Items.Count).Select(i => c.Items[i]).DistinctBy(i => i.Id)
                        .Select(i => (UIElement)Cards.Poster(i)).ToList();
                if (cards.Count > 0) sections.Add(Cards.Shelf(row.Title, cards));
            }
            return Scroll(sections.ToArray());
        }

        var missing = (c.Missing ?? c.Titles?.Where(t => t.Item == null).ToList() ?? []).DistinctBy(t => t.MediaType + t.TmdbId).ToList();
        if (missing.Count > 0)
            return Scroll(head,
                Cards.Shelf("In Your Library", c.Items.Select(i => (UIElement)Cards.Poster(i))),
                Cards.Shelf(App.State.CanStream ? "Stream the Rest" : "Not in Your Library", missing.Select(t => (UIElement)Cards.Title(t))));
        var g = new Grid { RowSpacing = 20 };
        g.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        g.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        g.Children.Add(head);
        var list = Cards.PosterGrid(c.Items);
        Grid.SetRow(list, 1);
        g.Children.Add(list);
        return g;
    }
}

sealed class GenrePage(string genre, string? library) : Page
{
    protected override async Task<UIElement> Build()
    {
        var page = await App.State.Api!.GenreItems(genre, library);
        var g = new Grid { RowSpacing = 20, Padding = new Thickness(0, 24, 0, 0) };
        g.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        g.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        g.Children.Add(PageTitle(genre, page.Total == 1 ? "1 title" : $"{page.Total} titles"));
        var grid = Cards.PosterGrid(page.Items);
        Grid.SetRow(grid, 1);
        g.Children.Add(grid);
        return g;
    }
}
