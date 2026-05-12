---
title: TUI keybindings
description: Every keybinding across every TUI tab and overlay in one searchable place.
---

# Keybindings cheat sheet

The TUI's footer always shows the bindings for the current view — there's no separate help screen. This page is the grep-friendly version of that footer, expanded across every tab and overlay.

## Globals

These work from any tab. Inside an overlay, `q` and digits still quit / switch tabs (the overlay closes on the way out); `esc` is overlay-routed so it closes the overlay first.

| Key | Action |
|---|---|
| `1` – `4` | Switch to tab N (Board, Features, Docs, History). Digits beyond the tab count fall through to the active view. |
| `q` | Quit. Always. |
| `ctrl-c` | Quit. Always. |
| `esc` | Close the active overlay; quit if no overlay is open. |

::: tip Note
`tab` / `shift-tab` are **not** global — views use them to cycle their own inner panes (e.g. description ↔ comments ↔ attachments inside the card overlay).
:::

## Board tab

| Key | Action |
|---|---|
| `h` / `l` | Move between columns. |
| `j` / `k` | Move between cards in the focused column. |
| `enter` | Open the focused card's detail overlay. |
| `c` | Open the column-visibility picker. |
| `f` | Open the feature filter picker. |
| `H` | Hide the focused column. |
| `d` | Show detail inline. |
| `r` | Reload from the database. |
| `q` | Quit. |

### Card overlay (open with `enter` on a card)

Cycles through three inner panes — description, comments, attachments — via `tab`. Bindings depend on which pane is focused.

| Key | Action |
|---|---|
| `tab` | Next pane (description → comments → attachments → description). |
| `j` / `k` | Scroll within the active pane (or move selection on the attachments pane). |
| `g` / `G` | Jump to top / bottom (description pane). |
| `enter` | Open all comments full-screen (comments pane) / open the focused attachment (attachments pane). |
| `esc` | Close the card overlay. |

### Comment overlay (full-screen comments view)

| Key | Action |
|---|---|
| `j` / `k` | Scroll. |
| `g` / `G` | Top / bottom. |
| `esc` | Back to the card overlay. |

### Column picker (open with `c`) and Feature picker (open with `f`)

| Key | Action |
|---|---|
| `j` / `k` | Move selection. |
| `space` | Toggle the focused item. |
| `a` | Select all. |
| `n` | Select none. |
| `esc` | Close the picker. |

## Features tab

| Key | Action |
|---|---|
| `j` / `k` | Move between features. |
| `enter` | Open the focused feature's overlay. |
| `r` | Reload. |
| `q` | Quit. |

### Feature overlay

| Key | Action |
|---|---|
| `j` / `k` | Scroll. |
| `g` / `G` | Top / bottom. |
| `esc` | Close. |

## Docs tab

| Key | Action |
|---|---|
| `j` / `k` | Move between documents. |
| `enter` | Open the focused document in the full-screen reader. |
| `r` | Reload. |
| `q` | Quit. |

### Doc overlay

| Key | Action |
|---|---|
| `j` / `k` | Scroll. |
| `g` / `G` | Top / bottom (`home` and `end` also work). |
| `esc` | Close. |

Cross-tab note: when a card's *Linked documents* attachment is opened (via `enter` on the attachments pane), the TUI jumps to the Docs tab with that file selected and remembers the original tab so `esc` returns you there.

## History tab

| Key | Action |
|---|---|
| `j` / `k` | Scroll. |
| `g` / `G` | Top / bottom. |
| `r` | Reload. |
| `q` | Quit. |

## Source of truth

These bindings are pulled from the `Help()` methods on each view in `internal/tui/` of the upstream repo. If the footer disagrees with this page, the footer wins — and that's a bug to file.
