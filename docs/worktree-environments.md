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
  api_port: 5324                            # reserves the pair 5323 (proxy) + 5324 (API)
  db_path: /Users/geoff/.bacio/db.sqlite   # shared store — the default
  # db_path: .bacio/db.sqlite              # per-worktree DB (--isolate-db)
  # log_dir: logs                          # optional (BACI-73); see below

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
  collision walk against the global registry). Each worktree actually
  reserves an adjacent **pair** of ports: `api_port` and the derived
  reverse-proxy port one below it (`api_port − 1`, BACI-344 — see
  [`reverse-proxy.md`](reverse-proxy.md)). The allocator keeps the pairs
  disjoint, so no worktree's API port ever lands on another's proxy
  port. The default pair `5320` (API) + `5319` (proxy) is reserved for
  the legacy manifest-free default; the lowest auto-allocated API port
  is therefore `5322` (its proxy `5321`).
- `allocations.db_path` — the SQLite DB this worktree's bacio resolves
  to. `bacio worktree init` pins the **shared** `~/.bacio/db.sqlite` by
  absolute path here unless DB isolation was requested (see
  "Dispatched-work worktrees" below). With `--isolate-db` it writes the
  relative `.bacio/db.sqlite` (resolved against the worktree root, and
  covered by the existing `.bacio/` gitignore rule). Hand-edits may use
  a relative or absolute path; an empty value resolves to the
  per-worktree `.bacio/db.sqlite`.
- `allocations.log_dir` — optional, BACI-73. When set, the long-running
  bacio processes (`bacio api`, `bacio channel`, the desktop binary)
  write per-day log files under this directory. Relative paths resolve
  against the worktree root; leave the field blank and the logging
  resolver synthesises `<worktree-root>/.bacio/logs/` for you. Mirrors
  the BACI-63 resolver chain (flag → `$BACIO_LOG_DIR` → manifest →
  `~/.bacio/logs/`). `bacio worktree init` does NOT write this field —
  the synthesised default works for everyone; the field is an opt-in
  hand-edit for users who want to pin a path. Full spec:
  [`logging.md`](logging.md).
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
    api_port: 5322   # pair: 5321 (proxy) + 5322 (API)
    db_path: /Users/geoff/Repos/bacio/.bacio/db.sqlite
    created_at: 2026-05-18T17:00:00+10:00
  - slug: bacio-baci-63
    path: /Users/geoff/Repos/bacio-BACI-63
    api_port: 5324   # pair: 5323 (proxy) + 5324 (API) — note the 2-port step
    db_path: /Users/geoff/Repos/bacio-BACI-63/.bacio/db.sqlite
    created_at: 2026-05-18T18:14:00+10:00
