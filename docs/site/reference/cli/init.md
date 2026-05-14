---
title: bacio init
description: Bind the current git repo to a 4-letter kanban prefix.
---

# `bacio init`

Bind the current git repo to a 4-letter prefix (e.g. `AUTH`, `MINI`) and register it in the global bacio database at `~/.bacio/db.sqlite`.

::: tip Heads up
`init` is **optional**. Running any mutating `bacio` command (e.g. `bacio issue add`) inside a fresh git repo auto-creates the row and allocates a prefix from the repo name. `bacio status` is the deliberate exception: it's strictly read-only and reports `registered: false` rather than binding the repo. Calling `init` explicitly just lets you choose the prefix and confirms the binding.

`init` does **not** create a `.bacio/` directory or a local SQLite file — the database is global, not per-repo.

If two repos share a basename, the second one's auto-allocated prefix gets a numeric suffix (`MINI` taken → `MIN2`, then `MIN3`, …). Use `bacio repo list` to confirm what was assigned, or pass `--prefix` explicitly.
:::

## Synopsis

```bash
bacio init [--prefix <PREFIX>]
```

## Flags

| Flag | What it does |
|---|---|
| `--prefix <PREFIX>` | Explicit 4-character prefix. Must be unique across the global DB. If omitted, a prefix is allocated from the repo name. |

Plus all [global flags](/reference/cli/index#global-flags).

## Worked examples

```bash
# Most common: auto-derive prefix from the repo name
cd ~/code/my-project
bacio init

# Pick your own prefix
bacio init --prefix AUTH

# Machine-readable
bacio init --prefix AUTH -o json
```

## Behaviour

- **Auto-derive.** Prefix is taken from the git repo's basename, normalised to 4 uppercase alphanumerics. `my-project` → `MYPR`, `bacio` → `MINI`.
- **Already bound.** If the repo is already registered, `init` is a no-op that prints the existing binding. Re-running is safe.
- **Prefix already in use.** Allocator skips taken prefixes with a numeric suffix (`MINI` taken → `MIN2`, then `MIN3`). With `--prefix`, a collision is a hard error — no auto-suffix.
- **Outside a git repo.** Hard error — bacio needs a git working tree.

## See also

- **[`bacio repo`](/reference/cli/repo)** — inspect / remove a binding.
- **[Your first board](/getting-started/first-board)** — the full ten-minute walk-through.
