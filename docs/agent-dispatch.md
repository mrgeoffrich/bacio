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
at, a mode (a job stage — see [Mode](#mode-prompt-templates-and-the-payload)
below), and an optional note. The data is
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

On the board, select an issue and press **`x`**. A three-step picker
opens (`internal/tui/board_dispatch.go`):

1. **pick an agent** — the repo's live sessions. Busy sessions (see
   [Busy agents](#busy-agents--dispatch-target-eligibility)) render
   greyed with a `busy · working <ISSUE-KEY>` reason and are
   non-selectable — `j`/`k` skip them and `enter` refuses them.
2. **pick a mode** — the dispatch stages whose state-gate admits the
   focused issue's current state, rendered with their imperative action
   labels. For a `todo` issue that is **Plan**, **Design**, and
   **Implement**; for an `in_review` issue it is **Review**, **Ship**,
   and **Fix-review**. The list is built by
   `availableDispatchModes(store, issueState)`, so it tracks the
   templates' state-gates and any user-added templates rather than being
   a fixed set.
3. **add a note** — optional, free-form

Confirm and the dispatch is written + an `agent.dispatch` history row is
recorded. `x` opens the picker on any issue; when no template's
state-gate admits the issue's current state (`availableDispatchModes`
returns an empty list) the picker is suppressed with a one-line hint
instead. `confirmDispatch` re-checks the chosen session isn't busy
before writing, so a session that goes busy mid-picker is caught.

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

The dispatch dropdown on a `todo` card also surfaces **compound
"Primary, then Follow-on" rows** (BACI-209). Picking *Plan, then
Implement* queues the primary plan dispatch AND a dormant implement
follow-on against the brand-new parent in one transaction (via
`AutoDispatchIssueWithFollowOn` / `POST .../dispatch-chain` /
`bacio agent dispatch-chain`). The follow-on rides on the next
BoardCard refresh as `card.followOn` and the controller's BACI-179
promote sweep fires it once the parent settles — same path the
post-hoc `bacio agent queue-followon` uses. The primary row remains
the default; the chain rows are indented under each primary's
section header so the menu still groups by primary.

**BACI-217 — blockers-clear follow-on variant.** `bacio agent
queue-followon` (and `POST .../followon`) also accepts a blocked-but-
idle card and queues a second variant that waits for every issue on
the `to` side of an open `blocks` edge pointing at it to reach `done`
or `cancelled` before firing. The dispatch row carries
`queued_until_blockers_clear = 1` instead of `queued_after_dispatch_id`;
the controller sweep re-reads live blockers on every tick (so a new
`blocks` edge added after queueing extends the wait), and `bacio
history --op agent.followon.queue` stamps `gate=blockers` in Details
so the variant is distinguishable from the parent-acks default. The
two variants are mutually exclusive on a single row, and the
single-slot-per-issue invariant covers both — a card that is both
blocked and has an in-flight parent dispatch falls through to the
parent-acks variant.

**BACI-246 — defense in depth + audit-snapshot enrichment.** The
dormant gate is enforced at **two** points, not one: the controller's
`PromoteReadyFollowOns` sweep AND `BindQueuedDispatch`'s CAS. The
matcher's `ListQueuedByRepoMode` returns rows that passed the gate at
the *snapshot read*; if a blocker transitions back to a non-terminal
state in the tens of milliseconds between snapshot and bind, the
bind's WHERE clause carries `AND NOT dormantFollowOnGateSQL()` and
the CAS misses. The dispatcher logs the rare miss at Info
("matcher bind missed (row no longer bindable)") and the next
promote sweep re-evaluates the row. The blockers-clear promote audit
row also carries a `blockers=[KEY:state,...]` clause snapshotted from
the live `blocks` relation set at the moment the gate cleared —
materialised on `model.AgentDispatch.BlockerSnapshot` (transient,
never persisted) by the store's promote sweep and stamped into
`followOnDetails` by the controller. `bacio history --op
agent.followon.promote -o json` is therefore the diagnostic surface
for "which blockers did the gate observe?". An empty
`blockers=[]` clause means the `blocks` rows were hard-deleted
between queue and promote — the gate cleared by virtue of the EXISTS
subquery returning empty.

#### State-gated prompts

> **Pipeline cutover + BACI-300.** The state-gate mechanism below drove
> the per-card dispatch button on the **issues board, which is now
> removed** (the Pipeline is the only driving surface). The legacy manual
> dispatch surfaces that fed it are also gone: the `bacio agent dispatch`
> CLI verb, the TUI `x` dispatch picker, and the new-issue auto-scope
> chain were retired in BACI-300, along with the `in_progress` /
> `needs_action` states themselves. **Every** dispatch — including the
> triage passes (`scope`, `research`, `plan_large`) — now flows through
> the Pipeline: the controller engine queues each job's dispatch from the
> chosen process chain, the card stays `in_pipeline` throughout, and the
> engine advances the chain when the dispatch is acked. **Pipeline-stage
> workers don't manage state or tags**: `bacio agent release` is
> claim-drop only (no `--state`), and a paused job is signalled by an open
> question (or the engine's `engine_pause_reason`), not a state flip. The
> shared dispatch machinery the state-gate built on (`AddDispatch`, the
> matcher, the channel) is untouched — it is the path the engine uses too.
> The state-gate table below is retained for any user-added templates that
> still consult it.

Each dispatch stage (`plan`, `design`, `implement`, `review`, `ship`,
`fix_review`) declares the set of issue states its prompt is valid to
run from — its **state-gate**. (Historically the per-card action button
only offered a prompt when the card's state was in that stage's gate —
see the cutover note above.) Built-in defaults
(`model.DefaultPromptStates`):

| stage        | valid-from states |
| ------------ | ----------------- |
| `plan`       | `todo`            |
| `design`     | `todo`            |
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

| status    | meaning                                                         |
| --------- | --------------------------------------------------------------- |
| `active`  | alive and seen within `AgentLivenessThreshold` (10 minutes)     |
| `idle`    | alive but quiet longer than that — between turns, or harness shut |
| `errored` | the session's turn aborted on an Anthropic API error (see below) |
| `ended`   | the session called `bacio agent end` (or a hook ended it)       |

`errored` (BACI-296) supersedes `active`/`idle` but never `ended`: the
`StopFailure` hook stamps `errored_at` / `error_type` / `error_message`
on the session row when a turn dies on a transport error, and the Agents
view renders a red `errored:<type>` pill. The flag is **transient
supervision metadata, not terminal** — the next successful heartbeat
(the session takes a fresh turn = recovered) clears it. An errored
session is skipped by the dispatch picker (`AutoPickFreeAgent`) until it
recovers, so a worker that just died mid-turn isn't immediately handed
more work. See [StopFailure — recording API failures](#stopfailure--recording-api-failures).

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

## Claims are state-neutral (BACI-300)

A claim is a **focus marker**, not a state move. `bacio agent claim`
records that a session is working a ticket and stamps the assignee, but
it does not change the issue's state, and `bacio agent release` (with no
`--state`) drops the claim without moving it either. The `taken` signal
(an open `agent_claims` row) is what the kanban / Pipeline read to show
"someone's on this".

This retired the pre-pipeline lock-step machinery that used to drive a
claimed card through `in_progress → needs_action → in_progress` via the
`Stop` / `UserPromptSubmit` hooks. Those two states are **gone**: work
flows through the Pipeline now, and "the agent is parked waiting on the
user" is signalled by an open `ask_user_question` row on the ticket
(surfaced as the kanban-card question pill) — or, for an `in_pipeline`
card, by the engine's `engine_pause_reason` (`open_question` for a
worker question, `agent_error_transient` / `agent_error_terminal` for an
API failure). The `Stop` hook still heartbeats the session and clears a
stale errored flag; it no longer touches any issue's state.

The Agents screens render a single blue `busy · <ISSUE-KEY>` badge when a
session holds an open claim (`model.SessionBusy`) — there is no separate
"waiting" badge, because nothing flips a claimed card into a "waiting"
state anymore. A session blocked on a question still surfaces it through
the per-card `?N` question count.

## StopFailure — recording API failures

Anthropic occasionally returns transient transport errors (529
overloaded, 5xx, 429 rate_limit) and a dispatched worker that dies on one
just goes quiet: the session keeps its last liveness and the Pipeline job
it was running stays `running` forever (the engine only completes a job
on a dispatch `acked` and only halts on `cancelled`). The Claude Code
`StopFailure` hook fires *"when the turn ends due to an API error"* —
bacio wires it as `bacio hook stop-failure` (BACI-296) to record the
failure so it can't strand a card silently.

The hook is **observe-only**: Claude Code ignores its stdout and exit
code, so it can only record state, never retry in place. Recovery
(re-arming a paused chain, or the user fixing an auth/billing problem) is
a user/controller concern. The handler is fail-open like every other
`bacio hook` — a problem goes to stderr, never fails the turn.

Two best-effort halves run on every fire (`internal/cli/hook.go`,
`hookStopFailureCmd`):

1. **Mark the agent errored** — `MarkAgentErrored` stamps `error_type` /
   `error_message` / `errored_at` on the session row (always, regardless
   of whether a job was in flight). Surfaced as the `errored` liveness and
   the dispatch-picker exclusion above.
2. **Reconcile the in-flight Pipeline job** — `FailPipelineForSession`
   walks the session's open claims and, for each whose issue is
   `in_pipeline` with a running job, drives `pipeline.Engine.FailRunning`,
   which branches on the error class:

Both error classes pause the chain **in place** (BACI-300): cancel the
running job + dispatch, halt Auto, and stamp a distinct
`engine_pause_reason` — the card stays `in_pipeline` either way. The class
only selects the pause reason so the Pipeline UI can word the halt
differently:

| error class | `error_type` values | `engine_pause_reason` | what the user sees |
| --- | --- | --- | --- |
| **transient** | `server_error`, `rate_limit` | `agent_error_transient` | "API outage — Start to retry once it clears". |
| **terminal** | everything else, incl. `unknown` (conservative default) | `agent_error_terminal` | "Account / billing / auth error — fix it, then Start". |

Neither auto-retries (re-binding into a sustained outage or a billing
failure just tight-loops); the user re-arms with Start/Auto.
`model.IsTransientAPIError` is the single source of truth for the branch.
(Pre-BACI-300 the terminal class yanked the card out of the pipeline to
`needs_action` via a dedicated guard-bypassing store method; that state —
and the method — are gone.)

**Recovery clear.** The errored flag is cleared edge-only on the next
`Stop` / `UserPromptSubmit` heartbeat (`hookContext.clearErrorOnRecovery`)
— a session that takes a fresh turn has recovered. A session that errors
and never takes another turn is still reaped by the BACI-57 idle-pinger
(`presumed_dead`), so the flag can't strand a card indefinitely.

**Correlation.** The `StopFailure` `session_id` is the supervisor's; the
in-flight Pipeline job is found via the session's *open claim* (the
subagent claims under the same session id). A worker that died before
claiming has no job to reconcile — the handler records the agent-errored
state and returns. Older Claude Code builds that don't emit `StopFailure`
simply never fire it; nothing else changes (no version gate needed).

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
| any `bacio` CLI call | no               | walk to `claude_pid` → `agents.json`, so `actor()` attributes history to the agent and `resolveSessionID()` finds the session — no `$CLAUDE_CODE_SESSION_ID` or any explicit identity flag needed |

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

