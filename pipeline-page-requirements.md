# Pipeline Page — Requirements

> Status: draft. Captures the functional definition of how the Pipeline page
> should work. Functional ambiguities have been resolved with the author and are
> baked in below; remaining implementation detail is under
> [Open questions](#open-questions).

## 1. Purpose

The Pipeline page is a three-column board that moves an issue from backlog →
processing → shipping. It **replaces the issues board** as the single surface
for driving work (see [§10](#10-deprecating-the-issues-board)). Unlike the
current UI mock (drag placement lives in component state and is lost on
unmount), this spec requires the pipeline state and the per-issue dispatch-job
history to be **persisted** (see [§8 Data model](#8-data-model--persistence)).

### Designs to use

Flat HTML mockups (built on bacio's real design tokens) are the agreed visual
reference for the build:

- [`pipeline-card-mockups.html`](pipeline-card-mockups.html) — **authoritative
  card design.** The compact Backlog / Shipping issue card (feature glyph,
  number, conditional plan + PR icon buttons, title, labels) and the in-process
  card that **grows to fill the stage** — issue header on top, processing detail
  (job chain + active-job todos / question) in the body, and **all operation
  buttons along the bottom**. Covers running / halted-on-question / complete /
  just-dropped. _(Light theme; the same tokens drive dark.)_
- [`pipeline-page-mockups.html`](pipeline-page-mockups.html) — page- and
  column-level context: the three-column frame, backlog drawer numbering, and the
  shipping controls. Where its in-pipeline card detail differs, the card mockup
  above wins.

## 2. Layout

Three columns, left to right:

| Column | Issue state | Role |
|---|---|---|
| **Backlog** | `todo` | Prioritised queue of issues waiting to be worked. |
| **In Pipeline** | `in_pipeline` *(new)* | Cards actively being processed by a chain of dispatch jobs. |
| **Shipping** | `to_be_shipped` *(new)* | FIFO queue of finished cards waiting to ship. |

`in_pipeline` and `to_be_shipped` are **new bacio issue states** (added to the
existing `todo | in_progress | needs_action | in_review | done | cancelled`
enum). A card's **column is its issue state**. "Shipped" (the existing count
pill / popover) is the terminal already-shipped set (`done`) and is **not** one
of the three columns.

## 3. Backlog column

- Ordered by **priority**: the next-to-go card is at the **top**, descending in
  priority going down.
- **Scrolls** vertically when the list overflows the column height.
- Users can **reorder** the list (drag to set priority). Order is persisted —
  it is the queue order the rest of the pipeline consumes.

### 3.1 Expanded drawer view

When the backlog drawer is expanded (the `»` toggle widens the panel into a
multi-column grid):

- Cards fill **top-to-bottom, then right-to-left**: the rightmost column fills
  downward first, then the next column to the left. **Position 1 (next to go)
  lands top-right.**
- Each card shows an **overlaid number badge at its top-right corner** with its
  priority position (1 = next to go), so order is unambiguous in a grid. The
  badge shows while the panel is open (expanded).

## 4. Shipping column

- **FIFO**, shown from the top: the **top card (position 1) is the next to
  ship**.
- Users can **reorder** the cards here too (overrides strict FIFO when needed).
- **SHIP dispatches the `ship` agent.** Shipping a card — whether via the manual
  SHIP button or auto-ship — **dispatches a `ship`-mode agent** against the top
  card.
- **Auto-ship toggle**: when on, the system automatically dispatches the ship
  agent for the next (top) card.
- **Manual SHIP button**: when auto-ship is off, the user initiates the ship
  dispatch for the next card themselves.

## 5. Dispatch jobs & processes

Issues now carry a **collection of dispatch jobs** that are assigned while the
issue is in the `in_pipeline` state.

### 5.1 Choosing a process

When a card **enters** `in_pipeline` — either by the user dragging ("drawing")
it in, or by the **zap button** pushing it from the backlog — a **menu appears
over the card** to select the process (the job chain) for managing it.

Starter processes:

1. **Plan → Implement → Ship**
2. **Implement → Ship**
3. **Plan → Implement**
4. **Plan** (single job)
5. **Implement** (single job)

Selecting a process **adds one row per dispatch job** to the card. A trailing
**Ship** stage is a hand-off, not an agent job — see
[§6](#6-job-execution-controls).

### 5.2 Issue card vs processing area

Strict separation inside the larger stage card (today:
`.mk-pipeline-stage-card`, with the issue card at
`.mk-pipeline-stage-card-issue`):

- **Inner issue card** — represents **the issue itself** only: its key, title,
  tags, and intrinsic state. Nothing about agent processing.
- **Surrounding area** (outside the issue card, inside the stage card) — all the
  **agent-processing UI**:
  - the **job chain**: the list of jobs to do, which one is **in process**, and
    which are **done**;
  - an **asked-question** indicator when the current job is waiting on the user
    (see [§6.1](#61-asked-questions));
  - the agent's **todo / task list** information for the active job;
  - other progress / status detail.

The card is **always `in_pipeline`** while in the pipeline — the processing
engine does **not** use the `in_progress` / `needs_action` / `in_review` issue
states at all. Per-job status (pending / running / complete / cancelled) and any
**open question on the current job** drive the surrounding UI; the card only
leaves `in_pipeline` when it hands off to Shipping.

## 6. Job execution controls

Each `in_pipeline` card has:

- **Start** — runs the **next** dispatch job, then stops.
- **Stop / Cancel** — cancels the running job.
- **Auto** — keeps running jobs consecutively without manual prompting; it
  **halts on an asked question** ([§6.1](#61-asked-questions)) and resumes once
  it's answered.

**"Ship" is a hand-off, not a dispatch.** A `Ship` stage in a process (and the
Ship button below) does not run an agent — it moves the card into the Shipping
column, where the actual shipping happens ([§4](#4-shipping-column)). Processes
that don't end in a `Ship` stage (e.g. Plan → Implement, or a single job) reach
the same hand-off via the Ship button / Auto.

Completion behaviour:

- When the **last job completes** and **Auto is on**, the card moves to the
  **Shipping** column automatically (the hand-off runs itself).
- When the last job completes and **Auto is off**, a **Ship button** appears so
  the user can move the card to Shipping manually.

### 6.1 Asked questions

The processing engine does not flip the issue to `needs_action`. Instead,
questions keep the **same shape they have today** (the existing
`bacio agent questions` records) but are now **associated with the dispatch-job
row** rather than the issue or agent session. An **open question on the current
job** is the signal that it's waiting on the user. While one is open:

- the surrounding area surfaces the question, and
- **Auto halts** — it will not dispatch the next job until the question is
  answered (it does not skip ahead).

Answering the question lets Auto resume.

## 7. Editing stages

- The user can **edit** stages and **add more** stages as needed.
- Once a stage is **complete it cannot be modified** (completed jobs are
  immutable; only not-yet-started stages are editable).

## 8. Data model & persistence

Persistence is **in scope** — the pipeline is backend-backed in bacio, not a
front-end mock. The following must persist:

- **Two new issue states** — `in_pipeline` and `to_be_shipped` — added to the
  enum (`todo | in_progress | needs_action | in_review | done | cancelled`). A
  card's column is its issue state: Backlog=`todo`, In Pipeline=`in_pipeline`,
  Shipping=`to_be_shipped`, Shipped=`done`. This touches the state enum across
  every surface (CLI, TUI, web, dispatch).
- **Backlog and shipping order** (a priority / ordering field per issue);
  reordering writes this, not board-local display state.
- The per-issue **dispatch-job collection** — the chosen process and each job's
  status (pending / running / complete / cancelled) — so job history survives
  reloads and is the source of truth. The engine does **not** use the
  `in_progress` / `needs_action` / `in_review` issue states; the card stays
  `in_pipeline` throughout.
- **Questions move onto the job row.** The existing `bacio agent questions`
  records keep their shape but are now associated with a **dispatch-job row**
  (not the issue / agent session); an open one on the current job is the
  "waiting on the user" signal ([§6.1](#61-asked-questions)).
- Completed jobs are **immutable** ([§7](#7-editing-stages)).

### 8.1 Every issue belongs to a feature

Features become **mandatory** — every issue must belong to one (today they are
optional). To make that seamless:

- Each repo has a **default feature**; an issue created without an explicit
  `feature_slug` is assigned the default (extends the existing
  `bacio settings default-feature`).
- A **new install bootstraps two catch-all features, `bugs` and `maintenance`**,
  with the repo default pointing at `maintenance` _(assumption — confirm)_. Both
  catch-alls run with **auto-close off** so the completion sweep never archives
  them out from under newly-filed issues.

This makes the **Features page the home for non-pipeline issues**: since every
issue sits under a feature, `done` / `cancelled` (and any other) issues are
found by opening their feature ([§10.1](#101-what-still-needs-a-home)).

## 9. Prompt simplification

The agents **no longer manage job or issue state at all** — the processing
engine owns it. Concretely, the per-mode `prompts/agents/*.md` lose:

- the **done-tagging** step — the dispatch-job history records completion, so
  the tag convention is redundant; and
- any **state-transition** steps (setting `in_progress` / `needs_action` /
  `in_review` / etc.) — the card is always `in_pipeline`, job status is tracked
  by the engine, and a paused job is signalled by an **open question on the job
  row** ([§6.1](#61-asked-questions)) rather than a state change.

## 10. Deprecating the issues board

The Pipeline **replaces** the issues board — the state-column kanban at
`/issues` (the "Issues" tab, internal view `board`, `Board.jsx`). The board is
**removed entirely**; the Pipeline becomes the only board surface for driving
work.

Removed with it — the board's whole work-management layer:

- the **state-column kanban** and **drag-between-columns** state changes
  (`onMoveCard`);
- per-card **dispatch** and **dispatch-chain** (`onDispatchFromCard`,
  `onDispatchChainFromCard`);
- **cancel-waiting** (`onCancelWaitingCard`);
- **follow-on** queue / cancel (`onSetFollowOn`, `onCancelFollowOn`);
- **quick-eval** (`onQuickEval`);
- the board-only column collapse / compact display state.

These are superseded by the Pipeline's dispatch-job processes ([§5](#5-dispatch-jobs--processes)–[§6](#6-job-execution-controls))
and its backlog / shipping flows ([§3](#3-backlog-column)–[§4](#4-shipping-column)).

### 10.1 What still needs a home

Issue CRUD the board hosted must remain reachable without it:

- **Create** — the existing "New issue" (`+`) composer.
- **View / edit** — opening a card (the issue workspace).
- **Browsing non-pipeline issues** — issues outside the three pipeline columns
  (`done` / `cancelled`) live on the **Features page**: every issue belongs to a
  feature ([§8.1](#81-every-issue-belongs-to-a-feature)), so you open the feature
  to see all its issues. Search (⌘K) still covers direct lookup.

## 11. Open questions

None blocking. One assumption to confirm: the new-install default feature is
`maintenance` (with `bugs` as the second bootstrapped catch-all) —
[§8.1](#81-every-issue-belongs-to-a-feature).

_(The rest of the functional design is settled — question records reuse the
existing `bacio agent questions` shape, re-parented onto the dispatch-job row,
[§6.1](#61-asked-questions).)_
