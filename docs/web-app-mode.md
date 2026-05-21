# Web app mode — running bacio's React frontend in a browser

The bacio desktop app's React frontend can also be built as a
**browser-deployed bundle** served by `bacio web` at `/ui/`. Same
React tree, different transport: the desktop build talks to Wails-
bound Go services; the web build talks to the existing HTTP API.

After BACI-72 the two HTTP commands have distinct intents:

- **`bacio web`** — HTTP API **plus** the embedded React bundle at
  `/ui/`, **plus** opens the OS default browser to the bundle. The
  one-liner humans want for the kanban board.
- **`bacio api`** — HTTP API only. No `/ui/` mount; `GET /ui/` returns
  404. The shape agents, scripts, `BACIO_REMOTE` rigs, and CI want
  when the React bundle is just dead weight.

This page is the durable reference for **what works**, **what
doesn't**, **how to build it**, and **what the v2 follow-ups are**.
Companion to [`docs/rest-api-design.md`](rest-api-design.md) (HTTP
API design) and `CLAUDE.md` ## Architecture in one screen → Desktop
app (build-mode summary).

---

## TL;DR

```bash
# One-shot from a clean checkout: build the web bundle, embed it into
# the bacio binary, install to ~/.local/bin/bacio. The web bundle now
# builds by default; --skip-desktop just avoids the Wails desktop step.
./build.sh --skip-desktop

# Run the API server, mount the bundle, and pop the browser. One line.
bacio web

# Or, for SSH / headless / agent-driven Playwright rigs: same server,
# no browser launch.
bacio web --no-open &
```

