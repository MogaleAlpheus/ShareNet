#!/usr/bin/env bash
# Installs ShareNet the way the Windows setup.exe does: binary, engine, a menu
# entry, an icon, the polkit action that lets the menu entry run elevated, the
# licences, and the phone app when a built APK is available.
#
# Offline by design: it only copies files that are already in this directory
# tree. It never builds and never downloads. Prepare the payload on a connected
# machine with scripts/build.sh and scripts/fetch-engine.sh, then carry linux/
# over as a folder.
#
# Usage: sudo bash scripts/install.sh [--prefix /usr/local]
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/.." && pwd)"
cd "$root"

prefix="/usr/local"
while [ $# -gt 0 ]; do
  case "$1" in
    --prefix) prefix="${2:?--prefix needs a path}"; shift 2 ;;
    --prefix=*) prefix="${1#*=}"; shift ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

if [ "$(id -u)" -ne 0 ]; then
  echo "Run the installer with sudo: sudo bash scripts/install.sh" >&2
  exit 1
fi

# Each artifact is looked up in the prepared payload folder first, then in the
# other build folder, so one copy of linux/ carries everything.
find_artifact() {
  for candidate in "$@"; do
    if [ -f "$candidate" ]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

binary="$(find_artifact dist/sharenet tools/sharenet)" || {
  echo "Missing the ShareNet binary (looked for dist/sharenet and tools/sharenet)." >&2
  echo "This installer copies local files only; it does not build or download." >&2
  echo "On a machine with Go installed, run linux/scripts/build.sh and copy dist/ over." >&2
  exit 1
}

engine="$(find_artifact tools/tun2socks dist/tun2socks)" || {
  echo "Missing the tunnel engine (looked for tools/tun2socks and dist/tun2socks)." >&2
  echo "This installer copies local files only; it does not build or download." >&2
  echo "On a machine with a network, run linux/scripts/fetch-engine.sh and copy tools/ over." >&2
  exit 1
}

echo "Installing to $prefix"
install -Dm755 "$binary" "$prefix/bin/sharenet"
install -Dm755 "$engine" "$prefix/bin/tun2socks"

logo="$root/../logo/ShareNet_Logo.svg"
if [ -f "$logo" ]; then
  install -Dm644 "$logo" "$prefix/share/icons/hicolor/scalable/apps/sharenet.svg"
else
  echo "Note: logo/ShareNet_Logo.svg not found; the menu entry will use a generic icon" >&2
fi

entry="$(mktemp)"
policy="$(mktemp)"
trap 'rm -f "$entry" "$policy"' EXIT

sed "s|@EXEC@|pkexec $prefix/bin/sharenet ui|" packaging/sharenet.desktop > "$entry"
sed "s|@EXEC@|$prefix/bin/sharenet|" packaging/com.sharenet.ui.policy > "$policy"

install -Dm644 "$entry" "$prefix/share/applications/sharenet.desktop"
install -Dm644 "$policy" "/usr/share/polkit-1/actions/com.sharenet.ui.policy"

# Licences and the user guide, like tools\licenses and README.txt on Windows.
docdir="$prefix/share/doc/sharenet"
install -Dm644 packaging/licenses/ShareNet-LICENSE.txt "$docdir/ShareNet-LICENSE.txt"
install -Dm644 packaging/licenses/tun2socks-LICENSE.txt "$docdir/tun2socks-LICENSE.txt"
install -Dm644 README.md "$docdir/README.md"

# The phone app, so this install is self-contained when the APK was built.
apk=""
for candidate in \
  "$root/../dist/ShareNet-android.apk" \
  "$root/../android/app/build/outputs/apk/release/app-release.apk"; do
  if [ -f "$candidate" ]; then
    apk="$candidate"
    break
  fi
done

if [ -n "$apk" ]; then
  install -Dm644 "$apk" "$prefix/share/sharenet/ShareNet-android.apk"
else
  echo "Note: no built APK found; build the phone app or copy it from a release" >&2
fi

if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database "$prefix/share/applications" >/dev/null 2>&1 || true
fi
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
  gtk-update-icon-cache -q -t "$prefix/share/icons/hicolor" >/dev/null 2>&1 || true
fi

cat <<EOF

ShareNet is installed.

  Menu:       ShareNet (asks for your password once per session)
  Terminal:   sudo sharenet ui
  Check:      sharenet doctor
  Connect:    sudo sharenet connect --hotspot --token <password>

  Phone app:  ${apk:-not installed (no built APK found)}
  User guide: $docdir/README.md
  Licences:   $docdir/

Settings and the remembered password live in /root/.config/sharenet/settings.json
because the tunnel runs with administrator rights. The desktop keyring is not
reachable from there, so the password falls back to that 0600 file.

Remove it again with: sudo bash scripts/uninstall.sh
EOF