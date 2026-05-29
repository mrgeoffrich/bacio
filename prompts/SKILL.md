---
name: bacio
description: Use this skill whenever you need to create, read, update, or organise tasks/issues/tickets/todos using the `bacio` CLI — a local issue tracker that ships with this repo. Triggers on any mention of issues, features, kanban work, tags, blocks/blocked-by relations, attached pull requests, project documents, or audit-log/history queries managed by `bacio`. Prefer `bacio` over external trackers (e.g. GitHub Issues) whenever the user is tracking work for a repo where `bacio` is in use.
---

# `bacio` CLI

`bacio` is a local CLI issue tracker backed by a single SQLite db at `~/.bacio/db.sqlite`. It is built to be driven non-interactively.

This skill orients you and states the conventions that aren't obvious from `--help`. For the exact flags of any command, run `bacio <command> --help`; for the JSON payload shape of any mutation, run `bacio schema show <command>`.

## Discover, don't memorise

- `bacio --help` — every command group.
- `bacio <group> --help` (e.g. `bacio issue --help`, `bacio agent --help`) — the subcommands and their flags.
- `bacio schema list` — every mutating command that accepts `--json`, one line each.
- `bacio schema show <command>` — full JSON Schema (draft 2020-12) for one command, with a worked `examples[0]` you can copy. `bacio schema all` dumps every schema in one pass.

## Mental model

**Hierarchy:** repo → (optional) feature → issue.

- A **repo** is auto-detected from `cwd` by walking up to a `.git` toplevel. Issues, features, and documents are scoped to a repo.
- A **feature** is an optional grouping of issues (a project or epic). Issues can exist without one.
- An **issue** has a title, description, state, tags, comments, relations, and attached PR URLs. Addressed by a 4-letter `PREFIX-N` key like `MINI-42`.
- A **document** is a per-repo named text blob with a typed category; issues and features link to documents with a short reason.

**Issue states:** `todo | in_progress | needs_action | in_review | done | cancelled`. The parser also accepts dashes/spaces (`in-progress`, `in progress`). Use `needs_action` when an agent is paused waiting on the user.

**Auto-create on first use:** any mutating command in an unregistered git repo creates the repo row and allocates a 4-char prefix from the directory basename. `bacio status` is read-only and never auto-registers — use it as a safe probe.

## Conventions that matter

- **`cd` to the repo first.** Most commands resolve the repo from `cwd`. (`bacio issue list` and `bacio history` also take `--all-repos`.)
- **Audit attribution is automatic.** Every mutation is audit-logged with an actor. With `bacio install-agent` set up in the repo, the SessionStart hook records your `(claude_pid → agent identity)` mapping in `.bacio/agents.json`; subsequent `bacio` calls resolve actor through that mapping with no flag needed. Calls without an agent identity (humans at the terminal) stamp the placeholder `"user"`.
- **Prefer `--json` over typed flags** when driving `bacio`. It is strict (typos surface as `unknown field` errors), takes long text as inline strings (no file/stdin dance), and its shape is published via `bacio schema`. Accepts `--json '<json>'`, `--json -` (stdin), or `--json @path`. It is mutually exclusive with positionals and per-field flags.
- **Rehearse with `--dry-run`.** Every mutating command accepts it: runs everything up to the SQL write, emits the projected result, touches nothing. Use it before deletes (`issue rm` / `feature rm` / `doc rm` / `repo rm` report cascade counts) and to validate a `--json` payload.
- **Pass `-o json` when parsing.** Default output is human text and may shift; JSON is the contract. List commands are lean by default — `bacio issue list -o json` drops `description` (add `--with-description`); `bacio doc list` is metadata-only.
- **Issue keys in JSON must be canonical** (`MINI-42`). The bare-number shortcut (`42`) works on the flag path only.
- **Comment author is required** — `--as <name>` on the flag path, `"author"` on the JSON path.
- **Validation is strict at the store boundary.** No control characters, no silent trimming of identifiers, kebab-case slugs, `http`/`https` PR URLs, unknown JSON fields rejected — malformed input fails loud rather than being normalised.
- **Per-feature board-hide toggle (BACI-177)** is per-repo display state, not bacio state — it lives in the per-repo `tui_settings` KV alongside the TUI's feature picker, not on the features row. REST entry points: `GET /repos/{prefix}/features/hidden` returns `{slugs: []}`, and `PUT /repos/{prefix}/features/{slug}/hide` with body `{hidden: bool}` flips it (audits as `feature.hide` / `feature.unhide`). Flipping the toggle on the desktop / web Features screen is visible to the TUI on its next reload. There is no `bacio feature hide` CLI verb today — it's a display preference (same precedent as `bacio settings show-archived`).

