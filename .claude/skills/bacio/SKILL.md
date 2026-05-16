---
name: bacio
description: Use this skill whenever you need to create, read, update, or organise tasks/issues/tickets/todos using the `bacio` CLI — a local issue tracker that ships with this repo. Triggers on any mention of issues, features, kanban work, tags, blocks/blocked-by relations, attached pull requests, project documents, or audit-log/history queries managed by `bacio`. Prefer `bacio` over external trackers (e.g. GitHub Issues) whenever the user is tracking work for a repo where `bacio` is in use.
---

# Working with `bacio`

`bacio` is a local CLI issue tracker. Everything lives in a single SQLite db at `~/.bacio/db.sqlite`. It's designed to be driven non-interactively by AI agents — every read command supports JSON output, every mutation accepts a JSON payload via `--json`, and every payload shape is published at runtime via `bacio schema`.

## Agent quick start

The five conventions below combine into one straightforward flow. Each is documented in detail further down; this section is the cheat-sheet:

1. **Discover the shape:** `bacio schema show <command>` (or `bacio schema all` for one-shot ingestion). Worked examples are in every schema's `examples[0]`.
2. **Compose the payload as JSON.** Every mutating command accepts `--json '<payload>'`, `--json -` (stdin), or `--json @path/to.json`. Long text (descriptions, comment bodies, doc content) goes inline as a string.
3. **Rehearse with `--dry-run`** if the call is destructive or non-trivial. Stdout has the same shape as a real call; stderr emits `[dry-run] no changes were written`. Especially useful before `bacio issue rm` / `feature rm` / `doc rm`, where the dry-run reports cascade counts.
4. **Run for real** without `--dry-run`. **Always pass `--user <agent-name>`** so the audit log attributes work correctly.
5. **Query lean by default.** `bacio issue list -o json` and `bacio feature list -o json` strip the heavy `description` field; pass `--with-description` if you actually need bodies. `bacio doc show --metadata` skips the body. `bacio issue brief --no-doc-content` gives you the structure without inlining linked-doc bodies.

A worked example, end to end:

```bash
# 1. Discover.
bacio schema show issue.add | jq .examples[0]

# 2. Compose & rehearse.
bacio issue add --user agent-claude --dry-run --json '{
  "title": "Pin tab strip",
  "feature_slug": "tui-polish",
  "description": "Body height should clip the tab strip.",
  "tags": ["ui", "tui"]
}' -o json

# 3. Commit (drop --dry-run).
bacio issue add --user agent-claude --json '{ ...same payload... }' -o json
```

## Mental model

**Hierarchy:** repo → (optional) feature → issue.

- A **repo** is auto-detected from the current working directory by walking up to find a `.git` toplevel. Issues, features, and attachments are scoped to a repo.
- A **feature** is an optional grouping of issues (think a project or epic). Issues can exist without one.
- An **issue** has a title, description, state, tags, comments, relations to other issues, and attached PR URLs. Issues are addressed by a 4-letter `PREFIX-N` key like `MINI-42`.
- A **document** is a per-repo named text blob (markdown, etc.) with a typed category (architecture, designs, project-in-planning, …). Issues and features can link to documents with a short reason; the same document can be linked to many issues and features.

**Issue states**: `todo | in_progress | needs_action | in_review | done | cancelled`. The state parser also accepts dashes or spaces (`in-progress`, `in progress`).

Use `needs_action` when an LLM agent is paused waiting on the user — keep the assignee, flip the state. It signals "the next move is the human's", as distinct from `in_progress` (agent is actively working) or `in_review` (work is done and awaiting human approval).

**Auto-create on first use:** running any mutating `bacio` command (e.g. `bacio issue add`, `bacio feature add`) in a git repo that hasn't been registered yet automatically creates the repo row and allocates a 4-char prefix from the directory basename (e.g. `bacio` → `MINI`). `bacio status` is strictly read-only and will NOT auto-register — use it as a safe probe; it reports `registered: false` when the working tree isn't bound yet. Outside any git repo, `bacio` errors out — never invent a working directory just to make it run.

**Identity:** the repo is keyed by its absolute git toplevel path. Moving the repo on disk creates a new row.

## Agent registry — declare yourself

bacio tracks live agent sessions in a local SQLite registry (never synced) so you and the user can see who's working on what. Two layers:

- **Agent identity** — a slug like `cheerful-otter@claude.shiny`, one per `claude` process. Recorded in `.bacio/agents.json`, a `claude_pid → identity` map, so every `bacio` call in that process knows who it is — and so multiple agents can share one repo, each addressable separately.
- **Session** (ephemeral) — one row per running instance. FKs back to the identity, so cross-session activity correlates.

### Pick your identity — automatic

**With bacio's hooks installed** (`bacio install-hooks` — they usually are), you do nothing. The `SessionStart` hook resolves the `claude` process you're running under, mints a fresh identity if that process has none yet, records it in `.bacio/agents.json` (gitignoring `.bacio/` for you), and registers your session — all before your first turn. Every later `bacio` call self-identifies from that file, so you don't need `--user` either.

**Only if hooks are NOT installed**, register by hand at session start, before any other bacio call: generate a slug of the form `<adjective>-<animal>@<harness>.<hostname>` (e.g. `cheerful-otter@claude.shiny`) and

```bash
bacio agent register --user <your-name> --agent <slug> --new
```

If bacio errors with `agent name "<slug>" already taken`, reroll the adjective/animal and retry. Re-registering later (drop `--new`) is idempotent. The session id is auto-read from `$CLAUDE_CODE_SESSION_ID`; if your harness doesn't set it, pass `--session <id>`. `--model`, `--mode`, `--branch` are optional.

**When you start focused work on an issue — claim it:**

```bash
bacio agent claim MINI-42 --user <your-name> \
  --prompt "Implement the tab-strip fix end-to-end, then open a PR."
```

This records *intent* **and** stamps the issue's `assignee` with your agent identity — so `taken` and `assignee` stay in lockstep. It does NOT move the issue's state — use `bacio issue state` for that. Multiple agents may claim the same issue (pairing/review is a real flow); the most recent claim wins the `assignee` field. Releasing the last open claim (or `bacio agent end`) clears the `assignee` again — unless a human reassigned it in the meantime, in which case that deliberate change is left alone.

Pass `--prompt` (or `"prompt"` in `--json`) with the instruction/dispatch text you're working from — it's stored on the claim so the issue carries a record of *who* worked it and *why*. Re-claiming with a fresher `--prompt` updates it in place. A claim makes the issue **`taken`** (a derived signal — true while the issue has any open claim); busy agents are excluded as dispatch targets in the TUI and desktop app until they release.

**When you stop — release the claim and end the session:**

```bash
bacio agent release MINI-42 --user <your-name>
bacio agent end --reason stop --user <your-name>
```

`bacio agent end` auto-releases every open claim the session holds, so you can skip `release` if you're shutting down anyway.

Heartbeats are optional — `register` / `claim` / `release` already bump `last_seen_at`. `bacio agent heartbeat` exists for long-running sessions with nothing else to write. Heartbeats deliberately don't write to `bacio history` (they'd flood the audit log); register/end/claim/release do.

Inspect the registry:

```bash
bacio agent list                # alive + recently-ended sessions in this repo
bacio agent list --active       # only sessions that haven't called `end`
bacio agent list --all-repos    # across every tracked repo
bacio agent show <session-id>   # session + full claim history
```

