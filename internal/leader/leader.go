// Package leader implements the UI leader-election elector that both the
// TUI and the desktop app run. Exactly one process holds the lease at a
// time; the others stand by and poll until the holder's heartbeat goes stale.
//
// The election loop is intentionally simple:
//   - Leader    → RENEW; on (false, _) or error, demote to standby.
//   - Standby   → ACQUIRE; on (true, nil), promote to leader.
//
// Both UIs gate their dispatch behaviour on [Elector.CurrentState].AmLeader.
package leader

import (
	"github.com/mrgeoffrich/bacio/internal/identity"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// Backend is the minimal store surface the elector needs. *store.Store
// satisfies this interface directly.
type Backend interface {
	TryAcquireLeader(token, label string) (bool, error)
	RenewLeader(token string) (bool, error)
	ReleaseLeader(token string) error
	CurrentLeader() (store.LeaderInfo, error)
}

// State is a snapshot of the elector's current position.
type State struct {
	// AmLeader is true when this process holds the lease.
	AmLeader bool
	// HolderLabel is the human-readable label of the process that currently
	// holds the lease (empty when AmLeader is true or the row is unset).
	HolderLabel string
}

// Elector runs the election loop. Create one per process with [New], then call
// [Tick] on each timer fire. [Release] on graceful shutdown.
type Elector struct {
	backend  Backend
	token    string
	label    string
	amLeader bool
	cached   State
}

// New creates an Elector with a fresh per-process token. label is a
// human-readable string embedded in the lease row (e.g. "tui pid=1234
// host=mybox") so a standby process can show whose control it's deferring to.
func New(b Backend, label string) *Elector {
	return &Elector{
		backend: b,
		token:   identity.New(),
		label:   label,
	}
}

// Tick runs one election step and returns the resulting [State].
// The caller must re-invoke Tick on every [store.UILeaderHeartbeatInterval]
// tick; missing a tick is safe (the lease stays valid until the stale
// threshold) but extends the window before a standby process promotes.
func (e *Elector) Tick() State {
	if e.amLeader {
		ok, err := e.backend.RenewLeader(e.token)
		if err != nil || !ok {
			// Lost the lease (another process took over, or DB error).
			// Demote immediately; next tick will try ACQUIRE.
			e.amLeader = false
		}
	} else {
		ok, err := e.backend.TryAcquireLeader(e.token, e.label)
		if err == nil && ok {
			e.amLeader = true
		}
	}
	e.cached = e.buildState()
	return e.cached
}

// Release best-effort releases the lease on graceful shutdown. The holder_token
// guard means this is a no-op if the lease has already been taken over.
func (e *Elector) Release() {
	_ = e.backend.ReleaseLeader(e.token)
}

// CurrentState returns the last state computed by [Tick] without contacting
// the backend. Safe to call from any goroutine after initialisation.
func (e *Elector) CurrentState() State {
	return e.cached
}

func (e *Elector) buildState() State {
	if e.amLeader {
		return State{AmLeader: true}
	}
	info, err := e.backend.CurrentLeader()
	if err != nil || info.Label == "" {
		return State{}
	}
	return State{HolderLabel: info.Label}
}
