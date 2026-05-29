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

// seedShipDispatchForBranch (BACI-227 test helper) creates an issue
// targeted at `branch` and queues a `ship` dispatch against it,
// binding the dispatch to a freshly-created agent + session so it
// surfaces in the per-(mode, branch) in-flight count. Returns the
// bound dispatch row (Status=pending, BaseBranch=branch).
func seedShipDispatchForBranch(t *testing.T, s *Store, repo *model.Repo, feat *model.Feature, title, agentName, branch string) *model.AgentDispatch {
	t.Helper()
	// Pin the issue with a base-branch override so ResolveBaseBranch
	// returns exactly `branch` — independent of whether a feature was
	// passed and what its branch_name is.
	override := branch
	var featID *int64
	if feat != nil {
		featID = &feat.ID
	}
	iss, err := s.CreateIssue(repo.ID, featID, title, "", model.StateInReview, nil, override)
	if err != nil {
		t.Fatalf("create issue %q: %v", title, err)
	}
	queued, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchModeShip,
		Payload:       "ship it",
		CreatedBy:     "supervisor",
		InitialStatus: model.DispatchQueued,
	})
	if err != nil {
		t.Fatalf("queue %q: %v", title, err)
	}
	ag, _, err := s.UpsertAgent(agentName, true)
	if err != nil {
		t.Fatalf("upsert agent %q: %v", agentName, err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-" + agentName, RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("upsert session for %q: %v", agentName, err)
	}
	bound, err := s.BindQueuedDispatch(queued.ID, ag.ID)
	if err != nil {
		t.Fatalf("bind %q: %v", title, err)
	}
	if bound.BaseBranch != branch {
		t.Fatalf("seed: bound base_branch = %q, want %q", bound.BaseBranch, branch)
	}
	return bound
}

// TestCountInFlightByModeBase_PerBranchIsolation (BACI-227) — three
// delivered ship dispatches on two different branches must group
// independently when grouped per branch. Without the per-branch
// grouping a `ship` cap of 1 would over-serialise across branches.
func TestCountInFlightByModeBase_PerBranchIsolation(t *testing.T) {
	s, repo, _, _, _ := seedDispatchFixture(t)

	// Two ship rows on feat/A, one on feat/B.
	seedShipDispatchForBranch(t, s, repo, nil, "feat/A ship 1", "feat-a-1@claude.test", "feat/A")
	seedShipDispatchForBranch(t, s, repo, nil, "feat/A ship 2", "feat-a-2@claude.test", "feat/A")
	seedShipDispatchForBranch(t, s, repo, nil, "feat/B ship", "feat-b-1@claude.test", "feat/B")

	cases := []struct {
		branch string
		want   int
	}{
		{branch: "feat/A", want: 2},
		{branch: "feat/B", want: 1},
		{branch: "main", want: 0},
	}
	for _, tc := range cases {
		n, err := s.CountInFlightByModeBase(repo.ID, model.DispatchModeShip, tc.branch)
		if err != nil {
			t.Fatalf("CountInFlightByModeBase(%s): %v", tc.branch, err)
		}
		if n != tc.want {
			t.Errorf("ship on %s = %d, want %d", tc.branch, n, tc.want)
		}
	}
}

// TestCountInFlightByModeBase_NullColumnCountsAsEmpty (BACI-227) — a
// legacy / NULL base_branch row groups with the empty-string key.
// Documents the COALESCE semantics so callers know to pass "" (not
// "main") to count pre-BACI-226 rows.
func TestCountInFlightByModeBase_NullColumnCountsAsEmpty(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, IssueID: &iss.ID,
		Mode: model.DispatchModeShip, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	if _, err := s.MarkDispatchDelivered(d.ID); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	// Force-NULL the base_branch column to simulate a legacy row
	// queued before BACI-226 stamped the value.
	if _, err := s.DB.Exec(
		`UPDATE agent_dispatches SET base_branch = NULL WHERE id = ?`, d.ID,
	); err != nil {
		t.Fatalf("force-null: %v", err)
	}

	// Sanity: the row's not counted under any concrete branch name.
	n, err := s.CountInFlightByModeBase(repo.ID, model.DispatchModeShip, "main")
	if err != nil {
		t.Fatalf("count main: %v", err)
	}
	if n != 0 {
		t.Errorf("NULL row counted under main = %d, want 0 (COALESCE collapses NULL to '')", n)
	}
	// The empty string is the right key for the legacy row.
	n, err = s.CountInFlightByModeBase(repo.ID, model.DispatchModeShip, "")
	if err != nil {
		t.Fatalf("count empty: %v", err)
	}
	if n != 1 {
		t.Errorf("NULL row counted under '' = %d, want 1", n)
	}
}

