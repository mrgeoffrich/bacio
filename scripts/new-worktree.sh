#!/usr/bin/env bash
# scripts/new-worktree.sh — spin up a manual git worktree under
# .claude/worktrees/ for local Claude Code work. The manual equivalent
# of the worktree the dispatch harness creates via the Agent tool's
# `isolation: "worktree"` parameter.
#
# Mirrors the bits of prompts/agents/implement.md the harness can't do
# for you: branches off the right remote tip and runs `bacio worktree
# init` so the new worktree has a unique bacio API port (and optionally
# an isolated DB, seeded or empty).
#
# Usage:
#   scripts/new-worktree.sh [--shared-db | --empty-db] <slug|ISSUE-KEY>
#
# Isolation modes:
#   (default)       port + DB isolated; DB seeded from your shared DB at this moment
#   --empty-db      port + DB isolated; DB is empty (fresh bacio install)
#   --shared-db     port isolated only; DB shared with your real ~/.bacio/db.sqlite
#
# Positional arg:
#   <slug>          kebab-case; the new branch and the worktree's tail dir name
#   <ISSUE-KEY>     e.g. BACI-256; the script reads `base_branch` from the
#                   issue's brief and branches off origin/<base_branch>
#
# Output:
#   stdout: just the resolved worktree path, so you can pipe it:
#             cd "$(scripts/new-worktree.sh BACI-256)"; claude
#   stderr: progress + a paste-into-Claude block to brief the in-worktree
#           session on what's isolated and what isn't.

set -euo pipefail

err() { printf '%s\n' "$*" >&2; }
die() { err "error: $*"; exit 1; }

usage() {
  cat >&2 <<'EOF'
Usage:
  scripts/new-worktree.sh [--shared-db | --empty-db] <slug|ISSUE-KEY>

Isolation modes:
  (default)     port + DB isolated; DB seeded from your shared DB at this moment
  --empty-db    port + DB isolated; DB is empty (fresh bacio install)
  --shared-db   port isolated only; DB shared with your real ~/.bacio/db.sqlite

Positional arg:
  <slug>        kebab-case; the new branch and the worktree's tail dir name
  <ISSUE-KEY>   e.g. BACI-256; the script reads `base_branch` from the
                issue's brief and branches off origin/<base_branch>

Output:
  stdout: just the resolved worktree path (pipe to cd)
  stderr: progress + a paste-into-Claude block to brief the in-worktree session
EOF
}

shared_db=
empty_db=
positional=

while [[ $# -gt 0 ]]; do
  case $1 in
    --shared-db)  shared_db=1 ;;
    --empty-db)   empty_db=1 ;;
    -h|--help)    usage; exit 0 ;;
    --)           shift; break ;;
    -*)           die "unknown flag: $1 (try --help)" ;;
    *)            [[ -n $positional ]] && die "unexpected positional: $1"
                  positional=$1 ;;
  esac
  shift
done

[[ -n $shared_db && -n $empty_db ]] \
  && die "--shared-db and --empty-db are mutually exclusive"

# Derive the two booleans the rest of the script branches on. Default
# is isolated + seeded.
isolate_db=1
seed_db=1
[[ -n $shared_db ]] && { isolate_db= ; seed_db= ; }
[[ -n $empty_db  ]] && seed_db=

[[ -n $positional ]] || { usage; exit 1; }

repo_root=$(git rev-parse --show-toplevel 2>/dev/null) \
  || die "not inside a git repo"
cd "$repo_root"

# Distinguish an issue-key arg (BACI-256 / baci-256) from a free-form slug.
issue_key=
if [[ $positional =~ ^[A-Za-z]{2,}-[0-9]+$ ]]; then
  issue_key=$(printf '%s' "$positional" | tr '[:lower:]' '[:upper:]')
  slug=$(printf '%s' "$positional" | tr '[:upper:]' '[:lower:]')
  err "issue mode: $issue_key"
  base_branch=$(bacio issue brief "$issue_key" -o json 2>/dev/null \
    | jq -r '.base_branch // "main"') \
    || die "failed to read brief for $issue_key (does the ticket exist?)"
else
  slug=$(printf '%s' "$positional" | tr '[:upper:]' '[:lower:]')
  base_branch=main
fi

# Validate slug against bacio's kebab-case rule (store.ValidateWorktreeSlug).
[[ $slug =~ ^[a-z0-9][a-z0-9-]*$ ]] \
  || die "slug must be kebab-case (got: $slug)"

worktree_path=$repo_root/.claude/worktrees/manual-$slug
branch=$slug
bacio_slug=manual-$slug

[[ -e $worktree_path ]] && die "$worktree_path already exists"

