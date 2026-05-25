package boardcards

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
	// evalCounts / transcriptCounts (BACI-141) drive the per-card
	// combined eval/transcript indicator. Keyed by issue id, same
	// shape as the store helpers.
	evalCounts       map[int64]int
	transcriptCounts map[int64]int
	// inflightByMode (BACI-145) is mode → count of in-flight dispatches
	// for the test's single repo. Nil leaves the map empty (no
	// concurrency-cap blocking).
	inflightByMode map[model.DispatchMode]int
	// dispatchAware (BACI-132) opts the fake into filtering the
	// returned todos by the pair's IssueKey and DispatchID — needed
	// for the per-dispatch scope test. Existing tests that pass the
	// todos map verbatim leave it false and the fake returns
	// everything (the assembler's per-row issue_key match still
	// catches the cross-card cases).
	dispatchAware bool
	// lastIssueFilter (BACI-177) captures the IssueFilter the
	// assembler passed to ListIssues so a test can pin the
	// HiddenFeatureSlugs threading without spinning up a real store.
	lastIssueFilter *client.IssueFilter
}

func (f *fakeClient) ListRepos(context.Context) ([]*model.Repo, error) {
	if f.repo == nil {
		return nil, nil
	}
	return []*model.Repo{f.repo}, nil
}
func (f *fakeClient) ListIssues(_ context.Context, filter client.IssueFilter) ([]*model.Issue, error) {
	// Stash the filter so BACI-177 / similar threading tests can
	// assert which slugs Assemble forwarded.
	captured := filter
	f.lastIssueFilter = &captured
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
func (f *fakeClient) CountEvalCommentsByIssue(context.Context, []int64) (map[int64]int, error) {
	if f.evalCounts == nil {
		return map[int64]int{}, nil
	}
	return f.evalCounts, nil
}
func (f *fakeClient) CountTranscriptDocsByIssue(context.Context, []int64) (map[int64]int, error) {
	if f.transcriptCounts == nil {
		return map[int64]int{}, nil
	}
	return f.transcriptCounts, nil
}
func (f *fakeClient) InflightByModeForRepo(context.Context, *model.Repo) (map[model.DispatchMode]int, error) {
	if f.inflightByMode == nil {
		return map[model.DispatchMode]int{}, nil
	}
	return f.inflightByMode, nil
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

	cards, err := Assemble(context.Background(), f, repo, false, nil)
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
	cards, err := Assemble(context.Background(), f, repo, false, nil)
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

	cards, err := Assemble(context.Background(), f, repo, false, nil)
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
	cards, err := Assemble(context.Background(), f, repo, false, nil)
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
	cards, err := Assemble(context.Background(), f, repo, false, nil)
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
	cards, err := Assemble(context.Background(), f, repo, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !cards[0].Taken || cards[0].ActiveVerb != "" || cards[0].TodosTotal != 0 {
		t.Errorf("got Taken=%v Verb=%q Total=%d, want Taken=true Verb=\"\" Total=0", cards[0].Taken, cards[0].ActiveVerb, cards[0].TodosTotal)
	}
}

// TestCompletionSortKey covers the BACI-138 ordering key:
// terminal_at when set (the source of truth post-BACI-138), else
// updated_at, else created_at as the final fallback. The
// terminal_at-wins case is the load-bearing one — a stray tag or
// title edit on a closed issue bumps updated_at but must NOT
// reshuffle the card, because terminal_at stayed put.
func TestCompletionSortKey(t *testing.T) {
	created := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC)
	terminal := time.Date(2026, 5, 5, 9, 0, 0, 0, time.UTC)

	// Terminal beats updated. The stray-edit scenario: updated_at is
	// newer (a tag edit landed after close), terminal_at is older
	// (the actual close time). The sort key must follow terminal_at.
	withTerminal := &model.Issue{CreatedAt: created, UpdatedAt: updated, TerminalAt: &terminal}
	if got := CompletionSortKey(withTerminal); !got.Equal(terminal) {
		t.Errorf("CompletionSortKey with terminal_at = %v, want %v (terminal wins over newer updated)", got, terminal)
	}
	// terminal_at nil → fall back to updated_at (pre-BACI-138 row).
	withUpdated := &model.Issue{CreatedAt: created, UpdatedAt: updated}
	if got := CompletionSortKey(withUpdated); !got.Equal(updated) {
		t.Errorf("CompletionSortKey with updated_at = %v, want %v", got, updated)
	}
	// terminal_at nil + updated_at zero → fall back to created_at.
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
	cards, err := Assemble(context.Background(), f, repo, false, nil)
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

// TestAssembleWaitingState (BACI-145, absorbing the BACI-130
// delivered-flag check) locks in the per-issue WaitingState
// derivation that drives every spinner-bearing surface:
//   - Pending dispatches surface a queued_no_agent state (the matcher
//     hasn't bound them yet; no concurrency cap on `plan`).
//   - Delivered dispatches surface a delivered state (the worker has
//     the Task in hand; the spinner-cancel button disappears).
//   - Cancelled / acked rows are skipped — they're not the "active"
//     dispatch and WaitingForClaim is false anyway, so WaitingState
//     stays nil.
//   - An issue without WaitingForClaim never surfaces a state.
func TestAssembleWaitingState(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "TEST"}
	t0 := time.Date(2026, 5, 24, 9, 0, 0, 0, time.UTC)
	issueIDs := struct{ pending, delivered, cancelled, acked, none int64 }{
		pending:   101,
		delivered: 102,
		cancelled: 103,
		acked:     104,
		none:      105,
	}
	issues := []*model.Issue{
		{ID: issueIDs.pending, RepoID: repo.ID, Key: "TEST-1", State: model.StateTodo, Title: "pending dispatch", WaitingForClaim: true},
		{ID: issueIDs.delivered, RepoID: repo.ID, Key: "TEST-2", State: model.StateTodo, Title: "delivered dispatch", WaitingForClaim: true},
		{ID: issueIDs.cancelled, RepoID: repo.ID, Key: "TEST-3", State: model.StateTodo, Title: "only cancelled dispatch", WaitingForClaim: false},
		{ID: issueIDs.acked, RepoID: repo.ID, Key: "TEST-4", State: model.StateTodo, Title: "only acked dispatch", WaitingForClaim: false},
		{ID: issueIDs.none, RepoID: repo.ID, Key: "TEST-5", State: model.StateTodo, Title: "no dispatch"},
	}
	pendingID := issueIDs.pending
	deliveredID := issueIDs.delivered
	cancelledID := issueIDs.cancelled
	ackedID := issueIDs.acked
	// RepoDispatches is newest-first. Mimic that here: each fake
	// dispatch row gets a CreatedAt that's older for older entries.
	dispatches := []*model.AgentDispatch{
		// TEST-2: the newest row is delivered.
		{ID: 200, IssueID: &deliveredID, IssueKey: "TEST-2", Mode: model.DispatchMode("ship"), Status: model.DispatchDelivered, CreatedAt: t0},
		// TEST-1: pending → queued_no_agent (no concurrency cap on `plan`).
		{ID: 201, IssueID: &pendingID, IssueKey: "TEST-1", Mode: model.DispatchMode("plan"), Status: model.DispatchPending, CreatedAt: t0.Add(-1 * time.Minute)},
		// TEST-3: a cancelled row is not "active".
		{ID: 202, IssueID: &cancelledID, IssueKey: "TEST-3", Mode: model.DispatchMode("plan"), Status: model.DispatchCancelled, CreatedAt: t0.Add(-2 * time.Minute)},
		// TEST-4: an acked row is not "active" either.
		{ID: 203, IssueID: &ackedID, IssueKey: "TEST-4", Mode: model.DispatchMode("plan"), Status: model.DispatchAcked, CreatedAt: t0.Add(-3 * time.Minute)},
	}
	// Templates: `ship` has a concurrency cap of 1; `plan` has none.
	// The cap doesn't trip here (only one delivered ship — the dispatch
	// itself counts toward the cap, but the deriver treats `delivered`
	// as already-bound, not blocked).
	templates := []*store.PromptTemplate{
		{Slug: "plan", Name: "Planning", ActionLabel: "Plan"},
		{Slug: "ship", Name: "Shipping", ActionLabel: "Ship it", ConcurrencyLimit: 1},
	}
	f := &fakeClient{repo: repo, issues: issues, dispatches: dispatches, templates: templates}
	cards, err := Assemble(context.Background(), f, repo, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	byKey := map[string]BoardCard{}
	for _, c := range cards {
		byKey[c.Key] = c
	}
	// TEST-1: pending plan → queued_no_agent.
	if ws := byKey["TEST-1"].WaitingState; ws == nil || ws.Kind != WaitingQueuedNoAgent {
		t.Errorf("TEST-1 WaitingState = %+v, want queued_no_agent", ws)
	}
	// TEST-2: delivered ship → delivered, with the ship action label.
	if ws := byKey["TEST-2"].WaitingState; ws == nil || ws.Kind != WaitingDelivered || ws.ActionLabel != "Ship it" {
		t.Errorf("TEST-2 WaitingState = %+v, want delivered (Ship it)", ws)
	}
	// TEST-3 / TEST-4 / TEST-5: WaitingForClaim is false (or no
	// dispatch) — WaitingState must be nil so the card renders no
	// spinner.
	for _, k := range []string{"TEST-3", "TEST-4", "TEST-5"} {
		if ws := byKey[k].WaitingState; ws != nil {
			t.Errorf("%s WaitingState = %+v, want nil (WaitingForClaim is false)", k, ws)
		}
	}
}

// TestAssembleWaitingStateBlockedByCap (BACI-145) — when the per-(repo,
// mode) inflight count hits the template's concurrency_limit, a queued
// dispatch in that mode surfaces queued_blocked, naming the mode.
// Default cap of 0 (unlimited) must NOT trip this branch.
func TestAssembleWaitingStateBlockedByCap(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "TEST"}
	t0 := time.Date(2026, 5, 24, 9, 0, 0, 0, time.UTC)
	queuedID := int64(201)
	uncappedID := int64(202)
	issues := []*model.Issue{
		{ID: queuedID, RepoID: repo.ID, Key: "TEST-1", State: model.StateTodo, Title: "ship queued, cap hit", WaitingForClaim: true},
		{ID: uncappedID, RepoID: repo.ID, Key: "TEST-2", State: model.StateTodo, Title: "plan queued, no cap", WaitingForClaim: true},
	}
	dispatches := []*model.AgentDispatch{
		{ID: 300, IssueID: &queuedID, IssueKey: "TEST-1", Mode: model.DispatchMode("ship"), Status: model.DispatchQueued, CreatedAt: t0},
		{ID: 301, IssueID: &uncappedID, IssueKey: "TEST-2", Mode: model.DispatchMode("plan"), Status: model.DispatchQueued, CreatedAt: t0.Add(-1 * time.Minute)},
	}
	templates := []*store.PromptTemplate{
		{Slug: "plan", Name: "Planning", ActionLabel: "Plan"},
		{Slug: "ship", Name: "Shipping", ActionLabel: "Ship it", ConcurrencyLimit: 1},
	}
	// Ship is at its cap (1 in flight); plan is uncapped (cap=0).
	inflight := map[model.DispatchMode]int{
		model.DispatchMode("ship"): 1,
		model.DispatchMode("plan"): 3, // count is irrelevant when limit==0
	}
	f := &fakeClient{
		repo: repo, issues: issues, dispatches: dispatches, templates: templates,
		inflightByMode: inflight,
	}
	cards, err := Assemble(context.Background(), f, repo, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	byKey := map[string]BoardCard{}
	for _, c := range cards {
		byKey[c.Key] = c
	}
	if ws := byKey["TEST-1"].WaitingState; ws == nil || ws.Kind != WaitingQueuedBlocked || ws.ActionLabel != "Ship it" {
		t.Errorf("TEST-1 WaitingState = %+v, want queued_blocked (Ship it)", ws)
	}
	// The "unlimited" cap (limit == 0) must NOT trip the blocked
	// branch — otherwise every queued dispatch would surface as
	// blocked, which would be wrong.
	if ws := byKey["TEST-2"].WaitingState; ws == nil || ws.Kind != WaitingQueuedNoAgent {
		t.Errorf("TEST-2 WaitingState = %+v, want queued_no_agent (cap=0 ⇒ unlimited)", ws)
	}
}

// TestDeriveWaitingStateUnits exercises the pure deriver outside the
// Assemble loop so the cap-checking and label-resolution rules are
// pinned without dragging in the full client surface.
func TestDeriveWaitingStateUnits(t *testing.T) {
	t0 := time.Date(2026, 5, 24, 9, 0, 0, 0, time.UTC)
	templates := []*store.PromptTemplate{
		{Slug: "plan", Name: "Planning", ActionLabel: "Plan"},
		{Slug: "ship", Name: "Shipping", ActionLabel: "Ship it", ConcurrencyLimit: 1},
	}
	// Not waiting at all → nil.
	if got := DeriveWaitingState(&model.Issue{}, &model.AgentDispatch{}, nil, templates); got != nil {
		t.Errorf("not-waiting issue: got %+v, want nil", got)
	}
	// Waiting but no active dispatch (race window) → no_agent fallback.
	if got := DeriveWaitingState(&model.Issue{WaitingForClaim: true}, nil, nil, templates); got == nil || got.Kind != WaitingQueuedNoAgent {
		t.Errorf("waiting + nil active: got %+v, want queued_no_agent", got)
	}
	// Delivered.
	got := DeriveWaitingState(&model.Issue{WaitingForClaim: true},
		&model.AgentDispatch{Mode: "ship", Status: model.DispatchDelivered, CreatedAt: t0},
		nil, templates)
	if got == nil || got.Kind != WaitingDelivered || got.ActionLabel != "Ship it" {
		t.Errorf("delivered ship: got %+v, want delivered/Ship it", got)
	}
	// Queued + cap hit.
	got = DeriveWaitingState(&model.Issue{WaitingForClaim: true},
		&model.AgentDispatch{Mode: "ship", Status: model.DispatchQueued, CreatedAt: t0},
		map[model.DispatchMode]int{"ship": 1}, templates)
	if got == nil || got.Kind != WaitingQueuedBlocked {
		t.Errorf("queued + cap hit: got %+v, want queued_blocked", got)
	}
	// Queued + cap not hit.
	got = DeriveWaitingState(&model.Issue{WaitingForClaim: true},
		&model.AgentDispatch{Mode: "ship", Status: model.DispatchQueued, CreatedAt: t0},
		map[model.DispatchMode]int{"ship": 0}, templates)
	if got == nil || got.Kind != WaitingQueuedNoAgent {
		t.Errorf("queued + cap clear: got %+v, want queued_no_agent", got)
	}
}

// TestWaitingStateLabel pins the user-facing wording in lockstep with
// the TS-side mirror in lib/waitingLabels.ts. The TS file MUST stay in
// sync — changing one and not the other surfaces as a label mismatch
// between the kanban and the issue lock banner.
func TestWaitingStateLabel(t *testing.T) {
	cases := []struct {
		name string
		ws   *WaitingState
		want string
	}{
		{"nil", nil, ""},
		{"no_agent", &WaitingState{Kind: WaitingQueuedNoAgent}, "Waiting for an available agent"},
		{"blocked", &WaitingState{Kind: WaitingQueuedBlocked, ActionLabel: "Ship it"}, "Waiting on Ship it job to finish"},
		{"blocked_unlabeled", &WaitingState{Kind: WaitingQueuedBlocked}, "Waiting on prior job to finish"},
		{"delivered", &WaitingState{Kind: WaitingDelivered, ActionLabel: "Ship it"}, "Worker has the Ship it job"},
		{"delivered_unlabeled", &WaitingState{Kind: WaitingDelivered}, "Worker is on this job"},
	}
	for _, tc := range cases {
		if got := WaitingStateLabel(tc.ws); got != tc.want {
			t.Errorf("%s: WaitingStateLabel = %q, want %q", tc.name, got, tc.want)
		}
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
	cards, err := Assemble(context.Background(), f, repo, false, nil)
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

// TestAssembleSortsCompletedColumnsByTerminalAt — BACI-138. When
// terminal_at is populated (the post-BACI-138 production path), the
// sort uses it instead of updated_at. The crucial assertion: a row
// whose updated_at is *newer* (because a stray tag / title edit
// landed after close) but whose terminal_at is *older* must NOT jump
// to the top of the column. That was the BACI-101 proxy's failure
// mode that the BACI-138 brief flagged.
func TestAssembleSortsCompletedColumnsByTerminalAt(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "TEST"}
	created := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	mk := func(t time.Time) *time.Time { return &t }

	// Done column: three rows, each with a distinct terminal_at.
	// TEST-3 was closed on May 5 but then got a tag edit on May 25
	// (updated_at). The proxy-era sort would have put TEST-3 at the
	// top — it must NOT, because terminal_at says May 5.
	issues := []*model.Issue{
		{Key: "TEST-1", State: model.StateDone, Title: "done newest",
			CreatedAt: created,
			UpdatedAt:  time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC),
			TerminalAt: mk(time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC))},
		{Key: "TEST-2", State: model.StateDone, Title: "done middle",
			CreatedAt: created,
			UpdatedAt:  time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC),
			TerminalAt: mk(time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC))},
		{Key: "TEST-3", State: model.StateDone, Title: "done oldest (recent updated_at)",
			CreatedAt: created,
			// Newer updated_at — would jump to top under the old proxy.
			UpdatedAt:  time.Date(2026, 5, 25, 9, 0, 0, 0, time.UTC),
			TerminalAt: mk(time.Date(2026, 5, 5, 9, 0, 0, 0, time.UTC))},
	}
	f := &fakeClient{repo: repo, issues: issues}
	cards, err := Assemble(context.Background(), f, repo, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	var done []string
	for _, c := range cards {
		if model.State(c.Column) == model.StateDone {
			done = append(done, c.Key)
		}
	}
	want := []string{"TEST-1", "TEST-2", "TEST-3"}
	if len(done) != len(want) {
		t.Fatalf("Done column = %v, want %v", done, want)
	}
	for i := range want {
		if done[i] != want[i] {
			t.Fatalf("Done column = %v, want %v (TEST-3's stray edit must not jump it to the top)", done, want)
		}
	}
}

