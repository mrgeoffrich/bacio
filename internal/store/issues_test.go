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
	feat, err := s.CreateFeature(repo.ID, "f", "F", "")
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