`bacio agent show` accepts the truncated id from the `list` table (a 12-char prefix is enough in practice). Ambiguous prefixes error out — pass more characters or the full id.

The registry is local-only in v1 — running under `--remote` errors with a clear hint. v2 will add HTTP parity.

## Calling `bacio` from an agent

- **Working directory matters.** Most commands resolve the repo from `cwd`. `cd` to the repo before running unless using `--all-repos` (available on `bacio issue list` and `bacio history`).
- **Output format.** Default is human-readable text. Pass `-o json` (alias `--output json`) for structured output — use this when parsing. Every record (repo, feature, issue, comment, document) JSON includes a `uuid` field — an immutable UUIDv7 identity assigned at create time. Keep using `key`/`slug`/`filename` for human-friendly addressing; `uuid` is the canonical identifier the git-backed sync layer matches on, but you only need it directly when debugging sync (see "Git-backed sync").
- **Timestamps.** Every entity carries a `created_at`. Features, issues, and documents additionally have `updated_at` (bumped automatically on edits / state changes / tag mutations). In JSON they're UTC RFC 3339 (e.g. `2026-05-03T07:27:14Z`) — that is the parsing contract. In text mode they render in the user's local timezone (`2026-05-03 17:27 AEST`).
- **Long-text inputs.** Description and comment body MUST come from a file (`--description-file path.md`) or stdin (`--description -`). There is no inline editor. For multi-line descriptions/comments, write to a temp file or pipe via `printf`/heredoc.
- **Identifiers.**
  - Issue keys: `PREFIX-N` (e.g. `MINI-42`). Any 4 alnum chars + `-` + digits.
  - Feature: slug string (kebab-case auto-derived from title, override with `--slug`).
  - `bacio doc link` / `unlink` accept either an issue key or a feature slug as the target; they auto-detect issue keys by the `PREFIX-N` shape and treat anything else as a feature slug in the current repo.
- **Comment author.** `--as <name>` is required on every comment. There is no auth — use a sensible identity (e.g. `Claude`, `Geoff`).
- **`--user` is REQUIRED for AI agents.** Every mutation is recorded in an audit log alongside the actor that performed it. The CLI will silently fall back to the OS username if `--user` is omitted, but for agents that produces useless `<your-os-user> did everything` history. **Always pass `--user <your-agent-name>` (e.g. `--user Claude`) on every mutating command.** Treat it as mandatory in any agent-driven invocation, even though the binary tolerates its absence for human users.
- **Database override.** `--db <path>` is a global flag, useful for tests. In production agents, leave it at the default.

### Recommended: drive `bacio` via `--json` (JSON)

Every mutating command accepts `--json` (alias `-j`) — a JSON payload that fully describes the operation. **Prefer this over typed flags when driving `bacio` from an agent.** It's strict (typos surface as `unknown field` errors instead of silent no-ops), it removes the `--description-file` / stdin dance for long text, and the schema is published at runtime so you don't have to memorise field names.

- `--json '<json>'` — inline JSON.
- `--json -` — read JSON from stdin.
- `--json @path/to.json` — read JSON from a file.

`--json` is **mutually exclusive** with positionals and per-field flags (`--title`, `--state`, etc.). Mixing them is rejected with a clear error.

**Discover shapes at runtime, don't guess:**

```bash
bacio schema list                # every command name with --json + one-line description
bacio schema show issue.add      # full JSON Schema (draft 2020-12) for one command
bacio schema all                 # every schema, keyed by command name (one ingest pass)
```

Each schema includes a worked `examples[0]` you can copy and adapt:

```bash
$ bacio schema show issue.add | jq .examples[0]
{
  "title": "Pin tab strip in place",
  "feature_slug": "tui-polish",
  "description": "Body height should clip the tab strip so it doesn't drift on overflow.",
  "state": "todo",
  "tags": ["ui", "tui"]
}

$ bacio schema show issue.add | jq .examples[0] | bacio issue add --user agent-claude --json -
```

**Conventions baked into the JSON path:**

- **Issue keys must be canonical** (`MINI-42`, not `42`). The bare-number shortcut is for humans on the CLI flag path only.
- **Long-text fields are inline strings.** No JSON-side `--description-file`; just put the markdown directly in the `description` / `body` / `content` field.
- **Edit semantics on `*string` fields:** field absent = no change; empty string = clear (where the model allows). Required fields like `title` reject empty strings — omit the field to leave the value alone.
- **Globals stay as flags.** `--user`, `--db`, and `-o text|json` are passed as flags alongside `--json`, not inside the JSON.
- **Strict decoding.** Unknown fields fail the call. If you get `unknown field "..."` errors, run `bacio schema show <command>` to see the exact accepted shape.

### `--dry-run` for safe rehearsals

Every mutating command accepts a global `--dry-run` flag. When set, the command runs everything up to the SQL write — input validation, entity resolution, slug/key derivation, cascade lookups — and emits the projected result without touching the database or the audit log. A `[dry-run] no changes were written` line is written to stderr; stdout has the same shape as a real call's output, so the same parsing code works.

Worth using when:

- You want to rehearse an `--json` payload before committing it, especially after composing it from `bacio schema show`.
- You're about to delete something and want the cascade counts first. `bacio issue rm --dry-run` returns the issue plus how many comments / relations / PR attachments / doc links would be removed alongside it; same shape on `bacio feature rm --dry-run` (issues unlinked, doc links removed), `bacio doc rm --dry-run` (links removed), and `bacio repo rm --dry-run` (issues, comments, features, documents, history — the full blast radius).
- `bacio repo rm <PREFIX>` is the one destructive command that *also* needs an explicit `--confirm <PREFIX>` token. Without it the command exits non-zero with a "STOP — DESTRUCTIVE OPERATION REQUIRES HUMAN APPROVAL" alert and the impact preview. **An agent must show that preview to the user, get an unambiguous confirmation, then re-run with `--confirm <PREFIX>`.** This is intentional friction — `bacio repo rm` cascades through every issue, comment, feature, doc, link, and history row attached to the repo.
- You want to confirm a complicated `bacio issue edit` patch would resolve to the right object (especially for `feature_slug: null` clears).

Notes:

- `bacio issue next --dry-run` is equivalent to `bacio issue peek` — it reports what would be claimed without flipping state.
- `bacio doc export --dry-run` reports the absolute destination path it would write to and the byte count, but doesn't create directories or files.
- Server-time fields (`id`, `created_at`, `updated_at`) come back as zero values in dry-run output; everything else is faithful to the real call.

### Output is lean by default

To keep agent context windows small, list-style commands strip heavy fields by default:

- `bacio issue list -o json` — no `description`. Pass `--with-description` to inline bodies.
- `bacio feature list -o json` — no `description`. Pass `--with-description` to inline bodies.
- `bacio doc list -o json` — already metadata-only (emits `size_bytes`, never `content`).

When you need just the metadata of a single document, pass `--metadata` to `bacio doc show <name>` and the body is skipped. Pair it with `--raw` (mutually exclusive) only if you wanted the body and nothing else.

`bacio issue brief <KEY>` is the one bulk-context call that *does* inline doc bodies on purpose — it exists so a skill can read everything in one shot. Three opt-outs trim it when the full payload is too much:

- `--no-feature-docs` — skip docs linked to the parent feature.
- `--no-comments` — skip the comments section.
- `--no-doc-content` — keep linked-doc metadata (filename, type, source_path, linked_via, description) but drop the bodies. Fetch specific bodies later via `bacio doc show <name>`.

