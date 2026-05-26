package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// seedParentDispatch is the test-side fixture for a follow-on parent:
// a delivered dispatch on the issue (any open status would do, but
// delivered is the most common shape — a worker has it in hand). The
// row is left bare on `target_session_id` (it's only relevant for
// matcher tests).
func seedParentDispatch(t *testing.T, s *store.Store, repo *model.Repo, iss *model.Issue, mode model.DispatchMode) *model.AgentDispatch {
	t.Helper()
	d, err := s.AddDispatch(store.AddDispatchIn{
		RepoID:        repo.ID,
		IssueID:       &iss.ID,
		Mode:          mode,
		Payload:       "parent",
		CreatedBy:     "supervisor",
		InitialStatus: model.DispatchQueued,
	})
	if err != nil {
		t.Fatalf("seed parent dispatch: %v", err)
	}
	return d
}

// TestREST_QueueFollowOnHappyPath drives the BACI-180 POST end-to-end:
// a queued dispatch on a todo issue accepts a follow-on; the new row
// is dormant (queued_after_dispatch_id set) and the audit log records
// agent.followon.queue.
func TestREST_QueueFollowOnHappyPath(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "follow-on me")
	parent := seedParentDispatch(t, s, repo, iss, model.DispatchModePlan)

	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+iss.Key+"/followon",
		map[string]any{"issue_key": iss.Key, "mode": string(model.DispatchModeImplement)})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
	var got model.AgentDispatch
	mustDecode(t, raw, &got)
	if got.ID == 0 {
		t.Fatalf("expected an id, got 0")
	}
	if got.Status != model.DispatchQueued {
		t.Errorf("Status = %q, want queued (dormant)", got.Status)
	}
	if got.QueuedAfterDispatchID == nil || *got.QueuedAfterDispatchID != parent.ID {
		t.Errorf("QueuedAfterDispatchID = %v, want %d", got.QueuedAfterDispatchID, parent.ID)
	}
	if string(got.Mode) != string(model.DispatchModeImplement) {
		t.Errorf("Mode = %q, want %q", got.Mode, model.DispatchModeImplement)
	}
	assertHistoryOps(t, s, []string{"agent.followon.queue"})
}

// TestREST_QueueFollowOnDryRun: dry-run projects the would-be row
// without writing — no DB rows beyond the parent, no audit entry.
func TestREST_QueueFollowOnDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "follow-on dryrun")
	parent := seedParentDispatch(t, s, repo, iss, model.DispatchModePlan)

	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+iss.Key+"/followon?dry_run=1",
		map[string]any{"issue_key": iss.Key, "mode": string(model.DispatchModeImplement)})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("X-Dry-Run"); got != "applied" {
		t.Fatalf("X-Dry-Run = %q, want applied", got)
	}
	var proj model.AgentDispatch
	mustDecode(t, raw, &proj)
	if proj.ID != 0 {
		t.Errorf("dry-run id = %d, want 0", proj.ID)
	}
	if proj.QueuedAfterDispatchID == nil || *proj.QueuedAfterDispatchID != parent.ID {
		t.Errorf("dry-run QueuedAfterDispatchID = %v, want %d", proj.QueuedAfterDispatchID, parent.ID)
	}

	ds, _ := s.ListDispatches(store.DispatchFilter{RepoID: &repo.ID})
	if len(ds) != 1 {
		t.Fatalf("dry-run persisted %d row(s), want 1 (the parent)", len(ds))
	}
	assertHistoryOps(t, s, nil)
}

