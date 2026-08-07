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
Companion to [`ARCHITECTURE.md`](../ARCHITECTURE.md) (system-wide
mental model — see "The React tree — one codebase, two transports"
for the build-mode summary).

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
| `listRepoActivity()` | `GET /repos/activity` (BACI-369) | Cross-repo activity summary the topbar picker ranks its rows by — `{prefix, last_activity_at?, active_jobs}` per repo, one aggregate query server-side. Reshape is snake→camel only (`api/wire/repo.ts`); the ordering itself is client-side in `components/repoPickerOrder.ts`. Deliberately not folded into `listBoards()`, which is loaded once at mount and N+1 in web mode — this one is polled on the shared 10s cadence. |
| `listColumns()` | static | Inlined as a constant. |
| `getLaunchRepo()` | `GET /launch-repo` (BACI-368) | The repo prefix the *server* process was started in — resolved once by the cobra command from its cwd (enrolling the repo if bacio has never seen it) and echoed by the API layer. `RepoProvider` uses it as the highest-priority `fallbackPrefix` source when the URL carries no prefix. `""` when the process wasn't started inside a git repo. In a cross-origin deployment this is the server's cwd, not the browser's; an older server that 404s the route degrades silently to the remembered pick. |
| `getStateGraph()` | `GET /settings/state-graph` (BACI-241) | Canonical state-transition graph as a display hint (not enforcement). `{states: [...], edges: [{from, to, category}]}`; categories are `primary` / `secondary` / `unusual`. The follow-on popup on `KanbanCard` reads it to promote / demote / tuck-away modes whose `allowedStates` overlap with the card's next-states from its current column. Server-side constant, no per-request cost; bundle fetches once on mount and never refreshes. |
| `listCards(prefix)` | `GET /repos/{p}/issues` | `model.Issue` already inlines `tags`, `taken`, `waiting_for_claim` — no extra round-trip needed. |
| `getIssue(prefix, key)` | `GET /repos/{p}/issues/{k}` | Reshape `IssueView` → `IssueDetail`. |
| `listDocs(prefix, type)` | `GET /repos/{p}/documents?type=…` | |
| `setIssueState(p, k, s)` | `PUT /repos/{p}/issues/{k}/state` | |
| `updateIssueDescription(p, k, d)` | `PATCH /repos/{p}/issues/{k}` then re-`getIssue` | |
| `addComment(p, k, a, b)` | `POST /repos/{p}/issues/{k}/comments` then re-`getIssue` | Author falls back to `X-Actor` (default `"web"`, override via `localStorage['bacio.actor']`). |
| `listFeatures(p)` | `GET /repos/{p}/features` | |
| `getFeature(p, slug)` | `GET /repos/{p}/features/{slug}` | |
| `setFeatureAutoClose(p, slug, enabled)` | `PUT /repos/{p}/features/{slug}/auto-close` (BACI-250) | Body `{enabled: bool}`. Flips the per-feature auto-close pin (`state_manual`) decoupled from `setFeatureState` — `enabled=false` keeps long-lived catch-alls active even when every child is terminal. Refetches via `getFeature`. Audits as `feature.auto-close`. |
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
| `listShippedIssues(p, sinceDays, limit)` | `GET /repos/{p}/shipped?since=&limit=`, or `GET /shipped?…` when `p` is `''` / `'all'` (BACI-187, BACI-221, BACI-371) | Topbar Shipped popover's list fetch. `sinceDays=0` is the "Forever" sentinel — the bundle omits `?since=` so the server returns the unbounded list. **BACI-371**: `'all'` is no longer an error — it is the popover's *default* repo scope and selects the root-level cross-repo route (same posture as `/notifications` and `/history`); a concrete prefix is the "this repo" narrowing. Response shape is `{rows, total}` either way, so the popover header can render "showing N of TOTAL" without a second round trip. |
| `countShippedIssues(p, sinceDays)` | `GET /repos/{p}/shipped/count?since=`, or `GET /shipped/count?…` when `p` is `''` / `'all'` (BACI-221, BACI-371) | Lean count-only sibling polled on the standard 10s `POLL_INTERVAL_MS` cadence so the topbar "Shipped · N" pill reflects the active Today / Last Week / Forever scope even when the popover is closed — and, since BACI-371, its all-repos / this-repo scope too. No `?limit=` parameter — count is total under the scope. |
| `listProxyStats(sinceDays)` | `GET /proxy/stats?since=` (BACI-304) | The Monitor screen's per-FQDN reverse-proxy rollup. **Cross-cutting** — `proxy_requests` has no `repo_id`, so this is the rare seam method that takes no repo prefix; the endpoint is the cross-cutting sibling of `/history`, behind the bearer-token auth (outside the `/anthropic/` exemption). `sinceDays=0` is the "All-time" sentinel (omit `?since=`); a positive value maps to the rolling `?since=Nd` lookback. Reshapes the server's snake_case `ProxyFQDNStat` rows into the camelCase shape `MonitorView` consumes. Wails twin is `MonitorService.ProxyStats`. |
| `listJobTranscripts(repo, issue, mode, sinceDays)` | `GET /proxy/jobs?repo=&issue=&mode=&since=` (BACI-322) | The Monitor Transcript page's row-per-dispatch list. Unlike `listProxyStats` this **is** active-repo scoped — `proxy_messages` has no `repo_id`, but each dispatch resolves to a repo prefix, so `repo` (the URL prefix) drives the scope and `issue` / `mode` narrow further. `sinceDays=0` is the "All-time" sentinel. Reshapes the snake_case `JobTranscriptRow` rows into the camelCase shape `TranscriptListPanel` consumes. Wails twin is `MonitorService.ListJobTranscripts`. |
| `addWorkspace(name, prefix?)` | `POST /workspaces` | Creates a **workspace** — a pathless, git-less `repos` row (`kind='workspace'`). A dedicated route rather than a `kind` flag on `POST /repos`: the git path requires a `path`, the workspace path forbids one, and the store rejects the impossible combination. Body is `{name, prefix?}`; an omitted prefix is derived from the name. Wails twin is `BoardService.AddWorkspace`. |
| `createFeature(p, title, slug, description, emoji, branchName)` | `POST /repos/{p}/features` | The New Epic page's create. `slug` is optional — pass `''` and the server derives it with `store.Slugify(title)` (mirrored client-side by `components/features/epicForm.deriveSlug` for the live preview only; the server stays authoritative). The handler decodes with `inputio.DecodeStrict`, so the body carries **exactly** `{title, slug?, description?, emoji?, branch_name?}` and the HTTP twin omits empty optionals rather than sending `""`. 201 answers with a bare `model.Feature`, so the twin re-reads through `getFeature` to hand back a `FeatureDetail`. 409 on a duplicate slug (the message is the raw SQLite `UNIQUE constraint failed` text — the page pre-checks its loaded list and keeps a friendly fallback for the race). Wails twin is `FeatureService.CreateFeature`. |
| `updateFeature(p, slug, fields)` | `PATCH /repos/{p}/features/{slug}` | The Edit Epic page's batched **Details** save: `title` / `description` / `emoji` / `branchName` in ONE round trip, which is the shape the backend has always spoken. `FeatureUpdateFields` is **presence, not value** — an absent key is "no change", a key present as `''` is "clear this field" (an empty `branchName` puts the epic back to shipping against `main`). Both transports build the body key by key; never spread an object carrying `undefined` members. The four per-field setters (`setFeatureEmoji` / `setFeatureBranchName` / `setFeatureDescription`, and the state / auto-close / handoffs / hide PUTs) stay for the detail pane's inline affordances. Wails twin is `FeatureService.UpdateFeature`, whose four `*string` params bind as `string \| null`. |
| `listKanbanColumns(prefix)` | `GET /repos/{p}/kanban/columns` | The Kanban lanes with their ordered `cards: [{key, position}]`. `cards` is **always present** (`[]` for an empty lane). Membership rides on the lane, so there is no per-issue lane field to fetch. |
| `createKanbanColumn(prefix, name)` | `POST /repos/{p}/kanban/columns` | 201 with the new lane, appended to the right. 409 on a duplicate name in the repo. |
| `renameKanbanColumn(prefix, uuid, name)` / `reorderKanbanColumn(prefix, uuid, position)` | `PATCH /repos/{p}/kanban/columns/{uuid}` | One route, a **presence map** body: the key that is present selects the operation (`{name}` renames, `{position}` reorders). Both-or-neither is a 400. `reorderKanbanColumn` re-reads the board afterwards — see §2a of [`frontend-architecture.md`](frontend-architecture.md). |
| `previewDeleteKanbanColumn` / `deleteKanbanColumn(prefix, uuid)` | `DELETE /repos/{p}/kanban/columns/{uuid}` (`?dry_run=true` for the preview) | The dry run answers 200 with `KanbanColumnDeletePreview` and `X-Dry-Run: applied`; the real delete answers 204. Deleting a lane takes its cards **off the board**; the issues survive. |
| `moveIssueToKanbanColumn(prefix, key, columnUuid, position)` | `PUT /repos/{p}/issues/{key}/kanban` | The drag-drop write. `column_uuid: ""` takes the card off the board — the only way to un-opt a git-repo card — so the key is always sent, never omitted. `position` **absent** means append; `0` means top of the lane. The PUT answers with the moved issue, so the seam re-reads the board. |
| `listDocFolders(prefix)` | `GET /repos/{p}/doc-folders` | Every folder of the page tree, flat; the client assembles the hierarchy. Always an array, never `null`. The wire carries a numeric `parent_id`; the reshaper resolves it to `parentUuid`. |
| `createDocFolder(prefix, name, parentUuid)` | `POST /repos/{p}/doc-folders` | Body `{name, parent_uuid?}`; `parent_uuid: ""` (or absent) is the tree root. 409 on a sibling-name collision or a depth-cap breach. |
| `renameDocFolder(prefix, uuid, name)` / `moveDocFolder(prefix, uuid, newParentUuid)` | `PATCH /repos/{p}/doc-folders/{uuid}` | Same presence-map shape as the lane PATCH: `{name}` renames, `{parent_uuid}` re-parents (present-but-empty ⇒ promote to root). 409 on a cycle — a folder can't move inside its own descendant. |
| `previewDeleteDocFolder` / `deleteDocFolder(prefix, uuid)` | `DELETE /repos/{p}/doc-folders/{uuid}` (`?dry_run=true` for the preview) | Preview → 200 `DocFolderDeletePreview` (descendant folder count + pages re-rooted); real delete → 204. A folder delete **never destroys a page** — `documents.folder_id` is `ON DELETE SET NULL`, so its pages fall back to the root. |
| `moveDocToFolder(prefix, filename, folderUuid, position)` | `PUT /repos/{p}/documents/{filename}/folder` | `folder_uuid: ""` moves the page to the tree root. Documents stay addressed by **filename** — folders are organisational metadata only, `UNIQUE(repo_id, filename)` is untouched, and no URL, CLI verb or sync path changes because a page moved. |
| `createDoc` / `renameDoc` / `deleteDoc` | `POST /repos/{p}/documents`, `POST /repos/{p}/documents/{filename}/rename`, `DELETE /repos/{p}/documents/{filename}` | The Documents page can create, rename and delete pages on both transports. `createDoc` requires a non-empty body — the route mirrors `bacio doc add` and 400s on an empty one. |

