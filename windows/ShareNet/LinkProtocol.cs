using System.Buffers.Binary;
using System.Net;
using System.Text;

namespace ShareNet;

/// <summary>
/// Mirror of the Android app's link protocol v1.
///
/// Handshake: "SNET" (4 bytes) | version (1) | auth mode (1) | [token length (1) | token]
/// Server replies with a status byte.
/// The client then selects TCP mode (1) or UDP mode (2).
/// </summary>
public static class LinkProtocol
{
    public const int Magic = 0x534E4554; // "SNET"
    public const byte Version = 1;

    public const byte AuthNone = 0;
    public const byte AuthToken = 1;

    public const byte StatusOk = 0;
    public const byte StatusBadAuth = 1;
    public const byte StatusBadVersion = 2;
    public const byte StatusBadRequest = 3;
    public const byte StatusConnectFailed = 4;

    public const byte ModeTcp = 1;
    public const byte ModeUdp = 2;

    public const byte AtypIpv4 = 1;
    public const byte AtypDomain = 3;
    public const byte AtypIpv6 = 4;

    public const int MaxFrame = 65535;

    public sealed record LinkAddress(string Host, int Port, IPAddress? Ip)
    {
        public static LinkAddress From(string host, int port) => new(host, port, null);
    }

    public sealed record UdpFrame(LinkAddress Address, byte[] Payload);

    public static async Task<int> ReadIntAsync(Stream stream, CancellationToken ct)
    {
        var buffer = new byte[4];
        await stream.ReadExactlyAsync(buffer, ct).ConfigureAwait(false);
        return BinaryPrimitives.ReadInt32BigEndian(buffer);
    }

    public static async Task<byte> ReadU8Async(Stream stream, CancellationToken ct)
    {
        var buffer = new byte[1];
        await stream.ReadExactlyAsync(buffer, ct).ConfigureAwait(false);
        return buffer[0];
    }

    public static async Task<int> ReadU16Async(Stream stream, CancellationToken ct)
    {
        var buffer = new byte[2];
        await stream.ReadExactlyAsync(buffer, ct).ConfigureAwait(false);
        return BinaryPrimitives.ReadUInt16BigEndian(buffer);
    }

    public static Task WriteU8Async(Stream stream, byte value, CancellationToken ct)
    {
        var buffer = new[] { value };
        return stream.WriteAsync(buffer, ct).AsTask();
    }

    public static Task WriteU16Async(Stream stream, int value, CancellationToken ct)
    {
        var buffer = new byte[2];
        BinaryPrimitives.WriteUInt16BigEndian(buffer, (ushort)value);
        return stream.WriteAsync(buffer, ct).AsTask();
    }

    public static Task WriteIntAsync(Stream stream, int value, CancellationToken ct)
    {
        var buffer = new byte[4];
        BinaryPrimitives.WriteInt32BigEndian(buffer, value);
        return stream.WriteAsync(buffer, ct).AsTask();
    }

    public static async Task WriteAddressAsync(Stream stream, string host, int port, CancellationToken ct)
    {
        if ((uint)port > 65535)
        {
            throw new ArgumentOutOfRangeException(nameof(port));
        }

        if (IPAddress.TryParse(host, out var ip))
        {
            var bytes = ip.GetAddressBytes();
            await WriteU8Async(stream, bytes.Length == 4 ? AtypIpv4 : AtypIpv6, ct).ConfigureAwait(false);
            await stream.WriteAsync(bytes, ct).ConfigureAwait(false);
        }
        else
        {
            var bytes = Encoding.UTF8.GetBytes(host);
            if (bytes.Length is 0 or > 255)
            {
                throw new ArgumentException("Domain name must be 1-255 UTF-8 bytes", nameof(host));
            }
            await WriteU8Async(stream, AtypDomain, ct).ConfigureAwait(false);
            await WriteU8Async(stream, (byte)bytes.Length, ct).ConfigureAwait(false);
            await stream.WriteAsync(bytes, ct).ConfigureAwait(false);
        }

        await WriteU16Async(stream, port, ct).ConfigureAwait(false);
    }

