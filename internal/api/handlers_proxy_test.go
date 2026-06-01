package api_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// seedProxyRow inserts one proxy_requests index row directly via the
// store — the GET /proxy/stats handler reads the aggregation off these.
func seedProxyRow(t *testing.T, s *store.Store, host string, status int, dur time.Duration) {
	t.Helper()
	if _, err := s.AddProxyRequest(store.AddProxyRequestIn{
		Method: "POST", Host: host, Path: "/v1/messages",
		Status: status, BytesIn: 100, BytesOut: 200, Duration: dur,
	}); err != nil {
		t.Fatalf("seed proxy row (%s): %v", host, err)
	}
}

// TestProxyStatsEndpoint seeds rows for two hosts and asserts the
// GET /proxy/stats JSON body groups and computes the rollup, ordered
// busiest-first.
func TestProxyStatsEndpoint(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedProxyRow(t, s, "api.anthropic.com", 200, 100*time.Millisecond)
	seedProxyRow(t, s, "api.anthropic.com", 200, 200*time.Millisecond)
	seedProxyRow(t, s, "api.anthropic.com", 500, 300*time.Millisecond)
	seedProxyRow(t, s, "example.com", 200, 50*time.Millisecond)

	resp, raw := apiGet(t, ts.URL+"/proxy/stats")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var stats []*model.ProxyFQDNStat
	if err := json.Unmarshal(raw, &stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("hosts: %d, want 2: %+v", len(stats), stats)
	}
	// Busiest-first: the 3-request host leads.
	if stats[0].Host != "api.anthropic.com" {
		t.Fatalf("busiest host first: got %q", stats[0].Host)
	}
	anthropic := stats[0]
	if anthropic.RequestCount != 3 {
		t.Fatalf("request_count = %d, want 3", anthropic.RequestCount)
	}
	if anthropic.ErrorCount != 1 {
		t.Fatalf("error_count = %d, want 1 (the 500)", anthropic.ErrorCount)
	}
	if anthropic.BytesIn != 300 || anthropic.BytesOut != 600 {
		t.Fatalf("byte sums wrong: in=%d out=%d", anthropic.BytesIn, anthropic.BytesOut)
	}
	if anthropic.P50MS == 0 || anthropic.P95MS == 0 {
		t.Fatalf("percentiles unpopulated: p50=%d p95=%d", anthropic.P50MS, anthropic.P95MS)
	}
}

// TestProxyStatsEmptyArrayNotNull asserts the handler emits [] not null
// for an empty proxy_requests table.
func TestProxyStatsEmptyArrayNotNull(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, raw := apiGet(t, ts.URL+"/proxy/stats")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if len(raw) == 0 || raw[0] != '[' {
		t.Fatalf("expected [], got %s", string(raw))
	}
}

// TestProxyCapturesEndpoint: GET /proxy/captures filters by host and
// enriches each correlated row with its dispatch's issue-key + mode, newest
// first. A capture with no dispatch_id comes back with an empty chip.
func TestProxyCapturesEndpoint(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "drill me")
	disp, err := s.AddDispatch(store.AddDispatchIn{
		RepoID: repo.ID, IssueID: &iss.ID, Mode: model.DispatchModeImplement,
		TargetSessionID: uuidFor("sess-cap"), CreatedBy: "user",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}

	t0 := time.Now().Add(-5 * time.Minute)
	// Two correlated anthropic captures on the host + one uncorrelated.
	for i, at := range []time.Time{t0, t0.Add(time.Minute)} {
		if _, err := s.AddProxyRequest(store.AddProxyRequestIn{
			Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages",
			Status: 200, IsAnthropic: true, DispatchID: &disp.ID,
			StartedAt: at, EndedAt: at,
		}); err != nil {
			t.Fatalf("seed correlated %d: %v", i, err)
		}
	}
	if _, err := s.AddProxyRequest(store.AddProxyRequestIn{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages?beta=count_tokens",
		Status: 200, StartedAt: t0.Add(2 * time.Minute), EndedAt: t0.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("seed uncorrelated: %v", err)
	}
	// A capture on a different host that the host filter must exclude.
	seedProxyRow(t, s, "example.com", 200, 10*time.Millisecond)

	resp, raw := apiGet(t, ts.URL+"/proxy/captures?host=api.anthropic.com")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var rows []*model.ProxyCaptureRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3 (host-scoped): %s", len(rows), raw)
	}
	// Newest-first.
	for i := 0; i+1 < len(rows); i++ {
		if rows[i].StartedAt.Before(rows[i+1].StartedAt) {
			t.Fatalf("rows not newest-first at %d", i)
		}
	}
	// The newest row is the uncorrelated probe — empty chip.
	if rows[0].DispatchID != nil || rows[0].IssueKey != "" || rows[0].Mode != "" {
		t.Fatalf("uncorrelated row should have empty chip: %+v", rows[0])
	}
	// The correlated rows carry the issue-key + mode.
	if rows[1].IssueKey != iss.Key || rows[1].Mode != string(model.DispatchModeImplement) {
		t.Fatalf("correlated chip wrong: key=%q mode=%q (want %q implement)", rows[1].IssueKey, rows[1].Mode, iss.Key)
	}
}