// TestCountInFlightByModeBase_StalenessGateApplies (BACI-227) — same
// staleness gate as the per-mode form: a delivered dispatch whose
// target session is past AgentIdlePingThreshold drops out of the
// per-branch count, freeing the slot for a live agent's bind.
func TestCountInFlightByModeBase_StalenessGateApplies(t *testing.T) {
	s, repo, _, _, _ := seedDispatchFixture(t)
	// One ship on feat/A (will be aged), one on feat/B (stays live).
	staleDispatch := seedShipDispatchForBranch(t, s, repo, nil, "stale feat/A", "stale-a@claude.test", "feat/A")
	seedShipDispatchForBranch(t, s, repo, nil, "live feat/B", "live-b@claude.test", "feat/B")

	// Force-stale the feat/A session so its dispatch drops out.
	if _, err := s.DB.Exec(
		`UPDATE agent_sessions SET last_seen_at = datetime('now','-3 hours') WHERE session_id = ?`,
		"sess-stale-a@claude.test",
	); err != nil {
		t.Fatalf("force-stale: %v", err)
	}

	n, err := s.CountInFlightByModeBase(repo.ID, model.DispatchModeShip, "feat/A")
	if err != nil {
		t.Fatalf("count feat/A: %v", err)
	}
	if n != 0 {
		t.Errorf("stale feat/A count = %d, want 0 (BACI-58 staleness drops orphan)", n)
	}
	// feat/B is untouched.
	n, err = s.CountInFlightByModeBase(repo.ID, model.DispatchModeShip, "feat/B")
	if err != nil {
		t.Fatalf("count feat/B: %v", err)
	}
	if n != 1 {
		t.Errorf("live feat/B count = %d, want 1 (sibling branch unaffected)", n)
	}
	// Suppress unused warning for the seeded dispatch — only used for shape.
	_ = staleDispatch
}

// TestCountInFlightByModeBase_ChannelCreatorExcluded (BACI-227) —
// SetupDispatchCreator rows stay excluded under the per-branch grouping
// just as they are in the per-mode form. A setup nudge has no
// base_branch (no issue), so the row groups under "" — and is still
// excluded by the creator filter.
func TestCountInFlightByModeBase_ChannelCreatorExcluded(t *testing.T) {
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
	n, err := s.CountInFlightByModeBase(repo.ID, model.DispatchModeShip, "")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("setup-creator count = %d, want 0 (creator-exclusion still wins per-branch)", n)
	}
}

