# Unified create affordance, New Epic page, Edit Epic page

**Status:** planning. No code written.
**Scope:** `desktop/frontend/src` (React, both transports), `desktop/featureservice.go` (Wails), regenerated bindings. **No CLI change, no store change, no HTTP route change, no sync-manifest change** — all three of those already do the job (see §5.2, §5.3).

The ask, verbatim: *"a consistent way for users to create new issues for the kanban board, add new epics, and add new documents — a consistent button in a place that makes sense. I also need a new epic page and an edit epic page."*

Three separable deliverables, and they are separable in the right order: the epic pages need a seam that does not exist yet (§3), the create affordance needs no new backend at all (§2). Sequencing in §6 puts the seam first so the pages have something to call.

---

## 0. What the code actually says (verify-first findings)

Everything the briefing asserted checks out, with three corrections and one finding that changes the shape of the work.

**Confirmed.** Routes live in `desktop/frontend/src/App.tsx:271-429`, all under `/:prefix/`. Epics are `App.tsx:325-340` (`/:prefix/epics` and `/:prefix/epics/:slug`, both `FeaturesView`); Documents `App.tsx:346-361`; Kanban `App.tsx:305-324`. The full-screen sub-route precedent is `/:prefix/pipeline/:key/process` → `ProcessEditor` at `App.tsx:290-299`. `api/feature.ts` has no `createFeature` (the file is 182 lines and every export is a read or a per-field setter). `bacio feature add` / `feature edit` exist (`internal/cli/feature.go:36-83`, `:175-262`). The topbar `+` moved into the Pipeline Backlog header (`components/Topbar.tsx:182-186` comment; the button itself now at `components/PipelineView.tsx:169-180`).

**Correction 1 — the HTTP side is already complete.** `POST /repos/{prefix}/features` is registered at `internal/api/router.go:60` and handled at `internal/api/handlers_feature.go:114-168`, with dry-run, slug derivation, audit row, and a 409 on slug collision. It is covered by seven tests (`internal/api/features_test.go:326-419`). The `PATCH …/features/{slug}` edit route (`handlers_feature.go:170-279`) already accepts `title`, `description`, `emoji` and `branch_name` **in one call** with presence-map semantics. So "the whole stack" for create is really *only* the Wails method and the two seam functions.

**Correction 2 — `api/feature.http.ts` has already drifted.** It exports `archiveFeature` / `unarchiveFeature` (`api/feature.http.ts:223-229`) which `api/feature.ts` does not. Nothing catches this: there is no `satisfies Contract` check between the transports for the feature domain (the only `satisfies` mentions in `src/api/` are prose comments — `api/pipeline.ts:60`, `api/settings.http.ts:223`). The parity guarantee `docs/frontend-architecture.md` §2 describes is enforced *per-DTO* in `contract.ts`, not per-function. That is a pre-existing bug, not ours to fix, but it means **adding a function to only one transport will compile and pass CI on the Wails build**. Discipline, not tooling, is what keeps this honest — add both twins in the same commit.

**Correction 3 — there is no way to edit an epic *title* anywhere in the UI.** `FeatureDetailPane.tsx:77` renders `<h2 className="mk-features-title">{detail.title}</h2>`, read-only. There is no `setFeatureTitle` on either transport. This is not "editing is scattered inline" — it is a genuine hole, and it is the strongest single argument for the Edit Epic page.

**The finding that changes the shape of the work — a workspace has no visible way to create an issue at all.** The `+` lives only in the Pipeline Backlog header (`PipelineView.tsx:169-180`, wired at `App.tsx:282`). `navFor()` (`lib/nav.ts:55-61`) hides the Pipeline tab whenever `showAgentSurfaces` is false, which is the **default for a workspace** (`docs/web-app-mode.md` §7b). The Kanban's lane `+` is `AddCardsMenu` (`components/kanban/KanbanLane.tsx:119-123`), which only places *already-existing* off-board cards (`components/kanban/AddCardsMenu.tsx:8-33`, candidates derived by `kanbanOffBoard.ts`) — it creates nothing. So on a manual workspace the only route to a new issue is the undiscoverable ⌘N (`App.tsx:201-208`). Symmetrically, on a git repo `show_kanban` defaults *off*, so the Kanban surface is unavailable there by default.

That inverts the framing. This is not primarily a consistency polish; it is closing a "there is no in-app way to do this" gap on two of the three record types, on at least one space kind each. The consistency is how we close it, not the reason to.

---

## 1. Survey: every create path today

