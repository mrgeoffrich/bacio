---
title: bacio history
description: Query the audit log — every mutation bacio has ever recorded, with actor, op, target, and timestamp.
---

# `bacio history`

Every mutation in bacio records an audit row: actor (the agent's identity for agent-driven calls, or the literal `user` placeholder for human-driven ones), op (`issue.create`, `feature.update`, `sync.renumber`, …), target, and a short details blob. **Reads are not logged.** `bacio history` is how you query that log.

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
| `--kind <kind>` | Filter by entity kind: `issue`, `feature`, `document`, `repo`, `agent`, or `sync`. Comment / relation / pr / tag ops all hang off an issue and record `kind=issue`, so they're picked up by `--kind issue` rather than their own kind. |
| `--since <duration>` | Look back this far: `30m`, `1h`, `1d`, `2w`. |
| `--from <timestamp>` | Inclusive lower bound. Mutually exclusive with `--since`. |
| `--to <timestamp>` | Inclusive upper bound. |
| `--all-repos` | Include every tracked repo. |

`--from` / `--to` accept either local-time stamps (`YYYY-MM-DD`, `YYYY-MM-DD HH:MM`, `YYYY-MM-DD HH:MM:SS`) or RFC 3339 (e.g. `2026-05-03T07:27:14Z`). Bare dates start at 00:00 in the local timezone.

## Op names

Dotted form: `<entity>.<verb>`. The right-hand column is the `kind` value the row carries (what `--kind` filters on).

| Op | `kind` |
|---|---|
| `repo.create`, `repo.upgrade_phantom` | `repo` |
| `feature.create`, `feature.update`, `feature.delete` | `feature` |
| `issue.create`, `issue.update`, `issue.state`, `issue.assign`, `issue.claim`, `issue.delete` | `issue` |
| `comment.add` | `issue` |
| `relation.create`, `relation.delete` | `issue` |
| `pr.attach`, `pr.detach` | `issue` |
| `tag.add`, `tag.remove` | `issue` |
| `document.create`, `document.update`, `document.rename`, `document.delete`, `document.link`, `document.unlink` | `document` |
| `agent.identity.create`, `agent.register`, `agent.end`, `agent.claim`, `agent.release` | `agent` |
| `sync.run`, `sync.init`, `sync.clone`, `sync.import`, `sync.renumber`, `sync.rename`, `sync.delete` | `sync` |
| `demo.seed` (hidden `bacio demo` command) | `repo` |

Notes:

- `bacio doc upsert` records `document.create` or `document.update` depending on whether it created the row.
- `bacio issue unassign` reuses `issue.assign` (with an empty assignee in the details blob) rather than its own op.
- `issue.claim` records the atomic claim performed by `bacio issue next` (it moves the issue to `in_progress` and stamps the assignee). `bacio agent claim` records `agent.claim`, which is the registry intent record only — it doesn't change state or assignee.
- `repo.upgrade_phantom` is emitted by the **auto-register flow** (`resolveRepo` / `EnsureRepo`) when any *mutating* `bacio` command runs inside a project working tree whose `remote_url` matches a phantom repo previously imported via sync. (`bacio status` is read-only and does not trigger this — it reports `registered: false` instead.) `bacio sync` itself does not emit this op.
- `bacio agent heartbeat` deliberately does **not** write an audit row — it would flood the log.

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
History is **pruned to 60 days** on every DB open. The audit log is local-only — `bacio sync` does not export it to the sync repo. For longer-lived change tracking, enable [git-backed sync](/guides/sync-across-machines) and rely on the git log of the YAML files: every state move, edit, rename, and link surfaces as a commit-level diff.
:::

## See also

- **[Local-first and the audit log](/concepts/local-first-and-audit)** — what gets recorded and what doesn't.
- **[TUI History tab](/reference/tui/history)** — the visual view of the same data.