// TestREST_QueueFollowOnInProgress_BACI195 (BACI-195 repro) —
// supersedes the original TestREST_QueueFollowOnState400. Queueing
// an implement follow-on while the parent dispatch is in flight and
// the issue is `in_progress` must succeed. This is the exact failure
// the brief reproduced today on BACI-193: POST /...//followon with
// {mode:implement} returned 400 "the implement prompt can't run from
// a in_progress issue". After dropping the queue-time gate the call
// must 201 with a dormant row attached to the parent.
//
// The fire-time gate replaces the queue-time gate: covered by the
// store-level tests in TestPromoteReadyFollowOns_GateFailCancels +
// the controller-level audit test
// TestFollowOnSweepIfLeader_GateFailWrites.
func TestREST_QueueFollowOnInProgress_BACI195(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "research running, queue implement next")
	// in_progress is the state research-mode parents leave the ticket
	// in while running; implement's default gate is `todo`, so the
	// pre-BACI-195 queue-time check rejected the call here.
	if err := s.SetIssueState(iss.ID, model.StateInProgress); err != nil {
		t.Fatalf("set in_progress: %v", err)
	}
	parent := seedParentDispatch(t, s, repo, iss, model.DispatchModePlan)

	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+iss.Key+"/followon",
		map[string]any{"issue_key": iss.Key, "mode": string(model.DispatchModeImplement)})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, body %s — BACI-195 expects this to succeed", resp.StatusCode, raw)
	}
	var got model.AgentDispatch
	mustDecode(t, raw, &got)
	if got.QueuedAfterDispatchID == nil || *got.QueuedAfterDispatchID != parent.ID {
		t.Fatalf("QueuedAfterDispatchID = %v, want %d (dormant on parent)", got.QueuedAfterDispatchID, parent.ID)
	}
	if got.Status != model.DispatchQueued {
		t.Fatalf("Status = %q, want queued (dormant)", got.Status)
	}
	assertHistoryOps(t, s, []string{"agent.followon.queue"})
}

// TestREST_QueueFollowOnNoParent: a follow-on on an issue with no
// in-flight dispatch returns 400 ("no active dispatch to follow on
// from") — the user-facing equivalent of "queue this when there's
// nothing to queue against".
func TestREST_QueueFollowOnNoParent(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "lonely issue")

	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+iss.Key+"/followon",
		map[string]any{"issue_key": iss.Key, "mode": string(model.DispatchModeImplement)})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "no active dispatch") {
		t.Errorf("body missing 'no active dispatch' hint: %s", raw)
	}
}

// TestREST_QueueFollowOnSingleSlot: a second queue-followon against
// the same issue while a dormant follow-on already exists is rejected
// (400 — the store-boundary single-slot guard).
func TestREST_QueueFollowOnSingleSlot(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "single-slot")
	_ = seedParentDispatch(t, s, repo, iss, model.DispatchModePlan)

	// First call — succeeds.
	resp1, raw1 := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+iss.Key+"/followon",
		map[string]any{"issue_key": iss.Key, "mode": string(model.DispatchModeImplement)})
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first call status %d, body %s", resp1.StatusCode, raw1)
	}
	// Second call — rejected with 400.
	resp2, raw2 := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+iss.Key+"/followon",
		map[string]any{"issue_key": iss.Key, "mode": string(model.DispatchModeImplement)})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("second call status %d, body %s", resp2.StatusCode, raw2)
	}
	if !strings.Contains(string(raw2), "already has a follow-on") {
		t.Errorf("body missing 'already has a follow-on' hint: %s", raw2)
	}
}

// TestREST_QueueFollowOnMissingMode: empty body returns 400 invalid_input
// (mirrors the dispatch verb's missing-mode behaviour).
func TestREST_QueueFollowOnMissingMode(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "missing mode")
	_ = seedParentDispatch(t, s, repo, iss, model.DispatchModePlan)

	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+iss.Key+"/followon",
		map[string]any{"issue_key": iss.Key})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
}

// TestREST_CancelFollowOnHappyPath: DELETE returns 200 + the cancelled
// row, and the audit log records agent.followon.cancel.
func TestREST_CancelFollowOnHappyPath(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "cancel me")
	_ = seedParentDispatch(t, s, repo, iss, model.DispatchModePlan)

	// Queue the follow-on so we have something to cancel.
	apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+iss.Key+"/followon",
		map[string]any{"issue_key": iss.Key, "mode": string(model.DispatchModeImplement)})

	resp, raw := apiDelete(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+iss.Key+"/followon", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
	var got model.AgentDispatch
	mustDecode(t, raw, &got)
	if got.Status != model.DispatchCancelled {
		t.Errorf("Status = %q, want cancelled", got.Status)
	}
	assertHistoryOps(t, s, []string{"agent.followon.queue", "agent.followon.cancel"})
}