If the repo has `bacio install-agent` set up, the **SessionStart** and
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

Wiring it up takes two steps, both handled by **`bacio install-agent`**
(`internal/cli/install_agent.go`, which performs the channel step
alongside the hook and agent-file steps):

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

### Channel MCP tools

The `bacio channel` MCP server exposes four tools:

| Tool                     | Purpose                                                                 |
| ------------------------ | ----------------------------------------------------------------------- |
| `reply`                  | Acknowledge a dispatch (the channel-native form of `bacio agent ack`).  |
| `register`               | Complete the session's registration (links the channel to a session id).|
| `ask_user_question`      | Surface a **blocking** multi-choice clarification question to the user (BACI-53). |
| `send_user_notification` | Fire a **non-blocking** agent→user notification into the bell (BACI-287). |

`reply`, `register`, and `send_user_notification` advertise
unconditionally — none of them park a JSON-RPC reply, so the
poller-gate that defers `ask_user_question` (a parked reply would never
be delivered without the drain step) does not apply to them. None of the
four has a REST or CLI mutate-equivalent — they are channel-only (the
notification read side — list / mark-read — is REST, but the *write* is
channel-only).

> **Retired tool — `attach_transcript` (BACI-85, removed in BACI-307).**
> The channel used to expose a fifth tool, `attach_transcript`, that the
> supervisor called after each `Task` returned to link the subagent's
> raw `.jsonl` transcript to the issue as a document. The
> `reverse-proxy-monitor` feature replaced it: BACI-305/306 capture and
> parse every agent-mode session's Anthropic traffic into `proxy_messages`,
> read per-job via `bacio proxy job <dispatch_id>` (REST:
> `GET /proxy/jobs/{dispatch_id}/transcript`). BACI-307 removed the tool,
> the dispatch-preamble step that invoked it, and the per-issue `.jsonl`
> transcript-doc plumbing.

