---
title: TUI — History tab
description: A git-blame-style view of every mutation, with actor, op, target, and timestamp.
---

# History tab

The History tab (`4`) shows the audit log — every mutation bacio recorded — newest first. One row per mutation, with timestamp, actor, op, target, and a short details blob.

```
2026-05-12 14:32 AEST  agent-claude  issue.add       MINI-12          "Pin tab strip" (feature=tui-polish)
2026-05-12 14:28 AEST  Geoff         issue.state     MINI-3           in_progress → in_review
2026-05-12 14:14 AEST  agent-claude  comment.add     MINI-7           tried clearing cookie, didn't help
2026-05-12 12:01 AEST  Geoff         feature.add     auth-rewrite     "Auth rewrite"
```

## Bindings

| Key | Action |
|---|---|
| `j` / `k` | Scroll one row. |
| `g` / `G` | Jump to top / bottom. |
| `r` | Reload from the database. |
| `q` | Quit. |

There's no overlay — the history view is read-only and flat. For richer filtering (by actor, op, target, since), use [`bacio history`](/reference/cli/history) on the CLI.

## What you'll see

Common op names:

| Op | What |
|---|---|
| `repo.create` | A new repo bound (explicit `bacio init` or auto-create). |
| `feature.add` / `.edit` / `.rm` | Feature CRUD. |
| `issue.add` / `.edit` / `.state` / `.assign` / `.unassign` / `.rm` | Issue lifecycle. |
| `comment.add` | A new comment. |
| `link.create` / `.remove` | Relations between issues. |
| `pr.attach` / `.detach` | Pull request URLs attached / removed. |
| `tag.add` / `.rm` | Tag changes. |
| `doc.add` / `.upsert` / `.edit` / `.rename` / `.rm` / `.link` / `.unlink` | Document lifecycle. |
| `sync.renumber` / `.rename` | Sync resolved a collision. |

## Retention

History is **pruned to 60 days** on every DB open. If you need longer-lived records, enable [git-backed sync](/guides/sync-across-machines) — the audit log goes with the sync repo and lives forever in git.

## Source of truth

This page mirrors `internal/tui/history.go`. If the footer disagrees, the footer wins.
