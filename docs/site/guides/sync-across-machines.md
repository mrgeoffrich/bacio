---
title: Sync across machines
description: Mirror your board to a git repo so a second laptop, a desktop, or a CI bot can see the same kanban — and you can read it in your editor.
---

# Sync across machines

bacio is local-first: by default your kanban lives in one SQLite file on one machine. `bacio sync` mirrors that file to a checked-in folder of YAML + markdown in a separate **sync repo**, and multiple machines collaborate by pushing and pulling that repo through normal git.

Sync is **opt-in**. A project repo without `.bacio/config.yaml` and a sync remote behaves exactly as before.

## The model

- **Sync repo** — a git repo, marked by a `bacio-sync.yaml` sentinel at its root. One sync repo can hold many projects (one folder per prefix under `repos/`).
- **Project repo** — your code repo. Each machine records the sync remote in a **machine-local** `.bacio/config.yaml` (`sync.remote: <git URL>`). Don't commit it — `.bacio/` should be gitignored (`bacio init` adds the rule for you). Collaborators get the remote URL out-of-band and pass it to `bacio sync clone --remote <url>`.
- **Reconciliation** — `bacio sync` runs `pull → import → export → commit → push`. Last-writer-wins per record. Already-in-git wins label collisions.
- **Identity** — every record has an immutable UUIDv7 assigned at create time. Sync matches by `uuid`, never by label, so renumbers and renames never lose history.

## First-time setup

From inside your project repo:

```bash
gh repo create your-project-bacio-sync --private              # an empty git remote
bacio sync init ~/sync/your-project \
  --remote git@github.com:you/your-project-bacio-sync.git
```

This:

1. Creates (or initialises) the sync repo at `~/sync/your-project`, writes `bacio-sync.yaml`.
2. Exports your project's data into the sync repo.
3. Commits and pushes (with `--remote`).
4. Writes a machine-local `.bacio/config.yaml` inside your project pointing at the sync remote (gitignored, not shared).

`<local-path>` may be missing, an empty directory, a freshly `git init`-ed folder with no working-tree files (e.g. a just-cloned empty bare remote), **or an already-populated bacio sync repo**. The last case is *attach mode*: bacio pulls, imports, re-exports, commits, and pushes — connecting your project repo to an existing sync repo that already holds other projects.

If the target already has an `origin` remote configured, `--remote` is **optional** — the URL is auto-detected. Pass `--remote` explicitly when bootstrapping a brand-new sync repo (or to assert the expected URL; a mismatch errors). Any empty git remote works — GitHub, GitLab, Gitea, a bare repo on your own server.

### The `index.yaml` TOC

Every export refreshes a top-level `index.yaml` at the sync-repo root: a machine-readable table-of-contents listing every project repo present (`prefix`, `uuid`, `name`, `remote`, plus `issues` / `features` / `documents` / `comments` counts). The per-repo `repos/<PREFIX>/repo.yaml` files remain authoritative; `index.yaml` is regenerated from them and is byte-stable across no-op runs so steady-state `bacio sync` doesn't churn a commit per invocation. It's safe to delete — the next export rewrites it.

## Joining from a second machine

After cloning your project on machine 2, you need the sync repo's git URL — get it from whoever set up sync (it's the same `--remote` URL used in first-time setup; it's also visible as `origin` in the sync repo itself):

```bash
cd ~/code/your-project
bacio sync clone --remote git@github.com:you/your-project-bacio-sync.git
```

`clone` clones the sync repo, runs the first import, and writes this machine's local `.bacio/config.yaml` so later `bacio sync` runs need no flags. `--remote` is required — `.bacio/config.yaml` is machine-local, so a freshly cloned project repo carries no remote to read. If your local DB already has rows for this project's prefix that would collide, `clone` refuses unless you pass `--allow-renumber`. Use `--dry-run` to see the projected renumbers / renames before committing.

## Steady state

```bash
bacio sync
```

Pull → import → export → commit → push. Run it whenever — there's no daemon. On a non-fast-forward push, bacio pulls, re-imports/re-exports, and retries once.

Useful flags:

| Flag | What it does |
|---|---|
| `--no-import` | Skip the pull/import phase. |
| `--no-export` | Skip the export/commit phase. |
| `--no-push` | Commit but don't push. |
| `--dry-run` | Roll back DB writes, skip commit and push. |

## Workspaces, folders and lanes

**The export is whole-database, not per-project.** Every `bacio sync` run walks every project in `~/.bacio/db.sqlite` and writes them all into the sync repo. Two consequences worth knowing:

