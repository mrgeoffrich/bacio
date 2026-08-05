package api_test

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func TestIssuesListEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed != "[]" {
		t.Fatalf("expected [], got %q", trimmed)
	}
}

func TestIssuesListRepoNotFound(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiGet(t, ts.URL+"/repos/NONE/issues")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssuesListPopulatedAndLeanByDefault(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	if _, err := s.CreateIssue(repo.ID, nil, "first", "long body", model.StateTodo, nil, "", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "long body") {
		t.Fatalf("description leaked without with_description: %s", body)
	}
	resp2, body2 := apiGet(t, ts.URL+"/repos/MINI/issues?with_description=true")
	if resp2.StatusCode != 200 {
		t.Fatalf("status: %d", resp2.StatusCode)
	}
	if !strings.Contains(string(body2), "long body") {
		t.Fatalf("description missing with with_description=true: %s", body2)
	}
}

func TestIssuesListFilterByState(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	a, _ := s.CreateIssue(repo.ID, nil, "a", "", model.StateTodo, nil, "", "")
	b, _ := s.CreateIssue(repo.ID, nil, "b", "", model.StateTodo, nil, "", "")
	_ = s.SetIssueState(b.ID, model.StateDone)
	_ = a
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues?state=done")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"key": "MINI-2"`) || strings.Contains(string(body), `"key": "MINI-1"`) {
		t.Fatalf("filter mismatch: %s", body)
	}
}

func TestIssuesListFilterByFeature(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "auth", "Auth")
	if _, err := s.CreateIssue(repo.ID, &feat.ID, "with feat", "", model.StateTodo, nil, "", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.CreateIssue(repo.ID, nil, "no feat", "", model.StateTodo, nil, "", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues?feature=auth")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"with feat"`) || strings.Contains(string(body), `"no feat"`) {
		t.Fatalf("feature filter mismatch: %s", body)
	}
}

