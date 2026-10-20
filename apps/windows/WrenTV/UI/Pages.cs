using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
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

sealed class HomePage : Page
{
    protected override async Task<UIElement> Build()
    {
        var api = App.State.Api!;
        var home = await api.Home();
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
                Ui.Button(target.InProgress ? "Resume" : "Play", () => Nav.Play(target), "", accent: true),
                Ui.Button("More Info", () => Nav.Item(target.Kind == "episode" && target.ShowId is { } sid ? sid : target.Id), ""),
            ]));
        }
        if (home.ContinueWatching.Count > 0)
            sections.Add(Cards.Shelf("Continue Watching", home.ContinueWatching.Select(i => (UIElement)Cards.Wide(i))));
        if (home.Watchlist is { Count: > 0 } wl)
            sections.Add(Cards.Shelf("Watchlist", wl.Select(i => (UIElement)Cards.Poster(i))));
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
            return Ui.Empty("", "Nothing here yet",
                App.State.IsAdmin ? "Add a library on the Server page, or in the server's web app, and it'll show up here." : "The server's libraries are empty. Check back once the owner adds something.");
        }
        return Scroll(sections.ToArray());
    }
}

sealed class DiscoverPage : Page
{
    protected override async Task<UIElement> Build()
    {
        var api = App.State.Api!;
        var d = await api.DiscoverHome();
        var sections = new List<UIElement>();
        if (!d.Enabled) return Ui.Empty("", "Discover is off", "The server needs a TMDB key to show what's trending.");
        if (d.Hero.FirstOrDefault() is { } h)
        {
            var actions = new List<Button>();
            if (h.Item is { } hi)
            {
                actions.Add(Ui.Button(hi.InProgress ? "Resume" : "Play", () => Nav.Play(hi), "", accent: true));
                actions.Add(Ui.Button("More Info", () => Nav.Item(hi), ""));
            }
            else actions.Add(Ui.Button(App.State.CanStream ? "Watch" : "More Info", () => Nav.Title(h), "", accent: true));
            var meta = string.Join(" · ", new[] { h.Year?.ToString(), h.MediaType == "show" ? "Series" : "Movie", h.Rating is { } r ? $"★ {r:0.0}" : null }.Where(x => x != null));
            sections.Add(Hero.Make(h.Title, h.Overview, h.BackdropPath, meta, actions));
        }
        if (d.ContinueWatching.Count > 0)
            sections.Add(Cards.Shelf("Continue Watching", d.ContinueWatching.Select(i => (UIElement)Cards.Wide(i))));
        if (d.Watchlist is { Count: > 0 } wl)
            sections.Add(Cards.Shelf("Watchlist", wl.Select(i => (UIElement)Cards.Poster(i))));
        foreach (var row in d.Rows.Where(r => r.Items.Count > 0))
        {
            var ranked = row.Id.Contains("top10", StringComparison.OrdinalIgnoreCase) || row.Title.StartsWith("Top 10");
            sections.Add(Cards.Shelf(row.Title, row.Items.Select((t, n) => (UIElement)Cards.Title(t, rank: ranked ? n + 1 : null))));
        }
        if (d.RecentlyWatched.Count > 0)
            sections.Add(Cards.Shelf("Recently Watched", d.RecentlyWatched.Select(i => (UIElement)Cards.Poster(i))));
        return Scroll(sections.ToArray());
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
        var items = await local;
        var sections = new List<UIElement>();
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
    protected override async Task<UIElement> Build()
    {
        var c = await App.State.Api!.Collection(id);
        var g = new Grid { RowSpacing = 20, Padding = new Thickness(0, 24, 0, 0) };
        g.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        g.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        g.Children.Add(PageTitle(c.Collection.Name.Length > 0 ? c.Collection.Name : name,
            string.IsNullOrEmpty(c.Collection.Description) ? $"{c.Items.Count} titles" : c.Collection.Description));
        UIElement list;
        var missing = c.Missing ?? c.Titles?.Where(t => t.Item == null).ToList() ?? [];
        if (missing.Count > 0)
        {
            var sections = new List<UIElement>
            {
                Cards.Shelf("In Your Library", c.Items.Select(i => (UIElement)Cards.Poster(i))),
                Cards.Shelf(App.State.CanStream ? "Stream the Rest" : "Not in Your Library", missing.Select(t => (UIElement)Cards.Title(t))),
            };
            var col = new StackPanel { Spacing = 36, Padding = new Thickness(0, 0, 0, 48) };
            foreach (var s in sections) col.Children.Add(s);
            list = new ScrollViewer { Content = col };
        }
        else list = Cards.PosterGrid(c.Items);
        Grid.SetRow((FrameworkElement)list, 1);
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
