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
| `h` / `l` (or `left` / `right`) | Previous / next column. |
| `j` / `k` (or `down` / `up`) | Previous / next card in the focused column. |
| `g` / `home` | Jump to the first card in the focused column. |
| `G` / `end` | Jump to the last card in the focused column. |
| `enter` | Open the focused card's detail overlay. |
| `c` | Open the column-visibility picker. |
| `f` | Open the feature filter picker. |
| `H` | Hide the focused column (refuses if it's the last visible one). |
| `d` | Toggle the inline detail pane. |
| `r` | Reload from the database. |
| `q` | Quit. |

The full surface — including the three overlays the Board owns — is on the [keybindings cheat sheet](/reference/tui/keybindings).

## The card overlay (`enter`)

A full-screen view of one card with three inner panes:

- **Description** — the issue's body, rendered with glamour.
- **Comments** — the threaded comment list.
- **Attachments** — linked documents and attached PR URLs.

`tab` / `shift+tab` cycles between panes. `j`/`k` (or arrow keys) scrolls within the active pane (or moves selection on Attachments). `g`/`home` and `G`/`end` jump top/bottom on every pane. `pgdown`/`space` and `pgup` jump 10 lines on the Description and Comments panes.

Pane-specific `enter`:

- **Description** — closes the overlay.
- **Comments** — opens every comment in a fullscreen scrollable view (`esc` returns to the overlay).
- **Attachments** — opens the focused attachment. For a document, that jumps to the [Docs tab](/reference/tui/docs) with the file open; `esc` from there returns you to this card.

`esc` (from any pane) closes the overlay and returns focus to the card on the board.

## The column picker (`c`)

A modal that lets you toggle which state columns are visible. Useful for hiding `done` and `cancelled` on a busy board.

| Key | Action |
|---|---|
| `j` / `k` | Move selection. |
| `space` | Toggle the focused column (refuses to hide the last visible one). |
| `a` | Show all columns. |
| `n` | Minimise — keep only the first state visible. |
| `esc` | Close. |

Hidden columns persist per-repo (in `tui_settings(repo_id, key, value)`), so the next time you open the TUI you're back where you left off.

## The feature picker (`f`)

A modal that lets you filter the board by feature. Each row shows a feature name and its issue count; toggling hides that feature's issues from the board.

| Key | Action |
|---|---|
| `j` / `k` | Move selection. |
| `space` | Toggle the focused feature. |
| `a` | Show all features. |
| `n` | Isolate — hide every feature except the focused one. |
| `esc` | Close. |

## Background refresh

The Board auto-reloads from the DB every few seconds — the timestamp in the footer's right-hand chip shows the last refresh. Useful when an agent is filing issues in another terminal and you want to watch them land.

## Source of truth

This page mirrors `internal/tui/board.go` in the upstream repo. If the footer's help text disagrees with this page, the footer wins.
