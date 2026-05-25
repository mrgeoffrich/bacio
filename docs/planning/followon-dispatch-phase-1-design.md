# Phase 1 design — follow-on dispatch storage, matcher predicate, sweeps

**Feature:** `followon-dispatch` (umbrella `BACI-176`, plan doc `docs/planning/followon-dispatch.md`)
**Design issue:** `BACI-178`
**Implementation issue (blocked by this):** `BACI-179`

This pins the open design questions in the parent plan's Phase 1 section so `BACI-179` can land mechanically. Anything the implementation issue needs to decide that **isn't** answered here is a planning miss — flag it before writing code.

## Summary of decisions

| # | Topic | Decision |
|---|---|---|
| Q1 | Matcher predicate shape | Correlated `NOT EXISTS` against `agent_dispatches.id` (the PK) — see §1. |
| Q2 | Promote audit shape | Dedicated `agent.followon.promote` row per cleared follow-on at sweep time. See §2. |
| Q3 | `EndAgentSession` cascade interaction | Predicate's "predecessor settled" is `acked` OR `cancelled`; the BACI-133 requeue path puts the parent back to `queued`, which the predicate treats as *not yet settled*, so the follow-on stays dormant. Table in §3. |
| Q4 | Orphan-cancel sweep edges | Cancel when issue is `done`/`cancelled`; do **not** cancel solely because the issue is archived; deleted issues are unreachable (FK cascades the follow-on too); pruned parent + dormant follow-on is fixed by `ON DELETE SET NULL` + sweep. Table in §4. |
| A | Schema `CHECK` constraint | **No.** The Go-side `AddDispatch` validator (`AddDispatchIn`) is the single guard; a SQL `CHECK` would tax every status transition and the column-set-NULL behaviour the sweep relies on. See §5. |
| B | Sweep cadence | Ride `QueueMatchInterval` (**5s**, not 3s — plan doc was off-by-2). One sweep tick → next matcher tick binds. No slower tick. See §6. |

The rest of this doc gives the rationale at the depth the impl PR will need.

---

## §1 — Matcher predicate shape (Q1)

### The constraint set

The two queries that the BACI-51 matcher reads are `ListQueuedModesByRepo` and `ListQueuedByRepoMode` in `internal/store/dispatches.go`. Both already carry:

- `d.repo_id = ?`
- `d.status = 'queued'`
- `d.issue_id IS NULL OR i.archived_at IS NULL` (BACI-68 archive guard via `LEFT JOIN issues i`)

The BACI-58 staleness gate lives in `CountInFlightByMode` / `InflightByModeForRepo` instead — it's an `EXISTS` over `agent_sessions`. Worth surveying alongside because it's the closest precedent the matcher already invokes per tick.

### The three candidate forms

The new "predecessor settled" guard must skip a queued row whose `queued_after_dispatch_id` is set **and** references a parent whose status is not yet `acked` or `cancelled`.

**(A) `LEFT JOIN` on the parent dispatch:**

```sql
LEFT JOIN agent_dispatches p ON p.id = d.queued_after_dispatch_id
WHERE …
  AND (d.queued_after_dispatch_id IS NULL OR p.status IN ('acked','cancelled'))
```

Pros: predicate expression is short.
Cons: adds a second `agent_dispatches` row to the join even when the row has no predecessor — every queued row pays for a join probe.

**(B) Correlated `NOT EXISTS`:**

```sql
WHERE …
  AND NOT EXISTS (
    SELECT 1 FROM agent_dispatches p
     WHERE p.id = d.queued_after_dispatch_id
       AND p.status NOT IN ('acked','cancelled')
  )
```

Pros: skipped entirely when `d.queued_after_dispatch_id IS NULL` (SQLite short-circuits `NOT EXISTS` against an empty seek). The subquery is a PK-equality lookup — index `sqlite_autoindex_agent_dispatches_1` on the PK, single-row probe.
Cons: prose is denser than form (A).

**(C) Correlated `EXISTS` with positive predicate:**

```sql
WHERE …
  AND (d.queued_after_dispatch_id IS NULL
       OR EXISTS (
         SELECT 1 FROM agent_dispatches p
          WHERE p.id = d.queued_after_dispatch_id
            AND p.status IN ('acked','cancelled')
       ))
```

