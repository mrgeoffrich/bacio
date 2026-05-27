package controller

import (
	"bytes"
	"fmt"
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
	iss, err := s.CreateIssue(repo.ID, nil, "matcher target", "", model.StateTodo, nil, "")
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
	iss, err := s.CreateIssue(repo.ID, nil, "done long ago", "", model.StateDone, nil, "")
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

// TestArchiveSweepIfLeaderWritesFeatureAutoStateAudit (BACI-199)
// seeds two features whose children's terminal states drive the
// auto-completion pass: one with two `done` children (should promote
// to `done`) and one with two `cancelled` children (should promote
// to `cancelled`). The leader-driven sweep emits one
// `feature.auto-state` row carrying `{"done":1,"cancelled":1}` in
// Details, alongside the existing `archive.sweep` summary row whose
// payload also carries `"features_auto_stated":2`.
func TestArchiveSweepIfLeaderWritesFeatureAutoStateAudit(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	repo, err := s.CreateRepo("AS", "as", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	// Feature D: two done children → expect promotion to done.
	featD, _ := s.CreateFeature(repo.ID, "feat-d", "Feat D", "", "", "")
	if _, err := s.CreateIssue(repo.ID, &featD.ID, "d1", "", model.StateDone, nil, ""); err != nil {
		t.Fatalf("create d1: %v", err)
	}
	if _, err := s.CreateIssue(repo.ID, &featD.ID, "d2", "", model.StateDone, nil, ""); err != nil {
		t.Fatalf("create d2: %v", err)
	}
	// Feature C: two cancelled children → expect promotion to cancelled.
	featC, _ := s.CreateFeature(repo.ID, "feat-c", "Feat C", "", "", "")
	if _, err := s.CreateIssue(repo.ID, &featC.ID, "c1", "", model.StateCancelled, nil, ""); err != nil {
		t.Fatalf("create c1: %v", err)
	}
	if _, err := s.CreateIssue(repo.ID, &featC.ID, "c2", "", model.StateCancelled, nil, ""); err != nil {
		t.Fatalf("create c2: %v", err)
	}

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	leaderEl, _ := newFakeElector(t, true)
	ArchiveSweepIfLeader(s, leaderEl, log)

	// One feature.auto-state row, Details = {"done":1,"cancelled":1}.
	stateRows, err := s.ListHistory(store.HistoryFilter{Op: "feature.auto-state"})
	if err != nil {
		t.Fatalf("ListHistory feature.auto-state: %v", err)
	}
	if len(stateRows) != 1 {
		t.Fatalf("feature.auto-state rows = %d, want 1", len(stateRows))
	}
	row := stateRows[0]
	if row.Actor != model.ControllerActor {
		t.Fatalf("feature.auto-state Actor = %q, want %q", row.Actor, model.ControllerActor)
	}
	if row.Kind != "sweep" {
		t.Fatalf("feature.auto-state Kind = %q, want sweep", row.Kind)
	}
	if !strings.Contains(row.Details, `"done":1`) || !strings.Contains(row.Details, `"cancelled":1`) {
		t.Fatalf("feature.auto-state Details = %q, want done:1 + cancelled:1", row.Details)
	}

	// archive.sweep summary row carries features_auto_stated:2.
	sumRows, err := s.ListHistory(store.HistoryFilter{Op: "archive.sweep"})
	if err != nil {
		t.Fatalf("ListHistory archive.sweep: %v", err)
	}
	if len(sumRows) != 1 {
		t.Fatalf("archive.sweep rows = %d, want 1", len(sumRows))
	}
	if !strings.Contains(sumRows[0].Details, `"features_auto_stated":2`) {
		t.Fatalf("archive.sweep Details missing features_auto_stated:2: %q", sumRows[0].Details)
	}

	// And the actual feature rows landed at the expected states.
	gotD, _ := s.GetFeatureByID(featD.ID)
	if gotD.State != model.FeatureStateDone {
		t.Fatalf("feat-d state = %q, want done", gotD.State)
	}
	gotC, _ := s.GetFeatureByID(featC.ID)
	if gotC.State != model.FeatureStateCancelled {
		t.Fatalf("feat-c state = %q, want cancelled", gotC.State)
	}
}

// TestArchiveSweepIfLeaderSkipsFeatureAutoStateAuditWhenZero confirms
// the silent-when-zero contract: a leader-driven sweep that didn't
// promote any features writes ZERO `feature.auto-state` rows (no
// noise on a quiet DB).
func TestArchiveSweepIfLeaderSkipsFeatureAutoStateAuditWhenZero(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	// Need at least one archivable issue so the sweep writes its
	// summary row — otherwise it bails before the new sibling row
	// could possibly fire either way and the test is vacuous.
	repo, _ := s.CreateRepo("AS", "as", t.TempDir(), "")
	iss, _ := s.CreateIssue(repo.ID, nil, "i", "", model.StateDone, nil, "")
	if _, err := s.DB.Exec(
		`UPDATE issues SET terminal_at = datetime('now','-30 days') WHERE id = ?`,
		iss.ID,
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	leaderEl, _ := newFakeElector(t, true)
	ArchiveSweepIfLeader(s, leaderEl, log)

	rows, err := s.ListHistory(store.HistoryFilter{Op: "feature.auto-state"})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("feature.auto-state rows = %d on a sweep that didn't promote any features, want 0", len(rows))
	}
}

// TestControllerStartRunsArchiveSweepOnStartup (BACI-175) pins the
// sweep-on-startup pass. A short-lived process that exits before the
// ArchiveSweepInterval ticker fires must still archive overdue rows on
// the way in. Seeds one done issue with a back-dated terminal_at,
// starts the Controller with a leader-true elector, then asserts one
// `archive.sweep` audit row landed before Stop. Mirrors
// TestArchiveSweepIfLeaderWritesAudit's setup; the only difference is
// who triggers the sweep (Start's synchronous startup pass vs a direct
// ArchiveSweepIfLeader call).
func TestControllerStartRunsArchiveSweepOnStartup(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	repo, err := s.CreateRepo("STRT", "startup-repo", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "done long ago", "", model.StateDone, nil, "")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	if _, err := s.DB.Exec(
		`UPDATE issues SET terminal_at = datetime('now','-30 days') WHERE id = ?`,
		iss.ID,
	); err != nil {
		t.Fatalf("backdate issue: %v", err)
	}

	fb := &fakeElectorBackend{acquireOK: true, renewOK: true}
	el := leader.New(fb, "test pid=1")
	c := New(s, el, nil, nil, nil, nil)

	c.Start(nil)

	// Sweep runs synchronously inside Start (after the initial
	// heartbeat, before goroutines spin), so the audit row must be
	// present before Stop returns — no sleep needed.
	rows, err := s.ListHistory(store.HistoryFilter{Op: "archive.sweep"})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("archive.sweep rows after Start = %d, want 1", len(rows))
	}
	if rows[0].Actor != model.ControllerActor {
		t.Fatalf("startup sweep Actor = %q, want %q", rows[0].Actor, model.ControllerActor)
	}
	if !strings.Contains(rows[0].Details, `"issues":1`) {
		t.Fatalf("startup sweep Details = %q, missing issues:1", rows[0].Details)
	}

	// Eligibility cleared — the issue is now archived.
	got, err := s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if got.ArchivedAt == nil {
		t.Fatal("startup sweep must stamp archived_at on the eligible issue")
	}

	c.Stop()
}

// TestControllerStartStartupSweepStandbyNoop (BACI-175) verifies the
// sweep-on-startup pass respects the leader gate: a standby Controller
// must NOT sweep on Start, even when there are overdue rows. Otherwise
// every standby UI would race to archive on launch.
func TestControllerStartStartupSweepStandbyNoop(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	repo, err := s.CreateRepo("STBY", "standby-repo", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "done long ago", "", model.StateDone, nil, "")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	if _, err := s.DB.Exec(
		`UPDATE issues SET terminal_at = datetime('now','-30 days') WHERE id = ?`,
		iss.ID,
	); err != nil {
		t.Fatalf("backdate issue: %v", err)
	}

	fb := &fakeElectorBackend{acquireOK: false, renewOK: false}
	el := leader.New(fb, "test pid=1")
	c := New(s, el, nil, nil, nil, nil)

	c.Start(nil)

	rows, err := s.ListHistory(store.HistoryFilter{Op: "archive.sweep"})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("standby Start wrote %d archive.sweep rows, want 0", len(rows))
	}
	got, err := s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if got.ArchivedAt != nil {
		t.Fatal("standby Start must NOT archive — leader gate violated")
	}

	c.Stop()
}

