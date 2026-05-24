package sync

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// mapLookup is the unit-test stand-in for *store.Store. It satisfies
// PrefixLookup from an in-memory map so most cases don't need to spin
// up a real SQLite store. (One integration case below uses a real
// *store.Store to catch interface drift.)
type mapLookup struct {
	repos map[string]*model.Repo
	err   error
}

func (m *mapLookup) GetRepoByPrefix(prefix string) (*model.Repo, error) {
	if m.err != nil {
		return nil, m.err
	}
	r, ok := m.repos[prefix]
	if !ok {
		return nil, store.ErrNotFound
	}
	return r, nil
}

// mkSyncRepoLayout writes a sync-style `<root>/repos/<prefix>/`
// directory for each name in prefixes. Returns the temp root. Stray
// extras can be written separately by the caller.
func mkSyncRepoLayout(t *testing.T, prefixes ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range prefixes {
		if err := os.MkdirAll(filepath.Join(root, "repos", p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}
	return root
}

// TestDiscoverMembership_EmptyRoot: an empty syncRepoRoot means we
// have no clone on this machine (the registry row exists but the
// clone is missing or never happened). Returns an empty slice + nil
// error so callers can render the registry row with empty membership.
func TestDiscoverMembership_EmptyRoot(t *testing.T) {
	got, err := DiscoverMembership("", &mapLookup{})
	if err != nil {
		t.Fatalf("DiscoverMembership(\"\"): %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("DiscoverMembership(\"\") = %v, want empty non-nil slice", got)
	}
}

// TestDiscoverMembership_MissingReposDir: the clone exists but has no
// repos/ subdir yet (freshly initialised, nothing exported). Same
// tolerance: empty + nil error.
func TestDiscoverMembership_MissingReposDir(t *testing.T) {
	root := t.TempDir()
	got, err := DiscoverMembership(root, &mapLookup{})
	if err != nil {
		t.Fatalf("DiscoverMembership: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

// TestDiscoverMembership_NonExistentRoot: pointing at a path that
// doesn't exist on disk also reports empty membership. Same rationale
// as EmptyRoot — registry row outlives its clone.
func TestDiscoverMembership_NonExistentRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	got, err := DiscoverMembership(root, &mapLookup{})
	if err != nil {
		t.Fatalf("DiscoverMembership(nonexistent): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

// TestDiscoverMembership_Mixed exercises the three-status classifier:
// a linked prefix (DB row, path != ""), a phantom prefix (DB row, path
// == ""), and an absent prefix (no DB row). Also asserts the result
// is sorted by Prefix ASC.
func TestDiscoverMembership_Mixed(t *testing.T) {
	root := mkSyncRepoLayout(t, "MINI", "PHTM", "UNKN")
	lookup := &mapLookup{repos: map[string]*model.Repo{
		"MINI": {Prefix: "MINI", Path: "/repos/mini"},
		"PHTM": {Prefix: "PHTM", Path: ""},
	}}

	got, err := DiscoverMembership(root, lookup)
	if err != nil {
		t.Fatalf("DiscoverMembership: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (got %+v)", len(got), got)
	}
	// Sorted by Prefix ASC: MINI, PHTM, UNKN.
	wantPrefixes := []string{"MINI", "PHTM", "UNKN"}
	wantStatuses := []MembershipStatus{StatusLinked, StatusPhantom, StatusAbsent}
	for i, m := range got {
		if m.Prefix != wantPrefixes[i] {
			t.Errorf("got[%d].Prefix = %q, want %q", i, m.Prefix, wantPrefixes[i])
		}
		if m.Status != wantStatuses[i] {
			t.Errorf("got[%d].Status = %q, want %q", i, m.Status, wantStatuses[i])
		}
	}
	// Absent rows must carry nil Repo; linked/phantom must carry a
	// non-nil one (so renderers can show repo.Name etc.).
	if got[0].Repo == nil || got[0].Repo.Prefix != "MINI" {
		t.Errorf("linked entry must carry repo, got %+v", got[0].Repo)
	}
	if got[1].Repo == nil || got[1].Repo.Path != "" {
		t.Errorf("phantom entry must carry phantom repo, got %+v", got[1].Repo)
	}
	if got[2].Repo != nil {
		t.Errorf("absent entry must carry nil repo, got %+v", got[2].Repo)
	}
}

// TestDiscoverMembership_IgnoresNonDirEntries: a stray file under
// repos/ shouldn't show up as a member or fail discovery — only
// directories are valid prefix folders.
func TestDiscoverMembership_IgnoresNonDirEntries(t *testing.T) {
	root := mkSyncRepoLayout(t, "MINI")
	stray := filepath.Join(root, "repos", "README.md")
	if err := os.WriteFile(stray, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write stray file: %v", err)
	}
	lookup := &mapLookup{repos: map[string]*model.Repo{
		"MINI": {Prefix: "MINI", Path: "/repos/mini"},
	}}

	got, err := DiscoverMembership(root, lookup)
	if err != nil {
		t.Fatalf("DiscoverMembership: %v", err)
	}
	if len(got) != 1 || got[0].Prefix != "MINI" {
		t.Fatalf("got %+v, want exactly one MINI entry", got)
	}
}

// TestDiscoverMembership_PropagatesLookupError: an unexpected DB error
// from the lookup must bubble up rather than be silently classified as
// "absent". (ErrNotFound is the only error the helper interprets.)
func TestDiscoverMembership_PropagatesLookupError(t *testing.T) {
	root := mkSyncRepoLayout(t, "MINI")
	boom := errors.New("connection refused")
	lookup := &mapLookup{err: boom}

	_, err := DiscoverMembership(root, lookup)
	if err == nil {
		t.Fatal("DiscoverMembership = nil error, want propagated lookup error")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("DiscoverMembership err = %v, want it to wrap %v", err, boom)
	}
}

// TestDiscoverMembership_NilLookup: a programmer error — refusing it
// loudly beats segfaulting deep inside the walk loop.
func TestDiscoverMembership_NilLookup(t *testing.T) {
	if _, err := DiscoverMembership("/tmp/whatever", nil); err == nil {
		t.Fatal("DiscoverMembership(nil lookup) = nil, want error")
	}
}

// TestDiscoverMembership_RealStore: integration test that wires up a
// real *store.Store with one CreateRepo + one CreatePhantomRepo, then
// confirms *store.Store satisfies PrefixLookup at compile time and
// behaves correctly when used via the interface. Catches drift if
// GetRepoByPrefix's signature ever changes.
func TestDiscoverMembership_RealStore(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.CreateRepo("MINI", "mini", "/repos/mini", ""); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if _, err := s.CreatePhantomRepo("019e4f15-2e05-7f32-84b3-dde881591cba", "PHTM", "phantom", ""); err != nil {
		t.Fatalf("CreatePhantomRepo: %v", err)
	}

	root := mkSyncRepoLayout(t, "MINI", "PHTM", "UNKN")
	// Compile-time satisfaction:
	var lookup PrefixLookup = s
	got, err := DiscoverMembership(root, lookup)
	if err != nil {
		t.Fatalf("DiscoverMembership: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (got %+v)", len(got), got)
	}
	wantStatuses := map[string]MembershipStatus{
		"MINI": StatusLinked,
		"PHTM": StatusPhantom,
		"UNKN": StatusAbsent,
	}
	for _, m := range got {
		if got, want := m.Status, wantStatuses[m.Prefix]; got != want {
			t.Errorf("%s: status = %q, want %q", m.Prefix, got, want)
		}
	}
}
