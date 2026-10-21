using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using WrenTV.Core;

namespace WrenTV.UI;

/// A movie, show, season or episode: artwork, what it is, how to play it,
/// and what's inside (seasons, episodes) or like it.
sealed class DetailPage(string id) : Page
{
    string? fileId;

    protected override async Task<UIElement> Build()
    {
        var api = App.State.Api!;
        var d = await api.Item(id);
        var item = d.Item;
        Extras? extras = null;
        if (item.Kind is "movie" or "show")
            try { extras = await api.Extras(item.Kind == "season" ? item.ShowId ?? item.Id : item.Id); } catch { }

        var sections = new List<UIElement> { Hero(d, extras) };
        switch (item.Kind)
        {
            case "show":
                var seasons = d.Children ?? [];
                if (seasons.Count > 0)
                    sections.Add(Cards.Shelf("Seasons", seasons.OrderBy(s => s.SeasonNumber == 0 ? int.MaxValue : s.SeasonNumber).Select(s => (UIElement)Cards.Poster(s))));
                break;
            case "season":
                sections.Add(Episodes(d.Children ?? []));
                break;
        }
        if (extras is { Cast.Count: > 0 } x) sections.Add(Cards.Shelf("Cast", x.Cast.Take(20).Select(Person)));
        if (extras is { MoreLikeThis.Count: > 0 } m) sections.Add(Cards.Shelf("More Like This", m.MoreLikeThis.Select(t => (UIElement)Cards.Title(t))));
        var col = new StackPanel { Spacing = 36, Padding = new Thickness(0, 0, 0, 48) };
        foreach (var s in sections) col.Children.Add(s);
        return new ScrollViewer { Content = col };
    }

    UIElement Hero(ItemDetail d, Extras? extras)
    {
        var item = d.Item;
        var api = App.State.Api!;
        var backdrop = item.BackdropPath ?? d.Show?.BackdropPath ?? item.StillPath;
        var g = new Grid { MinHeight = 520 };
        var art = Ui.Image(api.ImageUrl(backdrop, "w1280"), 1920);
        art.VerticalAlignment = VerticalAlignment.Top;
        art.Height = 560;
        art.Opacity = 0.55;
        g.Children.Add(art);
        g.Children.Add(new Border
        {
            Background = new LinearGradientBrush
            {
                StartPoint = new Windows.Foundation.Point(0, 0),
                EndPoint = new Windows.Foundation.Point(0, 1),
                GradientStops =
                {
                    new GradientStop { Color = Windows.UI.Color.FromArgb(0x30, 0x12, 0x10, 0x16), Offset = 0 },
                    new GradientStop { Color = Windows.UI.Color.FromArgb(0xFF, 0x12, 0x10, 0x16), Offset = 1 },
                },
            },
        });

        var row = new Grid { ColumnSpacing = 36, Margin = new Thickness(Gutter, 120, Gutter, 0) };
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        var poster = item.Kind == "episode"
            ? Ui.Frame(Ui.Image(api.ImageUrl(item.StillPath ?? backdrop, "w780"), 640), 400, 225, 12)
            : Ui.Frame(Ui.Image(api.ImageUrl(item.PosterPath ?? d.Show?.PosterPath, "w500"), 500), 240, 360, 12);
        poster.VerticalAlignment = VerticalAlignment.Top;
        row.Children.Add(poster);

        var info = Ui.Column(14);
        Grid.SetColumn(info, 1);
        if (item.Kind is "episode" or "season" && (d.Show?.Title ?? item.ShowTitle) is { } showTitle && item.ShowId is { } sid)
        {
            var link = new HyperlinkButton { Content = showTitle, Padding = new Thickness(0), FontSize = 16 };
            link.Click += (_, _) => Nav.Item(sid);
            info.Children.Add(link);
        }
        info.Children.Add(Ui.Text(item.DisplayTitle, 40, FontWeights.Bold, maxLines: 2));
        var meta = Format.Meta(item);
        var metaRow = Ui.Row(12);
        if (meta.Length > 0) metaRow.Children.Add(Ui.Secondary(meta, 14));
        if (extras?.Ratings is { } r)
        {
            if (r.Imdb is { } imdb) metaRow.Children.Add(Ui.Secondary($"IMDb {imdb:0.0}", 14));
            if (r.RottenTomatoes is { } rt) metaRow.Children.Add(Ui.Secondary($"🍅 {rt}%", 14));
            else if (r.Tmdb is { } tmdb) metaRow.Children.Add(Ui.Secondary($"★ {tmdb:0.0}", 14));
        }
        else if (item.Rating is { } rating) metaRow.Children.Add(Ui.Secondary($"★ {rating:0.0}", 14));
        info.Children.Add(metaRow);

        var files = d.Files ?? [];
        var file = files.FirstOrDefault(f => f.Id == fileId) ?? files.FirstOrDefault();
        if (file != null && file.Badges.Count > 0)
        {
            var badges = Ui.Row(6);
            foreach (var b in file.Badges) badges.Children.Add(Ui.Badge(b));
            info.Children.Add(badges);
        }
        if (!string.IsNullOrEmpty(item.Tagline)) info.Children.Add(Ui.Text(item.Tagline!, 16, FontWeights.SemiBold, Theme.SecondaryText, wrap: true));
        if (!string.IsNullOrEmpty(item.Overview))
        {
            var ov = Ui.Text(item.Overview!, 15, color: Theme.SecondaryText, maxLines: 5);
            ov.MaxWidth = 760;
            ov.HorizontalAlignment = HorizontalAlignment.Left;
            info.Children.Add(ov);
        }
        if (item.Genres is { Count: > 0 } genres)
        {
            var gl = Ui.Row(6);
            foreach (var gname in genres.Take(5))
            {
                var name = gname;
                var b = new HyperlinkButton { Content = name, Padding = new Thickness(0, 0, 8, 0) };
                b.Click += (_, _) => App.Shell?.Push(new GenrePage(name, item.LibraryId));
                gl.Children.Add(b);
            }
            info.Children.Add(gl);
        }
        info.Children.Add(Actions(d, extras, files));
        row.Children.Add(info);
        g.Children.Add(row);
        return g;
    }

