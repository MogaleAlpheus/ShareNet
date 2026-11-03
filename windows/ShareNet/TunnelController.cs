using System.Net;
using System.Net.Sockets;

namespace ShareNet;

public enum TunnelState
{
    Stopped,
    Connecting,
    Connected,
    Disconnecting,
    Error
}

/// <summary>Coordinates the phone link, the internal SOCKS bridge, tun2socks, and routes.</summary>
public sealed class TunnelController : IAsyncDisposable
{
    private readonly Logger _logger = new();
    private readonly SemaphoreSlim _gate = new(1, 1);
    private readonly RouteManager _routes;

    private Socks5Server? _socks;
    private Tun2SocksRunner? _tun;
    private AdbManager? _adb;
    private string? _adbSerial;
    private int _adbLocalPort;
    private bool _forwardActive;
    private IPAddress? _phoneRouteAddress;
    private bool _joinedHotspot;
    private int _phoneRouteInterface;
    private CancellationTokenSource? _sessionCts;
    private Task? _monitorTask;
    private Task? _watchdogTask;
    private PhoneLinkOptions? _linkOptions;
    private bool _staleCleaned;
    private bool _disposed;

    public TunnelController()
    {
        _routes = new RouteManager(Log);
        _logger.LineWritten += line => LogLine?.Invoke(line);
    }

    public TunnelState State { get; private set; } = TunnelState.Stopped;

    public string Status { get; private set; } = "Ready";

    public string? LastError { get; private set; }

    public bool IsBusy => State is TunnelState.Connecting or TunnelState.Disconnecting;

    public bool IsConnected => State == TunnelState.Connected;

    public event Action? Changed;

    public event Action<string>? LogLine;

    /// <summary>Live byte counters for the current tunnel session.</summary>
    public TrafficStats Traffic { get; } = new();

    public string LogSnapshot => _logger.Snapshot();

    public async Task CleanupStaleAsync()
    {
        await _gate.WaitAsync().ConfigureAwait(false);
        try
        {
            if (_staleCleaned || State != TunnelState.Stopped)
            {
                return;
            }
            _staleCleaned = true;

            Log("Checking for leftovers from a previous run…");

            try
            {
                new Tun2SocksRunner().KillStaleProcesses();
            }
            catch (Exception ex)
            {
                Log($"Stale engine check warning: {ex.Message}");
            }

            await _routes.CleanupAsync(CancellationToken.None).ConfigureAwait(false);

            var adbPath = AdbManager.FindAdb();
            if (adbPath is not null && SecurityHelper.IsAdministrator())
            {
                try
                {
                    var adb = new AdbManager(adbPath);
                    await adb.EnsureServerAsync(CancellationToken.None).ConfigureAwait(false);
                    await adb.RemoveShareNetForwardsAsync(CancellationToken.None).ConfigureAwait(false);
                    Log("Removed stale USB forwards");
                }
                catch (Exception ex)
                {
                    Log($"USB cleanup warning: {ex.Message}");
                }
            }
        }
        finally
        {
            _gate.Release();
        }
    }

    public async Task<IReadOnlyList<AdbDevice>> GetUsbDevicesAsync(CancellationToken ct)
    {
        var adbPath = AdbManager.FindAdb()
            ?? throw new InvalidOperationException(
                "adb.exe was not found. Reinstall ShareNet or connect through Wi-Fi instead.");
        var adb = new AdbManager(adbPath);
        await adb.EnsureServerAsync(ct).ConfigureAwait(false);
        return await adb.GetDevicesAsync(ct).ConfigureAwait(false);
    }

    public void AddLog(string message) => Log(message);

    public async Task ConnectUsbAsync(string? serial, CancellationToken ct = default)
    {
        await _gate.WaitAsync(ct).ConfigureAwait(false);
        try
        {
            EnsureCanConnect();
            await ConnectUsbCoreAsync(serial, ct).ConfigureAwait(false);
        }
        catch (Exception ex)
        {
            await FailAsync(ex).ConfigureAwait(false);
        }
        finally
        {
            _gate.Release();
        }
    }

