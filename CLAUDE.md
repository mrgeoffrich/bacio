# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Required reading before planning or implementing

Three docs in `docs/` carry the non-obvious conventions this codebase relies on. Read whichever is relevant **before** you start planning a change — they'll shape the design, not just check it after the fact.

- **[`docs/agent-cli-principles.md`](docs/agent-cli-principles.md)** — the six rules every mutating CLI command honours (JSON in via `--json`, schema reachable via `bacio schema`, lean output by default, validation at the store boundary, `--dry-run` support, documented in `SKILL.md`) plus the explicit "deliberately don't do" list. Read before adding or changing any CLI command.
- **[`docs/tui-cookbook.md`](docs/tui-cookbook.md)** — bubbletea v1.3.10 + lipgloss v1.1.1-pre + bubbles patterns, pinned to v1. Upstream READMEs have moved to v2 and will mislead you. Read before any non-trivial work in `internal/tui/`.
- **[`docs/markdown-rendering.md`](docs/markdown-rendering.md)** — the per-surface markdown audit (BACI-65) and the rule that every React-side read surface goes through `<MarkdownView>` (never `react-markdown` directly), with `remark-gfm` providing tables / task lists / autolinks / strikethrough. Read before touching any markdown rendering on any surface.

The deeper context for all three lives in the topic sections below (`## Agent-CLI principles`, `## TUI cookbook`, `## Markdown rendering`).

## Quick commands

