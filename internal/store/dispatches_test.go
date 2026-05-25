package store

import (
	"fmt"
	"strings"
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

// TestListQueuedHidesArchivedIssue — BACI-68 dispatcher guard. A
// queued dispatch whose target issue has since been archived must
// vanish from both ListQueuedModesByRepo and ListQueuedByRepoMode, so
// the background matcher can't bind a fresh agent to it.
func TestListQueuedHidesArchivedIssue(t *testing.T) {
	s, repo, iss, _, _ := seedDispatchFixture(t)
	if _, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchMode("implement"),
		Payload:       "first",
		CreatedBy:     "supervisor",
		InitialStatus: model.DispatchQueued,
	}); err != nil {
		t.Fatalf("add queued dispatch: %v", err)
	}
	// Sanity: before archiving, the queue is visible.
	modes, err := s.ListQueuedModesByRepo(repo.ID)
	if err != nil {
		t.Fatalf("list modes: %v", err)
	}
	if len(modes) != 1 {
		t.Fatalf("pre-archive: modes = %v, want 1", modes)
	}
	rows, err := s.ListQueuedByRepoMode(repo.ID, model.DispatchMode("implement"))
	if err != nil {
		t.Fatalf("list queued: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("pre-archive: queued = %d, want 1", len(rows))
	}
	if err := s.SetIssueArchived(iss.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	modes, err = s.ListQueuedModesByRepo(repo.ID)
	if err != nil {
		t.Fatalf("list modes 2: %v", err)
	}
	if len(modes) != 0 {
		t.Errorf("post-archive: modes = %v, want 0 (matcher must skip)", modes)
	}
	rows, err = s.ListQueuedByRepoMode(repo.ID, model.DispatchMode("implement"))
	if err != nil {
		t.Fatalf("list queued 2: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("post-archive: queued = %d, want 0 (matcher must skip)", len(rows))
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

// TestCountInFlightByModeStalenessGate (BACI-58 §A) locks in that
// CountInFlightByMode excludes delivered dispatches whose target is
// past the staleness window — without the gate, a single dead agent's
// undelivered dispatch permanently strands the queue (the original
// repro from the issue description).
func TestCountInFlightByModeStalenessGate(t *testing.T) {
	s, repo, _, ag, sess := seedDispatchFixture(t)

	// Sanity baseline — a fresh delivered dispatch counts.
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID,
		Mode: model.DispatchModeShip, Payload: "do it", CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	if _, err := s.MarkDispatchDelivered(d.ID); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	n, err := s.CountInFlightByMode(repo.ID, model.DispatchModeShip)
	if err != nil {
		t.Fatalf("count fresh: %v", err)
	}
	if n != 1 {
		t.Fatalf("fresh in-flight count = %d, want 1", n)
	}

	// Force the only session for this identity to look stale, well past
	// the 1h AgentIdlePingThreshold. The dispatch should drop out of the
	// in-flight count — the agent's plausibly dead, no slot consumed.
	if _, err := s.DB.Exec(
		`UPDATE agent_sessions SET last_seen_at = datetime('now','-2 hours') WHERE session_id = ?`,
		sess.SessionID,
	); err != nil {
		t.Fatalf("force-stale: %v", err)
	}
	n, err = s.CountInFlightByMode(repo.ID, model.DispatchModeShip)
	if err != nil {
		t.Fatalf("count stale: %v", err)
	}
	if n != 0 {
		t.Fatalf("stale in-flight count = %d, want 0 (BACI-58 §A excludes orphans)", n)
	}

	// A fresh sibling session for the same identity rescues the count —
	// the identity is plausibly alive again. The matcher should treat
	// the dispatch as occupying its slot.
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-fresh-sibling", RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("upsert sibling: %v", err)
	}
	n, err = s.CountInFlightByMode(repo.ID, model.DispatchModeShip)
	if err != nil {
		t.Fatalf("count sibling: %v", err)
	}
	if n != 1 {
		t.Fatalf("sibling-alive in-flight count = %d, want 1", n)
	}
}

// TestCountInFlightByModeEndedSessionExcluded (BACI-58 §A) covers the
// other half of the staleness gate — a session-targeted dispatch whose
// target session has ended_at set must not count, even if last_seen_at
// is recent. The end IS the orphan signal in that branch.
func TestCountInFlightByModeEndedSessionExcluded(t *testing.T) {
	s, repo, _, _, sess := seedDispatchFixture(t)
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetSessionID: sess.SessionID,
		Mode: model.DispatchModeShip, Payload: "session work", CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	if _, err := s.MarkDispatchDelivered(d.ID); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	// Alive session, fresh: counts.
	n, err := s.CountInFlightByMode(repo.ID, model.DispatchModeShip)
	if err != nil {
		t.Fatalf("count fresh session: %v", err)
	}
	if n != 1 {
		t.Fatalf("fresh-session count = %d, want 1", n)
	}

	// End the session and the dispatch should drop out — the row will
	// be auto-cancelled by §B in the real EndAgentSession path, but
	// even before that the matcher should free the slot.
	if _, err := s.DB.Exec(
		`UPDATE agent_sessions SET ended_at = CURRENT_TIMESTAMP, end_reason = 'crash' WHERE session_id = ?`,
		sess.SessionID,
	); err != nil {
		t.Fatalf("end session: %v", err)
	}
	n, err = s.CountInFlightByMode(repo.ID, model.DispatchModeShip)
	if err != nil {
		t.Fatalf("count ended session: %v", err)
	}
	if n != 0 {
		t.Fatalf("ended-session count = %d, want 0 (BACI-58 §A excludes ended)", n)
	}
}

// TestCountInFlightByModeChannelCreatorStillExcluded locks in that
// the BACI-58 §A staleness gate didn't accidentally drop the
// pre-existing exclusion of bacio-channel setup dispatches.
func TestCountInFlightByModeChannelCreatorStillExcluded(t *testing.T) {
	s, repo, _, _, sess := seedDispatchFixture(t)
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetSessionID: sess.SessionID,
		Mode: model.DispatchModeShip, Payload: "setup nudge",
		CreatedBy: model.SetupDispatchCreator,
	})
	if err != nil {
		t.Fatalf("add setup dispatch: %v", err)
	}
	if _, err := s.MarkDispatchDelivered(d.ID); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	n, err := s.CountInFlightByMode(repo.ID, model.DispatchModeShip)
	if err != nil {
		t.Fatalf("count setup: %v", err)
	}
	if n != 0 {
		t.Fatalf("setup-dispatch count = %d, want 0 (creator-exclusion still wins)", n)
	}
}

// TestInflightByModeForRepo (BACI-145) covers the bulked form: one
// query per repo returns mode → count for every mode that has at
// least one in-flight row, matching what the matcher's per-mode
// CountInFlightByMode walk would have produced. Modes with zero
// in-flight rows are omitted from the map.
func TestInflightByModeForRepo(t *testing.T) {
	s, repo, _, ag, _ := seedDispatchFixture(t)

	// One delivered ship + one pending plan = the two modes the bulk
	// form should surface.
	dShip, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID,
		Mode: model.DispatchModeShip, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add ship: %v", err)
	}
	if _, err := s.MarkDispatchDelivered(dShip.ID); err != nil {
		t.Fatalf("deliver ship: %v", err)
	}
	if _, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID,
		Mode: model.DispatchModePlan, CreatedBy: "supervisor",
	}); err != nil {
		t.Fatalf("add plan: %v", err)
	}

	got, err := s.InflightByModeForRepo(repo.ID)
	if err != nil {
		t.Fatalf("bulk: %v", err)
	}
	if got[model.DispatchModeShip] != 1 {
		t.Errorf("ship count = %d, want 1", got[model.DispatchModeShip])
	}
	if got[model.DispatchModePlan] != 1 {
		t.Errorf("plan count = %d, want 1", got[model.DispatchModePlan])
	}
	// Cross-check against the single-mode form so the two stay in sync.
	for mode := range got {
		n, err := s.CountInFlightByMode(repo.ID, mode)
		if err != nil {
			t.Fatalf("CountInFlightByMode(%s): %v", mode, err)
		}
		if n != got[mode] {
			t.Errorf("bulk vs single-row mismatch for %s: bulk=%d, single=%d", mode, got[mode], n)
		}
	}
	// A non-existent mode is absent from the map (not present as zero).
	if _, ok := got[model.DispatchMode("review")]; ok {
		t.Errorf("review present in map = true, want absent (no in-flight rows)")
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

// TestAddFollowOnDispatchSkipsMatcher (BACI-179) inserts a parent
// dispatch + a dormant follow-on (queued_after_dispatch_id set) and
// asserts the matcher's queued-row lists ignore the follow-on while
// its predecessor remains open. The parent itself is still pending
// (not queued), so it doesn't appear in the queued lists either —
// the test exercises the predicate, not the FIFO order.
func TestAddFollowOnDispatchSkipsMatcher(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	parent, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchModePlan,
		Payload:       "plan it",
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	follow, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeImplement, "supervisor")
	if err != nil {
		t.Fatalf("add follow-on: %v", err)
	}
	if follow.QueuedAfterDispatchID == nil || *follow.QueuedAfterDispatchID != parent.ID {
		t.Fatalf("follow.QueuedAfterDispatchID = %v, want %d", follow.QueuedAfterDispatchID, parent.ID)
	}
	if follow.Status != model.DispatchQueued {
		t.Fatalf("follow status = %q, want queued", follow.Status)
	}
	// Matcher predicate: the dormant row must not appear.
	modes, err := s.ListQueuedModesByRepo(repo.ID)
	if err != nil {
		t.Fatalf("list modes: %v", err)
	}
	if len(modes) != 0 {
		t.Fatalf("dormant follow-on visible to matcher: modes = %v, want empty", modes)
	}
	rows, err := s.ListQueuedByRepoMode(repo.ID, model.DispatchModeImplement)
	if err != nil {
		t.Fatalf("list queued: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("dormant follow-on visible: queued = %d, want 0", len(rows))
	}
}

// TestAddFollowOnDispatchRejectsSecondSlot (BACI-179) — single-slot
// invariant: a second follow-on on the same issue is rejected at the
// store boundary.
func TestAddFollowOnDispatchRejectsSecondSlot(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	parent, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchModePlan,
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	if _, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeImplement, "supervisor"); err != nil {
		t.Fatalf("first follow-on: %v", err)
	}
	if _, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeImplement, "supervisor"); err == nil {
		t.Fatal("expected second follow-on to be rejected, got nil")
	}
}

