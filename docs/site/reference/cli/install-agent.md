---
title: bacio install-agent
description: Set the current repo up for agent-driven bacio work — subagent files, hooks, and the channel MCP server, in one command.
---

# `bacio install-agent`

Set `<repo-root>` up for agent-driven bacio work in a single command. `install-agent` performs three steps in one invocation:

1. **Subagent files** — render `.claude/agents/bacio-<mode>-worker.md` for every dispatchable prompt template. Each file is a Claude Code custom subagent whose system prompt is the dispatch template's brief.
2. **Hooks** — merge bacio's agent-supervision hooks into `.claude/settings.json` (`SessionStart`, `UserPromptSubmit`, `Stop`, `SessionEnd`, `PostToolUse`, `PreToolUse`). The hooks keep the local agent registry in sync without the agent calling `bacio agent ...` by hand. `PostToolUse` carries two entries: the task-list mirror (matcher `TaskCreate|TaskUpdate`, command `bacio hook post-tool-use`) and the BACI-147 terminal-title hook (matcher `mcp__bacio__register`, command `bacio hook set-title`) which flips the host terminal's window title to the agent slug as soon as the channel's `register` tool completes.
3. **Channel** — register the `bacio` MCP server in `.mcp.json` so Claude Code can spawn `bacio channel` to push queued dispatches into a running session live.

```bash
bacio install-agent          # print the combined plan, ask once, then apply
bacio install-agent --yes    # accept all three steps non-interactively
```

One combined plan covering all three steps is printed, one confirmation prompt is asked. Pass `--yes` (`-y`) to skip the prompt — required when running non-interactively.

::: tip One command, three artefacts
`install-agent` replaces the former three-command setup (`install-channel`, `install-hooks`, `install-agents`). Those standalone verbs were removed — setting a repo up for agent work was almost always all three together.
:::

## What gets written

```
<git-root>/.claude/agents/bacio-<mode>-worker.md   # one per dispatchable template
<git-root>/.claude/settings.json                   # bacio hook groups merged in
<git-root>/.mcp.json                               # the "bacio" MCP server entry
```

Every step is **non-destructive and idempotent**: existing non-bacio hooks and other `mcpServers` entries are preserved, and bacio's own entries are refreshed in place. The subagent files are overwritten — re-run `install-agent` after editing a template body (via `bacio settings template set` or the Settings panel) to apply the change. `bacio status` reports per-template agent-file freshness (`up-to-date` / `missing` / `stale`).

`install-agent` walks up from the current working directory to find the git root; running it inside a subdirectory works the same as running it at the top.

## Activation

The hooks and channel are inert unless `BACIO_AGENT_MODE=1` is set in the environment of the Claude session that loads them. The recommended launch command (printed after install) is:

```bash
BACIO_AGENT_MODE=1 claude --dangerously-skip-permissions --dangerously-load-development-channels server:bacio
```

For normal interactive Claude sessions, launch without the env var — bacio's hooks and channel detect, log, and exit cleanly. `bacio status` reports the current value.

## See also

- **[`bacio install-skill`](/reference/cli/install-skill)** — install the canonical `SKILL.md` (a separate concern; left untouched by `install-agent`).
- **[Work with Claude Code](/guides/work-with-claude-code)** — the agent-side experience.
