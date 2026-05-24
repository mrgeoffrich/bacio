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
	questions  map[int64][]*model.SessionQuestion
	// blockers (BACI-114) is keyed by blocked-issue id — the same
	// shape store.BlockersFor returns.
	blockers map[int64][]store.IssueBlocker
	// dispatchAware (BACI-132) opts the fake into filtering the
	// returned todos by the pair's IssueKey and DispatchID — needed
	// for the per-dispatch scope test. Existing tests that pass the
	// todos map verbatim leave it false and the fake returns
	// everything (the assembler's per-row issue_key match still
	// catches the cross-card cases).
	dispatchAware bool
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
func (f *fakeClient) ListTodosBySessionsAndIssue(_ context.Context, pairs []store.SessionIssuePair) (map[int64][]model.SessionTodo, error) {
	if f.todos == nil {
		return map[int64][]model.SessionTodo{}, nil
	}
	if !f.dispatchAware {
		return f.todos, nil
	}
	// Build a (sessionID, issueKey, dispatchID-or-0) → bucket lookup
	// and filter each session's rows accordingly. Mirrors the store's
	// COALESCE(t.dispatch_id, 0) triple match.
	type key struct {
		issue    string
		dispatch int64
	}
	wants := make(map[string][]key)
	for _, p := range pairs {
		var d int64
		if p.DispatchID != nil {
			d = *p.DispatchID
		}
		wants[p.SessionID] = append(wants[p.SessionID], key{issue: p.IssueKey, dispatch: d})
	}
	// Map sessionPK → sessionID via the fake's sessions slice.
	idByPK := make(map[int64]string)
	for _, s := range f.sessions {
		idByPK[s.ID] = s.SessionID
	}
	out := make(map[int64][]model.SessionTodo, len(f.todos))
	for pk, list := range f.todos {
		sid, ok := idByPK[pk]
		if !ok {
			continue
		}
		want := wants[sid]
		for _, t := range list {
			var rowDispatch int64
			if t.DispatchID != nil {
				rowDispatch = *t.DispatchID
			}
			for _, k := range want {
				if k.issue == t.IssueKey && k.dispatch == rowDispatch {
					out[pk] = append(out[pk], t)
					break
				}
			}
		}
	}
	return out, nil
}
func (f *fakeClient) ListPromptTemplates(context.Context) ([]*store.PromptTemplate, error) {
	return f.templates, nil
}
func (f *fakeClient) ListOpenQuestionsBySessions(context.Context, []string) (map[int64][]*model.SessionQuestion, error) {
	if f.questions == nil {
		return map[int64][]*model.SessionQuestion{}, nil
	}
	return f.questions, nil
}
func (f *fakeClient) BlockersFor(context.Context, []int64) (map[int64][]store.IssueBlocker, error) {
	if f.blockers == nil {
		return map[int64][]store.IssueBlocker{}, nil
	}
	return f.blockers, nil
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
			{Position: 0, Content: "step A", Status: model.TodoCompleted, IssueKey: "TEST-1"},
			{Position: 1, Content: "step B", Status: model.TodoCompleted, IssueKey: "TEST-1"},
			{Position: 2, Content: "step C", Status: model.TodoInProgress, IssueKey: "TEST-1"},
			{Position: 3, Content: "step D", Status: model.TodoPending, IssueKey: "TEST-1"},
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

	cards, err := Assemble(context.Background(), f, repo, false)
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
	// BACI-75: the per-task rows flow through with the same shape the
	// kanban card expects — content + status, in storage (Position)
	// order. The counts above already check the totals; this locks in
	// the list view that drives the click-to-expand Tasks pill.
	wantTodos := []BoardCardTodo{
		{Content: "step A", Status: "completed"},
		{Content: "step B", Status: "completed"},
		{Content: "step C", Status: "in_progress"},
		{Content: "step D", Status: "pending"},
	}
	if len(taken.Todos) != len(wantTodos) {
		t.Fatalf("TEST-1 Todos len = %d, want %d", len(taken.Todos), len(wantTodos))
	}
	for i, want := range wantTodos {
		if taken.Todos[i] != want {
			t.Errorf("TEST-1 Todos[%d] = %+v, want %+v", i, taken.Todos[i], want)
		}
	}
	free := byKey["TEST-2"]
	if free.Taken || free.ActiveVerb != "" || free.TodosTotal != 0 {
		t.Errorf("TEST-2 should stay un-enriched, got Taken=%v Verb=%q Total=%d", free.Taken, free.ActiveVerb, free.TodosTotal)
	}
	if len(free.Todos) != 0 {
		t.Errorf("TEST-2 Todos = %+v, want empty (untaken card)", free.Todos)
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
	cards, err := Assemble(context.Background(), f, repo, false)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if cards[0].ActiveVerb != "implementing" {
		t.Errorf("ActiveVerb = %q, want %q", cards[0].ActiveVerb, "implementing")
	}
}

// TestAssembleSurfacesOpenQuestions covers BACI-53 follow-up: the
// winning claim's open ask_user_question rows whose issue_key matches
// THIS card's issue surface as OpenQuestions entries. Rows tied to a
// different issue (the same session juggling two claims) must not
// leak across cards.
func TestAssembleSurfacesOpenQuestions(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "TEST"}
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	sess := &model.AgentSession{ID: 20, SessionID: "sess-q", RepoID: repo.ID, RepoPrefix: repo.Prefix}
	issues := []*model.Issue{
		{Key: "TEST-10", State: model.StateNeedsAction, Title: "blocked on a question"},
		{Key: "TEST-11", State: model.StateInProgress, Title: "free, sibling claim"},
	}
	claims := []*model.AgentClaim{
		{SessionID: "sess-q", SessionPK: 20, IssueKey: "TEST-10", ClaimedAt: t0},
		{SessionID: "sess-q", SessionPK: 20, IssueKey: "TEST-11", ClaimedAt: t0},
	}
	questions := map[int64][]*model.SessionQuestion{
		20: {
			{
				ID: 7, IssueKey: "TEST-10", AskedAt: t0,
				Payload: model.QuestionPayload{Questions: []model.QuestionItem{
					{Header: "Pizza?", Question: "Pineapple on pizza: yes or no?"},
					{Header: "Crust?", Question: "Thin or thick?"},
				}},
			},
			{
				ID: 8, IssueKey: "TEST-99", AskedAt: t0, // unrelated issue
				Payload: model.QuestionPayload{Questions: []model.QuestionItem{{Header: "X", Question: "x?"}}},
			},
		},
	}
	f := &fakeClient{
		repo: repo, issues: issues, claims: claims,
		sessions: []*model.AgentSession{sess},
		questions: questions,
	}

	cards, err := Assemble(context.Background(), f, repo, false)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	byKey := map[string]BoardCard{}
	for _, c := range cards {
		byKey[c.Key] = c
	}
	blocked := byKey["TEST-10"]
	if len(blocked.OpenQuestions) != 1 {
		t.Fatalf("TEST-10 OpenQuestions = %d, want 1", len(blocked.OpenQuestions))
	}
	got := blocked.OpenQuestions[0]
	if got.ID != 7 || got.Header != "Pizza?" || got.FirstQuestion != "Pineapple on pizza: yes or no?" || got.Count != 2 {
		t.Errorf("TEST-10 OpenQuestions[0] = %+v, want {ID:7 Header:Pizza? First:'Pineapple…' Count:2}", got)
	}
	sibling := byKey["TEST-11"]
	if len(sibling.OpenQuestions) != 0 {
		t.Errorf("TEST-11 OpenQuestions = %+v, want empty (the question is tied to TEST-10)", sibling.OpenQuestions)
	}
}

