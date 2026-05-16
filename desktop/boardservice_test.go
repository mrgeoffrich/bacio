package main

import (
	"context"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
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

func TestPickFreeAgent(t *testing.T) {
	pending := DispatchDTO{Status: string(model.DispatchPending)}
	delivered := DispatchDTO{Status: string(model.DispatchDelivered)}
	acked := DispatchDTO{Status: string(model.DispatchAcked)}

	cases := []struct {
		name  string
		cards []AgentCard
		want  string
	}{
		{
			name:  "no agents",
			cards: nil,
			want:  "",
		},
		{
			name: "first idle agent wins",
			cards: []AgentCard{
				{AgentName: "otter", Status: "active"},
				{AgentName: "viper", Status: "idle"},
			},
			want: "otter",
		},
		{
			name: "skip ended",
			cards: []AgentCard{
				{AgentName: "otter", Status: "ended"},
				{AgentName: "viper", Status: "active"},
			},
			want: "viper",
		},
		{
			name: "skip busy (open claim)",
			cards: []AgentCard{
				{AgentName: "otter", Status: "active", Busy: true},
				{AgentName: "viper", Status: "active"},
			},
			want: "viper",
		},
		{
			name: "skip agent with no identity slug",
			cards: []AgentCard{
				{AgentName: "", Status: "active"},
				{AgentName: "viper", Status: "active"},
			},
			want: "viper",
		},
		{
			name: "skip agent with pending dispatch already queued",
			cards: []AgentCard{
				{AgentName: "otter", Status: "active", Dispatches: []DispatchDTO{pending}},
				{AgentName: "viper", Status: "active"},
			},
			want: "viper",
		},
		{
			name: "skip agent with delivered-but-unacked dispatch",
			cards: []AgentCard{
				{AgentName: "otter", Status: "active", Dispatches: []DispatchDTO{delivered}},
				{AgentName: "viper", Status: "idle"},
			},
			want: "viper",
		},
		{
			name: "acked dispatch does not occupy the agent",
			cards: []AgentCard{
				{AgentName: "otter", Status: "active", Dispatches: []DispatchDTO{acked}},
				{AgentName: "viper", Status: "idle"},
			},
			want: "otter",
		},
		{
			name: "everyone occupied yields no pick",
			cards: []AgentCard{
				{AgentName: "otter", Status: "active", Dispatches: []DispatchDTO{delivered}},
				{AgentName: "viper", Status: "active", Busy: true},
				{AgentName: "heron", Status: "ended"},
			},
			want: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pickFreeAgent(c.cards); got != c.want {
				t.Errorf("pickFreeAgent() = %q, want %q", got, c.want)
			}
		})
	}
}
