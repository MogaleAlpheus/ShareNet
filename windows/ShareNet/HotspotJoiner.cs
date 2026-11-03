using System.Diagnostics;
using System.Net;
using System.Net.NetworkInformation;
using System.Text;

namespace ShareNet;

public sealed record HotspotJoin(PhysicalAdapter Adapter, IPAddress PhoneAddress);

/// <summary>
/// Joins the phone's own ShareNet access point so Wi-Fi needs no address and no
/// Find press: the PC derives the network name and key from the link password,
/// writes a Windows WLAN profile and connects — PdaNet's model of the PC going
/// to the phone's hotspot rather than the user typing anything.
///
/// Success is decided by the ShareNet handshake on the new link's gateway, not
/// by parsing netsh output, so it works on any Windows display language.
/// </summary>
public static class HotspotJoiner
{
    public const string SsidPrefix = "ShareNet-";

    private static readonly TimeSpan JoinTimeout = TimeSpan.FromSeconds(25);

    public static string SsidFor(string token)
    {
        var normalized = Normalize(token);
        if (normalized.Length < 4)
        {
            throw new InvalidOperationException("Enter the password shown in the ShareNet app on the phone.");
        }

        return SsidPrefix + normalized[..4];
    }

    public static string Normalize(string token) =>
        new string(token.Where(char.IsLetterOrDigit).ToArray()).ToUpperInvariant();

    /// <summary>WLAN profile for a WPA2-PSK network; the same shape Windows exports.</summary>
    public static string BuildProfileXml(string ssid, string key)
    {
        var hex = Convert.ToHexString(Encoding.UTF8.GetBytes(ssid));
        return $"""
<?xml version="1.0"?>
<WLANProfile xmlns="http://www.microsoft.com/networking/WLAN/profile/v1">
    <name>{ssid}</name>
    <SSIDConfig>
        <SSID>
            <hex>{hex}</hex>
            <name>{ssid}</name>
        </SSID>
        <nonBroadcast>false</nonBroadcast>
    </SSIDConfig>
    <connectionType>ESS</connectionType>
    <connectionMode>manual</connectionMode>
    <MSM>
        <security>
            <authEncryption>
                <authentication>WPA2PSK</authentication>
                <encryption>AES</encryption>
                <useOneX>false</useOneX>
            </authEncryption>
            <sharedKey>
                <keyType>passPhrase</keyType>
                <protected>false</protected>
                <keyMaterial>{key}</keyMaterial>
            </sharedKey>
        </security>
    </MSM>
</WLANProfile>
""";
    }

    public static async Task<HotspotJoin?> JoinAsync(
        string token,
        Action<string> log,
        CancellationToken ct)
    {
        var normalized = Normalize(token);
        if (normalized.Length < 8)
        {
            throw new InvalidOperationException("Enter the password shown in the ShareNet app on the phone.");
        }

        var ssid = SsidPrefix + normalized[..4];
        var profilePath = Path.Combine(Path.GetTempPath(), $"sharenet-{Guid.NewGuid():N}.xml");
        var profileAdded = false;

        try
        {
            await File.WriteAllTextAsync(
                    profilePath,
                    BuildProfileXml(ssid, normalized),
                    new UTF8Encoding(false),
                    ct)
                .ConfigureAwait(false);

            log($"Joining the phone's network {ssid}…");
            var add = await RunNetshAsync(
                    new[] { "wlan", "add", "profile", $"filename=\"{profilePath}\"", "user=all" },
                    ct)
                .ConfigureAwait(false);
            if (!add.Ok)
            {
                log($"Windows rejected the Wi-Fi profile: {add.Output.Trim()}");
                return null;
            }

            profileAdded = true;

            var interfaceName = WirelessInterfaceName();
            var connectArgs = new List<string> { "wlan", "connect", $"name={ssid}", $"ssid={ssid}" };
            if (interfaceName is not null)
            {
                connectArgs.Add($"interface=\"{interfaceName}\"");
            }

            var connect = await RunNetshAsync(connectArgs, ct).ConfigureAwait(false);
            if (!connect.Ok)
            {
                log($"Wi-Fi connect: {connect.Output.Trim()}");
            }

            // The proof is the link handshake on the gateway of the network we just
            // joined: the phone answers there only when its hotspot is up.
            var deadline = DateTime.UtcNow + JoinTimeout;
            while (DateTime.UtcNow < deadline)
            {
                ct.ThrowIfCancellationRequested();

                foreach (var adapter in NetworkHelper.GetPhysicalAdapters().Where(a => a.IsWireless))
                {
                    var gateway = adapter.Gateway;
                    if (gateway is null || gateway.Equals(IPAddress.Any))
                    {
                        continue;
                    }

                    if (await AnswersAsync(gateway, normalized, adapter, ct).ConfigureAwait(false))
                    {
                        log($"Joined {ssid}; the phone is {gateway} on {adapter.Name}");
                        return new HotspotJoin(adapter, gateway);
                    }
                }

                await Task.Delay(1000, ct).ConfigureAwait(false);
            }

            log($"The {ssid} network did not answer. Start sharing over Wi-Fi on the phone and try again.");
            return null;
        }
        finally
        {
            try
            {
                File.Delete(profilePath);
            }
            catch
            {
                // The temp profile is disposable.
            }

            if (!profileAdded)
            {
                // Nothing was saved, so nothing to clean up.
            }
        }
    }

