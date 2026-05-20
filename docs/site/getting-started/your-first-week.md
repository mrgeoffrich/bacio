---
title: Your first week with bacio
description: What a real day-to-day workflow looks like — morning stand-up, filing tickets through Claude while you code, end-of-day sync, weekly grooming.
---

# Your first week

You've done the [first-board walk-through](/getting-started/first-board), the basic loop is muscle memory, and now you want a sense of the cadence — what to do every morning, what's automatic, what to commit to your calendar. This page describes a representative week.

The shape of the week, in one paragraph: *you mostly talk to your agent, your agent does the typing, you glance at the TUI when you want a visual, and you `bacio sync` at the end of any session where you want a second machine to see your work.*

## Monday morning — stand-up

You sit down with coffee and Claude Code:

> What changed yesterday, and what's in flight?

Claude runs `bacio issue list -o json --state in_progress`, `bacio issue list -o json --state in_review`, and `bacio history --since 1d -o json`, then summarises what's in flight, what's blocked, and what changed yesterday. Read-only — no writes.

Or:

> What's on my plate this week?

— same idea, slightly different phrasing. You read the summary, get oriented, decide what to pick up. You don't open the TUI yet.

## During the day — filing as you find

You're coding. You hit a bug. You ask Claude:

> File an issue: this caching layer drops the X-Forwarded-For header when the request body is gzip'd. Tag it bug, P1.

Claude composes the JSON, runs `bacio issue add --user agent-claude --json '{…}'`, prints the issue key. You go back to coding. Maybe twenty seconds elapsed.

Five minutes later you remember another thing:

> File an issue for the auth rewrite — actually break it down: create a feature, add three child issues, wire up MIDS-7 as a blocker on the first one.

Claude does `bacio feature add`, three `bacio issue add` calls under the new feature, and a `bacio link MIDS-7 blocks MIDS-X` (relations are stored one-directionally — "X blocked by Y" is created as "Y blocks X"; the reverse view is rendered automatically wherever bacio shows relations). You confirm what it picked, fix the title on the second issue (*"actually call it 'Token rotation' not 'Refresh logic'"*), and move on.

## Mid-day — glance at the board

Sometime mid-afternoon you want a visual. Drop into the TUI:

```bash
bacio tui
```

Tab `1` Board. Browse what's in progress, what's todo, hit `enter` on something interesting to read the description and comments. `esc` back, `q` quit. You're back in your editor in under a minute.

If you've got multiple features in flight and the board is busy, hit `f` to open the feature picker and narrow to just the one you're actively shipping.

## End of day — sync

If your bacio kanban is local-only (no sync set up), there's nothing to do — close your laptop, see you tomorrow.

If you've [set up sync](/guides/sync-across-machines):

```bash
bacio sync
```

Pull → import → export → commit → push. Takes a few seconds. Your desktop, your second laptop, or your laptop-on-the-couch sees the same board next time it runs.

## Friday afternoon — grooming

The triage / grooming workflow is the one weekly ritual worth keeping:

> triage the backlog

(via the `triage` sample skill) — Claude sweeps issues in the `todo` column, proposes tags, priorities, and feature groupings, **and asks before writing**. You go through its proposal — *"yes, no, yes, no, change this one to P1 not P2"* — and Claude applies the accepted changes.

If you've not installed the sample skill:

> Go through everything in the todo column. For each issue, propose a tag, a priority, and which feature (if any) it belongs to. Don't write anything yet — show me the table.

Same outcome, more setup per call.

## When something goes wrong

Things that have caught real users out:

- **Claude attributes work to your OS user instead of `agent-claude`.** It forgot `--user`. Mention it once and the skill self-corrects for the rest of the session.
- **You can't find an issue from last week.** Either filter the History tab (`5`) by the day, or `bacio history --since 1w`, or — if you've enabled sync — `cd ~/sync/your-project && git log --since="1 week ago"`.
- **A prefix collision on a second machine.** `bacio sync clone` refuses to overwrite. Re-run with `--allow-renumber` (after looking at `--dry-run` first) to let the joining side renumber.
- **A stale skill.** After upgrading bacio (`brew upgrade bacio` / `scoop update bacio`), re-run `bacio install-skill` so the doc updates land. Restart Claude Code.

## What you'll stop doing

After a week or so, most users stop doing things they did with their old tracker:

- **You stop opening a web UI.** There isn't one to open.
- **You stop writing precise titles by hand.** Claude writes better titles than you do, faster.
- **You stop tagging.** You ask Claude to tag during triage; you don't tag-as-you-file.
- **You stop checking issue numbers.** Canonical keys (`MINI-42`) work; Claude resolves them, the TUI lays them out, you only quote them in conversation.

## See also

- **[Work with Claude Code](/guides/work-with-claude-code)** — prompt patterns to lean on.
- **[Sync across machines](/guides/sync-across-machines)** — for multi-machine workflows.
- **[`bacio install-agent`](/reference/cli/install-agent)** — set the repo up for agent-driven dispatch.
