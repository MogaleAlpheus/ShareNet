using System.Net;
using System.Net.NetworkInformation;
using System.Net.Sockets;
using System.Text;

namespace ShareNet;

// The real types live in NetworkHelper.cs / TunnelController.cs, which drag in
// the rest of the app. Discovery only needs these shapes.
public sealed record PhysicalAdapter(
    string Name,
    string Description,
    int Index,
    IPAddress Address,
    int PrefixLength,
    IPAddress? Gateway,
    bool IsWireless);

public static class TunnelServiceConstants
{
    public const int WifiPort = 1080;
}

internal static class Program
{
    private static int _failures;

    private static async Task<int> Main()
    {
        await BeaconIsDiscovered();
        await SubnetProbeFindsListener();
        await GarbageIsIgnored();

        Console.WriteLine(_failures == 0 ? "ALL PASS" : $"{_failures} FAILURE(S)");
        return _failures == 0 ? 0 : 1;
    }

    private static void Check(bool ok, string label, string? detail = null)
    {
        Console.WriteLine($"{(ok ? "PASS" : "FAIL")}  {label}{(detail is null ? "" : $"  ({detail})")}");
        if (!ok)
        {
            _failures++;
        }
    }

    private static async Task BeaconIsDiscovered()
    {
        using var sender = new UdpClient();
        sender.EnableBroadcast = true;
        var payload = Encoding.UTF8.GetBytes("SHARENET1|1080|Pixel Test");
        using var stop = new CancellationTokenSource();
        var pump = Task.Run(async () =>
        {
            while (!stop.IsCancellationRequested)
            {
                try
                {
                    await sender.SendAsync(payload, new IPEndPoint(IPAddress.Loopback, LanDiscovery.BeaconPort));
                }
                catch
                {
                    return;
                }

                try
                {
                    await Task.Delay(120, stop.Token);
                }
                catch
                {
                    return;
                }
            }
        });

        var found = await LanDiscovery.ScanAsync(null, TimeSpan.FromSeconds(2));
        stop.Cancel();
        await Task.WhenAny(pump, Task.Delay(500));

        Check(found.Count == 1, "beacon: one phone found", $"{found.Count} result(s)");
        Check(found.Count > 0 && found[0].Address == "127.0.0.1", "beacon: address from the sender");
        Check(found.Count > 0 && found[0].Model == "Pixel Test", "beacon: model parsed",
            found.Count > 0 ? found[0].Model : "n/a");
        Check(found.Count > 0 && found[0].Port == 1080, "beacon: link port parsed",
            found.Count > 0 ? found[0].Port.ToString() : "n/a");
    }

    private static async Task SubnetProbeFindsListener()
    {
        TcpListener listener;
        try
        {
            listener = new TcpListener(IPAddress.Loopback, LanDiscovery.LinkPort);
            listener.Start();
        }
        catch (SocketException ex)
        {
            Console.WriteLine($"SKIP  probe: loopback port {LanDiscovery.LinkPort} unavailable ({ex.SocketErrorCode})");
            return;
        }

        try
        {
            var hosts = new List<IPAddress>
            {
                IPAddress.Parse("127.0.0.2"), IPAddress.Parse("127.0.0.3"), IPAddress.Loopback
            };
            var found = await LanDiscovery.ProbeHostsAsync(hosts, TimeSpan.FromSeconds(3));
            Check(found.Count == 1 && found[0].Address == "127.0.0.1",
                "probe: only the listening host is returned",
                $"{found.Count} candidate(s)");

            // Nothing listening on the other hosts, and a /24 of dead hosts must
            // not produce a pile of candidates.
            var dead = await LanDiscovery.ProbeHostsAsync(
                new[] { IPAddress.Parse("127.0.0.4"), IPAddress.Parse("127.0.0.5") },
                TimeSpan.FromSeconds(2));
            Check(dead.Count == 0, "probe: closed ports are not candidates", $"{dead.Count} result(s)");
        }
        finally
        {
            listener.Stop();
        }
    }

    private static async Task GarbageIsIgnored()
    {
        using var sender = new UdpClient();
        var payload = Encoding.UTF8.GetBytes("HELLO|1080|not a phone");
        using var stop = new CancellationTokenSource();
        var pump = Task.Run(async () =>
        {
            while (!stop.IsCancellationRequested)
            {
                try
                {
                    await sender.SendAsync(payload, new IPEndPoint(IPAddress.Loopback, LanDiscovery.BeaconPort));
                }
                catch
                {
                    return;
                }

                try
                {
                    await Task.Delay(120, stop.Token);
                }
                catch
                {
                    return;
                }
            }
        });

        var found = await LanDiscovery.ScanAsync(null, TimeSpan.FromSeconds(1.5));
        stop.Cancel();
        await Task.WhenAny(pump, Task.Delay(400));

        Check(found.Count == 0, "beacon: foreign datagrams ignored", $"{found.Count} result(s)");
    }

    /// <summary>
    /// Unused by the loopback probe, kept to document which adapters a sweep may
    /// use: virtual switches (Hyper-V, WSL, VPN) answer for every host in their
    /// subnet, so a real LAN adapter with a gateway is required.
    /// </summary>
    private static IPAddress? LocalAddress()
    {
        var fallback = (IPAddress?)null;
        try
        {
            foreach (var nic in NetworkInterface.GetAllNetworkInterfaces())
            {
                if (nic.OperationalStatus != OperationalStatus.Up ||
                    nic.NetworkInterfaceType is NetworkInterfaceType.Loopback or NetworkInterfaceType.Tunnel)
                {
                    continue;
                }

                var text = $"{nic.Name} {nic.Description}";
                if (new[] { "hyper-v", "vethernet", "wsl", "docker", "vmware", "virtualbox", "xray", "tunnel" }
                    .Any(marker => text.Contains(marker, StringComparison.OrdinalIgnoreCase)))
                {
                    continue;
                }

                var properties = nic.GetIPProperties();
                var address = properties.UnicastAddresses
                    .Select(a => a.Address)
                    .FirstOrDefault(a => a.AddressFamily == AddressFamily.InterNetwork &&
                                         !IPAddress.IsLoopback(a) &&
                                         !a.ToString().StartsWith("169.254.", StringComparison.Ordinal));
                if (address is null)
                {
                    continue;
                }

                var hasGateway = properties.GatewayAddresses.Any(g =>
                    g.Address.AddressFamily == AddressFamily.InterNetwork &&
                    !g.Address.Equals(IPAddress.Any));
                if (hasGateway)
                {
                    return address;
                }

                fallback ??= address;
            }
        }
        catch
        {
        }

        return fallback;
    }
}
