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
| `1` – `6` | Switch to tab N (Board, Features, Documents, Agents, History, Settings). Digits beyond the tab count fall through to the active view. |
| `q` | Quit. Always. |
| `ctrl-c` | Quit. Always. |
| `esc` | Close the active overlay; quit if no overlay is open. |

::: tip Note
`tab` / `shift-tab` are **not** global — views use them to cycle their own inner panes (e.g. description ↔ comments ↔ attachments inside the card overlay).
:::

## Board tab

| Key | Action |
|---|---|
| `h` / `l` (or `left` / `right`) | Move between columns. |
| `j` / `k` (or `down` / `up`) | Move between cards in the focused column. |
| `g` / `home` | Jump to the first card. |
| `G` / `end` | Jump to the last card. |
| `enter` | Open the focused card's detail overlay. |
| `c` | Open the column-visibility picker. |
| `f` | Open the feature filter picker. |
| `H` | Hide the focused column (refuses if it's the last visible one). |
| `d` | Toggle the inline detail pane. |
| `r` | Reload from the database. |
| `q` | Quit. |

### Card overlay (open with `enter` on a card)

Cycles through three inner panes — description, comments, attachments — via `tab` / `shift+tab`. Bindings depend on which pane is focused.

| Key | Action |
|---|---|
| `tab` / `shift+tab` | Next / previous pane (description ↔ comments ↔ attachments). |
| `j` / `k` (or `down` / `up`) | Scroll within the active pane (or move selection on the attachments pane). |
| `g` / `home` | Jump to top. |
| `G` / `end` | Jump to bottom. |
| `pgdown` / `space` | Scroll down 10 lines (description / comments). |
| `pgup` | Scroll up 10 lines (description / comments). |
| `enter` (description) | Close the overlay. |
| `enter` (comments) | Open all comments full-screen. |
| `enter` (attachments) | Open the focused attachment (a document jumps to the Docs tab; PR URLs print). |
| `esc` | Close the card overlay. |

### Comment overlay (full-screen comments view)

| Key | Action |
|---|---|
| `j` / `k` (or `down` / `up`) | Scroll. |
| `pgdown` / `space` | Scroll down 10 lines. |
| `pgup` | Scroll up 10 lines. |
| `g` / `home` | Top. |
| `G` / `end` | Bottom. |
| `esc` | Back to the card overlay. |

### Column picker (open with `c`)

| Key | Action |
|---|---|
| `j` / `k` | Move selection. |
| `space` | Toggle the focused column (refuses to hide the last visible one). |
| `a` | Show all columns. |
| `n` | Minimal — keep only the first state column visible (matches the in-app footer's `n minimal`). |
| `esc` | Close the picker. |

### Feature picker (open with `f`)

| Key | Action |
|---|---|
| `j` / `k` | Move selection. |
| `space` | Toggle the focused feature. |
| `a` | Show all features. |
| `n` | Isolate — hide every feature except the focused one. |
| `esc` | Close the picker. |

## Features tab

| Key | Action |
|---|---|
| `j` / `k` (or `down` / `up`) | Move between features. |
| `g` / `home` | First feature. |
| `G` / `end` | Last feature. |
| `enter` | Open the focused feature's overlay. |
| `r` | Reload. |
| `q` | Quit. |

### Feature overlay

| Key | Action |
|---|---|
| `j` / `k` (or `down` / `up`) | Scroll. |
| `pgdown` / `space` | Scroll down 10 lines. |
| `pgup` | Scroll up 10 lines. |
| `g` / `home` | Top. |
| `G` / `end` | Bottom. |
| `esc` / `enter` | Close. |

## Docs tab

| Key | Action |
|---|---|
| `j` / `k` (or `down` / `up`) | Move between documents. |
| `enter` | Open the focused document in the full-screen reader. |
| `r` | Reload. |
| `q` | Quit. |

### Doc overlay

| Key | Action |
|---|---|
| `j` / `k` (or `down` / `up`) | Scroll. |
| `pgdown` / `space` | Scroll down 10 lines. |
| `pgup` | Scroll up 10 lines. |
| `g` / `home` | Top. |
| `G` / `end` | Bottom. |
| `esc` / `enter` | Close. |

Cross-tab note: when a card's *Linked documents* attachment is opened (via `enter` on the attachments pane), the TUI jumps to the Docs tab with that file selected and remembers the original tab so `esc` returns you there.

## Agents tab

| Key | Action |
|---|---|
| `j` / `k` (or `down` / `up`) | Move between agent sessions. |
| `g` / `home` | First session. |
| `G` / `end` | Last session. |
| `enter` | Open the focused session's detail view. |
| `?` | Answer the session's open clarification question(s). |
| `a` | Toggle stub sessions (un-registered sessions) in/out of the list. |
| `r` | Reload. |
| `q` | Quit. |

### Session detail

| Key | Action |
|---|---|
| `j` / `k` (or `down` / `up`) | Scroll. |
| `esc` | Back to the session list. |
| `r` | Reload. |
| `q` | Quit. |

### Question overlay (open with `?`)

| Key | Action |
|---|---|
| `j` / `k` | Move selection. |
| `space` | Toggle the focused option. |
| `tab` | Next question. |
| `enter` | Submit the answer(s). |
| `d` | Dismiss the question. |
| `esc` | Close the overlay. |

## History tab

| Key | Action |
|---|---|
| `j` / `k` (or `down` / `up`) | Scroll. |
| `pgdown` / `space` | Scroll down 10 lines. |
| `pgup` | Scroll up 10 lines. |
| `g` / `home` | Top. |
| `G` / `end` | Bottom. |
| `r` | Reload. |
| `q` | Quit. |

## Settings tab

The Settings tab edits the dispatch prompt templates and board preferences.

| Key | Action |
|---|---|
| `j` / `k` (or `down` / `up`) | Move between templates. |
| `enter` | Edit the focused template. |
| `a` | Add a new template. |
| `r` | Rename the focused template. |
| `d` | Delete the focused template. |
| `R` | Restore the built-in templates. |
| `q` | Quit. |

### Template editor (open with `enter`)

| Key | Action |
|---|---|
| `tab` | Switch between the body and state-gate panes. |
| `ctrl+s` | Save. |
| `ctrl+r` | Reset a built-in template to its default. |
| `h` / `l` | Move between states (state-gate pane). |
| `space` | Toggle the focused state (saves immediately). |
| `esc` | Close the editor. |

## Source of truth

These bindings are pulled from the `Help()` methods on each view in `internal/tui/` of the upstream repo. If the footer disagrees with this page, the footer wins — and that's a bug to file.
