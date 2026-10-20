using Microsoft.UI.Text;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Windows.System;
using WrenTV.Core;
using WrenTV.Player;

namespace WrenTV.UI;

/// A window with mpv drawing into a native child window and a control bar
/// underneath. XAML can't draw over the video, so anything shown instead of
/// it (loading, errors, up next) hides the video window first.
abstract class VideoWindow : Window
{
    static readonly List<VideoWindow> open = [];

    protected readonly Grid Root = new() { Background = new SolidColorBrush(Microsoft.UI.Colors.Black) };
    protected readonly Grid VideoArea = new() { Background = new SolidColorBrush(Microsoft.UI.Colors.Black) };
    /// Shown in place of the video while it's hidden.
    protected readonly Grid Overlay = new();
    protected readonly Border Bar = new()
    {
        Background = new SolidColorBrush(Theme.Ink),
        BorderBrush = Theme.CardBorder,
        BorderThickness = new Thickness(0, 1, 0, 0),
        Padding = new Thickness(20, 8, 20, 12),
    };
    protected Mpv? Mpv { get; }
    readonly IntPtr video;
    bool videoShown = true;
    bool closed;
    readonly DispatcherTimer hideBar = new() { Interval = TimeSpan.FromSeconds(3) };

    protected VideoWindow(string title)
    {
        Title = title;
        AppWindow.SetIcon(Path.Combine(AppContext.BaseDirectory, "Assets", "WrenTV.ico"));
        AppWindow.Resize(new Windows.Graphics.SizeInt32(1280, 800));
        if (AppWindow.TitleBar is { } tb)
        {
            tb.BackgroundColor = Theme.Ink;
            tb.InactiveBackgroundColor = Theme.Ink;
            tb.ButtonBackgroundColor = Theme.Ink;
            tb.ButtonInactiveBackgroundColor = Theme.Ink;
            tb.ForegroundColor = Theme.Cream;
            tb.ButtonForegroundColor = Theme.Cream;
        }
        Root.RequestedTheme = ElementTheme.Dark;
        Root.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        Root.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        Overlay.Visibility = Visibility.Collapsed;
        VideoArea.Children.Add(Overlay);
        Bar.TrackHover();
        Root.Children.Add(VideoArea);
        Grid.SetRow(Bar, 1);
        Root.Children.Add(Bar);
        Content = Root;

        var hwnd = WinRT.Interop.WindowNative.GetWindowHandle(this);
        video = Win32.CreateVideoWindow(hwnd);
        try
        {
            if (!Mpv.Available) throw new InvalidOperationException("The video player (libmpv-2.dll) is missing. Reinstall Wren TV.");
            Mpv = new Mpv(video, a => App.OnUi(a));
            Mpv.OnInput = Input;
        }
        catch (Exception e)
        {
            Log.Write("mpv: " + e);
            Mpv = null;
            StartupError = e.Message;
        }

        VideoArea.SizeChanged += (_, _) => PlaceVideo();
        Root.Loaded += (_, _) => PlaceVideo();
        AppWindow.Changed += (_, a) =>
        {
            if (a.DidPresenterChange) FullscreenChanged();
        };
        Root.PreviewKeyDown += (_, e) =>
        {
            if (FocusManager.GetFocusedElement(Root.XamlRoot) is TextBox) return;
            if (Key(e.Key)) e.Handled = true;
        };
        Root.PointerMoved += (_, _) => ShowBar();
        hideBar.Tick += (_, _) =>
        {
            hideBar.Stop();
            if (IsFullscreen && !Bar.IsPointerOver()) Bar.Visibility = Visibility.Collapsed;
        };
        Activated += (_, a) =>
        {
            if (a.WindowActivationState != WindowActivationState.Deactivated && videoShown) Win32.SetFocus(video);
        };
        Closed += (_, _) => Shutdown();
        foreach (var w in open.ToList()) w.Close();
        open.Add(this);
    }

    /// Why the player couldn't start, if it couldn't.
    protected string? StartupError { get; }

    public static void CloseAll()
    {
        foreach (var w in open.ToList()) w.Close();
    }

    void Shutdown()
    {
        if (closed) return;
        closed = true;
        open.Remove(this);
        hideBar.Stop();
        Stopping();
        if (Mpv is { } m)
        {
            m.Destroy();
            m.WaitForExit(TimeSpan.FromSeconds(2));
        }
        Win32.DestroyWindow(video);
        App.State.Bump();
    }

    /// The window is closing: report progress and stop the stream.
    protected abstract void Stopping();

    /// A key the window didn't otherwise handle.
    protected virtual bool Key(VirtualKey k)
    {
        switch (k)
        {
            case VirtualKey.F or VirtualKey.F11: ToggleFullscreen(); return true;
            case VirtualKey.Escape when IsFullscreen: ToggleFullscreen(); return true;
        }
        return false;
    }

    /// Keys and clicks on the video itself, sent by mpv.
    protected virtual void Input(string action)
    {
        switch (action)
        {
            case "fullscreen": ToggleFullscreen(); break;
            case "escape" when IsFullscreen: ToggleFullscreen(); break;
            case "mouse": ShowBar(); break;
            case "pause": PlayPause(); break;
            case "back": Key(VirtualKey.Left); break;
            case "forward": Key(VirtualKey.Right); break;
            case "volup": Key(VirtualKey.Up); break;
            case "voldown": Key(VirtualKey.Down); break;
            case "mute": Key(VirtualKey.M); break;
            case "skip": Key(VirtualKey.S); break;
            case "next": Key(VirtualKey.N); break;
            case "prev": Key(VirtualKey.P); break;
        }
    }

