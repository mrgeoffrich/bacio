package client_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestAutoDispatchIssueLocal covers the happy path end-to-end against
// the local backend: state-gate accepts and the dispatch is enqueued
// (BACI-51) for the matcher to bind later. The presence of a free
// agent no longer changes the call's result — that decision moved to
// the background matcher.
func TestAutoDispatchIssueLocal(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "auto-dispatch me", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	d, err := p.local.AutoDispatchIssue(ctx, p.repo, iss.Key, "implement", false)
	if err != nil {
		t.Fatalf("AutoDispatchIssue: %v", err)
	}
	if d.TargetAgentID != nil || d.TargetAgentName != "" {
		t.Fatalf("queued dispatch should have no target, got agent_id=%v name=%q", d.TargetAgentID, d.TargetAgentName)
	}
	if d.IssueKey != iss.Key {
		t.Fatalf("dispatch issue = %q, want %q", d.IssueKey, iss.Key)
	}
	if string(d.Mode) != "implement" {
		t.Fatalf("dispatch mode = %q, want implement", d.Mode)
	}
	if d.Status != model.DispatchQueued {
		t.Fatalf("dispatch status = %q, want queued", d.Status)
	}
	// The audit op for the enqueue path is agent.queue (distinct from
	// targeted dispatch's agent.dispatch) so history readers can tell
	// the two apart.
	hist, err := p.local.ListHistory(ctx, p.repo, store.HistoryFilter{Limit: 10, Op: "agent.queue"})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("agent.queue history rows: got %d, want 1", len(hist))
	}
}

// TestAutoDispatchIssueAcceptsAnyMode (BACI-252 regression) — the
// per-template state-gate is gone, so a mode that used to be rejected
// because the template's gate didn't admit the issue's current state
// now succeeds. "review" from a todo issue is the canonical case the
// old gate refused.
func TestAutoDispatchIssueAcceptsAnyMode(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "any-state ok", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	d, err := p.local.AutoDispatchIssue(ctx, p.repo, iss.Key, "review", false)
	if err != nil {
		t.Fatalf("AutoDispatchIssue (review from todo) = %v, want nil (BACI-252: no state-gate)", err)
	}
	if d == nil || d.IssueKey != iss.Key {
		t.Fatalf("dispatch shape after queue: %+v", d)
	}
}

// TestAutoDispatchIssueArchivedRejected — BACI-68 dispatcher guard.
// Archiving an issue removes it from the board; the dispatch entry
// points must refuse to enqueue (or auto-pick) work for that issue —
// the only server-side guard that survived BACI-252.
func TestAutoDispatchIssueArchivedRejected(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "archived", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := p.store.SetIssueArchived(iss.ID, true); err != nil {
		t.Fatalf("SetIssueArchived: %v", err)
	}
	if _, err := p.local.AutoDispatchIssue(ctx, p.repo, iss.Key, "implement", false); err == nil {
		t.Fatalf("expected error dispatching an archived issue, got nil")
	} else if !strings.Contains(err.Error(), "archived") {
		t.Fatalf("error = %v, want a message mentioning archived", err)
	}
}

// TestAutoDispatchIssueNoFreeAgentQueues is the inverse of the
// pre-BACI-51 behaviour: with zero free agents the dispatch is queued
// instead of erroring. The matcher will bind it when an agent frees up.
func TestAutoDispatchIssueNoFreeAgentQueues(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "no agent", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	d, err := p.local.AutoDispatchIssue(ctx, p.repo, iss.Key, "implement", false)
	if err != nil {
		t.Fatalf("AutoDispatchIssue: %v", err)
	}
	if d.Status != model.DispatchQueued {
		t.Fatalf("status = %q, want queued", d.Status)
	}
	if d.TargetAgentID != nil || d.TargetSessionID != "" {
		t.Fatalf("queued dispatch should be target-less, got agent_id=%v session=%q", d.TargetAgentID, d.TargetSessionID)
	}
	// The matching issue should also flip waiting_for_claim so the UI
	// shows the spinner — the same plumbing pending dispatches use.
	got, err := p.store.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("GetIssueByID: %v", err)
	}
	if !got.WaitingForClaim {
		t.Fatalf("issue.waiting_for_claim should be true after enqueue")
	}
}

// TestAutoDispatchIssueRoundTrip drives the REST route via the remote
// client and asserts the local store sees the same dispatch row.
// Post-BACI-51 the row is queued (target-less) — the matcher binds it
// later from a UI process; this test stops at the enqueue.
func TestAutoDispatchIssueRoundTrip(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.local.CreateIssue(ctx, p.repo, inputs.IssueAddInput{
		Title: "round-trip auto", State: "todo",
	}, false)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	// Dry-run first: returns a projected queued dispatch but writes no
	// row and no audit entry.
	beforeHist, err := p.local.ListHistory(ctx, p.repo, store.HistoryFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	proj, err := p.remote.AutoDispatchIssue(ctx, p.repo, iss.Key, "implement", true)
	if err != nil {
		t.Fatalf("remote dry-run AutoDispatchIssue: %v", err)
	}
	if proj.Status != model.DispatchQueued {
		t.Fatalf("dry-run status = %q, want queued", proj.Status)
	}
	if proj.TargetAgentID != nil {
		t.Fatalf("dry-run should have no target, got agent_id=%v", proj.TargetAgentID)
	}
	if proj.ID != 0 {
		t.Fatalf("dry-run id = %d, want 0 (server-time field)", proj.ID)
	}
	rows, _ := p.store.ListDispatches(store.DispatchFilter{RepoID: &p.repo.ID})
	if len(rows) != 0 {
		t.Fatalf("dry-run persisted: now %d rows, want 0", len(rows))
	}
	afterHist, _ := p.local.ListHistory(ctx, p.repo, store.HistoryFilter{Limit: 100})
	if len(afterHist) != len(beforeHist) {
		t.Fatalf("dry-run wrote an audit row: before=%d after=%d", len(beforeHist), len(afterHist))
	}

	// Real call: queues the dispatch and the local store sees it.
	d, err := p.remote.AutoDispatchIssue(ctx, p.repo, iss.Key, "implement", false)
	if err != nil {
		t.Fatalf("remote AutoDispatchIssue: %v", err)
	}
	if d.Status != model.DispatchQueued {
		t.Fatalf("remote status = %q, want queued", d.Status)
	}
	rows, err = p.store.ListDispatches(store.DispatchFilter{RepoID: &p.repo.ID})
	if err != nil {
		t.Fatalf("ListDispatches: %v", err)
	}
	if len(rows) != 1 || rows[0].IssueKey != iss.Key || rows[0].Status != model.DispatchQueued {
		t.Fatalf("local dispatches: %+v", rows)
	}
}
