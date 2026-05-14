---
title: bacio sync
description: Mirror the SQLite DB to a git-backed YAML + markdown repo for cross-machine sharing and editor browsing.
status: beta
---

# `bacio sync`

`bacio sync` mirrors the SQLite DB to a checked-in folder of YAML + markdown in a separate *sync repo*. You push and pull through normal git; conflicts resolve last-writer-wins per record.

## Subcommands

| Subcommand | What it does |
|---|---|
| `bacio sync` | Steady state: pull → import → export → commit → push. Run whenever. |
| `bacio sync init <local-path>` | Connect the current project to a sync repo at `<local-path>` and run an initial sync. Handles fresh bootstrap, an empty `git init`-only / cloned-empty-bare folder, and *attach mode* against an already-populated sync repo. Writes `.bacio/config.yaml` in your project. |
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

`<local-path>` may be:

- a missing or empty directory (fresh bootstrap),
- an existing `git init`-only folder, or a freshly `git clone`-ed empty bare remote (sentinel + `.gitattributes` are written, then the first export is committed and pushed),
- an existing bacio sync repo (*attach mode*: pull → import → re-export → commit → push; the current project joins alongside any projects already exported there).

| Flag | What it does |
|---|---|
| `--remote <url>` | Git URL of the remote sync repo. Sets up `origin` and writes `.bacio/config.yaml`. **Optional** when `<local-path>` already has an `origin` configured — the URL is auto-detected; supplying a mismatching `--remote` errors. |

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