// TestAddFollowOnDispatchRejectsSettledParent (BACI-179) — the parent
// must still be open. A predecessor that's already acked is past the
// point where attaching a follow-on makes sense (the matcher would
// promote it on the next sweep, racing with the eventual bind).
func TestAddFollowOnDispatchRejectsSettledParent(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	parent, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchModePlan,
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	if _, err := s.AckDispatch(parent.ID, ""); err != nil {
		t.Fatalf("ack parent: %v", err)
	}
	if _, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeImplement, "supervisor"); err == nil {
		t.Fatal("expected follow-on against acked parent to be rejected, got nil")
	}
}

// TestAddFollowOnDispatchRequiresIssue (BACI-179) — follow-ons are
// issue-scoped (the orphan-cancel sweep keys on issue state). A
// parent dispatch without an issue can't carry a follow-on chain.
func TestAddFollowOnDispatchRequiresIssue(t *testing.T) {
	s, repo, _, ag, _ := seedDispatchFixture(t)
	parent, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		Mode:          model.DispatchModePlan,
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add issue-less parent: %v", err)
	}
	if _, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeImplement, "supervisor"); err == nil {
		t.Fatal("expected follow-on against issue-less parent to be rejected, got nil")
	}
}

// TestSchemaInvariant_FollowOnRejectedOnNonQueued (BACI-179 design §5)
// — the Go-side validator is the single guard for "follow-on link
// only on queued rows". Passing QueuedAfterDispatchID with
// InitialStatus = pending must error at the store boundary.
func TestSchemaInvariant_FollowOnRejectedOnNonQueued(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	parent, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchModePlan,
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	// InitialStatus defaults to DispatchPending — a follow-on link on a
	// pending row is the case the validator must reject.
	if _, err := s.AddDispatch(AddDispatchIn{
		RepoID:                repo.ID,
		TargetAgentID:         &ag.ID,
		IssueID:               &iss.ID,
		Mode:                  model.DispatchModeImplement,
		CreatedBy:             "supervisor",
		QueuedAfterDispatchID: &parent.ID,
	}); err == nil {
		t.Fatal("expected QueuedAfterDispatchID on pending dispatch to be rejected, got nil")
	}
}

