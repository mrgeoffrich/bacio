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
// the local backend: state-gate accepts, the registered+channel-live
// session is picked, and the dispatch is created targeting it.
func TestAutoDispatchIssueLocal(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "auto-dispatch me", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	ag, _, err := p.store.UpsertAgent("swift-otter@claude.test", true)
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	sess, err := p.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID:      "auto-sess-1",
		RepoID:         p.repo.ID,
		AgentID:        &ag.ID,
		Actor:          "tester",
		MarkRegistered: true,
	})
	if err != nil {
		t.Fatalf("UpsertAgentSession: %v", err)
	}
	if err := p.store.UpsertAgentChannel(store.UpsertAgentChannelIn{
		RepoID: p.repo.ID, AgentID: &ag.ID, Host: "host", ClaudePID: 1234, ChannelPID: 1235,
	}); err != nil {
		t.Fatalf("UpsertAgentChannel: %v", err)
	}
	if err := p.store.LinkSessionChannel(sess.SessionID, 1234, "host"); err != nil {
		t.Fatalf("LinkSessionChannel: %v", err)
	}
	d, err := p.local.AutoDispatchIssue(ctx, p.repo, iss.Key, "implement", false)
	if err != nil {
		t.Fatalf("AutoDispatchIssue: %v", err)
	}
	if d.TargetAgentName != ag.Name {
		t.Fatalf("dispatch target = %q, want %q", d.TargetAgentName, ag.Name)
	}
	if d.IssueKey != iss.Key {
		t.Fatalf("dispatch issue = %q, want %q", d.IssueKey, iss.Key)
	}
	if string(d.Mode) != "implement" {
		t.Fatalf("dispatch mode = %q, want implement", d.Mode)
	}
	if d.Status != model.DispatchPending {
		t.Fatalf("dispatch status = %q, want pending", d.Status)
	}
}

// TestAutoDispatchIssueStateGate verifies that the state-gate guard
// blocks a mode whose template doesn't accept the issue's current state.
// "review" is gated to in_review by default; a todo issue should fail.
func TestAutoDispatchIssueStateGate(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "wrong state", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := p.local.AutoDispatchIssue(ctx, p.repo, iss.Key, "review", false); err == nil {
		t.Fatalf("expected state-gate error for review on todo, got nil")
	} else if !strings.Contains(err.Error(), "can't run") {
		t.Fatalf("error = %v, want state-gate message", err)
	}
}

// TestAutoDispatchIssueNoFreeAgent covers the picker miss path —
// every registered session is either busy, ended, or has no channel.
func TestAutoDispatchIssueNoFreeAgent(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "no agent", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, err := p.local.AutoDispatchIssue(ctx, p.repo, iss.Key, "implement", false); err == nil {
		t.Fatalf("expected no-free-agent error, got nil")
	} else if !strings.Contains(err.Error(), "no free agent") {
		t.Fatalf("error = %v, want no-free-agent message", err)
	}
}

// TestAutoDispatchIssueRoundTrip drives the REST route via the remote
// client and asserts the local store sees the same dispatch row,
// closing the audit story alongside the desktop path.
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
	ag, _, err := p.store.UpsertAgent("quiet-lynx@claude.test", true)
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	sess, err := p.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID:      "auto-sess-rt",
		RepoID:         p.repo.ID,
		AgentID:        &ag.ID,
		Actor:          "tester",
		MarkRegistered: true,
	})
	if err != nil {
		t.Fatalf("UpsertAgentSession: %v", err)
	}
	if err := p.store.UpsertAgentChannel(store.UpsertAgentChannelIn{
		RepoID: p.repo.ID, AgentID: &ag.ID, Host: "host", ClaudePID: 4321, ChannelPID: 4322,
	}); err != nil {
		t.Fatalf("UpsertAgentChannel: %v", err)
	}
	if err := p.store.LinkSessionChannel(sess.SessionID, 4321, "host"); err != nil {
		t.Fatalf("LinkSessionChannel: %v", err)
	}

	// Dry-run first: returns a projection naming the free agent but
	// writes no row and no audit entry.
	beforeHist, err := p.local.ListHistory(ctx, p.repo, store.HistoryFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	proj, err := p.remote.AutoDispatchIssue(ctx, p.repo, iss.Key, "implement", true)
	if err != nil {
		t.Fatalf("remote dry-run AutoDispatchIssue: %v", err)
	}
	if proj.TargetAgentName != ag.Name {
		t.Fatalf("dry-run target = %q, want %q", proj.TargetAgentName, ag.Name)
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
	if d.TargetAgentName != ag.Name {
		t.Fatalf("remote target = %q, want %q", d.TargetAgentName, ag.Name)
	}
	rows, err = p.store.ListDispatches(store.DispatchFilter{RepoID: &p.repo.ID})
	if err != nil {
		t.Fatalf("ListDispatches: %v", err)
	}
	if len(rows) != 1 || rows[0].IssueKey != iss.Key {
		t.Fatalf("local dispatches: %+v", rows)
	}
}