// TestAssembleTerminalAtThreaded — BACI-187. The shipping-log topbar
// pill derives its "last 7 days of done" count client-side from the
// already-polled `cards` array, so BoardCard must carry the issue's
// terminal_at through. A done card with terminal_at set surfaces it on
// the wire; an open card with terminal_at nil leaves the field absent
// (omitempty drops it).
func TestAssembleTerminalAtThreaded(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "TEST"}
	stamp := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	issues := []*model.Issue{
		{Key: "TEST-1", State: model.StateDone, Title: "done", TerminalAt: &stamp},
		{Key: "TEST-2", State: model.StateTodo, Title: "open"},
	}
	f := &fakeClient{repo: repo, issues: issues}
	cards, err := Assemble(context.Background(), f, repo, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	byKey := map[string]BoardCard{}
	for _, c := range cards {
		byKey[c.Key] = c
	}
	done := byKey["TEST-1"]
	if done.TerminalAt == nil {
		t.Fatalf("TEST-1 TerminalAt = nil, want %v", stamp)
	}
	if !done.TerminalAt.Equal(stamp) {
		t.Errorf("TEST-1 TerminalAt = %v, want %v", *done.TerminalAt, stamp)
	}
	open := byKey["TEST-2"]
	if open.TerminalAt != nil {
		t.Errorf("TEST-2 TerminalAt = %v, want nil (open card)", *open.TerminalAt)
	}
}

