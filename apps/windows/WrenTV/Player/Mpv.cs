using System.Runtime.InteropServices;
using System.Text;

namespace WrenTV.Player;

public sealed record MpvTrack(int Id, string Type, string Title, bool Selected);

/// Owns an mpv handle drawing into a native window. Events are read on a
/// background thread and handed to the UI thread.
public sealed class Mpv
{
    const string Lib = "libmpv-2.dll";

    [DllImport(Lib)] static extern IntPtr mpv_create();
    [DllImport(Lib)] static extern int mpv_initialize(IntPtr ctx);
    [DllImport(Lib)] static extern void mpv_terminate_destroy(IntPtr ctx);
    [DllImport(Lib)] static extern void mpv_wakeup(IntPtr ctx);
    [DllImport(Lib)] static extern int mpv_set_option_string(IntPtr ctx, byte[] name, byte[] data);
    [DllImport(Lib)] static extern int mpv_set_property_string(IntPtr ctx, byte[] name, byte[] data);
    [DllImport(Lib)] static extern IntPtr mpv_get_property_string(IntPtr ctx, byte[] name);
    [DllImport(Lib)] static extern int mpv_command(IntPtr ctx, IntPtr[] args);
    [DllImport(Lib)] static extern int mpv_command_async(IntPtr ctx, ulong replyUserdata, IntPtr[] args);
    [DllImport(Lib)] static extern int mpv_observe_property(IntPtr ctx, ulong replyUserdata, byte[] name, int format);
    [DllImport(Lib)] static extern IntPtr mpv_wait_event(IntPtr ctx, double timeout);
    [DllImport(Lib)] static extern void mpv_free(IntPtr data);
    [DllImport(Lib)] static extern IntPtr mpv_error_string(int error);

    const int EventShutdown = 1, EventEndFile = 7, EventFileLoaded = 8, EventClientMessage = 16, EventPropertyChange = 22;
    const int FormatFlag = 3, FormatInt64 = 4, FormatDouble = 5;
    const int EndReasonError = 4;

    [StructLayout(LayoutKind.Sequential)]
    struct MpvEvent { public int EventId; public int Error; public ulong ReplyUserdata; public IntPtr Data; }

    [StructLayout(LayoutKind.Sequential)]
    struct MpvEventProperty { public IntPtr Name; public int Format; public IntPtr Data; }

    [StructLayout(LayoutKind.Sequential)]
    struct MpvEventEndFile { public int Reason; public int Error; }

    [StructLayout(LayoutKind.Sequential)]
    struct MpvEventClientMessage { public int NumArgs; public IntPtr Args; }

    IntPtr handle;
    readonly object gate = new();
    volatile bool stopping;
    readonly ManualResetEventSlim exited = new();
    readonly Action<Action> post;

    public Action<double, double>? OnTime;
    public Action<bool>? OnPause;
    public Action? OnEnd;
    public Action<List<MpvTrack>>? OnTracks;
    public Action<bool>? OnBuffering;
    public Action<string>? OnError;
    /// Keys and clicks on the video (bound below), e.g. "fullscreen".
    public Action<string>? OnInput;

    double position, duration, publishedPosition = -1;

    /// Whether libmpv-2.dll loads; see LoadError when it doesn't.
    public static bool Available => LoadError is null;

    /// Why libmpv-2.dll didn't load, or null when it did. A file that's there
    /// but built for another processor (x64 on ARM, say) fails here too.
    public static string? LoadError => loadError.Value;

    static readonly Lazy<string?> loadError = new(() =>
    {
        try
        {
            NativeLibrary.Load(Lib, typeof(Mpv).Assembly, null);
            return null;
        }
        catch (Exception e)
        {
            var path = Path.Combine(AppContext.BaseDirectory, Lib);
            var arch = RuntimeInformation.ProcessArchitecture.ToString().ToLowerInvariant();
            return File.Exists(path)
                ? $"The video player (libmpv-2.dll) couldn't load on this {arch} PC: {e.Message} Reinstall the latest Wren TV."
                : "The video player (libmpv-2.dll) is missing. Reinstall Wren TV.";
        }
    });

