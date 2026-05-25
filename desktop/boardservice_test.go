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

// ListOpenQuestionsBySessions is a fixed-empty stub — boardcards.Assemble
// reads open clarification questions per session to set the per-card
// needs-action flag. The taken-flag tests don't exercise that flow, so
// an empty map matches the "no outstanding questions" production case.
// Wiring this through (rather than relying on the embedded-interface
// panic) is the same pattern as ListTodosBySessionsAndIssue above.
func (f *fakeBoardClient) ListOpenQuestionsBySessions(context.Context, []string) (map[int64][]*model.SessionQuestion, error) {
	return map[int64][]*model.SessionQuestion{}, nil
}

// BlockersFor is a fixed-empty stub — boardcards.Assemble reads
// per-issue blockers to surface a per-card "blocked by N" badge. The
// taken-flag tests don't exercise blocker relations, so an empty map
// matches the "no blockers" case. Mirrors the fakeClient stub in
// internal/boardcards/cards_test.go.
func (f *fakeBoardClient) BlockersFor(context.Context, []int64) (map[int64][]store.IssueBlocker, error) {
	return map[int64][]store.IssueBlocker{}, nil
}

// InflightByModeForRepo (BACI-145) — empty map is the "no in-flight
// dispatches" case, which keeps every WaitingState derivation off the
// queued_blocked branch in these tests.
func (f *fakeBoardClient) InflightByModeForRepo(context.Context, *model.Repo) (map[model.DispatchMode]int, error) {
	return map[model.DispatchMode]int{}, nil
}

// CountEvalCommentsByIssue (BACI-131 / BACI-141) — boardcards.Assemble
// surfaces an eval-note glyph per card from this bulk count. The
// taken-flag tests don't exercise eval-comment rows, so an empty map
// matches the "no eval notes" production case. Mirrors the fakeClient
// stub in internal/boardcards/cards_test.go.
func (f *fakeBoardClient) CountEvalCommentsByIssue(context.Context, []int64) (map[int64]int, error) {
	return map[int64]int{}, nil
}

// CountTranscriptDocsByIssue (BACI-141) — same shape: an empty bulk
// count maps to "no transcript docs", which is the taken-flag test's
// world.
func (f *fakeBoardClient) CountTranscriptDocsByIssue(context.Context, []int64) (map[int64]int, error) {
	return map[int64]int{}, nil
}

// ListHiddenFeatureSlugs (BACI-177) — empty slice means "no features
// are hidden", which matches the production default and lets every
// taken-flag / waiting-state test pass cards through unchanged.
// BoardService.ListCards calls this once per repo to thread the set
// into boardcards.Assemble.
func (f *fakeBoardClient) ListHiddenFeatureSlugs(context.Context, *model.Repo) ([]string, error) {
	return nil, nil
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

// fakeShippedClient is a narrow stub for ListShipped — it serves the
// GetRepoByPrefix + ListShippedIssues + ListPRs trio the popover hits
// and nothing else.
type fakeShippedClient struct {
	client.Client
	repo    *model.Repo
	shipped []*model.Issue
	prs     map[string][]*model.PullRequest
}

func (f *fakeShippedClient) GetRepoByPrefix(context.Context, string) (*model.Repo, error) {
	return f.repo, nil
}
func (f *fakeShippedClient) ListShippedIssues(_ context.Context, _ *model.Repo, _ store.ShippedFilter) ([]*model.Issue, error) {
	return f.shipped, nil
}
func (f *fakeShippedClient) ListPRs(_ context.Context, _ *model.Repo, key string) ([]*model.PullRequest, error) {
	if f.prs == nil {
		return nil, nil
	}
	return f.prs[key], nil
}

// TestListShipped — BACI-187. Two done issues + one open issue;
// ListShipped surfaces only the done rows, in the order the fake
// returns them (newest-first is the store's responsibility, asserted
// in internal/store/shipped_test.go). Locks in the Wails binding's
// (issue → DTO) mapping including the first-PR-only chip rule.
func TestListShipped(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "TEST"}
	stamp := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	older := time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC)
	shipped := []*model.Issue{
		{Key: "TEST-2", Title: "newer", State: model.StateDone, TerminalAt: &stamp, Tags: []string{"feat"}},
		{Key: "TEST-1", Title: "older", State: model.StateDone, TerminalAt: &older, Tags: []string{}},
	}
	prs := map[string][]*model.PullRequest{
		"TEST-2": {
			{URL: "https://example.com/pr/2-first"},
			{URL: "https://example.com/pr/2-second"},
		},
	}
	svc := NewBoardService(&fakeShippedClient{repo: repo, shipped: shipped, prs: prs})

	rows, err := svc.ListShipped("TEST", 30, 20)
	if err != nil {
		t.Fatalf("ListShipped: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Key != "TEST-2" || rows[0].Title != "newer" {
		t.Fatalf("row 0 = %+v, want TEST-2/newer", rows[0])
	}
	if !rows[0].TerminalAt.Equal(stamp) {
		t.Fatalf("row 0 TerminalAt = %v, want %v", rows[0].TerminalAt, stamp)
	}
	if rows[0].PRURL != "https://example.com/pr/2-first" {
		t.Errorf("row 0 PR chip = %q, want first PR url", rows[0].PRURL)
	}
	if rows[1].PRURL != "" {
		t.Errorf("row 1 PR chip = %q, want empty (no PRs attached)", rows[1].PRURL)
	}
	if rows[0].Tags == nil {
		t.Errorf("row 0 Tags must not be nil — popover iterates unconditionally")
	}
}

// TestListShippedRejectsAllRepos — the popover is per-repo by design;
// calling ListShipped with "" or "all" must surface a clear error.
func TestListShippedRejectsAllRepos(t *testing.T) {
	svc := NewBoardService(&fakeShippedClient{repo: &model.Repo{Prefix: "TEST"}})
	if _, err := svc.ListShipped("", 0, 0); err == nil {
		t.Error("ListShipped(\"\") = nil, want error (per-repo only)")
	}
	if _, err := svc.ListShipped("all", 0, 0); err == nil {
		t.Error("ListShipped(\"all\") = nil, want error (per-repo only)")
	}
}
