using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using WrenTV.Core;

namespace WrenTV.UI;

/// The account and server you're on, playback languages, and app updates.
sealed class SettingsPage : Page
{
    public override bool ReloadsOnRefresh => false;

    protected override async Task<UIElement> Build()
    {
        var state = App.State;
        var api = state.Api!;
        UserPrefs? prefs = null;
        try { prefs = await api.Prefs(); } catch { }

        var account = Ui.Column(14, Section("Account"));
        account.Children.Add(Line("Signed in as", state.AccountEmail ?? state.User?.DisplayName ?? state.User?.Username ?? ""));
        account.Children.Add(Line("Server", state.Server?.Name ?? ""));
        account.Children.Add(Line("Address", api.BaseUrl.Host + (api.BaseUrl.IsDefaultPort ? "" : ":" + api.BaseUrl.Port)));
        if (state.Server?.Version is { Length: > 0 } v) account.Children.Add(Line("Server version", v));
        var acts = Ui.Row(10);
        if (state.Server?.UsesAccounts == true) acts.Children.Add(Ui.Button("Switch Server", state.SwitchServer, ""));
        acts.Children.Add(Ui.Button("Sign Out", async () =>
        {
            if (await Ui.Confirm(XamlRoot, "Sign out?", "You'll need to sign in again to watch.", "Sign Out")) state.SignOut();
        }, ""));
        account.Children.Add(acts);

        var playback = Ui.Column(14, Section("Playback"));
        if (prefs != null)
        {
            var audio = LanguageBox("Audio language", prefs.AudioLanguage, allowOff: false);
            var subs = LanguageBox("Subtitles", prefs.SubtitleLanguage ?? "", allowOff: true);
            var forced = new ToggleSwitch { Header = "Forced subtitles", IsOn = prefs.ForcedSubtitles ?? true };
            var foreign = new ToggleSwitch { Header = "Subtitles with foreign audio", IsOn = prefs.ForeignAudioSubtitles ?? true };
            var saved = Ui.Secondary("");
            var watched = NumberBox("Counts as watched at", UserPrefs.WatchedPercents.Select(n => (n, $"{n}%")), prefs.WatchedPercent ?? 90);
            var after = NumberBox("Add to Continue Watching after", UserPrefs.ContinueAfters, prefs.ContinueAfter ?? 30);
            async void Save()
            {
                try
                {
                    var p = new UserPrefs
                    {
                        AudioLanguage = Code(audio), SubtitleLanguage = Code(subs),
                        ForcedSubtitles = forced.IsOn, ForeignAudioSubtitles = foreign.IsOn,
                        WatchedPercent = Number(watched), ContinueAfter = Number(after),
                    };
                    await api.SetPrefs(p);
                    saved.Text = "Saved";
                }
                catch (Exception e) { saved.Text = e.Message; }
            }
            audio.SelectionChanged += (_, _) => Save();
            subs.SelectionChanged += (_, _) => Save();
            forced.Toggled += (_, _) => Save();
            foreign.Toggled += (_, _) => Save();
            watched.SelectionChanged += (_, _) => Save();
            after.SelectionChanged += (_, _) => Save();
            playback.Children.Add(Ui.Row(16, audio, subs));
            playback.Children.Add(Ui.Secondary("The player picks these when a title has them. You can still switch tracks while watching.", 13, 3));
            playback.Children.Add(Ui.Row(32, forced, foreign));
            playback.Children.Add(Ui.Secondary("Forced subtitles cover only foreign-language lines and signs, and show even with subtitles off. With foreign audio, subtitles in your audio language turn on when a title plays in another language.", 13, 3));
            playback.Children.Add(Ui.Row(16, watched, after));
            playback.Children.Add(Ui.Secondary("How far in a movie or episode counts as watched, and how long you watch before it's on Continue Watching. Saved to your account, so it applies on every device.", 13, 3));
            playback.Children.Add(saved);
        }
        else playback.Children.Add(Ui.Secondary("Couldn't load your playback settings."));
        if (!Player.Mpv.Available)
            playback.Children.Add(Ui.Error(Player.Mpv.LoadError!));

        var browsing = Ui.Column(14, Section("Browsing"));
        var sizes = new ComboBox { Header = "Poster size", Width = 220 };
        foreach (var (tag, label) in new[] { ("small", "Small"), ("medium", "Medium"), ("large", "Large") })
            sizes.Items.Add(new ComboBoxItem { Content = label, Tag = tag });
        var size = Store.GetString(Cards.PosterSizeKey) ?? "medium";
        sizes.SelectedItem = sizes.Items.OfType<ComboBoxItem>().FirstOrDefault(i => (string)i.Tag == size);
        sizes.SelectionChanged += (_, _) =>
        {
            var v = Code(sizes) is "" ? "medium" : Code(sizes);
            Store.Set(Cards.PosterSizeKey, v == "medium" ? null : v);
            _ = api.SetPosterSize(v).ContinueWith(_ => { });
        };
        browsing.Children.Add(sizes);
        browsing.Children.Add(Ui.Secondary("How big posters and tiles are in rows and grids. Saved to your account, so your other devices match.", 13, 3));

        var about = Ui.Column(14, Section("Wren TV for Windows"));
        about.Children.Add(Line("Version", Updates.CurrentLabel));
        var status = Ui.Secondary(Updates.Available is { } u ? $"Version {u.Version} is available." : "");
        var check = Ui.Button("Check for Updates", async () =>
        {
            status.Text = "Checking…";
            await Updates.Check();
            status.Text = Updates.Available is { } a ? $"Version {a.Version} is available." : "You're up to date.";
        }, "");
        var row = Ui.Row(10, check, Ui.Button("Release Notes", () => Ui.Open(new Uri(Updates.ReleasesPage)), ""));
        about.Children.Add(row);
        about.Children.Add(status);
        if (Updates.Available is { } avail)
            about.Children.Add(Ui.Button($"Download {avail.Version}", () => Ui.Open(avail.Download), "", accent: true));

        var simkl = Ui.Column(14, Section("Simkl"));
        var trakt = Ui.Column(14, Section("Trakt"));
        var linkTv = LinkTv();
        var col = Ui.Column(20, Ui.Card(account), Ui.Card(playback), Ui.Card(browsing), Ui.Card(simkl), Ui.Card(trakt), Ui.Card(linkTv), Ui.Card(about));
        foreach (var (t, panel, at) in new[] { (Tracker.Simkl, simkl, 3), (Tracker.Trakt, trakt, 4) })
        {
            var card = col.Children[at];
            card.Visibility = Visibility.Collapsed;
            _ = ShowTracker(t, panel, card);
        }
        col.MaxWidth = 720;
        col.HorizontalAlignment = HorizontalAlignment.Left;
        return Scroll("Settings", Padded(col));
    }

