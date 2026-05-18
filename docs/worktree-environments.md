# Worktree environments (BACI-63)

## Why this exists

Sibling git worktrees of the same bacio project used to clash whenever
each tried to run its own `bacio api` and/or desktop binary:

- `bacio api` in worktree B couldn't bind because worktree A held
  `127.0.0.1:5320`.
- Two desktop processes silently raced on the `ui_leader` lease in
  `~/.bacio/db.sqlite`. WAL kept the readers safe; the writers fought
  for the lease, leaving the dispatch matcher / idle pinger / prune
  loop running in two places at once.
- The desktop binary had no `--db` flag at all, so even with the CLI's
  `--db` escape hatch the GUI couldn't be steered onto an isolated DB.

The `--db` / `--addr` / `--port` flags technically let you work around
this if you threaded them through every shell invocation in every
worktree — but nobody does, so worktrees stomped on each other in
practice.

BACI-63 introduces a single opt-in file at `<worktree>/environment-config.yaml`
that captures the per-worktree allocations. Every bacio entry point —
CLI, `bacio api`, desktop binary, channel, hooks — resolves through
the same `internal/wtenv.Resolve` lookup, so two worktrees with their
own manifest never clash on the writer, the leader lease, or the bind
addr. Manifest-free behaviour is identical to bacio's pre-BACI-63
default (`~/.bacio/db.sqlite` + `127.0.0.1:5320`), so no existing user
is affected unless they explicitly run `bacio worktree init`.

## Files written

### `<worktree>/environment-config.yaml`

The source of truth for one worktree. Written by `bacio worktree init`;
gitignored at the same time so a clone doesn't accidentally carry one
user's allocations into another's working tree.

```yaml
# environment-config.yaml — bacio worktree environment manifest.
# Source of truth for the bacio instance running in this worktree.
# Do not commit; written by `bacio worktree init` and added to
# .gitignore at the same time.

identity:
  slug: bacio-baci-63
  worktree: /Users/geoff/Repos/bacio-BACI-63
  created_at: 2026-05-18T18:14:00+10:00

allocations:
  api_port: 5321
  db_path: .bacio/db.sqlite       # relative to the worktree root

extras:
  # Free-form. bacio doesn't read these; scripts can. Examples:
  # vite_port: 5174
  # wails_dev_url: http://127.0.0.1:34115
```

Fields:

- `identity.slug` — short stable label. Surfaces in `bacio worktree list`,
  the desktop window title, and the leader-election holder label.
- `identity.worktree` — absolute path at the time of `init`. Informational
  only; resolution does not depend on this matching the current path,
  so the worktree can be moved.
- `identity.created_at` — ISO-8601 timestamp written by `init`.
- `allocations.api_port` — the bind port `bacio api` uses by default.
  Auto-allocated at `init` time (deterministic hash of the slug plus a
  collision walk against the global registry); port `5320` is reserved
  for the legacy manifest-free default.
- `allocations.db_path` — relative to the worktree root; absolute paths
  also accepted for hand-edits. Defaults to `.bacio/db.sqlite`, which
  the existing `.bacio/` gitignore rule already covers.
- `extras` — opaque map of strings. bacio round-trips it untouched on
  rewrite so adjacent tooling (Vite, Wails dev URL, future MCP
  listeners) can add keys without schema churn. Prefix keys by tool
  name (`vite.port`, `wails.dev_url`) to avoid collisions between
  scripts.

### `~/.bacio/worktrees.yaml`

A per-user registry that tracks every manifest bacio has seen. It's
authoritative for port allocation (so `init` can pick a free port
without scanning every YAML) and it backs `bacio worktree list`
without a filesystem walk.

```yaml
worktrees:
  - slug: bacio
    path: /Users/geoff/Repos/bacio
    api_port: 5321
    db_path: /Users/geoff/Repos/bacio/.bacio/db.sqlite
    created_at: 2026-05-18T17:00:00+10:00
  - slug: bacio-baci-63
    path: /Users/geoff/Repos/bacio-BACI-63
    api_port: 5322
    db_path: /Users/geoff/Repos/bacio-BACI-63/.bacio/db.sqlite
    created_at: 2026-05-18T18:14:00+10:00
```

Notes:

- `db_path` here is always absolute, even when the per-worktree YAML
  stores it relative. Lets `bacio worktree list` print stats without
  opening each manifest.
- The legacy default (`~/.bacio/db.sqlite` + port `5320`) is NOT
  registered; only worktrees that ran `init` appear.
- The per-worktree YAML wins on disagreement. The registry is a
  cache; rebuilding it from the YAMLs is always safe.
- A `!` mark next to an entry in `bacio worktree list` means the
  per-worktree manifest is missing on disk (the path moved or was
  deleted). Drop the row with `bacio worktree rm <path> --confirm <slug>`.

### `<worktree>/.gitignore`

`bacio worktree init` appends `environment-config.yaml` to `.gitignore`
(idempotent — it checks for the existing line first). The same call
appends `.bacio/` if the existing `bacio init` flow hasn't already
done so. If `.gitignore` is itself untracked, `init` still writes the
line.

## Resolution chain

A new `internal/wtenv` package owns the lookup. Precedence, highest
first:

```
1. Explicit flags: --db, --addr / --port            (power-user override)
2. Env override:   BACIO_ENV=<absolute path to yaml>
3. Worktree YAML:  git rev-parse --show-toplevel → <root>/environment-config.yaml
4. Default:        ~/.bacio/db.sqlite + 127.0.0.1:5320
```

Step 3 returning "not present" is not an error — it falls through to
step 4, which is the legacy behaviour that manifest-free users keep.

