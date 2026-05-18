package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// seedDispatchFixture returns a store with one repo, one issue, one
// agent identity, and one registered session linked to that agent —
// the scaffold every dispatch test needs.
func seedDispatchFixture(t *testing.T) (*Store, *model.Repo, *model.Issue, *model.Agent, *model.AgentSession) {
	t.Helper()
	s, repo, iss := seedRepoAndIssue(t)
	ag, _, err := s.UpsertAgent("swift-otter@claude.test", true)
	if err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	sess, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-dispatch-1", RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
	})
	if err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	return s, repo, iss, ag, sess
}

// TestAddDispatchRequiresTarget locks in that a dispatch with neither an
// agent identity nor a session target is rejected at the store boundary.
func TestAddDispatchRequiresTarget(t *testing.T) {
	s, repo, _, _, _ := seedDispatchFixture(t)
	if _, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, CreatedBy: "supervisor", Payload: "do a thing",
	}); err == nil {
		t.Fatal("expected error for targetless dispatch, got nil")
	}
}

// TestAddDispatchRoundTrip checks that a dispatch round-trips with its
// joined repo prefix, agent name, and issue key populated.
func TestAddDispatchRoundTrip(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		IssueID:       &iss.ID,
		Payload:       "please pick this up",
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	if d.Status != model.DispatchPending {
		t.Fatalf("status = %q, want pending", d.Status)
	}
	if d.RepoPrefix != repo.Prefix {
		t.Fatalf("repo prefix = %q, want %q", d.RepoPrefix, repo.Prefix)
	}
	if d.TargetAgentName != ag.Name {
		t.Fatalf("agent name = %q, want %q", d.TargetAgentName, ag.Name)
	}
	if d.IssueKey != iss.Key {
		t.Fatalf("issue key = %q, want %q", d.IssueKey, iss.Key)
	}
}

// TestAddDispatchMode locks in that a structured mode round-trips and an
// unknown mode is rejected at the store boundary.
func TestAddDispatchMode(t *testing.T) {
	s, repo, _, ag, _ := seedDispatchFixture(t)
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		Mode:          model.DispatchModePlan,
		Payload:       "plan it",
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	if d.Mode != model.DispatchModePlan {
		t.Fatalf("mode = %q, want plan", d.Mode)
	}
	got, err := s.GetDispatch(d.ID)
	if err != nil {
		t.Fatalf("get dispatch: %v", err)
	}
	if got.Mode != model.DispatchModePlan {
		t.Fatalf("mode after reload = %q, want plan", got.Mode)
	}
	// After BACI-31 the mode is a slug rather than a closed enum; any
	// slug-shaped string is accepted (and may outlive the template
	// it references). Shape violations are still rejected — uppercase
	// breaks the kebab-/snake-case rule.
	if _, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		Mode:          model.DispatchMode("Refactor"),
		CreatedBy:     "supervisor",
	}); err == nil {
		t.Fatal("expected error for a malformed dispatch mode (uppercase), got nil")
	}
	// A slug-shaped but unregistered mode is accepted — the model
	// validator only checks shape, and the store records the value
	// verbatim. (Renderers treat an unknown slug as "removed".)
	if _, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		Mode:          model.DispatchMode("never-registered"),
		CreatedBy:     "supervisor",
	}); err != nil {
		t.Fatalf("unregistered-but-slug-shaped mode should be accepted: %v", err)
	}
}

// TestListDispatchesEitherTarget locks in the drain-query semantics:
// when both an agent id and a session id are supplied, dispatches aimed
// at EITHER come back. A dispatch to the agent identity and a separate
// dispatch to the bare session must both appear.
func TestListDispatchesEitherTarget(t *testing.T) {
	s, repo, _, ag, sess := seedDispatchFixture(t)
	toAgent, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, CreatedBy: "supervisor", Payload: "agent-scoped",
	})
	if err != nil {
		t.Fatalf("add agent dispatch: %v", err)
	}
	toSession, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetSessionID: sess.SessionID, CreatedBy: "supervisor", Payload: "session-scoped",
	})
	if err != nil {
		t.Fatalf("add session dispatch: %v", err)
	}

	got, err := s.ListDispatches(DispatchFilter{
		TargetAgentID:   &ag.ID,
		TargetSessionID: sess.SessionID,
		Statuses:        []model.DispatchStatus{model.DispatchPending},
	})
	if err != nil {
		t.Fatalf("list dispatches: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d dispatches, want 2", len(got))
	}
	seen := map[int64]bool{}
	for _, d := range got {
		seen[d.ID] = true
	}
	if !seen[toAgent.ID] || !seen[toSession.ID] {
		t.Fatalf("either-target query missed a row: agent=%v session=%v", seen[toAgent.ID], seen[toSession.ID])
	}
}

