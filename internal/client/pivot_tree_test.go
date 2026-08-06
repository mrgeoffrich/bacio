package client

// Client-layer coverage for the two uuid-addressed trees the pivot adds:
// doc folders and Kanban lanes. The store's own behaviour is covered in
// internal/store; what is tested here is the CLIENT contract three Phase-4
// transports code against — uuid addressing, the repo-ownership check on
// every mutator, the dry-run projections, and the nil-Position "append".

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// ---------- doc folders ----------

func TestDocFolderClientCRUD(t *testing.T) {
	ctx := context.Background()
	c, _ := openTestLocalClient(t)
	repo := seedRepo(t, c.store, "DOCS", t.TempDir())

	design, err := c.CreateDocFolder(ctx, repo, DocFolderCreateInput{Name: "Design"}, false)
	if err != nil {
		t.Fatalf("CreateDocFolder(Design): %v", err)
	}
	api, err := c.CreateDocFolder(ctx, repo, DocFolderCreateInput{Name: "API", ParentUUID: design.UUID}, false)
	if err != nil {
		t.Fatalf("CreateDocFolder(API): %v", err)
	}
	if api.ParentID == nil || *api.ParentID != design.ID {
		t.Fatalf("API was not nested under Design: %+v", api)
	}

	folders, err := c.ListDocFolders(ctx, repo)
	if err != nil {
		t.Fatalf("ListDocFolders: %v", err)
	}
	if len(folders) != 2 {
		t.Fatalf("ListDocFolders = %d rows, want 2", len(folders))
	}

	// The path helpers work off exactly what ListDocFolders returns —
	// that's the whole point of returning the tree flat and whole.
	hit, err := FindDocFolderByPath(folders, "Design/API")
	if err != nil {
		t.Fatalf("FindDocFolderByPath: %v", err)
	}
	if hit == nil || hit.UUID != api.UUID {
		t.Fatalf("FindDocFolderByPath resolved to %+v", hit)
	}
	if got := DocFolderDisplayPath(folders, hit); got != "Design/API" {
		t.Errorf("DocFolderDisplayPath = %q, want Design/API", got)
	}
	root, err := FindDocFolderByPath(folders, "")
	if err != nil || root != nil {
		t.Errorf("empty path should resolve to the (nil, nil) tree root; got %+v, %v", root, err)
	}
	if _, err := FindDocFolderByPath(folders, "Design/Missing"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing segment should wrap ErrNotFound; got %v", err)
	}

	renamed, err := c.RenameDocFolder(ctx, repo, api.UUID, "Auth", false)
	if err != nil {
		t.Fatalf("RenameDocFolder: %v", err)
	}
	if renamed.Name != "Auth" {
		t.Errorf("name = %q, want Auth", renamed.Name)
	}

	moved, err := c.MoveDocFolder(ctx, repo, DocFolderMoveInput{UUID: renamed.UUID}, false)
	if err != nil {
		t.Fatalf("MoveDocFolder to root: %v", err)
	}
	if moved.ParentID != nil {
		t.Errorf("empty NewParentUUID should re-root the folder; parent = %v", *moved.ParentID)
	}

	if _, err := c.MoveDocFolder(ctx, repo, DocFolderMoveInput{UUID: design.UUID, NewParentUUID: design.UUID}, false); err == nil {
		t.Error("moving a folder into itself should be refused")
	}
}

func TestDocFolderRejectsAnotherReposUUID(t *testing.T) {
	ctx := context.Background()
	c, _ := openTestLocalClient(t)
	mine := seedRepo(t, c.store, "MINE", t.TempDir())
	theirs := seedRepo(t, c.store, "THRS", t.TempDir())

	folder, err := c.CreateDocFolder(ctx, theirs, DocFolderCreateInput{Name: "Private"}, false)
	if err != nil {
		t.Fatalf("CreateDocFolder: %v", err)
	}
	// Reaching another repo's folder through THIS repo's prefix is the
	// exact hole the uuid namespace opens; every mutator must close it.
	for name, call := range map[string]func() error{
		"rename": func() error {
			_, err := c.RenameDocFolder(ctx, mine, folder.UUID, "Pwned", false)
			return err
		},
		"move": func() error {
			_, err := c.MoveDocFolder(ctx, mine, DocFolderMoveInput{UUID: folder.UUID}, false)
			return err
		},
		"delete": func() error {
			_, _, err := c.DeleteDocFolder(ctx, mine, folder.UUID, false)
			return err
		},
		"create_child": func() error {
			_, err := c.CreateDocFolder(ctx, mine, DocFolderCreateInput{Name: "Child", ParentUUID: folder.UUID}, false)
			return err
		},
	} {
		if err := call(); !errors.Is(err, store.ErrDocFolderOtherRepo) {
			t.Errorf("%s: want ErrDocFolderOtherRepo, got %v", name, err)
		}
	}
}

