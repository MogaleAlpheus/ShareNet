using System.Diagnostics;

namespace ShareNet;

/// <summary>Runs the bundled tun2socks engine that owns the WinTun adapter.</summary>
public sealed class Tun2SocksRunner : IDisposable
{
    private Process? _process;
    private bool _stopped;

    public event Action<string>? Output;
    public event Action<int>? Exited;

    public bool IsRunning
    {
        get
        {
            try
            {
                return _process is { HasExited: false };
            }
            catch
            {
                return false;
            }
        }
    }

    public static string? FindExecutable()
    {
        var candidates = new[]
        {
            Path.Combine(AppContext.BaseDirectory, "tools", "sharenet-tun2socks.exe"),
            Path.Combine(AppContext.BaseDirectory, "tools", "tun2socks.exe")
        };

        return candidates.FirstOrDefault(File.Exists);
    }

    public static string? FindWintun()
    {
        var candidate = Path.Combine(AppContext.BaseDirectory, "tools", "wintun.dll");
        return File.Exists(candidate) ? candidate : null;
    }

    public void Start(string executablePath, int socksPort, int mtu)
    {
        if (mtu is < 576 or > 1500)
        {
            throw new ArgumentOutOfRangeException(nameof(mtu), "The tunnel MTU must be between 576 and 1500.");
        }

        if (IsRunning)
        {
            throw new InvalidOperationException("The tunnel engine is already running");
        }

        var directory = Path.GetDirectoryName(executablePath)
            ?? throw new InvalidOperationException("Invalid tunnel engine path");
        var wintun = Path.Combine(directory, "wintun.dll");
        if (!File.Exists(wintun))
        {
            throw new FileNotFoundException(
                "wintun.dll is missing from the tools folder. Reinstall ShareNet.", wintun);
        }

        var psi = new ProcessStartInfo(executablePath)
        {
            WorkingDirectory = directory,
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true
        };

        psi.ArgumentList.Add("--device");
        // The engine registers its Windows adapter under the "tun" driver and
        // takes the adapter name from the URI host. "wintun://" is not a valid
        // driver name in this release and fails with "unsupported driver".
        psi.ArgumentList.Add("tun://ShareNet");
        psi.ArgumentList.Add("--proxy");
        psi.ArgumentList.Add($"socks5://127.0.0.1:{socksPort}");
        psi.ArgumentList.Add("--loglevel");
        psi.ArgumentList.Add("info");
        // USB keeps the conservative 1400 because the link is multiplexed through
        // adb; a direct Wi-Fi link carries a full 1500-byte frame like any other
        // Ethernet path, so it uses 1500 and spends ~7% fewer packets per byte.
        psi.ArgumentList.Add("--mtu");
        psi.ArgumentList.Add(mtu.ToString(System.Globalization.CultureInfo.InvariantCulture));

        // Remove netstack buffer ceilings so large transfers can use the full
        // link instead of being limited by a small default window.
        psi.ArgumentList.Add("--tcp-auto-tuning");
        psi.ArgumentList.Add("--tcp-sndbuf");
        psi.ArgumentList.Add("4MB");
        psi.ArgumentList.Add("--tcp-rcvbuf");
        psi.ArgumentList.Add("4MB");

        var process = new Process
        {
            StartInfo = psi,
            EnableRaisingEvents = true
        };

        process.OutputDataReceived += (_, args) =>
        {
            if (!string.IsNullOrWhiteSpace(args.Data))
            {
                Output?.Invoke(args.Data);
            }
        };
        process.ErrorDataReceived += (_, args) =>
        {
            if (!string.IsNullOrWhiteSpace(args.Data))
            {
                Output?.Invoke(args.Data);
            }
        };
        process.Exited += (_, _) =>
        {
            if (_stopped)
            {
                return;
            }

            var code = -1;
            try
            {
                code = process.ExitCode;
            }
            catch
            {
                // ignore
            }
            Exited?.Invoke(code);
        };

        if (!process.Start())
        {
            process.Dispose();
            throw new InvalidOperationException("Could not start the tunnel engine");
        }

        process.BeginOutputReadLine();
        process.BeginErrorReadLine();
        _process = process;
    }

    public async Task StopAsync(TimeSpan timeout)
    {
        _stopped = true;
        var process = _process;
        _process = null;
        if (process is null)
        {
            return;
        }

        try
        {
            if (!process.HasExited)
            {
                process.Kill(entireProcessTree: true);
            }
            await process.WaitForExitAsync().WaitAsync(timeout).ConfigureAwait(false);
        }
        catch
        {
            // The process is best effort; the adapter disappears with it.
        }
        finally
        {
            try
            {
                process.Dispose();
            }
            catch
            {
                // ignore
            }
        }
    }

    public void KillStaleProcesses()
    {
        foreach (var process in Process.GetProcessesByName("sharenet-tun2socks"))
        {
            try
            {
                process.Kill(entireProcessTree: true);
                process.WaitForExit(3000);
            }
            catch
            {
                // Another user's process or already gone.
            }
            finally
            {
                process.Dispose();
            }
        }
    }

    public void Dispose()
    {
        try
        {
            StopAsync(TimeSpan.FromSeconds(5)).GetAwaiter().GetResult();
        }
        catch
        {
            // ignore
        }
    }
}