- **`./build.sh`** — **always run this before validating, testing, retesting, or smoke-testing anything.** Default is the full rebuild: the web bundle (embedded into the CLI binary at `/ui/`), the CLI/TUI binary, the regenerated Wails bindings, the desktop frontend, and the desktop Go binary. **`./build.sh` does NOT install the CLI to `~/.local/bin/bacio`** — that would let a build in one worktree silently clobber the binary another worktree expects on PATH. Install explicitly from the worktree you want on PATH: `go build -o ~/.local/bin/bacio ./cmd/bacio`. The user's TUI + desktop + per-session `bacio channel` MCP processes all share the same SQLite store, so a schema change with a stale binary still running anywhere will surface as "no such column" errors. Opt-out flags for the inner loop: `--skip-web` (skip the Vite web bundle — saves the npm install + Vite step when nothing in `desktop/frontend/` changed) and `--skip-desktop` (skip the Wails desktop build). Combine them (`--skip-web --skip-desktop`) for CLI/TUI-only. Before asking the user to test, run this (and install if the user is running the CLI directly), then tell them to restart any running TUI / desktop / agent sessions to pick up the new binary.
- `go build ./...` — build everything in the main module (does NOT cover `desktop/` — it's a separate nested module; use `./build.sh` instead).
- `go build -o ~/.local/bin/bacio ./cmd/bacio` — install just the CLI/TUI binary to `~/.local/bin/bacio`. Run this explicitly from whichever worktree you want on PATH.
- `go vet ./...` — vet the codebase.
- `go test ./...` — run the unit tests.
- `bacio <subcommand>` from inside any git working tree drives the CLI.
- The SQLite database lives at `~/.bacio/db.sqlite` by default. Override via `--db <path>` when testing or validating changes.
- **Smoke-testing a desktop / web change.** The same React tree drives both the Wails desktop binary and the web bundle, so the cheapest agent-driven validation is the web path: run `bacio web --no-open` (default `http://localhost:5320/ui/`) and drive the UI with the `playwright-cli` skill — no Wails native-window dance, no browser pop, and snapshots come back as readable accessibility trees. `--no-open` is the right flag for hooks-in-the-loop testing: the agent drives the page through Playwright, so popping a real browser would just confuse the human watching the agent. (Humans iterating on the same surface can drop the flag and let `bacio web` pop the browser as a one-liner.) After a frontend change, `./build.sh --skip-desktop` is enough to refresh the embedded bundle; restart the running `bacio web` to pick up the new binary. `bacio api` is API-only after BACI-72 and serves 404 on `/ui/`, so reach for `bacio web` whenever the UI is in the loop.

## Worktree environments (BACI-63)

Sibling git worktrees of this repo can clash on the shared writer at `~/.bacio/db.sqlite` and the `127.0.0.1:5320` API port. The fix is opt-in: `bacio worktree init` writes a `<worktree-root>/environment-config.yaml` that binds the bacio instance in that worktree (CLI / `bacio api` / `bacio web` / desktop / channel / hooks) to its own port. Manifest-free worktrees keep today's behaviour exactly — the legacy default DB and port — so nothing changes for users who don't `init`.

**DB isolation is opt-in (BACI-87).** `bacio worktree init` defaults to **port isolation only** — it pins the shared `~/.bacio/db.sqlite` into the manifest so issue calls still reach the ticket. This is what a dispatched worker needs (its ticket lives in the shared DB) and what a normal user wants. DB isolation — a fresh per-worktree `.bacio/db.sqlite`, useful when testing bacio's own schema migrations across sibling worktrees — is opt-in via `--isolate-db` or `BACIO_WORKTREE_ISOLATE_DB=1`. Set that env var **per-invocation only**: ambiently set, a dispatched worker would inherit it and re-break BACI-87.

Resolution order (highest precedence first):

1. Explicit `--db` / `--addr` flags. The desktop binary takes `--db` and `--env` too.
2. `$BACIO_ENV=<path to a manifest YAML>`. The global `--env <path>` flag overrides the env var.
3. Worktree-root `environment-config.yaml`. Found by walking up from cwd to a git toplevel.
4. Legacy default: `~/.bacio/db.sqlite` + `127.0.0.1:5320`.

Step 3 returning "no manifest present" is not an error — it falls through to step 4 so existing users see no change.

The MCP channel and `bacio hook` subprocesses inherit cwd from Claude Code, so `bacio install-agent` does NOT bake `BACIO_ENV` into `.mcp.json` / `.claude/settings.json` (regression-tested in `internal/cli/install_channel_test.go`). The desktop binary captures cwd before Wails has a chance to chdir, then routes through the same resolver — and surfaces the resolved slug in its window title so two open desktop windows on different worktrees stay visually distinct. `bacio status` is the canonical readout — every status report includes `db_path`, `api_addr`, `env_source`, and (when relevant) `env_path`.

Designed and shipped per [`docs/worktree-environments.md`](docs/worktree-environments.md); CLI surface (`bacio worktree init / show / list / rm`) follows the six agent-CLI principles (`--json`, `--dry-run`, schema entries `worktree.init` / `worktree.rm`). Heavy lifting lives in [`internal/wtenv/`](internal/wtenv/).

## Profiling

Three hidden persistent root flags capture profiles/traces — a dev/debug affordance, kept out of `--help`:

- `bacio --cpuprofile <path> tui` — writes a CPU profile covering the whole interactive session.
- `bacio --memprofile <path> tui` — writes a heap profile (`runtime.GC()` then `pprof.WriteHeapProfile`) of what survived the session.
- `bacio --trace <path> tui` — writes an execution trace covering the whole session. Unlike the CPU profile, the trace captures off-CPU events (goroutine scheduling, blocking on syscalls/channels/mutexes), so it's the tool for diagnosing UI freezes — a stall on a slow query or `git` shell-out is invisible to CPU profiling but shows up here.

They start in the root `PersistentPreRunE` and flush in `stopProfiling` (`internal/cli/profiling.go`). `NewRoot()` returns that cleanup func; `cmd/bacio/main.go` runs it after `Execute()` returns — on success and error alike — so profiles flush even when a command exits via an error path (cobra skips `PersistentPostRunE` on error). Open CPU/heap output with `go tool pprof <path>`, trace output with `go tool trace <path>`. Today the flags are wired for `bacio tui`; extending them to the short-lived CLI commands is a possible follow-up.

## File logging (BACI-73)

Long-running processes (`bacio api`, `bacio channel`, the desktop binary) write a per-process log file alongside their stderr output. The destination resolves via the same precedence chain as the per-worktree DB + port (flag → env → worktree manifest → default fallback) — implemented in [`internal/logging`](internal/logging/logging.go). Two persistent root flags, both inert on short-lived CLI verbs: `--log-dir <path>` and `--log-level <debug|info|warn|error>` (mirrored by `$BACIO_LOG_DIR` / `$BACIO_LOG_LEVEL`). The desktop binary takes the same flags. Manifests can pin a path via the optional `allocations.log_dir` field; absent that, the resolver synthesises `<worktree-root>/.bacio/logs/`. Files roll daily and are named `bacio-<component>-YYYY-MM-DD.log` (`bacio-channel-pid<N>-YYYY-MM-DD.log` for the channel — concurrent channels for one project share a PID stamp). `bacio status` carries `log_dir` / `log_source` / `log_level`. A log-dir creation failure falls back to stderr-only with a single warning — never blocks the process from starting. Full spec: [`docs/logging.md`](docs/logging.md).

## Background sync (BACI-89)

`bacio sync` is no longer CLI-only. The leader-elected controller runs a sixth leader-gated ticker — `SyncIfLeader` (`internal/controller/controller.go`), on `store.SyncTickInterval` (5 min) — that mirrors every sync-enabled repo automatically, running the same `sync.Engine.Run()` pipeline a manual `bacio sync` runs. The per-tick logic lives in [`internal/sync/background.go`](internal/sync/background.go)'s `BackgroundRunner` (`controller` imports `sync`, not the reverse — no cycle); the controller stays a thin scheduler. The runner self-gates: an in-flight flag skips overlapping ticks rather than stacking them, and repeated push failures back off exponentially (capped ~1h). Because `bacio api` / `bacio web` and the desktop all construct the controller via `leaderservice.New`, they get the ticker for free; the TUI drives it itself via the package-level `SyncIfLeader` helper.

Background sync is **opt-out**, defaulting **ON** once sync is configured — the global `sync.background_enabled` `app_setting`. `Store.GetSyncBackgroundEnabled` deliberately inverts the usual default (missing reads as `true`). Toggle it via `bacio settings sync-background true|false` (schema entry `settings.sync-background`, `--json` / `--dry-run`) or the HTTP `GET / PUT /settings/sync-preferences` pair. Sync status — `last_sync_at`, `last_sync_error`, in-progress — is exposed read-only over `GET /sync` and `GET /repos/{prefix}/sync`, so `api.http.ts` no longer hardcodes `syncEnabled: false`; the desktop / web `Sync` topbar badge is a live status indicator (not a button — there is no manual "Sync now" affordance).

## Agent-CLI principles (read before planning a feature)

`docs/agent-cli-principles.md` is the durable reference for the conventions bacio adopted from Justin Poehnelt's "Rewrite Your CLI for AI Agents". Every mutating command accepts JSON via `--json`, publishes its schema via `bacio schema`, returns lean output by default, validates input at the store boundary, supports `--dry-run`, and is documented in `SKILL.md`. New CLI work should honour those six rules and the explicit "deliberately don't do" list (no NDJSON, no `--field` projection, no silent input normalisation, etc.).

The exception is **harness-integration shims** (see below): `bacio tui`, `bacio api`, `bacio web`, `bacio hook`, `bacio channel`. These aren't agent-facing mutation commands — they're glue to a host (a terminal, an HTTP client, the Claude Code hook/channel runtime) — so they deliberately skip the six rules (no `--json`, no `bacio schema` entry, no `--dry-run`). They're still documented in `SKILL.md`. Agent-facing dispatch verbs (`bacio agent dispatch` / `ack`) DO follow the six rules.

## TUI cookbook

`docs/tui-cookbook.md` is a synthesised reference for bubbletea v1.3.10 + lipgloss v1.1.1-pre + bubbles. Read it before doing anything non-trivial in `internal/tui/`. The snippets are pinned to v1; upstream READMEs have already moved to v2 and will mislead you.

Note: when changing or testing the TUI make sure to read `docs/tui-cookbook.md` for essential knowledge.

## Markdown rendering

`docs/markdown-rendering.md` is the per-surface audit (BACI-65) of how bacio renders markdown across the TUI, desktop and web. Two surface families, one canonical reader each:

- **TUI:** `internal/tui/markdown.go`'s `renderMarkdown` (glamour, dark style, per-width cache). GFM is on by default. Every TUI view that displays markdown goes through that helper — the per-view `mdCache` plumbing is consolidated on a single value type.
- **Desktop / web:** `desktop/frontend/src/lib/markdownView.tsx`'s `<MarkdownView>` (react-markdown + remark-gfm). **Never import `react-markdown` directly** outside that wrapper — the missing-`remark-gfm` regression that silently dropped GFM tables is structurally impossible to repeat once every read path runs through one seam. `.mk-markdown` in `app.css` carries the table / task-list / strikethrough styles. The TipTap editor in DocsView is the only WYSIWYG and stays separate.

Read the doc before touching markdown rendering on any surface.

## Dispatched jobs are delegated to per-mode `bacio-<mode>-worker` subagents

A `bacio channel`-equipped session is a **thin scheduler** for dispatched work: when an issue-tied `<channel>` event arrives, the parent session immediately calls `Task(subagent_type="bacio-<mode>-worker", prompt=<tiny stub>)` and forwards the subagent's one-line summary. All file reads / edits / bash calls happen inside the subagent's context, which is discarded on return, so the parent's context budget stays at "dispatch arrived → Task call → summary → reply" size across dozens of jobs (BACI-52). Don't add code that grows the parent's context per dispatch (e.g. don't push dispatch metadata back into the parent's TodoWrite). After `Task` returns, the supervisor calls `mcp__bacio__attach_transcript` (the channel's fourth MCP tool, BACI-85) with the issue key and the subagent's `agentId` — bacio attaches the raw subagent transcript (`.jsonl`, capped at ~2.5 MB) and links it to the issue as a `project_complete` doc, so a later reviewer can read exactly what the worker did. See `docs/agent-dispatch.md`.

