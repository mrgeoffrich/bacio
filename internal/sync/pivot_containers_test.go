package sync

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// seedPivotContainers layers the pivot's container data onto the
// standard export fixture: a doc-folder tree with the document placed
// inside it, a Kanban board with both issues on lanes, and a second
// repo that is a WORKSPACE (pathless, sentinel-bearing) carrying its
// own folder, document, lane and issue.
//
// Timestamps are forced to fixed values for the same reason
// seedExportFixture forces its own — container updated_at is the sync
// last-writer-wins key and the byte-identical assertions depend on it.
func seedPivotContainers(t *testing.T, s *store.Store) {
	t.Helper()

	repo, err := s.GetRepoByPrefix("MINI")
	if err != nil {
		t.Fatalf("get MINI: %v", err)
	}

	design, err := s.CreateDocFolder(repo.ID, nil, "Design")
	if err != nil {
		t.Fatalf("create Design: %v", err)
	}
	api, err := s.CreateDocFolder(repo.ID, &design.ID, "API")
	if err != nil {
		t.Fatalf("create API: %v", err)
	}
	doc, err := s.GetDocumentByFilename(repo.ID, "auth-overview.md", false)
	if err != nil {
		t.Fatalf("get doc: %v", err)
	}
	if err := s.SetDocumentFolder(doc.ID, &api.ID, 0); err != nil {
		t.Fatalf("place doc: %v", err)
	}

	if err := s.BootstrapKanbanColumns(repo.ID); err != nil {
		t.Fatalf("bootstrap kanban: %v", err)
	}
	cols, err := s.ListKanbanColumns(repo.ID)
	if err != nil || len(cols) < 2 {
		t.Fatalf("list kanban columns: %v (%d)", err, len(cols))
	}
	iss1, err := s.GetIssueByKey("MINI", 1)
	if err != nil {
		t.Fatalf("get MINI-1: %v", err)
	}
	iss2, err := s.GetIssueByKey("MINI", 2)
	if err != nil {
		t.Fatalf("get MINI-2: %v", err)
	}
	if err := s.SetIssueKanbanColumn(iss1.ID, &cols[0].ID, 0); err != nil {
		t.Fatalf("place MINI-1: %v", err)
	}
	if err := s.SetIssueKanbanColumn(iss2.ID, &cols[0].ID, 1); err != nil {
		t.Fatalf("place MINI-2: %v", err)
	}

	// A workspace: pathless, no remote, its own prefix namespace entry.
	ws, err := s.CreateWorkspace("WKSP", "Team notes")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	wsFolder, err := s.CreateDocFolder(ws.ID, nil, "Meetings")
	if err != nil {
		t.Fatalf("create workspace folder: %v", err)
	}
	wsDoc, err := s.CreateDocument(ws.ID, "standup.md", model.DocTypeUserDocs, "# Standup\n", "")
	if err != nil {
		t.Fatalf("create workspace doc: %v", err)
	}
	if err := s.SetDocumentFolder(wsDoc.ID, &wsFolder.ID, 0); err != nil {
		t.Fatalf("place workspace doc: %v", err)
	}
	wsIssue, err := s.CreateIssue(ws.ID, nil, "Book the offsite", "", model.StateTodo, nil, "", "")
	if err != nil {
		t.Fatalf("create workspace issue: %v", err)
	}
	wsCols, err := s.ListKanbanColumns(ws.ID)
	if err != nil || len(wsCols) == 0 {
		t.Fatalf("workspace lanes: %v (%d)", err, len(wsCols))
	}
	if err := s.SetIssueKanbanColumn(wsIssue.ID, &wsCols[0].ID, 0); err != nil {
		t.Fatalf("place workspace issue: %v", err)
	}

	freezeContainerTimestamps(t, s)
}

