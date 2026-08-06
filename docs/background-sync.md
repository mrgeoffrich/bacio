# Background sync (BACI-89)

`bacio sync` is no longer CLI-only. The leader-elected controller runs a leader-gated ticker that mirrors every sync-enabled repo automatically on a 5-minute interval, running the same `sync.Engine.Run()` pipeline a manual `bacio sync` runs.

This doc captures the design — the leader gating, the self-gating in the runner, the toggle, the HTTP read endpoints, and the per-tick logic.

## Where the code lives

- [`internal/sync/background.go`](../internal/sync/background.go) — `BackgroundRunner`. Owns the per-tick logic: in-flight overlap guard, exponential backoff on failure, per-repo error reporting.
- [`internal/controller/controller.go`](../internal/controller/controller.go) — the `SyncIfLeader` helper plus the sixth leader-gated ticker that calls it on `store.SyncTickInterval` (5 min).
- [`internal/leaderservice/service.go`](../internal/leaderservice/service.go) — the constructor every long-running surface uses; wires the runner into the controller automatically.
- [`internal/store/leader.go`](../internal/store/leader.go) — `SyncTickInterval` constant.

The dependency direction is `controller → sync` (controller imports sync, never the reverse) so there's no cycle.

## How it composes with the existing surfaces

Every long-running bacio surface that holds the `ui_leader` lease automatically runs the background sync ticker:

