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
            var saved = Ui.Secondary("");
            async void Save()
            {
                try
                {
                    var p = new UserPrefs { AudioLanguage = Code(audio), SubtitleLanguage = Code(subs) };
                    await api.SetPrefs(p);
                    saved.Text = "Saved";
                }
                catch (Exception e) { saved.Text = e.Message; }
            }
            audio.SelectionChanged += (_, _) => Save();
            subs.SelectionChanged += (_, _) => Save();
            playback.Children.Add(Ui.Row(16, audio, subs));
            playback.Children.Add(saved);
            playback.Children.Add(Ui.Secondary("The player picks these when a title has them. You can still switch tracks while watching.", 13, 3));
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
        sizes.SelectionChanged += (_, _) => Store.Set(Cards.PosterSizeKey, Code(sizes) is "medium" or "" ? null : Code(sizes));
        browsing.Children.Add(sizes);
        browsing.Children.Add(Ui.Secondary("How big posters and tiles are in rows and grids, on this PC.", 13, 3));

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

        var col = Ui.Column(20, Ui.Card(account), Ui.Card(playback), Ui.Card(browsing), Ui.Card(about));
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