| # | Affordance | Where it renders | What it creates | Chrome |
|---|---|---|---|---|
| 1 | Backlog `+` → `IssueComposer` | `components/PipelineView.tsx:169-180` (Pipeline Backlog header), modal mounted as a Shell sibling at `App.tsx:446-452` | issue | 20px icon-only `.mk-pl-new-issue`, `<Tooltip label="New issue (⌘N)">`, Radix `Modal` with a 4-field form (`components/IssueComposer.tsx:132-217`) |
| 2 | ⌘N | `App.tsx:201-208` | issue | keyboard only; guarded on `isEditingTarget` and a real prefix |
| 3 | Lane `+` → `AddCardsMenu` | `components/kanban/KanbanLane.tsx:119-123` | **nothing** — places existing off-board cards | `.mk-col-add-btn` + lucide `Plus size={14}`, Radix `DropdownMenu` with a combobox body (`AddCardsMenu.tsx:48-83`) |
| 4 | "Add lane" | `components/kanban/KanbanBoard.tsx:358-361` | kanban lane | trailing grid slot `.mk-col-add-lane`, lucide `Plus size={16}` + text, → `LaneNameDialog` (`KanbanBoard.tsx:365-376`) |
| 5 | Rail head New folder / New page | `components/docs/DocsTreeRail.tsx:97-114` | folder / page | two `.mk-icbtn .mk-docs-tree-headbtn`, lucide `FolderPlus` / `FilePlus2` `size={14}`, `title` + `aria-label` |
| 6 | Rail foot "New page" | `components/docs/DocsTreeRail.tsx:232-235` | page | `.mk-docs-rail-new`, lucide `Plus size={14}` + text label |
| 7 | Per-node "New page here" | `components/docs/DocsTreeNode.tsx:111-119` | page, **into that folder** | hover-revealed row button |
| 8 | Folder page header + empty state | `components/docs/DocsFolderPage.tsx:102-120`, `:137-145` | folder / page | `.mk-btn-secondary` × 2 + `.mk-btn-primary` |
| 9 | RepoPicker → "New Workspace…" | `components/RepoPicker.tsx:331` | workspace | Radix `Shelf` row → `WorkspaceCreateModal` (`components/workspace/WorkspaceCreateModal.tsx:29`, title at `:76`) |
| 10 | Settings → template Add | `components/settings/TemplateAddForm.tsx` | prompt template | inline form in the Settings pane |
| — | **Epics** | — | — | **nothing at all** |

Five distinct visual languages for the same verb: an icon-only 20px button with a tooltip (1), an icon-only 14px `.mk-icbtn` with a `title` (5), an icon+text ghost (6), a `.mk-btn-primary` (8), a trailing grid slot (4). Three distinct interaction models: modal form (1, 9), single-field naming dialog (4, 5, 6, 7, 8 — all funnelling through `DocsNameDialog` / `LaneNameDialog`), inline combobox popover (3). And one type — epics — with no affordance whatsoever, despite Epics being an **ungated** nav tab on every space (`lib/nav.ts:55-61`: only `agent`-group items and `board` are ever filtered).

The docs surface is the one that got this right, and it is worth naming why: it has a **cheap global entry** (rail head/foot) *and* a **context-carrying local entry** (per-node, per-folder-page) that passes the folder uuid through (`components/DocsView.tsx:361` derives `contextFolder`, threaded at `:410-411`, `:431-432`, `:465-466`). Any design that collapses those two into one loses the context.

---

## 2. A. The unified create affordance

### 2.1 The decision

**One shared `<CreateMenu>` component, rendered in two kinds of place: a global instance in the Topbar, and a scoped instance in each surface's own header.** Same component, same Radix `DropdownMenu` shell, same keyboard model, same glyph — differing only in a `scope` prop that reorders the items and pre-binds their context (which lane, which folder).

Concretely:

- **Topbar** — a `Plus` icon button in `.mk-topbar-right`, immediately left of `NotificationBell` (`components/Topbar.tsx:186-191`). Items: **New issue** (⌘N), **New epic**, **New page**. Disabled when `!activeBoard || activeBoard === 'all'` — the same gate `App.tsx:206` and `PipelineView.tsx:169` already apply.
- **Kanban lane header** — the existing `+` (`AddCardsMenu`) grows a pinned first row, `＋ New issue…`, above its search box. Not a second `+` button: the header is already carrying four controls in ~24px (`KanbanLane.tsx:111-146`) and a fifth would be noise. Both verbs answer the same question — *get a card into this lane* — so one menu is honest.
- **Epics list-pane head** — a new **New epic** button above the filter chips at `FeaturesView.tsx:93-97`, matching the Docs rail head's shape (`DocsTreeRail.tsx:95-124`). Single action, so it renders as a plain button rather than a menu; `<CreateMenu>` exposes a single-item degenerate form for exactly this.
- **Documents** — unchanged behaviour, re-skinned to the shared chrome. The per-node and folder-page entries stay as they are (they carry context the global menu cannot).
- **Pipeline Backlog `+`** — kept, re-skinned. It is a *placement* affordance ("new issue, into the Backlog"), the Pipeline's analogue of the lane menu.

