using System.Reflection;
using System.Text.Json;

namespace WrenTV.Core;

/// Looks for a newer Windows installer on the GitHub releases page.
static class Updates
{
    public const string ReleasesPage = "https://github.com/LTAGROUP/WrenTV/releases/latest";
    const string Asset = "WrenTV-Windows-Setup.exe";

    public static Version Current => Assembly.GetExecutingAssembly().GetName().Version ?? new Version(0, 0);
    public static string CurrentLabel => $"{Current.Major}.{Current.Minor}.{Current.Build}";

    /// The newer version and its installer, once found.
    public static (string Version, Uri Download)? Available { get; private set; }
    public static event Action? Found;

    public static async Task Check()
    {
        try
        {
            using var req = new HttpRequestMessage(HttpMethod.Get, "https://api.github.com/repos/LTAGROUP/WrenTV/releases/latest");
            req.Headers.UserAgent.ParseAdd("WrenTV-Windows/" + CurrentLabel);
            using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(15));
            using var resp = await WrenTVApi.Http.SendAsync(req, cts.Token);
            if (!resp.IsSuccessStatusCode) return;
            var root = JsonDocument.Parse(await resp.Content.ReadAsStringAsync()).RootElement;
            var tag = root.GetProperty("tag_name").GetString()?.TrimStart('v');
            if (tag == null || !Version.TryParse(tag, out var latest)) return;
            if (latest <= new Version(Current.Major, Current.Minor, Math.Max(Current.Build, 0))) return;
            Uri download = new(ReleasesPage);
            foreach (var a in root.GetProperty("assets").EnumerateArray())
                if (a.GetProperty("name").GetString() == Asset)
                    download = new Uri(a.GetProperty("browser_download_url").GetString()!);
            Available = (tag, download);
            Found?.Invoke();
        }
        catch { }
    }
}
