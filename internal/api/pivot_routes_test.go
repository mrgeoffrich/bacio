package api_test

// HTTP-layer tests for the pivot routes: workspaces, the document folder
// tree and the Kanban lanes.
//
// The load-bearing contract these pin is the *wire shape*, not the
// storage behaviour (the store and client layers own that):
//
//   - which PATCH key is PRESENT selects rename vs move/reorder, and
//     sending both or neither is a 400;
//   - a present-but-empty uuid means the tree root / off the board, and
//     is not the same as an absent one;
//   - an absent `position` appends, while an explicit 0 is the top;
//   - both DELETEs answer 204 for real and 200-plus-preview on dry_run;
//   - a uuid belonging to another repo is a 404, never a mutation.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// ---------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------

func decodeInto[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

func mustStatus(t *testing.T, resp *http.Response, raw []byte, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("status: want %d got %d body=%s", want, resp.StatusCode, raw)
	}
}

func seedWorkspace(t *testing.T, s *store.Store, prefix, name string) *model.Repo {
	t.Helper()
	ws, err := s.CreateWorkspace(prefix, name)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	return ws
}

func seedDocFolder(t *testing.T, s *store.Store, repo *model.Repo, parent *int64, name string) *model.DocFolder {
	t.Helper()
	f, err := s.CreateDocFolder(repo.ID, parent, name)
	if err != nil {
		t.Fatalf("CreateDocFolder %q: %v", name, err)
	}
	return f
}

func seedKanbanColumn(t *testing.T, s *store.Store, repo *model.Repo, name string) *model.KanbanColumn {
	t.Helper()
	c, err := s.CreateKanbanColumn(repo.ID, name)
	if err != nil {
		t.Fatalf("CreateKanbanColumn %q: %v", name, err)
	}
	return c
}

// ---------------------------------------------------------------------
// POST /workspaces
// ---------------------------------------------------------------------

func TestWorkspaceCreate(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	resp, raw := apiPost(t, ts.URL+"/workspaces", map[string]any{"name": "Design Space"})
	mustStatus(t, resp, raw, http.StatusCreated)

	got := decodeInto[model.Repo](t, raw)
	if got.Kind != model.RepoKindWorkspace {
		t.Fatalf("kind: %q", got.Kind)
	}
	if got.Path != "" || got.RemoteURL != "" {
		t.Fatalf("workspace carries a path/remote: %+v", got)
	}
	if got.Prefix == "" || got.ID == 0 {
		t.Fatalf("workspace not persisted: %+v", got)
	}
	// The wire shape must name the kind explicitly — the frontend's
	// 'git' | 'workspace' union has no member for "".
	if !strings.Contains(string(raw), `"kind": "workspace"`) {
		t.Fatalf("kind missing from wire bytes: %s", raw)
	}
	// BootstrapRepoDefaults ran: the starter board exists.
	cols, err := s.ListKanbanColumns(got.ID)
	if err != nil {
		t.Fatalf("ListKanbanColumns: %v", err)
	}
	if len(cols) != len(store.DefaultKanbanColumnNames) {
		t.Fatalf("starter board: %d lanes", len(cols))
	}
	assertHistoryOps(t, s, []string{"workspace.create"})
}

func TestWorkspaceCreate_ExplicitPrefix(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	resp, raw := apiPost(t, ts.URL+"/workspaces", map[string]any{"name": "Design", "prefix": "DSGN"})
	mustStatus(t, resp, raw, http.StatusCreated)
	if got := decodeInto[model.Repo](t, raw); got.Prefix != "DSGN" {
		t.Fatalf("prefix: %q", got.Prefix)
	}
	if _, err := s.GetRepoByPrefix("DSGN"); err != nil {
		t.Fatalf("GetRepoByPrefix: %v", err)
	}
}

func TestWorkspaceCreate_PrefixTaken(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s) // MINI
	resp, raw := apiPost(t, ts.URL+"/workspaces", map[string]any{"name": "Mini", "prefix": "MINI"})
	mustStatus(t, resp, raw, http.StatusConflict)
}

func TestWorkspaceCreate_NameRequired(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, raw := apiPost(t, ts.URL+"/workspaces", map[string]any{"name": "   "})
	mustStatus(t, resp, raw, http.StatusBadRequest)
	if !strings.Contains(string(raw), `"field": "name"`) && !strings.Contains(string(raw), `"field":"name"`) {
		t.Fatalf("expected field=name in details: %s", raw)
	}
}

func TestWorkspaceCreate_DryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	resp, raw := apiPost(t, ts.URL+"/workspaces?dry_run=true", map[string]any{"name": "Rehearsal"})
	mustStatus(t, resp, raw, http.StatusCreated)
	if resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("missing X-Dry-Run header")
	}
	got := decodeInto[model.Repo](t, raw)
	if got.Kind != model.RepoKindWorkspace || got.Prefix == "" {
		t.Fatalf("projection: %+v", got)
	}
	if got.ID != 0 {
		t.Fatalf("dry run assigned an id: %+v", got)
	}
	repos, err := s.ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("dry run wrote %d repos", len(repos))
	}
	assertHistoryOps(t, s, nil)
}

