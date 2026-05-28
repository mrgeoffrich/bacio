# Pipeline Page — Impact Analysis

> Companion to [`pipeline-page-requirements.md`](pipeline-page-requirements.md).
> Maps the requirements onto the codebase: every subsystem touched, with file
> pointers and the concrete change. Line numbers are pointers, not contracts —
> they drift. Produced from a parallel scan of store, CLI, API/MCP, controller,
> frontend, and prompts.

## 0. Reading guide — one clarification first

Three things called "ship" must not be conflated:

| Thing | Where | What it does |
|---|---|---|
| **`Ship` stage** in an in-pipeline process | `in_pipeline` job chain | **Hand-off only** — moves the card to `to_be_shipped`. **Dispatches no agent.** |
| **in-pipeline Ship button** (Auto off, last job done) | `in_pipeline` card | Same hand-off → `to_be_shipped`. |
| **SHIP button / auto-ship** | `to_be_shipped` column | **Dispatches a `ship`-mode agent** against the top card. |

So the `ship`-mode worker (`ship.md`) is dispatched from the **shipping column**,
not from inside the in-pipeline chain.

## 1. Cross-cutting summary

| Subsystem | Headline impact |
|---|---|
| **Schema / model** | +2 issue states; new `priority` column; new `pipeline_jobs` table; questions re-parented to a job; `feature_id` becomes mandatory + bootstrap. |
| **CLI** | State parser extends; new `issue reorder`, job-collection verbs, ship/auto-ship toggle; `agent release --state` and the follow-on/dispatch-chain verbs lose their role. |
| **API + MCP** | New endpoints for reorder, job control, ship; `boardcards` card shape grows a job collection + current job + per-job question; `ask_user_question` parents onto a job. |
| **Controller** | New leader-gated **job-engine** ticker + **auto-ship** ticker; the engine (not the worker) advances state; follow-on sweep superseded. |
| **Frontend** | Delete the issues board + board-only helpers; build the Pipeline processing UI; Features page grows done/cancelled browsing; composer enforces a feature. |
| **Prompts** | Strip state-transition and done-tagging steps from every mode body; agents stop managing state; re-run `install-agent`. |

The two **load-bearing primitives** the whole redesign hangs on:
1. **New issue states** `in_pipeline` / `to_be_shipped` (touches every layer).
2. **A persisted job collection** (`pipeline_jobs`) owned by a controller engine,
   replacing worker-driven `release --state` progression.

---

## 2. Schema & data model — `internal/store/`, `internal/model/`

### Current state
- Issue-state enum: `internal/model/state.go` (`type State`, the six constants, `ParseState` accepting dash/space/underscore variants). A SQL `CHECK (state IN (...))` mirrors it in `internal/store/schema.sql` (issues table).
- Migrations: single `migrate()` in `internal/store/store.go`, run at DB open — idempotent `ALTER`s guarded by `columnExists()`. Fresh DBs get `schema.sql`; older DBs get the ALTERs.
- `issues`: has `feature_id` **nullable** FK; **no** ordering/priority column.
- `agent_dispatches`: `mode`, `status` (`queued|pending|delivered|acked|cancelled`), `issue_id`, follow-on flags (`queued_after_dispatch_id`, `queued_until_blockers_clear`). No job-chain concept.
- Questions: `agent_session_questions` — parented to the **session** (`session_pk`), carries `issue_key`, `payload_json`, `answers_json`, `state`.
- `repo_settings.default_feature_id` (BACI-235); feature `state_manual` bit gates auto-close (BACI-250).

