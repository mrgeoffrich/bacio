---
title: Find things fast
description: Filter on the CLI, narrow the TUI Board with the column and feature pickers, and grep the synced YAML repo when you want full-text search.
---

# Find things fast

bacio has three filtering surfaces — CLI flags, two TUI pickers, and (if you've enabled sync) `ripgrep` against the synced YAML. There's deliberately **no `/` fuzzy-search** in the TUI; the design assumes you'll narrow with flags or pickers, not by typing partial matches.

## CLI: filter `bacio issue list`

The flag path is the densest filter surface. Compose freely:

```bash
bacio issue list --state in_progress
bacio issue list --state todo --tag P1
bacio issue list --feature auth-rewrite
bacio issue list --state in_progress --all-repos
```

Lean by default — descriptions stripped. Add `--with-description` if you actually need bodies. `bacio issue list` doesn't have a built-in `--assignee` filter — pipe `-o json` through `jq` (e.g. `jq '.[] | select(.assignee == "Geoff")'`) for that one.

For JSON consumers (scripts, agents):

```bash
bacio issue list -o json --state in_progress | jq '.[] | .key + "  " + .title'
```

## CLI: filter `bacio history`

```bash
bacio history --since 1d
bacio history --user-filter agent-claude   # filter by actor
bacio history --op issue.create
bacio history --kind issue --since 1w
bacio history --all-repos --since 1d
```

Combine freely. The `--since` flag accepts durations like `1h`, `2d`, `1w`. `bacio history` doesn't have a `--target` filter — narrow by `--kind` / `--op` / `--user-filter` / time range, or `jq` the JSON output on `target_label` if you need a specific target. The persistent `--user` records who's running the call rather than filtering output; the filter flag is `--user-filter`.

## TUI: the column picker (`c`)

On the Board tab, hit `c` to open the column-visibility picker. Toggle columns with `space`, select all / none with `a` / `n`, close with `esc`. Useful for hiding `done` and `cancelled` when you're focused on what's live.

Hidden columns persist per-repo in `tui_settings(repo_id, key, value)`, so the next time you open the TUI you're back where you left off.

## TUI: the feature picker (`f`)

Also on the Board, hit `f` to open the feature-filter picker. Each row shows a feature with its issue count; toggling a feature hides its issues from the board. Useful when you want to focus on one shipping unit at a time.

## TUI: in-tab filtering on Features, Docs, History

These tabs are scroll-and-select — there's no key-driven filter inside them. Use the CLI for surgical queries (`bacio feature list`, `bacio doc list --type ...`, `bacio history --since 1d --user-filter ...` and friends), or pipe `-o json | jq` for anything more bespoke, then jump back to the TUI for visual context.

## Full-text search via the sync repo

If you've enabled [git-backed sync](/guides/sync-across-machines), your board is mirrored as YAML + markdown. That means **ripgrep / your editor's grep / GitHub code search all work**:

```bash
cd ~/sync/your-project
rg -i 'safari'           # find every issue, comment, or doc mentioning safari
rg -F '[blocks]' repos/  # find issues with explicit blocks relations
```

The sync repo is one folder per prefix under `repos/`, one file per issue / feature / comment / document. Filenames embed the canonical key (e.g. `repos/MINI/issues/MINI-7.yaml`), so a `rg` hit gives you everything you need to look up the live record.

See [Browse in your editor](/guides/browse-in-your-editor) for the on-disk layout and recommended editor configurations.

## See also

- **[`bacio issue`](/reference/cli/issue)** — every flag on `list`.
- **[`bacio history`](/reference/cli/history)** — every flag on `history`.
- **[TUI Board tab](/reference/tui/board)** — column and feature pickers.
- **[Browse in your editor](/guides/browse-in-your-editor)** — full-text search via sync.