func TestDeleteDocFolderPreviewAndReRooting(t *testing.T) {
	ctx := context.Background()
	c, _ := openTestLocalClient(t)
	repo := seedRepo(t, c.store, "DELF", t.TempDir())

	design, err := c.CreateDocFolder(ctx, repo, DocFolderCreateInput{Name: "Design"}, false)
	if err != nil {
		t.Fatalf("CreateDocFolder: %v", err)
	}
	nested, err := c.CreateDocFolder(ctx, repo, DocFolderCreateInput{Name: "API", ParentUUID: design.UUID}, false)
	if err != nil {
		t.Fatalf("CreateDocFolder: %v", err)
	}
	for _, spec := range []struct{ filename, folderUUID string }{
		{"top.md", design.UUID},
		{"nested.md", nested.UUID},
	} {
		if _, err := c.CreateDocument(ctx, repo, DocCreateInput{
			Filename: spec.filename, Type: model.DocTypeUserDocs, Body: "body",
		}, false); err != nil {
			t.Fatalf("CreateDocument(%s): %v", spec.filename, err)
		}
		if _, err := c.MoveDocumentToFolder(ctx, repo, DocumentFolderMoveInput{
			Filename: spec.filename, FolderUUID: spec.folderUUID,
		}, false); err != nil {
			t.Fatalf("MoveDocumentToFolder(%s): %v", spec.filename, err)
		}
	}

	deleted, preview, err := c.DeleteDocFolder(ctx, repo, design.UUID, true)
	if err != nil {
		t.Fatalf("DeleteDocFolder(dry-run): %v", err)
	}
	if deleted != nil {
		t.Error("dry-run must not report a deleted folder")
	}
	if preview == nil {
		t.Fatal("dry-run returned no preview")
	}
	if preview.Cascade.Subfolders != 1 {
		t.Errorf("subfolders = %d, want 1", preview.Cascade.Subfolders)
	}
	if preview.Cascade.DocumentsReRooted != 2 {
		t.Errorf("documents_re_rooted = %d, want 2 (the whole subtree)", preview.Cascade.DocumentsReRooted)
	}
	if preview.Path != "Design" {
		t.Errorf("preview path = %q, want Design", preview.Path)
	}
	// Nothing written.
	if folders, lerr := c.ListDocFolders(ctx, repo); lerr != nil || len(folders) != 2 {
		t.Fatalf("dry-run mutated the tree: %d folders, %v", len(folders), lerr)
	}

	deleted, preview, err = c.DeleteDocFolder(ctx, repo, design.UUID, false)
	if err != nil {
		t.Fatalf("DeleteDocFolder: %v", err)
	}
	if preview != nil || deleted == nil || deleted.UUID != design.UUID {
		t.Fatalf("real delete returned (%+v, %+v)", deleted, preview)
	}
	folders, err := c.ListDocFolders(ctx, repo)
	if err != nil {
		t.Fatalf("ListDocFolders: %v", err)
	}
	if len(folders) != 0 {
		t.Errorf("child folder survived the parent delete: %+v", folders)
	}
	// Deleting a folder is organisational — the pages must survive, at
	// the root.
	docs, err := c.ListDocuments(ctx, repo, "", false)
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("documents = %d, want 2 survivors", len(docs))
	}
	for _, d := range docs {
		if d.FolderID != nil {
			t.Errorf("%s was not re-rooted", d.Filename)
		}
	}
}