    UIElement Actions(ItemDetail d, Extras? extras, List<MediaFileView> files)
    {
        var item = d.Item;
        var api = App.State.Api!;
        var row = Ui.Row(10);
        row.Margin = new Thickness(0, 6, 0, 0);
        if (item.IsPlayable)
        {
            var label = item.InProgress && item.Progress is { } p ? $"Resume · {Format.TimeLeft(p)}" : "Play";
            row.Children.Add(Ui.Button(label, () => Nav.Play(item, fileId: fileId), "", accent: true));
            if (item.InProgress) row.Children.Add(Ui.Button("From Beginning", () => Nav.Play(item, fromStart: true, fileId: fileId), ""));
            if (files.Count > 1)
            {
                var menu = new MenuFlyout();
                var current = fileId ?? files[0].Id;
                foreach (var f in files)
                {
                    var text = f.Label ?? string.Join(" · ", new[] { f.Badges.Count > 0 ? string.Join(" ", f.Badges) : null, Format.Size(f.Size) }.Where(x => x != null));
                    var m = new ToggleMenuFlyoutItem { Text = text, IsChecked = f.Id == current };
                    var fid = f.Id;
                    m.Click += (_, _) => { fileId = fid; Reload(quiet: true); };
                    menu.Items.Add(m);
                }
                row.Children.Add(new DropDownButton { Content = Ui.Label("", "Version"), Flyout = menu, Padding = new Thickness(14, 8, 14, 8), CornerRadius = new CornerRadius(8) });
            }
        }
        else if (item.Kind == "show" && d.NextEpisode is { } next)
        {
            var label = next.InProgress ? $"Resume {next.EpisodeLabel}" : $"Play {next.EpisodeLabel}";
            row.Children.Add(Ui.Button(label, () => Nav.Play(next), "", accent: true));
        }
        if (extras?.Trailer is { } t) row.Children.Add(Ui.Button("Trailer", () => App.Shell?.Push(new TrailerPage(t, item.Title)), ""));
        if (item.Kind is "movie" or "show" && d.OnWatchlist is { } on)
        {
            row.Children.Add(Ui.IconButton(on ? "" : "", on ? "Remove from Watchlist" : "Add to Watchlist", async () =>
            {
                try { await api.SetWatchlist(item.Id, !on); Reload(quiet: true); }
                catch (Exception e) { App.Shell?.ShowError(e.Message); }
            }, 18));
        }
        var watched = item.IsWatched || (item.Kind is "show" or "season" && item.UnwatchedCount == 0 && item.ChildCount > 0);
        row.Children.Add(Ui.IconButton(watched ? "" : "", watched ? "Mark as Unwatched" : "Mark as Watched", async () =>
        {
            try { await api.SetWatched(item.Id, !watched); App.State.Bump(); }
            catch (Exception e) { App.Shell?.ShowError(e.Message); }
        }, 18));
        return row;
    }