    /// <summary>
    /// Wi-Fi without an address: look for the phone on the current network and, if
    /// it is not there, join the access point the phone opened and take its address
    /// from that network's gateway. This is PdaNet's model — the PC goes to the
    /// phone, so no address and no Find press are needed.
    /// </summary>
    public async Task ConnectWifiAutoAsync(
        string token,
        string? adapterName,
        CancellationToken ct = default)
    {
        await _gate.WaitAsync(ct).ConfigureAwait(false);
        try
        {
            EnsureCanConnect();

            var adapters = NetworkHelper.GetPhysicalAdapters();
            PhysicalAdapter? adapter = string.IsNullOrWhiteSpace(adapterName)
                ? null
                : adapters.FirstOrDefault(a =>
                    a.Name.Equals(adapterName, StringComparison.OrdinalIgnoreCase));
            adapter ??= adapters.FirstOrDefault(a => a.IsWireless && a.Gateway is not null)
                        ?? adapters.FirstOrDefault(a => a.Gateway is not null);

            SetState(TunnelState.Connecting, "Looking for the phone…");
            var found = await LanDiscovery
                .ScanAsync(adapter, TimeSpan.FromSeconds(4), ct)
                .ConfigureAwait(false);
            if (found.Count > 0)
            {
                Log($"Found the phone at {found[0].Address}");
                await ConnectWifiCoreAsync(found[0].Address, token, adapter?.Name, ct)
                    .ConfigureAwait(false);
                return;
            }

            SetState(TunnelState.Connecting, "Joining the phone's network…");
            var joined = await HotspotJoiner.JoinAsync(token, Log, ct).ConfigureAwait(false);
            if (joined is null)
            {
                throw new InvalidOperationException(
                    "The phone was not found on this network, and its ShareNet hotspot could not be joined. " +
                    "Start sharing over Wi-Fi in the phone app (Android 11 or newer opens the hotspot), " +
                    "or put both devices on the same Wi-Fi network and press Find.");
            }

            _joinedHotspot = true;
            await ConnectWifiCoreAsync(joined.PhoneAddress.ToString(), token, joined.Adapter.Name, ct)
                .ConfigureAwait(false);
        }
        catch (Exception ex)
        {
            await ReleaseHotspotAsync().ConfigureAwait(false);
            await FailAsync(ex).ConfigureAwait(false);
        }
        finally
        {
            _gate.Release();
        }
    }

    /// <summary>Restores the PC's own Wi-Fi after a session on the phone's hotspot.</summary>
    private async Task ReleaseHotspotAsync()
    {
        if (!_joinedHotspot)
        {
            return;
        }

        _joinedHotspot = false;
        await HotspotJoiner.LeaveAsync(Log).ConfigureAwait(false);
    }

    public async Task ConnectWifiAsync(
        string host,
        string token,
        string? adapterName,
        CancellationToken ct = default)
    {
        await _gate.WaitAsync(ct).ConfigureAwait(false);
        try
        {
            EnsureCanConnect();
            await ConnectWifiCoreAsync(host, token, adapterName, ct).ConfigureAwait(false);
        }
        catch (Exception ex)
        {
            await FailAsync(ex).ConfigureAwait(false);
        }
        finally
        {
            _gate.Release();
        }
    }

    public async Task DisconnectAsync(CancellationToken ct = default)
    {
        await _gate.WaitAsync(ct).ConfigureAwait(false);
        try
        {
            if (State == TunnelState.Stopped && _tun is null && _socks is null)
            {
                SetState(TunnelState.Stopped, "Ready");
                return;
            }

            SetState(TunnelState.Disconnecting, "Disconnecting…");
            await CleanupCoreAsync(ct).ConfigureAwait(false);
            await ReleaseHotspotAsync().ConfigureAwait(false);
            SetState(TunnelState.Stopped, "Ready");
            Log("Disconnected");
        }
        catch (Exception ex)
        {
            Log($"Disconnect warning: {ex.Message}");
            SetState(TunnelState.Error, "Not connected", ex.Message);
        }
        finally
        {
            _gate.Release();
        }
    }

    private void EnsureCanConnect()
    {
        if (_disposed)
        {
            throw new ObjectDisposedException(nameof(TunnelController));
        }
        if (State is TunnelState.Connecting or TunnelState.Connected or TunnelState.Disconnecting)
        {
            throw new InvalidOperationException("ShareNet is already busy");
        }
        if (!SecurityHelper.IsAdministrator())
        {
            throw new InvalidOperationException(
                "ShareNet must run as administrator to create the virtual network adapter. " +
                "Close it and start it again with the elevated shortcut.");
        }
    }

