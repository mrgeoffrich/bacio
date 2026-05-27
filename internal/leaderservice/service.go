// Package leaderservice is the small composition layer that the desktop
// app and the `bacio api` server use to run the UI leader election. It
// owns a [leader.Elector] + [dispatcher.Matcher] + [controller.Controller]
// triple so the two host-side adapters
// (desktop/leaderservice.go, internal/api/leaderservice.go) collapse to
// thin shims that hand in a store + label and choose whether to forward
// the heartbeat tick to a Wails event bus.
//
// Why a new package and not inside [controller]: the controller package
// is named after the Controller type and slotting a higher-level façade
// in there muddies its role. And it can't live in [leader] because the
// elector primitive must stay free of any back-edge into controller.
package leaderservice

import (
	"log/slog"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/controller"
	"github.com/mrgeoffrich/bacio/internal/dispatcher"
	"github.com/mrgeoffrich/bacio/internal/idlepinger"
	"github.com/mrgeoffrich/bacio/internal/leader"
	"github.com/mrgeoffrich/bacio/internal/store"
	bsync "github.com/mrgeoffrich/bacio/internal/sync"
)

// StatusDTO is the wire shape both the desktop ("leaderStatus" Wails
// event + LeaderService.GetLeaderStatus) and the api (GET /leader)
// expose. Single source of truth for the JSON tags; the two host-side
// files re-export this as a type alias so their Wails / JSON bindings
// stay stable.
type StatusDTO struct {
	// AmLeader is true when the local process holds the UI leader lease.
	AmLeader bool `json:"amLeader"`
	// HolderLabel is the human-readable label of the process that currently
	// holds the lease — useful for "Standby — {HolderLabel} has control".
	// Empty when AmLeader is true or the lease has never been acquired.
	HolderLabel string `json:"holderLabel"`
}

// Service composes a *store.Store with a leader.Elector,
// dispatcher.Matcher, idlepinger.Pinger, and controller.Controller. It
// owns the elector + controller lifecycle but NOT the store — the
// caller still owns the *store.Store handle (matches the existing
// controller.Controller contract).
type Service struct {
	elector    *leader.Elector
	ctrl       *controller.Controller
	syncRunner *bsync.BackgroundRunner
}

// New builds a Service. label is the human-readable lease label
// ("desktop pid=… host=…" / "api pid=… host=… addr=…"); the caller
// constructs it so the host-specific shape is preserved. dbPath is the
// resolved SQLite path, threaded through to the BACI-89 background
// sync runner for its cross-process lock (an empty string falls back
// to store.DefaultPath at tick time). logger may be nil — defaults to
// slog.Default() inside controller helpers.
//
// The Service constructs the BACI-57 idle-pinger off a
// client.NewLocalFromStore wrapper around s — the pinger calls
// EnsurePingDispatch + EndAgent so its audit rows look identical to
// any other audited mutation. The wrapped client borrows the caller's
// store handle (doesn't open or close it), so the existing
// "Service owns the controller, caller owns the store" contract still
// holds.
//
// The BACI-89 background sync runner is constructed unconditionally —
// it self-gates on the sync.background_enabled toggle and on whether
// any repo is sync-enabled, so a non-sync user pays only an empty
// ListRepos loop every SyncTickInterval.
func New(s *store.Store, label, dbPath string, logger *slog.Logger) *Service {
	el := leader.New(s, label)
	// Use the host process label as the audit actor for reaper-driven
	// pings + ends. `recordOp` falls back to a generic default when the
	// actor is empty; spelling it explicitly here makes the rare bug
	// report ("who ended my session?") trivial to chase via the leader
	// label embedded in the audit row.
	pingerClient := client.NewLocalFromStore(s, label)
	syncRunner := bsync.NewBackgroundRunner(s, dbPath, label, logger)
	return &Service{
		elector:    el,
		ctrl:       controller.New(s, el, dispatcher.New(s).WithLogger(logger), idlepinger.New(s, pingerClient, logger), syncRunner, logger),
		syncRunner: syncRunner,
	}
}

// SyncInProgress reports whether the background sync runner is
// currently mid-tick. Only meaningful on the leader process — a
// standby never runs Tick, so it always reports false there. Used by
// the HTTP sync-status handler to surface a live "syncing now" flag.
func (s *Service) SyncInProgress() bool {
	return s.syncRunner.InProgress()
}

// Start kicks off the controller's six tick loops (heartbeat, prune,
// matcher, idle-pinger, archive sweep, background sync) and runs the
// synchronous initial heartbeat
// before returning so a caller seeding UI state from CurrentState gets
// a non-zero answer. emit is forwarded to controller.Start — nil
// disables it, non-nil is invoked on every heartbeat tick (including
// the synchronous startup one). Pair each Start with exactly one Stop.
func (s *Service) Start(emit func(leader.State)) {
	s.ctrl.Start(emit)
}

// Stop tears down the controller and releases the lease so a standby UI
// can promote within one tick (~10s) instead of waiting out the 180s
// stale window. Safe to call on a Service whose Start was never invoked.
func (s *Service) Stop() {
	s.ctrl.Stop()
}

// CurrentState returns the elector's cached state — synchronous, no DB
// round-trip. Used by GET /leader and the desktop's GetLeaderStatus to
// seed the UI before the first heartbeat event arrives.
func (s *Service) CurrentState() leader.State {
	return s.elector.CurrentState()
}
