# Surface parity audit — desktop × REST × CLI (× TUI)

**Date:** 2026-05-16 · **Source issue:** [BACI-32](BACI-32) · **Method:** walked
every desktop service method in `desktop/*.go`, the REST routes in
`internal/api/router.go`, and the CLI verb tree, then cross-checked each
direction.

## TL;DR

- **Desktop is the most feature-complete supervision surface, not the most
  feature-complete admin surface.** The desktop excels at "watch the board,
  drag a card, dispatch work to an agent"; it cannot create or fully edit
  issues, create features, manage tags / PRs / relations, or run sync. The
  REST API and CLI carry that admin weight.
- **The REST API is missing every supervision feature.** Agent registry,
  agent dispatch, prompt templates, board preferences — all return
  `ErrLocalOnly` on the remote backend (per `internal/client/remote_agent.go`).
  This is the parity story that matters for [BACI-30](BACI-30) (web-app
  mode): a browser frontend wired against `bacio api` cannot reproduce the
  desktop today.
- **The CLI has two parity gaps with the desktop**: no state-gated
  auto-pick dispatch (the per-card action button), and no board preferences
  verb (`hide_empty_columns`).
- **The starting gap list from BACI-32 is confirmed**, with one extra:
  list-style reads (`/repos/{prefix}/issues`, `bacio issue list`) don't
  carry the derived `taken` flag the desktop's Board needs, so any
  parity-equivalent web frontend has to derive it client-side via
  `ListOpenClaims` (which itself is local-only). Closing the REST agent
  gap closes this too, but it's worth naming so it doesn't fall through.
- **Eight follow-up gaps recommended to close now** — listed at the bottom,
  one issue per row.

## Conventions

| Mark | Meaning |
| --- | --- |
| ✅ | Surface covers this method end-to-end. |
| ⚠️ | Surface covers it partially (missing fields, missing auto-behaviour). |
| ❌ | Surface does not cover it. |
| — | Not applicable (e.g. event emission, lifecycle hook). |

Status column values: **parity** / **gap-close-now** / **gap-defer** /
**intentional-skip**.

The audit deliberately excludes the four harness-integration shims
(`bacio tui`, `bacio api`, `bacio hook`, `bacio channel`) and the
`install-*` filesystem commands — CLAUDE.md `## Harness-integration shims`
is the durable reference. Sync (`bacio sync`) is also out of scope per the
issue's "Out of scope" list.

## 1. Desktop services → REST × CLI (primary table)

One row per Wails-bound method, in file order.

### BoardService (`desktop/boardservice.go`)

| Desktop method | REST | CLI | Status | Notes |
| --- | --- | --- | --- | --- |
| `ListBoards()` | `GET /repos` ✅ | `bacio repo list` ✅ | parity | Desktop enriches each row with `issueCount` (derived) and `syncEnabled` (read from `.bacio/config.yaml`); REST/CLI return the raw repo. Enrichment gap is cosmetic. |
| `AddRepository()` | `POST /repos` ✅ | `bacio init` ✅ | parity | Same write; desktop wraps it with a native folder picker, CLI auto-detects from `cwd`, REST takes an explicit `{path, name}`. |
| `ListColumns()` | ❌ | ❌ | intentional-skip | Static enum (`model.AllStates()`); no DB read. Any client can derive locally. |
| `ListCards(prefix)` | `GET /repos/{prefix}/issues` ⚠️ | `bacio issue list -o json` ⚠️ | **gap-close-now** | List returns `waiting_for_claim` but not the derived `taken` flag (open-claim presence). Desktop derives it via `client.ListOpenClaims`, which is local-only — so a REST-driven board can't reproduce it without the agent-registry routes also landing. See follow-up #4. |
| `GetIssue(prefix, key)` | `GET /repos/{prefix}/issues/{key}` ✅ | `bacio issue show` / `bacio issue brief` ✅ | parity | All three return `taken` + `claimants` (CLAUDE.md `## Conventions that aren't obvious` confirms the REST shape). |
| `UpdateIssueDescription(...)` | `PATCH /repos/{prefix}/issues/{key}` ✅ | `bacio issue edit --description` ✅ | parity | Same write. |
| `SetIssueState(...)` | `PUT /repos/{prefix}/issues/{key}/state` ✅ | `bacio issue state` ✅ | parity | Same write. |
| `AddComment(...)` | `POST /repos/{prefix}/issues/{key}/comments` ✅ | `bacio comment add` ✅ | parity | Same write. |
| `ListAgents(prefix)` | ❌ `ErrLocalOnly` | `bacio agent list` ✅ | **gap-close-now** | The agent registry has no REST surface in v1 (per `internal/client/remote_agent.go`). Blocks the web-app mode investigation in [BACI-30](BACI-30). See follow-up #1. |
| `DispatchIssue(prefix, key, mode)` | ❌ `ErrLocalOnly` | `bacio agent dispatch --mode <stage> --to <slug> [key]` ⚠️ | **gap-close-now** | Two gaps: (a) REST has no dispatch routes at all; (b) the CLI `dispatch` verb requires explicit `--to` / `--session` and does not re-check the stage's state-gate. The desktop's per-card button auto-picks a free agent and gates on `prompt_states.<mode>` — neither REST nor CLI mirrors that. See follow-ups #2 and #6. |

