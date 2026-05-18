package main

import (
	"context"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// fakeBoardClient is a minimal client.Client for ListCards tests — it
// embeds the interface (so unused methods exist but panic if called)
// and implements only the touches the tests need.
type fakeBoardClient struct {
	client.Client
	repo       *model.Repo
	issues     []*model.Issue
	claims     []*model.AgentClaim
	sessions   []*model.AgentSession
	dispatches []*model.AgentDispatch
	sessClaims map[string][]*model.AgentClaim // session id -> claims for ShowAgentSession
}

func (f *fakeBoardClient) GetRepoByPrefix(context.Context, string) (*model.Repo, error) {
	return f.repo, nil
}

func (f *fakeBoardClient) ListIssues(context.Context, client.IssueFilter) ([]*model.Issue, error) {
	return f.issues, nil
}

func (f *fakeBoardClient) ListOpenClaims(context.Context, *model.Repo) ([]*model.AgentClaim, error) {
	return f.claims, nil
}

func (f *fakeBoardClient) ListAgentSessions(context.Context, client.AgentSessionFilter) ([]*model.AgentSession, error) {
	return f.sessions, nil
}

func (f *fakeBoardClient) RepoDispatches(context.Context, *model.Repo) ([]*model.AgentDispatch, error) {
	return f.dispatches, nil
}

func (f *fakeBoardClient) ShowAgentSession(_ context.Context, sessionID string) (*client.AgentSessionView, error) {
	return &client.AgentSessionView{Claims: f.sessClaims[sessionID]}, nil
}

// ListTodosBySessionsAndIssue is a fixed-empty stub — the existing tests
// don't exercise the TodoWrite mirror, so the agentcards assembler
// reads an empty map back and produces empty Todos arrays. Wiring it
// through instead of relying on the embedded-interface panic was
// required once BACI-50 moved ListAgents through agentcards.Assemble
// (which always bulk-reads todos); BACI-62 reshaped the bulk reader to
// the per-(session, issue) variant.
func (f *fakeBoardClient) ListTodosBySessionsAndIssue(context.Context, []store.SessionIssuePair) (map[int64][]model.SessionTodo, error) {
	return map[int64][]model.SessionTodo{}, nil
}

// ListRepos is only called by Assemble in the cross-repo case; the
// existing tests scope by a single repo so this is a no-op stub.
func (f *fakeBoardClient) ListRepos(context.Context) ([]*model.Repo, error) {
	if f.repo == nil {
		return nil, nil
	}
	return []*model.Repo{f.repo}, nil
}

// ListPromptTemplates is a fixed-empty stub — boardcards.Assemble reads
// the registered templates to resolve dispatch mode slugs into the
// per-card ActiveVerb label. An empty list means no verb is ever
// derived, which is what the existing taken-flag tests expect.
func (f *fakeBoardClient) ListPromptTemplates(context.Context) ([]*store.PromptTemplate, error) {
	return nil, nil
}

// GetDisplayShowArchived (BACI-68) is a fixed-false stub —
// BoardService.ListCards now consults the display.show_archived global
// setting to decide whether to inflate the BoardCards list with
// archived rows. The taken-flag tests don't care either way; default
// false matches production behaviour.
func (f *fakeBoardClient) GetDisplayShowArchived(context.Context) (bool, error) {
	return false, nil
}

func TestListCardsTaken(t *testing.T) {
	issues := []*model.Issue{
		{Key: "TEST-1", State: model.StateTodo, Title: "held by an agent"},
		{Key: "TEST-2", State: model.StateTodo, Title: "free"},
	}

	cases := []struct {
		name      string
		claims    []*model.AgentClaim
		wantTaken map[string]bool
	}{
		{
			name:      "open claim marks its issue taken",
			claims:    []*model.AgentClaim{{IssueKey: "TEST-1"}},
			wantTaken: map[string]bool{"TEST-1": true, "TEST-2": false},
		},
		{
			name:      "no open claims leaves every card free",
			claims:    nil,
			wantTaken: map[string]bool{"TEST-1": false, "TEST-2": false},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := NewBoardService(&fakeBoardClient{
				repo:   &model.Repo{Prefix: "TEST"},
				issues: issues,
				claims: c.claims,
			})
			cards, err := svc.ListCards("TEST")
			if err != nil {
				t.Fatalf("ListCards: %v", err)
			}
			for _, card := range cards {
				if got := card.Taken; got != c.wantTaken[card.Key] {
					t.Errorf("card %s Taken = %v, want %v", card.Key, got, c.wantTaken[card.Key])
				}
			}
		})
	}
}

// TestListAgentsWaiting covers the Stop-hook side of BACI-14: an open
// claim on a needs_action issue must surface as Waiting/WaitingIssue on
// the AgentCard, with the ClaimDTO.State propagated for the drill-down.
func TestListAgentsWaiting(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "TEST"}
	t0 := time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)
	sess := &model.AgentSession{
		ID: 10, SessionID: "sess-a", RepoID: repo.ID, RepoPrefix: repo.Prefix,
		AgentName: "witty-bison@claude.shiny", LastSeenAt: t0,
	}
	parkedClaim := &model.AgentClaim{IssueKey: "TEST-1", ClaimedAt: t0}
	issues := []*model.Issue{
		{Key: "TEST-1", State: model.StateNeedsAction, Title: "parked"},
	}

	svc := NewBoardService(&fakeBoardClient{
		repo:       repo,
		issues:     issues,
		sessions:   []*model.AgentSession{sess},
		dispatches: nil,
		sessClaims: map[string][]*model.AgentClaim{"sess-a": {parkedClaim}},
	})
	cards, err := svc.ListAgents("TEST")
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("got %d cards, want 1", len(cards))
	}
	card := cards[0]
	if !card.Waiting || card.WaitingIssue != "TEST-1" {
		t.Errorf("Waiting=%v WaitingIssue=%q, want (true, TEST-1)", card.Waiting, card.WaitingIssue)
	}
	if !card.Busy || card.BusyIssue != "TEST-1" {
		t.Errorf("Busy=%v BusyIssue=%q, want (true, TEST-1)", card.Busy, card.BusyIssue)
	}
	if len(card.Claims) != 1 || card.Claims[0].State != string(model.StateNeedsAction) {
		t.Errorf("Claims[0].State = %q, want %q", card.Claims[0].State, model.StateNeedsAction)
	}

	// Now flip the same issue to in_progress: Waiting should clear,
	// Busy stays on, and the drill-down state moves with it.
	issues[0].State = model.StateInProgress
	cards, err = svc.ListAgents("TEST")
	if err != nil {
		t.Fatalf("ListAgents (in_progress): %v", err)
	}
	if cards[0].Waiting {
		t.Errorf("Waiting = true after issue flipped to in_progress, want false")
	}
	if !cards[0].Busy {
		t.Errorf("Busy = false, want true (claim is still open)")
	}
	if cards[0].Claims[0].State != string(model.StateInProgress) {
		t.Errorf("Claims[0].State = %q, want in_progress", cards[0].Claims[0].State)
	}
}

// pickFreeAgent moved to client.autoPickFreeAgent (BACI-40) so REST,
// CLI, and desktop share it. The unit-test for the picker now lives in
// internal/client; this file no longer needs to exercise it.
