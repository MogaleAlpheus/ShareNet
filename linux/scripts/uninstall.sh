#!/usr/bin/env bash
# Removes ShareNet's installed files and restores the machine's networking.
# Settings are left in place, like the Windows uninstaller.
#
# Usage: sudo bash scripts/uninstall.sh [--prefix /usr/local]
set -euo pipefail

prefix="/usr/local"
while [ $# -gt 0 ]; do
  case "$1" in
    --prefix) prefix="${2:?--prefix needs a path}"; shift 2 ;;
    --prefix=*) prefix="${1#*=}"; shift ;;
    *) echo "Unknown argument: $1" >&2; exit 1 ;;
  esac
done

if [ "$(id -u)" -ne 0 ]; then
  echo "Run the uninstaller with sudo: sudo bash scripts/uninstall.sh" >&2
  exit 1
fi

if [ -x "$prefix/bin/sharenet" ]; then
  echo "Removing leftovers from a live or crashed session…"
  if ! "$prefix/bin/sharenet" cleanup; then
    echo "ShareNet is still running. Stop it (Ctrl+C or the window's Quit button) and re-run." >&2
    exit 1
  fi
fi

rm -f \
  "$prefix/bin/sharenet" \
  "$prefix/bin/tun2socks" \
  "$prefix/share/applications/sharenet.desktop" \
  "$prefix/share/icons/hicolor/scalable/apps/sharenet.svg" \
  "/usr/share/polkit-1/actions/com.sharenet.ui.policy"

rm -rf "$prefix/share/doc/sharenet" "$prefix/share/sharenet"

if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database "$prefix/share/applications" >/dev/null 2>&1 || true
fi

echo "ShareNet was removed."
echo "Saved settings were kept: /root/.config/sharenet/settings.json (and ~/.config/sharenet)."
