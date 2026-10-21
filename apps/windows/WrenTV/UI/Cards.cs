using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Markup;
using Microsoft.UI.Xaml.Media;
using Windows.UI;
using WrenTV.Core;

namespace WrenTV.UI;

/// Where pages go: the detail of an item, a library, the player.
static class Nav
{
    static AppState State => App.State;
    static MainWindow? Main => App.Shell;

    public static void Item(Item i)
    {
        Main?.Push(new DetailPage(i.Id));
    }

    public static void Item(string id) => Main?.Push(new DetailPage(id));

    public static void Play(Item i, bool fromStart = false, string? fileId = null) => Play(Format.Target(i, fromStart, fileId));

    public static void Play(PlayTarget t)
    {
        if (State.Api is not { } api) return;
        PlayerWindow.Open(t, api);
    }

    /// A TMDB title: its library item when the server has it, else added to the
    /// streaming library first; TMDB's page when streaming is off.
    public static async void Title(TitleView t)
    {
        if (t.Item is { } i) { Item(i); return; }
        if (State.Api is not { } api) return;
        if (!State.CanStream)
        {
            Ui.Open(new Uri($"https://www.themoviedb.org/{(t.MediaType == "show" ? "tv" : "movie")}/{t.TmdbId}"));
            return;
        }
        try
        {
            var item = await api.AddStreamTitle(t.TmdbId, t.MediaType);
            Item(item);
        }
        catch (Exception e) { Main?.ShowError(e.Message); }
    }
}

static class Cards
{
    /// Posters and tiles scale with the Poster size setting.
    public static double Scale => Store.GetString(PosterSizeKey) switch { "small" => 0.85, "large" => 1.25, _ => 1 };
    public const string PosterSizeKey = "posterSize";
    public static double PosterWidth => 150 * Scale;
    public static double PosterHeight => PosterWidth * 1.5;
    public static double WideWidth => 300 * Scale;
    public static double WideHeight => WideWidth * 9 / 16;

