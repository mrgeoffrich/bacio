---
title: bacio issue
description: Manage issues — the main verb you'll use day-to-day, directly or through an agent.
---

# `bacio issue`

The most-used command group. Every issue has a canonical key (e.g. `MINI-42`); humans on the flag path may use the bare number, but JSON payloads must use the full canonical key.

## Subcommands

| Subcommand | What it does |
|---|---|
| `bacio issue add <title>` | Create an issue. Initial state defaults to `todo`. Accepts `--json`. |
| `bacio issue list` | List issues with filters. Lean by default. |
| `bacio issue show <KEY>` | Show one issue with comments, relations, PRs, and linked documents. |
| `bacio issue brief <KEY>` | Bulk-context JSON: issue + parent feature + linked-doc bodies + comments + relations + PRs, in one read. Designed for agents. Always JSON. |
| `bacio issue edit <KEY>` | Patch title, description, or feature link. |
| `bacio issue state <KEY> <state>` | Change state. Accepts dashes/spaces (`in-progress`, `in progress`, `in_progress`). |
| `bacio issue assign <KEY> <name>` | Set the assignee. Free-form name; pass an agent identity when a bot picks up the work. |
| `bacio issue unassign <KEY>` | Clear the assignee. |
| `bacio issue next --feature <slug>` | Atomically claim the next ready issue in a feature (see below). |
| `bacio issue peek --feature <slug>` | Read-only counterpart to `next` — shows what would be claimed. |
| `bacio issue rm <KEY>` | Delete an issue. Cascades to comments, relations, PRs, tags, doc links. Use `--dry-run` first. |

A bare number like `42` is accepted for `<KEY>` on the CLI flag path; it resolves against the current repo's prefix. JSON payloads must use the canonical `PREFIX-N` form.

## States

`todo` (default) · `in_progress` · `needs_action` · `in_review` · `done` · `cancelled`

`needs_action` is the LLM-flow column: an agent in the middle of a `--feature` loop flips an issue from `in_progress` to `needs_action` when it's blocked on user input. The assignee stays put; the column signals that the next move is the human's, not the agent's. Move it back to `in_progress` (or onward to `in_review`) once you've answered.

The state parser tolerates dashes or spaces, so `in-progress`, `in progress`, and `in_progress` all work. The lowercase form is required.

## Filter `bacio issue list`

| Flag | What it does |
|---|---|
| `--state s1,s2` | Comma-separated states. |
| `--feature <slug>` (`-f`) | Limit to one feature. |
| `--tag <name>` | Repeatable. Multiple tags AND-combine. |
| `--all-repos` | Search every tracked repo. |
| `--with-description` | Inflate the `description` field (lean by default). |

## Worked examples

```bash
# Create with tags, attached to a feature, with body from a file
bacio issue add "Login broken on Safari" \
  -f auth-rewrite \
  --description-file /tmp/repro.md \
  --tag bug --tag P0

# List
bacio issue list --state todo,in_progress -o json
bacio issue list --tag bug --tag P0           # AND: bugs that are also P0

# Move state
bacio issue state MINI-42 in-progress

# Detail
bacio issue show MINI-42

# Bulk context for an agent — single read, full inlined payload
bacio issue brief MINI-42 | tee /tmp/ctx.json

# Delete (rehearse first)
bacio issue rm MINI-42 --dry-run
bacio issue rm MINI-42
```

`bacio issue brief` returns `{issue, feature?, relations, pull_requests, documents, comments, warnings}`. Each `documents` entry carries `filename`, `type`, `description` (the link's `--why`), `linked_via` (one or both of `"issue"` and `"feature/<slug>"`), `source_path`, and `content`. Docs reachable from both the issue and its parent feature are deduped to a single entry whose `linked_via` lists both paths.

Projection flags trim the brief when the full payload is too much:

- `--no-feature-docs` — skip docs linked to the parent feature.
- `--no-comments` — skip the comments section.
- `--no-doc-content` — keep linked-doc metadata but drop the bodies.

## `next` / `peek` — the atomic claim pattern

`bacio issue next --feature <slug>` claims the next ready issue: the lowest-numbered `todo` issue with **all blockers in a terminal state** (`done`, `cancelled`) and **no existing assignee**. It flips the issue to `in_progress` and stamps the assignee from `--user`.

```bash
bacio issue next --feature auth-rewrite --user agent-claude -o json
```

When nothing is currently claimable it emits `{"issue": null}` and **exits 0** — callers poll / retry rather than treating that as an error.

Multiple agents calling `next` in parallel is safe: SQLite serialises the claim. Crashed agents leave a stale `in_progress` / assigned issue; clear with:

```bash
bacio issue state MINI-3 todo
bacio issue unassign MINI-3
```

`bacio issue peek --feature <slug>` is the read-only counterpart — same algorithm, no state change, same empty-result shape.

## See also

- **[`bacio feature plan`](/reference/cli/feature)** — print open issues in execution order, respecting `blocks`.
- **[`bacio link`](/reference/cli/link)** — wire up `blocks` / `relates-to` / `duplicate-of`.
- **[How agents drive bacio](/concepts/how-agents-drive-bacio)** — the JSON + `--dry-run` + `--user` flow.