### Changes required
- **Two new states** in `state.go` (constants + `allStates` + `ParseState`). The `CHECK` constraint can't be `ALTER`ed in SQLite — either rebuild the table (rename/copy/drop) or **drop the CHECK and enforce in Go** (recommended; lower-risk migration).
- **Ordering**: add `issues.priority INTEGER NOT NULL DEFAULT 0` + index on `(state, priority)`. Used only by `todo` (backlog) and `to_be_shipped` (shipping).
- **New `pipeline_jobs` table** (the per-issue process chain). Recommended shape:
  `id`, `issue_id` FK, `sequence`, `mode` (`plan|implement|…`; `ship` is the hand-off sentinel), `status` (`pending|running|complete|cancelled`), `dispatch_id` FK→`agent_dispatches` (null until it runs), `created_at/started_at/completed_at`, `UNIQUE(issue_id, sequence)`. Plus per-issue engine fields: `engine_mode` (`off|auto`) and `engine_pause_reason` (null | `open_question`) — either columns on `issues` or a sidecar row.
  - Keep `agent_dispatches` for the actual agent run so the **matcher is reused**; a job points at its dispatch when running.
  - The preset processes (Plan→Implement→Ship, …) are an **in-code enumeration** (`internal/model`), not stored.
- **Questions re-parent**: add `pipeline_job_id` FK (nullable, `ON DELETE SET NULL`) to the questions table; an open question on the current job is the halt signal. Legacy rows keep `pipeline_job_id` NULL.
- **Feature mandatory**: backfill NULL `feature_id` → repo default (or bootstrapped `maintenance`), then enforce NOT NULL (SQLite needs a table rebuild for the nullability flip, or enforce in Go at the store boundary).
- **Bootstrap**: on first repo use / `init`, create `bugs` + `maintenance` features with auto-close **off** (`state_manual=1`) and set `default_feature_id` → `maintenance`.

### Migration risks
- **CHECK rebuild** vs Go-side enforcement — pick the latter to avoid a risky table copy.
- **Feature backfill** of existing feature-less issues must run before NOT NULL.
- **Existing dispatches/questions**: legacy `agent_dispatches` and session-parented questions won't have job rows — read paths must tolerate the mixed/legacy shape.
- Adding states means revisiting **terminal-state detection** (`terminal_at`), archive-sweep filters, and any state-machine assumptions.

Files: `internal/model/state.go`, `internal/model/types.go` (Issue gains `Priority`), a new `internal/model` job/process type, `internal/store/schema.sql`, `internal/store/store.go` (migrations), new `internal/store/pipeline_jobs.go` (CRUD), `internal/store/questions.go`.

---

## 3. CLI — `internal/cli/`, `internal/cli/inputs/`

### Current state
- Groups registered in `internal/cli/root.go`: `issue`, `feature`, `agent`, `settings`, `tag`, `comment`, `pr`, `schema`, …
- `bacio issue state` → `model.ParseState` (`internal/cli/issue.go`). Extends for free once `state.go` knows the new states.
- `bacio agent release` **requires `--state`** (`internal/cli/agent.go`) — this is the worker-driven progression the engine replaces.
- Follow-on chaining: `agent queue-followon` / `cancel-followon` / `dispatch-chain` (BACI-180/209).
- `bacio settings default-feature` (BACI-235) and `bacio feature auto-close` (BACI-250) already exist.
- Six agent-CLI rules (`docs/agent-cli-principles.md`): every new mutating verb needs a `*Input` struct, a `bacio schema` registry entry, lean list output, store-boundary validation, `--dry-run`, and a `SKILL.md` line.

### Changes required
- **State parser**: free extension once the model knows `in_pipeline` / `to_be_shipped`.
- **New mutating verbs** (each = `*Input` + schema entry + validators + `--dry-run`):
  - `bacio issue reorder <KEY> --position N` (backlog/shipping ordering).
  - Job-collection verbs — e.g. `bacio issue process set <KEY> --process <preset>` (creates the pending jobs), `bacio issue job list/start/stop/cancel` (or fold control under an engine verb). `start`/`stop`/`auto` may be **controller-internal** rather than user CLI — decide whether these are agent-facing verbs or only API/engine actions.
  - `bacio issue ship <KEY>` and an auto-ship toggle (`bacio issue auto-ship <KEY> on|off`) — or reuse `agent dispatch --mode ship` once the card is in `to_be_shipped`.
