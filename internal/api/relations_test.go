package api_test

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
)

func TestRelationCreateBlocks(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	a := seedIssue(t, s, repo, "a")
	b := seedIssue(t, s, repo, "b")
	resp, body := apiPost(t, ts.URL+"/repos/MINI/relations",
		`{"from":"`+a.Key+`","type":"blocks","to":"`+b.Key+`"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	for _, want := range []string{`"from_issue": "MINI-1"`, `"to_issue": "MINI-2"`, `"type": "blocks"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
	assertHistoryOps(t, s, []string{"relation.create"})
}

func TestRelationCreateRelatesToHyphen(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	a := seedIssue(t, s, repo, "a")
	b := seedIssue(t, s, repo, "b")
	resp, body := apiPost(t, ts.URL+"/repos/MINI/relations",
		`{"from":"`+a.Key+`","type":"relates-to","to":"`+b.Key+`"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"type": "relates_to"`) {
		t.Fatalf("body: %s", body)
	}
}

func TestRelationCreateDuplicateOf(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	a := seedIssue(t, s, repo, "a")
	b := seedIssue(t, s, repo, "b")
	resp, body := apiPost(t, ts.URL+"/repos/MINI/relations",
		`{"from":"`+a.Key+`","type":"duplicate_of","to":"`+b.Key+`"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"type": "duplicate_of"`) {
		t.Fatalf("body: %s", body)
	}
}

func TestRelationCreateBadType(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	a := seedIssue(t, s, repo, "a")
	b := seedIssue(t, s, repo, "b")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/relations",
		`{"from":"`+a.Key+`","type":"bogus","to":"`+b.Key+`"}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestRelationCreateSelfLink(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	a := seedIssue(t, s, repo, "a")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/relations",
		`{"from":"`+a.Key+`","type":"blocks","to":"`+a.Key+`"}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestRelationCreateFromCrossRepo(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	repo2 := seedRepo2(t, s)
	a := seedIssue(t, s, repo2, "a")
	b := seedIssue(t, s, repo, "b")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/relations",
		`{"from":"`+a.Key+`","type":"blocks","to":"`+b.Key+`"}`)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestRelationCreateToCrossRepoOK(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	repo2 := seedRepo2(t, s)
	a := seedIssue(t, s, repo, "a")
	b := seedIssue(t, s, repo2, "b")
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/relations",
		`{"from":"`+a.Key+`","type":"blocks","to":"`+b.Key+`"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestRelationCreateDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	a := seedIssue(t, s, repo, "a")
	b := seedIssue(t, s, repo, "b")
	resp, body := apiPost(t, ts.URL+"/repos/MINI/relations?dry_run=true",
		`{"from":"`+a.Key+`","type":"blocks","to":"`+b.Key+`"}`)
	if resp.StatusCode != 201 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header: %q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	if !strings.Contains(string(body), `"type": "blocks"`) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, nil)
}

func TestRelationDeleteHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	a := seedIssue(t, s, repo, "a")
	b := seedIssue(t, s, repo, "b")
	apiPost(t, ts.URL+"/repos/MINI/relations",
		`{"from":"`+a.Key+`","type":"blocks","to":"`+b.Key+`"}`)
	resp, body := apiDelete(t, ts.URL+"/repos/MINI/relations",
		`{"a":"`+a.Key+`","b":"`+b.Key+`"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"removed": 1`) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, []string{"relation.create", "relation.delete"})
}


func TestRelationDeleteDryRunCount(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	a := seedIssue(t, s, repo, "a")
	b := seedIssue(t, s, repo, "b")
	apiPost(t, ts.URL+"/repos/MINI/relations",
		`{"from":"`+a.Key+`","type":"blocks","to":"`+b.Key+`"}`)
	resp, body := apiDelete(t, ts.URL+"/repos/MINI/relations?dry_run=1",
		`{"a":"`+a.Key+`","b":"`+b.Key+`"}`)
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header: %q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	if !strings.Contains(string(body), `"would_remove": 1`) {
		t.Fatalf("body: %s", body)
	}
}

func TestRelationDeleteNoMatchStill200(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	a := seedIssue(t, s, repo, "a")
	b := seedIssue(t, s, repo, "b")
	resp, body := apiDelete(t, ts.URL+"/repos/MINI/relations",
		`{"a":"`+a.Key+`","b":"`+b.Key+`"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"removed": 0`) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, nil)
}
