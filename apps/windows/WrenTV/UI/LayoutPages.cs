using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Windows.UI;
using WrenTV.Core;

namespace WrenTV.UI;

/*
 * Home, Movies, TV Shows, libraries and hubs are built from the server's
 * layout rows, the same rows every Wren TV shows, under a banner of the
 * page's first titles.
 */

/// A page made of the server's layout rows. Servers from before layouts
/// answer 404 and get fallback instead.
sealed class RowsPage(string scope, string title, Func<Page> fallback, Func<Page>? browseAll = null) : Page
{
    protected override bool HasAmbient => true;

    /// Rows per request; the rest load as the page scrolls.
    const int PageRows = 6;
    /// How many rows were showing, so a refresh keeps them and the page doesn't jump.
    int shown;

    protected override async Task<UIElement> Build()
    {
        var api = App.State.Api!;
        LayoutResponse layout;
        try { layout = await api.Layout(scope, limit: Math.Max(PageRows, shown)); }
        catch (WrenTVException e) when (e.Status == 404) { return fallback(); }
        shown = layout.Rows.Count;

        var sections = new List<UIElement>();
        var hero = LayoutHero.Make(layout.Rows);
        if (hero != null) sections.Add(hero);
        else sections.Add(PageTitle(title));
        sections.AddRange(LayoutRows.Make(layout.Rows));
        if (layout.Rows.Count == 0)
            sections.Add(Ui.Empty("", "Nothing here yet",
                App.State.IsAdmin ? "Add a library on the Server page, and it'll show up here." : "Ask the server owner to share a library."));
        var scroll = Scroll(sections.ToArray());
        var col = (StackPanel)scroll.Content;
        if (hero != null) col.Padding = new Thickness(0, 0, 0, 48);

        // The next rows load as the last ones scroll into view.
        var next = layout.NextOffset;
        var have = layout.Rows.Select(r => r.Id).ToHashSet();
        var spinner = new ProgressRing { IsActive = true, Width = 28, Height = 28, HorizontalAlignment = HorizontalAlignment.Center };
        if (next != null) col.Children.Add(spinner);
        var loading = false;
        async void More()
        {
            if (loading || next is not { } offset || scroll.VerticalOffset < scroll.ScrollableHeight - 800) return;
            loading = true;
            try
            {
                var more = await api.Layout(scope, offset, PageRows);
                var fresh = more.Rows.Where(r => have.Add(r.Id)).ToList();
                var at = col.Children.IndexOf(spinner);
                foreach (var e in LayoutRows.Make(fresh)) col.Children.Insert(at++, e);
                shown += fresh.Count;
                next = more.NextOffset;
                if (next == null) col.Children.Remove(spinner);
            }
            catch { next = null; col.Children.Remove(spinner); }
            finally { loading = false; }
            More();
        }
        scroll.ViewChanged += (_, _) => More();
        scroll.Loaded += (_, _) => More();

        // Browse All and Customize, top right over the banner.
        var tools = Ui.Row(8);
        tools.HorizontalAlignment = HorizontalAlignment.Right;
        tools.VerticalAlignment = VerticalAlignment.Top;
        tools.Margin = new Thickness(0, 16, Gutter, 0);
        if (browseAll != null) tools.Children.Add(Ui.Button("Browse All", () => App.Shell?.Push(browseAll()), ""));
        tools.Children.Add(Ui.Button("Customize", () => App.Shell?.Push(new LayoutEditorPage(scope, title)), ""));
        var g = new Grid();
        g.Children.Add(scroll);
        g.Children.Add(tools);
        return g;
    }
}

/// A hub: a genre, a streaming service or an admin's own, as shelves.
sealed class HubPage(string id, string name) : Page
{
    protected override bool HasAmbient => true;