// freezeContainerTimestamps pins every container row's timestamps so
// exports are byte-stable across runs. Uses raw SQL on the test's own
// throwaway in-memory DB — never the user's store.
func freezeContainerTimestamps(t *testing.T, s *store.Store) {
	t.Helper()
	for _, stmt := range []string{
		`UPDATE doc_folders    SET created_at = '2026-01-05 08:00:00', updated_at = '2026-01-06 08:00:00'`,
		`UPDATE kanban_columns SET created_at = '2026-01-05 08:00:00', updated_at = '2026-01-06 08:00:00'`,
		`UPDATE repos SET created_at = '2026-01-04 08:00:00', updated_at = '2026-01-04 08:00:00' WHERE prefix = 'WKSP'`,
		`UPDATE issues SET created_at = '2026-01-04 08:00:00', updated_at = '2026-01-04 08:00:00' WHERE repo_id = (SELECT id FROM repos WHERE prefix = 'WKSP')`,
		`UPDATE documents SET created_at = '2026-01-04 08:00:00', updated_at = '2026-01-04 08:00:00' WHERE repo_id = (SELECT id FROM repos WHERE prefix = 'WKSP')`,
		`UPDATE features SET created_at = '2026-01-04 08:00:00', updated_at = '2026-01-04 08:00:00' WHERE repo_id = (SELECT id FROM repos WHERE prefix = 'WKSP')`,
	} {
		if _, err := s.DB.Exec(stmt); err != nil {
			t.Fatalf("freeze timestamps (%s): %v", stmt, err)
		}
	}
}

// TestExportWritesContainerRecordsAtSiblingPaths asserts the on-disk
// shape: the sentinel at repos/<P>/workspace.yaml, folders under
// repos/<P>/folders/<uuid>/, lanes under repos/<P>/kanban/<uuid>/, and
// membership written on the CONTAINER side.
func TestExportWritesContainerRecordsAtSiblingPaths(t *testing.T) {
	s, uuids := seedExportFixture(t)
	seedPivotContainers(t, s)

	dir := t.TempDir()
	eng := &Engine{Store: s}
	res, err := eng.Export(context.Background(), dir)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if res.DocFolders != 3 { // Design, API (MINI) + Meetings (WKSP)
		t.Errorf("DocFolders = %d, want 3", res.DocFolders)
	}
	if res.KanbanColumns != 8 { // 4 lanes per repo
		t.Errorf("KanbanColumns = %d, want 8", res.KanbanColumns)
	}

	// The sentinel exists for the workspace and NOT for the git repo.
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(WorkspaceYAMLFile("WKSP")))); err != nil {
		t.Fatalf("workspace sentinel missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(WorkspaceYAMLFile("MINI")))); !os.IsNotExist(err) {
		t.Fatalf("git repo must not get a workspace sentinel (err=%v)", err)
	}
	sentinel := readFileOrFail(t, filepath.Join(dir, filepath.FromSlash(WorkspaceYAMLFile("WKSP"))))
	ws, err := ParseWorkspaceYAML([]byte(sentinel))
	if err != nil {
		t.Fatalf("parse sentinel: %v", err)
	}
	if ws.Kind != string(model.RepoKindWorkspace) {
		t.Errorf("sentinel kind = %q", ws.Kind)
	}

	// The document's membership lives on its folder, not on doc.yaml.
	folders := readContainerManifests(t, dir, "MINI", DocFoldersSubdir, DocFolderManifestName)
	var api *ParsedDocFolder
	for _, body := range folders {
		f, err := ParseDocFolderYAML([]byte(body))
		if err != nil {
			t.Fatalf("parse folder: %v", err)
		}
		if f.Name == "API" {
			api = f
		}
	}
	if api == nil {
		t.Fatal("no API folder manifest")
	}
	if len(api.Documents) != 1 || api.Documents[0] != uuids["doc"] {
		t.Errorf("API folder documents = %v, want [%s]", api.Documents, uuids["doc"])
	}
	if api.ParentUUID == "" {
		t.Error("API folder should carry a parent_uuid")
	}

	// Both cards sit on the first lane, in order.
	lanes := readContainerManifests(t, dir, "MINI", KanbanColumnsSubdir, KanbanColumnManifestName)
	found := false
	for _, body := range lanes {
		c, err := ParseKanbanColumnYAML([]byte(body))
		if err != nil {
			t.Fatalf("parse column: %v", err)
		}
		if len(c.Issues) == 0 {
			continue
		}
		found = true
		if len(c.Issues) != 2 || c.Issues[0] != uuids["iss1"] || c.Issues[1] != uuids["iss2"] {
			t.Errorf("lane %q issues = %v, want [%s %s]", c.Name, c.Issues, uuids["iss1"], uuids["iss2"])
		}
	}
	if !found {
		t.Error("no lane carried any cards")
	}
}