Pros: matches the BACI-58 staleness gate's `EXISTS` shape (same precedent the matcher already reads).
Cons: two-clause boolean — the planner re-evaluates `d.queued_after_dispatch_id IS NULL` per row even though the EXISTS branch already covers it implicitly.

### Pick: form (B) — correlated `NOT EXISTS`.

Wins on three axes:

1. **Smallest index surveyed.** A PK seek on `agent_dispatches.id`. No new index needed beyond `idx_dispatches_queued_after` (which exists for the *sweep* queries, not the matcher).
2. **Cheapest skip.** A row with `queued_after_dispatch_id IS NULL` (the overwhelming majority — a normal queued dispatch with no follow-on chain) compiles down to a constant-true. The form-(A) `LEFT JOIN` still does the outer-join probe for these rows; form (C) still evaluates the OR.
3. **Reads cleanest in-place.** Both `ListQueuedModesByRepo` and `ListQueuedByRepoMode` are already `EXISTS`-flavoured (the BACI-58 gate uses the same shape in the sibling queries), so a reviewer recognises the pattern.

### The exact additions

Inside `ListQueuedModesByRepo`:

```sql
SELECT DISTINCT d.mode FROM agent_dispatches d
 LEFT JOIN issues i ON d.issue_id = i.id
 WHERE d.repo_id = ? AND d.status = 'queued'
   AND (d.issue_id IS NULL OR i.archived_at IS NULL)
   AND NOT EXISTS (
     SELECT 1 FROM agent_dispatches p
      WHERE p.id = d.queued_after_dispatch_id
        AND p.status NOT IN ('acked','cancelled')
   )
 ORDER BY d.mode ASC
```

Identical addition inside `ListQueuedByRepoMode` (before the `ORDER BY`):

```sql
   AND NOT EXISTS (
     SELECT 1 FROM agent_dispatches p
      WHERE p.id = d.queued_after_dispatch_id
        AND p.status NOT IN ('acked','cancelled')
   )
```

**Why `NOT IN` rather than `IN`:** the row's predecessor either *is* settled (acked / cancelled) or *isn't*. Phrasing the EXISTS positively (`p.status IN ('acked','cancelled')` → "settled exists") plus an outer `IS NULL OR` (form C) is the same boolean, but form (B) reads as "no row is blocking this dispatch" — which is the matcher's intent.

**Single-row predecessor — no fan-out.** `queued_after_dispatch_id` is a single FK column, so the subquery is always 0 or 1 rows. The implementation issue should not generalise this to "all predecessors settled" — there is no chain semantics in v1.

### Index posture

No new index for the matcher predicate itself — the `p.id = …` lookup uses the PK index. The planning doc's proposed `idx_dispatches_queued_after` on `agent_dispatches(queued_after_dispatch_id)` is for the **sweeps** in §2 / §4, which walk by parent id from the opposite direction. Land it with the schema migration as planned.

---

## §2 — Promote audit shape (Q2)

### What the BACI-160 precedent actually wrote

The matcher's audit is one `agent.bind` row per `queued → pending` transition, attributed to `model.MatcherActor` (`bacio-matcher`). The pattern is "one audit row per row-level state change the leader-gated tick committed". The archive sweep (BACI-160 gap 2) writes one row per non-empty sweep (a sweep-level row carrying counts in Details) attributed to `model.ControllerActor` (`bacio-controller`) — but that's because `ArchiveSweep` is a single multi-table SQL pass with summarised counts, not a row-level UPDATE loop.

### Two choices for the promote sweep

The promote sweep clears `queued_after_dispatch_id` on every dormant row whose predecessor has settled AND whose issue has no open claim — one UPDATE per row, the slice of cleared rows returned for the caller to audit (mirrors `Matcher.TickDetailed`).

**Option 1 — dedicated `agent.followon.promote` per row.** One audit row per follow-on cleared. Attributed to `model.ControllerActor`. Details: `dispatch_id=… issue=… mode=… parent_dispatch_id=…`. Closest analogue to `agent.bind`.

**Option 2 — compose into the eventual `agent.bind`.** Skip a promote-time audit row; on the next tick's bind, the matcher writes one extra Details field (`promoted_from_followon=true,parent_dispatch_id=…`) onto the `agent.bind` row. Fewer rows, single forensic point per follow-on, but the bind sees only the post-clear row — re-fetching the parent for the audit detail is a second read.

