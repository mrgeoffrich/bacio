# Agent supervision & dispatch

How bacio lets a human (or another agent) see which agents are connected
to a repo and hand them work — end to end, from the dispatch data model
through to the agent picking the work up.

This builds on the **agent registry** (`agents` / `agent_sessions` /
`agent_claims` tables — see `SKILL.md`). The registry answers *"who is
connected?"*; dispatch answers *"give that agent something to do."*

---

## The shape of it

```
  supervisor                          agent session
  ──────────                          ─────────────
  bacio agent dispatch ┐
  TUI board  (x)       ├─► agent_dispatches ─┬─► bacio hook   (pull) ─► agent's context
  desktop issue drawer ┘     (pending)       └─► bacio channel (push) ─► agent's context
                                                                              │
                            (acked) ◄── bacio agent ack / channel reply ◄──────┘
```

A **dispatch** is one unit of supervisor→agent work: an issue to look
at, an intent (plan vs implement), and an optional note. It's local-only
— never synced to GitHub, like the rest of the registry.

---

## The data model

`model.AgentDispatch` (`internal/model/agent.go`), table
`agent_dispatches` (`internal/store/schema.sql`):

| field                          | meaning                                                       |
| ------------------------------ | ------------------------------------------------------------- |
| `RepoID`                       | the repo the dispatch belongs to                              |
| `TargetAgentID` / `TargetSessionID` | who it's for — an agent identity, a session, or both     |
| `IssueID` / `IssueKey`         | the issue it concerns (optional)                              |
| `Mode`                         | `plan`, `implement`, or `""` (untyped)                        |
| `Payload`                      | the instruction body the agent reads                          |
| `Status`                       | `pending` → `delivered` → `acked` (or `cancelled`)            |
| `CreatedBy` / `CreatedAt`      | who queued it, when                                           |
| `DeliveredAt` / `AckedAt` / `AckNote` | lifecycle stamps + the agent's reply                  |

### Mode and the payload

`Mode` is a **structured field**, not parsed out of free text, so it's
queryable and displayable everywhere. `model.ComposeDispatchPayload(mode,
note)` builds the `Payload` the agent actually sees:

- `plan` → *"Run a planning pass on this issue: produce an
  implementation plan, don't write code yet."*
- `implement` → *"Implement this issue end-to-end."*
- a non-empty note is appended after a blank line.

So a dispatch carries both the machine-readable `Mode` **and** a
self-contained `Payload` — tooling can filter on the former; the agent
just reads the latter.

### Lifecycle

`pending` → an agent hasn't seen it yet. `delivered` → it's been drained
into a session (by a hook) or pushed (by a channel). `acked` → the agent
reported back. `cancelled` → the supervisor withdrew it. Settled
dispatches (acked/cancelled) are pruned after 60 days
(`AgentDispatchRetention`); open ones never expire. Store transitions:
`MarkDispatchDelivered`, `AckDispatch`, `CancelDispatch` in
`internal/store/dispatches.go`.

---

## Sending work to an agent

Three surfaces, one path: each resolves a target agent + issue + mode +
note, then calls into the store's `AddDispatch`.

### From the CLI

```bash
bacio agent dispatch BACI-12 --to swift-otter@claude.shiny \
  --mode plan --message "regressed after the tab-strip change"
```

`--to` / `--session` name the target (at least one required); `--mode`
is `plan` or `implement`; `--message` is the optional note. Honours the
six agent-CLI principles — `--json` input, `bacio schema show
agent.dispatch`, `--dry-run`. Code: `internal/cli/agent.go`
(`agentDispatchCmd`) → `internal/client/local_dispatch.go`
(`CreateDispatch`).

### From the TUI

On the board, select a **todo** issue and press **`x`**. A three-step
picker opens (`internal/tui/board_dispatch.go`):

1. **pick an agent** — the repo's live sessions
2. **pick a mode** — Plan or Implement
3. **add a note** — optional, free-form

Confirm and the dispatch is written + an `agent.dispatch` history row is
recorded. `x` on a non-todo issue is a no-op with a one-line hint.

### From the desktop app

Open a **todo** issue's drawer → the **Send to agent** section: pick a
dispatchable agent, optionally type a note, and hit **Send (Plan)** or
**Send (Implement)**. The card gets the breathing "claude" treatment
optimistically and the Agents panel counts refresh. Code:
`desktop/frontend/src/components/IssueDrawer.jsx` →
`desktop/boardservice.go` (`DispatchIssue`) → `client.CreateDispatch`.

> The desktop select only lists agents with a persistent identity slug —
> `DispatchIssue` routes by slug. The TUI picker also targets bare
> sessions (it passes the session id too).

---

## The Agents screen

A per-repo view of who's connected and what they're doing — read-only;
you dispatch work *to* agents, you don't act on them here.

**Status** comes from `model.SessionLiveness(session, now)`:

| status   | meaning                                                         |
| -------- | --------------------------------------------------------------- |
| `active` | alive and seen within `AgentLivenessThreshold` (10 minutes)     |
| `idle`   | alive but quiet longer than that — between turns, or harness shut |
| `ended`  | the session called `bacio agent end` (or a hook ended it)       |

Heartbeats fire on every prompt and on the Stop hook, so a working
session stays `active` comfortably inside the 10-minute window.

### In the TUI

