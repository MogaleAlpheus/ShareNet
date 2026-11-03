using System.Buffers.Binary;
using System.Net;
using System.Net.NetworkInformation;
using System.Net.Sockets;

namespace ShareNet;

public sealed record PhysicalAdapter(
    string Name,
    string Description,
    int Index,
    IPAddress Address,
    int PrefixLength,
    IPAddress? Gateway,
    bool IsWireless)
{
    public string DisplayName =>
        $"{Name} — {Address}" + (IsWireless ? " (Wi-Fi)" : string.Empty);
}

public static class NetworkHelper
{
    public static IReadOnlyList<PhysicalAdapter> GetPhysicalAdapters()
    {
        var result = new List<PhysicalAdapter>();

        foreach (var nic in NetworkInterface.GetAllNetworkInterfaces())
        {
            if (nic.OperationalStatus != OperationalStatus.Up)
            {
                continue;
            }

            if (nic.NetworkInterfaceType is NetworkInterfaceType.Loopback or NetworkInterfaceType.Tunnel)
            {
                continue;
            }

            if (nic.Name.Equals("ShareNet", StringComparison.OrdinalIgnoreCase) ||
                nic.Description.Contains("Wintun", StringComparison.OrdinalIgnoreCase) ||
                nic.Description.Contains("TAP-Windows", StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }

            IPInterfaceProperties properties;
            try
            {
                properties = nic.GetIPProperties();
            }
            catch
            {
                continue;
            }

            var unicast = properties.UnicastAddresses.FirstOrDefault(address =>
                address.Address.AddressFamily == AddressFamily.InterNetwork &&
                !IPAddress.IsLoopback(address.Address) &&
                !address.Address.ToString().StartsWith("169.254.", StringComparison.Ordinal));
            if (unicast is null)
            {
                continue;
            }

            var index = 0;
            try
            {
                index = properties.GetIPv4Properties()?.Index ?? 0;
            }
            catch
            {
                // Some virtual adapters do not expose IPv4 properties.
            }

            if (index == 0)
            {
                continue;
            }

            var gateway = properties.GatewayAddresses
                .FirstOrDefault(g =>
                    g.Address.AddressFamily == AddressFamily.InterNetwork &&
                    !g.Address.Equals(IPAddress.Any))
                ?.Address;

            var wireless = nic.NetworkInterfaceType == NetworkInterfaceType.Wireless80211 ||
                           nic.Name.Contains("Wi-Fi", StringComparison.OrdinalIgnoreCase) ||
                           nic.Name.Contains("WLAN", StringComparison.OrdinalIgnoreCase) ||
                           nic.Name.Contains("WiFi", StringComparison.OrdinalIgnoreCase);

            result.Add(new PhysicalAdapter(
                nic.Name,
                nic.Description,
                index,
                unicast.Address,
                unicast.PrefixLength,
                gateway,
                wireless));
        }

        return result;
    }

    public static PhysicalAdapter? FindForRemote(IPAddress remote, string? preferredName = null)
    {
        var adapters = GetPhysicalAdapters();
        if (adapters.Count == 0)
        {
            return null;
        }

        if (!string.IsNullOrWhiteSpace(preferredName))
        {
            var preferred = adapters.FirstOrDefault(a =>
                a.Name.Equals(preferredName, StringComparison.OrdinalIgnoreCase));
            if (preferred is not null)
            {
                return preferred;
            }
        }

        if (remote.AddressFamily == AddressFamily.InterNetwork)
        {
            var onLink = adapters.FirstOrDefault(a => SameSubnet(a.Address, a.PrefixLength, remote));
            if (onLink is not null)
            {
                return onLink;
            }
        }

        return adapters.FirstOrDefault(a => a.IsWireless && a.Gateway is not null)
               ?? adapters.FirstOrDefault(a => a.Gateway is not null)
               ?? adapters[0];
    }

    public static bool SameSubnet(IPAddress address, int prefixLength, IPAddress other)
    {
        var a = address.GetAddressBytes();
        var b = other.GetAddressBytes();
        if (a.Length != 4 || b.Length != 4)
        {
            return false;
        }

        if (prefixLength is < 0 or > 32)
        {
            return false;
        }

        var mask = prefixLength == 0 ? 0u : 0xFFFFFFFFu << (32 - prefixLength);
        var left = BinaryPrimitives.ReadUInt32BigEndian(a) & mask;
        var right = BinaryPrimitives.ReadUInt32BigEndian(b) & mask;
        return left == right;
    }

    public static async Task<bool> WaitForAdapterAsync(
        string name,
        TimeSpan timeout,
        CancellationToken ct)
    {
        var deadline = DateTime.UtcNow + timeout;
        while (DateTime.UtcNow < deadline)
        {
            ct.ThrowIfCancellationRequested();

            try
            {
                // The WinTun adapter can exist before it has an IP address and
                // therefore before Windows reports it as Up. Existence is enough:
                // the route manager assigns the IP and brings it into service.
                var match = NetworkInterface.GetAllNetworkInterfaces().FirstOrDefault(n =>
                    n.Name.Equals(name, StringComparison.OrdinalIgnoreCase));
                if (match is not null)
                {
                    return true;
                }
            }
            catch
            {
                // Keep polling.
            }

            await Task.Delay(250, ct).ConfigureAwait(false);
        }

        return false;
    }
}