// TestInflightByModeBaseForRepo (BACI-227) — the bulked form returns
// one entry per (mode, branch) pair with non-zero count and omits
// zero entries, matching the per-mode InflightByModeForRepo contract
// extended to the (mode, branch) key.
func TestInflightByModeBaseForRepo(t *testing.T) {
	s, repo, _, _, _ := seedDispatchFixture(t)

	seedShipDispatchForBranch(t, s, repo, nil, "feat/A ship", "bulk-a@claude.test", "feat/A")
	seedShipDispatchForBranch(t, s, repo, nil, "feat/B ship", "bulk-b@claude.test", "feat/B")

	// Plus a plan dispatch on main so we get a second mode too.
	planIss, err := s.CreateIssue(repo.ID, nil, "main plan", "", model.StateTodo, nil, "main")
	if err != nil {
		t.Fatalf("create plan issue: %v", err)
	}
	planQueued, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		IssueID:       &planIss.ID,
		Mode:          model.DispatchModePlan,
		Payload:       "plan",
		CreatedBy:     "supervisor",
		InitialStatus: model.DispatchQueued,
	})
	if err != nil {
		t.Fatalf("queue plan: %v", err)
	}
	planAg, _, err := s.UpsertAgent("bulk-plan@claude.test", true)
	if err != nil {
		t.Fatalf("upsert plan agent: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-bulk-plan", RepoID: repo.ID, AgentID: &planAg.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("upsert plan session: %v", err)
	}
	if _, err := s.BindQueuedDispatch(planQueued.ID, planAg.ID); err != nil {
		t.Fatalf("bind plan: %v", err)
	}

	got, err := s.InflightByModeBaseForRepo(repo.ID)
	if err != nil {
		t.Fatalf("bulk: %v", err)
	}

	want := map[InflightKey]int{
		{Mode: model.DispatchModeShip, BaseBranch: "feat/A"}: 1,
		{Mode: model.DispatchModeShip, BaseBranch: "feat/B"}: 1,
		{Mode: model.DispatchModePlan, BaseBranch: "main"}:   1,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("got[%+v] = %d, want %d", k, got[k], v)
		}
	}
	// Cross-check: the per-(mode, branch) single-row form must agree
	// with the bulked entry for every key.
	for k, v := range got {
		n, err := s.CountInFlightByModeBase(repo.ID, k.Mode, k.BaseBranch)
		if err != nil {
			t.Fatalf("CountInFlightByModeBase(%+v): %v", k, err)
		}
		if n != v {
			t.Errorf("bulk vs single-row mismatch for %+v: bulk=%d, single=%d", k, v, n)
		}
	}
	// A non-existent (mode, branch) pair is absent (not zero).
	zeroKey := InflightKey{Mode: model.DispatchMode("review"), BaseBranch: "main"}
	if _, ok := got[zeroKey]; ok {
		t.Errorf("review/main present in map = true, want absent (no in-flight rows)")
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
// BACI-252: the BACI-195 fire-time state-gate is gone; the sweep
// always promotes a ready row (orphan-cancel and blockers-clear gates
// still apply, both unchanged).
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
	// Predecessor still pending → sweep is a no-op.
	promoted, err := s.PromoteReadyFollowOns()
	if err != nil {
		t.Fatalf("promote (pre-ack): %v", err)
	}
	if len(promoted) != 0 {
		t.Fatalf("with open parent: promoted=%d, want 0", len(promoted))
	}
	// Settle the predecessor.
	if _, err := s.AckDispatch(parent.ID, ""); err != nil {
		t.Fatalf("ack parent: %v", err)
	}
	promoted, err = s.PromoteReadyFollowOns()
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if len(promoted) != 1 {
		t.Fatalf("promoted=%d, want 1", len(promoted))
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
	promoted, err = s.PromoteReadyFollowOns()
	if err != nil {
		t.Fatalf("promote rerun: %v", err)
	}
	if len(promoted) != 0 {
		t.Fatalf("promote rerun: promoted=%d, want 0", len(promoted))
	}
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
	// Add a fresh open claim on the issue. The sweep must skip the row.
	if _, _, _, _, err := s.AddAgentClaim(sess.SessionID, iss.ID, "in-progress"); err != nil {
		t.Fatalf("add claim: %v", err)
	}
	promoted, err := s.PromoteReadyFollowOns()
	if err != nil {
		t.Fatalf("promote (claim open): %v", err)
	}
	if len(promoted) != 0 {
		t.Fatalf("with open claim: promoted=%d, want 0", len(promoted))
	}
	// Release the claim → sweep promotes.
	if _, _, _, err := s.ReleaseAgentClaim(sess.SessionID, iss.ID, model.StateTodo); err != nil {
		t.Fatalf("release claim: %v", err)
	}
	promoted, err = s.PromoteReadyFollowOns()
	if err != nil {
		t.Fatalf("promote after release: %v", err)
	}
	if len(promoted) != 1 {
		t.Fatalf("after release: promoted=%d, want 1", len(promoted))
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
	promoted, err := s.PromoteReadyFollowOns()
	if err != nil {
		t.Fatalf("promote tolerant: %v", err)
	}
	if len(promoted) != 1 {
		t.Fatalf("with orphan claim: promoted=%d, want 1 (ended-session claim must not block)", len(promoted))
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
	promoted, err := s.PromoteReadyFollowOns()
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if len(promoted) != 0 {
		t.Fatalf("with requeued parent: promoted=%d, want 0", len(promoted))
	}
	// Move the parent through ack — the requeue cycle has resolved.
	if _, err := s.DB.Exec(
		`UPDATE agent_dispatches SET status = 'acked' WHERE id = ?`, parent.ID,
	); err != nil {
		t.Fatalf("ack parent: %v", err)
	}
	promoted, err = s.PromoteReadyFollowOns()
	if err != nil {
		t.Fatalf("promote post-cycle: %v", err)
	}
	if len(promoted) != 1 {
		t.Fatalf("post-cycle: promoted=%d, want 1", len(promoted))
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
	if _, err := s.PromoteReadyFollowOns(); err != nil {
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
// happens before the transaction so the dispatch row is not mutated.
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
	// BACI-255: the row is the spinner signal. The rejected cancel
	// must leave it in 'delivered', so WaitingDispatchForIssue keeps
	// returning it — the kanban keeps rendering the spinner.
	wd, err := s.WaitingDispatchForIssue(repo.ID, iss.ID)
	if err != nil {
		t.Fatalf("WaitingDispatchForIssue: %v", err)
	}
	if wd == nil || wd.ID != d.ID {
		t.Errorf("WaitingDispatchForIssue = %+v, want the same delivered row (rejected cancel must not change visibility)", wd)
	}
}

// TestPromoteReadyFollowOns_PromotesFromAnyState (BACI-252) — with
// the BACI-195 fire-time gate gone, a follow-on for any mode promotes
// regardless of the issue's current state. Locks in that, for example,
// a `ship` follow-on against a `todo` issue still fires when the
// parent settles. Pre-BACI-252 this row would have been cancelled with
// the agent.followon.gate_fail audit op.
func TestPromoteReadyFollowOns_PromotesFromAnyState(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	parent, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetAgentID: &ag.ID, IssueID: &iss.ID,
		Mode: model.DispatchModePlan, CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	// ship follow-on against a `todo` issue — pre-BACI-252 the
	// fire-time gate would reject this combination (ship's default
	// gate was `in_review`). With the gate gone the row promotes.
	follow, err := s.AddFollowOnDispatch(repo.ID, parent.ID, model.DispatchModeShip, "supervisor")
	if err != nil {
		t.Fatalf("add follow-on: %v", err)
	}
	if _, err := s.AckDispatch(parent.ID, ""); err != nil {
		t.Fatalf("ack parent: %v", err)
	}
	promoted, err := s.PromoteReadyFollowOns()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(promoted) != 1 {
		t.Fatalf("promoted=%d, want 1 (BACI-252: no fire-time gate)", len(promoted))
	}
	if promoted[0].ID != follow.ID {
		t.Fatalf("promoted id = %d, want %d", promoted[0].ID, follow.ID)
	}
}

// TestBindQueuedDispatch_NoOpAfterDelivered (BACI-200, Bug 1) locks in
// the matcher's stickiness gate. Once a dispatch has been delivered
// (`delivered_at IS NOT NULL`), the matcher must refuse to rebind it
// to a second agent — even if the row was re-stamped back to
// status='queued' (the BACI-133 reaper requeue path used to do this
// without resetting delivered_at, which produced the BACI-197
// duplicate-implementation incident).
func TestBindQueuedDispatch_NoOpAfterDelivered(t *testing.T) {
	s, repo, _, _, _ := seedDispatchFixture(t)
	first, _, err := s.UpsertAgent("first-otter@claude.test", true)
	if err != nil {
		t.Fatalf("upsert first agent: %v", err)
	}
	second, _, err := s.UpsertAgent("second-cobra@claude.test", true)
	if err != nil {
		t.Fatalf("upsert second agent: %v", err)
	}
	// Queue a dispatch (target-less, status='queued') — the BACI-51
	// auto-dispatch path the matcher consumes.
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		Mode:          model.DispatchModeImplement,
		Payload:       "work",
		CreatedBy:     "supervisor",
		InitialStatus: model.DispatchQueued,
	})
	if err != nil {
		t.Fatalf("add queued dispatch: %v", err)
	}
	// First bind succeeds (status='queued' → 'pending').
	bound, err := s.BindQueuedDispatch(d.ID, first.ID)
	if err != nil {
		t.Fatalf("first bind: %v", err)
	}
	if bound.Status != model.DispatchPending {
		t.Fatalf("after first bind: status = %q, want pending", bound.Status)
	}
	// Worker takes the dispatch — delivered_at is stamped.
	if _, err := s.MarkDispatchDelivered(d.ID); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	// Reaper presumes-dead requeue — but with the BACI-200 fix it also
	// resets delivered_at, so reaching this state via Requeue is the
	// "genuinely dead worker, retry" path. Instead emulate the older
	// broken behaviour (status='queued' + target=NULL but delivered_at
	// retained) directly, which is what the BACI-197 audit log
	// surfaced.
	if _, err := s.DB.Exec(
		`UPDATE agent_dispatches
		    SET status = 'queued', target_agent_id = NULL, target_session_id = ''
		  WHERE id = ?`, d.ID,
	); err != nil {
		t.Fatalf("emulate broken requeue: %v", err)
	}
	// Second bind from the matcher MUST refuse — delivered_at is set,
	// the dispatch is owned by the first worker.
	if _, err := s.BindQueuedDispatch(d.ID, second.ID); err == nil {
		t.Fatalf("second bind should have failed (delivered_at stickiness gate), got nil err")
	} else if err.Error() != ErrNotFound.Error() {
		t.Fatalf("second bind err = %v, want ErrNotFound", err)
	}
	// Row is unchanged from the post-emulated-requeue state — neither
	// status nor target_agent_id flipped.
	after, err := s.GetDispatch(d.ID)
	if err != nil {
		t.Fatalf("get after second bind: %v", err)
	}
	if after.Status != model.DispatchQueued {
		t.Fatalf("after blocked rebind: status = %q, want queued", after.Status)
	}
	if after.TargetAgentID != nil {
		t.Fatalf("after blocked rebind: target_agent_id = %v, want nil", after.TargetAgentID)
	}
}

// TestBindQueuedDispatch_RequeueRecoversDeliveredDispatch (BACI-200,
// Bug 1) is the symmetric coverage of the delivered_at gate: the
// BACI-133 reaper recovery path MUST still work for a genuinely-dead
// worker. The reaper's requeue branch resets delivered_at alongside
// status/target so the matcher's gate admits the row again.
func TestBindQueuedDispatch_RequeueRecoversDeliveredDispatch(t *testing.T) {
	s, repo, _, _, sess := seedDispatchFixture(t)
	second, _, err := s.UpsertAgent("recover-cobra@claude.test", true)
	if err != nil {
		t.Fatalf("upsert second agent: %v", err)
	}
	// Queue + bind + deliver to the first session.
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:          repo.ID,
		TargetSessionID: sess.SessionID,
		Mode:            model.DispatchModeImplement,
		Payload:         "work",
		CreatedBy:       "supervisor",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	if _, err := s.MarkDispatchDelivered(d.ID); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}
	// Reaper force-ends the session with presumed_dead + requeue cascade.
	// This is the BACI-133 path that should successfully recycle the
	// dispatch to a fresh agent.
	if _, _, _, _, _, err := s.EndAgentSession(
		sess.SessionID, string(model.EndReasonPresumedDead), model.StateInProgress, DispatchCascadeRequeue,
	); err != nil {
		t.Fatalf("end session (requeue): %v", err)
	}
	post, err := s.GetDispatch(d.ID)
	if err != nil {
		t.Fatalf("get post-requeue: %v", err)
	}
	if post.Status != model.DispatchQueued {
		t.Fatalf("after requeue: status = %q, want queued", post.Status)
	}
	if post.DeliveredAt != nil {
		t.Fatalf("after requeue: delivered_at = %v, want nil (BACI-200 reset for recovery)", post.DeliveredAt)
	}
	// Matcher rebind to a fresh agent succeeds — the delivered_at gate
	// admits the row now that the reaper cleared it.
	rebound, err := s.BindQueuedDispatch(d.ID, second.ID)
	if err != nil {
		t.Fatalf("rebind after requeue: %v", err)
	}
	if rebound.Status != model.DispatchPending {
		t.Fatalf("after rebind: status = %q, want pending", rebound.Status)
	}
	if rebound.TargetAgentID == nil || *rebound.TargetAgentID != second.ID {
		t.Fatalf("after rebind: target_agent_id = %v, want %d", rebound.TargetAgentID, second.ID)
	}
}

// seedBlockerFixture (BACI-217) extends seedDispatchFixture with a
// second issue inserted as an open blocker of the first: the returned
// blocked-side issue (`iss`) has at least one open `blocks` edge from
// the blocker. Used by every test of the blockers-clear follow-on
// variant. Returns (store, repo, blocked-issue, blocker-issue).
func seedBlockerFixture(t *testing.T) (*Store, *model.Repo, *model.Issue, *model.Issue) {
	t.Helper()
	s, repo, blocked, _, _ := seedDispatchFixture(t)
	blocker, err := s.CreateIssue(repo.ID, nil, "the blocker", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create blocker issue: %v", err)
	}
	if err := s.CreateRelation(blocker.ID, blocked.ID, model.RelBlocks); err != nil {
		t.Fatalf("create blocks edge: %v", err)
	}
	return s, repo, blocked, blocker
}

// TestAddBlockerFollowOnDispatch_HappyPath (BACI-217) locks in the
// blockers-clear insert path. A blocked-and-idle issue with at least
// one open blocker writes a queued row with the new flag set, no
// parent-acks link, no target. The dormant variant deliberately stays
// invisible to the kanban's "waiting" derivation — the dormant row
// isn't waiting for the matcher (it's waiting for the blocker gate),
// and surfacing it as waiting would render a misleading spinner on
// an otherwise-idle ticket. BACI-255: enforced by the
// activeDispatchByIssueID / TUI waitingIssues filters skipping any
// queued row with a dormant gate.
func TestAddBlockerFollowOnDispatch_HappyPath(t *testing.T) {
	s, repo, blocked, _ := seedBlockerFixture(t)
	d, err := s.AddBlockerFollowOnDispatch(repo.ID, blocked.ID, model.DispatchModePlan, "supervisor")
	if err != nil {
		t.Fatalf("AddBlockerFollowOnDispatch: %v", err)
	}
	if !d.QueuedUntilBlockersClear {
		t.Fatal("queued_until_blockers_clear flag not set on returned row")
	}
	if d.QueuedAfterDispatchID != nil {
		t.Fatalf("queued_after_dispatch_id should be nil on a blockers-clear row, got %v", d.QueuedAfterDispatchID)
	}
	if d.Status != model.DispatchQueued {
		t.Fatalf("status = %q, want queued", d.Status)
	}
}

// TestAddBlockerFollowOnDispatch_RefusesUnblockedIssue (BACI-217) — an
// issue with zero open blockers is not eligible for the blockers-clear
// variant; the matcher would bind it on its next tick, so writing a
// dormant row would just shadow that bind for a sweep cycle.
func TestAddBlockerFollowOnDispatch_RefusesUnblockedIssue(t *testing.T) {
	s, repo, blocked, blocker := seedBlockerFixture(t)
	// Close the only blocker so the blocked side now has zero open
	// blockers — the AddBlockerFollowOnDispatch call must refuse.
	if err := s.SetIssueState(blocker.ID, model.StateDone); err != nil {
		t.Fatalf("close blocker: %v", err)
	}
	_, err := s.AddBlockerFollowOnDispatch(repo.ID, blocked.ID, model.DispatchModePlan, "supervisor")
	if err == nil {
		t.Fatal("expected unblocked-issue to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "no open blockers") {
		t.Fatalf("error should name the unblocked case, got: %v", err)
	}
}

// TestAddBlockerFollowOnDispatch_RefusesWhenInflightParent (BACI-217)
// — when the issue is both blocked AND has an in-flight parent
// dispatch, the parent-acks variant is the right fit. The client
// wrapper's branch ordering enforces this; the boundary defence here
// keeps the store from accepting a second variant on a single issue.
func TestAddBlockerFollowOnDispatch_RefusesWhenInflightParent(t *testing.T) {
	s, repo, blocked, _ := seedBlockerFixture(t)
	_, ag, _ := bestEffortAgentSession(t, s, repo)
	parent, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		IssueID:       &blocked.ID,
		Mode:          model.DispatchModePlan,
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("seed parent dispatch: %v", err)
	}
	_ = parent
	if _, err := s.AddBlockerFollowOnDispatch(repo.ID, blocked.ID, model.DispatchModeImplement, "supervisor"); err == nil {
		t.Fatal("expected in-flight parent to be rejected, got nil")
	}
}

// bestEffortAgentSession re-resolves the seed fixture's agent + session
// from the store (seedBlockerFixture inherits seedDispatchFixture's
// agent+session pair). Returns (store, agent, session) — keeps the
// test scaffolding minimal. requireNew is false so a returning lookup
// against the pre-seeded slug is a no-op refresh rather than a clash.
func bestEffortAgentSession(t *testing.T, s *Store, repo *model.Repo) (*Store, *model.Agent, *model.AgentSession) {
	t.Helper()
	ag, _, err := s.UpsertAgent("swift-otter@claude.test", false)
	if err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	// Reuse the seed's session id ("sess-dispatch-1") so we don't
	// stand up a second session row. UpsertAgentSession is idempotent
	// on session_id, so this resolves the existing row.
	sess, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-dispatch-1", RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
	})
	if err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	return s, ag, sess
}

