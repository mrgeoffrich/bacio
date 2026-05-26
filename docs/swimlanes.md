# Swimlanes — design doc

A pipeline view inspired by the [factory-floor experiment](factory-floor.md):
**one lane per issue**, the card sliding left-to-right through slots
inside that lane as it progresses.

> Branch: `experiment/swimlanes` (off `main`)
> Status: **spec v2 — locked (BACI-212)**. Supersedes the earlier
> shared-header shape from BACI-211. The BACI-212 design pass landed five
> sharpenings: the resolver's activeVerb-precedence rule, the per-verb
> six-rules CLI table, the data-model edge cases (lane-position gaps,
> orphan `issue_key`, lazy `pipeline_to_ship`), the stuck-menu store-level
> meaning, and the confirmed-deferred open questions.
> Inspiration: `docs/factory-floor.md` + `docs/factory-floor-architecture.md`
> Stack: **plain React + CSS — no canvas, no SVG.** Each lane is a
> self-contained row built as a CSS grid with one column per slot; the
> card lives in the slot matching the issue's current state.
> `motion/react` handles the slide via `layoutId` FLIP. Nothing in this
> design should require a drawing surface.

## What changed from v1

The v1 spec (BACI-211) drew slot titles and mode chips as a single *shared
header row* across the top of all lanes, and pre-populated four empty lanes
by default. v2 is rebuilt around three constraints from the second design
pass:

- **No shared headers.** Each lane carries its own slot titles + mode chips
  inline. This unlocks per-lane variation later (e.g. one lane skips Review,
  another adds Design) without rewriting the layout.
- **No default lanes.** The page starts with zero lanes. The user adds them
  explicitly with a "+ Add lane" button, then drags cards from the backlog
  onto a lane to occupy it.
- **No configure drawer.** Mode chips (`auto ⚡ / manual ▶ / skip ⊘`) are on
  the screen, on every slot, all the time. Click to cycle.

Everything else (the backlog parking lot, the To Ship queue with max
concurrency 1, the state-to-slot resolver, the rework loop) carries over.

## The screen

```
┌──────────────┐                                                       ┌──────────┐
│   Backlog    │   ┌─ Lane 1 ──────────────────────────────── ✕ ─┐    │  To Ship │
│              │   │ ┌─Plan─⚡──┬─Impl─⚡──┬─Review─▶─┬─Fix─⚡──┐ │    │   ⚡     │
│ ▦  ▦         │   │ │          │          │   ▦      │         │ │    │          │
│ ▦  ▦         │   │ └──────────┴──────────┴──────────┴─────────┘ │    │   ▦      │
│ ▦  ▦         │   └─────────────────────────────────────────────  │    │   ▦      │
│ ▦  ▦         │                                                       │          │
│ ▦  ▦         │   ┌─ Lane 2 ──────────────────────────────── ✕ ─┐    │          │
│ ▦  ▦         │   │ ┌─Plan─⚡──┬─Impl─⚡──┬─Review─▶─┬─Fix─⚡──┐ │    │          │
│              │   │ │  ▦       │          │          │         │ │    │          │
│              │   │ └──────────┴──────────┴──────────┴─────────┘ │    │          │
│              │   └─────────────────────────────────────────────  │    │          │
│              │                                                       │          │
│              │   [+ Add lane]                                        │          │
└──────────────┘                                                       └──────────┘
```

Three regions, left → right:

1. **Backlog** (240px) — a parking lot of `todo` issues. 2-column grid;
   virtualised when item count >100.
2. **Lane region** (flex 1) — zero or more lanes stacked top-down. Each lane
   is a self-contained card containing its slot row. The lane region scrolls
   vertically when lanes overflow the viewport.
3. **To Ship** (240px) — vertical queue of issues that have finished all
   slots in their lane and are ready to be shipped. Max concurrency 1. The
   queue's mode chip (⚡/▶) sits at the top of the panel.