# Resolve the source DB now, while cwd is still in the main checkout —
# this is the DB bacio commands run from here would resolve to.
source_db=
if [[ -n $seed_db ]]; then
  command -v sqlite3 >/dev/null \
    || die "--seed-db needs sqlite3 on PATH"
  source_db=$(bacio status -o json | jq -r '.db_path') \
    || die "could not resolve current bacio DB path"
  [[ -f $source_db ]] || die "source DB does not exist: $source_db"
fi

# Warn-loud if going DB-isolated with an issue arg but no seed — the
# brief lookup above worked against the shared DB, but inside the new
# worktree the issue won't exist.
if [[ -n $empty_db && -n $issue_key ]]; then
  err "warning: --empty-db with $issue_key means the issue won't exist in the new worktree's empty DB."
  err "         (drop --empty-db if you want the ticket reachable in-worktree.)"
fi

err "→ fetch origin/$base_branch"
git fetch --quiet origin "$base_branch"

err "→ git worktree add $worktree_path (branch $branch off origin/$base_branch)"
git worktree add --quiet "$worktree_path" -b "$branch" "origin/$base_branch"

# bacio worktree init writes environment-config.yaml and registers in
# ~/.bacio/worktrees.yaml. If it fails, roll back the git worktree so
# we don't leave half-set-up state.
err "→ bacio worktree init --slug $bacio_slug${isolate_db:+ --isolate-db}"
if ! ( cd "$worktree_path" && bacio worktree init --slug "$bacio_slug" ${isolate_db:+--isolate-db} >/dev/null ); then
  err "bacio worktree init failed; rolling back the git worktree"
  git worktree remove --force "$worktree_path"
  exit 1
fi

# Seed AFTER init: .backup atomically writes the destination, and
# migrations on first open are no-ops because the seed came from the
# same binary version.
if [[ -n $seed_db ]]; then
  dest_db=$worktree_path/.bacio/db.sqlite
  err "→ seed $dest_db from $source_db (sqlite3 .backup)"
  mkdir -p "$(dirname "$dest_db")"
  sqlite3 "$source_db" ".backup '$dest_db'" \
    || die "sqlite3 .backup failed"
fi

# Pull resolved env back out of the new manifest for the prompt block.
env_json=$(cd "$worktree_path" && bacio worktree show -o json)
api_addr=$(printf '%s' "$env_json" | jq -r '.api_addr')
db_path=$(printf '%s' "$env_json" | jq -r '.db_path')

# Build the paste-into-Claude prompt. Goal: brief the in-worktree
# Claude on what's isolated, what isn't, and what mutations affect.
if [[ -n $seed_db ]]; then
  isolation_line="port + DB isolated. DB was seeded from $source_db at script-run time; mutations stay confined to this worktree's DB."
  db_caveat="- The DB is a snapshot taken at worktree creation. It diverges from the user's real DB as they keep working — don't treat it as a live mirror."
elif [[ -n $isolate_db ]]; then
  isolation_line="port + DB isolated. DB is EMPTY (fresh bacio install). Run \`bacio init\` to bind a repo before adding issues."
  db_caveat=
else
  isolation_line="port isolated, DB SHARED with the user's real ~/.bacio/db.sqlite. bacio mutations from here affect their real tickets — treat with the same care as the main checkout."
  db_caveat=
fi

if [[ -n $issue_key ]]; then
  issue_line="- Working on $issue_key. Load the full ticket: \`bacio issue brief $issue_key\`."
else
  issue_line=
fi

purge_flag=
[[ -n $isolate_db ]] && purge_flag=" --purge-db"

prompt="I've started Claude in a manual worktree (not a dispatch). Stay isolated to this worktree.

Worktree: $worktree_path
Branch:   $branch  (PR target: origin/$base_branch)
bacio:    api $api_addr, db $db_path
          $isolation_line"
[[ -n $issue_line ]] && prompt+="

$issue_line"
prompt+="

Rules:
- Every Read / Edit / Write \`file_path\` must start with $worktree_path.
- bacio CLI auto-resolves to this worktree's env from cwd inside it. From any path outside, pass \`--env $worktree_path/environment-config.yaml\`.
- Don't call \`bacio agent claim\` / \`release\` / \`reply\` — those are for dispatched workers; there is no dispatch here."
[[ -n $db_caveat ]] && prompt+="
$db_caveat"

err ""
err "─── paste into Claude after starting it in the worktree ────────────────"
printf '%s\n' "$prompt" >&2
err "───────────────────────────────────────────────────────────────────────"
err ""
err "when you're done with the worktree:"
err "  bacio worktree rm $worktree_path --confirm $bacio_slug$purge_flag"
err "  git worktree remove $worktree_path"
err ""
err "ready. cd into:"
printf '%s\n' "$worktree_path"