// TestAddBlockerFollowOnDispatch_SingleSlot (BACI-217) — the
// single-slot invariant is across BOTH variants: a pre-existing
// parent-acks follow-on blocks a blockers-clear queue on the same
// issue, and vice versa.
func TestAddBlockerFollowOnDispatch_SingleSlot(t *testing.T) {
	s, repo, blocked, _ := seedBlockerFixture(t)
	if _, err := s.AddBlockerFollowOnDispatch(repo.ID, blocked.ID, model.DispatchModePlan, "supervisor"); err != nil {
		t.Fatalf("first blocker follow-on: %v", err)
	}
	if _, err := s.AddBlockerFollowOnDispatch(repo.ID, blocked.ID, model.DispatchModeImplement, "supervisor"); err == nil {
		t.Fatal("expected second blockers-clear follow-on to be rejected, got nil")
	}
}

// TestPromoteReadyFollowOns_BlockerVariantFires (BACI-217) — when every
// blocker is `done` the promote sweep clears the blockers-clear flag
// so the matcher binds the row on its next tick. BACI-255: the row's
// own status (queued, no dormant gate) is what surfaces it through
// WaitingDispatchForIssue — the promoted row is now the card's
// "waiting" dispatch, so the kanban spinner lights up on the next
// reload.
func TestPromoteReadyFollowOns_BlockerVariantFires(t *testing.T) {
	s, repo, blocked, blocker := seedBlockerFixture(t)
	follow, err := s.AddBlockerFollowOnDispatch(repo.ID, blocked.ID, model.DispatchModePlan, "supervisor")
	if err != nil {
		t.Fatalf("AddBlockerFollowOnDispatch: %v", err)
	}
	// Pre-clear sweep — blocker still open, row stays dormant.
	promoted, err := s.PromoteReadyFollowOns()
	if err != nil {
		t.Fatalf("promote (pre-clear): %v", err)
	}
	if len(promoted) != 0 {
		t.Fatalf("with open blocker: promoted=%d, want 0", len(promoted))
	}
	if err := s.SetIssueState(blocker.ID, model.StateDone); err != nil {
		t.Fatalf("close blocker: %v", err)
	}
	promoted, err = s.PromoteReadyFollowOns()
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if len(promoted) != 1 {
		t.Fatalf("promoted=%d, want 1", len(promoted))
	}
	if promoted[0].ID != follow.ID {
		t.Fatalf("promoted id = %d, want %d", promoted[0].ID, follow.ID)
	}
	if promoted[0].QueuedUntilBlockersClear {
		t.Fatal("promoted row still carries queued_until_blockers_clear = 1")
	}
	if promoted[0].QueuedAfterDispatchID != nil {
		t.Fatalf("promoted row still has queued_after_dispatch_id = %v", promoted[0].QueuedAfterDispatchID)
	}
	// Matcher predicate now sees the row.
	modes, err := s.ListQueuedModesByRepo(repo.ID)
	if err != nil {
		t.Fatalf("list modes: %v", err)
	}
	if len(modes) != 1 || modes[0] != model.DispatchModePlan {
		t.Fatalf("post-promote modes = %v, want [plan]", modes)
	}
	// BACI-255: the promoted row is the issue's active dispatch — the
	// spinner on the card lights up once the matcher is about to bind.
	wd, err := s.WaitingDispatchForIssue(repo.ID, blocked.ID)
	if err != nil {
		t.Fatalf("WaitingDispatchForIssue: %v", err)
	}
	if wd == nil {
		t.Fatal("WaitingDispatchForIssue = nil after promote; the promoted row should now satisfy the predicate")
	}
	if wd.ID != follow.ID {
		t.Fatalf("WaitingDispatchForIssue id = %d, want %d (the promoted follow-on)", wd.ID, follow.ID)
	}
}

