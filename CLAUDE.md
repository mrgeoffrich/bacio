# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Required reading before planning or implementing

Two docs in `docs/` carry the non-obvious conventions this codebase relies on. Read whichever is relevant **before** you start planning a change — they'll shape the design, not just check it after the fact.

- **[`docs/agent-cli-principles.md`](docs/agent-cli-principles.md)** — the six rules every mutating CLI command honours (JSON in via `--json`, schema reachable via `bacio schema`, lean output by default, validation at the store boundary, `--dry-run` support, documented in `SKILL.md`) plus the explicit "deliberately don't do" list. Read before adding or changing any CLI command.
- **[`docs/tui-cookbook.md`](docs/tui-cookbook.md)** — bubbletea v1.3.10 + lipgloss v1.1.0 + bubbles patterns, pinned to v1. Upstream READMEs have moved to v2 and will mislead you. Read before any non-trivial work in `internal/tui/`.

The deeper context for both lives in the topic sections below (`## Agent-CLI principles` and `## TUI cookbook`).

## Quick commands

- `go build ./...` — build everything.
- `go build -o ~/.local/bin/bacio ./cmd/bacio` — install the CLI/TUI binary on the developer machine (this is how the user runs `bacio` and `bacio tui`).
- `go vet ./...` — vet the codebase.
- `go test ./...` — currently a no-op; there are no `*_test.go` files yet.
- `bacio <subcommand>` from inside any git working tree drives the CLI.
- The SQLite database lives at `~/.bacio/db.sqlite` by default. Override via `--db <path>` when testing or validating changes.

## Profiling

Three hidden persistent root flags capture profiles/traces — a dev/debug affordance, kept out of `--help`:

- `bacio --cpuprofile <path> tui` — writes a CPU profile covering the whole interactive session.
- `bacio --memprofile <path> tui` — writes a heap profile (`runtime.GC()` then `pprof.WriteHeapProfile`) of what survived the session.
- `bacio --trace <path> tui` — writes an execution trace covering the whole session. Unlike the CPU profile, the trace captures off-CPU events (goroutine scheduling, blocking on syscalls/channels/mutexes), so it's the tool for diagnosing UI freezes — a stall on a slow query or `git` shell-out is invisible to CPU profiling but shows up here.

They start in the root `PersistentPreRunE` and flush in `stopProfiling` (`internal/cli/profiling.go`). `NewRoot()` returns that cleanup func; `cmd/bacio/main.go` runs it after `Execute()` returns — on success and error alike — so profiles flush even when a command exits via an error path (cobra skips `PersistentPostRunE` on error). Open CPU/heap output with `go tool pprof <path>`, trace output with `go tool trace <path>`. Today the flags are wired for `bacio tui`; extending them to the short-lived CLI commands is a possible follow-up.

## Architecture in one screen