    private async Task ConnectUsbCoreAsync(string? serial, CancellationToken ct)
    {
        SetState(TunnelState.Connecting, "Preparing the tunnel…");
        await _routes.CleanupAsync(ct).ConfigureAwait(false);

        var adbPath = AdbManager.FindAdb()
            ?? throw new InvalidOperationException(
                "adb.exe was not found. Reinstall ShareNet or connect through Wi-Fi instead.");

        _adb = new AdbManager(adbPath);
        Log($"Using adb: {adbPath}");
        foreach (var foreign in AdbManager.FindForeignAdbProcesses(adbPath))
        {
            Log(
                $"Warning: another adb.exe is running from {foreign}. " +
                "Close it (for example PdaNet) if the phone is not found or the link drops.");
        }

        SetState(TunnelState.Connecting, "Looking for the phone…");
        await _adb.EnsureServerAsync(ct).ConfigureAwait(false);
        var devices = await _adb.GetDevicesAsync(ct).ConfigureAwait(false);
        var device = SelectDevice(devices, serial);

        _adbSerial = device.Serial;
        Log($"USB device: {device.DisplayName}");

        SetState(TunnelState.Connecting, "Forwarding the USB link…");
        var localPort = GetFreeTcpPort();
        await _adb.ForwardAsync(device.Serial, localPort, "localabstract:sharenet", ct)
            .ConfigureAwait(false);
        _adbLocalPort = localPort;
        _forwardActive = true;

        var options = new PhoneLinkOptions("127.0.0.1", localPort, Token: null);

        SetState(TunnelState.Connecting, "Checking the phone app…");
        try
        {
            await PhoneLink.TestAsync(options, ct).ConfigureAwait(false);
        }
        catch (PhoneLinkException ex)
        {
            // A few devices cannot forward to abstract sockets. The phone also
            // listens on loopback TCP, so retry through that path before
            // failing the connection.
            Log($"USB abstract link failed ({ex.Message}); trying the TCP fallback");
            SetState(TunnelState.Connecting, "Trying the USB fallback…");

            await _adb.RemoveForwardAsync(device.Serial, localPort, ct).ConfigureAwait(false);
            localPort = GetFreeTcpPort();
            await _adb.ForwardAsync(
                    device.Serial,
                    localPort,
                    $"tcp:{TunnelServiceConstants.UsbFallbackPort}",
                    ct)
                .ConfigureAwait(false);
            _adbLocalPort = localPort;
            options = new PhoneLinkOptions("127.0.0.1", localPort, Token: null);

            try
            {
                await PhoneLink.TestAsync(options, ct).ConfigureAwait(false);
            }
            catch (PhoneLinkException fallbackEx)
            {
                throw new InvalidOperationException(
                    $"{fallbackEx.Message} Start sharing in the ShareNet app on the phone, and accept the USB debugging prompt.",
                    fallbackEx);
            }
        }

        await StartDataPathAsync(options, UsbMtu, ct).ConfigureAwait(false);

        _sessionCts = new CancellationTokenSource();
        Traffic.StartSession();
        SetState(TunnelState.Connected, $"Connected over USB ({device.Model})");
        Log("Tunnel is ready. Windows now uses the phone's connection.");
        BeginMonitoring(_tun!, _sessionCts.Token);
        BeginLinkWatchdog(options, _sessionCts.Token);
        _ = VerifyConnectivityAsync(_sessionCts.Token);
    }

