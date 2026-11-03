using System.IO;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace ShareNet;

public sealed class AppSettings
{
    public string Mode { get; set; } = "usb";

    public string WifiHost { get; set; } = string.Empty;

    public string AdapterName { get; set; } = string.Empty;

    public string LastSerial { get; set; } = string.Empty;

    /// <summary>
    /// The link password, DPAPI-encrypted for the current Windows account. Storing
    /// the readable form would put the phone's sharing password in a plain file.
    /// </summary>
    public string WifiPasswordProtected { get; set; } = string.Empty;

    /// <summary>Readable password; only exists in memory and is never serialized.</summary>
    [JsonIgnore]
    public string WifiPassword { get; set; } = string.Empty;
}

internal static class Secret
{
    public static string Protect(string plain)
    {
        if (string.IsNullOrEmpty(plain))
        {
            return string.Empty;
        }

        try
        {
            var bytes = ProtectedData.Protect(
                Encoding.UTF8.GetBytes(plain), null, DataProtectionScope.CurrentUser);
            return Convert.ToBase64String(bytes);
        }
        catch
        {
            // No DPAPI (unusual): rather than write the password in the clear,
            // forget it and ask again next time.
            return string.Empty;
        }
    }

    public static string Unprotect(string protectedValue)
    {
        if (string.IsNullOrEmpty(protectedValue))
        {
            return string.Empty;
        }

        try
        {
            var bytes = ProtectedData.Unprotect(
                Convert.FromBase64String(protectedValue), null, DataProtectionScope.CurrentUser);
            return Encoding.UTF8.GetString(bytes);
        }
        catch
        {
            // Different Windows account or a damaged value.
            return string.Empty;
        }
    }
}

public static class SettingsStore
{
    private static readonly JsonSerializerOptions Options = new()
    {
        WriteIndented = true
    };

    private static string DirectoryPath =>
        Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData),
            "ShareNet");

    private static string FilePath => Path.Combine(DirectoryPath, "settings.json");

    public static AppSettings Load()
    {
        try
        {
            if (!File.Exists(FilePath))
            {
                return new AppSettings();
            }

            var json = File.ReadAllText(FilePath);
            var settings = JsonSerializer.Deserialize<AppSettings>(json, Options) ?? new AppSettings();
            settings.WifiPassword = Secret.Unprotect(settings.WifiPasswordProtected);
            return settings;
        }
        catch
        {
            return new AppSettings();
        }
    }

    public static void Save(AppSettings settings)
    {
        try
        {
            Directory.CreateDirectory(DirectoryPath);
            settings.WifiPasswordProtected = Secret.Protect(settings.WifiPassword);
            File.WriteAllText(FilePath, JsonSerializer.Serialize(settings, Options));
        }
        catch
        {
            // Settings are a convenience; never fail the connection over them.
        }
    }
}
