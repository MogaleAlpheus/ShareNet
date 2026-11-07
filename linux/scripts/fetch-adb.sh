#!/usr/bin/env bash
# Downloads the Linux adb binary for bundling in the RPM.
# Usage: ./scripts/fetch-adb.sh [amd64|arm64]
set -euo pipefail

machine="${1:-$(uname -m)}"

case "$machine" in
  x86_64|amd64)
    platform="linux"
    ;;
  aarch64|arm64)
    # Google doesn't provide arm64 Linux platform-tools; use system package instead
    echo "arm64 adb not available from Google; install via: sudo dnf install android-tools" >&2
    exit 1
    ;;
  *)
    echo "Unsupported architecture: $machine (expected amd64 or arm64)" >&2
    exit 1
    ;;
esac

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tools="$here/../tools"
mkdir -p "$tools"

# Latest platform-tools version (update when needed)
version="37.0.2"
url="https://dl.google.com/android/repository/platform-tools_r${version}-${platform}.zip"

echo "Downloading adb from $url"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

curl -fL --retry 3 --retry-delay 2 -o "$tmp/platform-tools.zip" "$url"

if ! command -v unzip >/dev/null 2>&1; then
  echo "unzip is required" >&2
  exit 1
fi

unzip -o -j "$tmp/platform-tools.zip" "platform-tools/adb" -d "$tmp" >/dev/null

install -m 0755 "$tmp/adb" "$tools/adb.new"
mv -f "$tools/adb.new" "$tools/adb"

echo "Installed $tools/adb"
"$tools/adb" --version