func TestIssuesListFilterByTag(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	if _, err := s.CreateIssue(repo.ID, nil, "tagged", "", model.StateTodo, []string{"ui"}, "", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.CreateIssue(repo.ID, nil, "untagged", "", model.StateTodo, nil, "", ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues?tag=ui")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"tagged"`) || strings.Contains(string(body), `"untagged"`) {
		t.Fatalf("tag filter mismatch: %s", body)
	}
}

func TestIssueShowHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "shown")
	resp, body := apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	for _, want := range []string{`"issue"`, `"comments"`, `"relations"`, `"pull_requests"`, `"documents"`, `"shown"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
}

func TestIssueShowMalformedKey(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiGet(t, ts.URL+"/repos/MINI/issues/not-a-key")
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssueShowNotFound(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiGet(t, ts.URL+"/repos/MINI/issues/MINI-999")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssueShowCrossRepoNotFound(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo1 := seedRepo(t, s)
	repo2 := seedRepo2(t, s)
	iss := seedIssue(t, s, repo2, "in other")
	_ = repo1
	resp, _ := apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssuesReadDoesNotWriteHistory(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	apiGet(t, ts.URL+"/repos/MINI/issues")
	apiGet(t, ts.URL+"/repos/MINI/issues/"+iss.Key)
	assertHistoryOps(t, s, nil)
}

func TestIssueCreateHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, body := apiPost(t, ts.URL+"/repos/MINI/issues",
		`{"title":"a new issue","tags":["ui"]}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"key": "MINI-1"`) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, []string{"issue.create"})
}

// TestIssueCreateAutoRun covers the BACI-374 toggle on the REST surface.
// Twin of TestCreateIssueAutoRun in internal/client — the two create
// paths must agree on what the same payload does.
func TestIssueCreateAutoRun(t *testing.T) {
	t.Run("arms_the_full_chain", func(t *testing.T) {
		ts, s := newTestAPI(t, api.Options{})
		seedRepo(t, s)
		resp, body := apiPost(t, ts.URL+"/repos/MINI/issues",
			`{"title":"drive it","auto_run":true}`)
		if resp.StatusCode != 201 {
			t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
		}
		// The 201 body must already read in_pipeline — the web composer's
		// optimistic card takes its column straight off this state.
		if !strings.Contains(string(body), `"state": "in_pipeline"`) {
			t.Fatalf("expected in_pipeline in the create response: %s", body)
		}
		iss, err := s.GetIssueByKey("MINI", 1)
		if err != nil {
			t.Fatalf("get issue: %v", err)
		}
		if iss.EngineMode != model.EngineAuto {
			t.Errorf("engine_mode = %q, want auto", iss.EngineMode)
		}
		assertAutoRunChain(t, s, iss.ID)
		assertHistoryOps(t, s, []string{"issue.create", "issue.process"})
	})

	t.Run("defaults_off", func(t *testing.T) {
		ts, s := newTestAPI(t, api.Options{})
		seedRepo(t, s)
		resp, body := apiPost(t, ts.URL+"/repos/MINI/issues", `{"title":"inert"}`)
		if resp.StatusCode != 201 {
			t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
		}
		iss, err := s.GetIssueByKey("MINI", 1)
		if err != nil {
			t.Fatalf("get issue: %v", err)
		}
		if iss.State != model.StateTodo {
			t.Errorf("state = %q, want todo", iss.State)
		}
		jobs, err := s.ListPipelineJobs(iss.ID)
		if err != nil {
			t.Fatalf("list jobs: %v", err)
		}
		if len(jobs) != 0 {
			t.Errorf("chain length = %d, want 0", len(jobs))
		}
	})

	t.Run("explicit_state_wins", func(t *testing.T) {
		ts, s := newTestAPI(t, api.Options{})
		seedRepo(t, s)
		resp, body := apiPost(t, ts.URL+"/repos/MINI/issues",
			`{"title":"already done","auto_run":true,"state":"done"}`)
		if resp.StatusCode != 201 {
			t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
		}
		iss, err := s.GetIssueByKey("MINI", 1)
		if err != nil {
			t.Fatalf("get issue: %v", err)
		}
		if iss.State != model.StateDone {
			t.Errorf("state = %q, want done", iss.State)
		}
		jobs, err := s.ListPipelineJobs(iss.ID)
		if err != nil {
			t.Fatalf("list jobs: %v", err)
		}
		if len(jobs) != 0 {
			t.Errorf("chain length = %d, want 0", len(jobs))
		}
	})

	t.Run("dry_run_projects_without_writing", func(t *testing.T) {
		ts, s := newTestAPI(t, api.Options{})
		seedRepo(t, s)
		resp, body := apiPost(t, ts.URL+"/repos/MINI/issues?dry_run=true",
			`{"title":"rehearse","auto_run":true}`)
		if resp.StatusCode != 201 {
			t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
		}
		if !strings.Contains(string(body), `"state": "in_pipeline"`) {
			t.Fatalf("expected the projection to read in_pipeline: %s", body)
		}
		assertHistoryOps(t, s, nil)
	})
}

// assertAutoRunChain checks the materialised chain matches the BACI-374
// auto-run preset: four pending jobs, ship last.
func assertAutoRunChain(t *testing.T, s *store.Store, issueID int64) {
	t.Helper()
	jobs, err := s.ListPipelineJobs(issueID)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	want := []string{
		model.BuiltinTemplateScope, model.BuiltinTemplatePlan,
		model.BuiltinTemplateImplement, model.ShipJobMode,
	}
	if len(jobs) != len(want) {
		t.Fatalf("chain length = %d, want %d", len(jobs), len(want))
	}
	for i, j := range jobs {
		if j.Mode != want[i] {
			t.Errorf("job %d mode = %q, want %q", i, j.Mode, want[i])
		}
		if j.Status != model.JobPending {
			t.Errorf("job %d status = %s, want pending", i, j.Status)
		}
	}
}

func TestIssueCreateDryRunQuery(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, body := apiPost(t, ts.URL+"/repos/MINI/issues?dry_run=true",
		`{"title":"would be"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("X-Dry-Run"); got != "applied" {
		t.Fatalf("X-Dry-Run header: %q", got)
	}
	if !strings.Contains(string(body), `"key": "MINI-1"`) {
		t.Fatalf("expected projected key MINI-1 in: %s", body)
	}
	assertHistoryOps(t, s, nil)
}

func TestIssueCreateDryRunHeader(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiReq(t, "POST", ts.URL+"/repos/MINI/issues",
		`{"title":"hdr"}`, map[string]string{"X-Dry-Run": "1"})
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Dry-Run"); got != "applied" {
		t.Fatalf("X-Dry-Run: %q", got)
	}
	assertHistoryOps(t, s, nil)
}

func TestIssueCreateTitleRequired(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues", `{}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	assertHistoryOps(t, s, nil)
}

func TestIssueCreateUnknownField(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues", `{"title":"x","mystery":1}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssueCreateUnknownFeature(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiPost(t, ts.URL+"/repos/MINI/issues",
		`{"title":"x","feature_slug":"nope"}`)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	assertHistoryOps(t, s, nil)
}

