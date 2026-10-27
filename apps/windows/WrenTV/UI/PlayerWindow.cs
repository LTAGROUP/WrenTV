using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Windows.System;
using WrenTV.Core;
using WrenTV.Player;
using static WrenTV.Player.PlayerModel;

namespace WrenTV.UI;

/// Plays a movie or episode: seek bar, tracks, versions, chapters, the
/// season's episodes, subtitle-site search, stats, skip intro / credits and
/// the next episode.
sealed class PlayerWindow : VideoWindow
{
    readonly PlayerModel? model;

    readonly TextBlock title = Ui.Text("", 15, FontWeights.SemiBold);
    readonly TextBlock subtitle = Ui.Secondary("");
    readonly TextBlock elapsed = Ui.Secondary("0:00");
    readonly TextBlock remaining = Ui.Secondary("0:00");
    readonly TextBlock method = Ui.Text("", 11, color: Theme.TertiaryText);
    readonly Slider seek = new() { Minimum = 0, Maximum = 1, StepFrequency = 1, IsThumbToolTipEnabled = true, IsTabStop = false, VerticalAlignment = VerticalAlignment.Center };
    readonly Button play;
    readonly StackPanel skipPanel = Ui.Row(8);
    readonly DropDownButton audio, subs, versions, chapters;
    readonly MenuFlyout audioMenu = new(), subsMenu = new(), versionsMenu = new(), chaptersMenu = new();
    readonly Slider volume;
    readonly Button mute;
    readonly Button fullscreen;
    readonly Button episodes, stats;
    readonly Flyout episodesFlyout = BarFlyout(), statsFlyout = BarFlyout(), searchFlyout = BarFlyout();
    readonly DispatcherTimer statsTimer = new() { Interval = TimeSpan.FromSeconds(1) };
    readonly Grid statsGrid = new() { ColumnSpacing = 16, RowSpacing = 3 };
    bool dragging;
    /// What each menu last showed: menus are only rebuilt when that changes,
    /// so an open menu doesn't close under the viewer.
    readonly Dictionary<object, string> shown = [];

    bool Unchanged(object part, string key)
    {
        if (shown.TryGetValue(part, out var k) && k == key) return true;
        shown[part] = key;
        return false;
    }
    CancellationTokenSource? upNextTimer;

    public static void Open(PlayTarget t, WrenTVApi api)
    {
        var w = new PlayerWindow(t, api);
        w.Activate();
    }

    PlayerWindow(PlayTarget target, WrenTVApi api) : base(target.Title)
    {
        play = BarButton("", "Play / Pause (Space)", () => model?.TogglePause(), 22);
        audio = MenuButton("", "Audio", audioMenu);
        subs = MenuButton("", "Subtitles", subsMenu);
        versions = MenuButton("", "Version", versionsMenu);
        chapters = MenuButton("", "Chapters", chaptersMenu);
        chapters.Content = Ui.Icon("", 18);
        (var volPanel, volume, mute) = VolumeControl(v => model?.SetVolume(v), () => model?.ToggleMute());
        fullscreen = BarButton("", "Full Screen (F)", ToggleFullscreen);
        episodes = BarButton("", "Episodes (E)", () => episodesFlyout.ShowAt(episodes));
        stats = BarButton("", "Stats (I)", () => statsFlyout.ShowAt(stats));
        episodes.Visibility = Visibility.Collapsed;
        episodesFlyout.Opening += (_, _) => { if (model?.Episode is { } ep) _ = ShowEpisodes(ep, ep.ParentId); };
        statsFlyout.Content = Ui.Column(8, Ui.Text("Stats", 15, FontWeights.SemiBold), statsGrid);
        statsFlyout.Opening += (_, _) => { RenderStats(); statsTimer.Start(); };
        statsFlyout.Closed += (_, _) => statsTimer.Stop();
        statsTimer.Tick += (_, _) => RenderStats();
        BuildBar(volPanel);

        if (Mpv == null)
        {
            ShowStatus(Status("Can't play video", StartupError, false, Ui.Button("Close", Close)));
            return;
        }
        model = new PlayerModel(target, api, Mpv);
        model.Changed += Render;
        model.Ticked += Tick;
        model.CloseRequested += () => App.OnUi(Close);
        Render();
        model.Start();
    }