// TestDispatchLifecycle walks pending -> delivered -> acked and checks
// the timestamps and note land.
func TestDispatchLifecycle(t *testing.T) {
	s, repo, _, ag, _ := seedDispatchFixture(t)
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, CreatedBy: "supervisor", Payload: "work",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}

	delivered, err := s.MarkDispatchDelivered(d.ID)
	if err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	if delivered.Status != model.DispatchDelivered || delivered.DeliveredAt == nil {
		t.Fatalf("after deliver: status=%q delivered_at=%v", delivered.Status, delivered.DeliveredAt)
	}

	acked, err := s.AckDispatch(d.ID, "done, opened PR")
	if err != nil {
		t.Fatalf("ack: %v", err)
	}
	if acked.Status != model.DispatchAcked || acked.AckedAt == nil {
		t.Fatalf("after ack: status=%q acked_at=%v", acked.Status, acked.AckedAt)
	}
	if acked.AckNote != "done, opened PR" {
		t.Fatalf("ack note = %q, want %q", acked.AckNote, "done, opened PR")
	}
}

// TestMarkDeliveredIdempotent locks in that re-delivering an
// already-delivered dispatch is a no-op rather than an error or a
// timestamp clobber.
func TestMarkDeliveredIdempotent(t *testing.T) {
	s, repo, _, ag, _ := seedDispatchFixture(t)
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	first, err := s.MarkDispatchDelivered(d.ID)
	if err != nil {
		t.Fatalf("first deliver: %v", err)
	}
	second, err := s.MarkDispatchDelivered(d.ID)
	if err != nil {
		t.Fatalf("second deliver: %v", err)
	}
	if !second.DeliveredAt.Equal(*first.DeliveredAt) {
		t.Fatalf("delivered_at moved on re-deliver: %v -> %v", first.DeliveredAt, second.DeliveredAt)
	}
}

// TestAckBumpsSessionLastSeen (BACI-57) locks in that acking a
// session-targeted dispatch advances the target session's
// last_seen_at. The idle-pinger relies on this: when it queues a
// ping and the agent acks it, the ack itself counts as a heartbeat
// so the next reaper tick doesn't immediately re-queue another ping.
func TestAckBumpsSessionLastSeen(t *testing.T) {
	s, repo, _, _, sess := seedDispatchFixture(t)

	// Force the session row to look stale, well past the
	// AgentIdlePingThreshold of 1h, so we can prove the ack
	// brings the timestamp forward rather than the test reading
	// the wall clock value seeded at upsert time.
	if _, err := s.DB.Exec(
		`UPDATE agent_sessions SET last_seen_at = datetime('now','-2 hours') WHERE session_id = ?`,
		sess.SessionID,
	); err != nil {
		t.Fatalf("force-stale last_seen_at: %v", err)
	}
	before, err := s.GetAgentSession(sess.SessionID)
	if err != nil {
		t.Fatalf("get session before ack: %v", err)
	}

	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:          repo.ID,
		TargetSessionID: sess.SessionID,
		Payload:         "bacio idle-check",
		CreatedBy:       model.IdlePingDispatchCreator,
	})
	if err != nil {
		t.Fatalf("add ping dispatch: %v", err)
	}
	if _, err := s.AckDispatch(d.ID, "still here"); err != nil {
		t.Fatalf("ack ping dispatch: %v", err)
	}

	after, err := s.GetAgentSession(sess.SessionID)
	if err != nil {
		t.Fatalf("get session after ack: %v", err)
	}
	if !after.LastSeenAt.After(before.LastSeenAt) {
		t.Fatalf("last_seen_at did not advance on session-targeted ack: before=%v after=%v",
			before.LastSeenAt, after.LastSeenAt)
	}
}

// TestAckAgentScopedDoesNotBumpSession (BACI-57) is the symmetric
// no-op guard: an agent-identity-targeted ack (no target_session_id)
// must NOT bump any session row's last_seen_at. The pinger's
// liveness signal is strictly "this session acked", not "some
// session for this identity acked".
func TestAckAgentScopedDoesNotBumpSession(t *testing.T) {
	s, repo, _, ag, sess := seedDispatchFixture(t)

	if _, err := s.DB.Exec(
		`UPDATE agent_sessions SET last_seen_at = datetime('now','-2 hours') WHERE session_id = ?`,
		sess.SessionID,
	); err != nil {
		t.Fatalf("force-stale last_seen_at: %v", err)
	}
	before, err := s.GetAgentSession(sess.SessionID)
	if err != nil {
		t.Fatalf("get session before ack: %v", err)
	}

	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		Payload:       "agent-wide work",
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add agent-scoped dispatch: %v", err)
	}
	if _, err := s.AckDispatch(d.ID, "got it"); err != nil {
		t.Fatalf("ack agent-scoped dispatch: %v", err)
	}

	after, err := s.GetAgentSession(sess.SessionID)
	if err != nil {
		t.Fatalf("get session after ack: %v", err)
	}
	if !after.LastSeenAt.Equal(before.LastSeenAt) {
		t.Fatalf("agent-scoped ack must not bump session last_seen_at: before=%v after=%v",
			before.LastSeenAt, after.LastSeenAt)
	}
}

// TestCancelThenAckRejected locks in that a cancelled dispatch can't be
// acked — the withdrawal is final.
func TestCancelThenAckRejected(t *testing.T) {
	s, repo, _, ag, _ := seedDispatchFixture(t)
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	if _, err := s.CancelDispatch(d.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := s.AckDispatch(d.ID, "too late"); err == nil {
		t.Fatal("expected ack of cancelled dispatch to error, got nil")
	}
}