The pattern that makes this "consistent" is stated as a rule rather than a component: **a `Plus` glyph in the top-right of whatever container the thing will be created into, opening either a menu (when the container accepts more than one type) or a form directly (when it accepts one).** The Topbar is the outermost container — the space itself — which is why the global instance offers all three.

### 2.2 Why, and what loses

**Runner-up: a topbar-only global "New ▾" split button.** It is the smaller change and it does fix the discoverability hole. It loses on two counts. First, it cannot carry context: "new page **here**" (`DocsTreeNode.tsx:111-119`) and "new issue **into this lane**" both depend on where the pointer is, and threading that up to the topbar means a context provider whose only consumer is a button — real complexity for a worse result. Second, an empty lane and an empty folder are precisely the moments a create action should be in the eye-line, and `DocsFolderPage.tsx:137-145` already encodes that judgement ("the moment you land on an empty folder is the moment you most want to write in it"). Deleting the local affordances to win consistency would regress the one surface that already got it right.

**Second runner-up: command-palette-only.** ⌘K already exists (`App.tsx:198-200`) and `CommandPalette.tsx` is the natural home for verbs. It loses outright on the literal ask — "a consistent **button** in a place that makes sense". An affordance reachable only by chord is not a button. It should be an *addition* (§2.4), never the primary.

**Third: per-surface only, no global.** This is the status quo plus an Epics button. It leaves the workspace hole open (no Pipeline tab → no issue create) and the git-repo hole open (no Kanban tab → no lane menu), which is the finding in §0. Rejected.

### 2.3 Where a type isn't creatable

All three types are creatable on every space — `navFor()` never gates `features` or `docs`, and issues exist on both repo kinds. So the global menu needs no per-item gating beyond the prefix guard. Two behavioural notes:

- **A new issue created from a Kanban lane is off-board on a git repo.** `internal/client/local_issue.go:450-468` places a new card on the first lane **only when `repo.IsWorkspace()`**; on a git repo `kanban_column_id` stays NULL by design, so the card would not appear in the lane it was created from. The lane-scoped create must therefore chain `api.addIssue(...)` → `api.moveIssueToKanbanColumn(prefix, key, column.uuid, 0)` — reusing `KanbanBoard.tsx:145-157`'s `placeCard` optimistic path so the card lands at the top of the lane it was created in. On a workspace the second call is a cheap no-op re-placement (position 0 rather than the default first lane), which is the behaviour a user asking for "new issue in *this* lane" expects anyway.
- **A new issue created from the Topbar** has no container context, so it uses the existing `IssueComposer` unchanged, including its auto-run switch (`IssueComposer.tsx:182-199`) and the `onComposerCreated` routing at `App.tsx:170-194`. That routing already branches on `activeView === 'pipeline'`; on a workspace (no Pipeline) it falls to `navigate(issuePath(...))`, which is correct.

### 2.4 CommandPalette

`CommandPalette.tsx` has **no create actions today** — `filtered` (`:39-41`) is the card list and nothing else. Adding a pinned **Create** section above **Issues** is the right keyboard story, but it is not free: the palette's activedescendant arithmetic (`:50-62`) assumes one flat array (`filtered[active]`), so a second section means the index must span both lists and `activeId` must resolve against the union. That is a ~30-line change to a file with an existing test (`components/__tests__/CommandPalette.test.tsx`). Scheduled as its own phase (§6, Phase 6) so it can be dropped without blocking anything.

### 2.5 Keyboard and a11y

- **⌘N stays "New issue"** (`App.tsx:201-208`) — muscle memory, and the tooltip already advertises it (`PipelineView.tsx:170`).
- **No new per-type chords.** ⌘D is browser-bookmark on the web transport; ⌘E and ⌘⇧E collide with editor shortcuts inside TipTap (`components/editor/`). The menu's own Radix typeahead ("e" → New epic) plus the ⌘K Create section is a better trade than three fragile global chords.
- The `<CreateMenu>` trigger is a `<button aria-label="Create…" aria-haspopup="menu">`; Radix `DropdownMenu` supplies `aria-expanded`, roving focus, Escape, and `role="menuitem"` for free — the same machinery `AddCardsMenu.tsx:52-82` and `LaneMenu.tsx` already rely on.
- **Breaking change to watch:** `AddCardsMenu.tsx:57-58` sets `aria-label`/`title` to `Add cards to ${laneName}`, and `components/kanban/__tests__/AddCardsMenu.test.tsx:30,37` asserts on it. Widening the menu to include create means relabelling (e.g. `Add or create cards in ${laneName}`) and updating that test. Cheap, but it *will* go red.
- The Docs rail head buttons already carry `title` + `aria-label` (`DocsTreeRail.tsx:101-102`, `:110-111`); the re-skin must preserve both.

---

## 3. B. The New Epic page

### 3.1 Route, and the collision