// TestProxyCapturesEmptyArrayNotNull: the list handler emits [] not null for
// an empty match.
func TestProxyCapturesEmptyArrayNotNull(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, raw := apiGet(t, ts.URL+"/proxy/captures?host=nope.example")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if len(raw) == 0 || raw[0] != '[' {
		t.Fatalf("expected [], got %s", string(raw))
	}
}

// seedJobCapture inserts one parsed proxy_messages row correlated to a
// dispatch — the BACI-322 GET /proxy/jobs handler aggregates these per
// dispatch.
func seedJobCapture(t *testing.T, s *store.Store, proxyReqID int64, dispatchID int64, modelName string, primary bool, in, out int64) {
	t.Helper()
	cap := &model.ParsedCapture{
		Model: modelName, SystemFP: "fp", MessageCount: 1,
		Turn: model.AnthropicTurn{Model: modelName, Usage: model.AnthropicUsage{InputTokens: in, OutputTokens: out}},
	}
	if _, err := s.AddProxyMessage(store.AddProxyMessageIn{
		ProxyRequestID: proxyReqID, DispatchID: &dispatchID,
		Capture: cap, IsPrimary: primary, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed job capture %d: %v", proxyReqID, err)
	}
}

// TestProxyJobsEndpoint seeds two dispatches in two repos and asserts
// GET /proxy/jobs returns one row per dispatch, repo-scopes via ?repo=, and
// narrows via ?mode=. Each row carries the dispatch enrichment.
func TestProxyJobsEndpoint(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repoA := seedRepo(t, s)          // MINI
	repoB := seedRepo2(t, s)         // OTHR
	issA := seedIssue(t, s, repoA, "job a")
	issB := seedIssue(t, s, repoB, "job b")
	dispA, err := s.AddDispatch(store.AddDispatchIn{
		RepoID: repoA.ID, IssueID: &issA.ID, Mode: model.DispatchModeImplement,
		TargetSessionID: uuidFor("sess-job-a"), CreatedBy: "user",
	})
	if err != nil {
		t.Fatalf("add dispatch A: %v", err)
	}
	dispB, err := s.AddDispatch(store.AddDispatchIn{
		RepoID: repoB.ID, IssueID: &issB.ID, Mode: model.DispatchModePlan,
		TargetSessionID: uuidFor("sess-job-b"), CreatedBy: "user",
	})
	if err != nil {
		t.Fatalf("add dispatch B: %v", err)
	}
	// Dispatch A: two primary captures + one auxiliary.
	seedJobCapture(t, s, 1, dispA.ID, "claude-opus-4-8", true, 100, 20)
	seedJobCapture(t, s, 2, dispA.ID, "claude-haiku", false, 30, 5)
	seedJobCapture(t, s, 3, dispA.ID, "claude-opus-4-8", true, 160, 15)
	seedJobCapture(t, s, 4, dispB.ID, "claude-sonnet-4-6", true, 50, 10)

	// Unscoped: both dispatches.
	resp, raw := apiGet(t, ts.URL+"/proxy/jobs")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var rows []*model.JobTranscriptRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("unscoped: got %d rows, want 2: %s", len(rows), raw)
	}

	// Repo scope drops the foreign repo.
	resp, raw = apiGet(t, ts.URL+"/proxy/jobs?repo=MINI")
	if resp.StatusCode != 200 {
		t.Fatalf("repo-scoped status: %d body=%s", resp.StatusCode, raw)
	}
	rows = nil
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("decode repo-scoped: %v", err)
	}
	if len(rows) != 1 || rows[0].DispatchID != dispA.ID {
		t.Fatalf("repo=MINI: got %d rows, want only dispatch A: %s", len(rows), raw)
	}
	a := rows[0]
	// Turn count = primary captures only; model is the primary thread's; usage
	// sums across all captures; enrichment lifted off the dispatch.
	if a.TurnCount != 2 || a.Model != "claude-opus-4-8" {
		t.Errorf("dispatch A row = turns %d model %q, want 2 / claude-opus-4-8", a.TurnCount, a.Model)
	}
	if a.Usage.InputTokens != 290 || a.Usage.OutputTokens != 40 {
		t.Errorf("dispatch A usage = %+v, want input 290 output 40", a.Usage)
	}
	if a.IssueKey != issA.Key || a.Mode != string(model.DispatchModeImplement) || a.RepoPrefix != "MINI" {
		t.Errorf("dispatch A enrichment = key=%q mode=%q prefix=%q, want %s/implement/MINI", a.IssueKey, a.Mode, a.RepoPrefix, issA.Key)
	}

	// Mode scope narrows to the plan job (dispatch B).
	resp, raw = apiGet(t, ts.URL+"/proxy/jobs?mode=plan")
	if resp.StatusCode != 200 {
		t.Fatalf("mode-scoped status: %d body=%s", resp.StatusCode, raw)
	}
	rows = nil
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("decode mode-scoped: %v", err)
	}
	if len(rows) != 1 || rows[0].DispatchID != dispB.ID {
		t.Fatalf("mode=plan: got %d rows, want only dispatch B: %s", len(rows), raw)
	}
}

