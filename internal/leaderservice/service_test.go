package leaderservice

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/leader"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestStartEmitsSynchronouslyAndStopReleases covers the two contract
// guarantees the desktop and api adapters rely on:
//
//   - Start fires the emit callback once synchronously with a non-zero
//     state, so the UI doesn't render blank for the first 10s.
//   - Stop releases the lease, so a standby UI can promote on its next
//     tick instead of waiting out the 180s stale window.
//
// The 50ms sleep between Start and Stop matches the existing
// controller.TestControllerStartStop pattern — it lets the three
// background goroutines park in their first select on c.done before
// Stop runs.
func TestStartEmitsSynchronouslyAndStopReleases(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	svc := New(s, "leaderservice-test pid=1", nil)

	var emits int
	var lastState leader.State
	svc.Start(func(st leader.State) {
		emits++
		lastState = st
	})

	if emits != 1 {
		t.Fatalf("Start should fire emit once synchronously, got %d", emits)
	}
	if !lastState.AmLeader {
		t.Fatalf("synchronous startup tick should claim the lease on a fresh store, got AmLeader=false")
	}
	if cs := svc.CurrentState(); cs != lastState {
		t.Fatalf("CurrentState should match the seeded state %+v, got %+v", lastState, cs)
	}

	// Give the goroutines time to park in select before Stop closes c.done.
	time.Sleep(50 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		svc.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return within 2s — goroutines did not exit")
	}

	// After Release, the ui_leader row's holder_token is left in place
	// but heartbeat_at is zeroed — the next ACQUIRE succeeds immediately
	// rather than waiting out the 180s stale window. Re-running
	// TryAcquireLeader proves the release landed.
	tookOver, err := s.TryAcquireLeader("standby-token", "standby pid=2")
	if err != nil {
		t.Fatalf("TryAcquireLeader after Stop: %v", err)
	}
	if !tookOver {
		t.Fatal("Stop should have released the lease; standby could not take over")
	}
}

// TestStartNilEmitTolerated: passing emit=nil mirrors the api adapter
// (no event bus to push state through). The synchronous heartbeat
// should still happen, just without a callback.
func TestStartNilEmitTolerated(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	svc := New(s, "leaderservice-test pid=1", nil)
	svc.Start(nil)
	// Defer Stop in a goroutine so the test fails fast if the controller
	// hangs rather than waiting for the 10-minute -timeout fallback.
	defer func() {
		time.Sleep(50 * time.Millisecond)
		done := make(chan struct{})
		go func() {
			svc.Stop()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Stop did not return within 2s — goroutines did not exit")
		}
	}()

	if cs := svc.CurrentState(); !cs.AmLeader {
		t.Fatal("synchronous startup tick should claim the lease even when emit is nil")
	}
}
