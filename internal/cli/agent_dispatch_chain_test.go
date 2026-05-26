package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// setupDispatchChainTest stands up an on-disk store seeded with a repo
// and one todo-state issue — the canonical shape the kanban's compound
// picker exercises (a fresh card with no parent dispatch in flight).
// Mirrors setupFollowOnTest's wiring of opts.dbPath / opts.dryRun so
// the global cli runners see the right environment.
func setupDispatchChainTest(t *testing.T) string {
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
	iss, err := s.CreateIssue(repo.ID, nil, "Chain me", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	_ = s.Close()
	return iss.Key
}

// TestAgentDispatchChainHappyPath (BACI-209) — the canonical kanban
// flow: pick "Plan, then Implement" on a todo card. Both rows land in
// one transaction, the follow-on is dormant and linked to the parent,
// and the audit log records both ops.
func TestAgentDispatchChainHappyPath(t *testing.T) {
	key := setupDispatchChainTest(t)

	if err := runAgentDispatchChain(inputs.AgentDispatchChainInput{
		IssueKey:     key,
		Mode:         string(model.DispatchModePlan),
		FollowOnMode: string(model.DispatchModeImplement),
	}); err != nil {
		t.Fatalf("runAgentDispatchChain: %v", err)
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
	if len(ds) != 2 {
		t.Fatalf("want 2 dispatches (parent + follow-on), got %d", len(ds))
	}
	var parent, follow *model.AgentDispatch
	for _, d := range ds {
		if d.QueuedAfterDispatchID != nil {
			follow = d
		} else {
			parent = d
		}
	}
	if parent == nil || follow == nil {
		t.Fatalf("missing rows: parent=%v follow=%v", parent, follow)
	}
	if parent.Status != model.DispatchQueued {
		t.Errorf("parent status = %q, want queued", parent.Status)
	}
	if string(parent.Mode) != string(model.DispatchModePlan) {
		t.Errorf("parent mode = %q, want %q", parent.Mode, model.DispatchModePlan)
	}
	if *follow.QueuedAfterDispatchID != parent.ID {
		t.Errorf("follow.QueuedAfterDispatchID = %d, want %d", *follow.QueuedAfterDispatchID, parent.ID)
	}
	if string(follow.Mode) != string(model.DispatchModeImplement) {
		t.Errorf("follow mode = %q, want %q", follow.Mode, model.DispatchModeImplement)
	}
	// Both audit rows present.
	qRows, err := s.ListHistory(store.HistoryFilter{Op: "agent.queue"})
	if err != nil {
		t.Fatalf("history queue: %v", err)
	}
	if len(qRows) != 1 {
		t.Errorf("want 1 agent.queue audit row, got %d", len(qRows))
	}
	fRows, err := s.ListHistory(store.HistoryFilter{Op: "agent.followon.queue"})
	if err != nil {
		t.Fatalf("history followon.queue: %v", err)
	}
	if len(fRows) != 1 {
		t.Errorf("want 1 agent.followon.queue audit row, got %d", len(fRows))
	}
}

// TestAgentDispatchChainGateMissPrimary (BACI-209) — the primary's
// state-gate is re-checked; calling with a primary mode whose gate
// doesn't admit the issue's state rejects the call before any insert.
func TestAgentDispatchChainGateMissPrimary(t *testing.T) {
	key := setupDispatchChainTest(t)
	// Move the issue out of todo so `plan`'s gate (which admits todo
	// only by default) rejects. in_review is a stage no built-in mode's
	// default gate admits.
	s, err := store.Open(opts.dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	prefix, num := splitIssueKey(t, key)
	iss, err := s.GetIssueByKey(prefix, num)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if err := s.SetIssueState(iss.ID, model.StateInReview); err != nil {
		t.Fatalf("set in_review: %v", err)
	}
	_ = s.Close()

	err = runAgentDispatchChain(inputs.AgentDispatchChainInput{
		IssueKey:     key,
		Mode:         string(model.DispatchModePlan),
		FollowOnMode: string(model.DispatchModeImplement),
	})
	if err == nil {
		t.Fatal("expected state-gate rejection on primary, got nil")
	}
	if !strings.Contains(err.Error(), "can't run from") {
		t.Fatalf("error should name the gate-miss, got: %v", err)
	}
	// No rows landed.
	s, err = store.Open(opts.dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer s.Close()
	repo, _ := s.GetRepoByPrefix("MINI")
	ds, err := s.ListDispatches(store.DispatchFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ds) != 0 {
		t.Fatalf("rejected chain landed %d row(s), want 0", len(ds))
	}
}

// TestAgentDispatchChainSameMode (BACI-209) — chaining the same mode
// (e.g. Plan then Plan) is meaningless and rejected at the client
// boundary so a stale UI / CLI caller surfaces a clear error.
func TestAgentDispatchChainSameMode(t *testing.T) {
	key := setupDispatchChainTest(t)

	err := runAgentDispatchChain(inputs.AgentDispatchChainInput{
		IssueKey:     key,
		Mode:         string(model.DispatchModePlan),
		FollowOnMode: string(model.DispatchModePlan),
	})
	if err == nil {
		t.Fatal("expected same-mode rejection, got nil")
	}
	if !strings.Contains(err.Error(), "matches the primary mode") {
		t.Fatalf("error should name the same-mode case, got: %v", err)
	}
}

// TestAgentDispatchChainDryRun (BACI-209) — dry-run projects without
// writing. No DB rows, no audit entries.
func TestAgentDispatchChainDryRun(t *testing.T) {
	key := setupDispatchChainTest(t)
	opts.dryRun = true
	t.Cleanup(func() { opts.dryRun = false })

	if err := runAgentDispatchChain(inputs.AgentDispatchChainInput{
		IssueKey:     key,
		Mode:         string(model.DispatchModePlan),
		FollowOnMode: string(model.DispatchModeImplement),
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
		t.Fatalf("list: %v", err)
	}
	if len(ds) != 0 {
		t.Fatalf("dry-run persisted %d row(s), want 0", len(ds))
	}
}

// TestAgentDispatchChainMissingFollowOnMode (BACI-209) — the chain
// verb requires both modes. An empty --then is rejected before any
// insert (a primary-only call goes through `bacio agent dispatch`).
func TestAgentDispatchChainMissingFollowOnMode(t *testing.T) {
	key := setupDispatchChainTest(t)

	err := runAgentDispatchChain(inputs.AgentDispatchChainInput{
		IssueKey: key,
		Mode:     string(model.DispatchModePlan),
	})
	if err == nil {
		t.Fatal("expected missing-follow-on rejection, got nil")
	}
}
