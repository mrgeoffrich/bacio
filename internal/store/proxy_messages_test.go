package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/anthropic"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// userMsg / asstTurn are tiny builders so the tests read as conversations.
func userMsg(text string) model.AnthropicMessage {
	return model.AnthropicMessage{Role: "user", Content: []model.AnthropicBlock{{Type: "text", Text: text}}}
}

func asstTurn(text string, in, out int64) model.AnthropicTurn {
	return model.AnthropicTurn{
		Model:  "claude-opus-4-8",
		Blocks: []model.AnthropicBlock{{Type: "text", Text: text}},
		Usage:  model.AnthropicUsage{InputTokens: in, OutputTokens: out},
	}
}

// TestAddProxyMessage_JobTranscript inserts a two-capture primary thread plus
// one auxiliary capture against a dispatch, then asserts JobTranscript assembles
// the ordered primary thread, sums usage, and keeps the auxiliary turn separate.
func TestAddProxyMessage_JobTranscript(t *testing.T) {
	s := newTestStore(t)
	dispatchID := int64(7)

	// Capture 1 (primary): the opening user turn + the first assistant turn.
	cap1 := &model.ParsedCapture{
		Model: "claude-opus-4-8", SystemFP: "fp-main", MessageCount: 1,
		Turn: asstTurn("first answer", 100, 20),
	}
	if _, err := s.AddProxyMessage(AddProxyMessageIn{
		ProxyRequestID: 1, DispatchID: &dispatchID, SessionID: "sess",
		Capture: cap1, Delta: []model.AnthropicMessage{userMsg("first question")},
		IsPrimary: true, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("add cap1: %v", err)
	}

	// Capture 2 (auxiliary): a title-gen probe — excluded from the primary thread.
	capAux := &model.ParsedCapture{
		Model: "claude-haiku", SystemFP: "fp-title", MessageCount: 1, HasOutputConfig: true,
		Turn: asstTurn(`{"title":"x"}`, 30, 5),
	}
	if _, err := s.AddProxyMessage(AddProxyMessageIn{
		ProxyRequestID: 2, DispatchID: &dispatchID, SessionID: "sess",
		Capture: capAux, Delta: nil, IsPrimary: false, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("add capAux: %v", err)
	}

	// Capture 3 (primary): extends the thread — its delta is the new user turn.
	cap3 := &model.ParsedCapture{
		Model: "claude-opus-4-8", SystemFP: "fp-main", MessageCount: 3,
		Turn: asstTurn("second answer", 160, 15),
	}
	if _, err := s.AddProxyMessage(AddProxyMessageIn{
		ProxyRequestID: 3, DispatchID: &dispatchID, SessionID: "sess",
		Capture: cap3, Delta: []model.AnthropicMessage{userMsg("second question")},
		IsPrimary: true, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("add cap3: %v", err)
	}

	tr, err := s.JobTranscript(dispatchID)
	if err != nil {
		t.Fatalf("JobTranscript: %v", err)
	}
	// Ordered primary thread: user, assistant, user, assistant. The aux probe
	// is not interleaved.
	wantRoles := []string{"user", "assistant", "user", "assistant"}
	if len(tr.Messages) != len(wantRoles) {
		t.Fatalf("assembled %d messages, want %d: %+v", len(tr.Messages), len(wantRoles), tr.Messages)
	}
	for i, want := range wantRoles {
		if tr.Messages[i].Role != want {
			t.Errorf("message %d role = %q, want %q", i, tr.Messages[i].Role, want)
		}
	}
	if len(tr.Auxiliary) != 1 {
		t.Errorf("auxiliary turns = %d, want 1", len(tr.Auxiliary))
	}
	// Usage sums only the primary turns: input 100+160=260, output 20+15=35.
	if tr.Usage.InputTokens != 260 || tr.Usage.OutputTokens != 35 {
		t.Errorf("summed usage = %+v, want input 260 output 35", tr.Usage)
	}
}

// TestLatestThreadState asserts the recorder's carry reflects the newest primary
// row for a dispatch (and is empty for an unknown / nil dispatch).
func TestLatestThreadState(t *testing.T) {
	s := newTestStore(t)
	dispatchID := int64(11)

	if st, err := s.LatestThreadState(nil); err != nil || st.SystemFP != "" {
		t.Fatalf("nil dispatch should yield zero state, got %+v err=%v", st, err)
	}
	if st, err := s.LatestThreadState(&dispatchID); err != nil || st.SystemFP != "" {
		t.Fatalf("unknown dispatch should yield zero state, got %+v err=%v", st, err)
	}

	for _, mc := range []int{1, 3} {
		cap := &model.ParsedCapture{Model: "claude-opus-4-8", SystemFP: "fp", MessageCount: mc, Turn: asstTurn("a", 1, 1)}
		if _, err := s.AddProxyMessage(AddProxyMessageIn{
			ProxyRequestID: int64(mc), DispatchID: &dispatchID, Capture: cap, IsPrimary: true, StartedAt: time.Now(),
		}); err != nil {
			t.Fatalf("add mc=%d: %v", mc, err)
		}
	}
	st, err := s.LatestThreadState(&dispatchID)
	if err != nil {
		t.Fatalf("LatestThreadState: %v", err)
	}
	if st.SystemFP != "fp" || st.Model != "claude-opus-4-8" || st.MessageCount != 3 {
		t.Errorf("latest thread state = %+v, want fp/opus/3 (newest primary)", st)
	}
	// Sanity: it threads into Classify cleanly.
	var _ anthropic.ThreadState = st
}

// TestCaptureMessage asserts a parsed-message row is fetchable by the
// proxy_requests id it parsed from, and ErrNotFound for an id with no row.
func TestCaptureMessage(t *testing.T) {
	s := newTestStore(t)
	cap := &model.ParsedCapture{Model: "claude-opus-4-8", SystemFP: "fp", MessageCount: 1, Turn: asstTurn("hi", 5, 2)}
	if _, err := s.AddProxyMessage(AddProxyMessageIn{
		ProxyRequestID: 42, Capture: cap, Delta: []model.AnthropicMessage{userMsg("q")}, IsPrimary: true, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	pm, err := s.CaptureMessage(42)
	if err != nil {
		t.Fatalf("CaptureMessage: %v", err)
	}
	if pm.ProxyRequestID != 42 || pm.Usage.OutputTokens != 2 {
		t.Errorf("row = %+v, want proxy_request_id 42 / output 2", pm)
	}
	// turn_json round-trips to the stored turn.
	var turn model.AnthropicTurn
	if err := json.Unmarshal([]byte(pm.TurnJSON), &turn); err != nil {
		t.Fatalf("turn_json not valid JSON: %v", err)
	}
	if len(turn.Blocks) != 1 || turn.Blocks[0].Text != "hi" {
		t.Errorf("turn_json round-trip = %+v, want one text block 'hi'", turn.Blocks)
	}
	if _, err := s.CaptureMessage(999); err != ErrNotFound {
		t.Errorf("CaptureMessage(unknown) err = %v, want ErrNotFound", err)
	}
}

// TestAddProxyMessage_BodyCap asserts an over-cap body is replaced with a marker
// rather than stored as a truncated (invalid) JSON blob.
func TestAddProxyMessage_BodyCap(t *testing.T) {
	s := newTestStore(t)
	huge := strings.Repeat("x", model.MaxProxyMessageBody+10)
	cap := &model.ParsedCapture{
		Model: "claude-opus-4-8", SystemFP: "fp", MessageCount: 1,
		Turn: model.AnthropicTurn{Blocks: []model.AnthropicBlock{{Type: "text", Text: huge}}},
	}
	pm, err := s.AddProxyMessage(AddProxyMessageIn{
		ProxyRequestID: 1, Capture: cap, IsPrimary: true, StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.HasPrefix(pm.TurnJSON, "[proxy message body truncated") {
		t.Errorf("over-cap turn_json should be a marker, got %q…", pm.TurnJSON[:40])
	}
}

// TestPruneProxyMessages asserts rows older than the retention window are
// deleted while recent rows survive.
func TestPruneProxyMessages(t *testing.T) {
	s := newTestStore(t)
	dispatchID := int64(5)
	old := &model.ParsedCapture{Model: "m", SystemFP: "fp", MessageCount: 1, Turn: asstTurn("old", 1, 1)}
	recent := &model.ParsedCapture{Model: "m", SystemFP: "fp", MessageCount: 1, Turn: asstTurn("new", 1, 1)}
	if _, err := s.AddProxyMessage(AddProxyMessageIn{
		ProxyRequestID: 1, DispatchID: &dispatchID, Capture: old, IsPrimary: true,
		StartedAt: time.Now().Add(-2 * ProxyRequestRetention),
	}); err != nil {
		t.Fatalf("add old: %v", err)
	}
	if _, err := s.AddProxyMessage(AddProxyMessageIn{
		ProxyRequestID: 2, DispatchID: &dispatchID, Capture: recent, IsPrimary: true,
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("add recent: %v", err)
	}
	if err := pruneProxyMessages(s.DB, ProxyRequestRetention); err != nil {
		t.Fatalf("prune: %v", err)
	}
	tr, err := s.JobTranscript(dispatchID)
	if err != nil {
		t.Fatalf("JobTranscript: %v", err)
	}
	// Only the recent capture's turn survives → one assistant message.
	var asst int
	for _, m := range tr.Messages {
		if m.Role == "assistant" {
			asst++
		}
	}
	if asst != 1 {
		t.Errorf("after prune: %d assistant messages, want 1 (old pruned)", asst)
	}
}

// seedSearchRow is a tiny helper for the SearchProxyMessages tests: it inserts
// one capture with the given assistant turn text and user delta text, correlated
// to the given dispatch/session/agent, so the tests read as fixtures.
func seedSearchRow(t *testing.T, s *Store, proxyReqID int64, dispatchID *int64, session, agent, asstText, userText string) {
	t.Helper()
	cap := &model.ParsedCapture{
		Model: "claude-opus-4-8", SystemFP: "fp", MessageCount: 1,
		Turn: model.AnthropicTurn{Blocks: []model.AnthropicBlock{{Type: "text", Text: asstText}}},
	}
	in := AddProxyMessageIn{
		ProxyRequestID: proxyReqID, DispatchID: dispatchID,
		SessionID: session, ClaudeAgentID: agent,
		Capture: cap, IsPrimary: true, StartedAt: time.Now(),
	}
	if userText != "" {
		in.Delta = []model.AnthropicMessage{userMsg(userText)}
	}
	if _, err := s.AddProxyMessage(in); err != nil {
		t.Fatalf("seed search row %d: %v", proxyReqID, err)
	}
}

// TestSearchProxyMessages_Basic asserts a bare needle finds matches on both the
// assistant (turn_json) and user (delta_json) sides, and the snippet is the
// collapsed block text — not raw JSON field names.
func TestSearchProxyMessages_Basic(t *testing.T) {
	s := newTestStore(t)
	// Assistant turn carries the needle; user delta does not.
	seedSearchRow(t, s, 1, nil, "", "", "a stray token court before the tool", "what is the weather")
	// User delta carries the needle; assistant turn does not.
	seedSearchRow(t, s, 2, nil, "", "", "all good here", "please go to court today")

	matches, err := s.SearchProxyMessages(ProxyMessageFilter{Query: "court"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("got %d matches, want 2 (one assistant, one user): %+v", len(matches), matches)
	}
	roles := map[string]string{}
	for _, m := range matches {
		roles[m.Role] = m.Snippet
		if m.Block != "text" {
			t.Errorf("block = %q, want text", m.Block)
		}
		// Snippet is real block text, never a raw JSON field name.
		if strings.Contains(m.Snippet, "\"text\":") || strings.Contains(m.Snippet, "{") {
			t.Errorf("snippet looks like raw JSON: %q", m.Snippet)
		}
	}
	if !strings.Contains(roles["assistant"], "stray token court") {
		t.Errorf("assistant snippet = %q, want the turn text", roles["assistant"])
	}
	if !strings.Contains(roles["user"], "go to court") {
		t.Errorf("user snippet = %q, want the delta text", roles["user"])
	}
}

// TestSearchProxyMessages_CaseInsensitive asserts the substring match ignores
// case (the motivating "court" forensic search shouldn't care about casing).
func TestSearchProxyMessages_CaseInsensitive(t *testing.T) {
	s := newTestStore(t)
	seedSearchRow(t, s, 1, nil, "", "", "The COURT adjourned", "")
	matches, err := s.SearchProxyMessages(ProxyMessageFilter{Query: "court"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want 1 (case-insensitive)", len(matches))
	}
}

// TestSearchProxyMessages_RoleScope asserts --role narrows to one column:
// assistant → turn only, user → delta only.
func TestSearchProxyMessages_RoleScope(t *testing.T) {
	s := newTestStore(t)
	// Needle in both sides of one capture.
	seedSearchRow(t, s, 1, nil, "", "", "court ruling", "the court case")

	assistant, err := s.SearchProxyMessages(ProxyMessageFilter{Query: "court", Role: "assistant"})
	if err != nil {
		t.Fatalf("search assistant: %v", err)
	}
	if len(assistant) != 1 || assistant[0].Role != "assistant" {
		t.Fatalf("role=assistant: got %+v, want one assistant match", assistant)
	}
	user, err := s.SearchProxyMessages(ProxyMessageFilter{Query: "court", Role: "user"})
	if err != nil {
		t.Fatalf("search user: %v", err)
	}
	if len(user) != 1 || user[0].Role != "user" {
		t.Fatalf("role=user: got %+v, want one user match", user)
	}
}

// TestSearchProxyMessages_BlockScope asserts --block excludes a text-only match
// and a tool_use match's text is Name + Input.
func TestSearchProxyMessages_BlockScope(t *testing.T) {
	s := newTestStore(t)
	// One capture: a text block and a tool_use block, both containing "court".
	cap := &model.ParsedCapture{
		Model: "claude-opus-4-8", SystemFP: "fp", MessageCount: 1,
		Turn: model.AnthropicTurn{Blocks: []model.AnthropicBlock{
			{Type: "text", Text: "the court text"},
			{Type: "tool_use", Name: "search_court", Input: json.RawMessage(`{"q":"court records"}`)},
		}},
	}
	if _, err := s.AddProxyMessage(AddProxyMessageIn{
		ProxyRequestID: 1, Capture: cap, IsPrimary: true, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Unscoped: both blocks match → two lines.
	all, err := s.SearchProxyMessages(ProxyMessageFilter{Query: "court"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("unscoped got %d, want 2 (text + tool_use)", len(all))
	}
	// --block tool_use excludes the text-only match.
	tu, err := s.SearchProxyMessages(ProxyMessageFilter{Query: "court", Block: "tool_use"})
	if err != nil {
		t.Fatalf("search block: %v", err)
	}
	if len(tu) != 1 || tu[0].Block != "tool_use" {
		t.Fatalf("block=tool_use: got %+v, want one tool_use match", tu)
	}
	if !strings.Contains(tu[0].Snippet, "search_court") {
		t.Errorf("tool_use snippet = %q, want Name + Input", tu[0].Snippet)
	}
}

// TestSearchProxyMessages_Correlation asserts the dispatch / session / agent
// filters scope the search to one job's captures.
func TestSearchProxyMessages_Correlation(t *testing.T) {
	s := newTestStore(t)
	d1, d2 := int64(11), int64(22)
	seedSearchRow(t, s, 1, &d1, "sess-a", "agent-a", "court one", "")
	seedSearchRow(t, s, 2, &d2, "sess-b", "agent-b", "court two", "")

	byDispatch, err := s.SearchProxyMessages(ProxyMessageFilter{Query: "court", DispatchID: &d1})
	if err != nil {
		t.Fatalf("search dispatch: %v", err)
	}
	if len(byDispatch) != 1 || byDispatch[0].ProxyRequestID != 1 {
		t.Fatalf("dispatch filter: got %+v, want only capture 1", byDispatch)
	}
	bySession, err := s.SearchProxyMessages(ProxyMessageFilter{Query: "court", SessionID: "sess-b"})
	if err != nil {
		t.Fatalf("search session: %v", err)
	}
	if len(bySession) != 1 || bySession[0].ProxyRequestID != 2 {
		t.Fatalf("session filter: got %+v, want only capture 2", bySession)
	}
	byAgent, err := s.SearchProxyMessages(ProxyMessageFilter{Query: "court", ClaudeAgentID: "agent-a"})
	if err != nil {
		t.Fatalf("search agent: %v", err)
	}
	if len(byAgent) != 1 || byAgent[0].ProxyRequestID != 1 {
		t.Fatalf("agent filter: got %+v, want only capture 1", byAgent)
	}
}

// TestSearchProxyMessages_LiteralPercent asserts a needle containing a LIKE
// wildcard (%) matches literally — the ESCAPE clause guards the wildcards.
func TestSearchProxyMessages_LiteralPercent(t *testing.T) {
	s := newTestStore(t)
	seedSearchRow(t, s, 1, nil, "", "", "discount is 50% off", "")
	seedSearchRow(t, s, 2, nil, "", "", "no markdown here", "")

	matches, err := s.SearchProxyMessages(ProxyMessageFilter{Query: "50%"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(matches) != 1 || matches[0].ProxyRequestID != 1 {
		t.Fatalf("literal %% match: got %+v, want only capture 1", matches)
	}
}

// TestSearchProxyMessages_Limit asserts --limit caps the match lines returned.
func TestSearchProxyMessages_Limit(t *testing.T) {
	s := newTestStore(t)
	seedSearchRow(t, s, 1, nil, "", "", "court alpha", "")
	seedSearchRow(t, s, 2, nil, "", "", "court bravo", "")
	seedSearchRow(t, s, 3, nil, "", "", "court charlie", "")

	matches, err := s.SearchProxyMessages(ProxyMessageFilter{Query: "court", Limit: 1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("limit=1: got %d, want 1", len(matches))
	}
}

// TestSearchProxyMessages_Empty asserts an unmatched needle returns a non-nil
// empty slice (not nil).
func TestSearchProxyMessages_Empty(t *testing.T) {
	s := newTestStore(t)
	seedSearchRow(t, s, 1, nil, "", "", "nothing relevant", "")
	matches, err := s.SearchProxyMessages(ProxyMessageFilter{Query: "court"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if matches == nil {
		t.Fatal("matches is nil, want non-nil empty slice")
	}
	if len(matches) != 0 {
		t.Fatalf("got %d matches, want 0", len(matches))
	}
}

// TestListJobTranscripts seeds captures across two dispatches in two repos and
// asserts the row-per-dispatch aggregation: turn count (primary captures only),
// summed usage, the primary thread's model (not an auxiliary capture's), the
// enrichment lifted off the dispatch, and that the repo / issue / mode filters
// narrow correctly. It also asserts the empty-table case yields a non-nil slice.
func TestListJobTranscripts(t *testing.T) {
	s := newTestStore(t)

	// Empty table → non-nil empty slice.
	if rows, err := s.ListJobTranscripts(JobTranscriptFilter{}); err != nil {
		t.Fatalf("empty ListJobTranscripts: %v", err)
	} else if rows == nil {
		t.Fatal("empty ListJobTranscripts returned nil, want non-nil empty slice")
	} else if len(rows) != 0 {
		t.Fatalf("empty table: got %d rows, want 0", len(rows))
	}

	// Two repos, one issue + one dispatch each.
	repoA, err := s.CreateRepo("AAAA", "repo-a", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo A: %v", err)
	}
	repoB, err := s.CreateRepo("BBBB", "repo-b", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo B: %v", err)
	}
	issA, err := s.CreateIssue(repoA.ID, nil, "issue a", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create issue A: %v", err)
	}
	issB, err := s.CreateIssue(repoB.ID, nil, "issue b", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create issue B: %v", err)
	}
	dispA := seedDispatch(t, s, repoA.ID, &issA.ID, "implement")
	dispB := seedDispatch(t, s, repoB.ID, &issB.ID, "plan")

	// Dispatch A: two primary captures (opus) + one auxiliary (haiku, title-gen).
	add := func(proxyReqID, dispatchID int64, modelName string, primary bool, in, out int64) {
		t.Helper()
		cap := &model.ParsedCapture{
			Model: modelName, SystemFP: "fp", MessageCount: 1,
			Turn: model.AnthropicTurn{Model: modelName, Usage: model.AnthropicUsage{InputTokens: in, OutputTokens: out}},
		}
		if _, err := s.AddProxyMessage(AddProxyMessageIn{
			ProxyRequestID: proxyReqID, DispatchID: &dispatchID,
			Capture: cap, IsPrimary: primary, StartedAt: time.Now(),
		}); err != nil {
			t.Fatalf("add capture %d: %v", proxyReqID, err)
		}
	}
	add(1, dispA, "claude-opus-4-8", true, 100, 20)
	add(2, dispA, "claude-haiku", false, 30, 5) // auxiliary
	add(3, dispA, "claude-opus-4-8", true, 160, 15)
	add(4, dispB, "claude-sonnet-4-6", true, 50, 10)

	// Unscoped: both dispatches, newest-first by last-seen.
	rows, err := s.ListJobTranscripts(JobTranscriptFilter{})
	if err != nil {
		t.Fatalf("ListJobTranscripts: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("unscoped: got %d rows, want 2: %+v", len(rows), rows)
	}
	byDispatch := map[int64]*model.JobTranscriptRow{}
	for _, r := range rows {
		byDispatch[r.DispatchID] = r
	}
	a := byDispatch[dispA]
	if a == nil {
		t.Fatalf("dispatch A row missing: %+v", rows)
	}
	// Turn count = primary captures only (2, not the 3 total).
	if a.TurnCount != 2 {
		t.Errorf("dispatch A turn_count = %d, want 2 (primary captures)", a.TurnCount)
	}
	// Usage sums across ALL captures (incl. the auxiliary): 100+30+160 in, 20+5+15 out.
	if a.Usage.InputTokens != 290 || a.Usage.OutputTokens != 40 {
		t.Errorf("dispatch A usage = %+v, want input 290 output 40", a.Usage)
	}
	// Model is the primary thread's, not the auxiliary haiku's.
	if a.Model != "claude-opus-4-8" {
		t.Errorf("dispatch A model = %q, want claude-opus-4-8 (primary, not aux haiku)", a.Model)
	}
	// Enrichment lifted off the dispatch.
	if a.IssueKey != issA.Key || a.Mode != "implement" || a.RepoPrefix != "AAAA" {
		t.Errorf("dispatch A enrichment = key=%q mode=%q prefix=%q, want %s/implement/AAAA", a.IssueKey, a.Mode, a.RepoPrefix, issA.Key)
	}

	// Repo-prefix scope drops the foreign-repo dispatch.
	scoped, err := s.ListJobTranscripts(JobTranscriptFilter{RepoPrefix: "AAAA"})
	if err != nil {
		t.Fatalf("repo-scoped ListJobTranscripts: %v", err)
	}
	if len(scoped) != 1 || scoped[0].DispatchID != dispA {
		t.Fatalf("repo scope AAAA: got %d rows, want only dispatch A: %+v", len(scoped), scoped)
	}

	// Issue filter narrows to the one issue.
	byIssue, err := s.ListJobTranscripts(JobTranscriptFilter{IssueKey: issB.Key})
	if err != nil {
		t.Fatalf("issue-scoped ListJobTranscripts: %v", err)
	}
	if len(byIssue) != 1 || byIssue[0].DispatchID != dispB {
		t.Fatalf("issue scope %s: got %d rows, want only dispatch B: %+v", issB.Key, len(byIssue), byIssue)
	}

	// Mode filter narrows to the one job mode.
	byMode, err := s.ListJobTranscripts(JobTranscriptFilter{Mode: "plan"})
	if err != nil {
		t.Fatalf("mode-scoped ListJobTranscripts: %v", err)
	}
	if len(byMode) != 1 || byMode[0].DispatchID != dispB {
		t.Fatalf("mode scope plan: got %d rows, want only dispatch B: %+v", len(byMode), byMode)
	}
}

// seedJobCapture adds one parsed primary capture for a dispatch carrying the
// given session / agent correlation — the seed the session/agent transcript
// tests group on.
func seedJobCapture(t *testing.T, s *Store, proxyReqID, dispatchID int64, session, agent string) {
	t.Helper()
	cap := &model.ParsedCapture{Model: "claude-opus-4-8", SystemFP: "fp", MessageCount: 1}
	if _, err := s.AddProxyMessage(AddProxyMessageIn{
		ProxyRequestID: proxyReqID, DispatchID: &dispatchID,
		SessionID: session, ClaudeAgentID: agent,
		Capture: cap, IsPrimary: true, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seedJobCapture %d: %v", proxyReqID, err)
	}
}

// TestListJobTranscripts_SessionAgentEnrichment asserts each row carries the
// dispatch's session_id / claude_agent_id (one per dispatch) and a session
// label: the session's agent name when an agent_sessions row names it, else
// the short session id when no row exists.
func TestListJobTranscripts_SessionAgentEnrichment(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("AAAA", "repo-a", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	// A named session: an agent_sessions row with an agent name to resolve.
	ag, _, err := s.UpsertAgent("swift-otter@claude.test", true)
	if err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	const namedSess = "11111111-2222-3333-4444-555555555555"
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: namedSess, RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("upsert session: %v", err)
	}

	dispNamed := seedDispatch(t, s, repo.ID, nil, "implement")
	seedJobCapture(t, s, 1, dispNamed, namedSess, "agent-aaa")
	seedJobCapture(t, s, 2, dispNamed, namedSess, "agent-aaa")

	// An unsynced session: captures reference a session id with no
	// agent_sessions row, so the label falls back to the short id.
	const orphanSess = "99999999-8888-7777-6666-555555555555"
	dispOrphan := seedDispatch(t, s, repo.ID, nil, "plan")
	seedJobCapture(t, s, 3, dispOrphan, orphanSess, "agent-bbb")

	rows, err := s.ListJobTranscripts(JobTranscriptFilter{})
	if err != nil {
		t.Fatalf("ListJobTranscripts: %v", err)
	}
	byDispatch := map[int64]*model.JobTranscriptRow{}
	for _, r := range rows {
		byDispatch[r.DispatchID] = r
	}

	named := byDispatch[dispNamed]
	if named == nil {
		t.Fatalf("named dispatch row missing: %+v", rows)
	}
	if named.SessionID != namedSess || named.ClaudeAgentID != "agent-aaa" {
		t.Errorf("named row session/agent = %q / %q, want %q / agent-aaa", named.SessionID, named.ClaudeAgentID, namedSess)
	}
	if named.SessionLabel != "swift-otter@claude.test" {
		t.Errorf("named row label = %q, want the agent name", named.SessionLabel)
	}

	orphan := byDispatch[dispOrphan]
	if orphan == nil {
		t.Fatalf("orphan dispatch row missing: %+v", rows)
	}
	if orphan.SessionLabel != orphanSess[:12] {
		t.Errorf("orphan row label = %q, want short id %q", orphan.SessionLabel, orphanSess[:12])
	}
}

// TestListJobTranscripts_SessionAgentFilters asserts the session / agent
// filters narrow the list to one supervisor session / one subagent identity.
func TestListJobTranscripts_SessionAgentFilters(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("AAAA", "repo-a", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	// One session, two dispatches (a plan→implement chain), each its own agent.
	const sess = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	dispPlan := seedDispatch(t, s, repo.ID, nil, "plan")
	dispImpl := seedDispatch(t, s, repo.ID, nil, "implement")
	seedJobCapture(t, s, 1, dispPlan, sess, "agent-plan")
	seedJobCapture(t, s, 2, dispImpl, sess, "agent-impl")

	// A second session entirely (a different sitting).
	const otherSess = "ffffffff-0000-1111-2222-333333333333"
	dispOther := seedDispatch(t, s, repo.ID, nil, "ship")
	seedJobCapture(t, s, 3, dispOther, otherSess, "agent-ship")

	// Session filter: both dispatches of that one session, not the other.
	bySession, err := s.ListJobTranscripts(JobTranscriptFilter{SessionID: sess})
	if err != nil {
		t.Fatalf("session-scoped ListJobTranscripts: %v", err)
	}
	if len(bySession) != 2 {
		t.Fatalf("session scope: got %d rows, want 2 (the chain): %+v", len(bySession), bySession)
	}
	for _, r := range bySession {
		if r.DispatchID == dispOther {
			t.Fatalf("session scope leaked the other session's dispatch: %+v", bySession)
		}
	}

	// Agent filter: only the dispatch that subagent ran.
	byAgent, err := s.ListJobTranscripts(JobTranscriptFilter{ClaudeAgentID: "agent-impl"})
	if err != nil {
		t.Fatalf("agent-scoped ListJobTranscripts: %v", err)
	}
	if len(byAgent) != 1 || byAgent[0].DispatchID != dispImpl {
		t.Fatalf("agent scope agent-impl: got %d rows, want only the implement dispatch: %+v", len(byAgent), byAgent)
	}
}
