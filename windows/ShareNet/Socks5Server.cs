using System.Collections.Concurrent;
using System.Net;
using System.Net.Sockets;
using System.Text;

namespace ShareNet;

/// <summary>
/// A minimal SOCKS5 server bound to loopback. It is consumed only by the bundled
/// tun2socks process; Windows applications never see or configure it.
///
/// CONNECT opens one link connection per TCP stream. UDP ASSOCIATE opens one link
/// UDP association per SOCKS association and forwards datagrams in both directions.
/// </summary>
public sealed class Socks5Server : IAsyncDisposable
{
    private readonly TcpListener _listener;
    private readonly Func<CancellationToken, Task<PhoneLink>> _createLink;
    private readonly Action<string> _log;
    private readonly TrafficStats _stats;
    private readonly CancellationTokenSource _cts = new();
    private readonly ConcurrentDictionary<TcpClient, byte> _clients = new();
    private readonly ConcurrentDictionary<Task, byte> _tasks = new();
    private readonly Task _acceptLoop;

    private Socks5Server(
        TcpListener listener,
        Func<CancellationToken, Task<PhoneLink>> createLink,
        Action<string> log,
        TrafficStats stats)
    {
        _listener = listener;
        _createLink = createLink;
        _log = log;
        _stats = stats;
        Port = ((IPEndPoint)listener.LocalEndpoint).Port;
        _acceptLoop = AcceptLoopAsync();
    }

    public int Port { get; }

    public static Socks5Server Start(
        Func<CancellationToken, Task<PhoneLink>> createLink,
        Action<string> log,
        TrafficStats stats)
    {
        var listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Start(512);
        return new Socks5Server(listener, createLink, log, stats);
    }

    private async Task AcceptLoopAsync()
    {
        while (!_cts.IsCancellationRequested)
        {
            TcpClient client;
            try
            {
                client = await _listener.AcceptTcpClientAsync(_cts.Token).ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                break;
            }
            catch (ObjectDisposedException)
            {
                break;
            }
            catch (SocketException)
            {
                continue;
            }

            _clients[client] = 0;
            var task = HandleClientAsync(client, _cts.Token);
            _tasks[task] = 0;
            _ = task.ContinueWith(
                completed =>
                {
                    _tasks.TryRemove(completed, out _);
                },
                CancellationToken.None,
                TaskContinuationOptions.ExecuteSynchronously,
                TaskScheduler.Default);
        }
    }

    private async Task HandleClientAsync(TcpClient client, CancellationToken ct)
    {
        try
        {
            client.NoDelay = true;
            TrySetBuffers(client.Client);
            using var stream = client.GetStream();

            using (var handshake = CancellationTokenSource.CreateLinkedTokenSource(ct))
            {
                handshake.CancelAfter(TimeSpan.FromSeconds(20));

                var greeting = new byte[2];
                await stream.ReadExactlyAsync(greeting, handshake.Token).ConfigureAwait(false);
                if (greeting[0] != 5)
                {
                    return;
                }

                var methods = new byte[greeting[1]];
                await stream.ReadExactlyAsync(methods, handshake.Token).ConfigureAwait(false);
                if (!methods.Contains((byte)0))
                {
                    await stream.WriteAsync(new byte[] { 5, 0xFF }, handshake.Token).ConfigureAwait(false);
                    return;
                }

                await stream.WriteAsync(new byte[] { 5, 0 }, handshake.Token).ConfigureAwait(false);

                // Read VER, CMD, RSV. The following ATYP byte is consumed by
                // ReadAddressAsync, which uses the same wire layout as SOCKS5.
                var request = new byte[3];
                await stream.ReadExactlyAsync(request, handshake.Token).ConfigureAwait(false);
                if (request[0] != 5 || request[2] != 0)
                {
                    return;
                }

                var target = await LinkProtocol.ReadAddressAsync(stream, handshake.Token).ConfigureAwait(false);
                var command = request[1];

                switch (command)
                {
                    case 1:
                        await HandleConnectAsync(client, stream, target, ct).ConfigureAwait(false);
                        break;
                    case 3:
                        await HandleUdpAssociateAsync(stream, target, ct).ConfigureAwait(false);
                        break;
                    default:
                        await WriteReplyAsync(stream, 0x07, ct).ConfigureAwait(false);
                        break;
                }
            }
        }
        catch (OperationCanceledException)
        {
            // Shutdown or client timeout.
        }
        catch (EndOfStreamException)
        {
            // Client closed during the handshake.
        }
        catch (IOException)
        {
            // Broken pipe / reset.
        }
        catch (SocketException)
        {
            // Network failure.
        }
        catch (Exception ex)
        {
            _log($"SOCKS client error: {ex.Message}");
        }
        finally
        {
            _clients.TryRemove(client, out _);
            try
            {
                client.Dispose();
            }
            catch
            {
                // ignore
            }
        }
    }

