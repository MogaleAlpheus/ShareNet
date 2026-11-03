using System.Net;
using System.Net.Sockets;

namespace ShareNet;

public sealed class PhoneLinkException : Exception
{
    public PhoneLinkException(string message, byte status = 0, Exception? inner = null)
        : base(message, inner)
    {
        Status = status;
    }

    public byte Status { get; }
}

public sealed record PhoneLinkOptions(
    string Host,
    int Port,
    string? Token,
    IPAddress? BindAddress = null,
    int? InterfaceIndex = null,
    TimeSpan? ConnectTimeout = null);

/// <summary>
/// One TCP connection to the phone's ShareNet link server, carrying a single
/// TCP stream or a single UDP association.
/// </summary>
public sealed class PhoneLink : IDisposable
{
    private readonly Socket _socket;
    private readonly NetworkStream _stream;
    private bool _disposed;

    private PhoneLink(Socket socket, NetworkStream stream)
    {
        _socket = socket;
        _stream = stream;
    }

    public static async Task<PhoneLink> ConnectAsync(PhoneLinkOptions options, CancellationToken ct)
    {
        if (!IPAddress.TryParse(options.Host, out var phoneAddress))
        {
            var addresses = await Dns.GetHostAddressesAsync(options.Host, ct).ConfigureAwait(false);
            phoneAddress = addresses.FirstOrDefault(a => a.AddressFamily == AddressFamily.InterNetwork)
                ?? addresses.FirstOrDefault()
                ?? throw new PhoneLinkException($"Unable to resolve {options.Host}");
        }

        var socket = new Socket(phoneAddress.AddressFamily, SocketType.Stream, ProtocolType.Tcp)
        {
            NoDelay = true
        };

        try
        {
            // The link carries every Windows connection; a bigger buffer keeps
            // high-BDP transfers from stalling on the socket window.
            socket.ReceiveBufferSize = 1 << 20;
            socket.SendBufferSize = 1 << 20;
        }
        catch
        {
            // Use the system default when a size is not accepted.
        }

        // Enable TCP keepalive to detect dead links (phone disconnect, USB pull)
        // without waiting for a write timeout. 10s idle, 3s interval, 3 retries.
        try
        {
            socket.SetSocketOption(SocketOptionLevel.Socket, SocketOptionName.KeepAlive, true);
            // Windows: KeepAliveTime=10000ms, KeepAliveInterval=3000ms
            var keepAliveValues = new byte[12];
            BitConverter.GetBytes(1u).CopyTo(keepAliveValues, 0);     // on/off
            BitConverter.GetBytes(10000u).CopyTo(keepAliveValues, 4); // idle time (ms)
            BitConverter.GetBytes(3000u).CopyTo(keepAliveValues, 8);  // interval (ms)
            socket.IOControl(IOControlCode.KeepAliveValues, keepAliveValues, null);
        }
        catch
        {
            // Platform may not support per-socket keepalive config; system default used.
        }

        // Windows uses the weak host model, so binding the source address alone
        // does not keep a socket on its adapter: once the tunnel's default route
        // is up, a reconnect to the phone can be pulled into the tunnel and loop.
        // IP_UNICAST_IF (31) pins the socket to the chosen interface; the index
        // travels in network byte order.
        if (options.InterfaceIndex is int interfaceIndex && interfaceIndex > 0)
        {
            try
            {
                socket.SetSocketOption(
                    SocketOptionLevel.IP,
                    (SocketOptionName)31,
                    new byte[]
                    {
                        (byte)(interfaceIndex >> 24),
                        (byte)(interfaceIndex >> 16),
                        (byte)(interfaceIndex >> 8),
                        (byte)interfaceIndex
                    });
            }
            catch
            {
                // Older stacks may reject it; the source bind and the pinned
                // host route still keep the link out of the tunnel.
            }
        }

        try
        {
            if (options.BindAddress is not null)
            {
                socket.Bind(new IPEndPoint(options.BindAddress, 0));
            }

            using var timeout = CancellationTokenSource.CreateLinkedTokenSource(ct);
            timeout.CancelAfter(options.ConnectTimeout ?? TimeSpan.FromSeconds(10));
            await socket.ConnectAsync(new IPEndPoint(phoneAddress, options.Port), timeout.Token)
                .ConfigureAwait(false);

            var stream = new NetworkStream(socket, ownsSocket: false);

            using (var handshakeTimeout = CancellationTokenSource.CreateLinkedTokenSource(ct))
            {
                handshakeTimeout.CancelAfter(TimeSpan.FromSeconds(10));
                await HandshakeAsync(stream, options, handshakeTimeout.Token).ConfigureAwait(false);
            }

            return new PhoneLink(socket, stream);
        }
        catch (PhoneLinkException)
        {
            socket.Dispose();
            throw;
        }
        catch (OperationCanceledException) when (!ct.IsCancellationRequested)
        {
            socket.Dispose();
            throw new PhoneLinkException($"Timed out connecting to {options.Host}:{options.Port}");
        }
        catch (Exception ex)
        {
            socket.Dispose();
            throw new PhoneLinkException($"Unable to reach the phone at {options.Host}:{options.Port}: {ex.Message}", 0, ex);
        }
    }

    public static async Task TestAsync(PhoneLinkOptions options, CancellationToken ct)
    {
        using var link = await ConnectAsync(options, ct).ConfigureAwait(false);
    }

