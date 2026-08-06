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
| `bacio issue state <KEY> <state>` | Change state. Accepts dashes/spaces (`in-review`, `in review`, `in_review`). |
| `bacio issue assign <KEY> <name>` | Set the assignee. Free-form name; pass an agent identity when a bot picks up the work. |
| `bacio issue unassign <KEY>` | Clear the assignee. |
| `bacio issue next --feature <slug>` | Atomically claim the next ready issue in a feature (see below). |
| `bacio issue peek --feature <slug>` | Read-only counterpart to `next` — shows what would be claimed. |
| `bacio issue rm <KEY>` | Delete an issue. Cascades to comments, relations, PRs, tags, doc links. Use `--dry-run` first. |

A bare number like `42` is accepted for `<KEY>` on the CLI flag path; it resolves against the current repo's prefix. JSON payloads must use the canonical `PREFIX-N` form.

## States

`todo` (default) · `in_review` · `done` · `cancelled`, plus the two Agentic Pipeline columns `in_pipeline` · `to_be_shipped`.

The state parser tolerates dashes or spaces, so `in-review`, `in review`, and `in_review` all work. The lowercase form is required.

::: tip State is not the Kanban lane
An issue's **state** is the Agentic Pipeline axis. Its **lane** is your own board, set with [`bacio kanban`](/reference/cli/kanban), and the two never move each other. See [Kanban and the Agentic Pipeline](/concepts/kanban-and-pipeline).
:::

## Filter `bacio issue list`

| Flag | What it does |
|---|---|
| `--state s1,s2` | Comma-separated states. |
| `--feature <slug>` (`-f`) | Limit to one feature. |
| `--tag <name>` | Repeatable. Multiple tags AND-combine. |
| `--repo <PREFIX>` | The global project selector — operate on this prefix instead of resolving from the current working tree. **Required for a [workspace](/concepts/workspaces)** and inside a sync repo, where `--all-repos` is the alternative. Falls back to `$BACIO_REPO`. |
| `--all-repos` | Search every tracked repo. Inside a sync repo, walks every prefix recorded in `index.yaml`. |
| `--with-description` | Inflate the `description` field (lean by default). |

Inside a [bacio sync repo](/guides/sync-across-machines) the command takes a YAML-on-disk branch — issues are read from `repos/<PREFIX>/issues/*/issue.yaml` rather than the local SQLite store. The filter flags above all apply identically. See [`bacio sync`](/reference/cli/sync) for the full mode-switch behaviour.

## Worked examples

```bash
# Create with tags, attached to a feature, with body from a file
bacio issue add "Login broken on Safari" \
  -f auth-rewrite \
  --description-file /tmp/repro.md \
  --tag bug --tag P0

# List
bacio issue list --state todo,in_review -o json
bacio issue list --tag bug --tag P0           # AND: bugs that are also P0

# Move state
bacio issue state MINI-42 in-review

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

`bacio issue next --feature <slug>` claims the next ready issue: the lowest-numbered `todo` issue with **all blockers in a terminal state** (`done`, `cancelled`) and **no existing assignee**. It stamps the assignee with the calling agent's identity (resolved via `.bacio/agents.json`). The claim is a focus marker — since BACI-300 it no longer changes the issue's state.

```bash
bacio issue next --feature auth-rewrite -o json
```

When nothing is currently claimable it emits `{"issue": null}` and **exits 0** — callers poll / retry rather than treating that as an error.

Multiple agents calling `next` in parallel is safe: SQLite serialises the claim. A crashed agent leaves a stale assignee; clear it with:

```bash
bacio issue unassign MINI-3
```

`bacio issue peek --feature <slug>` is the read-only counterpart — same algorithm, no state change, same empty-result shape.

## See also

- **[`bacio kanban`](/reference/cli/kanban)** — put an issue on your own board, orthogonal to its state.
- **[`bacio feature plan`](/reference/cli/feature)** — print open issues in execution order, respecting `blocks`.
- **[`bacio link`](/reference/cli/link)** — wire up `blocks` / `relates-to` / `duplicate-of`.
- **[How agents drive bacio](/concepts/how-agents-drive-bacio)** — the JSON + `--dry-run` flow and how agent identity is resolved.
