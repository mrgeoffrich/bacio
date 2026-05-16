package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// ---------- dispatch create + list-per-repo (BACI-35) ----------
//
// inbox + ack are covered by the BACI-34 tests on agent_test.go; this
// file focuses on the two routes the BACI-35 follow-up added.

// seedAgentIdentity inserts a persistent agent identity by name.
func seedAgentIdentity(t *testing.T, s *store.Store, name string) *model.Agent {
	t.Helper()
	ag, _, err := s.UpsertAgent(name, true)
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	return ag
}

// seedAgentSession inserts a registered AgentSession against repo and
// the given agent identity (nil for an identity-less session).
func seedAgentSession(t *testing.T, s *store.Store, repo *model.Repo, sessionID string, ag *model.Agent) *model.AgentSession {
	t.Helper()
	in := store.UpsertAgentSessionIn{
		SessionID:      sessionID,
		RepoID:         repo.ID,
		Actor:          "agent-test",
		MarkRegistered: true,
	}
	if ag != nil {
		in.AgentID = &ag.ID
	}
	sess, err := s.UpsertAgentSession(in)
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return sess
}

func TestDispatchCreateHappyPath(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "ship it")
	ag := seedAgentIdentity(t, s, "swift-otter@claude.test")
	_ = seedAgentSession(t, s, repo, "sess-1", ag)

	body := map[string]any{
		"target_agent": ag.Name,
		"issue_key":    iss.Key,
		"mode":         string(model.DispatchModeImplement),
		"message":      "go",
	}
	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/agents/dispatches", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
	var got model.AgentDispatch
	mustDecode(t, raw, &got)
	if got.ID == 0 {
		t.Fatalf("expected an id, got 0")
	}
	if got.RepoID != repo.ID {
		t.Errorf("RepoID = %d, want %d", got.RepoID, repo.ID)
	}
	if got.IssueKey != iss.Key {
		t.Errorf("IssueKey = %q, want %q", got.IssueKey, iss.Key)
	}
	if got.TargetAgentName != ag.Name {
		t.Errorf("TargetAgentName = %q, want %q", got.TargetAgentName, ag.Name)
	}
	if got.Status != model.DispatchPending {
		t.Errorf("Status = %q, want pending", got.Status)
	}
	if got.CreatedBy != "api" {
		t.Errorf("CreatedBy = %q, want %q", got.CreatedBy, "api")
	}
	assertHistoryOps(t, s, []string{"agent.dispatch"})
}

func TestDispatchCreateDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "ship it")
	ag := seedAgentIdentity(t, s, "swift-otter@claude.test")

	body := map[string]any{
		"target_agent": ag.Name,
		"issue_key":    iss.Key,
		"mode":         string(model.DispatchModeImplement),
	}
	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/agents/dispatches?dry_run=1", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("X-Dry-Run"); got != "applied" {
		t.Fatalf("X-Dry-Run = %q, want applied", got)
	}
	var proj model.AgentDispatch
	mustDecode(t, raw, &proj)
	if proj.ID != 0 {
		t.Errorf("dry-run projection has id %d, want 0 (server-time field)", proj.ID)
	}
	if proj.IssueKey != iss.Key {
		t.Errorf("IssueKey = %q, want %q", proj.IssueKey, iss.Key)
	}

	ds, err := s.ListDispatches(store.DispatchFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("ListDispatches: %v", err)
	}
	if len(ds) != 0 {
		t.Fatalf("dry-run wrote %d dispatch row(s), want 0", len(ds))
	}
	assertHistoryOps(t, s, nil)
}

func TestDispatchCreateNoTarget(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/agents/dispatches",
		map[string]any{"message": "hi"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
	var env map[string]any
	mustDecode(t, raw, &env)
	if env["code"] != "invalid_input" {
		t.Errorf("code = %v, want invalid_input", env["code"])
	}
}

func TestDispatchCreateUnknownAgent(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/agents/dispatches",
		map[string]any{"target_agent": "ghost@claude.nowhere"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
}

func TestDispatchCreateUnknownSession(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/agents/dispatches",
		map[string]any{"target_session": "00000000-0000-0000-0000-000000000000"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
}

func TestDispatchCreateIssueInOtherRepo(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	other := seedRepo2(t, s)
	otherIss := seedIssue(t, s, other, "lives elsewhere")
	ag := seedAgentIdentity(t, s, "swift-otter@claude.test")

	// Dispatch under MINI but pointing at an OTHR-N issue: must 404.
	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/agents/dispatches",
		map[string]any{
			"target_agent": ag.Name,
			"issue_key":    otherIss.Key,
		})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
}

func TestDispatchesList(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	other := seedRepo2(t, s)
	iss := seedIssue(t, s, repo, "ship it")
	otherIss := seedIssue(t, s, other, "other")
	ag := seedAgentIdentity(t, s, "swift-otter@claude.test")

	// Two dispatches against MINI, one against OTHR.
	for i := 0; i < 2; i++ {
		_, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/agents/dispatches",
			map[string]any{
				"target_agent": ag.Name,
				"issue_key":    iss.Key,
				"message":      fmt.Sprintf("dispatch #%d", i),
			})
		if len(raw) == 0 {
			t.Fatalf("expected body")
		}
	}
	_, _ = apiPost(t, ts.URL+"/repos/"+other.Prefix+"/agents/dispatches",
		map[string]any{"target_agent": ag.Name, "issue_key": otherIss.Key})

	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/agents/dispatches")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
	var got []model.AgentDispatch
	mustDecode(t, raw, &got)
	if len(got) != 2 {
		t.Fatalf("got %d dispatches in MINI, want 2 (cross-repo leak?)", len(got))
	}
	for _, d := range got {
		if d.RepoID != repo.ID {
			t.Errorf("dispatch %d has RepoID %d, want %d", d.ID, d.RepoID, repo.ID)
		}
	}
}

func TestDispatchesListEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/agents/dispatches")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if string(raw) != "[]\n" && string(raw) != "[]" {
		t.Fatalf("empty list body = %q, want []", string(raw))
	}
}

// mustDecode is a tiny helper so each test doesn't repeat the
// json.Unmarshal + error-check boilerplate.
func mustDecode(t *testing.T, raw []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode %T: %v (body: %s)", out, err, string(raw))
	}
}
