#!/usr/bin/env bash
# Spin up a throwaway bacio playground for poking at the UI.
#
# Builds bacio from the current source tree, seeds the gelato DEMO
# dataset into a temp SQLite DB under /tmp, sets up a throwaway git
# working tree alongside it (so `bacio issue create` and friends have
# somewhere to live), and drops you into the TUI looking at the DEMO
# repo. Your real ~/.bacio/db.sqlite is never touched.
#
# Usage:
#   ./scripts/test.sh                # build + seed large + launch TUI
#   ./scripts/test.sh small          # base dataset (4 features, ~38 issues)
#   ./scripts/test.sh large          # 10× (40 features, ~380 issues) [default]
#   ./scripts/test.sh --no-launch    # seed only, print follow-up commands

set -euo pipefail

cd "$(dirname "$0")/.."

WORKSPACE=/tmp/bacio-test-workspace
DB=/tmp/bacio-test.db
BIN=/tmp/bacio-test-bin

SIZE=large
LAUNCH=1

usage() {
	sed -n '2,14p' "$0"
}

for arg in "$@"; do
	case "$arg" in
		small|large) SIZE="$arg" ;;
		--no-launch) LAUNCH=0 ;;
		-h|--help) usage; exit 0 ;;
		*) echo "test.sh: unknown arg '$arg'" >&2; usage >&2; exit 2 ;;
	esac
done

echo "→ building bacio"
go build -o "$BIN" ./cmd/bacio

# Stable temp workspace so re-running lands in the same place. The
# git repo is just for commands like `bacio issue create` that
# auto-register from the cwd; the DEMO repo itself is synthetic and
# lives only in the SQLite store.
mkdir -p "$WORKSPACE"
if [ ! -d "$WORKSPACE/.git" ]; then
	echo "→ initialising git workspace at $WORKSPACE"
	(
		cd "$WORKSPACE"
		git init -q
		git config user.email test@bacio.local
		git config user.name "bacio test"
		: > README.md
		git add README.md
		git commit -qm "init"
	)
fi

echo "→ seeding DEMO ($SIZE) into $DB"
"$BIN" --db "$DB" demo "$SIZE" --reset

cat <<INFO

Playground ready.

  DB:        $DB
  Workspace: $WORKSPACE
  Binary:    $BIN

Useful commands (always pass --db so you don't hit your real DB):

  $BIN --db $DB tui --repo DEMO
  $BIN --db $DB issue list --all-repos
  $BIN --db $DB history --repo DEMO

Or alias for the rest of the shell session:

  alias bt='$BIN --db $DB'
  bt tui --repo DEMO

INFO

if [ "$LAUNCH" = "1" ]; then
	exec "$BIN" --db "$DB" tui --repo DEMO
fi
