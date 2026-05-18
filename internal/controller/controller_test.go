package controller

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/dispatcher"
	"github.com/mrgeoffrich/bacio/internal/leader"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// fakeElectorBackend lets us flip the elector's leader state inside a
// test without spinning up a real store.
type fakeElectorBackend struct {
	mu           sync.Mutex
	acquireOK    bool
	renewOK      bool
	releaseCalls int
}

func (f *fakeElectorBackend) TryAcquireLeader(token, label string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.acquireOK, nil
}
func (f *fakeElectorBackend) RenewLeader(token string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.renewOK, nil
}
func (f *fakeElectorBackend) ReleaseLeader(token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releaseCalls++
	return nil
}
func (f *fakeElectorBackend) CurrentLeader() (store.LeaderInfo, error) {
	return store.LeaderInfo{}, nil
}

func (f *fakeElectorBackend) setLeader(t *testing.T, on bool) {
	t.Helper()
	f.mu.Lock()
	f.acquireOK = on
	f.renewOK = on
	f.mu.Unlock()
}

// elector built against fakeElectorBackend with `on` as its current state.
func newFakeElector(t *testing.T, on bool) (*leader.Elector, *fakeElectorBackend) {
	t.Helper()
	fb := &fakeElectorBackend{acquireOK: on, renewOK: on}
	el := leader.New(fb, "test pid=1")
	el.Tick() // seed cached state
	return el, fb
}

// TestPruneIfLeaderRespectsLease: prune is a no-op when standby, runs
// when leader. The real store is opened in a temp dir to keep the test
// hermetic; the call should succeed against an empty agent_sessions
// table either way.
func TestPruneIfLeaderRespectsLease(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	standby, _ := newFakeElector(t, false)
	PruneIfLeader(s, standby, log)
	if buf.Len() != 0 {
		t.Fatalf("standby prune should be silent, got: %s", buf.String())
	}

	leaderEl, _ := newFakeElector(t, true)
	PruneIfLeader(s, leaderEl, log)
	if strings.Contains(buf.String(), "failed") {
		t.Fatalf("leader prune against empty store should not log a failure, got: %s", buf.String())
	}
}

// TestMatchIfLeaderRespectsLease: matcher is a no-op when standby; when
// leader and the matcher reports no work, no warn is emitted.
func TestMatchIfLeaderRespectsLease(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	m := dispatcher.New(s)
	standby, _ := newFakeElector(t, false)
	MatchIfLeader(m, standby, log)
	if buf.Len() != 0 {
		t.Fatalf("standby match should be silent, got: %s", buf.String())
	}

	leaderEl, _ := newFakeElector(t, true)
	MatchIfLeader(m, leaderEl, log)
	if strings.Contains(buf.String(), "failed") {
		t.Fatalf("leader match against empty store should not log a failure, got: %s", buf.String())
	}
}

// TestNilGuards: every helper tolerates nil inputs without panicking —
// this matches the "background work must never crash the host" contract.
func TestNilGuards(t *testing.T) {
	PruneIfLeader(nil, nil, nil)
	MatchIfLeader(nil, nil, nil)
}

// TestControllerStartStop: Start spins the three goroutines and Stop
// waits for them to exit + releases the lease. We use a tiny store and
// a leader elector and just confirm Stop returns cleanly without a
// deadlock and that Release was called.
func TestControllerStartStop(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	fb := &fakeElectorBackend{acquireOK: true, renewOK: true}
	el := leader.New(fb, "test pid=1")
	c := New(s, el, dispatcher.New(s), nil)

	var emits int
	c.Start(func(leader.State) { emits++ })
	if emits != 1 {
		t.Fatalf("Start should fire heartbeat synchronously once, got %d emits", emits)
	}

	// Give the goroutines a moment so Stop has something to actually stop.
	time.Sleep(50 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		c.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return within 2s — goroutines did not exit")
	}

	fb.mu.Lock()
	calls := fb.releaseCalls
	fb.mu.Unlock()
	if calls != 1 {
		t.Fatalf("Stop should call Release exactly once, got %d", calls)
	}
}
