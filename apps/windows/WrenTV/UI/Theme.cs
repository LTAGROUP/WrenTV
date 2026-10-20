using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Media.Imaging;
using Windows.UI;
using Windows.UI.Text;
using WrenTV.Core;

namespace WrenTV.UI;

static class Theme
{
    public static readonly Color Ember = Color.FromArgb(255, 0xE0, 0x4A, 0x2A);
    public static readonly Color EmberLight = Color.FromArgb(255, 0xEA, 0x63, 0x45);
    public static readonly Color EmberDark = Color.FromArgb(255, 0xC2, 0x3D, 0x21);
    public static readonly Color Ink = Color.FromArgb(255, 0x1C, 0x1A, 0x21);
    public static readonly Color Base = Color.FromArgb(255, 0x12, 0x10, 0x16);
    public static readonly Color Surface = Color.FromArgb(255, 0x26, 0x23, 0x2C);
    public static readonly Color Cream = Color.FromArgb(255, 0xF7, 0xF1, 0xE8);
    public static readonly Color Bad = Color.FromArgb(255, 0xFF, 0x73, 0x66);

    public static SolidColorBrush Brush(Color c) => new(c);
    public static SolidColorBrush Brush(Color c, double opacity) => new(c) { Opacity = opacity };
    public static readonly SolidColorBrush EmberBrush = new(Ember);
    public static readonly SolidColorBrush SecondaryText = new(Color.FromArgb(0xA8, 0xFF, 0xFF, 0xFF));
    public static readonly SolidColorBrush TertiaryText = new(Color.FromArgb(0x70, 0xFF, 0xFF, 0xFF));
    public static readonly SolidColorBrush CardBrush = new(Color.FromArgb(0x14, 0xFF, 0xFF, 0xFF));
    public static readonly SolidColorBrush CardBorder = new(Color.FromArgb(0x1A, 0xFF, 0xFF, 0xFF));

    /// The brand's accent in place of the Windows accent colour.
    public static void Apply(ResourceDictionary r)
    {
        var dark = new ResourceDictionary();
        void C(string k, Color c) { dark[k] = c; r[k] = c; }
        void B(string k, Color c) { dark[k] = new SolidColorBrush(c); r[k] = new SolidColorBrush(c); }
        C("SystemAccentColor", Ember);
        C("SystemAccentColorLight1", EmberLight);
        C("SystemAccentColorLight2", EmberLight);
        C("SystemAccentColorLight3", Cream);
        C("SystemAccentColorDark1", EmberDark);
        C("SystemAccentColorDark2", EmberDark);
        C("SystemAccentColorDark3", EmberDark);
        B("AccentFillColorDefaultBrush", Ember);
        B("AccentFillColorSecondaryBrush", EmberLight);
        B("AccentFillColorTertiaryBrush", EmberDark);
        B("AccentTextFillColorPrimaryBrush", EmberLight);
        B("AccentTextFillColorSecondaryBrush", EmberLight);
        B("AccentTextFillColorTertiaryBrush", Ember);
        B("TextOnAccentFillColorPrimaryBrush", Colors.White);
        B("NavigationViewSelectionIndicatorForeground", Ember);
        B("HyperlinkButtonForeground", EmberLight);
        B("WindowBackground", Base);
        r.ThemeDictionaries["Dark"] = dark;
    }

    static class Colors
    {
        public static readonly Color White = Color.FromArgb(255, 255, 255, 255);
    }
}

/// Small builders so pages read like layouts rather than property soup.
static class Ui
{
    public static TextBlock Text(string text, double size = 14, FontWeight? weight = null, Brush? color = null, int maxLines = 0, bool wrap = false) => new()
    {
        Text = text,
        FontSize = size,
        FontWeight = weight ?? FontWeights.Normal,
        Foreground = color ?? new SolidColorBrush(Theme.Cream),
        TextTrimming = TextTrimming.CharacterEllipsis,
        TextWrapping = wrap || maxLines > 1 ? TextWrapping.WrapWholeWords : TextWrapping.NoWrap,
        MaxLines = maxLines,
    };

