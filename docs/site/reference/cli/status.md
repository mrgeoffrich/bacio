---
title: bacio status
description: One-screen summary of the current repo — prefix, DB path, feature/issue counts by state, and the next issue key.
---

# `bacio status`

A quick-look read of the current repo. Good for *"where am I?"* moments in the terminal without opening the TUI. `bacio status` is strictly read-only — it never writes to the database, so agents can use it as a safe probe before doing work. If the current git tree hasn't been bound yet, status reports that distinctly; use [`bacio init`](/reference/cli/init) (or any mutating command — they auto-register on first use) to register the repo.

```bash
bacio status
```

## Inside a tracked repo

```
Repo:    MINI  (bacio)
Path:    /Users/you/code/bacio
Remote:  git@github.com:mrgeoffrich/bacio.git
DB:      /Users/you/.bacio/db.sqlite

Features: 3
Issues:   12
  todo:         4
  in_progress:  3
  needs_action: 1
  in_review:    1
  done:         3
Next:    MINI-13
```

## Inside an unregistered git tree

When you run `bacio status` in a git working tree that hasn't been bound to a prefix yet, status reports the unregistered state without writing anything:

```
Path:    /Users/you/code/fresh-project
Repo:    (unregistered — run `bacio init` to bind a prefix)
DB:      /Users/you/.bacio/db.sqlite
```

Run [`bacio init`](/reference/cli/init) to bind a prefix explicitly, or just call any mutating command (e.g. `bacio issue add`) which auto-registers as a side effect.

## Outside any git repo

```
DB:      /Users/you/.bacio/db.sqlite
Repos:   5
Issues:  47 (across all repos)

Not inside a git repo — cd into one and re-run.
```

The global counts are useful as a *"do I have any bacio data at all?"* read.

## JSON output

```bash
bacio status -o json
```

Returns a `statusReport` shape. Inside a registered git tree:

```json
{
  "db_path": "/Users/you/.bacio/db.sqlite",
  "in_repo": true,
  "registered": true,
  "path": "/Users/you/code/bacio",
  "repo": {
    "id": 1,
    "uuid": "0190a44e-...-uuid",
    "prefix": "MINI",
    "name": "bacio",
    "path": "/Users/you/code/bacio",
    "remote_url": "git@github.com:mrgeoffrich/bacio.git",
    "next_issue_number": 13,
    "created_at": "2026-04-01T08:12:31Z",
    "updated_at": "2026-05-12T14:32:01Z"
  },
  "stats": {
    "features": 3,
    "issues": 12,
    "issues_by_state": { "todo": 4, "in_progress": 3, "needs_action": 1, "in_review": 1, "done": 3 },
    "next_issue_key": "MINI-13"
  }
}
```

Inside an unregistered git tree the `repo` block and the per-repo stats are absent:

```json
{
  "db_path": "/Users/you/.bacio/db.sqlite",
  "in_repo": true,
  "registered": false,
  "path": "/Users/you/code/fresh-project",
  "stats": {}
}
```

`repo` is the full `model.Repo` shape (`id`, `uuid`, `prefix`, `name`, `path`, `remote_url`, `next_issue_number`, `created_at`, `updated_at`); `remote_url` is omitted when there's no remote configured. `registered` is always present when `in_repo` is true and tells callers whether a `repos` row exists for the working tree. `path` is the git working-tree root and is populated whenever `in_repo` is true. In the outside-a-repo branch, `repo` and `path` are absent and `stats` carries `tracked_repos` and `total_issues` instead of the per-repo fields.

## See also

- **[`bacio repo show`](/reference/cli/repo)** — same repo metadata without the stats.
- **[`bacio history`](/reference/cli/history)** — recent activity.
- **[Configuration](/reference/config)** — where the DB path comes from.