// ---------------------------------------------------------------------
// POST /repos with kind, and kind on the list shape
// ---------------------------------------------------------------------

func TestReposCreate_KindWorkspaceNeedsNoPath(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	resp, raw := apiPost(t, ts.URL+"/repos", map[string]any{"name": "Ideas", "kind": "workspace"})
	mustStatus(t, resp, raw, http.StatusCreated)
	got := decodeInto[model.Repo](t, raw)
	if got.Kind != model.RepoKindWorkspace || got.Path != "" {
		t.Fatalf("repo: %+v", got)
	}
	// Same audit op as POST /workspaces — one implementation, two doors.
	assertHistoryOps(t, s, []string{"workspace.create"})
}

func TestReposCreate_KindWorkspaceRejectsPath(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, raw := apiPost(t, ts.URL+"/repos", map[string]any{
		"name": "Ideas", "kind": "workspace", "path": "/tmp/ideas",
	})
	mustStatus(t, resp, raw, http.StatusBadRequest)
	if !strings.Contains(string(raw), "path") {
		t.Fatalf("expected a path complaint: %s", raw)
	}
}

func TestReposCreate_UnknownKind(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, raw := apiPost(t, ts.URL+"/repos", map[string]any{"name": "Ideas", "kind": "notebook"})
	mustStatus(t, resp, raw, http.StatusBadRequest)
}

func TestReposCreate_GitStillRequiresPath(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, raw := apiPost(t, ts.URL+"/repos", map[string]any{"name": "Ideas"})
	mustStatus(t, resp, raw, http.StatusBadRequest)
	if !strings.Contains(string(raw), "path is required") {
		t.Fatalf("expected path is required: %s", raw)
	}
}

// TestReposList_CarriesKind is the Phase 5/6 dependency: board.http.ts
// builds the frontend's board list off GET /repos, and Phase 6 hides the
// Agentic Pipeline nav entry on a workspace by reading this field.
func TestReposList_CarriesKind(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	seedWorkspace(t, s, "WKSP", "Workspace")

	resp, raw := apiGet(t, ts.URL+"/repos")
	mustStatus(t, resp, raw, http.StatusOK)
	rows := decodeInto[[]model.Repo](t, raw)
	if len(rows) != 2 {
		t.Fatalf("len: %d", len(rows))
	}
	byPrefix := map[string]model.RepoKind{}
	for _, r := range rows {
		byPrefix[r.Prefix] = r.Kind
	}
	if byPrefix["MINI"] != model.RepoKindGit {
		t.Fatalf("MINI kind: %q", byPrefix["MINI"])
	}
	if byPrefix["WKSP"] != model.RepoKindWorkspace {
		t.Fatalf("WKSP kind: %q", byPrefix["WKSP"])
	}
	// Never the empty string on the wire — an unmatched union member
	// downstream.
	if strings.Contains(string(raw), `"kind": ""`) {
		t.Fatalf("empty kind on the wire: %s", raw)
	}

	// The single-repo read carries it too.
	resp, raw = apiGet(t, ts.URL+"/repos/WKSP")
	mustStatus(t, resp, raw, http.StatusOK)
	if decodeInto[model.Repo](t, raw).Kind != model.RepoKindWorkspace {
		t.Fatalf("show: %s", raw)
	}
}

func TestReposCreate_DryRunProjectionCarriesKind(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, raw := apiPost(t, ts.URL+"/repos?dry_run=true", map[string]any{
		"name": "Git Thing", "path": t.TempDir(),
	})
	mustStatus(t, resp, raw, http.StatusCreated)
	if decodeInto[model.Repo](t, raw).Kind != model.RepoKindGit {
		t.Fatalf("projection kind: %s", raw)
	}
}

// ---------------------------------------------------------------------
// doc folders
// ---------------------------------------------------------------------

func TestDocFoldersList_EmptyIsArray(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/doc-folders")
	mustStatus(t, resp, raw, http.StatusOK)
	if strings.TrimSpace(string(raw)) == "null" {
		t.Fatalf("want [], got null")
	}
	if len(decodeInto[[]model.DocFolder](t, raw)) != 0 {
		t.Fatalf("want empty: %s", raw)
	}
}

func TestDocFolderCreate_RootAndChild(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	base := ts.URL + "/repos/" + repo.Prefix + "/doc-folders"

	resp, raw := apiPost(t, base, map[string]any{"name": "Design"})
	mustStatus(t, resp, raw, http.StatusCreated)
	root := decodeInto[model.DocFolder](t, raw)
	if root.ParentID != nil {
		t.Fatalf("absent parent_uuid must mean root: %+v", root)
	}

	resp, raw = apiPost(t, base, map[string]any{"name": "API", "parent_uuid": root.UUID})
	mustStatus(t, resp, raw, http.StatusCreated)
	child := decodeInto[model.DocFolder](t, raw)
	if child.ParentID == nil || *child.ParentID != root.ID {
		t.Fatalf("child not parented: %+v", child)
	}
	assertHistoryOps(t, s, []string{"doc_folder.create", "doc_folder.create"})
}