    /// post runs a callback on the UI thread.
    public Mpv(IntPtr window, Action<Action> post)
    {
        this.post = post;
        handle = mpv_create();
        if (handle == IntPtr.Zero) throw new InvalidOperationException("Couldn't start the video player.");
        var opts = new (string, string)[]
        {
            ("wid", window.ToInt64().ToString()),
            ("vo", "gpu-next"),
            ("gpu-api", "d3d11"),
            ("hwdec", "auto-safe"),
            ("target-colorspace-hint", "yes"),
            // Scale 4K down to the window cleanly instead of with the fast
            // default, which looks soft and grainy on a 1080p or 1440p screen.
            ("scale", "spline36"),
            ("dscale", "mitchell"),
            ("cscale", "spline36"),
            ("correct-downscaling", "yes"),
            ("linear-downscaling", "yes"),
            ("sigmoid-upscaling", "yes"),
            ("deband", "yes"),
            ("keep-open", "yes"),
            ("idle", "yes"),
            ("input-default-bindings", "no"),
            ("input-vo-keyboard", "yes"),
            ("osc", "no"),
            ("osd-level", "0"),
            ("terminal", "no"),
            ("cursor-autohide", "2500"),
            ("cache", "yes"),
            ("demuxer-max-bytes", "400MiB"),
            ("demuxer-readahead-secs", "60"),
            ("user-agent", "WrenTV/0.1"),
            ("sub-font-size", "44"),
            ("sub-border-size", "2.5"),
            ("audio-channels", "auto"),
            ("alang", "eng,en"),
            ("slang", "eng,en"),
        };
        foreach (var (k, v) in opts) mpv_set_option_string(handle, Z(k), Z(v));
        if (mpv_initialize(handle) < 0) throw new InvalidOperationException("Couldn't start the video player.");
        // The video window gets the keyboard and mouse while it has focus.
        foreach (var (key, action) in new[]
                 {
                     ("MBTN_LEFT_DBL", "fullscreen"), ("MBTN_LEFT", "pause"), ("MOUSE_MOVE", "mouse"),
                     ("SPACE", "pause"), ("k", "pause"), ("f", "fullscreen"), ("ESC", "escape"),
                     ("LEFT", "back"), ("RIGHT", "forward"), ("j", "back"), ("l", "forward"),
                     ("UP", "volup"), ("DOWN", "voldown"), ("WHEEL_UP", "volup"), ("WHEEL_DOWN", "voldown"),
                     ("m", "mute"), ("s", "skip"), ("n", "next"), ("PGUP", "prev"), ("PGDWN", "next"),
                 })
            Command("keybind", key, $"script-message wren {action}");
        foreach (var (name, format) in new[]
                 {
                     ("time-pos", FormatDouble), ("duration", FormatDouble), ("pause", FormatFlag),
                     ("eof-reached", FormatFlag), ("paused-for-cache", FormatFlag), ("track-list/count", FormatInt64),
                 })
            mpv_observe_property(handle, 0, Z(name), format);
        new Thread(EventLoop) { IsBackground = true, Name = "mpv events" }.Start();
    }

    static byte[] Z(string s) => Encoding.UTF8.GetBytes(s + "\0");

    public void Load(Uri url, double start)
    {
        if (start > 1) Set("start", "+" + start.ToString(System.Globalization.CultureInfo.InvariantCulture));
        Command("loadfile", url.AbsoluteUri);
    }

    public void Command(params string[] args)
    {
        lock (gate)
        {
            if (handle == IntPtr.Zero) return;
            var ptrs = new IntPtr[args.Length + 1];
            try
            {
                for (var i = 0; i < args.Length; i++) ptrs[i] = Marshal.StringToCoTaskMemUTF8(args[i]);
                mpv_command(handle, ptrs);
            }
            finally
            {
                foreach (var p in ptrs) if (p != IntPtr.Zero) Marshal.FreeCoTaskMem(p);
            }
        }
    }

    public void Set(string name, string value)
    {
        lock (gate)
        {
            if (handle != IntPtr.Zero) mpv_set_property_string(handle, Z(name), Z(value));
        }
    }

    public string? Get(string name)
    {
        lock (gate)
        {
            if (handle == IntPtr.Zero) return null;
            var p = mpv_get_property_string(handle, Z(name));
            if (p == IntPtr.Zero) return null;
            try { return Marshal.PtrToStringUTF8(p); } finally { mpv_free(p); }
        }
    }

    public void TogglePause() => Command("cycle", "pause");
    public void Seek(double t) => Command("seek", t.ToString(System.Globalization.CultureInfo.InvariantCulture), "absolute+keyframes");
    public void SelectTrack(MpvTrack t) => Set(t.Type == "audio" ? "aid" : "sid", t.Id.ToString());
    public void DisableSubtitles() => Set("sid", "no");

