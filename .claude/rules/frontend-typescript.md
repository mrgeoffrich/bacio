---
paths:
  - "desktop/frontend/**/*.ts"
  - "desktop/frontend/**/*.tsx"
---

# Frontend TypeScript conventions (`desktop/frontend/src`)

The React frontend is **strongly typed** (BACI-346). `tsconfig.json` runs `strict` +
`noImplicitAny` + `noUnusedParameters` + `noUnusedLocals`. The build gates on it:
`npm run build` and `npm run build:web` both run `tsc` first. Keep it green.

## Data flow & module layout (read `docs/frontend-architecture.md` first)

Post-BACI-350 the tree has a deliberate grain: `components/` (thin views) →
`state/` (Context providers + co-located mutations) → `api/` (one DTO contract,
two transports) → `lib/hooks/` (cross-cutting primitives). Land changes on it:

- **Don't hand-roll a fetch.** The `useState + useEffect(fetch) + .catch(reportError)`
  triad is replaced by `useAsyncResource` / `usePolledResource` (`lib/hooks/`).
  They own the loading/error state, the stale-load guard, and the
  `silent`-vs-`reportError` decision (pick the option that preserves today's
  behaviour). Optimistic writes go through `useOptimisticToggle` /
  `useOptimisticMutation`, persisted UI state through `useLocalStorage`.
- **Global state lives in `state/` providers** (`RepoProvider`, `CardsProvider`,
  `AgentsProvider`, `PreferencesProvider`), read via their `use*` hooks — not
  prop-drilled from `App`, which is now a thin shell.
- **DTOs live once in `api/contract.ts`** (no runtime imports); both transports
  `satisfies` it so drift is a `tsc` error. Wire types + reshapers live in
  `api/wire/*`; `api.ts` / `api.http.ts` are per-domain barrels.
- **`reportError` headlines** follow `"Couldn't <verb> <object>"`, double-quoted.
- **`React.memo`'d hot list items need stable callbacks** (latest-ref pattern,
  not a growing dep array — `react-hooks/exhaustive-deps` is an error) or the
  memo silently stops helping. See `components/pipeline/memoCard.ts`.

The full narrative — the four layers, the provider nesting, the hook table — is
in [`docs/frontend-architecture.md`](../../docs/frontend-architecture.md).

## Hard rules

- **No `any`.** No `as any`, no `@ts-ignore` / `@ts-expect-error` / `@ts-nocheck`. If a
  type is genuinely unknown, use `unknown` + a narrowing guard, or a precise cast.
- **TS only.** All source is `.ts` / `.tsx`. Never add `.js` / `.jsx`.
- **`tsc --noEmit` must pass** (`cd desktop/frontend && npx tsc --noEmit`) before you call a
  change done.

## Component shape

- Props: a named `type FooProps = { ... }` declared **above** the component, then
  `function Foo({ a, b }: FooProps)`. **Not** `React.FC`. `children` is `React.ReactNode`.
  Callback props get full signatures, e.g. `onPick: (prefix: string) => void`.
- When defining `*Props`, grep the call sites (`grep -rn '<Foo' desktop/frontend/src`) so the
  prop types match what callers actually pass; mark a prop `?` if any caller omits it.
- The one class component is `ErrorBoundary` → `class ErrorBoundary extends
  React.Component<Props, State>`. Everything else is a function component.

## Types come from the `api` seam — reuse, don't redefine

- Import domain types from the `./api` seam (`'../api'`, depth-adjusted) and the generated
  `bindings/` — `Board`, `IssueDetail`, `BoardCard`, `WaitingState`, `DispatchMode`, etc.
  Never hand-redefine a shape that already exists there.
- Use `import type { ... }` for type-only imports.
- Local sibling imports are **extensionless** (`import Icon from './Icon'`), not `'./Icon.tsx'`.

## Cross-transport seam — the one that bites (see `docs/web-app-mode.md`)

Components are type-checked by `tsc` against `api.ts` (Wails), but in **web mode** a Vite
alias swaps `./api` → `api.http.ts` and stubs `@wailsio/runtime` to a no-op. So `tsc` passing
is **not** enough — a wrong runtime import compiles but breaks `npm run build:web`.

- Import runtime values/functions only from the `./api` seam, **never directly from
  `bindings/...`** (binding modules import `Create` from `@wailsio/runtime`, which the web
  stub lacks → web build fails).
- **Never reference a Wails enum *member* at runtime** (`WaitingKind.WaitingQueuedNoAgent`,
  `QuestionState.QuestionOpen`). `api.http.ts` ships those names as *types only*. Use the
  string literal cast to the enum type, with a **type-only** import — same pattern as the
  existing `mode as DispatchMode`:
  - `kind: 'queued_no_agent' as WaitingKind`
  - `row.state === ('open' as QuestionState)`
- After any seam-adjacent change, run **`npm run build:web`** — it's the only thing that
  catches these.

## Annotations

- `useState`: add an explicit generic whenever the initial value is `[]` / `null` / `{}` and
  the real shape differs — `useState<BoardCard[]>([])`, `useState<IssueDetail | null>(null)`,
  `useState<Record<string, boolean>>({})`, `useState<Set<string>>(new Set())`.
- `useRef<HTMLDivElement>(null)`. Event handlers: rely on inference (`onChange={(e) => …}`);
  annotate `React.MouseEvent` / `React.ChangeEvent<HTMLInputElement>` only when inference fails.
- `noUnusedParameters`: prefix an intentionally-unused param with `_` (`_e`, `_i`).
- Null safety: Wails fields are often `field?: T | null`. Guard with optional chaining /
  narrowing that **preserves current runtime behaviour**; use `!` only on a genuine invariant.

## Other surface conventions (don't regress these)

- Every React read surface renders markdown through `<MarkdownView>`, never `react-markdown`
  directly — see `docs/markdown-rendering.md`.
- Card-movement / ship-flourish animations use Motion (pinned v11.18.2) — see
  `docs/motion-layout-animations.md`.

## Verify a frontend change

```
cd desktop/frontend
npx tsc --noEmit          # strict gate — must be clean
npm run build             # desktop bundle (tsc + vite)
npm run build:web         # web seam (catches cross-transport runtime mistakes)
```
Then `<worktree_root>/build.sh` for the full embed + a `bacio web --no-open` smoke if the UI changed.