Route: `/pipeline`. Top-nav entry: "Pipeline" (added after History in
`desktop/frontend/src/components/Topbar.jsx`'s `NAV`).

## Lanes

A **lane** is a horizontal track for one issue's lifecycle. Each lane is
self-contained — its own slot row, its own mode chips, its own remove
button.

A lane is either:

- **Empty** — drawn as an outlined row with a centred "Drop a backlog card
  here" affordance. The slot row is still rendered (faint) so the lane's
  shape is visible.
- **Occupied** — bound to one issue from the moment a backlog card is
  dropped on it until the issue's card moves into the To Ship queue. The
  issue key (`BACI-211 …`) shows in the lane's top-left corner; the card
  sits in whichever slot matches the resolver.

The user adds lanes one at a time via **+ Add lane** at the bottom of the
lane region. There's no default lane count; an empty pipeline shows just
the add button.

Lanes are **positionless** — the lane region renders lanes in creation
order, top-down. Drag-to-reorder is intentionally absent (the lane is a
slot, not a seat). Each lane carries a ✕ in its header:

- ✕ on an empty lane → removes the lane.
- ✕ on an occupied lane → confirmation, then the issue transitions back to
  `todo` (returns to the backlog) and the lane is removed.

## Slots

A **slot** is one stage column inside a lane. Each slot has:

| Property | Source |
|----------|--------|
| `name` | Per-lane (MVP: hardcoded to "Plan" / "Implement" / "Review" / "Fix-review") |
| `agent` | Per-lane (MVP: hardcoded to `plan` / `implement` / `review` / `fix_review`) |
| `mode` | Per-lane (`auto` / `manual` / `skip`) — defaults to `auto` / `auto` / `manual` / `auto` |
| `enter_state` | Per-lane (MVP: hardcoded `todo` / `in_progress` / `in_review` / `needs_action`) |
| `exit_state_success` | Per-lane (MVP: hardcoded `in_progress` / `in_review` / `done` / `in_review`) |
| `exit_state_rework` | Per-lane (MVP: only Review carries one, value `needs_action`) |

MVP point: **every lane has the same four slots, with the same names and
agent bindings.** Only the `mode` chip varies between lanes. The per-lane
slot model exists so the future "vary it up" path is a data-model change,
not a layout rewrite.

### Slot chrome

Each slot in an occupied lane is drawn as a column of the lane's CSS grid.
The slot's header sits at the top of the column and carries:

- The slot name in small caps (e.g. `PLAN`).
- The mode chip (`⚡` / `▶` / `⊘`) at the right of the header. Clicking
  the chip cycles ⚡ → ▶ → ⊘ → ⚡ for *this lane only*.

The slot body is the area below the header where the card sits when it
resolves to this slot.

Empty lanes show the same slot row but faint, with the headers visible
(so the user knows what the lane will look like) and no mode chips
(modes can't be cycled until the lane is occupied — they're per-lane
state).

### Stage modes

| Mode | Symbol | When the card lands in this slot |
|------|--------|---------------------------------|
| Auto | ⚡ | Controller dispatches the slot's agent immediately. |
| Manual | ▶ | Card shows a play button overlay; waits for the user to click. |
| Skip | ⊘ | Controller transitions the issue's state to the slot's `exit_state_success` immediately. The card slides through the slot without resting. |

The chips are *on the page*, not in a settings panel. Cycling a chip
persists immediately to the lane's row in `pipeline_lane_slot`.

## Cards

The issue card lives inside whichever slot matches the resolver. The card's
*horizontal position* is the progress indicator — no separate stepper, no
station circles, no connector lines.

When a slot completes, the card re-renders in the next slot's body. The
slide animation comes from `motion/react`'s `<m.div layoutId={`card-${issueKey}`}>`
FLIP — placement changes animate without coordinate maths.

Card chrome (the visual vocabulary, all CSS classes on the card itself):

| Appearance | Meaning |
|------------|---------|
| Static card | Slot complete, waiting (e.g. manual mode awaiting click) |
| Pulsing border | Currently dispatched (agent running) |
| Play-button overlay | Manual mode, awaiting user click |
| Red ring + `⚠` badge | Stuck / errored / no slot matches issue state |

The card is a slim wrapper around the existing `KanbanCard.jsx` chrome — the
pulse-border, blocked-by indicator, todos counter, and kebab menu primitives
all transfer.

## Backlog

Left panel (240px). Lists every `todo` issue for the active repo, priority
order then created-at. 2-column grid; virtualised when >100 items.

Each backlog card: issue key, title, priority chip, feature emoji (if any).
A slimmer `<MiniCard>` variant of the lane card.

Affordances:

- **Drag a backlog card onto an empty lane** — lane becomes occupied; the
  controller claims the issue and the resolver places the card in the slot
  whose `enter_state` matches the issue's current state (typically `todo`,
  so the card lands in the Plan slot).
