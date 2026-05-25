package sync

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// fakeRegistryStore is the unit-test stand-in for *store.Store. Wraps
// the mapLookup the membership tests already use, plus the two extra
// reads BuildRegistry needs (ListSyncRemotes / ListRepos).
type fakeRegistryStore struct {
	mapLookup
	remotes []*model.SyncRemote
	repos   []*model.Repo
	listErr error
}

func (f *fakeRegistryStore) ListSyncRemotes() ([]*model.SyncRemote, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.remotes, nil
}

func (f *fakeRegistryStore) ListRepos() ([]*model.Repo, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.repos, nil
}

// TestBuildRegistry_Empty: no sync_remotes rows and no project repos —
// both slices come back empty (and non-nil so JSON encoders emit `[]`).
func TestBuildRegistry_Empty(t *testing.T) {
	s := &fakeRegistryStore{
		mapLookup: mapLookup{repos: map[string]*model.Repo{}},
	}
	got, err := BuildRegistry(s, nil)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	if got == nil {
		t.Fatal("BuildRegistry returned nil registry")
	}
	if got.SyncRepos == nil || len(got.SyncRepos) != 0 {
		t.Fatalf("SyncRepos = %v, want empty non-nil slice", got.SyncRepos)
	}
	if got.UnsyncedProjects == nil || len(got.UnsyncedProjects) != 0 {
		t.Fatalf("UnsyncedProjects = %v, want empty non-nil slice", got.UnsyncedProjects)
	}
}

// TestBuildRegistry_MixedMembers: one sync_remotes row whose clone on
// disk carries a linked prefix, a phantom prefix and an absent prefix.
// Asserts the per-entry shape (label derivation, members status, no
// unsynced residual for the surfaced prefixes).
func TestBuildRegistry_MixedMembers(t *testing.T) {
	clone := mkSyncRepoLayout(t, "MINI", "PHTM", "UNKN")
	remote := "git@example.com:bacio/team-sync.git"

	miniRepo := &model.Repo{Prefix: "MINI", Name: "mini", UUID: "uuid-mini", Path: "/repos/mini"}
	phantomRepo := &model.Repo{Prefix: "PHTM", Name: "phantom", UUID: "uuid-phtm", Path: ""}
	s := &fakeRegistryStore{
		mapLookup: mapLookup{repos: map[string]*model.Repo{
			"MINI": miniRepo,
			"PHTM": phantomRepo,
		}},
		remotes: []*model.SyncRemote{{
			RemoteURL: remote,
			LocalPath: clone,
		}},
		repos: []*model.Repo{miniRepo, phantomRepo},
	}

	got, err := BuildRegistry(s, nil)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	if len(got.SyncRepos) != 1 {
		t.Fatalf("SyncRepos len = %d, want 1", len(got.SyncRepos))
	}
	entry := got.SyncRepos[0]
	if entry.RemoteURL != remote {
		t.Errorf("RemoteURL = %q, want %q", entry.RemoteURL, remote)
	}
	if entry.Label != "team-sync" {
		t.Errorf("Label = %q, want %q", entry.Label, "team-sync")
	}
	if entry.LocalPath != clone {
		t.Errorf("LocalPath = %q, want %q", entry.LocalPath, clone)
	}
	if len(entry.Members) != 3 {
		t.Fatalf("Members len = %d, want 3 (got %+v)", len(entry.Members), entry.Members)
	}
	// DiscoverMembership sorts ASC, so [MINI, PHTM, UNKN].
	wantStatuses := []MembershipStatus{StatusLinked, StatusPhantom, StatusAbsent}
	for i, m := range entry.Members {
		if m.Status != wantStatuses[i] {
			t.Errorf("Members[%d].Status = %q, want %q", i, m.Status, wantStatuses[i])
		}
	}
	// MINI (linked) carries name + uuid; UNKN (absent) carries neither.
	if entry.Members[0].Name != miniRepo.Name || entry.Members[0].UUID != miniRepo.UUID {
		t.Errorf("MINI member name/uuid = %q/%q, want %q/%q",
			entry.Members[0].Name, entry.Members[0].UUID, miniRepo.Name, miniRepo.UUID)
	}
	if entry.Members[2].Name != "" || entry.Members[2].UUID != "" {
		t.Errorf("UNKN absent member must carry empty name/uuid, got %q/%q",
			entry.Members[2].Name, entry.Members[2].UUID)
	}
	// Both real-row prefixes (linked + phantom) are surfaced under the
	// registry, so neither lands in unsynced_projects. The phantom is
	// also excluded by path=="".
	for _, up := range got.UnsyncedProjects {
		if up.Prefix == "MINI" || up.Prefix == "PHTM" {
			t.Fatalf("unsynced contains surfaced prefix %q: %+v", up.Prefix, got.UnsyncedProjects)
		}
	}
}