// TestDescriptionExcerpt (BACI-171) pins the per-card excerpt
// truncation the ActivityTray feeds into its entry rows: empty
// descriptions stay empty (so the BoardCard's omitempty drops the
// field), short bodies pass through verbatim with internal whitespace
// collapsed, long bodies cut on the nearest preceding word boundary
// with a trailing "…", and a multi-byte unicode body counts runes
// (not bytes) so the cap can't split a glyph.
func TestDescriptionExcerpt(t *testing.T) {
	// Build a body that comfortably exceeds the 140-rune cap and
	// guarantees a word boundary near the end so the trim is exercised.
	longWords := strings.Repeat("alpha beta gamma delta ", 20) // ~460 chars
	gotLong := descriptionExcerpt(longWords)
	if !strings.HasSuffix(gotLong, "…") {
		t.Errorf("long body excerpt = %q, want trailing ellipsis", gotLong)
	}
	// The truncation result must be no longer than the cap + 1 (the
	// appended ellipsis) and must end on a word, not mid-word.
	runeLen := utf8.RuneCountInString(gotLong)
	if runeLen > descriptionExcerptRunes+1 {
		t.Errorf("long body excerpt is %d runes, want ≤ %d", runeLen, descriptionExcerptRunes+1)
	}
	withoutEllipsis := strings.TrimSuffix(gotLong, "…")
	if strings.HasSuffix(withoutEllipsis, " ") {
		t.Errorf("long body excerpt left a trailing space before the ellipsis: %q", gotLong)
	}
	// Cut should land on a complete word: the trimmed body must end
	// with one of the input's known tokens, not a fragment.
	knownTails := []string{"alpha", "beta", "gamma", "delta"}
	matched := false
	for _, tail := range knownTails {
		if strings.HasSuffix(withoutEllipsis, tail) {
			matched = true
			break
		}
	}
	if !matched {
		t.Errorf("long body excerpt %q didn't end on a known word boundary", gotLong)
	}

	// Short body — within the cap, passes through verbatim with
	// internal whitespace collapsed and no trailing ellipsis.
	short := "Login broken on Safari\nrepro inline."
	gotShort := descriptionExcerpt(short)
	want := "Login broken on Safari repro inline."
	if gotShort != want {
		t.Errorf("short body excerpt = %q, want %q", gotShort, want)
	}

	// Empty body — empty string out so omitempty drops the wire field.
	if got := descriptionExcerpt(""); got != "" {
		t.Errorf("empty body excerpt = %q, want empty string", got)
	}
	// Whitespace-only body — collapses to nothing, same as empty.
	if got := descriptionExcerpt("   \n\t\n"); got != "" {
		t.Errorf("whitespace-only body excerpt = %q, want empty string", got)
	}

	// Multi-byte runes — a body of 200 emoji is well over the cap.
	// Each emoji is multiple bytes but exactly one rune, so the rune
	// counter must trip the cap correctly without splitting a glyph.
	emoji := strings.Repeat("🍕", 200)
	gotEmoji := descriptionExcerpt(emoji)
	emojiRunes := utf8.RuneCountInString(gotEmoji)
	if emojiRunes > descriptionExcerptRunes+1 {
		t.Errorf("emoji excerpt is %d runes, want ≤ %d", emojiRunes, descriptionExcerptRunes+1)
	}
	if !strings.HasSuffix(gotEmoji, "…") {
		t.Errorf("emoji excerpt = %q, want trailing ellipsis", gotEmoji)
	}
	// Every rune (except the trailing "…") must still be a pizza —
	// proves no mid-rune split happened.
	for _, r := range strings.TrimSuffix(gotEmoji, "…") {
		if r != '🍕' {
			t.Errorf("emoji excerpt contained an unexpected rune %q in %q", r, gotEmoji)
			break
		}
	}
}