// TestPromoteReadyFollowOns (BACI-179) walks the promote happy path:
// parent → bind → ack settles the predecessor, no open claim races
// in, and one PromoteReadyFollowOns call clears the
// queued_after_dispatch_id so the next matcher tick can bind the
// follow-on. A re-run of the sweep against the now-cleared row is a
// no-op.
//
// Per BACI-195 the sweep also takes a per-mode state-gates map and
// gate-checks at fire time; here the seeded issue is `todo` and
// implement's default gate admits `todo`, so the row promotes
// normally.
func TestPromoteReadyFollowOns(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	parent, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchModePlan,
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	follow, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeImplement, "supervisor")
	if err != nil {
		t.Fatalf("add follow-on: %v", err)
	}
	gates := mustLoadGates(t, s)
	// Predecessor still pending → sweep is a no-op.
	promoted, gateFailed, err := s.PromoteReadyFollowOns(gates)
	if err != nil {
		t.Fatalf("promote (pre-ack): %v", err)
	}
	if len(promoted) != 0 || len(gateFailed) != 0 {
		t.Fatalf("with open parent: promoted=%d gateFailed=%d, want 0/0", len(promoted), len(gateFailed))
	}
	// Settle the predecessor.
	if _, err := s.AckDispatch(parent.ID, ""); err != nil {
		t.Fatalf("ack parent: %v", err)
	}
	promoted, gateFailed, err = s.PromoteReadyFollowOns(gates)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if len(promoted) != 1 || len(gateFailed) != 0 {
		t.Fatalf("promoted=%d gateFailed=%d, want 1/0", len(promoted), len(gateFailed))
	}
	if promoted[0].ID != follow.ID {
		t.Fatalf("promoted id = %d, want %d", promoted[0].ID, follow.ID)
	}
	if promoted[0].QueuedAfterDispatchID != nil {
		t.Fatalf("promoted row still has queued_after_dispatch_id = %v", promoted[0].QueuedAfterDispatchID)
	}
	// Matcher predicate now sees the row.
	modes, err := s.ListQueuedModesByRepo(repo.ID)
	if err != nil {
		t.Fatalf("list modes: %v", err)
	}
	if len(modes) != 1 || modes[0] != model.DispatchModeImplement {
		t.Fatalf("post-promote modes = %v, want [implement]", modes)
	}
	// Re-run is a no-op.
	promoted, gateFailed, err = s.PromoteReadyFollowOns(gates)
	if err != nil {
		t.Fatalf("promote rerun: %v", err)
	}
	if len(promoted) != 0 || len(gateFailed) != 0 {
		t.Fatalf("promote rerun: promoted=%d gateFailed=%d, want 0/0", len(promoted), len(gateFailed))
	}
}