### DocService (`desktop/docservice.go`)

| Desktop method | REST | CLI | Status | Notes |
| --- | --- | --- | --- | --- |
| `ListDocs(prefix, type)` | `GET /repos/{prefix}/documents` ✅ | `bacio doc list --type <t>` ✅ | parity | All three return metadata-only by default. |
| `GetDoc(prefix, filename)` | `GET /repos/{prefix}/documents/{filename}` ✅ | `bacio doc show` ✅ | parity | All three include the body unless explicitly opted out. |
| `SaveDoc(prefix, filename, content)` | `PATCH /repos/{prefix}/documents/{filename}` ✅ | `bacio doc edit --content` ✅ | parity | Same write; desktop echoes the just-saved content back into the DTO so the editor pane survives the post-save round trip. |

### FeatureService (`desktop/featureservice.go`)

| Desktop method | REST | CLI | Status | Notes |
| --- | --- | --- | --- | --- |
| `ListFeatures(prefix)` | `GET /repos/{prefix}/features` ✅ | `bacio feature list` ✅ | parity | |
| `GetFeature(prefix, slug)` | `GET /repos/{prefix}/features/{slug}` ✅ | `bacio feature show` ✅ | parity | |

### HistoryService (`desktop/historyservice.go`)

| Desktop method | REST | CLI | Status | Notes |
| --- | --- | --- | --- | --- |
| `ListHistory(prefix, page, pageSize)` | `GET /repos/{prefix}/history` ✅ | `bacio history` ✅ | parity | Desktop paginates via over-fetch-by-one + `HasMore`; REST/CLI use `?limit`/`?offset` + `--limit`/`--offset`. Same store-level filter. |

### SettingsService (`desktop/settingsservice.go`)

| Desktop method | REST | CLI | Status | Notes |
| --- | --- | --- | --- | --- |
| `PromptPlaceholders()` | ❌ | ❌ | intentional-skip | Static enum (`model.PromptTemplateTokens`); the CLI surfaces the same list in `bacio settings template set --help`'s long text. |
| `BacioVersion()` | ❌ | `bacio --version` ✅ | gap-defer | Useful on REST too (so a web frontend can spot binary-vs-channel drift, like the desktop Agents panel does), but trivially addable as a field on `GET /healthz` — defer until [BACI-30](BACI-30) needs it. |
| `ListPromptTemplates()` | ❌ `ErrLocalOnly` | `bacio settings template list` ✅ | **gap-close-now** | Same `ErrLocalOnly` story as the agent registry. See follow-up #3. |
| `SavePromptTemplate(mode, body)` | ❌ `ErrLocalOnly` | `bacio settings template set` ✅ | **gap-close-now** | Same. |
| `SavePromptStates(mode, states)` | ❌ `ErrLocalOnly` | `bacio settings template states set` ✅ | **gap-close-now** | Same. |
| `GetBoardPreferences()` | ❌ `ErrLocalOnly` | ❌ | **gap-close-now** | `board.hide_empty_columns` is desktop-only today. CLI gap is a separate close-now (`bacio settings board prefs`); REST gap rides with the prompt-templates one. See follow-ups #5 and #7. |
| `SetBoardPreferences(hideEmptyColumns)` | ❌ `ErrLocalOnly` | ❌ | **gap-close-now** | Same. |