// TestPromoteReadyFollowOns_BlockerVariantCancelCountsAsClear (BACI-217)
// — a `cancelled` blocker counts as cleared for the gate (the user has
// explicitly said the work is no longer pending).
func TestPromoteReadyFollowOns_BlockerVariantCancelCountsAsClear(t *testing.T) {
	s, repo, blocked, blocker := seedBlockerFixture(t)
	if _, err := s.AddBlockerFollowOnDispatch(repo.ID, blocked.ID, model.DispatchModePlan, "supervisor"); err != nil {
		t.Fatalf("AddBlockerFollowOnDispatch: %v", err)
	}
	if err := s.SetIssueState(blocker.ID, model.StateCancelled); err != nil {
		t.Fatalf("cancel blocker: %v", err)
	}
	promoted, err := s.PromoteReadyFollowOns()
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if len(promoted) != 1 {
		t.Fatalf("promoted=%d, want 1 (cancelled counts as cleared)", len(promoted))
	}
}

// TestPromoteReadyFollowOns_BlockerVariantWaitsForNewBlocker (BACI-217)
// — a `blocks` edge added after queue time must extend the wait. The
// sweep re-reads live blockers every tick, so an INSERT of a second
// open blocker mid-flight keeps the row dormant even after the first
// blocker closes.
func TestPromoteReadyFollowOns_BlockerVariantWaitsForNewBlocker(t *testing.T) {
	s, repo, blocked, blocker1 := seedBlockerFixture(t)
	if _, err := s.AddBlockerFollowOnDispatch(repo.ID, blocked.ID, model.DispatchModePlan, "supervisor"); err != nil {
		t.Fatalf("AddBlockerFollowOnDispatch: %v", err)
	}
	// Add a second open blocker post-queue.
	blocker2, err := s.CreateIssue(repo.ID, nil, "second blocker", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create second blocker: %v", err)
	}
	if err := s.CreateRelation(blocker2.ID, blocked.ID, model.RelBlocks); err != nil {
		t.Fatalf("create second blocks edge: %v", err)
	}
	// Close blocker1 — blocker2 still open, the gate must stay.
	if err := s.SetIssueState(blocker1.ID, model.StateDone); err != nil {
		t.Fatalf("close blocker1: %v", err)
	}
	promoted, err := s.PromoteReadyFollowOns()
	if err != nil {
		t.Fatalf("promote (one still open): %v", err)
	}
	if len(promoted) != 0 {
		t.Fatalf("with open blocker2: promoted=%d, want 0", len(promoted))
	}
	// Close blocker2 — gate clears.
	if err := s.SetIssueState(blocker2.ID, model.StateDone); err != nil {
		t.Fatalf("close blocker2: %v", err)
	}
	promoted, err = s.PromoteReadyFollowOns()
	if err != nil {
		t.Fatalf("promote (all clear): %v", err)
	}
	if len(promoted) != 1 {
		t.Fatalf("promoted=%d, want 1", len(promoted))
	}
}