`send_user_notification` is the deliberate non-blocking sibling of
`ask_user_question`: the agent tells the user something and carries on
immediately (no parked reply, no `pending` entry). The user sees it in
the notification bell (desktop / web topbar) and the TUI Notifications
tab, with an unread badge and per-item / mark-all read. Its `issue_id`
arg is **optional** — present, the notification deep-links to that
ticket; absent, it's a ticket-less heads-up. Reach for it when the user
should *see* something; reach for `ask_user_question` when you need them
to *answer* something.

**MCP arg naming — `issue_id` (BACI-128).** `ask_user_question` takes
the canonical issue key as a required `issue_id` arg — without one it
parks an orphan row that the kanban-card pill surface filters out, so
the channel hard-rejects a missing or malformed value with an MCP tool
error before any row is inserted. Internally bacio still calls the
column `issue_key`; the MCP tool surface alone uses `issue_id` for
consistency.

### User→agent steer messages — `kind="message"` (BACI-286)

A **steer message** is the inverse of a dispatch: a free-form note the
*user* pushes at a *specific busy session* — the worker running a job
right now — so they can nudge it mid-flight without queuing fresh work.
It rides the **same** `notifications/claude/channel` notification a
dispatch uses, but carries `meta.kind = "message"` and **no
`dispatch_id`** — so the worker (and a channel-log reader) tell a steer
message apart from a dispatch by the kind attribute alone.

It is emphatically **not a dispatch**: no `agent_dispatches` row, no
matcher, no job-engine, no target binding, no ack lifecycle. Those would
all try to bind it to a *free* agent; a steer message is scoped to one
*busy* `session_id` by construction. It is **fire-and-forget** — there is
nothing to `reply`/ack, and `consumed_at` (the channel drained + pushed
it) is the only delivery signal, the same "delivered ≠ acted on" caveat a
dispatch's `delivered` carries.

**Delivery is turn-boundary only.** The channel injects context into the
worker's loop at its next turn boundary; it cannot interrupt a running
tool call (no abort, no mid-tool interrupt). A message fired while the
worker is mid-`Bash`/`Task` lands once that call returns. This is
acceptable by design — the affordance is for steering, not stopping.

**The data path.** A new `user_messages` table (keyed by `session_pk`,
cascaded out by the `agent_sessions` ON DELETE chain — never synced)
holds the queue. The REST endpoint `POST
/agents/sessions/{session_id}/messages` (body `{"body": "..."}`) is the
only write surface — a steer message is a harness affordance, not a
`bacio` mutation verb, so it gets **no `bacio agent message` CLI verb and
no `bacio schema` entry** (same six-rule split as the BACI-190 rescue
endpoint). It writes one `agent.message` audit row. The channel drains it
on its own tick step — `Source.DrainUserMessages`, a sibling of
`DrainAnsweredQuestions`, walking the same `SessionsByClaudePID` set —
marks the returned rows `consumed`, and pushes each via `pushMessage`. A
per-process dedup set mirrors the dispatch drain's `pushed`; because
`consumed_at` is stamped in the same transaction the rows are returned, a
fresh channel process never re-pushes a consumed row (consumed is
terminal, unlike a dispatch's re-drainable `delivered`).

**The two surfaces.** Both the desktop/web **Agents-page session card**
(targets `a.sessionId`, gated on `a.hasChannel`) and the **Pipeline
running-job card** (targets `card.runningSessionId`, only while a job is
`running`) carry a **Message** button that opens an inline compose box and
POSTs to the endpoint. The Pipeline card needs the bound session
surfaced, so `boardcards.BoardCard` gained a `RunningSessionID` field
populated from the winning open claim's session — the same claim whose
`ActiveVerb` / `Todos` / `OpenQuestions` the card already projects.

**Stop reuses this path (BACI-291).** The Pipeline **Stop** control isn't
just engine bookkeeping: because a delivered worker keeps running its
`Task` subagent in the background (BACI-130 — a delivered dispatch can't be
cancelled), cancelling the job alone doesn't stop the work. So
`pipeline.Engine.StopRunning` *also* enqueues a canned wind-down note
(`stopWorkerSteerBody`) as a steer message at the running worker's newest
open claim session — the same `AddUserMessage` write the Message button
uses. It's best-effort: no open claim (the dispatch was never delivered, or
the worker already released) is a silent skip, and a write error is
logged-and-continued so Stop never fails on the steer. Delivery is the same
turn-boundary mechanism above — Stop *asks* the worker to wind down at its
next turn, it can't hard-kill the subagent.

