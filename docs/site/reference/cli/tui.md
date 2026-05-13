---
title: bacio tui
description: Open the full-screen kanban TUI for the current repo, or snapshot a view for CI / layout debugging.
---

# `bacio tui`

Open the full-screen kanban board. Four tabs (Board, Features, Docs, History), all keyboard-driven. See the [TUI reference](/reference/tui/board) for what each tab does.

```bash
bacio tui
```

## Flags

| Flag | What it does |
|---|---|
| `--repo <PREFIX>` | Open this repo prefix instead of resolving from the current `cwd`. Useful for opening repos that have no real git working tree (e.g. the synthetic data the hidden `bacio demo` seed creates). |

## Snapshot mode

`--snapshot` renders a single view to stdout non-interactively, at the given terminal size. Useful for layout debugging and CI snapshot tests.

| Flag | What it does |
|---|---|
| `--snapshot <target>` | Render one view and exit. Targets: `board`, `features`, `docs`, `history`, `card-overlay`, `doc-overlay`, `feature-overlay`, `picker`. |
| `--issue <KEY>` | Issue to focus for `board` / `card-overlay` snapshots (e.g. `MINI-1`). |
| `--width <int>` | Terminal width for snapshot. Default `120`. |
| `--height <int>` | Terminal height for snapshot. Default `40`. |

::: tip Heads up
The TUI talks to SQLite directly — it doesn't honour `--remote`. If you're running against a remote `bacio api` server, the TUI is local-DB only.
:::
