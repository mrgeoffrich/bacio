---
title: bacio workspace
description: Create, list, and delete workspaces — bacio projects with a 4-letter prefix and no git working tree.
---

# `bacio workspace`

A **workspace** is a tracked project with no git checkout: issues, features, documents, a document tree and a Kanban board, but nothing on disk. See [Workspaces](/concepts/workspaces) for the mental model.

## Subcommands

| Subcommand | What it does |
|---|---|
| `bacio workspace add <NAME>` | Create a workspace. Optional `--prefix XXXX`. Accepts `--json`. |
| `bacio workspace list` | List workspaces only. Use `bacio repo list` for every tracked project. |
| `bacio workspace rm <PREFIX>` | Delete a workspace and everything in it. **Destructive** — requires `--confirm <PREFIX>`. |

## `bacio workspace add`

```bash
bacio workspace add "Home Renovation"
```

```
workspace HOME (Home Renovation) created — drive it with `bacio --repo HOME <command>`
```

The prefix is allocated from the name through the same machinery a git repo registration uses. Workspaces and git repos share one prefix namespace, so collisions de-collide the same way and stay 4 characters: a second `Home …` project becomes `HOM2`, a third `HOM3`. Pin your own with `--prefix HOUS`.

Creating a workspace also seeds the mandatory catch-all features and the starter Kanban board (`Backlog` / `Doing` / `Waiting` / `Done`), so it takes issues immediately.

| Flag | What it does |
|---|---|
| `--prefix <XXXX>` | Pin the 4-character prefix instead of allocating one from the name. |
| `--json` | `{"name": "Home Renovation", "prefix": "HOUS"}` — `prefix` optional. |

## Reaching a workspace afterwards

A workspace has no path, so `cwd` detection can never find it. Every repo-scoped command that targets one needs the global **`--repo <PREFIX>`** selector (or `$BACIO_REPO`):

```bash
bacio --repo HOME issue add "Replace the back fence"

export BACIO_REPO=HOME
bacio issue list
bacio kanban column list
```

`--repo` is case-insensitive, and it's a **lookup, never a create** — an unknown prefix errors rather than minting a project. Commands addressed by issue key (`bacio issue show HOME-1`, `bacio kanban move HOME-1 …`) resolve from the key's prefix and need no selector.

## `bacio workspace list`

```bash
bacio workspace list
bacio workspace list -o json
```

Workspaces only. `bacio repo list` shows both kinds with a `kind` field (`git` | `workspace`).

## `bacio workspace rm` — the destructive one

`rm` is [`bacio repo rm`](/reference/cli/repo) narrowed to workspaces. Identical cascade — every issue, comment, feature, document, doc folder, Kanban lane, link, relation, PR attachment, tag, agent session, notification and history row attached to it. There is no undo.

Two guards:

1. **`--dry-run -o json` first.** It returns the cascade counts so you see the blast radius before committing.
2. **`--confirm <PREFIX>`** is mandatory on the real run. Without it the command prints the impact preview and exits non-zero — the agent contract requires showing that preview to the user and getting unambiguous approval before re-running with `--confirm`.

It also refuses a git repo, so a mistyped prefix can't delete the wrong kind of project.

```bash
bacio workspace rm HOME --dry-run -o json
bacio workspace rm HOME --confirm HOME
```

## What a workspace refuses

Everything that needs a working tree:

| Command | Why |
|---|---|
| Agent dispatch | No checkout for a dispatched agent to work in. The Agentic Pipeline tab is hidden for the same reason. |
| `bacio sync init` / `clone` | Nowhere to keep a `.bacio/config.yaml`. A workspace is [mirrored anyway](/concepts/workspaces#sync) whenever a git repo syncs. |
| `bacio doc add --from-path` | Names a file inside a working tree. Use `--content` / `--content-file`. |
| `bacio doc export --to-path` / `--to` | Writes into a working tree. Use `bacio doc download`. |

Each refusal has a workspace-specific message, not the phantom-repo "link it first" — a workspace is pathless on purpose and permanently.

## See also

- **[Workspaces](/concepts/workspaces)** — the concept page.
- **[`bacio repo`](/reference/cli/repo)** — the git-repo equivalents.
- **[`bacio kanban`](/reference/cli/kanban)** — the board a workspace fills automatically.
