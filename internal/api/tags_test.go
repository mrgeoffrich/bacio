package api_test

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
)

func TestTagsAddHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	resp, body := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/tags",
		`{"tags":["ui","tui"]}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	for _, want := range []string{`"ui"`, `"tui"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
	assertHistoryOps(t, s, []string{"tag.add"})
}

func TestTagsAddIdempotent(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/tags", `{"tags":["ui"]}`)
	apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/tags", `{"tags":["ui"]}`)
	updated, _ := s.GetIssueByID(iss.ID)
	if len(updated.Tags) != 1 || updated.Tags[0] != "ui" {
		t.Fatalf("expected single ui tag, got %v", updated.Tags)
	}
}

func TestTagsAddDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	resp, body := apiPost(t,
		ts.URL+"/repos/MINI/issues/"+iss.Key+"/tags?dry_run=true",
		`{"tags":["ui","tui"]}`)
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header: %q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	for _, want := range []string{`"ui"`, `"tui"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
	updated, _ := s.GetIssueByID(iss.ID)
	if len(updated.Tags) != 0 {
		t.Fatalf("dry-run wrote tags: %v", updated.Tags)
	}
	assertHistoryOps(t, s, nil)
}

func TestTagsAddEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/tags",
		`{"tags":[]}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestTagsAddBadTag(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/tags",
		`{"tags":["bad tag"]}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestTagsRemoveHappyAuditOpName(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/tags",
		`{"tags":["ui","tui"]}`)
	resp, body := apiDelete(t,
		ts.URL+"/repos/MINI/issues/"+iss.Key+"/tags",
		`{"tags":["ui"]}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), `"ui"`) {
		t.Fatalf("ui still present: %s", body)
	}
	if !strings.Contains(string(body), `"tui"`) {
		t.Fatalf("tui missing: %s", body)
	}
	assertHistoryOps(t, s, []string{"tag.add", "tag.remove"})
}

func TestTagsRemoveDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/tags",
		`{"tags":["ui","tui"]}`)
	resp, body := apiDelete(t,
		ts.URL+"/repos/MINI/issues/"+iss.Key+"/tags?dry_run=1",
		`{"tags":["ui"]}`)
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header: %q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	if strings.Contains(string(body), `"ui"`) {
		t.Fatalf("ui still in body: %s", body)
	}
	updated, _ := s.GetIssueByID(iss.ID)
	if len(updated.Tags) != 2 {
		t.Fatalf("dry-run mutated DB: %v", updated.Tags)
	}
}

func TestTagsRemoveAbsentNoOp(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	resp, _ := apiDelete(t,
		ts.URL+"/repos/MINI/issues/"+iss.Key+"/tags",
		`{"tags":["nonexistent"]}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	assertHistoryOps(t, s, []string{"tag.remove"})
}

func TestTagsURLKeyWins(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "real")
	other := seedIssue(t, s, repo, "other")
	apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/tags",
		`{"issue_key":"`+other.Key+`","tags":["ui"]}`)
	updated, _ := s.GetIssueByID(iss.ID)
	if len(updated.Tags) != 1 || updated.Tags[0] != "ui" {
		t.Fatalf("URL key did not win: %v", updated.Tags)
	}
	otherIss, _ := s.GetIssueByID(other.ID)
	if len(otherIss.Tags) != 0 {
		t.Fatalf("body issue_key applied: %v", otherIss.Tags)
	}
}

func TestTagsCrossRepo(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	repo2 := seedRepo2(t, s)
	other := seedIssue(t, s, repo2, "other")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues/"+other.Key+"/tags",
		`{"tags":["ui"]}`)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}
