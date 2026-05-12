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