- **Search input** at the top — debounced 150ms client-side substring
  filter on title + tag.

There is no auto-pull. v1 had `backlog_auto_pull` controlling whether the
controller filled empty lanes from backlog automatically; v2 drops it
because lanes are user-added, not pre-allocated. The user adds a lane when
they want one, then drops a card on it.

## To Ship queue

Right panel (240px). Issues that have finished their lane's slots and are
ready for the ship dispatch.

- **Max concurrency: 1.** Always. The whole point of the queue.
- The top card is the one being shipped (or about to be).
- A mode chip (`⚡` / `▶`) sits at the top of the To Ship panel:
  - `⚡`: as soon as the ship slot is free, dispatch the top card.
  - `▶`: a "Ship next" button at the top of the panel.

When a ship dispatch completes successfully, the card leaves the queue.
The originating lane (the one that fed this card to To Ship) was already
removed when the card crossed into the queue — there's no lingering empty
lane after shipment.

## How an issue flows

```
backlog ──drag──▶ empty lane ──occupied──▶ Plan ──▶ Impl ──▶ Review ──▶ Fix-rev ──▶ To Ship ──ship──▶ done
                                              ▲                  │           │
                                              └── rework ────────┘  (skip / manual chips
                                                                     gate each transition)
```

1. **Add a lane.** User clicks "+ Add lane" → empty lane appears.
2. **Drop a backlog card.** Lane becomes occupied with that issue. The
   resolver puts the card in the slot matching the issue's current state
   (typically Plan).
3. **Advance through slots.** Each slot's chip decides:
   - `⚡`: controller dispatches the slot's agent on arrival.
   - `▶`: card shows ▶ overlay; user clicks to dispatch.
   - `⊘`: controller transitions state to `exit_state_success`
     immediately; no dispatch.
4. **Worker runs.** Same `bacio agent dispatch` path as today. When the
   worker releases with the slot's `exit_state_*`, the resolver re-runs and
   the card slides to the new slot.
5. **Loop on rework.** Review → `needs_action` slides the card left to
   Fix-review. Fix-review → `in_review` slides it right back to Review.
6. **Lane finishes.** When Review releases with `done`, the card moves into
   the To Ship queue and the lane is removed.
7. **Ship.** Single-concurrency dispatch. On success, the To Ship card
   disappears.

## State-to-slot resolver

Pure function shared between Go (controller) and TypeScript (frontend).

```ts
type Resolution =
  | { kind: 'slot'; slot: Slot; idx: number }
  | { kind: 'backlog' }
  | { kind: 'shipping' }
  | { kind: 'shipped' }
  | { kind: 'stuck' };

function resolveSlot(
  state: State,
  activeVerb: string,        // BoardCard.activeVerb
  slots: Slot[],             // this lane's slots
): Resolution
```

**Rules (sharpened by BACI-212), in order.** The critical fix landed in
this pass is rule 5 — `activeVerb` precedence. Without it, the resolver
miscategorises the Plan-vs-Implement case: `bacio agent claim` auto-
transitions an issue to `in_progress` regardless of source state
(BACI-126a — see [`internal/schema/registry.go`](../internal/schema/registry.go)
row `agent.claim`), so when Plan starts on a `todo` issue, the issue
flips to `in_progress` immediately. Without the activeVerb-precedence
rule, the resolver would walk `enter_state === in_progress` (matching
only Implement) and slide the card into Implement *while Plan is still
running*. activeVerb on the BoardCard wire
([`internal/boardcards/cards.go:169-174`](../internal/boardcards/cards.go))
disambiguates: "the worker for this slot is mid-run, so the card sits
in its slot regardless of any intermediate state flip."

1. `state === 'todo'` AND no activeVerb → `backlog`.
2. `state === 'done'` AND activeVerb matches the ship template's slug →
   `shipping` (the card is in the active To Ship slot, ship dispatch in
   flight).
3. `state === 'done'` (no shipping verb) → `shipped` (card sits in the
   To Ship queue awaiting dispatch).