**Spike note (the harness-routing unknown).** The channel-owning session
is the *supervisor*, which blocks inside `Task(...)` while the per-mode
worker subagent runs (see [Subagent delegation](#subagent-delegation-baci-52)
below). The open question BACI-286 set out to answer is *where* a pushed
`kind="message"` notification lands when the channel owner is suspended in
a subagent: only in the supervisor's queued context (useless — it arrives
after `Task` returns, work already done), or in the *active subagent's*
loop at its next turn boundary (the goal). Per
"[Subagents share the parent's session id](#subagents-share-the-parents-session-id)",
a `Task`-spawned subagent shares the parent's MCP connections, so the
notification reaches the same channel — but Claude Code's routing of that
notification into the active subagent loop is harness behaviour bacio
doesn't control. The channel-level wire shape is locked by
`TestChannelPushesUserMessage`; confirming the *worker hears it* requires
a live end-to-end (dispatch a long job, fire a steer message, grep the
worker's transcript for the `<channel kind="message">` tag) against an
installed binary with the supervisor's channel restarted on it.

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
   parent reads tag + preamble, calls Task(
        subagent_type = "bacio-<mode>-worker",   (from the stub)
        prompt        = <tiny stub: ticket + mode + dispatch_id>,
   )
              │
              ▼
   subagent (own context window; per-mode brief is its SYSTEM PROMPT):
       bacio agent claim <ISSUE> --prompt <mode>
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

The per-mode brief is **not** in the dispatch payload (BACI-76). It is
the system prompt of a per-mode custom subagent — one of
`bacio-design-worker`, `bacio-plan-worker`, `bacio-implement-worker`,
`bacio-review-worker`, `bacio-ship-worker`, `bacio-fix-review-worker`
— generated into `.claude/agents/` by `bacio install-agent`. The
payload the parent receives is just the rewritten preamble plus a
short stub naming the ticket, the mode, and the subagent type to
spawn. See "Worker contract" below.

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
- The parent's `Stop` / `UserPromptSubmit` hooks heartbeat the
  parent's session (the subagent shares it), keeping the supervisor
  live across the `Task` call. (Pre-BACI-300 the `Stop` hook also
  flipped a claimed `in_progress` card to `needs_action`; that flip
  and both states are retired — the hooks no longer touch issue
  state.)
- The parent's *own* tool calls (Task's return PostToolUse, every
  `mcp__bacio__*` call, every Bash) heartbeat the supervisor while
  the subagent runs (BACI-159 PostToolUse hook). This heartbeats the
  supervisor *between* its tool calls, but a single long-running
  `Task`/`Bash` call (e.g. a `./build.sh` + Playwright smoke phase)
  can still starve `last_seen_at` for the duration of that one call —
  the parent's PostToolUse hook only fires when the call returns. The
  BACI-159 graduated 40-min claim-holder cutoff is the backstop here
  (BACI-271): the idle-pinger's reap branch waits for `last_seen_at`
  to cross that cutoff rather than reaping on the proactive probe's
  no-ack window, so a claim-holding supervisor blocked in one long
  call is not force-ended at ~12 min.
- No new schema rows, no `parent_session_pk` column, no per-subagent
  registry entries.

### Worker contract: per-mode subagent system prompts (BACI-76)

The delegation contract has two pieces, both editable as
`prompt_templates` rows:

1. **The dispatch preamble** — a reserved row (`slug =
   _dispatch_preamble`) that `model.ComposeDispatchPayload` prepends
   to every typed dispatch. It instructs the parent supervisor session
   to spawn the per-mode subagent named in the stub. It stays in the
   dispatch payload — the parent supervisor is not a custom subagent,
   so it has no agent file. It is small and identical on every
   dispatch.
2. **The per-mode brief** — the `design` / `plan` / `implement` /
   `review` / `ship` / `fix_review` rows. Each brief is the **system
   prompt of a per-mode custom subagent**, written to
   `.claude/agents/bacio-<mode>-worker.md` by `bacio install-agent`.

The shape of a composed payload (BACI-76) is the preamble plus a tiny
stub — no brief body:

```
<preamble: spawn the per-mode subagent>

Ticket: BACI-76
Mode: implement
Subagent: bacio-implement-worker

<optional --note appended after a blank line>
```

The `dispatch_id` is **not** in the stub — the channel already emits it
as the `<channel dispatch_id="...">` tag attribute, and the preamble
tells the parent to read it from there and hand it to the worker.

The worker's `Task` prompt is a **fixed verbatim stub**, not a
supervisor-composed paragraph (BACI-103). The preamble instructs the
parent to copy the stub's `Ticket:` and `Mode:` lines verbatim and
append exactly one `Dispatch ID: <n>` line read from the `<channel>`
tag — no rephrasing, no free-form prose. The only variable content
reaching the worker is those three values; the per-mode agent file is
the single source of truth for what to do.

**Why this shape.** Before BACI-76 the whole per-mode brief (the
`design` brief is ~10K tokens) was interpolated into every dispatch's
`Task(...)` prompt. Because the brief was interpolated per-issue and
prefixed per-dispatch, every dispatch produced a unique content block
that never hit the prompt cache — the supervisor paid full prefill
TTFT on every spawn. Moving the brief into the subagent's system
prompt makes it a stable prefix across back-to-back same-mode spawns
(prompt-cache eligible within the 5-minute TTL) and shrinks the
per-dispatch channel `content` roughly an order of magnitude. The
subagent type is derived from the slug by
`model.SubagentTypeForTemplate` (`fix_review` → `bacio-fix-review-worker`).

