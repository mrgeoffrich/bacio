#!/usr/bin/env bash
# scripts/render-scoop-manifest.sh — Emit the Scoop manifest for the
# given bacio version. Reads sha256sum-format lines from stdin (the same
# layout as dist/checksums.txt that scripts/release.sh produces) and
# writes the rendered manifest JSON to stdout.
#
# Usage:
#   scripts/render-scoop-manifest.sh <version-without-leading-v> < checksums.txt > bucket/bacio.json
#
# Example: scripts/render-scoop-manifest.sh 0.2.1 < dist/checksums.txt

set -euo pipefail

VERSION="${1:-}"
if [[ -z "$VERSION" ]]; then
    echo "usage: $0 <version>   e.g. $0 0.2.1" >&2
    exit 1
fi
# Refuse a leading "v" so generated `version` field is right.
if [[ "$VERSION" == v* ]]; then
    echo "version must not include a leading 'v' (got $VERSION)" >&2
    exit 1
fi

CHECKSUMS="$(cat)"

sha_for() {
    local suffix="$1"
    local match
    match="$(printf '%s\n' "$CHECKSUMS" | grep -E "[[:space:]]bacio-v${VERSION}-${suffix}\\.zip\$" || true)"
    if [[ -z "$match" ]]; then
        echo "missing checksum line for ${suffix} (looked for bacio-v${VERSION}-${suffix}.zip)" >&2
        exit 1
    fi
    awk '{print $1}' <<< "$match"
}

WIN_AMD64="$(sha_for windows-amd64)"

URL_BASE="https://github.com/mrgeoffrich/bacio/releases/download/v${VERSION}"

cat <<EOF
{
  "version": "${VERSION}",
  "description": "Local-first issue tracker for AI agents, with CLI and TUI",
  "homepage": "https://github.com/mrgeoffrich/bacio",
  "license": "MIT",
  "architecture": {
    "64bit": {
      "url": "${URL_BASE}/bacio-v${VERSION}-windows-amd64.zip",
      "hash": "${WIN_AMD64}",
      "extract_dir": "bacio-v${VERSION}-windows-amd64"
    }
  },
  "bin": "bacio.exe",
  "checkver": "github",
  "autoupdate": {
    "architecture": {
      "64bit": {
        "url": "https://github.com/mrgeoffrich/bacio/releases/download/v\$version/bacio-v\$version-windows-amd64.zip",
        "extract_dir": "bacio-v\$version-windows-amd64"
      }
    },
    "hash": {
      "url": "https://github.com/mrgeoffrich/bacio/releases/download/v\$version/checksums.txt",
      "regex": "([a-fA-F0-9]{64})\\\\s+\$basename"
    }
  }
}
EOF