    private async Task ConnectWifiCoreAsync(
        string host,
        string token,
        string? adapterName,
        CancellationToken ct)
    {
        host = host.Trim();
        if (!IPAddress.TryParse(host, out var phoneAddress) ||
            phoneAddress.AddressFamily != AddressFamily.InterNetwork)
        {
            throw new InvalidOperationException(
                "Enter the phone's IPv4 address exactly as shown in the ShareNet app.");
        }

        var normalizedToken = LinkProtocol.NormalizeToken(token);
        if (normalizedToken.Length is < 4 or > 64)
        {
            throw new InvalidOperationException("Enter the password shown in the ShareNet app.");
        }

        var adapter = NetworkHelper.FindForRemote(phoneAddress, adapterName)
            ?? throw new InvalidOperationException(
                "No active Wi-Fi or Ethernet adapter was found. Connect the PC to the phone's hotspot or the same Wi-Fi network.");

        // Wi-Fi sharing only works when the phone is a neighbour on the same
        // link as this adapter. Without this check an off-subnet address gets a
        // gateway route and the handshake just times out with no explanation.
        if (!NetworkHelper.SameSubnet(adapter.Address, adapter.PrefixLength, phoneAddress))
        {
            throw new InvalidOperationException(
                $"The phone address {phoneAddress} is not on the network this PC is using " +
                $"for Wi-Fi ({adapter.Name}, {adapter.Address}/{adapter.PrefixLength}). " +
                "Put the PC and the phone on the same Wi-Fi network (or join the phone's hotspot) " +
                "and use the address the ShareNet app shows on the phone.");
        }

        Log($"Wi-Fi link will use {adapter.Name} ({adapter.Address})");
        SetState(TunnelState.Connecting, "Preparing the tunnel…");
        await _routes.CleanupAsync(ct).ConfigureAwait(false);

        _phoneRouteAddress = phoneAddress;
        _phoneRouteInterface = adapter.Index;
        try
        {
            await _routes.AddPhoneRouteAsync(adapter, phoneAddress, ct).ConfigureAwait(false);
        }
        catch (Exception ex)
        {
            Log($"Phone route warning: {ex.Message}");
        }

        var options = new PhoneLinkOptions(
            host,
            TunnelServiceConstants.WifiPort,
            token,
            BindAddress: adapter.Address,
            InterfaceIndex: adapter.Index);

        SetState(TunnelState.Connecting, "Checking the phone app…");
        try
        {
            await PhoneLink.TestAsync(options, ct).ConfigureAwait(false);
        }
        catch (PhoneLinkException ex)
        {
            throw new InvalidOperationException(
                $"{ex.Message} Check that the PC and phone are on the same network, sharing is enabled, and Wi-Fi sharing is on.",
                ex);
        }

        await StartDataPathAsync(options, WifiMtu, ct).ConfigureAwait(false);

        _sessionCts = new CancellationTokenSource();
        Traffic.StartSession();
        SetState(TunnelState.Connected, $"Connected over Wi-Fi ({host})");
        Log("Tunnel is ready. Windows now uses the phone's connection.");
        BeginMonitoring(_tun!, _sessionCts.Token);
        BeginLinkWatchdog(options, _sessionCts.Token);
        _ = VerifyConnectivityAsync(_sessionCts.Token);
    }

    /// <summary>MTU for the cable path: adb multiplexes the link, so stay conservative.</summary>
    private const int UsbMtu = 1400;

    /// <summary>MTU for a direct Wi-Fi link, which carries full 1500-byte frames.</summary>
    private const int WifiMtu = 1500;

    private async Task StartDataPathAsync(PhoneLinkOptions options, int mtu, CancellationToken ct)
    {
        _linkOptions = options;
        var includeIpv6 = await ProbePhoneIpv6Async(options, ct).ConfigureAwait(false);

        _socks = Socks5Server.Start(
            token => PhoneLink.ConnectAsync(options, token),
            Log,
            Traffic);
        Log($"Internal SOCKS5 bridge: 127.0.0.1:{_socks.Port}");

        var engine = Tun2SocksRunner.FindExecutable()
            ?? throw new InvalidOperationException(
                "The tunnel engine is missing. Reinstall ShareNet (tools\\sharenet-tun2socks.exe).");

        if (Tun2SocksRunner.FindWintun() is null)
        {
            throw new InvalidOperationException(
                "wintun.dll is missing. Reinstall ShareNet (tools\\wintun.dll).");
        }

        SetState(TunnelState.Connecting, "Creating the virtual adapter…");
        _tun = new Tun2SocksRunner();
        _tun.Output += line => Log($"tun2socks: {line}");
        _tun.Start(engine, _socks.Port, mtu);
        Log($"Tunnel engine started (MTU {mtu})");

        if (!await NetworkHelper.WaitForAdapterAsync(
                RouteManager.AdapterName,
                TimeSpan.FromSeconds(20),
                ct).ConfigureAwait(false))
        {
            throw new InvalidOperationException(
                "The ShareNet virtual adapter did not appear. See the activity log for tun2socks details.");
        }

        if (!_tun.IsRunning)
        {
            throw new InvalidOperationException(
                "The tunnel engine stopped during startup. See the activity log for details.");
        }

        SetState(TunnelState.Connecting, "Configuring routes and DNS…");
        await _routes.ConfigureAsync(ct, includeIpv6).ConfigureAwait(false);
    }