### Pick: option 1.

Reasons, in order of weight:

1. **Promote is a state transition, not a side effect of binding.** Clearing `queued_after_dispatch_id` is the moment the follow-on stops being dormant and becomes "regular queued". A `bacio history --op agent.followon.promote` reader can answer "when did this follow-on become eligible, and how long did it take to bind" by joining promote rows to bind rows on `dispatch_id`. Composing the info into `agent.bind` loses that decoupling — the bind only happens later (and may never happen at all if the worker pool is empty, but the promote still mattered).
2. **Mirrors `agent.bind`'s per-row shape.** The reviewer model is "one row per row-level UPDATE the leader-gated tick committed". The archive sweep is the exception because it's a single batch-COUNT, not a row-level loop.
3. **Forensic trail across the cascade.** When the predecessor was cancelled by `EndAgentSession`'s §B cascade (rather than acked), the promote sweep still fires (cancel counts as settled). A reader can chain `agent.cancel` → `agent.followon.promote` → `agent.bind` on `dispatch_id`s to reconstruct the chain. Composing into `agent.bind` collapses two events into one row and erases that.
4. **Trivial row volume.** A repo with N follow-on chains writes N promote rows over the chain's lifetime. The history table already absorbs the matcher's per-bind rows at the same scale.

The orphan-cancel sweep gets the symmetric treatment — one `agent.followon.cancel` row per row it cancels (see §4). Same actor (`model.ControllerActor`), same per-row shape.

### Op set introduced

Three new audit ops, all attributed to `model.ControllerActor` except the queue / cancel ones written from the client (see plan doc):

| Op | Written by | When |
|---|---|---|
| `agent.followon.queue` | `localClient.QueueFollowOnDispatch` (Phase 2) | User attached a follow-on to a taken card. |
| `agent.followon.cancel` | `localClient.CancelFollowOnDispatch` (Phase 2) **and** `CancelOrphanedFollowOnsIfLeader` sweep | User removed the chip, **or** the issue landed in done/cancelled before promote. |
| `agent.followon.promote` | `PromoteFollowOnsIfLeader` sweep | Sweep cleared `queued_after_dispatch_id`; the row is now eligible for the matcher. |

History ops in bacio are free-form strings — no central registry. The convention is dot-separated `subsystem.action`, which these match.

### Details string shape

Mirror `bindDetails` in `internal/controller/controller.go` — comma-separated `k=v`, high-signal fields only. For the promote/cancel sweeps:

```
dispatch_id=<id>,issue=<key>,mode=<mode>,parent_dispatch_id=<parent_id>
```

The user-driven `agent.followon.queue` / `agent.followon.cancel` rows include the same shape so a `--op agent.followon.cancel` reader doesn't have to branch on actor to know what to look for. Empty fields (no issue, no mode) are omitted, not stamped as `=`.

### `TargetID` / `TargetLabel`

Mirror `agent.bind`:

- `TargetID = dispatch.ID` (the follow-on row, not the parent)
- `TargetLabel = AgentName` once bound; pre-bind (promote / queue / cancel) the label is the issue key (or empty when no issue), since there's no bound agent yet.

---

## §3 — `EndAgentSession` cascade interaction (Q3)

### The two cascade modes

`EndAgentSession` settles every open dispatch the session held inside the same transaction. The cascade mode is set by the caller:

- `DispatchCascadeCancel` (default): every still-open dispatch → `status='cancelled'`. Used by every user-driven / hook-driven / supersede end path. The §B cascade in `resolveOpenDispatchesForSession` writes the UPDATE.
- `DispatchCascadeRequeue` (BACI-133 reaper only): every still-open dispatch → `status='queued'` with `target_session_id=''` and `target_agent_id=NULL` so the matcher can rebind. Only legal when `reason == presumed_dead`.

The dormant follow-on row's status is **`queued`** (with `queued_after_dispatch_id` set). It is not "open" in the cascade sense — the matcher hasn't touched it — but `resolveOpenDispatchesForSession` selects on `status IN ('queued','pending','delivered')`, so a dormant follow-on whose `target_session_id` happens to point at the dying session **would** be picked up.

