package store

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestTryAcquireLeaderFromSeed locks in that a fresh DB seeds ui_leader with
// heartbeat_at = 0 — which sorts below any datetime('now') string in SQLite —
// so the very first ACQUIRE succeeds.
func TestTryAcquireLeaderFromSeed(t *testing.T) {
	s := newTestStore(t)
	ok, err := s.TryAcquireLeader("tok-a", "tui pid=1")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if !ok {
		t.Fatal("first acquire on a fresh DB should succeed")
	}
}

// TestTryAcquireLeaderRejectsFreshLease locks in that a competing ACQUIRE
// fails while the current holder's heartbeat is fresh — the lease isn't stale.
func TestTryAcquireLeaderRejectsFreshLease(t *testing.T) {
	s := newTestStore(t)
	if ok, err := s.TryAcquireLeader("tok-a", "a"); err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}
	ok, err := s.TryAcquireLeader("tok-b", "b")
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if ok {
		t.Fatal("acquire against a fresh lease should fail")
	}
}

// acquireWithRetry calls TryAcquireLeader, retrying on SQLITE_BUSY. Multiple
// goroutines sharing one *sql.DB contend for the single WAL writer slot, and
// without a busy_timeout pragma the loser gets SQLITE_BUSY rather than
// blocking — retrying gives every racer a definitive true/false verdict so the
// "exactly one winner" invariant can be asserted deterministically.
func acquireWithRetry(t *testing.T, s *Store, token, label string) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ok, err := s.TryAcquireLeader(token, label)
		if err == nil {
			return ok
		}
		if !strings.Contains(err.Error(), "locked") && !strings.Contains(err.Error(), "BUSY") {
			t.Fatalf("acquire: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("acquire: still SQLITE_BUSY after 5s: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestTryAcquireLeaderSingleWinner is the CAS race: many goroutines racing
// ACQUIRE from the seed state. SQLite serialises the writes, so exactly one
// wins.
func TestTryAcquireLeaderSingleWinner(t *testing.T) {
	s := newTestStore(t)
	const racers = 12
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func(i int) {
			defer wg.Done()
			if acquireWithRetry(t, s, fmt.Sprintf("tok-%d", i), "racer") {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("expected exactly 1 winner, got %d", wins)
	}
}

// TestRenewLeader locks in the token guard: the holder can renew, a
// non-holder token cannot.
func TestRenewLeader(t *testing.T) {
	s := newTestStore(t)
	if ok, err := s.TryAcquireLeader("tok-a", "a"); err != nil || !ok {
		t.Fatalf("acquire: ok=%v err=%v", ok, err)
	}
	if ok, err := s.RenewLeader("tok-a"); err != nil || !ok {
		t.Fatalf("holder renew should succeed: ok=%v err=%v", ok, err)
	}
	if ok, err := s.RenewLeader("tok-b"); err != nil || ok {
		t.Fatalf("non-holder renew should fail: ok=%v err=%v", ok, err)
	}
}

// TestRenewLeaderAfterTakeover locks in that once another process takes the
// lease, the previous holder's RENEW returns false so it demotes.
func TestRenewLeaderAfterTakeover(t *testing.T) {
	s := newTestStore(t)
	if ok, _ := s.TryAcquireLeader("tok-a", "a"); !ok {
		t.Fatal("a should acquire")
	}
	// a releases (heartbeat_at = 0), b takes over.
	if err := s.ReleaseLeader("tok-a"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if ok, _ := s.TryAcquireLeader("tok-b", "b"); !ok {
		t.Fatal("b should acquire after release")
	}
	if ok, err := s.RenewLeader("tok-a"); err != nil || ok {
		t.Fatalf("a's renew after takeover should fail: ok=%v err=%v", ok, err)
	}
}

// TestReleaseLeaderMakesAcquirable locks in that a graceful release zeroes
// heartbeat_at so a challenger can ACQUIRE immediately instead of waiting out
// the stale window.
func TestReleaseLeaderMakesAcquirable(t *testing.T) {
	s := newTestStore(t)
	if ok, _ := s.TryAcquireLeader("tok-a", "a"); !ok {
		t.Fatal("a should acquire")
	}
	if ok, _ := s.TryAcquireLeader("tok-b", "b"); ok {
		t.Fatal("b should not acquire a fresh lease")
	}
	if err := s.ReleaseLeader("tok-a"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if ok, err := s.TryAcquireLeader("tok-b", "b"); err != nil || !ok {
		t.Fatalf("b should acquire after release: ok=%v err=%v", ok, err)
	}
}

// TestReleaseLeaderByNonHolderIsNoop locks in that ReleaseLeader is
// token-guarded — a non-holder release must not free the lease.
func TestReleaseLeaderByNonHolderIsNoop(t *testing.T) {
	s := newTestStore(t)
	if ok, _ := s.TryAcquireLeader("tok-a", "a"); !ok {
		t.Fatal("a should acquire")
	}
	if err := s.ReleaseLeader("tok-b"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if ok, _ := s.TryAcquireLeader("tok-b", "b"); ok {
		t.Fatal("non-holder release must not free the lease")
	}
}

// TestCurrentLeader locks in that CurrentLeader reflects the current holder's
// token and label, with AcquiredAt set after an acquire.
func TestCurrentLeader(t *testing.T) {
	s := newTestStore(t)
	if ok, _ := s.TryAcquireLeader("tok-a", "tui pid=42"); !ok {
		t.Fatal("acquire")
	}
	info, err := s.CurrentLeader()
	if err != nil {
		t.Fatalf("current leader: %v", err)
	}
	if info.Token != "tok-a" || info.Label != "tui pid=42" {
		t.Fatalf("unexpected leader info: %+v", info)
	}
	if info.AcquiredAt.IsZero() {
		t.Fatal("AcquiredAt should be set after an acquire")
	}
}
