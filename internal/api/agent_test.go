package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// ---------- helpers ----------

func registerSession(t *testing.T, ts string, prefix, sid string, body map[string]any) (int, []byte) {
	t.Helper()
	if body == nil {
		body = map[string]any{"session_id": sid, "actor": "agent-alice"}
	}
	resp, raw := apiReq(t, "POST", ts+"/repos/"+prefix+"/agents/sessions", body,
		map[string]string{"X-Actor": "agent-alice"})
	return resp.StatusCode, raw
}

// ---------- register ----------

func TestAgentRegisterHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	status, body := registerSession(t, ts.URL, "MINI", "sess-1", nil)
	if status != 201 {
		t.Fatalf("status: %d body: %s", status, body)
	}
	var sess model.AgentSession
	if err := json.Unmarshal(body, &sess); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sess.SessionID != "sess-1" || sess.Actor != "agent-alice" {
		t.Fatalf("unexpected session: %+v", sess)
	}
	assertHistoryOps(t, s, []string{"agent.register"})
}

func TestAgentRegisterDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, body := apiReq(t, "POST",
		ts.URL+"/repos/MINI/agents/sessions?dry_run=true",
		map[string]any{"session_id": "sess-dr", "actor": "agent-dr"},
		map[string]string{"X-Actor": "agent-dr"})
	if resp.StatusCode != 201 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d header=%q body=%s", resp.StatusCode, resp.Header.Get("X-Dry-Run"), body)
	}
	// No session row written.
	if _, err := s.GetAgentSession("sess-dr"); err == nil {
		t.Fatalf("dry-run wrote session row")
	}
	assertHistoryOps(t, s, nil)
}

func TestAgentRegisterUnknownField(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, body := apiReq(t, "POST", ts.URL+"/repos/MINI/agents/sessions",
		map[string]any{"session_id": "x", "actor": "a", "bogus": true},
		map[string]string{"X-Actor": "a"})
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d, body: %s", resp.StatusCode, body)
	}
}

func TestAgentRegisterMissingSessionID(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, body := apiReq(t, "POST", ts.URL+"/repos/MINI/agents/sessions",
		map[string]any{"actor": "a"}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d, body: %s", resp.StatusCode, body)
	}
	env := decode[map[string]any](t, strings.NewReader(string(body)))
	if details, _ := env["details"].(map[string]any); details["field"] != "session_id" {
		t.Fatalf("details.field: %v", details["field"])
	}
}