// TestProxyJobsEmptyArrayNotNull: the jobs handler emits [] not null for an
// empty match.
func TestProxyJobsEmptyArrayNotNull(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, raw := apiGet(t, ts.URL+"/proxy/jobs?repo=NONE")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if len(raw) == 0 || raw[0] != '[' {
		t.Fatalf("expected [], got %s", string(raw))
	}
}

// TestProxyCapturesBadDispatchID rejects a non-numeric dispatch_id at the
// handler boundary.
func TestProxyCapturesBadDispatchID(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiGet(t, ts.URL+"/proxy/captures?dispatch_id=nope")
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// TestProxyRawEndpoint serves the on-disk redacted .http bytes for a capture
// whose RawLogPath points at a real file.
func TestProxyRawEndpoint(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	dir := t.TempDir()
	rawPath := filepath.Join(dir, "123.http")
	want := "==== REQUEST ====\r\nPOST /v1/messages HTTP/1.1\r\n\r\n{\"hi\":1}\r\n"
	if err := os.WriteFile(rawPath, []byte(want), 0o644); err != nil {
		t.Fatalf("write raw file: %v", err)
	}
	pr, err := s.AddProxyRequest(store.AddProxyRequestIn{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages",
		Status: 200, RawLogPath: rawPath,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	resp, raw := apiGet(t, ts.URL+"/proxy/captures/"+strconv.FormatInt(pr.ID, 10)+"/raw")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Fatalf("content-type = %q, want text/plain; charset=utf-8", ct)
	}
	if string(raw) != want {
		t.Fatalf("raw body = %q, want %q", string(raw), want)
	}
}

// TestProxyRawMissingFile: a capture row whose RawLogPath is empty, or whose
// file has been pruned, 404s (not 500) so the sheet treats it as a clean miss.
func TestProxyRawMissingFile(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})

	// RawLogPath empty (write failed at capture time).
	noPath, err := s.AddProxyRequest(store.AddProxyRequestIn{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages", Status: 200,
	})
	if err != nil {
		t.Fatalf("add no-path: %v", err)
	}
	resp, _ := apiGet(t, ts.URL+"/proxy/captures/"+strconv.FormatInt(noPath.ID, 10)+"/raw")
	if resp.StatusCode != 404 {
		t.Fatalf("empty RawLogPath: expected 404, got %d", resp.StatusCode)
	}

	// RawLogPath set but the file is gone from disk.
	gone, err := s.AddProxyRequest(store.AddProxyRequestIn{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages", Status: 200,
		RawLogPath: filepath.Join(t.TempDir(), "pruned.http"),
	})
	if err != nil {
		t.Fatalf("add gone: %v", err)
	}
	resp2, _ := apiGet(t, ts.URL+"/proxy/captures/"+strconv.FormatInt(gone.ID, 10)+"/raw")
	if resp2.StatusCode != 404 {
		t.Fatalf("missing file: expected 404, got %d", resp2.StatusCode)
	}
}