---

## 5. Building and running

### 5.1 Local development

Two ways to iterate on the web bundle: the **Vite dev server with HMR**
(fast inner loop, recommended for any non-trivial UI change) and the
**same-origin embedded bundle** (closest to the shipped deployment,
useful as a final pre-PR smoke test).

#### 5.1(a) Vite dev server with HMR (fastest iteration)

Vite serves the bundle on `http://localhost:5174`, watches the React
source, and pushes hot updates to the open browser. A separately-running
`bacio api` answers data calls. Edits to `.tsx` / `.ts` / `.jsx` /
`.css` reflect in under a second — no `./build.sh`, no
`go build -o ~/.local/bin/bacio`, no restart.

Use `bacio api` (not `bacio web`) — the Vite dev server hosts the
bundle on `:5174`, so a second `/ui/` mount on `:5320` would be
confusing and `bacio web`'s browser-open behaviour would race the Vite
dev server.

```bash
# Terminal 1 — JSON API on :5320 with a CORS allow-list for the Vite origin
bacio api --cors-origin http://localhost:5174

# Terminal 2 — Vite dev server on :5174, pointed at the API
cd desktop/frontend
VITE_BACIO_API=http://127.0.0.1:5320 npm run dev:web
# open http://localhost:5174
```

How the two env vars stitch together:

- `--cors-origin http://localhost:5174` opens the API's CORS allow-list
  so the browser preflight from the Vite origin doesn't get blocked.
  The flag is **required** — without it every fetch from the Vite-served
  page gets a `CORS` error in the browser console.
- `VITE_BACIO_API=http://127.0.0.1:5320` is read by `src/api.http.ts`
  (the `API_BASE` constant) and prepended to every API path, so the
  Vite bundle reaches the cross-origin API host instead of the (empty)
  same-origin default.

The Vite dev server is wired in `desktop/frontend/vite.config.ts` and
pinned to `127.0.0.1:5174` (`strictPort: true`). The `--mode web` flag
also swaps `./api` → `src/api.http.ts` (fetch-based, same exported
surface as the Wails-mode `api.ts`) and stubs `@wailsio/runtime` to a
no-op, so the React tree compiles cleanly in a plain browser.

#### 5.1(b) Same-origin against a real `bacio web` (closest to deployment)

The single-binary path. Build the bundle, embed it into the Go binary,
serve it back from `/ui/`.

```bash
./build.sh --skip-desktop                 # builds bundle + embeds + installs (web bundle is default-on)
bacio web                                 # serves /ui/ + /repos/... + opens browser
```

Edits to React/TS require a rebuild + reinstall — no HMR, because the
bundle is baked into the Go binary at compile time. Reach for this when
you want to confirm the embedded surface still works end-to-end
(BACI-72 SPA fallback, browser launcher, the `/ui/` base path) before
opening a PR.

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
3. **Cross-origin dev/test rigs.** As shown in §5.1(a).

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

