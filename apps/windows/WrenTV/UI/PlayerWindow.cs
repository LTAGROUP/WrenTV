using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Input;
using Windows.System;
using WrenTV.Core;
using WrenTV.Player;
using static WrenTV.Player.PlayerModel;

namespace WrenTV.UI;

/// Plays a movie or episode: seek bar, tracks, versions, chapters, skip
/// intro / credits and the next episode.
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
        var right = Ui.Row(2, skipPanel, chapters, versions, audio, subs, volPanel, fullscreen);
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
        if (Unchanged(subsMenu, string.Join(",", tracks.Select(t => $"{t.Id}{t.Selected}")))) return;
        subsMenu.Items.Clear();
        subsMenu.Items.Add(Choice("Off", !tracks.Any(t => t.Selected), () => model.SelectTrack(null)));
        foreach (var t in tracks) subsMenu.Items.Add(Choice(t.Title, t.Selected, () => model.SelectTrack(t)));
        subs.Visibility = tracks.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
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