    /// <summary>Disconnect and forget the phone's network so Windows goes back to the usual one.</summary>
    public static async Task LeaveAsync(Action<string> log)
    {
        try
        {
            var interfaceName = WirelessInterfaceName();
            var args = new List<string> { "wlan", "disconnect" };
            if (interfaceName is not null)
            {
                args.Add($"interface=\"{interfaceName}\"");
            }

            await RunNetshAsync(args, CancellationToken.None).ConfigureAwait(false);
        }
        catch
        {
            // Best effort: the profile removal below still matters.
        }

        foreach (var profile in await ListProfilesAsync().ConfigureAwait(false))
        {
            if (!profile.StartsWith(SsidPrefix, StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }

            // netsh wants "delete profile name=<profile>"; "profile=<profile>" is
            // rejected with "The following command was not found".
            var result = await RunNetshAsync(
                    new[] { "wlan", "delete", $"profile name={profile}" },
                    CancellationToken.None)
                .ConfigureAwait(false);
            log(result.Ok
                ? $"Left the phone's network ({profile})"
                : $"Could not remove the {profile} profile: {result.Output.Trim()}");
        }
    }

    private static async Task<bool> AnswersAsync(
        IPAddress gateway,
        string token,
        PhysicalAdapter adapter,
        CancellationToken ct)
    {
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(ct);
        timeout.CancelAfter(TimeSpan.FromSeconds(2));
        try
        {
            await PhoneLink.TestAsync(
                    new PhoneLinkOptions(
                        gateway.ToString(),
                        TunnelServiceConstants.WifiPort,
                        token,
                        BindAddress: adapter.Address,
                        InterfaceIndex: adapter.Index,
                        ConnectTimeout: TimeSpan.FromSeconds(2)),
                    timeout.Token)
                .ConfigureAwait(false);
            return true;
        }
        catch (OperationCanceledException)
        {
            return false;
        }
        catch (Exception)
        {
            return false;
        }
    }

    private static string? WirelessInterfaceName() =>
        NetworkInterface.GetAllNetworkInterfaces()
            .FirstOrDefault(n => n.NetworkInterfaceType == NetworkInterfaceType.Wireless80211)
            ?.Name;

    private static async Task<IReadOnlyList<string>> ListProfilesAsync()
    {
        var result = await RunNetshAsync(new[] { "wlan", "show", "profiles" }, CancellationToken.None)
            .ConfigureAwait(false);
        if (!result.Ok)
        {
            return Array.Empty<string>();
        }

        var names = new List<string>();
        foreach (var line in result.Output.Split('\n'))
        {
            var separator = line.IndexOf(':');
            if (separator < 0 || separator == line.Length - 1)
            {
                continue;
            }

            var name = line[(separator + 1)..].Trim();
            if (name.Length > 0)
            {
                names.Add(name);
            }
        }

        return names;
    }

    private static async Task<(bool Ok, string Output)> RunNetshAsync(
        IReadOnlyList<string> args,
        CancellationToken ct)
    {
        var info = new ProcessStartInfo("netsh")
        {
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false,
            CreateNoWindow = true
        };
        foreach (var arg in args)
        {
            info.ArgumentList.Add(arg);
        }

        using var process = Process.Start(info);
        if (process is null)
        {
            return (false, "netsh could not be started");
        }

        var stdout = process.StandardOutput.ReadToEndAsync(ct);
        var stderr = process.StandardError.ReadToEndAsync(ct);
        try
        {
            await process.WaitForExitAsync(ct).ConfigureAwait(false);
        }
        catch (OperationCanceledException)
        {
            try
            {
                process.Kill(entireProcessTree: true);
            }
            catch
            {
                // Already gone.
            }

            throw;
        }

        var output = (await stdout.ConfigureAwait(false)) + (await stderr.ConfigureAwait(false));
        return (process.ExitCode == 0, output);
    }
}
