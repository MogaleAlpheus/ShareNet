using System.Collections.Concurrent;
using System.Net;
using System.Net.Sockets;
using System.Text;

namespace ShareNet;

public sealed record DiscoveredPhone(string Address, int Port, string Model)
{
    public string Display => $"{Model} ({Address})";
}

/// <summary>
/// Finds the ShareNet phone on the local network so the user does not have to
/// read an address off the phone screen — the part of PdaNet's Wi-Fi flow that
/// still fits ShareNet's architecture.
///
/// Two phases, cheapest first:
///   1. Listen for the phone's presence beacon (UDP 1081). Instant when the
///      phone is sharing over Wi-Fi and a firewall allows the datagram.
///   2. If nothing arrives, probe the adapter's own subnet for hosts with the
///      link port open. Outbound only, so it works without any firewall rule.
/// Either way the address is only a candidate: the connection still has to pass
/// the password handshake.
/// </summary>
public static class LanDiscovery
{
    public const int BeaconPort = 1081;
    public const int LinkPort = TunnelServiceConstants.WifiPort;

    private const string Prefix = "SHARENET1";
    private const int MaxProbeHosts = 512;

    // A home network has at most a couple of hosts with the link port open. More
    // than this means the sweep is not seeing real hosts — a virtual switch or a
    // security product answering on behalf of the subnet — so the result is not
    // trustworthy and no address is suggested.
    private const int MaxCandidates = 4;

    private static readonly string[] VirtualMarkers =
    {
        "hyper-v", "vethernet", "wsl", "docker", "vmware", "virtualbox",
        "xray", "tap-", "wintun", "tunnel", "loopback", "npcap", "bluetooth"
    };

    public static async Task<IReadOnlyList<DiscoveredPhone>> ScanAsync(
        PhysicalAdapter? adapter,
        TimeSpan window,
        CancellationToken ct = default)
    {
        // The beacon is the cheap path but it can be blocked by a firewall, so it
        // only gets a short slice before the sweep takes the rest of the budget.
        var beaconWindow = TimeSpan.FromMilliseconds(Math.Min(1500, window.TotalMilliseconds * 0.4));
        var announced = await ListenAsync(beaconWindow, ct).ConfigureAwait(false);
        if (announced.Count > 0)
        {
            return announced;
        }

        if (adapter is null)
        {
            return Array.Empty<DiscoveredPhone>();
        }

        var probeWindow = TimeSpan.FromMilliseconds(
            Math.Max(600, window.TotalMilliseconds - beaconWindow.TotalMilliseconds));
        return await ProbeSubnetAsync(adapter, probeWindow, ct).ConfigureAwait(false);
    }

    private static async Task<IReadOnlyList<DiscoveredPhone>> ListenAsync(
        TimeSpan window,
        CancellationToken ct)
    {
        var found = new List<DiscoveredPhone>();
        try
        {
            using var udp = new UdpClient();
            udp.Client.SetSocketOption(SocketOptionLevel.Socket, SocketOptionName.ReuseAddress, true);
            udp.Client.Bind(new IPEndPoint(IPAddress.Any, BeaconPort));

            using var deadline = CancellationTokenSource.CreateLinkedTokenSource(ct);
            deadline.CancelAfter(window);

            while (!deadline.IsCancellationRequested)
            {
                UdpReceiveResult result;
                try
                {
                    result = await udp.ReceiveAsync(deadline.Token).ConfigureAwait(false);
                }
                catch (OperationCanceledException)
                {
                    break;
                }
                catch (SocketException)
                {
                    break;
                }

                var phone = Parse(result.Buffer, result.RemoteEndPoint.Address);
                if (phone is null)
                {
                    continue;
                }

                if (found.Any(p => p.Address == phone.Address && p.Port == phone.Port))
                {
                    continue;
                }

                found.Add(phone);
            }
        }
        catch (SocketException)
        {
            // Beacon port unavailable: fall through to the probe.
        }
        catch (ObjectDisposedException)
        {
        }

        return found;
    }

