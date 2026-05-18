package boardcards

import (
	"context"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// fakeClient embeds client.Client so unused methods exist but panic if
// called — keeps the test surface narrow to only what Assemble touches.
type fakeClient struct {
	client.Client
	repo       *model.Repo
	issues     []*model.Issue
	claims     []*model.AgentClaim
	sessions   []*model.AgentSession
	dispatches []*model.AgentDispatch
	todos      map[int64][]model.SessionTodo
	templates  []*store.PromptTemplate
}

func (f *fakeClient) ListRepos(context.Context) ([]*model.Repo, error) {
	if f.repo == nil {
		return nil, nil
	}
	return []*model.Repo{f.repo}, nil
}
func (f *fakeClient) ListIssues(context.Context, client.IssueFilter) ([]*model.Issue, error) {
	return f.issues, nil
}
func (f *fakeClient) ListOpenClaims(context.Context, *model.Repo) ([]*model.AgentClaim, error) {
	return f.claims, nil
}
func (f *fakeClient) ListAgentSessions(context.Context, client.AgentSessionFilter) ([]*model.AgentSession, error) {
	return f.sessions, nil
}
func (f *fakeClient) RepoDispatches(context.Context, *model.Repo) ([]*model.AgentDispatch, error) {
	return f.dispatches, nil
}
func (f *fakeClient) ListTodosBySessions(context.Context, []string) (map[int64][]model.SessionTodo, error) {
	if f.todos == nil {
		return map[int64][]model.SessionTodo{}, nil
	}
	return f.todos, nil
}
func (f *fakeClient) ListPromptTemplates(context.Context) ([]*store.PromptTemplate, error) {
	return f.templates, nil
}

// TestAssembleVerbAndTodos covers the BACI-60 enrichment: an open
// claim resolves to the prompt template behind the newest matching
// dispatch (lower-cased label as ActiveVerb), and the claiming
// session's TodoWrite mirror flows through as TodosDone/TodosTotal.
func TestAssembleVerbAndTodos(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "TEST"}
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	agentID := int64(42)
	sess := &model.AgentSession{
		ID: 10, SessionID: "sess-a", RepoID: repo.ID, RepoPrefix: repo.Prefix,
		AgentID: &agentID, AgentName: "witty-bison",
	}
	issues := []*model.Issue{
		{Key: "TEST-1", State: model.StateInProgress, Title: "taken with verb + todos"},
		{Key: "TEST-2", State: model.StateTodo, Title: "free"},
	}
	claims := []*model.AgentClaim{
		{SessionID: "sess-a", SessionPK: 10, IssueKey: "TEST-1", ClaimedAt: t0},
	}
	// Two dispatches against the same (session, issue) — the newer one
	// wins, and a cancelled dispatch is excluded regardless of recency.
	dispatches := []*model.AgentDispatch{
		{ID: 1, TargetSessionID: "sess-a", IssueKey: "TEST-1", Mode: model.DispatchMode("plan"), Status: model.DispatchAcked, CreatedAt: t0.Add(-1 * time.Hour)},
		{ID: 2, TargetSessionID: "sess-a", IssueKey: "TEST-1", Mode: model.DispatchMode("design"), Status: model.DispatchAcked, CreatedAt: t0.Add(-30 * time.Minute)},
		{ID: 3, TargetSessionID: "sess-a", IssueKey: "TEST-1", Mode: model.DispatchMode("ship"), Status: model.DispatchCancelled, CreatedAt: t0.Add(-5 * time.Minute)},
	}
	todos := map[int64][]model.SessionTodo{
		10: {
			{Position: 0, Content: "step A", Status: model.TodoCompleted},
			{Position: 1, Content: "step B", Status: model.TodoCompleted},
			{Position: 2, Content: "step C", Status: model.TodoInProgress},
			{Position: 3, Content: "step D", Status: model.TodoPending},
		},
	}
	templates := []*store.PromptTemplate{
		{Slug: "plan", Name: "Planning"},
		{Slug: "design", Name: "Designing"},
		{Slug: "ship", Name: "Shipping"},
	}
	f := &fakeClient{
		repo: repo, issues: issues, claims: claims,
		sessions: []*model.AgentSession{sess}, dispatches: dispatches,
		todos: todos, templates: templates,
	}

	cards, err := Assemble(context.Background(), f, repo)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2", len(cards))
	}
	byKey := map[string]BoardCard{}
	for _, c := range cards {
		byKey[c.Key] = c
	}
	taken := byKey["TEST-1"]
	if !taken.Taken {
		t.Errorf("TEST-1 Taken = false, want true (open claim)")
	}
	if taken.ActiveVerb != "designing" {
		t.Errorf("TEST-1 ActiveVerb = %q, want %q (newest non-cancelled dispatch's lower-cased label)", taken.ActiveVerb, "designing")
	}
	if taken.TodosDone != 2 || taken.TodosTotal != 4 {
		t.Errorf("TEST-1 todos = %d/%d, want 2/4", taken.TodosDone, taken.TodosTotal)
	}
	free := byKey["TEST-2"]
	if free.Taken || free.ActiveVerb != "" || free.TodosTotal != 0 {
		t.Errorf("TEST-2 should stay un-enriched, got Taken=%v Verb=%q Total=%d", free.Taken, free.ActiveVerb, free.TodosTotal)
	}
}