The bundle covers every surface the desktop app does after BACI-50: the
Board, Features, Documents, Agents (composite REST endpoint server-side),
History, per-issue drawer + edit modal, per-card Dispatch button,
hide-empty-columns toggle, built-in + typed-CRUD prompt-template editor,
bacio-version readout, leader-election chip, and Add Repository
(path-input modal). See
[§3 What v1 deliberately doesn't ship](#3-what-v1-deliberately-doesnt-ship)
for the small remaining surface gap.

---

## 1. Why this exists

- **Zero-install demos and remote-first workflows.** Point a browser
  at a teammate's `bacio api` and get the kanban UI without
  installing the desktop app. The single binary already ships the
  bundle.
- **A shared rig for end-to-end tests.** The Phase 2 real-backend
  variant in BACI-28's Playwright plan needs a Vite-served frontend
  talking to a real `bacio api`. The web build is that rig.
- **An insurance policy against Wails v3 alpha churn.** Wails v3 is
  pinned at `v3.0.0-alpha.90`; the upstream API can move under us at
  any release. The web build is a structurally simpler deployment
  target that doesn't depend on the Wails runtime at all.

---

## 2. The seam

`desktop/frontend/src/api.ts` is the **single import chokepoint** every
React component uses for data. It wraps the Wails-generated bindings
from `desktop/frontend/bindings/` and normalises errors.

When Vite is run with `--mode web`, two aliases in `vite.config.ts` swap
the seam:

- `./api` (and `../api`) → `src/api.http.ts` — fetch-based variant
  against `bacio api` with the same exported surface and TS types.
- `@wailsio/runtime` → `src/wails-stub.ts` — a no-op `Events.On/Emit`
  so the module-level Wails imports in `App.jsx` don't crash a
  browser.

The `WEB_MODE` boolean (in `src/env.ts`, `import.meta.env.MODE === 'web'`)
is read by individual components to hide the surfaces that don't have
an HTTP equivalent.

```
React component  ──import * as api from '../api'──>  api.ts        (Wails build)
                                                     api.http.ts   (web build, via alias)
                                                          │
                                                          ▼
                                                     fetch → bacio api
```

---

## 3. What v1 deliberately doesn't ship

After BACI-50 there is no longer a `WEB_MODE`-only hidden surface —
Agents, typed prompt-template CRUD, Add Repository (path-input modal),
and the leader-election chip all work in both modes. The four entries
that used to live in this table were closed by:

- **Agents tab** — `GET /repos/{prefix}/agents/cards` and the cross-repo
  `GET /agents/cards` now serve the assembled `AgentCard` payload
  (busy / waiting derivation, dispatch bucketing, TodoWrite mirror)
  in a single round trip. The assembly logic moved to
  `internal/agentcards` so both `bacio api` and the desktop's
  `BoardService.ListAgents` share the same source of truth.
- **Settings → typed template CRUD** — `POST /settings/templates`,
  `POST /settings/templates/{slug}/rename`,
  `DELETE /settings/templates/{slug}/row`, and
  `POST /settings/templates/restore-builtins` close the Add / Rename
  / Delete / Restore-built-ins gap. `GET /settings/templates/full`
  returns the rich DTO (label, defaults, is_builtin, …) so the web
  bundle stops deriving labels client-side. The delete endpoint
  lives at `/{slug}/row` to coexist with the BACI-36
  `DELETE /settings/templates/{mode}` body-reset endpoint.
- **Add Repository** — `RepoPicker` opens a path-input modal in web
  mode that POSTs `{path, name, prefix?}` to the existing
  `POST /repos` endpoint, then jumps to the new repo. Helper text
  reminds the user the path is on the server's filesystem, not their
  local machine.
- **"Controlling" leader chip** — `bacio api` now runs the UI leader
  elector itself (alongside the desktop's `LeaderService` and the
  TUI's loop, all racing on the same `ui_leader` row). `GET /leader`
  exposes the cached state; `App.jsx` polls it every
  `POLL_INTERVAL_MS` (10s) in web mode. The chip renders only when
  the server holds the lease.

What `bacio api` deliberately still doesn't do is replace the desktop's
own use of the elector — both UIs (desktop + every running api server)
race for the lease, and only one wins at a time. Closing a desktop window
hands the lease over to a running api server within one tick (~10s).

**SVG document render (BACI-56)** is identical in both modes — the
Render/Source toggle, the `<img>` over a Blob object URL, and the
inert-script safety story all live in the shared React tree
(`DocsView.jsx` + `lib/docFormat.ts`). The web bundle gets the feature
for free because `DocContent.content` already streams the raw doc body
over `GET /repos/{prefix}/documents/{filename}`.

---

## 4. Reshape table

Most surfaced calls in `api.ts` have a direct REST equivalent today
(usually behind a small DTO reshape from `internal/api/views.go`'s
shapes into the desktop's `BoardCard` / `IssueDetail` / `DocSummary` /
`DocContent` / `FeatureSummary` / `FeatureDetail` / `HistoryEntryDTO` /
`PromptTemplateDTO` / `BoardPreferencesDTO`).

| `api.ts` call | REST equivalent | Notes |
|---|---|---|
| `listBoards()` | `GET /repos` + per-repo `GET /repos/{p}/issues` | Issue count comes from a list-then-count; `syncEnabled` and the live sync fields come from `GET /sync` (BACI-89), not a hardcoded `false`. |
| `listColumns()` | static | Inlined as a constant. |
| `listCards(prefix)` | `GET /repos/{p}/issues` | `model.Issue` already inlines `tags`, `taken`, `waiting_for_claim` — no extra round-trip needed. |
| `getIssue(prefix, key)` | `GET /repos/{p}/issues/{k}` | Reshape `IssueView` → `IssueDetail`. |
| `listDocs(prefix, type)` | `GET /repos/{p}/documents?type=…` | |
| `setIssueState(p, k, s)` | `PUT /repos/{p}/issues/{k}/state` | |
| `updateIssueDescription(p, k, d)` | `PATCH /repos/{p}/issues/{k}` then re-`getIssue` | |
| `addComment(p, k, a, b)` | `POST /repos/{p}/issues/{k}/comments` then re-`getIssue` | Author falls back to `X-Actor` (default `"web"`, override via `localStorage['bacio.actor']`). |
| `listFeatures(p)` | `GET /repos/{p}/features` | |
| `getFeature(p, slug)` | `GET /repos/{p}/features/{slug}` | |
| `listHistory(p, page, pageSize)` | `GET /repos/{p}/history?limit=&offset=` | Over-fetches by one so `hasMore` is derivable client-side. |
| `getDoc(p, name)` | `GET /repos/{p}/documents/{name}` | |
| `saveDoc(p, name, content)` | `PUT /repos/{p}/documents/{name}` then re-`getDoc` | |
| `dispatchIssue(p, k, mode)` | `POST /repos/{p}/issues/{k}/dispatch` (BACI-40 + BACI-51) | Server re-checks the state-gate and enqueues a target-less `queued` dispatch; the matcher (running in TUI/desktop/`bacio api`) binds an agent later. Never errors with "no free agent" — that case is now a queued row in the per-(repo, mode) FIFO. |
| `cancelWaitingDispatch(p, k)` | `GET /repos/{p}/issues/{k}/waiting-dispatch` then `POST /agents/dispatches/{id}/cancel` (BACI-51) | The spinner-as-cancel-button handler. A 404 on the GET means the matcher bound the dispatch (or another client cancelled) between the click and the call — treated as a no-op success, not an error. |
| `listPromptTemplates()` | `GET /settings/templates/full` (BACI-50) | Server returns the rich DTO (label, defaults, is_builtin, BACI-51 concurrency fields, …); the bundle reshapes snake_case → camelCase. The older `GET /settings/templates` body-map and `…/states` map still ship for back-compat. |
| `savePromptTemplate(mode, body)` | `PUT /settings/templates/{mode}` (or `DELETE` to reset on empty body) | Refetches the one DTO after the write. |
| `savePromptStates(mode, states)` | `PUT /settings/templates/{mode}/states` (or `DELETE` on empty list) | Same refetch pattern. |
| `savePromptConcurrency(mode, n)` | `PUT /settings/templates/{mode}/concurrency` (BACI-51) | `0` = unlimited, positive integers cap. No DELETE route — set to `0` to revert to "unlimited". The store-side validator enforces `>= 0`. |
| `addPromptTemplate(slug, name, body, states)` | `POST /settings/templates` (BACI-50) | Returns the new DTO. Audits as `template.create`. |
| `renamePromptTemplate(slug, newSlug, newName)` | `POST /settings/templates/{slug}/rename` (BACI-50) | Either field can be empty to leave it unchanged. Audits as `template.rename`. Cascade-updates historical `agent_dispatches.mode`. |
| `deletePromptTemplate(slug)` | `DELETE /settings/templates/{slug}/row` (BACI-50) | Distinct from the body-reset endpoint at `…/{mode}`. Audits as `template.delete`. |
| `restoreBuiltinPromptTemplates()` | `POST /settings/templates/restore-builtins` (BACI-50) | Returns `{restored: [...], templates: [...]}` so the UI replaces its state in one shot. Audits as `template.restore_defaults`. |
| `listAgents(prefix)` | `GET /repos/{p}/agents/cards` or `GET /agents/cards` (BACI-50) | Server-side composite — assembles `AgentCard[]` with claims + dispatches + todos in one round trip. Camel-case JSON tags match the existing TS shape, no reshape needed. |
| `addRepository(payload)` | `POST /repos` (BACI-50, web only) | Web bundle pops a path-input modal and POSTs `{path, name, prefix?}`; desktop binding ignores the payload and pops the native folder picker. |
| `getLeaderStatus()` | `GET /leader` (BACI-50) | `bacio api` runs the UI leader elector and exposes its cached state. App.jsx polls on `POLL_INTERVAL_MS` in web mode. |
| `getBoardPreferences()` | `GET /settings/board-preferences` (BACI-47/D) | Reshapes `{hide_empty_columns}` → `BoardPreferencesDTO`. |
| `setBoardPreferences(hide)` | `PUT /settings/board-preferences` (BACI-47/D) | Audits as `board_pref.update`. |
| `promptPlaceholders()` | static | Returns `['issue_id', 'issue_title', 'repo_prefix']`. |
| `bacioVersion()` | `GET /version` (BACI-47/A) | Returns the same `internal/version.String()` used by `bacio --version` and the per-session `bacio_version` in the Agents panel — cross-checking the readout against a session's version reliably surfaces stale channels. |

---

## 5. Building and running

### 5.1 Local development

Two ways to iterate on the web bundle:

**a) Same-origin against a real `bacio web`.** Closest to the
recommended deployment.

```bash
./build.sh --skip-desktop                 # builds bundle + embeds + installs (web bundle is default-on)
bacio web                                 # serves /ui/ + /repos/... + opens browser
```

Edits to React/TS require a rebuild + reinstall (no Vite HMR — the
bundle is baked into the Go binary).

**b) Cross-origin Vite dev server against a real `bacio api`.** Faster
iteration: Vite HMR for the bundle, pointed at a separately-running
API. `bacio api` (not `bacio web`) is the right backend here — the
dev server hosts the bundle, so a second `/ui/` mount would be
confusing and the browser-open behaviour would race the Vite dev
server.