// TestProxyRawBadID rejects a non-numeric id at the handler boundary.
func TestProxyRawBadID(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiGet(t, ts.URL+"/proxy/captures/nope/raw")
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// TestProxyStatsAuthRequired: /proxy/stats sits outside the /anthropic/
// auth exemption, so a bearer-protected deployment must require a token
// (unlike the agent passthrough route).
func TestProxyStatsAuthRequired(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{Token: "secret"})
	resp, _ := apiGet(t, ts.URL+"/proxy/stats")
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// TestProxyStatsBadLimit rejects a negative limit at the handler boundary.
func TestProxyStatsBadLimit(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiGet(t, ts.URL+"/proxy/stats?limit=-1")
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// TestProxyStatsSinceExcludesBackdated: a `since` lookback excludes rows
// whose started_at predates the window.
func TestProxyStatsSinceExcludesBackdated(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedProxyRow(t, s, "api.anthropic.com", 200, 10*time.Millisecond)
	stale, err := s.AddProxyRequest(store.AddProxyRequestIn{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/old", Status: 200,
	})
	if err != nil {
		t.Fatalf("add stale: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	if _, err := s.DB.Exec(`UPDATE proxy_requests SET started_at = ? WHERE id = ?`, old, stale.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	resp, raw := apiGet(t, ts.URL+"/proxy/stats?since=1h")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var stats []*model.ProxyFQDNStat
	_ = json.Unmarshal(raw, &stats)
	if len(stats) != 1 || stats[0].RequestCount != 1 {
		t.Fatalf("since window wrong: %+v", stats)
	}
}

// seedProxyMessage inserts one parsed proxy_messages row with the given
// assistant turn text — the GET /proxy/search handler greps these.
func seedProxyMessage(t *testing.T, s *store.Store, proxyReqID int64, asstText string) {
	t.Helper()
	cap := &model.ParsedCapture{
		Model: "claude-opus-4-8", SystemFP: "fp", MessageCount: 1,
		Turn: model.AnthropicTurn{Blocks: []model.AnthropicBlock{{Type: "text", Text: asstText}}},
	}
	if _, err := s.AddProxyMessage(store.AddProxyMessageIn{
		ProxyRequestID: proxyReqID, Capture: cap, IsPrimary: true, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed proxy message %d: %v", proxyReqID, err)
	}
}

// TestProxySearchEndpoint: GET /proxy/search?q=court returns the matching block
// as a ProxyMessageMatch carrying the capture id, role, block, and snippet.
func TestProxySearchEndpoint(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedProxyMessage(t, s, 1, "a stray token court before the tool")
	seedProxyMessage(t, s, 2, "nothing relevant here")

	resp, raw := apiGet(t, ts.URL+"/proxy/search?q=court")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, raw)
	}
	var matches []*model.ProxyMessageMatch
	if err := json.Unmarshal(raw, &matches); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want 1: %s", len(matches), raw)
	}
	m := matches[0]
	if m.ProxyRequestID != 1 || m.Role != "assistant" || m.Block != "text" {
		t.Fatalf("match wrong: %+v", m)
	}
	if !strings.Contains(m.Snippet, "stray token court") {
		t.Fatalf("snippet = %q, want the block text", m.Snippet)
	}
}

// TestProxySearchMissingQuery rejects a missing q at the handler boundary.
func TestProxySearchMissingQuery(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiGet(t, ts.URL+"/proxy/search")
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// TestProxySearchBadRole rejects an unknown role value.
func TestProxySearchBadRole(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiGet(t, ts.URL+"/proxy/search?q=x&role=bogus")
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// TestProxySearchEmptyArrayNotNull: the search handler emits [] not null for an
// unmatched needle.
func TestProxySearchEmptyArrayNotNull(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, raw := apiGet(t, ts.URL+"/proxy/search?q=nope")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if len(raw) == 0 || raw[0] != '[' {
		t.Fatalf("expected [], got %s", string(raw))
	}
}