// TestAssembleTodosScopedPerIssue (BACI-62) covers the live-repro
// from the issue body: one session worked TEST-1 first, then TEST-2 —
// the first job's completed rows must NOT show up in TEST-2's
// progress count. Both issues are taken (the same session claims
// both, pairing-style), so both cards have the session as their
// winning claim.
func TestAssembleTodosScopedPerIssue(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "TEST"}
	t0 := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	sess := &model.AgentSession{ID: 30, SessionID: "sess-juggle", RepoID: repo.ID, RepoPrefix: repo.Prefix}
	issues := []*model.Issue{
		{Key: "TEST-1", State: model.StateInProgress, Title: "first job"},
		{Key: "TEST-2", State: model.StateInProgress, Title: "second job"},
	}
	claims := []*model.AgentClaim{
		{SessionID: "sess-juggle", SessionPK: 30, IssueKey: "TEST-1", ClaimedAt: t0.Add(-1 * time.Hour)},
		{SessionID: "sess-juggle", SessionPK: 30, IssueKey: "TEST-2", ClaimedAt: t0},
	}
	// Session 30 has 4 TEST-1 rows (all completed — the prior job) and
	// 2 TEST-2 rows (one in_progress, one pending — the current job).
	todos := map[int64][]model.SessionTodo{
		30: {
			{Position: 0, Content: "test-1/a", Status: model.TodoCompleted, IssueKey: "TEST-1"},
			{Position: 1, Content: "test-1/b", Status: model.TodoCompleted, IssueKey: "TEST-1"},
			{Position: 2, Content: "test-1/c", Status: model.TodoCompleted, IssueKey: "TEST-1"},
			{Position: 3, Content: "test-1/d", Status: model.TodoCompleted, IssueKey: "TEST-1"},
			{Position: 4, Content: "test-2/a", Status: model.TodoInProgress, IssueKey: "TEST-2"},
			{Position: 5, Content: "test-2/b", Status: model.TodoPending, IssueKey: "TEST-2"},
		},
	}
	f := &fakeClient{
		repo: repo, issues: issues, claims: claims,
		sessions: []*model.AgentSession{sess}, todos: todos,
	}
	cards, err := Assemble(context.Background(), f, repo, false)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	byKey := map[string]BoardCard{}
	for _, c := range cards {
		byKey[c.Key] = c
	}
	one := byKey["TEST-1"]
	if one.TodosDone != 4 || one.TodosTotal != 4 {
		t.Errorf("TEST-1 todos = %d/%d, want 4/4 (its own four completed rows)", one.TodosDone, one.TodosTotal)
	}
	two := byKey["TEST-2"]
	if two.TodosDone != 0 || two.TodosTotal != 2 {
		t.Errorf("TEST-2 todos = %d/%d, want 0/2 (TEST-1's history must not leak)", two.TodosDone, two.TodosTotal)
	}
	// BACI-75: same scoping invariant for the list view. TEST-1 sees
	// only its own four rows; TEST-2 sees only its own two.
	wantOne := []BoardCardTodo{
		{Content: "test-1/a", Status: "completed"},
		{Content: "test-1/b", Status: "completed"},
		{Content: "test-1/c", Status: "completed"},
		{Content: "test-1/d", Status: "completed"},
	}
	if len(one.Todos) != len(wantOne) {
		t.Fatalf("TEST-1 Todos len = %d, want %d", len(one.Todos), len(wantOne))
	}
	for i, want := range wantOne {
		if one.Todos[i] != want {
			t.Errorf("TEST-1 Todos[%d] = %+v, want %+v", i, one.Todos[i], want)
		}
	}
	wantTwo := []BoardCardTodo{
		{Content: "test-2/a", Status: "in_progress"},
		{Content: "test-2/b", Status: "pending"},
	}
	if len(two.Todos) != len(wantTwo) {
		t.Fatalf("TEST-2 Todos len = %d, want %d (TEST-1's history must not leak)", len(two.Todos), len(wantTwo))
	}
	for i, want := range wantTwo {
		if two.Todos[i] != want {
			t.Errorf("TEST-2 Todos[%d] = %+v, want %+v", i, two.Todos[i], want)
		}
	}
}

