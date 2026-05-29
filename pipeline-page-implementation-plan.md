# Pipeline Page — Implementation Plan

> Companion to [`pipeline-page-requirements.md`](pipeline-page-requirements.md)
> (the functional spec) and [`pipeline-page-impact-analysis.md`](pipeline-page-impact-analysis.md)
> (the codebase map). This doc turns those into an **ordered, shippable build
> plan**. Each phase compiles, passes tests, and leaves the existing issues board
> working until the deliberate cutover in Phase 5.

## Sequencing strategy

Two load-bearing primitives the whole redesign hangs on (per the impact
analysis): the **two new issue states** (`in_pipeline` / `to_be_shipped`) and a
**persisted job collection** (`pipeline_jobs`) owned by a controller engine.

The impact analysis (§8) flags the biggest risk: *"removing worker state
authority (engine + prompts must land together or workers and engine race)."*
This plan **dissolves that coupling** with a single store-side guard introduced
in Phase 1:

> **Engine-governed states.** Once an issue is in `in_pipeline` or
> `to_be_shipped`, the controller engine owns its state. Worker/agent state
> writes that target a *processing* state (`in_progress` / `needs_action` /
> `in_review`) — i.e. `ReleaseAgentClaim(--state …)` and `bacio issue state` —
> are **no-op'd** (the claim still releases; an audit row records the ignored
> transition). Deliberate column moves (`todo`↔`in_pipeline`↔`to_be_shipped`↔
> `done`) still work.

With that guard in place the **legacy prompts keep working against pipeline
cards without fighting the engine**, so prompt simplification (Phase 6) becomes
pure cleanup rather than a correctness dependency. Issues on the old board
(`todo` / `in_progress` / …) are untouched by the guard and progress exactly as
they do today.

Result — a safe order where every step is independently shippable:

| Phase | Title | Ships behind |
|---|---|---|
| 1 | Model & store foundation | additive; nothing creates the new states yet |
| 2 | Controller job-engine | engine only governs `in_pipeline`; nothing creates them yet |
| 3 | API + MCP + bindings | new endpoints unused by current UI |
| 4 | Pipeline frontend (board still present) | new nav page alongside the old board |
| 5 | Cutover — delete the board | Pipeline becomes the only driving surface |
| 6 | Prompt & CLI cleanup | strip dead state/tag steps; retire superseded verbs |

---

## Design decisions baked in

