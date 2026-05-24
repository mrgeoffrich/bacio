---
title: Your first board
description: A guided ten-minute walk-through — init a repo, file issues by hand and by agent, open the TUI, read the audit log.
---

# Your first board

You've got [bacio installed](/getting-started/install). This page walks you through the first ten minutes — init a repo, file your first issue by hand, file your second one through Claude Code, open the TUI, and read the audit log.

The flavour of the workflow you should leave with: **your agent does the typing; you do the reading**. The CLI and TUI are first-class for humans too, but the muscle memory most users settle on is "ask the agent → glance at the TUI".

## 1. Bind a repo to a prefix

From inside any git working tree:

```bash
cd ~/code/my-project
bacio init
```

That prints something like:

```
Prefix:    MYPR
Name:      my-project
Path:      /Users/you/code/my-project
NextIssue: MYPR-1
Created:   2026-05-12 14:33 AEST
```

`init` registers the repo in bacio's global database at `~/.bacio/db.sqlite` and allocates a 4-letter prefix from the repo name. Issues in this repo will be keyed `MYPR-1`, `MYPR-2`, …

::: tip Note
`init` is **optional** — any mutating `bacio` command (e.g. `bacio issue add`) inside a fresh git working tree auto-registers the repo. `bacio status` is the exception: it's strictly read-only and reports `registered: false` rather than binding the repo. You only need `init` when you want to pick the prefix yourself: `bacio init --prefix AUTH`.
:::

## 2. File an issue by hand

Just to prove the CLI works:

```bash
bacio issue add "Login 500s on Safari when password contains '&'"
```

Output:

```
MYPR-1  Login 500s on Safari when password contains '&'
State:    todo
Created:  2026-05-12 14:35 AEST
Updated:  2026-05-12 14:35 AEST
```

(New issues land in `todo` by default. Move to `in_progress` once you start working on it, or `needs_action` if you're paused waiting on a human.)

You can list, show, and move it from the CLI:

```bash
bacio issue list                              # see all issues
bacio issue show MYPR-1                       # detail
bacio issue state MYPR-1 in_progress          # move state
```

For the full surface, [`bacio issue`](/reference/cli/issue) covers `add`, `list`, `show`, `edit`, `state`, `brief`, `assign`, `unassign`, `next`, and `peek`.

## 3. Install the agent skill

bacio ships with a single markdown file that teaches Claude Code (or Codex — both read from `.claude/skills/`) the JSON contract: the CLI commands, the payload shapes, the discover-and-rehearse flow. Install it once per repo:

```bash
bacio install-skill
```

```
installed bacio skill (12345 bytes) at /Users/you/code/my-project/.claude/skills/bacio/SKILL.md
```

Restart Claude Code in this repo so the new skill loads. Re-run `install-skill` after upgrading bacio (`brew upgrade bacio` / `scoop update bacio`) to pick up updates.

::: tip Agent-driven dispatch
To set this repo up so Claude Code can pick up dispatched work (subagent files, hooks, and the channel MCP server), run [`bacio install-agent`](/reference/cli/install-agent).
:::

## 4. File your second issue through Claude

Open Claude Code in the repo and ask:

> File an issue for the auth rewrite — break it into a feature with a few starter tasks.

Claude reads its installed `SKILL.md`, composes a JSON payload, optionally rehearses with `--dry-run`, then commits. You'll see something like:

> Filed feature `auth-rewrite` and three child issues: `MYPR-2`, `MYPR-3`, `MYPR-4`. Want me to set blockers?

Behind the scenes Claude is calling things like:

```bash
bacio feature add --json '{"slug":"auth-rewrite","title":"..."}'
bacio issue add --json '{"title":"...","feature_slug":"auth-rewrite"}'
```

Audit attribution happens automatically: with `bacio install-agent` set up, the SessionStart hook records Claude's `(claude_pid → agent identity)` mapping in `.bacio/agents.json`, and every subsequent `bacio` call resolves the agent's identity from there — no flag needed. See [How agents drive bacio](/concepts/how-agents-drive-bacio) for the principles behind the contract.

## 5. Open the TUI

Time to look at what you've built:

```bash
bacio tui
```

The six tabs are `1` Board, `2` Features, `3` Documents, `4` Agents, `5` History, `6` Settings. Switch with plain digits; the footer shows the bindings for the current view (there's no hidden `?` screen).

Try this:

- **Board** — `h`/`l` between columns, `j`/`k` between cards, `enter` to open one. Inside the card overlay, `tab` cycles description / comments / attachments.
- **Features** — `j`/`k`, `enter` for the overlay with the feature's description and child issues.
- **Agents** — the live agent sessions working this repo, their open claims, and the dispatch queue.
- **History** — every mutation, with actor and op. Notice that the issue you filed by hand attributed to your OS user, and the ones Claude filed attributed to `agent-claude`.

`q` or `esc` quits. The full keybinding surface is on the [cheat sheet](/reference/tui/keybindings).

## 6. Ask the agent what's on your plate

Back in Claude Code:

> What's in progress, and what's blocking what?

Claude calls `bacio issue list -o json --state in_progress` and `bacio issue brief MYPR-…` for anything interesting, then summarises. You read the summary, not the JSON.

## What next

- **[Your first week](/getting-started/your-first-week)** — what a real workflow feels like day-to-day.
- **[Work with Claude Code](/guides/work-with-claude-code)** — prompt patterns that actually work.
- **[Sync across machines](/guides/sync-across-machines)** — when you want the same board on a second laptop.
- **[CLI reference](/reference/cli/)** — every command, every flag.