    private async Task HandleConnectAsync(
        TcpClient client,
        NetworkStream clientStream,
        LinkProtocol.LinkAddress target,
        CancellationToken ct)
    {
        PhoneLink? pendingLink = null;
        LinkedTcp? linked = null;

        try
        {
            pendingLink = await _createLink(ct).ConfigureAwait(false);
            linked = await pendingLink.OpenTcpAsync(target.Host, target.Port, ct).ConfigureAwait(false);
            pendingLink = null; // linked now owns it

            await WriteReplyAsync(clientStream, 0, ct).ConfigureAwait(false);
            _log($"TCP {target.Host}:{target.Port}");

            await RelayAsync(client, clientStream, linked, ct).ConfigureAwait(false);
        }
        catch (PhoneLinkException ex)
        {
            var reply = ex.Status == LinkProtocol.StatusConnectFailed ? (byte)0x05 : (byte)0x01;
            await TryWriteReplyAsync(clientStream, reply).ConfigureAwait(false);
            _log($"TCP {target.Host}:{target.Port} failed: {ex.Message}");
        }
        catch (OperationCanceledException)
        {
            // Shutdown.
        }
        catch (Exception ex)
        {
            await TryWriteReplyAsync(clientStream, 0x01).ConfigureAwait(false);
            _log($"TCP {target.Host}:{target.Port} error: {ex.Message}");
        }
        finally
        {
            linked?.Dispose();
            pendingLink?.Dispose();
        }
    }

    private async Task RelayAsync(
        TcpClient client,
        NetworkStream clientStream,
        LinkedTcp linked,
        CancellationToken ct)
    {
        using var relayCts = CancellationTokenSource.CreateLinkedTokenSource(ct);

        var up = PumpAsync(
            clientStream,
            linked.Stream,
            _stats.AddUpload,
            () => linked.ShutdownSend(),
            relayCts.Token);

        var down = PumpAsync(
            linked.Stream,
            clientStream,
            _stats.AddDownload,
            () =>
            {
                try
                {
                    client.Client.Shutdown(SocketShutdown.Send);
                }
                catch
                {
                    // Peer already closed.
                }
            },
            relayCts.Token);

        try
        {
            await Task.WhenAll(up, down).ConfigureAwait(false);
        }
        catch (OperationCanceledException)
        {
            // Normal when either side disconnects.
        }
        catch (IOException)
        {
            // Reset.
        }
        catch (SocketException)
        {
            // Reset.
        }
    }

    private static async Task PumpAsync(
        Stream from,
        Stream to,
        Action<int> onBytes,
        Action onCompleted,
        CancellationToken ct)
    {
        // Large transfers should not be chopped into 16 KB pieces; 128 KB keeps
        // the relay busy without pinning excessive memory per connection.
        var buffer = new byte[128 * 1024];
        try
        {
            while (true)
            {
                var read = await from.ReadAsync(buffer, ct).ConfigureAwait(false);
                if (read <= 0)
                {
                    break;
                }
                await to.WriteAsync(buffer.AsMemory(0, read), ct).ConfigureAwait(false);
                onBytes(read);
            }
        }
        finally
        {
            try
            {
                onCompleted();
            }
            catch
            {
                // Best effort half-close.
            }
        }
    }

    private sealed class UdpClientState
    {
        public IPEndPoint? Endpoint;
    }

    private async Task HandleUdpAssociateAsync(
        NetworkStream control,
        LinkProtocol.LinkAddress clientHint,
        CancellationToken ct)
    {
        using var udp = new UdpClient(new IPEndPoint(IPAddress.Loopback, 0));
        TrySetBuffers(udp.Client);
        var local = (IPEndPoint)udp.Client.LocalEndPoint!;

        await WriteReplyAsync(control, 0, ct, local.Address, local.Port).ConfigureAwait(false);
        _log($"UDP association on 127.0.0.1:{local.Port}");

        using var associationCts = CancellationTokenSource.CreateLinkedTokenSource(ct);
        var state = new UdpClientState();

        if (clientHint.Ip is not null && !clientHint.Ip.Equals(IPAddress.Any) && clientHint.Port != 0)
        {
            state.Endpoint = new IPEndPoint(clientHint.Ip, clientHint.Port);
        }

        var udpTask = RunUdpAsync(udp, state, associationCts);

        // The TCP control connection defines the lifetime of the association.
        var drain = new byte[256];
        try
        {
            while (await control.ReadAsync(drain, associationCts.Token).ConfigureAwait(false) > 0)
            {
                // RFC 1928: data on the control connection is discarded.
            }
        }
        catch (OperationCanceledException)
        {
            // Shutdown.
        }
        catch (IOException)
        {
            // Client closed.
        }
        catch (SocketException)
        {
            // Client closed.
        }
        finally
        {
            associationCts.Cancel();
            try
            {
                await udpTask.ConfigureAwait(false);
            }
            catch (OperationCanceledException)
            {
                // Expected.
            }
            catch (Exception ex)
            {
                _log($"UDP association ended: {ex.Message}");
            }
        }
    }