// TestAssembleDescriptionExcerpt (BACI-171) covers the per-card
// surfacing of DescriptionExcerpt through Assemble: a card with no
// description gets an empty excerpt, a short description flows
// through verbatim (with whitespace collapsed), and a long
// description is truncated.
func TestAssembleDescriptionExcerpt(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "TEST"}
	longBody := strings.Repeat("alpha beta gamma delta ", 20)
	issues := []*model.Issue{
		{ID: 1, Key: "TEST-1", State: model.StateTodo, Title: "no description"},
		{ID: 2, Key: "TEST-2", State: model.StateTodo, Title: "short", Description: "One-liner repro."},
		{ID: 3, Key: "TEST-3", State: model.StateTodo, Title: "long", Description: longBody},
	}
	f := &fakeClient{repo: repo, issues: issues}
	cards, err := Assemble(context.Background(), f, repo, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	byKey := map[string]BoardCard{}
	for _, c := range cards {
		byKey[c.Key] = c
	}
	if got := byKey["TEST-1"].DescriptionExcerpt; got != "" {
		t.Errorf("TEST-1 excerpt = %q, want empty string (omitempty wire elision)", got)
	}
	if got := byKey["TEST-2"].DescriptionExcerpt; got != "One-liner repro." {
		t.Errorf("TEST-2 excerpt = %q, want verbatim short body", got)
	}
	gotLong := byKey["TEST-3"].DescriptionExcerpt
	if !strings.HasSuffix(gotLong, "…") {
		t.Errorf("TEST-3 excerpt = %q, want trailing ellipsis", gotLong)
	}
	if utf8.RuneCountInString(gotLong) > descriptionExcerptRunes+1 {
		t.Errorf("TEST-3 excerpt is %d runes, want ≤ %d", utf8.RuneCountInString(gotLong), descriptionExcerptRunes+1)
	}
}

