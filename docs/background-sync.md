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