### Input validation contract

Every mutation runs through validators in the store layer, so malformed input fails fast with a clear error rather than being silently normalised or stored as garbage. Rules an agent should know:

- **No control characters** anywhere. Single-line fields (titles, slugs, names, filenames, URLs, tags) reject all C0 controls and DEL (`\x00–\x1F`, `\x7F`). Multi-line fields (descriptions, comment bodies, document content) allow `\t \n \r` but reject the rest.
- **No silent trimming on identifiers.** Leading or trailing whitespace in a `filename`, `slug`, or URL is rejected — if you fat-finger a payload you'll see it instead of having it normalised away.
- **Length caps:** title 200 chars, name/assignee/`--user` 80, slug 60, filename 200, tag 80, PR URL 2 KiB, body fields 1 MiB. Generous for legitimate content, tight enough to fail loud on a runaway paste.
- **Slugs must be kebab-case** matching `^[a-z0-9][a-z0-9-]*$`. Auto-derived slugs (when you omit `slug` on `feature.add`) always satisfy this; explicit slugs in JSON must too.
- **PR URLs** must use `http` or `https` and have a host. `javascript:` and similar exotic schemes are rejected.
- **`--user` is validated once per command.** A `--user` that contains a newline or a control character is rejected at the start of the command before any work happens.
- **Strict JSON decode.** Unknown fields fail (covered by principle #1). Combined with the above, an agent that sends garbage gets a useful error pointing at the bad field rather than a successful write of corrupted data.

## Command reference

Every command supports `-o text|json` and `--db <path>` as global flags. Examples below omit them unless relevant.

### Repos

```
bacio init [--prefix XXXX]              Bind cwd to a prefix (auto-runs on first
                                     use; pass --prefix to override the
                                     derived prefix)
bacio repo list                         List every tracked repo
bacio repo show [PREFIX]                Show a repo (defaults to cwd's repo)
bacio repo rm <PREFIX> --confirm <PREFIX>
                                     ⚠️ DESTRUCTIVE & IRREVERSIBLE. Wipes
                                     the repo plus every issue, comment,
                                     feature, document, link, relation,
                                     PR attachment, TUI setting and
                                     history row attached to it. Without
                                     --confirm <PREFIX> the command
                                     prints the impact preview and exits
                                     non-zero — agents MUST stop and ask
                                     the user before re-running. Run
                                     with --dry-run first to inspect
                                     the cascade.
bacio status                            Read-only probe — never registers.
                                     Inside a registered repo, shows the
                                     repo, DB path, and quick stats (feature
                                     count + issues grouped by state).
                                     Inside an unregistered git tree, reports
                                     `registered: false`. Outside any git
                                     repo, shows global counts instead.
                                     When inside a git repo, the JSON output
                                     may include an `llm_recommendations`
                                     array — plain-English setup fixes bacio
                                     noticed (e.g. `.bacio/` not gitignored).
                                     Read it and action each item.
```

**Example:**
```bash
cd ~/Repos/bacio && bacio init --prefix MINI
bacio repo show
```

### Features

Optional groupings of issues, scoped to the current repo.

```
bacio feature add <title>               Create a feature
  --slug <kebab>                       Override slug (default: derived from title)
  --description <text|->                Inline text or "-" for stdin
  --description-file <path>             Read description from a file

bacio feature list                      List features in the current repo
bacio feature show <slug>               Show a feature with its issues and
                                     linked documents
bacio feature edit <slug>               Patch fields (pass --title and/or --description-file)
  --title <new title>
  --description <text|->
  --description-file <path>
bacio feature rm <slug>                 Delete a feature (issues remain, but lose their feature link)
bacio feature plan <slug>               Print open issues in execution order,
                                     respecting `blocks` dependencies. Issues
                                     with all blockers satisfied appear first;
                                     blocked issues appear after their
                                     blockers, annotated with `blocked_by`.
                                     Open = not done/cancelled.
                                     Cross-feature blockers are surfaced as
                                     `blocked_by` hints but don't gate the
                                     topo position. Errors out on a cycle.
                                     Use `-o json` to drive an agent through
                                     the order one issue at a time.
```

**Example:**
```bash
bacio feature add "Auth rewrite" --slug auth-rewrite \
  --description-file docs/auth-spec.md
```

### Issues

```
bacio issue add <title>                 Create an issue in the current repo
  -f, --feature <slug>                  Attach to a feature
  --description <text|->                Inline or stdin
  --description-file <path>
  --state <state>                       Initial state (default: todo)
  --tag <name>                          Repeatable; attach a tag at creation

bacio issue list                        List issues in the current repo
  --state s1,s2                         Filter by comma-separated states
  -f, --feature <slug>                  Limit to one feature
  --tag <name>                          Repeatable; require this tag (AND semantics)
  --all-repos                           Search every tracked repo

bacio issue show <KEY>                  Show issue + tags + comments + relations
                                     + PRs + linked documents + claimants
                                     (the agent-claim history + derived
                                     `taken` flag)
bacio issue brief <KEY>                 Bulk JSON for skills / LLMs: the issue,
                                     parent feature, deduped linked docs
                                     (with full content inlined), comments,
                                     relations, PRs, and a warnings array.
                                     Always emits JSON. Single read; replaces
                                     the show + jq + per-doc-fetch dance.
  --no-feature-docs                     Skip docs linked to the parent feature
  --no-comments                         Skip the comments section
bacio issue edit <KEY>
  --title <new title>
  --description <text|->
  --description-file <path>
  -f, --feature <slug>                  Move to a feature
  --no-feature                          Detach from any feature
bacio issue state <KEY> <state>         Change state (accepts dashes/spaces)
bacio issue assign <KEY> <name>         Set the assignee (free-form name; pass
                                     an agent identity when a bot picks
                                     up the work)
bacio issue unassign <KEY>              Clear the assignee
bacio issue next --feature <slug>       Atomically claim the next ready issue
                                     in a feature: lowest-numbered todo
                                     issue with all blockers done/cancelled
                                     and no existing assignee. Flips it to
                                     in_progress and stamps the assignee
                                     with --user. Emits
                                     `{"issue": null}` (and exit 0) when
                                     nothing is currently claimable —
                                     callers should poll/retry rather
                                     than treat that as an error.
bacio issue peek --feature <slug>       Read-only counterpart to `next`:
                                     shows what `next` would claim
                                     without mutating state. Same empty
                                     result shape when nothing is ready.
bacio issue rm <KEY>                    Delete an issue (cascades to comments,
                                     relations, PRs, tags, doc links)
```

A bare number like `42` is also accepted for `<KEY>` and is interpreted relative to the current repo's prefix.

**Example:**
```bash
bacio issue add "Login broken on Safari" -f auth-rewrite \
  --description-file /tmp/repro.md
bacio issue list --state todo,in_progress -o json
bacio issue state MINI-42 in-progress
bacio issue show MINI-42

# Bulk context for an LLM/skill: issue + feature + linked docs (with raw
# content) + comments, in one read. Always JSON.
bacio issue brief MINI-42 | tee /tmp/ctx.json
```

`bacio issue brief` returns a single object: `{issue, feature?, relations, pull_requests, documents, comments, claimants, taken, warnings}`. Each entry in `documents` carries `filename`, `type`, `description` (the link's `--why`), `linked_via` (one or both of `"issue"` and `"feature/<slug>"`), `source_path`, and `content`. Docs reachable from both the issue and its parent feature are deduped to a single entry whose `linked_via` lists both paths. If the issue and feature link rows have differing `--why` descriptions, the issue's wins and a string is appended to `warnings`.

`claimants` is the per-issue agent-claim history (open + released, newest first) — each entry carries `session_id`, `agent_name`, `prompt`, `claimed_at`, and `released_at` (absent while open). `taken` is the derived "an agent is actively holding this" flag — `true` iff any claimant is still open. `bacio issue show` carries the same `claimants` / `taken` fields, and so does the `bacio api` `/issues/{key}` and `/issues/{key}/brief` JSON.

Every issue also carries a `waiting_for_claim` boolean (in `bacio issue show -o json`, `bacio issue list -o json`, `bacio issue brief`, and the API issue JSON). It's `true` in the gap between a dispatch being queued against the issue and an agent recording an open claim on it — see the dispatch lifecycle in the Dispatches section. The TUI board and desktop app render a spinner on a waiting issue and refuse a fresh dispatch on it. In text output, `bacio issue show` prints a `Waiting for claim: yes` line only while it's set.

**Driving an agent through a feature in dependency order.** Inspect the topo order with `bacio feature plan <slug>`, then loop on `bacio issue next --feature <slug> --user <agent> -o json`, treating `{"issue": null}` as "retry later". Multiple agents can call `next` in parallel — SQLite serialises the claim. Crashed agents leave a stale `in_progress`/assigned issue; clear with `bacio issue state <KEY> todo` + `bacio issue unassign <KEY>`.

### Comments

```
bacio comment add <KEY>                 Add a comment
  --as <name>                           Required: author name (no auth)
  --body <text|->                       Inline or stdin
  --body-file <path>                    Read body from a file
bacio comment list <KEY>                List comments on an issue
```

**Example:**
```bash
printf 'Repro:\n1. open /login\n2. submit\n3. 500\n' \
  | bacio comment add MINI-42 --as Claude --body -
```

### Relations between issues

Stored one-directionally. `blocked-by` is implicit — it's just the inverse view of `blocks`, surfaced automatically in `bacio issue show`.

```
bacio link <FROM> <type> <TO>           Create a relation
                                     types: blocks, relates-to, duplicate-of
bacio unlink <A> <B>                    Remove every relation between two issues
                                     (regardless of direction)
```

**Example:**
```bash
bacio link MINI-42 blocks MINI-43      # MINI-42 blocks MINI-43
bacio link MINI-44 duplicate-of MINI-42
bacio unlink MINI-42 MINI-43
```

### History (audit log)

Every mutation made via `bacio` is appended to a per-DB audit log: who did it, when, against what, and a short detail string. Reads are not logged. The audit table has no foreign keys, so entries survive deletion of the entities they describe.

**Retention is 60 days.** Older entries are pruned automatically on every `bacio` invocation, so don't expect long-term forensic history. Snapshot the DB if you need to keep something forever.

```
bacio history                           Last 50 mutations in the current repo
  --limit N                            Cap output (default 50; 0 = no limit)
  --offset N                           Skip the first N entries (pagination)
  --oldest-first                       Reverse the default newest-first order
  --user-filter <name>                 Only entries by this actor
  --op <op>                            Exact op match (e.g. issue.state)
  --kind <kind>                        Filter by entity kind: issue, feature,
                                       document, or repo
  --since <duration>                   Look back this far: 30m, 1h, 1d, 2w
  --from <timestamp>                   Inclusive lower bound (mutually
                                       exclusive with --since)
  --to   <timestamp>                   Inclusive upper bound
  --all-repos                          Include every repo
```

`--from` / `--to` accept either local-time stamps (`YYYY-MM-DD`, `YYYY-MM-DD HH:MM`, `YYYY-MM-DD HH:MM:SS`) or RFC 3339 (e.g. `2026-05-03T07:27:14Z`). Bare dates start at 00:00 in the local timezone.

Op naming is dotted: `repo.create`, `feature.{create,update,delete}`, `issue.{create,update,state,assign,claim,delete}`, `comment.add`, `relation.{create,delete}`, `pr.{attach,detach}`, `tag.{add,remove}`, `document.{create,update,rename,delete,link,unlink}` (`bacio doc upsert` records `document.create` or `document.update` depending on whether it created the row). Filtering by op prefix is not currently supported — match exactly, or use `--kind` for an entity-level cut.

**Examples:**
```bash
bacio history --since 1d                                  # last 24h
bacio history --user-filter Claude --op issue.create      # what Claude filed
bacio history --kind document --since 1w                  # all doc activity this week
bacio history --from 2026-05-01 --to 2026-05-03           # absolute range
bacio history --oldest-first --since 1d                   # chronological replay
bacio history --limit 25 --offset 25                      # second page
```

### Documents

A per-repo store of named text documents (markdown specs, design notes, vendor docs, …). Each document has a logical filename (unique within the repo) and a type drawn from a fixed vocabulary. Issues and features can link to documents with an optional `--why` description; re-linking the same pair upserts the description.

**Document types** (canonical underscore form; the parser also accepts dashes/spaces):
`user_docs | project_in_planning | project_in_progress | project_complete | vendor_docs | architecture | designs | testing_plans`. The list is extensible — additional types may appear over time.

```
bacio doc add [filename]                Create a document
  --type <type>                         Required (unless derivable from --from-path)
  --content <text|->                    Body, or '-' for stdin
  --content-file <path>                 Read body from a file (UTF-8 text)
  --from-path <repo-relative-path>      Derive filename (and optionally type
                                         and content) from a path on disk

bacio doc upsert [filename]             Create or update — same flag surface as add.
                                     Use this from skills to skip the
                                     "show, branch on exit code, then add or
                                     edit" shell dance.

bacio doc list [--type <type>]          List documents in the current repo
bacio doc show <filename> [--raw]       Print metadata + content + links
                                     (--raw: content only, ignores --output)
bacio doc edit <filename>
  --type <type>                         Change type
  --content <text|->
  --content-file <path>
bacio doc rename <old> <new>            Rename in place. Links are preserved
  --type <new-type>                      Optionally also change the type
                                          (handy when a plan moves
                                           not-shipped/ → shipped/)
bacio doc export <filename>             Materialise a document onto disk
  --to-path                              Write to the path the doc was last
                                          imported from (--from-path on
                                          add/upsert; errors if none)
  --to <path>                            Write to an explicit repo-relative
                                          path
bacio doc rm <filename>                 Delete a document (and its links)

bacio doc link   <filename> <ISSUE-KEY|feature-slug> [--why <text>]
                                     Upsert a link with optional reason
bacio doc unlink <filename> <ISSUE-KEY|feature-slug>
```

`<ISSUE-KEY|feature-slug>` auto-detects: anything matching `PREFIX-N` is an issue key, otherwise it's a feature slug in the current repo.

`bacio issue show` and `bacio feature show` both surface a "Linked documents:" section listing each document with its type and the per-link `--why` description (e.g. `auth-spec.md (architecture) — Source of truth for the JWT switch`).

**`--from-path` filename derivation:** replaces `/` with `-`, so
`docs/planning/not-shipped/foo-plan.md` → `docs-planning-not-shipped-foo-plan.md`.

**`--from-path` type derivation:** `docs/planning/{not-shipped,in-progress,shipped}/` → `project_in_{planning,progress,complete}`. For any other path, pass `--type` explicitly. Explicit `--type` / `--content-file` always wins over derivation. When `--from-path` is given without `--content`/`--content-file`, the path itself is used as the content file.

**Example:**
```bash
# One-liner add: filename and type both derived, content read from disk.
bacio doc add --from-path docs/planning/not-shipped/auth-plan.md

# Idempotent maintenance from a skill — no probe-then-branch shell dance.
bacio doc upsert --from-path docs/planning/not-shipped/auth-plan.md

# Plan shipped: rename and bump the type in one step. Links survive.
bacio doc rename \
  docs-planning-not-shipped-auth-plan.md \
  docs-planning-shipped-auth-plan.md \
  --type project_complete

# Materialise the canonical version back onto disk (the inverse of
# --from-path; mkdir -p as needed; overwrites if the file exists).
bacio doc export docs-designs-foo.svg --to-path
bacio doc export auth-spec.md --to docs/auth-spec.md  # or to an explicit path

# Manual filename / type still works.
bacio doc add auth-spec.md --type architecture --content-file docs/auth.md
bacio doc link auth-spec.md auth-rewrite --why "Source of truth for the JWT switch"
bacio doc link auth-spec.md MINI-42 --why "Reference for the 500 fix"
bacio doc list --type architecture
bacio doc show auth-spec.md --raw > /tmp/auth.md
```

### Tags

Free-form string labels on issues. Case-sensitive (`WIP` ≠ `wip`), no internal whitespace, no fixed vocabulary — invent tags as you need them. Adding the same tag twice is a no-op.

```
bacio tag add <KEY> <tag> [<tag>...]    Add one or more tags (idempotent)
bacio tag rm  <KEY> <tag> [<tag>...]    Remove tags
```

For filtering or setting at creation, use the `--tag` flag on `bacio issue add` and `bacio issue list` (see above). Multiple `--tag` filters are AND-combined.

**Example:**
```bash
bacio issue add "Login broken" --tag bug --tag backend --tag P0
bacio tag add MINI-42 needs-design
bacio issue list --tag bug --tag P0       # bugs that are also P0
```

### Pull requests

Attach plain HTTPS URLs (typically GitHub PR URLs) to an issue. URLs are stored verbatim — no normalisation, so `…/pull/374` and `…/pull/374/` are distinct.

```
bacio pr attach <KEY> <URL>             Validates http/https + host
bacio pr detach <KEY> <URL>             Exact URL match
bacio pr list <KEY>                     One URL per line (or JSON)
```

**Example:**
```bash
bacio pr attach MINI-42 https://github.com/owner/repo/pull/7
```

### Agent registry

Local-only — never replicated to GitHub via `bacio sync`.

```
bacio agent register                    Register / refresh this session
  --user <name>                         Actor (required for agents)
  --agent <slug>                        Persistent identity (see "Pick your identity")
  --new                                 Assert --agent is fresh; errors on clash
  --session <id>                        Default: $CLAUDE_CODE_SESSION_ID
  --model <id>                          e.g. claude-sonnet-4-6
  --mode <id>                           Permission mode (acceptEdits/...)
  --host <hostname>                     Default: os.Hostname()
  --branch <name>                       Default: current git branch
bacio agent heartbeat                   Bump last_seen_at on a registered session
bacio agent end --reason <r>            Reason: stop|clear|logout|crash|other
                                     (also auto-releases every open claim,
                                     unassigning any issue left unclaimed)
bacio agent claim <ISSUE-KEY>           Record intent + stamp the issue's
                                     assignee with this agent's identity
                                     (does NOT move the issue's state).
                                     Multiple agents may claim the same
                                     issue (pairing/review); last claim
                                     wins the assignee field.
  --prompt <text>                       Instruction/dispatch text this session
                                     is working from — stored on the claim;
                                     a re-claim with a fresher --prompt updates
                                     it in place.
bacio agent release <ISSUE-KEY>         Release this session's claim on an
                                     issue; clears the assignee once the
                                     issue has no open claims left (a
                                     human-set assignee is left alone)
bacio agent dispatch [ISSUE-KEY]        Queue a work item for an agent / session
  --to <agent-slug>                     Target a persistent identity
  --session <id>                        Target one specific session
  --mode <stage>                        Job stage: plan, implement, review,
                                     ship, or fix_review (default: untyped).
                                     The stage's prompt template (editable
                                     via `bacio settings template` or the
                                     desktop Settings panel) is rendered
                                     with the issue id/title to form the
                                     instruction body.
  --message <text>                      Free-form note appended to the body
                                     (must pass --to and/or --session)
bacio agent inbox                       Open dispatches queued for this session
  --session <id>                        Default: $CLAUDE_CODE_SESSION_ID
bacio agent ack <DISPATCH-ID>           Acknowledge a dispatch
  --note <text>                         Optional reply recorded on the dispatch
bacio agent list                        Lean table of sessions in this repo
  --active                              Only sessions that haven't ended
  --all-repos                           Include sessions from every repo
  --since 30m                           Only sessions seen within this window
bacio agent show <session-id>           Session + full claim history
```

### Dispatches — supervisor → agent work queue

A **dispatch** is a unit of work one party queues for an agent: an issue to
look at, plus a free-form instruction. It targets an agent identity slug
(`--to`), a specific session (`--session`), or both. Dispatches are
local-only, like the rest of the registry.

An agent picks dispatches up two ways:

- **Pull** — the `bacio` Claude Code hooks (see "Hook integration" below)
  drain pending dispatches at session start and on every prompt, injecting
  them into context. Nothing to poll.
- **Push** — if the session runs the `bacio channel` MCP server, dispatches
  arrive live as `<channel source="bacio" ...>` events the moment they're
  created. `bacio install-channel --yes` registers the channel in the
  repo's `.mcp.json` and prints the `claude` launch command (channels are
  a research preview — the session opts in with
  `--dangerously-load-development-channels server:bacio`). `bacio agent
  list` shows a `CHANNEL` column (`live` / `-`) and an `MCP` column with
  the binary version the agent's channel reports (with a `!stale` flag
  if it doesn't match the binary running the list command).
  The channel exposes two MCP tools: `reply` (ack a dispatch) and
  `register` (complete the session's registration). The SessionStart
  hook now writes only a minimal stub — `bacio agent list` filters those
  out by default (use `--all` to see them); they're invisible until
  `register` enriches the row. On startup the channel itself queues a
  dispatch with `from="bacio-channel"` asking you to call `register`
  with `{"session_id": "$CLAUDE_CODE_SESSION_ID", "model": "<your model id>",
  "branch": "<your git branch>", "permission_mode": "<your permission
  mode>", "mcp_version": "<serverInfo.version from initialize>"}` —
  only `session_id` is required, but every extra field enriches the
  session row. Pass `mcp_version` from the value the MCP server reported
  at initialize so bacio can detect stale channel processes. Call
  register, then ack the dispatch with `reply`.

Either way, acknowledge each handled dispatch with `bacio agent ack <id>
--note "..."` (or the channel's `reply` tool). Acked/cancelled dispatches
drop out of `bacio agent inbox` and are pruned after 60 days.

**The `waiting_for_claim` lifecycle.** When a dispatch is queued against
a concrete issue, bacio immediately sets that issue's `waiting_for_claim`
flag to `true` — the "a dispatch is out, but no agent has picked it up
yet" signal. It is cleared back to `false` the moment an agent records
an open claim on the issue (`bacio agent claim`), and also if the
dispatch is cancelled. So the normal flow is: dispatch → `waiting_for_claim
= true` → agent claims → `waiting_for_claim = false`, `taken = true`. The
TUI and desktop boards show a spinner (and hide the dispatch action)
while an issue is waiting, so claiming promptly after you pick up a
dispatch is what clears the spinner. Known gap: if an agent session ends
without ever claiming or cancelling, the flag stays set.

### Dispatch prompt templates

When you `bacio agent dispatch --mode <stage>`, the instruction body the
agent sees is rendered from that stage's **prompt template**. Each of the
five stages (`plan`, `implement`, `review`, `ship`, `fix_review`) ships
with a built-in default; override them per-stage — globally, not
per-repo — with `bacio settings template`. The same templates are
editable from the desktop app's Settings panel.

```
bacio settings template list            Lean table: every stage's effective
                                        template + its allowed_states gate
bacio settings template show <stage>    One stage — effective body + built-in
                                        default + the state-gate
bacio settings template set <stage> <body>
                                        Override a stage's template
bacio settings template reset <stage>   Revert a stage to its built-in default

bacio settings template states show <stage>
                                        Show the issue states a stage's prompt
                                        is valid to run from
bacio settings template states set <stage> <state,state,...>
                                        Override a stage's state-gate
bacio settings template states reset <stage>
                                        Revert a stage's state-gate to default
```

`set` and `reset` (and their `states` siblings) are mutations — they
honour `--json`, `--dry-run`, and `bacio schema show
settings.template.set` (schema names `settings.template.set` /
`settings.template.reset` / `settings.template.states.set` /
`settings.template.states.reset`). A template body may interpolate
`{{issue_id}}`, `{{issue_title}}`, and `{{repo_prefix}}` — substituted
with the dispatched issue's context at dispatch time; an unknown
`{{...}}` token is left verbatim. `bacio settings` is local-only (the
`app_settings` store has no remote analogue in v1).

Each stage also has a **state-gate**: the set of issue states its prompt
is valid to run from (built-in defaults — `plan`/`implement` → `todo`,
`review`/`ship`/`fix_review` → `in_review`). The desktop app's per-card
action button only offers a prompt when the card's state is in that
stage's gate; `show`/`list` surface it as `allowed_states`.

```bash
bacio settings template set review --json '{"mode":"review","body":"Review {{issue_id}} ({{issue_title}}) — focus on correctness and tests."}'
bacio settings template reset review
bacio settings template states set review --json '{"mode":"review","states":["in_review","needs_action"]}'
bacio settings template states reset review
```

### Hook integration — automatic registration & supervision

`bacio install-hooks` merges four command hooks into the repo's
`.claude/settings.json` (it prints the plan and prompts first — pass
`--yes` to accept non-interactively):

| Event            | What `bacio hook <event>` does                                       |
| ---------------- | -------------------------------------------------------------------- |
| SessionStart     | mints + records the identity in `.bacio/agents.json` if absent, registers the session, injects assigned issues + claims |
| UserPromptSubmit | heartbeats; flips claimed `needs_action` issues back to `in_progress`; nudges on open claims; drains pending dispatches |
| Stop             | heartbeats; flips claimed `in_progress` issues to `needs_action` (the precise "agent parked" signal) |
| SessionEnd       | ends the session, auto-releasing every open claim                    |

With hooks installed, an agent no longer has to call `bacio agent register`
/ `heartbeat` / `end` by hand — the registry stays in sync automatically,
and the SessionStart hook mints and records the identity in
`.bacio/agents.json` itself (you don't run the "Pick your identity" steps,
and you don't need `--user` — every `bacio` call resolves its own
identity from that file). Every hook also stamps the session's
`claude_pid` and links it to a live `bacio channel` for the same process,
so `bacio agent list` can show the `CHANNEL` column. `bacio hook` and
`bacio channel` are harness-integration shims, like `bacio tui`: they
don't follow the six agent-CLI principles and aren't in `bacio schema`.

**Example agent loop, hooks NOT installed (manual fallback):**
```bash
SLUG="cheerful-otter@claude.$(hostname -s)"
until bacio agent register --user agent-claude --agent "$SLUG" --new 2>/dev/null; do
    SLUG="quiet-falcon@claude.$(hostname -s)"   # …regenerate on clash
done

bacio agent claim MINI-42 --user agent-claude
# ... actual work: bacio issue state, bacio comment add, edits, commits ...
bacio agent release MINI-42 --user agent-claude
bacio agent end --reason stop --user agent-claude
```

If the repo has `bacio install-hooks` set up, the register / heartbeat /
end calls happen automatically — the loop above collapses to just `claim`,
the work, `release`, and `bacio agent ack` for any dispatches that arrived.

The agent registry is reachable over HTTP for nine verbs — `register`,
`heartbeat`, `end`, `claim`, `release`, `list`, `show`, `inbox`, `ack` —
plus the bulk `ListOpenClaims` (used by the desktop Board to derive
`taken`). The CLI's `--remote` / `BACIO_REMOTE` mode drives these
verbs over the same routes as a web frontend would. The holdouts that
remain local-only are `bacio agent dispatch` (HTTP parity is a
follow-up), the channel/hook internals (`EnsureSetupDispatch`,
`DrainDispatches`, `CompleteRegistration`, `CreateSessionStub`, etc.),
and the prompt-template / board-preference settings — those error
clearly in remote mode with a "local-only" message.

## Git-backed sync

`bacio sync` mirrors the local SQLite DB to a checked-in folder of YAML + markdown inside a separate **sync repo**. Multiple machines collaborate by pushing/pulling that sync repo through normal git, and `bacio sync` reconciles it with the local DB — last-writer-wins per record, with already-in-git winning label collisions. Sync is opt-in: a project repo without `.bacio/config.yaml` and a sync remote behaves exactly as before.

**Last-writer-wins.** Issues, features, and documents each carry an `updated_at`. On import, if the remote YAML's `updated_at` is **older** than the local DB row's, bacio preserves the local row (and its tags/PRs/relations/links) instead of silently downgrading it. The skipped record is counted in `ImportResult.skipped` and reported per-record in `ImportResult.skipped_stale` (JSON) plus a `sync.skip_stale_remote` audit entry. The export phase on the same run writes the newer local content back out so the round-trip closes. Comments don't carry `updated_at` and are still subject to remote-wins on body/author drift — keep that in mind if multiple machines edit the same comment between syncs.

The sync repo is its own git repo, marked by an `bacio-sync.yaml` sentinel at its root. Each machine records the remote in a **machine-local** `.bacio/config.yaml` (`sync.remote: <git URL>`) — that file is gitignored, NOT shared via git, so the remote only ever enters bacio through a trusted `--remote` flag. The same sync repo can hold many projects — one folder per prefix under `repos/`.

Alongside the sentinel, every export refreshes an `index.yaml` at the sync-repo root: a machine-readable table-of-contents listing every project repo present (`prefix`, `uuid`, `name`, `remote`, plus `issues`/`features`/`documents`/`comments` counts). The per-repo `repos/<PREFIX>/repo.yaml` files remain authoritative; `index.yaml` is regenerated from them and is byte-stable across no-op runs so it doesn't churn commits. It's safe to delete — the next export rewrites it.

Inside a sync repo, the read-only list commands take a YAML-on-disk branch instead of hitting the local DB:

- `bacio repo list` reads `index.yaml` and prints the prefixes/names/remotes recorded there.
- `bacio issue list --repo <PREFIX>` (or `--all-repos`) walks `repos/<PREFIX>/issues/*/issue.yaml`. The usual `--state`, `--feature`, `--tag`, `--with-description` filters apply. Without `--repo`/`--all-repos` inside a sync repo, the command errors with a hint listing available prefixes.
- `bacio doc list --repo <PREFIX>` (or `--all-repos`) walks `repos/<PREFIX>/docs/*/doc.yaml`; `--type` filters as in project-repo mode.

Mutating commands (`bacio issue create`, `bacio doc add`, etc.) still refuse inside a sync repo with the existing `errSyncRepoMode` message; the read-only relaxation is scoped to those three list commands.

```
bacio sync init <local-path> [--remote URL]   Connect the current project repo
                                           to a sync repo at <local-path> and
                                           perform an initial sync. <local-path>
                                           may be empty/missing (fresh
                                           bootstrap), an existing
                                           'git init'/cloned-empty-bare folder
                                           (sentinel + .gitattributes written,
                                           then export+commit+push), or an
                                           already-populated bacio sync repo
                                           (attach mode: pull, import,
                                           re-export, commit, push). If the
                                           target already has an origin
                                           configured, --remote is optional —
                                           the URL is auto-detected; supplying
                                           a mismatching --remote errors.
                                           Attach-mode import is additive:
                                           local-only records (uuids absent
                                           from the sync repo's working tree)
                                           are preserved and exported in the
                                           same run. Only steady-state bacio
                                           sync propagates deletes.

bacio sync clone --remote <url> [<local-path>] [--allow-renumber] [--dry-run]
                                           Join an existing sync repo.
                                           --remote is REQUIRED: the sync
                                           URL is not read from any file
                                           (.bacio/config.yaml is machine-
                                           local), so pass it explicitly —
                                           ask the project owner, or read
                                           `origin` from the sync repo.
                                           Clones the remote, runs the first
                                           import, and writes this machine's
                                           local .bacio/config.yaml. If local
                                           DB has rows for the project's
                                           prefix that would collide, refuses
                                           unless --allow-renumber is set;
                                           --dry-run prints the preview
                                           without touching DB or disk.
                                           Import is additive — clone never
                                           destroys local-only records; only
                                           steady-state bacio sync propagates
                                           deletes.

bacio sync                                    Steady state: pull → import →
                                           export → commit → push. Run
                                           from inside a project repo. On
                                           non-fast-forward push it pulls,
                                           re-imports/re-exports, and
                                           retries once.
                                           Flags: --no-import, --no-export,
                                           --no-push for fine-grained
                                           skipping; --dry-run rolls back
                                           DB writes and skips commit/push.

bacio sync verify                             Diagnostic: walks the sync repo
                                           and reports parse failures,
                                           uuid collisions, dangling
                                           cross-references, case-folding
                                           folder collisions, redirect-
                                           chain cycles, orphan comment
                                           files, and body-hash drift.
                                           Errors → exit non-zero;
                                           warnings (dangling refs,
                                           hash drift) print but don't
                                           change exit status.
                                           Run from inside the sync repo.

bacio sync inspect <prefix>                   Read-only browse. Default is a
bacio sync inspect <prefix> --issue MINI-7    per-prefix summary (counts +
bacio sync inspect <prefix> --feature slug    recent renumbers). With one of
bacio sync inspect <prefix> --doc filename    the flags, prints the parsed
                                           record and its body. Run from
                                           inside the sync repo.
```

`bacio sync verify` and `bacio sync inspect` are the only sync commands that **must run inside the sync repo**. Everything else (`init`, `clone`, the bare `bacio sync`) runs from a project repo.

**On collisions.** If two clients separately create `MINI-7`, the one whose folder is already in git keeps the label; the other's local row gets renumbered to the next free number (or, for features/documents, suffixed: `auth-rewrite-2`, `auth-overview-2.md`). The audit log records `sync.renumber` / `sync.rename`; `redirects.yaml` in the sync repo records the old → new move so `bacio issue show MINI-7` still resolves via the redirect chain. External references (commit messages, PRs, free-text mentions inside descriptions) aren't rewritten — humans decide what to do with them.

**Identity.** Every record JSON includes a `uuid` field — an immutable UUIDv7 assigned at create time. Sync matches records by `uuid`, never by label, so renumbers and renames never lose history. Use `key`/`slug`/`filename` for human-friendly addressing in CLI calls; `uuid` is informational unless you're debugging the sync layer.

**Mode switch.** Inside a sync repo, bacio refuses to auto-register the directory as a tracked project (the `bacio-sync.yaml` sentinel switches bacio into sync-repo mode). Tracking commands (`bacio issue add`, `bacio feature edit`, …) error out with a "this is an bacio sync repo" message, pointing the user back to a real project working tree.

**Sync is local-only.** All sync commands error in remote mode (`--remote` / `BACIO_REMOTE`); the server is the source of truth there.

## HTTP API

`bacio api` exposes every CLI mutation and read over HTTP, backed by the same SQLite database, JSON shapes, validators, and audit log. **The CLI conventions above all apply** — discover schemas, compose JSON, dry-run, then commit. The only differences are HTTP plumbing.

```bash
bacio api                                # bind 127.0.0.1:5320, no auth
bacio api --addr 127.0.0.1:7777 --token T   # require Authorization: Bearer T
BACIO_API_TOKEN=T bacio api                 # token via env
```

### Discovery

- `GET /schema/list` — every command name + one-line summary (mirrors `bacio schema list`).
- `GET /schema/{name}` — full JSON Schema for one command with `examples[0]` (mirrors `bacio schema show`).
- `GET /schema` — every schema in one object.

Schemas describe payload shapes, not routes. Routes follow REST conventions under `/repos/{prefix}/...` — list/create on the collection, show/patch/delete on the item, plus sub-resources for state changes (`/state`, `/assignee`), batch ops (`/tags`, `/pull-requests`), graph edges (`/relations`, `/links`), bulk reads (`/brief`, `/plan`), and claim/peek (`/next`). Use `GET /schema/list` to enumerate every operation. Issue keys in URLs and bodies must be canonical (`MINI-42`); the bare-number CLI shortcut isn't accepted.

A few non-obvious mappings:

- **Tags:** `POST/DELETE /repos/{prefix}/issues/{key}/tags` with `{"tags":[...]}` (batch, not per-tag URLs).
- **Relations:** `POST /repos/{prefix}/relations` with `{"from","type","to"}`; `DELETE` with `{"a","b"}` (bidirectional).
- **PR detach:** `DELETE /repos/{prefix}/issues/{key}/pull-requests` with `{"url"}` or `?url=`.
- **Documents:** link/unlink at `POST/DELETE /documents/{filename}/links`; rename at `POST /documents/{filename}/rename`.
- **State / assignee:** `PUT /issues/{key}/state`, `PUT/DELETE /issues/{key}/assignee`.

### Headers, query params, dry-run

- **Auth.** When `--token` / `BACIO_API_TOKEN` is set, every request except `GET /healthz` needs `Authorization: Bearer <token>` (constant-time compare). The loopback default with no token is the trust boundary.
- **Actor.** `X-Actor: <agent-name>` stamps the audit log. Absent → falls back to the literal `"api"` (NOT the OS user). **Required** on `POST /repos/{prefix}/features/{slug}/next` — claiming work demands a real assignee.
- **Dry-run.** `?dry_run=true` (or `=1`) or `X-Dry-Run: 1`. Response status matches a real call, body is the projected entity, response carries `X-Dry-Run: applied`. No row written, no audit row recorded. Server-time fields (`id`, `created_at`, `updated_at`) come back zero. `DELETE` returns a `*DeletePreview` with cascade counts.
- **Lean lists.** Issue/feature lists drop `description`; doc list drops `content`. Inflate with `?with_description=true` or `?with_content=true`. For a single doc, `?with_content=false` strips the body the other way.
- **Brief opt-outs.** `?no_feature_docs=1`, `?no_comments=1`, `?no_doc_content=1` on `GET /issues/{key}/brief`.
- **History filters.** `?limit`, `?offset`, `?op`, `?kind`, `?actor` (alias `?user_filter`), `?since`, `?from`, `?to`, `?oldest_first` on `GET /history` and `GET /repos/{prefix}/history`. `since` and `from` are mutually exclusive.

### Errors

```json
{ "error": "title is required", "code": "invalid_input", "details": {"field": "title"} }
```

| Status | Code | When |
|---|---|---|
| 400 | `invalid_input` | malformed JSON, unknown fields, validator failure, missing required `X-Actor` on claim |
| 401 | `unauthorized` | token configured and bearer is missing/wrong |
| 404 | `not_found` | path resolves no such entity |
| 409 | `conflict` | duplicate slug/prefix/PR URL/document filename |
| 413 | `payload_too_large` | body > 4 MiB |
| 500 | `internal` | server-side panic (caught by recovery middleware) |

### API-only quirks

- **`POST /repos`** is the equivalent of `bacio init`, but the server can't see your CWD — supply `{"name":"...", "path":"..."}` (plus optional `prefix`) explicitly.
- **`GET /repos/{prefix}/documents/{filename}/download`** is the only non-JSON endpoint. Streams the body as `text/markdown` with `Content-Disposition: attachment`. No audit row, no dry-run, no `with_content`. The API never reads or writes the server filesystem, so callers materialise on disk by piping the response (`curl -O`).
- **CLI verbs with no API equivalent** (touch the local filesystem or terminal): `bacio init` (use `POST /repos`), `bacio install-skill`, `bacio install-hooks`, `bacio install-channel`, `bacio doc add --from-path` / `--content-file` (inline `content` in the body), `bacio doc export` (use `/download`), `bacio tui`, `bacio hook *`, `bacio channel`. Plus the local-only agent verbs `bacio agent dispatch` and the prompt-template / board-preference settings.
- **Agent registry endpoints.** The nine register/heartbeat/end/claim/release/list/show/inbox/ack verbs reach the server under `/repos/{prefix}/agents/sessions` (register, list-in-repo, list-open-claims-in-repo) and `/agents/sessions/{session_id}/...` (heartbeat/end/claim/release/inbox + show), plus `/agents/dispatches/{id}/ack`. Cross-repo variants of the two lists live at `/agents/sessions` and `/agents/claims/open`. Stub sessions (`registered_at` NULL) are hidden by default on the list endpoints — pass `?all=true` to include them.

For the full design rationale, threat model, and what the API deliberately doesn't do (NDJSON, per-user auth, CORS, cursor pagination, …), see `docs/rest-api-design.md`.

## CLI client mode (`--remote` / `BACIO_REMOTE`)

`bacio` can drive a remote `bacio api` server instead of the local DB. Set `--remote http://host:5320` (or `BACIO_REMOTE=...`) and, if the server enforces auth, `--token` / `BACIO_API_TOKEN`. Every read and mutating verb behaves identically — same flags, same JSON output, same `--dry-run`, same `--user`. The client translates each verb into the matching HTTP route; audit rows are written by the server.

```bash
BACIO_REMOTE=http://team-bacio:5320 BACIO_API_TOKEN=$T bacio issue list -o json
bacio --remote http://team-bacio:5320 issue add "Login broken" --feature auth
```

Verbs that touch the local filesystem or terminal error clearly in remote mode and stay local-direct: `bacio init`, `bacio install-skill`, `bacio install-hooks`, `bacio install-channel`, `bacio doc add --from-path` / `--content-file` (use `--content` inline instead), `bacio doc export` (use `bacio doc download <filename>` — writes to stdout or `--to <path>`), `bacio tui`, `bacio schema *`, `bacio status`, `bacio hook *`, `bacio channel`. The agent verbs `register / heartbeat / end / claim / release / list / show / inbox / ack` now work in remote mode (BACI-34); only `bacio agent dispatch` and the prompt-template / board-preference settings remain local-only.

## Gotchas

- **Never run `bacio` outside a git repo** when a command needs the current repo — it hard-errors with "not inside a git repository". `cd` first.
- **Comment author is required.** On the JSON path the field is `author`; on the flag path it's `--as <name>`. Forgetting it is the most common mistake.
- **`--user` is required for agents.** It controls the actor field in the audit log; without it every action looks like the OS user. The flag is permissive (no rejection if omitted) so this is on you to pass consistently.
- **Long text in JSON is just a string.** `description`, `body`, `content` etc. take inline strings — no `\n` translation magic, JSON's own `\n` escapes work as expected. The flag path's `--description-file` is unnecessary here.
- **Issue keys in JSON must be canonical** (`MINI-42`). The bare-number shortcut (`42`) is for humans on the flag path; agents driving JSON should always pass the prefix.
- **Mixing `--json` with positionals/flags is rejected.** Choose one mode per call.
- **State values** accept `in-progress`, `in progress`, or `in_progress` — but parsing is case-sensitive on the lowercase form.
- **Auto-created prefix can collide.** If two repos share a basename, `bacio init` allocates `XXX2`, `XXX3`, etc. Use `bacio repo list` to confirm what was assigned.
- **Issue numbers never repeat.** Deleting `MINI-3` does not free up the number — the next issue is still `MINI-4`.
- **JSON output is the contract.** When parsing programmatically, always pass `-o json`. Text output is for humans and may shift.

## Installation

If unsure whether `bacio` is installed for the user, run `bacio --help`. The currently installed version is shown by `bacio --version` — useful when reporting bugs or confirming a feature is available.

If `bacio` is missing, install it one of these ways (prefer whichever fits the user's environment):

```bash
# Prebuilt binary via Homebrew (macOS / Linux):
brew tap mrgeoffrich/bacio && brew install bacio

# Pure-Go install (no CGO):
go install github.com/mrgeoffrich/bacio/cmd/bacio@latest

# From a bacio checkout:
go build -o ~/.local/bin/bacio ./cmd/bacio
```

The binary is self-contained (pure-Go SQLite, no CGO).

To install this skill into another repo so its agents can find it via Claude Code's project-skill auto-discovery, run from anywhere inside that repo:

```bash
bacio install-skill
```

It walks up to the git root and writes `.claude/skills/bacio/SKILL.md`, creating the directory if needed. The bundled SKILL.md content is the version embedded in the build of `bacio` you're running, so re-run after upgrading `bacio` to pull doc updates.

To wire up automatic session registration and dispatch delivery, also run:

```bash
bacio install-hooks --yes
```

It merges the four `bacio hook` command hooks into `.claude/settings.json`
(non-destructively — existing hooks are preserved). It prints the planned
changes and asks for confirmation first; pass `--yes` (`-y`) to accept
automatically, which is required when running non-interactively. See
"Hook integration" above for what each hook does.

For real-time (push) dispatch delivery, also register the channel:

```bash
bacio install-channel --yes
```

It merges a `bacio` entry into the repo's `.mcp.json` and prints the
`claude --dangerously-load-development-channels server:bacio` command to
launch with — same confirmation + `--yes` behaviour as `install-hooks`.
