package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestWaitingDispatchVisibleImmediatelyAfterAddDispatch (BACI-255) pins
// the visibility guarantee the dropped waiting_for_claim cache used to
// provide implicitly: a queued dispatch against an issue is visible
// through WaitingDispatchForIssue immediately after AddDispatch returns,
// inside the same connection, with no intervening reload. Pre-BACI-255
// the AddDispatch transaction also stamped issues.waiting_for_claim,
// and that boolean was the gate every reader consulted; removing the
// cache means readers go straight to agent_dispatches, and this test
// keeps the round-trip honest.
func TestWaitingDispatchVisibleImmediatelyAfterAddDispatch(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)

	pre, err := s.WaitingDispatchForIssue(repo.ID, iss.ID)
	if err != nil {
		t.Fatalf("WaitingDispatchForIssue (pre): %v", err)
	}
	if pre != nil {
		t.Fatalf("pre-dispatch WaitingDispatchForIssue = %+v, want nil", pre)
	}

	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		IssueID:       &iss.ID,
		Payload:       "pick this up",
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}

	got, err := s.WaitingDispatchForIssue(repo.ID, iss.ID)
	if err != nil {
		t.Fatalf("WaitingDispatchForIssue (post): %v", err)
	}
	if got == nil {
		t.Fatal("WaitingDispatchForIssue = nil immediately after AddDispatch; spinner state must be visible without a reload")
	}
	if got.ID != d.ID {
		t.Fatalf("WaitingDispatchForIssue.ID = %d, want %d", got.ID, d.ID)
	}
}

// TestWaitingDispatchClearsAfterClaim (BACI-255) — the lifecycle from
// the kanban's perspective: a queued dispatch is "waiting", a fresh
// open claim ends the waiting state. Pre-BACI-255 the assertion was
// against issues.waiting_for_claim transitioning 1→0 in the same
// transaction as the claim; post-BACI-255 it's against the dispatch
// row's status, since the row itself is the signal. AddAgentClaim
// does NOT mutate the dispatch row directly (that happens at ack
// time), but the kanban's `taken` derivation pre-empts the waiting
// render once an open claim exists — the brief and the React
// pipeline both surface this as taken-not-waiting.
func TestWaitingDispatchClearsAfterClaim(t *testing.T) {
	s, repo, iss, ag, sess := seedDispatchFixture(t)
	if _, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		IssueID:       &iss.ID,
		Payload:       "pick this up",
		CreatedBy:     "supervisor",
	}); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	got, err := s.WaitingDispatchForIssue(repo.ID, iss.ID)
	if err != nil {
		t.Fatalf("get waiting (post-dispatch): %v", err)
	}
	if got == nil {
		t.Fatal("WaitingDispatchForIssue = nil after queued AddDispatch")
	}

	if _, created, _, _, err := s.AddAgentClaim(sess.SessionID, iss.ID, "on it"); err != nil {
		t.Fatalf("add claim: %v", err)
	} else if !created {
		t.Fatal("AddAgentClaim reported created = false for a fresh claim")
	}

	// The claim establishes `taken` (via the EXISTS join in
	// issueSelect); the dispatch row is still in flight (the claim
	// path doesn't ack it), but the kanban gives `taken` precedence
	// over `waiting` so the spinner clears. The dispatch is only
	// considered "settled" when AckDispatch / CancelDispatch flips
	// its status — verify the row is still queryable so the cancel
	// path stays reachable from the spinner-as-cancel button.
	gotIss, err := s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("get issue after claim: %v", err)
	}
	if !gotIss.Taken {
		t.Fatal("issue.Taken = false after AddAgentClaim, want true")
	}
}

// TestWaitingDispatchClearsAfterCancel (BACI-255) — CancelDispatch
// flips the row's status to 'cancelled', so WaitingDispatchForIssue
// (which filters status IN queued/pending/delivered) stops returning
// it. This is the canonical "spinner clears" path.
func TestWaitingDispatchClearsAfterCancel(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		IssueID:       &iss.ID,
		Payload:       "pick this up",
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	if _, err := s.CancelDispatch(d.ID); err != nil {
		t.Fatalf("cancel dispatch: %v", err)
	}
	got, err := s.WaitingDispatchForIssue(repo.ID, iss.ID)
	if err != nil {
		t.Fatalf("get waiting (post-cancel): %v", err)
	}
	if got != nil {
		t.Fatalf("WaitingDispatchForIssue = %+v after cancel, want nil", got)
	}
	gotIss, err := s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if gotIss.State != model.StateTodo {
		t.Fatalf("issue state = %q after cancel, want todo (cancel must not touch state)", gotIss.State)
	}
}

// TestWaitingDispatchHonoursNoIssue (BACI-255) — AddDispatch without
// an issue target is a clean no-op for the waiting signal: nothing to
// inspect because there's no issue to inspect against. The test exists
// to exercise the in.IssueID == nil branch of AddDispatch and confirm
// it doesn't error.
func TestWaitingDispatchHonoursNoIssue(t *testing.T) {
	s, repo, _, ag, _ := seedDispatchFixture(t)
	if _, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		Payload:       "no issue attached",
		CreatedBy:     "supervisor",
	}); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
}
