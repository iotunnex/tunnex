#!/bin/bash
# S6.5a — build the Go privilege helper and stage it into build/helper for
# electron-builder to bundle as an extraResource. Ad-hoc-signed (no Developer ID —
# that's S6.5b). Usage: stage-helper.sh [mac|win]  (default: current platform).
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
HELPER_SRC="$(cd "$HERE/../../helper" && pwd)"
OUT="$(cd "$HERE/.." && pwd)/build/helper"
# Hermetic: wipe any prior target's artifacts so a win pack never bundles the mac
# helper (or vice versa) — extraResources copies the whole build/helper dir.
rm -rf "$OUT"
mkdir -p "$OUT"
export GOFLAGS=-mod=readonly

TARGET="${1:-$([ "$(uname)" = "Darwin" ] && echo mac || echo win)}"

case "$TARGET" in
  mac)
    echo ">> building UNIVERSAL macOS helper (CGO caller-auth: libproc)"
    CGO_ENABLED=1 GOARCH=arm64 CC="clang -arch arm64" go build -C "$HELPER_SRC" -o "$OUT/tunnex-helper.arm64" ./cmd/tunnex-helper
    CGO_ENABLED=1 GOARCH=amd64 CC="clang -arch x86_64" go build -C "$HELPER_SRC" -o "$OUT/tunnex-helper.amd64" ./cmd/tunnex-helper
    lipo -create -output "$OUT/tunnex-helper" "$OUT/tunnex-helper.arm64" "$OUT/tunnex-helper.amd64"
    rm -f "$OUT/tunnex-helper.arm64" "$OUT/tunnex-helper.amd64"
    # Ad-hoc sign so the copied-to-/usr/local binary can exec on Apple Silicon
    # (an unsigned/invalidated mach-o gets Killed:9). Re-signed again at install time.
    codesign --force --sign - --timestamp=none "$OUT/tunnex-helper"
    echo ">> staged: $OUT/tunnex-helper"
    lipo -info "$OUT/tunnex-helper"
    codesign -dv "$OUT/tunnex-helper" 2>&1 | grep -E 'Signature|Identifier' | head -2 || true
    ;;
  win)
    echo ">> building windows amd64 helper (pure Go — WFP/wintun via x/sys/windows)"
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -C "$HELPER_SRC" -o "$OUT/tunnex-helper.exe" ./cmd/tunnex-helper
    echo ">> staged: $OUT/tunnex-helper.exe"
    # Bundle the vendored wintun.dll (committed, MIT — see vendor/wintun/README.md).
    VENDORED="$(cd "$HERE/.." && pwd)/vendor/wintun/wintun.dll"
    if [ -f "$VENDORED" ]; then
      cp "$VENDORED" "$OUT/wintun.dll"
      echo ">> staged: $OUT/wintun.dll"
    else
      echo "!! vendor/wintun/wintun.dll missing — Windows data plane needs it."
    fi
    ;;
  *)
    echo "usage: stage-helper.sh [mac|win]" >&2; exit 2 ;;
esac
