package api_test

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
)

func TestCommentsListEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/comments")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("expected [], got %s", body)
	}
}

func TestCommentAddHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, body := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/comments",
		`{"author":"alice","body":"hello"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	for _, want := range []string{`"author": "alice"`, `"body": "hello"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
	assertHistoryOps(t, s, []string{"comment.add"})
}

func TestCommentAddDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, body := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/comments?dry_run=true",
		`{"author":"alice","body":"hello"}`)
	if resp.StatusCode != 201 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header=%q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	if !strings.Contains(string(body), `"body": "hello"`) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, nil)
}

func TestCommentAddMissingAuthor(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/comments",
		`{"body":"hello"}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestCommentAddMissingBody(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/comments",
		`{"author":"alice"}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestCommentAddURLKeyWins(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	a := seedIssue(t, s, repo, "a")
	b := seedIssue(t, s, repo, "b")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues/"+a.Key+"/comments",
		`{"issue_key":"`+b.Key+`","author":"x","body":"hi"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	csA, _ := s.ListComments(a.ID)
	csB, _ := s.ListComments(b.ID)
	if len(csA) != 1 || len(csB) != 0 {
		t.Fatalf("URL did not win: a=%d b=%d", len(csA), len(csB))
	}
}

func TestCommentAddCrossRepo(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	repo2 := seedRepo2(t, s)
	iss := seedIssue(t, s, repo2, "x")
	_ = repo
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/comments",
		`{"author":"a","body":"b"}`)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestCommentAddBadAuthor(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/comments",
		`{"author":"bad\tname","body":"b"}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}