The `bacio install-agent` command renders one
`.claude/agents/bacio-<mode>-worker.md` per dispatchable template from
the current `prompt_templates` rows. Each generated file's frontmatter
carries the agent `name` (== file basename == `subagent_type`) and
`model: opus`; its body is the template body verbatim. The briefs are written verbatim, *not*
`{{token}}`-rendered — a system prompt is fixed per agent type and
cannot embed a specific issue id, so the six built-in briefs were
rewritten to refer to "the ticket named in your dispatch prompt". A
leftover `{{` in a body is a packaging bug — `model.RenderAgentFile`
rejects it (and a build-time test guards the built-ins).

The embedded default bodies live in
[`prompts/agents/`](../prompts/agents/) — one `.md` file per slug,
with shared blocks (`_preamble.md`, `_postamble.md`) inlined via the
`{{> name}}` include directive expanded by `model.ExpandPromptIncludes`
at load and render time. Fresh installs seed the rows from those files
with the directives already expanded. Existing DBs are brought
up to date by `backfillDispatchPreamble` (inserts the preamble row if
absent) and `refreshDispatchPreamble` (rewrites the old `general-purpose`
preamble to the new spawn-the-subagent default, but only when the user
never customised it) — both in `internal/store/store.go`, both
idempotent.

Editing a brief is the same flow as before — `bacio settings template
set <slug> --body "..."`, the TUI Settings tab, the desktop Settings
panel — but the body is now a generated artefact's source. **After
editing a body, run `bacio install-agent` to regenerate the agent
file**; until then the dispatched worker still uses the previous
brief. `bacio status` reports the per-template agent-file freshness
(`up-to-date` / `missing` / `stale`) so a forgotten re-run is visible.

Setup is now one install step: `bacio install-agent` renders the agent
files, merges bacio's Claude Code hooks, and registers the bacio
channel MCP server — all in a single invocation with one plan/confirm.

(This reverses the pre-BACI-76 decision — recorded here for the next
reader — that there should be *no* `.claude/agents/` file and *no*
install verb. The prompt-cache / TTFT win made the per-mode subagent
the better seam.)

### Feature handoff comments (BACI-124)

The `bacio-implement-worker` close-out includes a fourth step (after
"create a PR", before "drop the worktree"): if the dispatched issue
belongs to a feature, the worker posts a chronological handoff note on
the **parent feature** via `bacio feature comment add`. The note
captures three buckets — files of context, deviations from the
original plan, and work deferred / scoped out — so the next worker on
a sibling issue in the same feature inherits the context this run
built up rather than re-deriving it from the transcript.

Feature comments live in their own table (`feature_comments`) and are
addressed under `bacio feature comment {add,list,rm}` / `GET POST
DELETE /repos/{prefix}/features/{slug}/comments[/{uuid}]`. They sync
through the same git-backed round-trip as issue comments, just rooted
at `<featureFolder>/comments/` rather than `<issueFolder>/comments/`.
`bacio feature show <slug>` surfaces them in the `comments` field of
its FeatureView response.

Plan-mode and design-mode workers do not yet post handoff comments —
the same need applies and is tracked as a follow-up.

### Worktree-isolated workers (#114)

Each generated `.claude/agents/bacio-<mode>-worker.md` carries an
`isolation: worktree` line in its frontmatter (`model.AgentFileIsolation`,
`internal/model/agentfile.go`). Claude Code reads that and runs every
dispatched worker in its **own throwaway git worktree** — created when
the subagent spawns, removed automatically on a clean finish. The worker
never runs `git worktree add` / `remove` itself, and concurrent
dispatches can never edit each other's files.

This is **complementary to**, not a replacement for, `bacio worktree
init`. The two isolate different things:

- Claude Code's `isolation: worktree` isolates the **filesystem** — each
  worker gets a clean checkout.
- `bacio worktree init` isolates **bacio's own state** — a separate
  SQLite DB and API port, so the worker's `bacio` calls don't clash on
  `~/.bacio/db.sqlite` or `127.0.0.1:5320` with the user's running TUI /
  desktop / other workers.

So every per-mode brief still opens by running `bacio worktree init`
*inside* its Claude-Code-provided worktree: the worktree gives it an
isolated filesystem, and `bacio worktree init` gives that worktree its
own bacio environment. The worker tears the bacio environment down with
`bacio worktree rm` on close; Claude Code removes the git worktree
itself. See [`docs/worktree-environments.md`](worktree-environments.md)
for the bacio-side of that isolation.

#### Worktree bootstrap onto the resolved base branch (BACI-229)

Claude Code's `isolation: worktree` always branches the throwaway
worktree from whatever the **local** `main` HEAD points at — it does
not consult bacio, and exposes no `isolation.branch` config to
override the source commit. That leaves two failure modes a worker
inherits unless something rebases it onto the right tip before it
starts editing:

1. **Stale local main.** On a machine that mostly dispatches and
   rarely pulls, local main can be days behind `origin/main` —
   every worker then starts from an old base, propagating into PRs
   that rebase noisily and can land on top of bugs already fixed
   upstream. Closed by **BACI-237**.
2. **Wrong base branch.** When an issue's feature has
   `branch_name = feat/foo` (BACI-225) and the resolver picks that
   as the dispatch base (BACI-226), the worker's worktree is still
   branched from local main — three commits short of the feature
   tip. Closed by **BACI-229**, this section.