In practice the dormant follow-on is created by `AddDispatch` with `InitialStatus=DispatchQueued`, which the AddDispatch validator already rejects unless `target_session_id == ''` and `target_agent_id == nil`. So the dormant row never carries the session as its target — it's caught by neither the session-scoped UPDATE in `resolveOpenDispatchesForSession` nor the identity-scoped one. The cascade leaves dormant rows alone.

### The behaviour table

Rows = "what happened to the parent". Columns = "what happens to the dormant follow-on".

| Parent dispatch path | Parent ends up `status` = | Follow-on after the session ends | Why |
|---|---|---|---|
| Parent acked normally (worker ran `bacio agent release`) | `acked` | Sweep clears it on the next tick (predecessor settled) → matcher binds it the tick after | Happy path. |
| Parent cancelled by user / supersede / `DispatchCascadeCancel` | `cancelled` | Same as above — `cancelled` counts as settled per the §1 predicate. Sweep promotes; matcher binds. | Brief says "fire unconditionally including releases that arose from cancellation". |
| Parent requeued by BACI-133 reaper (`DispatchCascadeRequeue`) | `queued` | **Stays dormant.** | A `queued` parent is *not* in the settled set; the §1 predicate matches `acked` OR `cancelled` only. The matcher will rebind the parent on the next tick; when *that* parent eventually acks (or is cancelled), the sweep promotes the follow-on. Behaves correctly without any extra logic. |
| Parent hard-deleted by 60-day prune (`pruneDispatches`) | gone | Promoted by §4 fallback (parent FK is `ON DELETE SET NULL` → `queued_after_dispatch_id` becomes NULL → row matches the matcher predicate's `NOT EXISTS` short-circuit and binds on next tick) | The retention prune only touches `status IN ('acked','cancelled')` rows; a parent that gets pruned must have been settled at least 60d earlier. Treating the follow-on as ready is the right behaviour. |

### Why the cascade leaves dormant rows alone

The dormant row's target columns are empty (the AddDispatch validator forbids them on `InitialStatus=DispatchQueued`). `resolveOpenDispatchesForSession`'s two UPDATE statements select either by `target_session_id = '<this session>'` or by `target_agent_id = '<this identity>' AND no other live session`. Neither matches a dormant row — it's effectively invisible to the cascade.

That's deliberate, not accidental: the dormant follow-on is not "work this session was doing" — it's "work the user queued for later, attached to a parent dispatch this session held". Cancelling the dormant follow-on when its parent's session dies would defeat the entire feature (BACI-176's brief is "fire unconditionally" — and the matcher's eventual bind on a recovered or re-queued parent will service the chain).

### One subtle case: the bound-but-not-yet-acked predecessor

What about `target_session_id` on the follow-on being filled in *between* `AddFollowOnDispatch` and the predecessor ack? It can't — the matcher only fills `target_agent_id` (via `BindQueuedDispatch`), and only on a row whose `queued_after_dispatch_id` is NULL (the §1 predicate excludes dormant rows from the matcher's pool). So the dormant row's target columns stay empty for its entire dormant lifetime, by construction.

### No matcher predicate change for the requeue case

The §1 predicate (`p.status NOT IN ('acked','cancelled')`) treats the parent's `queued` status as *not yet settled*. That's correct for both the dormant-from-the-start case (parent never bound) and the re-queued case (parent was bound, ran, was reaped, went back to queued). The matcher will bind the parent first; the sweep promotes the follow-on after the second `acked` (or `cancelled`).

### Implementation note for the impl issue

The impl issue (`BACI-179`) does NOT need to modify `EndAgentSession` or `resolveOpenDispatchesForSession`. The decision is *not* to grow the cascade with a follow-on-aware branch — the existing target-based selection already does the right thing.

Add a single regression test (per plan doc Tests section): `TestMatcherIgnoresDormant_ParentRequeued`. Stand up parent → bind → reaper-requeue (`DispatchCascadeRequeue`) → run matcher Tick. The follow-on should stay dormant. After a second cycle (rebind, ack), the sweep promotes.

---

## §4 — Orphan-cancel sweep edge cases (Q4)

### The sweep's contract

`CancelOrphanedFollowOnsIfLeader` runs every `QueueMatchInterval` (5s, leader-gated). It scans for dormant follow-on rows whose **issue** is in a terminal state (`done`/`cancelled`) and cancels them. The dormant row's predecessor doesn't matter — the work the predecessor was queued to do is irrelevant once the issue itself is closed.

