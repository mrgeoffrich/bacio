---
title: TUI — History tab
description: A git-blame-style view of every mutation, with actor, op, target, and timestamp.
---

# History tab

The History tab (`4`) shows the audit log — every mutation bacio recorded — newest first. One row per mutation, with timestamp, actor, op, target, and a short details blob.

```
2026-05-12 14:32 AEST  agent-claude  issue.create    MINI-12          "Pin tab strip" (feature=tui-polish)
2026-05-12 14:28 AEST  Geoff         issue.state     MINI-3           in_progress → in_review
2026-05-12 14:14 AEST  agent-claude  comment.add     MINI-7           tried clearing cookie, didn't help
2026-05-12 12:01 AEST  Geoff         feature.create  auth-rewrite     "Auth rewrite"
```

## Bindings

| Key | Action |
|---|---|
| `j` / `k` (or `down` / `up`) | Scroll one row. |
| `pgdown` / `space` | Jump down 10 rows. |
| `pgup` | Jump up 10 rows. |
| `g` / `home` | Jump to the top. |
| `G` / `end` | Jump to the bottom. |
| `r` | Reload from the database. |
| `q` | Quit. |

There's no overlay — the history view is read-only and flat. For richer filtering (by actor, op, target, since), use [`bacio history`](/reference/cli/history) on the CLI.

## What you'll see

Common op names (canonical `<entity>.<verb>` form — these are CRUD-flavoured verbs, not the cobra-subcommand names):

| Op | What |
|---|---|
| `repo.create` / `.delete` / `.upgrade_phantom` | A new repo bound, removed, or promoted from sync-only to having a local working tree. |
| `feature.create` / `.update` / `.delete` | Feature CRUD. |
| `issue.create` / `.update` / `.state` / `.assign` / `.claim` / `.delete` | Issue lifecycle. `issue.unassign` reuses `issue.assign` with an empty assignee. |
| `comment.add` | A new comment. |
| `relation.create` / `.delete` | Typed links between issues (`blocks`, `relates_to`, `duplicate_of`). |
| `pr.attach` / `.detach` | Pull request URLs attached / removed. |
| `tag.add` / `.remove` | Tag changes. |
| `document.create` / `.update` / `.rename` / `.delete` / `.link` / `.unlink` | Document lifecycle. |
| `sync.run` / `.init` / `.clone` / `.import` / `.renumber` / `.rename` / `.delete` | Sync activity. |

## Retention

History is **pruned to 60 days** on every DB open. If you need longer-lived records, enable [git-backed sync](/guides/sync-across-machines) — the audit log goes with the sync repo and lives forever in git.

## Source of truth

This page mirrors `internal/tui/history.go`. If the footer disagrees, the footer wins.