- **`agent release`**: drop the `--state` requirement — workers release the claim only; the engine owns the next state. (Don't keep `--state` as a silent no-op; remove the worker's state authority cleanly.)
- **Follow-on / dispatch-chain verbs**: superseded by the in-pipeline process chain. Keep for legacy/manual use or retire — decide.
- **Mandatory feature + bootstrap**: enforced in the **store** layer (issue add applies the default; init seeds `bugs`/`maintenance`), so mostly no new CLI verb — `default-feature` already exists.

Files: `internal/cli/issue.go`, `internal/cli/agent.go`, `internal/cli/inputs/issue.go`, `internal/cli/inputs/agent.go`, `internal/cli/schema.go`, `internal/cli/feature.go`, `internal/cli/settings.go`, plus `prompts/SKILL.md`.

---

## 4. HTTP API + MCP channel — `internal/api/`, `internal/channel/`, `internal/boardcards/`

### Current state
- Routes in `internal/api/router.go`. Board-relevant: `GET /repos/{prefix}/cards`, `PUT …/issues/{key}/state`, dispatch endpoints (`POST …/agents/dispatches`, `POST …/issues/{key}/dispatch`, `…/dispatch-chain`, `…/waiting-dispatch`), follow-on (`POST/DELETE …/issues/{key}/followon`), questions (`GET …/sessions/{id}/questions`, `POST …/questions/{id}/answer|cancel`), features hide + `default-feature`, `shipped` + `shipped/count`, board hidden-states (BACI-248).
- `internal/boardcards/cards.go` assembles `BoardCard` (the shape the UI consumes): `Column`/state, `WaitingState`, `ActiveVerb`, `TodosDone/Total`, `OpenQuestions`, `Todos`, `FollowOn`, `LatestPlan/PR`, etc. One `ListIssues` + `ListOpenClaims` + per-repo dispatch/session/todo reads.
- MCP channel (`internal/channel/channel.go`): tools `register`, `reply`, `ask_user_question` (requires `issue_id`, parks the question, drains answers), `attach_transcript`. `SessionQuestion` model in `internal/model/questions.go`.
- Auth: bearer-token middleware (`internal/api/middleware.go`); new endpoints inherit CORS → auth → actor → bodyCap automatically.

### Changes required
- **New endpoints**:
  - Reorder: `PUT …/cards/reorder` (or `…/boards/backlog-order` + `…/shipping-order`) writing `issues.priority`.
  - Job collection: `POST …/issues/{key}/process` (assign a preset), `GET …/issues/{key}/jobs`, `POST …/issues/{key}/jobs/{seq}/(start|stop|cancel)`, engine toggle `PUT …/issues/{key}/auto`.
  - Ship: `POST …/issues/{key}/ship` (dispatch ship agent), auto-ship toggle.
