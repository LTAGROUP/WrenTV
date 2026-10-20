using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Media;
using WrenTV.Core;

namespace WrenTV.UI;

/// A library's titles in a grid, with sort, genre and unwatched filters, plus
/// its collections and genres.
sealed class LibraryPage(Library library) : Page
{
    static readonly (string Key, string Name)[] Sorts =
    [
        ("title", "Title"), ("added", "Recently Added"), ("year", "Year (newest)"), ("year_asc", "Year (oldest)"), ("rating", "Rating"),
    ];

    string tab = "titles";
    string sort = Store.GetString("librarySort") ?? "title";
    string? genre;
    bool unwatched;
    List<GenreCount>? genres;
    string filter = "";
    List<Item> all = [];
    GridView? grid;

    List<Item> Filtered => filter.Length == 0 ? all : all.Where(i => i.Title.Contains(filter, StringComparison.OrdinalIgnoreCase)).ToList();

    protected override async Task<UIElement> Build()
    {
        var api = App.State.Api!;
        genres ??= await api.Genres(library.Id).ContinueWith(t => t.IsCompletedSuccessfully ? t.Result : []);
        var g = new Grid { RowSpacing = 16, Padding = new Thickness(0, 24, 0, 0) };
        g.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        g.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        g.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });

        UIElement content;
        string subtitle;
        switch (tab)
        {
            case "collections":
            {
                var cols = await api.Collections(library.Id);
                subtitle = cols.Count == 1 ? "1 collection" : $"{cols.Count} collections";
                content = cols.Count == 0
                    ? Ui.Empty("", "No collections", "Collections from TMDB and ones you make show up here.")
                    : Tiles(cols.Select(c => Tile(c.Name, c.ItemCount is { } n ? $"{n} titles" : "", c.BackdropPath ?? c.PosterPath,
                        () => App.Shell?.Push(new CollectionPage(c.Id, c.Name)))));
                break;
            }
            case "genres":
                subtitle = genres.Count == 1 ? "1 genre" : $"{genres.Count} genres";
                content = genres.Count == 0
                    ? Ui.Empty("", "No genres", "Genres appear once titles are matched.")
                    : Tiles(genres.Select(x => Tile(x.Genre, $"{x.Count} titles", x.BackdropPath,
                        () => App.Shell?.Push(new GenrePage(x.Genre, library.Id)))));
                break;
            default:
            {
                var page = await api.Items(library.Id, sort, genre, unwatched);
                all = page.Items;
                subtitle = $"{page.Total} {(library.Type == "shows" ? "shows" : "movies")}";
                grid = all.Count == 0 ? null : Cards.PosterGrid(Filtered);
                content = grid ?? Ui.Empty(library.Glyph, genre != null || unwatched ? "Nothing matches" : "This library is empty",
                    genre != null || unwatched ? "Try a different filter." : "Titles appear here after the server scans the library's folders.");
                break;
            }
        }

        var header = new Grid { Margin = new Thickness(Gutter, 0, Gutter, 0) };
        header.Children.Add(Ui.Column(4, Ui.Title(library.Name, 30), Ui.Secondary(subtitle, 14)));
        var tabs = new SelectorBar { HorizontalAlignment = HorizontalAlignment.Right, VerticalAlignment = VerticalAlignment.Center };
        foreach (var (key, name) in new[] { ("titles", library.Type == "shows" ? "Shows" : "Movies"), ("collections", "Collections"), ("genres", "Genres") })
        {
            var item = new SelectorBarItem { Text = name, Tag = key, IsSelected = key == tab };
            tabs.Items.Add(item);
        }
        tabs.SelectionChanged += (s, _) =>
        {
            if (s.SelectedItem?.Tag is string t && t != tab) { tab = t; Reload(); }
        };
        header.Children.Add(tabs);
        g.Children.Add(header);

        if (tab == "titles")
        {
            var bar = Toolbar();
            Grid.SetRow(bar, 1);
            g.Children.Add(bar);
        }
        Grid.SetRow((FrameworkElement)content, 2);
        g.Children.Add(content);
        return g;
    }

    FrameworkElement Toolbar()
    {
        var row = Ui.Row(10);
        row.Margin = new Thickness(Gutter, 0, Gutter, 0);

        var find = new TextBox { PlaceholderText = "Filter titles", Width = 220, Text = filter };
        var typing = 0;
        find.TextChanged += async (_, _) =>
        {
            var n = ++typing;
            await Task.Delay(300);
            if (n != typing) return;
            filter = find.Text.Trim();
            if (grid != null) grid.ItemsSource = Filtered;
        };
        row.Children.Add(find);

        var genreMenu = new MenuFlyout();
        void AddGenre(string? g, string label)
        {
            var m = new ToggleMenuFlyoutItem { Text = label, IsChecked = g == genre };
            m.Click += (_, _) => { genre = g; Reload(quiet: true); };
            genreMenu.Items.Add(m);
        }
        AddGenre(null, "All genres");
        genreMenu.Items.Add(new MenuFlyoutSeparator());
        foreach (var x in genres ?? []) AddGenre(x.Genre, $"{x.Genre}  ({x.Count})");
        row.Children.Add(new DropDownButton { Content = Ui.Label("", genre ?? "All genres"), Flyout = genreMenu, CornerRadius = new CornerRadius(8) });

        var sortMenu = new MenuFlyout();
        foreach (var (key, name) in Sorts)
        {
            var m = new ToggleMenuFlyoutItem { Text = name, IsChecked = key == sort };
            m.Click += (_, _) =>
            {
                sort = key;
                Store.Set("librarySort", key);
                Reload(quiet: true);
            };
            sortMenu.Items.Add(m);
        }
        row.Children.Add(new DropDownButton
        {
            Content = Ui.Label("", "Sort: " + (Sorts.FirstOrDefault(s => s.Key == sort).Name ?? "Title")),
            Flyout = sortMenu,
            CornerRadius = new CornerRadius(8),
        });

        var watched = new ToggleButton { Content = Ui.Label("", "Unwatched"), IsChecked = unwatched, CornerRadius = new CornerRadius(8) };
        watched.Click += (_, _) => { unwatched = watched.IsChecked == true; Reload(quiet: true); };
        row.Children.Add(watched);
        return row;
    }

    static UIElement Tiles(IEnumerable<UIElement> tiles)
    {
        var wrap = new VariableSizedWrapGrid { Orientation = Orientation.Horizontal, ItemWidth = 296, ItemHeight = 186, Margin = new Thickness(Gutter - 8, 0, Gutter, 40) };
        foreach (var t in tiles) wrap.Children.Add(t);
        return new ScrollViewer { Content = wrap };
    }

    static UIElement Tile(string title, string sub, string? image, Action open)
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
                    new GradientStop { Color = Windows.UI.Color.FromArgb(0x10, 0, 0, 0), Offset = 0.3 },
                    new GradientStop { Color = Windows.UI.Color.FromArgb(0xD0, 0, 0, 0), Offset = 1 },
                },
            },
        });
        var text = Ui.Column(2, Ui.Text(title, 17, FontWeights.Bold), Ui.Secondary(sub));
        text.VerticalAlignment = VerticalAlignment.Bottom;
        text.Margin = new Thickness(14);
        g.Children.Add(text);
        var b = new Button
        {
            Content = Ui.Frame(g, 280, 158),
            Padding = new Thickness(0),
            Margin = new Thickness(8),
            BorderThickness = new Thickness(0),
            Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent),
            CornerRadius = new CornerRadius(10),
        };
        b.Click += (_, _) => open();
        return b;
    }
}