func TestIssueCreateRepoNotFound(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiPost(t, ts.URL+"/repos/NONE/issues", `{"title":"x"}`)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssueStateHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, body := apiPut(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/state",
		`{"state":"todo"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"state": "todo"`) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, []string{"issue.state"})
}

func TestIssueStateBadState(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, _ := apiPut(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/state",
		`{"state":"bogus"}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	assertHistoryOps(t, s, nil)
}

func TestIssueStateDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, body := apiPut(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/state?dry_run=1",
		`{"state":"done"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("X-Dry-Run header missing")
	}
	if !strings.Contains(string(body), `"state": "done"`) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, nil)
}

func TestIssueStateURLKeyWins(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	a := seedIssue(t, s, repo, "a")
	b := seedIssue(t, s, repo, "b")
	resp, _ := apiPut(t, ts.URL+"/repos/MINI/issues/"+a.Key+"/state",
		`{"key":"`+b.Key+`","state":"done"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	updatedA, _ := s.GetIssueByID(a.ID)
	updatedB, _ := s.GetIssueByID(b.ID)
	if updatedA.State != model.StateDone || updatedB.State == model.StateDone {
		t.Fatalf("URL did not win: a=%s b=%s", updatedA.State, updatedB.State)
	}
}

func TestIssueStateCrossRepo(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	repo2 := seedRepo2(t, s)
	iss := seedIssue(t, s, repo2, "x")
	_ = repo
	resp, _ := apiPut(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/state",
		`{"state":"done"}`)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssueAssignHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, body := apiPut(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/assignee",
		`{"assignee":"alice"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"assignee": "alice"`) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, []string{"issue.assign"})
}

func TestIssueAssignEmptyName(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, _ := apiPut(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/assignee",
		`{"assignee":"  "}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	assertHistoryOps(t, s, nil)
}

func TestIssueAssignDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, body := apiPut(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/assignee?dry_run=true",
		`{"assignee":"bob"}`)
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header: %q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	if !strings.Contains(string(body), `"assignee": "bob"`) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, nil)
}

func TestIssueUnassignHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	if err := s.SetIssueAssignee(iss.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	resp, body := apiDelete(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/assignee", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), `"assignee":`) {
		// assignee field has omitempty so an empty string drops it; just
		// confirm it isn't present.
		t.Fatalf("assignee should be cleared: %s", body)
	}
	assertHistoryOps(t, s, []string{"issue.assign"})
}

func TestIssueUnassignAlreadyEmptyNoHistory(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, _ := apiDelete(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/assignee", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	assertHistoryOps(t, s, nil)
}

func TestIssueEditTitleAndDescription(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "old")
	resp, body := apiPatch(t, ts.URL+"/repos/MINI/issues/"+iss.Key,
		`{"title":"new","description":"body"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"title": "new"`) ||
		!strings.Contains(string(body), `"description": "body"`) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, []string{"issue.update"})
}

// TestIssueEditCustomerImpact (BACI-349) exercises the PATCH path for the
// customer_impact field: set a value, read it back on the issue, then
// clear it (present-but-empty → "no impact" state). model.Issue's
// customer_impact tag is omitempty, so the cleared field drops out of the
// JSON entirely.
func TestIssueEditCustomerImpact(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "fix login")

	resp, body := apiPatch(t, ts.URL+"/repos/MINI/issues/"+iss.Key,
		`{"customer_impact":"Login no longer 500s on Safari"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("set status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"customer_impact": "Login no longer 500s on Safari"`) {
		t.Fatalf("impact not set: %s", body)
	}
	if got, _ := s.GetIssueByID(iss.ID); got.CustomerImpact != "Login no longer 500s on Safari" {
		t.Fatalf("store impact = %q, want the Safari line", got.CustomerImpact)
	}

	// Present-but-empty clears it back to "" (omitempty drops it from JSON).
	resp, body = apiPatch(t, ts.URL+"/repos/MINI/issues/"+iss.Key, `{"customer_impact":""}`)
	if resp.StatusCode != 200 {
		t.Fatalf("clear status: %d, body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), `"customer_impact"`) {
		t.Fatalf("impact not cleared from JSON: %s", body)
	}
	if got, _ := s.GetIssueByID(iss.ID); got.CustomerImpact != "" {
		t.Fatalf("store impact after clear = %q, want empty", got.CustomerImpact)
	}
}

