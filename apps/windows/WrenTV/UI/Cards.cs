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
    public const double PosterWidth = 150, PosterHeight = 225;
    public const double WideWidth = 300, WideHeight = 169;

    static Button Plain(UIElement content) => new()
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
    public static Button Poster(Item item, double width = PosterWidth)
    {
        var height = width * 1.5;
        var api = App.State.Api;
        var stack = Ui.Column(6,
            Overlaid(Ui.Image(api?.ImageUrl(item.PosterPath, "w342"), 300), item, width, height, true),
            Ui.Text(item.DisplayTitle, 13, FontWeights.SemiBold),
            Ui.Secondary(item.Kind == "season" ? (item.ChildCount is { } c ? $"{c} episodes" : "") : item.Year?.ToString() ?? ""));
        stack.Width = width;
        var b = Plain(stack);
        b.Click += (_, _) => Nav.Item(item);
        AttachMenu(b, item);
        return b;
    }

    /// A wide still or backdrop: continue watching and episodes. Plays on click.
    public static Button Wide(Item item, bool episodeTitle = false, double width = WideWidth)
    {
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
        b.PointerEntered += (_, _) => play.Opacity = 1;
        b.PointerExited += (_, _) => play.Opacity = 0;
        b.Click += (_, _) => Nav.Play(item);
        AttachMenu(b, item);
        return b;
    }

    /// A TMDB title (trending, more like this, discover).
    public static Button Title(TitleView t, double width = PosterWidth, int? rank = null)
    {
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
        var stack = Ui.Column(6, top, Ui.Text(t.Title, 13, FontWeights.SemiBold), Ui.Secondary(t.Year?.ToString() ?? ""));
        stack.Width = width;
        var b = Plain(stack);
        b.Click += (_, _) => Nav.Title(t);
        if (t.Item is { } item) AttachMenu(b, item);
        return b;
    }

    /// Right-click: play, from the beginning, watched.
    public static void AttachMenu(FrameworkElement e, Item item)
    {
        e.ContextRequested += (_, args) =>
        {
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

    /// A titled horizontal row of cards.
    public static UIElement Shelf(string title, IEnumerable<UIElement> cards, Action? seeAll = null, string? subtitle = null)
    {
        var list = cards.ToList();
        var row = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 16, Padding = new Thickness(Page.Gutter, 0, Page.Gutter, 4) };
        foreach (var c in list) row.Children.Add(c);
        var scroll = new ScrollViewer
        {
            Content = row,
            HorizontalScrollMode = ScrollMode.Enabled,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
            VerticalScrollMode = ScrollMode.Disabled,
            VerticalScrollBarVisibility = ScrollBarVisibility.Disabled,
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
        return Ui.Column(12, header, scroll);
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
            AttachMenu(host, item);
            args.Handled = true;
        };
        grid.ItemClick += (_, args) => { if (args.ClickedItem is Item i) Nav.Item(i); };
        return grid;
    }
}