    static UIElement Episodes(List<Item> episodes)
    {
        var col = Ui.Column(4);
        col.Margin = new Thickness(Gutter, 0, Gutter, 0);
        col.Children.Add(Ui.Text(episodes.Count == 1 ? "1 Episode" : $"{episodes.Count} Episodes", 20, FontWeights.Bold));
        var api = App.State.Api;
        foreach (var e in episodes.OrderBy(x => x.EpisodeNumber))
        {
            var ep = e;
            var g = new Grid { ColumnSpacing = 20, Padding = new Thickness(8) };
            g.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            g.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            var still = new Grid();
            still.Children.Add(Ui.Image(api?.ImageUrl(ep.StillPath, "w500"), 440));
            if (ep.IsWatched)
                still.Children.Add(new Border
                {
                    Width = 24, Height = 24, CornerRadius = new CornerRadius(12), Background = Theme.EmberBrush, Margin = new Thickness(8),
                    HorizontalAlignment = HorizontalAlignment.Right, VerticalAlignment = VerticalAlignment.Top,
                    Child = Ui.Icon("", 12),
                });
            if (ep.InProgress && ep.Progress is { } p)
            {
                var bar = Ui.Progress(p.Fraction);
                bar.VerticalAlignment = VerticalAlignment.Bottom;
                bar.Margin = new Thickness(8);
                still.Children.Add(bar);
            }
            g.Children.Add(Ui.Frame(still, 240, 135));
            var meta = new List<string> { $"Episode {ep.EpisodeNumber}" };
            if (ep.RuntimeMin is > 0 and var m) meta.Add(Format.Runtime(m));
            if (ep.AirDate is { Length: >= 10 } ad && DateTime.TryParse(ad[..10], out var air)) meta.Add(air.ToString("MMM d, yyyy"));
            if (ep.InProgress && ep.Progress is { } pp) meta.Add(Format.TimeLeft(pp));
            var text = Ui.Column(4,
                Ui.Text(ep.Title, 16, FontWeights.SemiBold),
                Ui.Secondary(string.Join(" · ", meta)),
                Ui.Secondary(ep.Overview ?? "", 14, 3));
            text.VerticalAlignment = VerticalAlignment.Center;
            Grid.SetColumn(text, 1);
            g.Children.Add(text);
            var b = new Button
            {
                Content = g,
                Padding = new Thickness(0),
                HorizontalAlignment = HorizontalAlignment.Stretch,
                HorizontalContentAlignment = HorizontalAlignment.Stretch,
                Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent),
                BorderThickness = new Thickness(0),
                CornerRadius = new CornerRadius(12),
            };
            b.Click += (_, _) => Nav.Play(ep);
            Cards.AttachMenu(b, ep);
            col.Children.Add(b);
        }
        return col;
    }

    static UIElement Person(CastMember c)
    {
        var api = App.State.Api;
        var face = new Border
        {
            Width = 110, Height = 110, CornerRadius = new CornerRadius(55),
            Background = new SolidColorBrush(Theme.Surface),
            Child = c.ProfilePath != null ? Ui.Image(api?.ImageUrl(c.ProfilePath, "w185"), 220) : Ui.Icon("", 36, Theme.TertiaryText),
        };
        var name = Ui.Text(c.Name, 13, FontWeights.SemiBold);
        var role = Ui.Secondary(c.Character ?? "", 12);
        name.TextAlignment = role.TextAlignment = TextAlignment.Center;
        name.HorizontalAlignment = role.HorizontalAlignment = HorizontalAlignment.Center;
        face.HorizontalAlignment = HorizontalAlignment.Center;
        var col = Ui.Column(8, face, name, role);
        col.Width = 120;
        if (c.Id is not { } id) return col;
        var b = Cards.Plain(col);
        ToolTipService.SetToolTip(b, $"See {c.Name}'s movies and shows");
        b.Click += (_, _) => App.Shell?.Push(new PersonPage(id, c.Name));
        return b;
    }
}