// TestAssembleTranscriptAndEvalCounts (BACI-141) covers the new
// per-card counts: a taken card surfaces them, an untaken card with
// only eval/transcript data still surfaces them (the whole point of
// the ticket is making this material visible after the agent has
// released), and a card with neither stays at zero values.
func TestAssembleTranscriptAndEvalCounts(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "TEST"}
	t0 := time.Date(2026, 5, 25, 9, 0, 0, 0, time.UTC)
	sess := &model.AgentSession{ID: 30, SessionID: "sess-e", RepoID: repo.ID, RepoPrefix: repo.Prefix}
	issues := []*model.Issue{
		{ID: 100, Key: "TEST-100", State: model.StateInProgress, Title: "taken with both indicators"},
		{ID: 101, Key: "TEST-101", State: model.StateTodo, Title: "untaken but still has eval / transcript"},
		{ID: 102, Key: "TEST-102", State: model.StateTodo, Title: "no indicators"},
	}
	claims := []*model.AgentClaim{
		{SessionID: "sess-e", SessionPK: 30, IssueKey: "TEST-100", ClaimedAt: t0},
	}
	evalCounts := map[int64]int{
		100: 3,
		101: 1,
	}
	transcriptCounts := map[int64]int{
		100: 2,
		101: 1,
	}
	f := &fakeClient{
		repo: repo, issues: issues, claims: claims,
		sessions: []*model.AgentSession{sess},
		evalCounts: evalCounts, transcriptCounts: transcriptCounts,
	}
	cards, err := Assemble(context.Background(), f, repo, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	byKey := map[string]BoardCard{}
	for _, c := range cards {
		byKey[c.Key] = c
	}
	taken := byKey["TEST-100"]
	if taken.EvalCommentCount != 3 || taken.TranscriptDocCount != 2 {
		t.Errorf("TEST-100 counts = (eval=%d, transcript=%d), want (3, 2)",
			taken.EvalCommentCount, taken.TranscriptDocCount)
	}
	released := byKey["TEST-101"]
	if released.Taken {
		t.Errorf("TEST-101 Taken = true, want false (no open claim)")
	}
	if released.EvalCommentCount != 1 || released.TranscriptDocCount != 1 {
		t.Errorf("TEST-101 (untaken) counts = (eval=%d, transcript=%d), want (1, 1) — the whole point of BACI-141 is surfacing these even when not taken",
			released.EvalCommentCount, released.TranscriptDocCount)
	}
	empty := byKey["TEST-102"]
	if empty.EvalCommentCount != 0 || empty.TranscriptDocCount != 0 {
		t.Errorf("TEST-102 counts = (eval=%d, transcript=%d), want (0, 0)",
			empty.EvalCommentCount, empty.TranscriptDocCount)
	}
}