// TestAssembleTodosScopedPerDispatch covers BACI-132: when one
// session has worked two dispatches on the same issue back-to-back
// (plan → implement), the card's Tasks pill and task list show only
// the active dispatch's rows. The fake client honours the
// DispatchID-keyed filter so we can assert the assembler asks for
// the right triple.
func TestAssembleTodosScopedPerDispatch(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "TEST"}
	t0 := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
	sess := &model.AgentSession{ID: 30, SessionID: "sess-plan-impl", RepoID: repo.ID, RepoPrefix: repo.Prefix}
	issues := []*model.Issue{
		{Key: "TEST-1", State: model.StateInProgress, Title: "plan-then-implement"},
	}
	claims := []*model.AgentClaim{
		{SessionID: "sess-plan-impl", SessionPK: 30, IssueKey: "TEST-1", ClaimedAt: t0.Add(-1 * time.Hour)},
	}
	planID := int64(11)
	implID := int64(22)
	dispatches := []*model.AgentDispatch{
		{ID: planID, IssueKey: "TEST-1", TargetSessionID: "sess-plan-impl",
			Mode: model.DispatchMode("plan"), Status: model.DispatchAcked, CreatedAt: t0.Add(-2 * time.Hour)},
		{ID: implID, IssueKey: "TEST-1", TargetSessionID: "sess-plan-impl",
			Mode: model.DispatchMode("implement"), Status: model.DispatchDelivered, CreatedAt: t0},
	}
	// All rows belong to session 30; the BACI-132 filter is the bulk
	// reader's job. The fake honours the DispatchID on the pair so we
	// can assert the assembler keys correctly.
	todos := map[int64][]model.SessionTodo{
		30: {
			{Position: 0, Content: "plan/a", Status: model.TodoCompleted, IssueKey: "TEST-1", DispatchID: &planID},
			{Position: 1, Content: "plan/b", Status: model.TodoCompleted, IssueKey: "TEST-1", DispatchID: &planID},
			{Position: 2, Content: "impl/a", Status: model.TodoInProgress, IssueKey: "TEST-1", DispatchID: &implID},
			{Position: 3, Content: "impl/b", Status: model.TodoPending, IssueKey: "TEST-1", DispatchID: &implID},
		},
	}
	f := &fakeClient{
		repo: repo, issues: issues, claims: claims,
		sessions: []*model.AgentSession{sess}, dispatches: dispatches,
		todos: todos, dispatchAware: true,
	}
	cards, err := Assemble(context.Background(), f, repo, false)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	byKey := map[string]BoardCard{}
	for _, c := range cards {
		byKey[c.Key] = c
	}
	got := byKey["TEST-1"]
	// Only the implement dispatch's rows must flow through — 2 rows,
	// one in_progress and one pending.
	if got.TodosTotal != 2 {
		t.Errorf("TodosTotal = %d, want 2 (only implement dispatch)", got.TodosTotal)
	}
	if got.TodosDone != 0 {
		t.Errorf("TodosDone = %d, want 0 (plan rows must not bleed)", got.TodosDone)
	}
	if len(got.Todos) != 2 || got.Todos[0].Content != "impl/a" || got.Todos[1].Content != "impl/b" {
		t.Fatalf("Todos = %+v, want [impl/a, impl/b]", got.Todos)
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
	cards, err := Assemble(context.Background(), f, repo, false)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !cards[0].Taken || cards[0].ActiveVerb != "" || cards[0].TodosTotal != 0 {
		t.Errorf("got Taken=%v Verb=%q Total=%d, want Taken=true Verb=\"\" Total=0", cards[0].Taken, cards[0].ActiveVerb, cards[0].TodosTotal)
	}
}