// TestCancelOrphanedFollowOns_BlockerVariantOnTerminalIssue (BACI-217)
// — when the blocked issue itself lands in a terminal state, the
// orphan-cancel sweep drops the blockers-clear dormant row alongside
// the existing parent-acks variant.
func TestCancelOrphanedFollowOns_BlockerVariantOnTerminalIssue(t *testing.T) {
	s, repo, blocked, _ := seedBlockerFixture(t)
	follow, err := s.AddBlockerFollowOnDispatch(repo.ID, blocked.ID, model.DispatchModePlan, "supervisor")
	if err != nil {
		t.Fatalf("AddBlockerFollowOnDispatch: %v", err)
	}
	if err := s.SetIssueState(blocked.ID, model.StateDone); err != nil {
		t.Fatalf("close blocked issue: %v", err)
	}
	cancelled, err := s.CancelOrphanedFollowOns()
	if err != nil {
		t.Fatalf("orphan-cancel: %v", err)
	}
	if len(cancelled) != 1 || cancelled[0].ID != follow.ID {
		t.Fatalf("cancelled = %v, want [%d]", cancelled, follow.ID)
	}
	if cancelled[0].Status != model.DispatchCancelled {
		t.Fatalf("status = %q, want cancelled", cancelled[0].Status)
	}
}

