using System.Diagnostics;
using System.Net;
using System.Security.Principal;

namespace ShareNet;

/// <summary>
/// Configures the WinTun adapter, routes, DNS policy, and the physical host route.
/// All changes are removed on disconnect and re-cleaned at startup.
/// </summary>
public sealed class RouteManager
{
    public const string AdapterName = "ShareNet";
    public const string TunnelIpv4 = "198.18.0.1";
    public const int TunnelPrefixLength = 15;
    public const string TunnelIpv6 = "fd00::1";
    public const int TunnelIpv6PrefixLength = 64;

    private readonly Action<string> _log;

    public RouteManager(Action<string> log)
    {
        _log = log;
    }

    public async Task ConfigureAsync(CancellationToken ct, bool includeIpv6 = true)
    {
        var ipv4Script = $$"""
            $ErrorActionPreference = 'Stop'
            $alias = '{{AdapterName}}'
            if (-not (Get-NetAdapter -Name $alias -ErrorAction SilentlyContinue)) {
                throw "The virtual adapter '$alias' was not created."
            }
            Get-NetRoute -InterfaceAlias $alias -ErrorAction SilentlyContinue | Remove-NetRoute -Confirm:$false -ErrorAction SilentlyContinue
            Get-NetIPAddress -InterfaceAlias $alias -ErrorAction SilentlyContinue | Remove-NetIPAddress -Confirm:$false -ErrorAction SilentlyContinue
            New-NetIPAddress -InterfaceAlias $alias -IPAddress '{{TunnelIpv4}}' -PrefixLength {{TunnelPrefixLength}} -AddressFamily IPv4 -ErrorAction Stop | Out-Null
            Set-NetIPInterface -InterfaceAlias $alias -AddressFamily IPv4 -InterfaceMetric 1 -ErrorAction Stop
            try {
                Set-DnsClientServerAddress -InterfaceAlias $alias -ServerAddresses @('1.1.1.1','8.8.8.8') -ErrorAction Stop
            } catch {
                Write-Output "DNS adapter warning: $($_.Exception.Message)"
            }
            New-NetRoute -InterfaceAlias $alias -DestinationPrefix '0.0.0.0/1' -NextHop '0.0.0.0' -RouteMetric 1 -ErrorAction Stop | Out-Null
            New-NetRoute -InterfaceAlias $alias -DestinationPrefix '128.0.0.0/1' -NextHop '0.0.0.0' -RouteMetric 1 -ErrorAction Stop | Out-Null
            Write-Output 'IPv4 routes installed'
            """;

        var dnsScript = """
            $ErrorActionPreference = 'SilentlyContinue'
            $alias = 'ShareNet'
            try {
                Get-DnsClientNrptRule -ErrorAction Stop | Where-Object { $_.DisplayName -eq 'ShareNet' } | ForEach-Object {
                    Remove-DnsClientNrptRule -Name $_.Name -Force -Confirm:$false -ErrorAction SilentlyContinue
                }
                Add-DnsClientNrptRule -Namespace '.' -NameServers @('1.1.1.1','8.8.8.8') -DisplayName 'ShareNet' -ErrorAction Stop | Out-Null
                Write-Output 'DNS policy installed'
            } catch {
                Write-Output "DNS policy warning: $($_.Exception.Message)"
            }
            Clear-DnsClientCache -ErrorAction SilentlyContinue
            """;

        var ipv4 = await RunAsync(ipv4Script, TimeSpan.FromSeconds(60), ct).ConfigureAwait(false);
        if (ipv4.ExitCode != 0)
        {
            throw new InvalidOperationException(
                $"Could not configure the virtual adapter: {Squash(ipv4.StandardError, ipv4.StandardOutput)}");
        }

        LogLines(ipv4);

        if (includeIpv6)
        {
            var ipv6Script = $$"""
                $ErrorActionPreference = 'SilentlyContinue'
                $alias = '{{AdapterName}}'
                try {
                    New-NetIPAddress -InterfaceAlias $alias -IPAddress '{{TunnelIpv6}}' -PrefixLength {{TunnelIpv6PrefixLength}} -AddressFamily IPv6 -ErrorAction Stop | Out-Null
                    Set-NetIPInterface -InterfaceAlias $alias -AddressFamily IPv6 -InterfaceMetric 1 -ErrorAction Stop
                    New-NetRoute -InterfaceAlias $alias -DestinationPrefix '::/1' -NextHop '::' -RouteMetric 1 -ErrorAction Stop | Out-Null
                    New-NetRoute -InterfaceAlias $alias -DestinationPrefix '8000::/1' -NextHop '::' -RouteMetric 1 -ErrorAction Stop | Out-Null
                    Write-Output 'IPv6 routes installed'
                } catch {
                    Write-Output "IPv6 skipped: $($_.Exception.Message)"
                }
                """;
            LogLines(await RunAsync(ipv6Script, TimeSpan.FromSeconds(60), ct).ConfigureAwait(false));
        }
        else
        {
            _log("IPv6 disabled: the phone's VPN has no IPv6 route.");
        }

        LogLines(await RunAsync(dnsScript, TimeSpan.FromSeconds(60), ct).ConfigureAwait(false));
    }

