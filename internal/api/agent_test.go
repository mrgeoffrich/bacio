package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// ---------- helpers ----------
//
// Since BACI-100 the API register route rejects a non-UUID session_id,
// so these tests address sessions by uuidFor("friendly-name") — a
// deterministic, structurally valid UUID — instead of bare "sess-X"
// strings. uuidFor lives in helpers_test.go.

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
	sid := uuidFor("sess-1")
	status, body := registerSession(t, ts.URL, "MINI", sid, nil)
	if status != 201 {
		t.Fatalf("status: %d body: %s", status, body)
	}
	var sess model.AgentSession
	if err := json.Unmarshal(body, &sess); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sess.SessionID != sid || sess.Actor != "agent-alice" {
		t.Fatalf("unexpected session: %+v", sess)
	}
	assertHistoryOps(t, s, []string{"agent.register"})
}

func TestAgentRegisterDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	sid := uuidFor("sess-dr")
	resp, body := apiReq(t, "POST",
		ts.URL+"/repos/MINI/agents/sessions?dry_run=true",
		map[string]any{"session_id": sid, "actor": "agent-dr"},
		map[string]string{"X-Actor": "agent-dr"})
	if resp.StatusCode != 201 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d header=%q body=%s", resp.StatusCode, resp.Header.Get("X-Dry-Run"), body)
	}
	// No session row written.
	if _, err := s.GetAgentSession(sid); err == nil {
		t.Fatalf("dry-run wrote session row")
	}
	assertHistoryOps(t, s, nil)
}

// TestAgentRegisterRejectsMalformedUUID locks in BACI-100: the API
// register route rejects a session_id that is not a structurally valid
// UUID with a 400 invalid_input naming the session_id field, and the
// dry-run path rejects it too.
func TestAgentRegisterRejectsMalformedUUID(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	const bad = "23543e26-6339-4b8-aff2-d8ea2013f287" // 3-hex third group

	for _, suffix := range []string{"", "?dry_run=true"} {
		resp, body := apiReq(t, "POST", ts.URL+"/repos/MINI/agents/sessions"+suffix,
			map[string]any{"session_id": bad, "actor": "agent-x"},
			map[string]string{"X-Actor": "agent-x"})
		if resp.StatusCode != 400 {
			t.Fatalf("suffix %q: status %d body %s", suffix, resp.StatusCode, body)
		}
		env := decode[map[string]any](t, strings.NewReader(string(body)))
		if env["code"] != "invalid_input" {
			t.Fatalf("suffix %q: code = %v", suffix, env["code"])
		}
		if details, _ := env["details"].(map[string]any); details["field"] != "session_id" {
			t.Fatalf("suffix %q: details.field = %v", suffix, details["field"])
		}
	}
	if _, err := s.GetAgentSession(bad); err == nil {
		t.Fatal("malformed-UUID session row was written")
	}
}

// TestAgentRegisterDedupesOnClaudePID locks in BACI-100: a second
// register for the same (host, claude_pid) under a different session_id
// reconciles the earlier live row away so one OS process holds exactly
// one live registry row.
func TestAgentRegisterDedupesOnClaudePID(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	first := uuidFor("dedupe-first")
	later := uuidFor("dedupe-later")

	for _, sid := range []string{first, later} {
		status, body := registerSession(t, ts.URL, "MINI", sid, map[string]any{
			"session_id": sid, "actor": "agent-alice",
			"host": "smoke-host", "claude_pid": 4242,
		})
		if status != 201 {
			t.Fatalf("register %s: status %d body %s", sid, status, body)
		}
	}

	live, err := s.SessionsByClaudePID("smoke-host", 4242)
	if err != nil {
		t.Fatalf("SessionsByClaudePID: %v", err)
	}
	if len(live) != 1 || live[0].SessionID != later {
		t.Fatalf("got %d live sessions, want 1 (=%s): %+v", len(live), later, live)
	}
	prior, err := s.GetAgentSession(first)
	if err != nil {
		t.Fatalf("GetAgentSession(first): %v", err)
	}
	if prior.EndedAt == nil || prior.EndReason != string(model.EndReasonSuperseded) {
		t.Fatalf("first session not superseded: %+v", prior)
	}
}

