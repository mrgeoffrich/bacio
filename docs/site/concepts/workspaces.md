---
title: Workspaces
description: A workspace is a bacio project with a 4-letter prefix and no git working tree — for tracking work that isn't code.
---

# Workspaces

A **workspace** is a bacio project that isn't a git repo.

It has a 4-letter prefix, issues, features, documents, a document tree and a Kanban board — everything a tracked repo has — except a checkout on disk. Nothing about it points at a folder, and nothing ever will.

That's the whole idea: bacio started as a tracker for code, and a lot of the things you actually want to track aren't code. A house renovation. Your ops runbook. The reading list for a project that hasn't got a repo yet. Before workspaces you had to invent a git repo to hang that off. Now you don't.

## Repo or workspace?

| | Git repo | Workspace |
|---|---|---|
| Bound to a folder on disk | yes — the git toplevel | **no, permanently** |
| How bacio finds it | `cwd`, by walking up to `.git` | `--repo <PREFIX>` only |
| Issues, features, docs, folders | yes | yes |
| Kanban board | yes, starts empty | yes, and every card lands on it |
| Agentic Pipeline / agent dispatch | yes | **no** — no working tree to work in |
| Can drive a `bacio sync` run | yes | no — but it's [mirrored anyway](#sync) |

Use a **git repo** when the work produces commits in that repo. Use a **workspace** for everything else. If you're unsure, the deciding question is *"would I ever want an agent to go and change files for this?"* — if yes, it belongs in the repo.

## Creating one

From the CLI, anywhere:

```bash
bacio workspace add "Home Renovation"
```

```
workspace HOME (Home Renovation) created — drive it with `bacio --repo HOME <command>`
```

The prefix is allocated from the name by the same machinery a git registration uses — and de-collides the same way, so a second `Home …` workspace gets `HOM2`, a third `HOM3`. Prefixes stay 4 characters. Pin your own with `--prefix`:

```bash
bacio workspace add "Home Renovation" --prefix HOUS
```

Creating a workspace seeds the mandatory catch-all features and the starter Kanban board (`Backlog` / `Doing` / `Waiting` / `Done`), so it takes issues immediately.

In the desktop app and in `bacio web`, open the project picker in the top bar and choose **New Workspace…** — the sibling of **Add Git Repository…**. It asks for a name and an optional prefix; there's no folder to pick. The picker groups the list into **Repositories** and **Workspaces**, and once a workspace is selected the closed picker carries a `Workspace` marker, so you always know which kind you're in.

## One prefix namespace

Workspaces and git repos share a single prefix namespace. `HOME-42` is unambiguous against `MINI-42` for the same reason `MINI-42` is unambiguous against `AUTH-42` — the prefix is globally unique in the database.

That means a workspace shows up in the normal cross-repo reads:

```bash
bacio repo list          # both kinds, with a `kind` column: git | workspace
bacio workspace list     # workspaces only
```

and issue keys resolve from anywhere without a selector, because the key carries its own prefix:

```bash
bacio issue show HOME-1
bacio kanban move HOME-1 --column Doing
```

## Reaching a workspace: `--repo`

Every other repo-scoped command resolves the repo from the current directory. A workspace has no directory, so `cwd` detection can never find one — and if you just ran `bacio issue list` from some unrelated folder, bacio would happily resolve (or auto-register) *that* folder's git repo instead. Silently wrong, not an error.

The global **`--repo <PREFIX>`** flag is the fix. It names the project explicitly and short-circuits `cwd` detection entirely:

```bash
bacio --repo HOME issue add "Replace the back fence"
bacio --repo HOME kanban column list
```

Set `BACIO_REPO` instead if you're doing a whole session's work in one workspace:

```bash
export BACIO_REPO=HOME
bacio issue list
bacio doc folder add Quotes
```

Three things worth knowing about it:

- It's **case-insensitive** — `--repo home` and `--repo HOME` are the same call.
- It's a **lookup, never a create**. An unknown prefix errors out rather than minting a new project, unlike the auto-registration you get inside a fresh git working tree.
- It's a **selector**, so it lives beside `--db` / `--remote` / `--dry-run` and never appears as a field in a `--json` payload.

It works on git repos too, which is handy for driving one repo's board from inside another.

::: tip Note
A couple of commands are deliberately deaf to `--repo`, because they're probes of *where you are* rather than operations on a project: `bacio status` reports on the current working tree, and `bacio sync init` / `bacio sync clone` set up sync for the repo you're standing in.
:::

## What a workspace can't do

Every refusal traces back to the same fact — there's no working tree.

- **No Agentic Pipeline.** The Pipeline nav entry is hidden in the desktop and web apps, and agent dispatch is refused server-side on every transport. A dispatched worker's job is to go and change files in a checkout; there isn't one. Track the planning here and dispatch from the git repo the change belongs in.
- **No `bacio doc export --to-path` / `bacio doc add --from-path`.** Both name a file inside a working tree. Use `--content` / `--content-file` to get bytes in, and [`bacio doc download`](/reference/cli/doc) to get them out.
- **No sync configuration of its own** — see below.

These refusals are workspace-specific, not the phantom-repo message. A [phantom](/guides/sync-across-machines) is a synced prefix that has no checkout *on this machine yet*; a workspace is pathless on purpose and forever, so "link it first" would send you hunting for something that will never exist.

## Sync

**A workspace is mirrored for free.** `bacio sync`'s export is whole-database, not per-project: the moment any git repo on the machine drives a sync run, that run carries every workspace's issues, documents, folders and lanes into the sync repo with it.

So there is nothing to configure. A workspace has **no sync settings of its own** and can't drive a sync run — it has nowhere to keep a `.bacio/config.yaml`. The Sync settings pane says exactly that when a workspace is the active project, and shows which sync repo is currently mirroring it. That's also why a workspace never appears in the "unsynced projects" list: that list is a call to action, and here there's no action to take.

Set up sync on any one git repo and your workspaces come along for the ride. See [Sync across machines](/guides/sync-across-machines#workspaces-folders-and-lanes) for what lands on disk.

## Deleting one

`bacio workspace rm` is `bacio repo rm` narrowed to workspaces: same cascade, same gate, and it refuses a git repo so a typo can't delete the wrong kind of thing.

```bash
bacio workspace rm HOME --dry-run -o json   # read the blast radius first
bacio workspace rm HOME --confirm HOME      # actually delete
```

**Destructive and irreversible** — every issue, comment, feature, document, folder, lane, link, tag and history row attached to the workspace goes with it.

## See also

- **[`bacio workspace`](/reference/cli/workspace)** — the CLI reference.
- **[Kanban and the Agentic Pipeline](/concepts/kanban-and-pipeline)** — why a workspace's board fills up automatically and a repo's doesn't.
- **[Document folders](/concepts/document-folders)** — the page tree, which workspaces get too.
- **[Sync across machines](/guides/sync-across-machines)** — how a workspace gets mirrored without asking.
