#!/usr/bin/env bash
# Fetch the XTLS/go-win7 patched Go SDK for the Windows 7 assets into
# .cache/go-win7, verified against scripts/go-win7.sha256.
set -euo pipefail

tag=patched-1.27.2
root=$(cd "$(dirname "$0")/.." && pwd)
dest="$root/.cache/go-win7"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
esac
zip="go-for-win7-$os-$arch.zip"

want=$(awk -v f="$zip" '$2 == f { print $1 }' "$root/scripts/go-win7.sha256")
if [ -z "$want" ]; then
  echo "fetch-go-win7: no checksum for $zip in scripts/go-win7.sha256" >&2
  exit 1
fi

if [ -x "$dest/bin/go" ] && [ "$(cat "$dest/.sha256" 2>/dev/null)" = "$want" ]; then
  exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL -o "$tmp/$zip" "https://github.com/XTLS/go-win7/releases/download/$tag/$zip"

if command -v sha256sum >/dev/null; then
  got=$(sha256sum "$tmp/$zip" | awk '{ print $1 }')
else
  got=$(shasum -a 256 "$tmp/$zip" | awk '{ print $1 }')
fi
if [ "$got" != "$want" ]; then
  echo "fetch-go-win7: checksum mismatch for $zip: got $got, want $want" >&2
  exit 1
fi

rm -rf "$dest"
mkdir -p "$dest"
unzip -q "$tmp/$zip" -d "$dest"
echo "$want" > "$dest/.sha256"
GOTOOLCHAIN=local "$dest/bin/go" version
