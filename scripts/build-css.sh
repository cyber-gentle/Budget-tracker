#!/usr/bin/env bash
# Builds internal/app/templates/css/tailwind.css with the Tailwind standalone CLI
# (no Node project needed). The CLI version is pinned and checksum-verified.
# Run after changing templates, scripts or tailwind.config.js, and commit the output.
set -euo pipefail

VERSION=3.4.17
cd "$(dirname "$0")/.."

case "$(uname -s)-$(uname -m)" in
  Linux-x86_64)   ASSET=tailwindcss-linux-x64;   SHA=7d24f7fa191d2193b78cd5f5a42a6093e14409521908529f42d80b11fde1f1d4 ;;
  Linux-aarch64)  ASSET=tailwindcss-linux-arm64; SHA=69b1378b8133192d7d2feb12a116fa12d035594f58db3eff215879e4ad8cf39b ;;
  Darwin-arm64)   ASSET=tailwindcss-macos-arm64; SHA=a1d0c7985759accca0bf12e51ac1dcbf0f6cf2fffb62e6e0f62d091c477a10a3 ;;
  Darwin-x86_64)  ASSET=tailwindcss-macos-x64;   SHA=6cbdad74be776c087ffa5e9a057512c54898f9fe8828d3362212dfe32fc933a3 ;;
  MINGW*|MSYS*)   ASSET=tailwindcss-windows-x64.exe; SHA=67f1c5e3f5a03406a7bf5badf5ada09b79f3ae78ec43450c15f7e983068da346 ;;
  *) echo "Unsupported platform: $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac

BIN=".cache/tailwind-$VERSION/$ASSET"
if [ ! -x "$BIN" ]; then
  mkdir -p "$(dirname "$BIN")"
  echo "Downloading Tailwind CLI v$VERSION ($ASSET)..."
  curl -fsSL -o "$BIN.tmp" "https://github.com/tailwindlabs/tailwindcss/releases/download/v$VERSION/$ASSET"
  if command -v sha256sum >/dev/null; then GOT=$(sha256sum "$BIN.tmp" | cut -d' ' -f1); else GOT=$(shasum -a 256 "$BIN.tmp" | cut -d' ' -f1); fi
  if [ "$GOT" != "$SHA" ]; then
    rm -f "$BIN.tmp"
    echo "Checksum mismatch for $ASSET (got $GOT, want $SHA)" >&2
    exit 1
  fi
  mv "$BIN.tmp" "$BIN" && chmod +x "$BIN"
fi

"$BIN" -c tailwind.config.js \
  -i internal/app/templates/css/tailwind.input.css \
  -o internal/app/templates/css/tailwind.css --minify