```bash
# Terminal 1
bacio api --cors-origin http://localhost:5174

# Terminal 2
cd desktop/frontend
VITE_BACIO_API=http://127.0.0.1:5320 npm run dev:web
open http://localhost:5174
```

`VITE_BACIO_API` overrides the same-origin default to point cross-origin
at the API host. The `--cors-origin` flag's allow-list answers preflight
requests for that origin.

### 5.2 The bundle path

```
desktop/frontend/src/*           # source
   ├─ env.ts                     # WEB_MODE
   ├─ api.ts                     # Wails-mode surface
   ├─ api.http.ts                # web-mode surface (aliased in via Vite)
   └─ wails-stub.ts              # @wailsio/runtime stub (aliased in via Vite)

         ↓ npm run build:web (vite --mode web)

desktop/frontend/dist-web/       # raw built bundle

         ↓ build.sh (cp to repo root; web bundle is default-on)

webui/                           # embed.go anchor
   ├─ .gitkeep                   # makes the embed directive valid on a clean checkout
   ├─ index.html
   └─ assets/…

         ↓ go build ./cmd/bacio

bacio binary                     # //go:embed all:webui in embed.go bakes it in
   serves /ui/ at runtime via internal/api/static.go
```

### 5.3 CI

`.github/workflows/ci.yml`'s `desktop-frontend` job runs both `npm run
build` (Wails mode) and `npm run build:web` so a type error or a stub
drift trips a PR. Updating `api.ts` without updating `api.http.ts`
(or vice versa) is the most common failure mode this catches.

---

## 6. Auth and identity

`bacio api` was loopback-only-no-auth by default before BACI-30 and is
still that today. Two changes were strictly necessary for a web bundle:

- **CORS allow-list.** `--cors-origin <url>` (repeatable; defaults
  empty) opens a list of trusted origins. The CORS middleware sits
  *outside* the auth middleware so a cross-origin OPTIONS preflight
  is answered before bearer-token enforcement could 401 it.
- **`X-Actor` from the browser.** The web bundle reads
  `localStorage['bacio.actor']` (default `"web"`) and sends it on
  every request, so audit rows attribute writes to a recognisable
  identity rather than the literal `"api"` default.

### Deployment shapes, ranked

1. **Same-origin loopback, no token.** Recommended for personal use:
   `bacio web` on `127.0.0.1`, browser on the same machine, no auth,
   no CORS. This is the configuration `./build.sh` + `bacio web`
   produces out of the box. (`bacio api` on the same host serves the
   JSON API for the CLI / `BACIO_REMOTE` / scripts but does NOT mount
   `/ui/` — reach for `bacio web` whenever the browser is in the
   loop.)
2. **Same-origin behind a reverse proxy with TLS.** Caddy or
   Tailscale Funnel terminates TLS, forwards to a local `bacio web`
   (or `bacio api` if you're only exposing the JSON API). Single
   shared bearer token via `--token`/`BACIO_API_TOKEN` gates access.
   Trust model: any user with the token has full write authority —
   same as a CLI user on the host. Auth, CORS, and `X-Actor` rules
   apply identically to both commands; the only difference is the
   `/ui/` mount.
3. **Cross-origin dev/test rigs.** As shown in §5.1(b).

### What's NOT in scope for v1

- **Per-user identity.** The single shared bearer token is the v1
  trust model. Real per-user auth (OIDC inside the binary, a `Users`
  table, RBAC) is a much bigger lift; the Phase 2 follow-up is to
  trust a reverse-proxy `X-Authenticated-User` header in deployments
  that have one.
- **Token-in-cookie.** The web bundle stashes its token in
  `localStorage` (XSS-exposed). Mitigations today: same-origin
  deployments, no inline scripts. Cookie-based auth is a separate
  hardening pass.

---

## 7. Phase 2 follow-ups

BACI-50 closed the four big web/desktop parity gaps (Agents tab,
typed template CRUD, Add Repository, leader chip). What's still
deferred:

1. **SSE/WebSocket push.** The desktop's leader chip + Agents view get
   live updates via Wails events; the web bundle polls every 10s
   (existing `POLL_INTERVAL_MS`). A `/events` SSE channel that
   re-broadcasts mutations would close the gap.
2. **Cross-repo Agents view (`?all=true`).** `GET /agents/cards` is
   already wired for the cross-repo case, but today's web
   `RepoPicker` doesn't surface an "all" option — defer the UI work
   until someone uses it.
3. **Path validation on `POST /repos`.** The Add Repository modal
   accepts any string today; server-side `handleReposCreate` would
   give better errors if it rejected non-absolute paths /
   non-existent dirs / non-git working trees with useful messages
   (matching the CLI's `git.Detect` failure modes).
4. **Bundle the `default` field on body PUT.** `PUT /settings/templates/{mode}`
   returns the persisted body only; `listPromptTemplates` (which uses
   `/settings/templates/full`) does carry the embedded `default`, so
   the existing refetch-one-after-write path works. Skipping the
   round-trip would mean having `PUT` return the full DTO too — small
   cleanup, not urgent.
5. **Headless matcher for web-only deployments (BACI-51).** The
   dispatch-queue matcher (`internal/dispatcher.Matcher`) is gated on
   `ui_leader` and runs only inside the TUI's tick loop, the desktop's
   `LeaderService`, or `bacio api`'s leader loop (BACI-50 added the
   third). A `bacio api` instance already participates in the
   election, so today's gap is narrower than it was: the matcher
   itself isn't wired into the api's leader goroutine yet, so a
   pure-web deployment (browser pointed at a `bacio api` with no
   TUI/desktop on the host) won't auto-bind queued dispatches. The
   follow-up is to call `dispatcher.Matcher.Tick()` from
   `internal/api/leaderservice.go` on the same 5 s cadence
   `QueueMatchInterval` uses everywhere else.

---

## 7a. Issue workspace hash route (BACI-54)

The IssueWorkspace (BACI-54) is a routed top-level view that replaces
the legacy IssueDrawer + IssueEditModal. In web mode only, the open
issue is reflected into the URL hash so the workspace is deep-linkable
and copy-pasteable between tabs.

- **Shape:** `#/<prefix>/<key>`, e.g. `#/BACI/BACI-54`. The hash
  encodes both the repo (so the picker lands on the right one) and
  the canonical issue key. Anything that doesn't match the regex
  `^#/[A-Za-z0-9]+/[A-Za-z0-9]+-\d+$` is ignored.