    void BuildBar(UIElement volPanel)
    {
        seek.AddHandler(UIElement.PointerPressedEvent, new PointerEventHandler((_, _) => dragging = true), true);
        seek.AddHandler(UIElement.PointerReleasedEvent, new PointerEventHandler((_, _) => { dragging = false; model?.SeekTo(seek.Value); }), true);
        seek.AddHandler(UIElement.PointerCaptureLostEvent, new PointerEventHandler((_, _) => { if (dragging) { dragging = false; model?.SeekTo(seek.Value); } }), true);
        seek.ThumbToolTipValueConverter = new TimeConverter();

        var timeline = new Grid { ColumnSpacing = 12 };
        timeline.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(64) });
        timeline.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        timeline.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(64) });
        elapsed.VerticalAlignment = remaining.VerticalAlignment = VerticalAlignment.Center;
        remaining.HorizontalAlignment = HorizontalAlignment.Right;
        timeline.Children.Add(elapsed);
        Grid.SetColumn(seek, 1);
        timeline.Children.Add(seek);
        Grid.SetColumn(remaining, 2);
        timeline.Children.Add(remaining);

        var controls = new Grid { ColumnSpacing = 16 };
        controls.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        controls.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        controls.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        var left = Ui.Row(2,
            BarButton("", "Back 10 seconds (Left)", () => model?.SeekBy(-10)),
            play,
            BarButton("", "Forward 30 seconds (Right)", () => model?.SeekBy(30)));
        left.VerticalAlignment = VerticalAlignment.Center;
        controls.Children.Add(left);
        var titles = Ui.Column(1, title, subtitle, method);
        titles.VerticalAlignment = VerticalAlignment.Center;
        Grid.SetColumn(titles, 1);
        controls.Children.Add(titles);
        var right = Ui.Row(2, skipPanel, episodes, chapters, versions, audio, subs, stats, volPanel, fullscreen);
        skipPanel.Margin = new Thickness(0, 0, 10, 0);
        skipPanel.VerticalAlignment = VerticalAlignment.Center;
        right.VerticalAlignment = VerticalAlignment.Center;
        Grid.SetColumn(right, 2);
        controls.Children.Add(right);
        Bar.Child = Ui.Column(2, timeline, controls);
    }

    sealed class TimeConverter : Microsoft.UI.Xaml.Data.IValueConverter
    {
        public object Convert(object value, Type targetType, object parameter, string language) => Format.Duration(System.Convert.ToDouble(value));
        public object ConvertBack(object value, Type targetType, object parameter, string language) => throw new NotSupportedException();
    }

    protected override void Stopping()
    {
        upNextTimer?.Cancel();
        statsTimer.Stop();
        model?.Stop();
    }

    protected override void PlayPause() => model?.TogglePause();

    protected override bool Key(VirtualKey k)
    {
        if (model == null) return base.Key(k);
        switch (k)
        {
            case VirtualKey.Space or VirtualKey.K: model.TogglePause(); return true;
            case VirtualKey.Left or VirtualKey.J: model.SeekBy(-10); ShowBar(); return true;
            case VirtualKey.Right or VirtualKey.L: model.SeekBy(30); ShowBar(); return true;
            case VirtualKey.Up: model.SetVolume(model.Volume + 5); return true;
            case VirtualKey.Down: model.SetVolume(model.Volume - 5); return true;
            case VirtualKey.M: model.ToggleMute(); return true;
            case VirtualKey.S:
                if (model.Skip == SkipKind.Intro) model.SkipIntro();
                else if (model.Skip == SkipKind.Credits) model.SkipCredits();
                return true;
            case VirtualKey.N when model.UpNext != null: model.PlayNext(); return true;
            case VirtualKey.E when model.Episode != null: ShowBar(); episodesFlyout.ShowAt(episodes); return true;
            case VirtualKey.I: ShowBar(); statsFlyout.ShowAt(stats); return true;
            case VirtualKey.Escape when !IsFullscreen: Close(); return true;
        }
        return base.Key(k);
    }

    void Tick()
    {
        if (model == null) return;
        if (model.Duration > 0 && seek.Maximum != model.Duration) seek.Maximum = model.Duration;
        if (!dragging) seek.Value = Math.Min(model.Position, seek.Maximum);
        elapsed.Text = Format.Duration(model.Position);
        remaining.Text = model.Duration > 0 ? "-" + Format.Duration(model.Duration - model.Position) : "";
        var chapter = model.CurrentChapter;
        if (chapter != null && chapters.Tag as Chapter != chapter)
        {
            chapters.Tag = chapter;
            RenderChapters();
        }
    }

    void Render()
    {
        if (model == null) return;
        title.Text = model.Title;
        Title = model.Subtitle is { } s && model.Title != s ? $"{model.Title} · {s}" : model.Title;
        subtitle.Text = model.Subtitle ?? "";
        subtitle.Visibility = model.Subtitle is { Length: > 0 } ? Visibility.Visible : Visibility.Collapsed;
        method.Text = model.Buffering ? "Buffering…" : model.MethodLabel;
        play.Content = Ui.Icon(model.Paused ? "" : "", 22);
        ShowVolume(volume, mute, model.Volume, model.Muted);
        fullscreen.Content = Ui.Icon(IsFullscreen ? "" : "", 18);

        switch (model.Phase)
        {
            case PhaseKind.Loading:
                upNextTimer?.Cancel();
                ShowStatus(Status("Loading…", model.Subtitle ?? model.Title, true));
                break;
            case PhaseKind.Pending:
                ShowStatus(Status("Getting it ready", model.Message, true, Ui.Button("Cancel", Close)));
                break;
            case PhaseKind.Error:
                ShowStatus(Status("Playback failed", model.Message, false,
                    Ui.Button("Try Again", model.Retry, "", accent: true), Ui.Button("Close", Close)));
                break;
            case PhaseKind.Ended:
                RenderEnded();
                break;
            default:
                ShowVideo(true);
                break;
        }

        var skipKey = model.Phase == PhaseKind.Playing ? $"{model.Skip}|{model.Countdown}" : "";
        if (!Unchanged(skipPanel, skipKey)) RenderSkip();

        episodes.Visibility = model.Episode != null ? Visibility.Visible : Visibility.Collapsed;
        RenderAudio();
        RenderSubtitles();
        RenderVersions();
        RenderChapters();
    }

    void RenderSkip()
    {
        if (model == null) return;
        skipPanel.Children.Clear();
        if (model.Phase == PhaseKind.Playing)
        {
            if (model.Skip == SkipKind.Intro)
                skipPanel.Children.Add(Ui.Button("Skip Intro", model.SkipIntro, "", accent: true));
            else if (model.Skip == SkipKind.Credits)
            {
                skipPanel.Children.Add(Ui.Button(model.Countdown is { } n ? $"Next Episode in {n}" : "Skip Credits", model.SkipCredits, "", accent: true));
                skipPanel.Children.Add(Ui.Button("Watch Credits", model.DismissCredits));
            }
        }
        foreach (var b in skipPanel.Children.OfType<Button>()) b.IsTabStop = false;
    }

    void RenderEnded()
    {
        if (model == null) return;
        if (model.UpNext is not { } next)
        {
            upNextTimer?.Cancel();
            ShowStatus(Status("Finished", model.Subtitle ?? model.Title, false, Ui.Button("Close", Close, accent: true)));
            return;
        }
        if (upNextTimer != null) return;
        var cts = upNextTimer = new CancellationTokenSource();
        var api = App.State.Api;
        var label = Ui.Secondary("", 14);
        var still = Ui.Frame(Ui.Image(api?.ImageUrl(next.StillPath ?? next.BackdropPath, "w780"), 800), 400, 225, 12);
        var panel = Ui.Column(14,
            Ui.Text("Up Next", 14, FontWeights.SemiBold, Theme.EmberBrush),
            still,
            Ui.Text($"{next.EpisodeLabel} · {next.Title}", 20, FontWeights.SemiBold),
            label,
            Ui.Row(10,
                Ui.Button("Play Now", () => { cts.Cancel(); upNextTimer = null; model.PlayNext(); }, "", accent: true),
                Ui.Button("Close", Close)));
        panel.HorizontalAlignment = HorizontalAlignment.Center;
        panel.VerticalAlignment = VerticalAlignment.Center;
        ShowStatus(panel);
        App.OnUi(async () =>
        {
            for (var n = 10; n > 0; n--)
            {
                label.Text = $"Starts in {n} seconds";
                try { await Task.Delay(1000, cts.Token); } catch { return; }
            }
            upNextTimer = null;
            model.PlayNext();
        });
    }

    void RenderAudio()
    {
        if (model == null) return;
        var key = model.SwitchesAudioInPlayer
            ? "t" + string.Join(",", model.Tracks.Where(t => t.Type == "audio").Select(t => $"{t.Id}{t.Selected}"))
            : "s" + string.Join(",", model.AudioStreams.Select(s => s.TypeIndex)) + "|" + model.AudioIndex;
        if (Unchanged(audioMenu, key)) return;
        audioMenu.Items.Clear();
        if (model.SwitchesAudioInPlayer)
        {
            var tracks = model.Tracks.Where(t => t.Type == "audio").ToList();
            foreach (var t in tracks) audioMenu.Items.Add(Choice(t.Title, t.Selected, () => model.SelectTrack(t)));
            audio.Visibility = tracks.Count > 1 ? Visibility.Visible : Visibility.Collapsed;
        }
        else
        {
            foreach (var s in model.AudioStreams) audioMenu.Items.Add(Choice(s.Label, s.TypeIndex == model.AudioIndex, () => model.SelectAudio(s.TypeIndex)));
            audio.Visibility = model.AudioStreams.Count > 1 ? Visibility.Visible : Visibility.Collapsed;
        }
    }

    void RenderSubtitles()
    {
        if (model == null) return;
        var tracks = model.Tracks.Where(t => t.Type == "sub").ToList();
        var key = string.Join(",", tracks.Select(t => $"{t.Id}{t.Selected}")) + "|" + model.OnlineSubtitles + "|" + string.Join(",", model.SideTracks.Select(t => t.Index));
        if (Unchanged(subsMenu, key)) return;
        subsMenu.Items.Clear();
        subsMenu.Items.Add(Choice("Off", !tracks.Any(t => t.Selected), () => model.SelectTrack(null)));
        foreach (var t in tracks) subsMenu.Items.Add(Choice(t.Title, t.Selected, () => model.SelectTrack(t)));
        if (model.SideTracks.Count > 0 || model.OnlineSubtitles)
        {
            subsMenu.Items.Add(new MenuFlyoutSeparator());
            subsMenu.Items.Add(Header("Online"));
            foreach (var t in model.SideTracks)
            {
                var item = new MenuFlyoutItem { Text = t.Title ?? t.Language ?? "Subtitles" };
                item.Click += (_, _) => model.ShowSideTrack(t);
                subsMenu.Items.Add(item);
            }
            if (model.OnlineSubtitles)
            {
                var search = new MenuFlyoutItem { Text = "Search Online…", Icon = new FontIcon { Glyph = "" } };
                search.Click += (_, _) => _ = SearchOnline();
                subsMenu.Items.Add(search);
            }
        }
        subs.Visibility = tracks.Count > 0 || model.SideTracks.Count > 0 || model.OnlineSubtitles ? Visibility.Visible : Visibility.Collapsed;
    }

    static Flyout BarFlyout()
    {
        var f = new Flyout { Placement = FlyoutPlacementMode.Top, ShouldConstrainToRootBounds = false };
        var style = new Style(typeof(FlyoutPresenter));
        style.Setters.Add(new Setter(FrameworkElement.MaxWidthProperty, 1100.0));
        style.Setters.Add(new Setter(FrameworkElement.MaxHeightProperty, 560.0));
        f.FlyoutPresenterStyle = style;
        return f;
    }

    /// The season's episodes in a row, with the show's other seasons above it.
    async Task ShowEpisodes(Item ep, string? seasonId)
    {
        if (model == null || seasonId == null) return;
        var api = App.State.Api;
        var column = Ui.Column(12, Ui.Spinner());
        episodesFlyout.Content = column;
        var seasonsTask = ep.ShowId is { } show ? model.Children(show) : Task.FromResult(new List<Item>());
        var eps = (await model.Children(seasonId)).OrderBy(e => e.EpisodeNumber).ToList();
        var seasons = (await seasonsTask).OrderBy(x => x.SeasonNumber).ToList();
        if (episodesFlyout.Content != column) return;
        column.Children.Clear();
        if (seasons.Count > 1)
        {
            var row = Ui.Row(6);
            foreach (var se in seasons)
            {
                var b = Ui.Button(se.DisplayTitle, () => _ = ShowEpisodes(ep, se.Id), accent: se.Id == seasonId);
                b.Padding = new Thickness(12, 5, 12, 5);
                row.Children.Add(b);
            }
            column.Children.Add(new ScrollViewer { Content = row, HorizontalScrollBarVisibility = ScrollBarVisibility.Auto, VerticalScrollBarVisibility = ScrollBarVisibility.Disabled, HorizontalScrollMode = ScrollMode.Enabled });
        }
        var cards = Ui.Row(12);
        FrameworkElement? current = null;
        foreach (var e in eps)
        {
            var playing = e.Id == model.ItemId;
            var frame = new Grid { Width = 220, Height = 124 };
            frame.Children.Add(Ui.Frame(Ui.Image(api?.ImageUrl(e.StillPath ?? e.BackdropPath, "w300"), 300), 220, 124, 8));
            if (e.Progress is { Position: > 0, Watched: false } pr && !playing)
            {
                var bar = Ui.Progress(pr.Fraction);
                bar.VerticalAlignment = VerticalAlignment.Bottom;
                frame.Children.Add(bar);
            }
            if (playing)
            {
                frame.Children.Add(new Border
                {
                    Background = Theme.EmberBrush, CornerRadius = new CornerRadius(4), Padding = new Thickness(6, 1, 6, 2), Margin = new Thickness(6),
                    HorizontalAlignment = HorizontalAlignment.Left, VerticalAlignment = VerticalAlignment.Top, Child = Ui.Text("Playing", 11, FontWeights.SemiBold),
                });
                frame.Children.Add(new Border { BorderBrush = Theme.EmberBrush, BorderThickness = new Thickness(2), CornerRadius = new CornerRadius(8) });
            }
            else if (e.IsWatched)
            {
                var check = Ui.Icon("", 16, Theme.EmberBrush);
                check.HorizontalAlignment = HorizontalAlignment.Right;
                check.VerticalAlignment = VerticalAlignment.Top;
                check.Margin = new Thickness(8);
                frame.Children.Add(check);
            }
            var cell = Ui.Column(4, frame, Ui.Text($"{e.EpisodeNumber}. {e.Title}", 13, maxLines: 1));
            if (e.RuntimeMin is int min && min > 0) cell.Children.Add(Ui.Secondary(Format.Runtime(min), 12));
            cell.Width = 220;
            var card = new Button
            {
                Content = cell, Padding = new Thickness(4), Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent),
                BorderThickness = new Thickness(0), IsEnabled = !playing,
            };
            var pick = e;
            card.Click += (_, _) => { episodesFlyout.Hide(); model.PlayEpisode(pick); };
            cards.Children.Add(card);
            if (playing) current = card;
        }
        column.Children.Add(new ScrollViewer { Content = cards, HorizontalScrollBarVisibility = ScrollBarVisibility.Auto, VerticalScrollBarVisibility = ScrollBarVisibility.Disabled, HorizontalScrollMode = ScrollMode.Enabled });
        if (current is { } c)
            c.Loaded += (_, _) => c.StartBringIntoView(new BringIntoViewOptions { HorizontalAlignmentRatio = 0, AnimationDesired = false });
    }

    void RenderStats()
    {
        if (model == null) return;
        statsGrid.Children.Clear();
        statsGrid.RowDefinitions.Clear();
        if (statsGrid.ColumnDefinitions.Count == 0)
        {
            statsGrid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            statsGrid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            statsGrid.MinWidth = 360;
        }
        var rows = model.Stats();
        for (var i = 0; i < rows.Count; i++)
        {
            statsGrid.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
            var k = Ui.Secondary(rows[i].Item1, 12);
            var v = Ui.Text(rows[i].Item2, 12, wrap: true);
            v.IsTextSelectionEnabled = true;
            Grid.SetRow(k, i);
            Grid.SetRow(v, i);
            Grid.SetColumn(v, 1);
            statsGrid.Children.Add(k);
            statsGrid.Children.Add(v);
        }
    }

    /// Searches the subtitle sites and lists what they have; picking one downloads it.
    async Task SearchOnline()
    {
        if (model == null) return;
        var column = Ui.Column(10, Ui.Text("Online Subtitles", 15, FontWeights.SemiBold), Ui.Spinner("Searching…"));
        column.Width = 520;
        searchFlyout.Content = column;
        searchFlyout.ShowAt(subs);
        List<OnlineSubtitle> found;
        try { found = await model.SearchOnlineSubtitles(); }
        catch (Exception e)
        {
            column.Children.RemoveAt(1);
            column.Children.Add(Ui.Secondary(e.Message, 13, 4));
            return;
        }
        if (searchFlyout.Content != column) return;
        column.Children.RemoveAt(1);
        if (found.Count == 0)
        {
            column.Children.Add(Ui.Secondary("Nothing found.", 13));
            return;
        }
        var list = Ui.Column(2);
        foreach (var r in found.Take(60))
        {
            var label = Ui.Column(1, Ui.Text(r.Name.Length > 0 ? r.Name : r.Provider, 13, maxLines: 2), Ui.Secondary(r.Detail, 12));
            var b = new Button
            {
                Content = label, HorizontalAlignment = HorizontalAlignment.Stretch, HorizontalContentAlignment = HorizontalAlignment.Left,
                Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent), BorderThickness = new Thickness(0), Padding = new Thickness(10, 6, 10, 6),
            };
            var pick = r;
            b.Click += async (_, _) =>
            {
                foreach (var x in list.Children.OfType<Button>()) x.IsEnabled = false;
                try
                {
                    await model.AddOnlineSubtitle(pick);
                    searchFlyout.Hide();
                }
                catch (Exception e)
                {
                    column.Children.Add(Ui.Secondary(e.Message, 12, 3));
                    foreach (var x in list.Children.OfType<Button>()) x.IsEnabled = true;
                }
            };
            list.Children.Add(b);
        }
        column.Children.Add(new ScrollViewer { Content = list, MaxHeight = 420 });
    }

    void RenderVersions()
    {
        if (model == null) return;
        if (Unchanged(versionsMenu, string.Join(",", model.Versions.Select(v => v.Id)) + "|" + model.FileId)) return;
        versionsMenu.Items.Clear();
        foreach (var v in model.Versions) versionsMenu.Items.Add(Choice(v.Label, v.Id == model.FileId, () => model.SelectVersion(v.Id)));
        versions.Visibility = model.Versions.Count > 1 ? Visibility.Visible : Visibility.Collapsed;
    }

    void RenderChapters()
    {
        if (model == null) return;
        var current = model.CurrentChapter;
        if (Unchanged(chaptersMenu, model.Chapters.Count + "|" + (current?.Start ?? -1))) return;
        chaptersMenu.Items.Clear();
        var n = 0;
        foreach (var c in model.Chapters)
        {
            n++;
            var name = string.IsNullOrWhiteSpace(c.Title) ? $"Chapter {n}" : c.Title;
            chaptersMenu.Items.Add(Choice($"{name}  ·  {Format.Duration(c.Start)}", c == current, () => model.SeekTo(c.Start)));
        }
        chapters.Visibility = model.Chapters.Count > 1 ? Visibility.Visible : Visibility.Collapsed;
    }

    protected override void OnFullscreenChanged() => Render();
}
