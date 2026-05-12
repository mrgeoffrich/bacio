---
title: Work with Claude Code
description: How Claude Code drives bacio — install the skill once, then talk to Claude in plain English. Prompt patterns that work, and how to spot when it's wrong.
---

# Work with Claude Code

This is the flagship workflow bacio is built for. The premise: you already have Claude Code open while you code. Once you install the bacio skill into a repo, Claude knows the full CLI — you just say what you want done.

## One-time setup, per repo

```bash
cd ~/code/my-project
bacio install-skill
```

That drops `SKILL.md` into `<repo>/.claude/skills/bacio/`. Restart Claude Code in this repo so the new skill loads. Re-run `bacio install-skill` after `brew upgrade bacio` to pick up doc updates.

## How Claude actually drives bacio

You don't need to know this to use it, but it helps to recognise good vs. confused behaviour. Behind every prompt, Claude follows the same five-step flow:

1. **Discover** — `bacio schema show <command>` if it's unsure of the payload shape (rare, but happens for less-common verbs).
2. **Compose** — build the JSON payload.
3. **Rehearse** — `--dry-run` for anything destructive (rm verbs especially). Stdout shape matches the real call; cascade counts are reported.
4. **Execute** — run for real with `--user agent-claude` so the audit log attributes the work to the agent, not to your OS user.
5. **Query lean** — `*.list` with filters, `*.show` only when full detail is needed.

If Claude ever asks *"do you want me to actually run that?"* after showing `--dry-run` output, that's the contract working as designed.

## Prompts that work

These are the patterns that hit the skill's trigger phrases cleanly. Plain English; no need to mention `bacio` by name.

### Filing work

> File an issue: the login page 500s on Safari when the password contains a `&`.

> Add a ticket for the flaky deploy test — it's been failing intermittently for a week.

> File these three issues, all under the `auth-rewrite` feature: …

### Planning

> Break down the auth rewrite. I want a feature with starter tasks and a stub design document linked to it.

> Plan the next sprint — what's in the backlog that I should pull up?

### Reading the board

> What's in progress?

> Tell me about MYPR-12.

> What's blocked, and by what?

> What did Claude do yesterday? *(the audit log)*

### State and links

> Move MYPR-3 to in review.

> Tag MYPR-12 as P1 and attach it to the auth-rewrite feature.

> MYPR-7 is blocked by MYPR-5 — wire that up.

### Documents

> Add a design note for the auth rewrite. Title it "JWT vs sessions"; I'll fill in the body later.

> What design docs do we have for `auth-rewrite`?

### Comments

> Add a comment on MYPR-7: I tried clearing the cookie and it didn't help.

## When to use which read

Claude knows when to grab full detail and when to scan. Two read patterns to know:

- **`bacio issue list`** — lean by default. Heavy fields (description, doc content) are stripped. Good for "what's the shape of my backlog?"
- **`bacio issue brief MYPR-42`** — the agent's bulk-context call. Pulls the issue, parent feature, comments, relations, attached PRs, and any linked design documents in one read. This is what Claude uses for *"catch me up on MYPR-42"*.

If you find yourself watching Claude make ten reads in a row, ask it to use `brief` — that's what it's there for.

## How to spot when the agent is wrong

A few tells:

- **It's making things up.** Claude can confidently invent a flag (`--with-skill`, `--agent`). Both don't exist. If the output looks like prose instead of bacio's structured output, ask for the real command and stdout. The skill teaches Claude to lean on `bacio schema show <name>` when unsure — nudge it that way.
- **It skipped `--user`.** Audit log will attribute to your OS user. Not catastrophic, but the history tab gets confusing. Worth pointing out once so it sticks for the session.
- **It's listing with full bodies.** If Claude is pulling huge JSON to summarise a backlog, it's missed the lean-by-default convention. Ask it to use the lean form (`-o json` without `--with-description`).
- **It tried to write without `--dry-run` on something destructive.** Especially `bacio issue rm` or `bacio feature rm`. Ask it to rehearse first.

## Sample skills for common flows

The canonical skill teaches Claude *how* to call the CLI. The sample skills teach it *when* and *why* for common flows:

```bash
bacio install-sample-skills                       # install all four
bacio install-sample-skills triage stand-up       # subset
```

| Skill | Trigger phrase | What it does |
|---|---|---|
| `file-issue` | *"file an issue"*, *"log this"*, *"add a ticket"* | Cleans up a one-line description into a proper title, body, and tags. |
| `triage` | *"triage the backlog"*, *"groom the board"* | Sweeps backlog issues, proposes tags / priorities / feature groupings; asks before writing. |
| `stand-up` | *"stand-up"*, *"daily summary"* | Pure-read summary of in-progress, blocked items, and the last 24h of history. |
| `plan-feature` | *"plan the auth rewrite"*, *"break this down"* | Creates a feature + child issues + blocks/blocked-by edges + an optional linked design doc. |

These files are checked into your repo. Once you've customised one, **stop re-running** `install-sample-skills` — it overwrites your edits.

## See also

- **[How agents drive bacio](/concepts/how-agents-drive-bacio)** — the six rules behind the contract.
- **[`bacio install-skill`](/reference/cli/install-skill)** — the canonical reference install.
- **[`bacio install-sample-skills`](/reference/cli/install-sample-skills)** — the workflow packs.
- **[JSON payloads](/reference/json-payloads)** — the agent contract from the human side.
- **[Drive it without an agent](/guides/drive-it-without-an-agent)** — if you want to skip Claude entirely.
