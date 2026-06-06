# Frontend architecture modernization — phased plan

**Scope:** `desktop/frontend/src` — the React tree shared by the Wails desktop app and the web app (the dual-transport seam in `docs/web-app-mode.md`).
**Status:** Proposal. No code changed yet — this doc is the spine for a sequence of small, independently-shippable PRs.
**Audience:** whoever picks up the next frontend refactor ticket. Read `.claude/rules/frontend-typescript.md` and `docs/web-app-mode.md` first — every phase here lives inside those constraints.

---

## 1. Why now

The frontend works and is strongly typed (BACI-346), but it has grown by accretion. Three structural problems compound as features land:

1. **`App.tsx` is a god component.** 1,590 lines, **23 `useState`**, **16 `useEffect`**, **48 `useCallback`**. It owns every piece of global state (boards, cards, agents, brief, six preference pairs, theme, leader, notifications, shipped count), every polling loop, and ~30 mutation handlers that all follow the same optimistic-flip-then-refresh dance. Every new feature adds another `useState` + handler + effect here.
2. **Cross-cutting concerns are hand-rolled and copy-pasted.** Polling (`setInterval(..., POLL_INTERVAL_MS)`), `localStorage` read/persist pairs, the optimistic-update-then-reconcile pattern, and per-component data fetching are reimplemented inline in ~25 places. There is **no data-fetching abstraction, no state library, and no React Context** — `grep createContext` returns nothing.
3. **A handful of files are too big to reason about.** `api.http.ts` (3,289), `PipelineView.tsx` (1,755), `App.tsx` (1,590), `api.ts` (1,485), `FeaturesView.tsx` (1,170), `SystemSettingsSection.tsx` (785). These bundle many responsibilities into one module each.

There is a real **test gap** behind this: 16 `*.smoketest.mjs` files (plain Node + `assert`, no framework) cover pure `lib/` functions, but **zero component or hook tests exist**, and **CI does not run the smoke tests at all** — `ci.yml` only runs `npm run build` and `npm run build:web`. So today there is no safety net for the refactors below.

### Current-state metrics

| Concern | Today |
|---|---|
| `App.tsx` size / hooks | 1,590 LOC · 23 `useState` · 16 `useEffect` · 48 `useCallback` |
| Components importing `api` directly | 25 of ~50 |
| Hand-rolled polling loops | 9 (`App.tsx` ×5, `TranscriptListPanel`, `NetworkPanel`, `SyncSettingsSection`, …) |
| `localStorage` read/persist pairs | 6 (theme, active repo, docs sort, sidebar, shipped scope, web token) |
| `reportError` call sites | 50+ |
| State-management library | none |
| React Context | none |
| Custom hooks | 2 (`useShipSfx`, `useShipFlourish`) — both animation/UX, none for data |
| Test framework | none (16 ad-hoc smoke tests, not run in CI) |
| Largest files | `api.http.ts` 3289 · `PipelineView` 1755 · `App` 1590 · `api.ts` 1485 · `FeaturesView` 1170 |

---

## 2. Guiding principles (constraints every phase must honour)

These are non-negotiable given the codebase, not stylistic preferences:

- **The transport seam is sacred.** Components import runtime values only from `./api`; Vite swaps `./api → api.http.ts` in web mode and stubs `@wailsio/runtime`. `tsc` passing is *not* sufficient — **`npm run build:web` is the only gate that catches a wrong runtime import**. Run it after any seam-adjacent change. (`docs/web-app-mode.md`, `.claude/rules/frontend-typescript.md`.)
- **No `any`, no `@ts-ignore`.** Strict mode is on and the build gates on it. New hooks must be fully generic-typed.
- **No new heavyweight dependency without a deliberate decision.** Adding `@tanstack/react-query`/`zustand`/`jotai` is a judgement call (see Phase 3) — it is called out explicitly, not smuggled in.
- **Behaviour-preserving, incrementally shippable.** Every phase is a sequence of PRs that each leave `main` green and the app working. No big-bang rewrite. The decompositions in Phases 4–5 are pure internal extraction — **no prop-type changes at call sites**.
- **Markdown still goes through `<MarkdownView>`; animations still through Motion (pinned v11.18.2).** Don't regress `docs/markdown-rendering.md` / `docs/motion-layout-animations.md`.

### Definition of done for any frontend PR in this plan

```
cd desktop/frontend
npx tsc --noEmit          # strict gate
npm run build             # desktop bundle (tsc + vite)
npm run build:web         # web seam — catches cross-transport runtime mistakes
node <each touched>.smoketest.mjs   # any smoke tests for touched lib/hook code
```
Then `<worktree_root>/build.sh` + a `bacio web --no-open` smoke (playwright-cli skill) if the UI changed.