    public static async Task<LinkAddress> ReadAddressAsync(Stream stream, CancellationToken ct)
    {
        var atyp = await ReadU8Async(stream, ct).ConfigureAwait(false);
        IPAddress? ip = null;
        string host;

        switch (atyp)
        {
            case AtypIpv4:
            {
                var bytes = new byte[4];
                await stream.ReadExactlyAsync(bytes, ct).ConfigureAwait(false);
                ip = new IPAddress(bytes);
                host = ip.ToString();
                break;
            }
            case AtypIpv6:
            {
                var bytes = new byte[16];
                await stream.ReadExactlyAsync(bytes, ct).ConfigureAwait(false);
                ip = new IPAddress(bytes);
                host = ip.ToString();
                break;
            }
            case AtypDomain:
            {
                var length = await ReadU8Async(stream, ct).ConfigureAwait(false);
                if (length == 0)
                {
                    throw new IOException("Empty domain name");
                }
                var bytes = new byte[length];
                await stream.ReadExactlyAsync(bytes, ct).ConfigureAwait(false);
                host = Encoding.UTF8.GetString(bytes);
                break;
            }
            default:
                throw new IOException($"Unsupported address type {atyp}");
        }

        var port = await ReadU16Async(stream, ct).ConfigureAwait(false);
        return new LinkAddress(host, port, ip);
    }

    public static async Task WriteUdpFrameAsync(
        Stream stream,
        string host,
        int port,
        ReadOnlyMemory<byte> payload,
        SemaphoreSlim writeGate,
        CancellationToken ct)
    {
        IPAddress? ip = IPAddress.TryParse(host, out var parsed) ? parsed : null;
        byte atyp;
        byte[] addressBytes;

        if (ip is not null)
        {
            addressBytes = ip.GetAddressBytes();
            atyp = addressBytes.Length == 4 ? AtypIpv4 : AtypIpv6;
        }
        else
        {
            addressBytes = Encoding.UTF8.GetBytes(host);
            if (addressBytes.Length is 0 or > 255)
            {
                throw new ArgumentException("Domain name must be 1-255 UTF-8 bytes", nameof(host));
            }
            atyp = AtypDomain;
        }

        var headerLength = 1 + addressBytes.Length + 2 + (atyp == AtypDomain ? 1 : 0);
        var frameLength = headerLength + payload.Length;
        if (frameLength > MaxFrame)
        {
            throw new IOException("UDP frame is too large");
        }

        await writeGate.WaitAsync(ct).ConfigureAwait(false);
        try
        {
            var lengthBytes = new byte[2];
            BinaryPrimitives.WriteUInt16BigEndian(lengthBytes, (ushort)frameLength);
            await stream.WriteAsync(lengthBytes, ct).ConfigureAwait(false);

            await WriteU8Async(stream, atyp, ct).ConfigureAwait(false);
            if (atyp == AtypDomain)
            {
                await WriteU8Async(stream, (byte)addressBytes.Length, ct).ConfigureAwait(false);
            }
            await stream.WriteAsync(addressBytes, ct).ConfigureAwait(false);
            await WriteU16Async(stream, port, ct).ConfigureAwait(false);
            await stream.WriteAsync(payload, ct).ConfigureAwait(false);
            // Do not flush after every UDP frame; let TCP batch frames
            // naturally. The length-prefixed framing allows the receiver to
            // parse frames correctly regardless of write boundaries.
        }
        finally
        {
            writeGate.Release();
        }
    }

    public static async Task<UdpFrame?> ReadUdpFrameAsync(Stream stream, CancellationToken ct)
    {
        int frameLength;
        try
        {
            frameLength = await ReadU16Async(stream, ct).ConfigureAwait(false);
        }
        catch (EndOfStreamException)
        {
            return null;
        }

        if (frameLength < 1)
        {
            throw new IOException($"Invalid UDP frame length {frameLength}");
        }

        var frame = new byte[frameLength];
        await stream.ReadExactlyAsync(frame, ct).ConfigureAwait(false);

        using var frameStream = new MemoryStream(frame, writable: false);
        var address = await ReadAddressAsync(frameStream, ct).ConfigureAwait(false);
        var payload = new byte[frameStream.Length - frameStream.Position];
        await frameStream.ReadExactlyAsync(payload, ct).ConfigureAwait(false);
        return new UdpFrame(address, payload);
    }

    public static string NormalizeToken(string value)
    {
        var builder = new StringBuilder(value.Length);
        foreach (var c in value.Trim().ToUpperInvariant())
        {
            if (c is '-' or ' ')
            {
                continue;
            }
            builder.Append(c);
        }
        return builder.ToString();
    }
}