    public static TextBlock Title(string text, double size = 28) => Text(text, size, FontWeights.Bold);
    public static TextBlock Secondary(string text, double size = 13, int maxLines = 1) => Text(text, size, color: Theme.SecondaryText, maxLines: maxLines);

    public static FontIcon Icon(string glyph, double size = 16, Brush? color = null)
    {
        var i = new FontIcon { Glyph = glyph, FontSize = size };
        if (color != null) i.Foreground = color;
        return i;
    }

    public static StackPanel Row(double spacing, params UIElement[] children)
    {
        var p = new StackPanel { Orientation = Orientation.Horizontal, Spacing = spacing };
        foreach (var c in children) p.Children.Add(c);
        return p;
    }

    public static StackPanel Column(double spacing, params UIElement[] children)
    {
        var p = new StackPanel { Spacing = spacing };
        foreach (var c in children) p.Children.Add(c);
        return p;
    }

    public static object Label(string glyph, string text) =>
        Row(8, Icon(glyph, 14), new TextBlock { Text = text, VerticalAlignment = VerticalAlignment.Center });

    public static Button Button(string text, Action onClick, string? glyph = null, bool accent = false)
    {
        var b = new Button { Content = glyph == null ? text : Label(glyph, text), Padding = new Thickness(16, 8, 16, 8), CornerRadius = new CornerRadius(8) };
        if (accent) b.Style = (Style)Application.Current.Resources["AccentButtonStyle"];
        b.Click += (_, _) => onClick();
        return b;
    }

    public static Button IconButton(string glyph, string tip, Action onClick, double size = 16)
    {
        var b = new Button
        {
            Content = Icon(glyph, size),
            Padding = new Thickness(10),
            Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent),
            BorderThickness = new Thickness(0),
            CornerRadius = new CornerRadius(8),
        };
        ToolTipService.SetToolTip(b, tip);
        b.Click += (_, _) => onClick();
        return b;
    }

    /// A remote image that fills its box, decoded at about the size shown.
    public static Image Image(Uri? url, int decodeWidth = 0, Stretch stretch = Stretch.UniformToFill)
    {
        var img = new Image { Stretch = stretch };
        if (url != null)
        {
            var bmp = new BitmapImage { UriSource = url };
            if (decodeWidth > 0) bmp.DecodePixelWidth = decodeWidth;
            img.Source = bmp;
        }
        return img;
    }

    public static Image Asset(string name, double height)
    {
        var path = Path.Combine(AppContext.BaseDirectory, "Assets", name);
        return new Image { Source = new BitmapImage(new Uri(path)), Height = height, Stretch = Stretch.Uniform, HorizontalAlignment = HorizontalAlignment.Left };
    }

    /// A rounded box clipping its content, with a dark placeholder underneath.
    public static Border Frame(UIElement content, double width, double height, double radius = 10) => new()
    {
        Width = width,
        Height = height,
        CornerRadius = new CornerRadius(radius),
        Background = new SolidColorBrush(Theme.Surface),
        Child = content,
    };

    public static Border Card(UIElement content, double padding = 20) => new()
    {
        Background = Theme.CardBrush,
        BorderBrush = Theme.CardBorder,
        BorderThickness = new Thickness(1),
        CornerRadius = new CornerRadius(12),
        Padding = new Thickness(padding),
        Child = content,
    };

    public static Border Badge(string text) => new()
    {
        BorderBrush = Theme.TertiaryText,
        BorderThickness = new Thickness(1),
        CornerRadius = new CornerRadius(4),
        Padding = new Thickness(6, 1, 6, 2),
        Child = Text(text, 11, FontWeights.SemiBold, Theme.SecondaryText),
    };

    public static ProgressBar Progress(double fraction) => new()
    {
        Minimum = 0,
        Maximum = 1,
        Value = fraction,
        Height = 4,
        MinHeight = 4,
        CornerRadius = new CornerRadius(2),
        Foreground = Theme.EmberBrush,
        Background = new SolidColorBrush(Color.FromArgb(0x50, 0, 0, 0)),
    };

    public static UIElement Spinner(string? message = null)
    {
        var p = Column(14, new ProgressRing { IsActive = true, Width = 40, Height = 40 });
        if (message != null) p.Children.Add(Secondary(message, 14, 3));
        p.HorizontalAlignment = HorizontalAlignment.Center;
        p.VerticalAlignment = VerticalAlignment.Center;
        p.Margin = new Thickness(40);
        foreach (var c in p.Children) ((FrameworkElement)c).HorizontalAlignment = HorizontalAlignment.Center;
        return p;
    }

    public static UIElement Empty(string glyph, string title, string message)
    {
        var p = Column(10, Icon(glyph, 40, Theme.TertiaryText), Text(title, 20, FontWeights.SemiBold), Secondary(message, 14, 4));
        p.HorizontalAlignment = HorizontalAlignment.Center;
        p.VerticalAlignment = VerticalAlignment.Center;
        p.Margin = new Thickness(60);
        p.MaxWidth = 460;
        foreach (var c in p.Children) ((FrameworkElement)c).HorizontalAlignment = HorizontalAlignment.Center;
        ((TextBlock)p.Children[2]).TextAlignment = TextAlignment.Center;
        return p;
    }

    public static InfoBar Error(string message) => new()
    {
        Severity = InfoBarSeverity.Error,
        Message = message,
        IsOpen = true,
        IsClosable = true,
    };

    public static async Task<bool> Confirm(XamlRoot root, string title, string message, string action)
    {
        var d = new ContentDialog
        {
            XamlRoot = root,
            Title = title,
            Content = message,
            PrimaryButtonText = action,
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Close,
        };
        return await d.ShowAsync() == ContentDialogResult.Primary;
    }

    public static void Open(Uri url) => _ = Windows.System.Launcher.LaunchUriAsync(url);
}