    static TextBlock Section(string title) => Ui.Text(title, 18, FontWeights.Bold);

    static UIElement Line(string label, string value)
    {
        var g = new Grid();
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(160) });
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        g.Children.Add(Ui.Secondary(label, 14));
        var v = Ui.Text(value, 14);
        v.IsTextSelectionEnabled = true;
        Grid.SetColumn(v, 1);
        g.Children.Add(v);
        return g;
    }

    /// Linking Simkl or Trakt: what's watched here is sent there, and its history
    /// imported. Hidden when the server has no app for the tracker.
    async Task ShowTracker(Tracker t, StackPanel panel, UIElement card)
    {
        var api = App.State.Api!;
        TrackerStatus? st;
        try { st = await api.TrackerState(t); } catch { st = null; }
        if (st == null || (!st.Available && !st.Linked)) { card.Visibility = Visibility.Collapsed; return; }
        card.Visibility = Visibility.Visible;
        while (panel.Children.Count > 1) panel.Children.RemoveAt(1);
        async void Do(Func<Task> f)
        {
            try { await f(); } catch (Exception e) { panel.Children.Add(Ui.Error(e.Message)); return; }
            await ShowTracker(t, panel, card);
        }
        if (st.Linked && st.Account is { } a)
        {
            panel.Children.Add(Line("Linked to", a.Username ?? t.Name()));
            var scrobble = new ToggleSwitch { Header = $"Send what I watch to {t.Name()}", IsOn = a.Scrobble };
            scrobble.Toggled += (_, _) => Do(() => api.TrackerScrobble(t, scrobble.IsOn));
            panel.Children.Add(scrobble);
            var importing = st.Importing || a.ImportStatus == "running";
            var import = Ui.Button("Import History Again", () => Do(() => api.TrackerImport(t)), "");
            import.IsEnabled = !importing;
            panel.Children.Add(Ui.Row(10, import, Ui.Button("Disconnect", () => Do(() => api.TrackerUnlink(t)), "")));
            var note = importing ? "Importing your history…"
                : a.ImportStatus == "done" ? $"Last import: {a.ImportMovies} movies and {a.ImportEpisodes} episodes marked watched."
                : !string.IsNullOrEmpty(a.ImportStatus) ? $"Import failed: {a.ImportStatus}" : null;
            if (note != null) panel.Children.Add(Ui.Secondary(note, 13, 3));
        }
        else if (st.Pending is { } p && string.IsNullOrEmpty(p.Error))
        {
            var code = Ui.Text(p.UserCode, 26, FontWeights.Bold);
            code.IsTextSelectionEnabled = true;
            panel.Children.Add(Ui.Secondary($"Open {p.Url.Replace("https://", "")} and enter", 14));
            panel.Children.Add(code);
            panel.Children.Add(Ui.Row(10, Ui.Button($"Open {t.Name()}", () => Ui.Open(new Uri(p.UrlComplete ?? p.Url)), "", accent: true),
                Ui.Button("Cancel", () => Do(() => api.TrackerCancelLink(t)), "")));
            panel.Children.Add(Ui.Secondary($"Waiting for you to approve it on {t.Name()}…"));
        }
        else
        {
            panel.Children.Add(Ui.Secondary(st.Pending?.Error ?? $"Link your {t.Name()} account to import what you've already watched and keep {t.Name()} up to date as you watch here.", 13, 3));
            panel.Children.Add(Ui.Button($"Connect {t.Name()}", () => Do(async () =>
            {
                await api.TrackerLink(t);
                if ((await api.TrackerState(t)).Pending is { } np) Ui.Open(new Uri(np.UrlComplete ?? np.Url));
            }), "", accent: true));
        }
        // Waiting on approval or an import: check again shortly, while this page is open.
        if (st.Busy)
        {
            await Task.Delay(3000);
            if (panel.XamlRoot != null) await ShowTracker(t, panel, card);
        }
    }

    /// Signs in a TV showing a code: to this account, or to this server as you.
    StackPanel LinkTv()
    {
        var state = App.State;
        var panel = Ui.Column(14, Section("Sign in a TV"));
        var box = new TextBox { PlaceholderText = "ABCD-EFGH", Width = 220, CharacterCasing = CharacterCasing.Upper, FontFamily = new Microsoft.UI.Xaml.Media.FontFamily("Consolas") };
        var note = Ui.Secondary("On the TV, choose Sign in with your phone, then type the code it shows.", 13, 3);
        Button go = null!;
        go = Ui.Button("Continue", async () =>
        {
            var code = NormalizeTvCode(box.Text);
            if (code.Length != 8) { note.Text = "Codes are 8 letters and numbers."; return; }
            go.IsEnabled = false;
            try
            {
                var api = state.Api!;
                string? token = api.Auth?.AccountSession != null ? await api.Auth.Token() : null;
                var client = new AccountClient(state.Server?.Supabase ?? SupabaseInfo.WrenTV);
                string? device = null;
                var account = false;
                Exception? accountError = null;
                if (token != null)
                {
                    try { device = await client.LinkDevice(code, true, token); account = true; }
                    catch (Exception e) { accountError = e; }
                }
                if (!account)
                {
                    try
                    {
                        var info = await api.PairInfo(code);
                        if (info.Approved) { note.Text = "This code was already used. Get a new one on the TV."; return; }
                        device = info.Device;
                    }
                    catch (Exception e) { note.Text = (accountError ?? e).Message; return; }
                }
                var name = string.IsNullOrEmpty(device) ? "TV" : device;
                var who = state.User?.DisplayName is { Length: > 0 } d ? d : state.User?.Username ?? "you";
                var message = account
                    ? $"{name} will be signed in to your Wren TV account ({state.AccountEmail ?? "this account"}) and open your servers."
                    : $"{name} will be signed in to {state.Server?.Name ?? "this server"} as {who}.";
                if (!await Ui.Confirm(XamlRoot, "Sign in this TV?", message, "Sign In")) return;
                if (account) await client.LinkDevice(code, false, token!);
                else await api.ApprovePair(code);
                note.Text = $"{name} is signed in as {who}.";
                box.Text = "";
            }
            catch (Exception e) { note.Text = e.Message; }
            finally { go.IsEnabled = true; }
        }, "");
        panel.Children.Add(Ui.Row(10, box, go));
        panel.Children.Add(note);
        return panel;
    }

    /// Codes are 8 characters; the TV shows them as ABCD-EFGH. A pasted link's
    /// code is used, never its address.
    static string NormalizeTvCode(string s)
    {
        var i = s.IndexOf("code=", StringComparison.OrdinalIgnoreCase);
        if (i >= 0) s = s[(i + 5)..].Split('&')[0];
        const string alpha = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
        return new string(s.ToUpperInvariant().Where(alpha.Contains).ToArray());
    }

    static int? Number(ComboBox box) => (box.SelectedItem as ComboBoxItem)?.Tag as int?;

    static ComboBox NumberBox(string header, IEnumerable<(int, string)> choices, int selected)
    {
        var box = new ComboBox { Header = header, Width = 220 };
        foreach (var (n, label) in choices) box.Items.Add(new ComboBoxItem { Content = label, Tag = n });
        if (!box.Items.OfType<ComboBoxItem>().Any(i => (int)i.Tag == selected))
            box.Items.Add(new ComboBoxItem { Content = $"{selected} seconds", Tag = selected });
        box.SelectedItem = box.Items.OfType<ComboBoxItem>().First(i => (int)i.Tag == selected);
        return box;
    }

    static string Code(ComboBox box) => (box.SelectedItem as ComboBoxItem)?.Tag as string ?? "";

    static ComboBox LanguageBox(string header, string selected, bool allowOff)
    {
        var box = new ComboBox { Header = header, Width = 220 };
        var options = new List<ComboBoxItem>();
        if (allowOff) options.Add(new ComboBoxItem { Content = "Off", Tag = "" });
        else options.Add(new ComboBoxItem { Content = "Original", Tag = "" });
        foreach (var (code, name) in Languages.Common) options.Add(new ComboBoxItem { Content = name, Tag = code });
        foreach (var o in options) box.Items.Add(o);
        box.SelectedItem = options.FirstOrDefault(o => (string)o.Tag == selected) ?? options[0];
        return box;
    }
}