func TestAgentRegisterRepoNotFound(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiReq(t, "POST", ts.URL+"/repos/NONE/agents/sessions",
		map[string]any{"session_id": "x", "actor": "a"},
		map[string]string{"X-Actor": "a"})
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestAgentRegisterActorDefaultsToHeader(t *testing.T) {
	// Body omits actor; server should fall back to X-Actor and stamp the
	// session row with it.
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, body := apiReq(t, "POST", ts.URL+"/repos/MINI/agents/sessions",
		map[string]any{"session_id": "sess-h"},
		map[string]string{"X-Actor": "agent-from-header"})
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	sess, err := s.GetAgentSession("sess-h")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.Actor != "agent-from-header" {
		t.Fatalf("actor: %q", sess.Actor)
	}
}

func TestAgentRegisterIdempotent(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	for i := 0; i < 2; i++ {
		status, body := registerSession(t, ts.URL, "MINI", "sess-id", nil)
		if status != 201 {
			t.Fatalf("attempt %d: status %d body %s", i, status, body)
		}
	}
	// Two register calls → two audit rows but only one session row.
	rows, _ := s.ListHistory(store.HistoryFilter{})
	count := 0
	for _, r := range rows {
		if r.Op == "agent.register" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected 2 agent.register entries, got %d", count)
	}
}

func TestAgentRegisterAgentNameConflict(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	// First register mints the identity.
	status, _ := registerSession(t, ts.URL, "MINI", "sess-a", map[string]any{
		"session_id": "sess-a", "actor": "agent-a", "agent": "lively-frog@claude.x",
	})
	if status != 201 {
		t.Fatalf("first register status: %d", status)
	}
	// Second tries to claim the same name with --new (NewIdentity=true) → 409.
	resp, body := apiReq(t, "POST", ts.URL+"/repos/MINI/agents/sessions",
		map[string]any{
			"session_id":   "sess-b",
			"actor":        "agent-b",
			"agent":        "lively-frog@claude.x",
			"new_identity": true,
		}, map[string]string{"X-Actor": "agent-b"})
	if resp.StatusCode != 409 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
}

// ---------- heartbeat ----------

func TestAgentHeartbeatHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	registerSession(t, ts.URL, "MINI", "sess-hb", nil)
	resp, body := apiReq(t, "POST", ts.URL+"/agents/sessions/sess-hb/heartbeat",
		map[string]any{"session_id": "sess-hb", "model": "claude-sonnet-4-6"}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	sess, _ := s.GetAgentSession("sess-hb")
	if sess.Model != "claude-sonnet-4-6" {
		t.Fatalf("model not updated: %q", sess.Model)
	}
}

func TestAgentHeartbeatSessionMismatch(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	registerSession(t, ts.URL, "MINI", "sess-hb", nil)
	resp, _ := apiReq(t, "POST", ts.URL+"/agents/sessions/sess-hb/heartbeat",
		map[string]any{"session_id": "different"}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestAgentHeartbeatSessionMissing(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiReq(t, "POST", ts.URL+"/agents/sessions/ghost/heartbeat",
		nil, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

// ---------- end ----------

func TestAgentEndHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	registerSession(t, ts.URL, "MINI", "sess-end", nil)
	resp, body := apiReq(t, "POST", ts.URL+"/agents/sessions/sess-end/end",
		map[string]any{"reason": "stop"},
		map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	sess, _ := s.GetAgentSession("sess-end")
	if sess.EndedAt == nil || sess.EndReason != "stop" {
		t.Fatalf("session not ended: %+v", sess)
	}
}

func TestAgentEndBadReason(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	registerSession(t, ts.URL, "MINI", "sess-end", nil)
	resp, _ := apiReq(t, "POST", ts.URL+"/agents/sessions/sess-end/end",
		map[string]any{"reason": "explode"}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestAgentEndAutoReleasesClaims(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "to-do")
	registerSession(t, ts.URL, "MINI", "sess-e", nil)
	// Claim it.
	apiReq(t, "POST", ts.URL+"/agents/sessions/sess-e/claims",
		map[string]any{"issue_key": iss.Key, "prompt": "do the thing"},
		map[string]string{"X-Actor": "agent-alice"})
	// Now end.
	apiReq(t, "POST", ts.URL+"/agents/sessions/sess-e/end",
		map[string]any{"reason": "stop"},
		map[string]string{"X-Actor": "agent-alice"})
	// Issue should be unassigned (claim auto-released, lockstep clear).
	updated, _ := s.GetIssueByID(iss.ID)
	if updated.Assignee != "" {
		t.Fatalf("expected unassigned after end, got %q", updated.Assignee)
	}
}

// ---------- claim / release ----------

func TestAgentClaimHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "first")
	registerSession(t, ts.URL, "MINI", "sess-c", nil)
	resp, body := apiReq(t, "POST", ts.URL+"/agents/sessions/sess-c/claims",
		map[string]any{"issue_key": iss.Key, "prompt": "go"},
		map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	updated, _ := s.GetIssueByID(iss.ID)
	if updated.Assignee != "agent-alice" {
		t.Fatalf("expected assignee=agent-alice, got %q", updated.Assignee)
	}
	claims, _ := s.ListClaimsForIssue(iss.ID)
	if len(claims) != 1 {
		t.Fatalf("claims: %d", len(claims))
	}
}

func TestAgentClaimRequiresIssueKey(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	registerSession(t, ts.URL, "MINI", "sess-c", nil)
	resp, _ := apiReq(t, "POST", ts.URL+"/agents/sessions/sess-c/claims",
		map[string]any{}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestAgentClaimIssueNotFound(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	registerSession(t, ts.URL, "MINI", "sess-c", nil)
	resp, _ := apiReq(t, "POST", ts.URL+"/agents/sessions/sess-c/claims",
		map[string]any{"issue_key": "MINI-999"}, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestAgentClaimDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "first")
	registerSession(t, ts.URL, "MINI", "sess-c", nil)
	resp, _ := apiReq(t, "POST",
		ts.URL+"/agents/sessions/sess-c/claims?dry_run=true",
		map[string]any{"issue_key": iss.Key},
		map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 201 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d header=%q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	updated, _ := s.GetIssueByID(iss.ID)
	if updated.Assignee != "" {
		t.Fatalf("dry-run claim persisted: %q", updated.Assignee)
	}
}

func TestAgentReleaseHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "first")
	registerSession(t, ts.URL, "MINI", "sess-r", nil)
	apiReq(t, "POST", ts.URL+"/agents/sessions/sess-r/claims",
		map[string]any{"issue_key": iss.Key},
		map[string]string{"X-Actor": "agent-alice"})
	resp, body := apiReq(t, "DELETE", ts.URL+"/agents/sessions/sess-r/claims",
		map[string]any{"issue_key": iss.Key},
		map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	updated, _ := s.GetIssueByID(iss.ID)
	if updated.Assignee != "" {
		t.Fatalf("release did not clear assignee: %q", updated.Assignee)
	}
}

func TestAgentReleaseNoClaim(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "first")
	registerSession(t, ts.URL, "MINI", "sess-r", nil)
	resp, _ := apiReq(t, "DELETE", ts.URL+"/agents/sessions/sess-r/claims",
		map[string]any{"issue_key": iss.Key}, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

// ---------- list / show ----------

func TestAgentSessionsListRepoScoped(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	registerSession(t, ts.URL, "MINI", "sess-l", nil)
	resp, body := apiGet(t, ts.URL+"/repos/MINI/agents/sessions")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"session_id": "sess-l"`) {
		t.Fatalf("missing session in body: %s", body)
	}
}

func TestAgentSessionsListEmpty(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, body := apiGet(t, ts.URL+"/repos/MINI/agents/sessions")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("expected [], got %q", body)
	}
}

func TestAgentSessionsListCrossRepo(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	seedRepo2(t, s)
	registerSession(t, ts.URL, "MINI", "sess-x", nil)
	registerSession(t, ts.URL, "OTHR", "sess-y", nil)
	resp, body := apiGet(t, ts.URL+"/agents/sessions")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	for _, want := range []string{"sess-x", "sess-y"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %q in cross-repo list: %s", want, body)
		}
	}
}

func TestAgentSessionsListHidesStubsByDefault(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	// Insert a stub (registered_at NULL) directly.
	if _, err := s.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: "stub-1",
		RepoID:    repo.ID,
		Actor:     "unregistered",
		Host:      "host",
		ClaudePID: 1234,
	}); err != nil {
		t.Fatalf("seed stub: %v", err)
	}
	// Plus a real registered session.
	registerSession(t, ts.URL, "MINI", "real-1", nil)

	// Default hides stubs.
	resp, body := apiGet(t, ts.URL+"/repos/MINI/agents/sessions")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if strings.Contains(string(body), "stub-1") {
		t.Fatalf("stub leaked into default list: %s", body)
	}
	if !strings.Contains(string(body), "real-1") {
		t.Fatalf("real session missing: %s", body)
	}
	// ?all=true surfaces it.
	resp2, body2 := apiGet(t, ts.URL+"/repos/MINI/agents/sessions?all=true")
	if resp2.StatusCode != 200 {
		t.Fatalf("status: %d", resp2.StatusCode)
	}
	if !strings.Contains(string(body2), "stub-1") {
		t.Fatalf("?all=true missed stub: %s", body2)
	}
}

func TestAgentSessionShow(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "first")
	registerSession(t, ts.URL, "MINI", "sess-sh", nil)
	apiReq(t, "POST", ts.URL+"/agents/sessions/sess-sh/claims",
		map[string]any{"issue_key": iss.Key, "prompt": "instruction"},
		map[string]string{"X-Actor": "agent-alice"})
	resp, body := apiGet(t, ts.URL+"/agents/sessions/sess-sh")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	for _, want := range []string{`"session"`, `"claims"`, `"issue_key": "MINI-1"`, `"prompt": "instruction"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %q in show body: %s", want, body)
		}
	}
}

func TestAgentSessionShowNotFound(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiGet(t, ts.URL+"/agents/sessions/ghost")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

// ---------- inbox / ack ----------

func TestAgentInboxAndAck(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "first")
	registerSession(t, ts.URL, "MINI", "sess-in", nil)
	// Create a dispatch directly via the store (the dispatch HTTP verb is
	// out of scope for BACI-34).
	d, err := s.AddDispatch(store.AddDispatchIn{
		RepoID:          repo.ID,
		TargetSessionID: "sess-in",
		IssueID:         &iss.ID,
		Payload:         "go work the thing",
		CreatedBy:       "supervisor",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/agents/sessions/sess-in/inbox")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"payload": "go work the thing"`) {
		t.Fatalf("missing payload in inbox: %s", body)
	}
	// Ack it.
	url := fmt.Sprintf("%s/agents/dispatches/%d/ack", ts.URL, d.ID)
	resp2, body2 := apiReq(t, "POST", url,
		map[string]any{"id": d.ID, "note": "done"},
		map[string]string{"X-Actor": "agent-alice"})
	if resp2.StatusCode != 200 {
		t.Fatalf("ack status: %d body: %s", resp2.StatusCode, body2)
	}
	got, _ := s.GetDispatch(d.ID)
	if got.Status != model.DispatchAcked || got.AckNote != "done" {
		t.Fatalf("ack did not persist: %+v", got)
	}
}

func TestAgentInboxSessionMissing(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiGet(t, ts.URL+"/agents/sessions/ghost/inbox")
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestAgentAckNotFound(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiReq(t, "POST", ts.URL+"/agents/dispatches/9999/ack", nil, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestAgentAckBadID(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiReq(t, "POST", ts.URL+"/agents/dispatches/abc/ack", nil, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

// ---------- open claims ----------

func TestAgentOpenClaimsRepoScoped(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "first")
	registerSession(t, ts.URL, "MINI", "sess-oc", nil)
	apiReq(t, "POST", ts.URL+"/agents/sessions/sess-oc/claims",
		map[string]any{"issue_key": iss.Key},
		map[string]string{"X-Actor": "agent-alice"})
	resp, body := apiGet(t, ts.URL+"/repos/MINI/agents/claims/open")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"issue_key": "MINI-1"`) {
		t.Fatalf("missing claim in open list: %s", body)
	}
}

func TestAgentOpenClaimsExcludesReleased(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "first")
	registerSession(t, ts.URL, "MINI", "sess-oc", nil)
	apiReq(t, "POST", ts.URL+"/agents/sessions/sess-oc/claims",
		map[string]any{"issue_key": iss.Key},
		map[string]string{"X-Actor": "agent-alice"})
	apiReq(t, "DELETE", ts.URL+"/agents/sessions/sess-oc/claims",
		map[string]any{"issue_key": iss.Key},
		map[string]string{"X-Actor": "agent-alice"})
	resp, body := apiGet(t, ts.URL+"/repos/MINI/agents/claims/open")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("expected [], got %q", body)
	}
}

func TestAgentOpenClaimsCrossRepo(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	repo2 := seedRepo2(t, s)
	iss := seedIssue(t, s, repo, "first")
	iss2 := seedIssue(t, s, repo2, "second")
	registerSession(t, ts.URL, "MINI", "sess-a", nil)
	registerSession(t, ts.URL, "OTHR", "sess-b", nil)
	apiReq(t, "POST", ts.URL+"/agents/sessions/sess-a/claims",
		map[string]any{"issue_key": iss.Key},
		map[string]string{"X-Actor": "agent-alice"})
	apiReq(t, "POST", ts.URL+"/agents/sessions/sess-b/claims",
		map[string]any{"issue_key": iss2.Key},
		map[string]string{"X-Actor": "agent-bob"})
	resp, body := apiGet(t, ts.URL+"/agents/claims/open")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	for _, want := range []string{`"issue_key": "MINI-1"`, `"issue_key": "OTHR-1"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %q in cross-repo open claims: %s", want, body)
		}
	}
}
