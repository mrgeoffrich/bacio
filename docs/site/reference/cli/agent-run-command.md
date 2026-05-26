---
title: bacio agent-run-command
description: Print the shell one-liner needed to spin up an agent-mode Claude Code session for this repo — nothing else.
---

# `bacio agent-run-command`

Print the exact shell command needed to spin up an agent-mode Claude Code session against this repo, and nothing else. Stdout is a single line — no banner, no log noise, no `-o json` wrapping — so it composes cleanly with `eval`, aliases, scripts, `tmux send-keys`, `wezterm spawn`, and friends.

```bash
bacio agent-run-command
```

Output:

```
BACIO_AGENT_MODE=1 claude --dangerously-skip-permissions --dangerously-load-development-channels server:bacio
```

## Composition

```bash
# launch an agent-mode session in this shell
eval "$(bacio agent-run-command)"

# stash the incantation behind an alias
alias bacio-agent="$(bacio agent-run-command)"
```

## Behaviour

- Read-only. Never mutates the store, never touches `.claude/`, never calls `install-agent`.
- Stdout is exactly the one-liner followed by a single newline. Errors (none, in practice) go to stderr; exit code is `0` on success, non-zero otherwise.
- Harness shim. Like `bacio tui` / `bacio web`, this verb does not follow the six agent-CLI rules — there is no `--json`, no `--dry-run`, and no `bacio schema agent-run-command` entry. It is meant to be typed (or piped through `eval`) by a human or shell, not parsed.

## Drift guard

The emitted command is the same one `bacio install-agent` surfaces in its post-install activation banner. Both call sites resolve through a single constant (`internal/agentmode.LaunchCommand`) so they cannot drift — a test in `internal/cli/agent_run_command_test.go` pins the cross-call equality.

## See also

- **[`bacio install-agent`](/reference/cli/install-agent)** — set the repo up for agent-driven dispatch (subagent files, hooks, channel). Surfaces the same one-liner in its activation banner.
- **[Work with Claude Code](/guides/work-with-claude-code)** — the agent-side experience.