### LeaderService (`desktop/leaderservice.go`)

| Desktop method | REST | CLI | Status | Notes |
| --- | --- | --- | --- | --- |
| `GetLeaderStatus()` | ❌ | ❌ | intentional-skip | UI-coordination state, not user-facing data. CLAUDE.md (`## Conventions that aren't obvious` → UI leader election) is the durable rationale. Re-evaluate only if a future supervisor needs to see who's leading from outside the UI. |
| `ServiceStartup` / `ServiceShutdown` | — | — | n/a | Wails lifecycle, not a binding. |
| `leaderStatus` event (every ~10 s) | — | — | n/a | Server-sent event; if a web frontend needs the equivalent, that's an SSE/WS design call, not a parity gap. |

### Desktop event emissions (`desktop/main.go`)

| Event | REST | CLI | Status | Notes |
| --- | --- | --- | --- | --- |
| `time` (every 1 s) | — | — | n/a | Demo plumbing leftover from the Wails template; not user-facing. Worth pruning during BACI-30 prep but not a parity row. |
| `leaderStatus` | — | — | n/a | See above. |

## 2. REST routes → desktop × CLI

The REST table that has no desktop binding. This is the "REST and CLI are
richer than the desktop" half — listed so the audit is symmetric, not
because the desktop should grow each one.

| REST | Desktop | CLI | Status | Notes |
| --- | --- | --- | --- | --- |
| `GET /healthz` | — | — | intentional-skip | Liveness probe; CLI/desktop equivalent is just running the binary. |
| `GET /schema`, `GET /schema/list`, `GET /schema/{name}` | — | `bacio schema {all,list,show}` ✅ | parity | No desktop need — schemas are an agent-driver affordance. |
| `GET /repos/{prefix}`, `DELETE /repos/{prefix}` | partial (no Remove Repository UI) | `bacio repo show` / `bacio repo rm` ✅ | gap-defer | Desktop doesn't need `rm` (destructive, confirmation-heavy); `show` is implicit in the sidebar/board. |
| `POST /repos/{prefix}/features`, `PATCH …`, `DELETE …` | ❌ (Features view is read-only) | `bacio feature add / edit / rm` ✅ | gap-defer | Desktop deliberately read-only on features; not a parity gap for v1. Flag for the desktop roadmap, not BACI-32. |
| `GET /repos/{prefix}/features/{slug}/plan` | ❌ | `bacio feature plan` ✅ | gap-defer | Topo-ordered planning; no desktop UI yet. Could be the basis for a "Plan" tab eventually. |
| `GET /repos/{prefix}/features/{slug}/next`, `POST .../next` | ❌ | `bacio issue peek` / `bacio issue next` ✅ | gap-defer | Atomic next-claim is an agent-runtime affordance; no desktop need today. |
| `POST /repos/{prefix}/issues` | ❌ (no New Issue UI) | `bacio issue add` ✅ | gap-defer | Desktop is supervision-first; "create issue" is a known gap, separate from parity. Flag for the desktop roadmap. |
| `PATCH /repos/{prefix}/issues/{key}` (full edit) | ⚠️ (description only) | `bacio issue edit` ✅ | gap-defer | Desktop only edits the description today; title/feature edits are CLI-only. Flag for the desktop roadmap. |
| `DELETE /repos/{prefix}/issues/{key}` | ❌ | `bacio issue rm` ✅ | gap-defer | Destructive; punt. |
| `PUT /repos/{prefix}/issues/{key}/assignee`, `DELETE …` | ❌ | `bacio issue assign / unassign` ✅ | gap-defer | Desktop assignee is set indirectly via claim; manual override has no UI. |
| `POST /repos/{prefix}/relations`, `DELETE …` | ❌ | `bacio link / unlink` ✅ | gap-defer | No desktop UI for issue relations. Possible polish for the drawer. |
| `POST /repos/{prefix}/issues/{key}/tags`, `DELETE …` | ❌ | `bacio tag add / rm` ✅ | gap-defer | Drawer shows tags but offers no editor. Flag for the desktop roadmap. |
| `GET /repos/{prefix}/issues/{key}/pull-requests`, `POST …`, `DELETE …` | ⚠️ (renders in drawer, no add/remove) | `bacio pr list / attach / detach` ✅ | gap-defer | Same. |
| `POST /repos/{prefix}/documents` (create) | ❌ | `bacio doc add` / `bacio doc upsert` ✅ | gap-defer | Desktop edits existing docs only; create is CLI-only. |
| `PUT /repos/{prefix}/documents/{filename}` (upsert) | ❌ | `bacio doc upsert` ✅ | gap-defer | Same. |
| `DELETE /repos/{prefix}/documents/{filename}` | ❌ | `bacio doc rm` ✅ | gap-defer | Same. |
| `GET /repos/{prefix}/documents/{filename}/download` | ❌ | `bacio doc download` (remote) / `bacio doc export` (local) ✅ | gap-defer | No desktop need (the editor already shows the body). |
| `POST /repos/{prefix}/documents/{filename}/rename` | ❌ | `bacio doc rename` ✅ | gap-defer | No desktop UI. |
| `POST /repos/{prefix}/documents/{filename}/links`, `DELETE …` | ⚠️ (renders, no editor) | `bacio doc link / unlink` ✅ | gap-defer | Drawer shows linked docs but offers no link/unlink. |
| `GET /history` (cross-repo) | ❌ (per-repo only) | `bacio history --all-repos` ✅ | gap-defer | Desktop history is intentionally per-repo. |

