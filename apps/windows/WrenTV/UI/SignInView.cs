using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Windows.System;
using WrenTV.Core;

namespace WrenTV.UI;

/// Everything before the library: the Wren TV account, picking one of its
/// servers, or a server reached by address with its own sign-in.
sealed class SignInView : Grid
{
    static AppState State => App.State;

    enum Mode { Account, CreateAccount, Address }
    Mode mode = Mode.Account;
    // Signing in to an account server with a local username instead.
    bool useUsername;
    readonly Grid card = new();
    bool busy;

    public SignInView()
    {
        Background = new SolidColorBrush(Theme.Base);
        var glow = new Border
        {
            Background = new RadialGradientBrush
            {
                Center = new Windows.Foundation.Point(0.5, 0),
                GradientOrigin = new Windows.Foundation.Point(0.5, 0),
                RadiusX = 0.9,
                RadiusY = 0.7,
                GradientStops =
                {
                    new GradientStop { Color = Windows.UI.Color.FromArgb(0x40, 0xE0, 0x4A, 0x2A), Offset = 0 },
                    new GradientStop { Color = Windows.UI.Color.FromArgb(0, 0xE0, 0x4A, 0x2A), Offset = 1 },
                },
            },
        };
        Children.Add(glow);
        var scroll = new ScrollViewer { Content = card, VerticalScrollBarVisibility = ScrollBarVisibility.Auto };
        card.HorizontalAlignment = HorizontalAlignment.Center;
        card.VerticalAlignment = VerticalAlignment.Center;
        card.Margin = new Thickness(24, 40, 24, 40);
        Children.Add(scroll);
        Render();
    }

    public void Render()
    {
        card.Children.Clear();
        UIElement content;
        if (State.AccessIssue is { } issue) content = AccessIssue(issue);
        else if (State.ServerUrl != null && State.Server is { } info) content = ServerSignIn(info);
        else if (State.Account != null && mode != Mode.Address) content = ServerList();
        else content = mode switch
        {
            Mode.Address => AddressForm(),
            Mode.CreateAccount => AccountForm(create: true),
            _ => AccountForm(create: false),
        };
        var logo = Ui.Asset("lockup.png", 44);
        logo.HorizontalAlignment = HorizontalAlignment.Center;
        var col = Ui.Column(28, logo, Ui.Card(content, 32));
        col.Width = 440;
        card.Children.Add(col);
    }

    static TextBox Field(string header, string placeholder = "") => new() { Header = header, PlaceholderText = placeholder };
    static PasswordBox Password(string header = "Password") => new() { Header = header };

    static StackPanel Heading(string title, string message)
    {
        var p = Ui.Column(6, Ui.Text(title, 22, FontWeights.Bold), Ui.Secondary(message, 14, 4));
        p.Margin = new Thickness(0, 0, 0, 6);
        return p;
    }

    static Button Wide(Button b)
    {
        b.HorizontalAlignment = HorizontalAlignment.Stretch;
        return b;
    }

    static HyperlinkButton Link(string text, Action onClick)
    {
        var l = new HyperlinkButton { Content = text, HorizontalAlignment = HorizontalAlignment.Center };
        l.Click += (_, _) => onClick();
        return l;
    }

    /// Runs a sign-in step with the form disabled and its error shown in place.
    async void Run(StackPanel form, InfoBar err, Func<Task> work)
    {
        if (busy) return;
        busy = true;
        err.IsOpen = false;
        form.IsHitTestVisible = false;
        form.Opacity = 0.6;
        try { await work(); }
        catch (Exception e)
        {
            err.Message = e.Message;
            err.IsOpen = true;
        }
        finally
        {
            busy = false;
            form.IsHitTestVisible = true;
            form.Opacity = 1;
        }
    }

    static void SubmitOnEnter(Control c, Action submit) =>
        c.KeyDown += (_, e) =>
        {
            if (e.Key == VirtualKey.Enter) { submit(); e.Handled = true; }
        };

    // Wren TV account