// TestCompletionSortKey covers the BACI-101 ordering key: updated_at
// when set, created_at as the fallback.
func TestCompletionSortKey(t *testing.T) {
	created := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)

	withUpdated := &model.Issue{CreatedAt: created, UpdatedAt: updated}
	if got := CompletionSortKey(withUpdated); !got.Equal(updated) {
		t.Errorf("CompletionSortKey with updated_at = %v, want %v", got, updated)
	}
	zeroUpdated := &model.Issue{CreatedAt: created}
	if got := CompletionSortKey(zeroUpdated); !got.Equal(created) {
		t.Errorf("CompletionSortKey with zero updated_at = %v, want created_at %v", got, created)
	}
	if got := CompletionSortKey(nil); !got.IsZero() {
		t.Errorf("CompletionSortKey(nil) = %v, want zero time", got)
	}
}

// TestIsCompletedColumn covers the BACI-101 column predicate.
func TestIsCompletedColumn(t *testing.T) {
	for _, st := range []model.State{model.StateDone, model.StateCancelled} {
		if !IsCompletedColumn(st) {
			t.Errorf("IsCompletedColumn(%q) = false, want true", st)
		}
	}
	for _, st := range []model.State{
		model.StateTodo, model.StateInProgress,
		model.StateNeedsAction, model.StateInReview,
	} {
		if IsCompletedColumn(st) {
			t.Errorf("IsCompletedColumn(%q) = true, want false", st)
		}
	}
}