    private async Task RunUdpAsync(UdpClient udp, UdpClientState state, CancellationTokenSource associationCts)
    {
        var ct = associationCts.Token;
        PhoneLink? pendingLink = null;
        UdpLink? udpLink = null;
        Task? receiveTask = null;
        var lastActivity = Environment.TickCount64;

        try
        {
            while (!ct.IsCancellationRequested)
            {
                UdpReceiveResult result;
                try
                {
                    result = await udp.ReceiveAsync(ct).ConfigureAwait(false);
                }
                catch (OperationCanceledException)
                {
                    break;
                }
                catch (ObjectDisposedException)
                {
                    break;
                }
                catch (SocketException ex)
                {
                    _log($"UDP receive error: {ex.Message}");
                    continue;
                }

                lastActivity = Environment.TickCount64;

                if (!TryParseDatagram(result.Buffer, out var host, out var port, out var payload))
                {
                    continue;
                }

                state.Endpoint = result.RemoteEndPoint;
                _stats.AddUpload(payload.Length);

                if (udpLink is null)
                {
                    pendingLink = await _createLink(ct).ConfigureAwait(false);
                    udpLink = await pendingLink.OpenUdpAsync(ct).ConfigureAwait(false);
                    pendingLink = null;

                    var current = udpLink;
                    receiveTask = Task.Run(
                        () => ReceiveFromPhoneAsync(udp, state, current, associationCts, ct),
                        CancellationToken.None);
                }

                try
                {
                    await udpLink.SendAsync(host, port, payload, ct).ConfigureAwait(false);
                }
                catch (Exception ex) when (ex is IOException or SocketException or ObjectDisposedException)
                {
                    _log($"UDP send failed: {ex.Message}");
                    associationCts.Cancel();
                    break;
                }

                if (Environment.TickCount64 - lastActivity > 30 * 60 * 1000)
                {
                    _log("UDP association idle timeout");
                    associationCts.Cancel();
                    break;
                }
            }
        }
        catch (OperationCanceledException)
        {
            // Shutdown.
        }
        catch (Exception ex)
        {
            _log($"UDP association error: {ex.Message}");
        }
        finally
        {
            try
            {
                associationCts.Cancel();
            }
            catch
            {
                // ignore
            }

            if (receiveTask is not null)
            {
                try
                {
                    await receiveTask.WaitAsync(TimeSpan.FromSeconds(3)).ConfigureAwait(false);
                }
                catch
                {
                    // Best effort.
                }
            }

            udpLink?.Dispose();
            pendingLink?.Dispose();
        }
    }

    private async Task ReceiveFromPhoneAsync(
        UdpClient udp,
        UdpClientState state,
        UdpLink link,
        CancellationTokenSource associationCts,
        CancellationToken ct)
    {
        try
        {
            while (!ct.IsCancellationRequested)
            {
                var frame = await link.ReceiveAsync(ct).ConfigureAwait(false);
                if (frame is null)
                {
                    break;
                }

                var endpoint = state.Endpoint;
                if (endpoint is null)
                {
                    continue;
                }

                _stats.AddDownload(frame.Payload.Length);
                var datagram = BuildDatagram(frame);
                await udp.SendAsync(datagram, endpoint, ct).ConfigureAwait(false);
            }
        }
        catch (OperationCanceledException)
        {
            // Normal.
        }
        catch (Exception ex) when (ex is IOException or SocketException or ObjectDisposedException)
        {
            _log($"UDP reply path closed: {ex.Message}");
            try
            {
                associationCts.Cancel();
            }
            catch
            {
                // ignore
            }
        }
        catch (Exception ex)
        {
            _log($"UDP reply error: {ex.Message}");
            try
            {
                associationCts.Cancel();
            }
            catch
            {
                // ignore
            }
        }
    }