    protected override async Task<UIElement> Build()
    {
        var page = await App.State.Api!.Hub(id);
        var hub = page.Hub;
        if (hub.BackdropPath != null) Backdrop?.Show(hub.BackdropPath);
        var head = Ui.Column(6);
        var kicker = Ui.Text(hub.KindLabel.ToUpperInvariant(), 12, FontWeights.Bold, Theme.EmberBrush);
        kicker.CharacterSpacing = 80;
        head.Children.Add(kicker);
        head.Children.Add(Ui.Title(hub.Name.Length > 0 ? hub.Name : name, 40));
        if (!string.IsNullOrEmpty(hub.Tagline)) head.Children.Add(Ui.Secondary(hub.Tagline, 15, 3));
        head.MaxWidth = 640;
        head.HorizontalAlignment = HorizontalAlignment.Left;
        head.Margin = new Thickness(Gutter, 120, Gutter, 0);
        var sections = new List<UIElement> { head };
        sections.AddRange(LayoutRows.Make(page.Rows));
        if (page.Rows.Count == 0) sections.Add(Ui.Empty("", "Nothing here yet", "None of this hub's shelves found any titles."));
        return Scroll(sections.ToArray());
    }
}

/// An actor or crew member over their best-known title's artwork: photo,
/// details and biography, then rows of what they're in with the role under
/// each title. Clicking a title opens its page.
sealed class PersonPage(int id, string name) : Page
{
    protected override bool HasAmbient => true;
    bool bioOpen;

    protected override async Task<UIElement> Build()
    {
        var p = await App.State.Api!.Person(id);
        if (p.BackdropPath != null) Backdrop?.Show(p.BackdropPath);
        var head = Ui.Column(8);
        if (!string.IsNullOrEmpty(p.KnownFor))
        {
            var kicker = Ui.Text(p.KnownFor.ToUpperInvariant(), 12, FontWeights.Bold, Theme.EmberBrush);
            kicker.CharacterSpacing = 80;
            head.Children.Add(kicker);
        }
        head.Children.Add(Ui.Title(p.Name.Length > 0 ? p.Name : name, 40));
        if (p.Facts.Count > 0) head.Children.Add(Ui.Secondary(string.Join("  ·  ", p.Facts), 14, 2));
        if (!string.IsNullOrWhiteSpace(p.Biography))
        {
            var bio = Ui.Text(p.Biography, 14, maxLines: bioOpen ? 0 : 6, wrap: true);
            head.Children.Add(bio);
            var more = new HyperlinkButton { Content = bioOpen ? "Less" : "More", Padding = new Thickness(0) };
            more.Click += (_, _) => { bioOpen = !bioOpen; bio.MaxLines = bioOpen ? 0 : 6; more.Content = bioOpen ? "Less" : "More"; };
            var links = Ui.Row(16, more);
            if (p.ImdbId is { } imdb) links.Children.Add(new HyperlinkButton { Content = "IMDb ↗", NavigateUri = new Uri($"https://www.imdb.com/name/{imdb}/"), Padding = new Thickness(0) });
            links.Children.Add(new HyperlinkButton { Content = "TMDB ↗", NavigateUri = new Uri($"https://www.themoviedb.org/person/{id}"), Padding = new Thickness(0) });
            head.Children.Add(links);
        }
        var photo = new Border
        {
            Width = 180, Height = 270, CornerRadius = new CornerRadius(12),
            Background = new SolidColorBrush(Theme.Surface),
            Child = p.ProfilePath != null ? Ui.Image(App.State.Api?.ImageUrl(p.ProfilePath, "w500"), 360) : Ui.Icon("\uE77B", 48, Theme.TertiaryText),
            VerticalAlignment = VerticalAlignment.Top,
        };
        var top = new Grid { ColumnSpacing = 28, Margin = new Thickness(Gutter, 100, Gutter, 0), MaxWidth = 1100, HorizontalAlignment = HorizontalAlignment.Left };
        top.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        top.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        top.Children.Add(photo);
        Grid.SetColumn(head, 1);
        top.Children.Add(head);
        var sections = new List<UIElement> { top };
        foreach (var r in p.Rows)
            sections.Add(Cards.Shelf(r.Title, r.Credits.DistinctBy(t => t.MediaType + t.TmdbId).Select(t => (UIElement)Cards.Title(t)), subtitle: r.Credits.Count.ToString()));
        if (p.Rows.Count == 0) sections.Add(Ui.Empty("\uE8B2", "No movies or shows listed", "TMDB doesn't list any titles for them yet."));
        return Scroll(sections.ToArray());
    }
}