    /// Stops playback; the event thread destroys the handle.
    public void Destroy()
    {
        lock (gate)
        {
            if (handle == IntPtr.Zero || stopping) return;
            stopping = true;
            mpv_wakeup(handle);
        }
    }

    void EventLoop()
    {
        while (!stopping)
        {
            var p = mpv_wait_event(handle, -1);
            if (stopping) break;
            var ev = Marshal.PtrToStructure<MpvEvent>(p);
            switch (ev.EventId)
            {
                case EventShutdown:
                    stopping = true;
                    break;
                case EventPropertyChange:
                    HandleProperty(Marshal.PtrToStructure<MpvEventProperty>(ev.Data));
                    break;
                case EventFileLoaded:
                    PublishTracks();
                    break;
                case EventEndFile:
                    var end = Marshal.PtrToStructure<MpvEventEndFile>(ev.Data);
                    if (end.Reason == EndReasonError)
                    {
                        var msg = Marshal.PtrToStringUTF8(mpv_error_string(end.Error)) ?? "Playback failed";
                        post(() => OnError?.Invoke(msg));
                    }
                    break;
                case EventClientMessage:
                    var m = Marshal.PtrToStructure<MpvEventClientMessage>(ev.Data);
                    if (m.NumArgs >= 2)
                    {
                        var first = Marshal.PtrToStringUTF8(Marshal.ReadIntPtr(m.Args, 0));
                        var action = Marshal.PtrToStringUTF8(Marshal.ReadIntPtr(m.Args, IntPtr.Size));
                        if (first == "wren" && action != null) post(() => OnInput?.Invoke(action));
                    }
                    break;
            }
        }
        lock (gate)
        {
            var h = handle;
            handle = IntPtr.Zero;
            if (h != IntPtr.Zero) mpv_terminate_destroy(h);
        }
        exited.Set();
    }

    /// Waits for mpv to let go of the window before the window goes away.
    public void WaitForExit(TimeSpan timeout) => exited.Wait(timeout);

    void HandleProperty(MpvEventProperty p)
    {
        if (p.Data == IntPtr.Zero) return;
        var name = Marshal.PtrToStringUTF8(p.Name);
        switch (name)
        {
            case "time-pos" when p.Format == FormatDouble:
                position = Marshal.PtrToStructure<double>(p.Data);
                if (Math.Abs(position - publishedPosition) < 0.25) return;
                publishedPosition = position;
                var (t, d) = (position, duration);
                post(() => OnTime?.Invoke(t, d));
                break;
            case "duration" when p.Format == FormatDouble:
                duration = Marshal.PtrToStructure<double>(p.Data);
                break;
            case "pause" when p.Format == FormatFlag:
                var paused = Marshal.ReadInt32(p.Data) != 0;
                post(() => OnPause?.Invoke(paused));
                break;
            case "paused-for-cache" when p.Format == FormatFlag:
                var waiting = Marshal.ReadInt32(p.Data) != 0;
                post(() => OnBuffering?.Invoke(waiting));
                break;
            case "eof-reached" when p.Format == FormatFlag:
                if (Marshal.ReadInt32(p.Data) != 0) post(() => OnEnd?.Invoke());
                break;
            case "track-list/count":
                PublishTracks();
                break;
        }
    }

    void PublishTracks()
    {
        var n = int.TryParse(Get("track-list/count"), out var c) ? c : 0;
        var tracks = new List<MpvTrack>();
        for (var i = 0; i < n; i++)
        {
            var p = $"track-list/{i}/";
            var type = Get(p + "type");
            if (type is null or "video" || !int.TryParse(Get(p + "id"), out var id)) continue;
            var parts = new List<string>();
            if (Get(p + "lang") is { Length: > 0 } lang) parts.Add(Core.Languages.Name(lang));
            if (Get(p + "title") is { Length: > 0 } title) parts.Add(title);
            if (Get(p + "codec") is { } codec) parts.Add(codec.ToUpperInvariant());
            if (Get(p + "forced") == "yes") parts.Add("Forced");
            tracks.Add(new MpvTrack(id, type, parts.Count > 0 ? string.Join(" · ", parts) : $"Track {id}", Get(p + "selected") == "yes"));
        }
        post(() => OnTracks?.Invoke(tracks));
    }
}
