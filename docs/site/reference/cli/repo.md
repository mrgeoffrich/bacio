---
title: bacio repo
description: List, show, and remove tracked repos from the global bacio database.
---

# `bacio repo`

Inspect and manage the repos bacio knows about. Repos auto-register when you first run any `bacio` command inside a git working tree, so this is mostly read-only — `bacio repo list` and `bacio repo show <PREFIX>` are the common calls.

## Subcommands

| Subcommand | What it does |
|---|---|
| `bacio repo list` | List every tracked repo with its prefix, name, path, and remote. |
| `bacio repo show [PREFIX]` | Show full detail for one repo. Defaults to the repo of the current working directory. |
| `bacio repo rm [PREFIX]` | Remove a repo binding from the global DB. **Destructive** — see notes below. |

## `bacio repo rm` — the destructive one

`rm` cascades through **every** issue, comment, feature, document, link, PR attachment, tag, TUI setting, and history row attached to the repo. To prevent accidents, the command requires two things:

1. **`--dry-run` first** is strongly recommended. It returns a `*DeletePreview` with cascade counts so you see the full blast radius before you commit.
2. **`--confirm <PREFIX>`** is **mandatory** on the real run. Without it the command exits non-zero with a "STOP — DESTRUCTIVE OPERATION REQUIRES HUMAN APPROVAL" alert and the impact preview. The agent contract requires showing this preview to the user, getting unambiguous confirmation, then re-running with `--confirm <PREFIX>`.

```bash
bacio repo rm MINI --dry-run               # see the cascade
bacio repo rm MINI --confirm MINI          # actually delete
```

## Worked examples

```bash
bacio repo list                           # every tracked repo
bacio repo show                           # detail for the current cwd's repo
bacio repo show MINI                      # detail for one by prefix

# Destructive — always rehearse first
bacio repo rm MINI --dry-run
bacio repo rm MINI --confirm MINI
```

## Inside a sync repo

`bacio repo list` is one of the three read-only commands that take a YAML-on-disk branch inside a [bacio sync repo](/guides/sync-across-machines). Instead of hitting the local SQLite store, it reads the top-level `index.yaml` and prints the prefixes, names, and remotes recorded there. Fields not stored in `index.yaml` (the local DB id, on-disk path, next issue number) come back empty. If `index.yaml` is missing — e.g. a sync repo created before that file existed — the command falls back to scanning `repos/*/repo.yaml`.

`bacio repo show` and `bacio repo rm` still refuse inside a sync repo with `errSyncRepoMode`; only `list` is sync-aware.

## See also

- **[`bacio init`](/reference/cli/init)** — bind a new repo.
- **[`bacio status`](/reference/cli/status)** — one-screen summary of the current repo.
- **[Sync across machines](/guides/sync-across-machines)** — the sync-repo mode the section above refers to.