// TestPivotRoundTrip is the headline correctness test: export a DB with
// a workspace, a nested folder tree and a populated Kanban, wipe the
// DB, import, and assert the tree, the membership AND the ordering of
// both come back identical.
func TestPivotRoundTrip(t *testing.T) {
	src, _ := seedExportFixture(t)
	seedPivotContainers(t, src)

	dir := t.TempDir()
	if _, err := (&Engine{Store: src}).Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}
	want := snapshotPivotState(t, src)

	dst, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open dst store: %v", err)
	}
	t.Cleanup(func() { _ = dst.Close() })
	if _, err := (&Engine{Store: dst}).Import(context.Background(), dir); err != nil {
		t.Fatalf("import: %v", err)
	}
	got := snapshotPivotState(t, dst)

	if want != got {
		t.Errorf("round-trip diverged.\n--- exported ---\n%s\n--- imported ---\n%s", want, got)
	}
}

// TestPivotRoundTripIsIdempotent re-imports the same tree into the
// already-imported DB and asserts nothing moves — the second pass must
// be all no-ops, never a churn of renames or re-placements.
func TestPivotRoundTripIsIdempotent(t *testing.T) {
	src, _ := seedExportFixture(t)
	seedPivotContainers(t, src)

	dir := t.TempDir()
	if _, err := (&Engine{Store: src}).Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}

	dst, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open dst store: %v", err)
	}
	t.Cleanup(func() { _ = dst.Close() })
	eng := &Engine{Store: dst}
	if _, err := eng.Import(context.Background(), dir); err != nil {
		t.Fatalf("first import: %v", err)
	}
	first := snapshotPivotState(t, dst)

	res, err := eng.Import(context.Background(), dir)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if len(res.Renamed) != 0 {
		t.Errorf("second import renamed containers: %+v", res.Renamed)
	}
	if len(res.Deleted) != 0 {
		t.Errorf("second import deleted records: %+v", res.Deleted)
	}
	if second := snapshotPivotState(t, dst); second != first {
		t.Errorf("second import moved things.\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

// TestImportedWorkspaceIsAWorkspaceNotAPhantom pins the sentinel's
// whole reason for existing, plus the promotion path an older binary
// leaves behind: a prefix it imported as an inert phantom (it cannot
// see workspace.yaml) becomes a workspace the first time a new binary
// imports the same tree.
func TestImportedWorkspaceIsAWorkspaceNotAPhantom(t *testing.T) {
	src, _ := seedExportFixture(t)
	seedPivotContainers(t, src)
	dir := t.TempDir()
	if _, err := (&Engine{Store: src}).Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}

	dst, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open dst store: %v", err)
	}
	t.Cleanup(func() { _ = dst.Close() })

	// Stand in for the older binary: it inserted the prefix as a plain
	// pathless git row because workspace.yaml is invisible to it.
	srcWS, err := src.GetRepoByPrefix("WKSP")
	if err != nil {
		t.Fatalf("get source workspace: %v", err)
	}
	if _, err := dst.CreatePhantomRepo(srcWS.UUID, "WKSP", "Team notes", ""); err != nil {
		t.Fatalf("seed legacy phantom: %v", err)
	}
	pre, err := dst.GetRepoByPrefix("WKSP")
	if err != nil {
		t.Fatalf("read seeded row: %v", err)
	}
	if !pre.IsPhantom() {
		t.Fatalf("precondition: seeded row should be a phantom, got kind=%q", pre.Kind)
	}

	if _, err := (&Engine{Store: dst}).Import(context.Background(), dir); err != nil {
		t.Fatalf("import: %v", err)
	}
	ws, err := dst.GetRepoByPrefix("WKSP")
	if err != nil {
		t.Fatalf("get imported workspace: %v", err)
	}
	if !ws.IsWorkspace() {
		t.Errorf("imported prefix should be a workspace, got kind=%q", ws.Kind)
	}
	if ws.Path != "" {
		t.Errorf("a workspace must stay pathless, got %q", ws.Path)
	}
	// The git repo beside it must NOT have been promoted.
	mini, err := dst.GetRepoByPrefix("MINI")
	if err != nil {
		t.Fatalf("get MINI: %v", err)
	}
	if mini.IsWorkspace() {
		t.Error("a prefix without a sentinel must import as a git repo")
	}
}