// TestREST_CancelFollowOnIdempotent: DELETE against an issue with no
// dormant follow-on returns 200 with a JSON-null body — idempotent
// no-op, no audit row.
func TestREST_CancelFollowOnIdempotent(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "nothing to cancel")

	resp, raw := apiDelete(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+iss.Key+"/followon", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
	body := strings.TrimSpace(string(raw))
	if body != "null" {
		t.Errorf("body = %q, want \"null\" (idempotent no-op)", body)
	}
	assertHistoryOps(t, s, nil)
}

// TestREST_DispatchChainHappyPath (BACI-209) — POST .../dispatch-chain
// on a fresh todo card queues a primary plan dispatch AND a dormant
// implement follow-on in one transaction. Both rows persist, the
// follow-on links to the parent, and the audit log records both ops.
func TestREST_DispatchChainHappyPath(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "chain me")

	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+iss.Key+"/dispatch-chain",
		map[string]any{
			"issue_key":      iss.Key,
			"mode":           string(model.DispatchModePlan),
			"follow_on_mode": string(model.DispatchModeImplement),
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
	var parent model.AgentDispatch
	mustDecode(t, raw, &parent)
	if parent.ID == 0 {
		t.Fatalf("parent id = 0")
	}
	if parent.Status != model.DispatchQueued {
		t.Errorf("parent status = %q, want queued", parent.Status)
	}
	if string(parent.Mode) != string(model.DispatchModePlan) {
		t.Errorf("parent mode = %q, want %q", parent.Mode, model.DispatchModePlan)
	}
	if parent.QueuedAfterDispatchID != nil {
		t.Errorf("parent has queued_after_dispatch_id = %v, want nil", parent.QueuedAfterDispatchID)
	}
	// Both rows visible on the store; follow-on links to the parent.
	ds, _ := s.ListDispatches(store.DispatchFilter{RepoID: &repo.ID})
	if len(ds) != 2 {
		t.Fatalf("ListDispatches = %d, want 2 (parent + follow-on)", len(ds))
	}
	var follow *model.AgentDispatch
	for _, d := range ds {
		if d.QueuedAfterDispatchID != nil {
			follow = d
		}
	}
	if follow == nil {
		t.Fatalf("no follow-on row landed")
	}
	if *follow.QueuedAfterDispatchID != parent.ID {
		t.Errorf("follow.QueuedAfterDispatchID = %d, want %d", *follow.QueuedAfterDispatchID, parent.ID)
	}
	if string(follow.Mode) != string(model.DispatchModeImplement) {
		t.Errorf("follow mode = %q, want %q", follow.Mode, model.DispatchModeImplement)
	}
	assertHistoryOps(t, s, []string{"agent.queue", "agent.followon.queue"})
}

// TestREST_DispatchChainStateGate (BACI-209) — a primary mode whose
// state-gate doesn't admit the issue's current state returns 400 and
// writes nothing.
func TestREST_DispatchChainStateGate(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "in-review card")
	if err := s.SetIssueState(iss.ID, model.StateInReview); err != nil {
		t.Fatalf("set in_review: %v", err)
	}

	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+iss.Key+"/dispatch-chain",
		map[string]any{
			"issue_key":      iss.Key,
			"mode":           string(model.DispatchModePlan),
			"follow_on_mode": string(model.DispatchModeImplement),
		})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "can't run from") {
		t.Errorf("body missing gate-miss hint: %s", raw)
	}
	ds, _ := s.ListDispatches(store.DispatchFilter{RepoID: &repo.ID})
	if len(ds) != 0 {
		t.Fatalf("gate-miss landed %d row(s), want 0", len(ds))
	}
}

// TestREST_DispatchChainMissingFollowOnMode (BACI-209) — empty
// follow_on_mode returns 400 invalid_input (mirrors the regular
// dispatch verb's missing-mode behaviour).
func TestREST_DispatchChainMissingFollowOnMode(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "missing follow-on")

	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+iss.Key+"/dispatch-chain",
		map[string]any{
			"issue_key": iss.Key,
			"mode":      string(model.DispatchModePlan),
		})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
}

