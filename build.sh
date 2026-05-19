#!/usr/bin/env bash
# build.sh — rebuild everything bacio: the web bundle (embedded into the
# CLI binary at /ui/), the CLI/TUI binary, the desktop frontend (Vite),
# the desktop's Wails bindings, and the desktop Go binary.
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
# Default is full build — everything in, nothing skipped. Opt out of the
# slower pieces when you know they're untouched. This script does NOT install
# the CLI binary anywhere on PATH — `go build ./...` just verifies it
# compiles. Install it explicitly with
# `go build -o ~/.local/bin/bacio ./cmd/bacio` from the worktree you want
# on PATH; that way sibling worktrees can rebuild without clobbering each
# other's installed binary.
#
# Usage:
#   ./build.sh                            # rebuild everything
#   ./build.sh --skip-web                 # skip the web bundle
#   ./build.sh --skip-desktop             # skip the desktop app
#   ./build.sh --skip-web --skip-desktop  # CLI/TUI only (inner loop)
#
# Run from the repo root.

set -euo pipefail

skip_desktop=0
skip_web=0
install_cli=0
for arg in "$@"; do
    case "$arg" in
        --skip-desktop) skip_desktop=1 ;;
        --skip-web) skip_web=1 ;;
        # --install is intentionally undocumented in --help / the header
        # comment. It exists as an escape hatch for the rare workflow
        # where rebuilding and installing in one step is more convenient
        # than the explicit two-step (build.sh, then
        # `go build -o ~/.local/bin/bacio ./cmd/bacio`). The default
        # remains "do not touch the binary on PATH" so sibling worktrees
        # can rebuild without clobbering each other.
        --install) install_cli=1 ;;
        -h|--help)
            sed -n '2,29p' "$0" | sed 's/^# \{0,1\}//'
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

# ---------- web bundle ----------
#
# Has to run BEFORE the CLI build because `//go:embed all:webui` in
# embed.go bakes the bundle into the binary at compile time.

if [ "$skip_web" -eq 0 ]; then
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

# ---------- main module: vet, test, build CLI/TUI ----------
#
# Build verifies the CLI/TUI compiles but deliberately does NOT install
# to ~/.local/bin/bacio — that would let a build in one worktree clobber
# the binary another worktree expects on PATH. Install explicitly when
# you want this worktree's binary on PATH:
#   go build -o ~/.local/bin/bacio ./cmd/bacio

echo "==> go vet ./..."
go vet ./...

echo "==> go test ./..."
go test ./...

echo "==> go build ./..."
go build ./...

if [ "$install_cli" -eq 1 ]; then
    echo "==> go build -o ~/.local/bin/bacio ./cmd/bacio (--install)"
    go build -o "$HOME/.local/bin/bacio" ./cmd/bacio
fi

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
