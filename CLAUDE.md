# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Required reading before planning or implementing

Two docs in `docs/` carry the non-obvious conventions this codebase relies on. Read whichever is relevant **before** you start planning a change — they'll shape the design, not just check it after the fact.

- **[`docs/agent-cli-principles.md`](docs/agent-cli-principles.md)** — the six rules every mutating CLI command honours (JSON in via `--json`, schema reachable via `bacio schema`, lean output by default, validation at the store boundary, `--dry-run` support, documented in `SKILL.md`) plus the explicit "deliberately don't do" list. Read before adding or changing any CLI command.
- **[`docs/tui-cookbook.md`](docs/tui-cookbook.md)** — bubbletea v1.3.10 + lipgloss v1.1.0 + bubbles patterns, pinned to v1. Upstream READMEs have moved to v2 and will mislead you. Read before any non-trivial work in `internal/tui/`.

The deeper context for both lives in the topic sections below (`## Agent-CLI principles` and `## TUI cookbook`).

## Quick commands

- **`./build.sh`** — **always run this before validating, testing, retesting, or smoke-testing anything.** Default is the full rebuild: the web bundle (embedded into the CLI binary at `/ui/`), the CLI/TUI binary (installs to `~/.local/bin/bacio`), the regenerated Wails bindings, the desktop frontend, and the desktop Go binary. The user's TUI + desktop + per-session `bacio channel` MCP processes all share the same SQLite store, so a schema change with a stale binary still running anywhere will surface as "no such column" errors. Opt-out flags for the inner loop: `--skip-web` (skip the Vite web bundle — saves the npm install + Vite step when nothing in `desktop/frontend/` changed) and `--skip-desktop` (skip the Wails desktop build). Combine them (`--skip-web --skip-desktop`) for CLI/TUI-only. Before asking the user to test, run this and tell them to restart any running TUI / desktop / agent sessions to pick up the new binary.
- `go build ./...` — build everything in the main module (does NOT cover `desktop/` — it's a separate nested module; use `./build.sh` instead).
- `go build -o ~/.local/bin/bacio ./cmd/bacio` — install just the CLI/TUI binary. Equivalent to the CLI step inside `./build.sh --skip-web --skip-desktop`.
- `go vet ./...` — vet the codebase.
- `go test ./...` — run the unit tests.
- `bacio <subcommand>` from inside any git working tree drives the CLI.
- The SQLite database lives at `~/.bacio/db.sqlite` by default. Override via `--db <path>` when testing or validating changes.
- **Smoke-testing a desktop / web change.** The same React tree drives both the Wails desktop binary and the web bundle, so the cheapest agent-driven validation is the web path: run `bacio api` (default `http://localhost:5320/ui/`) and drive the UI with the `playwright-cli` skill — no Wails native-window dance, and snapshots come back as readable accessibility trees. After a frontend change, `./build.sh --skip-desktop` is enough to refresh the embedded bundle; restart the running `bacio api` to pick up the new binary.

## Profiling

Three hidden persistent root flags capture profiles/traces — a dev/debug affordance, kept out of `--help`:

- `bacio --cpuprofile <path> tui` — writes a CPU profile covering the whole interactive session.
- `bacio --memprofile <path> tui` — writes a heap profile (`runtime.GC()` then `pprof.WriteHeapProfile`) of what survived the session.
- `bacio --trace <path> tui` — writes an execution trace covering the whole session. Unlike the CPU profile, the trace captures off-CPU events (goroutine scheduling, blocking on syscalls/channels/mutexes), so it's the tool for diagnosing UI freezes — a stall on a slow query or `git` shell-out is invisible to CPU profiling but shows up here.

They start in the root `PersistentPreRunE` and flush in `stopProfiling` (`internal/cli/profiling.go`). `NewRoot()` returns that cleanup func; `cmd/bacio/main.go` runs it after `Execute()` returns — on success and error alike — so profiles flush even when a command exits via an error path (cobra skips `PersistentPostRunE` on error). Open CPU/heap output with `go tool pprof <path>`, trace output with `go tool trace <path>`. Today the flags are wired for `bacio tui`; extending them to the short-lived CLI commands is a possible follow-up.

## Agent-CLI principles (read before planning a feature)

`docs/agent-cli-principles.md` is the durable reference for the conventions bacio adopted from Justin Poehnelt's "Rewrite Your CLI for AI Agents". Every mutating command accepts JSON via `--json`, publishes its schema via `bacio schema`, returns lean output by default, validates input at the store boundary, supports `--dry-run`, and is documented in `SKILL.md`. New CLI work should honour those six rules and the explicit "deliberately don't do" list (no NDJSON, no `--field` projection, no silent input normalisation, etc.).

The exception is **harness-integration shims** (see below): `bacio tui`, `bacio api`, `bacio hook`, `bacio channel`. These aren't agent-facing mutation commands — they're glue to a host (a terminal, an HTTP client, the Claude Code hook/channel runtime) — so they deliberately skip the six rules (no `--json`, no `bacio schema` entry, no `--dry-run`). They're still documented in `SKILL.md`. Agent-facing dispatch verbs (`bacio agent dispatch` / `ack`) DO follow the six rules.

## TUI cookbook

`docs/tui-cookbook.md` is a synthesised reference for bubbletea v1.3.10 + lipgloss v1.1.0 + bubbles. Read it before doing anything non-trivial in `internal/tui/`. The snippets are pinned to v1; upstream READMEs have already moved to v2 and will mislead you.

Note: when changing or testing the TUI make sure to read `docs/tui-cookbook.md` for essential knowledge.

## Dispatched jobs are delegated to a `general-purpose` subagent

A `bacio channel`-equipped session is a **thin scheduler** for dispatched work: when an issue-tied `<channel>` event arrives, the parent session immediately calls `Task(subagent_type="general-purpose", model="opus", prompt=<worker brief>)` and forwards the subagent's one-line summary. All file reads / edits / bash calls happen inside the subagent's context, which is discarded on return, so the parent's context budget stays at "dispatch arrived → Task call → summary → reply" size across dozens of jobs (BACI-52). Don't add code that grows the parent's context per dispatch (e.g. don't push dispatch metadata back into the parent's TodoWrite).

The contract that tells the parent to delegate is the **dispatch preamble** — a reserved row (`slug = _dispatch_preamble`) in the `prompt_templates` table that `ComposeDispatchPayload` prepends to every per-mode template body at dispatch time. Edit it the same way as any other template: `bacio settings template show _dispatch_preamble` / `bacio settings template set _dispatch_preamble --body "..."`, or via the Settings panel in the TUI / desktop app. The embedded default body lives in [`internal/model/prompttemplates/_dispatch_preamble.txt`](internal/model/prompttemplates/_dispatch_preamble.txt); the row is backfilled into existing DBs by the `backfillDispatchPreamble` migration step. Full design lives in [`docs/agent-dispatch.md`](docs/agent-dispatch.md) under "Subagent delegation".

## `agent_session_questions` — agent → user clarification (BACI-53)

The bacio channel exposes a third MCP tool, `ask_user_question`, that
parks an agent's clarification question in `agent_session_questions`
until the user submits an answer through the TUI, desktop, or web
modal. The channel keeps an in-memory `request_uuid → JSON-RPC id`
map so it can re-correlate the parked reply with the answered row on
the next poll tick (3s), then delivers `{questions, answers}` (or an
MCP tool error on cancel) back to the agent's tool call.

State lifecycle: `open → answered | cancelled | abandoned`. The
channel flips orphaned rows to `abandoned` at startup — the previous
channel process owned the parked reply, and the agent restarted with
the channel, so there's no path to recover.

The drain step is the fourth thing `internal/channel.tick` does each
poll, gated on `BACIO_AGENT_MODE` via the existing `Run` vs `ServeMCP`
split: an interactive session that didn't opt in to agent mode never
advertises the tool, and any call slips through with a clear "channel
poller is parked" error rather than parking forever.

Surfaces: TUI Agents tab shows a `?N` counter and lists the open rows
in the drill-down (full answer overlay is a follow-up); the desktop
+ web Agents view renders a `? N` badge that pops a modal with each
question rendered as radios/checkboxes plus an "Other" free-text per
question. Same modal is used in web mode against `bacio api`'s
`/agents/questions/{id}/answer` route.
