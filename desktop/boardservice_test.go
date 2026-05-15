package main

import (
	"context"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// fakeBoardClient is a minimal client.Client for ListCards tests — it
// embeds the interface (so unused methods exist but panic if called)
// and implements only the three ListCards touches.
type fakeBoardClient struct {
	client.Client
	repo   *model.Repo
	issues []*model.Issue
	claims []*model.AgentClaim
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