// TestFollowOnSweepIfLeaderRespectsLease (BACI-179) mirrors the other
// …IfLeader lease tests: standby is silent, leader runs against an
// empty store without warning. The combined helper is one tick that
// runs orphan-cancel then promote; an empty store has nothing to
// touch on either pass.
func TestFollowOnSweepIfLeaderRespectsLease(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	standby, _ := newFakeElector(t, false)
	FollowOnSweepIfLeader(s, standby, log)
	if buf.Len() != 0 {
		t.Fatalf("standby follow-on sweep should be silent, got: %s", buf.String())
	}

	leaderEl, _ := newFakeElector(t, true)
	FollowOnSweepIfLeader(s, leaderEl, log)
	if strings.Contains(buf.String(), "failed") {
		t.Fatalf("leader follow-on sweep against empty store should not log a failure, got: %s", buf.String())
	}
}

// TestFollowOnSweepIfLeader_LeaderWrites (BACI-179) drives the
// integrated promote path: a pending parent + a dormant follow-on +
// ack the parent + run the sweep as leader. One
// `agent.followon.promote` audit row must land, attributed to
// ControllerActor, with the issue key surfaced in TargetLabel and
// the dispatch id in Details. Mirrors TestMatchIfLeaderWritesBindAudit's
// shape so the two sweep-audit paths read consistently.
func TestFollowOnSweepIfLeader_LeaderWrites(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	repo, err := s.CreateRepo("FLOW", "followon-repo", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "promote me", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	ag, _, err := s.UpsertAgent("brave-otter@claude.test", true)
	if err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	parent, err := s.AddDispatch(store.AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, IssueID: &iss.ID,
		Mode: model.DispatchModePlan, Payload: "plan", CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	follow, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeImplement, "supervisor")
	if err != nil {
		t.Fatalf("add follow-on: %v", err)
	}
	if _, err := s.AckDispatch(parent.ID, ""); err != nil {
		t.Fatalf("ack parent: %v", err)
	}

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	leaderEl, _ := newFakeElector(t, true)

	FollowOnSweepIfLeader(s, leaderEl, log)

	// The promote sweep should have cleared queued_after_dispatch_id.
	got, err := s.GetDispatch(follow.ID)
	if err != nil {
		t.Fatalf("get follow: %v", err)
	}
	if got.QueuedAfterDispatchID != nil {
		t.Fatalf("post-sweep queued_after_dispatch_id = %v, want nil", got.QueuedAfterDispatchID)
	}

	rows, err := s.ListHistory(store.HistoryFilter{Op: "agent.followon.promote"})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("agent.followon.promote rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Actor != model.ControllerActor {
		t.Fatalf("promote row Actor = %q, want %q", row.Actor, model.ControllerActor)
	}
	if row.Kind != "agent" {
		t.Fatalf("promote row Kind = %q, want agent", row.Kind)
	}
	if row.TargetID == nil || *row.TargetID != follow.ID {
		t.Fatalf("promote row TargetID = %v, want %d", row.TargetID, follow.ID)
	}
	if row.TargetLabel != iss.Key {
		t.Fatalf("promote row TargetLabel = %q, want %q", row.TargetLabel, iss.Key)
	}
	if !strings.Contains(row.Details, "issue="+iss.Key) {
		t.Fatalf("promote row Details = %q, missing issue= clause", row.Details)
	}
	if !strings.Contains(row.Details, "mode=implement") {
		t.Fatalf("promote row Details = %q, missing mode=implement clause", row.Details)
	}
}

// TestFollowOnSweepIfLeader_OrphanCancelWrites (BACI-179) — the orphan
// path: dormant follow-on + issue lands in done before the
// predecessor settles. The sweep cancels the row and emits one
// `agent.followon.cancel` row attributed to ControllerActor.
func TestFollowOnSweepIfLeader_OrphanCancelWrites(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	repo, err := s.CreateRepo("ORPH", "orphan-repo", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "cancel me", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	ag, _, err := s.UpsertAgent("clever-fox@claude.test", true)
	if err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	parent, err := s.AddDispatch(store.AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, IssueID: &iss.ID,
		Mode: model.DispatchModePlan, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	follow, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeImplement, "supervisor")
	if err != nil {
		t.Fatalf("add follow-on: %v", err)
	}
	if err := s.SetIssueState(iss.ID, model.StateDone); err != nil {
		t.Fatalf("set issue done: %v", err)
	}

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	leaderEl, _ := newFakeElector(t, true)

	FollowOnSweepIfLeader(s, leaderEl, log)

	got, err := s.GetDispatch(follow.ID)
	if err != nil {
		t.Fatalf("get follow: %v", err)
	}
	if got.Status != model.DispatchCancelled {
		t.Fatalf("follow status post-sweep = %q, want cancelled", got.Status)
	}

	rows, err := s.ListHistory(store.HistoryFilter{Op: "agent.followon.cancel"})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("agent.followon.cancel rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Actor != model.ControllerActor {
		t.Fatalf("orphan-cancel row Actor = %q, want %q", row.Actor, model.ControllerActor)
	}
	if row.TargetID == nil || *row.TargetID != follow.ID {
		t.Fatalf("orphan-cancel row TargetID = %v, want %d", row.TargetID, follow.ID)
	}
}

// TestFollowOnSweepIfLeader_PromotesRegardlessOfState (BACI-252) —
// the BACI-195 fire-time state-gate is gone. A dormant follow-on
// whose post-release issue state would have failed the old gate now
// promotes cleanly, with an `agent.followon.promote` audit row and
// no `agent.followon.gate_fail` row in sight. Locks in that the
// gate-fail path is fully retired (no audit op, no status flip).
func TestFollowOnSweepIfLeader_PromotesRegardlessOfState(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	repo, err := s.CreateRepo("GATE", "gate-fail-repo", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	// in_review issue + an implement follow-on — pre-BACI-252 the
	// implement default gate (`todo`) would have failed here. After
	// BACI-252 the row promotes.
	iss, err := s.CreateIssue(repo.ID, nil, "any-state at fire", "", model.StateInReview, nil, "")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	ag, _, err := s.UpsertAgent("eager-eel@claude.test", true)
	if err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	parent, err := s.AddDispatch(store.AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, IssueID: &iss.ID,
		Mode: model.DispatchModeReview, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	follow, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeImplement, "supervisor")
	if err != nil {
		t.Fatalf("add follow-on: %v", err)
	}
	if _, err := s.AckDispatch(parent.ID, ""); err != nil {
		t.Fatalf("ack parent: %v", err)
	}

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	leaderEl, _ := newFakeElector(t, true)

	FollowOnSweepIfLeader(s, leaderEl, log)

	got, err := s.GetDispatch(follow.ID)
	if err != nil {
		t.Fatalf("get follow: %v", err)
	}
	if got.Status != model.DispatchQueued {
		t.Fatalf("follow status post-sweep = %q, want queued (BACI-252: no fire-time gate)", got.Status)
	}
	if got.QueuedAfterDispatchID != nil {
		t.Fatalf("promoted row still carries queued_after_dispatch_id = %v", got.QueuedAfterDispatchID)
	}

	// A promote audit row was written, attributed to the controller.
	promoteRows, err := s.ListHistory(store.HistoryFilter{Op: "agent.followon.promote"})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(promoteRows) != 1 {
		t.Fatalf("agent.followon.promote rows = %d, want 1", len(promoteRows))
	}
	if promoteRows[0].Actor != model.ControllerActor {
		t.Fatalf("promote row Actor = %q, want %q", promoteRows[0].Actor, model.ControllerActor)
	}

	// The retired gate-fail op writes nothing now — confirm zero rows.
	gateRows, err := s.ListHistory(store.HistoryFilter{Op: "agent.followon.gate_fail"})
	if err != nil {
		t.Fatalf("ListHistory (gate_fail): %v", err)
	}
	if len(gateRows) != 0 {
		t.Fatalf("agent.followon.gate_fail rows = %d, want 0 (op is retired)", len(gateRows))
	}
}

// TestFollowOnSweepIfLeader_BlockersClearPromoteCarriesSnapshot
// (BACI-246) — when the promote sweep clears a blockers-clear
// follow-on, the resulting `agent.followon.promote` audit row must
// stamp `gate=blockers` plus a `blockers=[KEY:state,...]` clause
// naming each blocker the gate observed. This is the diagnostic
// surface that closes the "user saw a follow-on fire while the
// blocker was non-terminal" gap — `bacio history --op
// agent.followon.promote -o json` carries the answer.
func TestFollowOnSweepIfLeader_BlockersClearPromoteCarriesSnapshot(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	repo, err := s.CreateRepo("BLOK", "blockers-clear-repo", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	blocked, err := s.CreateIssue(repo.ID, nil, "blocked-side", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create blocked: %v", err)
	}
	blocker, err := s.CreateIssue(repo.ID, nil, "blocker-side", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create blocker: %v", err)
	}
	if err := s.CreateRelation(blocker.ID, blocked.ID, model.RelBlocks); err != nil {
		t.Fatalf("create blocks edge: %v", err)
	}
	follow, err := s.AddBlockerFollowOnDispatch(repo.ID, blocked.ID, model.DispatchModePlan, "supervisor")
	if err != nil {
		t.Fatalf("AddBlockerFollowOnDispatch: %v", err)
	}
	// Close the blocker so the gate clears at promote time.
	if err := s.SetIssueState(blocker.ID, model.StateDone); err != nil {
		t.Fatalf("close blocker: %v", err)
	}

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	leaderEl, _ := newFakeElector(t, true)

	FollowOnSweepIfLeader(s, leaderEl, log)

	rows, err := s.ListHistory(store.HistoryFilter{Op: "agent.followon.promote"})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	var row *model.HistoryEntry
	for i := range rows {
		if rows[i].TargetID != nil && *rows[i].TargetID == follow.ID {
			row = rows[i]
			break
		}
	}
	if row == nil {
		t.Fatalf("no agent.followon.promote row for dispatch %d", follow.ID)
	}
	if !strings.Contains(row.Details, "gate=blockers") {
		t.Fatalf("promote row Details = %q, missing gate=blockers (the BACI-246 enrichment)", row.Details)
	}
	wantClause := fmt.Sprintf("blockers=[%s:done]", blocker.Key)
	if !strings.Contains(row.Details, wantClause) {
		t.Fatalf("promote row Details = %q, missing %q", row.Details, wantClause)
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
	FollowOnSweepIfLeader(nil, nil, nil)
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