/// A studio over its artwork: popular, top rated and recent movies, then
/// popular and top rated shows. Each shelf loads more as it nears its end.
sealed class StudioPage(int id, string name) : Page
{
    protected override bool HasAmbient => true;
    const int Ahead = 12;

    /// A studio's logo on a light tile, or its name where there's no logo.
    public static UIElement Logo(Studio s, double width)
    {
        FrameworkElement inner = s.LogoPath != null
            ? Ui.Image(App.State.Api?.ImageUrl(s.LogoPath, "w300"), 300, Stretch.Uniform)
            : new TextBlock { Text = s.Name, FontSize = 15, FontWeight = FontWeights.SemiBold, Foreground = new SolidColorBrush(Color.FromArgb(255, 0x1C, 0x1C, 0x1E)), TextWrapping = TextWrapping.Wrap, TextAlignment = TextAlignment.Center, VerticalAlignment = VerticalAlignment.Center, Margin = new Thickness(10) };
        if (s.LogoPath != null) inner.Margin = new Thickness(width * 0.14);
        return new Border
        {
            Width = width, Height = width / 2, CornerRadius = new CornerRadius(10),
            Background = new SolidColorBrush(Color.FromArgb(255, 0xF2, 0xF2, 0xF4)),
            Child = inner,
        };
    }

    protected override async Task<UIElement> Build()
    {
        var c = await App.State.Api!.Company(id);
        if (c.BackdropPath != null) Backdrop?.Show(c.BackdropPath);
        var head = Ui.Column(8);
        var kicker = Ui.Text("STUDIO", 12, FontWeights.Bold, Theme.EmberBrush);
        kicker.CharacterSpacing = 80;
        head.Children.Add(kicker);
        head.Children.Add(Ui.Title(c.Name.Length > 0 ? c.Name : name, 40));
        var facts = new[] { c.Headquarters, c.OriginCountry }.Where(f => !string.IsNullOrWhiteSpace(f)).ToList();
        if (facts.Count > 0) head.Children.Add(Ui.Secondary(string.Join("  ·  ", facts), 14, 2));
        if (!string.IsNullOrWhiteSpace(c.Description)) head.Children.Add(Ui.Text(c.Description, 14, wrap: true));
        var logo = Logo(new Studio { Id = id, Name = c.Name, LogoPath = c.LogoPath }, 240);
        ((FrameworkElement)logo).VerticalAlignment = VerticalAlignment.Top;
        var top = new Grid { ColumnSpacing = 28, Margin = new Thickness(Gutter, 100, Gutter, 0), MaxWidth = 1100, HorizontalAlignment = HorizontalAlignment.Left };
        top.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        top.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        top.Children.Add(logo);
        Grid.SetColumn(head, 1);
        top.Children.Add(head);
        var sections = new List<UIElement> { top };
        foreach (var r in c.Rows) sections.Add(Shelf(r));
        if (c.Rows.Count == 0) sections.Add(Ui.Empty("\uE8B2", "No movies or shows listed", "TMDB doesn't list any titles for this studio yet."));
        return Scroll(sections.ToArray());
    }

    /// One shelf, fetching its next page once it's scrolled within a few cards of the end.
    UIElement Shelf(StudioRow r)
    {
        var seen = new HashSet<string>();
        var items = r.Items.Where(t => seen.Add(t.MediaType + t.TmdbId)).ToList();
        int page = 1, total = r.TotalPages;
        var loading = false;
        StackPanel? row = null;
        ScrollViewer? sv = null;
        async void Near()
        {
            if (loading || page >= total || row == null || sv == null) return;
            var per = row.Children.Count == 0 ? 1 : (sv.ExtentWidth / row.Children.Count);
            var lastVisible = (sv.HorizontalOffset + sv.ViewportWidth) / Math.Max(1, per);
            if (lastVisible < row.Children.Count - Ahead) return;
            loading = true;
            try
            {
                var res = await App.State.Api!.Catalog(r.MediaType, r.Sort, null, page + 1, id);
                foreach (var t in res.Items.Where(t => seen.Add(t.MediaType + t.TmdbId))) row.Children.Add(Cards.Title(t));
                page = res.Page;
                total = res.TotalPages;
            }
            catch (Exception)
            {
                // Tried again when the row next moves.
            }
            finally
            {
                loading = false;
            }
        }
        return Cards.Shelf(r.Title, items.Select(t => (UIElement)Cards.Title(t)), attach: (rw, s) =>
        {
            row = rw;
            sv = s;
            s.ViewChanged += (_, _) => Near();
            s.SizeChanged += (_, _) => Near();
        });
    }
}