---

## 3. Phase overview

Ordered so the safety net comes first, then the reusable primitives, then the big moves that depend on them. Each phase is independently valuable — you can stop after any phase and be better off.

| Phase | Theme | Risk | Depends on |
|---|---|---|---|
| 0 | Test & lint foundation | low | — |
| 1 | Extract cross-cutting hooks | low | 0 (for tests) |
| 2 | Tame the `api` seam (split + de-dup) | low–med | 0 |
| 3 | A real data/state layer (lift App's load out) | med | 1 |
| 4 | Decompose the big view components | med | 1 |
| 5 | Consistency & hardening polish | low | 1–4 |

---

## Phase 0 — Test & lint foundation

**Goal:** establish a safety net *before* moving code, and start running the tests that already exist.

The 16 smoke tests are valuable but invisible — nothing runs them. Before refactoring, make them executable in CI and adopt a real test runner so Phases 1/4 can land with component- and hook-level coverage instead of only manual smoke.

### Work items

1. **Add Vitest** (`vitest` + `@testing-library/react` + `@testing-library/jest-dom` + `jsdom`) as dev deps. Vitest reuses the existing Vite config, so the transport-alias resolution and TS setup come for free — important, because tests must resolve `./api` the same way the build does. Add `"test": "vitest run"` and `"test:watch": "vitest"` scripts.
2. **Migrate the 16 `*.smoketest.mjs` to Vitest** *or* (lower-effort first step) add a `"test:smoke"` script that simply `node`-runs them all, then convert opportunistically. Converting unlocks watch mode, coverage, and a single command.
3. **Wire tests into CI.** Add a `npm run test` step to the `desktop-frontend` job in `.github/workflows/ci.yml` (it already does `npm ci` → `build` → `build:web`). This is the single highest-leverage item in the whole plan: it turns every later phase from "trust me" into "CI proves it."
4. **Add ESLint** with `eslint-plugin-react-hooks` (+ `react-refresh`). The hooks plugin catches the exact class of bug these refactors risk most — stale closures and wrong effect deps. The `.claude/rules/frontend-typescript.md` conventions (no `any`, prop-type shape) can be partially codified as lint rules. Gate it in CI.
5. **Author 3–5 seed component tests** against the most stable surfaces (e.g. `ErrorModal` rendering from the `errors.ts` bus, `Topbar` nav, `CommandPalette` filtering) to prove the harness works end-to-end through the `./api` alias.

### Exit criteria
`npm run test` runs all smoke + new tests green; CI fails on a TS error, a lint hooks-violation, or a failing test.

---

## Phase 1 — Extract cross-cutting hooks

**Goal:** replace the copy-pasted cross-cutting patterns with a small set of well-tested custom hooks in `src/lib/hooks/`. This is where "modern React practices" pays off most, and it directly shrinks `App.tsx` and every polling component.

Each hook is pure-ish and unit-testable (Phase 0 makes that real). None changes behaviour — they encapsulate behaviour that already exists in 5–9 copies.

### Hooks to build

1. **`useInterval(callback, delayMs, enabled)`** — the canonical self-cleaning interval (Dan Abramov's pattern: latest-callback ref so the timer never captures a stale closure). Replaces all **9** hand-rolled `setInterval(..., POLL_INTERVAL_MS)` blocks in `App.tsx`, `TranscriptListPanel`, `NetworkPanel`, `SyncSettingsSection`.

2. **`useLocalStorage<T>(key, initial)`** — the read/persist pair with the existing try/catch-for-hardened-profiles semantics baked in. Replaces the **6** bespoke pairs (`readTheme`/`persistTheme`, `readActiveRepo`/`persistActiveRepo`, docs sort, sidebar collapse, shipped scope, web token). Keep the non-fatal fallback behaviour exactly.

3. **`useAsyncResource<T>(fetcher, deps, { silent, onError })`** — load + `loading` + `error` + manual `refresh()`, with the established convention that `silent: true` logs instead of routing through `reportError`. Replaces the per-component `useState([]) + useEffect(fetch) + .catch(reportError)` triad in the ~25 self-fetching components and the App-level `refreshCards`/`refreshAgents`/`refreshBrief`/`refreshShippedCount`/`refreshNotifCount`.

4. **`usePolledResource<T>(fetcher, deps, { intervalMs, enabled })`** — composes `useAsyncResource` + `useInterval` (eager load on dep change, silent poll on the interval, cleanup on unmount/disable). This is the single most-repeated shape in `App.tsx` (cards, agents, brief, shipped count, notifications all do exactly this).

5. **`useOptimisticMutation`** — the move/dispatch/toggle dance: snapshot → optimistic local update → `await api.*` → reconcile on success / rollback on failure → `reportError`. `App.tsx` has ~10 instances (`moveCard`, `dispatchFromCard`, `setCardEngineMode`, `shipCardFromPipeline`, `onBlockCard`, …) and `PipelineView`/`FeaturesView` have more (`toggleAutoShip`, `toggleCollapsed`, `toggleImpactPrimary`, the four feature-property toggles). Start with the simplest variant (`useOptimisticToggle(value, persist)`) and grow to the snapshot/rollback form.

### Sequencing
Build + test each hook, then migrate call sites one PR at a time (e.g. "PR: route polling through `useInterval`"). Migrations are mechanical and individually reviewable. After this phase `App.tsx`'s effect/handler count drops sharply even before Phase 3 moves logic out of it.

### Exit criteria
Zero raw `setInterval` in components; the 6 `localStorage` pairs gone; new hooks unit-tested; `build:web` green (these hooks must not import anything Wails-only).

---

## Phase 2 — Tame the `api` seam

**Goal:** cut the duplication and drift risk between `api.ts` (1,485) and `api.http.ts` (3,289), and split the monoliths into per-domain modules — without touching the public export surface components consume.

### The problem
~48 domain DTOs (85% of the exported types) are **hand-written in both files** because `api.http.ts` can't import the Wails bindings (they pull in `@wailsio/runtime`). `api.http.ts` additionally carries ~31 snake_case `Api*` wire interfaces and ~16 reshape helpers. A backend field added to a binding flows into `api.ts` automatically but **silently goes stale in `api.http.ts`** — and `tsc` (which checks against `api.ts`) won't catch it; only a runtime web break will.

### Work items (each behaviour-preserving)

1. **Extract the wire layer.** Move the ~31 `Api*` snake_case interfaces and the ~16 reshape functions (`reshapeApiBrief`, `reshapeDispatch`, `mapApiComment`, …) into `src/api/wire/` modules by domain (`wire/issue.ts`, `wire/feature.ts`, `wire/dispatch.ts`, `wire/proxy.ts`, `wire/preference.ts`). Reshapers are pure functions → **add smoke tests** as they move (this is the first time they become testable). Shrinks `api.http.ts` by ~700 lines.

2. **Introduce a shared domain-type contract.** Create `src/api/contract.ts` — TS-only interfaces for the ~48 shared DTOs, in a module with **no runtime imports** so both transports can `import type` it. Have `api.ts` and `api.http.ts` each assert their return types satisfy the contract (`satisfies`/explicit return types). This converts the silent-drift failure into a `tsc` error, closing the gap the Vite alias currently masks. (Full codegen from the Go bindings is the ideal end state but is a larger, separate effort — the contract module gets 80% of the benefit at 10% of the cost.)

3. **Split both files by domain.** `api.ts`/`api.http.ts` become thin barrels re-exporting `api/board.ts`, `api/issue.ts`, `api/feature.ts`, `api/pipeline.ts`, `api/agents.ts`, `api/settings.ts`, `api/sync.ts`, `api/monitor.ts`, `api/notifications.ts`. Components keep importing from `./api` unchanged. Each transport's per-domain file stays small enough to read.

4. **Unify the preference get/set pattern.** The ~14 preference endpoints (`display`/`archive`/`audio`/`timezone`/`sync`/…) are mechanical snake↔camel round-trips. A small generic helper per transport removes the boilerplate and the per-pref drift surface.

### Risk note
This is the phase most likely to break the web build subtly. **Run `npm run build:web` after every PR**, and lean on the new contract module to surface drift at compile time. Do the wire-extraction and the contract module *before* the file split so the split lands on de-duplicated foundations.

### Exit criteria
`api.ts`/`api.http.ts` are barrels; no `Api*` type lives in two files; `contract.ts` is the single source of DTO shape and both transports compile against it; reshapers have smoke tests; `build` and `build:web` green.

---

## Phase 3 — A real data/state layer

**Goal:** stop `App.tsx` from being the owner of all global state. This is the largest single win for "modern React practices" and the one decision that needs an explicit call.

### The decision (needs sign-off before building)
Three viable directions, in increasing weight:

- **(A) Hooks + Context only (no new dep).** Move each global concern into a focused hook from Phase 1, then expose the ones that are genuinely cross-cutting (active repo, boards, cards, agents, preferences) via small React Contexts so deep children (`PipelineView`, `IssueWorkspace`, `Topbar`) read them without prop-drilling. Zero new dependency; fits the existing grain. Downside: caching/dedup/refetch-on-focus stay hand-rolled.
- **(B) Add `@tanstack/react-query`.** Purpose-built for exactly what `App.tsx` hand-rolls: polling (`refetchInterval`), cache, dedup, optimistic mutations with rollback, stale-while-revalidate. Would delete most of Phase 1's `usePolledResource`/`useOptimisticMutation` *and* the App-level refresh orchestration. Cost: a real dependency, a `QueryClient` provider, and it must be verified through the web alias (query itself is transport-agnostic; our `api` functions are the fetchers, so it composes cleanly).
- **(C) A lightweight store (`zustand`).** Good if the pain is "shared mutable state," less so for "server cache + polling," which is most of what we have.

**Recommendation:** **(A) first** (no dep, unblocks decomposition immediately), and re-evaluate **(B)** once Phase 1 hooks reveal whether the hand-rolled cache logic is getting heavy. Don't adopt (B) and hand-rolled hooks both — pick one per concern.

### Work items (assuming A, adjust if B is chosen)
1. **`useBoards()` / active-repo context.** Lift the boards list, the URL-derived `activeBoard`, and the repo-routing helpers (`pickBoard`, `fallbackPrefix`, the BACI-285 prefix logic) out of `App.tsx` into a `RepoProvider` + `useActiveRepo()` hook. `Topbar`, `RepoPicker`, `RepoNotFound`, and every view read it from context.
2. **`useCards(activeBoard)` / `useAgents(activeBoard)`** built on `usePolledResource`, exposing the mutation handlers (`moveCard`, `dispatchFromCard`, the pipeline handlers) co-located with the data they mutate instead of as 48 callbacks on `App`.
3. **`usePreferences()`** for the six preference pairs + theme/timezone, each an `useOptimisticToggle`-style setter.
4. **`useOpenIssue()`** for the brief load/poll + the `descEditing` guard + the workspace write callbacks (`saveTitle`, `saveDescription`, `addComment`, …).
5. **Slim `App.tsx` to a shell:** providers + `<Routes>` + the keyboard-shortcut effect. Target: under ~300 lines, single-digit `useState`.

### Exit criteria
`App.tsx` no longer owns cards/agents/brief/preferences state; views read global data from hooks/context, not 30+ props; prop lists on `PipelineView`/`IssueWorkspace` shrink; behaviour (polling cadence, optimistic flips, error modals) unchanged and covered by tests.

---

## Phase 4 — Decompose the big view components

**Goal:** break the largest view files into focused components + hooks. Pure internal extraction — **no call-site prop changes**. Each file gets its own sub-folder (`components/pipeline/`, `components/features/`, …).

These can run in parallel with each other and largely after Phase 1 (so extracted state can use the shared hooks). Order within each by "extract leaf components first, then hooks, then the shell."

### 4a. `PipelineView.tsx` (1,755 → ~400 shell + modules)
A dozen sub-components are defined inline. Extract to `components/pipeline/`:
- Leaf components → own files: `BlockedByBadge`, `CardHead`, `CardLabels`, `CardTitleBlock`, `PipelineCard`, `JobChain`, `ActiveJob`, `DoneBox`, `AbortedBox`, `QuestionPanel`, and the two big ones — `StageCard` (split into `StageCardBody` + `StageCardFooter`) and `ProcessMenu` (~160-line picker state machine).
- Hooks: `useDragState` / `useDragDropLogic` (column-move vs block-drag, BACI-342), `usePipelinePreferences(activeBoard)` (the 3 settings-fetch effects), `useStageCardState` (the ~15 derived booleans).

### 4b. `FeaturesView.tsx` (1,170 → list + detail + hooks)
- Hooks: `useFeatureSelection` (URL↔selection sync), `useFeatureDetail` (async fetch w/ mock path), `useFeatureFiltering` (memoized counts + visible list), `useFeaturePropertyUpdate` (the repeated set→api→refetch→error toggle pattern, ×8).
- Components: `FeaturePropertyToggle` (reusable row), and move the already-cohesive `FeatureBranchEditor` / `FeatureCommentsSection` to their own files under `components/features/`.

### 4c. `SystemSettingsSection.tsx` (785 → form + template manager)
- **`useTemplateManagement()`** — bundles the 8 template-CRUD `useState`s and 8 mutation fns. This is the densest local-state cluster in the codebase.
- `useTimezoneOptions()`, `useSubmodalBubble()` (the custom-event escape-suppression), `<TemplateRow>`, `<TemplateAddForm>`, and a reusable `<ConfirmModal>` for the rename/delete/restore trio.

### 4d. `AgentsView.tsx` (514)
- Move the 175-line mock dataset to `__mocks__/agents.ts`.
- `useRescueDispatch()` hook; split `AgentCard` into its conditional sections (`AgentQuestionsSection`, `AgentClaimsSection`, `AgentTodosSection`, `AgentDispatchesSection`).

### Reuse note
`useOptimisticToggle` (Phase 1) absorbs PipelineView's 3 toggles **and** FeaturesView's 4 property toggles — build it once, consume in both.

### Exit criteria
No view component over ~500 lines; extracted leaf components and hooks have tests where logic is non-trivial; `tsc`/`build`/`build:web` green; `bacio web --no-open` smoke shows Pipeline/Features/Settings/Agents behaving identically.

---

## Phase 5 — Consistency & hardening polish

**Goal:** close the long-tail gaps the bigger phases expose.

1. **Error-handling consistency.** With `useAsyncResource` owning the `silent`-vs-`reportError` decision, audit the 50+ `reportError` sites for consistent headlines and remove the now-redundant inline `.catch` boilerplate.
2. **Render performance.** With smaller components, apply `React.memo` to the hot leaf cards (`PipelineCard`, `StageCard`) so the 10s poll re-render doesn't cascade; verify with the React DevTools profiler. (`docs/profiling.md` for the TUI side has the mindset.)
3. **Accessibility pass.** Keyboard nav and focus management on the modals/overlays (`ProcessMenu`, `CommandPalette`, `QuestionModal`) and ARIA on the drag-and-drop affordances — the kind of thing that's easy once components are small.
4. **Document the new conventions.** Update `.claude/rules/frontend-typescript.md` with the hooks-first data pattern and the `api/` + `contract.ts` layout, and add a short `docs/frontend-architecture.md` (or extend `ARCHITECTURE.md`'s React section) so the next contributor finds the grain.

---

## 4. Suggested ticket breakdown

Each row is roughly one PR. File them as `bacio` issues under a `frontend-architecture` feature; Phase 0 blocks the rest only loosely (you *can* refactor without it, but you shouldn't).

| # | Phase | Ticket |
|---|---|---|
| 1 | 0 | Add Vitest + RTL; `test` script; convert/adopt smoke tests |
| 2 | 0 | Wire `npm run test` + ESLint (react-hooks) into `ci.yml` |
| 3 | 1 | `useInterval` + migrate the 9 polling loops |
| 4 | 1 | `useLocalStorage` + migrate the 6 persist pairs |
| 5 | 1 | `useAsyncResource` + `usePolledResource` |
| 6 | 1 | `useOptimisticMutation` / `useOptimisticToggle` |
| 7 | 2 | Extract `Api*` wire types + reshapers to `api/wire/*` (+ tests) |
| 8 | 2 | `api/contract.ts` shared DTO module; both transports `satisfies` it |
| 9 | 2 | Split `api.ts` / `api.http.ts` into per-domain barrels |
| 10 | 3 | **Decision PR:** Context-only vs react-query (this doc + spike) |
| 11 | 3 | `RepoProvider` / `useActiveRepo`; lift repo logic out of App |
| 12 | 3 | `useCards`/`useAgents`/`usePreferences`/`useOpenIssue`; slim App to a shell |
| 13 | 4a | Decompose `PipelineView` into `components/pipeline/*` |
| 14 | 4b | Decompose `FeaturesView` into `components/features/*` |
| 15 | 4c | Decompose `SystemSettingsSection` (`useTemplateManagement`, rows) |
| 16 | 4d | Decompose `AgentsView`; mocks to `__mocks__/` |
| 17 | 5 | Error-handling audit + `React.memo` hot cards + a11y pass + docs |

---

## 5. What this plan deliberately does *not* do

- **No big-bang rewrite, no framework swap.** React 18 + react-router 7 + the Wails/HTTP seam stay.
- **No change to the public `./api` export surface** — every component import keeps working through all of Phases 2–4.
- **No CSS/design-system overhaul** — that's a separate axis; this plan is structure and data flow only.
- **No Go-side binding codegen** in scope — the `contract.ts` module is the pragmatic drift-guard; full codegen is noted as a future ideal, not a phase.
- **No adoption of state libraries by default** — Phase 3 makes that an explicit, reversible decision, defaulting to no-new-dependency (Context + hooks) unless the hand-rolled caching proves heavy.