// TestPromoteReadyFollowOns_BlockerVariantOpenClaimDefers (BACI-217) —
// even when every blocker is cleared, an open claim held by a live
// session on the blocked issue defers the promote: the existing
// race-guard in PromoteReadyFollowOns excludes the row from both
// variants' selection.
func TestPromoteReadyFollowOns_BlockerVariantOpenClaimDefers(t *testing.T) {
	s, repo, blocked, blocker := seedBlockerFixture(t)
	if _, err := s.AddBlockerFollowOnDispatch(repo.ID, blocked.ID, model.DispatchModePlan, "supervisor"); err != nil {
		t.Fatalf("AddBlockerFollowOnDispatch: %v", err)
	}
	if err := s.SetIssueState(blocker.ID, model.StateDone); err != nil {
		t.Fatalf("close blocker: %v", err)
	}
	// Stand up a live session + an open claim on the blocked issue.
	_, ag, sess := bestEffortAgentSession(t, s, repo)
	_ = ag
	if _, _, _, _, err := s.AddAgentClaim(sess.SessionID, blocked.ID, "review"); err != nil {
		t.Fatalf("add agent claim: %v", err)
	}
	promoted, err := s.PromoteReadyFollowOns()
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if len(promoted) != 0 {
		t.Fatalf("with open claim: promoted=%d, want 0", len(promoted))
	}
}

// TestPromoteReadyFollowOns_BlockersInProgressStaysDormant (BACI-246)
// — a blockers-clear follow-on whose only blocker is stuck at
// `in_progress` must NOT promote, no matter how many times the sweep
// runs. Locks in the gate's "non-terminal blocker = still dormant"
// semantics against any future regression that broadens the gate.
func TestPromoteReadyFollowOns_BlockersInProgressStaysDormant(t *testing.T) {
	s, repo, blocked, blocker := seedBlockerFixture(t)
	follow, err := s.AddBlockerFollowOnDispatch(repo.ID, blocked.ID, model.DispatchModePlan, "supervisor")
	if err != nil {
		t.Fatalf("AddBlockerFollowOnDispatch: %v", err)
	}
	// Move blocker to in_progress — definitively non-terminal.
	if err := s.SetIssueState(blocker.ID, model.StateInProgress); err != nil {
		t.Fatalf("set blocker in_progress: %v", err)
	}
	for i := 0; i < 10; i++ {
		promoted, err := s.PromoteReadyFollowOns()
		if err != nil {
			t.Fatalf("promote (iter %d): %v", i, err)
		}
		if len(promoted) != 0 {
			t.Fatalf("iter %d: promoted=%d, want 0 (blocker still in_progress)", i, len(promoted))
		}
	}
	// Row is unchanged — still queued, still dormant.
	d, err := s.GetDispatch(follow.ID)
	if err != nil {
		t.Fatalf("reload follow: %v", err)
	}
	if d.Status != model.DispatchQueued {
		t.Fatalf("status = %q, want queued", d.Status)
	}
	if !d.QueuedUntilBlockersClear {
		t.Fatal("queued_until_blockers_clear cleared while blocker still in_progress")
	}
}

// TestPromoteReadyFollowOns_StampsBlockerSnapshot (BACI-246) — when a
// blockers-clear follow-on promotes, the returned row carries a
// BlockerSnapshot with one entry per blocker that the gate observed
// at fire time. A diagnostic for "which blockers did the gate
// consider cleared?" via `bacio history --op agent.followon.promote
// -o json`.
func TestPromoteReadyFollowOns_StampsBlockerSnapshot(t *testing.T) {
	s, repo, blocked, blocker1 := seedBlockerFixture(t)
	follow, err := s.AddBlockerFollowOnDispatch(repo.ID, blocked.ID, model.DispatchModePlan, "supervisor")
	if err != nil {
		t.Fatalf("AddBlockerFollowOnDispatch: %v", err)
	}
	// Add two more blockers (one done, one cancelled) so the gate
	// sees three rows total at fire time.
	blocker2, err := s.CreateIssue(repo.ID, nil, "second blocker", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create second blocker: %v", err)
	}
	if err := s.CreateRelation(blocker2.ID, blocked.ID, model.RelBlocks); err != nil {
		t.Fatalf("create blocks edge 2: %v", err)
	}
	blocker3, err := s.CreateIssue(repo.ID, nil, "third blocker", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create third blocker: %v", err)
	}
	if err := s.CreateRelation(blocker3.ID, blocked.ID, model.RelBlocks); err != nil {
		t.Fatalf("create blocks edge 3: %v", err)
	}
	// Close every blocker — gate clears.
	if err := s.SetIssueState(blocker1.ID, model.StateDone); err != nil {
		t.Fatalf("close blocker1: %v", err)
	}
	if err := s.SetIssueState(blocker2.ID, model.StateDone); err != nil {
		t.Fatalf("close blocker2: %v", err)
	}
	if err := s.SetIssueState(blocker3.ID, model.StateCancelled); err != nil {
		t.Fatalf("cancel blocker3: %v", err)
	}
	promoted, err := s.PromoteReadyFollowOns()
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if len(promoted) != 1 || promoted[0].ID != follow.ID {
		t.Fatalf("promoted = %v, want one entry for %d", promoted, follow.ID)
	}
	snap := promoted[0].BlockerSnapshot
	if len(snap) != 3 {
		t.Fatalf("snapshot len = %d, want 3 — entries: %+v", len(snap), snap)
	}
	// Build a lookup of (key → state) for shape-independent assertions
	// (the SELECT's row order is not guaranteed).
	got := map[string]model.State{}
	for _, o := range snap {
		got[o.BlockerKey] = o.BlockerState
	}
	if got[blocker1.Key] != model.StateDone {
		t.Errorf("blocker1 %q state = %q, want done", blocker1.Key, got[blocker1.Key])
	}
	if got[blocker2.Key] != model.StateDone {
		t.Errorf("blocker2 %q state = %q, want done", blocker2.Key, got[blocker2.Key])
	}
	if got[blocker3.Key] != model.StateCancelled {
		t.Errorf("blocker3 %q state = %q, want cancelled", blocker3.Key, got[blocker3.Key])
	}
}