/// The page's banner: its first titles with artwork.
static class LayoutHero
{
    public static UIElement? Make(List<LayoutRow> rows)
    {
        var titles = rows.FirstOrDefault(r => r.Kind == "titles" && r.Titles is { Count: > 0 })?.Titles ?? [];
        var picks = titles.DistinctBy(t => t.MediaType + t.TmdbId).Where(t => t.BackdropPath != null).Take(6).ToList();
        if (picks.Count > 0) return new DiscoverHero(picks);

        // Servers that don't stream: library titles with artwork.
        var item = rows.Where(r => r.Kind is "items" or "collection").SelectMany(r => r.Items ?? [])
            .FirstOrDefault(i => i.BackdropPath != null && i.Kind != "episode");
        if (item == null) return null;
        var hero = Hero.Make(item.Title, item.Overview, item.BackdropPath, Format.Meta(item),
        [
            Ui.Button(item.InProgress ? "Resume" : "Play", () => Nav.Play(item), "", accent: true),
            Ui.Button("More Info", () => Nav.Item(item), ""),
        ]);
        ((FrameworkElement)hero).Margin = new Thickness(Page.Gutter, 24, Page.Gutter, 0);
        return hero;
    }
}

/// The server's rows, each drawn the way its kind asks.
static class LayoutRows
{
    public static IEnumerable<UIElement> Make(List<LayoutRow> rows)
    {
        foreach (var row in rows)
        {
            var cards = new List<UIElement>();
            switch (row.Kind)
            {
                case "landscape":
                    cards.AddRange((row.Items ?? []).DistinctBy(i => i.Id).Select(i => (UIElement)Cards.Wide(i)));
                    break;
                case "hubs":
                    cards.AddRange((row.Hubs ?? []).DistinctBy(h => h.Id).Select(h => HubTile(h, row.Style == "logos")));
                    break;
                case "titles":
                    cards.AddRange((row.Titles ?? []).DistinctBy(t => t.MediaType + t.TmdbId)
                        .Select((t, k) => (UIElement)Cards.Title(t, rank: row.Ranked && k < 10 ? k + 1 : null)));
                    break;
                default:
                    cards.AddRange((row.Items ?? []).DistinctBy(i => i.Id).Select(i => (UIElement)Cards.Poster(i)));
                    cards.AddRange((row.Missing ?? []).DistinctBy(t => t.MediaType + t.TmdbId).Select(t => (UIElement)Cards.Title(t)));
                    break;
            }
            if (cards.Count == 0) continue;
            // A collection's or hub's row leads to the rest of it.
            Action? all = null;
            if (row.Collection is { } c)
            {
                // The header's "See All" opens it; the card only when the shelf doesn't show everything.
                all = () => App.Shell?.Push(new CollectionPage(c.Id, c.Name));
                if (row.More) cards.Add(MoreCard("View All", all));
            }
            else if (row.HubId is { } hub)
            {
                var title = row.Title;
                all = () => App.Shell?.Push(new HubPage(hub, title));
                cards.Add(MoreCard($"Open {title}", all));
            }
            yield return Cards.Shelf(row.Title, cards, all, row.Subtitle);
        }
    }

    static UIElement MoreCard(string label, Action open)
    {
        var inner = Ui.Column(10, Ui.Icon("", 22), Ui.Text(label, 14, maxLines: 2, wrap: true));
        inner.HorizontalAlignment = HorizontalAlignment.Center;
        inner.VerticalAlignment = VerticalAlignment.Center;
        foreach (var c in inner.Children) ((FrameworkElement)c).HorizontalAlignment = HorizontalAlignment.Center;
        ((TextBlock)inner.Children[1]).TextAlignment = TextAlignment.Center;
        var b = new Button
        {
            Content = inner,
            Width = Cards.PosterWidth, Height = Cards.PosterWidth * 1.5,
            VerticalAlignment = VerticalAlignment.Top,
            Background = Theme.CardBrush,
            BorderBrush = Theme.CardBorder,
            CornerRadius = new CornerRadius(10),
        };
        b.Click += (_, _) => open();
        return b;
    }