## 7a. Routing — BrowserRouter on both surfaces (BACI-203, BACI-285)

Every top-level screen and per-entity detail has a real URL. The same
`react-router` v7 `<BrowserRouter>` drives both `bacio web` and the
Wails desktop app — refresh / back-forward / deep-link / shareable
URLs all work without surface-specific code. Retired the BACI-54
hash route — `<BrowserRouter>` is its strict superset.

BACI-285 scoped every page route to the active repo's four-letter
prefix as its first segment (`/<PREFIX>/<page>`), so a link is
self-contained: opening `/ui/BACI/pipeline` selects the BACI repo and
the Pipeline page in one go. The **prefix segment is the source of
truth for the active repo** — `App` derives the active repo from
`location.pathname` rather than from `localStorage`.

- **The route map** (every page nested under the `/:prefix` segment):
  - `/:prefix/pipeline` → the **Agentic Pipeline** view (the agent-driving
    surface, keyed on issue `state`). The nav *label* reads "Agentic
    Pipeline"; the view id stays `'pipeline'`, which is load-bearing in
    `routes.ts`, `App.tsx`, `RepoProvider` and the `is-pipeline` class.
    The entry is gated on the space's **Agent Mode** setting
    (`repo_settings.show_agent_surfaces`), which defaults on for a git repo
    and off for a manual workspace — no working tree means nowhere for a
    dispatched worker to run. See "Per-space nav surfaces" below.
  - `/:prefix/issues` → the **Kanban** board (`KanbanBoard`), the human-lane
    axis keyed on `issues.kanban_column_id`. The nav view id is `board` and
    `viewPath('board')` has always mapped to `/issues` (matching the tab
    label); the route is what was vestigial. Orthogonal to the Pipeline: a
    card renders here **iff** it belongs to a lane, which is what stops the
    same card appearing on both pages by accident. Gated on the space's
    **Show Kanban Board** setting (`repo_settings.show_kanban`), which
    defaults *on* for a manual workspace and *off* for a git repo.
  - `/:prefix/issues/:key` → `IssueWorkspace` for that key. react-router
    ranks it above the Kanban list route above it.
  - `/:prefix/epics`, `/:prefix/epics/:slug` → `FeaturesView` with the
    slug pre-selected when present. The nav *label* and the URL read
    "Epics"; the view id stays `'features'`, as do the CLI verbs, the API
    routes, the JSON fields, the sync folder layout and the `mk-features-*`
    classes. `/:prefix/features` and `/:prefix/features/:slug` are still
    mounted, on a `LegacyFeaturesRedirect` that rewrites the page segment
    in place (preserving slug, query and hash) so pre-rename links keep
    working.
  - `/:prefix/epics/new` → `NewEpicPage`, and `/:prefix/epics/:slug/edit`
    → `EditEpicPage` — two full-screen sub-routes that *replace* the
    two-pane Epics layout, the same relationship `/:prefix/pipeline/:key/process`
    has to the Pipeline. `new` is a **static** segment at the same depth
    as `:slug`, and react-router ranks static above dynamic, so it wins
    wherever it is declared (it sits immediately above `:slug` for the
    reader, not for the router). The hazard runs the other way:
    `store.Slugify("New")` is `"new"`, so an epic slugged `new` would be
    shadowed by the create page forever. That is reserved **client-side
    only** — `RESERVED_SLUGS` in `components/features/epicForm.ts` plus a
    route guard on `NewEpicPage` — because reserving it in
    `store.ValidateSlug` would also constrain `bacio feature add` and
    could reject an already-existing row on its next edit. **No
    store-boundary change, so no sync-manifest impact.**
  - `/:prefix/documents`, `/:prefix/documents/:slug` → `DocsView` with
    the filename pre-selected. The `:slug` segment carries a
    `documentPath`-encoded filename, so dots / unusual characters
    survive the round-trip.
  - `/:prefix/agents` → `AgentsView`.
  - `/:prefix/history` → `HistoryView`.
  - `/:prefix/monitor` → `MonitorView` (BACI-304). The route nests under
    `/:prefix` for nav uniformity, but the per-FQDN proxy stats on the
    Network sub-tab are global (the `proxy_requests` table is
    cross-cutting, no `repo_id`), so that table ignores the active repo
    for its data.
  - `/:prefix/monitor/transcripts` → `MonitorView` (BACI-322). The same
    Monitor shell — it derives the active sub-tab (Network | Transcripts)
    from the URL, so this path renders the Transcript browser. The
    Transcript list **is** active-repo scoped (each dispatch resolves to a
    repo prefix), unlike the global Network table.
  - `/:prefix/monitor/transcript/:id` → `TranscriptRoute` (BACI-322). The
    deep-linkable full-transcript page for one dispatch (keyed on
    `dispatch_id`). react-router v7 ranks this more-specific path above
    `/:prefix/monitor` and the `/:prefix/*` catch-all, and the SPA
    fallback serves `index.html` for it on both transports, so a cold
    deep-link refresh resolves.
  - **Active-repo resolution.** App matches the URL's first segment to a
    known board *case-insensitively* (a lowercased shared link still
    resolves) but always emits the canonical uppercase prefix in
    generated URLs. The matched board becomes the active repo; the
    decision is deferred while the boards list is still loading so a
    valid prefix doesn't 404 itself on a cold load.
  - **Unknown prefix → hard 404.** A non-empty first segment that
    matches no board (and isn't a recognised legacy page word) renders
    the `RepoNotFound` screen — a "Repository &lt;prefix&gt; not found"
    empty state with the board list to jump back into the app.
  - **Prefix-less / bare paths → soft redirect.** Bare `/` and stale
    prefix-less legacy links (`/pipeline`, `/issues/BACI-1`, …)
    redirect to the fallback repo (the validated `localStorage` pick if
    it still exists, else the first board), preserving the page path so
    the recipient lands on the same screen. An unknown page *under a
    valid prefix* falls back to that repo's Pipeline.
- **Repo switch re-routes.** Picking a repo from the topbar `RepoPicker`
  (or the `RepoNotFound` board list) swaps the prefix segment and keeps
  the current page — `/BACI/epics` → pick MINI → `/MINI/epics` — unless
  the target space doesn't expose that tab, in which case it lands on
  that space's home view. On
  a detail route the trailing entity segment is dropped because the path
  builder emits the list/page root only (`/BACI/issues/BACI-100` → pick
  MINI → `/MINI/pipeline`). `localStorage['bacio-active-repo']` is still
  written (so a fresh prefix-less `/ui/` knows where to redirect) but is
  no longer the runtime source of truth.
- **Basename derivation.** `main.tsx` reads
  `import.meta.env.BASE_URL.replace(/\/$/, '')` into `<BrowserRouter
  basename={...}>`. This evaluates to `/ui` in web mode (matches the
  `bacio web` mount under `/ui/`) and the empty string in desktop
  mode (Wails serves the bundle at `/`). One source tree drives both
  targets — Vite's `base` and the router's `basename` stay coupled. The
  repo prefix sits *inside* the basename, so `/ui/BACI/pipeline` (web)
  and `/BACI/pipeline` (desktop) share one `<Routes>` block.
- **SPA-fallback contract.** The router needs an asset server that
  returns `index.html` for every unknown non-asset path under the
  root. `internal/api/static.go::handleUI` does this for `bacio web`
  (extension-less paths fall through to `index.html`; asset-shaped
  paths still 404 cleanly). The Wails desktop uses
  `application.AssetFileServerFS` in `desktop/main.go`, which
  implements the same SPA fallback. If a future Wails v3 release
  breaks that, the fallback is `<HashRouter>` everywhere with the
  same path shapes (`#/BACI/issues/BACI-100`) — a one-line change in
  `main.tsx`, no other code touched. The fallback is *prefix-agnostic*
  on the Go side: both asset servers serve `index.html` for any
  non-asset path under the root, so `/ui/BACI/pipeline` already serves
  the bundle and the client decides (after boards load) whether the
  prefix is real — there is no server-side per-prefix 404.
- **Prefixed URLs.** A deep link now carries its repo in the path
  (`/ui/BACI/issues/BACI-100`), so opening it selects the BACI repo and
  loads the issue without a manual pick. An *unknown* prefix renders the
  `RepoNotFound` screen rather than a blank workspace; a prefix-less
  legacy link soft-redirects to the active repo (see the route map
  above). This supersedes the BACI-203 "keys-only URLs" trade-off where
  the active repo lived only in `localStorage` and a foreign key landed
  on the workspace skeleton until the recipient picked the matching repo.
- **Path helpers.** `desktop/frontend/src/lib/routes.ts` is the single
  source of truth for path shapes — `viewPath`, `issuePath`,
  `featurePath`, `documentPath` (each takes the active repo `prefix` as
  its first argument under BACI-285), plus `prefixFromPath` (read the
  active prefix off a pathname) and `viewFromPath` (skip the prefix
  segment, classify the page). Every callsite that needs a path imports
  from here rather than template-stringing inline. The smoke test in
  `lib/__tests__/routes.smoketest.mjs` (plain Node + assert, the
  existing pattern) covers the helpers.
- **localStorage keeps its job.** Per-view UI state (board horizontal
  scroll, column collapse / compact, pinned-keys, theme, active
  repo) stays in `localStorage`. These are per-user preferences, not
  addressable state, and don't belong on a shareable URL. The URL
  carries only the path.
- **Topbar derives the active view from `useLocation`.** The segmented
  `Kanban / Epics / Documents / History │ Agentic Pipeline / Agents /
  Monitor` buttons highlight the matching segment via `viewFromPath`, which
  skips the leading prefix segment before classifying (`/BACI/issues/...` →
  `board`, `/BACI/epics/...` → `features`, ...).
  `navFor(surfaces)` (`lib/nav.ts`) is what the Topbar actually renders. It
  is exported and consumed by `App` too, because the digit hotkeys map onto
  the **filtered** list by position; filtering in only one place would
  silently desync the keyboard from the buttons. A hairline
  (`.mk-segmented-sep`) is drawn between consecutive entries whose group
  differs, so it appears only when both groups are non-empty.
  The breadcrumb pill that surfaces while the workspace route is mounted
  reads the key off the path directly (`/<prefix>/issues/:key`), then
  calls `navigate(-1)` on click — the browser back stack handles the
  prior view.
- **Linked-doc panels are links.** `LinkedDocPanel` no longer renders
  markdown / SVG / transcript bodies inline; it surfaces metadata + a
  `<Link to={documentPath(prefix, filename)}>` to the canonical
  document page. The brief assemblers (`internal/api/handlers_brief.go::briefDocContent`
  and `internal/client/local_issue.go::briefDocContent`) strip every
  linked-doc body before serving the brief, narrowing the BACI-115
  plan/review carve-out to "never". This saves a chunk of bytes on
  every 10s brief poll. The BACI-141 transcript-anchored eval
  composer migrates onto the document detail page as a follow-up;
  eval-flagged comments continue to surface in the issue workspace's
  main Activity timeline today.
- **No more BACI-54 hash route.** No backend code emits the old
  `#/<prefix>/<key>` shape, so there's nothing to redirect. External
  notes pointing at old hash URLs are a one-shot fix-up.

The workspace's other surfaces (description editor, comment composer,
PR attach form, dispatch button) all run through the same `api.*`
calls in both modes; the seam aliases swap `api.ts` for `api.http.ts`
and the workspace doesn't know the difference.

