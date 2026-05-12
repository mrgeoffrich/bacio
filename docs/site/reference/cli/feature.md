---
title: bacio feature
description: Manage features — groups of issues that ship together.
---

# `bacio feature`

Features are the unit of grouping above issues — a feature is a shipping unit, an issue is a task inside it. Slug-addressed (e.g. `auth-rewrite`), with their own description, state, and linked design documents.

## Subcommands

| Subcommand | What it does |
|---|---|
| `bacio feature add <title>` | Create a feature. Slug derived from title (override with `--slug`). |
| `bacio feature list` | List features. Lean by default — pass `--with-description` to inflate. |
| `bacio feature show <slug>` | Show one feature with its issues and linked documents. |
| `bacio feature edit <slug>` | Patch title or description. |
| `bacio feature plan <slug>` | Print open issues in execution order, respecting `blocks` dependencies. See below. |
| `bacio feature rm <slug>` | Delete a feature. Issues remain but lose their `feature_slug`. |

## `bacio feature plan` — execution order

`plan` topo-sorts the feature's open issues (not `done` / `cancelled`), so issues with all blockers satisfied come first; blocked issues appear after their blockers, annotated with `blocked_by`. Cross-feature blockers surface as hints but don't gate the topo position. Errors out on a cycle.

```bash
bacio feature plan auth-rewrite                     # text output for humans
bacio feature plan auth-rewrite -o json             # drive an agent through the order
```

Pair with `bacio issue next --feature <slug>` to claim the next ready issue atomically.

## Worked examples

```bash
# Create with description from a file
bacio feature add "Auth rewrite" \
  --slug auth-rewrite \
  --description-file docs/auth-spec.md

# Read
bacio feature list
bacio feature show auth-rewrite

# Edit
bacio feature edit auth-rewrite --title "Auth rewrite (Q2)"

# Plan + claim loop
bacio feature plan auth-rewrite -o json
bacio issue next --feature auth-rewrite --user agent-claude -o json
```

## See also

- **[`bacio issue`](/reference/cli/issue)** — `next` / `peek` for the atomic claim pattern.
- **[`bacio doc link`](/reference/cli/doc)** — attach design docs to a feature.