    /// <summary>
    /// Checks whether the phone's VPN can carry IPv6. Without this, assigning
    /// the PC an IPv6 address and half-default routes makes apps prefer IPv6
    /// over a path the phone cannot use, so they fail instead of using IPv4.
    /// </summary>
    private async Task<bool> ProbePhoneIpv6Async(PhoneLinkOptions options, CancellationToken ct)
    {
        var targets = new[]
        {
            ("2606:4700:4700::1111", 443),
            ("2001:4860:4860::8888", 443)
        };

        foreach (var (host, port) in targets)
        {
            using var attempt = CancellationTokenSource.CreateLinkedTokenSource(ct);
            attempt.CancelAfter(TimeSpan.FromSeconds(4));
            try
            {
                using var link = await PhoneLink
                    .ConnectAsync(options, attempt.Token)
                    .ConfigureAwait(false);
                using var tcp = await link
                    .OpenTcpAsync(host, port, attempt.Token)
                    .ConfigureAwait(false);
                Log($"IPv6 works through the phone's VPN ({host}:{port})");
                return true;
            }
            catch (OperationCanceledException) when (!ct.IsCancellationRequested)
            {
                Log($"IPv6 probe timed out ({host}:{port})");
            }
            catch (Exception ex) when (!ct.IsCancellationRequested)
            {
                Log($"IPv6 probe failed ({host}:{port}): {ex.Message}");
            }
        }

        ct.ThrowIfCancellationRequested();
        return false;
    }

    /// <summary>
    /// While connected, probes the phone's link server every few seconds.
    /// Three consecutive failures mean the phone stopped sharing or the
    /// transport died (USB unplugged, Wi-Fi dropped), so the tunnel is torn
    /// down instead of leaving Windows pointed at a dead path.
    /// </summary>
    private void BeginLinkWatchdog(PhoneLinkOptions options, CancellationToken ct)
    {
        _linkOptions = options;
        _watchdogTask = LinkWatchdogAsync(options, ct);
    }

    private async Task LinkWatchdogAsync(PhoneLinkOptions options, CancellationToken ct)
    {
        const int strikesNeeded = 3;
        var strikes = 0;
        try
        {
            while (!ct.IsCancellationRequested)
            {
                await Task.Delay(TimeSpan.FromSeconds(5), ct).ConfigureAwait(false);

                try
                {
                    using var timeout = CancellationTokenSource.CreateLinkedTokenSource(ct);
                    timeout.CancelAfter(TimeSpan.FromSeconds(4));
                    await PhoneLink.TestAsync(options, timeout.Token).ConfigureAwait(false);
                    strikes = 0;
                }
                catch (OperationCanceledException) when (ct.IsCancellationRequested)
                {
                    return;
                }
                catch (Exception ex)
                {
                    strikes++;
                    Log($"Link check failed ({strikes}/{strikesNeeded}): {ex.Message}");
                    if (strikes < strikesNeeded)
                    {
                        continue;
                    }

                    await _gate.WaitAsync(CancellationToken.None).ConfigureAwait(false);
                    try
                    {
                        if (State == TunnelState.Connected)
                        {
                            Log("The phone link was lost. Disconnecting the PC automatically.");
                            await CleanupCoreAsync(CancellationToken.None).ConfigureAwait(false);
                            await ReleaseHotspotAsync().ConfigureAwait(false);
                            SetState(
                                TunnelState.Error,
                                "Not connected",
                                "The phone link was lost. Start sharing on the phone again, then connect.");
                        }
                    }
                    finally
                    {
                        _gate.Release();
                    }
                    return;
                }
            }
        }
        catch (OperationCanceledException)
        {
            // Normal shutdown.
        }
        catch (Exception ex)
        {
            Log($"Link watchdog warning: {ex.Message}");
        }
    }