// mustLoadGates is a test helper that reads the per-mode state-gates
// from the seeded store. The migration seeds built-in templates with
// their default state-gates, so this returns the same gates the
// controller loads in production via AllPromptStates.
func mustLoadGates(t *testing.T, s *Store) map[model.DispatchMode][]model.State {
	t.Helper()
	gates, err := s.AllPromptStates()
	if err != nil {
		t.Fatalf("load prompt state-gates: %v", err)
	}
	return gates
}

// TestPromoteSweep_BlockedByOpenClaim (BACI-179 design §8) — the
// second NOT EXISTS guards against a re-claim racing in between
// predecessor-ack and the promote tick. If the same issue has a fresh
// open claim, the sweep must NOT promote — the claim holder should
// service the issue first.
func TestPromoteSweep_BlockedByOpenClaim(t *testing.T) {
	s, repo, iss, ag, sess := seedDispatchFixture(t)
	parent, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, IssueID: &iss.ID,
		Mode: model.DispatchModePlan, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	if _, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeImplement, "supervisor"); err != nil {
		t.Fatalf("add follow-on: %v", err)
	}
	if _, err := s.AckDispatch(parent.ID, ""); err != nil {
		t.Fatalf("ack parent: %v", err)
	}
	gates := mustLoadGates(t, s)
	// Add a fresh open claim on the issue. The sweep must skip the row.
	if _, _, _, _, err := s.AddAgentClaim(sess.SessionID, iss.ID, "in-progress"); err != nil {
		t.Fatalf("add claim: %v", err)
	}
	promoted, gateFailed, err := s.PromoteReadyFollowOns(gates)
	if err != nil {
		t.Fatalf("promote (claim open): %v", err)
	}
	if len(promoted) != 0 || len(gateFailed) != 0 {
		t.Fatalf("with open claim: promoted=%d gateFailed=%d, want 0/0", len(promoted), len(gateFailed))
	}
	// Release the claim → sweep promotes.
	if _, _, _, err := s.ReleaseAgentClaim(sess.SessionID, iss.ID, model.StateTodo); err != nil {
		t.Fatalf("release claim: %v", err)
	}
	promoted, gateFailed, err = s.PromoteReadyFollowOns(gates)
	if err != nil {
		t.Fatalf("promote after release: %v", err)
	}
	if len(promoted) != 1 || len(gateFailed) != 0 {
		t.Fatalf("after release: promoted=%d gateFailed=%d, want 1/0", len(promoted), len(gateFailed))
	}
}

// TestPromoteSweep_TolerantOfOrphanClaim (BACI-179 design §8 /
// BACI-140 mirror) — a claim held by a session whose ended_at is set
// must NOT block promote. Without this carve-out, a reaper-killed
// claim that didn't get released would strand the follow-on
// indefinitely.
func TestPromoteSweep_TolerantOfOrphanClaim(t *testing.T) {
	s, repo, iss, ag, sess := seedDispatchFixture(t)
	parent, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, IssueID: &iss.ID,
		Mode: model.DispatchModePlan, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	if _, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeImplement, "supervisor"); err != nil {
		t.Fatalf("add follow-on: %v", err)
	}
	if _, err := s.AckDispatch(parent.ID, ""); err != nil {
		t.Fatalf("ack parent: %v", err)
	}
	// Add an open claim, then end the session without releasing it —
	// simulates the BACI-140 reaper-killed-but-not-released path.
	if _, _, _, _, err := s.AddAgentClaim(sess.SessionID, iss.ID, "in-progress"); err != nil {
		t.Fatalf("add claim: %v", err)
	}
	if _, err := s.DB.Exec(
		`UPDATE agent_sessions SET ended_at = CURRENT_TIMESTAMP, end_reason = 'presumed_dead' WHERE session_id = ?`,
		sess.SessionID,
	); err != nil {
		t.Fatalf("end session: %v", err)
	}
	// AddAgentClaim flipped the issue to in_progress (BACI-126a auto-
	// transition); reset it to todo so the BACI-195 fire-time gate
	// can be tested in isolation from the unrelated "orphan claim
	// must not block" assertion this test exists for.
	if err := s.SetIssueState(iss.ID, model.StateTodo); err != nil {
		t.Fatalf("reset state to todo for gate: %v", err)
	}
	promoted, gateFailed, err := s.PromoteReadyFollowOns(mustLoadGates(t, s))
	if err != nil {
		t.Fatalf("promote tolerant: %v", err)
	}
	if len(promoted) != 1 || len(gateFailed) != 0 {
		t.Fatalf("with orphan claim: promoted=%d gateFailed=%d, want 1/0 (ended-session claim must not block)", len(promoted), len(gateFailed))
	}
}

