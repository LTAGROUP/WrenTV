using Microsoft.UI.Composition.SystemBackdrops;
using Microsoft.UI.Text;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Windows.System;
using WrenTV.Core;

namespace WrenTV.UI;

/// The library window: sign-in until there's a server, then a sidebar like
/// the Mac app's with a page stack on the right. A streaming server shows
/// Movies and TV Shows from TMDB's catalog; otherwise each library is listed.
sealed class MainWindow : Window
{
    static AppState State => App.State;

    readonly Grid root = new() { Background = new SolidColorBrush(Theme.Base) };
    readonly Grid body = new();
    readonly InfoBar update = new() { Severity = InfoBarSeverity.Informational, IsClosable = true, IsOpen = false };
    readonly InfoBar error = new() { Severity = InfoBarSeverity.Error, IsClosable = true, IsOpen = false };
    NavigationView? nav;
    readonly Grid pageHost = new();
    readonly List<Page> stack = [];
    string? section;
    string shape = "";
    CancellationTokenSource? errorTimer;

    public MainWindow()
    {
        Title = "Wren TV";
        SystemBackdrop = new MicaBackdrop { Kind = MicaKind.Base };
        AppWindow.SetIcon(Path.Combine(AppContext.BaseDirectory, "Assets", "WrenTV.ico"));
        var size = Store.Get<int[]>("windowSize") is [var w, var h] && w > 600 && h > 400 ? (w, h) : (1400, 880);
        AppWindow.Resize(new Windows.Graphics.SizeInt32(size.Item1, size.Item2));
        AppWindow.Changed += (_, a) =>
        {
            if (a.DidSizeChange && AppWindow.Presenter is OverlappedPresenter { State: OverlappedPresenterState.Restored })
                Store.Set("windowSize", new[] { AppWindow.Size.Width, AppWindow.Size.Height });
        };
        if (AppWindow.TitleBar is { } tb)
        {
            tb.BackgroundColor = Theme.Base;
            tb.InactiveBackgroundColor = Theme.Base;
            tb.ButtonBackgroundColor = Theme.Base;
            tb.ButtonInactiveBackgroundColor = Theme.Base;
            tb.ForegroundColor = Theme.Cream;
            tb.ButtonForegroundColor = Theme.Cream;
        }

        root.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        root.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        var bars = Ui.Column(0, update, error);
        root.Children.Add(bars);
        Grid.SetRow(body, 1);
        root.Children.Add(body);
        root.RequestedTheme = ElementTheme.Dark;
        Content = root;

        // Back with the mouse's back button or Alt+Left.
        root.PointerPressed += (_, e) =>
        {
            if (e.GetCurrentPoint(root).Properties.IsXButton1Pressed && Back()) e.Handled = true;
        };
        var backKey = new KeyboardAccelerator { Key = VirtualKey.Left, Modifiers = VirtualKeyModifiers.Menu };
        backKey.Invoked += (_, a) => a.Handled = Back();
        root.KeyboardAccelerators.Add(backKey);
        var find = new KeyboardAccelerator { Key = VirtualKey.F, Modifiers = VirtualKeyModifiers.Control };
        find.Invoked += (_, a) => { FocusSearch(); a.Handled = true; };
        root.KeyboardAccelerators.Add(find);

        update.ActionButton = new Button { Content = "Download" };
        ((Button)update.ActionButton).Click += (_, _) =>
        {
            if (Updates.Available is { } u) Ui.Open(u.Download);
        };
        Updates.Found += ShowUpdate;

        State.Changed += Rebuild;
        State.Refreshed += () =>
        {
            for (var i = 0; i < stack.Count; i++)
            {
                if (!stack[i].ReloadsOnRefresh) continue;
                if (i == stack.Count - 1) stack[i].Reload(quiet: true); else stack[i].Stale = true;
            }
        };
        Closed += (_, _) => PlayerWindow.CloseAll();
        Rebuild();
    }

    void ShowUpdate()
    {
        if (Updates.Available is not { } u) return;
        update.Title = $"Wren TV {u.Version} is available";
        update.Message = $"You have {Updates.CurrentLabel}.";
        update.IsOpen = true;
    }

    /// A short-lived error banner.
    public void ShowError(string message)
    {
        error.Message = message;
        error.IsOpen = true;
        errorTimer?.Cancel();
        var cts = errorTimer = new CancellationTokenSource();
        App.OnUi(async () =>
        {
            try { await Task.Delay(8000, cts.Token); } catch { return; }
            error.IsOpen = false;
        });
    }

    /// Rebuilds the window for the sign-in state. The shell survives
    /// changes that only touch the sidebar (libraries, streaming, live TV).
    void Rebuild()
    {
        if (State.Restoring)
        {
            SetShape("restoring", () => Ui.Spinner());
            return;
        }
        if (State.Api == null || State.AccessIssue != null)
        {
            nav = null;
            stack.Clear();
            if (shape == "signin" && body.Children.FirstOrDefault() is SignInView v) v.Render();
            else SetShape("signin", () => new SignInView());
            return;
        }
        if (nav == null || shape != "shell")
        {
            nav = BuildNav();
            stack.Clear();
            section = null;
            SetShape("shell", () => nav, force: true);
            Select("home");
        }
        else
        {
            RefreshMenu();
        }
    }

    void SetShape(string s, Func<UIElement> make, bool force = false)
    {
        if (shape == s && !force) return;
        shape = s;
        body.Children.Clear();
        body.Children.Add(make());
    }

    // Sidebar

    AutoSuggestBox? search;

