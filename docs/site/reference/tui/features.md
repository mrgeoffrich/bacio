---
title: TUI — Features tab
description: Group issues into features and read the feature overview — description, child issues, linked documents.
---

# Features tab

The Features tab (`2`) is a scrollable list of every feature in the current repo. Open one to see its description, its child issues with their states, and any linked documents.

## Bindings (default view)

| Key | Action |
|---|---|
| `j` / `k` | Previous / next feature. |
| `enter` | Open the feature overlay. |
| `r` | Reload from the database. |
| `q` | Quit. |

## The feature overlay

| Key | Action |
|---|---|
| `j` / `k` | Scroll. |
| `g` / `G` | Top / bottom. |
| `esc` | Close. |

The overlay shows:

- **Title and slug.**
- **Description** rendered with glamour.
- **Issues** — every issue with this `feature_slug`, with its canonical key, state, and title.
- **Linked documents** — every document attached to this feature, with the optional `--why` description if one was set.

## When to use it

The Features tab is the orientation surface — *"what shipping units do we have, and how big are they?"* For the actual day-to-day of moving cards between states, the [Board tab](/reference/tui/board) is the right view.

## Source of truth

This page mirrors `internal/tui/features.go`. If the footer disagrees, the footer wins.