// TestREST_DispatchChainDryRun (BACI-209) — dry-run projects the
// parent without writing.
func TestREST_DispatchChainDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "chain dryrun")

	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+iss.Key+"/dispatch-chain?dry_run=1",
		map[string]any{
			"issue_key":      iss.Key,
			"mode":           string(model.DispatchModePlan),
			"follow_on_mode": string(model.DispatchModeImplement),
		})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("X-Dry-Run"); got != "applied" {
		t.Fatalf("X-Dry-Run = %q, want applied", got)
	}
	var proj model.AgentDispatch
	mustDecode(t, raw, &proj)
	if proj.ID != 0 {
		t.Errorf("dry-run id = %d, want 0", proj.ID)
	}
	ds, _ := s.ListDispatches(store.DispatchFilter{RepoID: &repo.ID})
	if len(ds) != 0 {
		t.Fatalf("dry-run persisted %d row(s), want 0", len(ds))
	}
	assertHistoryOps(t, s, nil)
}

// TestREST_QueueFollowOnBlockedIdleCard (BACI-217) — a blocked-and-idle
// card (no in-flight parent, ≥1 open `blocks` edge pointing at it)
// accepts a follow-on as the blockers-clear variant. The 201 response
// carries the new flag, no parent-acks link, and the audit log records
// agent.followon.queue with gate=blockers in Details.
func TestREST_QueueFollowOnBlockedIdleCard(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	blocked := seedIssue(t, s, repo, "blocked-and-idle")
	blocker := seedIssue(t, s, repo, "the blocker")
	if err := s.CreateRelation(blocker.ID, blocked.ID, model.RelBlocks); err != nil {
		t.Fatalf("create blocks edge: %v", err)
	}

	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+blocked.Key+"/followon",
		map[string]any{"issue_key": blocked.Key, "mode": string(model.DispatchModeImplement)})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
	var got model.AgentDispatch
	mustDecode(t, raw, &got)
	if !got.QueuedUntilBlockersClear {
		t.Error("expected queued_until_blockers_clear = true on the blockers-clear variant")
	}
	if got.QueuedAfterDispatchID != nil {
		t.Errorf("blockers-clear variant must not carry queued_after_dispatch_id, got %v", got.QueuedAfterDispatchID)
	}
	if got.Status != model.DispatchQueued {
		t.Errorf("Status = %q, want queued (dormant)", got.Status)
	}
	// Audit row stamps gate=blockers so the variant is distinguishable
	// in `bacio history --op agent.followon.queue`.
	rows, err := s.ListHistory(store.HistoryFilter{Limit: 10, Op: "agent.followon.queue"})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("agent.followon.queue rows: got %d, want 1", len(rows))
	}
	if !strings.Contains(rows[0].Details, "gate=blockers") {
		t.Errorf("audit Details = %q, want gate=blockers", rows[0].Details)
	}
}

// TestREST_QueueFollowOnIdleUnblockedCard (BACI-217) — an issue with
// neither an in-flight parent nor any open blockers still returns 400
// with the "no active dispatch" hint (today's UX preserved).
func TestREST_QueueFollowOnIdleUnblockedCard(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	iss := seedIssue(t, s, repo, "idle and unblocked")

	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+iss.Key+"/followon",
		map[string]any{"issue_key": iss.Key, "mode": string(model.DispatchModeImplement)})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "no active dispatch") {
		t.Errorf("body missing 'no active dispatch' hint: %s", raw)
	}
}

// TestREST_CancelFollowOnIssueInOtherRepo: DELETE against a key from a
// different repo than the URL prefix returns 404 (no cross-repo leak).
func TestREST_CancelFollowOnIssueInOtherRepo(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	other := seedRepo2(t, s)
	otherIss := seedIssue(t, s, other, "lives elsewhere")

	resp, raw := apiDelete(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+otherIss.Key+"/followon", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, body %s", resp.StatusCode, raw)
	}
}
