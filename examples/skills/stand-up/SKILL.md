---
name: stand-up
description: Use this skill for daily stand-up summaries, status reports, or "what changed?" / "what's blocked?" / "what's in progress?" questions. Triggers on phrases like "stand-up", "daily summary", "what's the status", "what changed yesterday", "what did Claude do?", "what's blocking me?". Pure read — never mutates. Combines `bacio issue list --state in_progress`, `bacio history --since 1d`, and feature plan walks to produce a concise report.
---

# Stand-up summary

Read-only daily report: what's moving, what's stuck, what changed.

## When to use this

- The user says *"stand-up"*, *"daily summary"*, *"give me a summary"*, *"what's the status"*, *"what changed yesterday"*, *"what's in progress?"*, *"what's blocked?"*, *"what did Claude do?"*.
- They want a *report*, not an action. **This skill never writes** — no `--user` calls, no mutations.

## Procedure

1. **In progress.**
   ```bash
   bacio issue list --state in_progress -o json
   ```
   For each, surface: `KEY title (assignee)`. If there are more than ~7, group by feature or assignee.

2. **Recently changed.**
   ```bash
   bacio history --since 1d -o json
   ```
   Highlight `issue.create`, `issue.state`, and `issue.update` ops. Group by actor — useful for *"what did Claude do?"* or *"what did Geoff do?"* variants.

3. **Blocked.** Walk active features and surface anything waiting:
   ```bash
   bacio feature list -o json | jq -r '.[] | .slug' | while read slug; do
     bacio feature plan "$slug" -o json
   done
   ```
   For each issue with a non-empty `blocked_by` array whose blocker is still open, surface *"`YOUR-12` is waiting on `YOUR-9` (still in_progress)"*.

4. **Compose the summary.** Three short sections:
   - **In progress** — 1–5 items, key + title.
   - **Changed since yesterday** — 3–10 highlights, grouped by actor or theme.
   - **Blocked** — items waiting on something, with the blocker.

   Cap each section. If a section is empty, say so in one line (*"Nothing blocked."*) rather than padding.

## Tone

Tight bullets, no JSON dumps, no preamble. The user is skimming — assume 30-second attention.

## Variants

- *"What's in `auth-rewrite`?"* → run `bacio feature plan auth-rewrite -o json` and summarise the topo order.
- *"Just show me what changed."* → skip the in-progress and blocked sections; only run `bacio history`.
- *"What did Claude do today?"* → `bacio history --since 1d --user-filter Claude -o json`.
- *"What's the status of `YOUR-3`?"* → `bacio issue brief YOUR-3` and summarise.

## Reference

Full bacio surface (history filters, feature plans, brief): `.claude/skills/bacio/SKILL.md`.
