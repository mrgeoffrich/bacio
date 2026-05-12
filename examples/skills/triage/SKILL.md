---
name: triage
description: Use this skill when the user wants to triage, groom, sweep, or review the backlog/inbox. Triggers on phrases like "triage the backlog", "groom the board", "let's go through the inbox", "what's in backlog?", "what should I look at next?". Lists open `todo` issues, proposes tags / priorities / feature groupings / state moves, but ONLY mutates after explicit user confirmation. Reads via `bacio issue list -o json`; mutates via `bacio issue edit`, `bacio tag add`, `bacio issue state`, `bacio link`.
---

# Triage the todo column

Walk the user through ungroomed issues, propose changes, then write only what they approve.

## When to use this

- The user says *"triage"*, *"groom"*, *"sweep"*, *"let's go through the inbox"*, *"what should I look at next?"*.
- They want a guided pass over the `todo` column — not a single capture (`file-issue`) and not a status report (`stand-up`).

## Procedure

1. **Pull the queue.**
   ```bash
   bacio issue list --state todo -o json
   ```

2. **Group and summarise.** Don't dump raw JSON. Cluster by likely theme — bug vs. feature vs. chore, by feature, by tag — and present a concise list:
   > *"6 issues. 3 look like duplicates of `YOUR-12`. 2 belong to the auth rewrite feature. 1 is a stale chore from 4 weeks ago — candidate to cancel."*

3. **Propose moves explicitly.** For each item, name the action: tag it, attach to a feature, move to `in_progress` / `cancelled`, close as a duplicate of another. Show ≤ 5 proposals at a time so the user can review without scrolling.

4. **Wait for confirmation.** Do not write anything until the user says yes. If they only confirm some, do only those. If they reject a proposal, drop it — don't argue.

5. **Apply confirmed changes.** Always pass `--user <agent-name>`. Common moves:
   ```bash
   bacio issue edit YOUR-7 --feature auth-rewrite --user Claude
   bacio tag add YOUR-7 P1 --user Claude
   bacio issue state YOUR-9 in_progress --user Claude

   # Mark a duplicate: record the relation, then cancel the dupe.
   # (There's no `duplicate` state — `duplicate_of` is a relation type.)
   bacio link YOUR-12 duplicate-of YOUR-3 --user Claude
   bacio issue state YOUR-12 cancelled --user Claude
   ```

6. **Report.** *"Updated 4 issues; 2 left in todo."*

## Tone

This is read-mostly. Be a *triage assistant*, not an autonomous scrubber — a wrong cancel is annoying to undo. Bias toward proposing, summarising, and asking *"do you want me to apply these?"*.

When uncertain about a duplicate or a feature attachment, surface the uncertainty — *"this looks like it might be a duplicate of `YOUR-3`, but the repro steps differ — should I link them as relates-to instead?"*.

## Reference

Full bacio surface: `.claude/skills/bacio/SKILL.md`.
