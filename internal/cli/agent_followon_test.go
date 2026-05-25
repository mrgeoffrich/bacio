package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// setupFollowOnTest stands up an on-disk SQLite store, seeds a repo +
// issue (in todo), and optionally a parent dispatch on the issue.
// Returns the canonical issue key plus the parent dispatch ID (when a
// parent was seeded). Mirrors setupPRCreateTest's wiring of the global
// opts the runner reads — same pattern, narrower fixture.
func setupFollowOnTest(t *testing.T, seedParent bool) (string, int64) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	prevDB := opts.dbPath
	prevDry := opts.dryRun
	opts.dbPath = dbPath
	opts.dryRun = false
	t.Cleanup(func() {
		opts.dbPath = prevDB
		opts.dryRun = prevDry
	})

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	repo, err := s.CreateRepo("MINI", "miniproject", filepath.Join(t.TempDir(), "miniproject"), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "Follow-on me", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	var parentID int64
	if seedParent {
		d, err := s.AddDispatch(store.AddDispatchIn{
			RepoID:        repo.ID,
			IssueID:       &iss.ID,
			Mode:          model.DispatchModePlan,
			Payload:       "parent",
			CreatedBy:     "supervisor",
			InitialStatus: model.DispatchQueued,
		})
		if err != nil {
			t.Fatalf("seed parent dispatch: %v", err)
		}
		parentID = d.ID
	}
	_ = s.Close()
	return iss.Key, parentID
}

// TestAgentQueueFollowOnHappyPath drives the canonical flag-path verb
// end-to-end: queues a follow-on against a known parent, then asserts
// the dormant row + audit op landed.
func TestAgentQueueFollowOnHappyPath(t *testing.T) {
	key, parentID := setupFollowOnTest(t, true)

	if err := runAgentQueueFollowOn(inputs.AgentQueueFollowOnInput{
		IssueKey: key,
		Mode:     string(model.DispatchModeImplement),
	}); err != nil {
		t.Fatalf("runAgentQueueFollowOn: %v", err)
	}

	s, err := store.Open(opts.dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer s.Close()
	repo, _ := s.GetRepoByPrefix("MINI")
	ds, err := s.ListDispatches(store.DispatchFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("ListDispatches: %v", err)
	}
	// One parent + one follow-on.
	if len(ds) != 2 {
		t.Fatalf("want 2 dispatches (parent + follow-on), got %d", len(ds))
	}
	var followOn *model.AgentDispatch
	for _, d := range ds {
		if d.QueuedAfterDispatchID != nil {
			followOn = d
			break
		}
	}
	if followOn == nil {
		t.Fatalf("no follow-on row (queued_after_dispatch_id is unset on every row)")
	}
	if *followOn.QueuedAfterDispatchID != parentID {
		t.Errorf("QueuedAfterDispatchID = %d, want %d", *followOn.QueuedAfterDispatchID, parentID)
	}
	if followOn.Status != model.DispatchQueued {
		t.Errorf("Status = %q, want queued (dormant)", followOn.Status)
	}
	if string(followOn.Mode) != string(model.DispatchModeImplement) {
		t.Errorf("Mode = %q, want %q", followOn.Mode, model.DispatchModeImplement)
	}

	rows, err := s.ListHistory(store.HistoryFilter{Op: "agent.followon.queue"})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 agent.followon.queue audit row, got %d", len(rows))
	}
}

// TestAgentQueueFollowOnRejectsNoParent is the BACI-180 plan's
// "rejects when there's no in-flight dispatch to follow on from" test.
// Without a parent dispatch on the issue, the verb returns a clear
// error and writes no rows.
func TestAgentQueueFollowOnRejectsNoParent(t *testing.T) {
	key, _ := setupFollowOnTest(t, false)

	err := runAgentQueueFollowOn(inputs.AgentQueueFollowOnInput{
		IssueKey: key,
		Mode:     string(model.DispatchModeImplement),
	})
	if err == nil {
		t.Fatal("expected error when there is no parent dispatch; got nil")
	}
	if !strings.Contains(err.Error(), "no active dispatch") {
		t.Fatalf("error should name the missing-parent case, got: %v", err)
	}
}

// TestAgentQueueFollowOnRejectsNoMode: the verb refuses to queue when
// --mode is empty (a follow-on without a stage is meaningless).
func TestAgentQueueFollowOnRejectsNoMode(t *testing.T) {
	key, _ := setupFollowOnTest(t, true)

	err := runAgentQueueFollowOn(inputs.AgentQueueFollowOnInput{
		IssueKey: key,
		Mode:     "",
	})
	if err == nil {
		t.Fatal("expected error when --mode is empty; got nil")
	}
}