```

Notes:

- `db_path` here is always absolute, even when the per-worktree YAML
  stores it relative. Lets `bacio worktree list` print stats without
  opening each manifest.
- The legacy default (`~/.bacio/db.sqlite` + API port `5320`, proxy
  port `5319`) is NOT registered; only worktrees that ran `init` appear.
  The allocator still reserves that pair so an `init` never lands on it.
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
`install-agent`, `sync` etc. share one repo identity across every
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

## Dispatched-work worktrees: port isolation without DB isolation (BACI-87)

A manifest bundles two isolations — an API port **and** a SQLite DB —
but their audiences differ. Port isolation is for everyone: a
worktree's `bacio web` smoke test shouldn't collide with the user's
running UI on `5320`. DB isolation is narrow: its only real use is
testing bacio's *own* schema migrations across sibling worktrees, so a
stale binary in one worktree doesn't hit a migrated schema in another.
A normal user — and every dispatched worker — wants the issue DB to
stay shared: a worker dispatched onto `BACI-12` needs every `bacio`
issue call (claim, brief, state, pr attach, release) to reach the
`~/.bacio/db.sqlite` where that ticket actually lives.

So `bacio worktree init` **defaults to port isolation only**. It
allocates a fresh port and pins the shared `~/.bacio/db.sqlite` into
`allocations.db_path` by absolute path. DB isolation is opt-in:

- `--isolate-db` — bind a per-worktree `.bacio/db.sqlite` instead.
- `BACIO_WORKTREE_ISOLATE_DB=1` — supplies the default for the
  `--isolate-db` flag, so `BACIO_WORKTREE_ISOLATE_DB=1 bacio worktree
  init` is equivalent. `--isolate-db=false` overrides the env var back
  off; `--db-path <path>` pins an explicit path and overrides both.

**Set `BACIO_WORKTREE_ISOLATE_DB` per-invocation only** — never in a
shell profile or a checked-in `.envrc`. A worker dispatched onto a
bacio ticket runs in a git worktree of the bacio repo and would
inherit an ambient setting, re-introducing exactly the bug BACI-87
fixed (its `in_review` / pr-attach written into a throwaway DB).
`BACIO_AGENT_MODE` can't be used to auto-detect and undo this: an
interactive bacio-dev session sets it too, so it doesn't distinguish a
dispatched worker from the developer. Typing `--isolate-db` on the one
`init` command per dev worktree is the reliable override.

`bacio worktree rm --purge-db` refuses to delete a shared-DB
`db_path` — purging the global store would wipe every project's
issues — and names the path in the error so the refusal is legible.

## Teardown process reaping (BACI-93)

`bacio worktree rm` removes the manifest and the registry row, but a
long-running bacio process (`bacio api` / `bacio web` /
`bacio-desktop`) that was bound to that worktree keeps running after
teardown. Such an orphan keeps heartbeating the shared `ui_leader`
lease, so a stale `bacio web --no-open` from a torn-down worktree can
hold the controller lease and starve the "Controlling" badge on the
bacio instance the user is actually looking at — killing the orphan
flips the lease over within one election tick.

So `bacio worktree rm` **reaps port-bound processes by default**.
Before any filesystem mutation it discovers every process holding a
`LISTEN` socket on the manifest's `allocations.api_port`
(`internal/procfind`, the only platform-specific surface — `lsof` on
unix, `netstat -ano` + `tasklist` on Windows), then for each one whose
command names a bacio binary (`bacio` / `bacio-desktop`) it sends
`SIGTERM`, waits a short grace (~3 s), and escalates to `SIGKILL` if
the process is still alive. Safety rails:

- **Non-bacio listeners are never signalled.** A stranger that merely
  grabbed the worktree's port is reported in the result with a "not a
  bacio process; left running" note.
- **Port 5320 is never touched.** A manifest somehow pointing at the
  legacy default port skips the reap entirely — `worktree init` won't
  allocate 5320, but a hand-edited manifest could.
- **Discovery failure is non-fatal.** A missing `lsof`/`netstat`, or
  any discovery error, is recorded as a `process_scan_note` and
  teardown still completes — the user is told to kill manually.
- **`--keep-processes`** (`keep_processes: true` in `--json`) opts out
  of the reap entirely.
- **`--dry-run`** lists the PIDs it *would* signal under "Would
  signal:" without touching anything.

Documented limitation: a `bacio channel` process has no HTTP listener,
so the port-listener match cannot find it — channels are not reaped.
Catching one would need a cwd-walk; that is deliberately out of scope.

## Surfaces touched

| Surface | How it picks the right env |
| --- | --- |
| CLI (`bacio issue/feature/doc/...`) | `internal/cli/context.go` → `resolveEnv()` → `wtenv.Resolve` |
| `bacio api` | `--addr` defaults to `Resolve().APIAddr` when the flag wasn't set |
| `bacio status` | Surfaces `db_path`, `api_addr`, `env_source`, `env_path` in JSON; one extra `Env:` row in text mode |
| `bacio channel` | Resolves at startup from cwd (Claude Code spawns the MCP subprocess with the project root as cwd) |
| `bacio hook *` | Resolves at every hook fire; logs the resolved DB when a manifest is in play, so misconfigured projects show up in the hook log. The `bacio hook pre-tool-use` confinement hook (BACI-116, BACI-129) no longer relies on `wtenv.Resolve`; it classifies cwd directly via `git rev-parse` (primary vs linked worktree) so confinement engages even when no manifest is present — e.g. a supervisor running in the main checkout with `BACIO_AGENT_MODE=1` set |
| Desktop binary | `desktop/main.go` captures cwd before any chdir, calls `wtenv.Resolve`, threads the result into `client.Open` and `LeaderService`. Adds the resolved slug to the window title |

The desktop binary takes two new flags — `--db <path>` (was missing
entirely before BACI-63) and `--env <path>` (BACIO_ENV equivalent for
desktop launches outside a shell that exports it).

## CLI surface

```
bacio worktree init [--slug <name>] [--port <n>] [--isolate-db] [--db-path <path>] [--force] [--json] [--dry-run]
bacio worktree show [path]
bacio worktree list
bacio worktree rm [path] --confirm <slug> [--purge-db] [--keep-processes] [--json] [--dry-run]
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

# Worktree B — manifest-driven: isolated port, shared DB (the default).
git worktree add ../bacio-spike -b spike
cd ../bacio-spike
bacio worktree init --slug spike        # add --isolate-db for a per-worktree DB
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