- `bacio api` / `bacio web` — both go through `leaderservice.New`.
- `bacio-desktop` — same constructor.
- `bacio tui` — drives the package-level `SyncIfLeader` helper directly (it doesn't construct the full `leaderservice`).

Only one of them runs the ticker at a time (whichever holds the lease). Standbys skip the tick entirely.

## The runner's self-gating

`BackgroundRunner.Tick` has three short-circuits before doing any work:

1. **In-flight guard.** An `atomic.Bool` flag set at the start of a tick and cleared at the end. A slow git run that outlasts the 5-minute interval causes the next tick to skip rather than stack.
2. **Backoff state.** After a tick where any repo errored, `failCount` is bumped and `nextEligible` pushed out exponentially (base = 5 min, capped at 1 h). A clean tick resets both. This keeps an unattended loop from hammering a remote that's rejecting pushes (a real conflict needing human resolution).
3. **No sync-enabled repos.** A non-sync user pays only an empty `ListRepos` loop every 5 min — no git invocations, no remote network traffic.

## The toggle: opt-out, default-ON

Once sync is configured for at least one repo, background sync is **on by default**. The global `sync.background_enabled` `app_setting` toggles it.

`Store.GetSyncBackgroundEnabled` deliberately inverts the usual `app_setting` default: a missing row reads as `true`. So fresh installs and pre-BACI-89 DBs both get the new behaviour without a migration write.

Toggle via:

- `bacio settings sync-background true|false` — CLI. Schema entry `settings.sync-background`, follows the six agent-CLI principles (`--json`, `--dry-run`).
- HTTP `GET / PUT /settings/sync-preferences` — read or update the same setting from any HTTP client / the React UI.

## Live status

Three fields are tracked per repo so the desktop and web UIs can render a live mirror state:

- `last_sync_at` — when the last successful tick finished.
- `last_sync_error` — the most recent error string, cleared on a successful tick.
- in-progress flag — `BackgroundRunner.InProgress()`, surfaced from the runner directly (no store row needed; only meaningful on the leader).

Exposed read-only over:

- `GET /sync` — global state.
- `GET /repos/{prefix}/sync` — per-repo state.

The React `api.http.ts` consumes both. The desktop / web `Sync` topbar badge is a **live status indicator**, not a button — there is no manual "Sync now" affordance in the UI; the ticker is the only writer. Manual `bacio sync` from the CLI still works and is the right tool for "I want to force a push right now."

### `configured` is not "is this repo synced" (BACI-376)

The export is **whole-DB**: [`Engine.Export`](../internal/sync/export.go) walks `store.ListRepos()` with no filter and writes every tracked repo into `repos/<prefix>/` of whichever sync repo the tick is running against. So a project that has never seen `bacio sync init` still has its issues mirrored the moment *any other* project on this machine drives a tick.

That makes two per-repo questions, and the status payload answers both:

| Field | Means |
|---|---|
| `configured` | this repo has a `sync.remote` in its own `.bacio/config.yaml` **and** a `sync_remotes` row resolving it — i.e. it can drive a tick itself. Gate for the setup flow and the "Unsynced projects" list. |
| `mirrored_by` | the label of the sync repo whose local clone already carries this repo's `repos/<prefix>/` folder — i.e. its data *is* being mirrored, whoever drove the tick. When set, `last_sync_at` / `last_error` describe that sync repo's last run. |

[`sync.MirrorCoverage`](../internal/sync/coverage.go) computes the second one (one `os.ReadDir` per registered sync remote) and both the HTTP handler and `client.localClient.SyncStatuses` read through it, so the two transports can't drift. It answers the same on-disk question `DiscoverMembership` does, which is what keeps the topbar badge and the Sync settings registry consistent — before BACI-376 the badge read only `configured` and reported "sync not configured" for repos the settings screen simultaneously listed as `linked` members of the sync repo.

The badge's variants live in [`desktop/frontend/src/lib/syncBadge.ts`](../desktop/frontend/src/lib/syncBadge.ts): `syncing` → `paused` (mirrored, but `background_enabled` is off) → `error` → `enabled` (configured **or** mirrored) → `unconfigured` (nothing mirrors it).

## The on-disk record layout, and the rule that keeps it compatible

Two mechanisms decide whether an **older** bacio binary survives a sync repo written by a newer one, and together they dictate where every new record kind is allowed to live:

- **Every manifest is parsed strictly.** `strictDecode` ([`internal/sync/yaml_parse.go`](../internal/sync/yaml_parse.go)) runs the decoder with `KnownFields(true)`, and `ParseRepoYAML` / `ParseIssueYAML` / `ParseDocumentYAML` / `ParseCommentYAML` / `ParseRedirectsYAML` and `ReadIndex` all go through it. A key an older binary doesn't know **fails its entire `bacio sync` run**, not just that record.
- **An older binary silently strips what it doesn't emit.** `ExportStaged` ([`internal/sync/export_staging.go`](../internal/sync/export_staging.go)) diffs staging against target byte-wise and writes any file whose bytes differ. An old binary's staged `repo.yaml` has no unknown key → bytes differ → it overwrites the target and the key is gone.
- **Files inside an existing record folder are worse than either.** `recordFolderOf` maps `repos/<P>/{features,issues,docs}/<label>/…` onto a deletable record folder. A stray `repos/<P>/docs/<label>/folder.yaml` therefore makes an old binary `os.RemoveAll` the whole document record, and the next import's `propagateDeletes` drops that document from the DB **on every machine**.

> **The rule: all new synced data lands in a NEW top-level sibling under `repos/<PREFIX>/`, or as a new file directly at `repos/<PREFIX>/<name>.yaml`. Never a new key in an existing manifest. Never a new file inside an existing record folder. Never a nested record folder.**

Three path segments (`repos/<P>/workspace.yaml`) or an unrecognised second segment (`repos/<P>/folders/…`, `repos/<P>/kanban/…`) both make `recordFolderOf` return `""`, so no binary — old or new — ever plans a delete against them; and an old binary's scanners only ever walk `repo.yaml` plus `features/`, `issues/`, `docs/`, so the new siblings are invisible to it. `TestLegacyRecordFolderOfIgnoresPivotPaths` runs the *frozen pre-pivot algorithm* against the new paths to pin that, and the back-compat tests in `pivot_backcompat_test.go` assert `repo.yaml` / `issue.yaml` / `doc.yaml` bytes are unchanged versus the pre-pivot emitter.

### The three record kinds

Path helpers live in [`internal/sync/paths.go`](../internal/sync/paths.go); the parsed shapes in [`internal/sync/yaml_parse.go`](../internal/sync/yaml_parse.go).

```
repos/<PREFIX>/workspace.yaml                     # sentinel: presence ⇔ this prefix is a workspace
  created_at, kind: "workspace", updated_at, uuid # uuid is the REPO's uuid

repos/<PREFIX>/folders/<folder-uuid>/folder.yaml  # one node of the page tree
  created_at, documents: [<doc-uuid>…], name, parent_uuid ("" == root),
  position, updated_at, uuid

repos/<PREFIX>/kanban/<column-uuid>/column.yaml   # one Kanban lane
  created_at, issues: [<issue-uuid>…], name, position, updated_at, uuid
```

**Record-folder segments are uuids, not labels.** A label would be renameable, which would need rename detection, redirect entries and case-collision handling; a uuid is immutable, so the path is a pure function of identity and a **rename is a pure content change**. (`indexKindFolders` still indexes both kinds by uuid so a *new* binary moving a record emits a `git mv` and preserves history.)

### Membership lives on the container, never on the member

`folder.yaml` lists document uuids; `column.yaml` lists issue uuids. Nothing about a folder or a lane is ever written into `doc.yaml` or `issue.yaml`.

That is the load-bearing choice of the whole design, and it follows straight from the rule above: a per-member field would have to become a new key in `doc.yaml` / `issue.yaml`, which is exactly the hard-fail case. Keeping membership on the container leaves those two manifests **byte-identical to what an older binary writes**, so nothing breaks in either direction. Two things fall out for free:

- **Order within the YAML sequence *is* the order** — per-folder page order and per-lane card order, with no extra field, and the importer can set `folder_id` + `folder_position` (or `kanban_column_id` + `kanban_position`) in one pass.
- **The store writers bump the container's `updated_at`, never the member's.** `SetDocumentFolder` and `SetIssueKanbanColumn` both do this deliberately: bumping the member would churn sync bytes for a field that never reaches disk, and would let a pure move win the last-writer-wins race against a real content edit made elsewhere.

### Membership dedupe

A bad three-way merge can leave the same document uuid listed in two `folder.yaml` files (or the same issue in two `column.yaml` files). The importer resolves it deterministically rather than failing: **sort the competing claims ascending by `(container updated_at, container uuid)` and the last one wins** — i.e. the most recently updated container takes the member, and an exact `updated_at` tie breaks on the higher container uuid. Within a *single* manifest a repeated uuid keeps its first index. Implemented as `claimMembership` / `claimBeats` in [`internal/sync/import_containers.go`](../internal/sync/import_containers.go), unit-tested in `pivot_containers_test.go`.

Only manifests this run treated as **authoritative** (seen on disk *and* not skipped by the last-writer-wins gate) get to place members. A folder whose local row won the LWW gate keeps its local membership, and a folder that exists only locally is left alone — otherwise the first import would empty every not-yet-shared folder.

### The workspace sentinel: presence promotes, absence means nothing

`upsertRepo` ([`internal/sync/import.go`](../internal/sync/import.go)) reads `kind` from the sibling `workspace.yaml`, never from `repo.yaml`, and inserts through `store.InsertPhantomRepoTx` — a helper that takes **no path parameter at all**, so the "a workspace is permanently pathless" invariant holds by construction.

**The sentinel's absence must never demote a workspace back to git.** An older binary's export simply never writes the file, so absence is not evidence of anything; demoting on absence would silently destroy every workspace on the first sync with a mixed-version peer. The transition is therefore one-way — a prefix that an older binary imported as an inert phantom is promoted once a sentinel appears, and a row that already has a working tree is never promoted at all (that collision is warned about, not papered over).

### A workspace is mirrored, but cannot drive a tick

There is **no per-workspace sync remote**. A sync configuration lives in `.bacio/config.yaml` at a git toplevel, and a workspace has no toplevel to put one in, so `client.SetupSync` refuses a workspace with its own message and `BackgroundRunner` keeps skipping any repo without a working tree. Because the export is whole-DB (see [`configured` is not "is this repo synced"](#configured-is-not-is-this-repo-synced-baci-376) above), a workspace's issues, documents, folders and lanes are mirrored anyway the moment any git repo on the machine drives a tick. `sync.DiscoverMembership` reports such a prefix as `StatusWorkspace` rather than `StatusPhantom` so the registry and the "unsynced projects" residual stay honest.

The gap this leaves: a machine with workspaces and **no** git repo at all never syncs. Giving a workspace a remote of its own is the follow-on.

## Per-tick logic (the algorithm)

For each sync-enabled repo:

1. Acquire the sync lock (lock file beside the SQLite DB; covers concurrent CLI `bacio sync` runs too).
2. Run the standard `sync.Engine.Run()` pipeline: pull → import → export → commit → push.
3. Record per-run status (success → clear error + bump `last_sync_at`; failure → set `last_sync_error`).
4. Release the lock.

A repo failure doesn't abort the tick — every other repo still runs. The tick is considered "failed" (for backoff purposes) if **any** repo errored.

## Why the actor is non-empty

`BackgroundRunner.NewBackgroundRunner` defaults the audit actor to `"bacio-background-sync"`. Audit log rows from the background ticker are visibly distinct from a manual `bacio sync` call, so a `bacio history --since 1d` can tell which side wrote what.

## Out of scope (v1)

- A "sync now" button in the UI. Deliberate: every push is leader-gated and rate-limited by the ticker. If a user really wants to force a sync they can run `bacio sync` from the CLI.
- Per-repo configuration of the tick interval. One global 5-minute interval, hardcoded in `store.SyncTickInterval`.
- Webhook-triggered sync. The pipeline is pull-based by design (git as the transport); push hooks aren't in scope.

## Compatibility note

The default-on semantics mean **existing DBs see background sync start running** after the user upgrades to a BACI-89 binary, with no user action. Users who don't want the ambient git activity can `bacio settings sync-background false` once.
