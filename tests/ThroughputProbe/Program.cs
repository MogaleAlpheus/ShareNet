using System.Diagnostics;
using System.Net;
using System.Text;
using ShareNet;

// Measures one HTTP download through the ShareNet link client, without
// tun2socks in the path. This isolates the USB/Wi-Fi link and the phone VPN.
//
// Usage: ThroughputProbe <forward-port> <url> [seconds]
var port = args.Length > 0 ? int.Parse(args[0]) : 30000;
var url = args.Length > 1 ? args[1] : "http://ipv4.download.thinkbroadband.com/10MB.zip";
var seconds = args.Length > 2 ? double.Parse(args[2]) : 30;

var uri = new Uri(url);
var host = uri.Host;
var targetPort = uri.IsDefaultPort ? (uri.Scheme == "https" ? 443 : 80) : uri.Port;
if (uri.Scheme != "http")
{
    Console.WriteLine("Only http:// URLs are supported; TLS is not part of the link probe.");
    return 2;
}

using var link = await PhoneLink.ConnectAsync(
    new PhoneLinkOptions("127.0.0.1", port, Token: null),
    CancellationToken.None);
using var tcp = await link.OpenTcpAsync(host, targetPort, CancellationToken.None);

var request = Encoding.ASCII.GetBytes(
    $"GET {uri.PathAndQuery} HTTP/1.1\r\nHost: {host}\r\n" +
    "User-Agent: ShareNet-ThroughputProbe/1.0\r\nConnection: close\r\n\r\n");
await tcp.Stream.WriteAsync(request);
await tcp.Stream.FlushAsync();

using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(seconds));
var buffer = new byte[256 * 1024];
var total = 0L;
var stopwatch = Stopwatch.StartNew();

try
{
    int read;
    while ((read = await tcp.Stream.ReadAsync(buffer, timeout.Token)) > 0)
    {
        total += read;
    }
}
catch (OperationCanceledException)
{
    // The timed sample is still valid.
}

stopwatch.Stop();
var secondsElapsed = Math.Max(stopwatch.Elapsed.TotalSeconds, 0.001);
Console.WriteLine(
    $"{total} bytes in {secondsElapsed:F3}s = {total / secondsElapsed / 1024 / 1024:F2} MiB/s " +
    $"({total * 8 / secondsElapsed / 1_000_000:F2} Mbit/s)");
return total > 0 ? 0 : 1;
