using System.Diagnostics;
using System.Text.RegularExpressions;

namespace ShareNet;

public sealed record AdbDevice(string Serial, string State, string Model, string Description)
{
    public bool IsUsable => State.Equals("device", StringComparison.OrdinalIgnoreCase);

    public static AdbDevice Placeholder(string text) =>
        new(string.Empty, "placeholder", text, text);

    public string DisplayName
    {
        get
        {
            if (State.Equals("placeholder", StringComparison.OrdinalIgnoreCase))
            {
                return Model;
            }

            var name = string.IsNullOrWhiteSpace(Model) ? Serial : $"{Model} ({Serial})";
            return IsUsable ? name : $"{name} — {State}";
        }
    }
}

/// <summary>Thin wrapper around the bundled adb executable.</summary>
public sealed class AdbManager
{
    private readonly string _adbPath;

    public AdbManager(string adbPath)
    {
        _adbPath = adbPath;
    }

    public string AdbPath => _adbPath;

    public static string? FindAdb()
    {
        var explicitPath = Environment.GetEnvironmentVariable("SHARENET_ADB");
        if (!string.IsNullOrWhiteSpace(explicitPath) && File.Exists(explicitPath))
        {
            return explicitPath;
        }

        var candidates = new List<string>
        {
            Path.Combine(AppContext.BaseDirectory, "tools", "adb.exe"),
            Path.Combine(AppContext.BaseDirectory, "adb.exe")
        };

        // Development fallback: walk up to the repository's portable SDK.
        var directory = new DirectoryInfo(AppContext.BaseDirectory);
        for (var i = 0; i < 8 && directory is not null; i++)
        {
            candidates.Add(Path.Combine(
                directory.FullName,
                "_tools",
                "android-sdk",
                "platform-tools",
                "adb.exe"));
            directory = directory.Parent;
        }

        foreach (var candidate in candidates)
        {
            if (File.Exists(candidate))
            {
                return candidate;
            }
        }

        var path = Environment.GetEnvironmentVariable("PATH") ?? string.Empty;
        foreach (var folder in path.Split(Path.PathSeparator, StringSplitOptions.RemoveEmptyEntries))
        {
            try
            {
                var candidate = Path.Combine(folder.Trim(), "adb.exe");
                if (File.Exists(candidate))
                {
                    return candidate;
                }
            }
            catch
            {
                // Ignore malformed PATH entries.
            }
        }

        return null;
    }

    public async Task EnsureServerAsync(CancellationToken ct)
    {
        var result = await RunAsync(new[] { "start-server" }, TimeSpan.FromSeconds(30), ct)
            .ConfigureAwait(false);

        if (result.ExitCode != 0 &&
            !result.StandardError.Contains("already", StringComparison.OrdinalIgnoreCase))
        {
            var detail = FirstLine(result.StandardError, result.StandardOutput);
            var hint = detail.Contains("version", StringComparison.OrdinalIgnoreCase)
                ? " Another adb server (for example PdaNet's) may be using port 5037. Close it or set SHARENET_ADB."
                : string.Empty;
            throw new InvalidOperationException($"adb could not start: {detail}.{hint}");
        }
    }

    /// <summary>
    /// Returns adb executables other than the one ShareNet uses. A second adb
    /// server can fight over port 5037 and make the phone disappear.
    /// </summary>
    public static IReadOnlyList<string> FindForeignAdbProcesses(string adbPath)
    {
        var foreign = new List<string>();
        string own;
        try
        {
            own = Path.GetFullPath(adbPath);
        }
        catch
        {
            own = adbPath;
        }

        foreach (var process in Process.GetProcessesByName("adb"))
        {
            try
            {
                var path = process.MainModule?.FileName;
                if (string.IsNullOrWhiteSpace(path))
                {
                    continue;
                }

                var full = Path.GetFullPath(path);
                if (!full.Equals(own, StringComparison.OrdinalIgnoreCase) &&
                    !foreign.Contains(full, StringComparer.OrdinalIgnoreCase))
                {
                    foreign.Add(full);
                }
            }
            catch
            {
                // Access to another user's process is not required to continue.
            }
            finally
            {
                process.Dispose();
            }
        }

        return foreign;
    }