**`/:prefix/epics/new`**, rendered by a new `components/features/NewEpicPage.tsx`. A page, not a modal: the ask says page; `ProcessEditor` at `App.tsx:290-299` is the established precedent for a full-screen sub-route under a nav view; and the form carries five fields including a markdown description, which is past what `Modal` (`components/Modal.tsx`) comfortably holds — `IssueComposer` at four fields is already at the edge.

**The collision with `/:prefix/epics/:slug` (`App.tsx:333-340`) resolves itself in the router but not in the data.** react-router is pinned at `^7.15.1` (`desktop/frontend/package.json:41`) and ranks branches by score, not declaration order: a static segment scores higher than a dynamic one at the same depth, so `/:prefix/epics/new` wins over `/:prefix/epics/:slug` **wherever it is declared**. Declare it immediately above the `:slug` route anyway, for the reader.

The hazard runs the other way. `store.Slugify("New")` → `"new"`, and `featurePath(prefix, "new")` (`lib/routes.ts:94-96`) emits `/PREFIX/epics/new` — so an epic literally slugged `new` becomes unreachable and its list row would open the create page. Mitigation, in order of cost:

1. **Reserve `new` in the New Epic form's own slug validation** (client-side, in the pure `epicForm.ts` helper). Cheap, covers the surface that would create the problem. **Recommended.**
2. Belt-and-braces: `NewEpicPage` checks the already-loaded feature list and, if a feature with slug `new` exists, `<Navigate replace>` to its detail route. ~5 lines. Also recommended.
3. Reserving `new` at the store boundary (`ValidateSlug`, `internal/store/validate.go:212-224`) would also change `bacio feature add` behaviour and reject existing rows on edit. **Out of scope — flag for the human (§7).**

`/:prefix/epics/:slug/edit` has no equivalent hazard: `ValidateSlug` forbids `/`, so a slug can never swallow the trailing segment, and the deeper path outranks the shallower one unconditionally.

Add `newEpicPath(prefix)` and `editEpicPath(prefix, slug)` to `lib/routes.ts` beside `featurePath` (`:94-96`) — that file is the documented single source of truth for path shapes (`docs/web-app-mode.md` §7a) and has its own smoke suite at `lib/__tests__/routes.smoketest.mjs`.

### 3.2 The stack, and the pattern to copy

Layer by layer, with what already exists:

| Layer | File | Status |
|---|---|---|
| Store | `store.CreateFeature` — `internal/store/features.go:32-63` | **exists** |
| Local client | `localClient.CreateFeature` — `internal/client/local_feature.go:45-80` | **exists** |
| Remote client | `remoteClient.CreateFeature` — `internal/client/remote_feature.go:51` | **exists** |
| HTTP route | `POST /repos/{prefix}/features` — `internal/api/router.go:60`, `internal/api/handlers_feature.go:114-168` | **exists**, tested at `internal/api/features_test.go:326-419` |
| Wails service | `FeatureService.CreateFeature` — `desktop/featureservice.go` | **new** |
| Bindings | `desktop/frontend/bindings/.../featureservice.ts` | **regenerated** by `./build.sh` (`build.sh:168`, `wails3 build`). Never hand-edit. |
| Seam (Wails) | `createFeature` in `desktop/frontend/src/api/feature.ts` | **new** |
| Seam (HTTP) | `createFeature` in `desktop/frontend/src/api/feature.http.ts` | **new** |
| Contract | `api/contract.ts` | **no new DTO** — return the existing `FeatureDetail` (`contract.ts:776-801`) |

**The end-to-end pattern to copy is `addWorkspace`.** It is the most recent addition that crossed every layer at once and it is documented in the reshape table (`docs/web-app-mode.md` §4, the `addWorkspace(name, prefix?)` row):

- Wails method: `BoardService.AddWorkspace` (`desktop/boardservice.go`), reshaping into a DTO before returning.
- HTTP twin: `POST /workspaces` → `internal/api/handlers_workspace.go`.
- Seam Wails: `api/board.ts:87-93` — try / `normalize(err)` / reshape.
- Seam HTTP: `api/board.http.ts:105-112` — build the body from only the fields present, `call<ApiRepo>(…)`, reshape.
- Docs: a row added to `docs/web-app-mode.md` §4.

Two mechanical notes for the HTTP twin. `POST /repos/{p}/features` returns a bare `model.Feature` (`handlers_feature.go:167`), not a `FeatureView`, so `createFeature` must re-`getFeature(prefix, slug)` after the POST to hand back a `FeatureDetail` — which is exactly what every other mutator in `api/feature.http.ts` already does (`:60-65`, `:83-88`, `:102-107`, …). And the strict decoder (`inputio.DecodeStrict[inputs.FeatureAddInput]`, `handlers_feature.go:123`) rejects unknown fields with a 400 (`features_test.go:365-373`), so the body must carry exactly `{title, slug?, description?, emoji?, branch_name?}`.