// TestBuildRegistry_UnsyncedResidual: two project repos; one has a
// .bacio/config.yaml whose sync.remote is in the registry, the other
// has no config. Only the second lands in unsynced_projects.
func TestBuildRegistry_UnsyncedResidual(t *testing.T) {
	// Repo "AAAA" lives under a real working-tree path with a
	// .bacio/config.yaml pointing at the registry's remote.
	configuredPath := t.TempDir()
	registeredRemote := "https://example.com/team/registered.git"
	if err := WriteProjectConfig(configuredPath, ProjectConfig{
		Sync: ProjectSync{Remote: registeredRemote},
	}); err != nil {
		t.Fatalf("WriteProjectConfig: %v", err)
	}
	configured := &model.Repo{
		Prefix: "AAAA", Name: "configured", UUID: "uuid-aaaa", Path: configuredPath,
	}

	// Repo "BBBB" lives at another path with NO .bacio/config.yaml — it
	// must surface in unsynced_projects.
	unsyncedPath := t.TempDir()
	unsynced := &model.Repo{
		Prefix: "BBBB", Name: "unsynced", UUID: "uuid-bbbb", Path: unsyncedPath,
	}

	// Empty clone — no repos/ subdir, so DiscoverMembership returns []
	// without error. The registry entry still appears with empty members.
	clone := t.TempDir()
	s := &fakeRegistryStore{
		mapLookup: mapLookup{repos: map[string]*model.Repo{}},
		remotes: []*model.SyncRemote{{
			RemoteURL: registeredRemote,
			LocalPath: clone,
		}},
		repos: []*model.Repo{configured, unsynced},
	}

	got, err := BuildRegistry(s, nil)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	if len(got.SyncRepos) != 1 {
		t.Fatalf("SyncRepos len = %d, want 1", len(got.SyncRepos))
	}
	if len(got.SyncRepos[0].Members) != 0 {
		t.Fatalf("empty clone should yield zero members, got %+v", got.SyncRepos[0].Members)
	}
	if len(got.UnsyncedProjects) != 1 {
		t.Fatalf("UnsyncedProjects len = %d, want 1 (got %+v)", len(got.UnsyncedProjects), got.UnsyncedProjects)
	}
	if got.UnsyncedProjects[0].Prefix != "BBBB" {
		t.Errorf("unsynced[0].Prefix = %q, want %q", got.UnsyncedProjects[0].Prefix, "BBBB")
	}
}

// TestBuildRegistry_PropagatesStoreError: a ListSyncRemotes failure
// bubbles up — that's a real DB error, not "empty registry".
func TestBuildRegistry_PropagatesStoreError(t *testing.T) {
	boom := errors.New("disk gone")
	s := &fakeRegistryStore{
		mapLookup: mapLookup{repos: map[string]*model.Repo{}},
		listErr:   boom,
	}
	_, err := BuildRegistry(s, nil)
	if err == nil {
		t.Fatal("BuildRegistry = nil error, want propagated store error")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("BuildRegistry err = %v, want it to wrap %v", err, boom)
	}
}

// TestBuildRegistry_RealStore: integration test confirming *store.Store
// satisfies RegistryStore at compile time and the full composition
// works against a real DB. Mirrors TestDiscoverMembership_RealStore.
func TestBuildRegistry_RealStore(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Linked repo for prefix MINI; we'll let UpgradePhantomRepo etc. be
	// out of scope and just construct a plain CreateRepo row.
	repoPath := t.TempDir()
	if _, err := s.CreateRepo("MINI", "mini", repoPath, ""); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if _, err := s.CreatePhantomRepo("019e4f15-2e05-7f32-84b3-dde881591ccc", "PHTM", "phantom", ""); err != nil {
		t.Fatalf("CreatePhantomRepo: %v", err)
	}

	clone := mkSyncRepoLayout(t, "MINI", "PHTM", "UNKN")
	remote := "git@example.com:bacio/integration.git"
	if err := s.UpsertSyncRemote(remote, clone); err != nil {
		t.Fatalf("UpsertSyncRemote: %v", err)
	}

	// Compile-time satisfaction:
	var registry RegistryStore = s
	got, err := BuildRegistry(registry, nil)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	if len(got.SyncRepos) != 1 {
		t.Fatalf("SyncRepos len = %d, want 1", len(got.SyncRepos))
	}
	entry := got.SyncRepos[0]
	if entry.Label != "integration" {
		t.Errorf("Label = %q, want %q", entry.Label, "integration")
	}
	if len(entry.Members) != 3 {
		t.Fatalf("Members len = %d, want 3 (got %+v)", len(entry.Members), entry.Members)
	}
	// MINI is the linked repo with a real working-tree path; it must
	// NOT appear in unsynced_projects (the registry surfaces it).
	for _, up := range got.UnsyncedProjects {
		if up.Prefix == "MINI" {
			t.Fatalf("MINI appears in unsynced_projects: %+v", up)
		}
	}
}

// TestBuildRegistry_BrokenProjectConfigStillReportsUnsynced: a project
// repo whose .bacio/config.yaml is unparseable shouldn't make the repo
// vanish from the surface — it lands in unsynced_projects with a logger
// warning. Mirrors the original handler's tolerance.
func TestBuildRegistry_BrokenProjectConfigStillReportsUnsynced(t *testing.T) {
	repoPath := t.TempDir()
	// Write an unparseable config — random bytes the YAML decoder will
	// reject.
	cfgDir := filepath.Join(repoPath, ".bacio")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir .bacio: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("not: valid: yaml: ::"), 0o644); err != nil {
		t.Fatalf("write broken config: %v", err)
	}
	repo := &model.Repo{Prefix: "CCCC", Name: "broken", UUID: "uuid-cccc", Path: repoPath}
	s := &fakeRegistryStore{
		mapLookup: mapLookup{repos: map[string]*model.Repo{}},
		repos:     []*model.Repo{repo},
	}

	got, err := BuildRegistry(s, nil)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	if len(got.UnsyncedProjects) != 1 || got.UnsyncedProjects[0].Prefix != "CCCC" {
		t.Fatalf("UnsyncedProjects = %+v, want one CCCC entry", got.UnsyncedProjects)
	}
}
