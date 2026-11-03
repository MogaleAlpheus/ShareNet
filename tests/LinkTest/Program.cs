using System.Buffers.Binary;
using System.Net;
using System.Net.Sockets;
using System.Text;
using ShareNet;

var failures = 0;
void Check(bool condition, string what)
{
    Console.WriteLine((condition ? "PASS  " : "FAIL  ") + what);
    if (!condition) failures++;
}

const string Token = "TESTTOKEN1234567";

// A local TCP echo server that stands in for an internet host.
var echoListener = new TcpListener(IPAddress.Loopback, 0);
echoListener.Start();
var echoPort = ((IPEndPoint)echoListener.LocalEndpoint).Port;
_ = Task.Run(async () =>
{
    while (true)
    {
        TcpClient client;
        try { client = await echoListener.AcceptTcpClientAsync(); }
        catch { return; }
        _ = Task.Run(async () =>
        {
            using var c = client;
            var s = c.GetStream();
            var buffer = new byte[4096];
            try
            {
                int n;
                while ((n = await s.ReadAsync(buffer)) > 0)
                {
                    await s.WriteAsync(buffer.AsMemory(0, n));
                }
            }
            catch { }
        });
    }
});

using var phone = new MockPhone(Token);
Console.WriteLine($"Mock phone listening on {phone.Port}");

// 1. Direct link TCP round trip, with token auth.
try
{
    using var link = await PhoneLink.ConnectAsync(
        new PhoneLinkOptions("127.0.0.1", phone.Port, Token), CancellationToken.None);
    using var tcp = await link.OpenTcpAsync("127.0.0.1", echoPort, CancellationToken.None);
    var hello = Encoding.ASCII.GetBytes("hello-link");
    await tcp.Stream.WriteAsync(hello);
    var reply = new byte[hello.Length];
    await tcp.Stream.ReadExactlyAsync(reply);
    Check(Encoding.ASCII.GetString(reply) == "hello-link", "link TCP round trip");
}
catch (Exception ex) { Check(false, "link TCP round trip: " + ex.Message); }

// 2. Wrong password is rejected with the right status.
try
{
    using var link = await PhoneLink.ConnectAsync(
        new PhoneLinkOptions("127.0.0.1", phone.Port, "WRONG"), CancellationToken.None);
    Check(false, "wrong password rejected");
}
catch (PhoneLinkException ex)
{
    Check(ex.Status == LinkProtocol.StatusBadAuth, "wrong password rejected");
}
catch (Exception ex) { Check(false, "wrong password rejected: " + ex.Message); }

// 3. SOCKS5 CONNECT through the bridge.
using var traffic = new TrafficStats();
traffic.StartSession();
try
{
    await using var socks = Socks5Server.Start(
        ct => PhoneLink.ConnectAsync(new PhoneLinkOptions("127.0.0.1", phone.Port, Token), ct),
        line => Console.WriteLine("  socks: " + line),
        traffic);

    using var client = new TcpClient();
    await client.ConnectAsync(IPAddress.Loopback, socks.Port);
    var s = client.GetStream();

    await s.WriteAsync(new byte[] { 5, 1, 0 });
    var greeting = new byte[2];
    await s.ReadExactlyAsync(greeting);
    Check(greeting[0] == 5 && greeting[1] == 0, "SOCKS5 greeting");

    await s.WriteAsync(new byte[]
    {
        5, 1, 0, 1, 127, 0, 0, 1, (byte)(echoPort >> 8), (byte)(echoPort & 0xFF)
    });
    var connectReply = new byte[4];
    await s.ReadExactlyAsync(connectReply);
    Check(connectReply[0] == 5 && connectReply[1] == 0, "SOCKS5 CONNECT accepted");
    // Consume the rest of the reply (ATYP + address + port).
    var rest = new byte[connectReply[3] == 1 ? 6 : 18];
    await s.ReadExactlyAsync(rest);

    var payload = Encoding.ASCII.GetBytes("hello-socks");
    await s.WriteAsync(payload);
    var echoed = new byte[payload.Length];
    await s.ReadExactlyAsync(echoed);
    Check(Encoding.ASCII.GetString(echoed) == "hello-socks", "SOCKS5 TCP relay");
    Check(traffic.UploadBytes >= payload.Length && traffic.DownloadBytes >= payload.Length,
        "TCP metering counts both directions");
}
catch (Exception ex) { Check(false, "SOCKS5 TCP relay: " + ex.Message); }

// 4. SOCKS5 UDP ASSOCIATE through the bridge.
try
{
    await using var socks = Socks5Server.Start(
        ct => PhoneLink.ConnectAsync(new PhoneLinkOptions("127.0.0.1", phone.Port, Token), ct),
        line => Console.WriteLine("  socks: " + line),
        traffic);

    using var control = new TcpClient();
    await control.ConnectAsync(IPAddress.Loopback, socks.Port);
    var cs = control.GetStream();
    await cs.WriteAsync(new byte[] { 5, 1, 0 });
    var greet = new byte[2];
    await cs.ReadExactlyAsync(greet);

    await cs.WriteAsync(new byte[] { 5, 3, 0, 1, 0, 0, 0, 0, 0, 0 });
    var head = new byte[4];
    await cs.ReadExactlyAsync(head);
    var familyExtra = head[3] == 1 ? 6 : 18;
    var extra = new byte[familyExtra];
    await cs.ReadExactlyAsync(extra);
    var relayPort = (extra[familyExtra - 2] << 8) | extra[familyExtra - 1];
    Check(head[1] == 0 && relayPort != 0, "SOCKS5 UDP ASSOCIATE accepted");

    using var udp = new UdpClient(new IPEndPoint(IPAddress.Loopback, 0));
    var datagram = new byte[] { 0, 0, 0, 1, 127, 0, 0, 1, 0x1F, 0x90, (byte)'p', (byte)'i', (byte)'n', (byte)'g' };
    await udp.SendAsync(datagram, new IPEndPoint(IPAddress.Loopback, relayPort));

    var receive = udp.ReceiveAsync().WaitAsync(TimeSpan.FromSeconds(10));
    var result = await receive;
    var text = Encoding.ASCII.GetString(result.Buffer);
    Check(result.Buffer.Length >= 10 &&
          result.Buffer[3] == 1 &&
          result.Buffer[4] == 198 && result.Buffer[5] == 51 && result.Buffer[6] == 100 && result.Buffer[7] == 7 &&
          result.Buffer[8] == 0x27 && result.Buffer[9] == 0x0F &&
          text.EndsWith("pong", StringComparison.Ordinal),
        "SOCKS5 UDP relay (reply address and payload)");
    Check(traffic.UploadBytes >= 14 && traffic.DownloadBytes >= 4,
        "UDP metering counts both directions");
}
catch (Exception ex) { Check(false, "SOCKS5 UDP relay: " + ex.Message); }

