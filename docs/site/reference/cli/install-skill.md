---
title: bacio install-skill
description: Install the canonical SKILL.md into the current repo so Claude Code / Codex know how to drive bacio.
---

# `bacio install-skill`

Drop the bundled `SKILL.md` into `<repo-root>/.claude/skills/bacio/SKILL.md`, creating the directory if needed. The file is embedded in the bacio binary at build time, so re-running picks up doc updates after `brew upgrade bacio`.

```bash
bacio install-skill
```

The skill is the **canonical reference** for agents — it documents every CLI command, every JSON payload, and the discover → compose → rehearse → execute flow. Agents read it on startup; re-run `install-skill` whenever you upgrade bacio.

::: tip Works for Claude Code and Codex
Both Claude Code and Codex read skills from `.claude/skills/` — one install command covers both. There is no `--agent` flag, no separate Codex install path. After running `install-skill`, restart whichever agent you're using in this repo so the new file loads.
:::

## What gets written

```
<git-root>/.claude/skills/bacio/SKILL.md
```

The bundled SKILL.md is whatever was embedded into the bacio binary at build time. `install-skill` overwrites any existing copy on every run — no "is it newer?" check, no merge — so the file in your repo always matches the installed bacio version.

`install-skill` walks up from the current working directory to find the git root; running it inside a subdirectory of a git repo works the same as running it at the top.

## See also

- **[`bacio install-sample-skills`](/reference/cli/install-sample-skills)** — workflow-level skill packs (file-issue, triage, stand-up, plan-feature) that build on this one.
- **[Work with Claude Code](/guides/work-with-claude-code)** — the agent-side experience.
- **[Work with Codex](/guides/work-with-codex)** — the same flow, Codex-flavoured.
