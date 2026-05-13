---
title: bacio agent
description: Track live AI-agent sessions and their issue claims — a local-only registry so you and your tools can see who's working on what.
---

# `bacio agent`

A small local registry of live AI-agent sessions and the issues each one is focused on. Designed for the LLM-driven flow: an agent declares itself at session start, claims the issues it's actively working on, and tears down on exit. Records are **never** replicated to GitHub via `bacio sync` — this is runtime flow data, not durable project state.

Two layers:

- **Agent identity** (long-lived) — a slug like `cheerful-otter@claude.shiny` stored in the `agents` table. Survives `/clear`, restarts, and reboots; persisted on disk in `.bacio/agent` so the next session in the same repo picks it up automatically.
- **Session** (ephemeral) — one row in `agent_sessions` per running instance. FKs back to the identity, so cross-session activity correlates to a single logical agent.

Claims are *intent-only*: claiming an issue does **not** move it or change `assignee`. Multiple agents may claim the same issue at once (pairing or review is a real flow); use `bacio issue state` / `bacio issue assign` for the durable ownership change.

::: tip Local-only in v1
Every agent verb short-circuits with a clear error if `--remote` or `BACIO_REMOTE` is set. HTTP API parity is a v2 follow-up.
:::

## Subcommands

| Subcommand | What it does |
|---|---|
| `bacio agent register` | Register (or refresh) this session. Optionally attaches a persistent `--agent <slug>` identity. |
| `bacio agent heartbeat` | Bump `last_seen_at` on an already-registered session. Optional — register/claim/release already heartbeat. |
| `bacio agent end --reason <r>` | End this session. Auto-releases every open claim it holds. |
| `bacio agent claim <ISSUE-KEY>` | Record intent — this session is focused on the issue. Does **not** touch state or assignee. |
| `bacio agent release <ISSUE-KEY>` | Release this session's claim on an issue. |
| `bacio agent list` | Lean table of sessions in this repo. |
| `bacio agent show <session-id>` | Session detail + full claim history. Accepts any unique prefix of the id (12 chars is plenty in practice). |

Every mutating verb accepts `--json`, supports `--dry-run`, and publishes its input schema via `bacio schema list`. `register` / `end` / `claim` / `release` write audit rows; `heartbeat` deliberately doesn't (it would flood the log).

## Pick your identity

At session start, before any other bacio call, walk this loop:

1. Look for `.bacio/agent` in the repo root. If present, read the single-line slug and skip to step 5.
2. Otherwise, generate a slug of the form `<adjective>-<animal>@<harness>.<hostname>`:
   - **adjective** — pick at random from common English adjectives (`cheerful`, `quiet`, `swift`, `bold`, `clever`, …).
   - **animal** — pick at random from common animals (`otter`, `gorilla`, `panda`, `falcon`, `lynx`, …).
   - **harness** — your harness name (`claude` for Claude Code, `codex` for Codex, …).
   - **hostname** — `hostname -s` on Unix, `%COMPUTERNAME%` on Windows.
3. Register with `--new`:
   ```bash
   bacio agent register --user <name> --agent <slug> --new
   ```
4. If bacio errors with `agent name "<slug>" already taken`, your random slug clashed with another agent's. Pick a different adjective/animal and retry — the host suffix keeps cross-machine identities apart, the retry handles same-machine clashes.
5. On success, write the slug to `.bacio/agent` (just the slug — no surrounding quotes; trailing whitespace is tolerated). Make sure `.bacio/agent` is gitignored — it's per-machine identity, not project state.

On subsequent sessions in the same repo, step 1 short-circuits the loop. Register **without** `--new`:

```bash
bacio agent register --user <name> --agent "$(cat .bacio/agent)"
```

Re-registering is idempotent: bacio refreshes `last_seen_at` and re-links the session to the existing identity row.

## Flags

### `bacio agent register`

| Flag | What it does |
|---|---|
| `--agent <slug>` | Persistent identity slug (see [Pick your identity](#pick-your-identity)). |
| `--new` | Assert `--agent` is a fresh slug. Errors with a stable `agent name … already taken` message when the slug clashes, so an agent loop can detect via `errors.Is(err, store.ErrAgentNameTaken)` (or a stderr grep) and regenerate. Requires `--agent`. |
| `--session <id>` | Session id. Defaults to `$CLAUDE_CODE_SESSION_ID`; errors if neither is set. Other harnesses can pass any opaque id. |
| `--model <id>` | e.g. `claude-sonnet-4-6`. |
| `--mode <id>` | Permission mode (`plan`, `acceptEdits`, `bypass`, …). |
| `--host <hostname>` | Defaults to `os.Hostname()`. |
| `--branch <name>` | Defaults to the current git branch (detected via `git rev-parse --abbrev-ref HEAD`). |

### `bacio agent heartbeat`

| Flag | What it does |
|---|---|
| `--session <id>` | As above. |
| `--model <id>` | Overwrite previous model on the session if different. |
| `--mode <id>` | Permission mode. |
| `--branch <name>` | Git branch. |

### `bacio agent end`

| Flag | What it does |
|---|---|
| `--reason <r>` | One of `stop` (default) · `clear` · `logout` · `crash` · `other`. |
| `--session <id>` | As above. |

`end` auto-releases every open claim the session holds before stamping `ended_at` — you can skip `release` when you're shutting down anyway.

### `bacio agent claim <ISSUE-KEY>` / `release <ISSUE-KEY>`

| Flag | What it does |
|---|---|
| `--session <id>` | As above. |

Both verbs accept a positional `<ISSUE-KEY>` **or** a `--json` payload. The session must currently be alive — claims against an ended session are rejected.

### `bacio agent list`

| Flag | What it does |
|---|---|
| `--active` | Only sessions that haven't been ended (`ended_at IS NULL`). |
| `--all-repos` | Include sessions from every tracked repo (default: just this one). |
| `--since <duration>` | Only sessions seen within this window. Accepts `30m`, `4h`, `1d`, … |

Text output is a `tabwriter` table; JSON output is `[]model.AgentSession` (always an array, never `null`).

### `bacio agent show <session-id>`

No flags. Accepts any unique prefix of the session id — the 12-char prefix shown by `agent list` is enough in practice. Ambiguous prefixes error out; pass more characters or the full id.

## Worked example

A short agent loop, from first session in a repo through to teardown:

```bash
# Identity bootstrap: read saved slug, else generate + claim + persist.
if [ -f .bacio/agent ]; then
    SLUG=$(cat .bacio/agent)
    bacio agent register --user agent-claude --agent "$SLUG"
else
    SLUG="cheerful-otter@claude.$(hostname -s)"
    until bacio agent register --user agent-claude --agent "$SLUG" --new 2>/dev/null; do
        SLUG="quiet-falcon@claude.$(hostname -s)"   # …regenerate
    done
    printf '%s' "$SLUG" > .bacio/agent
fi

bacio agent claim MINI-42 --user agent-claude
# ...do the work: bacio issue state, bacio comment add, edits, commits ...
bacio agent release MINI-42 --user agent-claude
bacio agent end --reason stop --user agent-claude
```

## See also

- **[How agents drive bacio](/concepts/how-agents-drive-bacio)** — the six rules behind the CLI contract.
- **[Working with Claude Code](/guides/work-with-claude-code)** — the end-to-end agent flow with the registry plugged in.
- **[`bacio issue`](/reference/cli/issue)** — claim records intent; `issue state` / `issue assign` are still the durable ownership moves.