---

## 7b. Per-space nav surfaces

Which top-nav tabs a space exposes is a **per-space setting**, not a
function of `repos.kind`. Two boolean gates, both on `repo_settings`:

| Setting | UI label | Gates | Default (git) | Default (workspace) |
|---|---|---|---|---|
| `show_agent_surfaces` | Agent Mode | Agentic Pipeline, Agents, Monitor | on | off |
| `show_kanban` | Show Kanban Board | Kanban | off | on |

Epics, Documents and History are never gated, so the nav is never empty.

**The columns are nullable and the defaults are resolved in Go**, in
`model.ResolveRepoSurfaces` — the single reader every layer goes
through. Two reasons a `DEFAULT` can't do this job: `repo_settings` rows
are created lazily by whichever setting is written first (so `NULL` is
the common state, and `GetRepoSettings` maps `ErrNoRows` to a zero
struct), and the intended default depends on `repos.kind`, which
`repo_settings` cannot see. Never read `show_agent_surfaces` /
`show_kanban` raw — `RepoSettings` exposes them as
`ShowAgentSurfacesSet` / `ShowKanbanSet` (`*bool`) precisely so a caller
can't mistake the raw nullable for the effective value.

**The resolved pair rides every repo payload** — `api.RepoOut` on the
HTTP side, `Board` on the Wails side — rather than having its own GET.
`RepoProvider.pickBoard` computes the *target* space's home view
synchronously inside a click handler, so a separate fetch would put an
await mid-click. Writes go through `PUT /repos/{prefix}/show-agent-surfaces`
and `PUT /repos/{prefix}/show-kanban`, which is why those are PUT-only.