func TestIssueEditNullDescription(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss, _ := s.CreateIssue(repo.ID, nil, "x", "had body", model.StateTodo, nil, "", "")
	resp, body := apiPatch(t, ts.URL+"/repos/MINI/issues/"+iss.Key,
		`{"description":null}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "had body") {
		t.Fatalf("description not cleared: %s", body)
	}
}

func TestIssueEditFeatureClear(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "feat", "F")
	iss, _ := s.CreateIssue(repo.ID, &feat.ID, "x", "", model.StateTodo, nil, "", "")
	resp, body := apiPatch(t, ts.URL+"/repos/MINI/issues/"+iss.Key,
		`{"feature_slug":null}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if strings.Contains(string(body), `"feature_slug"`) {
		t.Fatalf("feature_slug not cleared: %s", body)
	}
}

func TestIssueEditFeatureChange(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "old-feat", "Old")
	feat2 := seedFeature(t, s, repo, "new-feat", "New")
	iss, _ := s.CreateIssue(repo.ID, &feat.ID, "x", "", model.StateTodo, nil, "", "")
	_ = feat2
	resp, body := apiPatch(t, ts.URL+"/repos/MINI/issues/"+iss.Key,
		`{"feature_slug":"new-feat"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"feature_slug": "new-feat"`) {
		t.Fatalf("body: %s", body)
	}
}

func TestIssueEditTitleEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, _ := apiPatch(t, ts.URL+"/repos/MINI/issues/"+iss.Key,
		`{"title":""}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssueEditNothingToUpdate(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, _ := apiPatch(t, ts.URL+"/repos/MINI/issues/"+iss.Key, `{}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssueEditUnknownField(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, _ := apiPatch(t, ts.URL+"/repos/MINI/issues/"+iss.Key,
		`{"title":"x","mystery":1}`)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssueEditDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	resp, body := apiPatch(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"?dry_run=true",
		`{"title":"projected"}`)
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header: %q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	if !strings.Contains(string(body), `"title": "projected"`) {
		t.Fatalf("body: %s", body)
	}
	assertHistoryOps(t, s, nil)
	roundtrip, _ := s.GetIssueByID(iss.ID)
	if roundtrip.Title != "x" {
		t.Fatalf("title was actually changed: %s", roundtrip.Title)
	}
}

func TestIssueEditURLKeyWins(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	a := seedIssue(t, s, repo, "a")
	b := seedIssue(t, s, repo, "b")
	resp, _ := apiPatch(t, ts.URL+"/repos/MINI/issues/"+a.Key,
		`{"key":"`+b.Key+`","title":"renamed"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	updatedA, _ := s.GetIssueByID(a.ID)
	updatedB, _ := s.GetIssueByID(b.ID)
	if updatedA.Title != "renamed" || updatedB.Title == "renamed" {
		t.Fatalf("URL did not win: a=%s b=%s", updatedA.Title, updatedB.Title)
	}
}

func TestIssueDeleteHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "Captured Title")
	resp, body := apiDelete(t, ts.URL+"/repos/MINI/issues/"+iss.Key, nil)
	if resp.StatusCode != 204 {
		t.Fatalf("status: %d, body=%s", resp.StatusCode, body)
	}
	if len(body) != 0 {
		t.Fatalf("body should be empty: %q", body)
	}
	assertHistoryOps(t, s, []string{"issue.delete"})
	rows, _ := s.ListHistory(store.HistoryFilter{OldestFirst: true})
	if rows[0].Details != "Captured Title" {
		t.Fatalf("audit details: %q", rows[0].Details)
	}
}

func TestIssueDeleteDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "preview")
	resp, body := apiDelete(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"?dry_run=true", nil)
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header: %q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	for _, want := range []string{`"would_delete": true`, `"cascade"`, `"comments"`, `"tags"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
	assertHistoryOps(t, s, nil)
	roundtrip, err := s.GetIssueByID(iss.ID)
	if err != nil || roundtrip == nil {
		t.Fatalf("issue was actually deleted: %v", err)
	}
}

func TestIssueDeleteNotFound(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiDelete(t, ts.URL+"/repos/MINI/issues/MINI-999", nil)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestIssueUnassignDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "x")
	_ = s.SetIssueAssignee(iss.ID, "alice")
	resp, _ := apiDelete(t, ts.URL+"/repos/MINI/issues/"+iss.Key+"/assignee?dry_run=1", nil)
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d, header: %q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	assertHistoryOps(t, s, nil)
}