4. `state === 'cancelled'` → `stuck` from the resolver, dropped from view
   at the page level (cancellation is quiet; the controller mirrors this
   by removing the lane on the next `pipelineTick`).
5. **`activeVerb` is non-empty:**
   - Walk slots left-to-right; pick the first whose `agent` matches
     activeVerb. That's the slot — the worker is mid-run, the card sits
     in its slot regardless of any intermediate state flip the claim
     performed.
   - If no slot matches activeVerb (e.g. a slot's `agent` was renamed
     out from under us, or the user dispatched a one-off template from
     the kebab menu that no slot owns) → fall through to step 6.
6. Find slots whose `enter_state === state`:
   - Zero matches → `stuck`.
   - One match → that slot.
   - More than one (future per-lane variation case): walk left-to-right,
     take the leftmost. (Without an activeVerb to disambiguate further,
     leftmost is the deterministic tie-break.)

**Rework loop walk-through** (the case the spec must keep handling
correctly):

| State | activeVerb | Rule | Resolved slot |
|---|---|---|---|
| `in_review` | (empty) | 6 (one match) | Review |
| `in_review` | `reviewing` | 5 | Review |
| `needs_action` | (empty) | 6 (one match) | Fix-review |
| `in_progress` | `fix-reviewing` | 5 | Fix-review (not Implement — the activeVerb match wins) |
| `in_review` | (empty, after Fix-review releases) | 6 | Review |

`activeVerb` is already on the BoardCard wire
([`internal/boardcards/cards.go:169-174`](../internal/boardcards/cards.go))
— no dispatch-history walk needed.

## Data model

Per-repo lanes; per-lane slots. SQLite schema:

```sql
CREATE TABLE pipeline_lane (
  id INTEGER PRIMARY KEY,
  repo_id INTEGER NOT NULL,
  position INTEGER NOT NULL,           -- creation order, 0 = topmost
  issue_key TEXT,                      -- null = empty; non-null = occupied
  created_at TIMESTAMP NOT NULL,
  UNIQUE (repo_id, position)
);

CREATE TABLE pipeline_lane_slot (
  lane_id INTEGER NOT NULL REFERENCES pipeline_lane(id) ON DELETE CASCADE,
  position INTEGER NOT NULL,           -- 0 = leftmost slot in this lane
  name TEXT NOT NULL,
  agent TEXT NOT NULL,                 -- dispatch mode slug
  mode TEXT NOT NULL,                  -- 'auto' | 'manual' | 'skip'
  enter_state TEXT NOT NULL,
  exit_state_success TEXT NOT NULL,
  exit_state_rework TEXT,              -- nullable; set on Review only
  PRIMARY KEY (lane_id, position)
);

CREATE TABLE pipeline_to_ship (
  repo_id INTEGER PRIMARY KEY,
  mode TEXT NOT NULL DEFAULT 'auto'    -- 'auto' | 'manual'
);
```

When the user clicks "+ Add lane", the controller inserts one
`pipeline_lane` row and four `pipeline_lane_slot` rows (the hardcoded
defaults). Cycling a mode chip writes one `pipeline_lane_slot` row.

There is **no global `pipeline_stage` table** in v2. Stages are per-lane.

For MVP, the slot defaults are hardcoded in Go and inserted by `add-lane`;
the frontend never reads stage definitions from the server (it could
hardcode them too), so the per-lane storage is mostly for the mode chip
state. The schema is sized for the future-flexibility path — letting one
lane skip Review while another keeps it — without locking the layout in.

### Edge cases (sharpened by BACI-212)

1. **Lane positions don't compact on remove.** `pipeline_lane.position`
   is `UNIQUE (repo_id, position)` and `add-lane` appends
   `MAX(position) + 1`. Removing a middle lane leaves a gap (positions
   `0, 1, 3` after removing 2). That's fine — the frontend orders by
   `position ASC` and gaps are invisible. No compaction job needed,
   and we explicitly do *not* re-pack on every remove (it would touch
   N rows for one user action).
