---
title: bacio history
description: Query the audit log — every mutation bacio has ever recorded, with actor, op, target, and timestamp.
---

# `bacio history`

Every mutation in bacio records an audit row: actor (`--user`), op (`issue.create`, `feature.update`, `sync.renumber`, …), target, and a short details blob. **Reads are not logged.** `bacio history` is how you query that log.

```bash
bacio history                            # last 50 mutations in the current repo (newest-first)
```

## Flags

| Flag | What it does |
|---|---|
| `--limit N` | Cap output. Default `50`. `0` = no limit. |
| `--offset N` | Skip the first N entries (pagination). |
| `--oldest-first` | Reverse the default newest-first order. |
| `--user-filter <name>` | Only entries by this actor. |
| `--op <op>` | Exact op match (e.g. `issue.create`). No prefix matching — use `--kind` instead. |
| `--kind <kind>` | Filter by entity kind: `issue`, `feature`, `document`, or `repo`. |
| `--since <duration>` | Look back this far: `30m`, `1h`, `1d`, `2w`. |
| `--from <timestamp>` | Inclusive lower bound. Mutually exclusive with `--since`. |
| `--to <timestamp>` | Inclusive upper bound. |
| `--all-repos` | Include every tracked repo. |

`--from` / `--to` accept either local-time stamps (`YYYY-MM-DD`, `YYYY-MM-DD HH:MM`, `YYYY-MM-DD HH:MM:SS`) or RFC 3339 (e.g. `2026-05-03T07:27:14Z`). Bare dates start at 00:00 in the local timezone.

## Op names

Dotted form: `<entity>.<verb>`.

| Entity | Verbs |
|---|---|
| `repo` | `create`, `delete`, `upgrade_phantom` |
| `feature` | `create`, `update`, `delete` |
| `issue` | `create`, `update`, `state`, `assign`, `claim`, `delete` |
| `comment` | `add` |
| `relation` | `create`, `delete` |
| `pr` | `attach`, `detach` |
| `tag` | `add`, `remove` |
| `document` | `create`, `update`, `rename`, `delete`, `link`, `unlink` |
| `sync` | `run`, `init`, `clone`, `import`, `renumber`, `rename`, `delete` |

Notes:

- `bacio doc upsert` records `document.create` or `document.update` depending on whether it created the row.
- `bacio issue unassign` reuses `issue.assign` (with an empty assignee in the details blob) rather than its own op.
- `repo.upgrade_phantom` is emitted by `bacio sync` when a placeholder "phantom" repo (a prefix that existed only in the synced YAML) gets a real local working tree on this machine.

## Worked examples

```bash
bacio history --since 1d                                  # last 24h
bacio history --user-filter Claude --op issue.create      # what Claude filed
bacio history --kind document --since 1w                  # all doc activity this week
bacio history --from 2026-05-01 --to 2026-05-03           # absolute range
bacio history --oldest-first --since 1d                   # chronological replay
bacio history --limit 25 --offset 25                      # second page
bacio history --all-repos --since 1d -o json | jq .       # cross-repo, machine-readable
```

::: tip Retention
History is **pruned to 60 days** on every DB open. For longer retention, enable [git-backed sync](/guides/sync-across-machines) — the audit log is included in the synced YAML and survives prune; the sync repo's git history is forever.
:::

## See also

- **[Local-first and the audit log](/concepts/local-first-and-audit)** — what gets recorded and what doesn't.
- **[TUI History tab](/reference/tui/history)** — the visual view of the same data.