The per-mode brief is **not** in the dispatch payload (BACI-76) — it is the system prompt of a per-mode custom subagent (`bacio-plan-worker`, `bacio-design-worker`, …, `bacio-fix-review-worker`), written to `.claude/agents/bacio-<mode>-worker.md` by `bacio install-agent`. Moving the brief out of the payload makes it prompt-cache-eligible across back-to-back same-mode spawns and shrinks per-dispatch channel traffic ~10×. The payload `ComposeDispatchPayload` now produces is the **dispatch preamble** plus a tiny stub (ticket / mode / subagent type). The preamble — a reserved row (`slug = _dispatch_preamble`) — tells the parent supervisor to spawn the per-mode subagent named in the stub; it stays in the payload (the supervisor is not a custom subagent). Edit any template the same way as before: `bacio settings template set <slug> --body "..."`, or via the Settings panel in the TUI / desktop app — **but a template body is now a generated artefact's source; run `bacio install-agent` after editing a body to regenerate the `.claude/agents/` files** (`bacio status` reports per-template agent-file freshness). The embedded default bodies live in [`internal/model/prompttemplates/`](internal/model/prompttemplates/); the preamble row is seeded by `backfillDispatchPreamble` and rewritten from the old `general-purpose` default by `refreshDispatchPreamble`, both in `internal/store/store.go`. Setup is now one install step: `bacio install-agent` (it does all three — agent files, hooks, channel). Full design lives in [`docs/agent-dispatch.md`](docs/agent-dispatch.md) under "Subagent delegation".