- **Inbound (hashchange listener).** `App.jsx` parses the hash on
  mount and on every `hashchange`. If the parsed value differs from
  current state, the active repo and `openIssueKey` are set together
  and `activeView` flips to `'issue'`. If the hash is cleared while a
  workspace is open (back button, manual edit), the workspace closes
  and `activeView` returns to `previousView`. The guard "only act if
  we'd actually change something" prevents fighting the outbound
  reflect.
- **Outbound (history.replaceState).** When `openIssueKey` /
  `activeBoard` change, the canonical hash is written back. `replaceState`
  (not `pushState`) is deliberate: opening a dozen cards leaves one
  history entry, not twelve, so the browser back button still
  reliably returns to whatever page the user navigated *from* into
  the app.
- **Desktop ignores both effects** — the `WEB_MODE` guard returns
  early at the top of both hooks. There's no Wails-side router
  primitive to integrate; the native app uses the existing
  click-a-card / breadcrumb / esc affordances.
- **No router library.** A single `hashchange` listener and a two-line
  parser is enough — bringing in `react-router` would buy nothing
  this view actually needs.
- **Repo switch.** Picking a different repo while a workspace is
  open follows the same "only act if we'd actually change something"
  guard: the outbound effect rewrites the hash to the new prefix,
  but the workspace stays mounted with the now-foreign issue key
  until the user picks a card from the new repo. (Today this is
  acceptable because the brief fetch will error out and surface
  through the global modal; tightening it — clear `openIssueKey` on
  repo change — is a 1-line follow-up if it ever bites.)