// TestCancelOrphanedFollowOns (BACI-179) — when the issue lands in a
// terminal state (done or cancelled) before the predecessor settles,
// the orphan-cancel sweep flips the dormant row to cancelled. A
// non-terminal issue is a no-op.
func TestCancelOrphanedFollowOns(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	parent, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, IssueID: &iss.ID,
		Mode: model.DispatchModePlan, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	follow, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeImplement, "supervisor")
	if err != nil {
		t.Fatalf("add follow-on: %v", err)
	}
	// Non-terminal issue → no-op.
	cancelled, err := s.CancelOrphanedFollowOns()
	if err != nil {
		t.Fatalf("orphan-cancel (non-terminal): %v", err)
	}
	if len(cancelled) != 0 {
		t.Fatalf("orphan-cancel on non-terminal = %d, want 0", len(cancelled))
	}
	// Land the issue in done; the sweep should cancel the follow-on.
	if err := s.SetIssueState(iss.ID, model.StateDone); err != nil {
		t.Fatalf("set issue done: %v", err)
	}
	cancelled, err = s.CancelOrphanedFollowOns()
	if err != nil {
		t.Fatalf("orphan-cancel: %v", err)
	}
	if len(cancelled) != 1 {
		t.Fatalf("orphan-cancel = %d, want 1", len(cancelled))
	}
	if cancelled[0].ID != follow.ID {
		t.Fatalf("cancelled id = %d, want %d", cancelled[0].ID, follow.ID)
	}
	if cancelled[0].Status != model.DispatchCancelled {
		t.Fatalf("cancelled status = %q, want cancelled", cancelled[0].Status)
	}
}

// TestOrphanCancelSweep_ArchivedNotTerminalIsNoop (BACI-179 design
// §4.a) — archive alone must NOT trigger cancel. The matcher already
// hides archived issues; the lifecycle terminal is the trigger, not
// the visibility flag.
func TestOrphanCancelSweep_ArchivedNotTerminalIsNoop(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	parent, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, IssueID: &iss.ID,
		Mode: model.DispatchModePlan, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	if _, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeImplement, "supervisor"); err != nil {
		t.Fatalf("add follow-on: %v", err)
	}
	// Issue is still in_progress when archived (not a terminal state).
	if err := s.SetIssueState(iss.ID, model.StateInProgress); err != nil {
		t.Fatalf("set in_progress: %v", err)
	}
	if err := s.SetIssueArchived(iss.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	cancelled, err := s.CancelOrphanedFollowOns()
	if err != nil {
		t.Fatalf("orphan-cancel (archived): %v", err)
	}
	if len(cancelled) != 0 {
		t.Fatalf("orphan-cancel on archived-but-not-terminal = %d, want 0", len(cancelled))
	}
}

// TestOrphanCancelSweep_IssuelessRowIgnored (BACI-179 design §4.c) —
// a dormant row whose issue_id has been nulled (FK ON DELETE SET NULL
// after a hard-delete) is NOT the orphan-cancel sweep's
// responsibility. The promote sweep eventually clears the column
// (via the §3 row-4 fallback) and the matcher binds the row.
func TestOrphanCancelSweep_IssuelessRowIgnored(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	parent, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, IssueID: &iss.ID,
		Mode: model.DispatchModePlan, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	follow, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeImplement, "supervisor")
	if err != nil {
		t.Fatalf("add follow-on: %v", err)
	}
	// Simulate the FK cascade — null the issue_id on the follow-on
	// directly (a hard-delete of the issue would also cascade-delete
	// the parent dispatch via the FK, which would interfere with the
	// rest of the assertion). We're testing the sweep's predicate, not
	// the cascade mechanics.
	if _, err := s.DB.Exec(
		`UPDATE agent_dispatches SET issue_id = NULL WHERE id = ?`, follow.ID,
	); err != nil {
		t.Fatalf("null issue_id: %v", err)
	}
	cancelled, err := s.CancelOrphanedFollowOns()
	if err != nil {
		t.Fatalf("orphan-cancel (issueless): %v", err)
	}
	if len(cancelled) != 0 {
		t.Fatalf("orphan-cancel on issueless = %d, want 0 (promote sweep handles this case)", len(cancelled))
	}
}

