#!/usr/bin/env bash
# build.sh — rebuild everything bacio: the CLI/TUI binary, the desktop
# frontend (Vite), the desktop's Wails bindings, and the desktop Go binary.
#
# Why this exists: the desktop app is a separate nested Go module (`desktop/`,
# pinned via `replace github.com/mrgeoffrich/bacio => ../`) AND a React
# frontend, so `go build ./...` from the repo root only rebuilds the CLI/TUI
# half. After changes that touch the shared store / model / client packages
# (`internal/store`, `internal/model`, `internal/client`), the desktop binary
# needs a separate rebuild or it'll panic on schema mismatches. New methods on
# any Wails-bound service also need `wails3 generate bindings` to surface in
# the React side.
#
# Usage:
#   ./build.sh                 # rebuild everything
#   ./build.sh --skip-desktop  # CLI/TUI only (handy in the inner loop)
#   ./build.sh --web           # also build the web bundle into webui/
#                              # (BACI-30). Composes with --skip-desktop.
#
# Run from the repo root.

set -euo pipefail

skip_desktop=0
build_web=0
for arg in "$@"; do
    case "$arg" in
        --skip-desktop) skip_desktop=1 ;;
        --web) build_web=1 ;;
        -h|--help)
            sed -n '2,21p' "$0" | sed 's/^# \{0,1\}//'
            exit 0
            ;;
        *)
            echo "build.sh: unknown arg: $arg" >&2
            exit 2
            ;;
    esac
done

repo_root=$(cd "$(dirname "$0")" && pwd)
cd "$repo_root"

# ---------- optional: web bundle ----------
#
# Has to run BEFORE the CLI build because `//go:embed all:webui` in
# embed.go bakes the bundle into the binary at compile time. Skip if
# --web wasn't passed.

if [ "$build_web" -eq 1 ]; then
    echo "==> npm install (desktop frontend deps, for web build)"
    ( cd desktop/frontend && npm install --silent )

    echo "==> npm run build:web (Vite --mode web → desktop/frontend/dist-web/)"
    ( cd desktop/frontend && npm run build:web )

    echo "==> sync dist-web/ → webui/"
    # Wipe everything except the .gitkeep anchor, then copy the fresh
    # bundle in. rsync would be cleaner but isn't ubiquitous on macOS;
    # find + cp does the same with the tools every machine has.
    find webui -mindepth 1 -not -name .gitkeep -delete
    cp -R desktop/frontend/dist-web/. webui/
    ls -la webui | head -10
fi

# ---------- main module: vet, test, install CLI/TUI ----------

echo "==> go vet ./..."
go vet ./...

echo "==> go test ./..."
go test ./...

echo "==> go build -o ~/.local/bin/bacio ./cmd/bacio"
mkdir -p "$HOME/.local/bin"
go build -o "$HOME/.local/bin/bacio" ./cmd/bacio
ls -la "$HOME/.local/bin/bacio"

if [ "$skip_desktop" -eq 1 ]; then
    echo "==> --skip-desktop set; done."
    exit 0
fi

# ---------- desktop: bindings + Vite + Go ----------
#
# Wails handles everything in one `wails3 build`: it regenerates the
# TypeScript bindings from the service methods, runs `npm run build`
# (tsc + Vite), then `go build` for the desktop module. We also do a
# preflight `npm install` so a freshly-cloned worktree has node_modules
# before the build runs.

if ! command -v wails3 >/dev/null 2>&1; then
    echo "build.sh: wails3 not found on PATH — install with:" >&2
    echo "  go install github.com/wailsapp/wails/v3/cmd/wails3@latest" >&2
    exit 1
fi

echo "==> npm install (desktop frontend deps)"
( cd desktop/frontend && npm install --silent )

echo "==> wails3 build (regenerates bindings + builds frontend + Go)"
( cd desktop && wails3 build )
ls -la desktop/bin/bacio-desktop

echo "==> done. Restart any running desktop app to pick up the new binary."
