package api_test

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
)

func TestPRsListEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/pull-requests")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("expected [], got %s", body)
	}
}

func TestPRsListPopulated(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	url := "https://github.com/example/x/pull/1"
	apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/pull-requests",
		`{"url":"`+url+`"}`)
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/pull-requests")
	if resp.StatusCode != 200 || !strings.Contains(string(body), url) {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
}

func TestPRAttachHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	url := "https://github.com/example/x/pull/42"
	resp, body := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/pull-requests",
		`{"url":"`+url+`"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), url) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, []string{"pr.attach"})
}

func TestPRAttachBadURL(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/pull-requests",
		`{"url":"javascript:alert(1)"}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestPRAttachEmptyURL(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/pull-requests",
		`{"url":""}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestPRAttachDuplicate(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	url := "https://github.com/example/x/pull/1"
	apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/pull-requests",
		`{"url":"`+url+`"}`)
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/pull-requests",
		`{"url":"`+url+`"}`)
	if resp.StatusCode != 409 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestPRAttachDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	url := "https://github.com/example/x/pull/1"
	resp, body := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/pull-requests?dry_run=true",
		`{"url":"`+url+`"}`)
	if resp.StatusCode != 201 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header: %q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	if !strings.Contains(string(body), url) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, nil)
}

func TestPRAttachURLKeyWins(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "real")
	other := seedIssue(t, s, repo, "other")
	url := "https://github.com/example/x/pull/1"
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/pull-requests",
		`{"issue_key":"`+other.Key+`","url":"`+url+`"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	prs, _ := s.ListPRs(iss.ID)
	if len(prs) != 1 {
		t.Fatalf("expected PR attached to URL issue, got %d", len(prs))
	}
}

func TestPRDetachBodyURL(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	url := "https://github.com/example/x/pull/1"
	apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/pull-requests",
		`{"url":"`+url+`"}`)
	resp, _ := apiDelete(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/pull-requests",
		`{"url":"`+url+`"}`)
	if resp.StatusCode != 204 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	assertHistoryOps(t, s, []string{"pr.attach", "pr.detach"})
}

func TestPRDetachQueryURL(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	url := "https://github.com/example/x/pull/1"
	apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/pull-requests",
		`{"url":"`+url+`"}`)
	resp, _ := apiDelete(t,
		ts.URL+"/repos/MINI/issues/"+iss.Key+"/pull-requests?url="+url, nil)
	if resp.StatusCode != 204 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestPRDetachNoMatch404(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	resp, _ := apiDelete(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/pull-requests",
		`{"url":"https://github.com/example/x/pull/99"}`)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestPRDetachDryRunMatch(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	url := "https://github.com/example/x/pull/1"
	apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/pull-requests",
		`{"url":"`+url+`"}`)
	resp, body := apiDelete(t,
		ts.URL+"/repos/MINI/issues/"+iss.Key+"/pull-requests?dry_run=1",
		`{"url":"`+url+`"}`)
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header: %q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	if !strings.Contains(string(body), `"would_remove": 1`) {
		t.Fatalf("body: %s", body)
	}
	prs, _ := s.ListPRs(iss.ID)
	if len(prs) != 1 {
		t.Fatalf("dry-run removed the PR: %d", len(prs))
	}
}

func TestPRDetachDryRunNoMatch(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "i")
	resp, _ := apiDelete(t,
		ts.URL+"/repos/MINI/issues/"+iss.Key+"/pull-requests?dry_run=true",
		`{"url":"https://github.com/example/x/pull/99"}`)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}
