#!/usr/bin/env bash
# scripts/release.sh — Cross-compile bacio and package per-platform archives.
#
# Usage:
#   scripts/release.sh <version>
#
#   <version> is a semver tag like v0.1.0 (with optional pre-release suffix
#   such as v0.1.0-rc.1).
#
# Output (under dist/):
#   bacio-<version>-linux-amd64.tar.gz
#   bacio-<version>-linux-arm64.tar.gz
#   bacio-<version>-darwin-amd64.tar.gz
#   bacio-<version>-darwin-arm64.tar.gz
#   bacio-<version>-windows-amd64.zip
#   checksums.txt    (sha256 over every archive above)
#
# This script does NOT tag, push, or publish — it only builds artefacts.
# The release-bacio skill orchestrates the irreversible steps after this
# script has succeeded.

set -euo pipefail

VERSION="${1:-}"
if [[ -z "$VERSION" ]]; then
    echo "usage: $0 <version>   e.g. $0 v0.1.0" >&2
    exit 1
fi
if ! [[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.]+)?$ ]]; then
    echo "version $VERSION must look like vMAJOR.MINOR.PATCH (with optional -prerelease)" >&2
    exit 1
fi

# Run from repo root so paths like ./cmd/bacio and ./LICENSE resolve regardless
# of where the caller invoked us from.
cd "$(git rev-parse --show-toplevel)"

DIST="dist"
PKG="./cmd/bacio"
# `-X .../version.Version=$VERSION` makes `bacio --version` print the tag
# the binary was built from (e.g. v0.2.1) instead of the "dev" default.
LDFLAGS="-s -w -X github.com/mrgeoffrich/bacio/internal/version.Version=${VERSION}"

# The standard release matrix. Edit deliberately — keeping the platform set
# stable across versions makes life easier for downstream package managers
# and for users curl-installing a specific architecture.
PLATFORMS=(
    "linux amd64"
    "linux arm64"
    "darwin amd64"
    "darwin arm64"
    "windows amd64"
)

# Pick whichever sha256 helper is on PATH; both are common across dev
# machines (sha256sum is GNU-default, shasum ships with macOS via Perl).
if command -v sha256sum >/dev/null 2>&1; then
    SHA256=(sha256sum)
elif command -v shasum >/dev/null 2>&1; then
    SHA256=(shasum -a 256)
else
    echo "neither sha256sum nor shasum found on PATH" >&2
    exit 1
fi

rm -rf "$DIST"
mkdir -p "$DIST"

for entry in "${PLATFORMS[@]}"; do
    read -r os arch <<< "$entry"
    bin="bacio"
    [[ "$os" == "windows" ]] && bin="bacio.exe"

    base="bacio-${VERSION}-${os}-${arch}"
    stage="${DIST}/${base}"
    mkdir -p "$stage"

    echo "==> ${os}/${arch}"
    GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 \
        go build -trimpath -ldflags "$LDFLAGS" -o "${stage}/${bin}" "$PKG"

    # End users opening a tarball expect to find these. Don't fail the
    # build if a file is genuinely missing — release.sh shouldn't be the
    # thing that flags an absent LICENSE.
    cp -f LICENSE "$stage/" 2>/dev/null || true
    cp -f README.md "$stage/" 2>/dev/null || true

    if [[ "$os" == "windows" ]]; then
        (cd "$DIST" && zip -qr "${base}.zip" "$base")
    else
        tar -czf "${DIST}/${base}.tar.gz" -C "$DIST" "$base"
    fi
    rm -rf "$stage"
done

(cd "$DIST" && "${SHA256[@]}" bacio-*.tar.gz bacio-*.zip > checksums.txt)

echo
echo "Built artefacts in ${DIST}/:"
ls -lh "$DIST"