    NavigationView BuildNav()
    {
        var n = new NavigationView
        {
            PaneDisplayMode = NavigationViewPaneDisplayMode.Left,
            IsSettingsVisible = false,
            IsBackButtonVisible = NavigationViewBackButtonVisible.Visible,
            IsBackEnabled = false,
            OpenPaneLength = 260,
            IsTitleBarAutoPaddingEnabled = false,
            Content = pageHost,
            AlwaysShowHeader = false,
        };
        var brand = Ui.Asset("lockup.png", 30);
        brand.Margin = new Thickness(16, 10, 0, 6);
        n.PaneHeader = brand;
        search = new AutoSuggestBox
        {
            PlaceholderText = "Search",
            QueryIcon = new SymbolIcon(Symbol.Find),
            Margin = new Thickness(0, 0, 0, 4),
        };
        search.QuerySubmitted += (_, a) =>
        {
            var q = (a.QueryText ?? "").Trim();
            if (q.Length == 0) return;
            nav!.SelectedItem = null;
            section = "search";
            stack.Clear();
            Push(new SearchPage(q));
        };
        n.AutoSuggestBox = search;
        n.ItemInvoked += (_, a) =>
        {
            if (a.InvokedItemContainer?.Tag is string tag) Select(tag, force: true);
        };
        n.BackRequested += (_, _) => Back();
        nav = n;
        RefreshMenu();
        return n;
    }

    void FocusSearch() => search?.Focus(FocusState.Keyboard);

    static NavigationViewItem Item(string text, string glyph, string tag) => new()
    {
        Content = text,
        Icon = new FontIcon { Glyph = glyph },
        Tag = tag,
    };

    string menuShape = "";

    void RefreshMenu()
    {
        if (nav == null) return;
        var libs = State.Libraries;
        var key = string.Join("|", libs.Select(l => l.Id + l.Name)) + $"|{State.CanStream}|{State.HasLiveTv}|{State.IsAdmin}";
        if (key == menuShape) return;
        menuShape = key;
        nav.MenuItems.Clear();
        nav.MenuItems.Add(Item("Home", "\uE80F", "home"));
        nav.MenuItems.Add(Item("Watchlist", "\uE728", "watchlist"));
        if (State.HasLiveTv) nav.MenuItems.Add(Item("Live TV", "\uE93E", "live"));
        if (State.CanStream)
        {
            nav.MenuItems.Add(Item("Movies", "\uE8B2", "movies"));
            nav.MenuItems.Add(Item("TV Shows", "\uE7F4", "shows"));
            nav.MenuItems.Add(Item("Collections", "\uE8F1", "collections"));
        }
        else
        {
            nav.MenuItems.Add(Item("Collections", "\uE8F1", "collections"));
            if (libs.Count > 0)
            {
                nav.MenuItems.Add(new NavigationViewItemHeader { Content = "Libraries" });
                foreach (var l in libs) nav.MenuItems.Add(Item(l.Name, l.Glyph, "lib:" + l.Id));
            }
        }
        nav.FooterMenuItems.Clear();
        if (State.IsAdmin) nav.FooterMenuItems.Add(Item("Server", "", "admin"));
        nav.FooterMenuItems.Add(Item("Layouts", "\uE8A9", "layouts"));
        nav.FooterMenuItems.Add(Item("Settings", "", "settings"));
        MarkSelected();
        // The section shown went away (e.g. live TV was turned off).
        if (section is { } s && s != "search" && Find(s) == null) Select("home");
    }

    NavigationViewItem? Find(string tag) =>
        nav?.MenuItems.Concat(nav.FooterMenuItems).OfType<NavigationViewItem>().FirstOrDefault(i => (string)i.Tag == tag);

    void MarkSelected()
    {
        if (nav == null) return;
        nav.SelectedItem = section == null ? null : Find(section);
    }

    /// Opens a sidebar section at its first page.
    public void Select(string tag, bool force = false)
    {
        if (tag == section && !force) return;
        if (tag == section && stack.Count == 1) { stack[0].Reload(quiet: true); return; }
        Page? page = tag switch
        {
            "home" => new RowsPage("home", "Home", () => new HomePage()),
            "watchlist" => new WatchlistPage(),
            "movies" => new RowsPage("movies", "Movies", () => new BrowsePage("movie"), () => new BrowsePage("movie")),
            "shows" => new RowsPage("shows", "TV Shows", () => new BrowsePage("show"), () => new BrowsePage("show")),
            "collections" => new CollectionsPage(),
            "layouts" => new LayoutsPage(),
            "live" => new LiveTvPage(),
            "admin" => new AdminPage(),
            "settings" => new SettingsPage(),
            _ when tag.StartsWith("lib:") && State.Libraries.FirstOrDefault(l => "lib:" + l.Id == tag) is { } lib => new RowsPage("library:" + lib.Id, lib.Name, () => new LibraryPage(lib), () => new LibraryPage(lib)),
            _ => null,
        };
        if (page == null) return;
        section = tag;
        stack.Clear();
        MarkSelected();
        Push(page);
    }

    public void OpenLibrary(Library lib) => Select("lib:" + lib.Id, force: true);

    public Page? TopPage => stack.Count > 0 ? stack[^1] : null;

    // Page stack

    public void Push(Page page)
    {
        stack.Add(page);
        ShowTop();
    }

    public bool Back()
    {
        if (stack.Count <= 1) return false;
        stack.RemoveAt(stack.Count - 1);
        ShowTop();
        return true;
    }

    void ShowTop()
    {
        pageHost.Children.Clear();
        if (stack.Count == 0) return;
        pageHost.Children.Add(stack[^1]);
        if (nav != null) nav.IsBackEnabled = stack.Count > 1;
    }
}
