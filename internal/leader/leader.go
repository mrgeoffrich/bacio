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
	"fmt"
	"log/slog"
	"sync"

	"github.com/mrgeoffrich/bacio/internal/identity"
	"github.com/mrgeoffrich/bacio/internal/model"
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

// AuditRecorder is the optional history-writing surface the elector
// uses to emit a `leader.takeover` row (BACI-160 gap 5) when this
// process takes the lease away from a different holder. *store.Store
// satisfies it; a Backend that doesn't (e.g. the test fake) simply
// won't emit takeover rows. Kept separate from Backend so the existing
// fakeBackend in leader_test.go stays minimal.
type AuditRecorder interface {
	RecordHistory(e model.HistoryEntry) error
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
	backend Backend
	token   string
	label   string
	// amLeader is owned by the Tick goroutine — Tick is the only reader and
	// writer, so it needs no lock.
	amLeader bool
	// mu guards cached: Tick (election goroutine) writes it, CurrentState
	// (e.g. the desktop's Wails binding goroutine) reads it.
	mu     sync.Mutex
	cached State
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
//
// On a successful takeover from a previous holder (BACI-160 gap 5),
// emits one `leader.takeover` history row carrying the previous
// holder's label in Details. First-ever acquisition (no previous
// holder) and self-renewals are deliberately silent — `bacio history
// --op leader.takeover` is the answer to "when did the lease change
// hands", not "every time leadership existed". Audit emission goes
// through whichever Backend also satisfies AuditRecorder (every real
// *store.Store does; the test fakeBackend does not, and stays
// silent).
func (e *Elector) Tick() State {
	if e.amLeader {
		ok, err := e.backend.RenewLeader(e.token)
		if err != nil || !ok {
			// Lost the lease (another process took over, or DB error).
			// Demote immediately; next tick will try ACQUIRE.
			e.amLeader = false
		}
	} else {
		// Capture the previous holder BEFORE the acquire attempt so a
		// successful takeover can be audited with the displaced label.
		// A read failure here is logged-and-swallowed: it must not block
		// the acquire (the election loop's whole point is to be resilient
		// to transient DB blips), and a missing previous-holder snapshot
		// just means the takeover audit row reads "from=unknown".
		prev, prevErr := e.backend.CurrentLeader()
		ok, err := e.backend.TryAcquireLeader(e.token, e.label)
		if err == nil && ok {
			e.amLeader = true
			e.maybeRecordTakeover(prev, prevErr)
		}
	}
	state := e.buildState()
	e.mu.Lock()
	e.cached = state
	e.mu.Unlock()
	return state
}

// maybeRecordTakeover writes a single `leader.takeover` history row
// (Actor=acquiring label, Kind=leader) when this acquire really did
// displace a different holder. Skipped when:
//   - The backend doesn't implement AuditRecorder (test fake).
//   - The previous holder was empty (first-ever acquisition).
//   - The previous label matches our own (we're re-acquiring a lease
//     we held before — only theoretically possible if a demote-then-
//     reacquire happened without anyone else touching the row, which
//     would be a self-recovery, not a takeover).
//
// Errors writing the row are logged at warn level and swallowed —
// losing one audit row must never break the elector loop.
func (e *Elector) maybeRecordTakeover(prev store.LeaderInfo, prevErr error) {
	rec, ok := e.backend.(AuditRecorder)
	if !ok {
		return
	}
	if prevErr != nil {
		// Best-effort: still record the takeover, just without the
		// previous label. Better than dropping the row entirely; the
		// reader can correlate via timestamps and acquiring-label.
		_ = rec.RecordHistory(model.HistoryEntry{
			Actor:       e.label,
			Op:          "leader.takeover",
			Kind:        "leader",
			TargetLabel: e.label,
			Details:     "from=unknown",
		})
		return
	}
	if prev.Label == "" || prev.Label == e.label {
		return
	}
	if err := rec.RecordHistory(model.HistoryEntry{
		Actor:       e.label,
		Op:          "leader.takeover",
		Kind:        "leader",
		TargetLabel: e.label,
		Details:     fmt.Sprintf("from=%s,to=%s", prev.Label, e.label),
	}); err != nil {
		// No injected logger on this struct — fall back to slog.Default
		// (matches the controller helpers' loggerOrDefault contract).
		slog.Default().Warn(
			"bacio: failed to record leader.takeover audit",
			"from", prev.Label, "to", e.label, "err", err,
		)
	}
}

// Release best-effort releases the lease on graceful shutdown. The holder_token
// guard means this is a no-op if the lease has already been taken over.
func (e *Elector) Release() {
	_ = e.backend.ReleaseLeader(e.token)
}

// CurrentState returns the last state computed by [Tick] without contacting
// the backend. Safe to call from any goroutine — the read is mutex-guarded.
func (e *Elector) CurrentState() State {
	e.mu.Lock()
	defer e.mu.Unlock()
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