**A [workspace](/concepts/workspaces) is mirrored for free.** The moment any git repo on the machine drives a sync run, that run carries every workspace's issues, documents, folders and lanes along with it. There is nothing to set up.

A workspace also **has no sync settings of its own** and cannot drive a run — it has no working tree, so nowhere to keep a `.bacio/config.yaml`. `bacio sync init` / `bacio sync clone` operate on the repo you're standing in and won't target a workspace. That's why a workspace never appears in the "unsynced projects" list in the Sync settings pane: the pane instead names the sync repo currently mirroring it. Set up sync on any one git repo and your workspaces come along.

**Folders and lanes ride along too**, as new sibling records under each project:

```
repos/<PREFIX>/workspace.yaml            # present ⇔ this prefix is a workspace
repos/<PREFIX>/folders/<uuid>/folder.yaml
repos/<PREFIX>/kanban/<uuid>/column.yaml
```

Membership lives on the container: a `folder.yaml` lists the pages inside it and a `column.yaml` lists the cards in the lane, both **in order** — so the tree shape, the order of pages within a folder, and the order of cards within a lane all survive a round trip. The `repo.yaml`, `issue.yaml` and `doc.yaml` files are byte-for-byte what they always were.

::: tip An older bacio keeps syncing fine
That last sentence is the whole compatibility story. A machine still running an older bacio can pull, import, export and push a sync repo written by a newer one without error — it simply doesn't see workspaces, folders or lanes. A workspace imports as an inert prefix with no local checkout, and the `folders/` and `kanban/` directories are invisible to it and left untouched. Upgrade that machine and everything appears.
:::

## What happens on a collision

If two machines independently create `MINI-7`:

- The one whose folder is **already in git** keeps the label.
- The other's local row is renumbered to the next free number (or for features/documents, suffixed: `auth-rewrite-2`, `auth-overview-2.md`).
- The audit log records `sync.renumber` / `sync.rename`.
- `redirects.yaml` in the sync repo records the old → new move, so `bacio issue show MINI-7` still resolves via the redirect chain.

External references (commit messages, PRs, free-text mentions inside descriptions) **aren't rewritten** — humans decide what to do with them.

## Diagnostics

Two commands that run **inside the sync repo**, not the project repo:

```bash
cd ~/sync/your-project
bacio sync verify              # parse failures, uuid collisions, dangling refs, hash drift
bacio sync inspect MINI        # per-prefix summary (counts + recent renumbers)
bacio sync inspect MINI --issue MINI-7
bacio sync inspect MINI --feature auth-rewrite
bacio sync inspect MINI --doc design.md
```

`verify` exits non-zero on errors; warnings (dangling refs, body-hash drift) print but don't change the exit code. Use it on CI in the sync repo if you want pre-commit validation.

## Mode switch

Inside a sync repo, bacio refuses to auto-register the directory as a tracked project (the `bacio-sync.yaml` sentinel switches bacio into sync-repo mode). Mutating commands (`bacio issue add`, `bacio feature edit`, …) error out with a "this is a bacio sync repo" message, pointing you back to a real project working tree.

The read-only list commands take a YAML-on-disk branch instead of refusing:

- `bacio repo list` reads `index.yaml` and prints the prefixes / names / remotes recorded there.
- `bacio issue list --repo <PREFIX>` (or `--all-repos`) walks `repos/<PREFIX>/issues/*/issue.yaml`. The usual `--state`, `--feature`, `--tag`, `--with-description` filters apply. Without `--repo` or `--all-repos`, the command errors with a hint listing available prefixes.
- `bacio doc list --repo <PREFIX>` (or `--all-repos`) walks `repos/<PREFIX>/docs/*/doc.yaml`; `--type` filters as in project-repo mode.

That's the full sync-repo-aware list surface; everything else still refuses with `errSyncRepoMode`.

## When NOT to use sync

- **Solo, one machine** — pure local SQLite is faster and simpler. Just back up `~/.bacio/db.sqlite`.
- **Real-time collaboration** — sync is git-paced, not realtime. Two people editing the same issue in the same minute will fight git, not bacio.

## See also

- **[`bacio sync`](/reference/cli/sync)** — the CLI reference.
- **[Workspaces](/concepts/workspaces)** — mirrored by every sync run, with nothing to configure.
- **[Browse in your editor](/guides/browse-in-your-editor)** — once you have a sync repo, you can ripgrep your board.
- **[Local-first and the audit log](/concepts/local-first-and-audit)** — what survives sync and what doesn't.
