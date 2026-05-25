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
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	bsync "github.com/mrgeoffrich/bacio/internal/sync"
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
	MatchIfLeader(m, standby, s, log)
	if buf.Len() != 0 {
		t.Fatalf("standby match should be silent, got: %s", buf.String())
	}

	leaderEl, _ := newFakeElector(t, true)
	MatchIfLeader(m, leaderEl, s, log)
	if strings.Contains(buf.String(), "failed") {
		t.Fatalf("leader match against empty store should not log a failure, got: %s", buf.String())
	}
}

// TestSyncIfLeaderRespectsLease: SyncIfLeader is a no-op on standby and
// does not log a failure on the leader when no repo is sync-enabled
// (BACI-89). Mirrors TestMatchIfLeaderRespectsLease.
func TestSyncIfLeaderRespectsLease(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	runner := bsync.NewBackgroundRunner(s, filepath.Join(dir, "db.sqlite"), "test", log)

	standby, _ := newFakeElector(t, false)
	SyncIfLeader(runner, standby, log)
	if buf.Len() != 0 {
		t.Fatalf("standby sync should be silent, got: %s", buf.String())
	}

	leaderEl, _ := newFakeElector(t, true)
	SyncIfLeader(runner, leaderEl, log)
	if strings.Contains(buf.String(), "failed") {
		t.Fatalf("leader sync against an empty store should not log a failure, got: %s", buf.String())
	}
}

// TestMatchIfLeaderWritesBindAudit (BACI-160 gap 1) drives the
// integrated path: a leader-state elector + a queued dispatch + a
// free, channel-connected agent. After one MatchIfLeader call the
// audit log must carry exactly one `agent.bind` row attributed to
// MatcherActor, with the bound dispatch id surfaced in Details so
// `bacio history --op agent.bind` is a useful matcher ledger.
func TestMatchIfLeaderWritesBindAudit(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	repo, err := s.CreateRepo("BIND", "bind-repo", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "matcher target", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	ag, _, err := s.UpsertAgent("brave-otter@claude.test", true)
	if err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	if _, err := s.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: "sess-bind-1", RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
		MarkRegistered: true,
	}); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	// Picker requires channel_seen_at; mark it so the agent is a
	// real candidate.
	if _, err := s.DB.Exec(
		`UPDATE agent_sessions SET channel_seen_at = CURRENT_TIMESTAMP WHERE session_id = ?`,
		"sess-bind-1",
	); err != nil {
		t.Fatalf("mark channel-connected: %v", err)
	}
	queued, err := s.AddDispatch(store.AddDispatchIn{
		RepoID:        repo.ID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchModeImplement,
		Payload:       "do the work",
		CreatedBy:     "supervisor",
		InitialStatus: model.DispatchQueued,
	})
	if err != nil {
		t.Fatalf("add queued dispatch: %v", err)
	}

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	leaderEl, _ := newFakeElector(t, true)
	m := dispatcher.New(s)

	MatchIfLeader(m, leaderEl, s, log)

	// The matcher should have flipped the queued dispatch to pending.
	got, err := s.GetDispatch(queued.ID)
	if err != nil {
		t.Fatalf("get dispatch: %v", err)
	}
	if got.Status != model.DispatchPending {
		t.Fatalf("dispatch status after MatchIfLeader = %q, want pending", got.Status)
	}

	rows, err := s.ListHistory(store.HistoryFilter{Op: "agent.bind"})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("agent.bind rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Actor != model.MatcherActor {
		t.Fatalf("bind row Actor = %q, want %q", row.Actor, model.MatcherActor)
	}
	if row.Kind != "agent" {
		t.Fatalf("bind row Kind = %q, want agent", row.Kind)
	}
	if row.TargetID == nil || *row.TargetID != queued.ID {
		t.Fatalf("bind row TargetID = %v, want %d", row.TargetID, queued.ID)
	}
	if !strings.Contains(row.Details, "agent=brave-otter@claude.test") {
		t.Fatalf("bind row Details = %q, missing agent= clause", row.Details)
	}
	if !strings.Contains(row.Details, "issue="+iss.Key) {
		t.Fatalf("bind row Details = %q, missing issue= clause", row.Details)
	}
}

// TestArchiveSweepIfLeaderWritesAudit (BACI-160 gap 2) seeds an
// archivable issue (terminal state + updated_at past the age window),
// then asserts the leader-driven sweep emits one `archive.sweep`
// audit row with the per-pass counts. A standby pass on a fresh
// store stays silent so the test also confirms the no-op/no-audit
// contract.
func TestArchiveSweepIfLeaderWritesAudit(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	repo, err := s.CreateRepo("SWEP", "sweep-repo", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "done long ago", "", model.StateDone, nil)
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	// Back-date terminal_at past the configurable retention window
	// (BACI-162 made it configurable; default is 7 days) so the
	// terminal-issue pass picks the row up. The store seeds terminal_at
	// on insert when the issue starts in a terminal state, but the
	// retention clock runs against `terminal_at` post-BACI-162 — and
	// `datetime('now','-30 days')` is comfortably past any sane
	// retention default.
	if _, err := s.DB.Exec(
		`UPDATE issues SET terminal_at = datetime('now','-30 days') WHERE id = ?`,
		iss.ID,
	); err != nil {
		t.Fatalf("backdate issue: %v", err)
	}

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Standby: no sweep, no audit row.
	standby, _ := newFakeElector(t, false)
	ArchiveSweepIfLeader(s, standby, log)
	standbyRows, err := s.ListHistory(store.HistoryFilter{Op: "archive.sweep"})
	if err != nil {
		t.Fatalf("ListHistory standby: %v", err)
	}
	if len(standbyRows) != 0 {
		t.Fatalf("standby ArchiveSweepIfLeader wrote %d archive.sweep rows, want 0", len(standbyRows))
	}

	// Leader: sweep runs, one summary audit row lands.
	leaderEl, _ := newFakeElector(t, true)
	ArchiveSweepIfLeader(s, leaderEl, log)

	rows, err := s.ListHistory(store.HistoryFilter{Op: "archive.sweep"})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("archive.sweep rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Actor != model.ControllerActor {
		t.Fatalf("sweep row Actor = %q, want %q", row.Actor, model.ControllerActor)
	}
	if row.Kind != "sweep" {
		t.Fatalf("sweep row Kind = %q, want sweep", row.Kind)
	}
	if !strings.Contains(row.Details, `"issues":1`) {
		t.Fatalf("sweep row Details = %q, missing issues:1", row.Details)
	}
}

// TestNilGuards: every helper tolerates nil inputs without panicking —
// this matches the "background work must never crash the host" contract.
func TestNilGuards(t *testing.T) {
	PruneIfLeader(nil, nil, nil)
	MatchIfLeader(nil, nil, nil, nil)
	PingIfLeader(nil, nil, nil)
	ArchiveSweepIfLeader(nil, nil, nil)
	SyncIfLeader(nil, nil, nil)
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
	// pinger left nil — TestControllerStartStop only verifies Start/Stop
	// semantics, not the loop's behaviour. The BACI-57 pinger goroutine
	// becomes a quiet no-op via PingIfLeader's nil guard.
	c := New(s, el, dispatcher.New(s), nil, nil, nil)

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
