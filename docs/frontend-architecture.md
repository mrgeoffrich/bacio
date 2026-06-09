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
- `components/features/*`, `components/settings/*` — the same treatment for the
  Features and Settings screens.

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