    /// A streaming service's tile face: its icon, ringed so it stands off any
    /// colour, over its name on a dark wash of its colour. The same for every
    /// service, on every app.
    static UIElement ServiceFace(string name, Color? tint, Uri? logo, double height)
    {
        var ink = Color.FromArgb(255, 0x0C, 0x0B, 0x10);
        var g = new Grid { Background = new SolidColorBrush(tint ?? Color.FromArgb(255, 0x3A, 0x36, 0x40)) };
        g.Children.Add(new Border
        {
            Background = new LinearGradientBrush
            {
                StartPoint = new Windows.Foundation.Point(0, 0),
                EndPoint = new Windows.Foundation.Point(1, 1),
                GradientStops =
                {
                    new GradientStop { Color = Color.FromArgb(0x73, ink.R, ink.G, ink.B), Offset = 0 },
                    new GradientStop { Color = Color.FromArgb(0xD6, ink.R, ink.G, ink.B), Offset = 1 },
                },
            },
        });
        g.Children.Add(new Border
        {
            Background = new LinearGradientBrush
            {
                StartPoint = new Windows.Foundation.Point(0, 0),
                EndPoint = new Windows.Foundation.Point(0, 1),
                GradientStops =
                {
                    new GradientStop { Color = Color.FromArgb(0x14, 255, 255, 255), Offset = 0 },
                    new GradientStop { Color = Color.FromArgb(0, 255, 255, 255), Offset = 0.6 },
                },
            },
        });
        var side = height * 0.46;
        var icon = new Border
        {
            Width = side,
            Height = side,
            CornerRadius = new CornerRadius(side * 0.22),
            BorderBrush = new SolidColorBrush(Color.FromArgb(0x38, 255, 255, 255)),
            BorderThickness = new Thickness(1),
            Child = Ui.Image(logo, 300),
        };
        var label = Ui.Text(name, 13, FontWeights.SemiBold, new SolidColorBrush(Color.FromArgb(0xF0, 255, 255, 255)), maxLines: 1);
        label.HorizontalAlignment = HorizontalAlignment.Center;
        label.Margin = new Thickness(10, height * 0.07, 10, 0);
        var stack = new StackPanel { VerticalAlignment = VerticalAlignment.Center, HorizontalAlignment = HorizontalAlignment.Center };
        stack.Children.Add(icon);
        stack.Children.Add(label);
        g.Children.Add(stack);
        return g;
    }

    /// A hub's tile: its artwork with the name over it, or a service's icon and name.
    public static UIElement HubTile(HubCard hub, bool logos)
    {
        var api = App.State.Api;
        var tint = ParseColor(hub.Color);
        var g = new Grid();
        if (logos && hub.LogoPath != null)
            g.Children.Add(ServiceFace(hub.Name, tint, api?.ImageUrl(hub.LogoPath, "w300"), Cards.WideWidth * 9 / 16));
        else
        {
            g.Children.Add(Ui.Image(api?.ImageUrl(hub.BackdropPath, "w780"), 560));
            var shade = tint ?? Color.FromArgb(255, 0, 0, 0);
            g.Children.Add(new Border
            {
                Background = new LinearGradientBrush
                {
                    StartPoint = new Windows.Foundation.Point(0, 0),
                    EndPoint = new Windows.Foundation.Point(0, 1),
                    GradientStops =
                    {
                        new GradientStop { Color = Color.FromArgb(0, shade.R, shade.G, shade.B), Offset = 0.4 },
                        new GradientStop { Color = Color.FromArgb(0xD8, shade.R, shade.G, shade.B), Offset = 1 },
                    },
                },
            });
            var name = Ui.Text(hub.Name, 17, FontWeights.Bold, maxLines: 2);
            name.VerticalAlignment = VerticalAlignment.Bottom;
            name.Margin = new Thickness(14);
            g.Children.Add(name);
        }
        var b = new Button
        {
            Content = Ui.Frame(g, Cards.WideWidth, Cards.WideWidth * 9 / 16),
            Padding = new Thickness(0),
            BorderThickness = new Thickness(0),
            VerticalAlignment = VerticalAlignment.Top,
            Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent),
            CornerRadius = new CornerRadius(10),
        };
        ToolTipService.SetToolTip(b, hub.Name);
        b.Click += (_, _) => App.Shell?.Push(new HubPage(hub.Id, hub.Name));
        b.PointerEntered += (_, _) => Ambient.Hover(hub.BackdropPath);
        return b;
    }

    static Color? ParseColor(string? hex)
    {
        if (hex is not { Length: 7 } || hex[0] != '#' || !uint.TryParse(hex[1..], System.Globalization.NumberStyles.HexNumber, null, out var v)) return null;
        return Color.FromArgb(255, (byte)(v >> 16), (byte)(v >> 8), (byte)v);
    }
}

