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
`bacio install-skill` is named after Claude Code's skill convention, but the skill itself is agent-agnostic — it documents the JSON CLI contract (`bacio schema show <name>`, `--json`, `--dry-run`) without assuming any particular host. Codex picks it up from the same `.claude/skills/` path.
:::

## How Codex drives bacio

Same flow as Claude Code, because the contract is the same — bracketed by *register* and *end*:

0. **Declare itself** — `bacio agent register --agent <slug>` at session start, then `bacio agent claim <KEY>` when it starts focused work. On first contact with a repo, Codex generates a memorable slug (e.g. `clever-lynx@codex.shiny`), registers with `--new`, and persists it to `.bacio/agent` so the identity is reused next time.
1. **Discover** — `bacio schema show <command>` if Codex is unsure of the payload shape.
2. **Compose** — build the JSON payload.
3. **Rehearse** — `--dry-run` for anything destructive.
4. **Execute** — run for real. The audit log resolves the actor from `.bacio/agents.json` (the PID lookup the bacio hook wires up), so no flag is needed.
5. **Query lean** — `*.list` with filters; `bacio issue brief <KEY>` for bulk-context reads.
6. **Tear down** — `bacio agent release <KEY>` when Codex stops, `bacio agent end --reason stop` at session end (auto-releases anything still claimed).

See [How agents drive bacio](/concepts/how-agents-drive-bacio) for the rules behind the contract, and [`bacio agent`](/reference/cli/agent) for the registry surface.

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
- Honour the same `--dry-run` discipline.

The handful of practical differences:

- **Persistent slug.** Codex picks its own `--agent <slug>` and persists it to `.bacio/agent` — the harness suffix differs from Claude (`codex` vs `claude`) but the bootstrap loop in [`bacio agent`](/reference/cli/agent#pick-your-identity) is identical. Make sure `.bacio/agent` is gitignored.
- **Restart cadence.** After `bacio install-skill` or `brew upgrade bacio`, restart Codex in this repo so the new skill loads.

## How to spot when the agent is wrong

The same tells apply across both agents:

- **Made-up flags.** `bacio init --with-skill`, `bacio install-skill --agent codex` — neither exists. If Codex is reaching for a flag that looks too convenient, ask it to check `bacio --help` or `bacio schema show <name>`.
- **Mutations attributed to `user`, not Codex's agent name.** The `(claude_pid → identity)` mapping in `.bacio/agents.json` is missing. Re-run the hook setup (the bacio hook is what populates that mapping at session start).
- **Reads the description-heavy form by default.** Lists are lean for a reason — ask the agent to use `bacio issue list` without `--with-description` when summarising.
- **Destructive call without `--dry-run`.** Especially `bacio issue rm` / `bacio feature rm` / `bacio repo rm`. Ask the agent to rehearse first; `bacio repo rm` additionally requires `--confirm <PREFIX>` and refuses without it.

## See also

- **[Work with Claude Code](/guides/work-with-claude-code)** — the equivalent guide; the workflow patterns are essentially identical.
- **[How agents drive bacio](/concepts/how-agents-drive-bacio)** — the six rules behind the contract.
- **[`bacio agent`](/reference/cli/agent)** — the registry Codex registers itself against.
- **[`bacio install-skill`](/reference/cli/install-skill)** — installs the canonical skill at `.claude/skills/bacio/SKILL.md`.
- **[`bacio install-agent`](/reference/cli/install-agent)** — set the repo up for agent-driven dispatch.
