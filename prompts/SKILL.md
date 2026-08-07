---
name: bacio
description: Use this skill whenever you need to create, read, update, or organise tasks/issues/tickets/todos using the `bacio` CLI — a local issue tracker that ships with this repo. Triggers on any mention of issues, features, kanban work, kanban lanes/columns, workspaces, tags, blocks/blocked-by relations, attached pull requests, project documents, document folders, or audit-log/history queries managed by `bacio`. Prefer `bacio` over external trackers (e.g. GitHub Issues) whenever the user is tracking work for a repo where `bacio` is in use.
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
- A **workspace** is a repo with **no git working tree** — a tracked space for work that isn't code. It holds issues, documents, doc folders and a Kanban board exactly as a git repo does, and shares the same prefix namespace. Because it has no path, it can *never* be resolved from `cwd`: see the `--repo` selector below.
- A **feature** is an optional grouping of issues (a project or epic). Issues can exist without one.
- An **issue** has a title, description, state, tags, comments, relations, and attached PR URLs. Addressed by a 4-letter `PREFIX-N` key like `MINI-42`.
- A **document** is a per-repo named text blob with a typed category; issues and features link to documents with a short reason. Documents can be filed into a **doc folder** tree, which is purely organisational — a filename stays flat and unique per repo, so folders never change a doc's identity, links or URLs.
- A **Kanban lane** is a per-repo board column, orthogonal to issue state. A card is on the board **iff** it sits in a lane.

**Two boards, one tracker.** The **Agentic Pipeline** is keyed on `state` (`in_pipeline`, `to_be_shipped`, …) and is where dispatched agents work. The **Kanban** is keyed on lanes and is the human board. They are independent axes: `bacio kanban move` never changes an issue's state, and `bacio issue state` never changes its lane. In a **workspace**, new issues land on the leftmost lane automatically and there is no Pipeline (no working tree for an agent to work in — dispatch is refused server-side). In a **git repo**, new issues start **off** the Kanban and are opted in explicitly.