/// The pages whose rows can be chosen.
sealed class LayoutsPage : Page
{
    protected override Task<UIElement> Build()
    {
        var list = Ui.Column(8);
        void Add(string scope, string label, string glyph)
        {
            var b = new Button
            {
                Content = Ui.Label(glyph, label),
                HorizontalAlignment = HorizontalAlignment.Stretch,
                HorizontalContentAlignment = HorizontalAlignment.Left,
                Padding = new Thickness(16, 12, 16, 12),
                CornerRadius = new CornerRadius(8),
            };
            b.Click += (_, _) => App.Shell?.Push(new LayoutEditorPage(scope, label));
            list.Children.Add(b);
        }
        Add("home", "Home", "");
        if (App.State.CanStream)
        {
            Add("movies", "Movies", "");
            Add("shows", "TV Shows", "");
        }
        else
        {
            foreach (var l in App.State.Libraries) Add("library:" + l.Id, l.Name, l.Glyph);
        }
        list.MaxWidth = 640;
        list.HorizontalAlignment = HorizontalAlignment.Left;
        return Task.FromResult<UIElement>(Scroll(PageTitle("Layouts", "Choose which rows each page shows, and in what order."), Padded(list)));
    }
}

/// One page's rows: move, hide, remove, add. Admins also choose whether
/// they're editing their own layout or everyone's default.
sealed class LayoutEditorPage(string scope, string title) : Page
{
    public override bool ReloadsOnRefresh => false;

    bool everyone;
    LayoutConfig? config;

