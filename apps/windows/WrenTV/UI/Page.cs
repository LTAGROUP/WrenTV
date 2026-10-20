using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace WrenTV.UI;

/// A screen in the main window. Build loads its data and returns the layout;
/// the page shows a spinner meanwhile and an error with Try Again if it fails.
abstract class Page : Grid
{
    public const double Gutter = 36;

    /// Whether to rebuild when the server says something changed.
    public virtual bool ReloadsOnRefresh => true;

    /// Set when a refresh arrived while this page was out of sight.
    public bool Stale { get; set; }

    /// Pages that show the hovered title's art behind them.
    protected virtual bool HasAmbient => false;

    public Ambient? Backdrop { get; }

    bool loaded;
    int generation;

    protected Page()
    {
        if (HasAmbient) Backdrop = new Ambient();
        Loaded += (_, _) =>
        {
            if (!loaded || Stale) Reload();
        };
    }

    protected abstract Task<UIElement> Build();

    /// Rebuilds the page. A quiet reload keeps the old content up until the new one is ready.
    public async void Reload(bool quiet = false)
    {
        loaded = true;
        Stale = false;
        var gen = ++generation;
        if (!quiet || Children.Count == 0) Show(Ui.Spinner());
        try
        {
            var content = await Build();
            if (gen == generation) Show(content);
        }
        catch (Exception e)
        {
            if (gen != generation) return;
            if (quiet && Children.Count > 0) return;
            Show(ErrorView(e.Message, () => Reload()));
        }
    }

    protected void Show(UIElement e)
    {
        Children.Clear();
        if (Backdrop != null) Children.Add(Backdrop);
        Children.Add(e);
    }

    public static UIElement ErrorView(string message, Action retry)
    {
        var p = Ui.Column(14,
            Ui.Icon("", 36, Theme.TertiaryText),
            Ui.Text("Something went wrong", 20, Microsoft.UI.Text.FontWeights.SemiBold),
            Ui.Secondary(message, 14, 4),
            Ui.Button("Try Again", retry, accent: true));
        p.HorizontalAlignment = HorizontalAlignment.Center;
        p.VerticalAlignment = VerticalAlignment.Center;
        p.MaxWidth = 480;
        p.Margin = new Thickness(40);
        foreach (var c in p.Children) ((FrameworkElement)c).HorizontalAlignment = HorizontalAlignment.Center;
        ((TextBlock)p.Children[2]).TextAlignment = TextAlignment.Center;
        return p;
    }

    /// A vertically scrolling page with the standard header.
    protected static ScrollViewer Scroll(params UIElement[] sections) => Scroll(null, sections);

    protected static ScrollViewer Scroll(string? title, params UIElement[] sections)
    {
        var col = new StackPanel { Spacing = 36, Padding = new Thickness(0, 24, 0, 48) };
        if (title != null) col.Children.Add(PageTitle(title));
        foreach (var s in sections) col.Children.Add(s);
        return new ScrollViewer
        {
            Content = col,
            VerticalScrollBarVisibility = ScrollBarVisibility.Auto,
            HorizontalScrollMode = ScrollMode.Disabled,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled,
        };
    }

    public static FrameworkElement PageTitle(string title, string? subtitle = null)
    {
        var t = Ui.Column(4, Ui.Title(title, 30));
        if (subtitle != null) t.Children.Add(Ui.Secondary(subtitle, 14));
        t.Margin = new Thickness(Gutter, 0, Gutter, 0);
        return t;
    }

    /// Content padded to the page gutter.
    public static FrameworkElement Padded(FrameworkElement e)
    {
        e.Margin = new Thickness(Gutter, 0, Gutter, 0);
        return e;
    }
}