Resolving the open items from requirements §11 and impact analysis §9. The first
three are recommendations carried into the plan; the last is the one assumption
to **confirm before Phase 1** (see [Needs confirmation](#needs-confirmation)).

1. **`pipeline_jobs` is its own table**, not JSON on `issues` — queryable, and a
   clean FK target for the re-parented questions.
2. **States enforced in Go, CHECK dropped via table rebuild.** Follows the exact
   precedent of `migrateAgentDispatchesModeCheck` /
   `migrateAgentDispatchesStatusCheck` (rebuild table, drop the CHECK,
   `ParseState` guards the boundary). Avoids a fragile per-state CHECK migration
   forever after.
3. **Feature mandatory via store-boundary enforcement**, not a SQL `NOT NULL`
   flip. Backfill NULL `feature_id` → repo default; `AddIssue` applies the
   default; no risky table rebuild for nullability.
4. **`start` / `stop` / `auto` are API/engine actions, not agent CLI verbs.**
   Keeps the CLI lean (impact analysis §9). The CLI gains only what an agent/
   human genuinely types: `issue reorder`, `issue process set`, `issue ship`,
   `issue auto-ship`.
5. **Ship is a dedicated hand-off, distinct from the ship *agent*.** Three
   "ship" things stay separate (impact analysis §0): the in-process `Ship`
   *stage* and the in-pipeline *Ship button* both just move the card to
   `to_be_shipped` (no agent); only the **shipping-column SHIP / auto-ship**
   dispatches the `ship`-mode agent.
6. **Superseded verbs/endpoints are retired in Phase 6, not deleted early** —
   keeps the old board functional until cutover.

---

## Phase 1 — Model & store foundation

**Goal:** every persistence primitive exists and is enforced, with zero
behavioural change to the running product (nothing yet puts an issue into the new
states).

### Deliverables
- **New states** in `internal/model/state.go`: add `StateInPipeline` /
  `StateToBeShipped` to the constants, `allStates`, and (free) `ParseState`.
  Audit every `switch` on `model.State` across the tree (CLI labels, TUI
  columns, `boardcards` mapping, terminal-state detection) — the two-state
  fan-out the impact analysis warns about. Grep `model.State` consumers.
- **CHECK rebuild** `migrateIssuesStateCheck` in `internal/store/store.go` +
  drop the CHECK from `issues.state` in `schema.sql` (keyed off stored CREATE
  TABLE SQL, same shape as `migrateAgentDispatchesStatusCheck`).
- **Ordering**: `issues.priority INTEGER NOT NULL DEFAULT 0` (idempotent ALTER) +
  `idx_issues_state_priority` on `(state, priority)`. `Issue` gains `Priority` in
  `internal/model/types.go`. Add `Store.ReorderIssue(key, position)` that
  rewrites priorities within a `(repo, state)` band transactionally.
- **`pipeline_jobs` table** + `internal/store/pipeline_jobs.go` CRUD. Shape:
  `id`, `issue_id` FK (CASCADE), `sequence`, `mode`, `status`
  (`pending|running|complete|cancelled`), `dispatch_id` FK→`agent_dispatches`
  (`ON DELETE SET NULL`, null until it runs), `created_at/started_at/
  completed_at`, `UNIQUE(issue_id, sequence)`. Per-issue engine fields
  `engine_mode` (`off|auto`) + `engine_pause_reason` (null|`open_question`) as
  columns on `issues` (simpler than a sidecar). `ship` is a sentinel job mode
  (the hand-off), recognised by the engine, never dispatched.
- **Preset processes** as an in-code enumeration in `internal/model` (Plan→
  Implement→Ship, Implement→Ship, Plan→Implement, Plan, Implement) with a
  constructor that materialises the `pipeline_jobs` rows.
- **Questions re-parent**: add `pipeline_job_id` FK (nullable, `ON DELETE SET
  NULL`) to `agent_session_questions` + index. Legacy rows stay NULL.
- **Feature mandatory + bootstrap**: backfill NULL `feature_id` → repo default
  (creating it if needed); `AddIssue` applies the default when none given;
  bootstrap `bugs` + `maintenance` features with auto-close off (`state_manual=1`)
  and `default_feature_id` → `maintenance` on first repo use / `init`.
- **The engine-governed-state guard** (the sequencing keystone): in
  `internal/store/agents.go`, `ReleaseAgentClaim` and `SetIssueState` ignore a
  transition *into* a processing state when the issue is `in_pipeline` /
  `to_be_shipped` (claim still released; audit row records the no-op). Unit-test
  this directly — it is what lets legacy prompts coexist with the engine.

### Out of scope
No engine, no API, no UI. `bacio issue state <KEY> in_pipeline` becomes legal
(free from `ParseState`) — that is the manual hook Phase 2 tests against.

### Risks & mitigations
- **Feature backfill correctness** — run it (and bootstrap) *before* any code
  reads `feature_id` as mandatory; cover the "issue created pre-default" path.
- **Two-state fan-out** — a missed `switch` shows as a card in no column or a
  panic; the grep-every-consumer step is the guard.
- **Terminal-state detection** (`terminal_at`, archive sweep) must treat the two
  new states as **non-terminal**.

### Definition of done
`go test ./...` green; new store tests for `pipeline_jobs` CRUD, `ReorderIssue`,
the feature-default backfill, and the engine-governed-state guard. `bacio issue
state <KEY> in_pipeline` round-trips on a migrated copy of a real DB.

---

## Phase 2 — Controller job-engine

**Goal:** the engine advances a card's job chain end to end for `in_pipeline`
cards, queuing ordinary dispatches the existing matcher binds. Old board flow
untouched.

### Deliverables
- **Job-engine ticker** (leader-gated, `QueueMatchInterval` cadence) added to
  `internal/controller/controller.go` as a new `…IfLeader` helper + goroutine,
  mirroring `FollowOnSweepIfLeader`. Per `in_pipeline` issue, read its
  `pipeline_jobs` + `engine_mode`:
  - **Start**: flip the next `pending` job → `running`, queue an
    `agent_dispatch` for its mode (matcher binds it), stop.
  - **Auto**: when the running job's dispatch is **acked**, mark the job
    `complete` and queue the next; when the last non-`ship` job completes (or the
    `ship` sentinel stage is reached), move the issue → `to_be_shipped`.
  - **Halt**: if the current job has an open question, set
    `engine_pause_reason=open_question` and do nothing until it clears.
- **Auto-ship ticker** (leader-gated): for the top `to_be_shipped` card with
  auto-ship on, dispatch a `ship`-mode agent; on ack advance → `done`.
- **`EngineAdvanceJob`** store method (`internal/store/pipeline_jobs.go`):
  CAS-safe per-job transition + the issue hand-off, superseding the `finalState`
  arm of `ReleaseAgentClaim` for pipeline cards. Completion is detected by the
  engine (dispatch acked), not chosen by the worker.
- **Audit rows** per job-completion / hand-off / auto-ship, matching the
  `agent.bind` / `agent.followon.*` convention already in the controller.
- **Reuse, unchanged**: matcher, idle-pinger, archive sweep, leader service,
  channel/hook delivery. The engine just queues dispatches.

### Dependencies
Phase 1 (states, `pipeline_jobs`, engine fields, the state guard).

### Risks & mitigations
- **Engine vs. matcher concurrency** — both leader-gated; per-issue atomicity via
  CAS on job status / engine fields (the matcher's `BindQueuedDispatch` pattern).
- **Legacy follow-on sweep overlap** — the engine governs only `in_pipeline`;
  the follow-on sweep governs queued-with-gate dispatches. Disjoint sets; verify
  no issue is driven by both.
- **Stuck jobs** — a dispatch that never acks must not wedge the chain; rely on
  the existing idle-pinger / dispatch retention, and make "running with a dead
  dispatch" observable.

### Definition of done
A scripted test (`bacio issue state … in_pipeline`, attach a process via the new
store method, set `engine_mode=auto`) drives a card through plan→implement→
`to_be_shipped`→`done` with the engine queuing each dispatch and an open question
halting/resuming Auto. Controller unit tests for the new tickers.

---

## Phase 3 — API + MCP + bindings

**Goal:** every engine/ordering action is reachable over HTTP and Wails, and the
card shape the UI consumes carries the job collection.

### Deliverables
- **New routes** in `internal/api/router.go` (inherit CORS→auth→actor→bodyCap):
  - Reorder: `PUT …/cards/reorder` (backlog + shipping) → `Store.ReorderIssue`.
  - Process: `POST …/issues/{key}/process` (assign a preset),
    `GET …/issues/{key}/jobs`.
  - Job control: `POST …/issues/{key}/jobs/{seq}/(start|stop|cancel)`, engine
    toggle `PUT …/issues/{key}/auto`.
  - Ship: `POST …/issues/{key}/ship` (hand-off → `to_be_shipped`),
    `PUT …/issues/{key}/auto-ship`.
- **`boardcards.BoardCard` grows** (`internal/boardcards/cards.go`): a `Jobs`
  collection (seq, mode, status), a `CurrentJob`, `EngineMode`/`PauseReason`, and
  the open question carries its `pipeline_job_id`. Derive `ActiveVerb` /
  `TodosDone` from the current job. Add the two states to the column mapping /
  `stateLabels`.
- **MCP `ask_user_question`** (`internal/channel/channel.go`): associate the new
  question with the session's **current pipeline job** (session → active
  dispatch → its job row), so an open question lands on the job and trips the
  engine halt. Answer/cancel return job context.
- **Wails seam** (`internal/client/` + `desktop/bindings/`): mirror every new
  method so desktop and web stay in lockstep.
- **CLI verbs** that warrant typing (full agent-CLI treatment — `*Input`,
  `bacio schema` entry, validators, `--dry-run`, `SKILL.md` line):
  `bacio issue reorder`, `bacio issue process set`, `bacio issue ship`,
  `bacio issue auto-ship`. (`start`/`stop`/`auto` stay API/engine-only.)

### Dependencies
Phases 1–2.

### Risks & mitigations
- **Mixed/legacy card shape** — `boardcards` read paths must tolerate issues with
  no `pipeline_jobs` and session-parented (NULL-job) questions.
- **Two transports drift** — add each method to `api.ts` *and* `api.http.ts` in
  the same change (Phase 4 consumes both).

### Definition of done
`bacio web --no-open` serves cards with the job collection; curl/playwright
exercises reorder, process-set, ship, auto toggles; the MCP question lands on the
job row (engine halts). Bindings regenerate cleanly via `./build.sh`.

---

## Phase 4 — Pipeline frontend (board still present)

**Goal:** build the real Pipeline page to the agreed mockups, mounted alongside
the still-present issues board, so it can be dogfooded before cutover.

### Reference
[`pipeline-card-mockups.html`](pipeline-card-mockups.html) is the authoritative
card design; [`pipeline-page-mockups.html`](pipeline-page-mockups.html) is the
page/column frame. Where they differ on in-pipeline card detail, the card mockup
wins.

### Deliverables
- **`components/PipelineView.jsx`** promoted from mock to real, keyed on the new
  states (replace the local `placement` Map / hardcoded `NEXT_STAGE` with
  server state):
  - **Backlog**: priority order, vertical scroll, drag-to-reorder
    (`reorderBacklog`); expanded drawer = multi-column grid filling
    top-to-bottom, right-to-left with **top-right number badges** (1 = next, at
    top-right) shown while expanded.
  - **In Pipeline**: **process menu over the card** on entry (drag-in or zap);
    inner issue card (key/title/tags) strictly separated from the **surrounding
    processing area** (job-chain rows with in-process/done, open-question
    indicator, active job's todo/task list); **Start / Stop / Auto / Ship**
    controls along the bottom per the card mockup.
  - **Shipping**: FIFO from top, drag-to-reorder, **auto-ship toggle**, manual
    **SHIP** (dispatches the ship agent).
- **API seam**: add the Phase-3 methods to both `src/api.ts` and
  `src/api.http.ts` (`reorderBacklog`/`reorderShipping`, `getIssueJobs`,
  `setProcess`, `startJob`/`stopJob`/`cancelJob`, `setAutoMode`, `shipIssue`/
  `setAutoShip`; extend `setIssueState` for the two new states).
- **Reuse** `KanbanCard`, `QuestionModal`, `ActivityTray`, `ShippedPopover`,
  `DispatchMenuContent` / `dispatchMenuRows` — fold into the Pipeline rather than
  duplicate.
- **Features page** (`components/FeaturesView.jsx`): add done/cancelled issue
  browsing — the new home for non-pipeline issues (every issue now has a feature).
- **Composer / workspace** (`IssueComposer.jsx`, `IssueWorkspace.jsx`): feature
  picker, default pre-selected (features mandatory).

### Dependencies
Phases 1–3.

### Risks & mitigations
- **Shared desktop/web tree** — smoke-test via `bacio web --no-open` + the
  `playwright-cli` skill (golden path + drag/reorder/question/halt edge cases);
  per `CLAUDE.md`, UI correctness needs a browser, not just type-check.
- **Drag semantics now server-backed** — reorder must be optimistic but
  reconcile to server priority on refresh.

### Definition of done
Pipeline drives a card backlog→in_pipeline (process menu)→running→halt-on-
question→resume→`to_be_shipped`→ship, verified in a browser. Board still works
on `/issues`.

---

## Phase 5 — Cutover: delete the issues board

**Goal:** the Pipeline becomes the only board surface; the state-column kanban and
its whole work-management layer are removed (requirements §10).

### Deliverables
- **Delete** `components/Board.jsx`, `boardCollapsePersistence.ts`,
  `boardCompactPersistence.ts`; the `/issues` board route and the "Issues" nav
  tab (`App.jsx`, `lib/routes.ts` `board`↔`/issues`, `Topbar.jsx` NAV). Keep the
  `/issues/:key` workspace route and ⌘K search.
- **Remove the board-only callbacks** wired through `App.jsx`: `onMoveCard`,
  `onDispatchFromCard`, `onDispatchChainFromCard`, `onCancelWaitingCard`,
  `onSetFollowOn`, `onCancelFollowOn`, `onQuickEval`, collapse/compact state.
- **Confirm "what still needs a home"** (requirements §10.1): create via the
  `+` composer; view/edit via the workspace; non-pipeline issues browse via the
  Features page; ⌘K for direct lookup.

### Dependencies
Phase 4 (Pipeline must be a complete replacement first).

### Risks & mitigations
- **Orphaned API endpoints** — the board-only move/dispatch-from-card/follow-on/
  quick-eval endpoints lose their caller; mark for retirement in Phase 6 (don't
  delete server-side here — keep the change frontend-only and revertible).
- **Lost affordances** — diff Board's feature set against Pipeline before delete;
  anything still wanted (e.g. quick-eval) folds into the Pipeline card.

### Definition of done
App has no `/issues` board route; all driving happens on the Pipeline; create/
view/edit/browse paths verified in a browser; nav shows Pipeline, not Issues.

---

## Phase 6 — Prompt & CLI cleanup

**Goal:** remove the now-dead worker state machinery and retire superseded verbs/
endpoints. Pure cleanup — the engine has owned state since Phase 2.

### Deliverables
- **Prompts** (`prompts/agents/*.md`): remove the `--state` arg on `agent
  release` (release becomes claim-drop only), the `bacio tag add …
  <planned|implemented|…>` step, and the `_postamble.md` post-reply `bacio issue
  state … in_progress` hop. `ship.md` stays (dispatched from the shipping column)
  but loses `--state done`. Then **`bacio install-agent`** to regenerate
  `.claude/agents/bacio-*-worker.md`; keep `internal/model/prompttemplates`
  built-ins in sync. Update `docs/agent-dispatch.md` and the review-mode-final-
  state convention.
- **CLI**: drop the `--state` requirement on `bacio agent release` (remove the
  worker's state authority cleanly — not a silent no-op). Decide retire-vs-keep
  on `agent queue-followon` / `cancel-followon` / `dispatch-chain` (superseded by
  the in-pipeline chain).
- **API**: retire the now-callerless board-only endpoints (move / dispatch-from-
  card / dispatch-chain / waiting-dispatch / follow-on / quick-eval) flagged in
  Phase 5, or document why they stay.

### Dependencies
Phases 2 + 5 (engine owns state; board gone).

### Risks & mitigations
- **A prompt body edited without re-install** silently keeps the old behaviour —
  `bacio status` reports per-template agent-file freshness; the install-agent
  step is mandatory (CLAUDE.md tripwire).
- **Retiring follow-on** — confirm no dormant legacy rows depend on the sweep
  before removing it; safest to keep the sweep, remove only the user-facing verbs.

### Definition of done
A fresh dispatched worker (plan/implement/ship) runs with no state/tag steps; the
engine advances the chain; `bacio install-agent` clean; `docs/agent-dispatch.md`
matches the new close-out.

---

## Cross-cutting risks & test strategy

- **Migration on a real DB.** Each schema phase must run against a *copy* of a
  populated `~/.bacio/db.sqlite` before install (CHECK rebuild, feature backfill,
  priority/jobs/question-FK adds). The stale-binary tripwire applies: after every
  rebuild + install, restart every long-running bacio.
- **Worker/engine coexistence (Phases 2–5).** The engine-governed-state guard
  (Phase 1) is the single point that keeps legacy prompts from fighting the
  engine — it deserves the most thorough unit coverage and a live cross-check in
  Phase 2.
- **Three "ship" meanings.** Keep the hand-off (stage + in-pipeline button →
  `to_be_shipped`) distinct from the ship *agent* (shipping column) in code,
  tests, and UI labels.
- **Two-transport parity.** Every API method lands in `api.ts` and
  `api.http.ts` together; `./build.sh` regenerates Wails bindings.

## Needs confirmation

1. **Default feature on a new install** (requirements §11 / §8.1): plan assumes
   `maintenance` as the repo default with `bugs` as the second bootstrapped
   catch-all, both auto-close off. Confirm before Phase 1.
2. **Retire vs. keep** the follow-on / dispatch-chain CLI verbs and the
   board-only API endpoints once the UI caller is gone (Phase 6). Plan keeps the
   sweep, retires the user-facing verbs — confirm.
