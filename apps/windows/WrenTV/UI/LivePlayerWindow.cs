using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Windows.System;
using WrenTV.Core;
using WrenTV.Player;

namespace WrenTV.UI;

/// Watches a live channel, with channel up / down and the channel list.
sealed class LivePlayerWindow : VideoWindow
{
    readonly LivePlayerModel? model;
    readonly TextBlock channelName = Ui.Text("", 15, FontWeights.SemiBold);
    readonly TextBlock programme = Ui.Secondary("");
    readonly ProgressBar progress = Ui.Progress(0);
    readonly Border logo = new() { Width = 72, Height = 40, CornerRadius = new CornerRadius(6), Padding = new Thickness(4), Background = new SolidColorBrush(Theme.Surface) };
    readonly Button play, mute, fullscreen;
    readonly Slider volume;
    readonly MenuFlyout channelMenu = new();
    readonly DispatcherTimer clock = new() { Interval = TimeSpan.FromSeconds(30) };
    string? logoFor;

    public static void Open(LiveChannel channel, List<LiveChannel> channels, WrenTVApi api)
    {
        var w = new LivePlayerWindow(channel, channels, api);
        w.Activate();
    }

    LivePlayerWindow(LiveChannel channel, List<LiveChannel> channels, WrenTVApi api) : base(channel.Name)
    {
        play = BarButton("", "Pause (Space)", () => model?.TogglePause(), 22);
        (var volPanel, volume, mute) = VolumeControl(v => model?.SetVolume(v), () => model?.ToggleMute());
        fullscreen = BarButton("", "Full Screen (F)", ToggleFullscreen);
        var list = MenuButton("", "Channels", channelMenu);

        var g = new Grid { ColumnSpacing = 16 };
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        var left = Ui.Row(2,
            BarButton("", "Previous Channel (Page Up)", () => model?.Step(-1)),
            play,
            BarButton("", "Next Channel (Page Down)", () => model?.Step(1)));
        left.VerticalAlignment = VerticalAlignment.Center;
        g.Children.Add(left);
        logo.VerticalAlignment = VerticalAlignment.Center;
        Grid.SetColumn(logo, 1);
        g.Children.Add(logo);
        progress.Width = 220;
        progress.HorizontalAlignment = HorizontalAlignment.Left;
        var info = Ui.Column(3, channelName, programme, progress);
        info.VerticalAlignment = VerticalAlignment.Center;
        Grid.SetColumn(info, 2);
        g.Children.Add(info);
        var right = Ui.Row(2, list, volPanel, fullscreen);
        right.VerticalAlignment = VerticalAlignment.Center;
        Grid.SetColumn(right, 3);
        g.Children.Add(right);
        Bar.Child = g;
        Bar.Padding = new Thickness(20, 12, 20, 12);

        if (Mpv == null)
        {
            ShowStatus(Status("Can't play video", StartupError, false, Ui.Button("Close", Close)));
            return;
        }
        model = new LivePlayerModel(api, Mpv, channel, channels);
        model.Changed += Render;
        BuildChannelMenu();
        clock.Tick += (_, _) => Render();
        clock.Start();
        model.Tune(channel);
    }

    void BuildChannelMenu()
    {
        if (model == null) return;
        channelMenu.Items.Clear();
        foreach (var c in model.Channels.Take(400))
        {
            var ch = c;
            var text = string.IsNullOrEmpty(c.Number) ? c.Name : $"{c.Number}  {c.Name}";
            if (c.Now is { } p) text += $"  ·  {p.Title}";
            channelMenu.Items.Add(Choice(text, c.Id == model.Channel.Id, () => model.Tune(ch)));
        }
    }

    protected override void Stopping()
    {
        clock.Stop();
        model?.Stop();
    }

    protected override void PlayPause() => model?.TogglePause();

    protected override bool Key(VirtualKey k)
    {
        if (model == null) return base.Key(k);
        switch (k)
        {
            case VirtualKey.Space or VirtualKey.K: model.TogglePause(); return true;
            case VirtualKey.PageUp or VirtualKey.P: model.Step(-1); ShowBar(); return true;
            case VirtualKey.PageDown or VirtualKey.N: model.Step(1); ShowBar(); return true;
            case VirtualKey.Up: model.SetVolume(model.Volume + 5); return true;
            case VirtualKey.Down: model.SetVolume(model.Volume - 5); return true;
            case VirtualKey.M: model.ToggleMute(); return true;
            case VirtualKey.Escape when !IsFullscreen: Close(); return true;
        }
        return base.Key(k);
    }

    // Channel up / down from mpv's keys on the video.
    protected override void Input(string action)
    {
        switch (action)
        {
            case "prev" or "back": model?.Step(-1); ShowBar(); break;
            case "next" or "forward": model?.Step(1); ShowBar(); break;
            default: base.Input(action); break;
        }
    }

    void Render()
    {
        if (model == null) return;
        var c = model.Channel;
        Title = c.Name;
        channelName.Text = string.IsNullOrEmpty(c.Number) ? c.Name : $"{c.Number} · {c.Name}";
        if (c.Now is { } p)
        {
            programme.Text = $"{p.Title} · {p.TimeRange}";
            progress.Value = p.ProgressAt(DateTimeOffset.Now);
            progress.Visibility = Visibility.Visible;
        }
        else
        {
            programme.Text = "";
            progress.Visibility = Visibility.Collapsed;
        }
        if (logoFor != c.Id)
        {
            logoFor = c.Id;
            logo.Child = App.State.Api?.LiveLogoUrl(c.Logo) is { } u ? Ui.Image(u, 144, Stretch.Uniform) : null;
            BuildChannelMenu();
        }
        play.Content = Ui.Icon(model.Paused ? "" : "", 22);
        ShowVolume(volume, mute, model.Volume, model.Muted);
        fullscreen.Content = Ui.Icon(IsFullscreen ? "" : "", 18);
        if (model.Error is { } e)
            ShowStatus(Status("Can't play this channel", e, false,
                Ui.Button("Try Again", model.Retry, "", accent: true), Ui.Button("Next Channel", () => model.Step(1))));
        else if (model.Loading)
            ShowStatus(Status("Tuning…", c.Name, true));
        else
            ShowVideo(true);
    }

    protected override void OnFullscreenChanged() => Render();
}
