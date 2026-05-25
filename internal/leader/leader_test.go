package leader

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/store"
)

// fakeBackend is a scripted Backend for exercising the elector's
// promote/demote transitions without a real database.
type fakeBackend struct {
	acquireOK  bool
	acquireErr error
	renewOK    bool
	renewErr   error
	current    store.LeaderInfo
	currentErr error
	released   bool
}

func (f *fakeBackend) TryAcquireLeader(token, label string) (bool, error) {
	return f.acquireOK, f.acquireErr
}
func (f *fakeBackend) RenewLeader(token string) (bool, error) { return f.renewOK, f.renewErr }
func (f *fakeBackend) ReleaseLeader(token string) error       { f.released = true; return nil }
func (f *fakeBackend) CurrentLeader() (store.LeaderInfo, error) {
	return f.current, f.currentErr
}

// TestElectorPromotesOnAcquire: a standby elector whose ACQUIRE succeeds
// promotes to leader.
func TestElectorPromotesOnAcquire(t *testing.T) {
	fb := &fakeBackend{acquireOK: true}
	el := New(fb, "tui pid=1")
	st := el.Tick()
	if !st.AmLeader {
		t.Fatal("elector should promote when ACQUIRE succeeds")
	}
	if st.HolderLabel != "" {
		t.Fatalf("leader state should carry no HolderLabel, got %q", st.HolderLabel)
	}
}

// TestElectorStaysStandby: a failed ACQUIRE leaves the elector on standby and
// surfaces the current holder's label for display.
func TestElectorStaysStandby(t *testing.T) {
	fb := &fakeBackend{
		acquireOK: false,
		current:   store.LeaderInfo{Label: "desktop pid=99"},
	}
	el := New(fb, "tui pid=1")
	st := el.Tick()
	if st.AmLeader {
		t.Fatal("elector should stay standby when ACQUIRE fails")
	}
	if st.HolderLabel != "desktop pid=99" {
		t.Fatalf("standby state should carry holder label, got %q", st.HolderLabel)
	}
}

// TestElectorDemotesWhenRenewFails: a leader whose RENEW returns false
// (another process took the lease) demotes on the next tick.
func TestElectorDemotesWhenRenewFails(t *testing.T) {
	fb := &fakeBackend{acquireOK: true, renewOK: true}
	el := New(fb, "tui pid=1")
	if st := el.Tick(); !st.AmLeader {
		t.Fatal("should be leader after first tick")
	}
	// Lease taken over: RENEW now fails and a new holder is on the row.
	fb.renewOK = false
	fb.current = store.LeaderInfo{Label: "desktop pid=99"}
	st := el.Tick()
	if st.AmLeader {
		t.Fatal("elector should demote when RENEW fails")
	}
	if st.HolderLabel != "desktop pid=99" {
		t.Fatalf("demoted state should carry the new holder label, got %q", st.HolderLabel)
	}
}

// TestElectorDemotesOnRenewError: a DB error during RENEW also demotes — the
// elector falls back to standby and will re-ACQUIRE on the next tick.
func TestElectorDemotesOnRenewError(t *testing.T) {
	fb := &fakeBackend{acquireOK: true, renewOK: true}
	el := New(fb, "tui pid=1")
	el.Tick() // promote
	fb.renewErr = errors.New("db gone")
	if st := el.Tick(); st.AmLeader {
		t.Fatal("elector should demote when RENEW errors")
	}
}

// TestElectorReacquiresAfterDemotion: once demoted, the elector tries ACQUIRE
// again and can re-promote.
func TestElectorReacquiresAfterDemotion(t *testing.T) {
	fb := &fakeBackend{acquireOK: true, renewOK: false}
	el := New(fb, "tui pid=1")
	el.Tick()       // standby -> ACQUIRE succeeds -> leader
	el.Tick()       // leader -> RENEW fails -> standby
	st := el.Tick() // standby -> ACQUIRE succeeds again -> leader
	if !st.AmLeader {
		t.Fatal("elector should re-promote after demotion when ACQUIRE succeeds")
	}
}