// TestPromoteReadyFollowOns_NoBlockerRowsRecorded (BACI-246) — a
// blockers-clear follow-on whose `blocks` relation rows were
// hard-deleted between queue and promote still gets promoted (the
// EXISTS subquery returns empty, so the gate is "clear" by the
// store's definition). The audit-row enrichment carries an EMPTY
// BlockerSnapshot, not a nil — that's the forensic signal "the
// relation rows were gone at fire time".
func TestPromoteReadyFollowOns_NoBlockerRowsRecorded(t *testing.T) {
	s, repo, blocked, blocker := seedBlockerFixture(t)
	follow, err := s.AddBlockerFollowOnDispatch(repo.ID, blocked.ID, model.DispatchModePlan, "supervisor")
	if err != nil {
		t.Fatalf("AddBlockerFollowOnDispatch: %v", err)
	}
	// Drop the only `blocks` edge — leaves the dispatch's flag set
	// but no rows for the EXISTS to find.
	if _, err := s.DeleteRelation(blocker.ID, blocked.ID); err != nil {
		t.Fatalf("delete relation: %v", err)
	}
	promoted, err := s.PromoteReadyFollowOns()
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if len(promoted) != 1 {
		t.Fatalf("promoted=%d, want 1", len(promoted))
	}
	if promoted[0].ID != follow.ID {
		t.Fatalf("promoted id = %d, want %d", promoted[0].ID, follow.ID)
	}
	// The BlockerSnapshot is a non-nil empty slice — the "I checked
	// and there were no blocker rows" forensic signal.
	if promoted[0].BlockerSnapshot == nil {
		t.Fatal("BlockerSnapshot is nil; want non-nil empty slice for blockers-clear variant with no rows")
	}
	if len(promoted[0].BlockerSnapshot) != 0 {
		t.Fatalf("BlockerSnapshot len = %d, want 0 — entries: %+v", len(promoted[0].BlockerSnapshot), promoted[0].BlockerSnapshot)
	}
}

// TestBindQueuedDispatch_RejectsReDormantRow (BACI-246) — the bind-time
// belt-and-braces gate. If a dispatch row was promoted (queued, no
// dormant flags) but a follow-on with a still-open blocker would also
// match a naive `status='queued' AND delivered_at IS NULL` CAS, the
// CAS must miss. Simulates the stale-matcher-snapshot race the
// promote sweep alone can't close.
func TestBindQueuedDispatch_RejectsReDormantRow(t *testing.T) {
	s, repo, blocked, blocker := seedBlockerFixture(t)
	// Insert a blockers-clear follow-on with the gate STILL OPEN
	// (blocker is `todo`, the seed's default).
	follow, err := s.AddBlockerFollowOnDispatch(repo.ID, blocked.ID, model.DispatchModePlan, "supervisor")
	if err != nil {
		t.Fatalf("AddBlockerFollowOnDispatch: %v", err)
	}
	// Need a target agent for the CAS — the row carries no target until
	// the matcher binds it.
	ag, _, err := s.UpsertAgent("vigilant-vole@claude.test", true)
	if err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	// First call: blocker is still open → CAS must miss with ErrNotFound,
	// row stays queued + dormant.
	if _, err := s.BindQueuedDispatch(follow.ID, ag.ID); !errIsNotFound(err) {
		t.Fatalf("bind with open blocker: err = %v, want ErrNotFound", err)
	}
	d, err := s.GetDispatch(follow.ID)
	if err != nil {
		t.Fatalf("reload after first bind: %v", err)
	}
	if d.Status != model.DispatchQueued {
		t.Fatalf("after rejected bind: status = %q, want queued", d.Status)
	}
	if !d.QueuedUntilBlockersClear {
		t.Fatal("after rejected bind: queued_until_blockers_clear cleared")
	}
	// Close the blocker → gate clears → CAS now succeeds.
	if err := s.SetIssueState(blocker.ID, model.StateDone); err != nil {
		t.Fatalf("close blocker: %v", err)
	}
	bound, err := s.BindQueuedDispatch(follow.ID, ag.ID)
	if err != nil {
		t.Fatalf("bind with cleared gate: %v", err)
	}
	if bound.Status != model.DispatchPending {
		t.Fatalf("post-bind status = %q, want pending", bound.Status)
	}
}

// errIsNotFound is a small helper to keep the test's intent legible
// (we don't import errors just for one Is() call).
func errIsNotFound(err error) bool {
	return err == ErrNotFound
}
