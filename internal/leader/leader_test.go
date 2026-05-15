package leader

import (
	"errors"
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
