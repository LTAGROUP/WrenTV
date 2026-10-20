using Microsoft.UI;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using WrenTV.Core;
using WrenTV.UI;

namespace WrenTV;

public sealed class App : Application
{
    public static AppState State { get; } = new();
    internal static MainWindow? Shell { get; private set; }
    static DispatcherQueue? ui;

    public App()
    {
        RequestedTheme = ApplicationTheme.Dark;
        UnhandledException += (_, e) =>
        {
            Log.Write("unhandled: " + e.Exception);
            e.Handled = true;
        };
    }

    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        ui = DispatcherQueue.GetForCurrentThread();
        Resources.MergedDictionaries.Add(new XamlControlsResources());
        Theme.Apply(Resources);
        Shell = new MainWindow();
        Shell.Activate();
        _ = State.Restore();
        _ = Updates.Check();
    }

    /// Runs on the UI thread (at once when already there).
    public static void OnUi(Action a)
    {
        if (ui == null || ui.HasThreadAccess) a();
        else ui.TryEnqueue(() => a());
    }

    /// Starts async work on the UI thread, logging what it throws.
    public static void OnUi(Func<Task> f) => OnUi(() => { _ = Guard(f); });

    static async Task Guard(Func<Task> f)
    {
        try { await f(); } catch (Exception e) { Log.Write(e.ToString()); }
    }

    [STAThread]
    static void Main()
    {
        WinRT.ComWrappersSupport.InitializeComWrappers();
        Start(_ =>
        {
            var context = new DispatcherQueueSynchronizationContext(DispatcherQueue.GetForCurrentThread());
            SynchronizationContext.SetSynchronizationContext(context);
            new App();
        });
    }
}

static class Log
{
    static readonly string File = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Wren TV", "log.txt");

    public static void Write(string s)
    {
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(File)!);
            var info = new FileInfo(File);
            if (info.Exists && info.Length > 1_000_000) info.Delete();
            System.IO.File.AppendAllText(File, $"{DateTime.Now:s} {s}\n");
        }
        catch { }
    }
}