Step 3 uses `git rev-parse --show-toplevel` (wrapped in
`internal/git.WorktreeRoot`), which returns the LINKED worktree's own
root. This is deliberately different from `internal/git.Detect`, which
walks back to the *main* worktree's root so `resolveRepo`,
`install-hooks`, `sync` etc. share one repo identity across every
linked worktree of a project. The manifest layer wants the opposite
contract — each linked worktree's bacio reads its own
`environment-config.yaml`, not its parent's — so the writer
(`bacio worktree init`) and the resolver both use `WorktreeRoot`.
(BACI-71: this gap previously had the writer using `Detect` and the
docs promising `--show-toplevel`, which meant `init` from a linked
worktree silently clobbered the main worktree's manifest.)

When both `--db` and `--addr` are set, the resolver short-circuits
the manifest read entirely: an explicit flag pair is strictly more
specific than any manifest, and a broken `BACIO_ENV` shouldn't be
able to take down a call where the user has named the DB directly.

The global `--env <path>` CLI flag is equivalent to setting `BACIO_ENV`
for one invocation; it wins over the env var when both are set.

## Surfaces touched

| Surface | How it picks the right env |
| --- | --- |
| CLI (`bacio issue/feature/doc/...`) | `internal/cli/context.go` → `resolveEnv()` → `wtenv.Resolve` |
| `bacio api` | `--addr` defaults to `Resolve().APIAddr` when the flag wasn't set |
| `bacio status` | Surfaces `db_path`, `api_addr`, `env_source`, `env_path` in JSON; one extra `Env:` row in text mode |
| `bacio channel` | Resolves at startup from cwd (Claude Code spawns the MCP subprocess with the project root as cwd) |
| `bacio hook *` | Resolves at every hook fire; logs the resolved DB when a manifest is in play, so misconfigured projects show up in the hook log |
| Desktop binary | `desktop/main.go` captures cwd before any chdir, calls `wtenv.Resolve`, threads the result into `client.Open` and `LeaderService`. Adds the resolved slug to the window title |

The desktop binary takes two new flags — `--db <path>` (was missing
entirely before BACI-63) and `--env <path>` (BACIO_ENV equivalent for
desktop launches outside a shell that exports it).

## CLI surface

```
bacio worktree init [--slug <name>] [--port <n>] [--db-path <rel>] [--force] [--json] [--dry-run]
bacio worktree show [path]
bacio worktree list
bacio worktree rm [path] --confirm <slug> [--purge-db] [--json] [--dry-run]
```

- The two mutating verbs (`init`, `rm`) follow the six agent-CLI
  principles: `--json`, `--dry-run`, schema entries
  `worktree.init` / `worktree.rm`, validation at the store boundary
  (`store.ValidateWorktreeSlug`).
- The two read-only verbs (`show`, `list`) follow the `bacio status`
  precedent — no `--json` mutation contract, no schema entry. They
  still respect `-o json` for the output format itself.
- `bacio init` is unchanged in scope: it still binds a repo prefix
  inside whichever DB is resolved. The usual flow becomes
  `bacio worktree init` → `bacio init` → start using `bacio issue add`.

## Why YAML, not TOML or a `.bacio/` location

- **YAML** matches the existing `.bacio/config.yaml` — same parser
  (`go.yaml.in/yaml/v4`), no new dependency.
- **Wide schema with `extras`** so adjacent scripts (Vite, Wails dev
  URL, future MCP listeners) don't need a schema bump to share the
  file.
- **Located at the worktree root**, not under `.bacio/`, so `ls`
  shows it. The init flow gitignores it explicitly so a checked-in
  repo doesn't accidentally carry one user's allocations.

## Rejected alternatives

- **A named `BACIO_PROFILE` indirection** with manifests under
  `~/.bacio/profiles/<name>/`. Worse because the source of truth
  drifts away from the working tree; the slug-to-allocations join
  has to live in another file; and "which profile am I on?" becomes
  a question the user has to answer instead of one bacio answers
  from cwd.
- **Auto-create a manifest on first mutation** when inside a git
  worktree. Convenient, but fragments state when the user just
  wanted a quick branch and doesn't realise a fresh DB came with
  it. Strict opt-in via `bacio worktree init` is the safer default.
- **Lock files / a mutex on `~/.bacio/db.sqlite`** so the second
  instance waits. Doesn't solve the use case — the user *wants* to
  run side by side, not serialised.

## Smoke test recipe

```bash
# Worktree A — manifest-free, picks up the legacy default.
cd ~/Repos/bacio
bacio status                            # Env: default

# Worktree B — manifest-driven, isolated DB + port.
git worktree add ../bacio-spike -b spike
cd ../bacio-spike
bacio worktree init --slug spike
bacio status                            # Env: worktree manifest (...)
bacio worktree show -o json | jq .api_addr

# Launch a `bacio api` in each. They co-exist.
( cd ~/Repos/bacio        && bacio api & )
( cd ~/Repos/bacio-spike  && bacio api & )

# Same for two desktops — each window title carries its slug.
~/.local/bin/bacio-desktop &
( cd ~/Repos/bacio-spike && ~/.local/bin/bacio-desktop & )
```

## Follow-ups (deliberately out of scope for BACI-63)

- `bacio worktree reconcile` — repair drift between the registry and
  on-disk manifests after a `rm -rf <worktree>` without `bacio worktree rm`.
- `bacio worktree use <slug>` — shell helper that prints
  `export BACIO_ENV=<resolved path>` for `direnv`/`.envrc`.
- `bacio worktree get <key>` — scripts that want `api_port` without
  pulling in `yq`.
- A standalone `manifest.schema.json` so editors lint the manifest.
- Auto-derived manifest on first mutation, revisited if users keep
  typing `bacio worktree init` by hand.
- Cross-worktree dispatch / agent visibility (`bacio worktree aggregate`).