### The four edge cases

| Case | Sweep behaviour | Why |
|---|---|---|
| (a) Issue is `archived_at IS NOT NULL` but in any state | **Do NOT cancel solely on archive.** | Archive is a visibility flag, not a lifecycle terminal. An archived issue in `in_progress` could still be unarchived and worked. The matcher's BACI-68 archived-row guard prevents *binding* the follow-on while the issue is archived — that's the right safety. If the issue is archived **and** terminal, condition (c) below covers it through the state check, not the archive flag. |
| (b) Issue is in `done` or `cancelled` (the planned case) | Cancel the follow-on. Write one `agent.followon.cancel` audit row per cancelled row. | Brief says "auto-cancel" — the work the follow-on was queued for is irrelevant if the issue is closed. |
| (c) Issue has been hard-deleted (FK cascade) | Unreachable. | `agent_dispatches.issue_id` is `REFERENCES issues(id) ON DELETE SET NULL` (existing schema, line 477 of schema.sql). A deleted issue silently nulls the FK; the follow-on row survives but loses its `issue_id`. The sweep filter (see §4.b SQL below) skips rows with `issue_id IS NULL` — they have no issue to be "orphaned" against. A subsequent §1 predicate match against a NULL parent OR an issueless dormant row eventually promotes them via the §3-row-4 path (parent settled, no issue to gate on, matcher just binds). This is the right behaviour: a dormant follow-on whose issue vanished has nothing to do, but it's not the orphan-cancel sweep's job to clean it up. The 60-day retention prune sweeps settled rows; an "issueless dormant" row is the FK-cascade analogue of a stranded pending dispatch — fix the issue at the source (don't delete claimed issues; the `bacio issue rm` flow already cascades). |
| (d) Parent dispatch was pruned by 60-day retention | Sweep promotes (via §3-row-4 fallback), not cancels. | `queued_after_dispatch_id` is `ON DELETE SET NULL`, so a pruned parent leaves the column NULL. The §1 matcher predicate then short-circuits the `NOT EXISTS` (no parent to check), the row is eligible, and the matcher binds it on the next tick — **as long as the issue isn't terminal**. If the issue *is* terminal at the same moment, case (b) wins and the row is cancelled first. The race is a no-op either way (a cancelled row stays cancelled if the matcher hasn't bound it). |

### The exact SQL

`CancelOrphanedFollowOns` (returns the slice it touched, for per-row audit by the leader helper):

```sql
UPDATE agent_dispatches
   SET status = 'cancelled'
 WHERE status = 'queued'
   AND queued_after_dispatch_id IS NOT NULL
   AND issue_id IS NOT NULL
   AND EXISTS (
     SELECT 1 FROM issues i
      WHERE i.id = agent_dispatches.issue_id
        AND i.state IN ('done','cancelled')
   )
RETURNING id, repo_id, …
```

`PromoteReadyFollowOns` (for symmetry — referenced from §2):

```sql
UPDATE agent_dispatches
   SET queued_after_dispatch_id = NULL
 WHERE status = 'queued'
   AND queued_after_dispatch_id IS NOT NULL
   AND NOT EXISTS (
     SELECT 1 FROM agent_dispatches p
      WHERE p.id = agent_dispatches.queued_after_dispatch_id
        AND p.status NOT IN ('acked','cancelled')
   )
   AND NOT EXISTS (
     SELECT 1 FROM agent_claims c
       JOIN agent_sessions s ON s.id = c.session_pk
      WHERE c.issue_id = agent_dispatches.issue_id
        AND c.released_at IS NULL
        AND s.ended_at IS NULL
   )
RETURNING id, repo_id, …
```

Note the second `NOT EXISTS` on `agent_claims` — the brief's "no new claim has raced in" gate, joined with `agent_sessions.ended_at IS NULL` so an ended session's stale-but-not-yet-released claim doesn't block the promote indefinitely (BACI-133 + BACI-140 territory). Mirrors the live-claim check at line 1344 of `internal/store/agents.go`. When `issue_id IS NULL` the second EXISTS short-circuits trivially (no claim is keyed on a NULL issue), so an issueless dormant row gets promoted — the §4.c fallback.

### Order of sweeps inside the controller

