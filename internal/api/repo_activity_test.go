// BACI-369: GET /repos/activity — the cross-repo activity summary the
// topbar's repository picker orders itself by.
package api_test

import (
	"net/http"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
)

func TestRepoActivityList(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedRepo2(t, s)
	seedIssue(t, s, repo, "something happened")

	resp, body := apiGet(t, ts.URL+"/repos/activity")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	var got []map[string]any
	mustJSON(t, body, &got)
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2 (one per repo): %s", len(got), body)
	}
	byPrefix := map[string]map[string]any{}
	for _, row := range got {
		prefix, _ := row["prefix"].(string)
		byPrefix[prefix] = row
	}
	mini, ok := byPrefix["MINI"]
	if !ok {
		t.Fatalf("MINI missing from payload: %s", body)
	}
	if _, ok := mini["active_jobs"].(float64); !ok {
		t.Errorf("active_jobs missing or not a number: %v", mini["active_jobs"])
	}
	if _, ok := mini["last_activity_at"].(string); !ok {
		t.Errorf("last_activity_at missing on a repo with an issue: %v", mini)
	}
	// The empty repo still ships a row, with the timestamp omitted.
	othr, ok := byPrefix["OTHR"]
	if !ok {
		t.Fatalf("OTHR missing from payload: %s", body)
	}
	if _, present := othr["last_activity_at"]; present {
		t.Errorf("last_activity_at should be omitted for an empty repo: %v", othr)
	}
}

// TestRepoActivityCountsRunningJobs exercises the in-flight count end to
// end so the wire number tracks a real running pipeline job.
func TestRepoActivityCountsRunningJobs(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "busy")
	proc, err := model.ProcessBySlug("plan-implement-ship")
	if err != nil {
		t.Fatalf("ProcessBySlug: %v", err)
	}
	jobs, err := s.SetIssueProcess(iss.ID, proc)
	if err != nil {
		t.Fatalf("SetIssueProcess: %v", err)
	}
	if err := s.SetPipelineJobStatus(jobs[0].ID, model.JobRunning); err != nil {
		t.Fatalf("SetPipelineJobStatus: %v", err)
	}

	resp, body := apiGet(t, ts.URL+"/repos/activity")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	var got []api.RepoActivityOut
	mustJSON(t, body, &got)
	if len(got) != 1 || got[0].Prefix != "MINI" {
		t.Fatalf("unexpected payload: %s", body)
	}
	if got[0].ActiveJobs != 1 {
		t.Errorf("active_jobs = %d, want 1", got[0].ActiveJobs)
	}
}

// TestRepoActivityRouteNotShadowed guards the literal-vs-wildcard
// registration: GET /repos/{prefix} must still resolve to repo-show.
func TestRepoActivityRouteNotShadowed(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)

	resp, body := apiGet(t, ts.URL+"/repos/MINI")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, body)
	}
	var repo model.Repo
	mustJSON(t, body, &repo)
	if repo.Prefix != "MINI" {
		t.Fatalf("prefix = %q, want MINI (route shadowed by /repos/activity?)", repo.Prefix)
	}
}
