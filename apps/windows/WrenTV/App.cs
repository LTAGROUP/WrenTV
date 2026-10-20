using Microsoft.UI;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Markup;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.XamlTypeInfo;
using System.Runtime.InteropServices;
using WrenTV.Core;
using WrenTV.UI;

namespace WrenTV;

/// Code-only WinUI has no generated XAML type info, so the app hands out the
/// control library's own: without it the default control styles can't load
/// and the app quits at launch.
public sealed partial class App : Application, IXamlMetadataProvider
{
    readonly XamlControlsXamlMetaDataProvider controls = new();

    public IXamlType GetXamlType(Type type) => controls.GetXamlType(type);
    public IXamlType GetXamlType(string fullName) => controls.GetXamlType(fullName);
    public XmlnsDefinition[] GetXmlnsDefinitions() => controls.GetXmlnsDefinitions();

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
        try
        {
            ui = DispatcherQueue.GetForCurrentThread();
            Resources.MergedDictionaries.Add(new XamlControlsResources());
            Theme.Apply(Resources);
            Shell = new MainWindow();
            Shell.Activate();
        }
        catch (Exception e)
        {
            Fatal(e);
            return;
        }
        _ = State.Restore();
        _ = Updates.Check();
    }

    /// Logs a startup failure and says so, instead of quitting silently.
    static void Fatal(Exception e)
    {
        Log.Write("startup failed: " + e);
        MessageBoxW(IntPtr.Zero, $"Wren TV couldn't start.\n\n{e.Message}\n\nDetails are in {Log.Path}", "Wren TV", 0x10);
        Environment.Exit(1);
    }

    [DllImport("user32.dll", CharSet = CharSet.Unicode)]
    static extern int MessageBoxW(IntPtr hWnd, string text, string caption, uint type);

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
        AppDomain.CurrentDomain.UnhandledException += (_, e) => Log.Write("fatal: " + e.ExceptionObject);
        try
        {
            WinRT.ComWrappersSupport.InitializeComWrappers();
            Start(_ =>
            {
                var context = new DispatcherQueueSynchronizationContext(DispatcherQueue.GetForCurrentThread());
                SynchronizationContext.SetSynchronizationContext(context);
                new App();
            });
        }
        catch (Exception e)
        {
            Fatal(e);
        }
    }
}

static class Log
{
    public static readonly string Path = System.IO.Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Wren TV", "log.txt");
    static string File => Path;

    public static void Write(string s)
    {
        try
        {
            Directory.CreateDirectory(System.IO.Path.GetDirectoryName(File)!);
            var info = new FileInfo(File);
            if (info.Exists && info.Length > 1_000_000) info.Delete();
            System.IO.File.AppendAllText(File, $"{DateTime.Now:s} {s}\n");
        }
        catch { }
    }
}
