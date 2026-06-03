package api_test

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func TestFeaturePeekEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features/auth/next")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"issue": null`) {
		t.Fatalf("expected issue: null, got: %s", body)
	}
	assertHistoryOps(t, s, nil)
}

func TestFeaturePeekClaimable(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	if _, err := s.CreateIssue(repo.ID, &feat.ID, "ready", "", model.StateTodo, nil, "", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/features/auth/next")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"key": "MINI-1"`) {
		t.Fatalf("expected MINI-1 issue, got: %s", body)
	}
	assertHistoryOps(t, s, nil)
}

func TestFeaturePeekDoesNotMutate(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	iss, _ := s.CreateIssue(repo.ID, &feat.ID, "ready", "", model.StateTodo, nil, "", "")
	apiGet(t, ts.URL+"/repos/MINI/features/auth/next")
	roundtrip, _ := s.GetIssueByID(iss.ID)
	if roundtrip.State != model.StateTodo || roundtrip.Assignee != "" {
		t.Fatalf("peek mutated state: state=%s assignee=%s", roundtrip.State, roundtrip.Assignee)
	}
}

func TestFeatureClaimRequiresActor(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	if _, err := s.CreateIssue(repo.ID, &feat.ID, "ready", "", model.StateTodo, nil, "", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, body := apiPost(t, ts.URL+"/repos/MINI/features/auth/next", nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "X-Actor is required to claim work") {
		t.Fatalf("expected X-Actor error: %s", body)
	}
	assertHistoryOps(t, s, nil)
}

func TestFeatureClaimHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	iss, _ := s.CreateIssue(repo.ID, &feat.ID, "ready", "", model.StateTodo, nil, "", "")
	resp, body := apiReq(t, "POST", ts.URL+"/repos/MINI/features/auth/next", nil,
		map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	for _, want := range []string{
		`"key": "MINI-1"`, `"state": "todo"`, `"assignee": "agent-alice"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
	roundtrip, _ := s.GetIssueByID(iss.ID)
	if roundtrip.State != model.StateTodo || roundtrip.Assignee != "agent-alice" {
		t.Fatalf("claim did not persist: state=%s assignee=%s", roundtrip.State, roundtrip.Assignee)
	}
	assertHistoryOps(t, s, []string{"issue.claim"})
}

func TestFeatureClaimAuditDetails(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	if _, err := s.CreateIssue(repo.ID, &feat.ID, "ready", "", model.StateTodo, nil, "", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	apiReq(t, "POST", ts.URL+"/repos/MINI/features/auth/next", nil,
		map[string]string{"X-Actor": "agent-bob"})
	rows, _ := s.ListHistory(store.HistoryFilter{OldestFirst: true})
	if len(rows) != 1 {
		t.Fatalf("history rows: %d", len(rows))
	}
	if !strings.Contains(rows[0].Details, "claimed by agent-bob") {
		t.Fatalf("audit details: %q", rows[0].Details)
	}
	if rows[0].Actor != "agent-bob" {
		t.Fatalf("audit actor: %q", rows[0].Actor)
	}
}

func TestFeatureClaimNothingClaimable(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")
	resp, body := apiReq(t, "POST", ts.URL+"/repos/MINI/features/auth/next", nil,
		map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"issue": null`) {
		t.Fatalf("expected issue: null, got: %s", body)
	}
	assertHistoryOps(t, s, nil)
}

func TestFeatureClaimDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	iss, _ := s.CreateIssue(repo.ID, &feat.ID, "ready", "", model.StateTodo, nil, "", "")
	resp, body := apiReq(t, "POST", ts.URL+"/repos/MINI/features/auth/next?dry_run=true", nil,
		map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header=%q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	if !strings.Contains(string(body), `"key": "MINI-1"`) {
		t.Fatalf("expected projected MINI-1: %s", body)
	}
	roundtrip, _ := s.GetIssueByID(iss.ID)
	if roundtrip.State != model.StateTodo || roundtrip.Assignee != "" {
		t.Fatalf("dry-run mutated state: %+v", roundtrip)
	}
	assertHistoryOps(t, s, nil)
}

func TestFeatureClaimSlugNotFound(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiReq(t, "POST", ts.URL+"/repos/MINI/features/nope/next", nil,
		map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}
