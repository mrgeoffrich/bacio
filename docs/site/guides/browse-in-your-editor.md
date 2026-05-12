---
title: Browse your board in your editor
description: With sync enabled, your kanban is plain YAML + markdown — read, grep, and diff it in VS Code, Zed, Vim, or any editor you already love.
---

# Browse in your editor

This guide is for after you've enabled [git-backed sync](/guides/sync-across-machines). The sync repo mirrors your SQLite DB into a folder of YAML + markdown — one file per record — so every tool you already use on code works on your kanban too. Fuzzy finders, ripgrep, split panes, gitlens-style blame, all of it.

## Where things live

Inside the sync repo:

```
~/sync/your-project/
├── bacio-sync.yaml              # sentinel: marks this as a sync repo
├── redirects.yaml               # historical renumbers / renames
└── repos/
    └── MINI/                    # one folder per prefix
        ├── repo.yaml            # repo metadata
        ├── features/
        │   └── auth-rewrite.yaml
        ├── issues/
        │   ├── MINI-1.yaml      # filename embeds the canonical key
        │   └── MINI-2.yaml
        ├── comments/
        │   └── MINI-1/
        │       └── 001.yaml
        ├── documents/
        │   ├── design.md        # body lives next to the metadata
        │   └── design.yaml
        └── history.yaml         # audit log
```

A second project synced to the same sync repo just adds another folder under `repos/` — `repos/AUTH/`, `repos/SITE/`, and so on.

## Read

```bash
cd ~/sync/your-project
$EDITOR repos/MINI/issues/MINI-7.yaml
```

Markdown bodies are stored alongside metadata so a doc reader / markdown preview Just Works. Comments are one file per comment under `comments/<KEY>/`, ordered numerically.

## Grep / ripgrep

```bash
rg -i 'safari'                            # find every mention of safari
rg -F '[blocks]' repos/                   # find issues with explicit blocks relations
rg -B 2 -A 2 'cookies' repos/MINI/docs/   # full-text doc search with context
rg --files-with-matches 'P1' repos/       # which records carry the P1 tag
```

The filenames embed canonical keys, so any hit gives you everything you need to look up the live record with `bacio issue show <KEY>`.

## Fuzzy finders

In VS Code (`⌘P`) or Zed or Helix, typing `MINI-` and a number jumps straight to that issue file. Typing a feature slug fragment narrows on the features folder. Helpful for *"where's that issue about the deploy script…"*.

## Don't edit the YAML directly

It's tempting. Don't. Two reasons:

1. **You lose the audit log.** Changes you make through your editor are commits in git, but they aren't `bacio` mutations — there's no `history` row, no `--user` attribution, and the next `bacio sync` may renumber-around your edit.
2. **You lose validation.** bacio's validators run at the store boundary; bypassing them risks corrupting state (illegal control chars in titles, slug whitespace, malformed UUIDs).

Use the sync repo for **reading**. For writing, go through the CLI or the API. If you need a bulk edit, write a script that calls `bacio issue edit` per row.

## Setting up your editor

Nothing bacio-specific — but if you want a starting point:

- **Markdown preview** — use your editor's default. Doc bodies are plain markdown.
- **YAML schema validation** — bacio doesn't ship JSON Schemas for the on-disk YAML (only for the JSON CLI payloads). Treat the files as opaque to your editor's lint.
- **Git integration** — the sync repo is a normal git repo, so the usual gitlens / inline blame setup gives you who-changed-what-when on every record.

## Diffing changes over time

```bash
cd ~/sync/your-project
git log --follow repos/MINI/issues/MINI-7.yaml      # every revision of one issue
git diff HEAD~10 -- repos/MINI/issues/MINI-7.yaml   # what changed in the last 10 commits
git log -p --since="1 week ago" -- repos/MINI/      # everything in MINI last week
```

The audit log inside the DB is pruned to 60 days; the sync repo's git history is **forever**. If you need long-term traceability, the sync repo is the archive.

## See also

- **[Sync across machines](/guides/sync-across-machines)** — set up the sync repo first.
- **[`bacio sync verify`](/reference/cli/sync)** — validate that the YAML and the DB agree.
- **[Find things fast](/guides/filter-and-search)** — when ripgrep on the sync repo is the right tool.
