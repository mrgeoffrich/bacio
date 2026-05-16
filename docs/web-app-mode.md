# Web app mode — running bacio's React frontend in a browser

The bacio desktop app's React frontend can also be built as a
**browser-deployed bundle** served by `bacio api` at `/ui/`. Same
React tree, different transport: the desktop build talks to Wails-
bound Go services; the web build talks to the existing HTTP API.

This page is the durable reference for **what works**, **what
doesn't**, **how to build it**, and **what the v2 follow-ups are**.
Companion to [`docs/rest-api-design.md`](rest-api-design.md) (HTTP
API design) and `CLAUDE.md` ## Architecture in one screen → Desktop
app (build-mode summary).

---

## TL;DR

```bash
# One-shot from a clean checkout: build the web bundle, embed it into
# the bacio binary, install to ~/.local/bin/bacio.
./build.sh --web --skip-desktop

# Run the API server (loopback default) and open the UI.
bacio api &
open http://127.0.0.1:5320/ui/
```

The bundle is **read-mostly** in v1: the Board, Features, Documents,
History and per-issue drawer + edit modal all work; the Agents tab,
per-card Dispatch button, Settings → prompt templates, native folder
picker, and leader-status chip are deliberately hidden. See
[§3 What v1 deliberately doesn't ship](#3-what-v1-deliberately-doesnt-ship)
for the why-and-how.

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

Five UI surfaces are hidden in `WEB_MODE`. All of them either touch
local-only state (the agent registry, `app_settings`) or the host
process (folder picker, leader election):

| Surface | Why hidden | What the user sees |
|---|---|---|
| **Agents tab** | No HTTP endpoint assembles the `AgentCard` payload (busy/waiting derivation + dispatch bucketing). | Tab dropped from the topbar. |
| **Per-card Dispatch button** | Needs server-side agent auto-pick + state-gate re-check, which `BoardService.DispatchIssue` does locally. | Button never renders (`promptConfig` is empty in web mode). |
| **Settings → prompt templates** | Typed CRUD lives in `app_settings`, which has no HTTP parity yet. The body + state-gate routes (BACI-36) exist but the full edit UX needs the typed verbs too. | Section hidden; only the theme picker + bacio-version readout remain. |
| **Hide-empty-columns toggle** | Persistence is in `app_settings`. | Toggle hidden; web build always shows every column. |
| **Native folder picker (+ Add Repository)** | No browser equivalent. | Picker dropdown shows a "run `bacio init` on the server" hint instead. |
| **"Controlling" leader chip** | Per-process Wails concept — the browser doesn't run an elector. | Chip never renders. |

The hidden state for each surface is *also* enforced in `api.http.ts`:
the stubbed functions throw `WebModeUnavailableError` so a caller that
slips past component gating surfaces a clear error via the modal,
rather than failing silently.

---

## 4. Reshape table

13 of the 23 surfaced calls in `api.ts` have a direct REST equivalent
today (most behind a small DTO reshape from `internal/api/views.go`'s
shapes into the desktop's `BoardCard` / `IssueDetail` / `DocSummary` /
`DocContent` / `FeatureSummary` / `FeatureDetail` / `HistoryEntryDTO`).

| `api.ts` call | REST equivalent | Notes |
|---|---|---|
| `listBoards()` | `GET /repos` + per-repo `GET /repos/{p}/issues` | Issue count comes from a list-then-count; `syncEnabled` is local-only (always false in web mode). |
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
| `promptPlaceholders()` | static | Returns `['issue_id', 'issue_title', 'repo_prefix']`. |
| `bacioVersion()` | (stub) | Returns `"web"`; no HTTP endpoint exposes the binary version today. |

---

## 5. Building and running

### 5.1 Local development

Two ways to iterate on the web bundle:

**a) Same-origin against a real `bacio api`.** Closest to the
recommended deployment.

```bash
./build.sh --web --skip-desktop          # builds bundle + embeds + installs
bacio api                                 # serves /ui/ + /repos/...
open http://127.0.0.1:5320/ui/
```