    private AdbDevice SelectDevice(IReadOnlyList<AdbDevice> devices, string? serial)
    {
        if (!string.IsNullOrWhiteSpace(serial))
        {
            var requested = devices.FirstOrDefault(d =>
                d.Serial.Equals(serial, StringComparison.OrdinalIgnoreCase));
            if (requested is null)
            {
                throw new InvalidOperationException(
                    $"The selected phone ({serial}) is not connected. Reconnect it or press Refresh.");
            }
            EnsureUsable(requested);
            return requested;
        }

        var usable = devices.FirstOrDefault(d => d.IsUsable);
        if (usable is not null)
        {
            return usable;
        }

        if (devices.Count > 0)
        {
            EnsureUsable(devices[0]);
        }

        throw new InvalidOperationException(
            "No Android phone was found. Connect the USB cable, enable USB debugging, and accept the prompt on the phone.");
    }

    private static void EnsureUsable(AdbDevice device)
    {
        if (device.IsUsable)
        {
            return;
        }

        var hint = device.State.ToLowerInvariant() switch
        {
            "unauthorized" =>
                "USB debugging is not authorized. Unlock the phone and tap Allow on the USB debugging prompt.",
            "offline" =>
                "The phone is offline. Unplug the USB cable, plug it back in, and try again.",
            "no permissions" =>
                "Windows cannot access the phone's USB interface. Try a different cable or USB port.",
            _ =>
                $"The phone is not ready (state: {device.State}). Check USB debugging and reconnect the cable."
        };

        throw new InvalidOperationException(hint);
    }

    private void BeginMonitoring(Tun2SocksRunner tun, CancellationToken ct)
    {
        _monitorTask = MonitorAsync(tun, ct);
    }

    private async Task MonitorAsync(Tun2SocksRunner tun, CancellationToken ct)
    {
        try
        {
            while (!ct.IsCancellationRequested)
            {
                await Task.Delay(1000, ct).ConfigureAwait(false);
                if (tun.IsRunning)
                {
                    continue;
                }

                Log("The tunnel engine stopped unexpectedly");
                await _gate.WaitAsync(CancellationToken.None).ConfigureAwait(false);
                try
                {
                    if (State == TunnelState.Connected)
                    {
                        await CleanupCoreAsync(CancellationToken.None).ConfigureAwait(false);
                        SetState(
                            TunnelState.Error,
                            "Disconnected",
                            "The tunnel engine stopped unexpectedly. Reconnect to restore the tunnel.");
                    }
                }
                finally
                {
                    _gate.Release();
                }
                return;
            }
        }
        catch (OperationCanceledException)
        {
            // Normal shutdown.
        }
        catch (Exception ex)
        {
            Log($"Tunnel monitor warning: {ex.Message}");
        }
    }

    private async Task VerifyConnectivityAsync(CancellationToken ct)
    {
        try
        {
            // Routes and the NRPT policy were only just installed. Give the
            // resolver and the tunnel a moment, and retry rather than warning
            // on the normal first-query race.
            await Task.Delay(1500, ct).ConfigureAwait(false);

            Exception? lastError = null;
            for (var attempt = 0; attempt < 3; attempt++)
            {
                try
                {
                    using var timeout = CancellationTokenSource.CreateLinkedTokenSource(ct);
                    timeout.CancelAfter(TimeSpan.FromSeconds(10));
                    using var client = new TcpClient();
                    await client.ConnectAsync("www.msftconnecttest.com", 80, timeout.Token)
                        .ConfigureAwait(false);
                    Log("Connectivity check: TCP through the tunnel succeeded");
                    lastError = null;
                    break;
                }
                catch (OperationCanceledException) when (ct.IsCancellationRequested)
                {
                    throw;
                }
                catch (Exception ex)
                {
                    lastError = ex;
                }

                await Task.Delay(2000, ct).ConfigureAwait(false);
            }

            if (lastError is not null)
            {
                Log($"Connectivity check warning: {lastError.Message}");
            }

            Exception? dnsError = null;
            for (var attempt = 0; attempt < 3; attempt++)
            {
                try
                {
                    var lookup = Dns.GetHostAddressesAsync("example.com").WaitAsync(
                        TimeSpan.FromSeconds(10),
                        ct);
                    var addresses = await lookup.ConfigureAwait(false);
                    Log(addresses.Length > 0
                        ? $"DNS check: resolved example.com ({addresses.Length} addresses)"
                        : "DNS check: no addresses returned");
                    dnsError = null;
                    break;
                }
                catch (OperationCanceledException) when (ct.IsCancellationRequested)
                {
                    throw;
                }
                catch (Exception ex)
                {
                    dnsError = ex;
                }

                await Task.Delay(2000, ct).ConfigureAwait(false);
            }

            if (dnsError is not null)
            {
                Log($"DNS check warning: {dnsError.Message}");
            }
        }
        catch (OperationCanceledException)
        {
            // Disconnected.
        }
        catch (Exception ex)
        {
            Log($"Verification warning: {ex.Message}");
        }
    }

