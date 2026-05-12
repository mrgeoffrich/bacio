---
title: bacio status
description: One-screen summary of the current repo — prefix, DB path, feature/issue counts by state, and the next issue key.
---

# `bacio status`

A quick-look read of the current repo. Good for *"where am I?"* moments in the terminal without opening the TUI. Auto-registers the current repo on first use — same behaviour as every other mutating command.

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

If the repo was registered as a side effect of this call (i.e. you ran `bacio status` before `bacio init` in a fresh git tree), the output is prefixed with `Just registered this git repo as MINI.`

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

Returns a `statusReport` shape:

```json
{
  "db_path": "/Users/you/.bacio/db.sqlite",
  "in_repo": true,
  "repo": { "prefix": "MINI", "name": "bacio", "path": "...", "remote_url": "...", "next_issue_number": 13, ... },
  "just_registered": false,
  "stats": {
    "features": 3,
    "issues": 12,
    "issues_by_state": { "todo": 4, "in_progress": 3, "needs_action": 1, "in_review": 1, "done": 3 },
    "next_issue_key": "MINI-13"
  }
}
```

In the outside-a-repo branch, `repo` is absent and `stats` carries `tracked_repos` and `total_issues` instead of the per-repo fields.

## See also

- **[`bacio repo show`](/reference/cli/repo)** — same repo metadata without the stats.
- **[`bacio history`](/reference/cli/history)** — recent activity.
- **[Configuration](/reference/config)** — where the DB path comes from.
