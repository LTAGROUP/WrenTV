namespace WrenTV.Core;

/// Every address a server publishes, and picking the best one that answers:
/// secure LAN, plain LAN, the owner's public address, the automatic remote
/// address, then the address typed in. All are tried at once.
static class AddressPicker
{
    static readonly TimeSpan PreferWait = TimeSpan.FromMilliseconds(1500);

    public static List<Uri> Candidates(Uri? entered, IEnumerable<string> localUrls, string? publicUrl, string? remoteUrl)
    {
        var lan = localUrls.Select(Parse).OfType<Uri>().ToList();
        var all = lan.Where(u => u.Scheme == "https").Concat(lan.Where(u => u.Scheme != "https")).ToList();
        all.AddRange(new[] { publicUrl, remoteUrl }.Select(Parse).OfType<Uri>());
        if (entered != null) all.Add(entered);
        var seen = new HashSet<string>();
        return all.Where(u => seen.Add(Key(u))).ToList();
    }

    static Uri? Parse(string? s) => s != null && Uri.TryCreate(s, UriKind.Absolute, out var u) ? u : null;

    /// The best candidate that answers as this server, or null when none does.
    public static async Task<Uri?> Best(IReadOnlyList<Uri> candidates, string id)
    {
        if (candidates.Count == 0) return null;
        using var cts = new CancellationTokenSource();
        var results = new bool?[candidates.Count];
        var tasks = candidates.Select((u, i) => Answers(u, id, cts.Token).ContinueWith(t => (i, ok: t.Status == TaskStatus.RanToCompletion && t.Result))).ToList();
        var pending = new List<Task<(int i, bool ok)>>(tasks);
        Task? deadline = null;
        while (pending.Count > 0)
        {
            var waitOn = new List<Task>(pending);
            if (deadline != null) waitOn.Add(deadline);
            var done = await Task.WhenAny(waitOn);
            if (done == deadline) break; // waited long enough for a better answer
            var (i, ok) = await (Task<(int, bool)>)done;
            pending.Remove((Task<(int, bool)>)done);
            results[i] = ok;
            // Best-ranked success whose better-ranked rivals have all finished.
            for (var k = 0; k < results.Length; k++)
            {
                if (results[k] == null) break;
                if (results[k] == true) { cts.Cancel(); return candidates[k]; }
            }
            if (ok && deadline == null) deadline = Task.Delay(PreferWait);
        }
        cts.Cancel();
        var first = Array.FindIndex(results, r => r == true);
        return first >= 0 ? candidates[first] : null;
    }

    static async Task<bool> Answers(Uri baseUrl, string id, CancellationToken ct)
    {
        try
        {
            using var c = CancellationTokenSource.CreateLinkedTokenSource(ct);
            c.CancelAfter(TimeSpan.FromSeconds(5));
            var text = await WrenTVApi.Http.GetStringAsync(new Uri(baseUrl, "/api/server"), c.Token);
            return Json.Parse<ServerInfo>(text).Identity == id;
        }
        catch { return false; }
    }

    public static string Key(Uri u) => u.AbsoluteUri.TrimEnd('/').ToLowerInvariant();
}