    private static bool TryParseDatagram(
        byte[] datagram,
        out string host,
        out int port,
        out ReadOnlyMemory<byte> payload)
    {
        host = string.Empty;
        port = 0;
        payload = default;

        if (datagram.Length < 4 || datagram[2] != 0)
        {
            // Fragmented datagrams are not supported, and malformed ones are dropped.
            return false;
        }

        var offset = 3;
        var atyp = datagram[offset++];
        switch (atyp)
        {
            case LinkProtocol.AtypIpv4:
            {
                if (datagram.Length < offset + 4 + 2)
                {
                    return false;
                }
                host = new IPAddress(datagram.AsSpan(offset, 4)).ToString();
                offset += 4;
                break;
            }
            case LinkProtocol.AtypIpv6:
            {
                if (datagram.Length < offset + 16 + 2)
                {
                    return false;
                }
                host = new IPAddress(datagram.AsSpan(offset, 16)).ToString();
                offset += 16;
                break;
            }
            case LinkProtocol.AtypDomain:
            {
                if (datagram.Length < offset + 1)
                {
                    return false;
                }
                var length = datagram[offset++];
                if (length == 0 || datagram.Length < offset + length + 2)
                {
                    return false;
                }
                host = Encoding.UTF8.GetString(datagram, offset, length);
                offset += length;
                break;
            }
            default:
                return false;
        }

        port = (datagram[offset] << 8) | datagram[offset + 1];
        offset += 2;

        if (port == 0)
        {
            return false;
        }

        payload = datagram.AsMemory(offset);
        return true;
    }

    private static byte[] BuildDatagram(LinkProtocol.UdpFrame frame)
    {
        var addressBytes = frame.Address.Ip?.GetAddressBytes()
            ?? Encoding.UTF8.GetBytes(frame.Address.Host);
        var atyp = frame.Address.Ip is not null
            ? (byte)(addressBytes.Length == 4 ? LinkProtocol.AtypIpv4 : LinkProtocol.AtypIpv6)
            : LinkProtocol.AtypDomain;

        var headerLength = 3 + 1 + addressBytes.Length + (atyp == LinkProtocol.AtypDomain ? 1 : 0) + 2;
        var datagram = new byte[headerLength + frame.Payload.Length];

        datagram[0] = 0;
        datagram[1] = 0;
        datagram[2] = 0;
        datagram[3] = atyp;

        var offset = 4;
        if (atyp == LinkProtocol.AtypDomain)
        {
            datagram[offset++] = (byte)addressBytes.Length;
        }
        addressBytes.CopyTo(datagram, offset);
        offset += addressBytes.Length;

        datagram[offset++] = (byte)(frame.Address.Port >> 8);
        datagram[offset++] = (byte)(frame.Address.Port & 0xFF);
        frame.Payload.CopyTo(datagram, offset);

        return datagram;
    }

    private static void TrySetBuffers(Socket socket)
    {
        try
        {
            socket.ReceiveBufferSize = 1 << 20;
            socket.SendBufferSize = 1 << 20;
        }
        catch
        {
            // The system default is used when a buffer size is rejected.
        }
    }

    private static async Task WriteReplyAsync(
        Stream stream,
        byte reply,
        CancellationToken ct,
        IPAddress? boundAddress = null,
        int boundPort = 0)
    {
        var address = boundAddress ?? IPAddress.Any;
        var bytes = address.GetAddressBytes();
        var buffer = new byte[3 + 1 + bytes.Length + 2];
        buffer[0] = 5;
        buffer[1] = reply;
        buffer[2] = 0;
        buffer[3] = (byte)(bytes.Length == 4 ? LinkProtocol.AtypIpv4 : LinkProtocol.AtypIpv6);
        bytes.CopyTo(buffer, 4);
        buffer[^2] = (byte)(boundPort >> 8);
        buffer[^1] = (byte)(boundPort & 0xFF);
        await stream.WriteAsync(buffer, ct).ConfigureAwait(false);
        await stream.FlushAsync(ct).ConfigureAwait(false);
    }

    private static async Task TryWriteReplyAsync(Stream stream, byte reply)
    {
        try
        {
            await WriteReplyAsync(stream, reply, CancellationToken.None).ConfigureAwait(false);
        }
        catch
        {
            // Client already gone.
        }
    }

    public async ValueTask DisposeAsync()
    {
        try
        {
            _cts.Cancel();
        }
        catch
        {
            // ignore
        }

        try
        {
            _listener.Stop();
        }
        catch
        {
            // ignore
        }

        foreach (var client in _clients.Keys)
        {
            try
            {
                client.Dispose();
            }
            catch
            {
                // ignore
            }
        }

        try
        {
            await _acceptLoop.WaitAsync(TimeSpan.FromSeconds(2)).ConfigureAwait(false);
        }
        catch
        {
            // ignore
        }

        var tasks = _tasks.Keys.ToArray();
        if (tasks.Length > 0)
        {
            try
            {
                await Task.WhenAll(tasks).WaitAsync(TimeSpan.FromSeconds(3)).ConfigureAwait(false);
            }
            catch
            {
                // ignore
            }
        }

        _cts.Dispose();
    }
}