## 3. CLI verbs → desktop × REST

The CLI verbs not already covered by either of the two tables above. Most
are admin verbs the desktop deliberately doesn't expose; the audit
classifies them, it doesn't recommend the desktop adopt them all.

| CLI | Desktop | REST | Status | Notes |
| --- | --- | --- | --- | --- |
| `bacio status` | ❌ | ❌ | intentional-skip | Read-only probe for shells/scripts; desktop equivalent is "the app is running". |
| `bacio issue brief <KEY>` | partial (built into `GetIssue`'s payload) | `GET …/brief` ✅ | parity | Bulk read for skills; desktop equivalent is the drawer payload. |
| `bacio comment list <KEY>` | partial (comments inline in `GetIssue`) | `GET …/comments` ✅ | parity | Same. |
| `bacio sync …` | ❌ | ❌ | intentional-skip | Out of scope per issue. |
| `bacio install-skill / install-hooks / install-channel / install-sample-skills` | ❌ | ❌ | intentional-skip | Touch the filesystem of the calling shell; not a REST/desktop concern. |
| `bacio demo` (hidden) | — | — | intentional-skip | Hidden dev helper. |
| `bacio tui` (harness shim) | — | — | n/a | Out of scope. |
| `bacio hook *` (hidden shim) | — | — | n/a | Out of scope. |
| `bacio channel` (hidden shim) | — | — | n/a | Out of scope. |
| `bacio api` (harness shim) | — | — | n/a | The thing being audited, not a row. |
| `bacio agent register / heartbeat / end / claim / release / show / inbox / ack` | ❌ | ❌ `ErrLocalOnly` | **gap-close-now** (rides with #1) | All eight ride the same agent-registry REST gap as `ListAgents` / `DispatchIssue`. Bundled into follow-up #1 rather than fanned out into eight tickets — the routes land or don't land together. |
| `bacio settings template list / show / set / reset` (+ `states …`) | ✅ (`SettingsService` covers list/save/state-gate) | ❌ `ErrLocalOnly` | **gap-close-now** (rides with #3) | Same — bundled. |

## 4. TUI vs desktop notes

The TUI ships the same six tabs (Board, Features, Documents, Agents,
History, Settings) the desktop does and broadly tracks the desktop's
feature set tab-for-tab. Two notable points the audit confirms:

- **Hide-empty-columns is implemented twice, with different UX.**
  - TUI: per-repo `tui_settings` rows `board.hidden_states` (the user
    picks which specific states to hide) and `board.hidden_features`
    (filter the board by feature slug). Fine-grained, per-repo.
  - Desktop: global `app_settings` boolean `board.hide_empty_columns`
    (auto-hide every column with zero cards). Coarse-grained, global.
  - These are *different features*, not two implementations of the same
    feature: the TUI's hide-specific-columns has no desktop equivalent,
    and the desktop's auto-hide-empty has no TUI equivalent. Unifying
    them would either lose functionality (drop TUI's per-column toggle)
    or add new UX to the desktop (a per-column picker). **Recommend:
    leave as-is; rename the desktop preference to clarify it's auto-hide
    behaviour, not "the same setting as the TUI".** Not a close-now gap.
- **TUI Settings tab covers what the desktop's does for prompt templates
  + state-gates, and nothing else.** Board preferences live only on the
  desktop today. If we add `bacio settings board prefs` (follow-up #7),
  the TUI Settings tab is the natural third surface and should grow a
  second section to match.

## 5. Gap classifications

### gap-close-now (raise follow-ups)

| # | Surface(s) | Gap |
| --- | --- | --- |
| 1 | REST | Agent registry: `register`, `heartbeat`, `end`, `claim`, `release`, `list`, `show`, `inbox`, `ack`, plus the open-claims read used by the desktop Board to derive `taken`. Blocks BACI-30 web-app mode. |
| 2 | REST | Agent dispatch: `create`, `list`, `inbox`, `ack`. State-gated auto-pick variant included. |
| 3 | REST | Prompt templates: `GetPromptTemplates`, `SetPromptTemplate`, `GetPromptStates`, `SetPromptStates` (full parity with `bacio settings template`). |
| 4 | REST + CLI | `taken` flag on list-style issue reads (`/repos/{prefix}/issues`, `bacio issue list -o json`). Closing #1 makes derivation possible client-side; emitting it server-side avoids a fan-out query per board. |
| 5 | REST | Board preferences: `GET / PUT /board-preferences` (or sibling shape under `/settings/`), serving `hide_empty_columns`. |
| 6 | REST + CLI | State-gated auto-pick dispatch. REST: `POST /repos/{prefix}/issues/{key}/dispatch` with `{"mode": "..."}` that re-checks the state-gate and picks a free agent (same logic as `BoardService.DispatchIssue`). CLI: `bacio agent dispatch [issue-key] --mode <stage> --auto` (or equivalent) so an agent doesn't have to enumerate agents to pick a target. |
| 7 | CLI | Board preferences: `bacio settings board prefs` (`get` / `set hide-empty-columns true|false`), full --json + --dry-run + schema. |
| 8 | TUI | When #7 lands, add a Board Preferences section to the TUI Settings tab so the third surface matches. |

### gap-defer

Tracked in §2 above. Each row is independently shippable but the audit
recommends bundling them under "desktop admin parity" rather than fanning
out individual tickets; the larger ones (New Issue UI, Tag editor, Link
editor) should be roadmap calls, not parity follow-ups. The cross-repo
history view in the desktop is a small one worth keeping on the radar.

### intentional-skip

- `BoardService.ListColumns`, `SettingsService.PromptPlaceholders` — static
  enums; any client can derive locally.
- `LeaderService.GetLeaderStatus` and the `leaderStatus`/`time` events —
  UI-internal; CLAUDE.md `## Conventions that aren't obvious` is the
  durable rationale for the leader.
- The four harness-integration shims and the four `install-*` commands.
- `bacio sync` and `bacio status`.

## 6. Follow-up issues to raise

This issue closes when:

1. This audit doc lands on `main`.
2. The eight follow-ups above are raised as separate bacio issues,
   tagged `rest-api` / `cli` / `tui` as appropriate, linked back to
   BACI-32 with `relates_to`.

The seven REST gaps (#1, #2, #3, #4 REST half, #5, #6 REST half) should
also be linked to [BACI-30](BACI-30) — they collectively define the
"web-app mode" parity work.

Schema discoverability flag (per agent-CLI principle #2): every new CLI
verb (#6 CLI half, #7) MUST land with a `--json` payload, a registered
schema, an `examples[0]`, and a `--dry-run` projection — see
[`docs/agent-cli-principles.md`](agent-cli-principles.md).