The **Agents** tab (`internal/tui/agents.go`) shows a **card per
session**: name/identity, a status pill, model + branch, last-seen,
and `N open claims · M pending dispatches`. `j`/`k` move between cards;
**`enter`** drills into one agent's open claims and the dispatches aimed
at it; `esc` backs out; `r` reloads.

### In the desktop app

The topbar's agents button opens **`AgentsPanel`**
(`desktop/frontend/src/components/AgentsPanel.jsx`) — the same card per
session, click a card to expand its claims + dispatches inline. Backed
by `BoardService.ListAgents`, which bundles the claims and dispatches
into each `AgentCard` so the drill-down needs no second round trip.

---

## How a dispatch reaches the agent

A dispatch sits `pending` until the agent's session picks it up. There
are two delivery paths — an agent can use either or both.

### Pull — via hooks (default)

If the repo has `bacio install-hooks` set up, the **SessionStart** and
**UserPromptSubmit** hooks call `emitDrainedDispatches`
(`internal/cli/hook.go`): they drain the session's `pending` dispatches,
mark them `delivered`, and print them to stdout — which Claude Code
injects into the agent's context. Nothing to poll; the work shows up on
the agent's next turn.

### Push — via a channel (real-time)

If the session runs `bacio channel` (an MCP-over-stdio server —
`internal/channel/`), dispatches arrive **live** as
`notifications/claude/channel` events the moment they're created. The
channel polls the dispatch queue (~3s) and pushes each new dispatch as a
`<channel source="bacio" dispatch_id="..." issue="..." mode="...">` tag.

**Scoping.** Unlike a hook, a channel is *not* told its session id —
Claude Code only sets `CLAUDE_PROJECT_DIR` in a stdio MCP server's
environment. So `bacio channel` resolves the **repo** from that
directory and the **agent identity** from its `.bacio/agent` file, and
pushes the dispatches queued for that identity (`DrainAgentDispatches`).
A channel that can't resolve a repo or an identity still starts — it
just runs idle. (A bare `--session`-only dispatch with no agent identity
therefore can't reach a channel; it's delivered via the hook pull path,
which *does* know the session id.)

Wiring it up takes two steps, both handled by **`bacio install-channel`**
(`internal/cli/install_channel.go`):

1. it merges a `bacio` entry into the repo's `.mcp.json` so Claude Code
   knows how to spawn `bacio channel` (non-destructive — other MCP
   servers are preserved; `--yes` skips the confirmation prompt);
2. it prints the launch command — channels are a Claude Code research
   preview, and a custom channel isn't on the Anthropic allowlist, so
   the session must opt in with the development flag:

   ```bash
   claude --dangerously-load-development-channels server:bacio
   ```

   (Requires Claude Code v2.1.80+.)

Either way — pull or push — the agent sees the same thing: the issue,
the mode, and the composed payload.

---

## Acknowledging

When the agent has handled a dispatch it acks it — which moves the
dispatch to `acked`, records an optional reply note, and drops it out of
the inbox:

```bash
bacio agent ack 12 --note "claimed BACI-12, opening a PR"
```

From a channel session the agent calls the channel's **`reply`** tool
instead (same effect — `internal/channel/channel.go`). `bacio agent
inbox` lists a session's still-open dispatches at any time.

---

## End-to-end walkthrough

1. A supervisor selects todo issue **BACI-12** on the TUI board, presses
   **`x`**, picks agent `swift-otter@claude.shiny`, mode **Plan**, and
   adds a note. → `agent_dispatches` row, `status=pending`,
   `mode=plan`, `payload=` *"Run a planning pass…\n\n<note>"*.
2. `swift-otter`'s next prompt fires the **UserPromptSubmit** hook →
   `DrainDispatches` flips the row to `delivered` and injects it into
   the agent's context. (Or, if the session runs `bacio channel`, it
   arrived live a few seconds after step 1.)
3. The agent reads the issue + the plan instruction, does the planning
   work, then runs `bacio agent ack 12 --note "plan attached as a
   comment"` → `status=acked`.
4. The supervisor sees the dispatch leave the inbox and the
   acked count update on the **Agents** screen.

---

## Where it lives

| concern                     | files                                                                 |
| --------------------------- | --------------------------------------------------------------------- |
| Model + helpers             | `internal/model/agent.go`                                             |
| Storage + migration         | `internal/store/schema.sql`, `internal/store/store.go`, `internal/store/dispatches.go` |
| Client surface              | `internal/client/client.go`, `internal/client/local_dispatch.go`      |
| CLI                         | `internal/cli/agent.go`, `internal/cli/inputs/agent.go`               |
| Pull delivery (hooks)       | `internal/cli/hook.go`, `internal/cli/install_hooks.go`               |
| Push delivery (channel)     | `internal/channel/channel.go`, `internal/cli/channel.go`, `internal/cli/install_channel.go` |
| TUI Agents tab + picker     | `internal/tui/agents.go`, `internal/tui/board_dispatch.go`, `internal/tui/audit.go` |
| Desktop                     | `desktop/boardservice.go`, `desktop/frontend/src/components/AgentsPanel.jsx`, `desktop/frontend/src/components/IssueDrawer.jsx` |

See also: `docs/agent-cli-principles.md` (why `bacio hook` / `bacio
channel` are exempt from the six rules) and `SKILL.md` (the agent-facing
reference for `bacio agent dispatch` / `inbox` / `ack`).
