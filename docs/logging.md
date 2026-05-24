# File logging for long-running bacio processes (BACI-73)

The long-running bacio processes — `bacio api`, `bacio web`, `bacio tui`,
`bacio channel`, and the desktop binary — write a per-process log file
alongside their stderr output. The feature is
opt-out free at the file-format level (every long-running process
writes a log file when the resolver picks a directory), but the
directory itself is opt-in by way of the same precedence chain
`bacio` already uses for the per-worktree SQLite DB + API port
(BACI-63 — see [`worktree-environments.md`](worktree-environments.md)).

Short-lived CLI verbs (`bacio issue list`, `bacio comment add`, …)
deliberately do not open a log file. Their existing stderr-only
behaviour is unchanged.

## TL;DR

```bash
# Manifest-aware worktree — log lands inside the worktree.
cd ~/work/feature-baci-73
bacio worktree init
bacio api                          # writes <worktree>/.bacio/logs/bacio-api-YYYY-MM-DD.log
bacio status -o json | jq .log_dir

# Pin a path on the command line.
bacio --log-dir /tmp/bacio-logs api

# Crank the level up.
bacio --log-level debug channel
```

## Resolution chain for the log directory

Mirrors the BACI-63 wtenv chain, highest precedence first:

1. Explicit `--log-dir <path>` flag on the long-running command
   (`bacio api`, `bacio channel`) or the desktop binary
   (`bacio-desktop --log-dir`). Absolute or relative-to-cwd.
2. `$BACIO_LOG_DIR` env var.
3. Worktree manifest at `<worktree-root>/environment-config.yaml`,
   reading the optional `allocations.log_dir` field. When the field
   is absent, the resolver synthesises `<worktree-root>/.bacio/logs/`
   — siblings the existing `.bacio/db.sqlite` location, so the same
   `.bacio/` gitignore covers it.
4. Default fallback when no manifest is in play:
   `~/.bacio/logs/`.

The directory is created on demand (`mkdir -p`). If creation fails,
the process emits **one** warning to stderr (`bacio api: file
logging disabled: <err>`) and continues without a file sink. The
"never block the process from starting" rule is firm — a log dir
problem must not take a server down.

`bacio status` surfaces the resolved values:

```bash
bacio status -o json | jq '{log_dir, log_source, log_level}'
# {
#   "log_dir": "/Users/geoff/work/.bacio/logs",
#   "log_source": "worktree",
#   "log_level": "info"
# }
```

Same three values render in the human-readable view alongside the
existing `DB:` / `API:` / `Env:` lines:

```
DB:      /Users/geoff/work/.bacio/db.sqlite
API:     127.0.0.1:5330
Env:     worktree manifest (/Users/geoff/work/environment-config.yaml)
Log:     /Users/geoff/work/.bacio/logs (level=info source=worktree)
```

## Log levels

`debug | info | warn | error`. Default `info`. Override with
`--log-level <level>` or `$BACIO_LOG_LEVEL=<level>`. Case-insensitive:
`DEBUG`, `Debug`, `debug` all parse to the same value. Anything else —
including the legacy `warning` alias — fails loud at startup so a
typo doesn't silently degrade to info.

## Filename layout

One file per process per calendar day, in the process's local
timezone. Daily rotation is triggered by the first write of each new
day; older files are left for `find -mtime` or a future `bacio logs
prune` subcommand.

| Process         | Filename                                |
| --------------- | --------------------------------------- |
| `bacio api`     | `bacio-api-YYYY-MM-DD.log`              |
| `bacio web`     | `bacio-web-YYYY-MM-DD.log`              |
| `bacio tui`     | `bacio-tui-YYYY-MM-DD.log`              |
| `bacio channel` | `bacio-channel-pid<N>-YYYY-MM-DD.log`   |
| `bacio-desktop` | `bacio-desktop-YYYY-MM-DD.log`          |

