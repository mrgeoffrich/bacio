---
title: bacio kanban
description: Manage the human work board — lanes, and which lane a card sits in. Orthogonal to issue state and to the Agentic Pipeline.
---

# `bacio kanban`

The Kanban is your own board, keyed on **lanes** you own. It's a separate axis from an issue's `state` and from the [Agentic Pipeline](/concepts/kanban-and-pipeline): moving a card between lanes never changes its state, and `bacio issue state` never changes its lane.

> A card is on the Kanban **if and only if** it sits in a lane.

In a [workspace](/concepts/workspaces) new issues land on the leftmost lane automatically. In a git repo they start **off** the board and are opted in explicitly.

## Subcommands

| Subcommand | What it does |
|---|---|
| `bacio kanban move <KEY>` | Put one card in a lane, or take it off with `--off-board`. |
| `bacio kanban column list` | List lanes left to right, with card counts. |
| `bacio kanban column add <NAME>` | Add a lane at the right-hand end. |
| `bacio kanban column rename <NAME> <NEW-NAME>` | Rename a lane in place. Cards keep their lane and order. |
| `bacio kanban column mv <NAME> --position N` | Reorder a lane. `--position` is 0-based. |
| `bacio kanban column rm <NAME>` | Delete a lane. Its cards come off the board; no issue is deleted. |

`column` also answers to `columns`, `lane`, and `lanes`.

## Addressing a lane

Lanes are addressed **by name**. Matching is exact first, then a *unique* case-insensitive match — so `--column doing` finds the stock `Doing`. Two lanes differing only in case make the reference ambiguous and the command errors rather than guessing.

Names are stored exactly as you type them: the case-insensitive fallback is lookup convenience, not normalisation.

## `bacio kanban move`

```bash
bacio kanban move MINI-42 --column Doing               # append to the bottom of the lane
bacio kanban move MINI-42 --column Doing --position 0  # top of the lane
bacio kanban move MINI-42 --off-board                  # take it off the Kanban
```

| Flag | What it does |
|---|---|
| `--column <NAME>` | Destination lane. An empty string means **off the board** — a real destination, not a blank. |
| `--off-board` | The explicit spelling of `--column ""`. Mutually exclusive with `--column`. |
| `--position <N>` | 0-based slot within the lane; the lane re-densifies to `0..n-1` around the moved card. Omit to append. |
| `--json` | `{"issue_key": "MINI-42", "column": "Doing", "position": 0}` |

The destination is **required** — one of `--column` or `--off-board` must be given. On the `--json` path the `column` key must be *present*, because `""` is a meaningful value: omitting it would otherwise sweep a card off the board on a typo.

The issue key carries its own prefix, so a card can be moved from anywhere without `--repo`.

## Lanes

```bash
bacio kanban column list
```

```
0   Backlog                  3 card(s)
1   Doing                    1 card(s)
2   Waiting                  0 card(s)
3   Done                     7 card(s)
```

Every project is seeded with `Backlog` / `Doing` / `Waiting` / `Done` at positions 0–3 when it's first registered. They're yours to change:

```bash
bacio kanban column add "Waiting on Bob"
bacio kanban column rename Waiting Blocked
bacio kanban column mv Blocked --position 1
bacio kanban column rm Blocked --dry-run
```

`--position` on `column mv` is **0-based** and dense: `0` is the leftmost lane, and the siblings re-densify around the moved one so the board is always a gapless `0..n-1`. Out-of-range values are clamped. Only the moved lane comes back in the output — re-read `column list` for the new board order.

::: warning `bacio issue reorder` is 1-based
It orders cards within a Pipeline band, not on the Kanban. Different surface, different convention.
:::

### Deleting a lane never deletes an issue

`column rm` takes every card in the lane off the board. The issues themselves are untouched — same state, same feature, same everything. Put them back with `bacio kanban move <KEY> --column <NAME>`.

`--dry-run` reports how many cards would come off:

```bash
bacio kanban column rm Waiting --dry-run
```

## Workspaces

A workspace is driven with the global `--repo` selector, since it has no working tree to detect:

```bash
bacio --repo HOME kanban column list
bacio --repo HOME kanban column add "Quotes in"
```

`bacio kanban move HOME-1 --column Doing` needs no selector — the issue key names the project.

## See also

- **[Kanban and the Agentic Pipeline](/concepts/kanban-and-pipeline)** — the two-axis model.
- **[`bacio issue`](/reference/cli/issue)** — states, which the Kanban is orthogonal to.
- **[Workspaces](/concepts/workspaces)** — where every card lands on the board by default.
