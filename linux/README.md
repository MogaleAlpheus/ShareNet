# ShareNet for Linux (Fedora)

The Linux client for ShareNet. It does what the Windows app does: it turns the
VPN running **on your phone** into a transparent tunnel for this machine, so
every program uses the phone's connection without a proxy, a VPN client, or
per-app configuration.

The phone app is unchanged; this client speaks the same link protocol v1 over USB
(adb) or Wi-Fi, and includes the same window, live traffic meters and remembered
settings as the Windows build.

```
programs ──> sharenet TUN (198.18.0.1/15) ──> tun2socks ──> 127.0.0.1:<port>
           ──> link protocol v1 ──> Android ShareNet app ──> phone VPN
```

## What matches the Windows app

| Windows | Linux |
| --- | --- |
| WPF window with connect panel, live traffic and activity log | Local window served on `127.0.0.1` and opened in your browser (`sharenet ui`) |
| USB device list with Scan, remembers the last phone | `adb devices -l`, Scan button, `lastSerial` remembered |
| Wi-Fi address, password, adapter, Find | Same fields, subnet scan on the chosen adapter |
| Hotspot join (`ShareNet-XXXX`, WPA2) | `nmcli` join with the same derived SSID and key, verified by a real handshake on the gateway |
| Password remembered with DPAPI | Desktop keyring (`secret-tool`), 0600 file fallback |
| Tray icon with live rates, connect/disconnect, Exit | Desktop notifications, window in the browser; the process keeps running when the tab closes |
| Single instance mutex | `flock` on `$XDG_RUNTIME_DIR/sharenet-session.lock` |
| Tray balloon on connect/failure | `notify-send`, delivered in your own session when elevated |
| `--autoconnect` | `sharenet ui --autoconnect` |
| Cleanup on startup | `sharenet ui` removes leftovers before the first connect |
| Installer, Start menu entry | `scripts/install.sh` with desktop entry, icon, polkit action, licences and the APK when built |

Two deliberate differences: there is **no tray icon** (a status-notifier icon
needs libraries outside the Go standard library), and the window is a local web
page rather than a native toolkit window. Closing the tab does not stop the
tunnel; `sharenet ui` keeps running until Ctrl+C, the window's Quit button, or
`sudo sharenet cleanup`.

## Requirements

- Fedora with `systemd-resolved` (the default) and `iproute2`
- `polkit` for the menu entry (installed by default on Fedora Workstation)
- Root, or `CAP_NET_ADMIN` plus `CAP_NET_RAW`, to create the TUN device and
  change routes and DNS
- `NetworkManager` (`nmcli`) only for joining the phone's own hotspot
- The tun2socks engine, pinned to v2.7.0 and checksum-verified by the fetch
  script, staged in `linux/tools/` (the installer does not download it)
- The phone runs the ShareNet Android app (Android 8 or newer); the app's
  automatic `ShareNet-XXXX` hotspot needs Android 11 or newer
- For USB: `android-tools` (adb) and USB debugging enabled on the phone
- For Wi-Fi: the phone and this machine on the same network

```bash
sudo dnf install iproute systemd-resolved android-tools unzip libnotify
sudo modprobe tun
```

## Install

The installer is **offline**. It copies the binary and the engine that are
already in the `linux/` folder tree and never builds or downloads anything, so
the same folder works on a machine with no network.

```bash
cd linux
sudo bash scripts/install.sh        # binary, engine, menu entry, icon, polkit action
```

If an artifact is missing it stops with an error naming the file instead of
fetching it.

### Preparing the payload

Do this once, on a machine with a network and the Go toolchain:

```bash
cd linux
bash scripts/build.sh               # dist/sharenet + dist/sharenet.sha256
bash scripts/fetch-engine.sh        # tools/tun2socks (v2.7.0, checksum-verified)
```

Then carry `linux/` over as a folder — zip it or copy it from a USB stick.
`dist/` and `tools/` are gitignored, so a `git clone` on the target brings
neither file and the installer will refuse to run.

The binary built above is `linux/amd64`. For another architecture, build with
`GOARCH=arm64 bash scripts/build.sh` and fetch the matching engine with
`bash scripts/fetch-engine.sh arm64` (both steps still happen on the connected
machine).

`install.sh` accepts `--prefix` (default `/usr/local`). It adds:

- `$prefix/bin/sharenet` and `$prefix/bin/tun2socks`
- a **ShareNet** menu entry that runs `pkexec sharenet ui`, asking for your
  password once per session
