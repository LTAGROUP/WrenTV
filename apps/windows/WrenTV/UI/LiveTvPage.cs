using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using WrenTV.Core;

namespace WrenTV.UI;

/// The channel guide: what's on now and next, by group.
sealed class LiveTvPage : Page
{
    string? group;
    public override bool ReloadsOnRefresh => false;

    protected override async Task<UIElement> Build()
    {
        var api = App.State.Api!;
        var channels = await api.LiveChannels();
        if (channels.Count == 0) return Ui.Empty("", "No channels", "Add an M3U playlist in the server's Live TV settings.");
        var groups = channels.Select(c => c.Group).Where(g => !string.IsNullOrEmpty(g)).Distinct().Cast<string>().ToList();
        if (group != null && !groups.Contains(group)) group = null;
        var shown = group == null ? channels : channels.Where(c => c.Group == group).ToList();

        var g = new Grid { RowSpacing = 16, Padding = new Thickness(0, 24, 0, 0) };
        g.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        g.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        var header = new Grid { Margin = new Thickness(Gutter, 0, Gutter, 0) };
        header.Children.Add(Ui.Column(4, Ui.Title("Live TV", 30), Ui.Secondary(shown.Count == 1 ? "1 channel" : $"{shown.Count} channels", 14)));
        if (groups.Count > 1)
        {
            var menu = new MenuFlyout();
            void Add(string? value, string label)
            {
                var m = new ToggleMenuFlyoutItem { Text = label, IsChecked = value == group };
                m.Click += (_, _) => { group = value; Reload(quiet: true); };
                menu.Items.Add(m);
            }
            Add(null, "All channels");
            menu.Items.Add(new MenuFlyoutSeparator());
            foreach (var x in groups) Add(x, x);
            header.Children.Add(new DropDownButton
            {
                Content = Ui.Label("", group ?? "All channels"),
                Flyout = menu,
                HorizontalAlignment = HorizontalAlignment.Right,
                VerticalAlignment = VerticalAlignment.Center,
                CornerRadius = new CornerRadius(8),
            });
        }
        g.Children.Add(header);

        var now = DateTimeOffset.Now;
        var list = Ui.Column(6);
        list.Margin = new Thickness(Gutter, 0, Gutter, 40);
        foreach (var c in shown) list.Children.Add(Row(c, shown, now));
        var scroll = new ScrollViewer { Content = list };
        Grid.SetRow(scroll, 1);
        g.Children.Add(scroll);
        return g;
    }

    static UIElement Row(LiveChannel c, List<LiveChannel> all, DateTimeOffset now)
    {
        var api = App.State.Api!;
        var g = new Grid { ColumnSpacing = 18, Padding = new Thickness(12) };
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(48) });
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(120) });
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(2, GridUnitType.Star) });
        g.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });

        var number = Ui.Text(c.Number ?? "", 15, FontWeights.SemiBold, Theme.SecondaryText);
        number.VerticalAlignment = VerticalAlignment.Center;
        g.Children.Add(number);

        var logo = new Border
        {
            Width = 120, Height = 64, CornerRadius = new CornerRadius(8), Padding = new Thickness(8),
            Background = new SolidColorBrush(Theme.Surface),
            Child = api.LiveLogoUrl(c.Logo) is { } u
                ? Ui.Image(u, 240, Stretch.Uniform)
                : Ui.Text(c.Name, 12, FontWeights.SemiBold, maxLines: 2),
        };
        Grid.SetColumn(logo, 1);
        g.Children.Add(logo);

        var nowCol = Ui.Column(4, Ui.Text(c.Name, 13, FontWeights.SemiBold, Theme.SecondaryText));
        if (c.Now is { } p)
        {
            nowCol.Children.Add(Ui.Text(p.Title, 16, FontWeights.SemiBold));
            var bar = Ui.Progress(p.ProgressAt(now));
            bar.Width = 180;
            bar.HorizontalAlignment = HorizontalAlignment.Left;
            nowCol.Children.Add(Ui.Row(10, bar, Ui.Secondary(p.TimeRange, 12)));
        }
        else nowCol.Children.Add(Ui.Secondary("No guide information"));
        nowCol.VerticalAlignment = VerticalAlignment.Center;
        Grid.SetColumn(nowCol, 2);
        g.Children.Add(nowCol);

        if (c.Next is { } n)
        {
            var next = Ui.Column(2, Ui.Secondary("Next", 12), Ui.Text(n.Title, 14, maxLines: 1), Ui.Secondary(n.StartDate.LocalDateTime.ToShortTimeString(), 12));
            next.VerticalAlignment = VerticalAlignment.Center;
            Grid.SetColumn(next, 3);
            g.Children.Add(next);
        }

        var b = new Button
        {
            Content = g,
            Padding = new Thickness(0),
            HorizontalAlignment = HorizontalAlignment.Stretch,
            HorizontalContentAlignment = HorizontalAlignment.Stretch,
            CornerRadius = new CornerRadius(12),
        };
        if (c.Now?.Description is { Length: > 0 } desc) ToolTipService.SetToolTip(b, desc);
        b.Click += (_, _) => LivePlayerWindow.Open(c, all, api);
        return b;
    }
}
