# Agent supervision & dispatch

How bacio lets a human (or another agent) see which agents are connected
to a repo and hand them work — end to end, from the dispatch data model
through to the agent picking the work up.

This builds on the **agent registry** (`agents` / `agent_sessions` /
`agent_claims` / `agent_channels` tables — see `SKILL.md`). The registry
answers *"who is connected?"*; dispatch answers *"give that agent
something to do."* Both lean on the `claude_pid` correlation described
under [Agent identity](#agent-identity--the-claude_pid-correlation).

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
at, an intent (plan vs implement), and an optional note. The data is
never synced to GitHub (like the rest of the registry), but the full
CRUD — create / list / inbox / ack — is reachable over `bacio api`
(BACI-34 + BACI-35), so a remote supervisor can queue work for an
agent against a server holding the local SQLite store. Only the
side-effect-bearing drain path stays local-only (the `bacio hook`
talks to the store directly).

---

## The data model

`model.AgentDispatch` (`internal/model/agent.go`), table
`agent_dispatches` (`internal/store/schema.sql`):

| field                          | meaning                                                       |
| ------------------------------ | ------------------------------------------------------------- |
| `RepoID`                       | the repo the dispatch belongs to                              |
| `TargetAgentID` / `TargetSessionID` | who it's for — an agent identity, a session, or both     |
| `IssueID` / `IssueKey`         | the issue it concerns (optional)                              |
| `Mode`                         | job stage: `plan`, `design`, `implement`, `review`, `ship`, `fix_review`, or `""` (untyped) |
| `Payload`                      | the instruction body the agent reads                          |
| `Status`                       | `pending` → `delivered` → `acked` (or `cancelled`)            |
| `CreatedBy` / `CreatedAt`      | who queued it, when                                           |
| `DeliveredAt` / `AckedAt` / `AckNote` | lifecycle stamps + the agent's reply                  |

### Mode, prompt templates, and the payload

`Mode` is a **structured field**, not parsed out of free text, so it's
queryable and displayable everywhere. It names a **stage of working a
job** — one of `plan`, `design`, `implement`, `review`, `ship`, `fix_review` (or
`""` for untyped).

Each stage has a **prompt template**: the instruction text, with
`{{token}}` placeholders. The shipped defaults live in
`model.DefaultPromptTemplate(mode)`; users override them per-stage
either from the desktop app's **Settings panel** or from the CLI
(`bacio settings template list / show / set / reset` — `internal/cli/settings.go`),
persisted globally in the `app_settings` KV table
(`prompt_template.<mode>`). Supported placeholders are
`model.PromptTemplateTokens` — `{{issue_id}}`, `{{issue_title}}`,
`{{repo_prefix}}`; an unknown `{{...}}` token is left verbatim rather
than failing the dispatch.

At dispatch time the payload is assembled by
`model.ComposeDispatchPayload(template, vars, note)`: the resolved
template (custom override, else built-in default) is rendered against
the issue's context, then a non-empty note is appended after a blank
line. Template resolution happens in the Go dispatch path
(`client.CreateDispatch`, and `tui/board_dispatch.go` for the TUI
picker) — it needs DB access, so `localStorage` isn't an option.

So a dispatch carries both the machine-readable `Mode` **and** a
self-contained `Payload` — tooling can filter on the former; the agent
just reads the latter.

Each stage also carries a **state-gate** — the set of issue states its
prompt is valid to run from — alongside its template. It lives in
`app_settings` as `prompt_states.<mode>` (sibling of
`prompt_template.<mode>`); built-in defaults are `model.DefaultPromptStates`.
The desktop per-card action button reads it to decide which prompts to
offer; see [State-gated prompts](#state-gated-prompts).

### Lifecycle

`pending` → an agent hasn't seen it yet. `delivered` → bacio has *tried*
to hand it over at least once (drained by a hook, or pushed by a
channel) — but `delivered` is not a confirmation the agent actually saw
it, so a `delivered`-but-un-acked dispatch stays drainable and is
re-surfaced on the next drain (a lost push is recovered, not stranded).
`acked` → the agent reported back; this is the only state that retires a
dispatch. `cancelled` → the supervisor withdrew it. Settled dispatches
(acked/cancelled) are pruned after 60 days (`AgentDispatchRetention`);
open ones never expire. Store transitions: `MarkDispatchDelivered`,
`AckDispatch`, `CancelDispatch` in `internal/store/dispatches.go`.

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
is the job stage (`plan`, `design`, `implement`, `review`, `ship`, `fix_review`);
`--message` is the optional note appended to the rendered template.
Honours the six agent-CLI principles — `--json` input, `bacio schema
show agent.dispatch`, `--dry-run`. Code: `internal/cli/agent.go`
(`agentDispatchCmd`) → `internal/client/local_dispatch.go`
(`CreateDispatch`).

### From the TUI

On the board, select a **todo** issue and press **`x`**. A three-step
picker opens (`internal/tui/board_dispatch.go`):

1. **pick an agent** — the repo's live sessions. Busy sessions (see
   [Busy agents](#busy-agents--dispatch-target-eligibility)) render
   greyed with a `busy · working <ISSUE-KEY>` reason and are
   non-selectable — `j`/`k` skip them and `enter` refuses them.
2. **pick a mode** — Plan or Implement
3. **add a note** — optional, free-form

Confirm and the dispatch is written + an `agent.dispatch` history row is
recorded. `x` on a non-todo issue is a no-op with a one-line hint.
`confirmDispatch` re-checks the chosen session isn't busy before writing,
so a session that goes busy mid-picker is caught.

### From the desktop app

Every Board card carries an **action button** in its bottom-right
corner. Clicking it opens a popover of the dispatch prompts valid for
that card's current state (see [State-gated
prompts](#state-gated-prompts) below); picking one queues the dispatch.
The card gets the breathing "claude" treatment optimistically and the
Agents panel counts refresh. Code:
`desktop/frontend/src/components/KanbanCard.jsx` →
`desktop/boardservice.go` (`DispatchIssue`) → `client.CreateDispatch`.

There is **no manual agent picker** and no free-form note: `DispatchIssue`
auto-picks a free agent (the most-recently-active live session holding
no open claim — a persistent-identity slug, since it routes by slug)
and rejects with a clear error when none is free. The issue drawer no
longer carries any agent-selection UI.

#### State-gated prompts

Each dispatch stage (`plan`, `design`, `implement`, `review`, `ship`,
`fix_review`) declares the set of issue states its prompt is valid to
run from — its **state-gate**. The per-card action button only offers a
prompt when the card's state is in that stage's gate. Built-in defaults
(`model.DefaultPromptStates`):

| stage        | valid-from states |
| ------------ | ----------------- |
| `plan`       | `todo`            |
| `implement`  | `todo`            |
| `review`     | `in_review`       |
| `ship`       | `in_review`       |
| `fix_review` | `in_review`       |

State-gates are global (not per-repo), stored in `app_settings` as
`prompt_states.<mode>` — the sibling of `prompt_template.<mode>`. Edit
them per-stage from the desktop **Settings** screen (a chip-toggle under
each prompt-template editor) or the CLI (`bacio settings template
states show / set / reset`). `DispatchIssue` re-checks the gate against
the issue's current state as a backing guard, so a stale UI can't queue
a prompt the issue's state doesn't allow.

The Settings screen itself is now a **full-screen view** (reached from
the topbar gear icon), not a centred modal.

### Busy agents — dispatch target eligibility

A **busy** session is one holding an open (unreleased) `agent_claims`
row — it's actively working a job. Busy is *derived*, never stored: the
open claim rows are the single source of truth, and `model.SessionBusy`
computes it. A valid dispatch target is a session that is **not busy**
(and, once BACI-11 lands, also channel-connected — the eligibility
predicate is built to compose). The TUI picker excludes busy agents and
surfaces a clear reason; the desktop app's per-card dispatch auto-picks
a free (non-busy) agent and errors when none is available.

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
Separately, `bacio agent list` carries a `CHANNEL` column showing
whether a live `bacio channel` is wired up for the session — see
[Channel presence](#channel-presence--the-channel-column).

**Busy** is a second, orthogonal signal layered on top of liveness: a
session holding an open claim is `busy` regardless of whether it's
`active` or `idle`. It renders as a separate `busy · <ISSUE-KEY>` badge
beside the liveness pill — see
[Busy agents](#busy-agents--dispatch-target-eligibility).

### In the TUI

The **Agents** tab (`internal/tui/agents.go`) shows a **card per
session**: name/identity, a status pill (plus a `busy · <ISSUE-KEY>`
badge when the session holds an open claim), model + branch, last-seen,
and `N open claims · M pending dispatches`. `j`/`k` move between cards;
**`enter`** drills into one agent's open claims (each with the prompt
that session ran) and the dispatches aimed at it; `esc` backs out; `r`
reloads.

### In the desktop app

The topbar's agents button opens **`AgentsPanel`**
(`desktop/frontend/src/components/AgentsPanel.jsx`) — the same card per
session, with a `busy · <ISSUE-KEY>` chip when applicable, click a card
to expand its claims (with prompts) + dispatches inline. Backed by
`BoardService.ListAgents`, which bundles the claims and dispatches into
each `AgentCard` so the drill-down needs no second round trip.

---

## Claims, prompts, and the `taken` signal

A **claim** (`agent_claims`) records *"this session is focused on this
issue"*. Two pieces of state hang off it:

- **`prompt`** — the instruction/dispatch text the agent was working
  from when it claimed the issue. `bacio agent claim` accepts it via
  `--prompt` (flag path) or `"prompt"` (`--json` path); a re-claim with
  a fresher prompt updates it in place without writing a duplicate
  audit row. Empty when the claim was made without one.
- **`taken`** — a *derived* signal, never a stored column: an issue is
  taken iff it has at least one open (unreleased) claim. The claim rows
  are the single source of truth, so `taken` can't drift.

The **session list against an issue** is `store.ListClaimsForIssue` — a
query over `agent_claims` for that issue, open and historical, newest
first, each row carrying the session id, the agent identity slug, and
the prompt. It surfaces as `claimants` + `taken` on `bacio issue show`
and `bacio issue brief` (and the matching `bacio api` endpoints), in the
desktop issue drawer's **Claimed by** section, and in the TUI board
overlay's attachments pane. Busy status on the Agents screen is the same
data viewed session-first instead of issue-first.

---

## Claimed-issue state tracks agent idle/active

A claimed issue stays in lock-step with whether the agent is currently
working it. The signal is precise: the Claude Code `Stop` hook fires
exactly when the agent's turn ends and it parks waiting on the user — a
word-for-word match for `model.StateNeedsAction`. So:

- **`Stop` hook** → for each open claim the session holds, if the
  claimed issue is `in_progress`, flip it to `needs_action`.
- **`UserPromptSubmit` hook** → symmetric inverse: a fresh prompt
  arrived, so flip every `needs_action` claimed issue back to
  `in_progress`.

The flip is **edge-only** — only issues already in the matching "from"
state are touched, so a no-op turn writes nothing (no `SetIssueState`
call, no `issue.state` audit row, no `updated_at` bump). The shared
helper is `hookContext.syncClaimedIssueStates(sessionID, idle bool)` in
`internal/cli/hook.go`; every error path logs to stderr and never fails
the hook.

The kanban side-effect is free: the claimed issue naturally appears in
the `needs_action` column whenever its agent is parked, so the board
shows at a glance which jobs are waiting on you.

The Agents screens surface the same signal as a `waiting · <ISSUE-KEY>`
badge in place of the `busy` badge — waiting supersedes busy because it
points at the same issue and is the actionable state. The derive is
`model.SessionWaiting(openClaims, needsActionKeys)` — same shape as
`SessionBusy`, plus the key-set of issues currently in `needs_action`:

- **TUI** (`internal/tui/agents.go`) — `agentsView.needsAction` is
  populated by a per-reload `ListIssues(states=needs_action)` against
  the repo; `renderCard`/`viewDetail` render the amber `agentWaitingBadge`
  in preference to the blue `agentBusyBadge`, and the drill-down
  annotates each `(needs action)` claim line.
- **Desktop** (`desktop/boardservice.go`) — `BoardService.ListAgents`
  bulk-fetches every non-terminal issue per in-scope repo into a
  `key → state` map, so each `ClaimDTO` carries its `State` and the
  card carries `Waiting`/`WaitingIssue`. The frontend pill is
  `.mk-status-waiting` (`AgentsView.jsx` + `desktop.css`), painted with
  the needs_action column palette so the badge and the column read as
  the same thing.

Cost is purely cosmetic — every agent turn on a claimed issue toggles
its state, writing two `issue.state` audit rows + two `updated_at`
bumps. The audit rows are bounded by `HistoryRetention` (60 days). The
`updated_at` churn feeds git-backed sync's last-writer-wins; if it ever
bites, a quieter audit path (a non-recording store call, or recording
only the `in_progress → needs_action` edge) is the noted follow-up. A
`Notification`/`idle_prompt` hook tier is also possible as a future
stronger "idle for a while" signal layered on top.

---

## Agent identity & the `claude_pid` correlation

Dispatch delivery leans on one fact about the Claude Code process tree:
every `bacio` subprocess a session spawns — the `bacio hook` handlers
*and* the `bacio channel` server — descends from the same `claude`
process. That shared **`claude` pid** is the correlation key, because
Claude Code hands out the session id unevenly: the hooks get it in their
JSON payload, but the channel gets nothing except `CLAUDE_PROJECT_DIR`.

### `.bacio/agents.json`

A per-repo file (gitignored) mapping `claude_pid → identity`:

```json
{
  "46365": {
    "name": "curious-otter@claude.shiny",
    "host": "shiny.local",
    "sessions": ["a6ff7514-…", "b2c3d4e5-…"],
    "updated_at": "2026-05-15T04:42:04Z"
  }
}
```

One entry per `claude` process — which is what lets **multiple agents
share one repo**, each with its own addressable identity. The `sessions`
array collects the session ids that process has gone through (a fresh
one per `/clear`). Writes are atomic (temp-file + rename) with a few
retries; entries whose pid is no longer a live process are pruned on
every write. Code: `internal/cli/agentsfile.go`; the process-tree walk
is `findClaudeAncestor` in `internal/cli/proctree.go`.

### Who resolves identity, and how

| component            | session id?      | resolves identity by…                                                                                                  |
| -------------------- | ---------------- | ---------------------------------------------------------------------------------------------------------------------- |
| `bacio hook`         | yes (payload)    | walk to `claude_pid` → look up (or **mint**) in `agents.json`, then record the session id under that pid               |
| `bacio channel`      | no               | walk to `claude_pid` → look up in `agents.json`, **re-read every poll tick** (the entry is written by the session-start hook, which can race the channel's spawn) |
| any `bacio` CLI call | no               | walk to `claude_pid` → `agents.json`, so `actor()` attributes history to the agent and `resolveSessionID()` finds the session — no `--user` / `$CLAUDE_CODE_SESSION_ID` needed |

The session-start hook **mints** a fresh identity (a random
`adjective-animal@claude.host` slug, retried against the `agents.name`
UNIQUE constraint until it sticks) the first time it sees a `claude` pid
with no entry — so an agent never has to bootstrap its own identity by
hand.

### Channel presence — the `CHANNEL` column

The channel can't stamp an `agent_sessions` row directly (no session
id), so it records its own liveness in **`agent_channels`**, keyed on
`(host, claude_pid)` and heartbeated every poll tick. The hooks — which
*do* know the session id, and walk to the same `claude` pid — join that
back onto the session: `agent_sessions.claude_pid` is stamped and
`channel_seen_at` lit up whenever a fresh `agent_channels` row matches.
`bacio agent list` surfaces it as the `CHANNEL` column (`live` / `-`).
Stale `agent_channels` rows (dead pid) are pruned on store open.

---

## How a dispatch reaches the agent

A dispatch sits `pending` until the agent's session picks it up. There
are two delivery paths — an agent can use either or both.

### Pull — via hooks (default)

If the repo has `bacio install-hooks` set up, the **SessionStart** and
**UserPromptSubmit** hooks call `emitDrainedDispatches`
(`internal/cli/hook.go`): they drain the session's **un-acked**
dispatches (`pending` *and* `delivered`), flip any still-`pending` ones
to `delivered`, and print them to stdout — which Claude Code injects
into the agent's context. Because `delivered`-but-un-acked dispatches are
re-drained, a dispatch whose channel push never landed is re-surfaced on
the agent's next turn rather than silently lost — only an ack retires
it. Nothing to poll; the work shows up on the agent's next turn. (The
same hooks also mint the session's identity and link it to a running
channel — see [Agent identity](#agent-identity--the-claude_pid-correlation)
above.)

### Push — via a channel (real-time)

If the session runs `bacio channel` (an MCP-over-stdio server —
`internal/channel/`), dispatches arrive **live** as
`notifications/claude/channel` events the moment they're created. The
channel polls the dispatch queue (~3s) and pushes each new dispatch as a
`<channel source="bacio" dispatch_id="..." issue="..." mode="...">` tag.
`DrainAgentDispatches` returns un-acked dispatches (`pending` and
`delivered`) every tick; the channel dedups against an in-process
`pushed` set so it emits each dispatch once per process — but a *fresh*
channel process (a restart) re-pushes anything still un-acked, so a push
lost to a crash isn't stranded.

**Scoping.** A channel is *not* told its session id (see [Agent
identity](#agent-identity--the-claude_pid-correlation) above). It
resolves the **repo** from `CLAUDE_PROJECT_DIR` and its **agent
identity** by walking to its `claude` pid and looking that pid up in
`.bacio/agents.json` — **re-read on every poll tick**, not cached at
startup, because the session-start hook that writes the entry routinely
runs *after* the channel subprocess spawns (caching it once at startup
froze an empty identity for the whole session — the original delivery
bug). It then pushes the dispatches queued for that identity
(`DrainAgentDispatches`). A channel that can't resolve a repo or an
identity still starts — it just runs idle until a later tick resolves
it. (A bare `--session`-only dispatch with no agent identity therefore
can't reach a channel; it's delivered via the hook pull path, which
*does* know the session id.)

Wiring it up takes two steps, both handled by **`bacio install-channel`**
(`internal/cli/install_channel.go`):

1. it merges a `bacio` entry into the repo's `.mcp.json` so Claude Code
   knows how to spawn `bacio channel` (non-destructive — other MCP
   servers are preserved; `--yes` skips the confirmation prompt);
2. it prints the recommended launch command — channels are a Claude
   Code research preview, and a custom channel isn't on the Anthropic
   allowlist, so the session must opt in with the development flag.
   The shared activation banner emits a one-line copy-paste hint that
   also sets the activation env var and waives the per-tool approval
   prompt (BACI-49):

   ```bash
   BACIO_AGENT_MODE=1 claude --dangerously-skip-permissions --dangerously-load-development-channels server:bacio
   ```

   (Requires Claude Code v2.1.80+.)

Either way — pull or push — the agent sees the same thing: the issue,
the mode, and the composed payload.

---

## Subagent delegation (BACI-52)

The parent Claude Code session that owns the bacio channel is a **thin
scheduler**. Each dispatched job's file reads, edits, bash calls, and
intermediate scratchpad would otherwise pile up in the parent's
context window — and once that fills, the session falls over. Instead
the parent immediately delegates the work to a `Task`-spawned
subagent, waits for the subagent to return a one-line summary,
forwards that summary, and goes back to waiting for the next
`<channel>` event.

```
   <channel> tag arrives in parent
              │
              ▼
   parent reads tag + channel instructions, calls Task(
        subagent_type = "general-purpose",
        model         = "opus",
        prompt        = <worker contract + dispatch payload + issue ref>,
   )
              │
              ▼
   subagent (own context window):
       bacio agent claim <ISSUE> --prompt <payload first line>
       … Read / Edit / Write / Bash / Grep / Glob …
       bacio agent release <ISSUE>
       mcp__bacio__reply --dispatch_id <id> --note <summary>
       return to parent: <one-line summary>
              │
              ▼
   parent writes a short user-visible status line, awaits next <channel>
```

Per dispatch the parent consumes roughly *dispatch arrived → Task
call → short summary line*, not *all the work*.

### Subagents share the parent's session id

A `Task`-spawned subagent is **not** a separate Claude Code session.
It shares the parent's `CLAUDE_CODE_SESSION_ID`, the parent's `claude`
process (same PPID for spawned bash, so the same `.bacio/agents.json`
entry), the parent's task store (a `TaskCreate` call from the
subagent shows up in the parent's task list), and the parent's MCP
connections. The bacio MCP server cannot tell a subagent's
`mcp__bacio__reply` call apart from a parent's — both arrive under
the same session id, claim the same agent identity, and write into
the same audit-log actor. There is exactly one row in `agent_sessions`
per `claude` process; subagents do not get their own.

Practical consequences:

- The session-start hook's identity registration and the channel's
  `claude_pid`/identity resolution work unchanged.
- `bacio agent claim` / `release` / `reply` from inside the subagent
  attribute to the parent's session, which is what the registry
  expects.
- BACI-14's `Stop`-hook-flips-`in_progress`-to-`needs_action` path
  still works without changes: the parent's `Stop` hook fires when
  the parent's turn ends (after `Task` returns and the parent has
  written its summary), and that's the precise *agent parked* signal.
- No new schema rows, no `parent_session_pk` column, no per-subagent
  registry entries.

### Worker contract lives in the dispatch preamble

The full delegation contract is the **dispatch preamble** — a reserved
row (`slug = _dispatch_preamble`) in the `prompt_templates` table that
`model.ComposeDispatchPayload` prepends to every per-mode template body
at dispatch compose time. The agent receives the wrapper inside the
dispatch payload itself; there is no system-prompt-level instruction
to follow. The shape of a composed payload is:

```
<preamble: BACI-52 delegation wrapper>

---

<per-mode template body, rendered with {{issue_id}} etc.>

<optional --note appended after a blank line>
```

The embedded default body lives in
[`internal/model/prompttemplates/_dispatch_preamble.txt`](../internal/model/prompttemplates/_dispatch_preamble.txt);
fresh installs seed the row from that file. Existing DBs are brought
up to date by `backfillDispatchPreamble` in
`internal/store/store.go` — idempotent, runs on every `Open`, no-op
once the row is present (or has been deliberately deleted).

Editing the contract is the same flow as editing any other template:

- CLI: `bacio settings template show _dispatch_preamble` /
  `bacio settings template set _dispatch_preamble --body "..."`.
- TUI: open Settings (`s` from the main menu) → the preamble row
  leads the template list with the label "Dispatch preamble
  (prepended to every job)".
- Desktop: same row in the Settings panel.
- Restore to the embedded default: `bacio settings template reset
  _dispatch_preamble`.

There is deliberately **no** `.claude/agents/bacio-worker.md` file,
**no** `bacio install-worker` command, and **no** `//go:embed` of the
contract into a per-repo template. Two install steps —
`bacio install-channel` and `bacio install-hooks` — remain the only
setup story. Every dispatch picks up contract edits on the next
compose; nothing to re-deploy across consuming repos.

### Subagent tool surface and the TodoWrite-mirror gap

`general-purpose` has a restricted tool list relative to the parent:
Read, Edit, Write, Bash, Grep, Glob, `TaskCreate` (no `Task`, no
`TodoWrite`), plus every MCP tool the parent has connected. For the
work bacio dispatches care about — clone a worktree, edit code, run
tests, open a PR — that surface is sufficient.

The one **known limitation** is the `TodoWrite` gap: BACI-45's
`PostToolUse`-on-`TodoWrite` mirror cannot fire for delegated work,
because the subagent has no `TodoWrite` tool. The Agents view's
per-card `n/m done` badge therefore stays at whatever the parent
itself wrote — which is nothing, by design. Dispatch correctness is
unaffected. If the missing sub-step telemetry becomes painful in
practice, the follow-up is a small `mcp__bacio__todo_replace` MCP
tool the worker contract would call at each plan step (~30 lines
reusing `client.ReplaceSessionTodos`); the gap is documented here so
the next reader doesn't waste a spike on it.

### Trivial-dispatch carve-out (structural, not instruction-based)

Not every dispatch is worth a subagent. The channel's own
**setup-register** nudge (`from="bacio-channel"`) and the BACI-57
**idle-ping** probe (`from="bacio-channel-ping"`) are each a single
MCP call — calling `Task` to wrap them would cost more than the call
itself. They don't need a carve-out instruction in the wrapper
because they don't go through `ComposeDispatchPayload` at all:
`client.buildSetupDispatchPayload` and `client.buildPingDispatchPayload`
hand-roll their payloads and never see the preamble. The parent
agent reads a tag with no delegation wrapper attached and handles it
inline.

For any dispatch that does go through `ComposeDispatchPayload` —
which is every issue-tied per-mode dispatch (`plan` / `design` /
`implement` / `review` / `ship` / `fix_review`, plus any user-added
template) — the preamble is unconditionally prepended whenever the
template body is non-empty. An untyped dispatch (no template, just a
freeform `--note`) also skips the preamble: there's no work brief
worth delegating.

### Single concurrent, no mid-flight cancel

v1 leans on the parent's natural serial execution: an LLM in the
middle of a `Task` call processes its return before reading further
events. A second `<channel>` event arriving mid-job is queued by
Claude Code and read after the current `Task` returns — visible
effect: the second job starts when the first finishes. No
bacio-side per-session cap is enforced; the per-template
`prompt_templates.concurrency_limit` (BACI-51) is per-(repo, mode),
not per-session, and would over-serialise.

Cancelling a dispatch in flight (the TUI `X` keybind on a waiting
card, the desktop spinner-as-cancel button, `bacio agent cancel`)
clears the queued dispatch and resets `waiting_for_claim`, but
**does not stop a subagent already running**. That work runs to
completion; the user just won't see it surfaced through the dispatch
UI.

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
| Storage + migration         | `internal/store/schema.sql`, `internal/store/store.go`, `internal/store/dispatches.go`, `internal/store/channels.go` |
| Client surface              | `internal/client/client.go`, `internal/client/local_dispatch.go`, `internal/client/local_channel.go` |
| CLI                         | `internal/cli/agent.go`, `internal/cli/inputs/agent.go`               |
| Identity + `claude_pid`     | `internal/cli/agentsfile.go`, `internal/cli/proctree.go`              |
| Pull delivery (hooks)       | `internal/cli/hook.go`, `internal/cli/install_hooks.go`               |
| Push delivery (channel)     | `internal/channel/channel.go`, `internal/cli/channel.go`, `internal/cli/install_channel.go` |
| TUI Agents tab + picker     | `internal/tui/agents.go`, `internal/tui/board_dispatch.go`, `internal/tui/audit.go` |
| Desktop                     | `desktop/boardservice.go`, `desktop/frontend/src/components/AgentsPanel.jsx`, `desktop/frontend/src/components/IssueDrawer.jsx` |

See also: `docs/agent-cli-principles.md` (why `bacio hook` / `bacio
channel` are exempt from the six rules) and `SKILL.md` (the agent-facing
reference for `bacio agent dispatch` / `inbox` / `ack`).
