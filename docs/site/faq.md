---
title: FAQ
description: Common questions about bacio — local-first, teams, agents, conflicts, comparisons, where data lives.
---

# Frequently asked questions

## Is bacio for teams?

It's built for **solo work or a tiny team**. Sync is git-paced, last-writer-wins per record — two people editing the same issue in the same minute will fight git, not bacio. If you need real-time multi-user collaboration, Linear or Jira is the right tool. If you want a personal tracker that your agent can drive reliably, bacio is.

## Where does my data live?

One SQLite file at `~/.bacio/db.sqlite`. That's the only place your kanban data exists until you [enable sync](/guides/sync-across-machines), at which point a second copy lives in a git repo of your choosing.

## Do I need to use Claude Code?

No. The CLI and TUI are first-class for humans — see [Drive it without an agent](/guides/drive-it-without-an-agent). The design just *optimises* for the case where you have an agent, because that's where most of the value lives.

## Why a CLI tool and not a web app?

The premise is that you're already in the terminal, already running an LLM agent, and don't want to open a browser tab to file a bug. The TUI gives you the kanban visual without leaving the terminal; the CLI gives an agent a contract that doesn't leak credentials.

If you just want the bundled kanban in a browser, run [`bacio web`](/reference/cli/web) — same React tree as the desktop app, served at `/ui/`, with the OS browser popped automatically. If you want to build a different web app on top of bacio, run [`bacio api`](/reference/cli/api) (API-only, no `/ui/` mount) and point your client at it — the REST surface mirrors the CLI exactly.

## Will issue numbers ever repeat?

No. Deleting `MINI-3` does not free up the number — the next issue is still `MINI-4`. This is intentional: external references (commit messages, PRs, free-text mentions) keep pointing at something real.

## What happens to issue keys when two machines collide?

If both machines independently create `MINI-7`, the one whose folder is already in git keeps the label. The other's local row is renumbered (`MINI-8`, `MINI-9`, …); features and documents get suffixed (`auth-rewrite-2`, `design-2.md`). The audit log records the renumber, and `redirects.yaml` in the sync repo records the old → new move so `bacio issue show MINI-7` still resolves via the redirect chain.

See [Sync across machines](/guides/sync-across-machines) for the full conflict semantics.

## How long does bacio keep audit history?

60 days, on the local DB. `pruneHistory` runs on every DB open.

The audit log is **local-only** — `bacio sync` does not write a history file into the sync repo, so the on-disk YAML never carries audit rows. If you need longer-lived change tracking, [enable sync](/guides/sync-across-machines) and rely on the sync repo's git history: every state move, edit, and rename surfaces as a commit-level diff there, and the git history is forever.

## Can I run bacio in CI?

`bacio sync verify` in the sync repo is the obvious CI target. Anything that touches state in the project DB doesn't really make sense in CI — the audit-log model assumes a human (or their agent) made the change.

## How do I compare two states of an issue over time?

If you have sync enabled:

```bash
cd ~/sync/your-project
git log -p --follow repos/MINI/issues/MINI-7/issue.yaml
git log -p --follow repos/MINI/issues/MINI-7/description.md
```

git history is the source of truth for *when* something changed; the local audit log inside bacio is the source of truth for *who* changed it and which op was recorded.

## What's the difference between an "issue" and a "feature"?

A **feature** is an optional grouping — think *project*, *epic*, *shipping unit*. An **issue** is the unit of work. Issues can exist without a feature; multiple issues belong to a feature; one issue, one feature. See [Data model](/concepts/data-model).

## Can I have more than one repo?

Yes. Every git repo you run `bacio init` (or any mutating `bacio` command — `bacio status` is read-only and won't register) in registers as a new row with its own prefix. The global DB at `~/.bacio/db.sqlite` holds them all. `bacio issue list --all-repos` and `bacio history --all-repos` are the cross-repo reads.

## Can I track something that isn't a git repo?

Yes — that's a [workspace](/concepts/workspaces). `bacio workspace add "Home Renovation"` gives you a project with a 4-letter prefix, issues, documents and a Kanban board, and no folder on disk. Because there's no working tree to detect, every command that targets it needs `--repo HOME` (or `export BACIO_REPO=HOME`).

## Why does my repo's Kanban start empty?

Because a card is on the Kanban only if you put it in a lane. The Kanban is a **separate axis** from the Agentic Pipeline, not a second view of it — otherwise every `todo` card would render twice on two boards. Put one on with `bacio kanban move MINI-42 --column Doing`. (Workspaces are the exception: there's no Pipeline there, so every new issue lands on a lane automatically.) See [Kanban and the Agentic Pipeline](/concepts/kanban-and-pipeline).

## Can two documents in different folders have the same name?

No. Filenames are unique across the whole project — folders are organisational only. That's what keeps a page's URL, links, and sync path stable when you move it around the tree. See [Document folders](/concepts/document-folders).

## Does my second machine need upgrading before I use workspaces?

No. An older bacio can keep syncing the same sync repo without error — it just won't see workspaces, document folders, or Kanban lanes. The new data lands in new sibling files that an old binary never reads, writes, or deletes; the `repo.yaml` / `issue.yaml` / `doc.yaml` files it does read are unchanged. Upgrade it whenever you like and everything appears.

## What if I lose my laptop?

If you don't sync: you've lost your kanban. The data was only ever on the laptop.

If you sync: the sync repo is a second copy. Run `bacio sync clone` from any new machine pointed at the same remote, and you're back to the state of your last `bacio sync` push.

The relevant lever for paranoia is *what's the maximum amount of work you're willing to lose?* — and then `bacio sync` at that cadence.

## Where do I report a bug or request a feature?

[GitHub issues](https://github.com/mrgeoffrich/bacio/issues). Yes, the irony is noted.

## Why is it called bacio?

"Kiss" in Italian. More importantly, it's a chocolate-hazelnut gelato flavour. Pronounced *BAH-choh*.
