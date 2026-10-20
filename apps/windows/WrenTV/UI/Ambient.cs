using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Windows.Graphics.Imaging;
using Windows.UI;
using WrenTV.Core;

namespace WrenTV.UI;

/// The hovered title's artwork behind a page, like the Mac app's cinematic
/// backdrop: the art at its own colours in the top right, fading into a
/// background tinted from it so rows and titles stay readable.
sealed class Ambient : Grid
{
    static readonly HttpClient http = new() { Timeout = TimeSpan.FromSeconds(10) };
    static CancellationTokenSource? pending;

    readonly Grid art = new() { HorizontalAlignment = HorizontalAlignment.Right, VerticalAlignment = VerticalAlignment.Top };
    readonly Border fadeLeft = new(), fadeBottom = new();
    string? shown;
    Color tint = Theme.Base;

    public Ambient()
    {
        IsHitTestVisible = false;
        Background = new SolidColorBrush(Theme.Base);
        BackgroundTransition = new BrushTransition { Duration = TimeSpan.FromMilliseconds(700) };
        art.Children.Add(new Grid());
        Children.Add(art);
        Children.Add(fadeLeft);
        Children.Add(fadeBottom);
        SizeChanged += (_, _) => Layout();
        Paint();
    }

    /// Called as the pointer moves over a card: the page shows that title's
    /// art once the pointer settles, so skimming a row doesn't flash every poster.
    public static void Hover(string? backdrop)
    {
        if (string.IsNullOrEmpty(backdrop)) return;
        pending?.Cancel();
        var cts = pending = new CancellationTokenSource();
        App.OnUi(async () =>
        {
            try { await Task.Delay(260, cts.Token); } catch { return; }
            App.Shell?.TopPage?.Backdrop?.Show(backdrop);
        });
    }

    public async void Show(string path)
    {
        if (path == shown || App.State.Api?.ImageUrl(path, "w1280") is not { } url) return;
        shown = path;
        var image = Ui.Image(url, 1280);
        image.Opacity = 0;
        image.OpacityTransition = new ScalarTransition { Duration = TimeSpan.FromMilliseconds(650) };
        image.ImageOpened += (_, _) => image.Opacity = 1;
        foreach (var old in art.Children.OfType<Image>().ToList())
        {
            old.OpacityTransition ??= new ScalarTransition { Duration = TimeSpan.FromMilliseconds(650) };
            old.Opacity = 0;
            _ = RemoveLater(old);
        }
        art.Children.Add(image);
        if (await DominantColor(url) is { } c && shown == path)
        {
            tint = Mix(Theme.Base, c, 0.42);
            Paint();
        }
    }

    async Task RemoveLater(UIElement e)
    {
        await Task.Delay(800);
        art.Children.Remove(e);
    }

    void Layout()
    {
        art.Width = ActualWidth * 0.74;
        art.Height = ActualHeight * 0.8;
        fadeLeft.Width = art.Width;
        fadeLeft.Height = art.Height;
        fadeLeft.HorizontalAlignment = HorizontalAlignment.Right;
        fadeLeft.VerticalAlignment = VerticalAlignment.Top;
        fadeBottom.Width = art.Width;
        fadeBottom.Height = art.Height;
        fadeBottom.HorizontalAlignment = HorizontalAlignment.Right;
        fadeBottom.VerticalAlignment = VerticalAlignment.Top;
    }

    /// The tint behind everything, and the art's left and bottom edges fading into it.
    void Paint()
    {
        Background = new SolidColorBrush(tint);
        var clear = Color.FromArgb(0, tint.R, tint.G, tint.B);
        fadeLeft.Background = Gradient(new(0, 0.5), new(1, 0.5), (tint, 0), (clear, 0.45));
        fadeBottom.Background = Gradient(new(0.5, 0), new(0.5, 1), (clear, 0.5), (tint, 1));
    }

    static LinearGradientBrush Gradient(Windows.Foundation.Point from, Windows.Foundation.Point to, params (Color c, double at)[] stops)
    {
        var b = new LinearGradientBrush { StartPoint = from, EndPoint = to };
        foreach (var (c, at) in stops) b.GradientStops.Add(new GradientStop { Color = c, Offset = at });
        return b;
    }

    static Color Mix(Color a, Color b, double t) => Color.FromArgb(255,
        (byte)(a.R + (b.R - a.R) * t), (byte)(a.G + (b.G - a.G) * t), (byte)(a.B + (b.B - a.B) * t));

    /// The art's main colour, favouring saturated pixels, kept dark enough for white text.
    static async Task<Color?> DominantColor(Uri url)
    {
        try
        {
            var bytes = await http.GetByteArrayAsync(url);
            using var stream = new MemoryStream(bytes).AsRandomAccessStream();
            var decoder = await BitmapDecoder.CreateAsync(stream);
            var data = await decoder.GetPixelDataAsync(BitmapPixelFormat.Rgba8, BitmapAlphaMode.Ignore,
                new BitmapTransform { ScaledWidth = 32, ScaledHeight = 18 }, ExifOrientationMode.IgnoreExifOrientation, ColorManagementMode.DoNotColorManage);
            var px = data.DetachPixelData();
            double r = 0, g = 0, b = 0, sum = 0;
            for (var i = 0; i + 3 < px.Length; i += 4)
            {
                double pr = px[i] / 255.0, pg = px[i + 1] / 255.0, pb = px[i + 2] / 255.0;
                double mx = Math.Max(pr, Math.Max(pg, pb)), mn = Math.Min(pr, Math.Min(pg, pb));
                var sat = mx == 0 ? 0 : (mx - mn) / mx;
                var k = sat * mx + 0.04;
                r += pr * k; g += pg * k; b += pb * k; sum += k;
            }
            if (sum <= 0) return null;
            r /= sum; g /= sum; b /= sum;
            // Cap the brightness (HSV value) at 0.62, keeping hue and saturation.
            var v = Math.Max(r, Math.Max(g, b));
            var scale = v > 0.62 ? 0.62 / v : 1;
            return Color.FromArgb(255, (byte)(r * scale * 255), (byte)(g * scale * 255), (byte)(b * scale * 255));
        }
        catch
        {
            return null;
        }
    }
}