    public static Button Plain(UIElement content) => new()
    {
        Content = content,
        Padding = new Thickness(0),
        Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent),
        BorderThickness = new Thickness(0),
        CornerRadius = new CornerRadius(10),
        HorizontalContentAlignment = HorizontalAlignment.Left,
        VerticalAlignment = VerticalAlignment.Top,
    };

    static UIElement Overlaid(Image image, Item? item, double width, double height, bool showProgress)
    {
        var g = new Grid();
        g.Children.Add(image);
        if (item != null)
        {
            if (item.IsWatched || (item.Kind is "show" or "season" && item.UnwatchedCount == 0 && item.ChildCount > 0))
            {
                var mark = new Border
                {
                    Width = 24, Height = 24, CornerRadius = new CornerRadius(12),
                    Background = Theme.EmberBrush, Margin = new Thickness(8),
                    HorizontalAlignment = HorizontalAlignment.Right, VerticalAlignment = VerticalAlignment.Top,
                    Child = new FontIcon { Glyph = "", FontSize = 12, Foreground = new SolidColorBrush(Microsoft.UI.Colors.White) },
                };
                g.Children.Add(mark);
            }
            else if (item.Kind is "show" or "season" && item.UnwatchedCount is > 0 and var n)
            {
                g.Children.Add(new Border
                {
                    MinWidth = 24, Height = 24, CornerRadius = new CornerRadius(12), Padding = new Thickness(7, 0, 7, 0),
                    Background = Theme.EmberBrush, Margin = new Thickness(8),
                    HorizontalAlignment = HorizontalAlignment.Right, VerticalAlignment = VerticalAlignment.Top,
                    Child = new TextBlock { Text = n.ToString(), FontSize = 12, FontWeight = FontWeights.Bold, VerticalAlignment = VerticalAlignment.Center, HorizontalAlignment = HorizontalAlignment.Center },
                });
            }
            if (showProgress && item.InProgress && item.Progress is { } p)
            {
                var bar = Ui.Progress(p.Fraction);
                bar.VerticalAlignment = VerticalAlignment.Bottom;
                bar.Margin = new Thickness(8);
                g.Children.Add(bar);
            }
        }
        return Ui.Frame(g, width, height);
    }

    /// A poster with its title underneath; opens the item.
    public static Button Poster(Item item, double? size = null)
    {
        var width = size ?? PosterWidth;
        var height = width * 1.5;
        var api = App.State.Api;
        var stack = Ui.Column(6,
            Overlaid(Ui.Image(api?.ImageUrl(item.PosterPath, "w342"), 300), item, width, height, true),
            Ui.Text(item.DisplayTitle, 13, FontWeights.SemiBold),
            Ui.Secondary(item.Kind == "season" ? (item.ChildCount is { } c ? $"{c} episodes" : "") : item.Year?.ToString() ?? ""));
        stack.Width = width;
        var b = Plain(stack);
        b.Click += (_, _) => Nav.Item(item);
        b.PointerEntered += (_, _) => Ambient.Hover(item.BackdropPath);
        AttachMenu(b, item);
        return b;
    }

    /// A wide still or backdrop: continue watching and episodes. Plays on click.
    public static Button Wide(Item item, bool episodeTitle = false, double? size = null)
    {
        var width = size ?? WideWidth;
        var height = width * 9 / 16;
        var api = App.State.Api;
        var image = item.Kind == "episode" ? item.StillPath ?? item.BackdropPath : item.BackdropPath ?? item.StillPath;
        var title = item.Kind == "episode" && !episodeTitle ? item.ShowTitle ?? item.Title : item.Title;
        var sub = item.Kind == "episode"
            ? episodeTitle ? item.EpisodeLabel : $"{item.EpisodeLabel} · {item.Title}"
            : item.Year?.ToString() ?? "";
        if (item.InProgress && item.Progress is { } p) sub = string.IsNullOrEmpty(sub) ? Format.TimeLeft(p) : $"{sub} · {Format.TimeLeft(p)}";
        var frame = Overlaid(Ui.Image(api?.ImageUrl(image, "w780"), 600), item, width, height, true);
        var play = new Border
        {
            Width = 44, Height = 44, CornerRadius = new CornerRadius(22),
            Background = new SolidColorBrush(Color.FromArgb(0xB0, 0, 0, 0)),
            Child = new FontIcon { Glyph = "", FontSize = 18 },
            Opacity = 0,
        };
        var g = new Grid { Width = width, Height = height };
        g.Children.Add(frame);
        g.Children.Add(play);
        var stack = Ui.Column(6, g, Ui.Text(title, 13, FontWeights.SemiBold), Ui.Secondary(sub));
        stack.Width = width;
        var b = Plain(stack);
        b.PointerEntered += (_, _) => { play.Opacity = 1; Ambient.Hover(item.BackdropPath ?? item.StillPath); };
        b.PointerExited += (_, _) => play.Opacity = 0;
        b.Click += (_, _) => Nav.Play(item);
        AttachMenu(b, item);
        return b;
    }

    /// A TMDB title (trending, more like this, discover).
    public static Button Title(TitleView t, double? size = null, int? rank = null)
    {
        var b = Plain(TitleCell(t, size ?? PosterWidth, rank));
        b.Click += (_, _) => Nav.Title(t);
        b.PointerEntered += (_, _) => Ambient.Hover(t.BackdropPath);
        if (t.Item is { } item) AttachMenu(b, item);
        return b;
    }

    static StackPanel TitleCell(TitleView t, double? size = null, int? rank = null)
    {
        var width = size ?? PosterWidth;
        var api = App.State.Api;
        var image = Ui.Image(api?.ImageUrl(t.PosterPath, "w342"), 300);
        var frame = Overlaid(image, t.Item, width, width * 1.5, true);
        UIElement top = frame;
        if (rank is { } r)
        {
            var g = new Grid();
            g.Children.Add(frame);
            g.Children.Add(new TextBlock
            {
                Text = r.ToString(), FontSize = 64, FontWeight = FontWeights.Black, Foreground = new SolidColorBrush(Theme.Cream),
                Margin = new Thickness(8, 0, 0, -10), VerticalAlignment = VerticalAlignment.Bottom, Opacity = 0.9,
            });
            top = g;
        }
        var stack = Ui.Column(6, top, Ui.Text(t.Title, 13, FontWeights.SemiBold), Ui.Secondary(t.Caption));
        stack.Width = width;
        return stack;
    }

    /// Right-click: play, from the beginning, watched.
    public static void AttachMenu(FrameworkElement e, Item item) => AttachMenu(e, () => item);

    public static void AttachMenu(FrameworkElement e, Func<Item?> current)
    {
        e.ContextRequested += (_, args) =>
        {
            if (current() is not { } item) return;
            var menu = new MenuFlyout();
            void Add(string text, string glyph, Action a)
            {
                var m = new MenuFlyoutItem { Text = text, Icon = new FontIcon { Glyph = glyph } };
                m.Click += (_, _) => a();
                menu.Items.Add(m);
            }
            if (item.IsPlayable)
            {
                Add(item.InProgress ? "Resume" : "Play", "", () => Nav.Play(item));
                if (item.InProgress) Add("Play from Beginning", "", () => Nav.Play(item, fromStart: true));
            }
            Add("Details", "", () => Nav.Item(item));
            if (item.Kind == "episode" && item.ShowId is { } sid) Add("Go to Show", "", () => Nav.Item(sid));
            if (item.Kind != "season")
            {
                var watched = item.IsWatched;
                Add(watched ? "Mark as Unwatched" : "Mark as Watched", watched ? "" : "", async () =>
                {
                    if (App.State.Api is not { } api) return;
                    try { await api.SetWatched(item.Id, !watched); App.State.Bump(); }
                    catch (Exception ex) { App.Shell?.ShowError(ex.Message); }
                });
            }
            menu.ShowAt(e, args.TryGetPosition(e, out var pt) ? new FlyoutShowOptions { Position = pt } : new FlyoutShowOptions());
            args.Handled = true;
        };
    }

    /// A titled horizontal row of cards. The row scrolls with its arrows, a
    /// sideways swipe or Shift+wheel; a plain wheel scrolls the page, never the row.
    public static UIElement Shelf(string title, IEnumerable<UIElement> cards, Action? seeAll = null, string? subtitle = null)
    {
        var list = cards.ToList();
        var row = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 16, Padding = new Thickness(Page.Gutter, 0, Page.Gutter, 4) };
        foreach (var c in list) row.Children.Add(c);
        var scroll = new ScrollViewer
        {
            Content = row,
            HorizontalScrollMode = ScrollMode.Disabled,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Hidden,
            VerticalScrollMode = ScrollMode.Disabled,
            VerticalScrollBarVisibility = ScrollBarVisibility.Disabled,
            IsHorizontalRailEnabled = false,
        };
        void By(double dx) => scroll.ChangeView(Math.Clamp(scroll.HorizontalOffset + dx, 0, scroll.ScrollableWidth), null, null);
        Button Arrow(string glyph, int dir)
        {
            var a = new Button
            {
                Content = new FontIcon { Glyph = glyph, FontSize = 16 },
                Width = 40, Height = 64, Padding = new Thickness(0), CornerRadius = new CornerRadius(8),
                Background = new SolidColorBrush(Color.FromArgb(0xC0, 0x12, 0x10, 0x16)),
                BorderThickness = new Thickness(0),
                HorizontalAlignment = dir < 0 ? HorizontalAlignment.Left : HorizontalAlignment.Right,
                VerticalAlignment = VerticalAlignment.Center,
                Margin = new Thickness(dir < 0 ? 4 : 0, 0, dir > 0 ? 4 : 0, 40),
                Visibility = Visibility.Collapsed,
            };
            a.Click += (_, _) => By(dir * Math.Max(200, scroll.ViewportWidth - Page.Gutter * 2 - 80));
            return a;
        }
        var left = Arrow("\uE76B", -1);
        var right = Arrow("\uE76C", 1);
        var hovering = false;
        void Arrows()
        {
            left.Visibility = hovering && scroll.HorizontalOffset > 1 ? Visibility.Visible : Visibility.Collapsed;
            right.Visibility = hovering && scroll.HorizontalOffset < scroll.ScrollableWidth - 1 ? Visibility.Visible : Visibility.Collapsed;
        }
        scroll.ViewChanged += (_, _) => Arrows();
        var strip = new Grid();
        strip.Children.Add(scroll);
        strip.Children.Add(left);
        strip.Children.Add(right);
        strip.PointerEntered += (_, _) => { hovering = true; Arrows(); };
        strip.PointerExited += (_, _) => { hovering = false; Arrows(); };
        strip.PointerWheelChanged += (_, e) =>
        {
            var p = e.GetCurrentPoint(strip).Properties;
            var shift = (e.KeyModifiers & Windows.System.VirtualKeyModifiers.Shift) != 0;
            if (!p.IsHorizontalMouseWheel && !shift) return;
            By(p.IsHorizontalMouseWheel ? p.MouseWheelDelta : -p.MouseWheelDelta);
            e.Handled = true;
        };

        var header = new Grid { Margin = new Thickness(Page.Gutter, 0, Page.Gutter, 0) };
        var titles = Ui.Column(2, Ui.Text(title, 20, FontWeights.Bold));
        if (subtitle != null) titles.Children.Add(Ui.Secondary(subtitle));
        header.Children.Add(titles);
        if (seeAll != null)
        {
            var link = new HyperlinkButton { Content = "See All", HorizontalAlignment = HorizontalAlignment.Right, VerticalAlignment = VerticalAlignment.Center };
            link.Click += (_, _) => seeAll();
            header.Children.Add(link);
        }
        return Ui.Column(12, header, strip);
    }

    static DataTemplate? hostTemplate;

    /// A template that's just an empty grid; cells are built in code as they scroll in.
    static DataTemplate HostTemplate => hostTemplate ??= (DataTemplate)XamlReader.Load(
        "<DataTemplate xmlns=\"http://schemas.microsoft.com/winfx/2006/xaml/presentation\"><Grid/></DataTemplate>");

    /// A virtualized grid of posters for libraries, genres and collections.
    public static GridView PosterGrid(IList<Item> items)
    {
        var grid = new GridView
        {
            ItemsSource = items,
            ItemTemplate = HostTemplate,
            IsItemClickEnabled = true,
            SelectionMode = ListViewSelectionMode.None,
            Padding = new Thickness(Page.Gutter - 4, 0, Page.Gutter, 40),
        };
        grid.ContainerContentChanging += (_, args) =>
        {
            if (args.InRecycleQueue || args.ItemContainer.ContentTemplateRoot is not Grid host || args.Item is not Item item) return;
            host.Children.Clear();
            var cell = Ui.Column(6,
                Overlaid(Ui.Image(App.State.Api?.ImageUrl(item.PosterPath, "w342"), 300), item, PosterWidth, PosterHeight, true),
                Ui.Text(item.DisplayTitle, 13, FontWeights.SemiBold),
                Ui.Secondary(item.Year?.ToString() ?? ""));
            cell.Width = PosterWidth;
            cell.Margin = new Thickness(4, 4, 4, 10);
            host.Children.Add(cell);
            if (host.Tag == null)
            {
                AttachMenu(host, () => host.Tag as Item);
                host.PointerEntered += (_, _) => Ambient.Hover((host.Tag as Item)?.BackdropPath);
            }
            host.Tag = item;
            args.Handled = true;
        };
        grid.ItemClick += (_, args) => { if (args.ClickedItem is Item i) Nav.Item(i); };
        return grid;
    }

    /// A virtualized grid of TMDB titles; calls more() as the end scrolls in.
    public static GridView TitleGrid(IList<TitleView> titles, Action? more = null)
    {
        var grid = new GridView
        {
            ItemsSource = titles,
            ItemTemplate = HostTemplate,
            IsItemClickEnabled = true,
            SelectionMode = ListViewSelectionMode.None,
            Padding = new Thickness(Page.Gutter - 4, 0, Page.Gutter, 40),
        };
        grid.ContainerContentChanging += (_, args) =>
        {
            if (args.InRecycleQueue || args.ItemContainer.ContentTemplateRoot is not Grid host || args.Item is not TitleView t) return;
            host.Children.Clear();
            var cell = TitleCell(t);
            cell.Margin = new Thickness(4, 4, 4, 10);
            host.Children.Add(cell);
            if (host.Tag == null)
            {
                AttachMenu(host, () => (host.Tag as TitleView)?.Item);
                host.PointerEntered += (_, _) => Ambient.Hover((host.Tag as TitleView)?.BackdropPath);
            }
            host.Tag = t;
            if (more != null && args.ItemIndex >= titles.Count - 12) more();
            args.Handled = true;
        };
        grid.ItemClick += (_, args) => { if (args.ClickedItem is TitleView t) Nav.Title(t); };
        return grid;
    }

    /// Wide image tiles for collections and genres.
    public static UIElement Tiles(IEnumerable<UIElement> tiles)
    {
        var wrap = new VariableSizedWrapGrid { Orientation = Orientation.Horizontal, ItemWidth = 296 * Scale, ItemHeight = 186 * Scale, Margin = new Thickness(Page.Gutter - 8, 0, Page.Gutter, 40) };
        foreach (var t in tiles) wrap.Children.Add(t);
        return new ScrollViewer { Content = wrap };
    }

    public static Button Tile(string title, string sub, string? image, Action open, double margin = 8, string? flag = null, bool accent = false)
    {
        var g = new Grid();
        g.Children.Add(Ui.Image(App.State.Api?.ImageUrl(image, "w780"), 560));
        g.Children.Add(new Border
        {
            Background = new LinearGradientBrush
            {
                StartPoint = new Windows.Foundation.Point(0, 0),
                EndPoint = new Windows.Foundation.Point(0, 1),
                GradientStops =
                {
                    new GradientStop { Color = Color.FromArgb(0x10, 0, 0, 0), Offset = 0.3 },
                    new GradientStop { Color = Color.FromArgb(0xD0, 0, 0, 0), Offset = 1 },
                },
            },
        });
        var text = Ui.Column(2, Ui.Text(title, 17, FontWeights.Bold), Ui.Secondary(sub));
        text.VerticalAlignment = VerticalAlignment.Bottom;
        text.Margin = new Thickness(14);
        g.Children.Add(text);
        if (flag != null) g.Children.Add(Flag(flag, accent));
        var b = new Button
        {
            Content = Ui.Frame(g, 280 * Scale, 158 * Scale),
            Padding = new Thickness(0),
            Margin = new Thickness(margin),
            BorderThickness = new Thickness(0),
            Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent),
            CornerRadius = new CornerRadius(10),
        };
        b.Click += (_, _) => open();
        b.PointerEntered += (_, _) => Ambient.Hover(image);
        return b;
    }

    /// A small label in a card's corner, like UP NEXT.
    public static Border Flag(string text, bool accent = false) => new()
    {
        Background = accent ? Theme.EmberBrush : new SolidColorBrush(Color.FromArgb(0xD0, 0x12, 0x10, 0x16)),
        CornerRadius = new CornerRadius(5),
        Padding = new Thickness(7, 3, 7, 3),
        Margin = new Thickness(10),
        HorizontalAlignment = HorizontalAlignment.Left,
        VerticalAlignment = VerticalAlignment.Top,
        Child = new TextBlock { Text = text.ToUpperInvariant(), FontSize = 10.5, FontWeight = FontWeights.Bold },
    };
}
