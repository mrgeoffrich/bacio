package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// priorityOf reads back the persisted priority for an issue id.
func priorityOf(t *testing.T, s *Store, id int64) int {
	t.Helper()
	iss, err := s.GetIssueByID(id)
	if err != nil {
		t.Fatalf("get issue %d: %v", id, err)
	}
	return iss.Priority
}

// TestSetIssueState_ToBeShippedAppendsToBack — BACI-275: cards entering
// to_be_shipped queue behind whatever is already there. Three arrivals
// get priorities 0,1,2 and TopShippingIssue returns the first arrival.
func TestSetIssueState_ToBeShippedAppendsToBack(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("TST", "test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	first, _ := s.CreateIssue(repo.ID, nil, "first", "", model.StateTodo, nil, "")
	second, _ := s.CreateIssue(repo.ID, nil, "second", "", model.StateTodo, nil, "")
	third, _ := s.CreateIssue(repo.ID, nil, "third", "", model.StateTodo, nil, "")

	for _, iss := range []*model.Issue{first, second, third} {
		if err := s.SetIssueState(iss.ID, model.StateToBeShipped); err != nil {
			t.Fatalf("ship %s: %v", iss.Title, err)
		}
	}

	if got := priorityOf(t, s, first.ID); got != 0 {
		t.Fatalf("first priority = %d, want 0", got)
	}
	if got := priorityOf(t, s, second.ID); got != 1 {
		t.Fatalf("second priority = %d, want 1", got)
	}
	if got := priorityOf(t, s, third.ID); got != 2 {
		t.Fatalf("third priority = %d, want 2", got)
	}

	top, err := s.TopShippingIssue(repo.ID)
	if err != nil {
		t.Fatalf("top shipping: %v", err)
	}
	if top == nil || top.ID != first.ID {
		t.Fatalf("top shipping = %v, want first (%d)", top, first.ID)
	}
}

// TestSetIssueState_ToBeShippedLowerNumberArrivesLast — the regression
// the ticket describes: a lower-numbered card arriving last must still
// sort behind the older arrivals, not jump the queue on the number
// tiebreak. We force the low number to arrive last by shipping a
// higher-numbered card first.
func TestSetIssueState_ToBeShippedLowerNumberArrivesLast(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("TST", "test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	// low has the smaller number but ships last.
	low, _ := s.CreateIssue(repo.ID, nil, "low", "", model.StateTodo, nil, "")
	high, _ := s.CreateIssue(repo.ID, nil, "high", "", model.StateTodo, nil, "")

	if err := s.SetIssueState(high.ID, model.StateToBeShipped); err != nil {
		t.Fatalf("ship high: %v", err)
	}
	if err := s.SetIssueState(low.ID, model.StateToBeShipped); err != nil {
		t.Fatalf("ship low: %v", err)
	}

	if got := priorityOf(t, s, high.ID); got != 0 {
		t.Fatalf("high priority = %d, want 0 (arrived first)", got)
	}
	if got := priorityOf(t, s, low.ID); got != 1 {
		t.Fatalf("low priority = %d, want 1 (arrived last)", got)
	}

	top, err := s.TopShippingIssue(repo.ID)
	if err != nil {
		t.Fatalf("top shipping: %v", err)
	}
	if top == nil || top.ID != high.ID {
		t.Fatalf("top shipping = %v, want high (%d) — lower number must not jump", top, high.ID)
	}
}

// TestSetIssueState_ReassertToBeShippedKeepsPriority — re-asserting
// to_be_shipped on an already-queued card (the engine's idempotent
// no-op hand-off) must not re-shuffle it to the back.
func TestSetIssueState_ReassertToBeShippedKeepsPriority(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("TST", "test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	first, _ := s.CreateIssue(repo.ID, nil, "first", "", model.StateTodo, nil, "")
	second, _ := s.CreateIssue(repo.ID, nil, "second", "", model.StateTodo, nil, "")

	if err := s.SetIssueState(first.ID, model.StateToBeShipped); err != nil {
		t.Fatalf("ship first: %v", err)
	}
	if err := s.SetIssueState(second.ID, model.StateToBeShipped); err != nil {
		t.Fatalf("ship second: %v", err)
	}

	// Re-assert to_be_shipped on the head card — priority must stay 0.
	if err := s.SetIssueState(first.ID, model.StateToBeShipped); err != nil {
		t.Fatalf("re-ship first: %v", err)
	}

	if got := priorityOf(t, s, first.ID); got != 0 {
		t.Fatalf("first priority after re-assert = %d, want 0 (unchanged)", got)
	}
	if got := priorityOf(t, s, second.ID); got != 1 {
		t.Fatalf("second priority = %d, want 1", got)
	}
}