// TestMatcherIgnoresDormant_ParentRequeued (BACI-179 design §3) — the
// reaper requeue path puts the parent back to `queued`, which the
// predicate treats as not-yet-settled. The follow-on must stay
// dormant until the parent eventually acks (or is cancelled).
func TestMatcherIgnoresDormant_ParentRequeued(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	parent, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, IssueID: &iss.ID,
		Mode: model.DispatchModePlan, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	if _, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeImplement, "supervisor"); err != nil {
		t.Fatalf("add follow-on: %v", err)
	}
	// Reaper-requeue analogue: flip the parent back to queued (status
	// the matcher sees as "not settled" per the design §1 predicate).
	if _, err := s.DB.Exec(
		`UPDATE agent_dispatches SET status = 'queued', target_agent_id = NULL, target_session_id = '' WHERE id = ?`,
		parent.ID,
	); err != nil {
		t.Fatalf("requeue parent: %v", err)
	}
	gates := mustLoadGates(t, s)
	promoted, gateFailed, err := s.PromoteReadyFollowOns(gates)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if len(promoted) != 0 || len(gateFailed) != 0 {
		t.Fatalf("with requeued parent: promoted=%d gateFailed=%d, want 0/0", len(promoted), len(gateFailed))
	}
	// Move the parent through ack — the requeue cycle has resolved.
	if _, err := s.DB.Exec(
		`UPDATE agent_dispatches SET status = 'acked' WHERE id = ?`, parent.ID,
	); err != nil {
		t.Fatalf("ack parent: %v", err)
	}
	promoted, gateFailed, err = s.PromoteReadyFollowOns(gates)
	if err != nil {
		t.Fatalf("promote post-cycle: %v", err)
	}
	if len(promoted) != 1 || len(gateFailed) != 0 {
		t.Fatalf("post-cycle: promoted=%d gateFailed=%d, want 1/0", len(promoted), len(gateFailed))
	}
}

// TestFollowOnForIssue_DormantOnly (BACI-179) — FollowOnForIssue
// returns the row only while it's dormant. After the promote sweep
// clears queued_after_dispatch_id, it's a regular queued dispatch
// (the BoardCard chip surface stops showing it as a follow-on).
func TestFollowOnForIssue_DormantOnly(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	parent, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, IssueID: &iss.ID,
		Mode: model.DispatchModePlan, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	if _, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeImplement, "supervisor"); err != nil {
		t.Fatalf("add follow-on: %v", err)
	}
	got, err := s.FollowOnForIssue(repo.ID, iss.ID)
	if err != nil {
		t.Fatalf("follow-on for issue: %v", err)
	}
	if got == nil {
		t.Fatal("FollowOnForIssue returned nil for live dormant row")
	}
	if _, err := s.AckDispatch(parent.ID, ""); err != nil {
		t.Fatalf("ack parent: %v", err)
	}
	if _, _, err := s.PromoteReadyFollowOns(mustLoadGates(t, s)); err != nil {
		t.Fatalf("promote: %v", err)
	}
	got, err = s.FollowOnForIssue(repo.ID, iss.ID)
	if err != nil {
		t.Fatalf("follow-on for issue (post-promote): %v", err)
	}
	if got != nil {
		t.Fatal("FollowOnForIssue returned non-nil after promote — chip surface should stop showing the row")
	}
}

// TestCancelDeliveredRejected (BACI-130) locks in the store-level
// guard: once a dispatch is delivered (the worker has taken the Task
// call) the row must not be cancellable, because doing so would just
// drop the kanban activity pill while the work continues. The reject
// happens before the transaction so neither the dispatch row nor the
// issue's waiting_for_claim flag is mutated.
func TestCancelDeliveredRejected(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		IssueID:       &iss.ID,
		Payload:       "do a thing",
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	if _, err := s.MarkDispatchDelivered(d.ID); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	if _, err := s.CancelDispatch(d.ID); err == nil {
		t.Fatal("expected cancel of delivered dispatch to error, got nil")
	} else if !strings.Contains(err.Error(), "delivered") {
		t.Errorf("error %q should contain \"delivered\"", err.Error())
	} else if !strings.Contains(err.Error(), fmt.Sprint(d.ID)) {
		t.Errorf("error %q should contain dispatch id %d", err.Error(), d.ID)
	}
	// The reject must not mutate the dispatch row.
	got, err := s.GetDispatch(d.ID)
	if err != nil {
		t.Fatalf("re-read dispatch: %v", err)
	}
	if got.Status != model.DispatchDelivered {
		t.Errorf("status after rejected cancel = %q, want delivered", got.Status)
	}
	// The reject must not clear the issue's waiting_for_claim flag
	// either — the claim path is what clears it, not a failed cancel.
	issAfter, err := s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("re-read issue: %v", err)
	}
	if !issAfter.WaitingForClaim {
		t.Errorf("issue.waiting_for_claim = false, want true (rejected cancel must not clear)")
	}
}