func TestDocFolderCreate_DuplicateSiblingIsConflict(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedDocFolder(t, s, repo, nil, "Design")
	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/doc-folders", map[string]any{"name": "Design"})
	mustStatus(t, resp, raw, http.StatusConflict)
}

func TestDocFolderCreate_ForeignParentIs404(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	other := seedRepo2(t, s)
	foreign := seedDocFolder(t, s, other, nil, "TheirFolder")

	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/doc-folders",
		map[string]any{"name": "Mine", "parent_uuid": foreign.UUID})
	mustStatus(t, resp, raw, http.StatusNotFound)

	// Nothing was created in either repo.
	for _, r := range []*model.Repo{repo, other} {
		folders, err := s.ListDocFolders(r.ID)
		if err != nil {
			t.Fatalf("ListDocFolders: %v", err)
		}
		if r.ID == repo.ID && len(folders) != 0 {
			t.Fatalf("leaked a folder into %s: %d", r.Prefix, len(folders))
		}
	}
}

func TestDocFolderCreate_DryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, raw := apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/doc-folders?dry_run=true",
		map[string]any{"name": "Design"})
	mustStatus(t, resp, raw, http.StatusCreated)
	if resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("missing X-Dry-Run")
	}
	folders, err := s.ListDocFolders(repo.ID)
	if err != nil {
		t.Fatalf("ListDocFolders: %v", err)
	}
	if len(folders) != 0 {
		t.Fatalf("dry run wrote %d folders", len(folders))
	}
}

// TestDocFolderPatch_PresenceMap is the core semantic test: which key is
// present decides the operation, and the other axis must be untouched.
func TestDocFolderPatch_PresenceMap(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	design := seedDocFolder(t, s, repo, nil, "Design")
	api1 := seedDocFolder(t, s, repo, &design.ID, "API")
	ops := ts.URL + "/repos/" + repo.Prefix + "/doc-folders/"

	// name present => rename only. Parent is untouched.
	resp, raw := apiPatch(t, ops+api1.UUID, map[string]any{"name": "HTTP"})
	mustStatus(t, resp, raw, http.StatusOK)
	got := decodeInto[model.DocFolder](t, raw)
	if got.Name != "HTTP" {
		t.Fatalf("rename: %+v", got)
	}
	if got.ParentID == nil || *got.ParentID != design.ID {
		t.Fatalf("rename moved the folder: %+v", got)
	}

	// parent_uuid present-but-EMPTY => move to the tree root. Name is
	// untouched. This is the empty-vs-absent case.
	resp, raw = apiPatch(t, ops+api1.UUID, map[string]any{"parent_uuid": ""})
	mustStatus(t, resp, raw, http.StatusOK)
	got = decodeInto[model.DocFolder](t, raw)
	if got.ParentID != nil {
		t.Fatalf("empty parent_uuid must mean root: %+v", got)
	}
	if got.Name != "HTTP" {
		t.Fatalf("move renamed the folder: %+v", got)
	}

	// parent_uuid present and non-empty => re-parent.
	resp, raw = apiPatch(t, ops+api1.UUID, map[string]any{"parent_uuid": design.UUID})
	mustStatus(t, resp, raw, http.StatusOK)
	got = decodeInto[model.DocFolder](t, raw)
	if got.ParentID == nil || *got.ParentID != design.ID {
		t.Fatalf("re-parent: %+v", got)
	}

	assertHistoryOps(t, s, []string{"doc_folder.rename", "doc_folder.move", "doc_folder.move"})
}

func TestDocFolderPatch_BothOrNeitherIs400(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	f := seedDocFolder(t, s, repo, nil, "Design")
	url := ts.URL + "/repos/" + repo.Prefix + "/doc-folders/" + f.UUID

	resp, raw := apiPatch(t, url, map[string]any{})
	mustStatus(t, resp, raw, http.StatusBadRequest)

	resp, raw = apiPatch(t, url, map[string]any{"name": "X", "parent_uuid": ""})
	mustStatus(t, resp, raw, http.StatusBadRequest)

	// An unknown key is rejected too — the strict decoder is the reason
	// a typo can't silently become a no-op.
	resp, raw = apiPatch(t, url, map[string]any{"nmae": "X"})
	mustStatus(t, resp, raw, http.StatusBadRequest)
}

