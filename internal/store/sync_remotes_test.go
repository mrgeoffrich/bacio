package store

import (
	"testing"
)

// TestListSyncRemotes_Empty locks in the empty-table contract: returns
// an empty slice (not nil), nil error. The non-nil slice matters for
// JSON encoders that prefer `[]` over `null`.
func TestListSyncRemotes_Empty(t *testing.T) {
	s := newTestStore(t)

	got, err := s.ListSyncRemotes()
	if err != nil {
		t.Fatalf("ListSyncRemotes: %v", err)
	}
	if got == nil {
		t.Fatal("ListSyncRemotes empty result = nil, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("ListSyncRemotes empty result len = %d, want 0", len(got))
	}
}

// TestListSyncRemotes_MultipleSortedByURL inserts two rows out of order
// and asserts the helper returns them sorted by remote_url ASC. Pins
// the deterministic ordering contract callers (CLI / UI renderers) lean
// on.
func TestListSyncRemotes_MultipleSortedByURL(t *testing.T) {
	s := newTestStore(t)

	// Inserted in non-alphabetical order so the test fails if the
	// underlying query drops the ORDER BY.
	const (
		urlZ = "git@github.com:zed/sync.git"
		urlA = "git@github.com:alpha/sync.git"
	)
	if err := s.UpsertSyncRemote(urlZ, "/local/zed"); err != nil {
		t.Fatalf("UpsertSyncRemote zed: %v", err)
	}
	if err := s.UpsertSyncRemote(urlA, "/local/alpha"); err != nil {
		t.Fatalf("UpsertSyncRemote alpha: %v", err)
	}

	got, err := s.ListSyncRemotes()
	if err != nil {
		t.Fatalf("ListSyncRemotes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListSyncRemotes len = %d, want 2", len(got))
	}
	if got[0].RemoteURL != urlA || got[0].LocalPath != "/local/alpha" {
		t.Errorf("got[0] = {%q, %q}, want {%q, /local/alpha}", got[0].RemoteURL, got[0].LocalPath, urlA)
	}
	if got[1].RemoteURL != urlZ || got[1].LocalPath != "/local/zed" {
		t.Errorf("got[1] = {%q, %q}, want {%q, /local/zed}", got[1].RemoteURL, got[1].LocalPath, urlZ)
	}
}

// TestListSyncRemotes_PointersRoundTrip exercises the optional
// LastSyncAt / LastSyncError columns through the List path: after a
// MarkSyncFailed + MarkSyncCompleted dance, the listed row must carry
// the same non-nil pointers GetSyncRemote returns.
func TestListSyncRemotes_PointersRoundTrip(t *testing.T) {
	s := newTestStore(t)

	const remote = "git@github.com:user/sync.git"
	if err := s.UpsertSyncRemote(remote, "/local/sync"); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}
	if err := s.MarkSyncFailed(remote, "git push: rejected"); err != nil {
		t.Fatalf("MarkSyncFailed: %v", err)
	}

	got, err := s.ListSyncRemotes()
	if err != nil {
		t.Fatalf("ListSyncRemotes: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].LastSyncError == nil || *got[0].LastSyncError != "git push: rejected" {
		t.Errorf("after MarkSyncFailed, LastSyncError = %v, want the message", got[0].LastSyncError)
	}
	if got[0].LastSyncAt != nil {
		t.Error("MarkSyncFailed must not stamp last_sync_at")
	}

	if err := s.MarkSyncCompleted(remote); err != nil {
		t.Fatalf("MarkSyncCompleted: %v", err)
	}
	got, _ = s.ListSyncRemotes()
	if got[0].LastSyncAt == nil {
		t.Error("after MarkSyncCompleted, LastSyncAt = nil, want non-nil")
	}
	if got[0].LastSyncError != nil {
		t.Errorf("after MarkSyncCompleted, LastSyncError = %v, want nil", got[0].LastSyncError)
	}
}
