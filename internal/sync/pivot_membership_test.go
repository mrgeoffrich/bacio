package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestDiscoverMembershipReportsWorkspaces. Both a phantom and a
// workspace are pathless, so the historical `path == ""` test called
// every workspace a phantom — a lie in both directions: there is no
// checkout on another machine to go and link, and the data is fully
// present here. Only `kind` tells them apart.
func TestDiscoverMembershipReportsWorkspaces(t *testing.T) {
	root := mkSyncRepoLayout(t, "LINK", "PHTM", "WKSP", "UNKN")
	lookup := &mapLookup{repos: map[string]*model.Repo{
		"LINK": {Prefix: "LINK", Kind: model.RepoKindGit, Path: "/work/link"},
		"PHTM": {Prefix: "PHTM", Kind: model.RepoKindGit},
		"WKSP": {Prefix: "WKSP", Kind: model.RepoKindWorkspace},
	}}

	got, err := DiscoverMembership(root, lookup)
	if err != nil {
		t.Fatalf("DiscoverMembership: %v", err)
	}
	want := map[string]MembershipStatus{
		"LINK": StatusLinked,
		"PHTM": StatusPhantom,
		"WKSP": StatusWorkspace,
		"UNKN": StatusAbsent,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d members, want %d", len(got), len(want))
	}
	for _, m := range got {
		if want[m.Prefix] != m.Status {
			t.Errorf("%s: status = %q, want %q", m.Prefix, m.Status, want[m.Prefix])
		}
	}
}

// TestBuildRegistryNeverListsAWorkspaceAsUnsynced.
//
// UnsyncedProjects is a CALL TO ACTION, not an inventory: the settings
// UI renders every row as a "set up sync" affordance
// (SyncSettingsSection -> SyncSetupModal), and SetupSync refuses a
// pathless repo outright. A workspace listed here would therefore offer
// a button that cannot work, so it is excluded whether or not any sync
// repo currently mirrors it. Whether a workspace IS mirrored is real
// information, but its home is the BACI-376 sync badge.
//
// internal/client/sync_status.go has a parallel implementation of this
// residual, pinned by TestSyncRegistryOmitsWorkspace; the two must not
// diverge.
func TestBuildRegistryNeverListsAWorkspaceAsUnsynced(t *testing.T) {
	workspace := &model.Repo{Prefix: "WKSP", Name: "Team notes", UUID: "u-ws", Kind: model.RepoKindWorkspace}
	phantom := &model.Repo{Prefix: "PHTM", Name: "elsewhere", UUID: "u-ph", Kind: model.RepoKindGit}

	t.Run("not yet mirrored", func(t *testing.T) {
		s := &fakeRegistryStore{
			mapLookup: mapLookup{repos: map[string]*model.Repo{"WKSP": workspace, "PHTM": phantom}},
			repos:     []*model.Repo{workspace, phantom},
		}
		got, err := BuildRegistry(s, nil)
		if err != nil {
			t.Fatalf("BuildRegistry: %v", err)
		}
		// Neither kind qualifies: both are pathless, for different
		// reasons (see the residual's comment in registry.go).
		if len(got.UnsyncedProjects) != 0 {
			t.Fatalf("UnsyncedProjects = %+v, want none — a pathless repo has nothing to set up", got.UnsyncedProjects)
		}
	})

	t.Run("mirrored by a sync repo", func(t *testing.T) {
		clone := mkSyncRepoLayout(t, "WKSP")
		// The sentinel isn't needed for discovery — the local DB's kind
		// is what DiscoverMembership reads — but write it so the fixture
		// matches what an export actually produces.
		if err := os.WriteFile(
			filepath.Join(clone, filepath.FromSlash(WorkspaceYAMLFile("WKSP"))),
			[]byte("kind: \"workspace\"\n"), 0o644,
		); err != nil {
			t.Fatalf("write sentinel: %v", err)
		}
		s := &fakeRegistryStore{
			mapLookup: mapLookup{repos: map[string]*model.Repo{"WKSP": workspace}},
			repos:     []*model.Repo{workspace},
			remotes:   []*model.SyncRemote{{RemoteURL: "git@example.com:me/sync.git", LocalPath: clone}},
		}
		got, err := BuildRegistry(s, nil)
		if err != nil {
			t.Fatalf("BuildRegistry: %v", err)
		}
		if len(got.UnsyncedProjects) != 0 {
			t.Errorf("a mirrored workspace should drop off the unsynced list, got %+v", got.UnsyncedProjects)
		}
		if len(got.SyncRepos) != 1 || len(got.SyncRepos[0].Members) != 1 ||
			got.SyncRepos[0].Members[0].Status != StatusWorkspace {
			t.Errorf("expected one workspace member, got %+v", got.SyncRepos)
		}
	})
}

// TestComputeFileOpsDeletesStaleContainerRecords is the delete-planner
// half of the container lifecycle. An older bacio must never delete a
// container record folder (proved by
// TestLegacyRecordFolderOfIgnoresPivotPaths) — but THIS one has to, or
// a folder deleted from the DB would leave folder.yaml on disk forever
// and the next import would resurrect it on every machine.
func TestComputeFileOpsDeletesStaleContainerRecords(t *testing.T) {
	const u = "0191f0d2-1111-7000-8000-aaaaaaaaaaaa"
	target := t.TempDir()
	staging := t.TempDir()

	// The target still carries a container record and a doc record;
	// staging (the desired shape) carries neither.
	writeAt(t, target, DocFolderYAMLFile(DocFolderFolder("MINI", u)), "uuid: \""+u+"\"\n")
	writeAt(t, target, KanbanColumnYAMLFile(KanbanColumnFolder("MINI", u)), "uuid: \""+u+"\"\n")
	writeAt(t, target, WorkspaceYAMLFile("MINI"), "kind: \"workspace\"\n")
	writeAt(t, staging, RepoYAMLFile("MINI"), "uuid: \"repo-uuid\"\n")
	writeAt(t, target, RepoYAMLFile("MINI"), "uuid: \"repo-uuid\"\n")

	ops, err := computeFileOps(target, staging)
	if err != nil {
		t.Fatalf("computeFileOps: %v", err)
	}
	deletes := map[string]bool{}
	for _, op := range ops {
		if op.Kind == opDelete {
			deletes[op.Path] = true
		}
	}
	if !deletes[DocFolderFolder("MINI", u)] {
		t.Errorf("a stale folder record must be delete-planned; got %v", deletes)
	}
	if !deletes[KanbanColumnFolder("MINI", u)] {
		t.Errorf("a stale kanban record must be delete-planned; got %v", deletes)
	}
	if deletes[WorkspaceYAMLFile("MINI")] {
		t.Error("workspace.yaml must never be delete-planned — it is repo.yaml's sibling, and a repo folder is never pruned")
	}
}

func writeAt(t *testing.T, root, rel, body string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
