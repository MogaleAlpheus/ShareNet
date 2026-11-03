using System.Diagnostics;
using System.Text;
using ShareNet;

// Probes the real phone through adb forward with the same link client the
// Windows app uses: handshake, TCP CONNECT, one HTTP request.
var port = args.Length > 0 ? int.Parse(args[0]) : 30000;
var host = args.Length > 1 ? args[1] : "example.com";
var targetPort = args.Length > 2 ? int.Parse(args[2]) : 80;

var stopwatch = Stopwatch.StartNew();
try
{
    using var link = await PhoneLink.ConnectAsync(
        new PhoneLinkOptions("127.0.0.1", port, Token: null),
        CancellationToken.None);
    Console.WriteLine($"PASS  handshake on 127.0.0.1:{port} ({stopwatch.ElapsedMilliseconds} ms)");

    using var tcp = await link.OpenTcpAsync(host, targetPort, CancellationToken.None);
    Console.WriteLine($"PASS  CONNECT {host}:{targetPort} ({stopwatch.ElapsedMilliseconds} ms)");

    var request = Encoding.ASCII.GetBytes(
        $"GET / HTTP/1.0\r\nHost: {host}\r\nConnection: close\r\n\r\n");
    await tcp.Stream.WriteAsync(request);
    await tcp.Stream.FlushAsync();

    using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(20));
    using var response = new MemoryStream();
    var buffer = new byte[16 * 1024];
    int read;
    while ((read = await tcp.Stream.ReadAsync(buffer, timeout.Token)) > 0)
    {
        response.Write(buffer, 0, read);
    }

    var text = Encoding.ASCII.GetString(response.ToArray());
    var firstLine = text.Split('\n', 2)[0].Trim();
    if (text.Length == 0)
    {
        Console.WriteLine("FAIL  no HTTP response");
        return 1;
    }

    Console.WriteLine($"PASS  HTTP {firstLine} ({response.Length} bytes, {stopwatch.ElapsedMilliseconds} ms)");
    return 0;
}
catch (Exception ex)
{
    Console.WriteLine($"FAIL  {ex.GetType().Name}: {ex.Message}");
    return 1;
}
