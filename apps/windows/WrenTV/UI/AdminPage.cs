using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using WrenTV.Core;

namespace WrenTV.UI;

/// For the server's owner and admins: libraries, people and downloads.
/// Everything else lives in the server's web app.
sealed class AdminPage : Page
{
    string tab = "libraries";
    CancellationTokenSource? poll;

    public AdminPage()
    {
        Unloaded += (_, _) => poll?.Cancel();
    }

    protected override async Task<UIElement> Build()
    {
        poll?.Cancel();
        var api = App.State.Api!;
        UIElement content = tab switch
        {
            "users" => Users(await api.Users()),
            "downloads" => Downloads(await api.ServerDownloads()),
            _ => Libraries(await api.Libraries()),
        };

        var g = new Grid { RowSpacing = 20, Padding = new Thickness(0, 24, 0, 0) };
        g.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        g.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        var header = new Grid { Margin = new Thickness(Gutter, 0, Gutter, 0) };
        header.Children.Add(Ui.Column(4, Ui.Title(App.State.Server?.Name ?? "Server", 30), Ui.Secondary("Manage your server", 14)));
        var right = Ui.Row(16);
        right.HorizontalAlignment = HorizontalAlignment.Right;
        right.VerticalAlignment = VerticalAlignment.Center;
        var tabs = new SelectorBar();
        foreach (var (key, name) in new[] { ("libraries", "Libraries"), ("users", "Users"), ("downloads", "Downloads") })
            tabs.Items.Add(new SelectorBarItem { Text = name, Tag = key, IsSelected = key == tab });
        tabs.SelectionChanged += (s, _) =>
        {
            if (s.SelectedItem?.Tag is string t && t != tab) { tab = t; Reload(); }
        };
        right.Children.Add(tabs);
        right.Children.Add(Ui.Button("Open Web Admin", () => Ui.Open(api.BaseUrl), ""));
        header.Children.Add(right);
        g.Children.Add(header);
        var scroll = new ScrollViewer { Content = content };
        Grid.SetRow(scroll, 1);
        g.Children.Add(scroll);
        return g;
    }

    static StackPanel List()
    {
        var p = Ui.Column(10);
        p.Margin = new Thickness(Gutter, 0, Gutter, 40);
        p.MaxWidth = 960;
        p.HorizontalAlignment = HorizontalAlignment.Left;
        return p;
    }

    static Grid RowGrid(params GridLength[] cols)
    {
        var g = new Grid { ColumnSpacing = 16 };
        foreach (var c in cols) g.ColumnDefinitions.Add(new ColumnDefinition { Width = c });
        return g;
    }

    static readonly GridLength Auto = GridLength.Auto;
    static readonly GridLength Star = new(1, GridUnitType.Star);

    static T At<T>(T e, int col) where T : FrameworkElement
    {
        Grid.SetColumn(e, col);
        e.VerticalAlignment = VerticalAlignment.Center;
        return e;
    }

    // Libraries

    UIElement Libraries(List<Library> libs)
    {
        var p = List();
        if (libs.Count == 0)
            p.Children.Add(Ui.Secondary("No libraries yet. Add one in the web admin.", 14));
        foreach (var l in libs)
        {
            var g = RowGrid(Auto, Star, Auto);
            g.Children.Add(At(Ui.Icon(l.Glyph, 22, Theme.EmberBrush), 0));
            var scanned = l.LastScanAt is { } t ? "Scanned " + DateTimeOffset.FromUnixTimeSeconds(t > 10_000_000_000 ? t / 1000 : t).LocalDateTime.ToString("g") : "Not scanned yet";
            var detail = l.IsStream ? "Streaming library" : string.Join(", ", l.Paths);
            var count = l.ItemCount is { } n ? $"{n} {(l.Type == "shows" ? "shows" : "movies")} · " : "";
            g.Children.Add(At(Ui.Column(3, Ui.Text(l.Name, 16, FontWeights.SemiBold), Ui.Secondary(count + scanned), Ui.Secondary(detail, 12)), 1));
            if (!l.IsStream)
            {
                var lib = l;
                Button? scan = null;
                scan = Ui.Button("Scan", async () =>
                {
                    scan!.IsEnabled = false;
                    try
                    {
                        await App.State.Api!.ScanLibrary(lib.Id);
                        scan.Content = Ui.Label("", "Scanning");
                    }
                    catch (Exception e) { App.Shell?.ShowError(e.Message); scan.IsEnabled = true; }
                }, "");
                g.Children.Add(At(scan, 2));
            }
            p.Children.Add(Ui.Card(g, 16));
        }
        return p;
    }

    // Users

    UIElement Users(List<ServerUser> users)
    {
        var p = List();
        p.Children.Add(Ui.Secondary("Share the server by inviting people's email addresses in the web admin. Limits apply to each person's streams and downloads.", 13, 3));
        foreach (var u in users)
        {
            var g = RowGrid(Star, Auto);
            var name = u.DisplayName.Length > 0 ? u.DisplayName : u.Username;
            var role = u.IsAdmin ? "Admin" : "Member";
            var limits = u.Effective;
            var summary = $"{role} · {(limits.CanStream ? "Can stream" : "No streaming")} · {(limits.MaxStreams > 0 ? $"{limits.MaxStreams} streams at once" : "Unlimited streams")}";
            g.Children.Add(At(Ui.Column(3, Ui.Text(name, 16, FontWeights.SemiBold), Ui.Secondary(u.Email ?? u.Username), Ui.Secondary(summary, 12)), 0));
            if (!u.IsAdmin)
            {
                var user = u;
                g.Children.Add(At(Ui.Button("Limits", () => EditLimits(user), ""), 1));
            }
            p.Children.Add(Ui.Card(g, 16));
        }
        return p;
    }

