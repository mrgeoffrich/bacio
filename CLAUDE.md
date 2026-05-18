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