func TestDocFolderPatch_ForeignUUIDIs404(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	other := seedRepo2(t, s)
	foreign := seedDocFolder(t, s, other, nil, "TheirFolder")

	resp, raw := apiPatch(t, ts.URL+"/repos/"+repo.Prefix+"/doc-folders/"+foreign.UUID,
		map[string]any{"name": "Hijacked"})
	mustStatus(t, resp, raw, http.StatusNotFound)

	still, err := s.GetDocFolderByUUID(foreign.UUID)
	if err != nil {
		t.Fatalf("GetDocFolderByUUID: %v", err)
	}
	if still.Name != "TheirFolder" {
		t.Fatalf("cross-repo rename went through: %+v", still)
	}
}

func TestDocFolderPatch_CycleIsConflict(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	parent := seedDocFolder(t, s, repo, nil, "Design")
	child := seedDocFolder(t, s, repo, &parent.ID, "API")

	resp, raw := apiPatch(t, ts.URL+"/repos/"+repo.Prefix+"/doc-folders/"+parent.UUID,
		map[string]any{"parent_uuid": child.UUID})
	mustStatus(t, resp, raw, http.StatusConflict)
}

func TestDocFolderDelete_DryRunPreviewThenDelete(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	design := seedDocFolder(t, s, repo, nil, "Design")
	apiF := seedDocFolder(t, s, repo, &design.ID, "API")
	doc := seedDocument(t, s, repo, "spec.md", model.DocTypeUserDocs, "body")
	if err := s.SetDocumentFolder(doc.ID, &apiF.ID, 0); err != nil {
		t.Fatalf("SetDocumentFolder: %v", err)
	}
	url := ts.URL + "/repos/" + repo.Prefix + "/doc-folders/" + design.UUID

	resp, raw := apiDelete(t, url+"?dry_run=true", nil)
	mustStatus(t, resp, raw, http.StatusOK)
	if resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("missing X-Dry-Run")
	}
	preview := decodeInto[struct {
		Folder  *model.DocFolder `json:"folder"`
		Path    string           `json:"path"`
		Cascade struct {
			Subfolders        int `json:"subfolders"`
			DocumentsReRooted int `json:"documents_re_rooted"`
		} `json:"cascade"`
		WouldDelete bool `json:"would_delete"`
	}](t, raw)
	if preview.Folder == nil || preview.Folder.UUID != design.UUID {
		t.Fatalf("preview folder: %s", raw)
	}
	if preview.Path != "Design" {
		t.Fatalf("preview path: %q", preview.Path)
	}
	if preview.Cascade.Subfolders != 1 || preview.Cascade.DocumentsReRooted != 1 {
		t.Fatalf("cascade: %+v", preview.Cascade)
	}
	if !preview.WouldDelete {
		t.Fatalf("would_delete false")
	}
	if _, err := s.GetDocFolderByUUID(design.UUID); err != nil {
		t.Fatalf("dry run deleted the folder: %v", err)
	}
	assertHistoryOps(t, s, nil)

	// The real thing: 204 and no body.
	resp, raw = apiDelete(t, url, nil)
	mustStatus(t, resp, raw, http.StatusNoContent)
	if len(raw) != 0 {
		t.Fatalf("204 carried a body: %s", raw)
	}
	folders, err := s.ListDocFolders(repo.ID)
	if err != nil {
		t.Fatalf("ListDocFolders: %v", err)
	}
	if len(folders) != 0 {
		t.Fatalf("subtree survived: %+v", folders)
	}
	// Pages are re-rooted, never deleted.
	after, err := s.GetDocumentByID(doc.ID, false)
	if err != nil {
		t.Fatalf("GetDocumentByID: %v", err)
	}
	if after.FolderID != nil {
		t.Fatalf("document not re-rooted: %+v", after)
	}
	assertHistoryOps(t, s, []string{"doc_folder.delete"})
}

func TestDocFolderDelete_ForeignUUIDIs404(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	other := seedRepo2(t, s)
	foreign := seedDocFolder(t, s, other, nil, "TheirFolder")

	resp, raw := apiDelete(t, ts.URL+"/repos/"+repo.Prefix+"/doc-folders/"+foreign.UUID, nil)
	mustStatus(t, resp, raw, http.StatusNotFound)
	if _, err := s.GetDocFolderByUUID(foreign.UUID); err != nil {
		t.Fatalf("cross-repo delete went through: %v", err)
	}
}

// ---------------------------------------------------------------------
// PUT /repos/{prefix}/documents/{filename}/folder
// ---------------------------------------------------------------------

func TestDocumentFolderSet_AbsentPositionAppends(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	folder := seedDocFolder(t, s, repo, nil, "Design")
	first := seedDocument(t, s, repo, "one.md", model.DocTypeUserDocs, "1")
	second := seedDocument(t, s, repo, "two.md", model.DocTypeUserDocs, "2")
	base := ts.URL + "/repos/" + repo.Prefix + "/documents/"

	resp, raw := apiPut(t, base+first.Filename+"/folder",
		map[string]any{"filename": first.Filename, "folder_uuid": folder.UUID})
	mustStatus(t, resp, raw, http.StatusOK)
	if got := decodeInto[model.Document](t, raw); got.FolderPosition != 0 {
		t.Fatalf("first append: %+v", got)
	}

	resp, raw = apiPut(t, base+second.Filename+"/folder",
		map[string]any{"filename": second.Filename, "folder_uuid": folder.UUID})
	mustStatus(t, resp, raw, http.StatusOK)
	got := decodeInto[model.Document](t, raw)
	if got.FolderPosition != 1 {
		t.Fatalf("second append should land after the first: %+v", got)
	}
	if got.FolderID == nil || *got.FolderID != folder.ID {
		t.Fatalf("folder: %+v", got)
	}
	assertHistoryOps(t, s, []string{"document.move", "document.move"})
}