func TestMoveDocumentToFolderAppends(t *testing.T) {
	ctx := context.Background()
	c, _ := openTestLocalClient(t)
	repo := seedRepo(t, c.store, "MVDO", t.TempDir())
	folder, err := c.CreateDocFolder(ctx, repo, DocFolderCreateInput{Name: "Notes"}, false)
	if err != nil {
		t.Fatalf("CreateDocFolder: %v", err)
	}

	var got []int
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		if _, err := c.CreateDocument(ctx, repo, DocCreateInput{
			Filename: name, Type: model.DocTypeUserDocs, Body: "x",
		}, false); err != nil {
			t.Fatalf("CreateDocument(%s): %v", name, err)
		}
		// nil Position == append after the folder's current members.
		moved, err := c.MoveDocumentToFolder(ctx, repo, DocumentFolderMoveInput{
			Filename: name, FolderUUID: folder.UUID,
		}, false)
		if err != nil {
			t.Fatalf("MoveDocumentToFolder(%s): %v", name, err)
		}
		got = append(got, moved.FolderPosition)
	}
	for i, pos := range got {
		if pos != i {
			t.Errorf("doc %d landed at position %d, want %d", i, pos, i)
		}
	}

	// An explicit position wins, and "" moves the page back to the root.
	pinned := 0
	rerooted, err := c.MoveDocumentToFolder(ctx, repo, DocumentFolderMoveInput{
		Filename: "c.md", FolderUUID: "", Position: &pinned,
	}, false)
	if err != nil {
		t.Fatalf("MoveDocumentToFolder(root): %v", err)
	}
	if rerooted.FolderID != nil {
		t.Errorf("empty FolderUUID should re-root the page")
	}
	if rerooted.FolderPosition != 0 {
		t.Errorf("explicit position ignored: %d", rerooted.FolderPosition)
	}
}

// ---------- kanban lanes ----------

func TestKanbanColumnClientCRUD(t *testing.T) {
	ctx := context.Background()
	c, _ := openTestLocalClient(t)
	repo := seedBootstrappedRepo(t, c, "KANB")

	lanes, err := c.ListKanbanColumns(ctx, repo)
	if err != nil {
		t.Fatalf("ListKanbanColumns: %v", err)
	}
	starter := len(lanes)
	if starter == 0 {
		t.Fatal("expected a bootstrapped starter board")
	}

	added, err := c.CreateKanbanColumn(ctx, repo, "Blocked", false)
	if err != nil {
		t.Fatalf("CreateKanbanColumn: %v", err)
	}
	if added.Position != starter {
		t.Errorf("new lane position = %d, want %d (appended)", added.Position, starter)
	}

	renamed, err := c.RenameKanbanColumn(ctx, repo, added.UUID, "On hold", false)
	if err != nil {
		t.Fatalf("RenameKanbanColumn: %v", err)
	}
	if renamed.Name != "On hold" {
		t.Errorf("name = %q", renamed.Name)
	}

	moved, err := c.ReorderKanbanColumn(ctx, repo, renamed.UUID, 0, false)
	if err != nil {
		t.Fatalf("ReorderKanbanColumn: %v", err)
	}
	if moved.Position != 0 {
		t.Errorf("position = %d, want 0", moved.Position)
	}
	// Positions stay dense 0..n-1 across the whole board.
	after, err := c.ListKanbanColumns(ctx, repo)
	if err != nil {
		t.Fatalf("ListKanbanColumns: %v", err)
	}
	for i, lane := range after {
		if lane.Position != i {
			t.Errorf("lane %q at position %d, want %d", lane.Name, lane.Position, i)
		}
	}

	// A reorder past the end clamps rather than erroring.
	last, err := c.ReorderKanbanColumn(ctx, repo, renamed.UUID, 999, false)
	if err != nil {
		t.Fatalf("ReorderKanbanColumn(999): %v", err)
	}
	if last.Position != len(after)-1 {
		t.Errorf("clamped position = %d, want %d", last.Position, len(after)-1)
	}

	if _, err := c.CreateKanbanColumn(ctx, repo, "On hold", false); err == nil {
		t.Error("duplicate lane name should be refused")
	}
	if _, err := c.CreateKanbanColumn(ctx, repo, "bad/name", false); err == nil {
		t.Error("a '/' in a lane name should be refused by the validator")
	}
}

func TestKanbanColumnRejectsAnotherReposUUID(t *testing.T) {
	ctx := context.Background()
	c, _ := openTestLocalClient(t)
	mine := seedBootstrappedRepo(t, c, "MINK")
	theirs := seedBootstrappedRepo(t, c, "THRK")

	lanes, err := c.ListKanbanColumns(ctx, theirs)
	if err != nil {
		t.Fatalf("ListKanbanColumns: %v", err)
	}
	target := lanes[0].UUID
	for name, call := range map[string]func() error{
		"rename": func() error {
			_, err := c.RenameKanbanColumn(ctx, mine, target, "Pwned", false)
			return err
		},
		"reorder": func() error {
			_, err := c.ReorderKanbanColumn(ctx, mine, target, 0, false)
			return err
		},
		"delete": func() error {
			_, _, err := c.DeleteKanbanColumn(ctx, mine, target, false)
			return err
		},
	} {
		err := call()
		if err == nil || !strings.Contains(err.Error(), "not in repo") {
			t.Errorf("%s: want a cross-repo refusal, got %v", name, err)
		}
	}
}

