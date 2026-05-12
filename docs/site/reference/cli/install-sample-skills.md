---
title: bacio install-sample-skills
description: Install the bundled workflow skill packs — file-issue, triage, stand-up, plan-feature.
---

# `bacio install-sample-skills`

The canonical [`bacio install-skill`](/reference/cli/install-skill) teaches your agent **how** to call the CLI. The *sample* skills installed here teach it **when** and **why** for common flows — they're trigger-phrase shortcuts you can drop into your repo and tweak.

```bash
bacio install-sample-skills                       # install all four
bacio install-sample-skills triage stand-up       # install a subset
```

Each lands at `<repo-root>/.claude/skills/<name>/SKILL.md` and is overwritten on every run, so re-running picks up bundled updates.

## Bundled skills

| Skill | Trigger | What it does |
|---|---|---|
| `file-issue` | *"file an issue"*, *"log this"*, *"add a ticket"* | Cleans up a one-line description into a proper title/body/tags. |
| `triage` | *"triage the backlog"*, *"groom the board"* | Sweeps backlog issues, proposes tags, priorities, feature groupings. |
| `stand-up` | *"stand-up"*, *"daily summary"* | Pure-read summary of in-progress, blocked, and last-24h history. |
| `plan-feature` | *"plan the auth rewrite"*, *"break this down"* | Creates a feature, child issues, blocks/blocked-by edges, optionally a linked design doc. |

::: tip Heads up
These files are checked into your repo. Once you've customised one, **stop re-running** `install-sample-skills` — it overwrites your edits with the bundled version.
:::
