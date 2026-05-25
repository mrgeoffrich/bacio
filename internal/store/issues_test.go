package store

import (
	"path/filepath"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestCreateIssueCounterAtomicity locks in the invariant the importer
// agent stumbled over: a CreateIssue that fails at the DB level (here, by
// supplying a state the schema CHECK rejects) must NOT advance the repo's
// next_issue_number. Otherwise we'd burn issue keys on every failed create.
func TestCreateIssueCounterAtomicity(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("TST", "test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	// First create — counter goes 1 → 2.
	first, err := s.CreateIssue(repo.ID, nil, "first", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if first.Number != 1 {
		t.Fatalf("first issue number = %d, want 1", first.Number)
	}
	r, err := s.GetRepoByID(repo.ID)
	if err != nil {
		t.Fatalf("get repo: %v", err)
	}
	if r.NextIssueNumber != 2 {
		t.Fatalf("counter after first = %d, want 2", r.NextIssueNumber)
	}

	// Force a failure inside the CreateIssue transaction. The schema's
	// state CHECK constraint rejects anything outside the canonical set,
	// so this hits an error AFTER AllocateIssueNumber has run inside the
	// tx — exactly the path the importer agent hit. The counter must roll
	// back along with the failed insert.
	if _, err := s.CreateIssue(repo.ID, nil, "bad", "", model.State("bogus_state"), nil); err == nil {
		t.Fatal("expected CreateIssue to fail with invalid state")
	}
	r, err = s.GetRepoByID(repo.ID)
	if err != nil {
		t.Fatalf("get repo after fail: %v", err)
	}
	if r.NextIssueNumber != 2 {
		t.Fatalf("counter after failed create = %d, want 2 (rollback regressed!)", r.NextIssueNumber)
	}

	// Next valid create must be number 2 — no gap.
	second, err := s.CreateIssue(repo.ID, nil, "second", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if second.Number != 2 {
		t.Fatalf("second issue number = %d, want 2 (counter gap detected)", second.Number)
	}
}

// TestIssueUUIDRoundTrip pins the Phase 1 invariants for the new uuid
// column: every Create* path stamps a non-empty uuid, two creates
// produce different uuids, and the value round-trips through
// GetByID/GetByKey/GetByUUID. The test is deliberately small — Phase 1
// does not change any other behaviour.
func TestIssueUUIDRoundTrip(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("TST", "test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if repo.UUID == "" {
		t.Fatalf("repo uuid is empty")
	}

	first, err := s.CreateIssue(repo.ID, nil, "first", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	second, err := s.CreateIssue(repo.ID, nil, "second", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if first.UUID == "" || second.UUID == "" {
		t.Fatalf("issue uuid empty: first=%q second=%q", first.UUID, second.UUID)
	}
	if first.UUID == second.UUID {
		t.Fatalf("issue uuids collided: %q", first.UUID)
	}

	got, err := s.GetIssueByUUID(first.UUID)
	if err != nil {
		t.Fatalf("get by uuid: %v", err)
	}
	if got.ID != first.ID || got.UUID != first.UUID {
		t.Fatalf("uuid round-trip mismatch: got %+v want %+v", got, first)
	}
	gotByKey, err := s.GetIssueByKey("TST", first.Number)
	if err != nil {
		t.Fatalf("get by key: %v", err)
	}
	if gotByKey.UUID != first.UUID {
		t.Fatalf("get by key uuid mismatch: got %q want %q", gotByKey.UUID, first.UUID)
	}
}

// TestPeekClaimNextIssueSkipArchived — BACI-68 dispatcher guard. An
// archived todo must be invisible to both PeekNextIssue and
// ClaimNextIssue so the auto-pick / matcher / `bacio issue next`
// paths can't quietly revive work the user has hidden.
func TestPeekClaimNextIssueSkipArchived(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("AR", "archive-next", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	feat, err := s.CreateFeature(repo.ID, "f", "F", "", "")
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	// Two todos in the same feature, both eligible. AR-1 is the
	// lowest-numbered, so it would normally be picked first.
	iss1, err := s.CreateIssue(repo.ID, &feat.ID, "first", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create iss1: %v", err)
	}
	iss2, err := s.CreateIssue(repo.ID, &feat.ID, "second", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create iss2: %v", err)
	}
	// Sanity: with neither archived, Peek picks iss1.
	picked, err := s.PeekNextIssue(repo.ID, feat.ID)
	if err != nil || picked == nil || picked.ID != iss1.ID {
		t.Fatalf("pre-archive peek: got %+v err=%v, want iss1", picked, err)
	}
	// Archive iss1: Peek should now hand back iss2 instead of skipping
	// to nil. A manually-hidden todo must not block the next eligible
	// one underneath it.
	if err := s.SetIssueArchived(iss1.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	picked, err = s.PeekNextIssue(repo.ID, feat.ID)
	if err != nil || picked == nil || picked.ID != iss2.ID {
		t.Fatalf("post-archive peek: got %+v err=%v, want iss2", picked, err)
	}
	// Claim should also pick iss2, not the archived iss1.
	claimed, err := s.ClaimNextIssue(repo.ID, feat.ID, "geoff")
	if err != nil || claimed == nil || claimed.ID != iss2.ID {
		t.Fatalf("post-archive claim: got %+v err=%v, want iss2", claimed, err)
	}
}

// TestTerminalAtLifecycle — BACI-138. The denormalised terminal_at
// column on issues must follow the state transitions:
//
//   - CreateIssue in a non-terminal state → terminal_at is NULL.
//   - CreateIssue directly in a terminal state → terminal_at is set.
//   - SetIssueState(terminal) → terminal_at is set.
//   - SetIssueState(non-terminal) → terminal_at is cleared.
//   - Re-closing a row re-stamps terminal_at (a re-close beats an
//     old close on the board's newest-first sort).
//   - Non-state edits (tags, title/description, archive, assignee)
//     must NOT touch terminal_at. This is the BACI-138 bug: the
//     pre-BACI-138 sort used updated_at as a proxy and a stray tag
//     edit on a Done issue jumped the card to the top of the column.
func TestTerminalAtLifecycle(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("TER", "terminal", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	// Non-terminal create — terminal_at must stay NULL.
	open, err := s.CreateIssue(repo.ID, nil, "open issue", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create open: %v", err)
	}
	if open.TerminalAt != nil {
		t.Fatalf("fresh todo TerminalAt = %v, want nil", *open.TerminalAt)
	}

	// Direct-to-done create — terminal_at must be set.
	closed, err := s.CreateIssue(repo.ID, nil, "born done", "", model.StateDone, nil)
	if err != nil {
		t.Fatalf("create closed: %v", err)
	}
	if closed.TerminalAt == nil {
		t.Fatalf("CreateIssue(StateDone) TerminalAt = nil, want set")
	}
	firstClose := *closed.TerminalAt

	// Move open → done. terminal_at must populate.
	if err := s.SetIssueState(open.ID, model.StateDone); err != nil {
		t.Fatalf("set state done: %v", err)
	}
	got, _ := s.GetIssueByID(open.ID)
	if got.TerminalAt == nil {
		t.Fatal("after SetIssueState(StateDone), TerminalAt = nil, want set")
	}
	openClose := *got.TerminalAt

	// Move done → in_progress (reopen). terminal_at must clear.
	if err := s.SetIssueState(open.ID, model.StateInProgress); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, _ = s.GetIssueByID(open.ID)
	if got.TerminalAt != nil {
		t.Fatalf("after reopen, TerminalAt = %v, want nil", *got.TerminalAt)
	}

	// Re-close — terminal_at must re-stamp with a fresh value (and at
	// minimum not regress to the original close time). SQLite's
	// CURRENT_TIMESTAMP has 1-second granularity, so we only assert
	// that the timestamp is at least the original openClose value
	// (re-close happens later than the original close in wall-clock
	// time, so >= is the deterministic predicate).
	if err := s.SetIssueState(open.ID, model.StateCancelled); err != nil {
		t.Fatalf("re-close as cancelled: %v", err)
	}
	got, _ = s.GetIssueByID(open.ID)
	if got.TerminalAt == nil {
		t.Fatal("after re-close, TerminalAt = nil, want set")
	}
	if got.TerminalAt.Before(openClose) {
		t.Fatalf("re-close TerminalAt %v regressed before original %v", *got.TerminalAt, openClose)
	}

	// Non-state edits on the still-closed `closed` issue must NOT
	// touch terminal_at. This is the BACI-138 regression guard.
	beforeEdit, _ := s.GetIssueByID(closed.ID)
	if beforeEdit.TerminalAt == nil || !beforeEdit.TerminalAt.Equal(firstClose) {
		t.Fatalf("setup: closed TerminalAt drifted before edits: was %v, now %v",
			firstClose, beforeEdit.TerminalAt)
	}
	newTitle := "renamed"
	if err := s.UpdateIssue(closed.ID, &newTitle, nil, nil); err != nil {
		t.Fatalf("rename title: %v", err)
	}
	if err := s.AddTagsToIssue(closed.ID, []string{"ui"}); err != nil {
		t.Fatalf("add tag: %v", err)
	}
	if err := s.SetIssueAssignee(closed.ID, "geoff"); err != nil {
		t.Fatalf("set assignee: %v", err)
	}
	if err := s.SetIssueArchived(closed.ID, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	got, _ = s.GetIssueByID(closed.ID)
	if got.TerminalAt == nil || !got.TerminalAt.Equal(firstClose) {
		t.Fatalf("non-state edits drifted TerminalAt: was %v, now %v",
			firstClose, got.TerminalAt)
	}
}