The fix is worker-side, in [`prompts/agents/_preamble.md`](../prompts/agents/_preamble.md)
step 4 — a "Position the worktree on the resolved base branch"
step that every dispatched worker runs before reading project
conventions or making any edit. It reads the `<base_branch>` tag
from the worker's Task prompt (forwarded verbatim from the
dispatch envelope by **BACI-226**'s dispatch_preamble) and:

- **`base_branch == "main"`** — `git fetch origin main` +
  `git merge --ff-only origin/main`. The fast-forward is expected
  because Claude Code just branched from local main; a rejection is
  a real signal that something tampered with the worktree branch.
- **`base_branch != "main"`** (a feature branch) —
  `git fetch origin <base_branch>` +
  `git reset --hard origin/<base_branch>`. The throwaway
  `worktree-agent-<hash>` branch has no committed work yet, so the
  reset is safe — functionally a checkout of the feature tip onto
  the same branch name. The branch name itself stays
  `worktree-agent-<hash>`; bacio does not rename it, and the PR's
  source-branch name is cosmetic. `merge --ff-only` is not used
  here because a feature branch will (correctly) refuse to
  fast-forward off of local main.

A `git fetch` failure on a non-main base means
`origin/<base_branch>` does not exist on the remote — either the
feature's `branch_name` is a typo, or nobody has pushed the feature
branch yet. The worker surfaces the message and stops. It does not
fall back to `main`, and it does not `git push -u origin
<base_branch>` to create the branch — the user fixes the missing
branch and re-dispatches. This matches BACI-237's "rejection is a
real signal" precedent for the main case.

An absent `<base_branch>` tag (issue-less dispatches, pre-BACI-226
envelopes) is treated as `main` — same fallback the
dispatch_preamble documents for the supervisor's Task-prompt
forward.

Post-run cleanup is unaffected: Claude Code removes the worktree
when the subagent finishes regardless of which commit HEAD was at,
and `bacio worktree rm` drops the bacio environment without
inspecting git state. The preamble step is the only authoritative
source of the bootstrap behaviour; every worker file inlines it
through the `{{> _preamble}}` include, and
`bacio install-agent --reset-templates` regenerates them all from
the embedded default after a preamble edit.

### Worktree+branch safety guard (BACI-91)

`isolation: worktree` is what Claude Code is *supposed* to honour — but
nothing verifies it actually did. If worktree isolation silently fails,
or a brief is somehow run in a non-worktree context, a dispatched
worker would otherwise claim the ticket and start editing/committing on
the **primary checkout's main branch**.

To make that failure mode loud, `model.RenderAgentFile` prepends a
shared **worktree safety guard** (`model.WorktreeGuardPreamble`,
`internal/model/agentfile.go`) to *every* generated agent file's body —
before the template's own `## Setup` section. The guard instructs the
worker, as its very first action, to verify with `git rev-parse` that:

- it is in a **linked worktree** — `--git-dir` and `--git-common-dir`
  resolve to *different* paths (they are identical in the primary
  checkout); and
- the current branch is **not** the repo's main branch (`main` /
  `master`).

If either check fails the worker aborts immediately — no bacio skill,
no claim, no state change, no edits, no commits — and returns a clear
message that the dispatch must be re-run with proper worktree
isolation.

The guard is centralised in `prompts/agents/_preamble.md` and inlined
into every built-in body via a `{{> _preamble}}` include at the top of
each `prompts/agents/<slug>.md` source file. The include is resolved
by `model.ExpandPromptIncludes` at both load time (so the seeded body
already carries the guard) and at `RenderAgentFile` time (so a
user-customised body that keeps the directive still resolves it). A
custom template that omits the directive deliberately gets no guard —
this is the explicit-opt-in trade-off of the include model. It is a
*soft* (prompt-level) guard: editing a template body and re-running
`bacio install-agent` regenerates the `.claude/agents/` files with the
guard already in place.
Covered by `TestRenderAgentFileCarriesWorktreeGuard` /
`TestRenderAgentFileBuiltinsCarryGuard`.

The guard also mandates two **process-layer** mitigations (BACI-116):
the worker's *first* `TaskCreate` task must be an explicit "Establish
working directory" step that records the worktree-root prefix verbatim,
and the worker must re-run `git branch --show-current` immediately
before `git commit` and before `git push`. These raise the floor and
leave an audit signal — they do not *enforce*; the PreToolUse hook
below does.

### Worktree confinement — the PreToolUse `Write|Edit|Bash` hook (BACI-116, BACI-129, BACI-134)

The worktree+branch guard above is a one-time **cwd snapshot**: it
proves where the worker stood at startup, but it does not constrain
where later `Read`/`Edit`/`Write` calls point. Those tools take an
absolute `file_path` — cwd is irrelevant to them — and a dispatch
worktree lives *inside* the repo it branches from, so a parent-repo
absolute path is always a valid, existing file. A worker can therefore
silently do every edit / commit / push in the primary checkout while
its startup check reported "I'm in a worktree" (this is exactly the
BACI-102 failure).

The enforcement is a sixth `bacio hook` subcommand, `pre-tool-use`,
wired by `bacio install-agent` as a **PreToolUse** hook with matcher
`Write|Edit|Bash`. Two sibling deciders share the hook entry: Write/Edit
goes through the worktree-confinement guard below, Bash goes through the
sqlite3 confinement guard (BACI-134) further down. Only one deny ever
leaves the hook so Claude Code's surface stays uncluttered — Write/Edit
checked first, then Bash.

#### Write/Edit confinement (BACI-116, BACI-129)

On every `Write`/`Edit` call the Write/Edit decider:

1. classifies `cwd` via `git rev-parse --git-dir` vs `--git-common-dir`
   into one of (a) not-in-a-repo (allow), (b) primary worktree (deny
   every edit — the main-checkout escape closed by BACI-129), or
   (c) linked worktree (allow edits under its root). BACI-116
   originally consulted bacio's `wtenv` manifest layer here, which
   left supervisors in the main checkout uncontained; BACI-129
   replaced that with the manifest-free git classification;