    private static DiscoveredPhone? Parse(byte[] buffer, IPAddress remote)
    {
        if (buffer.Length is 0 or > 512)
        {
            return null;
        }

        var text = Encoding.UTF8.GetString(buffer).Trim('\0', '\r', '\n', ' ');
        var parts = text.Split('|');
        if (parts.Length < 2 || !parts[0].Equals(Prefix, StringComparison.Ordinal))
        {
            return null;
        }

        if (!int.TryParse(parts[1], out var port) || port is < 1 or > 65535)
        {
            port = LinkPort;
        }

        var model = parts.Length > 2 && parts[2].Length > 0 ? parts[2].Trim() : "ShareNet phone";
        return new DiscoveredPhone(remote.ToString(), port, model);
    }

    private static async Task<IReadOnlyList<DiscoveredPhone>> ProbeSubnetAsync(
        PhysicalAdapter adapter,
        TimeSpan window,
        CancellationToken ct)
    {
        if (LooksVirtual(adapter))
        {
            // Sweeping a software switch finds its own members, not the phone.
            return Array.Empty<DiscoveredPhone>();
        }

        var hosts = LocalSubnetHosts(adapter, MaxProbeHosts);
        if (hosts.Count == 0)
        {
            return Array.Empty<DiscoveredPhone>();
        }

        return await ProbeHostsAsync(hosts, window, ct).ConfigureAwait(false);
    }

    /// <summary>Sweep an explicit host list; split out so it can be exercised directly.</summary>
    internal static async Task<IReadOnlyList<DiscoveredPhone>> ProbeHostsAsync(
        IReadOnlyCollection<IPAddress> hosts,
        TimeSpan window,
        CancellationToken ct = default)
    {
        var open = new ConcurrentBag<IPAddress>();
        using var linked = CancellationTokenSource.CreateLinkedTokenSource(ct);
        linked.CancelAfter(window);
        using var gate = new SemaphoreSlim(64);

        var tasks = hosts.Select(async host =>
        {
            try
            {
                await gate.WaitAsync(linked.Token).ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                return;
            }

            try
            {
                using var socket = new Socket(AddressFamily.InterNetwork, SocketType.Stream, ProtocolType.Tcp);
                using var timeout = CancellationTokenSource.CreateLinkedTokenSource(linked.Token);
                timeout.CancelAfter(TimeSpan.FromMilliseconds(300));
                await socket.ConnectAsync(new IPEndPoint(host, LinkPort), timeout.Token)
                    .ConfigureAwait(false);
                open.Add(host);
            }
            catch
            {
                // Closed, filtered or timed out: not a candidate.
            }
            finally
            {
                gate.Release();
            }
        });

        try
        {
            await Task.WhenAll(tasks).ConfigureAwait(false);
        }
        catch (OperationCanceledException)
        {
        }

        if (open.Count is 0 or > MaxCandidates)
        {
            return Array.Empty<DiscoveredPhone>();
        }

        return open
            .OrderBy(a => a.GetAddressBytes()[3])
            .Select(a => new DiscoveredPhone(a.ToString(), LinkPort, "ShareNet phone"))
            .ToList();
    }

    private static bool LooksVirtual(PhysicalAdapter adapter)
    {
        var text = $"{adapter.Name} {adapter.Description}";
        return VirtualMarkers.Any(marker =>
            text.Contains(marker, StringComparison.OrdinalIgnoreCase));
    }

    /// <summary>Usable host addresses around this adapter, capped for big prefixes.</summary>
    private static List<IPAddress> LocalSubnetHosts(PhysicalAdapter adapter, int maxHosts)
    {
        var result = new List<IPAddress>();
        var own = adapter.Address.GetAddressBytes();
        if (own.Length != 4)
        {
            return result;
        }

        // Probe a /24 around the adapter address: wider prefixes are far too big
        // to sweep, and the phone has to be a neighbour anyway.
        var prefix = Math.Max(adapter.PrefixLength, 24);
        var hostBits = 32 - prefix;
        var size = 1 << hostBits;
        var mask = prefix == 0 ? 0u : 0xFFFFFFFFu << hostBits;
        var network = (uint)((own[0] << 24) | (own[1] << 16) | (own[2] << 8) | own[3]) & mask;

        for (var i = 1; i < size - 1 && result.Count < maxHosts; i++)
        {
            var value = network + (uint)i;
            var bytes = new[]
            {
                (byte)(value >> 24), (byte)(value >> 16), (byte)(value >> 8), (byte)value
            };
            if (bytes.SequenceEqual(own))
            {
                continue;
            }

            result.Add(new IPAddress(bytes));
        }

        return result;
    }
}