The workspace's other surfaces (description editor, comment composer,
PR attach form, dispatch button) all run through the same `api.*`
calls in both modes; the seam aliases swap `api.ts` for `api.http.ts`
and the workspace doesn't know the difference.

---

## 8. Implementation map

When you go to extend or fix this, the relevant files are:

- **Frontend (web-mode surface)**
  - `desktop/frontend/src/api.http.ts` — fetch wrappers + reshape.
  - `desktop/frontend/src/env.ts` — `WEB_MODE`.
  - `desktop/frontend/src/wails-stub.ts` — `@wailsio/runtime` no-op.
  - `desktop/frontend/vite.config.ts` — `--mode web` aliases + dist-web/.
  - `desktop/frontend/package.json` — `dev:web` / `build:web` /
    `preview:web` scripts.
- **Frontend (gating)**
  - `desktop/frontend/src/App.jsx` — leader subscription seeds once
    on mount; web mode then polls `GET /leader` every
    `POLL_INTERVAL_MS`, desktop mode listens on the `leaderStatus`
    Wails event.
  - `desktop/frontend/src/components/Topbar.jsx` — `NAV` is identical
    in both modes; the leader chip renders whenever the elector
    reports we hold the lease, regardless of build target.
  - `desktop/frontend/src/components/RepoPicker.jsx` — Add Repository
    pops the native folder picker in desktop mode and a path-input
    modal in web mode (the modal POSTs to `/repos`).
  - `desktop/frontend/src/components/SettingsView.jsx` — every
    template affordance (body textarea, state chips, Reset body,
    Reset gate, toolbar Add / Restore built-ins, per-template Rename
    / Delete) works in both modes. The hide-empty-columns toggle is
    visible in both modes too.