// TestSentinelAbsenceNeverDemotesAWorkspace. An older binary's export
// never writes workspace.yaml, so its ABSENCE carries no information.
// Treating it as "this is a git repo now" would wipe out every
// workspace on the first sync with a mixed-version peer.
func TestSentinelAbsenceNeverDemotesAWorkspace(t *testing.T) {
	src, _ := seedExportFixture(t)
	seedPivotContainers(t, src)
	dir := t.TempDir()
	if _, err := (&Engine{Store: src}).Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}
	// Simulate the older peer having stripped the sentinel.
	if err := os.Remove(filepath.Join(dir, filepath.FromSlash(WorkspaceYAMLFile("WKSP")))); err != nil {
		t.Fatalf("remove sentinel: %v", err)
	}
	if _, err := (&Engine{Store: src}).Import(context.Background(), dir); err != nil {
		t.Fatalf("import: %v", err)
	}
	ws, err := src.GetRepoByPrefix("WKSP")
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if !ws.IsWorkspace() {
		t.Errorf("workspace was demoted by a missing sentinel (kind=%q)", ws.Kind)
	}
}

// TestMembershipDedupeIsDeterministic pins THE dedupe rule for the
// merge artefact the design brief calls out: a bad three-way merge
// listing one document uuid in two folder.yaml files.
//
// Rule: last writer by folder `updated_at`, tie-broken by folder uuid
// ascending — i.e. sort the competing claims ascending by
// (updated_at, uuid) and the last one wins.
func TestMembershipDedupeIsDeterministic(t *testing.T) {
	early := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	t.Run("newer updated_at wins", func(t *testing.T) {
		claims := map[string]membershipClaim{}
		claimMembership(claims, "doc", membershipClaim{containerUUID: "zzz", containerID: 1, updatedAt: early, position: 0})
		claimMembership(claims, "doc", membershipClaim{containerUUID: "aaa", containerID: 2, updatedAt: late, position: 3})
		if got := claims["doc"]; got.containerID != 2 || got.position != 3 {
			t.Errorf("got %+v, want the container with the newer updated_at (id 2, pos 3)", got)
		}
	})

	t.Run("insertion order does not matter", func(t *testing.T) {
		claims := map[string]membershipClaim{}
		claimMembership(claims, "doc", membershipClaim{containerUUID: "aaa", containerID: 2, updatedAt: late, position: 3})
		claimMembership(claims, "doc", membershipClaim{containerUUID: "zzz", containerID: 1, updatedAt: early, position: 0})
		if got := claims["doc"]; got.containerID != 2 {
			t.Errorf("got %+v, want id 2 regardless of the order claims arrive in", got)
		}
	})

	t.Run("exact tie breaks on the higher container uuid", func(t *testing.T) {
		claims := map[string]membershipClaim{}
		claimMembership(claims, "doc", membershipClaim{containerUUID: "aaa", containerID: 1, updatedAt: late})
		claimMembership(claims, "doc", membershipClaim{containerUUID: "bbb", containerID: 2, updatedAt: late})
		if got := claims["doc"]; got.containerUUID != "bbb" {
			t.Errorf("got %q, want the last uuid in ascending order (bbb)", got.containerUUID)
		}
		// Reverse the arrival order: same answer.
		claims = map[string]membershipClaim{}
		claimMembership(claims, "doc", membershipClaim{containerUUID: "bbb", containerID: 2, updatedAt: late})
		claimMembership(claims, "doc", membershipClaim{containerUUID: "aaa", containerID: 1, updatedAt: late})
		if got := claims["doc"]; got.containerUUID != "bbb" {
			t.Errorf("got %q, want bbb", got.containerUUID)
		}
	})

	t.Run("a manifest listing the same member twice keeps the first index", func(t *testing.T) {
		got := dedupeOrdered([]string{"a", "b", "a", "c", "b"})
		want := []string{"a", "b", "c"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

// TestMembershipDedupeEndToEnd drives the same rule through the real
// importer: hand-edit the exported tree so the document is listed by
// BOTH folders, and assert it lands in exactly one — the one whose
// folder.yaml carries the newer updated_at.
func TestMembershipDedupeEndToEnd(t *testing.T) {
	src, uuids := seedExportFixture(t)
	seedPivotContainers(t, src)
	dir := t.TempDir()
	if _, err := (&Engine{Store: src}).Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}

	// "Design" currently has no documents and "API" holds the doc. Add
	// the doc to Design as well (the merge artefact) and make Design the
	// newer writer, so the rule must move the doc to Design.
	designPath, designUUID := findFolderManifest(t, dir, "MINI", "Design")
	body := readFileOrFail(t, designPath)
	body = strings.Replace(body, "documents: []", "documents:\n  - \""+uuids["doc"]+"\"", 1)
	body = strings.Replace(body, `updated_at: "2026-01-06T08:00:00.000Z"`, `updated_at: "2027-01-06T08:00:00.000Z"`, 1)
	if err := os.WriteFile(designPath, []byte(body), 0o644); err != nil {
		t.Fatalf("rewrite Design manifest: %v", err)
	}

	dst, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	t.Cleanup(func() { _ = dst.Close() })
	if _, err := (&Engine{Store: dst}).Import(context.Background(), dir); err != nil {
		t.Fatalf("import: %v", err)
	}

	repo, err := dst.GetRepoByPrefix("MINI")
	if err != nil {
		t.Fatalf("get MINI: %v", err)
	}
	doc, err := dst.GetDocumentByFilename(repo.ID, "auth-overview.md", false)
	if err != nil {
		t.Fatalf("get doc: %v", err)
	}
	if doc.FolderID == nil {
		t.Fatal("doc ended up in no folder at all")
	}
	winner, err := dst.GetDocFolderByUUID(designUUID)
	if err != nil {
		t.Fatalf("get Design folder: %v", err)
	}
	if *doc.FolderID != winner.ID {
		t.Errorf("doc landed in folder %d, want Design (%d) — the newer folder.yaml must win",
			*doc.FolderID, winner.ID)
	}
}

// TestKanbanLaneNameCollisionConverges is the collision that is
// guaranteed rather than hypothetical: BootstrapKanbanColumns seeds
// every repo with the same four lane names under machine-local uuids,
// so the first sync between two machines that both opened the same repo
// arrives with four certain name collisions against
// uniq_kanban_columns_name.
//
// The import must survive it (a raw INSERT would abort the whole run),
// keep every lane, and reach the same answer on a re-import.
func TestKanbanLaneNameCollisionConverges(t *testing.T) {
	src, _ := seedExportFixture(t)
	seedPivotContainers(t, src)
	dir := t.TempDir()
	if _, err := (&Engine{Store: src}).Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}

	// The receiving machine already bootstrapped MINI itself, so it has
	// its own Backlog/Doing/Waiting/Done under different uuids.
	dst, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	t.Cleanup(func() { _ = dst.Close() })
	// Same repo uuid — this machine got the prefix from an earlier sync
	// (back when the sync repo carried no kanban/ folder at all) and
	// then bootstrapped its own lanes locally.
	srcRepo, err := src.GetRepoByPrefix("MINI")
	if err != nil {
		t.Fatalf("get source MINI: %v", err)
	}
	local, err := dst.CreatePhantomRepo(srcRepo.UUID, "MINI", "bacio", "git@github.com:user/bacio.git")
	if err != nil {
		t.Fatalf("create local MINI: %v", err)
	}
	if err := dst.BootstrapKanbanColumns(local.ID); err != nil {
		t.Fatalf("bootstrap local lanes: %v", err)
	}

	eng := &Engine{Store: dst}
	res, err := eng.Import(context.Background(), dir)
	if err != nil {
		t.Fatalf("import must not abort on a lane name collision: %v", err)
	}
	if len(res.Renamed) == 0 {
		t.Error("expected the local-only lanes to yield their names")
	}

	repo, err := dst.GetRepoByPrefix("MINI")
	if err != nil {
		t.Fatalf("get MINI: %v", err)
	}
	cols, err := dst.ListKanbanColumns(repo.ID)
	if err != nil {
		t.Fatalf("list lanes: %v", err)
	}
	if len(cols) != 8 {
		t.Errorf("got %d lanes, want 8 (4 local renamed + 4 imported)", len(cols))
	}
	names := map[string]int{}
	incoming := 0
	for _, c := range cols {
		names[c.Name]++
		if !strings.Contains(c.Name, "(") {
			incoming++
		}
	}
	for name, n := range names {
		if n != 1 {
			t.Errorf("lane name %q used %d times — uniqueness broke", name, n)
		}
	}
	if incoming != 4 {
		t.Errorf("the four incoming lanes should keep their bare names, got %d", incoming)
	}

	// Re-import: already converged, nothing further should move.
	res2, err := eng.Import(context.Background(), dir)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if len(res2.Renamed) != 0 {
		t.Errorf("re-import renamed again (no convergence): %+v", res2.Renamed)
	}
}

// TestDeletedContainerPropagates covers the half of the lifecycle the
// A0 sibling rule makes non-obvious: because an OLDER binary must never
// delete a container record folder, THIS binary has to. Without it a
// stale folder.yaml would sit on disk and the next import would
// resurrect the folder on every machine.
func TestDeletedContainerPropagates(t *testing.T) {
	src, _ := seedExportFixture(t)
	seedPivotContainers(t, src)
	dir := t.TempDir()
	if _, err := (&Engine{Store: src}).Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}

	dst, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	t.Cleanup(func() { _ = dst.Close() })
	eng := &Engine{Store: dst}
	if _, err := eng.Import(context.Background(), dir); err != nil {
		t.Fatalf("import: %v", err)
	}

	// The peer deleted the "API" folder: its record folder is gone from
	// the sync repo, and "Design" no longer lists it as a parent.
	apiPath, apiUUID := findFolderManifest(t, dir, "MINI", "API")
	if err := os.RemoveAll(filepath.Dir(apiPath)); err != nil {
		t.Fatalf("remove API record: %v", err)
	}

	res, err := eng.Import(context.Background(), dir)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	sawDelete := false
	for _, d := range res.Deleted {
		if d.UUID == apiUUID && d.Kind == store.SyncKindDocFolder {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("deleting a folder record should propagate; got %+v", res.Deleted)
	}
	if _, err := dst.GetDocFolderByUUID(apiUUID); err == nil {
		t.Error("the folder row survived a propagated delete")
	}
	// The document it held is untouched — dropping a container drops
	// placement, never content.
	repo, err := dst.GetRepoByPrefix("MINI")
	if err != nil {
		t.Fatalf("get MINI: %v", err)
	}
	if _, err := dst.GetDocumentByFilename(repo.ID, "auth-overview.md", false); err != nil {
		t.Errorf("the document must survive its folder being deleted: %v", err)
	}
}

// TestLocalOnlyContainersSurviveImport. A folder or lane created
// locally and never exported has no manifest on disk. The membership
// pass must leave its members alone — clearing them would empty every
// not-yet-shared folder on the first sync.
func TestLocalOnlyContainersSurviveImport(t *testing.T) {
	s, _ := seedExportFixture(t)
	seedPivotContainers(t, s)
	dir := t.TempDir()
	if _, err := (&Engine{Store: s}).Export(context.Background(), dir); err != nil {
		t.Fatalf("export: %v", err)
	}

	// Created after the export, so nothing on disk knows about it.
	repo, err := s.GetRepoByPrefix("MINI")
	if err != nil {
		t.Fatalf("get MINI: %v", err)
	}
	scratch, err := s.CreateDocFolder(repo.ID, nil, "Scratch")
	if err != nil {
		t.Fatalf("create Scratch: %v", err)
	}
	doc, err := s.GetDocumentByFilename(repo.ID, "auth-overview.md", false)
	if err != nil {
		t.Fatalf("get doc: %v", err)
	}
	if err := s.SetDocumentFolder(doc.ID, &scratch.ID, 0); err != nil {
		t.Fatalf("place doc: %v", err)
	}

	if _, err := (&Engine{Store: s}).Import(context.Background(), dir); err != nil {
		t.Fatalf("import: %v", err)
	}
	after, err := s.GetDocumentByFilename(repo.ID, "auth-overview.md", false)
	if err != nil {
		t.Fatalf("re-read doc: %v", err)
	}
	if after.FolderID == nil || *after.FolderID != scratch.ID {
		t.Errorf("a local-only folder lost its member on import (folder_id=%v, want %d)",
			after.FolderID, scratch.ID)
	}
}

// ---------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------

// snapshotPivotState renders every pivot-relevant fact about a store as
// a stable string: repo kinds, the folder tree with derived paths, doc
// placement + order, lanes with their order, and card placement +
// order. Two stores that round-trip correctly produce the same text.
func snapshotPivotState(t *testing.T, s *store.Store) string {
	t.Helper()
	var b strings.Builder
	repos, err := s.ListRepos()
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	for _, r := range repos {
		// `path` is deliberately absent: it is local client state that
		// repo.yaml has never carried, so an imported prefix is pathless
		// until the user runs bacio inside the matching checkout. `kind`
		// IS compared — that is the whole point of the sentinel.
		b.WriteString("repo " + r.Prefix + " kind=" + string(r.Kind) + "\n")

		folders, err := s.ListDocFolders(r.ID)
		if err != nil {
			t.Fatalf("list folders: %v", err)
		}
		for _, f := range folders {
			fp, err := s.ResolveFolderPath(f.ID)
			if err != nil {
				t.Fatalf("resolve folder path: %v", err)
			}
			b.WriteString("  folder " + fp.Display + " pos=" + itoa(f.Position) + "\n")
			docs, err := s.ListDocuments(store.DocumentFilter{
				RepoID: r.ID, Folder: store.InFolder(f.ID), IncludeArchived: true,
			})
			if err != nil {
				t.Fatalf("list folder docs: %v", err)
			}
			for i, d := range docs {
				b.WriteString("    doc[" + itoa(i) + "] " + d.Filename + " pos=" + itoa(d.FolderPosition) + "\n")
			}
		}

		cols, err := s.ListKanbanColumns(r.ID)
		if err != nil {
			t.Fatalf("list lanes: %v", err)
		}
		for _, c := range cols {
			b.WriteString("  lane " + c.Name + " pos=" + itoa(c.Position) + "\n")
		}
		id := r.ID
		onBoard := true
		issues, err := s.ListIssues(store.IssueFilter{RepoID: &id, IncludeArchived: true, OnKanban: &onBoard})
		if err != nil {
			t.Fatalf("list issues: %v", err)
		}
		byLane := map[int64][]string{}
		laneName := map[int64]string{}
		for _, c := range cols {
			laneName[c.ID] = c.Name
		}
		for _, iss := range issues {
			byLane[*iss.KanbanColumnID] = append(byLane[*iss.KanbanColumnID],
				itoa(iss.KanbanPosition)+":"+iss.Key)
		}
		for _, c := range cols {
			cards := byLane[c.ID]
			sort.Strings(cards)
			for _, card := range cards {
				b.WriteString("    card " + laneName[c.ID] + " " + card + "\n")
			}
		}
	}
	return b.String()
}

func readContainerManifests(t *testing.T, root, prefix, subdir, manifest string) []string {
	t.Helper()
	dir := filepath.Join(root, "repos", prefix, subdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, readFileOrFail(t, filepath.Join(dir, e.Name(), manifest)))
	}
	return out
}

// findFolderManifest locates the on-disk folder.yaml whose `name` field
// matches, returning its path and uuid.
func findFolderManifest(t *testing.T, root, prefix, name string) (string, string) {
	t.Helper()
	dir := filepath.Join(root, "repos", prefix, DocFoldersSubdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name(), DocFolderManifestName)
		parsed, err := ParseDocFolderYAML([]byte(readFileOrFail(t, p)))
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		if parsed.Name == name {
			return p, parsed.UUID
		}
	}
	t.Fatalf("no folder manifest named %q under %s", name, dir)
	return "", ""
}

func itoa(n int) string { return strconv.Itoa(n) }
