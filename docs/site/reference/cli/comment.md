---
title: bacio comment
description: Add and list comments on an issue.
---

# `bacio comment`

## Subcommands

| Subcommand | What it does |
|---|---|
| `bacio comment add [KEY]` | Add a comment to an issue. Requires `--as <name>` (or `"author":"..."` in JSON). Body via `--body`, `--body-file`, stdin, or `--json`. |
| `bacio comment list <KEY>` | List comments on an issue, oldest first. |

::: tip Heads up
**`--as <name>` is required** on every `comment add` (or `author` on the JSON path). bacio has no auth — you name yourself. Forgetting this is the most common mistake.
:::

```bash
bacio comment add MINI-3 --as Geoff --body "Tried clearing the cookie, didn't help."
```

## Validation

- **Author** (`--as` or JSON `author`) — single-line, length-capped at 80, no control characters.
- **Body** — multi-line allowed (`\t \n \r` OK), no other control characters. Length-capped at 1 MiB.

## Where comments show up

- **`bacio comment list <KEY>`** — oldest-first listing.
- **`bacio issue show <KEY>`** — comments section at the bottom of the rendered output.
- **`bacio issue brief <KEY>`** — included in the bulk-context JSON for agents. Strip with `--no-comments`.
- **TUI Board card overlay** — the "Comments" pane.

## See also

- **[`bacio issue brief`](/reference/cli/issue)** — the bulk-context call.
- **[TUI Board](/reference/tui/board)** — the card overlay and its comments pane.
