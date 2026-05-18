// Package controller is the shared leader-gated background loop that the
// desktop app, the `bacio api` server, and the TUI all run. Before this
// package existed, each of those three UIs hand-rolled the same three
// loops (heartbeat / live-list prune / BACI-51 queue matcher) inline,
// which is exactly how the api shipped without a matcher goroutine —
// drift between three near-identical copies. Centralising the loops here
// makes that class of bug impossible.
//
// Two surfaces:
//
//   - [Controller] wraps the elector + matcher + store and runs the
//     three loops as goroutines under a single done channel. Used by
//     long-lived processes that have no other event loop to drive ticks
//     off (the desktop's Wails lifecycle and the api's http.Server).
//   - [MatchIfLeader] and [PruneIfLeader] are per-tick helpers that
//     callers drive themselves. Used by the TUI, where bubbletea owns
//     the timer model and per-tick work runs inside Update handlers.
//
// Both surfaces gate on the elector's cached AmLeader state so exactly
// one process across the cluster runs the prune / matcher work at a
// time — the matcher's BindQueuedDispatch is conditional-UPDATE-safe,
// but two prune passes racing on the same DELETE would just waste
// cycles.
package controller

import (
	"log/slog"
	"sync"
	"time"

	"github.com/mrgeoffrich/bacio/internal/dispatcher"
	"github.com/mrgeoffrich/bacio/internal/leader"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// MatchIfLeader runs one [dispatcher.Matcher] tick if el holds the
// lease. Errors are logged at warn level and never returned — the queue
// matcher is mechanical janitor work (no audit row, no user-visible
// surface), and a transient DB blip should never propagate up to the
// caller's tick handler. A nil matcher or elector is a no-op.
func MatchIfLeader(m *dispatcher.Matcher, el *leader.Elector, log *slog.Logger) {
	if m == nil || el == nil || !el.CurrentState().AmLeader {
		return
	}
	if _, err := m.Tick(); err != nil {
		loggerOrDefault(log).Warn("bacio: queue match failed", "err", err)
	}
}

// PruneIfLeader runs one live-list prune of ended agent_sessions if el
// holds the lease. Drops rows older than
// [store.AgentSessionLiveListRetention] (the 4h "agents tab tidy"
// retention, not the 60-day store-open retention — that one runs on
// every Store.Open). Errors are logged at warn level and never
// returned. A nil store or elector is a no-op.
func PruneIfLeader(s *store.Store, el *leader.Elector, log *slog.Logger) {
	if s == nil || el == nil || !el.CurrentState().AmLeader {
		return
	}
	if _, err := s.PruneEndedAgentSessions(store.AgentSessionLiveListRetention); err != nil {
		loggerOrDefault(log).Warn("bacio: live agent-session prune failed", "err", err)
	}
}

// Controller owns the three background goroutines (heartbeat, prune,
// matcher) for desktop + api. The TUI does not use it — it drives the
// per-tick work itself from bubbletea Update handlers using the
// package-level helpers above — so this type intentionally has no
// bubbletea coupling.
type Controller struct {
	st      *store.Store
	el      *leader.Elector
	matcher *dispatcher.Matcher
	log     *slog.Logger

	done chan struct{}
	wg   sync.WaitGroup
}

// New builds a Controller backed by an already-constructed elector and
// matcher. The Controller takes ownership of the elector for shutdown
// (Stop calls Release); the store is not closed by the Controller —
// the caller still owns the *store.Store handle's lifecycle.
//
// log may be nil; helpers fall back to slog.Default().
func New(s *store.Store, el *leader.Elector, m *dispatcher.Matcher, log *slog.Logger) *Controller {
	return &Controller{st: s, el: el, matcher: m, log: log}
}

// Start fires the heartbeat synchronously once (so the caller sees a
// non-zero leader state before Start returns), then spins three
// goroutines: heartbeat on UILeaderHeartbeatInterval, prune on
// UILeaderPruneInterval, matcher on QueueMatchInterval. If emit is
// non-nil it is called with every heartbeat result (including the
// synchronous startup tick) — used by the desktop to push leader state
// to the Wails frontend.
//
// Start is not safe to call twice on the same Controller; pair each
// Start with exactly one Stop.
func (c *Controller) Start(emit func(leader.State)) {
	c.done = make(chan struct{})
	if c.el != nil {
		st := c.el.Tick()
		if emit != nil {
			emit(st)
		}
	}

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(store.UILeaderHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if c.el == nil {
					continue
				}
				st := c.el.Tick()
				if emit != nil {
					emit(st)
				}
			case <-c.done:
				return
			}
		}
	}()

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(store.UILeaderPruneInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				PruneIfLeader(c.st, c.el, c.log)
			case <-c.done:
				return
			}
		}
	}()

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(store.QueueMatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				MatchIfLeader(c.matcher, c.el, c.log)
			case <-c.done:
				return
			}
		}
	}()
}

// Stop closes the done channel, waits for the goroutines to exit, and
// releases the elector's lease so a standby UI can promote within one
// tick (~10s) rather than waiting out the 180s stale window. Safe to
// call on a Controller whose Start was never called: it just releases
// the elector.
func (c *Controller) Stop() {
	if c.done != nil {
		close(c.done)
		c.done = nil
	}
	c.wg.Wait()
	if c.el != nil {
		c.el.Release()
	}
}

// loggerOrDefault returns log if non-nil, else slog.Default(). The
// helpers want a guaranteed-non-nil destination so a forgotten logger
// in a caller doesn't crash the tick goroutine.
func loggerOrDefault(log *slog.Logger) *slog.Logger {
	if log != nil {
		return log
	}
	return slog.Default()
}