func TestDocumentFolderSet_ExplicitPositionZero(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	folder := seedDocFolder(t, s, repo, nil, "Design")
	doc := seedDocument(t, s, repo, "one.md", model.DocTypeUserDocs, "1")

	// 0 is an ordinary target, not "unset" — the pointer on the wire is
	// what keeps the two distinguishable.
	resp, raw := apiPut(t, ts.URL+"/repos/"+repo.Prefix+"/documents/"+doc.Filename+"/folder",
		map[string]any{"filename": doc.Filename, "folder_uuid": folder.UUID, "position": 0})
	mustStatus(t, resp, raw, http.StatusOK)
	if got := decodeInto[model.Document](t, raw); got.FolderPosition != 0 {
		t.Fatalf("position: %+v", got)
	}

	resp, raw = apiPut(t, ts.URL+"/repos/"+repo.Prefix+"/documents/"+doc.Filename+"/folder",
		map[string]any{"filename": doc.Filename, "folder_uuid": folder.UUID, "position": -1})
	mustStatus(t, resp, raw, http.StatusBadRequest)
}

func TestDocumentFolderSet_EmptyFolderUUIDIsRoot(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	folder := seedDocFolder(t, s, repo, nil, "Design")
	doc := seedDocument(t, s, repo, "one.md", model.DocTypeUserDocs, "1")
	if err := s.SetDocumentFolder(doc.ID, &folder.ID, 0); err != nil {
		t.Fatalf("SetDocumentFolder: %v", err)
	}

	resp, raw := apiPut(t, ts.URL+"/repos/"+repo.Prefix+"/documents/"+doc.Filename+"/folder",
		map[string]any{"filename": doc.Filename, "folder_uuid": ""})
	mustStatus(t, resp, raw, http.StatusOK)
	if got := decodeInto[model.Document](t, raw); got.FolderID != nil {
		t.Fatalf("empty folder_uuid must mean the tree root: %+v", got)
	}
}

// TestDocumentFolderSet_URLWinsOverBody pins that the body's `filename`
// is decorative. The Kanban twin *needs* this (the remote client sends
// an unresolved key there); the two routes behave the same way so a
// reader doesn't have to remember which is which.
func TestDocumentFolderSet_URLWinsOverBody(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	folder := seedDocFolder(t, s, repo, nil, "Design")
	target := seedDocument(t, s, repo, "target.md", model.DocTypeUserDocs, "t")
	decoy := seedDocument(t, s, repo, "decoy.md", model.DocTypeUserDocs, "d")

	resp, raw := apiPut(t, ts.URL+"/repos/"+repo.Prefix+"/documents/"+target.Filename+"/folder",
		map[string]any{"filename": decoy.Filename, "folder_uuid": folder.UUID})
	mustStatus(t, resp, raw, http.StatusOK)
	if got := decodeInto[model.Document](t, raw); got.Filename != target.Filename {
		t.Fatalf("body filename won: %+v", got)
	}
	after, err := s.GetDocumentByID(decoy.ID, false)
	if err != nil {
		t.Fatalf("GetDocumentByID: %v", err)
	}
	if after.FolderID != nil {
		t.Fatalf("decoy was moved: %+v", after)
	}
}

func TestDocumentFolderSet_ForeignFolderIs404(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	other := seedRepo2(t, s)
	foreign := seedDocFolder(t, s, other, nil, "TheirFolder")
	doc := seedDocument(t, s, repo, "one.md", model.DocTypeUserDocs, "1")

	resp, raw := apiPut(t, ts.URL+"/repos/"+repo.Prefix+"/documents/"+doc.Filename+"/folder",
		map[string]any{"filename": doc.Filename, "folder_uuid": foreign.UUID})
	mustStatus(t, resp, raw, http.StatusNotFound)
	after, err := s.GetDocumentByID(doc.ID, false)
	if err != nil {
		t.Fatalf("GetDocumentByID: %v", err)
	}
	if after.FolderID != nil {
		t.Fatalf("document escaped into another repo's tree: %+v", after)
	}
}

