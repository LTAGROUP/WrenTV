using System.Runtime.InteropServices;

namespace WrenTV.Player;

/// The few Win32 calls needed to give mpv a window of its own inside ours.
static class Win32
{
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    struct WNDCLASSEX
    {
        public int cbSize;
        public uint style;
        public IntPtr lpfnWndProc;
        public int cbClsExtra;
        public int cbWndExtra;
        public IntPtr hInstance;
        public IntPtr hIcon;
        public IntPtr hCursor;
        public IntPtr hbrBackground;
        public string? lpszMenuName;
        public string lpszClassName;
        public IntPtr hIconSm;
    }

    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    static extern ushort RegisterClassExW(ref WNDCLASSEX c);

    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    static extern IntPtr CreateWindowExW(uint exStyle, string className, string windowName, uint style,
        int x, int y, int width, int height, IntPtr parent, IntPtr menu, IntPtr instance, IntPtr param);

    [DllImport("user32.dll")] public static extern bool DestroyWindow(IntPtr hwnd);
    [DllImport("user32.dll")] static extern bool SetWindowPos(IntPtr hwnd, IntPtr after, int x, int y, int cx, int cy, uint flags);
    [DllImport("user32.dll")] static extern bool ShowWindow(IntPtr hwnd, int cmd);
    [DllImport("user32.dll")] public static extern IntPtr SetFocus(IntPtr hwnd);
    [DllImport("user32.dll")] static extern IntPtr LoadCursorW(IntPtr instance, IntPtr name);
    [DllImport("kernel32.dll", CharSet = CharSet.Unicode)] static extern IntPtr GetModuleHandleW(string? name);
    [DllImport("kernel32.dll", CharSet = CharSet.Ansi)] static extern IntPtr GetProcAddress(IntPtr module, string name);
    [DllImport("gdi32.dll")] static extern IntPtr CreateSolidBrush(uint color);

    const uint WS_CHILD = 0x40000000, WS_VISIBLE = 0x10000000, WS_CLIPSIBLINGS = 0x04000000, WS_CLIPCHILDREN = 0x02000000;
    const uint SWP_NOACTIVATE = 0x0010, SWP_SHOWWINDOW = 0x0040;
    static readonly IntPtr HWND_TOP = IntPtr.Zero;
    const string ClassName = "WrenTVVideo";
    static bool registered;

    /// A black child window for video, drawn above the XAML content.
    public static IntPtr CreateVideoWindow(IntPtr parent)
    {
        var instance = GetModuleHandleW(null);
        if (!registered)
        {
            var c = new WNDCLASSEX
            {
                cbSize = Marshal.SizeOf<WNDCLASSEX>(),
                // The window does nothing itself; mpv draws into it and handles input.
                lpfnWndProc = GetProcAddress(GetModuleHandleW("user32.dll"), "DefWindowProcW"),
                hInstance = instance,
                hCursor = LoadCursorW(IntPtr.Zero, 32512), // IDC_ARROW
                hbrBackground = CreateSolidBrush(0),
                lpszClassName = ClassName,
            };
            RegisterClassExW(ref c);
            registered = true;
        }
        return CreateWindowExW(0, ClassName, "", WS_CHILD | WS_VISIBLE | WS_CLIPSIBLINGS | WS_CLIPCHILDREN,
            0, 0, 1, 1, parent, IntPtr.Zero, instance, IntPtr.Zero);
    }

    public static void Place(IntPtr hwnd, int x, int y, int w, int h) =>
        SetWindowPos(hwnd, HWND_TOP, x, y, Math.Max(1, w), Math.Max(1, h), SWP_NOACTIVATE | SWP_SHOWWINDOW);

    public static void Hide(IntPtr hwnd) => ShowWindow(hwnd, 0);
}
