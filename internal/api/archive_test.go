// BACI-68 archive lifecycle API tests: per-entity archive / unarchive,
// the include_archived list filter, the sweep verb, and the
// display.show_archived setting. Mirrors the structure of the store
// tests but goes over the HTTP surface.
package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
)

func TestArchiveIssueRoundtrip(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "to archive")

	// Archive — POST returns the updated issue with archived_at set.
	resp, body := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/archive", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("archive: status %d, body %s", resp.StatusCode, body)
	}
	got := decodeIssue(t, body)
	if got.ArchivedAt == nil {
		t.Fatal("archived issue must carry archived_at in JSON")
	}

	// List default — issue is hidden.
	resp, body = apiGet(t, ts.URL+"/repos/MINI/issues")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status %d", resp.StatusCode)
	}
	issues := decodeIssues(t, body)
	if len(issues) != 0 {
		t.Fatalf("default list must hide archived; got %d", len(issues))
	}

	// List with ?include_archived=1 — issue surfaces again.
	resp, body = apiGet(t, ts.URL+"/repos/MINI/issues?include_archived=1")
	issues = decodeIssues(t, body)
	if len(issues) != 1 {
		t.Fatalf("inclusive list: got %d, want 1", len(issues))
	}

	// Unarchive — POST clears archived_at.
	resp, body = apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/unarchive", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unarchive: status %d, body %s", resp.StatusCode, body)
	}
	got = decodeIssue(t, body)
	if got.ArchivedAt != nil {
		t.Fatal("after unarchive, archived_at must be nil")
	}

	// Audit log: one issue.archive + one issue.unarchive, in order
	// (seedRepo/seedIssue write rows directly via the store, so they
	// don't go through the audit-emitting handler — the API verbs are
	// the only ops with audit rows in this test).
	assertHistoryOps(t, s, []string{"issue.archive", "issue.unarchive"})
}

// Without ?include_archived, the issue / doc / feature list endpoints
// fall back to the display.show_archived global setting — matching
// the boardcards handler's behaviour. (Reviewer point #6 on PR #103.)
func TestListEndpointsFallBackToDisplayShowArchived(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	// Archive an issue, a feature, and a document so each list endpoint
	// has something to hide-or-surface based on the toggle.
	iss := seedIssue(t, s, repo, "to hide")
	if err := s.SetIssueArchived(iss.ID, true); err != nil {
		t.Fatalf("archive issue: %v", err)
	}
	feat := seedFeature(t, s, repo, "hidden-feat", "Hidden")
	if err := s.SetFeatureArchived(feat.ID, true); err != nil {
		t.Fatalf("archive feature: %v", err)
	}
	doc, err := s.CreateDocument(repo.ID, "hidden.md", model.DocTypeUserDocs, "Hidden", "")
	if err != nil {
		t.Fatalf("seed doc: %v", err)
	}
	if err := s.SetDocumentArchived(doc.ID, true); err != nil {
		t.Fatalf("archive doc: %v", err)
	}

	// Default: setting is false, no flag → all three lists are empty.
	for _, path := range []string{"/repos/MINI/issues", "/repos/MINI/features", "/repos/MINI/documents"} {
		_, body := apiGet(t, ts.URL+path)
		var rows []json.RawMessage
		mustJSON(t, body, &rows)
		if len(rows) != 0 {
			t.Fatalf("%s default: got %d rows, want 0", path, len(rows))
		}
	}

	// Flip the global setting → all three lists surface the archived rows
	// without needing ?include_archived on the query string.
	if err := s.SetDisplayShowArchived(true); err != nil {
		t.Fatalf("set show_archived: %v", err)
	}
	for _, path := range []string{"/repos/MINI/issues", "/repos/MINI/features", "/repos/MINI/documents"} {
		_, body := apiGet(t, ts.URL+path)
		var rows []json.RawMessage
		mustJSON(t, body, &rows)
		if len(rows) != 1 {
			t.Fatalf("%s with setting on: got %d rows, want 1", path, len(rows))
		}
	}
}

// Dry-run over an already-archived row must preserve the original
// timestamp (nit #10 on PR #103). Without the fix, the projection
// stamps `now` even though the real write would no-op.
func TestArchiveDryRunPreservesExistingTimestamp(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "already archived")
	if err := s.SetIssueArchived(iss.ID, true); err != nil {
		t.Fatalf("seed archive: %v", err)
	}
	before, err := s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	if before.ArchivedAt == nil {
		t.Fatal("seed must have archived_at set")
	}
	resp, body := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/archive?dry_run=1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dry-run archive: status %d, body %s", resp.StatusCode, body)
	}
	got := decodeIssue(t, body)
	if got.ArchivedAt == nil || !got.ArchivedAt.Equal(*before.ArchivedAt) {
		t.Fatalf("dry-run must preserve existing archived_at; before=%v got=%v",
			before.ArchivedAt, got.ArchivedAt)
	}
}

