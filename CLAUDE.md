# CLAUDE.md

Guidance to Claude Code (claude.ai/code) when working in this repository. **Read this first**, then the topic-specific doc for whatever you're about to touch.

## Paths & your working tree

Throughout this file, **`<worktree_root>`** means the output of `git rev-parse --show-toplevel` **in your own session** — the root of the working tree you are actually operating in. Substitute it into every `<worktree_root>/…` path below before you read, edit, or `cd` to it.

- **If you are a dispatched worker**, `<worktree_root>` is your linked worktree (`…/.claude/worktrees/agent-<slug>`), **not** the primary checkout. A harness header may show this file as `Contents of /Users/.../Repos/bacio/CLAUDE.md` (the primary checkout) — that copy may be stale or the very file your dispatch is editing, so **do not** adopt its directory as your prefix. The Write/Edit guard blocks writes to the primary checkout, but reads can silently leak to it; anchor every path on `<worktree_root>` so you read the same tree you edit.
- Paths written with a leading `~/` (e.g. `~/.bacio/db.sqlite`, `~/.local/bin/bacio`) or a host:port (e.g. `127.0.0.1:5320`) are **deliberately absolute** — they live in your home dir or on the network, are identical across worktrees, and are *not* `<worktree_root>`-relative.
- `./...` in a `go` command (`go build ./...`) is Go's cwd-relative package syntax — leave it as-is; it already resolves against your cwd, not the header.

## Mental model

`<worktree_root>/ARCHITECTURE.md` is the one-shot onboarding read — binaries, processes, the shared SQLite store, the leader-elected controller, how the React tree is shared between desktop and web, how Claude Code subagent dispatch flows end-to-end. Read it before any cross-subsystem change.

## Required reading by topic

Read the relevant doc **before** you start planning a change — these shape the design, not just check it after the fact.

| Doc | Read before |
|---|---|
| `<worktree_root>/docs/agent-cli-principles.md` | Adding or changing any mutating CLI command. Covers the six rules (JSON in via `--json`, schema reachable via `bacio schema`, lean output, store-boundary validation, `--dry-run`, documented in `<worktree_root>/prompts/SKILL.md`) plus the "deliberately don't do" list. |
| `<worktree_root>/docs/agent-dispatch.md` | Touching `<worktree_root>/internal/dispatcher` / `<worktree_root>/internal/channel` / `<worktree_root>/prompts/agents/` or anything in the dispatch pipeline. |
| `<worktree_root>/docs/tui-cookbook.md` | Non-trivial work in `<worktree_root>/internal/tui/`. **bubbletea v1.3.10**, not v2 — upstream READMEs have moved to v2 and will mislead you. |
| `<worktree_root>/docs/markdown-rendering.md` | Touching markdown rendering on any surface. Rule: every React read surface goes through `<MarkdownView>` (never `react-markdown` directly); TUI goes through `renderMarkdown`. |
| `<worktree_root>/docs/motion-layout-animations.md` | Touching card-movement animations on the React surfaces — the Pipeline cards or the ship flourish (Motion, pinned at v11.18.2). |
| `<worktree_root>/docs/worktree-environments.md` | Touching `<worktree_root>/internal/wtenv/` or anything that resolves a DB / port / log dir. |
| `<worktree_root>/docs/frontend-architecture.md` | Any non-trivial change to `<worktree_root>/desktop/frontend/src` — the `lib/hooks/` data primitives, the `api/contract.ts` DTO seam, the `state/` Context providers, and the decomposed `components/<domain>/` views. |
| `<worktree_root>/docs/web-app-mode.md` | Changing the seam between Wails and HTTP transports (`<worktree_root>/desktop/frontend/src/api.ts` and friends). |
| `<worktree_root>/.claude/rules/frontend-typescript.md` (path-scoped — also auto-loads when editing `desktop/frontend/**`) | Writing or changing any React frontend TS/TSX — strict-typing rules, the `api` seam, the cross-transport enum footgun. |
| `<worktree_root>/docs/reverse-proxy.md` | Touching `<worktree_root>/internal/proxy/` or the `/anthropic/*` reverse-proxy route, its auth-exemption, or the `agentmode.LaunchCommand` launch-env injection. |
| `<worktree_root>/docs/background-sync.md` | Touching `<worktree_root>/internal/sync/` or the sync UI. |
| `<worktree_root>/docs/logging.md` | Debugging a long-running process or adding a new structured log emitter. |
| `<worktree_root>/docs/profiling.md` | Diagnosing a TUI freeze or memory issue. |
| `<worktree_root>/docs/getting-started.md` | Orienting a new bacio user (not for codebase work — but useful context for UX changes). |

## Quick commands