- **Backend**
  - `embed.go` — `//go:embed all:webui var WebUIFS`.
  - `internal/api/static.go` — `/ui/` handler with SPA fallback;
    `HasWebUI()` exported so `bacio web` can probe at startup
    (BACI-72).
  - `internal/api/middleware.go` — `cors()` allow-list middleware.
  - `internal/api/router.go` — `/ui` + `/ui/` routes register only
    when `Options.MountUI` is true (BACI-72); cors sits outside auth.
  - `internal/api/server.go` — `Options.CORSOrigins` + `Options.MountUI`
    (BACI-72).
  - `internal/cli/api.go` — `--cors-origin` flag; `MountUI` left at
    its zero-value (API-only by design).
  - `internal/cli/web.go` — `bacio web`: same flag surface as
    `bacio api` plus `--no-open`, sets `MountUI: true`, spawns the
    browser-launcher goroutine after `/healthz` reports up
    (BACI-72).
  - `internal/browseropen/` — pure-Go helper that opens the OS
    default browser via `open` / `xdg-open` / `rundll32`. Test seam
    swaps out the launcher (BACI-72).
- **Build**
  - `build.sh` — npm install + `build:web` + dist-web/ → webui/ (default-on; opt out with `--skip-web`).
  - `.github/workflows/ci.yml` — extra `build:web` step.
- **Tracking**
  - `webui/.gitkeep` — embed-directive anchor on clean checkouts.

---

## 8a. Markdown rendering

The web bundle reuses the React tree the Wails desktop ships, so the markdown story is identical across both surfaces: every read view (issue descriptions, comment timeline, linked-doc panel, feature description) goes through `desktop/frontend/src/lib/markdownView.tsx`'s `<MarkdownView>` wrapper, which is the only call-site for `react-markdown` outside DocsView's TipTap editor. `remark-gfm` is wired so GFM tables, task lists, autolinks and strikethrough render uniformly. See [`docs/markdown-rendering.md`](markdown-rendering.md) for the full per-surface audit, the canonical-renderer decision, and the rule against importing `react-markdown` directly elsewhere. `remark-gfm` adds ~50 KB minified to the embedded bundle — already inside the chunk-size budget Vite warns about.

---

## 9. Out of scope, explicitly

- **Replacing the Wails desktop app.** Wails gives us native window
  chrome, OS tray, native folder picker, and a process to hold the
  leader-election lease. The web build is an *additional* deployment
  target, not a replacement.
- **Mobile layouts.** The Board is desktop-class; mobile is a
  separate consideration.
- **Bundler swap to `wails3 build:server`.** That's the third row of
  BACI-28's plan §2.2 and stays parked — web mode does the job
  without needing a Wails-managed server build.