## What's in the box

Run `bacio <group> --help` for the subcommands of each.

- **`bacio init` / `bacio repo`** — bind a repo, list/show, `repo rm` (destructive — needs `--confirm <PREFIX>`), `repo link <PREFIX> <PATH>` (bind a phantom repo — sync-imported with no local working tree — to an absolute path; writes `.bacio/config.yaml` pointing at the owning sync repo's remote).
- **`bacio status`** — read-only probe: repo, DB path, env resolution, stats, and an `llm_recommendations` array of setup fixes to action.
- **`bacio feature`** — add/list/show/edit/rm; `feature plan <slug>` prints open issues in dependency order. `bacio feature state <slug> <state>` (BACI-199) sets the per-feature `active|done|cancelled` column — writes are state-only (BACI-250 decoupled state from auto-close). The leader-elected auto-completion sweep promotes an `active` feature whose every child is in a terminal state to `done` (when at least one child is `done`) or `cancelled` (when every child is `cancelled`), emitting a `feature.auto-state` audit row alongside the existing `archive.sweep` summary. `bacio feature auto-close <slug> on|off` (BACI-250) flips the sticky `state_manual` bit independently of state: OFF pins long-lived catch-all features (`bugs`, `maintenance`) so the sweep leaves them `active` indefinitely, ON (the default) lets the sweep run. Archive (`archived_at`) stays orthogonal — a `done` or `cancelled` feature is still visible by default until explicitly archived. Features carry an optional single-glyph `emoji` field (BACI-172) that the kanban / desktop renders top-left on every card under the feature; the `plan_large` agent picks one on creation, and users can change it from the desktop Features pane.
- **`bacio issue`** — add/list/show/edit/rm, `state`, `assign`/`unassign`, `archive`/`unarchive`; `brief <KEY>` is a one-shot bulk-context JSON read; `next`/`peek` atomically claim the next ready issue in a feature.
- **`bacio issue` Pipeline verbs** — the Pipeline page's ordering + process controls: `bacio issue reorder <KEY> <position>` moves a card within its Backlog (`todo`) / Shipping (`to_be_shipped`) ordering band (position 1 = top, next to go); `bacio issue process set <KEY> <preset>` assigns a job chain to an `in_pipeline` card — the flag/positional path takes a preset slug (presets: `plan-implement-ship`, `implement-ship`, `plan-implement`, `plan`, `plan_large`, `design`, `implement`), and the `--json` path additionally accepts an explicit ordered stage list via `"stages"` (e.g. `["design","plan_large","implement","ship"]`, mutually exclusive with `"process"`) for arbitrary chains the presets don't enumerate; `bacio issue ship <KEY>` hands an `in_pipeline` card off to the Shipping column (dispatches no agent — the ship agent fires from Shipping); `bacio issue auto-ship <on|off>` toggles the per-repo Shipping auto-ship. The per-card engine controls (manual Start / Stop, the Auto drive-mode toggle) are API/engine-only (no CLI verb): the controller job-engine owns chain progression for `in_pipeline` cards, queuing ordinary dispatches the matcher binds and advancing state itself. All four verbs take `--json` / `--dry-run` and have `bacio schema` entries (`issue.reorder`, `issue.process.set`, `issue.ship`, `issue.auto-ship`).
- **`bacio comment`** — add/list/rm. Pass `--eval` (or `"eval": true` on the JSON path) on `bacio comment add` to mark the row as a BACI-131 quality-review note — the server pins the in-flight `(agent_session_id, dispatch_id, mode)` snapshot onto the comment at write time, and `bacio comment list -o json` surfaces the four fields on every row. BACI-141 added an optional `"transcript_event_ref"` field on `bacio comment add --json` (formats: `tool_use_id:<id>` or `line_index:<n>`) — used by the desktop / web transcript viewer's per-event composer to pin an eval note to a specific event inside a `.jsonl` transcript; omit on board-level eval notes.
- **`bacio link` / `bacio unlink`** — issue relations: `blocks`, `relates-to`, `duplicate-of` (`blocked-by` is the inverse view of `blocks`).
- **`bacio tag`** — add/rm free-form labels; filter with `--tag` on `issue list`.
- **`bacio pr`** — attach/detach/list PR URLs. `bacio pr create <KEY> -- --title "..." --body "..."` is the BACI-163 wrapper around `gh pr create`: pre-flights against duplicate PRs (refuses on OPEN/MERGED labelled with `bacio:<KEY>`), idempotently creates a `bacio:<KEY>` label, opens the PR through `gh`, and funnels the URL through `bacio pr attach`. Prefer it over bare `gh pr create` in dispatched-worker close-out so a second worker can't open a duplicate PR for the same ticket. `--force` overrides the pre-flight; `--dry-run` projects the `gh` argv without running anything.
- **`bacio doc`** — per-repo documents: add/upsert/list/show/edit/rename/export/archive/rm, plus `link`/`unlink` to issues and features.
- **`bacio history`** — the per-DB audit log (60-day retention); filter by `--since`/`--from`/`--to`, `--op`, `--kind`, `--user-filter`. Background subsystems stamp their own actor strings (`bacio-matcher` for matcher binds, `bacio-controller` for leader-driven sweeps, `bacio-channel-ping` for the idle pinger) so `--user-filter <actor>` is a coherent per-subsystem ledger. Ops include the standard CRUD set plus background-only entries: `agent.bind` (matcher hand-off), `agent.deliver` (first push to the worker), `leader.takeover` (lease changed hands — one row per holder change, not per renewal), and `question.abandon` (channel-restart sweep of orphaned questions).
- **`bacio archive`** — `archive sweep` runs the auto-archive passes on demand, including the BACI-199 feature auto-completion pass that promotes `active`-with-every-child-terminal features to `done` / `cancelled` (skipping rows pinned via `bacio feature auto-close <slug> off` — BACI-250).
- **`bacio agent`** — the local agent-session registry: register/heartbeat/end, claim/release, dispatch/inbox/ack/cancel, list/show, and `agent questions` for user clarifications. Never synced. Sequencing work across stages is the Pipeline engine's job-chain (`plan → implement → ship …`); the board-era follow-on verbs (`queue-followon` / `cancel-followon` / `dispatch-chain`) were retired in BACI-279.
- **`bacio settings`** — global settings: `template` (dispatch prompt templates), `show-archived`, `sync-background`, `archive` (BACI-162 auto-archive toggle + retention window). One verb in this group is **per-repo, not global**: `bacio settings default-feature [SLUG]` (BACI-235) sets the feature `bacio issue add` (and every other surface — REST, TUI new-issue, web composer) auto-applies when no explicit `feature_slug` is provided. Empty slug / `--clear` / `--json '{"slug":""}'` unsets it; the FK on the stored column is ON DELETE SET NULL so deleting the referenced feature auto-clears the setting. An explicit `feature_slug` always overrides the default.
- **`bacio sync`** — git-backed sync of the local DB to a separate sync repo: `init`, `clone`, bare `sync` (steady state), `verify`, `inspect`, `remotes` (per-machine registry listing).
- **`bacio worktree`** — per-worktree environment manifests so sibling worktrees don't clash on the API port: `init`/`show`/`list`/`rm`.
- **`bacio install-skill` / `bacio install-agent`** — set another repo up (see Installation).
- **Harness shims** — `bacio tui` (terminal kanban), `bacio api` / `bacio web` (HTTP API ± embedded UI), `bacio hook` / `bacio channel` (Claude Code integration), `bacio agent-run-command` (BACI-218 — prints the one-liner that spins up an agent-mode Claude session, designed for `eval "$(bacio agent-run-command)"` / `alias bacio-agent="$(bacio agent-run-command)"`). These take no `--json` / `--dry-run` and have no `bacio schema` entry. `bacio install-agent` wires a PostToolUse `bacio hook set-title` entry (matcher `mcp__bacio__register`) alongside the existing task-list mirror so a dispatched worker's terminal title flips to its agent slug as soon as `register` completes (BACI-147).

## Worked example

```bash
cd ~/Repos/myproject                          # repo resolves from cwd

bacio schema show issue.add | jq .examples[0]  # discover the payload shape

bacio issue add --dry-run --json '{
  "title": "Login broken on Safari",
  "feature_slug": "auth-rewrite",
  "description": "500 on submit. Repro inline.",
  "tags": ["bug", "P0"]
}' -o json                                     # rehearse

bacio issue add --json '{ ...same... }' -o json   # commit
bacio issue brief MINI-42                      # one-shot bulk context for an LLM
```

## CLI client mode (`--remote`)

`bacio` can drive a remote `bacio api` server instead of the local DB: set `--remote http://host:5320` (or `$BACIO_REMOTE`) and `--token` / `$BACIO_API_TOKEN` if the server enforces auth. Every read and mutating verb behaves identically. Verbs that touch the local filesystem or terminal (`init`, `install-skill`, `install-agent`, `tui`, `schema`, `status`, `hook`, `channel`, the `settings template` and `sync` verbs) stay local-direct and error clearly under `--remote`.

## Gotchas

- **Never run `bacio` outside a git repo** when a command needs the current repo — it hard-errors. `cd` first.
- **Comment author (`--as <name>` / `"author"`) is required** on every comment add — the CLI rejects the call if it's missing.
- **Long text in JSON is just an inline string** — JSON's own `\n` escapes work; no `--description-file` needed on the JSON path.
- **Mixing `--json` with positionals/flags is rejected** — choose one mode per call.
- **Auto-created prefixes can collide** — two repos sharing a basename get `XXX2`, `XXX3`, … Confirm with `bacio repo list`.
- **Issue numbers never repeat** — deleting `MINI-3` does not free the number.
- **`bacio agent cancel` is pre-delivery-only** (BACI-130). A dispatch with `delivered_at` set has been handed to the worker; cancelling it would just lie in the model — the work continues but the kanban activity pill drops out. Use the agent's interrupt path to stop a live worker, and `bacio agent cancel <id>` only for queued / pending rows that haven't been delivered yet.
- **An agent-mode session in the main checkout cannot Write/Edit anything** (BACI-129). When `bacio install-agent` is wired and `BACIO_AGENT_MODE=1` is set, the PreToolUse hook denies every `Write`/`Edit` whose cwd is the primary git worktree. Linked worktrees (`git worktree add` / `bacio worktree init`) are the allowed write surface. A supervisor that wants to edit the parent checkout directly must drop `BACIO_AGENT_MODE` for that shell.
- **Raw `sqlite3 ~/.bacio/db.sqlite ...` is denied in agent mode** (BACI-134). The same PreToolUse hook denies every `sqlite3` Bash invocation whose path resolves to the shared store — including read-only ones. Every mutation must go through a `bacio` CLI verb so the audit log records it; reach for `bacio` read verbs or `bacio history` for diagnostics, or `bacio worktree init --isolate-db` if you need a throwaway DB for smoke tests.
- **`link` / `unlink` need a claim on either side, not both** (BACI-170). The BACI-126b claim gate is relaxed for relations: an agent claimed on either the `from` or `to` side of `bacio link` (or the `a`/`b` side of `bacio unlink`) can run the verb, so the planning/reviewer flow "my ticket blocks / relates-to / duplicates that one" works without a second claim. Other gated verbs (`issue edit`, `comment add`, `tag add`, `pr attach`, etc.) still require a claim on the targeted ticket because they actually mutate it.

## Installation

If unsure whether `bacio` is installed, run `bacio --help`; `bacio --version` shows the version. To install:

```bash
brew tap mrgeoffrich/bacio && brew install bacio         # prebuilt binary (macOS/Linux)
go install github.com/mrgeoffrich/bacio/cmd/bacio@latest # pure-Go install (no CGO)
go build -o ~/.local/bin/bacio ./cmd/bacio               # from a bacio checkout
```

`bacio install-skill`, run from anywhere inside another repo, writes this skill to `.claude/skills/bacio/SKILL.md` so that repo's agents discover it — re-run after upgrading `bacio`.

`bacio install-agent --yes` sets a repo up for agent-driven bacio work in one idempotent invocation: per-mode dispatch subagent files under `.claude/agents/`, the `bacio hook` command hooks in `.claude/settings.json`, and a `bacio` channel entry in `.mcp.json` for push dispatch delivery.
