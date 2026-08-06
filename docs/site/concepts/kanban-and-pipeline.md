---
title: Kanban and the Agentic Pipeline
description: Two boards, one tracker — the Pipeline is where agents run work, the Kanban is your own lanes, and they are orthogonal axes.
---

# Kanban and the Agentic Pipeline

bacio shows you two boards, and they are not two views of the same thing.

- The **Agentic Pipeline** is where *agents* run work. Its columns are issue **states**: Backlog (`todo`) → In Pipeline (`in_pipeline`) → Shipping (`to_be_shipped`). The controller engine drives cards along it.
- The **Kanban** is *your* board. Its columns are **lanes** you own — `Backlog` / `Doing` / `Waiting` / `Done` out of the box, renameable to whatever your week actually looks like.

They're **orthogonal axes**. An issue has a state *and* (maybe) a lane, and neither one moves the other:

- `bacio kanban move` never changes an issue's state.
- `bacio issue state` never changes its lane.

## The rule that keeps them apart

> **A card is on the Kanban if and only if it has been put in a lane.**

That's the whole model. There's no "which board does this belong to?" setting — a card with no lane simply isn't on the Kanban, and that's the default for a git repo.

It matters because `todo` is already the Pipeline's Backlog column. If the Kanban were keyed on state, every `todo` card would render twice on two boards with two different drag semantics. Lanes being a separate, opt-in axis is what stops that.

## The two defaults

The default differs by project kind, and both defaults are the useful one:

**In a [workspace](/concepts/workspaces), everything lands on the board.** A new issue drops into the leftmost lane automatically, and the Agentic Pipeline nav entry is hidden entirely. A workspace has no working tree, so there's nowhere for a dispatched agent to work — the Pipeline would be an empty promise. Here, the Kanban *is* the board.

**In a git repo, the Kanban starts empty.** A new issue has no lane; it sits on the Pipeline Backlog where agent work is dispatched from. You opt a card onto the Kanban when *you* want to track it by hand:

```bash
bacio kanban move MINI-42 --column Doing
```

From then on it appears on both — deliberately, because you asked for it. Take it back off with:

```bash
bacio kanban move MINI-42 --off-board
```

::: tip Why opt-in on a repo
Most cards in a code repo are agent work, and the Pipeline already tracks those better than a lane could. The Kanban earns its place for the handful of things that *aren't* a dispatch — a call you need to make, a decision you're waiting on, a chore. Putting all of them on the board by default would just duplicate the Pipeline.
:::

## Lanes

Lanes are per-project, ordered left to right, and fully editable:

```bash
bacio kanban column list                       # left-to-right, with card counts
bacio kanban column add "Waiting on Bob"       # appended to the right-hand end
bacio kanban column rename Waiting Blocked
bacio kanban column mv Blocked --position 1    # 0-based; 0 is the leftmost lane
bacio kanban column rm Blocked --dry-run
```

Two things never happen when you touch a lane:

- **Deleting a lane never deletes an issue.** Its cards just come off the board — same state, same feature, same everything, just no longer on the Kanban. `--dry-run` tells you how many.
- **Renaming or reordering a lane never moves a card between lanes.** Membership and within-lane order are preserved.

Lane names are unique per project and are how you address a lane everywhere. Matching is exact first, then a *unique* case-insensitive match — so `--column doing` finds the stock `Doing`, but two lanes differing only in case make it an error rather than a coin toss.

## Where you see each board

In the desktop app and `bacio web`, they're the first two tabs in the top nav — **Agentic Pipeline**, then **Kanban**. The Pipeline tab is hidden when the active project is a workspace; the Kanban tab is never hidden, because it's the one board every project kind has.

On the CLI, the Pipeline is `bacio issue state` / `bacio issue ship` / the `process` verbs, and the Kanban is [`bacio kanban`](/reference/cli/kanban).

On the Kanban, drag a card between lanes with the mouse; the write goes through optimistically and reverts if the server refuses it. Everything on this page is also reachable from the CLI, so scripts and agents drive the same board.

::: warning The TUI is a third thing
`bacio tui`'s **Board** tab is neither of these. It's the original state-keyed kanban — one column per `todo` / `in_review` / `done` / `cancelled` — and it doesn't render lanes or the Pipeline columns. If you're working lanes, work them from the CLI or the desktop / web app.
:::

## Positions

`--position` on `bacio kanban move` is the 0-based, top-to-bottom slot within the destination lane, and the lane re-densifies to `0..n-1` around the card you moved. Omit it and the card is appended to the bottom.

`bacio kanban column mv --position` is the same idea one level up: a dense 0-based board slot, where `0` is the leftmost lane. Only the moved lane comes back in the output — the siblings shift underneath it, so re-read `bacio kanban column list` for the new board order.

(`bacio issue reorder`, which orders cards within a Pipeline band, is **1-based**. Different surface, different convention.)

## See also

- **[`bacio kanban`](/reference/cli/kanban)** — every lane and card verb.
- **[Workspaces](/concepts/workspaces)** — why a workspace's board fills itself in.
- **[Data model](/concepts/data-model)** — where lanes sit relative to issues and states.
