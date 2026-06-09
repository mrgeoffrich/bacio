# Architecture

A one-shot onboarding read for the bacio codebase. Covers the mental model — what the binaries are, how processes coexist, how data flows, how the React tree is shared, and how Claude Code plugs in — without diving into any one feature. Topic-specific deep dives live in [`docs/`](docs/); this doc is the index in narrative form.

## The big picture in one paragraph

`bacio` is a kanban for one developer (or a small team), driven mostly by Claude Code. The whole project ships as **one Go binary** (`bacio`) plus an **optional desktop wrapper** (`bacio-desktop`). All state lives in a **single SQLite store at `~/.bacio/db.sqlite`** — there is no daemon, no server-process-you-must-keep-running. Every surface (CLI, TUI, HTTP API, web bundle, desktop app, agent integration) is a different entry point into the same store. The same React tree powers both the desktop app and the browser-served bundle. Concurrent processes coordinate through a leader lease in the DB; only one of them runs the background tickers at a time.

## Binaries

### `bacio` — the single binary that does everything

`bacio` is a cobra app with one set of subcommands grouped into two intent categories:

- **Mutating CLI verbs** — `bacio issue add` / `feature edit` / `doc upsert` / `tag add` / `agent dispatch` / `sync` / etc. These are one-shot: exit after writing. They follow the [six agent-CLI principles](docs/agent-cli-principles.md) — JSON in via `--json`, schema reachable via `bacio schema`, lean output by default, validation at the store boundary, `--dry-run`, documented in `SKILL.md`.

- **Harness-integration shims** — `bacio tui`, `bacio api`, `bacio web`, `bacio channel`, `bacio hook`, `bacio install-skill`, `bacio install-agent`. Long-running or host-driven; they sit outside the six-rule contract because the host (a terminal, an HTTP client, the Claude Code hook/channel runtime, the local filesystem) defines their I/O contract.

The shim distinction matters: documentation and tests that talk about "agent-facing mutation commands" mean only the first category.

### `bacio-desktop` — the optional Wails wrapper

