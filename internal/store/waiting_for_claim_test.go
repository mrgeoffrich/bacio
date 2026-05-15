package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestWaitingForClaimDefaultsFalse locks in that a freshly created issue
// has waiting_for_claim = false and that the column round-trips through
// the read paths.
func TestWaitingForClaimDefaultsFalse(t *testing.T) {
	s, _, iss := seedRepoAndIssue(t)
	if iss.WaitingForClaim {
		t.Fatal("freshly created issue has waiting_for_claim = true, want false")
	}
	got, err := s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if got.WaitingForClaim {
		t.Fatal("read path reports waiting_for_claim = true on a fresh issue")
	}
}

// TestSetWaitingForClaimRoundTrips checks the explicit setter flips the
// flag both ways and that ListIssues reflects it.
func TestSetWaitingForClaimRoundTrips(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	if err := s.SetWaitingForClaim(iss.ID, true); err != nil {
		t.Fatalf("set true: %v", err)
	}
	got, err := s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if !got.WaitingForClaim {
		t.Fatal("waiting_for_claim = false after SetWaitingForClaim(true)")
	}
	list, err := s.ListIssues(IssueFilter{RepoID: &repo.ID})
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	if len(list) != 1 || !list[0].WaitingForClaim {
		t.Fatalf("ListIssues did not reflect waiting_for_claim: %+v", list)
	}
	if err := s.SetWaitingForClaim(iss.ID, false); err != nil {
		t.Fatalf("set false: %v", err)
	}
	got, err = s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if got.WaitingForClaim {
		t.Fatal("waiting_for_claim = true after SetWaitingForClaim(false)")
	}
}

// TestAddDispatchSetsWaitingForClaim locks in that dispatching against a
// concrete issue flips its waiting_for_claim flag — the dispatch→claim
// gap this feature closes.
func TestAddDispatchSetsWaitingForClaim(t *testing.T) {
	s, repo, iss, ag, _ := seedDispatchFixture(t)
	if _, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		IssueID:       &iss.ID,
		Payload:       "pick this up",
		CreatedBy:     "supervisor",
	}); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	got, err := s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if !got.WaitingForClaim {
		t.Fatal("waiting_for_claim = false after AddDispatch against the issue")
	}
}

// TestAddDispatchNoIssueLeavesFlagAlone checks that a dispatch with no
// issue target is a clean no-op for waiting_for_claim — no panic, no
// stray update.
func TestAddDispatchNoIssueLeavesFlagAlone(t *testing.T) {
	s, repo, _, ag, _ := seedDispatchFixture(t)
	if _, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		Payload:       "no issue attached",
		CreatedBy:     "supervisor",
	}); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	// Nothing to assert beyond "it didn't error / panic" — there is no
	// issue to inspect. The test exists to exercise the in.IssueID == nil
	// branch of AddDispatch.
}

// TestAddAgentClaimClearsWaitingForClaim locks in the core lifecycle:
// dispatch sets the flag, a fresh open claim clears it.
func TestAddAgentClaimClearsWaitingForClaim(t *testing.T) {
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
	got, err := s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("get issue after dispatch: %v", err)
	}
	if !got.WaitingForClaim {
		t.Fatal("waiting_for_claim = false after dispatch, want true")
	}

	if _, created, err := s.AddAgentClaim(sess.SessionID, iss.ID, "on it"); err != nil {
		t.Fatalf("add claim: %v", err)
	} else if !created {
		t.Fatal("AddAgentClaim reported created = false for a fresh claim")
	}
	got, err = s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("get issue after claim: %v", err)
	}
	if got.WaitingForClaim {
		t.Fatal("waiting_for_claim = true after an agent claimed the issue")
	}
}

// TestNoopReclaimDoesNotTouchWaitingForClaim checks that a no-op
// re-claim (same session, same issue, already open) does not clear the
// flag — only the new-claim path does. Contrived but locks in the
// branch boundary.
func TestNoopReclaimDoesNotTouchWaitingForClaim(t *testing.T) {
	s, _, iss, _, sess := seedDispatchFixture(t)
	// First claim — the new-claim path; clears the flag (which is false
	// here anyway).
	if _, created, err := s.AddAgentClaim(sess.SessionID, iss.ID, "first"); err != nil {
		t.Fatalf("first claim: %v", err)
	} else if !created {
		t.Fatal("first claim reported created = false")
	}
	// Re-set the flag behind the claim, then re-claim: the no-op branch
	// must leave it alone.
	if err := s.SetWaitingForClaim(iss.ID, true); err != nil {
		t.Fatalf("set flag: %v", err)
	}
	if _, created, err := s.AddAgentClaim(sess.SessionID, iss.ID, "again"); err != nil {
		t.Fatalf("re-claim: %v", err)
	} else if created {
		t.Fatal("re-claim reported created = true, want false (no-op)")
	}
	got, err := s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if !got.WaitingForClaim {
		t.Fatal("no-op re-claim cleared waiting_for_claim; only the new-claim path should")
	}
}

// TestCancelDispatchClearsWaitingForClaim locks in that cancelling a
// dispatch clears the issue's flag — a cancelled dispatch is no longer
// "waiting".
func TestCancelDispatchClearsWaitingForClaim(t *testing.T) {
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
	got, err := s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if got.WaitingForClaim {
		t.Fatal("waiting_for_claim = true after the dispatch was cancelled")
	}
	if got.State != model.StateTodo {
		t.Fatalf("issue state = %q, want todo (cancel must not touch state)", got.State)
	}
}
