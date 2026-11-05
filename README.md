# ShareNet

Share a phone's active VPN connection with a Windows PC as a transparent,
full-system tunnel. It is a clean-room PdaNet-style tool: no PdaNet code,
assets, name, or protocol is used.

- **Android app** (`android/`): relays TCP and UDP (including DNS) over USB
  (adb `localabstract`) or Wi-Fi (TCP 1080 + password).
- **Windows app** (`windows/ShareNet`): creates a signed WinTun adapter, runs
  tun2socks against an internal loopback SOCKS5 bridge, and installs half-default
  routes plus an NRPT DNS rule.
- **Linux client** (`linux/`): Fedora-first client with the same window, live
  traffic meters, remembered settings and hotspot join as the Windows app, using
  an `ip tuntap` device, `ip route` half-defaults and `resolvectl` for DNS. See
  `linux/README.md`.
- No Windows system proxy and no per-app proxy configuration. Every application
  uses the tunnel automatically.
- The phone app does **not** create a `VpnService`; it uses normal sockets so the
  phone's existing VPN applies to relayed traffic.

## Deliverables

- `dist/ShareNet-0.2.1-win-x64-setup.exe` - Windows installer (recommended);
  installs the app, the APK and the user README.
- `dist/ShareNet-0.2.1-win-x64-setup.exe.sha256` - SHA-256 checksum
  (`db61bcef00c41d2fc3bebf92e0e159d607b99d48e385a1e73d87e066910cc99d`).
- `dist/ShareNet-0.2.1-win-x64.zip` - portable Windows app with the APK and
  README.
- `dist/ShareNet-0.2.1-win-x64.zip.sha256` - SHA-256 checksum
  (`0ef6fb241096dfd8ee67536c3dc8c0f9b2401b70bad50f09515f0b13938e3384`).
- `dist/ShareNet-win-x64/ShareNet.exe` — self-contained Windows executable.
- `dist/ShareNet-win-x64/ShareNet-android.apk` — signed release APK.
- `android/app/build/outputs/apk/release/app-release.apk` — build output.

## Quick start

1. Install `ShareNet-android.apk`, connect the phone's VPN, choose a transport,
   and tap **Start sharing**. For Wi-Fi, join a network (or turn the phone's
   hotspot on) and note the password.
2. Install it with `ShareNet-0.2.1-win-x64-setup.exe`, or unzip the portable
   build and run `ShareNet.exe` as administrator.
3. USB: enable USB debugging, connect the cable, accept the prompt, select the
   phone, and click **Connect**.
   Wi-Fi: click **Find** — the PC locates the phone on the network by itself —
   then enter the password and click **Connect**. The address field can still be
   filled in by hand.
4. Reload open apps. Closing the window keeps the tunnel in the notification
   area; use the tray icon to disconnect or exit.

See `dist/ShareNet-win-x64/README.txt` for requirements, cleanup behavior, and
troubleshooting.

### Linux (Fedora)

```bash
cd linux
bash scripts/fetch-engine.sh
sudo bash scripts/install.sh     # binary, engine, menu entry, polkit action
sudo sharenet doctor
sudo sharenet ui                 # or: sudo sharenet ui --autoconnect
```

The window offers the same controls as the Windows app (USB device picker,
Wi-Fi address/password/adapter with Find, search-then-hotspot connect, live rates
and sparkline, activity log). The installer also ships the user guide, both
licences and the phone APK when built, and `scripts/uninstall.sh` reverses it.
`sudo sharenet connect --find` still works headless. Build, requirements,
routing/DNS details and troubleshooting are in `linux/README.md`.

## How it works

```
Windows apps
    │  normal IP traffic
    ▼
WinTun "ShareNet" (198.18.0.1/15)
    │
tun2socks ── SOCKS5 ── 127.0.0.1:<random>
    │
PhoneLink (link protocol v1 over adb forward or Wi-Fi)
    │
Android ShareNet ── normal sockets ── phone VPN ── internet
```

- IPv4 half-default routes `0.0.0.0/1` + `128.0.0.0/1` avoid replacing the
  physical default route, so cleanup is simple. IPv6 half-default routes
  `::/1` + `8000::/1` are added only when the phone's VPN passes an IPv6
  reachability probe; otherwise IPv6 is left alone so apps use IPv4 instead of
  trying a path the phone cannot carry.
- In Wi-Fi mode the phone link is bound to the physical adapter address and a
  `/32` host route to the phone is added, so the link never enters the tunnel.
- DNS is forced through the tunnel with an NRPT rule. The phone rewrites
  port-53 traffic to its own VPN DNS servers and restores the original source
  address, so public resolvers do not need to be reachable through the VPN.
- The SOCKS5 hop is internal and loopback-only; Windows never sees a proxy.
- USB normally uses `adb forward` to `localabstract:sharenet`. The phone also
  listens on loopback TCP 30001; if a device cannot forward the abstract socket,
  the PC retries with `adb forward tcp:X tcp:30001`. Both phone listeners
  restart automatically after a failure.