func TestArchiveSweepHTTP(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "f", "F")
	iss, err := s.CreateIssue(repo.ID, &feat.ID, "i", "", model.StateDone, nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Manually archive the child so the sweep's feature-cascade pass
	// has something to chew on (the issue-age pass relies on
	// datetime('now') which the unit test sidesteps).
	if err := s.SetIssueArchived(iss.ID, true); err != nil {
		t.Fatalf("archive child: %v", err)
	}

	resp, body := apiPost(t, ts.URL+"/archive/sweep", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sweep: status %d, body %s", resp.StatusCode, body)
	}
	type sweepResp struct {
		IssuesArchived    int64 `json:"issues_archived"`
		FeaturesArchived  int64 `json:"features_archived"`
		DocumentsArchived int64 `json:"documents_archived"`
	}
	var got sweepResp
	mustJSON(t, body, &got)
	if got.FeaturesArchived != 1 {
		t.Fatalf("features_archived = %d, want 1", got.FeaturesArchived)
	}
}

func TestDisplayPreferencesRoundtrip(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})

	// Default — false.
	resp, body := apiGet(t, ts.URL+"/settings/display-preferences")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: status %d, body %s", resp.StatusCode, body)
	}
	type out struct {
		ShowArchived bool `json:"show_archived"`
	}
	var got out
	mustJSON(t, body, &got)
	if got.ShowArchived {
		t.Fatal("default show_archived must be false")
	}

	// Set true.
	resp, body = apiPut(t, ts.URL+"/settings/display-preferences", map[string]any{"show_archived": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set: status %d, body %s", resp.StatusCode, body)
	}
	mustJSON(t, body, &got)
	if !got.ShowArchived {
		t.Fatal("after PUT true, response must reflect true")
	}

	// Re-read — persists.
	_, body = apiGet(t, ts.URL+"/settings/display-preferences")
	mustJSON(t, body, &got)
	if !got.ShowArchived {
		t.Fatal("after PUT true, GET must still return true")
	}
}

// TestArchivePreferencesRoundtrip — BACI-162. GET returns the
// defaults (auto_enabled=true, retention_days=7). PUT writes a
// custom pair atomically. Subsequent GET reflects the write. PUT
// with retention_days=0 is rejected with 400.
func TestArchivePreferencesRoundtrip(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})

	type out struct {
		AutoEnabled   bool `json:"auto_enabled"`
		RetentionDays int  `json:"retention_days"`
	}

	// Default — auto_enabled=true, retention_days=7.
	resp, body := apiGet(t, ts.URL+"/settings/archive-preferences")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: status %d, body %s", resp.StatusCode, body)
	}
	var got out
	mustJSON(t, body, &got)
	if !got.AutoEnabled || got.RetentionDays != 7 {
		t.Fatalf("defaults: got %+v, want {true, 7}", got)
	}

	// Set custom — atomic write of both fields.
	resp, body = apiPut(t, ts.URL+"/settings/archive-preferences", map[string]any{
		"auto_enabled":   false,
		"retention_days": 14,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set: status %d, body %s", resp.StatusCode, body)
	}
	mustJSON(t, body, &got)
	if got.AutoEnabled || got.RetentionDays != 14 {
		t.Fatalf("after PUT: got %+v, want {false, 14}", got)
	}

	// Re-read — persists.
	_, body = apiGet(t, ts.URL+"/settings/archive-preferences")
	mustJSON(t, body, &got)
	if got.AutoEnabled || got.RetentionDays != 14 {
		t.Fatalf("after PUT, GET: got %+v, want {false, 14}", got)
	}

	// Zero retention_days is rejected — disable via the boolean
	// instead. Other out-of-range values 400 the same way.
	resp, body = apiPut(t, ts.URL+"/settings/archive-preferences", map[string]any{
		"auto_enabled":   true,
		"retention_days": 0,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PUT retention=0: status %d, want 400; body %s", resp.StatusCode, body)
	}
	resp, body = apiPut(t, ts.URL+"/settings/archive-preferences", map[string]any{
		"auto_enabled":   true,
		"retention_days": 99999,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PUT retention=huge: status %d, want 400; body %s", resp.StatusCode, body)
	}
}

// decodeIssue / decodeIssues / mustJSON are local helpers that
// duplicate the shape work helpers_test.go uses for other entities —
// not factored into helpers_test.go because the existing helpers there
// have evolved entity-specific shapes (e.g. assertHistoryOps), and
// keeping the archive helpers next to the archive tests keeps the
// blast radius of a future refactor small.
func decodeIssue(t *testing.T, body []byte) *model.Issue {
	t.Helper()
	var iss model.Issue
	mustJSON(t, body, &iss)
	return &iss
}

func decodeIssues(t *testing.T, body []byte) []*model.Issue {
	t.Helper()
	var issues []*model.Issue
	mustJSON(t, body, &issues)
	return issues
}

func mustJSON(t *testing.T, body []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("decode %T: %v (body: %s)", out, err, body)
	}
}
