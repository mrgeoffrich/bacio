#!/usr/bin/env bash
# Build the browser-side WASM bundle for the bacio TUI demo.
#
# Outputs:
#   web/dist/bacio.wasm     — the Go binary, GOOS=js GOARCH=wasm
#   web/dist/wasm_exec.js   — Go's runtime glue, copied from $GOROOT
#
# These two files are what bacio-website pulls at build time. The
# web/ directory also holds a standalone index.html for local
# development — open it with any static file server.

set -euo pipefail

cd "$(dirname "$0")/.."

OUT_DIR="web/dist"
mkdir -p "$OUT_DIR"

echo "→ compiling Go to WASM"
GOOS=js GOARCH=wasm go build \
  -ldflags="-s -w" \
  -o "$OUT_DIR/bacio.wasm" \
  ./cmd/bacio-wasm

# Locate wasm_exec.js. Go 1.24+ moved it from $GOROOT/misc/wasm/ to
# $GOROOT/lib/wasm/; support both for forwards-compat.
GOROOT_DIR="$(go env GOROOT)"
WASM_EXEC=""
for candidate in \
  "$GOROOT_DIR/lib/wasm/wasm_exec.js" \
  "$GOROOT_DIR/misc/wasm/wasm_exec.js"
do
  if [[ -f "$candidate" ]]; then
    WASM_EXEC="$candidate"
    break
  fi
done

if [[ -z "$WASM_EXEC" ]]; then
  echo "could not find wasm_exec.js in \$GOROOT ($GOROOT_DIR)" >&2
  exit 1
fi

echo "→ copying $WASM_EXEC"
cp "$WASM_EXEC" "$OUT_DIR/wasm_exec.js"

SIZE_HUMAN=$(du -h "$OUT_DIR/bacio.wasm" | awk '{print $1}')
echo "✓ built bacio.wasm ($SIZE_HUMAN)"