**No CLI verb, no `bacio schema` entry.** Nothing in Go branches on
these — they gate React buttons and one route redirect — which is the
`pipeline.backlog_collapsed` case, not the `auto_ship` case. See
`docs/agent-cli-principles.md`.

**`repo_settings` is unsynced**, so these are per-machine. Adding them
to `repo.yaml` instead would have tripped the A0 rule
(`internal/sync/paths.go`).

**Frontend consumption** lives in `lib/nav.ts`: `navFor(surfaces)`
filters `NAV`, and `homeView(surfaces)` picks the landing view from an
explicit precedence list (`pipeline` → `board` → `features`) rather than
"first surviving entry" — the visual order puts the work group first,
but a git repo must still land on the Pipeline, and decoupling the two
means the groups can be reordered without moving everyone's home.

**Hiding a tab also guards its route.** `App`'s `<SurfaceGate>` wraps
each gated page and redirects a deep-link to a switched-off surface back
to the space's home. It is applied **per route**, not as one effect
above `<Routes>`: the active view is derived from the path, and
`viewFromPath('/BACI/issues/BACI-1')` is `'board'`, so a path-agnostic
guard would evict the *issue workspace* whenever Show Kanban was off.
`/:prefix/issues/:key` is deliberately ungated — it is reached from the
Pipeline, Epics, the command palette and notifications.

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
  - `desktop/frontend/src/components/RepoPicker.tsx` — a two-option add
    menu. **Add Git Repository…** forks on `WEB_MODE`: the native folder
    picker in desktop mode, a path-input modal POSTing to `/repos` in web
    mode. **New Workspace…** does *not* fork — a workspace is pathless, so
    there is nothing native to invoke and the same
    `components/workspace/WorkspaceCreateModal` collects `{name, prefix?}`
    on both transports and calls `api.addWorkspace`. The picker list is
    grouped by kind with a badge.
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