    protected abstract void PlayPause();

    // Video placement

    protected void ShowVideo(bool show)
    {
        if (videoShown == show) return;
        videoShown = show;
        Overlay.Visibility = show ? Visibility.Collapsed : Visibility.Visible;
        PlaceVideo();
    }

    void PlaceVideo()
    {
        if (closed || VideoArea.XamlRoot is not { } xr) return;
        if (!videoShown || VideoArea.ActualWidth < 2 || VideoArea.ActualHeight < 2)
        {
            Win32.Hide(video);
            return;
        }
        var scale = xr.RasterizationScale;
        var at = VideoArea.TransformToVisual(null).TransformPoint(new Windows.Foundation.Point(0, 0));
        Win32.Place(video, (int)Math.Round(at.X * scale), (int)Math.Round(at.Y * scale),
            (int)Math.Round(VideoArea.ActualWidth * scale), (int)Math.Round(VideoArea.ActualHeight * scale));
    }

    // Fullscreen and the control bar

    protected bool IsFullscreen => AppWindow.Presenter.Kind == AppWindowPresenterKind.FullScreen;

    protected void ToggleFullscreen() =>
        AppWindow.SetPresenter(IsFullscreen ? AppWindowPresenterKind.Overlapped : AppWindowPresenterKind.FullScreen);

    void FullscreenChanged()
    {
        Bar.Visibility = Visibility.Visible;
        if (IsFullscreen) hideBar.Start(); else hideBar.Stop();
        OnFullscreenChanged();
    }

    protected virtual void OnFullscreenChanged() { }

    protected void ShowBar()
    {
        if (Bar.Visibility != Visibility.Visible) Bar.Visibility = Visibility.Visible;
        if (IsFullscreen)
        {
            hideBar.Stop();
            hideBar.Start();
        }
    }

    // Building blocks for the bar

    protected static Button BarButton(string glyph, string tip, Action onClick, double size = 18)
    {
        var b = Ui.IconButton(glyph, tip, onClick, size);
        b.IsTabStop = false;
        return b;
    }

    /// A menu button whose flyout can open over the video.
    protected static DropDownButton MenuButton(string glyph, string tip, MenuFlyout menu)
    {
        menu.ShouldConstrainToRootBounds = false;
        menu.Placement = FlyoutPlacementMode.Top;
        var b = new DropDownButton
        {
            Content = Ui.Icon(glyph, 18),
            Flyout = menu,
            Padding = new Thickness(10, 8, 6, 8),
            Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent),
            BorderThickness = new Thickness(0),
            CornerRadius = new CornerRadius(8),
            IsTabStop = false,
        };
        ToolTipService.SetToolTip(b, tip);
        return b;
    }

    protected static ToggleMenuFlyoutItem Choice(string text, bool selected, Action onClick)
    {
        var m = new ToggleMenuFlyoutItem { Text = text, IsChecked = selected };
        m.Click += (_, _) => onClick();
        return m;
    }

    protected static MenuFlyoutItem Header(string text) => new() { Text = text, IsEnabled = false, FontWeight = FontWeights.SemiBold };

    /// The volume button and slider.
    protected static (StackPanel Panel, Slider Slider, Button Mute) VolumeControl(Action<int> set, Action toggleMute)
    {
        var slider = new Slider { Minimum = 0, Maximum = 100, Width = 110, VerticalAlignment = VerticalAlignment.Center, IsTabStop = false };
        slider.ValueChanged += (_, e) =>
        {
            if (slider.Tag is true) return;
            set((int)e.NewValue);
        };
        var mute = BarButton("", "Mute (M)", toggleMute);
        return (Ui.Row(0, mute, slider), slider, mute);
    }

    protected static void ShowVolume(Slider slider, Button mute, int volume, bool muted)
    {
        slider.Tag = true;
        slider.Value = muted ? 0 : volume;
        slider.Tag = null;
        mute.Content = Ui.Icon(muted || volume == 0 ? "" : volume < 50 ? "" : "", 18);
    }

    /// A message in place of the video: loading, failed, finished.
    protected void ShowStatus(UIElement content)
    {
        Overlay.Children.Clear();
        Overlay.Children.Add(content);
        ShowVideo(false);
    }

    protected static UIElement Status(string title, string? message, bool spinner, params Button[] actions)
    {
        var p = Ui.Column(16);
        if (spinner) p.Children.Add(new ProgressRing { IsActive = true, Width = 44, Height = 44 });
        p.Children.Add(Ui.Text(title, 22, FontWeights.SemiBold));
        if (message != null)
        {
            var m = Ui.Secondary(message, 14, 4);
            m.TextAlignment = TextAlignment.Center;
            m.MaxWidth = 520;
            p.Children.Add(m);
        }
        if (actions.Length > 0)
        {
            var row = Ui.Row(10, actions);
            row.Margin = new Thickness(0, 6, 0, 0);
            p.Children.Add(row);
        }
        foreach (var c in p.Children) ((FrameworkElement)c).HorizontalAlignment = HorizontalAlignment.Center;
        p.HorizontalAlignment = HorizontalAlignment.Center;
        p.VerticalAlignment = VerticalAlignment.Center;
        return p;
    }
}

static class PointerExtensions
{
    /// Whether the mouse is over the element (so the bar doesn't hide under it).
    public static bool IsPointerOver(this FrameworkElement e) => e.Tag is "hover";

    public static void TrackHover(this FrameworkElement e)
    {
        e.PointerEntered += (_, _) => e.Tag = "hover";
        e.PointerExited += (_, _) => e.Tag = null;
    }
}
