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
| `bacio sync export <path>` | (Hidden) Export the DB to a path without git operations. |
| `bacio sync import <path>` | (Hidden) Import from a path without git operations. |

## Flags

### `bacio sync` (steady state)

| Flag | What it does |
|---|---|
| `--no-import` | Skip the import phase (files → DB). |
| `--no-export` | Skip the export phase (DB → files). |
| `--no-push` | Do everything but the final `git push`. |

Plus `--dry-run` from the global set, which projects what would change without writing.

### `bacio sync init <local-path>`

| Flag | What it does |
|---|---|
| `--remote <url>` | Git URL of the remote sync repo. Sets up `origin` and writes `.bacio/config.yaml`. Pass when bootstrapping a brand-new sync repo. |

### `bacio sync clone [<local-path>]`

| Flag | What it does |
|---|---|
| `--allow-renumber` | Permit local rows to be renumbered or renamed to resolve collisions with the sync repo. Without it, `clone` errors with a preview if there's anything to renumber. |

### `bacio sync inspect <prefix>`

At most one of these may be set; without any, you get a per-prefix summary:

| Flag | What it does |
|---|---|
| `--issue <KEY>` | Show one issue's parsed YAML + body. |
| `--feature <slug>` | Show one feature. |
| `--doc <filename>` | Show one document. |

For the full walk-through (first-time setup, joining a second machine, conflict semantics, on-disk YAML/markdown layout, diagnostics) see **[Sync across machines](/guides/sync-across-machines)** and **[Browse in your editor](/guides/browse-in-your-editor)**.

## See also

- **[Sync across machines](/guides/sync-across-machines)** — the end-to-end guide.
- **[Browse in your editor](/guides/browse-in-your-editor)** — the on-disk layout, grep recipes.
