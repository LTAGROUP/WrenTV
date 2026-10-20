using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

namespace WrenTV.Core;

/// Settings and sign-ins, kept in %LOCALAPPDATA%\Wren TV. Secrets are
/// encrypted for this Windows user (DPAPI).
static class Store
{
    static readonly string Dir = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Wren TV");
    static readonly string SettingsFile = Path.Combine(Dir, "settings.json");
    static readonly string SecretsFile = Path.Combine(Dir, "secrets.bin");
    static readonly object Gate = new();
    static Dictionary<string, JsonElement>? settings;
    static Dictionary<string, string>? secrets;

    static Dictionary<string, JsonElement> Settings
    {
        get
        {
            if (settings != null) return settings;
            try { settings = JsonSerializer.Deserialize<Dictionary<string, JsonElement>>(File.ReadAllText(SettingsFile)); } catch { }
            return settings ??= [];
        }
    }

    public static string? GetString(string key) =>
        Settings.TryGetValue(key, out var v) && v.ValueKind == JsonValueKind.String ? v.GetString() : null;

    public static T? Get<T>(string key)
    {
        lock (Gate)
        {
            if (!Settings.TryGetValue(key, out var v)) return default;
            try { return v.Deserialize<T>(); } catch { return default; }
        }
    }

    public static void Set<T>(string key, T? value)
    {
        lock (Gate)
        {
            if (value == null) Settings.Remove(key);
            else Settings[key] = JsonSerializer.SerializeToElement(value);
            try
            {
                Directory.CreateDirectory(Dir);
                File.WriteAllText(SettingsFile, JsonSerializer.Serialize(Settings));
            }
            catch { }
        }
    }

    static Dictionary<string, string> Secrets
    {
        get
        {
            if (secrets != null) return secrets;
            try
            {
                var raw = ProtectedData.Unprotect(File.ReadAllBytes(SecretsFile), null, DataProtectionScope.CurrentUser);
                secrets = JsonSerializer.Deserialize<Dictionary<string, string>>(Encoding.UTF8.GetString(raw));
            }
            catch { }
            return secrets ??= [];
        }
    }

    public static string? GetSecret(string key)
    {
        lock (Gate) return Secrets.TryGetValue(key, out var v) ? v : null;
    }

    public static void SetSecret(string key, string? value)
    {
        lock (Gate)
        {
            if (value == null) Secrets.Remove(key); else Secrets[key] = value;
            try
            {
                Directory.CreateDirectory(Dir);
                var data = Encoding.UTF8.GetBytes(JsonSerializer.Serialize(Secrets));
                File.WriteAllBytes(SecretsFile, ProtectedData.Protect(data, null, DataProtectionScope.CurrentUser));
            }
            catch { }
        }
    }

    public static AccountSession? Session(string key)
    {
        var s = GetSecret(key);
        if (s == null) return null;
        try { return JsonSerializer.Deserialize<AccountSession>(s); } catch { return null; }
    }

    public static void SetSession(string key, AccountSession? s) =>
        SetSecret(key, s == null ? null : JsonSerializer.Serialize(s));
}
