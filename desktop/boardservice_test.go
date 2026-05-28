package main

import (
	"context"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
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

// InflightByModeBaseForRepo (BACI-227) is the per-(mode, branch)
// sibling — the kanban / brief deriver reads this map to enforce the
// per-branch concurrency gate. The taken-flag tests don't exercise
// cap-blocked queued dispatches, so an empty map matches the
// "no in-flight rows" production case.
func (f *fakeBoardClient) InflightByModeBaseForRepo(context.Context, *model.Repo) (map[store.InflightKey]int, error) {
	return map[store.InflightKey]int{}, nil
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

// LatestPlanByIssue (BACI-216) — boardcards.Assemble reads the
// per-issue latest-plan doc to surface the kanban card's plan icon.
// The taken-flag tests don't exercise that affordance, so an empty
// map matches the "no plans linked" production case. Drive-by fix
// for a pre-existing nil-pointer panic in the embedded-interface
// stub — see the BACI-221 PR description.
func (f *fakeBoardClient) LatestPlanByIssue(context.Context, []int64) (map[int64]*model.LatestPlan, error) {
	return map[int64]*model.LatestPlan{}, nil
}

// LatestPRByIssue (BACI-239) — boardcards.Assemble reads the per-issue
// latest-PR row to surface the kanban card's PR chip. Same
// "empty-map = no PRs attached" semantics as the LatestPlanByIssue
// stub above; the taken-flag tests don't exercise the affordance, so
// the empty production case is the right default.
func (f *fakeBoardClient) LatestPRByIssue(context.Context, []int64) (map[int64]*model.LatestPR, error) {
	return map[int64]*model.LatestPR{}, nil
}

// ListHiddenFeatureSlugs (BACI-177) — empty slice means "no features
// are hidden", which matches the production default and lets every
// taken-flag / waiting-state test pass cards through unchanged.
// BoardService.ListCards calls this once per repo to thread the set
// into boardcards.Assemble.
func (f *fakeBoardClient) ListHiddenFeatureSlugs(context.Context, *model.Repo) ([]string, error) {
	return nil, nil
}

// CreateIssue (BACI-166) — stub the client.Client.CreateIssue path the
// BoardService.AddIssue Wails seam takes. Append the new row to the
// fake's issues slice with the input's title/description so the
// subsequent boardcards.Assemble round-trip (which calls ListIssues
// again) sees the freshly-created card. Mirrors the "the store would
// have written this" semantics the real client.LocalImpl gives.
func (f *fakeBoardClient) CreateIssue(_ context.Context, _ *model.Repo, in inputs.IssueAddInput, _ bool) (*model.Issue, error) {
	state := model.StateTodo
	if in.State != "" {
		st, err := model.ParseState(in.State)
		if err != nil {
			return nil, err
		}
		state = st
	}
	iss := &model.Issue{
		ID:          int64(len(f.issues)) + 1,
		Key:         "TEST-99",
		State:       state,
		Title:       in.Title,
		Description: in.Description,
		Tags:        in.Tags,
	}
	f.issues = append(f.issues, iss)
	return iss, nil
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
// GetRepoByPrefix + ListShippedIssues + CountShippedIssues + ListPRs
// quartet the popover hits and nothing else.
type fakeShippedClient struct {
	client.Client
	repo    *model.Repo
	shipped []*model.Issue
	prs     map[string][]*model.PullRequest
	// total overrides the count returned by CountShippedIssues so a
	// test can exercise the "20 of 47" path without seeding 47 rows.
	// Zero falls back to len(shipped).
	total int
	// lastFilter / lastCountFilter capture the filter the production
	// code passed in so tests can assert "Forever" came through as a
	// nil Since rather than a 30-day cutoff (the pre-BACI-221 default).
	lastFilter      store.ShippedFilter
	lastCountFilter store.ShippedFilter
}

func (f *fakeShippedClient) GetRepoByPrefix(context.Context, string) (*model.Repo, error) {
	return f.repo, nil
}
func (f *fakeShippedClient) ListShippedIssues(_ context.Context, _ *model.Repo, sf store.ShippedFilter) ([]*model.Issue, error) {
	f.lastFilter = sf
	return f.shipped, nil
}
func (f *fakeShippedClient) CountShippedIssues(_ context.Context, _ *model.Repo, sf store.ShippedFilter) (int, error) {
	f.lastCountFilter = sf
	if f.total > 0 {
		return f.total, nil
	}
	return len(f.shipped), nil
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
	fake := &fakeShippedClient{repo: repo, shipped: shipped, prs: prs}
	svc := NewBoardService(fake)

	out, err := svc.ListShipped("TEST", 30, 20)
	if err != nil {
		t.Fatalf("ListShipped: %v", err)
	}
	rows := out.Rows
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
	if out.Total != 2 {
		t.Errorf("Total = %d, want 2 (fake fell back to len(shipped))", out.Total)
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

// TestListShippedReturnsTotal (BACI-221) — the wrapper exposes the
// total count under the scope independently of the per-fetch row count,
// so the popover can render "showing N of TOTAL".
func TestListShippedReturnsTotal(t *testing.T) {
	repo := &model.Repo{ID: 1, Prefix: "TEST"}
	stamp := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	shipped := []*model.Issue{
		{Key: "TEST-1", Title: "row", State: model.StateDone, TerminalAt: &stamp},
	}
	fake := &fakeShippedClient{repo: repo, shipped: shipped, total: 47}
	svc := NewBoardService(fake)

	out, err := svc.ListShipped("TEST", 7, 20)
	if err != nil {
		t.Fatalf("ListShipped: %v", err)
	}
	if len(out.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(out.Rows))
	}
	if out.Total != 47 {
		t.Fatalf("Total = %d, want 47 (count independent of fetched rows)", out.Total)
	}
}

// TestListShippedForeverPassesNilSince (BACI-221) — sinceDays==0 means
// "Forever", which must reach the store as a nil Since pointer (no
// cutoff) rather than a same-instant cutoff that would still exclude
// older rows on a slow clock.
func TestListShippedForeverPassesNilSince(t *testing.T) {
	fake := &fakeShippedClient{repo: &model.Repo{Prefix: "TEST"}}
	svc := NewBoardService(fake)
	if _, err := svc.ListShipped("TEST", 0, 0); err != nil {
		t.Fatalf("ListShipped: %v", err)
	}
	if fake.lastFilter.Since != nil {
		t.Fatalf("Since = %v, want nil for sinceDays=0 (Forever)", fake.lastFilter.Since)
	}
	if fake.lastCountFilter.Since != nil {
		t.Fatalf("count Since = %v, want nil for sinceDays=0", fake.lastCountFilter.Since)
	}
}

// TestCountShipped (BACI-221) — happy path: forwards the right filter
// to the client and returns the count verbatim.
func TestCountShipped(t *testing.T) {
	fake := &fakeShippedClient{repo: &model.Repo{Prefix: "TEST"}, total: 17}
	svc := NewBoardService(fake)

	total, err := svc.CountShipped("TEST", 7)
	if err != nil {
		t.Fatalf("CountShipped: %v", err)
	}
	if total != 17 {
		t.Fatalf("total = %d, want 17", total)
	}
	if fake.lastCountFilter.Since == nil {
		t.Fatalf("Since = nil, want non-nil for sinceDays=7")
	}
}

// TestCountShippedRejectsAllRepos — same per-repo gate as ListShipped.
func TestCountShippedRejectsAllRepos(t *testing.T) {
	svc := NewBoardService(&fakeShippedClient{repo: &model.Repo{Prefix: "TEST"}})
	if _, err := svc.CountShipped("", 0); err == nil {
		t.Error("CountShipped(\"\") = nil, want error (per-repo only)")
	}
	if _, err := svc.CountShipped("all", 0); err == nil {
		t.Error("CountShipped(\"all\") = nil, want error (per-repo only)")
	}
}

// TestBoardService_AddIssue covers the BACI-166 Wails seam — the React
// composer (+ button in the Topbar) calls AddIssue to create an issue
// before queuing the scope dispatch via the existing DispatchIssue
// path. The returned BoardCard must carry the new key, column, label,
// and propagated title so the optimistic prepend in App.jsx matches
// the shape ListCards would draw on the next poll.
func TestBoardService_AddIssue(t *testing.T) {
	svc := NewBoardService(&fakeBoardClient{
		repo:   &model.Repo{Prefix: "TEST"},
		issues: nil,
	})

	card, err := svc.AddIssue("TEST", "Login broken on Safari", "500 on submit", "")
	if err != nil {
		t.Fatalf("AddIssue: %v", err)
	}
	if card.Key != "TEST-99" {
		t.Errorf("Key = %q, want TEST-99", card.Key)
	}
	if card.Column != string(model.StateTodo) {
		t.Errorf("Column = %q, want %q", card.Column, model.StateTodo)
	}
	if card.ColumnLabel != "Todo" {
		t.Errorf("ColumnLabel = %q, want Todo", card.ColumnLabel)
	}
	if card.Title != "Login broken on Safari" {
		t.Errorf("Title = %q, want propagated", card.Title)
	}
	if card.Taken {
		t.Errorf("Taken = true on a fresh issue, want false")
	}
}

// TestBoardService_AddIssueRejectsAllRepos — the composer always has a
// real prefix in hand from activeBoard; the "all" pseudo-board has no
// concept of "which repo do I create in", so AddIssue must reject empty
// and "all" prefixes with a clear error.
func TestBoardService_AddIssueRejectsAllRepos(t *testing.T) {
	svc := NewBoardService(&fakeBoardClient{repo: &model.Repo{Prefix: "TEST"}})
	if _, err := svc.AddIssue("", "t", "", ""); err == nil {
		t.Error("AddIssue(\"\") = nil, want error (cross-repo not supported)")
	}
	if _, err := svc.AddIssue("all", "t", "", ""); err == nil {
		t.Error("AddIssue(\"all\") = nil, want error (cross-repo not supported)")
	}
}

// fakeDefaultFeatureClient stubs the per-repo default-feature path so
// the BoardService methods can be exercised without spinning up a real
// store. Embeds the BoardService fake so unused methods stay legal.
type fakeDefaultFeatureClient struct {
	fakeBoardClient
	current *model.Feature
}

func (f *fakeDefaultFeatureClient) GetDefaultFeature(context.Context, *model.Repo) (*model.Feature, error) {
	return f.current, nil
}
func (f *fakeDefaultFeatureClient) SetDefaultFeature(_ context.Context, _ *model.Repo, slug string, _ bool) (*model.Feature, error) {
	feat := &model.Feature{Slug: slug, Title: "title:" + slug, Emoji: "🪲"}
	f.current = feat
	return feat, nil
}
func (f *fakeDefaultFeatureClient) ClearDefaultFeature(context.Context, *model.Repo, bool) error {
	f.current = nil
	return nil
}

// TestBoardService_DefaultFeature_RoundTrip pins the BACI-235 Wails
// seam: empty when unset, populates on set, clears via the explicit
// verb. Slim DTO shape carries slug + title + emoji for the dropdown.
func TestBoardService_DefaultFeature_RoundTrip(t *testing.T) {
	fake := &fakeDefaultFeatureClient{fakeBoardClient: fakeBoardClient{repo: &model.Repo{Prefix: "TEST"}}}
	svc := NewBoardService(fake)

	// Unset → empty DTO.
	got, err := svc.GetDefaultFeature("TEST")
	if err != nil {
		t.Fatalf("Get (unset): %v", err)
	}
	if got.Slug != "" {
		t.Fatalf("Get (unset): Slug=%q, want empty", got.Slug)
	}

	// Set → returns the populated DTO.
	got, err = svc.SetDefaultFeature("TEST", "catchall")
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got.Slug != "catchall" {
		t.Fatalf("Set: Slug=%q, want catchall", got.Slug)
	}
	if got.Title == "" || got.Emoji == "" {
		t.Fatalf("Set: missing inflated fields (DTO=%+v)", got)
	}

	// Read it back via Get.
	got, err = svc.GetDefaultFeature("TEST")
	if err != nil {
		t.Fatalf("Get (after set): %v", err)
	}
	if got.Slug != "catchall" {
		t.Fatalf("Get (after set): Slug=%q, want catchall", got.Slug)
	}

	// Clear.
	got, err = svc.ClearDefaultFeature("TEST")
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if got.Slug != "" {
		t.Fatalf("Clear: Slug=%q, want empty", got.Slug)
	}
	got, _ = svc.GetDefaultFeature("TEST")
	if got.Slug != "" {
		t.Fatalf("post-clear Get: Slug=%q, want empty", got.Slug)
	}
}