2. **`issue_key` is text, not a foreign key.** A deleted issue would
   orphan the `issue_key` string on its lane row. The controller's
   `pipelineTick` notices this on the next tick (the resolver returns
   `stuck` because the issue lookup fails) and the lane gets the `⚠`
   badge. The user can then ✕ it. We deliberately *don't* add a FK
   constraint — `pipeline_lane` is per-repo; an `issue_key` referring
   to another repo's issue would be invalid anyway, and that check is
   cheaper to do at the store layer (`BindLaneIssue` walks the issue
   lookup) than via foreign keys.
3. **`pipeline_to_ship` is one row per repo, lazily created.** First
   `set-to-ship` for a repo INSERTs; subsequent calls UPDATE. The
   `GetPipeline` read does a LEFT JOIN with a `COALESCE(mode, 'auto')`
   default so the absent row reads as `'auto'`. Avoids a "seed on repo
   create" migration.

## CLI

A new `bacio pipeline` verb manages lanes + slots. Every mutation
satisfies all six [`agent-cli-principles.md`](agent-cli-principles.md)
rules:

| Verb | Schema name | Audit op | `--json` payload | `--dry-run` projection | Lean output |
|---|---|---|---|---|---|
| `bacio pipeline get [--json]` | n/a (read) | n/a (reads aren't audited) | n/a — no mutation | n/a | Slot rows are small; full inflation. |
| `bacio pipeline add-lane` | `pipeline.add-lane` | `pipeline.create` | `{}` (empty — defaults are hardcoded). Future: `{stages: [...]}` for variation. | Projected lane row + its four projected slot rows; `id` / `created_at` zero. | Returns the full new lane + slots inline. |
| `bacio pipeline rm-lane <id>` | `pipeline.rm-lane` | `pipeline.delete` | `{lane_id: int, on_occupied: "release" \| "cancel"}` | Cascade preview: slot rows that will cascade-delete; issue's projected next state. | Returns the deleted lane row. |
| `bacio pipeline set-slot` | `pipeline.set-slot` | `pipeline.update` | `{lane_id: int, position: int, mode: "auto" \| "manual" \| "skip"}` | Projected slot row with the new mode. | Returns the updated slot row. |
| `bacio pipeline set-to-ship` | `pipeline.set-to-ship` | `pipeline.update` | `{mode: "auto" \| "manual"}` | Projected to-ship row. | Returns the updated row. |

Notes:

- **Rule 4 (validators at the store boundary).** `mode` values go through
  a `validateSlotMode` helper checking the three legal strings
  (`auto / manual / skip`) plus a separate `validateToShipMode` for the
  two-value queue chip; slot `agent` slugs go through `ValidateSlug`;
  slot `name` goes through `ValidateName`. No new validator class —
  reuse what's already in `internal/store/validate.go`.
- **Rule 6 (SKILL.md).** Append a `bacio pipeline` line to the "What's
  in the box" section of `.claude/skills/bacio/SKILL.md` on the same
  commit as the CLI lands.
- `bacio pipeline get` is a read; rules #1, #2, #5 don't apply (no
  `--json` payload, no schema entry, no `--dry-run`). Rule #3 — slot
  rows are tiny; no opt-in inflation needed. Reads are not audited
  (current bacio precedent — only mutations land in `bacio history`).
- Per-lane stage variation (`add-slot` / `rm-slot` / `reorder-slot`)
  is deferred — those verbs land when the variation UI does.

## User-visible interactions

Every interaction (page-level chrome aside):

| Interaction | Effect |
|---|---|
| Click "+ Add lane" | Inserts a new empty lane at the bottom. |
| Click ✕ on empty lane | Removes the lane. No confirmation. |
| Click ✕ on occupied lane | Confirm dialog → issue state goes to `todo`, lane removed. |
| Drag backlog card onto empty lane | Lane becomes occupied; resolver places card in the matching slot. |
| Drag backlog card onto occupied lane / outside any lane | Drop rejected with a brief toast. |
| Click mode chip on a slot header | Cycles `⚡ → ▶ → ⊘ → ⚡` for that (lane, slot). Persisted. |
| Click ▶ overlay on a card | Dispatches the slot's agent. Card flips to pulse-border on accept. |
| Click ⚠ badge on a stuck lane | Radix dropdown: Retry · Skip · Send back to backlog · Cancel issue. |
| Click card body | Opens the existing per-issue workspace at `/issues/:key`. |
| Click card kebab menu | The same per-card menu the kanban card carries. |
| Click "Ship next" (manual-mode To Ship) | Dispatches the ship agent on the top To Ship card. |
| Click To Ship mode chip | Cycles `⚡ ↔ ▶`. |