    protected override async Task<UIElement> Build()
    {
        var api = App.State.Api!;
        config = await api.LayoutConfig(scope, everyone);
        var c = config;
        var col = Ui.Column(12);
        col.MaxWidth = 720;
        col.HorizontalAlignment = HorizontalAlignment.Left;

        if (App.State.IsAdmin)
        {
            var who = new ComboBox { Items = { "My layout", "Default for everyone" }, SelectedIndex = everyone ? 1 : 0, MinWidth = 220 };
            who.SelectionChanged += (_, _) => { everyone = who.SelectedIndex == 1; Reload(quiet: true); };
            col.Children.Add(who);
        }

        for (var i = 0; i < c.Entries.Count; i++)
        {
            var n = i;
            var e = c.Entries[i];
            var row = new Grid { Padding = new Thickness(16, 10, 8, 10), CornerRadius = new CornerRadius(8), Background = Theme.CardBrush };
            row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            var text = Ui.Column(2, Ui.Text(string.IsNullOrEmpty(e.Title) ? e.Ref : e.Title, 15, color: e.Hidden ? Theme.TertiaryText : null));
            if (!string.IsNullOrEmpty(e.Detail)) text.Children.Add(Ui.Secondary(e.Detail));
            text.VerticalAlignment = VerticalAlignment.Center;
            row.Children.Add(text);
            var tools = Ui.Row(2);
            var top = Ui.IconButton("", "Move to Top", () => Move(n, 0));
            var up = Ui.IconButton("", "Move Up", () => Move(n, n - 1));
            var down = Ui.IconButton("", "Move Down", () => Move(n, n + 1));
            top.IsEnabled = up.IsEnabled = n > 0;
            down.IsEnabled = n < c.Entries.Count - 1;
            tools.Children.Add(top);
            tools.Children.Add(up);
            tools.Children.Add(down);
            tools.Children.Add(Ui.IconButton(e.Hidden ? "" : "", e.Hidden ? "Show" : "Hide", () =>
            {
                e.Hidden = !e.Hidden;
                Save();
            }));
            tools.Children.Add(Ui.IconButton("", "Remove", () =>
            {
                c.Entries.RemoveAt(n);
                Save();
            }));
            Grid.SetColumn(tools, 1);
            row.Children.Add(tools);
            col.Children.Add(row);
        }

        var have = c.Entries.Select(e => e.Key).ToHashSet();
        var options = c.Options.Where(o => !have.Contains(o.Key)).ToList();
        if (options.Count > 0)
        {
            var menu = new MenuFlyout();
            foreach (var o in options)
            {
                var opt = o;
                var item = new MenuFlyoutItem { Text = string.Join(" — ", new[] { string.IsNullOrEmpty(o.Title) ? o.Ref : o.Title, o.Detail }.Where(s => !string.IsNullOrEmpty(s))) };
                item.Click += (_, _) => { c.Entries.Add(opt); Save(); };
                menu.Items.Add(item);
            }
            col.Children.Add(new DropDownButton { Content = Ui.Label("", "Add a Row"), Flyout = menu, Padding = new Thickness(14, 8, 14, 8), CornerRadius = new CornerRadius(8) });
        }

        if (!c.Custom)
            col.Children.Add(Ui.Secondary(!everyone && c.HasDefault ? "Showing the server's default. Changing anything makes it your own." : "Showing Wren TV's built-in layout.", 13, 2));
        else
            col.Children.Add(Ui.Button(!everyone && c.HasDefault ? "Use the Default Layout" : "Back to the Built-in Layout", async () =>
            {
                try { await api.ResetLayout(scope, everyone); App.State.Bump(); Reload(quiet: true); }
                catch (Exception ex) { App.Shell?.ShowError(ex.Message); }
            }));

        return Scroll(PageTitle(title, everyone ? "The default layout for everyone" : "Your layout"), Padded(col));
    }

    void Move(int from, int to)
    {
        if (config == null || to < 0 || to >= config.Entries.Count) return;
        var e = config.Entries[from];
        config.Entries.RemoveAt(from);
        config.Entries.Insert(to, e);
        Save();
    }

    async void Save()
    {
        if (config == null || App.State.Api is not { } api) return;
        try
        {
            await api.SetLayout(scope, everyone, config.Entries);
            App.State.Bump();
        }
        catch (Exception e) { App.Shell?.ShowError(e.Message); }
        Reload(quiet: true);
    }
}

/// A title's trailer, played here rather than in the browser.
sealed class TrailerPage(Trailer trailer, string title) : Page
{
    public override bool ReloadsOnRefresh => false;

    protected override Task<UIElement> Build()
    {
        var web = new WebView2
        {
            Source = new Uri($"https://www.youtube.com/embed/{Uri.EscapeDataString(trailer.YoutubeKey)}?autoplay=1&rel=0&playsinline=1"),
            DefaultBackgroundColor = Color.FromArgb(255, 0, 0, 0),
        };
        // Leaving the page stops the video.
        web.Unloaded += (_, _) => web.Close();
        var g = new Grid { Padding = new Thickness(Gutter, 16, Gutter, 32), RowSpacing = 16 };
        g.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        g.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        var head = Ui.Row(12);
        head.Children.Add(Ui.Title(string.IsNullOrEmpty(trailer.Name) ? $"{title} — Trailer" : trailer.Name, 24));
        head.Children.Add(Ui.Button("Open in Browser", () => Ui.Open(trailer.Url), ""));
        g.Children.Add(head);
        var frame = new Border { Child = web, CornerRadius = new CornerRadius(10), Background = new SolidColorBrush(Microsoft.UI.Colors.Black) };
        Grid.SetRow(frame, 1);
        g.Children.Add(frame);
        return Task.FromResult<UIElement>(g);
    }
}