    async void EditLimits(ServerUser u)
    {
        var streaming = new ToggleSwitch { Header = "Streaming from the internet", IsOn = u.AllowStreaming };
        var downloads = new ComboBox { Header = "Downloads", Width = 240 };
        foreach (var (v, label) in new[] { (-1, "Server default"), (1, "Allowed"), (0, "Not allowed") })
            downloads.Items.Add(new ComboBoxItem { Content = label, Tag = v });
        downloads.SelectedItem = downloads.Items.Cast<ComboBoxItem>().FirstOrDefault(i => (int)i.Tag == u.AllowDownloads) ?? downloads.Items[0];
        var streams = new NumberBox { Header = "Streams at once (0 = no limit, -1 = server default)", Value = u.MaxStreams, Minimum = -1, Maximum = 20, SpinButtonPlacementMode = NumberBoxSpinButtonPlacementMode.Inline };
        var dls = new NumberBox { Header = "Downloads at once (0 = no limit, -1 = server default)", Value = u.MaxDownloads, Minimum = -1, Maximum = 50, SpinButtonPlacementMode = NumberBoxSpinButtonPlacementMode.Inline };
        var d = new ContentDialog
        {
            XamlRoot = XamlRoot,
            Title = $"Limits for {(u.DisplayName.Length > 0 ? u.DisplayName : u.Username)}",
            Content = Ui.Column(16, streaming, downloads, streams, dls),
            PrimaryButtonText = "Save",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Primary,
        };
        if (await d.ShowAsync() != ContentDialogResult.Primary) return;
        try
        {
            await App.State.Api!.SetUserLimits(u.Id, new UserLimitsPatch
            {
                AllowStreaming = streaming.IsOn,
                AllowDownloads = (downloads.SelectedItem as ComboBoxItem)?.Tag is int a ? a : -1,
                MaxStreams = double.IsNaN(streams.Value) ? -1 : (int)streams.Value,
                MaxDownloads = double.IsNaN(dls.Value) ? -1 : (int)dls.Value,
            });
            Reload(quiet: true);
        }
        catch (Exception e) { App.Shell?.ShowError(e.Message); }
    }

    // Downloads

    UIElement Downloads(List<ServerDownload> list)
    {
        var p = List();
        if (list.Count == 0)
        {
            p.Children.Add(Ui.Secondary("Nothing downloading. Streamed titles the server saves for later show up here.", 14, 3));
            return p;
        }
        foreach (var dl in list.OrderByDescending(x => x.Active).ThenByDescending(x => x.CreatedAt))
        {
            var g = RowGrid(Star, Auto);
            var info = Ui.Column(4, Ui.Text(dl.Title, 15, FontWeights.SemiBold));
            var status = dl.Status switch
            {
                "downloading" => dl.Total > 0 ? $"{Format.Size(dl.Bytes)} of {Format.Size(dl.Total)} · {dl.Fraction:P0}" : "Downloading",
                "queued" => "Waiting",
                "done" or "completed" => "Done" + (dl.Total > 0 ? $" · {Format.Size(dl.Total)}" : ""),
                "failed" or "error" => "Failed" + (dl.Error is { } e ? $": {e}" : ""),
                "cancelled" => "Cancelled",
                _ => dl.Status,
            };
            info.Children.Add(Ui.Secondary(string.Join(" · ", new[] { dl.Quality, status }.Where(x => x.Length > 0)), 12, 2));
            if (dl.Active)
            {
                var bar = Ui.Progress(dl.Fraction);
                bar.MaxWidth = 520;
                bar.HorizontalAlignment = HorizontalAlignment.Left;
                info.Children.Add(bar);
            }
            g.Children.Add(At(info, 0));
            var id = dl.Id;
            if (dl.Active)
                g.Children.Add(At(Ui.Button("Cancel", () => Act(() => App.State.Api!.CancelServerDownload(id)), ""), 1));
            else if (dl.Status is "failed" or "error" or "cancelled")
                g.Children.Add(At(Ui.Button("Retry", () => Act(() => App.State.Api!.RetryServerDownload(id)), ""), 1));
            p.Children.Add(Ui.Card(g, 16));
        }
        // Keep the progress moving while something's downloading.
        if (list.Any(x => x.Active))
        {
            var cts = poll = new CancellationTokenSource();
            App.OnUi(async () =>
            {
                try { await Task.Delay(3000, cts.Token); } catch { return; }
                if (tab == "downloads") Reload(quiet: true);
            });
        }
        return p;
    }

    async void Act(Func<Task> f)
    {
        try { await f(); Reload(quiet: true); }
        catch (Exception e) { App.Shell?.ShowError(e.Message); }
    }
}