func TestAgentRegisterUnknownField(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, body := apiReq(t, "POST", ts.URL+"/repos/MINI/agents/sessions",
		map[string]any{"session_id": uuidFor("x"), "actor": "a", "bogus": true},
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
		map[string]any{"session_id": uuidFor("x"), "actor": "a"},
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
	sid := uuidFor("sess-h")
	resp, body := apiReq(t, "POST", ts.URL+"/repos/MINI/agents/sessions",
		map[string]any{"session_id": sid},
		map[string]string{"X-Actor": "agent-from-header"})
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	sess, err := s.GetAgentSession(sid)
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
		status, body := registerSession(t, ts.URL, "MINI", uuidFor("sess-id"), nil)
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
	status, _ := registerSession(t, ts.URL, "MINI", uuidFor("sess-a"), map[string]any{
		"session_id": uuidFor("sess-a"), "actor": "agent-a", "agent": "lively-frog@claude.x",
	})
	if status != 201 {
		t.Fatalf("first register status: %d", status)
	}
	// Second tries to claim the same name with --new (NewIdentity=true) → 409.
	resp, body := apiReq(t, "POST", ts.URL+"/repos/MINI/agents/sessions",
		map[string]any{
			"session_id":   uuidFor("sess-b"),
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
	sid := uuidFor("sess-hb")
	registerSession(t, ts.URL, "MINI", sid, nil)
	resp, body := apiReq(t, "POST", ts.URL+"/agents/sessions/"+sid+"/heartbeat",
		map[string]any{"session_id": sid, "model": "claude-sonnet-4-6"}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	sess, _ := s.GetAgentSession(sid)
	if sess.Model != "claude-sonnet-4-6" {
		t.Fatalf("model not updated: %q", sess.Model)
	}
}

func TestAgentHeartbeatSessionMismatch(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	sid := uuidFor("sess-hb")
	registerSession(t, ts.URL, "MINI", sid, nil)
	resp, _ := apiReq(t, "POST", ts.URL+"/agents/sessions/"+sid+"/heartbeat",
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
	sid := uuidFor("sess-end")
	registerSession(t, ts.URL, "MINI", sid, nil)
	resp, body := apiReq(t, "POST", ts.URL+"/agents/sessions/"+sid+"/end",
		map[string]any{"reason": "stop"},
		map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	sess, _ := s.GetAgentSession(sid)
	if sess.EndedAt == nil || sess.EndReason != "stop" {
		t.Fatalf("session not ended: %+v", sess)
	}
}

func TestAgentEndBadReason(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	sid := uuidFor("sess-end")
	registerSession(t, ts.URL, "MINI", sid, nil)
	resp, _ := apiReq(t, "POST", ts.URL+"/agents/sessions/"+sid+"/end",
		map[string]any{"reason": "explode"}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestAgentEndAutoReleasesClaims(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "to-do")
	sid := uuidFor("sess-e")
	registerSession(t, ts.URL, "MINI", sid, nil)
	// Claim it.
	apiReq(t, "POST", ts.URL+"/agents/sessions/"+sid+"/claims",
		map[string]any{"issue_key": iss.Key, "prompt": "do the thing"},
		map[string]string{"X-Actor": "agent-alice"})
	// Now end.
	apiReq(t, "POST", ts.URL+"/agents/sessions/"+sid+"/end",
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
	sid := uuidFor("sess-c")
	registerSession(t, ts.URL, "MINI", sid, nil)
	resp, body := apiReq(t, "POST", ts.URL+"/agents/sessions/"+sid+"/claims",
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
	sid := uuidFor("sess-c")
	registerSession(t, ts.URL, "MINI", sid, nil)
	resp, _ := apiReq(t, "POST", ts.URL+"/agents/sessions/"+sid+"/claims",
		map[string]any{}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestAgentClaimIssueNotFound(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	sid := uuidFor("sess-c")
	registerSession(t, ts.URL, "MINI", sid, nil)
	resp, _ := apiReq(t, "POST", ts.URL+"/agents/sessions/"+sid+"/claims",
		map[string]any{"issue_key": "MINI-999"}, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestAgentClaimDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "first")
	sid := uuidFor("sess-c")
	registerSession(t, ts.URL, "MINI", sid, nil)
	resp, _ := apiReq(t, "POST",
		ts.URL+"/agents/sessions/"+sid+"/claims?dry_run=true",
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
	sid := uuidFor("sess-r")
	registerSession(t, ts.URL, "MINI", sid, nil)
	apiReq(t, "POST", ts.URL+"/agents/sessions/"+sid+"/claims",
		map[string]any{"issue_key": iss.Key},
		map[string]string{"X-Actor": "agent-alice"})
	resp, body := apiReq(t, "DELETE", ts.URL+"/agents/sessions/"+sid+"/claims",
		map[string]any{"issue_key": iss.Key, "final_state": "in_review"},
		map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	updated, _ := s.GetIssueByID(iss.ID)
	if updated.Assignee != "" {
		t.Fatalf("release did not clear assignee: %q", updated.Assignee)
	}
	// BACI-126c: the release also moves the issue to the supplied final state.
	if updated.State != model.StateInReview {
		t.Fatalf("release did not move state: %q, want in_review", updated.State)
	}
}

func TestAgentReleaseNoClaim(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "first")
	sid := uuidFor("sess-r")
	registerSession(t, ts.URL, "MINI", sid, nil)
	resp, _ := apiReq(t, "DELETE", ts.URL+"/agents/sessions/"+sid+"/claims",
		map[string]any{"issue_key": iss.Key, "final_state": "in_review"}, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

// TestAgentReleaseWithoutFinalState locks in the Pipeline cutover:
// final_state is now OPTIONAL. A release without it succeeds (claim
// dropped) and leaves the issue's state untouched — the controller
// engine owns pipeline-card state, so a pipeline-stage worker releases
// the claim only. (The pre-pipeline triage passes still pass --state.)
func TestAgentReleaseWithoutFinalState(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "first")
	sid := uuidFor("sess-r")
	registerSession(t, ts.URL, "MINI", sid, nil)
	apiReq(t, "POST", ts.URL+"/agents/sessions/"+sid+"/claims",
		map[string]any{"issue_key": iss.Key},
		map[string]string{"X-Actor": "agent-alice"})
	resp, body := apiReq(t, "DELETE", ts.URL+"/agents/sessions/"+sid+"/claims",
		map[string]any{"issue_key": iss.Key},
		map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 releasing without final_state, got %d body: %s", resp.StatusCode, body)
	}
}

// ---------- list / show ----------

func TestAgentSessionsListRepoScoped(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	sid := uuidFor("sess-l")
	registerSession(t, ts.URL, "MINI", sid, nil)
	resp, body := apiGet(t, ts.URL+"/repos/MINI/agents/sessions")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"session_id": "`+sid+`"`) {
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
	sidX := uuidFor("sess-x")
	sidY := uuidFor("sess-y")
	registerSession(t, ts.URL, "MINI", sidX, nil)
	registerSession(t, ts.URL, "OTHR", sidY, nil)
	resp, body := apiGet(t, ts.URL+"/agents/sessions")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	for _, want := range []string{sidX, sidY} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %q in cross-repo list: %s", want, body)
		}
	}
}

func TestAgentSessionsListHidesStubsByDefault(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	// Insert a stub (registered_at NULL) directly. The store-level upsert
	// keeps accepting non-UUID ids — only the register entry points
	// require a UUID (BACI-100) — so "stub-1" is a fine direct seed.
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
	realSID := uuidFor("real-1")
	registerSession(t, ts.URL, "MINI", realSID, nil)

	// Default hides stubs.
	resp, body := apiGet(t, ts.URL+"/repos/MINI/agents/sessions")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if strings.Contains(string(body), "stub-1") {
		t.Fatalf("stub leaked into default list: %s", body)
	}
	if !strings.Contains(string(body), realSID) {
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
	sid := uuidFor("sess-sh")
	registerSession(t, ts.URL, "MINI", sid, nil)
	apiReq(t, "POST", ts.URL+"/agents/sessions/"+sid+"/claims",
		map[string]any{"issue_key": iss.Key, "prompt": "instruction"},
		map[string]string{"X-Actor": "agent-alice"})
	resp, body := apiGet(t, ts.URL+"/agents/sessions/"+sid)
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
	sid := uuidFor("sess-in")
	registerSession(t, ts.URL, "MINI", sid, nil)
	// Create a dispatch directly via the store (the dispatch HTTP verb is
	// out of scope for BACI-34).
	d, err := s.AddDispatch(store.AddDispatchIn{
		RepoID:          repo.ID,
		TargetSessionID: sid,
		IssueID:         &iss.ID,
		Payload:         "go work the thing",
		CreatedBy:       "supervisor",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/agents/sessions/"+sid+"/inbox")
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

// TestAgentCancelDeliveredRejected (BACI-130) locks in the HTTP
// boundary's mirror of the store-level guard: cancelling a delivered
// dispatch returns 409 "conflict" with a clear message, and the row
// itself stays delivered (the reject happens before any write). The
// kanban spinner-cancel button hides the click affordance for
// delivered cards, but a stale view can still hit the endpoint —
// matching the existing already-acked → 409 pattern keeps the surface
// symmetric.
func TestAgentCancelDeliveredRejected(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "delivered-dispatch")
	sid := uuidFor("sess-deliver")
	registerSession(t, ts.URL, "MINI", sid, nil)
	d, err := s.AddDispatch(store.AddDispatchIn{
		RepoID:          repo.ID,
		TargetSessionID: sid,
		IssueID:         &iss.ID,
		Payload:         "deliver me",
		CreatedBy:       "supervisor",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	if _, err := s.MarkDispatchDelivered(d.ID); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	url := fmt.Sprintf("%s/agents/dispatches/%d/cancel", ts.URL, d.ID)
	resp, body := apiReq(t, "POST", url, map[string]any{"id": d.ID}, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("cancel status = %d, want 409 (body %s)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"code":"conflict"`) {
		t.Errorf("missing conflict code in body: %s", body)
	}
	if !strings.Contains(string(body), "has been delivered") {
		t.Errorf("missing 'has been delivered' fragment in body: %s", body)
	}
	got, err := s.GetDispatch(d.ID)
	if err != nil {
		t.Fatalf("re-read dispatch: %v", err)
	}
	if got.Status != model.DispatchDelivered {
		t.Errorf("status after rejected cancel = %q, want delivered", got.Status)
	}
}

// ---------- open claims ----------

func TestAgentOpenClaimsRepoScoped(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "first")
	sid := uuidFor("sess-oc")
	registerSession(t, ts.URL, "MINI", sid, nil)
	apiReq(t, "POST", ts.URL+"/agents/sessions/"+sid+"/claims",
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
	sid := uuidFor("sess-oc")
	registerSession(t, ts.URL, "MINI", sid, nil)
	apiReq(t, "POST", ts.URL+"/agents/sessions/"+sid+"/claims",
		map[string]any{"issue_key": iss.Key},
		map[string]string{"X-Actor": "agent-alice"})
	apiReq(t, "DELETE", ts.URL+"/agents/sessions/"+sid+"/claims",
		map[string]any{"issue_key": iss.Key, "final_state": "in_review"},
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
	sidA := uuidFor("sess-a")
	sidB := uuidFor("sess-b")
	registerSession(t, ts.URL, "MINI", sidA, nil)
	registerSession(t, ts.URL, "OTHR", sidB, nil)
	apiReq(t, "POST", ts.URL+"/agents/sessions/"+sidA+"/claims",
		map[string]any{"issue_key": iss.Key},
		map[string]string{"X-Actor": "agent-alice"})
	apiReq(t, "POST", ts.URL+"/agents/sessions/"+sidB+"/claims",
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
