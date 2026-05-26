package client_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// mustQueueFollowOnFilter is the canonical filter the BACI-217 client
// tests use to read the agent.followon.queue audit row back. Limit is
// generous (the seed only writes one row) so a regression that emits
// duplicate audit rows shows up as len > 1.
func mustQueueFollowOnFilter() store.HistoryFilter {
	return store.HistoryFilter{Limit: 10, Op: "agent.followon.queue"}
}

// TestQueueFollowOnDispatch_FallbackToBlockerVariant (BACI-217) — when
// an issue has no in-flight dispatch but is blocked by at least one
// open issue, QueueFollowOnDispatch falls back to the blockers-clear
// variant rather than erroring with "no active dispatch". The dispatch
// row carries the new flag, no parent-acks link, and the
// agent.followon.queue audit row stamps `gate=blockers` in Details.
func TestQueueFollowOnDispatch_FallbackToBlockerVariant(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	blocked, err := p.store.CreateIssue(p.repo.ID, nil, "blocked", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create blocked: %v", err)
	}
	blocker, err := p.store.CreateIssue(p.repo.ID, nil, "blocker", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create blocker: %v", err)
	}
	if err := p.store.CreateRelation(blocker.ID, blocked.ID, model.RelBlocks); err != nil {
		t.Fatalf("create blocks edge: %v", err)
	}

	d, err := p.local.QueueFollowOnDispatch(ctx, p.repo, blocked.Key, "implement", false)
	if err != nil {
		t.Fatalf("QueueFollowOnDispatch: %v", err)
	}
	if !d.QueuedUntilBlockersClear {
		t.Fatal("queued_until_blockers_clear flag not set on returned row")
	}
	if d.QueuedAfterDispatchID != nil {
		t.Fatalf("queued_after_dispatch_id should be nil on a blockers-clear row, got %v", d.QueuedAfterDispatchID)
	}
	if d.Status != model.DispatchQueued {
		t.Fatalf("status = %q, want queued", d.Status)
	}
	if string(d.Mode) != "implement" {
		t.Fatalf("mode = %q, want implement", d.Mode)
	}
	// Audit row carries the gate=blockers tag so a reader of
	// `bacio history --op agent.followon.queue` can tell the variants
	// apart.
	hist, err := p.local.ListHistory(ctx, p.repo, mustQueueFollowOnFilter())
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("agent.followon.queue rows: got %d, want 1", len(hist))
	}
	if !strings.Contains(hist[0].Details, "gate=blockers") {
		t.Fatalf("audit Details should mention gate=blockers, got %q", hist[0].Details)
	}
}

// TestQueueFollowOnDispatch_PrefersParentVariantWhenInflight (BACI-217)
// — when the issue has BOTH an in-flight parent dispatch AND open
// blockers, QueueFollowOnDispatch writes the parent-acks variant (the
// historical default). The branch order in the client wrapper is what
// keeps the two variants from racing on the same issue.
func TestQueueFollowOnDispatch_PrefersParentVariantWhenInflight(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	blocked, err := p.store.CreateIssue(p.repo.ID, nil, "blocked-but-active", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	blocker, err := p.store.CreateIssue(p.repo.ID, nil, "open blocker", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create blocker: %v", err)
	}
	if err := p.store.CreateRelation(blocker.ID, blocked.ID, model.RelBlocks); err != nil {
		t.Fatalf("create blocks edge: %v", err)
	}
	// Queue an in-flight parent dispatch on the blocked issue.
	if _, err := p.local.AutoDispatchIssue(ctx, p.repo, blocked.Key, "plan", false); err != nil {
		t.Fatalf("AutoDispatchIssue: %v", err)
	}

	d, err := p.local.QueueFollowOnDispatch(ctx, p.repo, blocked.Key, "implement", false)
	if err != nil {
		t.Fatalf("QueueFollowOnDispatch: %v", err)
	}
	if d.QueuedUntilBlockersClear {
		t.Fatal("parent variant should not set queued_until_blockers_clear")
	}
	if d.QueuedAfterDispatchID == nil {
		t.Fatal("parent variant should set queued_after_dispatch_id")
	}
}

// TestQueueFollowOnDispatch_NeitherErrors (BACI-217) — an issue with
// neither an in-flight dispatch nor any open blockers keeps today's
// "no active dispatch" error verbatim.
func TestQueueFollowOnDispatch_NeitherErrors(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "neither", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	_, err = p.local.QueueFollowOnDispatch(ctx, p.repo, iss.Key, "implement", false)
	if err == nil {
		t.Fatal("expected error on issue with no parent and no blockers, got nil")
	}
	if !strings.Contains(err.Error(), "no active dispatch") {
		t.Fatalf("error should preserve 'no active dispatch' message, got: %v", err)
	}
}

// TestQueueFollowOnDispatch_BlockerVariantDryRun (BACI-217) — dry-run
// projects the would-be blockers-clear row without touching the DB.
func TestQueueFollowOnDispatch_BlockerVariantDryRun(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	blocked, err := p.store.CreateIssue(p.repo.ID, nil, "blocked", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create blocked: %v", err)
	}
	blocker, err := p.store.CreateIssue(p.repo.ID, nil, "blocker", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create blocker: %v", err)
	}
	if err := p.store.CreateRelation(blocker.ID, blocked.ID, model.RelBlocks); err != nil {
		t.Fatalf("create blocks edge: %v", err)
	}

	d, err := p.local.QueueFollowOnDispatch(ctx, p.repo, blocked.Key, "implement", true)
	if err != nil {
		t.Fatalf("QueueFollowOnDispatch dry-run: %v", err)
	}
	if !d.QueuedUntilBlockersClear {
		t.Fatal("dry-run projection should set queued_until_blockers_clear")
	}
	if d.QueuedAfterDispatchID != nil {
		t.Fatalf("dry-run projection should not carry queued_after_dispatch_id, got %v", d.QueuedAfterDispatchID)
	}
	// Confirm no real row was written.
	ds, err := p.local.RepoDispatches(ctx, p.repo)
	if err != nil {
		t.Fatalf("RepoDispatches: %v", err)
	}
	if len(ds) != 0 {
		t.Fatalf("dry-run wrote %d real dispatch(es), want 0", len(ds))
	}
}
