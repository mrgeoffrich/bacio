---
title: bacio sync
description: Mirror the SQLite DB to a git-backed YAML + markdown repo for cross-machine sharing and editor browsing.
---

# `bacio sync`

`bacio sync` mirrors the SQLite DB to a checked-in folder of YAML + markdown in a separate *sync repo*. You push and pull through normal git; conflicts resolve last-writer-wins per record.

## Subcommands

| Subcommand | What it does |
|---|---|
| `bacio sync` | Steady state: pull → import → export → commit → push. Run whenever. |
| `bacio sync init <local-path>` | First-time setup. Exports current state, commits, pushes. Writes `.bacio/config.yaml` in your project. |
| `bacio sync clone [<local-path>]` | Join an existing sync repo from a second machine. Refuses to overwrite local issues unless `--allow-renumber` is passed. |
| `bacio sync verify` | Check that local DB and sync repo agree, without writing. |
| `bacio sync inspect <prefix>` | Inspect the synced state for one repo prefix. |
| `bacio sync export <path>` | (Internal) Export the DB to a path without git operations. |
| `bacio sync import <path>` | (Internal) Import from a path without git operations. |

For the full walk-through (first-time setup, joining a second machine, conflict semantics, on-disk YAML/markdown layout, diagnostics) see **[Sync across machines](/guides/sync-across-machines)** and **[Browse in your editor](/guides/browse-in-your-editor)**.

## See also

- **[Sync across machines](/guides/sync-across-machines)** — the end-to-end guide.
- **[Browse in your editor](/guides/browse-in-your-editor)** — the on-disk layout, grep recipes.