Console.WriteLine(failures == 0 ? "ALL TESTS PASSED" : $"{failures} TEST(S) FAILED");
return failures == 0 ? 0 : 1;

/// <summary>Speaks the Android side of the ShareNet protocol for local testing.</summary>
sealed class MockPhone : IDisposable
{
    private readonly TcpListener _listener;
    private readonly string _token;
    private readonly CancellationTokenSource _cts = new();

    public MockPhone(string token)
    {
        _token = token;
        _listener = new TcpListener(IPAddress.Loopback, 0);
        _listener.Start();
        Port = ((IPEndPoint)_listener.LocalEndpoint).Port;
        _ = Task.Run(AcceptLoop);
    }

    public int Port { get; }

    private async Task AcceptLoop()
    {
        while (!_cts.IsCancellationRequested)
        {
            TcpClient client;
            try { client = await _listener.AcceptTcpClientAsync(_cts.Token); }
            catch { return; }
            _ = Task.Run(() => Handle(client));
        }
    }

    private async Task Handle(TcpClient client)
    {
        using var _ = client;
        try
        {
            var stream = client.GetStream();
            var magic = await ReadInt(stream);
            var version = await ReadU8(stream);
            if (magic != LinkProtocol.Magic || version != LinkProtocol.Version)
            {
                return;
            }

            var authMode = await ReadU8(stream);
            if (authMode == LinkProtocol.AuthToken)
            {
                var length = await ReadU8(stream);
                var tokenBytes = new byte[length];
                await stream.ReadExactlyAsync(tokenBytes);
                var supplied = LinkProtocol.NormalizeToken(Encoding.UTF8.GetString(tokenBytes));
                if (supplied != LinkProtocol.NormalizeToken(_token))
                {
                    await stream.WriteAsync(new[] { LinkProtocol.StatusBadAuth });
                    return;
                }
            }

            await stream.WriteAsync(new[] { LinkProtocol.StatusOk });

            var mode = await ReadU8(stream);
            if (mode == LinkProtocol.ModeTcp)
            {
                await HandleTcp(stream);
            }
            else if (mode == LinkProtocol.ModeUdp)
            {
                await HandleUdp(stream);
            }
        }
        catch
        {
            // Connection torn down.
        }
    }

    private static async Task HandleTcp(NetworkStream stream)
    {
        var target = await LinkProtocol.ReadAddressAsync(stream, CancellationToken.None);
        try
        {
            using var remote = new TcpClient();
            await remote.ConnectAsync(target.Host, target.Port);
            await stream.WriteAsync(new[] { LinkProtocol.StatusOk });

            var remoteStream = remote.GetStream();
            var up = Pump(stream, remoteStream);
            var down = Pump(remoteStream, stream);
            await Task.WhenAny(up, down);
        }
        catch
        {
            await stream.WriteAsync(new[] { LinkProtocol.StatusConnectFailed });
        }
    }

    private static async Task HandleUdp(NetworkStream stream)
    {
        while (true)
        {
            var frame = await LinkProtocol.ReadUdpFrameAsync(stream, CancellationToken.None);
            if (frame is null)
            {
                return;
            }

            // Reply from a synthetic server address so the test can verify the
            // SOCKS5 UDP response header is built from the phone's frame.
            var payload = Encoding.ASCII.GetBytes("pong");
            var address = new LinkProtocol.LinkAddress("198.51.100.7", 9999, IPAddress.Parse("198.51.100.7"));
            await LinkProtocol.WriteUdpFrameAsync(
                stream,
                address.Host,
                address.Port,
                payload,
                new SemaphoreSlim(1, 1),
                CancellationToken.None);
        }
    }

    private static async Task Pump(Stream from, Stream to)
    {
        var buffer = new byte[16384];
        try
        {
            int n;
            while ((n = await from.ReadAsync(buffer)) > 0)
            {
                await to.WriteAsync(buffer.AsMemory(0, n));
            }
        }
        catch { }
    }

    private static async Task<int> ReadInt(Stream stream)
    {
        var buffer = new byte[4];
        await stream.ReadExactlyAsync(buffer);
        return BinaryPrimitives.ReadInt32BigEndian(buffer);
    }

    private static async Task<byte> ReadU8(Stream stream)
    {
        var buffer = new byte[1];
        await stream.ReadExactlyAsync(buffer);
        return buffer[0];
    }

    public void Dispose()
    {
        _cts.Cancel();
        _listener.Stop();
        _cts.Dispose();
    }
}