    private void LogLines(PowerShellResult result)
    {
        foreach (var line in (result.StandardOutput + "\n" + result.StandardError)
                     .Split('\n', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries))
        {
            _log(line);
        }
    }

    public async Task CleanupAsync(CancellationToken ct)
    {
        // Remove the DNS policy first and in its own process. Removing the
        // adapter address while tun2socks still owns the adapter can throw a
        // terminating error; when that happened in the old single script, the
        // NRPT rule was never reached and Windows kept the tunnel DNS policy.
        var dnsScript = """
            $ErrorActionPreference = 'Continue'
            $rules = @(Get-DnsClientNrptRule -ErrorAction SilentlyContinue |
                Where-Object { $_.DisplayName -eq 'ShareNet' })
            if ($rules.Count -eq 0) { exit 0 }
            $removed = 0
            foreach ($rule in $rules) {
                try {
                    Remove-DnsClientNrptRule -Name $rule.Name -Force -Confirm:$false -ErrorAction Stop
                    $removed++
                } catch {
                    Write-Output "DNS policy removal failed: $($_.Exception.Message)"
                }
            }
            $left = @(Get-DnsClientNrptRule -ErrorAction SilentlyContinue |
                Where-Object { $_.DisplayName -eq 'ShareNet' })
            if ($left.Count -gt 0) {
                Write-Output "DNS policy still present after cleanup: $($left.Count)"
                exit 1
            }
            Write-Output "Removed $removed ShareNet DNS policy rule(s)"
            Clear-DnsClientCache -ErrorAction SilentlyContinue
            """;

        try
        {
            var dns = await RunAsync(dnsScript, TimeSpan.FromSeconds(20), ct).ConfigureAwait(false);
            foreach (var line in (dns.StandardOutput + "\n" + dns.StandardError)
                         .Split('\n', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries))
            {
                _log(line);
            }
            if (dns.ExitCode != 0)
            {
                _log($"DNS policy cleanup warning: exit code {dns.ExitCode}");
            }
        }
        catch (Exception ex)
        {
            _log($"DNS policy cleanup warning: {ex.Message}");
        }

        var script = """
            $ErrorActionPreference = 'Continue'
            $alias = 'ShareNet'
            foreach ($route in @(Get-NetRoute -InterfaceAlias $alias -ErrorAction SilentlyContinue)) {
                try {
                    $route | Remove-NetRoute -Confirm:$false -ErrorAction Stop
                } catch {
                    Write-Output "Route removal failed: $($_.Exception.Message)"
                }
            }
            foreach ($address in @(Get-NetIPAddress -InterfaceAlias $alias -ErrorAction SilentlyContinue)) {
                try {
                    $address | Remove-NetIPAddress -Confirm:$false -ErrorAction Stop
                } catch {
                    Write-Output "Address removal failed: $($_.Exception.Message)"
                }
            }
            Clear-DnsClientCache -ErrorAction SilentlyContinue
            """;

        try
        {
            var result = await RunAsync(script, TimeSpan.FromSeconds(30), ct).ConfigureAwait(false);
            foreach (var line in (result.StandardOutput + "\n" + result.StandardError)
                         .Split('\n', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries))
            {
                _log(line);
            }
            _log("Removed previous ShareNet routes and DNS policy");
        }
        catch (Exception ex)
        {
            _log($"Route cleanup warning: {ex.Message}");
        }
    }