func TestDocumentFolderSet_DryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	folder := seedDocFolder(t, s, repo, nil, "Design")
	doc := seedDocument(t, s, repo, "one.md", model.DocTypeUserDocs, "1")

	resp, raw := apiPut(t, ts.URL+"/repos/"+repo.Prefix+"/documents/"+doc.Filename+"/folder?dry_run=true",
		map[string]any{"filename": doc.Filename, "folder_uuid": folder.UUID})
	mustStatus(t, resp, raw, http.StatusOK)
	if resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("missing X-Dry-Run")
	}
	after, err := s.GetDocumentByID(doc.ID, false)
	if err != nil {
		t.Fatalf("GetDocumentByID: %v", err)
	}
	if after.FolderID != nil {
		t.Fatalf("dry run moved the document: %+v", after)
	}
}

// ---------------------------------------------------------------------
// kanban lanes
// ---------------------------------------------------------------------

func TestKanbanColumnsList_EmptyIsArray(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	resp, raw := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/kanban/columns")
	mustStatus(t, resp, raw, http.StatusOK)
	if strings.TrimSpace(string(raw)) == "null" {
		t.Fatalf("want [], got null")
	}
	if len(decodeInto[[]model.KanbanColumn](t, raw)) != 0 {
		t.Fatalf("want empty: %s", raw)
	}
}

func TestKanbanColumnCreate(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	base := ts.URL + "/repos/" + repo.Prefix + "/kanban/columns"

	resp, raw := apiPost(t, base, map[string]any{"name": "Doing"})
	mustStatus(t, resp, raw, http.StatusCreated)
	if got := decodeInto[model.KanbanColumn](t, raw); got.Position != 0 || got.Name != "Doing" {
		t.Fatalf("first lane: %+v", got)
	}
	resp, raw = apiPost(t, base, map[string]any{"name": "Done"})
	mustStatus(t, resp, raw, http.StatusCreated)
	if got := decodeInto[model.KanbanColumn](t, raw); got.Position != 1 {
		t.Fatalf("second lane appends to the right: %+v", got)
	}
	// Duplicate lane name is a collision, not a 500.
	resp, raw = apiPost(t, base, map[string]any{"name": "Done"})
	mustStatus(t, resp, raw, http.StatusConflict)

	assertHistoryOps(t, s, []string{"kanban_column.create", "kanban_column.create"})
}

func TestKanbanColumnPatch_PresenceMap(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	a := seedKanbanColumn(t, s, repo, "A")
	seedKanbanColumn(t, s, repo, "B")
	c := seedKanbanColumn(t, s, repo, "C")
	ops := ts.URL + "/repos/" + repo.Prefix + "/kanban/columns/"

	// name present => rename only.
	resp, raw := apiPatch(t, ops+a.UUID, map[string]any{"name": "Alpha"})
	mustStatus(t, resp, raw, http.StatusOK)
	got := decodeInto[model.KanbanColumn](t, raw)
	if got.Name != "Alpha" || got.Position != 0 {
		t.Fatalf("rename: %+v", got)
	}

	// position present => reorder only. 0 is a real target, and the
	// response is the moved lane alone (siblings re-densify underneath).
	resp, raw = apiPatch(t, ops+c.UUID, map[string]any{"position": 0})
	mustStatus(t, resp, raw, http.StatusOK)
	got = decodeInto[model.KanbanColumn](t, raw)
	if got.UUID != c.UUID || got.Position != 0 || got.Name != "C" {
		t.Fatalf("reorder: %+v", got)
	}
	cols, err := s.ListKanbanColumns(repo.ID)
	if err != nil {
		t.Fatalf("ListKanbanColumns: %v", err)
	}
	order := make([]string, 0, len(cols))
	for _, col := range cols {
		order = append(order, col.Name)
	}
	if strings.Join(order, ",") != "C,Alpha,B" {
		t.Fatalf("board order: %v", order)
	}
	assertHistoryOps(t, s, []string{"kanban_column.rename", "kanban_column.reorder"})
}

func TestKanbanColumnPatch_BothOrNeitherIs400(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	col := seedKanbanColumn(t, s, repo, "A")
	url := ts.URL + "/repos/" + repo.Prefix + "/kanban/columns/" + col.UUID

	resp, raw := apiPatch(t, url, map[string]any{})
	mustStatus(t, resp, raw, http.StatusBadRequest)

	resp, raw = apiPatch(t, url, map[string]any{"name": "X", "position": 0})
	mustStatus(t, resp, raw, http.StatusBadRequest)

	resp, raw = apiPatch(t, url, map[string]any{"position": -1})
	mustStatus(t, resp, raw, http.StatusBadRequest)
}

func TestKanbanColumnPatch_ForeignUUIDIs404(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	other := seedRepo2(t, s)
	foreign := seedKanbanColumn(t, s, other, "TheirLane")

	resp, raw := apiPatch(t, ts.URL+"/repos/"+repo.Prefix+"/kanban/columns/"+foreign.UUID,
		map[string]any{"name": "Hijacked"})
	mustStatus(t, resp, raw, http.StatusNotFound)
	still, err := s.GetKanbanColumnByUUID(foreign.UUID)
	if err != nil {
		t.Fatalf("GetKanbanColumnByUUID: %v", err)
	}
	if still.Name != "TheirLane" {
		t.Fatalf("cross-repo rename went through: %+v", still)
	}
}

