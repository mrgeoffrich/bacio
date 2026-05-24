# CLAUDE.md

Guidance to Claude Code (claude.ai/code) when working in this repository. **Read this first**, then the topic-specific doc for whatever you're about to touch.

## Mental model

[`ARCHITECTURE.md`](ARCHITECTURE.md) is the one-shot onboarding read — binaries, processes, the shared SQLite store, the leader-elected controller, how the React tree is shared between desktop and web, how Claude Code subagent dispatch flows end-to-end. Read it before any cross-subsystem change.

## Required reading by topic

Read the relevant doc **before** you start planning a change — these shape the design, not just check it after the fact.

| Doc | Read before |
|---|---|
| [`docs/agent-cli-principles.md`](docs/agent-cli-principles.md) | Adding or changing any mutating CLI command. Covers the six rules (JSON in via `--json`, schema reachable via `bacio schema`, lean output, store-boundary validation, `--dry-run`, documented in `SKILL.md`) plus the "deliberately don't do" list. |
| [`docs/agent-dispatch.md`](docs/agent-dispatch.md) | Touching `internal/dispatcher` / `internal/channel` / `prompts/agents/` or anything in the dispatch pipeline. |
| [`docs/tui-cookbook.md`](docs/tui-cookbook.md) | Non-trivial work in `internal/tui/`. **bubbletea v1.3.10**, not v2 — upstream READMEs have moved to v2 and will mislead you. |
| [`docs/markdown-rendering.md`](docs/markdown-rendering.md) | Touching markdown rendering on any surface. Rule: every React read surface goes through `<MarkdownView>` (never `react-markdown` directly); TUI goes through `renderMarkdown`. |
| [`docs/worktree-environments.md`](docs/worktree-environments.md) | Touching `internal/wtenv/` or anything that resolves a DB / port / log dir. |
| [`docs/web-app-mode.md`](docs/web-app-mode.md) | Changing the seam between Wails and HTTP transports (`desktop/frontend/src/api.ts` and friends). |
| [`docs/background-sync.md`](docs/background-sync.md) | Touching `internal/sync/` or the sync UI. |
| [`docs/logging.md`](docs/logging.md) | Debugging a long-running process or adding a new structured log emitter. |
| [`docs/profiling.md`](docs/profiling.md) | Diagnosing a TUI freeze or memory issue. |
| [`docs/getting-started.md`](docs/getting-started.md) | Orienting a new bacio user (not for codebase work — but useful context for UX changes). |

## Quick commands

- **`./build.sh`** — full rebuild (web bundle → CLI binary embeds it → Wails bindings regen → desktop frontend → desktop Go binary). **Always run this before validating, testing, retesting, or smoke-testing anything.** Opt-out flags `--skip-web` and `--skip-desktop` for the inner loop. **Does NOT install to `~/.local/bin/bacio`** — install explicitly with `go build -o ~/.local/bin/bacio ./cmd/bacio` from the worktree you want on PATH. After install, **restart any running TUI / desktop / agent sessions** so they pick up the new binary against the (often-migrated) shared DB.
- `go build ./...` — main module only (does NOT cover `desktop/` — separate nested module; use `./build.sh` for that).
- `go vet ./...` / `go test ./...` — vet and unit tests.
- `bacio <subcommand>` — from inside any git working tree drives the CLI.
- **Smoke-testing UI changes.** The same React tree drives desktop and web. The cheapest agent-driven path is `bacio web --no-open` + the `playwright-cli` skill. `--no-open` is the right flag when an agent is driving — pop the browser only for human iteration. `bacio api` is API-only after BACI-72 (404 on `/ui/`) — reach for `bacio web` whenever the UI is in the loop.

## Tripwires

One-liners whose absence would cause repeated mistakes. The detail lives in the linked doc; these are the rules to keep in your head.

- **Stale binary, shared store.** `bacio tui` / desktop / `bacio channel` MCP / `bacio web` all share `~/.bacio/db.sqlite`. A schema change with a stale binary still running anywhere surfaces as `no such column` errors. After every rebuild + install, restart every long-running bacio.
- **Never `pkill -f "bacio web"` or `pkill -f bacio`.** They match every bacio process on the machine and will kill the user's own running bacio UI. When you start `bacio web` in the background for a smoke test, capture its PID (`web_pid=$!`) and stop only that process (`kill "$web_pid"`).
- **Port-in-use is not yours to resolve.** If `bacio web` / `bacio api` reports a port already in use, do NOT kill whatever holds it — it's the user's bacio. Re-check you're inside your worktree, or pass `--port`.
- **The dispatch supervisor is a thin scheduler.** When a `<channel>` dispatch arrives, the parent session immediately calls `Task(subagent_type="bacio-<mode>-worker", prompt=stub)` and forwards the subagent's one-line summary. All real work happens in the subagent. Don't add code that grows the parent's per-dispatch context (e.g. no pushing dispatch metadata into the parent's TodoWrite). See [`docs/agent-dispatch.md`](docs/agent-dispatch.md).
- **Worktree env is opt-in.** `bacio worktree init` is opt-in; manifest-free worktrees keep the legacy default (`~/.bacio/db.sqlite` + `127.0.0.1:5320`) exactly. `init` defaults to **port isolation only** — DB stays shared so dispatched workers reach their ticket. DB isolation is opt-in via `--isolate-db`; never set `BACIO_WORKTREE_ISOLATE_DB` ambiently.
- **`bacio agent dispatch` / `ack` follow the six agent-CLI rules; `bacio tui` / `api` / `web` / `channel` / `hook` / `install-*` are harness-integration shims and don't.** When adding a CLI verb, ask "who types this command — a person/agent, or a runtime?".
- **Edited a prompt template body? Run `bacio install-agent`.** Template bodies are the source of generated `.claude/agents/<name>.md` files; without re-install the dispatched worker still uses the old body. `bacio status` reports per-template agent-file freshness.
- **Raw `sqlite3 ~/.bacio/db.sqlite ...` is hook-denied in agent mode (BACI-134).** The PreToolUse hook denies every `sqlite3` invocation whose path argument resolves to the shared store, including read-only ones — every mutation must go through a `bacio` CLI verb so the audit log records it, and read diagnostics belong in `bacio` read verbs / `bacio history`. A worktree-isolated DB (`bacio worktree init --isolate-db`) is at a different absolute path and is allowed.
- **Workspace vs system bacio (BACI-139).** Inside a linked worktree, `./build.sh` writes `.bin/bacio-agent-<slug>` — a uniquely-named workspace binary that embeds your in-progress source. Use it ONLY for smoke-testing the change you are implementing. Every close-out bookkeeping call (`pr attach`, `agent release`, `tag add`, `worktree rm`, `comment add`, `install-agent`, `install-skill`) must use the bare `bacio` on PATH, which is the known-good system binary that the dispatch pipeline expects.

If you find a footgun that fits this list, add it here — the bar is "would a model that hasn't read the linked doc do the wrong thing?".