// TestAssembleBlockedBy (BACI-114) covers the per-card BlockedBy
// surfacing: an inbound open-state `blocks` edge populates the
// blocked card's BlockedBy slice with the blocker's key + state, the
// blocker card stays unblocked (no inbound edge), and a closed-state
// blocker does NOT mark the target as blocked (the open-state filter
// is exercised).
func TestAssembleBlockedBy(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "TEST"}
	issues := []*model.Issue{
		{ID: 1, Key: "TEST-1", State: model.StateTodo, Title: "blocker (todo)"},
		{ID: 2, Key: "TEST-2", State: model.StateTodo, Title: "blocked by TEST-1"},
		{ID: 3, Key: "TEST-3", State: model.StateDone, Title: "blocker (done)"},
		{ID: 4, Key: "TEST-4", State: model.StateTodo, Title: "blocked by TEST-3 only"},
		{ID: 5, Key: "TEST-5", State: model.StateTodo, Title: "lone, no edges"},
	}
	// TEST-2 is blocked by TEST-1 (open). TEST-4 is blocked by TEST-3
	// (done — should be filtered out by isCardBlockerOpen).
	blockers := map[int64][]store.IssueBlocker{
		2: {{BlockedID: 2, BlockerID: 1, BlockerKey: "TEST-1", BlockerState: model.StateTodo}},
		4: {{BlockedID: 4, BlockerID: 3, BlockerKey: "TEST-3", BlockerState: model.StateDone}},
	}
	f := &fakeClient{repo: repo, issues: issues, blockers: blockers}
	cards, err := Assemble(context.Background(), f, repo, false)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	byKey := map[string]BoardCard{}
	for _, c := range cards {
		byKey[c.Key] = c
	}
	// Blocked card surfaces an open blocker.
	blocked := byKey["TEST-2"]
	if len(blocked.BlockedBy) != 1 {
		t.Fatalf("TEST-2 BlockedBy = %d, want 1", len(blocked.BlockedBy))
	}
	if blocked.BlockedBy[0].Key != "TEST-1" || blocked.BlockedBy[0].State != model.StateTodo {
		t.Errorf("TEST-2 BlockedBy[0] = %+v, want {TEST-1 todo}", blocked.BlockedBy[0])
	}
	// Blocker card carries no inbound edge, so its BlockedBy stays nil.
	if len(byKey["TEST-1"].BlockedBy) != 0 {
		t.Errorf("TEST-1 BlockedBy = %+v, want nil (it's the blocker, not the blocked)", byKey["TEST-1"].BlockedBy)
	}
	// A done blocker is filtered out — TEST-4 stays unblocked.
	if len(byKey["TEST-4"].BlockedBy) != 0 {
		t.Errorf("TEST-4 BlockedBy = %+v, want nil (its only blocker is in a done state)", byKey["TEST-4"].BlockedBy)
	}
	// Cards with no edges stay clean.
	if len(byKey["TEST-5"].BlockedBy) != 0 {
		t.Errorf("TEST-5 BlockedBy = %+v, want nil (no edges at all)", byKey["TEST-5"].BlockedBy)
	}
}

// TestAssembleSortsCompletedColumns covers the BACI-101 board sort:
// Done and Cancelled cards render newest-completed first (updated_at
// descending), while non-completed columns keep their incoming
// creation order.
func TestAssembleSortsCompletedColumns(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "TEST"}
	created := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)

	// Issues arrive in creation order (the store's prefix,number sort).
	// Done/Cancelled rows deliberately get updated_at values out of
	// creation order so the sort has something to do; the created_at
	// fallback row (TEST-7) has a zero updated_at.
	issues := []*model.Issue{
		// Todo column — order must be preserved.
		{Key: "TEST-1", State: model.StateTodo, Title: "todo a", CreatedAt: created},
		{Key: "TEST-2", State: model.StateTodo, Title: "todo b", CreatedAt: created},
		// Done column — closed out of creation order.
		{Key: "TEST-3", State: model.StateDone, Title: "done oldest",
			CreatedAt: created, UpdatedAt: time.Date(2026, 5, 5, 9, 0, 0, 0, time.UTC)},
		{Key: "TEST-4", State: model.StateDone, Title: "done newest",
			CreatedAt: created, UpdatedAt: time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)},
		{Key: "TEST-5", State: model.StateDone, Title: "done middle",
			CreatedAt: created, UpdatedAt: time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)},
		// Cancelled column.
		{Key: "TEST-6", State: model.StateCancelled, Title: "cancelled older",
			CreatedAt: created, UpdatedAt: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)},
		// created_at fallback — zero updated_at, recent created_at.
		{Key: "TEST-7", State: model.StateCancelled, Title: "cancelled newest via created_at",
			CreatedAt: time.Date(2026, 5, 15, 9, 0, 0, 0, time.UTC)},
	}
	f := &fakeClient{repo: repo, issues: issues}
	cards, err := Assemble(context.Background(), f, repo, false)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	column := func(col model.State) []string {
		var keys []string
		for _, c := range cards {
			if model.State(c.Column) == col {
				keys = append(keys, c.Key)
			}
		}
		return keys
	}
	eq := func(name string, got, want []string) {
		if len(got) != len(want) {
			t.Fatalf("%s column = %v, want %v", name, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s column = %v, want %v", name, got, want)
			}
		}
	}

	// Done: newest updated_at first.
	eq("Done", column(model.StateDone), []string{"TEST-4", "TEST-5", "TEST-3"})
	// Cancelled: TEST-7's created_at (May 15) beats TEST-6's updated_at (May 3).
	eq("Cancelled", column(model.StateCancelled), []string{"TEST-7", "TEST-6"})
	// Todo: untouched creation order.
	eq("Todo", column(model.StateTodo), []string{"TEST-1", "TEST-2"})
}