Edits to React/TS require a rebuild + reinstall (no Vite HMR — the
bundle is baked into the Go binary).

**b) Cross-origin against a real `bacio api`.** Faster iteration: Vite
dev server with HMR, pointed at a separately-running API.

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

         ↓ build.sh --web (cp to repo root)

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
   `bacio api` on `127.0.0.1`, browser on the same machine, no auth,
   no CORS. This is the configuration `./build.sh --web` + `bacio api`
   produces out of the box.
2. **Same-origin behind a reverse proxy with TLS.** Caddy or
   Tailscale Funnel terminates TLS, forwards to a local `bacio api`.
   Single shared bearer token via `--token`/`BACIO_API_TOKEN`
   gates access. Trust model: any user with the token has full
   write authority — same as a CLI user on the host.
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

The triggers below are concrete "a user is now blocked by this" signals,
not aspirational TODOs. Pick them up in this order if web mode starts
seeing real use.

1. **Agent-registry HTTP shapes for AgentCard.** The raw register /
   heartbeat / claim / release / list-sessions / list-open-claims
   verbs already ship (BACI-34); what the desktop's Agents view also
   needs is a *composite* endpoint that returns `AgentCard[]` directly
   (with busy/waiting derivation and dispatch bucketing). Without it
   the web build can't unhide the Agents tab without doing N+1
   round-trips per session.
2. **Server-side dispatch auto-pick.** Today the desktop's
   `BoardService.DispatchIssue` picks the agent locally. Move that
   logic into a new `POST /repos/{p}/issues/{k}/dispatch` (or extend
   the existing `POST /repos/{p}/agents/dispatches`) that takes only
   the issue key + mode and picks the target agent server-side. Then
   the per-card action button can unhide in web mode.
3. **`app_settings` HTTP parity.** Add `/settings/board-preferences`
   read/write + the typed prompt-template CRUD endpoints
   (`add`/`rename`/`delete`/`restore-defaults`). Then the Settings
   → prompt templates section + hide-empty-columns toggle can
   unhide.
4. **"Register repo" path-input modal.** `POST /repos` already exists;
   the web RepoPicker could surface a path-input modal that POSTs to
   it, replacing the "run `bacio init` on the server" copy-only line.
5. **SSE/WebSocket push.** The desktop's leader chip + Agents view get
   live updates via Wails events; the web bundle polls every 10s
   (existing `POLL_INTERVAL_MS`). A `/events` SSE channel that
   re-broadcasts mutations would close the gap.
6. **`GET /version`.** So `bacioVersion()` doesn't return the literal
   `"web"` placeholder.

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
  - `desktop/frontend/src/App.jsx` — leader subscription gated on
    `!WEB_MODE`; `refreshAgents` no-ops in web mode.
  - `desktop/frontend/src/components/Topbar.jsx` — Agents tab
    filtered out of `NAV`; leader chip hidden.
  - `desktop/frontend/src/components/RepoPicker.jsx` — Add Repository
    replaced with a copy-only hint.
  - `desktop/frontend/src/components/SettingsView.jsx` — prompt-
    template section + hide-empty-columns toggle gated.
- **Backend**
  - `embed.go` — `//go:embed all:webui var WebUIFS`.
  - `internal/api/static.go` — `/ui/` handler with SPA fallback.
  - `internal/api/middleware.go` — `cors()` allow-list middleware.
  - `internal/api/router.go` — routes `/ui` + `/ui/`; cors sits
    outside auth.
  - `internal/api/server.go` — `Options.CORSOrigins`.
  - `internal/cli/api.go` — `--cors-origin` flag.
- **Build**
  - `build.sh --web` — npm install + `build:web` + dist-web/ → webui/.
  - `.github/workflows/ci.yml` — extra `build:web` step.
- **Tracking**
  - `webui/.gitkeep` — embed-directive anchor on clean checkouts.

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