The channel filename carries the channel process's PID stamp so two
concurrent channels for the same project (rare — a transient race
during a `/clear`) don't stomp on each other.

`bacio web` keeps its own per-day filename (rather than reusing
`bacio-api-*`) so a user running both `bacio web` and `bacio api`
side-by-side doesn't have the two processes fighting over one log
handle. `bacio tui` writes the BACI-89 background-sync runner and the
other leader-gated tickers (idle-pinger, archive sweep, queue matcher,
prune) into its own file, so an operator running a long-lived TUI
session can grep for sync push failures or leader-lease flaps after
the fact.

## Log format

Plain text, one event per line, slog's default `TextHandler`
encoding. Every line carries a `time=` (RFC 3339 with milliseconds),
a `level=` (DEBUG/INFO/WARN/ERROR), a `component=` (`api`, `web`,
`tui`, `channel`, `desktop`), a `msg=`, and any structured fields the
emitter passed.

```
time=2026-05-19T19:44:51.819+10:00 level=INFO msg="api starting" component=api addr=127.0.0.1:5330 db=/wt/.bacio/db.sqlite env_source=worktree env_path=/wt/environment-config.yaml version=dev
time=2026-05-19T19:44:51.820+10:00 level=INFO msg="api listening" component=api addr=127.0.0.1:5330
time=2026-05-19T19:44:52.808+10:00 level=INFO msg=request component=api method=GET path=/healthz status=200 duration_ms=0 actor=api remote_addr=127.0.0.1:55270
time=2026-05-19T19:44:53.352+10:00 level=INFO msg="api shutdown" component=api reason=signal
```

Greppable by design — structured JSON output is a possible v2.

## What gets logged

- **Server lifecycle.** `api starting` / `web starting` (with addr,
  db, env source, env path, version), `api listening`, `api shutdown`
  / `web shutdown`. Same shape on the channel: handshake
  notification, register, MCP tool calls, drain ticks,
  abandoned-question startup sweep. The desktop emits a single
  `bacio-desktop starting` line at boot, and `bacio tui` emits a
  `tui starting` line.
- **HTTP request/response** for `bacio api` / `bacio web` — method,
  path, status, latency, actor (`X-Actor`), remote addr.
- **MCP tool calls** for `bacio channel` — every `initialize`,
  `tools/list`, `tools/call`, and the reply/register/ask_user_question
  outcomes.
- **Leader-gated background work** (BACI-121) — sync push failures
  and completions, dispatch matcher bind/ack/cancel, idle-pinger
  reaper actions, archive-sweep results, prune outcomes. The
  long-running surfaces (`bacio api`, `bacio web`, the desktop
  binary, `bacio tui`) all route these through the file sink so a
  grep on the log dir surfaces them after the fact.
- **SQLite errors and unexpected panics** with stack — the existing
  `recoverPanic` middleware already routes these through slog.

Reads from the audit log are **not** mirrored here. `bacio history`
covers mutations; the log file is for operational telemetry.

## What's out of scope (v1)

- Log rotation by file size. Daily rotation only; a chatty debug
  channel for a week typically lands under 100 MiB, which is fine.
- Structured JSON output. v2 candidate.
- Remote log shipping.
- A `bacio logs tail` subcommand. Use `tail -f` on the resolved
  path until then.
- Short-lived CLI commands. They keep stderr-only behaviour; their
  failure modes are already covered by `--cpuprofile` / `--trace`.

## Compatibility note

The `allocations.log_dir` field on `environment-config.yaml` is
opt-in: `bacio worktree init` deliberately leaves it blank, so the
manifest stays byte-stable for existing users. A worktree that
hand-edits the field to pin a path is implicitly declaring "I'm on
the BACI-73 binary"; an older bacio reading that manifest will
fail-loud with a strict-decode error (the resolver rejects unknown
typed fields). That's an acceptable trade — the field is opt-in and
the failure is loud, not silent corruption.