Lives under [`desktop/`](desktop/) as a separate nested Go module. Bundles the React tree as a native window using [Wails v3](https://wails.io) (currently pinned at `v3.0.0-alpha.90`). Calls into the same store through generated bindings — no HTTP hop. Takes `--db` and `--env` flags to participate in the per-worktree environment system, and surfaces the resolved slug in its window title so two open desktop windows on different worktrees stay visually distinct.

### What's NOT a separate binary

There is no `bacio-server`, no `bacio-daemon`, no `bacio-sync-worker`. Every long-running role is just a subcommand of the main binary, and short-lived CLI calls reach the store directly (no IPC).

## Processes and lifecycles

| Process | Lifecycle | What it does |
|---|---|---|
| CLI subcommand (`bacio issue add` etc.) | One-shot | Opens DB, mutates, exits. |
| `bacio tui` | Long-running (user-bound) | Interactive kanban; also runs leader-gated tickers when it holds the lease. |
| `bacio api` | Long-running | HTTP API only. No `/ui/` mount; serves 404 there (BACI-72). |
| `bacio web` | Long-running | HTTP API **plus** the embedded React bundle at `/ui/`, **plus** opens the OS default browser. The one-liner humans want. By default also opens a second `/anthropic`-only listener on the proxy port (BACI-344; `--no-proxy` opts out). |
| `bacio proxy serve` | Long-running (rarely restarted) | BACI-344: standalone reverse-proxy listener — hosts ONLY `/anthropic/*` (+ `/healthz` + `/version`) on the **stable proxy port** (API port − 1). The process agents pin via `ANTHROPIC_BASE_URL`; splitting it off `bacio web` lets the UI / binary / schema be upgraded without interrupting in-flight agent turns. Keep it alive with `bacio proxy install-service` (launchd / systemd user unit). |
| `bacio channel` | Long-running (Claude Code-spawned) | MCP-over-stdio server. One per Claude Code session, started by `.mcp.json`. Bridges agent ↔ bacio (dispatch delivery, reply, ask_user_question, send_user_notification). |
| `bacio hook <event>` | One-shot (Claude Code-spawned) | Claude Code event hooks (SessionStart, PreToolUse, PostToolUse, …). Writes to the store and exits. |
| `bacio-desktop` | Long-running (user-bound) | Wails window; runs the same leader-gated tickers as `bacio api` / `bacio tui` via `leaderservice`. |

The long-running processes (`bacio tui` / `api` / `web` / `channel` / desktop) all share the same SQLite store. **A schema change with a stale binary still running anywhere surfaces as `no such column` errors.** After `./build.sh` + install, restart every long-running bacio you have open before testing.

## Data stores

### `~/.bacio/db.sqlite` — the source of truth

A single SQLite file in WAL mode. All issues, features, comments, documents, tags, agent registry, dispatches, audit log — everything — lives here. Schema is migrated at startup by `internal/store`. WAL gives concurrent readers; writes serialize.

Multiple repos coexist in one DB, keyed by repo prefix (`BACI`, `MINI`, …). `bacio init` allocates a prefix; running any mutating CLI command from a fresh git repo auto-allocates one if `init` hasn't been called.

### `~/.bacio/worktrees.yaml` — per-user registry

Tracks every git worktree that ran `bacio worktree init`. Authoritative for port allocation (so `init` doesn't have to scan filesystem YAMLs) and backs `bacio worktree list`. See [`docs/worktree-environments.md`](docs/worktree-environments.md).

### `<worktree>/environment-config.yaml` — per-worktree manifest (opt-in)

Optional. Written by `bacio worktree init`. Pins the bacio instance in that worktree (CLI / api / web / desktop / channel / hooks) to its own API port; **DB stays shared by default** so dispatched workers reach the ticket they were assigned. Resolution chain: explicit `--db`/`--addr` flags > `$BACIO_ENV` > worktree manifest > legacy default (`~/.bacio/db.sqlite` + `127.0.0.1:5320`). The resolver also derives a stable **proxy addr** (BACI-344: `ProxyAddr` = API port − 1) alongside the API addr, for the standalone `bacio proxy serve` listener and the launch one-liner's `ANTHROPIC_BASE_URL`. Implemented in [`internal/wtenv/`](internal/wtenv/).

### `~/sync/<project>/` — git-backed sync mirror (opt-in)

Optional. Mirrors the SQLite slice for one project as YAML + markdown in a separate git repo. `bacio sync` pulls → imports → exports → commits → pushes. Designed for sharing one board across machines or with a teammate. See [getting-started.md §4](docs/getting-started.md#4-sync-across-machines-when-youre-ready).

## The leader-elected controller

When two long-running bacio processes are running against the same DB (e.g. the user's `bacio tui` plus a `bacio web` smoke test), they would otherwise both run the same background tickers — sync, dispatch matcher, idle pinger, archive sweep, prune, queue matcher — and stomp on each other. The fix is a **leader lease** in a `ui_leader` row:

- `internal/leader` — the lease primitive (heartbeat, takeover, election).
- `internal/leaderservice` — the wrapper every long-running process constructs at startup. Owns the lease loop and exposes "am I the leader right now?".
- `internal/controller` — the thin scheduler. Drives six leader-gated tickers (sync, dispatch match, idle ping, archive sweep, prune, queue match) each gated on the lease.

Every leader-gated piece of background work — including [BACI-89 background sync](docs/background-sync.md) — runs at most once on the machine, on whichever process currently holds the lease. The TUI also drives the same package-level helpers when it's the leader.

`bacio worktree rm` reaps any bacio process holding a `LISTEN` socket on the torn-down worktree's API port (BACI-93) so an orphan can't keep heartbeating the lease after teardown.

## The React tree — one codebase, two transports

[`desktop/frontend/`](desktop/frontend/) is a React + Vite app. The single import chokepoint is `src/api.ts` — every component imports data through it.

**Two build modes,** selected by Vite mode:

- **Desktop mode** — `npm run build` (default). `api.ts` is the Wails-bound version that calls generated bindings under `bindings/`. The desktop binary embeds the bundle and runs it in a native Wails window.
- **Web mode** — `npm run build:web` (`vite --mode web`). Two aliases swap the seam: `./api` resolves to `src/api.http.ts` (fetch against `bacio api`'s REST endpoints), and `@wailsio/runtime` resolves to a no-op stub. The output ships to the repo-root `webui/` directory.

The CLI binary `//go:embed`s `webui/`. **`bacio web` mounts the bundle at `/ui/` and pops the browser; `bacio api` is API-only after BACI-72 and returns 404 on `/ui/`.** Reach for `bacio web` whenever the UI is in the loop — including agent-driven Playwright smoke testing (`bacio web --no-open` is the right flag, then drive with the `playwright-cli` skill).

Client-side routing via `react-router` v7, `<BrowserRouter>` on both surfaces (BACI-203). Basename derives from `import.meta.env.BASE_URL` so the same source tree resolves to `/ui` in web mode and `/` in desktop mode; both asset servers (`internal/api/static.go` for `bacio web`, `application.AssetFileServerFS` for Wails) implement SPA fallback on unknown paths. See [`docs/web-app-mode.md`](docs/web-app-mode.md) §7a for the route map and the path-helper module at `desktop/frontend/src/lib/routes.ts`.

Markdown rendering across both surfaces follows one rule: every read surface in the React tree goes through `<MarkdownView>` (`desktop/frontend/src/lib/markdownView.tsx`); never `react-markdown` directly. The TUI side uses `internal/tui/markdown.go`'s `renderMarkdown` (glamour). See [`docs/markdown-rendering.md`](docs/markdown-rendering.md).

The tree's internal grain — the `lib/hooks/` data primitives, the `api/contract.ts` DTO seam, the `state/` Context providers that keep `App.tsx` a thin shell, and the decomposed `components/<domain>/` views — is documented in [`docs/frontend-architecture.md`](docs/frontend-architecture.md). Read it before any non-trivial frontend change.

## Claude Code integration

bacio is designed to be driven by an agent — primarily Claude Code — through four host-integration surfaces, all set up by **one install verb**: `bacio install-agent`.

### `.claude/skills/bacio/SKILL.md` — the skill

`bacio install-skill` writes the agent-facing skill at `.claude/skills/bacio/SKILL.md`. This is the **single source of truth** for how to drive bacio from an agent — schemas, examples, the "discover → compose → rehearse → execute → query lean" loop. The skill's frontmatter description is what makes Claude auto-load it; keep the trigger keywords broad.

### `.claude/agents/bacio-<mode>-worker.md` — per-mode subagent system prompts

Each dispatch mode (`plan`, `design`, `implement`, `review`, `ship`, `fix_review`) has a custom subagent file under `.claude/agents/`. Their bodies come from [`prompts/agents/<slug>.md`](prompts/agents/) — one `.md` per slug, with shared blocks (`_preamble.md`, `_postamble.md`) inlined via the `{{> name}}` include directive that `model.ExpandPromptIncludes` resolves at load and render time. Editing happens via `bacio settings template set <slug> --body "..."`, the TUI Settings tab, or the desktop Settings panel — **after editing a body, run `bacio install-agent` to regenerate the agent files**.

The render frontmatter sets `isolation: worktree`, so Claude Code spawns each dispatched worker in its own throwaway git worktree and cleans it up when the worker finishes.

### `.claude/settings.json` — hooks

`bacio install-agent` merges its event hooks into `.claude/settings.json`:

- `SessionStart` — records the agent identity from `.bacio/agents.json`.
- `PostToolUse` (TaskCreate, Write/Edit) — mirrors the agent's TaskCreate rows into bacio and updates issue activity.
- `PreToolUse` (Write/Edit matcher) — confines dispatched workers and agent-mode sessions to the linked worktree (BACI-116, hardened in BACI-129): denies any Write/Edit in the primary checkout; restricts paths inside a linked worktree to that worktree's root.
- `StopFailure` — fires when a turn ends on an Anthropic API error (529 overloaded, 5xx, 429 rate_limit, auth/billing failures). Records the errored state on the agent session (`error_type` / `error_message` / `errored_at`, surfaced as a red "errored" liveness) and reconciles the worker's in-flight Pipeline job: transient errors pause the chain in place (`engine_pause_reason = agent_error`, Auto off), terminal errors pull the card out to `needs_action`. Observe-only — Claude Code ignores its stdout/exit code, so recovery (re-arm) is a user/controller concern (BACI-296).
- `Notification` / `Stop` / `SubagentStop` — agent lifecycle plumbing.

### `.mcp.json` — the bacio channel MCP server

The channel surfaces four MCP tools to the agent: `register` (called by the SessionStart hook), `reply` (acks a dispatch), `ask_user_question` (parks a clarification for the supervisor to answer), `send_user_notification` (fires a non-blocking agent→user notification). (BACI-307 retired a fifth, `attach_transcript`; captured Anthropic traffic now lives in `proxy_messages`.)

The channel and `bacio hook` subprocesses **inherit cwd from Claude Code**, so `bacio install-agent` deliberately does NOT bake `BACIO_ENV` into `.mcp.json` / `.claude/settings.json` (regression-tested in `internal/cli/install_channel_test.go`). The per-worktree resolver picks up the right environment from cwd.

## Dispatch flow — end to end

```
human / agent              bacio store              session's channel             supervisor          custom subagent
─────────────              ───────────              ─────────────────             ──────────          ───────────────
bacio agent dispatch ──►  agent_dispatches
                          (pending)                                                                    
                              │                                                                      
                          (leader-gated matcher binds)                                               
                              ▼                                                                      
                          (bound to session) ────►  drain tick polls
                                                    sees a target
                                                    forwards as <channel>
                                                    event              ──────►   Task(subagent_type=
                                                                                  "bacio-<mode>-worker",
                                                                                  prompt=tiny stub)
                                                                                                      ▼
                                                                                                  isolation: worktree
                                                                                                  (Claude Code spawns
                                                                                                  the throwaway worktree)
                                                                                                      │
                                                                                                  per-mode brief is
                                                                                                  the system prompt
                                                                                                      │
                                                                                                  - bacio agent claim
                                                                                                  - bacio worktree init
                                                                                                  - bacio issue brief
                                                                                                  - do the work
                                                                                                  - bacio agent release
                                                                                                    --state <next>
                                                                                                  - mcp__bacio__reply
                                                                                                      │
                                                                                 ◄────────────────────┘
                                                                                  one-line summary
                                                                                  returns from Task
                                                                                      │
                                                                                  supervisor forwards
                                                                                  the summary + replies
```

The supervisor stays a **thin scheduler** — its context budget across dozens of jobs is "dispatch arrived → Task call → summary → reply". All file reads, edits, and bash calls happen inside the subagent's context, which Claude Code discards on return. Per-mode briefs being the subagent's system prompt (rather than per-dispatch payload — the BACI-76 reversal) keeps them prompt-cache-eligible across back-to-back same-mode spawns.

Per-job todos don't leak across dispatches: the PostToolUse hook stamps each TaskCreate row with the session's current `(issue_key, dispatch_id)` (BACI-132), so a session that handles plan-then-implement on one ticket shows only the current dispatch's tasks on the kanban card.

Full detail: [`docs/agent-dispatch.md`](docs/agent-dispatch.md). The PreToolUse confinement: [`docs/agent-dispatch.md`](docs/agent-dispatch.md) + the BACI-116 / BACI-129 sections.

## Package layout (Go side)

```
cmd/bacio/                   # main entry point; thin: NewRoot() + Execute()
internal/
  cli/                       # cobra commands, inputs/, schema.go, install-*.go
  cli/inputs/                # *Input structs reflected for --json + bacio schema
  store/                     # SQLite + migrations + validators + lookups
  model/                     # domain types (Issue, Feature, AgentDispatch, …)
  model/prompttemplates/     # legacy: embedded built-in prompt bodies (prompts/agents/ now)
  api/                       # HTTP handlers for bacio api / bacio web
  webui/                     # web mode helpers (mount, embed, etc.)
  tui/                       # bubbletea v1 + lipgloss app; see docs/tui-cookbook.md
  channel/                   # MCP server for bacio channel
  controller/                # leader-gated tickers (sync, match, ping, sweep, prune, qmatch)
  leader/                    # lease primitive
  leaderservice/             # constructor wrapping leader + controller for HTTP surfaces
  wtenv/                     # per-worktree environment resolution (BACI-63 stack)
  logging/                   # file logging resolver (BACI-73)
  procfind/                  # cross-platform port-listener discovery (BACI-93)
  sync/                      # git-backed sync engine + background runner (BACI-89)
  agentmode/                 # agent-mode detection + cwd helpers
  dispatcher/                # agent_dispatches → session binding matcher
  idlepinger/                # session-keepalive ticker (BACI-133)
  boardcards/                # composite AgentCard assembly (BACI-50 server-side)
  git/                       # git toplevel + branch helpers
  transcript/                # subagent transcript ingest (BACI-85)
  client/                    # the Wails-bound bindings target (desktop/)
  schema/                    # JSON Schema reflection helpers
prompts/                     # SKILL.md (installed copy) + agents/ (worker briefs)
  agents/                    # _preamble.md, _postamble.md, _dispatch_preamble.md,
                             # plan.md, design.md, implement.md, review.md, ship.md, fix_review.md
docs/                        # contributor-facing topic deep dives (this file is the index)
  site/                      # USER-facing docs — markdown source for https://bacio.io/docs/,
                             # synced into the bacio-website repo at build time. Edit here if
                             # you're changing what users see; the public site rebuilds from
                             # this directory. Do NOT mix contributor notes in here.
  designs/                   # archived design docs from past `design` dispatches
  screenshots/               # assets referenced from the contributor docs
desktop/                     # nested Go module: Wails v3 + React frontend
  frontend/                  # Vite + React; src/api.ts is the import chokepoint
  bindings/                  # Wails-generated TS bindings (do not hand-edit)
embed.go                     # //go:embed of SkillMarkdown, PromptsFS, WebUIFS
```

## Build & install

`./build.sh` — full rebuild (web bundle → CLI binary embeds it → Wails bindings regen → desktop frontend → desktop Go binary). Opt-out flags `--skip-web` and `--skip-desktop` shorten the inner loop. **`./build.sh` does NOT install to `~/.local/bin/bacio`** — that's deliberate so a build in one worktree can't silently clobber the binary another worktree expects on PATH. Install explicitly from the worktree you want on PATH: `go build -o ~/.local/bin/bacio ./cmd/bacio`.

Always run `./build.sh` before validating, testing, retesting, or smoke-testing anything — and after install, restart any running TUI / desktop / agent sessions so they pick up the new binary against the (often-migrated) shared DB.

## Where to read next

The docs in this table are **contributor-facing** — they describe how the code works. The user-facing documentation site at https://bacio.io/docs/ has its own source under [`docs/site/`](docs/site/); edit there for anything users see.

| Topic | Doc | When to read |
|---|---|---|
| Agent-facing CLI conventions | [`docs/agent-cli-principles.md`](docs/agent-cli-principles.md) | Before adding or changing any mutating CLI command. |
| Dispatched-work pipeline (data model, matcher, channel, subagent delegation) | [`docs/agent-dispatch.md`](docs/agent-dispatch.md) | Before touching `internal/dispatcher` / `internal/channel` / `prompts/agents/`. |
| TUI patterns | [`docs/tui-cookbook.md`](docs/tui-cookbook.md) | Before any non-trivial work in `internal/tui/` — bubbletea v1, not v2. |
| Markdown rendering | [`docs/markdown-rendering.md`](docs/markdown-rendering.md) | Before touching markdown rendering on any surface. |
| Motion layout animations | [`docs/motion-layout-animations.md`](docs/motion-layout-animations.md) | Before touching card-movement animations on the React surfaces — the Pipeline cards or the ship flourish. |
| Per-worktree environments | [`docs/worktree-environments.md`](docs/worktree-environments.md) | Before touching `internal/wtenv/` or anything that resolves a DB / port / log dir. |
| File logging | [`docs/logging.md`](docs/logging.md) | When debugging a long-running process or adding a new structured log emitter. |
| Background sync | [`docs/background-sync.md`](docs/background-sync.md) | Before touching `internal/sync/` or the sync UI. |
| Profiling | [`docs/profiling.md`](docs/profiling.md) | When diagnosing a TUI freeze or memory issue. |
| Frontend architecture (hooks / api seam / state providers / view decomposition) | [`docs/frontend-architecture.md`](docs/frontend-architecture.md) | Before any non-trivial change to `desktop/frontend/src`. |
| Web app mode (browser-served React) | [`docs/web-app-mode.md`](docs/web-app-mode.md) | When changing the seam between Wails and HTTP transports. |
| User-facing intro | [`docs/getting-started.md`](docs/getting-started.md) | When orienting a new bacio user (not for codebase work). |