- Entry point: `cmd/bacio/main.go` → `internal/cli.NewRoot()` (cobra). `NewRoot()` returns `(*cobra.Command, func())` — the func is a cleanup closure `main.go` runs after `Execute()` to flush pprof profiles (see ## Profiling).
- CLI commands: `internal/cli/*.go`, one file per command group (`issue`, `feature`, `repo`, `doc`, `link`, `pr`, `tag`, `comment`, `history`, `status`, `init`, `install_skill`, `install_hooks`, `tui`, `agent`, `hook`, `channel`). Cross-cutting helpers live in `audit.go`, `context.go`, `output.go`, `output_flag.go`, `input.go`, `doc.go`.
- Agent supervision/dispatch: `internal/cli/hook.go` (the `bacio hook` Claude Code hook shim) and `internal/cli/channel.go` + `internal/channel/` (the `bacio channel` MCP-over-stdio server) keep the local agent registry in sync and deliver `agent_dispatches` work items to running sessions. Both are harness-integration shims — see ## Harness-integration shims.
- Persistence: `internal/store/` over SQLite (`modernc.org/sqlite`, pure-Go, no CGO). Schema is in `internal/store/schema.sql` and re-applied on every `Open` — adding a new table is a matter of appending another `CREATE TABLE IF NOT EXISTS …`. Schema changes that need real ALTERs go through `migrate()` in `internal/store/store.go`.
- Domain types: `internal/model/` — pure structs/enums, no DB.
- Git detection: `internal/git/detect.go` shells out to `git` for repo root + remote URL.
- TUI: `internal/tui/` — bubbletea v1.3.10 + lipgloss v1.1.0. Shell in `tui.go` owns the tab strip and routes keys; each tab implements the local `view` interface.
- Desktop app: `desktop/` is a **separate nested Go module** (`github.com/mrgeoffrich/bacio/desktop`) driven by Wails v3 + React + Vite (TypeScript). It is invisible to `go build ./...` from the repo root — build it via `wails3 build` from inside `desktop/`. The durable reference is the `docs-wails-v3-react-research.md` bacio doc; v3 is alpha (pinned to `v3.0.0-alpha.90`) so don't trust hosted docs at `v3.wails.io` blindly.

## Agent-CLI principles (read before planning a feature)

`docs/agent-cli-principles.md` is the durable reference for the conventions bacio adopted from Justin Poehnelt's "Rewrite Your CLI for AI Agents". Every mutating command accepts JSON via `--json`, publishes its schema via `bacio schema`, returns lean output by default, validates input at the store boundary, supports `--dry-run`, and is documented in `SKILL.md`. New CLI work should honour those six rules and the explicit "deliberately don't do" list (no NDJSON, no `--field` projection, no silent input normalisation, etc.).

The exception is **harness-integration shims** (see below): `bacio tui`, `bacio api`, `bacio hook`, `bacio channel`. These aren't agent-facing mutation commands — they're glue to a host (a terminal, an HTTP client, the Claude Code hook/channel runtime) — so they deliberately skip the six rules (no `--json`, no `bacio schema` entry, no `--dry-run`). They're still documented in `SKILL.md`. Agent-facing dispatch verbs (`bacio agent dispatch` / `ack`) DO follow the six rules.

## Harness-integration shims

`bacio hook` and `bacio channel` integrate bacio with the Claude Code agent runtime; they keep the local agent registry (`agents` / `agent_sessions` / `agent_claims` / `agent_dispatches`) in sync without the agent calling `bacio agent …` by hand.

- **`bacio hook <event>`** (`internal/cli/hook.go`) — hidden command group. Each subcommand (`session-start`, `user-prompt-submit`, `stop`, `session-end`) reads a Claude Code hook-event JSON payload on stdin, correlates by `session_id`, and registers / heartbeats / ends the session or drains pending dispatches. Hard rule: a hook handler must NEVER fail the agent's session — every path returns nil (exit 0); problems go to stderr. `bacio install-hooks` merges these into a repo's `.claude/settings.json` (non-destructive merge — see ## `bacio install-skill`).
- **`bacio channel`** (`internal/cli/channel.go` + `internal/channel/`) — hidden command. A pure-Go MCP server over stdio implementing Claude Code's "channel" contract: it pushes `agent_dispatches` into the running session live as `notifications/claude/channel` events and exposes a `reply` tool that acks dispatches. `internal/channel/` is the wire protocol (newline-delimited JSON-RPC 2.0), decoupled from the store via the `channel.Source` interface. Channels are a Claude Code research preview — a custom channel needs `--dangerously-load-development-channels`; caveats are in the command's `Long` help. `bacio install-channel` (`internal/cli/install_channel.go`) registers the `bacio` server in `<git-root>/.mcp.json` (non-destructive merge, same `--yes`/confirm-prompt behaviour as `install-hooks`) and prints the launch command.
- The **pull** path (hooks draining dispatches on prompt) and the **push** path (the channel) are independent — an agent can use either or both. `DrainDispatches` on the `client.Client` interface is the shared primitive.

## Conventions that aren't obvious

- **`recordOp` after every mutation.** Every CLI command that writes to the database calls `recordOp(s, model.HistoryEntry{...})` from `internal/cli/audit.go`. History prune failures and audit-write failures log to stderr but never fail the user-visible command. New mutating commands MUST follow this pattern.
- **Repos auto-register.** `resolveRepo()` in `internal/cli/context.go` creates a `repos` row on first use from inside a git working tree. There's intentionally no separate `bacio repo init`.
- **Actor (`--user`).** Every history row is stamped with an actor name. The default is the OS user; AI agents are expected to pass `--user <name>` explicitly so audits attribute work correctly. Plumbing lives in `audit.go`.
- **TUI view contract.** Each tab in `internal/tui/` is a struct implementing pointer-receiver `Update(msg tea.Msg) tea.Cmd`, `View(width, height int) string`, `Help() string`, and `HasOverlay() bool`. When `HasOverlay()` returns true the shell stops intercepting `q` / `esc` / digit / `tab`, so the overlay's own `esc` closes it instead of quitting the program.
- **Per-repo TUI state.** Generic key-value table `tui_settings(repo_id, key, value)` lives in `schema.sql`; helpers in `internal/store/tui_settings.go`. Used today for hidden columns; reuse for any future TUI preference instead of adding a typed table.
- **History retention.** `HistoryRetention` in `internal/store/store.go` is 60 days. `pruneHistory` runs on every `Open`. Adjust there if needed. `AgentSessionRetention` and `AgentDispatchRetention` mirror it (60 days, ended/settled rows only) — `pruneAgentSessions` / `pruneDispatches` also run on every `Open`.

## `bacio install-skill` and `embed.go`

- `embed.go` lives at the module root because `//go:embed` cannot traverse upward; it embeds `.claude/skills/bacio/SKILL.md` into `embed.SkillMarkdown`.
- `bacio install-skill` writes that markdown to `<git-root>/.claude/skills/bacio/SKILL.md`, overwriting on every run so doc updates land in any repo that re-runs the command.
- `bacio install-hooks` (`internal/cli/install_hooks.go`) is the sibling for hooks: it *merges* bacio's four command hooks into `<git-root>/.claude/settings.json` rather than overwriting — existing hooks for other events, and any non-bacio hooks on bacio's events, are preserved. Re-running replaces bacio's own hook groups in place (matched by the `bacio hook ` command marker), so it's idempotent. The hook block is defined inline in Go (`bacioHookEvents`), not embedded — it's config, not a doc. It prints a plan and prompts for confirmation before writing; `--yes`/`-y` skips the prompt (and `confirmPrompt` there is shared with `install-channel`).
- `bacio install-channel` (`internal/cli/install_channel.go`) is the same pattern for the channel MCP server: a non-destructive merge of a `bacio` entry into `<git-root>/.mcp.json` (other `mcpServers` and top-level keys preserved), confirm-prompt + `--yes`, then it prints the `claude --dangerously-load-development-channels server:bacio` launch command. The entry's `command` is the absolute path of the running binary (`os.Executable()`), so it resolves regardless of the MCP subsystem's PATH.
- `SKILL.md` is the canonical CLI reference for AI agents — keep it in sync when adding or changing commands.

## TUI cookbook

`docs/tui-cookbook.md` is a synthesised reference for bubbletea v1.3.10 + lipgloss v1.1.0 + bubbles. Read it before doing anything non-trivial in `internal/tui/`. The snippets are pinned to v1; upstream READMEs have already moved to v2 and will mislead you.

Note: when changing or testing the TUI make sure to read `docs/tui-cookbook.md` for essential knowledge.