- **`boardcards.BoardCard` grows**: a `Jobs` collection (seq, mode, status), a `CurrentJob`, and the open question now carries its `pipeline_job_id`. `ActiveVerb`/`TodosDone` can be derived from the current job instead of inferred. Add the new states to the `stateLabels`/column mapping.
- **MCP `ask_user_question`**: associate the created question with the **current pipeline job** (the channel knows the session's active dispatch → its job), so it lands on the job row. Answer/cancel endpoints return job context.
- **Removed with the board**: not the `/cards` route (the Pipeline reuses it), but the board-only move/dispatch-from-card/follow-on/quick-eval endpoints lose their UI caller; decide whether to retire them.
- **Wails seam** (`internal/client/` + `desktop/bindings/`): mirror every new method (job control, reorder, ship) so desktop and web stay in lockstep.

---

## 5. Controller / job engine — `internal/controller/`, `internal/dispatcher/`

### Current state
- `internal/controller/controller.go` runs leader-gated goroutines (~5s matcher, ~5s follow-on sweep, ~5s idle-ping, ~5m archive sweep, ~15s heartbeat, ~5m sync), each gated via `…IfLeader` helpers on the lease.
- **Matcher** (`internal/dispatcher/dispatcher.go`): per `(repo, mode, base_branch)`, enforce the concurrency cap, bind the oldest `queued` dispatch to a free agent (CAS-safe). Only advances `queued → pending`.
- **Follow-on promotion sweep** (BACI-179/217) — the **closest existing analogue**: orphan-cancels follow-ons whose parent is terminal, and promotes dormant rows once the gate clears (parent acked / blockers terminal), riding the matcher's ticker. It does **not** dispatch agents, halt on questions, or advance issue state.
- **Archive sweep** auto-completes features when all children are terminal **unless** `state_manual=1` (so `bugs`/`maintenance` with auto-close off are safe).
- **State advance today**: `ReleaseAgentClaim(session, issue, finalState)` in `internal/store/agents.go` — the worker's `release --state` (or `mcp reply`) atomically releases the claim and moves the issue. **This is the seam the engine replaces.**

### Changes required
- **New job-engine ticker** (leader-gated, ~matcher cadence): for each `in_pipeline` issue, read its `pipeline_jobs` + `engine_mode`:
  - **Start**: flip next `pending` job → `running`, queue an `agent_dispatch` for its mode (matcher then binds it), and stop.
  - **Auto**: when the running job completes, queue the next; when the **last non-ship job** completes (or the chain's `ship` hand-off stage is reached), move the issue to `to_be_shipped`.
  - **Halt**: if the current job has an open question, set `engine_pause_reason=open_question` and do nothing until it's answered.
- **New auto-ship ticker** (leader-gated): for the top `to_be_shipped` card with auto-ship on, dispatch a `ship`-mode agent; advance to `done` on success.
- **Replace worker state authority**: completion is detected by the engine (job's dispatch acked) rather than the worker choosing `--state`. A new `EngineAdvanceJob`-style store method supersedes the `finalState` arm of `ReleaseAgentClaim` (release still drops the claim).
- **Reuse**: matcher, idle-pinger, archive sweep, leader service, channel/hook delivery — unchanged. The engine queues ordinary dispatches and lets the matcher bind them.
- **Supersede**: the follow-on promotion sweep (the process chain replaces it) — keep only for legacy dormant rows.
- **Concurrency**: new tickers leader-gated like the rest; per-issue atomicity via CAS on the engine fields / job status. Audit gains a per-job-completion row.

---

## 6. Frontend — `desktop/frontend/src/`

### Delete (issues board + board-only helpers)
- `components/Board.jsx` (the kanban) and its props/controls: `onMoveCard`, dispatch / dispatch-chain, cancel-waiting, follow-on, quick-eval, collapse/compact.
- `components/boardCollapsePersistence.ts`, `components/boardCompactPersistence.ts` (board-only state).
- The `/issues` route + the "Issues" nav tab (`App.jsx`, `lib/routes.ts` `board ↔ /issues`, `Topbar.jsx` NAV). Route `viewFromPath`/`viewPath` lose `board`, gain/keep `pipeline`.
- Reusable bits to **keep**: `KanbanCard`, `QuestionModal`, `ActivityTray`, `ShippedPopover`, `DispatchMenuContent` / `dispatchMenuRows` (fold into the Pipeline).

### Build / extend (Pipeline)
- `components/PipelineView.jsx` (exists as the mock) → real three-column board keyed on the new states. Backlog reorder + expanded-drawer grid with **top-right number badges** (top-to-bottom, right-to-left) + scroll; in-pipeline **process menu** on card entry; the **surrounding processing area** (job-chain rows, open-question indicator, active job's todo/task list) split from the inner issue card; **Start/Stop/Auto/Ship** controls; shipping **FIFO reorder + auto-ship toggle + SHIP**.
- `components/FeaturesView.jsx`: add **done/cancelled issue browsing** (the new home for non-pipeline issues).
- `IssueComposer.jsx` / `IssueWorkspace.jsx`: add a **feature picker** (features are now mandatory; default pre-selected).
- `QuestionModal.jsx`: unchanged component, now also surfaced per-job in the processing area.

### API seam (both `src/api.ts` and `src/api.http.ts`)
- New methods mirroring §4: `reorderBacklog` / `reorderShipping`, `getIssueJobs`, `setProcess`, `startJob` / `stopJob` / `cancelJob`, `setAutoMode`, `shipIssue` / `setAutoShip`; extend `setIssueState` for the two new states.

---

## 7. Prompts & install — `prompts/agents/`, `internal/cli/install_agent.go`

### Current state
- Mode bodies (`plan/design/implement/review/ship/fix_review.md`) include shared `_preamble.md` / `_postamble.md` / `_dispatch_preamble.md` via `{{> name}}`, expanded by `model.ExpandPromptIncludes` (`internal/model/agent.go`). `bacio install-agent` regenerates `.claude/agents/bacio-<mode>-worker.md`.
- Close-out today does two engine-owned-from-now-on things in **every** mode:
  - **State transition**: `bacio agent release <issue> --state <next>` — plan→`todo`, design→`todo`, implement→`in_review`, review→`in_review`, ship→`done`, fix_review→`in_review`.
  - **Done-tagging**: `bacio tag add <issue> <planned|design|implemented|reviewed|fixed>` (no tag for ship).
  - `_postamble.md` also tells the worker to `bacio issue state <issue> in_progress` after a user reply.

### Changes required
- **Remove** from each body: the `--state` arg on release (release becomes claim-drop only), the `tag add` step, and the post-reply `issue state` hop.
- Simplified close-out: claim → brief → work → reply/release (no state, no tags). The engine advances the chain; an open question (re-parented to the job) is the pause signal — no `needs_action` flip.
- `ship.md` stays (dispatched from the shipping column) but loses its `--state done`.
- Update `docs/agent-dispatch.md` (the mode/state-gate narrative) and the review-mode-final-state convention.
- **Process**: edit bodies → `bacio install-agent` to regenerate (the source-of-truth → generated `.claude/agents/*.md` flow); keep `internal/model/prompttemplates` built-ins in sync if they embed bodies.

---

## 8. Migration & sequencing

A safe build order (each step shippable):
1. **Model/store foundation** — new states, `priority`, `pipeline_jobs`, question FK, feature backfill + bootstrap. Enforce states/feature in Go to avoid CHECK/NOT-NULL table rebuilds.
2. **Controller engine** — job-engine + auto-ship tickers; `EngineAdvanceJob` replacing the worker `--state`.
3. **API/MCP** — endpoints + `boardcards` shape + `ask_user_question` job parenting.
4. **Prompts** — strip state/tag steps; `install-agent`. (Can land with step 2 so workers stop fighting the engine.)
5. **Frontend** — Pipeline UI, Features browsing, composer feature picker; then delete the board.
6. **CLI** — reorder/job/ship verbs + `release --state` removal (coordinate with step 4).

**Biggest risks**: the feature-mandatory backfill (data correctness), removing worker state authority (steps 2+4 must land together or workers and engine race), and the two-new-states fan-out (every `switch` on state across all layers).

## 9. Open design decisions (carried from the scan)
- `pipeline_jobs` as its own table vs JSON on `issues` — recommend the table (queryable, FK target for questions).
- Are `start/stop/auto` agent-facing CLI verbs, or API/engine-only? (Likely API/engine; keep the CLI lean.)
- Retire vs keep `agent queue-followon` / `dispatch-chain` and the board-only API endpoints once the UI caller is gone.
- Ship verb: dedicated `issue ship` vs reuse `agent dispatch --mode ship` from `to_be_shipped`.
