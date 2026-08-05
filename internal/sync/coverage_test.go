package sync

import (
	"errors"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// fakeCoverageStore is the in-memory stand-in for *store.Store —
// MirrorCoverage only needs ListSyncRemotes.
type fakeCoverageStore struct {
	remotes []*model.SyncRemote
	err     error
}

func (f *fakeCoverageStore) ListSyncRemotes() ([]*model.SyncRemote, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.remotes, nil
}

// TestMirrorCoverage_MapsEveryOnDiskPrefix: the export is whole-DB, so
// every prefix the clone carries is mirrored — including projects that
// have no sync config of their own. That's BACI-376's core claim.
func TestMirrorCoverage_MapsEveryOnDiskPrefix(t *testing.T) {
	root := mkSyncRepoLayout(t, "BACI", "OPER")
	last := time.Date(2026, 8, 5, 21, 27, 2, 0, time.UTC)
	s := &fakeCoverageStore{remotes: []*model.SyncRemote{{
		RemoteURL:  "https://github.com/mrgeoffrich/bacio-sync",
		LocalPath:  root,
		LastSyncAt: &last,
	}}}

	got, err := MirrorCoverage(s)
	if err != nil {
		t.Fatalf("MirrorCoverage: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("coverage = %v, want 2 entries", got)
	}
	oper, ok := got["OPER"]
	if !ok {
		t.Fatal("OPER not covered, want covered (its folder is in the clone)")
	}
	if oper.Label != "bacio-sync" {
		t.Fatalf("OPER label = %q, want bacio-sync", oper.Label)
	}
	if oper.LastSyncAt == nil || !oper.LastSyncAt.Equal(last) {
		t.Fatalf("OPER LastSyncAt = %v, want %v", oper.LastSyncAt, last)
	}
}

// TestMirrorCoverage_MissingCloneIsNotCovered: a registry row whose
// clone was never created (or has been deleted) contributes nothing
// rather than erroring — "not covered" is the honest answer.
func TestMirrorCoverage_MissingCloneIsNotCovered(t *testing.T) {
	s := &fakeCoverageStore{remotes: []*model.SyncRemote{
		{RemoteURL: "https://example.com/gone", LocalPath: "/nonexistent/path"},
		{RemoteURL: "https://example.com/never-cloned", LocalPath: ""},
	}}

	got, err := MirrorCoverage(s)
	if err != nil {
		t.Fatalf("MirrorCoverage: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("coverage = %v, want empty", got)
	}
}

// TestMirrorCoverage_FirstRemoteWins: a prefix carried by two sync repos
// resolves to the first in registry order — the status surfaces name one
// mirror.
func TestMirrorCoverage_FirstRemoteWins(t *testing.T) {
	first := mkSyncRepoLayout(t, "BACI")
	second := mkSyncRepoLayout(t, "BACI")
	s := &fakeCoverageStore{remotes: []*model.SyncRemote{
		{RemoteURL: "https://example.com/team-one", LocalPath: first},
		{RemoteURL: "https://example.com/team-two", LocalPath: second},
	}}

	got, err := MirrorCoverage(s)
	if err != nil {
		t.Fatalf("MirrorCoverage: %v", err)
	}
	if got["BACI"].Label != "team-one" {
		t.Fatalf("BACI label = %q, want team-one", got["BACI"].Label)
	}
}

// TestMirrorCoverage_StoreErrorPropagates: a real read failure isn't
// swallowed — only a missing clone is.
func TestMirrorCoverage_StoreErrorPropagates(t *testing.T) {
	want := errors.New("db is on fire")
	if _, err := MirrorCoverage(&fakeCoverageStore{err: want}); !errors.Is(err, want) {
		t.Fatalf("MirrorCoverage err = %v, want %v", err, want)
	}
}