**Issue states:** `todo | in_review | done | cancelled`, plus the Pipeline-page columns `in_pipeline | to_be_shipped`. The parser accepts dashes/spaces (`in-review`, `in review`). The legacy `in_progress` / `needs_action` states were retired (BACI-300) — work now flows through the Pipeline, and "an agent is paused waiting on the user" is signalled by an open `ask_user_question` on the ticket (or, for a pipeline card, the engine's `engine_pause_reason`), not a state.

**Auto-create on first use:** any mutating command in an unregistered git repo creates the repo row and allocates a 4-char prefix from the directory basename. `bacio status` is read-only and never auto-registers — use it as a safe probe. `--repo <PREFIX>` is a lookup, never a create: an unknown prefix errors out rather than minting a repo.

## Conventions that matter

- **`cd` to the repo first — or pass `--repo <PREFIX>`.** Most commands resolve the repo from `cwd`. The global `--repo <PREFIX>` flag (falling back to `$BACIO_REPO`) names the repo explicitly and short-circuits that detection entirely. It is **mandatory for workspaces** — they have no working tree, so `cwd` can never find one, and without it bacio would auto-register whatever unrelated git repo you happen to be standing in. It is a *selector*, so it lives beside `--db` / `--remote` / `--dry-run` and never appears as a `--json` field. It is **case-insensitive** (`--repo home` == `--repo HOME`) and a **lookup, never a create**: an unknown prefix errors instead of minting a repo. (`bacio issue list` and `bacio history` also take `--all-repos`.)
- **Audit attribution is automatic.** Every mutation is audit-logged with an actor. With `bacio install-agent` set up in the repo, the SessionStart hook records your `(claude_pid → agent identity)` mapping in `.bacio/agents.json`; subsequent `bacio` calls resolve actor through that mapping with no flag needed. Calls without an agent identity (humans at the terminal) stamp the placeholder `"user"`.
- **Prefer `--json` over typed flags** when driving `bacio`. It is strict (typos surface as `unknown field` errors), takes long text as inline strings (no file/stdin dance), and its shape is published via `bacio schema`. Accepts `--json '<json>'`, `--json -` (stdin), or `--json @path`. It is mutually exclusive with positionals and per-field flags.
- **Rehearse with `--dry-run`.** Every mutating command accepts it: runs everything up to the SQL write, emits the projected result, touches nothing. Use it before deletes (`issue rm` / `feature rm` / `doc rm` / `repo rm` report cascade counts) and to validate a `--json` payload.
- **Pass `-o json` when parsing.** Default output is human text and may shift; JSON is the contract. List commands are lean by default — `bacio issue list -o json` drops `description` (add `--with-description`); `bacio doc list` is metadata-only.
- **Issue keys in JSON must be canonical** (`MINI-42`). The bare-number shortcut (`42`) works on the flag path only.
- **Comment author is required** — `--as <name>` on the flag path, `"author"` on the JSON path.
- **Validation is strict at the store boundary.** No control characters, no silent trimming of identifiers, kebab-case slugs, `http`/`https` PR URLs, unknown JSON fields rejected — malformed input fails loud rather than being normalised.
- **Per-feature board-hide toggle (BACI-177)** is per-repo display state, not bacio state — it lives in the per-repo `tui_settings` KV alongside the TUI's feature picker, not on the features row. REST entry points: `GET /repos/{prefix}/features/hidden` returns `{slugs: []}`, and `PUT /repos/{prefix}/features/{slug}/hide` with body `{hidden: bool}` flips it (audits as `feature.hide` / `feature.unhide`). Flipping the toggle on the desktop / web Features screen is visible to the TUI on its next reload. There is no `bacio feature hide` CLI verb today — it's a display preference (same precedent as `bacio settings show-archived`).

## What's in the box

A map of the command groups, not a manual. Run `bacio <group> --help` for the verbs and flags of each, and `bacio schema show <command>` for a mutation's payload — those are always current, this list is not. What's spelled out below is the semantics `--help` can't give you; **Gotchas** at the end carries the rest.

- **`bacio init` / `bacio repo`** — bind a repo, list/show, `repo rm` (destructive — needs `--confirm <PREFIX>`), `repo link <PREFIX> <PATH>` (bind a phantom repo — sync-imported with no local working tree — to an absolute path; writes `.bacio/config.yaml` pointing at the owning sync repo's remote). `repo list` carries a `kind` field: `git` or `workspace`.
- **`bacio workspace`** — `add <NAME> [--prefix XXXX]` / `list` / `rm` for repos with no git working tree. `add` seeds the mandatory catch-all features and the starter Kanban board (Backlog / Doing / Waiting / Done), and allocates a prefix from the name unless you pin one. `rm` is `repo rm` narrowed to workspaces: same cascade, same `--confirm <PREFIX>` gate, and it refuses a git repo so you can't delete the wrong kind of thing by typo. Everything afterwards needs `--repo <PREFIX>` — except verbs addressed by issue key (`bacio issue show HOME-1`, `bacio kanban move HOME-1 …`), which resolve the repo from the key's own prefix.
- **`bacio kanban`** — the human board. `kanban column add|rename|mv|rm|list` manage lanes (addressed **by name**; matching is exact first, then a unique case-insensitive match, so `--column doing` finds `Doing`). `kanban move <KEY> --column <NAME> [--position N]` places one card; `--off-board` takes it off. `column mv --position` is **0-based** (0 is the leftmost lane) and only the moved lane comes back — re-read `kanban column list` for the new board order. Deleting a lane never deletes an issue: its cards just come off the board.
- **`bacio doc folder` / `bacio doc mv`** — the document tree. `doc folder add <NAME> [--parent <PATH>]`, `rename <PATH> <NEW-NAME>`, `mv <PATH> --to <PATH>|--to-root`, `rm <PATH>`, `list`. Folders are addressed by their **slash display path** exactly as `doc folder list` prints it; segments are matched exactly and are **case-sensitive**. `doc mv <FILE> --folder <PATH>|--to-root [--position N]` files a page. Deleting a folder deletes its subfolders but **re-roots** every page inside — a folder is organisational, so losing one never loses a document. Nesting is capped at 16 levels, and moving a folder into its own subtree is refused inside the write transaction — so both can fail *after* a `--dry-run` said otherwise.
- **`bacio status`** — read-only probe: repo, DB path, env resolution, stats, and an `llm_recommendations` array of setup fixes to action.
- **`bacio feature`** — add/list/show/edit/rm; `feature plan <slug>` prints open issues in dependency order; `feature comment add` takes `--kind note|handoff`. Three per-feature switches are independent verbs, not one setting: `feature state <active|done|cancelled>`, `feature auto-close on|off` (may the leader-elected sweep auto-promote a feature whose children are all terminal — turn OFF for standing buckets like `bugs` / `maintenance`), and `feature handoffs on|off` (are worker close-out handoffs collected). Archiving is orthogonal to all three. Two optional fields shape downstream behaviour: `emoji` (single glyph, rendered on every child card) and `branch_name` (set = every child issue ships to that integration branch instead of `main`).
- **`bacio issue`** — add/list/show/edit/rm, `state`, `assign`/`unassign`, `archive`/`unarchive`; `brief <KEY>` is the one-shot bulk-context JSON read (issue + feature + relations + PRs + documents with plan/review bodies inlined + comments + claimants) and is what a dispatched worker should reach for first; `next`/`peek` atomically claim the next ready issue in a feature. `customer_impact` is an optional single line stating the change in the user's terms; it fronts the shipped list and the impact-first board view, falling back to the title when blank. Blank is a first-class "no user-facing change" — don't manufacture one. `auto_run` (`--auto-run`) on `issue add` starts the new card running the full Scope → Plan → Implement → Ship chain immediately — it lands `in_pipeline` with that chain and engine Auto on, no manual start. It is **off by default on the CLI and REST surfaces** so a triage session filing ten tickets doesn't spawn ten workers; the UI composers (desktop / web / TUI) default it on with a per-issue toggle. An explicit `state` other than `in_pipeline` wins and turns it off.
- **`bacio issue` Pipeline verbs** — `reorder`, `process set|edit|reset`, `ship`, `mark-done`, `auto-ship`; all take `--json` / `--dry-run` and have `bacio schema` entries. A card's **process** is an ordered chain of job modes: `process set` takes a preset slug (`bacio issue process set --help` lists them) or, on the `--json` path, an explicit `"stages"` list for chains no preset covers. `process edit` re-orders only the **pending tail** — completed / running / cancelled jobs stay as a locked prefix. `process reset` wipes the entire chain, history included, back to the from-scratch picker (refused while a job is running). `ship`, `shelve`, and `mark_done` are **terminal sentinels** — each runs no agent, they are mutually exclusive, and one may appear only as the final stage: `ship` hands off to the Shipping column, `shelve` demotes the card back to Backlog and clears its chain, `mark_done` closes it out directly. Start / Stop and the Auto drive-mode toggle are engine-side with no CLI verb — the controller job-engine owns chain progression for `in_pipeline` cards.
- **`bacio comment`** — add/list/rm. `--eval` (or `"eval": true`) marks the row as a quality-review note: the server pins the in-flight `(agent_session_id, dispatch_id, mode)` snapshot onto it at write time, and `comment list -o json` surfaces those fields on every row.
- **`bacio link` / `bacio unlink`** — issue relations: `blocks`, `relates-to`, `duplicate-of` (`blocked-by` is the inverse view of `blocks`).
- **`bacio tag`** — add/rm free-form labels; filter with `--tag` on `issue list`.
- **`bacio pr`** — attach/detach/list PR URLs. `bacio pr create <KEY> -- --title "..." --body "..."` is the BACI-163 wrapper around `gh pr create`: pre-flights against duplicate PRs (refuses on OPEN/MERGED labelled with `bacio:<KEY>`), idempotently creates a `bacio:<KEY>` label, opens the PR through `gh`, and funnels the URL through `bacio pr attach`. Prefer it over bare `gh pr create` in dispatched-worker close-out so a second worker can't open a duplicate PR for the same ticket. `--force` overrides the pre-flight; `--dry-run` projects the `gh` argv without running anything.
- **`bacio doc`** — per-repo documents: add/upsert/list/show/edit/rename/export/archive/rm, plus `link`/`unlink` to issues and features, and the `folder` / `mv` tree verbs above.
- **`bacio history`** — the per-DB audit log (60-day retention); filter by `--since`/`--from`/`--to`, `--op`, `--kind`, `--user-filter`. Background subsystems stamp their own actor strings (`bacio-matcher` for matcher binds, `bacio-controller` for leader-driven sweeps, `bacio-channel-ping` for the idle pinger) so `--user-filter <actor>` is a coherent per-subsystem ledger. Ops include the standard CRUD set plus background-only entries: `agent.bind` (matcher hand-off), `agent.deliver` (first push to the worker), `leader.takeover` (lease changed hands — one row per holder change, not per renewal), and `question.abandon` (channel-restart sweep of orphaned questions).
- **`bacio proxy`** — the reverse-proxy capture index: every Anthropic turn an agent made, recorded and parsed. Reads, cheapest first: `stats` (per-FQDN rollup), `jobs` (one row per dispatch with parsed captures — scope with `--repo` / `--issue` / `--mode` / `--session` / `--agent` / `--since`), `job <dispatch_id>` (one dispatch's turns as an ordered transcript), `captures` / `capture <id>` / `raw <id>` (index rows → one parsed turn → the raw bytes), and `grep <text>` (content search across parsed bodies; each hit carries the capture id you drill into). The index is cross-cutting — no repo column, so these run anywhere. Reads take `-o json` but have no `--json` / `--dry-run` / schema entry, like `bacio history`. The one mutating verb is `reparse`, which backfills `proxy_messages` for dispatches the live recorder missed; `--retry-failed` also un-sticks captures the parser previously gave up on, and `--rebuild --dispatch <id>` destructively replays one dispatch through the current parser (recovery after a parser bug is fixed — it requires `--dispatch`, and a global `--rebuild` is refused). Two harness shims live here too: `proxy serve` (a standalone `/anthropic/*`-only listener on the stable proxy port — the API port − 1 — so `bacio web` can be restarted without dropping in-flight agent turns; `bacio web` / `api` open one themselves by default, `--no-proxy` opts out) and `proxy install-service` (a per-user keep-alive unit: launchd on macOS, systemd user unit on Linux).
- **`bacio archive`** — `archive sweep` runs the auto-archive passes on demand, including the BACI-199 feature auto-completion pass that promotes `active`-with-every-child-terminal features to `done` / `cancelled` (skipping rows pinned via `bacio feature auto-close <slug> off` — BACI-250).
- **`bacio agent`** — the local agent-session registry: register/heartbeat/end, claim/release, dispatch/inbox/ack/cancel, list/show, and `agent questions` for user clarifications. Never synced. Sequencing work across stages is the Pipeline engine's job-chain (`plan → implement → ship …`); the board-era follow-on verbs (`queue-followon` / `cancel-followon` / `dispatch-chain`) were retired in BACI-279.
- **Per-space nav surfaces** are display state with **no CLI verb**: `show_agent_surfaces` ("Agent Mode" — the Agentic Pipeline / Agents / Monitor tabs) and `show_kanban` ("Show Kanban Board"). They live on `repo_settings`, default by repo kind (agents on + Kanban off for a git repo, the reverse for a manual workspace), and are written over `PUT /repos/{prefix}/show-agent-surfaces` / `PUT /repos/{prefix}/show-kanban` with body `{enabled: bool}`. Nothing in Go branches on them, so they get no `bacio schema` entry — same precedent as the per-feature board-hide toggle above. `repo_settings` is unsynced, so they are per-machine.
- **`bacio settings`** — global settings: `template` (dispatch prompt templates), `show-archived`, `sync-background`, `archive` (auto-archive toggle + retention window), `timezone` (an IANA zone driving the Shipping column's local-midnight "Today" cutoff; unset by default, auto-detected by the UI, server stays zone-agnostic). One verb here is **per-repo, not global**: `settings default-feature [SLUG]` sets the feature every new-issue surface applies when no explicit `feature_slug` is given — an explicit one always wins, and deleting the referenced feature auto-clears the setting.
- **`bacio sync`** — git-backed sync of the local DB to a separate sync repo: `init`, `clone`, bare `sync` (steady state), `verify`, `inspect`, `remotes` (per-machine registry listing).
- **`bacio worktree`** — per-worktree environment manifests so sibling worktrees don't clash on the API port: `init`/`show`/`list`/`rm`.
- **`bacio install-skill` / `bacio install-agent`** — set another repo up (see Installation).
- **Harness shims** — `bacio tui` (terminal kanban), `bacio api` / `bacio web` (HTTP API ± embedded UI), `bacio hook` / `bacio channel` (Claude Code integration), `bacio agent-run-command` (prints the one-liner that spins up an agent-mode Claude session — built for `eval "$(bacio agent-run-command)"`). None take `--json` / `--dry-run` or have a schema entry: a runtime types these, not a person or an agent.

  The **`bacio channel` MCP server** is the one a dispatched worker actually calls, exposing five tools: `register` (complete session registration), `reply` (ack a dispatch), `ask_user_question` (**blocking** multi-choice clarification), `send_user_notification` (**non-blocking** agent→user note into the notification bell / TUI Notifications tab; optional `issue_id` deep-links it), and `report_subagent_incomplete` (the supervisor's path when a `Task` subagent ended early — it cancels the running job + dispatch, halts neutrally, and releases the stale claim; it **replaces** `reply` for that dispatch, never accompanies it). Reach for `send_user_notification` when the user should *see* something and you carry on; `ask_user_question` when you need them to *answer*.

## Worked example

```bash
cd ~/Repos/myproject                          # repo resolves from cwd

bacio schema show issue.add | jq .examples[0]  # discover the payload shape

bacio issue add --dry-run --json '{
  "title": "Login broken on Safari",
  "feature_slug": "auth-rewrite",
  "description": "500 on submit. Repro inline.",
  "customer_impact": "Login no longer 500s on Safari",
  "tags": ["bug", "P0"]
}' -o json                                     # rehearse

bacio issue add --json '{ ...same... }' -o json   # commit
bacio issue brief MINI-42                      # one-shot bulk context for an LLM
```

Driving a **workspace** — note that `cd` buys you nothing here, so every call carries the selector:

```bash
bacio workspace add "Home Renovation"          # → prefix HOME, board seeded
export BACIO_REPO=HOME                         # or pass --repo HOME per call

bacio issue add "Replace the back fence"       # lands on the leftmost lane
bacio kanban column list                       # Backlog / Doing / Waiting / Done + card counts
bacio kanban move HOME-1 --column doing --position 0

bacio doc folder add Quotes
bacio doc add fence-quote.md --type architecture --content "Two quotes attached."
bacio doc mv fence-quote.md --folder Quotes
bacio doc folder list                          # slash paths, one per line
```

## CLI client mode (`--remote`)

`bacio` can drive a remote `bacio api` server instead of the local DB: set `--remote http://host:5320` (or `$BACIO_REMOTE`) and `--token` / `$BACIO_API_TOKEN` if the server enforces auth. Every read and mutating verb behaves identically. Verbs that touch the local filesystem or terminal (`init`, `install-skill`, `install-agent`, `tui`, `schema`, `status`, `hook`, `channel`, the `settings template` and `sync` verbs) stay local-direct and error clearly under `--remote`.

## Gotchas

- **Never run `bacio` outside a git repo** when a command needs the current repo — it hard-errors. `cd` first, or pass `--repo <PREFIX>`.
- **A workspace is unreachable without `--repo` / `$BACIO_REPO`.** It has no path, so `cwd` detection can never find it — `git.Detect` walks up for a `.git` toplevel and a workspace has none, so there is no other route to one on any surface. Running a repo-scoped verb from an unrelated directory without the selector will resolve (or auto-register) *that* directory's git repo instead — a silent wrong-target, not an error.
- **A few verbs ignore `--repo` by design** because they probe *where you are* rather than operate on a named project: `bacio status` (a cwd/environment probe — `BACIO_REPO=WKSP bacio status` still reports on the current working tree, or "not inside a git repository") and `bacio sync init` / `sync clone` (they set sync up for the checkout you're standing in). A workspace legitimately has no answer for either.
- **`""` is a destination, not a blank.** On `doc folder mv` (`to`), `doc mv` (`folder`) and `kanban move` (`column`), the empty string means **the tree root** / **off the board**. Because that is a real value, the `--json` path requires the key to be *present*: omitting it is an error, never an implicit no-op. On the flag path use the explicit `--to-root` / `--off-board` spelling. The one exception is `doc folder add`'s `parent`, where omitting the key and passing `""` both mean the root — creating at the root is a harmless default, whereas *moving* something there silently is not.
- **Omitting `position` means append**, on both `doc mv` and `kanban move`. Kanban positions are dense 0-based indices within a lane (and `kanban column mv --position` is a dense 0-based board slot); doc-folder positions are a loose sort key that siblings may share, tie-broken on filename. `bacio issue reorder`, by contrast, is 1-based — different surface.
- **Folder paths are case-sensitive; lane names are not (quite).** A doc folder segment must match exactly. A Kanban lane name matches exactly first, then falls back to a *unique* case-insensitive match — an ambiguous fold errors rather than guessing.
- **Deleting containers never deletes contents.** `doc folder rm` re-roots every page in the subtree; `kanban column rm` takes cards off the board. Both report the counts under `--dry-run`.
- **Comment author (`--as <name>` / `"author"`) is required** on every comment add — the CLI rejects the call if it's missing.
- **Long text in JSON is just an inline string** — JSON's own `\n` escapes work; no `--description-file` needed on the JSON path.
- **Mixing `--json` with positionals/flags is rejected** — choose one mode per call.
- **Auto-created prefixes can collide** — a prefix stays 4 characters, so a second project deriving `HOME` gets `HOM2`, a third `HOM3`. Same namespace for git repos and workspaces. Confirm with `bacio repo list`.
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