Per-job todos don't leak across dispatches: when the PostToolUse hook records a `TaskCreate`, it stamps the row with the session's currently-claimed `issue_key` (resolved from the single open claim at hook time; orphan-bucketed when zero or many claims are open). The Agents view and the kanban card's `n/m` Tasks pill both filter per-(session, issue), so a session that handles two dispatches back-to-back only shows the current job's rows on each surface. Prior-job rows stay in `agent_session_todos` (queryable per `issue_key`), they just stop bleeding into the foreground UI (BACI-62).

### Imperative `action_label` vs gerund `name` (BACI-67)

Each `prompt_templates` row carries two display strings: `name` is the gerund ("Planning", "Designing", "Implementing") that lower-cases into the activity pill on a taken card ("planning · BACI-12"), and `action_label` is the imperative form ("Plan", "Design", "Implement") rendered as the button text on the dispatch action menus — the kanban-card dropdown, the issue-workspace shelf, and the TUI per-card picker. The split exists so a button reads as a call to action without breaking the status-description form the pill needs. Built-ins ship with both seeded; user-created templates can set `action_label` explicitly or leave it empty and the UI derives one from `name` via `model.DeriveActionLabel` (`Planning → Plan`, `Shipping → Ship`, etc.). Set / clear via `bacio settings template set-action-label <slug> <label>` (empty string clears the override), the `--action-label` flag on `bacio settings template add`, the Action label input in the desktop / web Settings panel, or the Action label pane in the TUI Settings tab's add overlay. The activity-pill derivation in `internal/boardcards/cards.go` still reads `name` — don't conflate the two when adding a surface.

## `agent_session_questions` — agent → user clarification (BACI-53)

The bacio channel exposes `ask_user_question` (one of its four MCP
tools — alongside `reply`, `register`, and `attach_transcript`) that
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