## Link protocol v1

Handshake: `SNET` (4) | version (1) | auth mode (1) | [length (1) | token].
Server replies with a status byte. The client then selects TCP (1) or UDP (2)
mode. TCP mode carries one address + a raw byte stream. UDP mode carries
length-prefixed `ATYP/ADDR/PORT + payload` frames.

## Repository layout

- `android/` — the Android app (Kotlin). No Gradle wrapper is committed; build
  with the portable Gradle in `_tools/`.
- `windows/ShareNet/` — the Windows app (WPF, .NET 8) plus the adb, tun2socks and
  wintun binaries it ships with (`tools/`).
- `linux/` — the Linux client (Go, no external modules): window and CLI, link
  protocol, SOCKS5 bridge, tun2socks supervision, TUN/routes/DNS setup, nmcli
  hotspot join, settings and install packaging.
- `tests/` — standalone test and probe projects that compile the Windows link
  sources directly against a mock phone. No phone required.
- `logo/` — logo source as SVG.
- `dist/` — local release output, not committed; rebuilds write here.
- `_tools/` — local portable toolchains, not committed.

## Build

Toolchains used for this build are in `_tools/` (portable, not part of the
release and not committed): JDK 21, Gradle 8.7, Android SDK 34 / build-tools
34.0.0, .NET 8 SDK.

### Android

```powershell
$env:JAVA_HOME = "$PWD\_tools\jdk21"
$env:ANDROID_HOME = "$PWD\_tools\android-sdk"
_tools\gradle-8.7\bin\gradle.bat -p android assembleRelease
```

The release signing key is generated at `android/sharenet-release.jks` and its
passwords are in `android/keystore.properties`. Keep both private.

### Windows

```powershell
_tools\dotnet\dotnet.exe publish windows\ShareNet\ShareNet.csproj `
  -c Release -r win-x64 --self-contained true -o dist\ShareNet-win-x64
```

### Windows installer

Publish first, then package the published folder:

```powershell
& "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe" -DAppVersion=0.2.1 `
  windows\installer\ShareNet.iss
```

Needs Inno Setup 6 (`winget install JRSoftware.InnoSetup`). Use `-D`, not `/D`:
ISCC treats a leading `/` as a path on Windows. The setup lands in `dist/`.

### Tests

```powershell
# Android protocol tests
_tools\gradle-8.7\bin\gradle.bat -p android testDebugUnitTest

# Windows link/SOCKS bridge against a mock phone
_tools\dotnet\dotnet.exe run --project tests\LinkTest\LinkTest.csproj -c Release
```

## Live traffic

Both apps meter the bytes they actually relay and sample the rate twice a
second. The phone and the PC show download and upload speed, peak rates, session
totals, and a sparkline of the last minute. Nothing throttles the link, and the
counters are never truncated.

## Status

- [x] Android app: USB + Wi-Fi listeners, token auth, TCP/UDP relay, DNS
  forwarding, foreground service, wake lock, battery-optimization shortcut,
  live traffic meters.
- [x] Windows app: WPF UI, tray icon, adb wrapper, WinTun/tun2socks, SOCKS5
  bridge, routes, NRPT DNS, cleanup, settings, single instance, live traffic
  meters in the window and tray tooltip.
- [x] Signed release APK and self-contained Windows build.
- [x] Real-phone USB test (0.2.1): handshake, TCP relay, UDP/DNS, traffic
  meters, both USB paths (abstract and loopback fallback), and fast stop/start
  recovery.
- [x] Full Windows end-to-end test (0.2.1, elevated, real phone and VPN): WinTun
  adapter, half-default routes, conditional IPv6, NRPT DNS, DNS and TCP/UDP
  relay, HTTP/HTTPS through the tunnel, adapter byte counters, and complete
  route/NRPT cleanup.
- [ ] Wi-Fi mode on real hardware (the protocol is covered by the mock-phone
  tests, but the phone was not on a shared network during this build).
- [ ] Bluetooth and reverse tethering (out of scope for v1).

## License

ShareNet's own source is released under the MIT license; see `LICENSE`. It is
an original implementation: no PdaNet code, assets, name, or protocol is used.

Bundled components keep their own terms, with the full texts in
`windows/ShareNet/tools/licenses/` (copied into the release):

- `sharenet-tun2socks.exe` — tun2socks, MIT.
- `wintun.dll` — WireGuard LLC's prebuilt binary license, which allows
  redistribution alongside software that uses it only through the documented
  API. Do not modify or reverse engineer the DLL.
- `adb.exe`, `AdbWinApi.dll`, `AdbWinUsbApi.dll` — Android SDK Platform-Tools,
  distributed by Google under the Android SDK terms rather than an
  open-source license.