static class Format
{
    public static string Duration(double seconds)
    {
        if (!double.IsFinite(seconds) || seconds < 0) seconds = 0;
        var t = TimeSpan.FromSeconds(seconds);
        return t.TotalHours >= 1 ? $"{(int)t.TotalHours}:{t.Minutes:00}:{t.Seconds:00}" : $"{t.Minutes}:{t.Seconds:00}";
    }

    public static string Runtime(int minutes) => minutes >= 60 ? $"{minutes / 60}h {minutes % 60}m" : $"{minutes}m";

    public static string TimeLeft(WatchState p)
    {
        var left = (int)Math.Max(0, (p.Duration - p.Position) / 60);
        return left >= 60 ? $"{left / 60}h {left % 60}m left" : $"{left}m left";
    }

    public static string Size(long bytes)
    {
        string[] u = ["B", "KB", "MB", "GB", "TB"];
        double v = bytes;
        var i = 0;
        while (v >= 1024 && i < u.Length - 1) { v /= 1024; i++; }
        return $"{v:0.#} {u[i]}";
    }

    /// "2019 · 2h 10m · PG-13" style line.
    public static string Meta(Item i)
    {
        var parts = new List<string>();
        if (i.Kind == "episode") parts.Add(i.EpisodeLabel);
        if (i.Year is { } y) parts.Add(y.ToString());
        if (i.RuntimeMin is > 0 and var m) parts.Add(Runtime(m));
        if (!string.IsNullOrEmpty(i.ContentRating)) parts.Add(i.ContentRating!);
        if (i.Kind == "show" && i.ChildCount is > 0 and var n) parts.Add(n == 1 ? "1 season" : $"{n} seasons");
        return string.Join(" · ", parts);
    }

    public static PlayTarget Target(Item i, bool fromStart = false, string? fileId = null) =>
        i.Kind == "episode"
            ? new PlayTarget(i.Id, i.ShowTitle ?? i.Title, $"{i.EpisodeLabel} — {i.Title}", fromStart, fileId)
            : new PlayTarget(i.Id, i.Title, i.Year?.ToString(), fromStart, fileId);
}
