---
title: bacio link / unlink
description: Connect issues with typed relationships — blocks, blocked-by, relates-to.
---

# `bacio link` / `bacio unlink`

Issues form a graph through typed relations. `link` creates an edge, `unlink` removes one. Relations are stored **one-directionally** — `blocked-by` is implicit, surfaced automatically in `bacio issue show` as the inverse view of `blocks`.

## Usage

```bash
bacio link <FROM> <type> <TO>
bacio unlink <A> <B>                   # removes any relation between two issues, regardless of direction
```

## Relation types

| Type | Semantics |
|---|---|
| `blocks` | `FROM` blocks `TO`. The inverse view (`TO` is blocked by `FROM`) is surfaced automatically in `bacio issue show`. |
| `relates-to` | Loose association. No direction. |
| `duplicate-of` | `FROM` is a duplicate of `TO`. |

There is **no `blocked-by` create form** — write it as `blocks` from the other direction.

## Worked examples

```bash
bacio link MINI-42 blocks MINI-43        # MINI-42 blocks MINI-43
bacio link MINI-44 duplicate-of MINI-42  # MINI-44 is a duplicate of MINI-42
bacio link MINI-7 relates-to MINI-12     # loose link

bacio unlink MINI-42 MINI-43             # removes the blocks edge above
```

## How relations show up elsewhere

- **`bacio issue show <KEY>`** — outgoing and incoming relations both listed.
- **`bacio issue brief <KEY>`** — included in the bulk-context JSON for agents.
- **TUI Board card overlay** — relations section, surfaced inside the card detail view.
- **`bacio feature plan <slug>`** — uses `blocks` to compute execution order; blocked issues appear after their blockers with a `blocked_by` annotation.

## See also

- **[`bacio issue brief`](/reference/cli/issue)** — the bulk-context call that includes relations.
- **[`bacio feature plan`](/reference/cli/feature)** — execution order respecting `blocks`.
- **[Data model](/concepts/data-model)** — where relations sit alongside issues, features, comments, etc.
