# Frontend architecture (`desktop/frontend/src`)

How the React tree is structured *after* the BACI-350 frontend-architecture
modernization (Phases 0–5). Read this before any non-trivial frontend change so
your code lands on the existing grain instead of re-growing the patterns the
modernization removed. It is the **data-flow & module-layout** companion to two
adjacent docs:

- [`.claude/rules/frontend-typescript.md`](../.claude/rules/frontend-typescript.md) — the hard typing rules (no `any`, the `./api` seam, the cross-transport enum footgun). Auto-loads when you edit `desktop/frontend/**`.
- [`docs/web-app-mode.md`](web-app-mode.md) — the Wails-vs-HTTP transport seam itself.

## The shape: four layers, hooks underneath

```
components/   ← view layer: thin, decomposed, read data from state/ + hooks
state/        ← global app state: React Context providers + co-located mutations
api/          ← the data seam: one DTO contract, two transports, per-domain barrels
lib/hooks/    ← cross-cutting primitives every layer composes
```

The headline outcome of the modernization: `App.tsx` is a **thin shell** (~370
lines, single-digit `useState`) that mounts the providers, the `<Routes>`, and
the overlay siblings. It no longer owns global state, polling loops, or the
~30 optimistic-mutation handlers — those moved down into `state/` and
`lib/hooks/`.

## 1. The hooks-first data pattern (`lib/hooks/`)

Before the modernization, every self-fetching component hand-rolled the same
triad: `useState(initial) + useEffect(fetch) + .catch(reportError)`, plus a
hand-written stale-load guard that several copies got subtly wrong. **Do not
write that triad again.** Reach for the primitives:

| Hook | Use it for |
|---|---|
| `useAsyncResource<T>(fetcher, initial, deps, opts)` | The load + `loading` + `error` + manual `refresh()` shape. Owns the monotonic stale-load guard and the latest-callback refs once. Returns `{ data, loading, error, refresh, setData }`. |
| `usePolledResource<T>(...)` | `useAsyncResource` + a background poll on `POLL_INTERVAL_MS`. The eager load surfaces failures per policy; **the poll is always silent** (a flapping network can't fan a transient failure into a stack of modals). |
| `useOptimisticToggle(initial, persist, opts)` | The simplest optimistic dance: a self-owned boolean that flips on screen, persists in the background, reverts on failure. The PipelineView display toggles + `FeaturePropertyToggle` consume it. |
| `useOptimisticMutation()` | The full snapshot → optimistic update → `await persist` → reconcile / rollback → `reportError` orchestrator. The card mutation handlers route through `mutate({ optimisticUpdate, persist, onSuccess, rollback, errorHeadline })`. |
| `useLocalStorage(key, initial, codec)` | Persisted UI state (shipped-scope window, backlog-collapse). Round-trips through a codec and falls back gracefully when storage throws. |
| `useInterval(fn, ms, enabled)` | The bare timer primitive (Dan Abramov's latest-ref pattern). `usePolledResource` composes it; reach for it directly only for non-resource ticks. |

**The `silent`-vs-`reportError` decision lives in the hook, not the call site.**
`useAsyncResource` routes a failed load through `reportError` (with the
configured `errorHeadline`), unless `silent: true` (→ `console.warn`, the
convention for best-effort polls / live badges) or a custom `onError` is
supplied. When you migrate a fetch, pick the option that matches today's
behaviour — don't re-add an inline `.catch`.

These hooks are the most-tested code in the tree (`lib/hooks/__tests__/`);
mirror their test style (load / loading / error / silent / rollback branches)
when you add one.

## 2. The `api/` seam — one contract, two transports

`src/api.ts` (Wails) and `src/api.http.ts` (HTTP) are the two transport
implementations of the same surface, selected by the Vite alias (see
`docs/web-app-mode.md`). They are now thin **barrels** over per-domain modules:

- **`api/contract.ts`** — the single source of truth for the ~48 shared DTO
  interfaces (`BoardCard`, `IssueDetail`, `Board`, …). It has **no runtime
  imports**. Both transports `satisfies` it, so a web-only drift becomes a
  `tsc` error instead of a silent runtime bug. **Add a new DTO here, once.**
- **`api/wire/*`** — the snake_case `Api*` wire interfaces + the reshape helpers
  (`reshapeApiBrief`, `mapApiComment`, …) that convert wire → DTO, grouped by
  domain. Pure and unit-tested (`api/wire/__tests__/`).
- **`api/<domain>.ts` / `<domain>.http.ts`** — per-domain modules (`board`,
  `issue`, `feature`, `pipeline`, `agents`, `settings`, `sync`, `monitor`,
  `notifications`, `docs`) re-exported by the `api.ts` / `api.http.ts` barrels.
- **`api/normalize.ts`** — the shared Wails-error normaliser, so the per-domain
  wrappers stay terse and the React layer sees a consistent `Error`.

Rule of thumb: **types come from `contract.ts`, runtime functions come from the
`./api` barrel.** Never import a runtime value from `bindings/...` directly, and
never reference a Wails enum *member* at runtime — both break `build:web`. See
the rules doc for the exact casts.

`Board.kind` is the live worked example: it is a **string-literal union**
(`type RepoKind = 'git' | 'workspace'`) declared in `contract.ts`, deliberately
**not** a Wails enum. `api.http.ts` ships Wails enum names as *types only*, so a
component that wrote `RepoKind.Workspace` would pass `tsc` against the Wails
build and blow up in the browser. Compare against the literal
(`board.kind === 'workspace'`) or cast one (`'workspace' as RepoKind`) — never a
member. The Wails binding types `kind` as a bare `string`, so `api/board.ts`'s
`toBoard` narrows it through `normalizeRepoKind` (which also maps a legacy `''`
onto `'git'`) before the DTO leaves the seam.

### 2a. Four reshaping decisions the two transports had to agree on

The Kanban and page-tree surfaces are where the two transports diverged most, and
the contract picked a winner in each case. Follow these when you extend either.

1. **`uuid`, never `id`.** The HTTP wire nests the full `model.KanbanColumn` /
   `model.DocFolder` — numeric `id`, `repo_id`, timestamps, snake_case — while
   Wails ships a lean camelCase DTO. The contract exposes **`uuid` only**; the
   reshapers in `api/wire/kanban.ts` / `api/wire/doc.ts` drop the numeric ids.
   A uuid is the only identity that survives a sync round trip, and every
   mutator on both transports is uuid-addressed.
2. **`parentUuid`, not `parent_id`.** HTTP's `DocFolder` carries a numeric
   `parent_id` (absent = root); the contract's `DocFolder.parentUuid` is a
   string where `''` means the tree root — a *value*, not an absence, because
   the root is not itself a folder and `''` is the only way to address it. The
   HTTP side builds an id→uuid map from the folder list it just fetched
   (`folderUuidIndex` / `resolveFolderUuid` in `api/wire/doc.ts`) — do that in
   the wire module, never in a view.
3. **`DocSummary.folderUuid`, not `folderId`.** Same reasoning, same index. The
   HTTP `Document` still carries `folder_id` on the wire; `reshapeDocSummary`
   resolves it. The field is required rather than optional, because `''` (root)
   is meaningful.
4. **Reorder / delete / move return the refreshed board.** The Wails
   `ReorderKanbanColumn` / `DeleteKanbanColumn` / `SetIssueKanbanColumn` answer
   with the whole `KanbanColumn[]`; the HTTP routes answer with the single moved
   lane (or 204). Siblings **re-densify** underneath either write — positions are
   dense 0-based indices server-side — so the single-row answer is never enough
   to repaint. The seam signature is therefore "returns the refreshed board" on
   both sides, and `api/kanban.http.ts` re-reads `listKanbanColumns` after the
   write to honour it.

One more shared convention: a `Position *int` on the Go side binds as
`position: number | null` in the seam, and **`null` maps to an absent `position`
on the HTTP wire** — absent means "append", `0` means "top of the lane", and the
two must not collapse.

## 3. The `state/` layer — global state out of `App.tsx`

Global app state lives in a small stack of Context providers, each owning one
slice plus its co-located mutation handlers. They nest (outer → inner):

```
TooltipProvider
  PreferencesProvider   ← the 6 preference pairs + theme / timezone / audio
    RepoProvider        ← boards list, URL-derived activeBoard, openIssue/openCard nav
      AgentsProvider    ← the polled agent-session registry
        CardsProvider   ← the hot 10s-polled board cards + ~22 mutation handlers
          Shell         ← the prefix-unknown branch, <Routes>, overlay siblings
```

- A view reads global data through the matching `use*` hook
  (`useActiveRepo()`, `useCards()`, `usePreferences()`, …) — **not** through a
  30-prop drill from `App`.
- `CardsProvider` is the innermost data provider because it depends on
  `activeBoard` (RepoProvider) and `timezone`/`audioEnabled` (PreferencesProvider).
  It builds its cards resource on `useAsyncResource` and its mutations on
  `useOptimisticMutation` / direct `api.*` calls.
- `useOpenIssue` / `useNotifications` / `useLeaderStatus` are the standalone
  state hooks the providers and Shell compose.

The data-layer decision was deliberate: **Context + hooks, no new state
library** (the Phase 3 design pass ratified this — see the plan doc). Don't
reach for Redux / Zustand / React Query without re-opening that decision.

## 4. View decomposition (`components/<domain>/`)

The big view files were decomposed into per-domain folders of small components +
local hooks:

- `components/pipeline/*` — the Pipeline board: `PipelineCard`, `StageCard`,
  `ProcessMenu`, the `CardHead` / `CardTitleBlock` / `CardLabels` leaves, and the
  drag/drop + preferences hooks (`useDragDropLogic`, `useDragState`,
  `usePipelinePreferences`, `useStageCardState`).
- `components/kanban/*` — the Kanban board at `/<prefix>/issues`, the human-lane
  axis (see ARCHITECTURE.md's "two board axes"). `KanbanBoard` holds two
  resources and joins them: the lanes with their ordered card references from
  `api.listKanbanColumns`, and the card detail from the `CardsProvider` list the
  whole app already polls — **membership rides on the container, so `BoardCard`
  carries no lane field.** `KanbanLane` owns one lane's header and drop target;
  `KanbanCard` is a deliberately slim human-work card (emoji, key, title, tags,
  assignees, blocked lock) with none of the Pipeline's dispatch affordances;
  lane CRUD and the "put a card on the board" picker hang off the lane header as
  their own menu / dialog leaves. **The fiddly pure bits live in sibling `.ts`
  modules, not in the components** — the optimistic drag reshuffle and the
  container-side join in `kanbanPlacement.ts`, the off-board derivation in
  `kanbanOffBoard.ts`, the lane ordering arithmetic in `kanbanLanes.ts` — so
  ordering maths and irreversible-action wording are testable without a DOM. The
  `*Persistence.ts` modules hold the per-view UI state (scroll, collapse,
  compact). The DnD is the same hand-rolled HTML5 gesture the pre-pivot board
  used — **no DnD library.**
- `components/docs/*` — the Confluence-style page tree. `DocsTreeRail` is a
  single left rail replacing both pre-pivot left panes (facet rail + list), which
  is what buys the TipTap editor its width; it has two body modes and the parent
  usually picks — typing in search or activating a facet auto-flattens to ranked
  results and flips back when cleared. `DocsTreeNode` is the recursive row,
  `DocsFolderPage` the folder-selected surface (a real page with a live children
  index, so clicking a folder is never a dead end), `DocsNav` the breadcrumb +
  peer-jump. State splits three ways: `useDocsTree` (expansion, persisted per
  repo by folder **uuid**, plus auto-expanding down to the selection),
  `useDocsTreeDrag` (the same hand-rolled HTML5 DnD), and `useDocsActions` (every
  create/rename/delete/move round trip plus its dialogs). Tree assembly itself is
  pure and lives in `lib/docsFilter.ts`. `DocsViewer` and the TipTap
  `NotionEditor` are untouched by the decomposition — including the
  **ref-not-state** `initializedRef` guard (BACI-340).
- `components/features/*`, `components/settings/*` — the same treatment for the
  Epics and Settings screens. (The directory and the type names stay
  `feature*`; only the display term and the URL became "Epics".) `features/`
  also holds the two full-screen epic forms — `NewEpicPage` and `EditEpicPage`
  — plus the pure `epicForm.ts` (a TS mirror of `store.Slugify`, the reserved
  `new` slug, and the local collision check) beside them, the same
  fiddly-bits-in-a-sibling-`.ts` split `kanbanPlacement.ts` and `docsActions.ts`
  use. `EditEpicPage` is deliberately **two-speed**: Details (title,
  description, emoji, branch) batch through one `api.updateFeature` PATCH
  behind `Save details`, while the four properties keep the per-field
  optimistic setters they use on the detail pane — they render through the
  *same* `FeaturePropertyToggle` / `FeatureStateControl` components, so the
  identical control cannot behave differently on the two screens it appears on.
- `components/CreateMenu.tsx` — the shared create chrome, and the single place
  the rule lives: *a Plus in the top-right of the container the thing lands in;
  a menu when the container accepts more than one type, the create flow
  directly when it accepts one.* It exports the Topbar's multi-type
  `+ New ▾` menu and the `ScopedCreateButton` every surface header wears
  (Epics list head, Pipeline Backlog, the Kanban lane trigger's chrome). The
  global instance is not cosmetic: `navFor()` hides the Pipeline tab when
  `showAgentSurfaces` is off — the default for a manual workspace — and the
  Backlog header was the only place a "new issue" button lived, so a workspace
  previously had no in-app way to create an issue at all.
- `lib/nav.ts` — the top-nav data (`NAV`) plus `navFor()` / `homeView()`, which
  gate the tabs on the active space's `showAgentSurfaces` / `showKanban`. It
  lives in `lib/`, not in `Topbar.tsx`, because `RepoProvider` and `App` both
  need `homeView` and the plain-node smoke suites can't import a `.tsx`.
  `navFor()` is the *single* filter: `Topbar` renders it and `App` indexes it
  for the digit hotkeys, so filtering in one place only would desync the
  keyboard from the buttons. See `docs/web-app-mode.md` §7b.

**Hot leaf cards are `React.memo`'d** (`PipelineCard`, `StageCard` via
`memoCard.ts`'s `cardPropsEqual`) so the 10s poll re-render doesn't cascade
through every card's subtree. For the memo to bite, the parent must pass
**stable callbacks** — the drag handlers are stabilized with the latest-ref
pattern in `useDragDropLogic`, and PipelineView passes the providers' (already
`useCallback`'d) handlers straight through rather than per-render closures. If
you add a card callback, keep it stable, or the memo silently stops helping.

## 5. Error handling (`errors.ts`)

A single module-level error bus. `reportError(err, { headline })` pushes onto it;
the `ErrorModal` mounted in `App` surfaces one error at a time (back-to-back
duplicates coalesce). The hooks own the routing decision (§1), so component code
rarely calls `reportError` directly any more — when it does, the headline
follows the dominant convention: **`"Couldn't <verb> <object>"`**, double-quoted,
no trailing punctuation (e.g. `"Couldn't load boards"`). Keep new headlines in
that form.

### 5a. Ship sound (`lib/shipSfxEngine.ts`)

The second module-level singleton, for the same reason as `errors.ts`: it owns a
browser-global resource (an `AudioContext`) plus window-level gesture listeners,
and the Settings pane needs to read its state without prop-threading. The React
side is a thin facade — `lib/shipSfx.ts` keeps the Vite-only `kaching.mp3` import
and exposes `useShipSfx({ enabled }) → { play }` (CardsProvider's count-rise
effect) and `useShipSfxStatus()` (a `useSyncExternalStore` read for the Settings
status line). The pure decisions live in `lib/shipSfxGate.ts`, importable from
Node.

**The autoplay contract — read this before touching the unlock path.** The two
engines grant autoplay on different axes. Chromium uses **origin-level sticky
activation**: any past click on the origin permits a later `play()`. WebKit
(Safari, and the desktop app's WKWebView) grants **per-context**, and only when
the unlock runs inside a real gesture. A ship usually has no gesture of its own —
an agent moves the card and the count arrives on a poll — so the engine unlocks
eagerly on the user's first click: `new AudioContext()` + `ctx.resume()`, both
**synchronous** inside the handler (Safari loses the activation across an
`await`); the fetch + `decodeAudioData` follow asynchronously, needing no gesture.

The rule that BACI-375 exists to enforce: **never mark the sound unlocked
optimistically.** BACI-336 set an `unlocked` flag before its `play()` promise
settled, so on WebKit the flag went true, the attempt was refused, and the
first-gesture listener early-returned forever — invisible in Chrome, permanently
silent in Safari. "Am I unlocked" is now read straight off `ctx.state`, the
gesture listeners stay armed until it is genuinely `running`, and `onstatechange`
re-arms them if it later suspends or is interrupted.

## 6. The safety net — tests & lint

- **Vitest + Testing Library** under `__tests__/` folders, run under jsdom with
  the web-mode `./api` alias (`vitest.config.ts`). `npm test` runs them plus the
  legacy `*.smoketest.mjs` node assertions. CI runs the lot.
- **ESLint** (`.eslintrc.cjs`) is deliberately narrow: `react-hooks/rules-of-hooks`
  + `react-hooks/exhaustive-deps` (both **error**) and `@typescript-eslint/no-explicit-any`.
  `tsc` enforces the rest of strict typing. The exhaustive-deps rule is why the
  latest-ref pattern (not a growing dep array) is the idiom for "stable callback
  that reads fresh state".

## Where to extend

- Need data in a component? Compose `useAsyncResource` / `usePolledResource`, or
  read an existing provider hook — never hand-roll a fetch triad.
- New shared shape on the wire? Add it to `contract.ts`; let `tsc` find the
  transports that drift.
- New global state? Extend the matching provider; only add a provider for a
  genuinely new slice.
- New hot list item? `React.memo` it with a value comparator and keep its
  callbacks stable.