// TestCurrentStateReturnsLastTick: CurrentState returns the cached state from
// the last Tick without contacting the backend.
func TestCurrentStateReturnsLastTick(t *testing.T) {
	fb := &fakeBackend{acquireOK: true}
	el := New(fb, "tui pid=1")
	el.Tick()
	if !el.CurrentState().AmLeader {
		t.Fatal("CurrentState should reflect the last Tick")
	}
}

// TestReleaseCallsBackend: Release delegates to the backend's ReleaseLeader.
func TestReleaseCallsBackend(t *testing.T) {
	fb := &fakeBackend{}
	el := New(fb, "tui pid=1")
	el.Release()
	if !fb.released {
		t.Fatal("Release should call backend.ReleaseLeader")
	}
}

// TestTakeoverWritesAudit (BACI-160 gap 5) drives a real *store.Store
// across two electors: the first holds the lease, then is force-staled
// so the second can take it over. The second elector's takeover must
// write one `leader.takeover` history row carrying the displaced
// holder's label in Details.
func TestTakeoverWritesAudit(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "leader.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	// Process A acquires first — no previous holder, so this must NOT
	// write a takeover row.
	procA := New(s, "tui pid=1 host=alpha")
	st := procA.Tick()
	if !st.AmLeader {
		t.Fatalf("procA should hold the lease after first tick")
	}
	rows, _ := s.ListHistory(store.HistoryFilter{Op: "leader.takeover"})
	if len(rows) != 0 {
		t.Fatalf("first-ever acquire wrote %d leader.takeover rows, want 0", len(rows))
	}

	// Force the lease stale so procB's ACQUIRE conditional UPDATE
	// succeeds (mirrors the production "previous holder died" path).
	if _, err := s.DB.Exec(`UPDATE ui_leader SET heartbeat_at = 0 WHERE id = 1`); err != nil {
		t.Fatalf("force-stale lease: %v", err)
	}

	// Process B takes over. Must write exactly one row, Actor=its
	// label, Details carries the displaced label.
	procB := New(s, "desktop pid=99 host=beta")
	stB := procB.Tick()
	if !stB.AmLeader {
		t.Fatalf("procB should have taken over after force-stale")
	}
	rows, err = s.ListHistory(store.HistoryFilter{Op: "leader.takeover"})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("leader.takeover rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Actor != "desktop pid=99 host=beta" {
		t.Fatalf("takeover Actor = %q, want acquiring label", row.Actor)
	}
	if row.Kind != "leader" {
		t.Fatalf("takeover Kind = %q, want leader", row.Kind)
	}
	if !strings.Contains(row.Details, "from=tui pid=1 host=alpha") {
		t.Fatalf("takeover Details = %q, missing from= clause", row.Details)
	}
	if !strings.Contains(row.Details, "to=desktop pid=99 host=beta") {
		t.Fatalf("takeover Details = %q, missing to= clause", row.Details)
	}

	// Self-renewal on procB must NOT write a second row.
	stB2 := procB.Tick()
	if !stB2.AmLeader {
		t.Fatalf("procB should still hold the lease after self-renew")
	}
	rows, _ = s.ListHistory(store.HistoryFilter{Op: "leader.takeover"})
	if len(rows) != 1 {
		t.Fatalf("leader.takeover rows after self-renew = %d, want 1 (no duplicate)", len(rows))
	}
}

// TestTakeoverFakeBackendStaysSilent: the test fake doesn't satisfy
// AuditRecorder, so a successful acquire MUST NOT panic and there is
// no audit-row sink to assert against. Confirms the type-assertion
// guard in maybeRecordTakeover (the production *store.Store DOES
// satisfy AuditRecorder; only the test fake skips it).
func TestTakeoverFakeBackendStaysSilent(t *testing.T) {
	fb := &fakeBackend{acquireOK: true, current: store.LeaderInfo{Label: "someone else"}}
	el := New(fb, "tui pid=1")
	if st := el.Tick(); !st.AmLeader {
		t.Fatalf("fake-backend acquire should still promote the elector")
	}
}
