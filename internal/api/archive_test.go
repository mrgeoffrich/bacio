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