    UIElement AccountForm(bool create)
    {
        var name = Field("Your name");
        var email = Field("Email", "you@example.com");
        email.InputScope = new InputScope { Names = { new InputScopeName(InputScopeNameValue.EmailSmtpAddress) } };
        var pass = Password();
        var err = new InfoBar { Severity = InfoBarSeverity.Error, IsClosable = false, IsOpen = false };
        var done = new InfoBar { Severity = InfoBarSeverity.Success, IsClosable = false, IsOpen = false };
        var form = Ui.Column(14);
        void Submit() => Run(form, err, async () =>
        {
            if (email.Text.Trim().Length == 0 || pass.Password.Length == 0) throw new WrenTVException("Enter your email and password.");
            var ok = await State.SignInToAccount(email.Text.Trim(), pass.Password, create ? (name.Text.Trim() is { Length: > 0 } n ? n : email.Text.Trim()) : null);
            if (!ok)
            {
                done.Title = "Check your email";
                done.Message = $"We sent a confirmation link to {email.Text.Trim()}. Open it, then sign in here.";
                done.IsOpen = true;
                mode = Mode.Account;
            }
        });
        form.Children.Add(Heading(create ? "Create your account" : "Sign in",
            create ? "One Wren TV account for every server you own or are shared." : "Sign in with your Wren TV account to see your servers."));
        if (create) form.Children.Add(name);
        form.Children.Add(email);
        form.Children.Add(pass);
        form.Children.Add(err);
        form.Children.Add(done);
        form.Children.Add(Wide(Ui.Button(create ? "Create Account" : "Sign In", Submit, accent: true)));
        SubmitOnEnter(pass, Submit);
        form.Children.Add(Link(create ? "Have an account? Sign in" : "New here? Create an account", () =>
        {
            mode = create ? Mode.Account : Mode.CreateAccount;
            Render();
        }));
        form.Children.Add(Link("Connect to a server by address", () => { mode = Mode.Address; Render(); }));
        return form;
    }

    // The account's servers

    UIElement ServerList()
    {
        var form = Ui.Column(12);
        var err = new InfoBar { Severity = InfoBarSeverity.Error, IsClosable = false, IsOpen = false };
        form.Children.Add(Heading("Choose a server", $"Signed in as {State.AccountEmail}."));
        if (State.AccountServers is not { } servers)
        {
            form.Children.Add(State.AccountServersError is { } e
                ? Ui.Error(e)
                : new ProgressRing { IsActive = true, Width = 32, Height = 32, HorizontalAlignment = HorizontalAlignment.Center });
        }
        else if (servers.Count == 0)
        {
            form.Children.Add(Ui.Secondary("No servers yet. Install Wren TV Server and sign in to it with this account, or ask a friend to share theirs with this email address.", 14, 6));
        }
        else
        {
            foreach (var s in servers)
            {
                var seen = s.LastSeenAt is { } at
                    ? DateTimeOffset.UtcNow - at < TimeSpan.FromMinutes(10) ? "Online" : $"Last seen {at.LocalDateTime:g}"
                    : "Not seen yet";
                var row = new Grid { ColumnSpacing = 14 };
                row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
                row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
                row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
                var icon = new Border
                {
                    Width = 40, Height = 40, CornerRadius = new CornerRadius(10),
                    Background = Theme.Brush(Theme.Ember, 0.18),
                    Child = Ui.Icon("", 18, Theme.EmberBrush),
                };
                row.Children.Add(icon);
                var text = Ui.Column(2, Ui.Text(s.Name, 15, FontWeights.SemiBold), Ui.Secondary(seen));
                text.VerticalAlignment = VerticalAlignment.Center;
                Grid.SetColumn(text, 1);
                row.Children.Add(text);
                var chevron = Ui.Icon("", 12, Theme.TertiaryText);
                Grid.SetColumn(chevron, 2);
                row.Children.Add(chevron);
                var b = new Button
                {
                    Content = row,
                    HorizontalAlignment = HorizontalAlignment.Stretch,
                    HorizontalContentAlignment = HorizontalAlignment.Stretch,
                    Padding = new Thickness(12),
                    CornerRadius = new CornerRadius(10),
                };
                b.Click += (_, _) => Run(form, err, () => State.Open(s));
                form.Children.Add(b);
            }
        }
        form.Children.Add(err);
        var actions = Ui.Row(8,
            Ui.Button("Refresh", () => _ = State.LoadAccountServers(), ""),
            Ui.Button("Sign Out", State.SignOut, ""));
        actions.HorizontalAlignment = HorizontalAlignment.Center;
        actions.Margin = new Thickness(0, 8, 0, 0);
        form.Children.Add(actions);
        form.Children.Add(Link("Connect to a server by address", () => { mode = Mode.Address; Render(); }));
        return form;
    }

    // A server by address

    UIElement AddressForm()
    {
        var address = Field("Server address", "192.168.1.20:32500");
        var err = new InfoBar { Severity = InfoBarSeverity.Error, IsClosable = false, IsOpen = false };
        var form = Ui.Column(14);
        void Submit() => Run(form, err, async () =>
        {
            if (address.Text.Trim().Length == 0) throw new WrenTVException("Enter the server's address.");
            await State.Connect(address.Text);
            Render();
        });
        form.Children.Add(Heading("Connect to a server", "Enter the address of a Wren TV server on your network or the internet."));
        form.Children.Add(address);
        form.Children.Add(err);
        form.Children.Add(Wide(Ui.Button("Connect", Submit, accent: true)));
        SubmitOnEnter(address, Submit);
        form.Children.Add(Link(State.Account != null ? "Back to your servers" : "Sign in with a Wren TV account instead", () =>
        {
            mode = Mode.Account;
            Render();
        }));
        return form;
    }