### 3.3 Field set

| Field | Required | Notes |
|---|---|---|
| **Title** | yes | `ValidateTitle` at the store boundary; the handler 400s on empty with `{"field":"title"}` (`handlers_feature.go:128-131`). |
| **Slug** | no — derived | `store.Slugify(title)` (`internal/store/features.go:16-27`): lowercase, trim, `[^a-z0-9]+` → `-`, strip leading/trailing `-`, empty → `"feature"`, truncate to 60. **Both** create paths apply it identically (`handlers_feature.go:132-135` and `local_feature.go:49-52`), so the UI can mirror it exactly for a live preview and still leave the server authoritative. Surface it as a live-derived read-only field with an "edit" toggle — the slug is the epic's identity in the URL (`featurePath`), the CLI, and the sync folder layout. |
| **Emoji** | no | Reuse `components/FeatureEmojiPicker.tsx` (mounted today at `FeatureDetailPane.tsx:62-76`). Store validates exactly one grapheme cluster or empty. |
| **Description** | no | Markdown. Use a plain `<textarea>` in create mode, **not** `InlineDescriptionEditor` (`FeatureOverviewSections.tsx:125`) — that component is built around "there is a saved value, click to edit it". |
| **Branch** | no | Reuse `lib/branchName.isValidBranchName` for the pre-flight, as `FeatureBranchEditor.tsx:49` does. **Hint that the branch is per-machine** — see §5.2. |
| State / auto-close / collect-handoffs / show-on-board | **not on create** | `store.CreateFeature`'s signature (`features.go:32`) can't set them; each would need its own follow-up round trip; all four default sensibly. Edit page only. |

**Slug collision needs a client-side pre-check.** `features` has `UNIQUE(repo_id, slug)` (`internal/store/schema.sql:98`) and `store.CreateFeature` does not pre-check, so a duplicate surfaces as the raw SQLite text `UNIQUE constraint failed: features.repo_id, features.slug`. The HTTP layer maps that to a 409 (`internal/api/errors.go:66-69`) but keeps the message verbatim — and the Wails path has no mapping at all. Compare `createKanbanColumn`, where the store replaces the text with `kanban column %q already exists in this repo` (`errors.go:117-125`). Since `FeaturesView` has already loaded the full feature list (`FeaturesView.tsx:75-79`), the New Epic form should check locally and refuse before the round trip, with a "that slug is taken by *<title>*" message. Keep a server-error fallback for the race.

---

## 4. C. The Edit Epic page

### 4.1 Route

**`/:prefix/epics/:slug/edit`** → `components/features/EditEpicPage.tsx`. No collision (§3.1). Entry point: an **Edit** button in the title row of `FeatureDetailPane.tsx:61-81`, beside the state pill.

### 4.2 What moves, and — more importantly — what does not

A wholesale migration would be a clear regression. Be explicit:

**Stays inline, unchanged.** The four property rows in `FeatureOverviewSections.tsx:50-123` — the tri-state **State** segmented control (`:51-80`), **Auto Close** (`:82-91`), **Collect handoffs** (`:93-103`), **Show on board** (`:105-115`) — plus **`FeatureBranchEditor`** (`:117-122`) and the **emoji picker** (`FeatureDetailPane.tsx:62-76`). Every one of these is a single-gesture optimistic write: the three booleans flip through `FeaturePropertyToggle` → `useOptimisticToggle`, the tri-state through `useFeaturePropertyUpdate.update` (`components/features/useFeaturePropertyUpdate.ts:60-69`), the branch saves on blur/Enter with inline validation (`FeatureBranchEditor.tsx:42-84`), the emoji persists on pick. Routing to a page and pressing Save to flip a switch is strictly worse than what ships today.

**Stays inline, also present on the page.** The **description** — `InlineDescriptionEditor` at `FeatureOverviewSections.tsx:125-143` works well and should not be removed.

**Gains its only home on the page.** The **title**. There is no title editor anywhere in the React tree and no `setFeatureTitle` on either transport (§0, Correction 3).

So frame the Edit page as *"every editable property of the epic in one form, including the one that has no other home"* — a bulk-edit surface, not a replacement for the inline affordances. Its field set is the same five as the New page (title, slug **read-only**, emoji, description, branch) plus the four toggles rendered as the same controls the detail pane uses, so nothing has two different appearances. Slug is read-only because renaming it is a redirect-generating operation the sync layer models separately (`internal/sync/redirects.go`) and is well out of scope.

### 4.3 Batched update vs per-field setters

**Add one batched `updateFeature`.** Reasoning:

- The existing per-field setters (`api/feature.ts:43-84`) each resolve to `client.UpdateFeature(ctx, repo, slug, nil, nil, &x, nil, false)` — one column per call. A Save that changed title + description + emoji + branch would be **four sequential round-trips** with four partial-failure states, and the page would have to reconcile a half-applied edit.
- Both backends already accept all four in one call: `client.UpdateFeature` (`internal/client/local_feature.go:82-132`) takes four `*string`s, and `PATCH /repos/{p}/features/{slug}` (`handlers_feature.go:170-279`) decodes a presence map and builds the same four pointers (`:189-227`).
- So the batched shape is *already the backend's native shape*. The four per-field setters are the special case, and they stay for the inline affordances.

The alternative — add only `setFeatureTitle` and let the page fire N calls — loses on atomicity and on fidelity to the PATCH semantics the server already implements.

**Presence-map footgun (HTTP transport).** `handlers_feature.go:199-227` treats a key that is *present but null/empty* as "clear this field" and an *absent* key as "no change". So `updateFeature` must build its body from only the keys the caller actually supplied:

```ts
const body: Record<string, string> = { slug };
if (fields.title !== undefined) body.title = fields.title;
if (fields.description !== undefined) body.description = fields.description;
// … never spread an object containing `undefined` values
```

Spreading `{...fields}` with `undefined` members would serialise them away in `JSON.stringify` and *look* right — until someone changes the serialiser. Build the object explicitly.

**Wails shape.** `FeatureService.UpdateFeature(repoPrefix, slug string, title, description, emoji, branchName *string) (FeatureDetail, error)`. Pointer params bind cleanly in Wails v3: `DocService.MoveDocToFolder` takes `position *int` (`desktop/docservice.go:323`) and binds as `position: number | null` (`bindings/.../boardservice.ts:695` shows the same for `SetIssueKanbanColumn`). So `*string` → `string | null`, and the seam maps `undefined` → `null`. If the alpha surprises us at bindings-regen time, the fallback is four `string` params plus four `bool` "set" flags — uglier, no ambiguity. Phase 1's `./build.sh` proves which.

Also update the stale comment at `desktop/featureservice.go:127-131`: *"Read-only: features are created and edited via the CLI."*

---

## 5. D. Cross-cutting

### 5.1 The cross-transport enum / runtime-import footgun

**This work touches no Wails enum.** `FeatureDetail.state` is a plain `string` in the contract (`contract.ts:787`), and `FEATURE_STATE_OPTIONS` in `components/features/constants.ts` are plain string ids — so there is no `WaitingKind.X` / `QuestionState.X`-shaped hazard here (`.claude/rules/frontend-typescript.md`, "Cross-transport seam").

The two things that **will** pass `npx tsc --noEmit` and break `npm run build:web`:

1. **Importing `FeatureService` from `bindings/...` outside `api/feature.ts`.** The binding module imports `Create` from `@wailsio/runtime`, which the web stub (`src/wails-stub.ts`) does not provide. If `NewEpicPage` reaches for the binding directly to save a round trip, the desktop build is green and the web build dies. Runtime values come from the `./api` barrel, always.
2. **Adding `createFeature` / `updateFeature` to one transport only.** `tsc` type-checks components against `api.ts` (Wails), so a missing HTTP twin is invisible until `build:web` — and, per §0 Correction 2, there is no `satisfies` check for the feature domain to catch it either. CI runs both builds (`.github/workflows/ci.yml`'s `desktop-frontend` job, `docs/web-app-mode.md` §5.3), which is the real net. Add both twins in the same commit.

Run `npm run build:web` after every seam-adjacent phase, not just at the end.

### 5.2 Sync manifest

**No manifest change, confirmed rather than assumed.**

`ParsedFeature` (`internal/sync/yaml_parse.go:51-88`) is the on-disk shape of `feature.yaml` and it already carries `uuid`, `slug`, `title`, `description_hash`, `created_at`, `updated_at`, `emoji`, `archived_at`, `state`, `state_manual`, `collect_handoffs` — every field the New and Edit pages write. Nothing new goes into an existing manifest, so the `strictDecode` / `KnownFields(true)` hard-fail described in the CLAUDE.md tripwire is not in play.

Creating an epic writes a `features` row, which `Engine.Export` renders as a **new top-level record folder** `repos/<PREFIX>/features/<slug>/` (`internal/sync/paths.go:32`, `:91`, `:96-98`). That is the allowed shape — the tripwire forbids new *keys* in an existing manifest and new *files inside an existing record folder*, not new sibling record folders under an already-known parent, which is what every `bacio feature add` has always produced.

**One nuance worth writing down: `branch_name` does not sync.** There is no `BranchName` field on `ParsedFeature`, so the per-feature integration branch is per-machine state. Setting it on the New Epic page is fine; *adding it to `feature.yaml`* to "fix" that would be exactly the forbidden manifest-key addition. Do not.

### 5.3 CLI

**No CLI change.** `bacio feature add` (`internal/cli/feature.go:36-83`) and `bacio feature edit` (`:175-262`) already cover create and batched edit, both honour `--json` / `--dry-run` / strict decode, and both are in `bacio schema`. Nothing in `docs/agent-cli-principles.md` is triggered — this work adds no verb a person or agent types.

### 5.4 Tests

| Suite | What to add |
|---|---|
| `desktop/frontend/src/components/features/__tests__/` | New: `epicForm.test.ts` — the pure helpers (slug derivation mirroring `Slugify`, reserved-word check, local slug-collision check). Follows the `kanbanPlacement.ts` / `docsActions.ts` precedent of keeping fiddly pure bits in a sibling `.ts`. Plus a render test for the New page's required-title gate. |
| `desktop/frontend/src/lib/__tests__/routes.smoketest.mjs` | `newEpicPath` / `editEpicPath` shapes. Plain Node + assert, matching the existing file. |
| `desktop/frontend/src/components/kanban/__tests__/AddCardsMenu.test.tsx` | Update the two `aria-label` assertions (`:30`, `:37`); add a case for the pinned New-issue row. |
| `desktop/frontend/src/components/__tests__/CommandPalette.test.tsx` | Only if Phase 6 lands: the Create section and the spanning activedescendant index. |
| `desktop/featureservice_test.go` | `CreateFeature` / `UpdateFeature` against the fake client (the file already has four tests in this shape, `:56-149`). |
| `internal/api/features_test.go` | Nothing — `TestFeatureCreate*` (`:326-419`) and `TestFeatureEdit*` (`:420+`) already cover the routes we are consuming. |

Component tests mock the whole `../../../api` module (see `components/docs/__tests__/DocsView.test.tsx:22-46`) — every new seam function must be added to those mock objects or the mocked module goes undefined-shaped at runtime.

### 5.5 TUI

**Explicitly out of scope.** `internal/tui/` has no feature-create or feature-edit surface at all — the only `CreateFeature` references are test fixtures (`internal/tui/features_handoffs_test.go:24`, `internal/tui/settings_test.go:263,266`). The TUI's Features tab is a browser. The CLI already covers the verb for terminal users. No parallel work.

---

## 6. E. Work breakdown

Six phases. Each is independently buildable and independently useful; each names its files and its gate.

### Phase 1 — the feature seam: create + batched update

Backend-to-seam only; nothing renders yet. Doing this first means every later phase has something real to call.

- `desktop/featureservice.go` — add `CreateFeature(repoPrefix, title, slug, description, emoji, branchName string) (FeatureDetail, error)` and `UpdateFeature(repoPrefix, slug string, title, description, emoji, branchName *string) (FeatureDetail, error)`; both delegate to `client.CreateFeature` / `client.UpdateFeature` and return `f.GetFeature(...)`, mirroring `SetFeatureDescription` (`:343-353`). Fix the stale header comment at `:127-131`.
- `desktop/featureservice_test.go` — two tests.
- `desktop/frontend/src/api/feature.ts` — `createFeature`, `updateFeature` (try / `normalize(err)`).
- `desktop/frontend/src/api/feature.http.ts` — the twins. `createFeature` POSTs then re-`getFeature`; `updateFeature` builds the presence-map body explicitly (§4.3) then re-`getFeature`.
- `desktop/frontend/src/api/contract.ts` — a `FeatureUpdateFields` input type (`{title?, description?, emoji?, branchName?}`). No new response DTO.
- `docs/web-app-mode.md` §4 — two rows in the reshape table.

**Verify:** `go build ./...`, `go test ./...`, then `./build.sh` (regenerates bindings — this is the step that proves the `*string` binding), then `cd desktop/frontend && npx tsc --noEmit && npm run build:web`.

### Phase 2 — routes and path helpers

- `desktop/frontend/src/lib/routes.ts` — `newEpicPath`, `editEpicPath`.
- `desktop/frontend/src/lib/__tests__/routes.smoketest.mjs` — cases.
- `desktop/frontend/src/App.tsx` — two `<Route>`s inside the Epics block (`:325-345`), `/:prefix/epics/new` declared immediately above `/:prefix/epics/:slug`, each wrapped in `<ErrorBoundary>` like its siblings. Placeholder components so the phase builds.

**Verify:** `npx tsc --noEmit`, `npm test`.

### Phase 3 — New Epic page

- `desktop/frontend/src/components/features/epicForm.ts` — pure: `deriveSlug` (mirroring `store.Slugify`), `RESERVED_SLUGS = new Set(['new'])`, `slugTaken(features, slug)`.
- `desktop/frontend/src/components/features/NewEpicPage.tsx` — the form; reuses `FeatureEmojiPicker` and `isValidBranchName`; on success `navigate(featurePath(prefix, created.slug))`.
- `desktop/frontend/src/components/features/__tests__/epicForm.test.ts`.
- Styles in `desktop/frontend/src/styles/` reusing `.mk-features-*`.

**Verify:** `npx tsc --noEmit`, `npm test`, `npm run build:web`.

### Phase 4 — Edit Epic page

- `desktop/frontend/src/components/features/EditEpicPage.tsx` — loads via `useFeatureDetail`-style fetch, saves one batched `updateFeature`, then `navigate(featurePath(...))`.
- `desktop/frontend/src/components/features/FeatureDetailPane.tsx` — an **Edit** button in the title row (`:61-81`). Nothing removed.

**Verify:** `npx tsc --noEmit`, `npm test`, `npm run build:web`.

### Phase 5 — the unified create affordance

- `desktop/frontend/src/components/CreateMenu.tsx` — new shared component (Radix `DropdownMenu`, single-item degenerate form, `scope` prop).
- `desktop/frontend/src/components/Topbar.tsx` — global instance in `.mk-topbar-right` before `NotificationBell` (`:186-191`). Requires threading the composer-open flag out of `Shell` (`App.tsx:117`) or lifting it into a tiny context — decide at implementation time; the `onOpenSettings` / `onOpenSync` props (`Topbar.tsx:24-28`) are the existing precedent for shell-owned overlay flags as props.
- `desktop/frontend/src/components/FeaturesView.tsx` — **New epic** button above the filter strip (`:93-97`).
- `desktop/frontend/src/components/kanban/AddCardsMenu.tsx` — pinned `＋ New issue…` row; relabel the trigger.
- `desktop/frontend/src/components/kanban/KanbanBoard.tsx` — the create-then-place chain (§2.3), reusing `placeCard` (`:145-157`).
- `desktop/frontend/src/components/kanban/__tests__/AddCardsMenu.test.tsx` — update labels, add a case.
- `desktop/frontend/src/components/PipelineView.tsx` (`:169-180`), `desktop/frontend/src/components/docs/DocsTreeRail.tsx` (`:97-114`, `:232-235`) — re-skin to the shared chrome.

**Verify:** `npx tsc --noEmit`, `npm test`, `npm run build:web`, then `./build.sh` + `bacio web --no-open` and a Playwright pass over: git repo (Pipeline + Epics + Docs), manual workspace (Kanban + Epics + Docs — the hole this closes).

### Phase 6 — CommandPalette Create section, docs

- `desktop/frontend/src/components/CommandPalette.tsx` — pinned Create section; index spans both lists (§2.4).
- `desktop/frontend/src/components/__tests__/CommandPalette.test.tsx`.
- `docs/web-app-mode.md` §7a — the two new routes in the route map.
- `docs/frontend-architecture.md` §4 — a line on `components/features/` gaining the two pages and on `CreateMenu` being the shared create chrome.

**Verify:** `npm test`, `npm run build:web`, `./build.sh`.

---

## 7. Risks and open questions

**Risk 1 — the seam has no parity check, so a one-transport addition ships green.** §0 Correction 2: `archiveFeature` / `unarchiveFeature` already exist on HTTP only. Mitigation is procedural (both twins, same commit) plus CI's dual build. If the implementer wants a real fix, a `satisfies` shape for the feature domain is a small, separate, welcome change — but it is not this work.

**Risk 2 — the topbar `+` is a partial revert of BACI-287.** The `+` was deliberately moved *out* of the top-right into the Pipeline Backlog header, and `Topbar.tsx:182-186` records that the bell now holds that corner. This plan puts a `+` back there. The defence is that BACI-287 moved an *issue-only, Pipeline-bound* button, and what returns is a *type-agnostic menu* that fixes a hole BACI-287 created for workspaces. That is a judgement call about the user's own past decision — see §8.

**Risk 3 — a create action inside `AddCardsMenu` mixes two verbs in one popover.** "Put an existing card here" and "make a new card here" are different operations sharing a trigger. The alternative (a second `+`) crowds a 24px header. If the design agent's mockups say otherwise, the plan bends — nothing downstream depends on which of the two shapes wins.

**Question for the human — should `new` be a reserved feature slug at the store boundary?** §3.1 recommends client-side reservation only. Store-level reservation (`ValidateSlug`) would also constrain `bacio feature add` and could reject an already-existing row on its next edit. Cheap to do, not obviously right. **Human decides.**

**Question for the human — how much of the Edit page duplicates the detail pane?** §4.2 keeps every inline affordance and puts the same four toggles on the page too, so nothing has two appearances. The alternative is a lean page (title + description + emoji + branch only) with the toggles staying inline-only. Leaner is less code and less to keep in sync; fuller matches "an edit page" more literally. **Human decides**, and the design agent's mockup will make this concrete.

**Non-question, stated to close it:** the TUI needs no work (§5.5), sync needs no work (§5.2), the CLI needs no work (§5.3), and the HTTP API needs no work (§3.2). The entire backend delta is two methods on `desktop/featureservice.go`.
