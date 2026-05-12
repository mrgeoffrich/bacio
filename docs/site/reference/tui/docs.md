---
title: TUI — Docs tab
description: Read and navigate per-repo documents — design notes, decisions, anything markdown — in a full-screen reader with glamour rendering.
---

# Docs tab

The Docs tab (`3`) lists every document attached to the current repo. Open one for a full-screen markdown reader.

## Bindings (default view)

| Key | Action |
|---|---|
| `j` / `k` | Previous / next document. |
| `enter` | Open the focused document. |
| `r` | Reload from the database. |
| `q` | Quit. |

## The doc reader overlay

| Key | Action |
|---|---|
| `j` / `k` (or `down` / `up`) | Scroll one line. |
| `g` / `home` | Jump to top. |
| `G` / `end` | Jump to bottom. |
| `esc` | Close. |

Markdown rendering is via glamour — headings, code blocks, lists, and inline emphasis all render. The doc's metadata (filename, type, created/updated, linked-from) shows at the top.

## Cross-tab jumps

When you open a document via the **Attachments pane** of a card overlay on the Board, the TUI:

1. Switches to the Docs tab.
2. Selects the document by filename.
3. Opens the doc overlay.

Pressing `esc` from there returns you to the Board card you came from. The "return tab" is tracked in the model so navigation is reversible.

## Source of truth

This page mirrors `internal/tui/docs.go`. If the footer disagrees, the footer wins.
