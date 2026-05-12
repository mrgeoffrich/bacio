---
title: bacio tag
description: Add or remove free-form tags on issues.
---

# `bacio tag`

Tags are free-form string labels on issues — used for filtering and visual grouping. **Case-sensitive** (`WIP` ≠ `wip`), no internal whitespace, no fixed vocabulary. Invent tags as you need them.

## Subcommands

| Subcommand | What it does |
|---|---|
| `bacio tag add <KEY> <tag> [<tag>...]` | Add one or more tags. Idempotent — adding the same tag twice is a no-op. |
| `bacio tag rm <KEY> <tag> [<tag>...]` | Remove one or more tags. |

For setting at creation, use the `--tag` flag on `bacio issue add` (repeatable). For filtering, use `--tag` on `bacio issue list` (repeatable, multiple tags AND-combine).

## Worked examples

```bash
# Tag at creation
bacio issue add "Login broken" --tag bug --tag backend --tag P0

# Add / remove after the fact
bacio tag add MINI-42 needs-design
bacio tag rm  MINI-42 needs-design

# Filter (AND semantics — bugs that are also P0)
bacio issue list --tag bug --tag P0
```

## Validation

- Single line. No internal whitespace, no control characters.
- Length cap: 80 chars.
- Case-sensitive — `WIP`, `wip`, and `Wip` are three different tags.

## See also

- **[`bacio issue`](/reference/cli/issue)** — `--tag` on `add` and `list`.
- **[Find things fast](/guides/filter-and-search)** — using tags as a filter dimension.