    public async Task<IReadOnlyList<AdbDevice>> GetDevicesAsync(CancellationToken ct)
    {
        var result = await RunAsync(new[] { "devices", "-l" }, TimeSpan.FromSeconds(20), ct)
            .ConfigureAwait(false);

        if (result.ExitCode != 0)
        {
            throw new InvalidOperationException(
                $"adb devices failed: {FirstLine(result.StandardError, result.StandardOutput)}");
        }

        var devices = new List<AdbDevice>();
        foreach (var rawLine in result.StandardOutput.Split('\n', StringSplitOptions.RemoveEmptyEntries))
        {
            var line = rawLine.Trim();
            if (line.Length == 0 ||
                line.StartsWith("List of devices", StringComparison.OrdinalIgnoreCase) ||
                line.StartsWith("* daemon", StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }

            var parts = Regex.Split(line, "\\s+");
            if (parts.Length < 2)
            {
                continue;
            }

            var serial = parts[0];
            var state = parts[1];
            var model = string.Empty;
            foreach (var part in parts.Skip(2))
            {
                if (part.StartsWith("model:", StringComparison.OrdinalIgnoreCase))
                {
                    model = part["model:".Length..].Replace('_', ' ');
                }
            }

            devices.Add(new AdbDevice(serial, state, model, line));
        }

        return devices;
    }

    public async Task ForwardAsync(string serial, int localPort, string remote, CancellationToken ct)
    {
        var result = await RunAsync(
                new[] { "-s", serial, "forward", $"tcp:{localPort}", remote },
                TimeSpan.FromSeconds(20),
                ct)
            .ConfigureAwait(false);

        if (result.ExitCode != 0)
        {
            throw new InvalidOperationException(
                $"adb forward failed: {FirstLine(result.StandardError, result.StandardOutput)}");
        }
    }

    public async Task RemoveForwardAsync(string serial, int localPort, CancellationToken ct)
    {
        await RunAsync(
                new[] { "-s", serial, "forward", "--remove", $"tcp:{localPort}" },
                TimeSpan.FromSeconds(10),
                ct)
            .ConfigureAwait(false);
    }

    public async Task RemoveShareNetForwardsAsync(CancellationToken ct)
    {
        var result = await RunAsync(new[] { "forward", "--list" }, TimeSpan.FromSeconds(15), ct)
            .ConfigureAwait(false);
        if (result.ExitCode != 0)
        {
            return;
        }

        foreach (var rawLine in result.StandardOutput.Split('\n', StringSplitOptions.RemoveEmptyEntries))
        {
            var line = rawLine.Trim();
            if (!line.Contains("localabstract:sharenet", StringComparison.OrdinalIgnoreCase) &&
                !line.Contains($"tcp:{TunnelServiceConstants.UsbFallbackPort}", StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }

            var parts = Regex.Split(line, "\\s+");
            if (parts.Length < 2)
            {
                continue;
            }

            var serial = parts[0];
            var local = parts[1];
            if (!local.StartsWith("tcp:", StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }

            await RunAsync(
                    new[] { "-s", serial, "forward", "--remove", local },
                    TimeSpan.FromSeconds(10),
                    ct)
                .ConfigureAwait(false);
        }
    }

    private async Task<ProcessResult> RunAsync(
        IReadOnlyList<string> arguments,
        TimeSpan timeout,
        CancellationToken ct)
    {
        var psi = new ProcessStartInfo(_adbPath)
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true
        };

        foreach (var argument in arguments)
        {
            psi.ArgumentList.Add(argument);
        }

        using var process = new Process { StartInfo = psi };
        if (!process.Start())
        {
            throw new InvalidOperationException("Could not start adb");
        }

        var stdout = process.StandardOutput.ReadToEndAsync(ct);
        var stderr = process.StandardError.ReadToEndAsync(ct);

        using var timeoutCts = CancellationTokenSource.CreateLinkedTokenSource(ct);
        timeoutCts.CancelAfter(timeout);

        try
        {
            await process.WaitForExitAsync(timeoutCts.Token).ConfigureAwait(false);
        }
        catch (OperationCanceledException) when (!ct.IsCancellationRequested)
        {
            try
            {
                process.Kill(entireProcessTree: true);
            }
            catch
            {
                // ignore
            }
            throw new TimeoutException("adb did not respond in time");
        }

        var output = await stdout.ConfigureAwait(false);
        var error = await stderr.ConfigureAwait(false);
        return new ProcessResult(process.ExitCode, output, error);
    }

    private static string FirstLine(string first, string second)
    {
        var text = !string.IsNullOrWhiteSpace(first) ? first : second;
        var line = text.Split('\n', StringSplitOptions.RemoveEmptyEntries).FirstOrDefault();
        return string.IsNullOrWhiteSpace(line) ? "unknown error" : line.Trim();
    }

    private sealed record ProcessResult(int ExitCode, string StandardOutput, string StandardError);
}