// TestAgentQueueFollowOnSingleSlot: a second queue against the same
// issue while a dormant follow-on already exists is rejected with the
// store-boundary "already has a follow-on" error.
func TestAgentQueueFollowOnSingleSlot(t *testing.T) {
	key, _ := setupFollowOnTest(t, true)

	if err := runAgentQueueFollowOn(inputs.AgentQueueFollowOnInput{
		IssueKey: key,
		Mode:     string(model.DispatchModeImplement),
	}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	err := runAgentQueueFollowOn(inputs.AgentQueueFollowOnInput{
		IssueKey: key,
		Mode:     string(model.DispatchModeImplement),
	})
	if err == nil {
		t.Fatal("expected single-slot rejection; got nil")
	}
	if !strings.Contains(err.Error(), "already has a follow-on") {
		t.Fatalf("error should name the single-slot case, got: %v", err)
	}
}

// TestAgentQueueFollowOnDryRun: --dry-run projects the would-be row
// without writing — no DB rows beyond the parent, no audit entry.
func TestAgentQueueFollowOnDryRun(t *testing.T) {
	key, _ := setupFollowOnTest(t, true)
	opts.dryRun = true
	t.Cleanup(func() { opts.dryRun = false })

	if err := runAgentQueueFollowOn(inputs.AgentQueueFollowOnInput{
		IssueKey: key,
		Mode:     string(model.DispatchModeImplement),
	}); err != nil {
		t.Fatalf("dry-run: %v", err)
	}

	s, err := store.Open(opts.dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer s.Close()
	repo, _ := s.GetRepoByPrefix("MINI")
	ds, err := s.ListDispatches(store.DispatchFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("ListDispatches: %v", err)
	}
	if len(ds) != 1 {
		t.Fatalf("dry-run persisted %d row(s), want 1 (the parent only)", len(ds))
	}
}

// TestAgentCancelFollowOnHappyPath: queue a follow-on, then cancel it —
// the row flips to cancelled and the audit log records both ops.
func TestAgentCancelFollowOnHappyPath(t *testing.T) {
	key, _ := setupFollowOnTest(t, true)

	if err := runAgentQueueFollowOn(inputs.AgentQueueFollowOnInput{
		IssueKey: key,
		Mode:     string(model.DispatchModeImplement),
	}); err != nil {
		t.Fatalf("queue: %v", err)
	}
	if err := runAgentCancelFollowOn(inputs.AgentCancelFollowOnInput{IssueKey: key}); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	s, err := store.Open(opts.dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer s.Close()
	rows, err := s.ListHistory(store.HistoryFilter{Op: "agent.followon.cancel"})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 agent.followon.cancel audit row, got %d", len(rows))
	}
}

// TestAgentCancelFollowOnIdempotent is the BACI-180 plan's idempotent
// test: running cancel-followon twice in a row leaves the state
// unchanged after the first call, and writes no extra audit row on
// the second.
func TestAgentCancelFollowOnIdempotent(t *testing.T) {
	key, _ := setupFollowOnTest(t, true)

	if err := runAgentQueueFollowOn(inputs.AgentQueueFollowOnInput{
		IssueKey: key,
		Mode:     string(model.DispatchModeImplement),
	}); err != nil {
		t.Fatalf("queue: %v", err)
	}
	if err := runAgentCancelFollowOn(inputs.AgentCancelFollowOnInput{IssueKey: key}); err != nil {
		t.Fatalf("first cancel: %v", err)
	}
	// Second cancel: idempotent no-op, no error, no extra audit row.
	if err := runAgentCancelFollowOn(inputs.AgentCancelFollowOnInput{IssueKey: key}); err != nil {
		t.Fatalf("second cancel: %v", err)
	}

	s, err := store.Open(opts.dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer s.Close()
	rows, err := s.ListHistory(store.HistoryFilter{Op: "agent.followon.cancel"})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 agent.followon.cancel audit row (second cancel is a no-op), got %d", len(rows))
	}
}

// TestAgentCancelFollowOnNothingToCancel: cancel against an issue with
// no follow-on is an idempotent no-op (no error, no rows touched).
func TestAgentCancelFollowOnNothingToCancel(t *testing.T) {
	key, _ := setupFollowOnTest(t, false)

	if err := runAgentCancelFollowOn(inputs.AgentCancelFollowOnInput{IssueKey: key}); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	s, err := store.Open(opts.dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer s.Close()
	rows, err := s.ListHistory(store.HistoryFilter{})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	for _, r := range rows {
		if r.Op == "agent.followon.cancel" {
			t.Fatalf("idempotent no-op should not write an audit row, found: %+v", r)
		}
	}
}
