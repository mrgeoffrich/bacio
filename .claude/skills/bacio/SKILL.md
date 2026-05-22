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
- **Always pass `--user <your-agent-name>`** on every mutating command. Every mutation is audit-logged with its actor; without `--user` the CLI silently falls back to the OS username, making the log useless.
- **Prefer `--json` over typed flags** when driving `bacio`. It is strict (typos surface as `unknown field` errors), takes long text as inline strings (no file/stdin dance), and its shape is published via `bacio schema`. Accepts `--json '<json>'`, `--json -` (stdin), or `--json @path`. It is mutually exclusive with positionals and per-field flags.
- **Rehearse with `--dry-run`.** Every mutating command accepts it: runs everything up to the SQL write, emits the projected result, touches nothing. Use it before deletes (`issue rm` / `feature rm` / `doc rm` / `repo rm` report cascade counts) and to validate a `--json` payload.
- **Pass `-o json` when parsing.** Default output is human text and may shift; JSON is the contract. List commands are lean by default — `bacio issue list -o json` drops `description` (add `--with-description`); `bacio doc list` is metadata-only.
- **Issue keys in JSON must be canonical** (`MINI-42`). The bare-number shortcut (`42`) works on the flag path only.
- **Comment author is required** — `--as <name>` on the flag path, `"author"` on the JSON path.
- **Validation is strict at the store boundary.** No control characters, no silent trimming of identifiers, kebab-case slugs, `http`/`https` PR URLs, unknown JSON fields rejected — malformed input fails loud rather than being normalised.

## What's in the box

Run `bacio <group> --help` for the subcommands of each.

- **`bacio init` / `bacio repo`** — bind a repo, list/show, `repo rm` (destructive — needs `--confirm <PREFIX>`).
- **`bacio status`** — read-only probe: repo, DB path, env resolution, stats, and an `llm_recommendations` array of setup fixes to action.
- **`bacio feature`** — add/list/show/edit/rm; `feature plan <slug>` prints open issues in dependency order.
- **`bacio issue`** — add/list/show/edit/rm, `state`, `assign`/`unassign`, `archive`/`unarchive`; `brief <KEY>` is a one-shot bulk-context JSON read; `next`/`peek` atomically claim the next ready issue in a feature.
- **`bacio comment`** — add/list.
- **`bacio link` / `bacio unlink`** — issue relations: `blocks`, `relates-to`, `duplicate-of` (`blocked-by` is the inverse view of `blocks`).
- **`bacio tag`** — add/rm free-form labels; filter with `--tag` on `issue list`.
- **`bacio pr`** — attach/detach/list PR URLs.
- **`bacio doc`** — per-repo documents: add/upsert/list/show/edit/rename/export/archive/rm, plus `link`/`unlink` to issues and features.
- **`bacio history`** — the per-DB audit log (60-day retention); filter by `--since`/`--from`/`--to`, `--op`, `--kind`, `--user-filter`.
- **`bacio archive`** — `archive sweep` runs the auto-archive passes on demand.
- **`bacio agent`** — the local agent-session registry: register/heartbeat/end, claim/release, dispatch/inbox/ack/cancel, list/show, and `agent questions` for user clarifications. Never synced.
- **`bacio settings`** — global settings: `template` (dispatch prompt templates), `show-archived`, `sync-background`.
- **`bacio sync`** — git-backed sync of the local DB to a separate sync repo: `init`, `clone`, bare `sync` (steady state), `verify`, `inspect`.
- **`bacio worktree`** — per-worktree environment manifests so sibling worktrees don't clash on the API port: `init`/`show`/`list`/`rm`.
- **`bacio install-skill` / `bacio install-agent`** — set another repo up (see Installation).
- **Harness shims** — `bacio tui` (terminal kanban), `bacio api` / `bacio web` (HTTP API ± embedded UI), `bacio hook` / `bacio channel` (Claude Code integration). These take no `--json` / `--dry-run` and have no `bacio schema` entry.

## Worked example

```bash
cd ~/Repos/myproject                          # repo resolves from cwd

bacio schema show issue.add | jq .examples[0]  # discover the payload shape

bacio issue add --user agent-claude --dry-run --json '{
  "title": "Login broken on Safari",
  "feature_slug": "auth-rewrite",
  "description": "500 on submit. Repro inline.",
  "tags": ["bug", "P0"]
}' -o json                                     # rehearse

bacio issue add --user agent-claude --json '{ ...same... }' -o json   # commit
bacio issue brief MINI-42                      # one-shot bulk context for an LLM
```

## CLI client mode (`--remote`)

`bacio` can drive a remote `bacio api` server instead of the local DB: set `--remote http://host:5320` (or `$BACIO_REMOTE`) and `--token` / `$BACIO_API_TOKEN` if the server enforces auth. Every read and mutating verb behaves identically. Verbs that touch the local filesystem or terminal (`init`, `install-skill`, `install-agent`, `tui`, `schema`, `status`, `hook`, `channel`, the `settings template` and `sync` verbs) stay local-direct and error clearly under `--remote`.

## Gotchas

- **Never run `bacio` outside a git repo** when a command needs the current repo — it hard-errors. `cd` first.
- **`--user` and comment author are easy to forget** and the CLI won't stop you — pass them consistently.
- **Long text in JSON is just an inline string** — JSON's own `\n` escapes work; no `--description-file` needed on the JSON path.
- **Mixing `--json` with positionals/flags is rejected** — choose one mode per call.
- **Auto-created prefixes can collide** — two repos sharing a basename get `XXX2`, `XXX3`, … Confirm with `bacio repo list`.
- **Issue numbers never repeat** — deleting `MINI-3` does not free the number.

## Installation

If unsure whether `bacio` is installed, run `bacio --help`; `bacio --version` shows the version. To install:

```bash
brew tap mrgeoffrich/bacio && brew install bacio         # prebuilt binary (macOS/Linux)
go install github.com/mrgeoffrich/bacio/cmd/bacio@latest # pure-Go install (no CGO)
go build -o ~/.local/bin/bacio ./cmd/bacio               # from a bacio checkout
```

`bacio install-skill`, run from anywhere inside another repo, writes this skill to `.claude/skills/bacio/SKILL.md` so that repo's agents discover it — re-run after upgrading `bacio`.

`bacio install-agent --yes` sets a repo up for agent-driven bacio work in one idempotent invocation: per-mode dispatch subagent files under `.claude/agents/`, the `bacio hook` command hooks in `.claude/settings.json`, and a `bacio` channel entry in `.mcp.json` for push dispatch delivery.