// TestFollowOnGatePasses_PureHelper (BACI-195) — locks in the
// per-row gate-check semantics independently of the DB. A row with
// no issue (state == "") always passes; otherwise the issue state
// must be in gates[mode].
func TestFollowOnGatePasses_PureHelper(t *testing.T) {
	gates := map[model.DispatchMode][]model.State{
		model.DispatchModeImplement: {model.StateTodo},
		model.DispatchModeReview:    {model.StateInReview},
	}
	cases := []struct {
		name  string
		gates map[model.DispatchMode][]model.State
		mode  model.DispatchMode
		state model.State
		want  bool
	}{
		{"no_issue_always_passes", gates, model.DispatchModeImplement, "", true},
		{"implement_from_todo", gates, model.DispatchModeImplement, model.StateTodo, true},
		{"implement_from_in_progress_blocked", gates, model.DispatchModeImplement, model.StateInProgress, false},
		{"implement_from_done_blocked", gates, model.DispatchModeImplement, model.StateDone, false},
		{"review_from_in_review", gates, model.DispatchModeReview, model.StateInReview, true},
		{"unknown_mode_blocked", gates, model.DispatchMode("nosuchmode"), model.StateTodo, false},
		{"nil_gates_blocks_typed_row", nil, model.DispatchModeImplement, model.StateTodo, false},
		{"nil_gates_allows_issueless_row", nil, model.DispatchModeImplement, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := followOnGatePasses(c.gates, c.mode, c.state); got != c.want {
				t.Fatalf("followOnGatePasses(%q, %q) = %v, want %v", c.mode, c.state, got, c.want)
			}
		})
	}
}

// TestPromoteReadyFollowOns_GateFailCancels (BACI-195) — when the
// issue's post-release state doesn't admit the follow-on's mode, the
// sweep cancels the row instead of clearing the FK. The cancelled
// row is returned in gateFailed; the promoted slice is empty.
// queued_after_dispatch_id stays set on the cancelled row so the
// audit Details still references the parent dispatch.
func TestPromoteReadyFollowOns_GateFailCancels(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	parent, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, IssueID: &iss.ID,
		Mode: model.DispatchModePlan, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	// Queue a ship follow-on. ship's default gate is `in_review`; the
	// issue is `todo` after the parent settles — gate fails.
	follow, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeShip, "supervisor")
	if err != nil {
		t.Fatalf("add follow-on: %v", err)
	}
	if _, err := s.AckDispatch(parent.ID, ""); err != nil {
		t.Fatalf("ack parent: %v", err)
	}
	promoted, gateFailed, err := s.PromoteReadyFollowOns(mustLoadGates(t, s))
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(promoted) != 0 || len(gateFailed) != 1 {
		t.Fatalf("promoted=%d gateFailed=%d, want 0/1", len(promoted), len(gateFailed))
	}
	if gateFailed[0].ID != follow.ID {
		t.Fatalf("gateFailed id = %d, want %d", gateFailed[0].ID, follow.ID)
	}
	if gateFailed[0].Status != model.DispatchCancelled {
		t.Fatalf("gateFailed status = %q, want cancelled", gateFailed[0].Status)
	}
	// queued_after_dispatch_id intact on the cancelled row — audit
	// readers join through it back to the parent.
	if gateFailed[0].QueuedAfterDispatchID == nil {
		t.Fatal("gateFailed queued_after_dispatch_id = nil, want parent id (audit join surface)")
	}
	if *gateFailed[0].QueuedAfterDispatchID != parent.ID {
		t.Fatalf("gateFailed queued_after_dispatch_id = %d, want %d", *gateFailed[0].QueuedAfterDispatchID, parent.ID)
	}
	// Re-run is a no-op — the cancelled row no longer matches the
	// status='queued' predicate.
	promoted, gateFailed, err = s.PromoteReadyFollowOns(mustLoadGates(t, s))
	if err != nil {
		t.Fatalf("sweep rerun: %v", err)
	}
	if len(promoted) != 0 || len(gateFailed) != 0 {
		t.Fatalf("rerun: promoted=%d gateFailed=%d, want 0/0", len(promoted), len(gateFailed))
	}
}