- `/usr/share/polkit-1/actions/com.sharenet.ui.policy` and the app icon
- `$prefix/share/doc/sharenet/` with the user guide and both licences
- `$prefix/share/sharenet/ShareNet-android.apk` when a built APK is present

`build.sh` writes `dist/sharenet` and `dist/sharenet.sha256` without installing.
To remove ShareNet again (settings are kept):

```bash
sudo bash scripts/uninstall.sh
```

## Install the phone app

Copy `ShareNet-android.apk` to the phone and open it, allowing installs from
this source when Android asks. The installer places a built APK at
`$prefix/share/sharenet/ShareNet-android.apk`; release downloads carry the same
file. Open it, choose a transport (USB, Wi-Fi, or both) and tap the power
button. The password belongs to that install and is shown in the app; the PC
needs it only the first time.

`adb` is not bundled: Fedora's `android-tools` package provides it, unlike the
Windows build which ships Google's platform-tools.

## Use the window

```bash
sudo sharenet ui                 # opens the window, USB selected
sudo sharenet ui --autoconnect   # connect straight away
sharenet ui --no-browser         # just print the local address
```

1. Start sharing in the Android app (USB or Wi-Fi).
2. USB: plug in the cable, press **Scan**, pick the phone, press **Connect**.
3. Wi-Fi: enter the password from the phone app and press **Connect** — the
   phone is searched for on the current network first, and the phone's hotspot
   is joined only when that search finds nothing. Typing an address or pressing
   **Find** pins the phone instead. **Join the phone's hotspot automatically**
   is on by default and can be turned off.
4. The right side shows live download/upload rates, a one-minute sparkline,
   peak rates, totals and the session clock; **Activity** expands the log.

The window's footer shows whether the process has root rights and whether the
engine was found. Every choice — transport, address, adapter, serial, hotspot
toggle and the Wi-Fi password — is remembered for the next launch.

## Use the terminal

```bash
sudo sharenet connect                       # USB if a phone is attached
sudo sharenet connect --wifi 192.168.1.20
sudo sharenet connect --find
sudo sharenet connect --hotspot --token 7K4P-9Q2M
sudo sharenet connect --usb --serial R58M123ABC --token 7K4P-9Q2M
sudo sharenet doctor
sudo sharenet cleanup
```

`connect` runs in the foreground, prints live rates every five seconds, and
restores networking on Ctrl+C. Flags override remembered settings; anything not
passed comes from the last session.

| Command | What it does |
| --- | --- |
| `sharenet ui` | window and session control |
| `sharenet connect` | connect and hold the tunnel open |
| `sharenet devices` | list phones attached over USB |
| `sharenet doctor` | check root, TUN, iproute2, DNS, engine (runs its `--version`), adb, nmcli, notify-send |
| `sharenet cleanup` | remove a leftover adapter, DNS link, hotspot profile and adb forwards |

Global options: `-v/--verbose`, `--engine PATH`, `--log FILE`, `--no-notify`,
`--version`.

`doctor` prints the engine's reported release. An engine that is not the pinned
v2.7.0 is listed as a warning, and `connect` logs the same mismatch before it
starts, because a newer or older release may not accept the flags ShareNet
passes. Neither refuses to run: the engine still gets its chance.

## The phone's hotspot

When **Join the phone's hotspot automatically** is on and no address is typed,
the client first searches the network this machine is already on and uses the
phone if it answers — both devices on one Wi-Fi never need a join. Only when that
search finds nothing does it:

1. derive the SSID as `ShareNet-` plus the first four letters and digits of the
   password (upper-cased), and the WPA2 key as the full normalized password —
   the same rules as the Windows app;
2. join it with `nmcli device wifi connect`;
3. wait up to 25 seconds for the ShareNet handshake on the new default gateway,
   so success means the phone answered, not that nmcli printed something;
4. connect the tunnel and pin the gateway route like any other Wi-Fi session.

On disconnect it brings the connection down and deletes the `ShareNet-*`
profile, so the machine returns to its usual network.

## What it changes on this machine

While connected:

- creates a TUN device named `sharenet`, address `198.18.0.1/15` (the
  benchmarking range, never a real network), MTU 1400 over USB and 1500 over
  Wi-Fi
- installs `0.0.0.0/1` and `128.0.0.0/1` instead of replacing the default route,
  so all traffic goes through the tunnel and cleanup is one link delete
- adds a `/32` host route for the phone through the physical adapter, so the link
  itself can never be pulled into the tunnel
