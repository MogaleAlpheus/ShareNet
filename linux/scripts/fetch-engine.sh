#!/usr/bin/env bash
# Downloads the pinned tun2socks release into linux/tools/tun2socks.
# Usage: ./scripts/fetch-engine.sh [amd64|arm64]
set -euo pipefail

version="${SHARENET_ENGINE_VERSION:-v2.7.0}"
machine="${1:-$(uname -m)}"

case "$machine" in
  x86_64|amd64)
    asset="tun2socks-linux-amd64.zip"
    checksum="a612baa287a3b6de6221f74fd02b442a50888508227ecf51e1288a5ccbb77381"
    ;;
  aarch64|arm64)
    asset="tun2socks-linux-arm64.zip"
    checksum="3931476c9cfa8fa236d23aeaf36767df0eb27cc11ecaab699faba57744450f49"
    ;;
  *)
    echo "Unsupported architecture: $machine (expected amd64 or arm64)" >&2
    exit 1
    ;;
esac

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tools="$here/../tools"
mkdir -p "$tools"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

url="https://github.com/xjasonlyu/tun2socks/releases/download/${version}/${asset}"
echo "Downloading $url"
curl -fL --retry 3 --retry-delay 2 -o "$tmp/$asset" "$url"

echo "$checksum  $tmp/$asset" | sha256sum -c -

if ! command -v unzip >/dev/null 2>&1; then
  echo "unzip is required: sudo dnf install unzip" >&2
  exit 1
fi

unzip -o -j "$tmp/$asset" -d "$tmp" >/dev/null
install -m 0755 "$tmp/${asset%.zip}" "$tools/tun2socks.new"
mv -f "$tools/tun2socks.new" "$tools/tun2socks"

echo "Installed $tools/tun2socks"
"$tools/tun2socks" --version || true