Things deliberately absent from the MVP:

- Drag a card backwards across slots (no data-model meaning).
- Drag lanes to reorder (lanes are positionless).
- Per-issue mode overrides (slot modes are per-lane; that's already an
  override surface).
- Configure-pipeline drawer (modes are on the page).

## Stuck lanes

A lane is "stuck" when:

- A dispatch errors (worker crashes, hook denies, etc.).
- A worker ends without releasing.
- The user moves the issue to a state no slot owns.

The card gets a red ring; the lane's right edge gets a `⚠` badge. Clicking
the badge opens a Radix dropdown. Each menu item is a one-shot mutation
against the existing CLI; no new verbs needed for the stuck-menu
(BACI-212 sharpening):

- **Retry** — re-dispatch the resolver-suggested slot's agent via
  `bacio agent dispatch`. Same code path as the `auto` mode tick.
- **Skip** — transition issue to the suggested slot's `exit_state_success`
  via `bacio issue state`. No dispatch. Card slides to the next slot.
- **Send back to backlog** — `bacio issue state <key> todo`, then
  unbind the lane's issue (`pipeline_lane.issue_key = NULL`). Lane goes
  back to empty.
- **Cancel issue** — `bacio issue state <key> cancelled`. The next
  `pipelineTick` notices the lane's issue is cancelled and removes the
  lane.

## Controller: `pipelineTick`

The leader-elected controller (existing) gains a `pipelineTick` step on
the event bus (issue state change, session ended, dispatch state change).
Per repo with at least one lane:

1. For each occupied lane: resolve the current slot from
   `(issue state, activeVerb, this lane's slots)`. If the slot's `mode` is
   `auto` AND there's no active dispatch for this issue AND the previous
   slot is complete (or this is slot 0), dispatch.
2. For each occupied lane where the resolver returns `shipped`: move the
   card into the To Ship queue and remove the lane.
3. For each occupied lane whose issue has been deleted or whose state is
   `cancelled`: remove the lane (the resolver-orphan and cancellation-
   sweep cases — see Data model edge case 2).
4. For the To Ship queue: if mode is `auto` AND in-flight ship count is 0,
   dispatch the top card.

Backpressure is implicit. The user controls in-flight concurrency by how
many lanes they add. No `lane_count` cap.

## Open questions (deferred, not blocking)

All four items below were checked at BACI-212 lock-in and are *explicitly*
deferred — nothing load-bearing for the MVP is left dangling.

- **Per-lane stage variation UI.** The data model supports it, but how
  does the user add/remove/edit a slot inside a lane? Not MVP — every
  lane ships with the same four slots. Adding a "Stages…" affordance to
  each lane is a follow-up once the four-slot version is stable.
- **Ship history strip.** A small "Shipped today" strip below the To Ship
  queue could show recently shipped cards. Not MVP — the existing
  `ShippedPopover`
  ([`desktop/frontend/src/components/ShippedPopover.jsx`](../desktop/frontend/src/components/ShippedPopover.jsx))
  already covers the same need on the kanban surface.
- **Backlog priority editing.** Drag to reorder backlog. Currently
  read-only by repo priority. Probably a separate feature.
- **Multi-repo view.** Cross-repo lanes are possible but not MVP — the
  kanban "all" pseudo-board doesn't have lanes; consistency with that
  surface argues for "Pipeline" being single-repo.

## Why this is simpler than v1

- **No shared header row** — every layout primitive lives inside a lane,
  so the layout is a stack of identical components, not a grid + header.
- **No default state** — empty page on first load means no controller
  pre-population, no "is this a new repo or did the user delete all
  lanes" ambiguity.
- **No configure drawer** — the mode chips are the configuration. Removes
  ~half of the v1 spec (settings UI, lane-count slider, stage-reorder,
  add-stage / rm-stage handles).
- **Per-lane data model** — future "vary it up" is a data change, not a
  layout rewrite. The MVP just doesn't expose the variation surface yet.

The page's job is: show in-flight work, let the user start/finish/recover
items. v2 does that in a flat stack of lanes with no global controls
above them.