func TestKanbanColumnDelete_DryRunPreviewThenDelete(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	lane := seedKanbanColumn(t, s, repo, "Doing")
	iss := seedIssue(t, s, repo, "card")
	if err := s.SetIssueKanbanColumn(iss.ID, &lane.ID, 0); err != nil {
		t.Fatalf("SetIssueKanbanColumn: %v", err)
	}
	url := ts.URL + "/repos/" + repo.Prefix + "/kanban/columns/" + lane.UUID

	resp, raw := apiDelete(t, url+"?dry_run=true", nil)
	mustStatus(t, resp, raw, http.StatusOK)
	if resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("missing X-Dry-Run")
	}
	preview := decodeInto[struct {
		Column  *model.KanbanColumn `json:"column"`
		Cascade struct {
			IssuesRemovedFromBoard int `json:"issues_removed_from_board"`
		} `json:"cascade"`
		WouldDelete bool `json:"would_delete"`
	}](t, raw)
	if preview.Column == nil || preview.Column.UUID != lane.UUID {
		t.Fatalf("preview column: %s", raw)
	}
	if preview.Cascade.IssuesRemovedFromBoard != 1 || !preview.WouldDelete {
		t.Fatalf("preview: %s", raw)
	}
	if _, err := s.GetKanbanColumnByUUID(lane.UUID); err != nil {
		t.Fatalf("dry run deleted the lane: %v", err)
	}

	resp, raw = apiDelete(t, url, nil)
	mustStatus(t, resp, raw, http.StatusNoContent)
	if len(raw) != 0 {
		t.Fatalf("204 carried a body: %s", raw)
	}
	// The card comes off the board; the issue itself survives.
	after, err := s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("GetIssueByID: %v", err)
	}
	if after.KanbanColumnID != nil {
		t.Fatalf("card still on a deleted lane: %+v", after)
	}
	assertHistoryOps(t, s, []string{"kanban_column.delete"})
}

func TestKanbanColumnDelete_ForeignUUIDIs404(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	other := seedRepo2(t, s)
	foreign := seedKanbanColumn(t, s, other, "TheirLane")

	resp, raw := apiDelete(t, ts.URL+"/repos/"+repo.Prefix+"/kanban/columns/"+foreign.UUID, nil)
	mustStatus(t, resp, raw, http.StatusNotFound)
	if _, err := s.GetKanbanColumnByUUID(foreign.UUID); err != nil {
		t.Fatalf("cross-repo delete went through: %v", err)
	}
}

// ---------------------------------------------------------------------
// PUT /repos/{prefix}/issues/{key}/kanban
// ---------------------------------------------------------------------

func TestIssueKanbanSet_AbsentPositionAppends(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	lane := seedKanbanColumn(t, s, repo, "Doing")
	first := seedIssue(t, s, repo, "first")
	second := seedIssue(t, s, repo, "second")

	resp, raw := apiPut(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+first.Key+"/kanban",
		map[string]any{"issue_key": first.Key, "column_uuid": lane.UUID})
	mustStatus(t, resp, raw, http.StatusOK)
	if got := decodeInto[model.Issue](t, raw); got.KanbanPosition != 0 {
		t.Fatalf("first append: %+v", got)
	}

	resp, raw = apiPut(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+second.Key+"/kanban",
		map[string]any{"issue_key": second.Key, "column_uuid": lane.UUID})
	mustStatus(t, resp, raw, http.StatusOK)
	got := decodeInto[model.Issue](t, raw)
	if got.KanbanPosition != 1 {
		t.Fatalf("second append should land at the bottom: %+v", got)
	}
	if got.KanbanColumnID == nil || *got.KanbanColumnID != lane.ID {
		t.Fatalf("lane: %+v", got)
	}
	assertHistoryOps(t, s, []string{"issue.kanban", "issue.kanban"})
}

func TestIssueKanbanSet_ExplicitPositionZeroIsTheTop(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	lane := seedKanbanColumn(t, s, repo, "Doing")
	first := seedIssue(t, s, repo, "first")
	second := seedIssue(t, s, repo, "second")
	if err := s.SetIssueKanbanColumn(first.ID, &lane.ID, 0); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, raw := apiPut(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+second.Key+"/kanban",
		map[string]any{"issue_key": second.Key, "column_uuid": lane.UUID, "position": 0})
	mustStatus(t, resp, raw, http.StatusOK)
	if got := decodeInto[model.Issue](t, raw); got.KanbanPosition != 0 {
		t.Fatalf("explicit 0 must be the top, not 'unset': %+v", got)
	}
	pushed, err := s.GetIssueByID(first.ID)
	if err != nil {
		t.Fatalf("GetIssueByID: %v", err)
	}
	if pushed.KanbanPosition != 1 {
		t.Fatalf("incumbent not pushed down: %+v", pushed)
	}
}