    private static async Task HandshakeAsync(NetworkStream stream, PhoneLinkOptions options, CancellationToken ct)
    {
        await LinkProtocol.WriteIntAsync(stream, LinkProtocol.Magic, ct).ConfigureAwait(false);
        await LinkProtocol.WriteU8Async(stream, LinkProtocol.Version, ct).ConfigureAwait(false);

        if (string.IsNullOrEmpty(options.Token))
        {
            await LinkProtocol.WriteU8Async(stream, LinkProtocol.AuthNone, ct).ConfigureAwait(false);
        }
        else
        {
            await LinkProtocol.WriteU8Async(stream, LinkProtocol.AuthToken, ct).ConfigureAwait(false);
            var token = System.Text.Encoding.UTF8.GetBytes(LinkProtocol.NormalizeToken(options.Token));
            if (token.Length is 0 or > 128)
            {
                throw new PhoneLinkException("The password length is invalid");
            }
            await LinkProtocol.WriteU8Async(stream, (byte)token.Length, ct).ConfigureAwait(false);
            await stream.WriteAsync(token, ct).ConfigureAwait(false);
        }

        await stream.FlushAsync(ct).ConfigureAwait(false);

        byte status;
        try
        {
            status = await LinkProtocol.ReadU8Async(stream, ct).ConfigureAwait(false);
        }
        catch (Exception ex) when (ex is EndOfStreamException or IOException)
        {
            throw new PhoneLinkException("The phone closed the connection during the handshake", 0, ex);
        }

        switch (status)
        {
            case LinkProtocol.StatusOk:
                return;
            case LinkProtocol.StatusBadAuth:
                throw new PhoneLinkException("Wrong password. Check the password shown on the phone.", status);
            case LinkProtocol.StatusBadVersion:
                throw new PhoneLinkException("The phone and PC app versions are not compatible.", status);
            case LinkProtocol.StatusBadRequest:
                throw new PhoneLinkException("The phone rejected the request.", status);
            default:
                throw new PhoneLinkException($"The phone returned an unknown status ({status}).", status);
        }
    }

    public async Task<LinkedTcp> OpenTcpAsync(string host, int port, CancellationToken ct)
    {
        ThrowIfDisposed();

        await LinkProtocol.WriteU8Async(_stream, LinkProtocol.ModeTcp, ct).ConfigureAwait(false);
        await LinkProtocol.WriteAddressAsync(_stream, host, port, ct).ConfigureAwait(false);
        await _stream.FlushAsync(ct).ConfigureAwait(false);

        var status = await LinkProtocol.ReadU8Async(_stream, ct).ConfigureAwait(false);
        if (status != LinkProtocol.StatusOk)
        {
            throw new PhoneLinkException(
                status == LinkProtocol.StatusConnectFailed
                    ? $"The phone could not connect to {host}:{port}"
                    : $"The phone rejected the connection (status {status})",
                status);
        }

        return new LinkedTcp(this, _stream, _socket);
    }

    public async Task<UdpLink> OpenUdpAsync(CancellationToken ct)
    {
        ThrowIfDisposed();
        await LinkProtocol.WriteU8Async(_stream, LinkProtocol.ModeUdp, ct).ConfigureAwait(false);
        await _stream.FlushAsync(ct).ConfigureAwait(false);
        return new UdpLink(this, _stream, _socket);
    }

    private void ThrowIfDisposed()
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }
        _disposed = true;
        try
        {
            _stream.Dispose();
        }
        catch
        {
            // ignore
        }
        try
        {
            _socket.Dispose();
        }
        catch
        {
            // ignore
        }
    }
}

public sealed class LinkedTcp : IDisposable
{
    private readonly PhoneLink _owner;
    private readonly Socket _socket;
    private bool _disposed;

    internal LinkedTcp(PhoneLink owner, NetworkStream stream, Socket socket)
    {
        _owner = owner;
        Stream = stream;
        _socket = socket;
    }

    public NetworkStream Stream { get; }

    public void ShutdownSend()
    {
        try
        {
            _socket.Shutdown(SocketShutdown.Send);
        }
        catch
        {
            // The peer may already be gone.
        }
    }

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }
        _disposed = true;
        _owner.Dispose();
    }
}

public sealed class UdpLink : IDisposable
{
    private readonly PhoneLink _owner;
    private readonly NetworkStream _stream;
    private readonly Socket _socket;
    private readonly SemaphoreSlim _writeGate = new(1, 1);
    private bool _disposed;

    internal UdpLink(PhoneLink owner, NetworkStream stream, Socket socket)
    {
        _owner = owner;
        _stream = stream;
        _socket = socket;
    }

    public Task SendAsync(string host, int port, ReadOnlyMemory<byte> payload, CancellationToken ct)
        => LinkProtocol.WriteUdpFrameAsync(_stream, host, port, payload, _writeGate, ct);

    public Task<LinkProtocol.UdpFrame?> ReceiveAsync(CancellationToken ct)
        => LinkProtocol.ReadUdpFrameAsync(_stream, ct);

    public void Dispose()
    {
        if (_disposed)
        {
            return;
        }
        _disposed = true;
        _writeGate.Dispose();
        _owner.Dispose();
        try
        {
            _socket.Dispose();
        }
        catch
        {
            // ignore
        }
    }
}
