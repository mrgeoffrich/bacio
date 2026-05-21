package sync

import (
	"context"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/store"
)

// bgStore opens an in-memory store with no sync-enabled repos — enough
// to exercise the BackgroundRunner's gating logic without real git.
func bgStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestBackgroundRunner_Tick_SkipWhenToggleOff: with
// sync.background_enabled = false, Tick returns nil without doing
// anything, and the backoff state stays clean.
func TestBackgroundRunner_Tick_SkipWhenToggleOff(t *testing.T) {
	s := bgStore(t)
	if err := s.SetSyncBackgroundEnabled(false); err != nil {
		t.Fatalf("disable background sync: %v", err)
	}
	r := NewBackgroundRunner(s, "", "test", nil)
	if err := r.Tick(context.Background()); err != nil {
		t.Fatalf("Tick with toggle off: %v", err)
	}
	if r.failCount != 0 || !r.nextEligible.IsZero() {
		t.Fatalf("toggle-off tick must not touch backoff state, got failCount=%d nextEligible=%v",
			r.failCount, r.nextEligible)
	}
}

// TestBackgroundRunner_Tick_NoSyncReposIsCleanSuccess: a DB with zero
// sync-enabled repos is a clean tick — it resets backoff, never errors.
func TestBackgroundRunner_Tick_NoSyncReposIsCleanSuccess(t *testing.T) {
	s := bgStore(t)
	// A repo with no .bacio/config.yaml on disk: must be skipped, not
	// errored.
	if _, err := s.CreateRepo("MINI", "bacio", t.TempDir(), ""); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	r := NewBackgroundRunner(s, "", "test", nil)
	// Pre-load stale backoff state — a past failure whose backoff
	// window has already elapsed — to prove a clean tick clears it.
	r.failCount = 3
	r.nextEligible = time.Now().Add(-time.Hour)
	if err := r.Tick(context.Background()); err != nil {
		t.Fatalf("Tick over a no-sync DB should be a clean success: %v", err)
	}
	if r.failCount != 0 || !r.nextEligible.IsZero() {
		t.Fatalf("clean tick must reset backoff, got failCount=%d nextEligible=%v",
			r.failCount, r.nextEligible)
	}
}

// TestBackgroundRunner_Tick_OverlapGuard: while a Tick holds the
// in-flight flag, a concurrent Tick is skipped (returns nil) rather
// than stacking.
func TestBackgroundRunner_Tick_OverlapGuard(t *testing.T) {
	s := bgStore(t)
	r := NewBackgroundRunner(s, "", "test", nil)
	// Simulate a tick already in flight by setting the flag directly.
	if !r.inFlight.CompareAndSwap(false, true) {
		t.Fatal("in-flight flag should start clear")
	}
	defer r.inFlight.Store(false)
	// A second Tick must observe the flag and skip with no error.
	if err := r.Tick(context.Background()); err != nil {
		t.Fatalf("overlapping Tick should skip cleanly, got: %v", err)
	}
}

// TestBackgroundRunner_Backoff: recordFailure advances nextEligible
// exponentially; recordSuccess clears it. While inside the backoff
// window, Tick skips.
func TestBackgroundRunner_Backoff(t *testing.T) {
	s := bgStore(t)
	r := NewBackgroundRunner(s, "", "test", nil)

	r.recordFailure()
	first := r.nextEligible
	if first.IsZero() || time.Now().After(first) {
		t.Fatalf("first failure should push nextEligible into the future, got %v", first)
	}
	if r.failCount != 1 {
		t.Fatalf("failCount after one failure = %d, want 1", r.failCount)
	}

	r.recordFailure()
	if r.failCount != 2 {
		t.Fatalf("failCount after two failures = %d, want 2", r.failCount)
	}
	// Second failure waits longer than the first (exponential).
	if !r.nextEligible.After(first) {
		t.Fatalf("second failure should back off further: first=%v second=%v", first, r.nextEligible)
	}

	// Inside the backoff window, Tick is a skip (no error, no state change).
	if err := r.Tick(context.Background()); err != nil {
		t.Fatalf("Tick inside backoff window should skip cleanly: %v", err)
	}
	if r.failCount != 2 {
		t.Fatalf("a backed-off Tick must not touch failCount, got %d", r.failCount)
	}

	r.recordSuccess()
	if r.failCount != 0 || !r.nextEligible.IsZero() {
		t.Fatalf("recordSuccess must clear backoff, got failCount=%d nextEligible=%v",
			r.failCount, r.nextEligible)
	}
}

// TestBackgroundRunner_NilStoreIsNoop: a runner with no store tolerates
// Tick (a defensive contract — the controller's …IfLeader helper relies
// on it).
func TestBackgroundRunner_NilStoreIsNoop(t *testing.T) {
	r := NewBackgroundRunner(nil, "", "test", nil)
	if err := r.Tick(context.Background()); err != nil {
		t.Fatalf("Tick on a nil-store runner should be a no-op: %v", err)
	}
	var nilRunner *BackgroundRunner
	if err := nilRunner.Tick(context.Background()); err != nil {
		t.Fatalf("Tick on a nil runner should be a no-op: %v", err)
	}
	if nilRunner.InProgress() {
		t.Fatal("InProgress on a nil runner should be false")
	}
}
