#!/usr/bin/env bash
# Builds a static linux/sharenet binary into linux/dist/.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$here/.."

version="${VERSION:-0.1.0}"
arch="${GOARCH:-amd64}"

mkdir -p dist
# Build beside the target and rename, so an install running at the same time
# never copies a half-written binary.
tmp="dist/sharenet.new"
rm -f "$tmp"
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build \
  -trimpath \
  -ldflags "-s -w -X main.Version=${version}" \
  -o "$tmp" \
  ./cmd/sharenet
mv -f "$tmp" dist/sharenet

echo "Built linux/dist/sharenet ($version, linux/$arch)"

# Same verification step the Windows release ships: a sidecar checksum.
if command -v sha256sum >/dev/null 2>&1; then
  (cd dist && sha256sum sharenet > sharenet.sha256)
  echo "SHA-256: $(cut -d' ' -f1 dist/sharenet.sha256)  (dist/sharenet.sha256)"
fi