    public async Task AddPhoneRouteAsync(
        PhysicalAdapter adapter,
        IPAddress phoneAddress,
        CancellationToken ct)
    {
        var onLink = NetworkHelper.SameSubnet(adapter.Address, adapter.PrefixLength, phoneAddress);
        var nextHop = onLink || adapter.Gateway is null ? "0.0.0.0" : adapter.Gateway.ToString();

        var script = $$"""
            $ErrorActionPreference = 'Stop'
            $index = {{adapter.Index}}
            $prefix = '{{phoneAddress}}/32'
            Get-NetRoute -InterfaceIndex $index -DestinationPrefix $prefix -ErrorAction SilentlyContinue | Remove-NetRoute -Confirm:$false -ErrorAction SilentlyContinue
            New-NetRoute -InterfaceIndex $index -DestinationPrefix $prefix -NextHop '{{nextHop}}' -RouteMetric 1 -ErrorAction Stop | Out-Null
            Write-Output 'Phone route installed'
            """;

        var result = await RunAsync(script, TimeSpan.FromSeconds(30), ct).ConfigureAwait(false);
        if (result.ExitCode != 0)
        {
            throw new InvalidOperationException(
                $"Could not pin the phone connection to '{adapter.Name}': {Squash(result.StandardError, result.StandardOutput)}");
        }
    }

    public async Task RemovePhoneRouteAsync(
        IPAddress phoneAddress,
        int interfaceIndex,
        CancellationToken ct)
    {
        var script = $$"""
            $ErrorActionPreference = 'SilentlyContinue'
            Get-NetRoute -InterfaceIndex {{interfaceIndex}} -DestinationPrefix '{{phoneAddress}}/32' -ErrorAction SilentlyContinue | Remove-NetRoute -Confirm:$false -ErrorAction SilentlyContinue
            """;

        try
        {
            await RunAsync(script, TimeSpan.FromSeconds(15), ct).ConfigureAwait(false);
        }
        catch
        {
            // Best effort.
        }
    }

    private static async Task<PowerShellResult> RunAsync(
        string script,
        TimeSpan timeout,
        CancellationToken ct)
    {
        var psi = new ProcessStartInfo("powershell.exe")
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true
        };
        psi.ArgumentList.Add("-NoProfile");
        psi.ArgumentList.Add("-NonInteractive");
        psi.ArgumentList.Add("-ExecutionPolicy");
        psi.ArgumentList.Add("Bypass");
        psi.ArgumentList.Add("-Command");
        psi.ArgumentList.Add(script);

        using var process = new Process { StartInfo = psi };
        if (!process.Start())
        {
            throw new InvalidOperationException("Could not start PowerShell");
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
            throw new TimeoutException("PowerShell did not finish in time");
        }

        return new PowerShellResult(
            process.ExitCode,
            await stdout.ConfigureAwait(false),
            await stderr.ConfigureAwait(false));
    }

    private static string Squash(string first, string second)
    {
        var text = string.IsNullOrWhiteSpace(first) ? second : first;
        var line = text.Split('\n', StringSplitOptions.RemoveEmptyEntries).FirstOrDefault();
        return string.IsNullOrWhiteSpace(line) ? "unknown error" : line.Trim();
    }

    private sealed record PowerShellResult(int ExitCode, string StandardOutput, string StandardError);
}

public static class SecurityHelper
{
    public static bool IsAdministrator()
    {
        try
        {
            using var identity = WindowsIdentity.GetCurrent();
            var principal = new WindowsPrincipal(identity);
            return principal.IsInRole(WindowsBuiltInRole.Administrator);
        }
        catch
        {
            return false;
        }
    }
}