- **`<worktree_root>/build.sh`** — full rebuild (web bundle → CLI binary embeds it → Wails bindings regen → desktop frontend → desktop Go binary). **Always run this before validating, testing, retesting, or smoke-testing anything.** Opt-out flags `--skip-web` and `--skip-desktop` for the inner loop. **Does NOT install to `~/.local/bin/bacio`** — install explicitly with `go build -o ~/.local/bin/bacio <worktree_root>/cmd/bacio` from the worktree you want on PATH. After install, **restart any running TUI / desktop / agent sessions** so they pick up the new binary against the (often-migrated) shared DB.
- `go build ./...` — main module only (does NOT cover `<worktree_root>/desktop/` — separate nested module; use `<worktree_root>/build.sh` for that).
- `go vet ./...` / `go test ./...` — vet and unit tests.
- `bacio <subcommand>` — from inside any git working tree drives the CLI.
- **Smoke-testing UI changes.** The same React tree drives desktop and web. The cheapest agent-driven path is `bacio web --no-open` + the `playwright-cli` skill. `--no-open` is the right flag when an agent is driving — pop the browser only for human iteration. `bacio api` is API-only after BACI-72 (404 on `/ui/`) — reach for `bacio web` whenever the UI is in the loop. For **human iteration on React/TS code** the fast inner loop is the Vite dev server with HMR: `bacio api --cors-origin http://localhost:5174` in one terminal, `cd <worktree_root>/desktop/frontend && VITE_BACIO_API=http://127.0.0.1:5320 npm run dev:web` in another, browse `http://localhost:5174` — edits hot-reload without a `<worktree_root>/build.sh` round-trip. See `<worktree_root>/docs/web-app-mode.md` §5.1(a).

## Tripwires

One-liners whose absence would cause repeated mistakes. The detail lives in the linked doc; these are the rules to keep in your head.

- **Stale binary, shared store.** `bacio tui` / desktop / `bacio channel` MCP / `bacio web` all share `~/.bacio/db.sqlite`. A schema change with a stale binary still running anywhere surfaces as `no such column` errors. After every rebuild + install, restart every long-running bacio.
- **Never `pkill -f "bacio web"` or `pkill -f bacio`.** They match every bacio process on the machine and will kill the user's own running bacio UI. When you start `bacio web` in the background for a smoke test, capture its PID (`web_pid=$!`) and stop only that process (`kill "$web_pid"`).
- **Port-in-use is not yours to resolve.** If `bacio web` / `bacio api` reports a port already in use, do NOT kill whatever holds it — it's the user's bacio. Re-check you're inside your worktree, or pass `--port`.
- **The dispatch supervisor is a thin scheduler.** When a `<channel>` dispatch arrives, the parent session immediately calls `Task(subagent_type="bacio-<mode>-worker", prompt=stub)` and forwards the subagent's one-line summary. All real work happens in the subagent. Don't add code that grows the parent's per-dispatch context (e.g. no pushing dispatch metadata into the parent's TodoWrite). See `<worktree_root>/docs/agent-dispatch.md`.
- **Worktree env is opt-in.** `bacio worktree init` is opt-in; manifest-free worktrees keep the legacy default (`~/.bacio/db.sqlite` + `127.0.0.1:5320`) exactly. `init` defaults to **port isolation only** — DB stays shared so dispatched workers reach their ticket. DB isolation is opt-in via `--isolate-db`; never set `BACIO_WORKTREE_ISOLATE_DB` ambiently.
- **`bacio agent dispatch` / `ack` follow the six agent-CLI rules; `bacio tui` / `api` / `web` / `channel` / `hook` / `install-*` are harness-integration shims and don't.** When adding a CLI verb, ask "who types this command — a person/agent, or a runtime?".
- **Edited a prompt template body? Run `bacio install-agent`.** Template bodies are the source of generated `<worktree_root>/.claude/agents/<name>.md` files; without re-install the dispatched worker still uses the old body. `bacio status` reports per-template agent-file freshness.
- **Raw `sqlite3 ~/.bacio/db.sqlite ...` is hook-denied in agent mode (BACI-134).** The PreToolUse hook denies every `sqlite3` invocation whose path argument resolves to the shared store, including read-only ones — every mutation must go through a `bacio` CLI verb so the audit log records it, and read diagnostics belong in `bacio` read verbs / `bacio history`. A worktree-isolated DB (`bacio worktree init --isolate-db`) is at a different absolute path and is allowed.
- **Workspace vs system bacio (BACI-139).** Inside a linked worktree, `<worktree_root>/build.sh` writes `<worktree_root>/.bin/bacio-agent-<slug>` — a uniquely-named workspace binary that embeds your in-progress source. Use it ONLY for smoke-testing the change you are implementing. Every close-out bookkeeping call (`pr attach`, `agent release`, `tag add`, `worktree rm`, `comment add`, `install-agent`, `install-skill`) must use the bare `bacio` on PATH, which is the known-good system binary that the dispatch pipeline expects.

If you find a footgun that fits this list, add it here — the bar is "would a model that hasn't read the linked doc do the wrong thing?".