The plan doc proposes two separate goroutines, both on `QueueMatchInterval`. Inside one tick the two sweeps are independent — neither's result depends on the other's. Order doesn't matter for correctness. For determinism (and so the audit log reads chronologically), the impl issue should run **orphan-cancel before promote** inside the same goroutine (a single helper that calls both), rather than two parallel goroutines:

- A row that's both "issue terminal" (orphan-cancel target) and "predecessor settled" (promote target) shouldn't promote first and then bind to a worker on a terminal issue — that's a wasted bind. Orphan-cancel first means the row is gone before promote can clear it.
- Two goroutines on the same interval also race the matcher's tick. Single sequential helper, called on a single ticker, is one fewer goroutine to reason about and matches the precedent of `MatchIfLeader` (one helper, one ticker).

This is a deviation from the parent plan doc, which says "two goroutines". Flag it explicitly when implementing — see §6 for the consolidated cadence.

---

## §5 — Schema CHECK constraint (open question A)

### The question

The plan's open questions ask: should the schema include
```sql
CHECK (queued_after_dispatch_id IS NULL OR status = 'queued')
```
on `agent_dispatches`? Pro: defends the invariant that only `queued` rows carry a predecessor link. Con: makes every status-changing UPDATE pass the CHECK.

### Pick: no.

Three reasons:

1. **The sweep deliberately clears the column on `queued` rows.** `PromoteReadyFollowOns` does `SET queued_after_dispatch_id = NULL WHERE status='queued' AND …`. A CHECK constraint is satisfied by both states ("NULL" and "queued with non-NULL FK") — but the sweep is the *only* writer that flips that bit, and it always lands the row in `queued + NULL`, never in an invalid state. The CHECK adds no protection against the sweep itself.
2. **`BindQueuedDispatch` would silently get harder.** The bind UPDATE flips `status` to `pending` on a row that was `queued`. Right before the bind, the sweep has already cleared `queued_after_dispatch_id` (else the row wouldn't be in the matcher's pool — §1 predicate). So at bind time the column is NULL, the CHECK is fine. **But** a hypothetical race window — sweep clears the column, then a concurrent process re-attaches a follow-on (which the Go-side validator already rejects: you can only attach a follow-on while the parent is open) — would surface as a SQLite CHECK violation rather than the Go-side rejection's nice error. The Go-side validator is the better place for the invariant because it gives a useful error.
3. **The Go-side validator is already the single guard.** `AddDispatch`'s switch on `status` already enforces:
   - `InitialStatus = DispatchQueued` rows have no target — and we'll extend it (per plan doc) to allow `QueuedAfterDispatchID` only on the `DispatchQueued` branch.
   - No other code path sets `queued_after_dispatch_id` (the column is set on INSERT only — no UPDATE-to-set-FK helper).
   The store-boundary validator pattern is the convention (per `internal/store/agents.go` rules, e.g. `ValidateActor`, `ValidateSessionID`). A SQL CHECK would be a second guard at a layer that can't produce a useful error.

### What the impl issue should add instead

Inside `AddDispatch`:

```go
if in.QueuedAfterDispatchID != nil && status != model.DispatchQueued {
    return nil, errors.New("queued_after_dispatch_id is only valid on queued dispatches")
}
```

That's the entire enforcement surface for the v1 invariant. Land the FK with `ON DELETE SET NULL` (per plan doc) and a single matching index, but no CHECK.

---

## §6 — Sweep cadence (open question B)

### The question

