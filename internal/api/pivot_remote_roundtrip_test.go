package api_test

// The pivot's remote client driven against the live HTTP surface.
//
// This is the test that catches a route-shape mistake. The remote
// implementations in internal/client were written against a *written-down*
// URL table before this handler package existed, so a typo'd segment, a
// wrong verb, a status the client can't decode or a body key that doesn't
// line up would compile cleanly on both sides and only fail at runtime in
// web mode. Exercising every new Client method over real HTTP is the only
// thing that proves the two halves agree.
//
// It lives here rather than in internal/client because these assertions
// are about the *server's* contract; internal/client's own round-trip
// harness stays focused on local-vs-remote behavioural parity.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// newRemotePair spins the api over a temp-DB store and returns a remote
// client pointed at it, plus the store for direct assertions.
func newRemotePair(t *testing.T) (client.Client, *store.Store, *model.Repo) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	repo, err := s.CreateRepo("RMTE", "remote-repo", t.TempDir(), "")
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	srv := httptest.NewServer(api.New(s, api.Options{DBPath: dbPath},
		slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	t.Cleanup(srv.Close)

	remote, err := client.Open(context.Background(), client.Options{Remote: srv.URL, Actor: "tester"})
	if err != nil {
		t.Fatalf("client.Open(remote): %v", err)
	}
	t.Cleanup(func() { _ = remote.Close() })
	return remote, s, repo
}

func TestRemoteRoundTrip_Workspace(t *testing.T) {
	ctx := context.Background()
	remote, s, _ := newRemotePair(t)

	projected, err := remote.CreateWorkspace(ctx, client.WorkspaceCreateInput{Name: "Ideas"}, true)
	if err != nil {
		t.Fatalf("CreateWorkspace(dry): %v", err)
	}
	if projected.Kind != model.RepoKindWorkspace {
		t.Fatalf("dry-run kind: %q", projected.Kind)
	}

	ws, err := remote.CreateWorkspace(ctx, client.WorkspaceCreateInput{Name: "Ideas", Prefix: "IDEA"}, false)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if ws.Prefix != "IDEA" || ws.Kind != model.RepoKindWorkspace || ws.Path != "" {
		t.Fatalf("workspace: %+v", ws)
	}
	stored, err := s.GetRepoByPrefix("IDEA")
	if err != nil {
		t.Fatalf("GetRepoByPrefix: %v", err)
	}
	if !stored.IsWorkspace() {
		t.Fatalf("stored row is not a workspace: %+v", stored)
	}
}

func TestRemoteRoundTrip_DocFolders(t *testing.T) {
	ctx := context.Background()
	remote, s, repo := newRemotePair(t)

	folders, err := remote.ListDocFolders(ctx, repo)
	if err != nil {
		t.Fatalf("ListDocFolders: %v", err)
	}
	if len(folders) != 0 {
		t.Fatalf("expected an empty tree, got %d", len(folders))
	}

	design, err := remote.CreateDocFolder(ctx, repo, client.DocFolderCreateInput{Name: "Design"}, false)
	if err != nil {
		t.Fatalf("CreateDocFolder: %v", err)
	}
	child, err := remote.CreateDocFolder(ctx, repo,
		client.DocFolderCreateInput{Name: "API", ParentUUID: design.UUID}, false)
	if err != nil {
		t.Fatalf("CreateDocFolder(child): %v", err)
	}

	renamed, err := remote.RenameDocFolder(ctx, repo, child.UUID, "HTTP", false)
	if err != nil {
		t.Fatalf("RenameDocFolder: %v", err)
	}
	if renamed.Name != "HTTP" || renamed.ParentID == nil || *renamed.ParentID != design.ID {
		t.Fatalf("rename must not move: %+v", renamed)
	}

	// Empty NewParentUUID is the tree root — the case a naive
	// omitempty-on-string wire shape would silently lose.
	moved, err := remote.MoveDocFolder(ctx, repo,
		client.DocFolderMoveInput{UUID: child.UUID, NewParentUUID: ""}, false)
	if err != nil {
		t.Fatalf("MoveDocFolder(root): %v", err)
	}
	if moved.ParentID != nil {
		t.Fatalf("move to root: %+v", moved)
	}
	if moved.Name != "HTTP" {
		t.Fatalf("move must not rename: %+v", moved)
	}

	// Documents: absent Position appends.
	doc, err := s.CreateDocument(repo.ID, "spec.md", model.DocTypeUserDocs, "body", "")
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	placed, err := remote.MoveDocumentToFolder(ctx, repo,
		client.DocumentFolderMoveInput{Filename: doc.Filename, FolderUUID: design.UUID}, false)
	if err != nil {
		t.Fatalf("MoveDocumentToFolder: %v", err)
	}
	if placed.FolderID == nil || *placed.FolderID != design.ID || placed.FolderPosition != 0 {
		t.Fatalf("placed: %+v", placed)
	}
	rooted, err := remote.MoveDocumentToFolder(ctx, repo,
		client.DocumentFolderMoveInput{Filename: doc.Filename, FolderUUID: ""}, false)
	if err != nil {
		t.Fatalf("MoveDocumentToFolder(root): %v", err)
	}
	if rooted.FolderID != nil {
		t.Fatalf("empty folder_uuid must root the page: %+v", rooted)
	}

	// Dry-run delete answers the preview body; the real one answers 204
	// and the client back-fills the row from the list route.
	_, preview, err := remote.DeleteDocFolder(ctx, repo, design.UUID, true)
	if err != nil {
		t.Fatalf("DeleteDocFolder(dry): %v", err)
	}
	if preview == nil || preview.Folder == nil || !preview.WouldDelete {
		t.Fatalf("preview: %+v", preview)
	}
	deleted, preview, err := remote.DeleteDocFolder(ctx, repo, design.UUID, false)
	if err != nil {
		t.Fatalf("DeleteDocFolder: %v", err)
	}
	if preview != nil || deleted == nil || deleted.Name != "Design" {
		t.Fatalf("delete: deleted=%+v preview=%+v", deleted, preview)
	}
	if _, err := s.GetDocFolderByUUID(design.UUID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("folder survived the delete: %v", err)
	}
}

func TestRemoteRoundTrip_Kanban(t *testing.T) {
	ctx := context.Background()
	remote, s, repo := newRemotePair(t)

	if cols, err := remote.ListKanbanColumns(ctx, repo); err != nil || len(cols) != 0 {
		t.Fatalf("ListKanbanColumns: %v / %d", err, len(cols))
	}

	doing, err := remote.CreateKanbanColumn(ctx, repo, "Doing", false)
	if err != nil {
		t.Fatalf("CreateKanbanColumn: %v", err)
	}
	done, err := remote.CreateKanbanColumn(ctx, repo, "Done", false)
	if err != nil {
		t.Fatalf("CreateKanbanColumn: %v", err)
	}

	renamed, err := remote.RenameKanbanColumn(ctx, repo, doing.UUID, "In Progress", false)
	if err != nil {
		t.Fatalf("RenameKanbanColumn: %v", err)
	}
	if renamed.Name != "In Progress" || renamed.Position != 0 {
		t.Fatalf("rename must not reorder: %+v", renamed)
	}
	// Position 0 over the wire — the value a non-pointer int would make
	// indistinguishable from "absent".
	reordered, err := remote.ReorderKanbanColumn(ctx, repo, done.UUID, 0, false)
	if err != nil {
		t.Fatalf("ReorderKanbanColumn: %v", err)
	}
	if reordered.Position != 0 || reordered.Name != "Done" {
		t.Fatalf("reorder must not rename: %+v", reordered)
	}

	iss, err := s.CreateIssue(repo.ID, nil, "card", "", model.StateTodo, nil, "", "")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	onBoard, err := remote.MoveIssueToKanbanColumn(ctx, repo,
		client.IssueKanbanMoveInput{IssueKey: iss.Key, ColumnUUID: done.UUID}, false)
	if err != nil {
		t.Fatalf("MoveIssueToKanbanColumn: %v", err)
	}
	if onBoard.KanbanColumnID == nil || *onBoard.KanbanColumnID != done.ID {
		t.Fatalf("on board: %+v", onBoard)
	}
	// The remote impl resolves a BARE number for the URL while sending the
	// unresolved value in the body — the case that forces the handler to
	// treat the URL as authoritative.
	bare := iss.Key[len(repo.Prefix)+1:]
	offBoard, err := remote.MoveIssueToKanbanColumn(ctx, repo,
		client.IssueKanbanMoveInput{IssueKey: bare, ColumnUUID: ""}, false)
	if err != nil {
		t.Fatalf("MoveIssueToKanbanColumn(off, bare key): %v", err)
	}
	if offBoard.Key != iss.Key || offBoard.KanbanColumnID != nil {
		t.Fatalf("off board: %+v", offBoard)
	}

	_, preview, err := remote.DeleteKanbanColumn(ctx, repo, done.UUID, true)
	if err != nil {
		t.Fatalf("DeleteKanbanColumn(dry): %v", err)
	}
	if preview == nil || preview.Column == nil || !preview.WouldDelete {
		t.Fatalf("preview: %+v", preview)
	}
	deleted, preview, err := remote.DeleteKanbanColumn(ctx, repo, done.UUID, false)
	if err != nil {
		t.Fatalf("DeleteKanbanColumn: %v", err)
	}
	if preview != nil || deleted == nil || deleted.Name != "Done" {
		t.Fatalf("delete: deleted=%+v preview=%+v", deleted, preview)
	}
}

// A uuid from another repo must be a not-found over the wire too — the
// remote client rehydrates a 404 into store.ErrNotFound, so a caller that
// branches on errors.Is keeps working across transports.
func TestRemoteRoundTrip_CrossRepoUUIDIsNotFound(t *testing.T) {
	ctx := context.Background()
	remote, s, repo := newRemotePair(t)

	other, err := s.CreateRepo("OTHR", "other", t.TempDir(), "")
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	foreignFolder, err := s.CreateDocFolder(other.ID, nil, "Theirs")
	if err != nil {
		t.Fatalf("CreateDocFolder: %v", err)
	}
	foreignLane, err := s.CreateKanbanColumn(other.ID, "TheirLane")
	if err != nil {
		t.Fatalf("CreateKanbanColumn: %v", err)
	}

	if _, err := remote.RenameDocFolder(ctx, repo, foreignFolder.UUID, "Hijacked", false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RenameDocFolder cross-repo: %v", err)
	}
	if _, err := remote.RenameKanbanColumn(ctx, repo, foreignLane.UUID, "Hijacked", false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RenameKanbanColumn cross-repo: %v", err)
	}
}
