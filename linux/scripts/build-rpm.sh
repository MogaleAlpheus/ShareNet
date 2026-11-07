#!/usr/bin/env bash
# Builds an RPM package for ShareNet (amd64 only).
# Requires: rpmbuild, rpmdevtools
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$here/.."
project_root="$(cd "$root/.." && pwd)"
cd "$root"

version="0.1.0"

echo "=== Building ShareNet RPM for amd64 ==="

# 1. Build the sharenet binary
echo "Building sharenet binary..."
bash scripts/build.sh

# 2. Fetch tun2socks engine
echo "Fetching tun2socks engine..."
bash scripts/fetch-engine.sh amd64

# 3. Fetch adb
echo "Fetching adb..."
bash scripts/fetch-adb.sh amd64

# 4. Create source tarball for rpmbuild
echo "Creating source tarball..."
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

srcdir="$tmpdir/sharenet-$version"
mkdir -p "$srcdir"

# Copy all needed files
cp -r dist "$srcdir/"
cp -r tools "$srcdir/"
cp -r packaging "$srcdir/"
cp -r "$project_root/logo" "$srcdir/"
cp README.md "$srcdir/"

# Create tarball
mkdir -p "$HOME/rpmbuild/SOURCES"
tar -C "$tmpdir" -czf "$HOME/rpmbuild/SOURCES/sharenet-$version.tar.gz" "sharenet-$version"

# 5. Build RPM
echo "Building RPM..."
rpmbuild -ba packaging/rpm/sharenet.spec

echo ""
echo "=== RPM built successfully ==="
echo "Find it at: ~/rpmbuild/RPMS/x86_64/sharenet-$version-1.*.x86_64.rpm"