    UIElement ServerSignIn(ServerInfo info)
    {
        if (info.UsesAccounts && !useUsername) return ServerAccountForm(info);
        var setup = info.NeedsOwner;
        var user = Field("Username");
        var pass = Password();
        var confirm = Password("Confirm password");
        var err = new InfoBar { Severity = InfoBarSeverity.Error, IsClosable = false, IsOpen = false };
        var form = Ui.Column(14);
        void Submit() => Run(form, err, async () =>
        {
            if (user.Text.Trim().Length == 0 || pass.Password.Length == 0) throw new WrenTVException("Enter a username and password.");
            if (setup && pass.Password != confirm.Password) throw new WrenTVException("The passwords don't match.");
            await State.Login(user.Text.Trim(), pass.Password, setup);
        });
        form.Children.Add(Heading(setup ? $"Set up {info.Name}" : $"Sign in to {info.Name}",
            setup ? "Create the owner account for this server." : info.UsesAccounts ? "Use the username the server's admin gave you." : "Use your account on this server."));
        form.Children.Add(user);
        form.Children.Add(pass);
        if (setup) form.Children.Add(confirm);
        form.Children.Add(err);
        form.Children.Add(Wide(Ui.Button(setup ? "Create Owner Account" : "Sign In", Submit, accent: true)));
        SubmitOnEnter(setup ? confirm : pass, Submit);
        if (info.UsesAccounts) form.Children.Add(Link("Sign in with a Wren TV account instead", () => { useUsername = false; Render(); }));
        form.Children.Add(Link("Use a different server", State.ForgetUnreachable));
        return form;
    }

    UIElement ServerAccountForm(ServerInfo info)
    {
        var create = mode == Mode.CreateAccount;
        var name = Field("Your name");
        var email = Field("Email", "you@example.com");
        var pass = Password();
        var err = new InfoBar { Severity = InfoBarSeverity.Error, IsClosable = false, IsOpen = false };
        var done = new InfoBar { Severity = InfoBarSeverity.Success, IsClosable = false, IsOpen = false };
        var form = Ui.Column(14);
        void Submit() => Run(form, err, async () =>
        {
            if (email.Text.Trim().Length == 0 || pass.Password.Length == 0) throw new WrenTVException("Enter your email and password.");
            var ok = await State.AccountSignIn(email.Text.Trim(), pass.Password, create ? (name.Text.Trim() is { Length: > 0 } n ? n : email.Text.Trim()) : null);
            if (!ok)
            {
                done.Title = "Check your email";
                done.Message = $"We sent a confirmation link to {email.Text.Trim()}. Open it, then sign in here.";
                done.IsOpen = true;
            }
        });
        form.Children.Add(Heading(create ? "Create your account" : $"Sign in to {info.Name}",
            "This server uses Wren TV accounts. Sign in with the email the owner shared it with."));
        if (create) form.Children.Add(name);
        form.Children.Add(email);
        form.Children.Add(pass);
        form.Children.Add(err);
        form.Children.Add(done);
        form.Children.Add(Wide(Ui.Button(create ? "Create Account" : "Sign In", Submit, accent: true)));
        SubmitOnEnter(pass, Submit);
        form.Children.Add(Link(create ? "Have an account? Sign in" : "New here? Create an account", () =>
        {
            mode = create ? Mode.Account : Mode.CreateAccount;
            Render();
        }));
        if (info.LocalAccounts == true) form.Children.Add(Link("Have a username for this server? Sign in with it", () => { useUsername = true; Render(); }));
        form.Children.Add(Link("Use a different server", State.ForgetUnreachable));
        return form;
    }

    UIElement AccessIssue(string issue)
    {
        var err = new InfoBar { Severity = InfoBarSeverity.Error, IsClosable = false, IsOpen = false };
        var form = Ui.Column(14);
        form.Children.Add(Heading("No access yet", issue));
        form.Children.Add(err);
        form.Children.Add(Wide(Ui.Button("Try Again", () => Run(form, err, State.RetryAccess), accent: true)));
        var row = Ui.Row(8, Ui.Button("Other Servers", State.SwitchServer), Ui.Button("Sign Out", State.SignOut));
        row.HorizontalAlignment = HorizontalAlignment.Center;
        form.Children.Add(row);
        return form;
    }
}
