---
title: TUI — Board tab
description: The kanban view — one column per issue state, cards keyboard-navigable, card overlay with description / comments / attachments panes.
---

# Board tab

The Board (tab `1`) is the kanban view of the current repo's issues. One column per state, cards stacked top-down, focus moves with the arrow-style keys.

```
┌─ Backlog ─┬─ Todo ──┬─ In Progress ─┬─ In Review ─┬─ Done ──┐
│ MINI-12   │ MINI-7  │ MINI-3        │ MINI-1      │ MINI-2  │
│ MINI-9    │ MINI-5  │ MINI-4        │             │         │
│           │         │               │             │         │
└───────────┴─────────┴───────────────┴─────────────┴─────────┘
```

## Bindings (default view)

| Key | Action |
|---|---|
| `h` / `l` | Previous / next column. |
| `j` / `k` | Previous / next card in the focused column. |
| `enter` | Open the focused card's detail overlay. |
| `c` | Open the column-visibility picker. |
| `f` | Open the feature filter picker. |
| `H` | Hide the focused column. |
| `d` | Show detail inline. |
| `r` | Reload from the database. |
| `q` | Quit. |

The full surface — including the three overlays the Board owns — is on the [keybindings cheat sheet](/reference/tui/keybindings).

## The card overlay (`enter`)

A full-screen view of one card with three inner panes:

- **Description** — the issue's body, rendered with glamour.
- **Comments** — the threaded comment list.
- **Attachments** — linked documents and attached PR URLs.

`tab` cycles between panes. `j`/`k` scrolls within the active pane (or moves selection on Attachments). `g`/`G` jump top/bottom on the Description. `enter` on Attachments opens the focused attachment — for a document, that jumps to the [Docs tab](/reference/tui/docs) with the file open, and `esc` from there returns you to the Board.

`esc` closes the overlay and returns focus to the card on the board.

## The column picker (`c`)

A modal that lets you toggle which state columns are visible. Useful for hiding `done` and `cancelled` on a busy board.

| Key | Action |
|---|---|
| `j` / `k` | Move selection. |
| `space` | Toggle the focused column. |
| `a` | Show all. |
| `n` | Hide all. |
| `esc` | Close. |

Hidden columns persist per-repo (in `tui_settings(repo_id, key, value)`), so the next time you open the TUI you're back where you left off.

## The feature picker (`f`)

A modal that lets you filter the board by feature. Each row shows a feature name and its issue count; toggling hides that feature's issues from the board.

Same controls as the column picker (`j`/`k`, `space`, `a`/`n`, `esc`).

## Background refresh

The Board auto-reloads from the DB every few seconds — the timestamp in the footer's right-hand chip shows the last refresh. Useful when an agent is filing issues in another terminal and you want to watch them land.

## Source of truth

This page mirrors `internal/tui/board.go` in the upstream repo. If the footer's help text disagrees with this page, the footer wins.