- pins the Wi-Fi link socket to that adapter and source address
  (`SO_BINDTODEVICE`)
- points DNS at the tunnel with `resolvectl dns sharenet 1.1.1.1 8.8.8.8`,
  `resolvectl domain sharenet "~."` and `default-route yes`
- only enables IPv6 when the phone's VPN actually reaches an IPv6 address

On disconnect it reverts the DNS link, deletes the adapter and its routes, stops
the engine, removes the adb forward, and leaves the phone's hotspot network.

## Settings and the password

- Settings live in `~/.config/sharenet/settings.json` (mode 0600), or in
  `/root/.config/sharenet/settings.json` when the tunnel runs elevated.
- The Wi-Fi password is stored in the desktop keyring with
  `secret-tool` when one is reachable. Elevated sessions usually have no keyring
  bus, so it falls back to an encoded value in that 0600 file — `SHARENET_NO_KEYRING=1`
  forces the fallback.
- Leaving the password box empty reuses the saved one; typing a new password
  replaces it after a successful connection.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `must run as root` / `NO ROOT` badge | use `sudo sharenet ui` or the menu entry |
| Window does not open from the menu | `sudo sharenet ui` in a terminal and open the printed address |
| `/dev/net/tun is missing` | `sudo modprobe tun` |
| `the tunnel engine (tun2socks) is missing` | `tools/tun2socks` was not carried over; copy it or pass `--engine` |
| `adb was not found` | `sudo dnf install android-tools`, or use Wi-Fi |
| `Tunnel engine v2.x.y does not match the pinned v2.7.0` | stage the pinned engine with `bash scripts/fetch-engine.sh` on a connected machine and reinstall; a different release may reject ShareNet's flags |
| `the tunnel engine exited before the ShareNet adapter was ready` | the message quotes the engine's own complaint; reinstall with `scripts/install.sh` and run `sharenet doctor` |
| USB shows no phone | run `sharenet devices` and accept the prompt on the phone |
| phone shows as `unauthorized` | unlock it and accept the USB debugging prompt |
| phone shows as `no permissions` | add a udev rule for the vendor ID, or use Wi-Fi |
| `could not reach the phone` | start sharing in the app, check the address and password, check both devices share a network |
| hotspot join fails | `nmcli device wifi list` must show `ShareNet-XXXX`; the password must be the full one from the app |
| connected, but sites do not load | check the phone's VPN is up; `resolvectl status sharenet`; try `--verbose` |
| DNS leaks or fails | `systemctl status systemd-resolved`; `sudo sharenet cleanup`, then reconnect |
| `ShareNet is already running (pid N)` | use the open window, or `sudo sharenet cleanup` if it crashed |

`--verbose` shows per-connection and engine details. `--log /var/log/sharenet.log`
keeps a copy.

## Limitations

- **Fedora first.** Any systemd-resolved + iproute2 distribution should work, but
  only Fedora is tested.
- **No tray icon.** Notifications and the window replace it. A tray icon needs
  GTK/libappindicator, which would end the zero-dependency build.
- **One phone, one session.** The lock file makes a second `ui`/`connect` refuse
  to start instead of fighting over the adapter.
- **IPv6 is opportunistic.** Enabled only when the phone's VPN answers over IPv6;
  otherwise the tunnel stays IPv4-only rather than leaking IPv6 around it.
- **UDP is best effort.** DNS and QUIC go through the tunnel; applications that
  need UDP hole punching may not work through the netstack.
- **No suspend handling.** After resume, reconnect if traffic stops.

## Layout

```
linux/
  cmd/sharenet/        CLI and window entry point
  internal/link/       link protocol v1 client (TCP + UDP)
  internal/socks/      loopback SOCKS5 bridge for the engine
  internal/engine/     tun2socks process supervision
  internal/netcfg/     TUN addressing, routes, DNS, adapter list (Linux)
  internal/tunnel/     session orchestration, watchdog, hotspot, notifications
  internal/hotspot/    nmcli join/leave with handshake verification
  internal/webui/      the window: HTTP API, embedded page, live traffic
  internal/settings/   remembered choices and the protected password
  internal/adb/        adb forward management
  internal/discovery/  beacon + subnet scan for Wi-Fi
  internal/session/    the desktop user behind sudo/pkexec
  internal/instance/   single-instance lock
  internal/notify/     desktop notifications
  internal/mockphone/  test-only mock of the Android app
  packaging/           desktop entry, polkit action and licence texts
  scripts/             engine fetch, build, install and uninstall
```