// TestAssembleAgentIdentityDispatch covers the case where a dispatch
// targets the agent identity rather than a specific session — the
// open claim must still resolve to the right verb.
func TestAssembleAgentIdentityDispatch(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "TEST"}
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	agentID := int64(7)
	sess := &model.AgentSession{
		ID: 11, SessionID: "sess-b", RepoID: repo.ID, RepoPrefix: repo.Prefix,
		AgentID: &agentID,
	}
	issues := []*model.Issue{{Key: "TEST-3", State: model.StateInProgress}}
	claims := []*model.AgentClaim{{SessionID: "sess-b", SessionPK: 11, IssueKey: "TEST-3", ClaimedAt: t0}}
	dispatches := []*model.AgentDispatch{
		{ID: 5, TargetAgentID: &agentID, IssueKey: "TEST-3", Mode: model.DispatchMode("implement"), Status: model.DispatchAcked, CreatedAt: t0.Add(-10 * time.Minute)},
	}
	templates := []*store.PromptTemplate{{Slug: "implement", Name: "Implementing"}}
	f := &fakeClient{
		repo: repo, issues: issues, claims: claims,
		sessions: []*model.AgentSession{sess}, dispatches: dispatches,
		templates: templates,
	}
	cards, err := Assemble(context.Background(), f, repo)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if cards[0].ActiveVerb != "implementing" {
		t.Errorf("ActiveVerb = %q, want %q", cards[0].ActiveVerb, "implementing")
	}
}

// TestAssembleNoDispatchNoVerb covers the manual-claim path: an open
// claim with no matching dispatch shouldn't make up a verb.
func TestAssembleNoDispatchNoVerb(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "TEST"}
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	sess := &model.AgentSession{ID: 12, SessionID: "sess-c", RepoID: repo.ID}
	issues := []*model.Issue{{Key: "TEST-4", State: model.StateInProgress}}
	claims := []*model.AgentClaim{{SessionID: "sess-c", SessionPK: 12, IssueKey: "TEST-4", ClaimedAt: t0}}
	f := &fakeClient{
		repo: repo, issues: issues, claims: claims,
		sessions: []*model.AgentSession{sess},
	}
	cards, err := Assemble(context.Background(), f, repo)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !cards[0].Taken || cards[0].ActiveVerb != "" || cards[0].TodosTotal != 0 {
		t.Errorf("got Taken=%v Verb=%q Total=%d, want Taken=true Verb=\"\" Total=0", cards[0].Taken, cards[0].ActiveVerb, cards[0].TodosTotal)
	}
}
