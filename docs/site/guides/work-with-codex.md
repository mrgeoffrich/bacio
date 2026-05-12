---
title: Work with Codex
description: Drive bacio from Codex — same one-line install as Claude Code, same JSON contract, same prompts. Codex reads the bacio skill from the same `.claude/skills/` path.
---

# Work with Codex

bacio works the same way under Codex as it does under Claude Code: install the skill once per repo, restart your agent, then ask in plain English. The skill file (`.claude/skills/bacio/SKILL.md`) is the contract; both agents read it from the same path.

## One-time setup, per repo

```bash
cd ~/code/my-project
bacio install-skill
```

That drops `SKILL.md` into `<repo>/.claude/skills/bacio/`. Restart Codex in this repo so the new skill loads. Re-run `bacio install-skill` after `brew upgrade bacio` to pick up doc updates.

::: tip Heads up
`bacio install-skill` is named after Claude Code's skill convention, but the skill itself is agent-agnostic — it documents the JSON CLI contract (`bacio schema show <name>`, `--json`, `--dry-run`, `--user`) without assuming any particular host. Codex picks it up from the same `.claude/skills/` path.
:::

## How Codex drives bacio

Same five-step flow as Claude Code, because the contract is the same:

1. **Discover** — `bacio schema show <command>` if Codex is unsure of the payload shape.
2. **Compose** — build the JSON payload.
3. **Rehearse** — `--dry-run` for anything destructive.
4. **Execute** — run for real with `--user <agent-name>` so the audit log attributes the work to the agent, not to your OS user.
5. **Query lean** — `*.list` with filters; `bacio issue brief <KEY>` for bulk-context reads.

See [How agents drive bacio](/concepts/how-agents-drive-bacio) for the rules behind the contract.

## Prompts that work

Plain English; no need to mention `bacio` by name. The skill triggers on issue / ticket / kanban / feature / docs / history language.

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

> What did Codex do yesterday? *(the audit log)*

### State and links

> Move MYPR-3 to in review.

> Tag MYPR-12 as P1 and attach it to the auth-rewrite feature.

> MYPR-7 is blocked by MYPR-5 — wire that up.

## What's different from Claude Code

In practice, almost nothing. Both agents:

- Read the same `<repo>/.claude/skills/bacio/SKILL.md`.
- Compose the same JSON payloads.
- Honour the same `--dry-run` / `--user` discipline.

The handful of practical differences:

- **Identity in the audit log.** Pass `--user agent-codex` (or whatever name you give the agent) so `bacio history` distinguishes its work from yours.
- **Restart cadence.** After `bacio install-skill` or `brew upgrade bacio`, restart Codex in this repo so the new skill loads.

## Sample skills work the same way

The bundled flow-level skill packs land in `.claude/skills/<name>/` too:

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

## How to spot when the agent is wrong

The same tells apply across both agents:

- **Made-up flags.** `bacio init --with-skill`, `bacio install-skill --agent codex` — neither exists. If Codex is reaching for a flag that looks too convenient, ask it to check `bacio --help` or `bacio schema show <name>`.
- **Skipped `--user`.** Audit log attribution falls back to your OS user. Worth pointing out once so the agent self-corrects for the session.
- **Reads the description-heavy form by default.** Lists are lean for a reason — ask the agent to use `bacio issue list` without `--with-description` when summarising.
- **Destructive call without `--dry-run`.** Especially `bacio issue rm` / `bacio feature rm` / `bacio repo rm`. Ask the agent to rehearse first; `bacio repo rm` additionally requires `--confirm <PREFIX>` and refuses without it.

## See also

- **[Work with Claude Code](/guides/work-with-claude-code)** — the equivalent guide; the workflow patterns are essentially identical.
- **[How agents drive bacio](/concepts/how-agents-drive-bacio)** — the six rules behind the contract.
- **[`bacio install-skill`](/reference/cli/install-skill)** — installs the canonical skill at `.claude/skills/bacio/SKILL.md`.
- **[`bacio install-sample-skills`](/reference/cli/install-sample-skills)** — the workflow packs.