// TestAssembleHidesFeatureCards (BACI-177) pins that the
// hiddenFeatureSlugs parameter Assemble accepts is threaded straight
// into the IssueFilter handed to ListIssues. The actual row filtering
// is the store's responsibility (TestStore_ListIssues_HiddenFeatureSlugsFilter
// pins that), so this layer just verifies the wiring.
func TestAssembleHidesFeatureCards(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "TEST", Name: "test"}
	// No issues required — the threading assertion is on the filter,
	// not the cards array.
	f := &fakeClient{repo: repo}

	hidden := []string{"auth", "ops"}
	if _, err := Assemble(context.Background(), f, repo, false, hidden); err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if f.lastIssueFilter == nil {
		t.Fatalf("ListIssues was never called")
	}
	if !stringSliceEqual(f.lastIssueFilter.HiddenFeatureSlugs, hidden) {
		t.Fatalf("HiddenFeatureSlugs threaded as %v, want %v",
			f.lastIssueFilter.HiddenFeatureSlugs, hidden)
	}

	// nil slice → filter carries nil too (and the store-side WHERE
	// shortcuts on len == 0).
	f2 := &fakeClient{repo: repo}
	if _, err := Assemble(context.Background(), f2, repo, false, nil); err != nil {
		t.Fatalf("Assemble (nil): %v", err)
	}
	if f2.lastIssueFilter == nil {
		t.Fatalf("ListIssues was never called (nil case)")
	}
	if len(f2.lastIssueFilter.HiddenFeatureSlugs) != 0 {
		t.Fatalf("nil hidden slice: filter carried %v, want empty",
			f2.lastIssueFilter.HiddenFeatureSlugs)
	}
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestAssembleSurfacesFollowOnDispatch (BACI-182) pins the in-memory
// follow-on deriver: dormant rows (status=queued AND
// QueuedAfterDispatchID != nil) surface on the BoardCard.FollowOnDispatch
// field with the right mode + action label; issues without a dormant
// row leave the field nil; on an issue with multiple queued rows, only
// the one carrying the dormant link wins.
func TestAssembleSurfacesFollowOnDispatch(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "TEST"}
	t0 := time.Date(2026, 5, 25, 9, 0, 0, 0, time.UTC)
	happyID := int64(101)
	noneID := int64(102)
	multiID := int64(103)
	parentID := int64(900) // shared notional parent for the dormant rows
	issues := []*model.Issue{
		{ID: happyID, RepoID: repo.ID, Key: "TEST-1", State: model.StateInProgress, Title: "follow-on queued"},
		{ID: noneID, RepoID: repo.ID, Key: "TEST-2", State: model.StateInProgress, Title: "no follow-on"},
		{ID: multiID, RepoID: repo.ID, Key: "TEST-3", State: model.StateInProgress, Title: "two queued, one dormant"},
	}
	// RepoDispatches is newest-first; older entries get an older
	// CreatedAt so the deriver's "first hit wins" loop walks newest
	// first.
	dispatches := []*model.AgentDispatch{
		// TEST-1: pending parent + dormant follow-on (implement).
		{ID: 200, IssueID: &happyID, IssueKey: "TEST-1", Mode: model.DispatchMode("implement"), Status: model.DispatchQueued, CreatedAt: t0, QueuedAfterDispatchID: &parentID},
		{ID: 201, IssueID: &happyID, IssueKey: "TEST-1", Mode: model.DispatchMode("plan"), Status: model.DispatchPending, CreatedAt: t0.Add(-1 * time.Minute)},
		// TEST-2: just a pending parent — no dormant row attached.
		{ID: 202, IssueID: &noneID, IssueKey: "TEST-2", Mode: model.DispatchMode("plan"), Status: model.DispatchPending, CreatedAt: t0.Add(-2 * time.Minute)},
		// TEST-3: two queued rows; only the newer one is dormant (has
		// QueuedAfterDispatchID set). The older queued row is a plain
		// queued dispatch waiting for the matcher — the deriver must
		// skip it.
		{ID: 203, IssueID: &multiID, IssueKey: "TEST-3", Mode: model.DispatchMode("ship"), Status: model.DispatchQueued, CreatedAt: t0.Add(-3 * time.Minute), QueuedAfterDispatchID: &parentID},
		{ID: 204, IssueID: &multiID, IssueKey: "TEST-3", Mode: model.DispatchMode("plan"), Status: model.DispatchQueued, CreatedAt: t0.Add(-4 * time.Minute)},
	}
	templates := []*store.PromptTemplate{
		{Slug: "plan", Name: "Planning", ActionLabel: "Plan"},
		{Slug: "implement", Name: "Implementing", ActionLabel: "Implement"},
		{Slug: "ship", Name: "Shipping", ActionLabel: "Ship it"},
	}
	f := &fakeClient{repo: repo, issues: issues, dispatches: dispatches, templates: templates}
	cards, err := Assemble(context.Background(), f, repo, false, nil)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	byKey := map[string]BoardCard{}
	for _, c := range cards {
		byKey[c.Key] = c
	}
	// TEST-1: dormant follow-on surfaces with the implement mode + label.
	if fo := byKey["TEST-1"].FollowOnDispatch; fo == nil {
		t.Errorf("TEST-1 FollowOnDispatch = nil, want non-nil (dormant row present)")
	} else {
		if fo.Mode != model.DispatchMode("implement") {
			t.Errorf("TEST-1 FollowOnDispatch.Mode = %q, want implement", fo.Mode)
		}
		if fo.ActionLabel != "Implement" {
			t.Errorf("TEST-1 FollowOnDispatch.ActionLabel = %q, want Implement", fo.ActionLabel)
		}
		if fo.DispatchID != 200 {
			t.Errorf("TEST-1 FollowOnDispatch.DispatchID = %d, want 200", fo.DispatchID)
		}
	}
	// TEST-2: no dormant row — field must stay nil.
	if fo := byKey["TEST-2"].FollowOnDispatch; fo != nil {
		t.Errorf("TEST-2 FollowOnDispatch = %+v, want nil (no dormant row)", fo)
	}
	// TEST-3: two queued rows; only the dormant one (ID 203, ship) wins.
	if fo := byKey["TEST-3"].FollowOnDispatch; fo == nil {
		t.Errorf("TEST-3 FollowOnDispatch = nil, want the dormant row")
	} else if fo.DispatchID != 203 || fo.Mode != model.DispatchMode("ship") {
		t.Errorf("TEST-3 FollowOnDispatch = {ID=%d, Mode=%q}, want {ID=203, Mode=ship} — the non-dormant queued row leaked through", fo.DispatchID, fo.Mode)
	}
}