    private async Task FailAsync(Exception ex)
    {
        var message = ex is OperationCanceledException
            ? "Connection cancelled"
            : ex.Message;
        Log($"Connection failed: {message}");

        try
        {
            await CleanupCoreAsync(CancellationToken.None).ConfigureAwait(false);
        }
        catch (Exception cleanup)
        {
            Log($"Cleanup warning: {cleanup.Message}");
        }

        SetState(TunnelState.Error, "Not connected", message);
    }

    private async Task CleanupCoreAsync(CancellationToken ct)
    {
        try
        {
            _sessionCts?.Cancel();
        }
        catch
        {
            // ignore
        }

        Traffic.StopSession();

        // CleanupAsync removes the DNS policy in its own process first, so a
        // failure while touching the live adapter can no longer leave Windows
        // pointed at a dead tunnel resolver.
        try
        {
            await _routes.CleanupAsync(ct).ConfigureAwait(false);
        }
        catch (Exception ex)
        {
            Log($"Route cleanup warning: {ex.Message}");
        }

        if (_tun is not null)
        {
            try
            {
                await _tun.StopAsync(TimeSpan.FromSeconds(6)).ConfigureAwait(false);
            }
            catch (Exception ex)
            {
                Log($"Tunnel engine cleanup warning: {ex.Message}");
            }
            _tun.Dispose();
            _tun = null;
        }

        if (_socks is not null)
        {
            try
            {
                await _socks.DisposeAsync().ConfigureAwait(false);
            }
            catch (Exception ex)
            {
                Log($"SOCKS bridge cleanup warning: {ex.Message}");
            }
            _socks = null;
        }

        if (_forwardActive && _adb is not null && !string.IsNullOrWhiteSpace(_adbSerial))
        {
            try
            {
                await _adb.RemoveForwardAsync(_adbSerial, _adbLocalPort, ct).ConfigureAwait(false);
            }
            catch (Exception ex)
            {
                Log($"USB forward cleanup warning: {ex.Message}");
            }
            _forwardActive = false;
        }

        if (_phoneRouteAddress is not null)
        {
            await _routes.RemovePhoneRouteAsync(
                    _phoneRouteAddress,
                    _phoneRouteInterface,
                    ct)
                .ConfigureAwait(false);
            _phoneRouteAddress = null;
        }

        _monitorTask = null;
        _watchdogTask = null;
        _linkOptions = null;

        // The monitor may still hold a registration on this source. Let it be
        // collected instead of disposing while it can still be observed.
        _sessionCts = null;
    }

    private void SetState(TunnelState state, string status, string? error = null)
    {
        State = state;
        Status = status;
        LastError = error;
        try
        {
            Changed?.Invoke();
        }
        catch
        {
            // UI errors must not break the tunnel.
        }
    }

    private void Log(string message) => _logger.Write(message);

    private static int GetFreeTcpPort()
    {
        var listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Start();
        try
        {
            return ((IPEndPoint)listener.LocalEndpoint).Port;
        }
        finally
        {
            listener.Stop();
        }
    }

    public async ValueTask DisposeAsync()
    {
        if (_disposed)
        {
            return;
        }
        _disposed = true;

        try
        {
            await DisconnectAsync().WaitAsync(TimeSpan.FromSeconds(20)).ConfigureAwait(false);
        }
        catch
        {
            // Shutdown is best effort.
        }

        Traffic.Dispose();
        _gate.Dispose();
    }
}

public static class TunnelServiceConstants
{
    public const int WifiPort = 1080;

    /// <summary>Loopback TCP port the phone exposes for the USB fallback.</summary>
    public const int UsbFallbackPort = 30001;
}