// TestPromoteReadyFollowOns_MixedBatch (BACI-195) — within one sweep
// some ready rows promote (gate passes) and others cancel (gate
// fails). The transaction commits both partitions atomically; the
// returned slices reflect the split.
func TestPromoteReadyFollowOns_MixedBatch(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("MIX", "mix", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	// Issue A — stays in todo (implement gate passes).
	issA, err := s.CreateIssue(repo.ID, nil, "todo-issue", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create issue A: %v", err)
	}
	// Issue B — in_review (implement gate fails; review gate would
	// pass, but we queue implement deliberately to fail the gate).
	issB, err := s.CreateIssue(repo.ID, nil, "in-review-issue", "", model.StateInReview, nil)
	if err != nil {
		t.Fatalf("create issue B: %v", err)
	}
	ag, _, err := s.UpsertAgent("brave-otter@claude.test", true)
	if err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	parentA, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, IssueID: &issA.ID,
		Mode: model.DispatchModePlan, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent A: %v", err)
	}
	parentB, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, IssueID: &issB.ID,
		Mode: model.DispatchModeReview, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent B: %v", err)
	}
	followA, err := s.AddFollowOnDispatch(repo.ID, parentA.ID, model.DispatchModeImplement, "supervisor")
	if err != nil {
		t.Fatalf("add follow-on A: %v", err)
	}
	followB, err := s.AddFollowOnDispatch(repo.ID, parentB.ID, model.DispatchModeImplement, "supervisor")
	if err != nil {
		t.Fatalf("add follow-on B: %v", err)
	}
	if _, err := s.AckDispatch(parentA.ID, ""); err != nil {
		t.Fatalf("ack parent A: %v", err)
	}
	if _, err := s.AckDispatch(parentB.ID, ""); err != nil {
		t.Fatalf("ack parent B: %v", err)
	}
	promoted, gateFailed, err := s.PromoteReadyFollowOns(mustLoadGates(t, s))
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(promoted) != 1 {
		t.Fatalf("promoted = %d, want 1", len(promoted))
	}
	if promoted[0].ID != followA.ID {
		t.Fatalf("promoted id = %d, want %d (the todo-issue follow-on)", promoted[0].ID, followA.ID)
	}
	if len(gateFailed) != 1 {
		t.Fatalf("gateFailed = %d, want 1", len(gateFailed))
	}
	if gateFailed[0].ID != followB.ID {
		t.Fatalf("gateFailed id = %d, want %d (the in_review-issue follow-on)", gateFailed[0].ID, followB.ID)
	}
}

// TestPromoteReadyFollowOns_IssuelessRowPromotes (BACI-195 design)
// — a dormant row whose issue_id was nulled by an FK cascade has no
// state to gate against; the sweep promotes it regardless of gates,
// matching the existing comment that the matcher's regular path
// handles issue-less queued rows fine.
func TestPromoteReadyFollowOns_IssuelessRowPromotes(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	parent, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, IssueID: &iss.ID,
		Mode: model.DispatchModePlan, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	follow, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeShip, "supervisor")
	if err != nil {
		t.Fatalf("add follow-on: %v", err)
	}
	if _, err := s.AckDispatch(parent.ID, ""); err != nil {
		t.Fatalf("ack parent: %v", err)
	}
	// Simulate the FK cascade — null the issue_id on the follow-on so
	// the gate check skips it (no state to look up). Without this it
	// would gate-fail (ship requires in_review, issue is todo).
	if _, err := s.DB.Exec(
		`UPDATE agent_dispatches SET issue_id = NULL WHERE id = ?`, follow.ID,
	); err != nil {
		t.Fatalf("null issue_id: %v", err)
	}
	// Empty gates intentionally — issueless rows must promote regardless.
	promoted, gateFailed, err := s.PromoteReadyFollowOns(nil)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(promoted) != 1 || len(gateFailed) != 0 {
		t.Fatalf("issueless row: promoted=%d gateFailed=%d, want 1/0", len(promoted), len(gateFailed))
	}
}

// TestPromoteReadyFollowOns_ImplementFromInProgressRepro (BACI-195
// repro) — the specific bug from the brief. The user queues an
// implement follow-on while the parent (research) is in_progress;
// after the parent releases the issue back to `todo` (research
// workflow shape) the follow-on must promote. Before the fix the
// queue-time gate rejected the call; after the fix the queue
// succeeds and fire-time gate passes.
func TestPromoteReadyFollowOns_ImplementFromInProgressRepro(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	// Parent research dispatch — in_progress while the worker runs.
	if err := s.SetIssueState(iss.ID, model.StateInProgress); err != nil {
		t.Fatalf("set in_progress: %v", err)
	}
	parent, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, IssueID: &iss.ID,
		Mode: model.DispatchModePlan, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	// User queues an implement follow-on while the issue is still
	// in_progress — this is exactly the call that used to 400 with
	// "the implement prompt can't run from a in_progress issue".
	follow, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeImplement, "supervisor")
	if err != nil {
		t.Fatalf("add follow-on (the bug case): %v", err)
	}
	// Parent finishes and releases the issue back to `todo` (the
	// post-release state the research-worker leaves behind).
	if _, err := s.AckDispatch(parent.ID, ""); err != nil {
		t.Fatalf("ack parent: %v", err)
	}
	if err := s.SetIssueState(iss.ID, model.StateTodo); err != nil {
		t.Fatalf("set post-release state: %v", err)
	}
	promoted, gateFailed, err := s.PromoteReadyFollowOns(mustLoadGates(t, s))
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(promoted) != 1 || len(gateFailed) != 0 {
		t.Fatalf("repro: promoted=%d gateFailed=%d, want 1/0 (gate passes from post-release todo)", len(promoted), len(gateFailed))
	}
	if promoted[0].ID != follow.ID {
		t.Fatalf("promoted id = %d, want %d", promoted[0].ID, follow.ID)
	}
}