Two new sweeps (promote + orphan-cancel) ride `QueueMatchInterval` (the parent plan said 3s, but it's actually 5s — see `internal/store/leader.go:36`). Is 5s right, or should they ride a slower tick?

### Pick: stay on `QueueMatchInterval` (5s), single combined helper.

### Reasons

1. **End-to-end latency budget.** The user's expectation (parent plan doc, "Done when…") is "release the claim → second worker picks up follow-on within ~6 seconds". The chain is: ack → next tick (5s avg, 10s worst case) → promote → next tick → matcher → bind → MarkDispatchDelivered → SessionStart hook drains. Each `+QueueMatchInterval` step is real user-visible latency. Slowing the sweep to a 30s or 1m cadence breaks the "feels instant" goal.
2. **Cost is negligible.** Both sweeps are leader-gated single UPDATE statements over the dormant pool (the index `idx_dispatches_queued_after` keeps the scan tight). One queries-by-issue-state, the other does a PK probe per dormant row. On a board with no follow-ons the index is empty and the sweeps return in microseconds.
3. **No tick alignment between matcher and sweeps.** Both ride 5s but on independent tickers (the controller starts each goroutine with its own `time.NewTicker`). The matcher's tick may fire seconds before the sweep's, in which case the freshly-promoted row binds on the *next* matcher tick — adds up to one extra `QueueMatchInterval` in the worst case. That's already factored into the 6-second budget above.
4. **Slower ticks add no robustness.** The orphan-cancel sweep is also informational — the matcher's `(d.issue_id IS NULL OR i.archived_at IS NULL)` BACI-68 guard plus the `state` check in `CancelOrphanedFollowOns`'s WHERE would prevent a follow-on landing on a terminal issue anyway (the matcher predicate could be extended to skip terminal-issue follow-ons, but that's belt-and-braces — see §6.next).

### Implementation: one helper, one goroutine

The parent plan doc says "two new goroutines on `QueueMatchInterval`". §4 already argued for a single helper that runs orphan-cancel before promote, to avoid race wastage. Bake that consolidation into the controller wiring: one new goroutine, one helper `FollowOnSweepIfLeader(s, el, log)` that calls both store helpers in sequence and writes both audit ops. Mirrors `ArchiveSweepIfLeader`'s shape (one helper, one ticker, one audit pass).

Deviation from plan doc: **one goroutine, not two.** The audit-writing caller knows which sweep produced each row by the helper that returned it — the slices are kept separate inside the helper.

### Possible later optimisation (not for v1)

The matcher predicate could grow a `AND (issue_id IS NULL OR i.state NOT IN ('done','cancelled'))` guard so a terminal-issue follow-on never binds even if the orphan-cancel sweep missed a tick. Not worth doing in v1 — the sweep runs on the same cadence, and the BACI-68 archived guard isn't enough on its own (an unarchived terminal issue would slip through). Park it as a follow-up if real telemetry shows missed-tick binds happen.

---

## §7 — Store-helper signatures (for `BACI-179`)

To minimise design ambiguity in the impl issue, here are the exact signatures the impl should land:

```go
// AddFollowOnDispatch creates a dormant follow-on row attached to an
// open parent dispatch on the same repo. The parent must be in an
// open state (queued, pending, or delivered) and target an issue —
// follow-ons are issue-scoped. Returns ErrNotFound if the parent doesn't
// exist; an explicit error if the parent is already settled (acked /
// cancelled) or has no issue; an explicit error if a follow-on already
// exists for the parent's issue (single-slot v1).
func (s *Store) AddFollowOnDispatch(repoID, parentDispatchID int64, mode model.DispatchMode, createdBy string) (*model.AgentDispatch, error)

// CancelFollowOnDispatch cancels the current dormant follow-on for an
// issue. Idempotent: returns (nil, nil) if there is no current follow-on
// (e.g. it was already promoted and is queued for real, or already
// cancelled). Returns the cancelled row when it actually flipped it.
func (s *Store) CancelFollowOnDispatch(repoID, issueID int64) (*model.AgentDispatch, error)

// PromoteReadyFollowOns clears queued_after_dispatch_id on every dormant
// row whose predecessor is settled (acked/cancelled) AND whose issue has
// no open claim from a live session. Returns the cleared rows so the
// caller can write per-row audit history. Empty slice (no error) when
// no row was promotable. Leader-gated by the caller.
func (s *Store) PromoteReadyFollowOns() ([]*model.AgentDispatch, error)

// CancelOrphanedFollowOns cancels every dormant follow-on whose issue
// is in a terminal state (done/cancelled). Returns the cancelled rows
// so the caller can write per-row audit history. Empty slice (no error)
// when no row was orphan-cancellable. Leader-gated by the caller.
func (s *Store) CancelOrphanedFollowOns() ([]*model.AgentDispatch, error)

// FollowOnForIssue returns the current dormant follow-on for an issue,
// or (nil, nil) when none exists. Used by the kanban board assembler to
// fill the chip data on a card. NB: returns the row only while it is
// dormant (queued_after_dispatch_id IS NOT NULL); a promoted row is no
// longer a "follow-on" — it's a regular queued dispatch heading for the
// matcher.
func (s *Store) FollowOnForIssue(repoID, issueID int64) (*model.AgentDispatch, error)
```

`AddDispatchIn` gains:

```go
type AddDispatchIn struct {
    // … existing fields …

    // QueuedAfterDispatchID, when non-nil, links this dispatch as a
    // follow-on to the named parent dispatch. Valid only on
    // InitialStatus = DispatchQueued; rejected otherwise. The matcher
    // skips queued rows whose parent isn't yet acked/cancelled (§1).
    QueuedAfterDispatchID *int64
}
```

`model.AgentDispatch` gains:

```go
QueuedAfterDispatchID *int64 `json:"queued_after_dispatch_id,omitempty"`
```

`scanDispatch` scans the new column into `*int64` via `sql.NullInt64` — identical pattern to the existing `IssueID` handling.

### Per-row audit-writing helpers

Mirror `recordBindAudit` in `internal/controller/controller.go`. One helper per op:

```go
func recordFollowOnPromoteAudit(s *store.Store, d *model.AgentDispatch, log *slog.Logger) { … }
func recordFollowOnCancelAudit(s *store.Store, d *model.AgentDispatch, log *slog.Logger) { … }
```

Both emit `Actor=model.ControllerActor`, Kind=`agent`, Details composed by a shared `followOnDetails(d *model.AgentDispatch) string` that produces `dispatch_id=…,issue=…,mode=…,parent_dispatch_id=…` (omitting empty fields). `TargetID = d.ID`; `TargetLabel = d.IssueKey` (or empty when no issue).

---

## §8 — Tests (additions to the plan doc)

The plan doc names six tests. Add these:

- **`TestMatcherIgnoresDormant_ParentRequeued`** (§3): parent bound → reaper-requeues → matcher Tick must leave the follow-on dormant, predecessor is now in `queued`.
- **`TestPromoteSweep_BlockedByOpenClaim`** (§4 PromoteReadyFollowOns): parent acked, but the issue still has an open claim from a live session → sweep is a no-op. Release the claim → sweep promotes on the next call.
- **`TestPromoteSweep_TolerantOfOrphanClaim`** (BACI-140 mirror): the claim's `agent_sessions.ended_at` is set → that claim does NOT block promote (matches the live-claim check in the helper SQL). Without this the sweep would never promote on a reaper-killed predecessor.
- **`TestOrphanCancelSweep_ArchivedNotTerminalIsNoop`** (§4.a): issue is archived but still in `in_progress` → sweep does not cancel.
- **`TestOrphanCancelSweep_IssuelessRowIgnored`** (§4.c): follow-on whose issue was deleted (FK cascade nulled `issue_id`) → sweep does not cancel; promote (when predecessor settles) clears `queued_after_dispatch_id`; matcher binds.
- **`TestSchemaInvariant_FollowOnRejectedOnNonQueued`** (§5): `AddDispatchIn{InitialStatus: DispatchPending, QueuedAfterDispatchID: &x}` returns the Go-side validator error.
- **`TestPromoteAndCancelAuditRowsWritten`** (§2): drive the controller helper end-to-end; assert one `agent.followon.promote` row and one `agent.followon.cancel` row, both attributed to `model.ControllerActor`, with the expected Details fields.

---

## §9 — Out of scope (impl-issue checklist)

The implementation issue (`BACI-179`) must NOT:

- Modify `EndAgentSession` or `resolveOpenDispatchesForSession`. The cascade does the right thing already (§3).
- Add a SQL `CHECK` constraint on `agent_dispatches` for the new column (§5).
- Add a matcher-side `i.state NOT IN ('done','cancelled')` guard. The orphan-cancel sweep is the v1 mechanism (§6 closing).
- Spin two separate goroutines for the sweeps. One helper, one ticker (§6).
- Add a generic "dispatch dependency graph" data shape. Single nullable FK only (parent plan doc Out of scope).

The implementation issue MAY:

- Pick `agent.followon.cancel` row's actor based on the writer — `model.ControllerActor` from the sweep, the calling user/agent from the client-driven cancel. Both legitimate; the actor distinguishes the two paths in `bacio history --op agent.followon.cancel`.
- Add internal helpers (e.g. `followOnDetails`) wherever the controller package finds them readable. The §7 surface is normative; the package-internal organisation is the implementer's call.

---

*End of design.*