2. resolves the tool's `file_path` (symlink-evaluated, boundary-safe
   prefix test) against the linked-worktree root;
3. **denies** the call — `permissionDecision: "deny"` with a
   `permissionDecisionReason` that names the relevant root verbatim —
   either because the cwd is the primary checkout (BACI-129) or
   because the path resolves outside the linked-worktree root
   (BACI-116), so the model self-corrects and retries.

This collapses the whole BACI-102 chain at the first edit: deny the
edit → the parent checkout stays clean → a later `cd <main> &&
git commit` commits nothing → the push carries nothing.

Bash is **not** routed through this decider: `tool_input.command` is a
raw string and parsing it for paths in general is fragile. Confining
the write tools defuses the file-confinement Bash escape for free —
with the parent checkout kept clean, a stray `cd <main> && git commit`
has nothing to commit.

#### Bash `sqlite3` confinement (BACI-134)

A different escape class lives in Bash that the Write/Edit confinement
above doesn't cover: a worker can shell straight to `sqlite3
~/.bacio/db.sqlite "DELETE ..."` and mutate the live shared store
without ever taking a `Write`/`Edit` path. Every audit-log `history`
row depends on the SQL going through `internal/store/*` paths, so a
raw `sqlite3` invocation drops the row through the floor — the
incident that motivated BACI-134 had a dispatched worker delete two
tickets this way after `bacio issue rm` refused them, leaving orphaned
`issue.create` / `agent.dispatch` / `agent.claim` rows pointing at gone
issues.

The Bash decider (`decideBashSqlite3`) is **path-based, not
verb-based**. It tokenises `tool_input.command`, finds the first token
whose basename is `sqlite3`, then walks for the first non-flag token
after it — the DB path argument. Strips surrounding quotes, expands a
leading `~/` against `$HOME`, resolves relative paths against `cwd`,
symlink-evals both sides, and **denies iff** the candidate resolves to
the live shared DB (`~/.bacio/db.sqlite`, via `wtenv.DefaultDBPath`).
Anything else — a worktree-isolated `<worktree>/.bacio/db.sqlite`, a
`/tmp/scratch.db`, no path at all — is allowed by virtue of not
matching. There is no verb-based carve-out: even a `SELECT 1` against
the live DB is denied, because raw SQL on the shared store is not a
path a dispatched worker should reach for (the `bacio` read verbs and
`bacio history` cover legitimate diagnostics).

The decider is deliberately **fail-open on every ambiguous shell
construct**: a pipe (`|`), command substitution (`$(...)`), backtick
substitution, env-var or glob in the candidate path (`$X`, `*`, `?`,
`{`, `(`), a `~user/...` form — each falls through to allow. A worker
that actively wants to evade the guard can:
`bash -c "sqlite3 ~/.bacio/db.sqlite ..."`,
`eval "sqlite3 ..."`, `BACIO_DB=$HOME/.bacio/db.sqlite; sqlite3
"$BACIO_DB" ...`, heredoc-pipe stdin, etc. The plan explicitly accepts
these as fail-open by design — the guard's job is to make the obvious
bypass loud, not to be a general bash sandbox. The brief layer's
"ask via `mcp__bacio__ask_user_question` instead of reaching for raw
SQL" rule is the complementary nudge.

The supervisor-mode angle: a session that has `BACIO_AGENT_MODE=1` set
(the BACI-129 case) is also gated by this hook, including the
sqlite3 confinement. That's the right behaviour — a supervisor that
needs to mutate the live DB by hand should drop
`BACIO_AGENT_MODE` for that shell.

#### Shared invariants

Both deciders honour the same **fail-open** invariant as every other
hook: stdin unreadable, JSON malformed, resolver error, symlink eval
failure — every failure mode *allows* the call (exit 0, no deny
payload) and logs one line to stderr. A `deny` is emitted only on a
positive "resolved outside a known worktree root" / "this command
targets the live DB" determination. The pure decision functions
`decidePreToolUse` and `decideBashSqlite3` (`internal/cli/hook.go`)
are unit-tested directly (`hook_pretooluse_test.go`), as is the
boundary-safe `pathWithin` helper.

### Subagent tool surface

The generated agent files carry **no `tools:` line**. Omitting the
field makes Claude Code give each dispatched worker the parent
session's full tool set — every code-work tool, the `Task*` task-list
family, `Skill`, `WebFetch`/`WebSearch`, and every MCP surface the
supervisor has connected (including the bacio channel tools).

This drops the earlier BACI-76 allowlist. That allowlist was uniform
across the six modes and named ~15 tools explicitly; in practice it
kept costing dispatched workers tools they legitimately needed (a brief
that referenced a skill or an MCP surface the list didn't enumerate),
and the failure mode of a missing tool is *silent* — the worker just
can't do the step, with no error pointing at the allowlist. Inheriting
the full set is the simpler, more robust default; `model.RenderAgentFile`
no longer emits the field at all.

BACI-45's `PostToolUse`-on-`TaskCreate`/`TaskUpdate` mirror fires for
subagent task-list calls too — they share the parent's task store.

### Terminal title — `PostToolUse` on `mcp__bacio__register` (BACI-147)

`bacio install-agent` wires a second `PostToolUse` group alongside the
task-list mirror, matcher `mcp__bacio__register`, command
`bacio hook set-title`. The moment the agent calls the bacio channel's
`register` tool — i.e. the moment the session's identity slug is
written into `.bacio/agents.json` keyed on the `claude` pid — the
hook resolves that slug and writes the standard
`ESC ] 0 ; <slug> BEL` OSC sequence to `/dev/tty`, so the host
terminal's window title flips to the agent's slug
(`brave-koala@claude.shiny`). An operator with several dispatched
workers open in tabs can tell them apart at a glance.

The OSC bytes go to `/dev/tty`, **not** `stdout` — Claude Code injects
hook stdout into the model's context, and a stray escape would land in
the transcript. Gated by `BACIO_AGENT_MODE` like every other hook, so
human-driven sessions don't get retitled. Fail-open everywhere: a
missing `/dev/tty` (CI, headless pipe), an open or write failure, or
a still-empty `agents.json` slug (the rare race where `register` fires
before the entry lands) — each logs one stderr line and returns
nil. MVP scope is slug-only; appending the open-claim issue key into
the title and resetting it on `SessionEnd` are noted follow-ups.

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

#### Reaper cadence + re-queue on `presumed_dead` (BACI-133, BACI-159)

The BACI-57 idle-pinger runs on a tightened **20 minute** cadence
(`model.AgentIdlePingThreshold`, was 1 h). With
`AgentPingNoAckTimeout = 2 min` the worst-case end-to-end reap window
is ~22 min from last heartbeat to force-end — tight enough that the
autonomous recovery beats a user noticing the silence and intervening
manually. The same constant doubles as the BACI-58 staleness window
in `store.CountInFlightByMode`, so a delivered-but-unacknowledged
dispatch on a quiet session drops out of the per-(repo, mode)
concurrency cap promptly.

**Heartbeat sources.** `agent_sessions.last_seen_at` is the sole
liveness signal the reaper consults. It is bumped from four places,
each one a different "the agent is still here" signal:

| source | when | where |
|---|---|---|
| `UserPromptSubmit` hook | turn boundary — the user just typed | `internal/cli/hook.go` |
| `Stop` hook | turn boundary — the agent just stopped speaking | `internal/cli/hook.go` |
| **`PostToolUse` hook** (BACI-159) | every supervisor tool call — empty matcher | `internal/cli/hook.go:hookPostToolUseHeartbeatCmd` |
| `AckDispatch` | the agent (or a `Task`-spawned subagent under the same session id) acked a dispatch | `internal/store/dispatches.go` |

The BACI-159 `PostToolUse` heartbeat closes the failure mode where a
long `Task`-spawned subagent run has no parent-side turns until it
returns: Claude Code fires the subagent's `PostToolUse` against the
*subagent's* transcript, never the supervisor's, so without this
heartbeat the supervisor's `last_seen_at` could fall past
`AgentIdlePingThreshold` while the subagent was still doing real
work, and the reaper would force-end the session and auto-requeue
its in-flight dispatch. The new hook fires on the supervisor's own
tool calls (every `Task` return, every `mcp__bacio__*`, every Bash,
every Read/Edit/Write), keeping `last_seen_at` fresh as long as the
parent is meaningfully active. The hook is stdout-silent by design
— Claude Code merges PostToolUse stdout into the supervisor's
context, so any byte leaked there would land in the transcript.

**Graduated cutoff for claim-holders.** Per BACI-159 a session that
holds at least one open claim gets `ClaimHolderIdlePingMultiplier ×
AgentIdlePingThreshold = 40 min` as its effective reap cutoff
instead of the base 20 min — a real worker mid-job gets double the
slack before the reaper fires. The pinger calls
`OpenClaimsForSession` per alive session per tick (single indexed
session-keyed query) and picks the cutoff via the pure helper
`effectiveIdleCutoff(now, openClaims)`. A genuinely wedged
claim-holder is still eventually reaped — just on a 2× clock.

**Proactive 10-min probe.** Per BACI-159 the pinger also runs a
lighter probe at `AgentProactiveProbeThreshold = 10 min` — a "still
there?" prod well before the reap gate. The probe enqueues the same
`bacio-channel-ping` dispatch via `EnsurePingDispatch` (idempotent
on a session-keyed already-pending row), so a claim-holder that
doesn't respond is fine: the agent's eventual ack bumps
`last_seen_at`, and the 40-min graduated reap gate is what
eventually decides "presumed dead", not the probe itself.

When the reaper force-ends a session with `reason=presumed_dead`,
the dispatch cascade inside `EndAgentSession` no longer cancels the
session's still-open (`queued`/`pending`/`delivered`) dispatches —
it **re-queues** them. Each touched row flips back to
`status='queued'` with `target_session_id=''` and
`target_agent_id=NULL`, so the BACI-51 matcher rebinds the row to a
fresh live agent on its next tick. Per-row audit lands as
`agent.dispatch.requeue` (kind `agent`, actor
`bacio-channel-ping`), so `bacio history --op agent.dispatch.requeue`
or `--user-filter bacio-channel-ping` returns a coherent
reaper-activity ledger. The linked issue's
`waiting_for_claim` flag stays set — a queued row is exactly the
case the flag exists for; the next `AddAgentClaim` clears it the
usual way.

Every other end reason keeps today's cancel-on-end semantics
(hook-driven `stop`/`clear`/`logout`/`other`, operator-driven
`bacio agent end`, the BACI-100 `superseded` phantom-row dedupe).
The store-side `DispatchCascadeMode` parameter enforces the pairing
— `DispatchCascadeRequeue` with any reason other than
`presumed_dead` is rejected at the boundary. The cascade is derived
from `reason` inside `EndAgent` (client + API handler), so the
public JSON surface on `agent.end` is unchanged.

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

### Two display strings per template (BACI-67)

`prompt_templates.action_label` is the imperative button text the
dispatch action menus render ("Plan", "Design", "Implement"); the
gerund `name` ("Planning", "Designing", …) keeps feeding the
lower-cased activity-pill derivation on a taken card. Setting
`action_label` doesn't affect the activity verb, and the matcher /
state-gate / payload composition are all keyed on `slug`, so a
rename of either string is purely cosmetic.

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
