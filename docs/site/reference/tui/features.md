---
title: TUI — Epics tab
description: Group issues into epics and read the epic overview — description, child issues, linked documents.
---

# Epics tab

The Epics tab (`2`) is a scrollable list of every epic in the current repo. Open one to see its description, its child issues with their states, and any linked documents.

::: tip Naming
"Epic" is the display term. The CLI verbs (`bacio feature ...`), the API routes, the JSON fields and the on-disk sync layout all still say `feature` — the rename was deliberately display-only so existing sync repos and agent prompts keep working.
:::

## Bindings (default view)

| Key | Action |
|---|---|
| `j` / `k` (or `down` / `up`) | Previous / next feature. |
| `g` / `home` | Jump to the first feature. |
| `G` / `end` | Jump to the last feature. |
| `enter` | Open the feature overlay. |
| `r` | Reload from the database. |
| `q` | Quit. |

## The feature overlay

| Key | Action |
|---|---|
| `j` / `k` (or `down` / `up`) | Scroll one line. |
| `pgdown` / `space` | Scroll down 10 lines. |
| `pgup` | Scroll up 10 lines. |
| `g` / `home` | Jump to the top. |
| `G` / `end` | Jump to the bottom. |
| `esc` / `enter` | Close. |

The overlay shows:

- **Title and slug.**
- **Description** rendered with glamour.
- **Issues** — every issue with this `feature_slug`, with its canonical key, state, and title.

::: tip Linked documents on epics
Documents that are linked to an epic show up in `bacio feature show -o json` and on linked issues' `bacio issue brief` output (under `documents[].linked_via`). They are deliberately **not** rendered inside the Epics overlay — open one of the epic's issues on the [Board tab](/reference/tui/board) (or use [`bacio feature show`](/reference/cli/feature)) to see them.
:::

## When to use it

The Epics tab is the orientation surface — *"what shipping units do we have, and how big are they?"* For the actual day-to-day of moving cards between states, the [Board tab](/reference/tui/board) is the right view.

## Source of truth

This page mirrors `internal/tui/features.go`. If the footer disagrees, the footer wins.