func TestMoveIssueToKanbanColumn(t *testing.T) {
	ctx := context.Background()
	c, _ := openTestLocalClient(t)
	repo := seedBootstrappedRepo(t, c, "MOVK")
	lanes, err := c.ListKanbanColumns(ctx, repo)
	if err != nil {
		t.Fatalf("ListKanbanColumns: %v", err)
	}

	var keys []string
	for _, title := range []string{"one", "two"} {
		iss, cerr := c.CreateIssue(ctx, repo, inputs.IssueAddInput{Title: title}, false)
		if cerr != nil {
			t.Fatalf("CreateIssue: %v", cerr)
		}
		if iss.KanbanColumnID != nil {
			t.Fatal("a git-repo card must start off the board")
		}
		keys = append(keys, iss.Key)
	}

	// nil Position appends.
	for i, key := range keys {
		moved, merr := c.MoveIssueToKanbanColumn(ctx, repo, IssueKanbanMoveInput{
			IssueKey: key, ColumnUUID: lanes[0].UUID,
		}, false)
		if merr != nil {
			t.Fatalf("MoveIssueToKanbanColumn(%s): %v", key, merr)
		}
		if moved.KanbanColumnID == nil || *moved.KanbanColumnID != lanes[0].ID {
			t.Fatalf("%s did not land in the target lane", key)
		}
		if moved.KanbanPosition != i {
			t.Errorf("%s: position = %d, want %d", key, moved.KanbanPosition, i)
		}
	}

	// An explicit 0 jumps the second card to the top and re-densifies.
	top := 0
	if _, err := c.MoveIssueToKanbanColumn(ctx, repo, IssueKanbanMoveInput{
		IssueKey: keys[1], ColumnUUID: lanes[0].UUID, Position: &top,
	}, false); err != nil {
		t.Fatalf("MoveIssueToKanbanColumn(top): %v", err)
	}
	first, err := c.GetIssueByKey(ctx, repo, keys[0])
	if err != nil {
		t.Fatalf("GetIssueByKey: %v", err)
	}
	if first.KanbanPosition != 1 {
		t.Errorf("displaced card position = %d, want 1", first.KanbanPosition)
	}

	// An empty ColumnUUID takes the card off the board entirely — the
	// only way to un-opt a git repo's card.
	off, err := c.MoveIssueToKanbanColumn(ctx, repo, IssueKanbanMoveInput{IssueKey: keys[0]}, false)
	if err != nil {
		t.Fatalf("MoveIssueToKanbanColumn(off): %v", err)
	}
	if off.KanbanColumnID != nil {
		t.Errorf("card is still on the board")
	}
}

func TestDeleteKanbanColumnPreview(t *testing.T) {
	ctx := context.Background()
	c, _ := openTestLocalClient(t)
	ws := seedWorkspace(t, c, "Board")
	lanes, err := c.ListKanbanColumns(ctx, ws)
	if err != nil {
		t.Fatalf("ListKanbanColumns: %v", err)
	}
	for _, title := range []string{"a", "b", "c"} {
		if _, cerr := c.CreateIssue(ctx, ws, inputs.IssueAddInput{Title: title}, false); cerr != nil {
			t.Fatalf("CreateIssue: %v", cerr)
		}
	}

	deleted, preview, err := c.DeleteKanbanColumn(ctx, ws, lanes[0].UUID, true)
	if err != nil {
		t.Fatalf("DeleteKanbanColumn(dry-run): %v", err)
	}
	if deleted != nil || preview == nil {
		t.Fatalf("dry-run returned (%+v, %+v)", deleted, preview)
	}
	if preview.Cascade.IssuesRemovedFromBoard != 3 {
		t.Errorf("cascade = %d, want 3", preview.Cascade.IssuesRemovedFromBoard)
	}
	if remaining, lerr := c.ListKanbanColumns(ctx, ws); lerr != nil || len(remaining) != len(lanes) {
		t.Fatalf("dry-run mutated the board: %d lanes, %v", len(remaining), lerr)
	}

	if _, _, err := c.DeleteKanbanColumn(ctx, ws, lanes[0].UUID, false); err != nil {
		t.Fatalf("DeleteKanbanColumn: %v", err)
	}
	// The issues survive; only their board membership is gone.
	issues, err := c.store.ListIssues(store.IssueFilter{RepoID: &ws.ID})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 3 {
		t.Fatalf("issues = %d, want 3 survivors", len(issues))
	}
	for _, iss := range issues {
		if iss.KanbanColumnID != nil {
			t.Errorf("%s is still on the board", iss.Key)
		}
	}
}