func TestIssueKanbanSet_EmptyColumnUUIDTakesTheCardOffTheBoard(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	lane := seedKanbanColumn(t, s, repo, "Doing")
	iss := seedIssue(t, s, repo, "card")
	if err := s.SetIssueKanbanColumn(iss.ID, &lane.ID, 0); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, raw := apiPut(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+iss.Key+"/kanban",
		map[string]any{"issue_key": iss.Key, "column_uuid": ""})
	mustStatus(t, resp, raw, http.StatusOK)
	if got := decodeInto[model.Issue](t, raw); got.KanbanColumnID != nil {
		t.Fatalf("empty column_uuid must mean off the board: %+v", got)
	}
	// The issue's pipeline state is a separate axis and must be untouched.
	after, err := s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("GetIssueByID: %v", err)
	}
	if after.State != model.StateTodo {
		t.Fatalf("kanban move changed the pipeline state: %q", after.State)
	}
}

// TestIssueKanbanSet_BodyKeyIgnored is not cosmetic: the remote client
// resolves a short reference to a full key for the URL but sends the
// UNRESOLVED value in the body, so the two legitimately differ and the
// handler must never read the body's.
func TestIssueKanbanSet_BodyKeyIgnored(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	lane := seedKanbanColumn(t, s, repo, "Doing")
	target := seedIssue(t, s, repo, "target")
	decoy := seedIssue(t, s, repo, "decoy")

	resp, raw := apiPut(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+target.Key+"/kanban",
		map[string]any{"issue_key": decoy.Key, "column_uuid": lane.UUID})
	mustStatus(t, resp, raw, http.StatusOK)
	if got := decodeInto[model.Issue](t, raw); got.Key != target.Key {
		t.Fatalf("body key won: %+v", got)
	}
	after, err := s.GetIssueByID(decoy.ID)
	if err != nil {
		t.Fatalf("GetIssueByID: %v", err)
	}
	if after.KanbanColumnID != nil {
		t.Fatalf("decoy was moved: %+v", after)
	}
}

func TestIssueKanbanSet_ForeignColumnIs404(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	other := seedRepo2(t, s)
	foreign := seedKanbanColumn(t, s, other, "TheirLane")
	iss := seedIssue(t, s, repo, "card")

	resp, raw := apiPut(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+iss.Key+"/kanban",
		map[string]any{"issue_key": iss.Key, "column_uuid": foreign.UUID})
	mustStatus(t, resp, raw, http.StatusNotFound)
	after, err := s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("GetIssueByID: %v", err)
	}
	if after.KanbanColumnID != nil {
		t.Fatalf("card landed on another repo's board: %+v", after)
	}
}

// A card addressed through the wrong repo's prefix is a 404 too — the
// issue-side twin of the foreign-uuid rule.
func TestIssueKanbanSet_ForeignIssueIs404(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	other := seedRepo2(t, s)
	lane := seedKanbanColumn(t, s, repo, "Doing")
	foreignIssue := seedIssue(t, s, other, "theirs")

	resp, raw := apiPut(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+foreignIssue.Key+"/kanban",
		map[string]any{"issue_key": foreignIssue.Key, "column_uuid": lane.UUID})
	mustStatus(t, resp, raw, http.StatusNotFound)
}

func TestIssueKanbanSet_DryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	lane := seedKanbanColumn(t, s, repo, "Doing")
	iss := seedIssue(t, s, repo, "card")

	resp, raw := apiPut(t, ts.URL+"/repos/"+repo.Prefix+"/issues/"+iss.Key+"/kanban?dry_run=true",
		map[string]any{"issue_key": iss.Key, "column_uuid": lane.UUID})
	mustStatus(t, resp, raw, http.StatusOK)
	if resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("missing X-Dry-Run")
	}
	if got := decodeInto[model.Issue](t, raw); got.KanbanColumnID == nil {
		t.Fatalf("projection should show the destination: %+v", got)
	}
	after, err := s.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("GetIssueByID: %v", err)
	}
	if after.KanbanColumnID != nil {
		t.Fatalf("dry run moved the card: %+v", after)
	}
	assertHistoryOps(t, s, nil)
}

// ---------------------------------------------------------------------
// unknown repo prefix
// ---------------------------------------------------------------------

func TestPivotRoutes_UnknownRepoIs404(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	for _, tc := range []struct {
		method string
		url    string
		body   any
	}{
		{http.MethodGet, "/repos/NOPE/doc-folders", nil},
		{http.MethodPost, "/repos/NOPE/doc-folders", map[string]any{"name": "X"}},
		{http.MethodGet, "/repos/NOPE/kanban/columns", nil},
		{http.MethodPost, "/repos/NOPE/kanban/columns", map[string]any{"name": "X"}},
	} {
		resp, raw := apiReq(t, tc.method, ts.URL+tc.url, tc.body, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s %s: want 404 got %d body=%s", tc.method, tc.url, resp.StatusCode, raw)
		}
	}
}
