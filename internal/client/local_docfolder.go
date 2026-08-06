package client

import (
	"context"
	"fmt"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// Doc folders — the organisational tree over a repo's documents.
//
// Two invariants shape every method here:
//
//   - Folders are addressed by **uuid**, never by int64 id. The HTTP
//     surface has no way to hand out ids, the uuid is what the sync
//     record layout keys on, and it is stable across machines. Each
//     mutator therefore resolves uuid → row and re-checks that the row
//     belongs to `repo` before touching it: without that check a caller
//     holding any folder uuid could mutate another repo's tree through
//     this repo's prefix.
//   - Folder membership is NOT part of a document's synced form. Moving a
//     page changes no byte of doc.yaml; the store bumps the affected
//     FOLDERS' updated_at instead. Don't "fix" the missing
//     documents.updated_at bump.

func (c *localClient) ListDocFolders(ctx context.Context, repo *model.Repo) ([]*model.DocFolder, error) {
	folders, err := c.store.ListDocFolders(repo.ID)
	if err != nil {
		return nil, err
	}
	if folders == nil {
		folders = []*model.DocFolder{}
	}
	return folders, nil
}

func (c *localClient) CreateDocFolder(ctx context.Context, repo *model.Repo, in DocFolderCreateInput, dryRun bool) (*model.DocFolder, error) {
	// Rehearse the store-boundary validator so a dry-run rejects exactly
	// what the real call would.
	name, err := store.ValidateFolderName(in.Name)
	if err != nil {
		return nil, err
	}
	var parentID *int64
	if in.ParentUUID != "" {
		parent, perr := c.docFolderInRepo(repo, in.ParentUUID)
		if perr != nil {
			return nil, perr
		}
		parentID = &parent.ID
	}
	if dryRun {
		return &model.DocFolder{
			RepoID:   repo.ID,
			ParentID: parentID,
			Name:     name,
		}, nil
	}
	folder, err := c.store.CreateDocFolder(repo.ID, parentID, name)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "doc_folder.create", Kind: "doc_folder",
		TargetID: &folder.ID, TargetLabel: folder.Name,
		Details: "path=" + c.folderDisplayPath(folder.ID),
	})
	return folder, nil
}

func (c *localClient) RenameDocFolder(ctx context.Context, repo *model.Repo, uuid, newName string, dryRun bool) (*model.DocFolder, error) {
	folder, err := c.docFolderInRepo(repo, uuid)
	if err != nil {
		return nil, err
	}
	name, err := store.ValidateFolderName(newName)
	if err != nil {
		return nil, err
	}
	if dryRun {
		projected := *folder
		projected.Name = name
		return &projected, nil
	}
	oldName := folder.Name
	if err := c.store.RenameDocFolder(folder.ID, name); err != nil {
		return nil, err
	}
	updated, err := c.store.GetDocFolderByID(folder.ID)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "doc_folder.rename", Kind: "doc_folder",
		TargetID: &updated.ID, TargetLabel: updated.Name,
		Details: fmt.Sprintf("%s → %s", oldName, updated.Name),
	})
	return updated, nil
}

func (c *localClient) MoveDocFolder(ctx context.Context, repo *model.Repo, in DocFolderMoveInput, dryRun bool) (*model.DocFolder, error) {
	folder, err := c.docFolderInRepo(repo, in.UUID)
	if err != nil {
		return nil, err
	}
	var newParentID *int64
	if in.NewParentUUID != "" {
		parent, perr := c.docFolderInRepo(repo, in.NewParentUUID)
		if perr != nil {
			return nil, perr
		}
		if parent.ID == folder.ID {
			return nil, fmt.Errorf("%w: %s", store.ErrDocFolderCycle, folder.Name)
		}
		newParentID = &parent.ID
	}
	if dryRun {
		// The cycle / depth guards live inside the store transaction, so
		// a rehearsal can't prove the move is legal without doing it.
		// Project the intent; the commit path is still the gate.
		projected := *folder
		projected.ParentID = newParentID
		return &projected, nil
	}
	from := c.folderDisplayPath(folder.ID)
	if err := c.store.MoveDocFolder(folder.ID, newParentID); err != nil {
		return nil, err
	}
	updated, err := c.store.GetDocFolderByID(folder.ID)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "doc_folder.move", Kind: "doc_folder",
		TargetID: &updated.ID, TargetLabel: updated.Name,
		Details: fmt.Sprintf("%s → %s", from, c.folderDisplayPath(updated.ID)),
	})
	return updated, nil
}

func (c *localClient) DeleteDocFolder(ctx context.Context, repo *model.Repo, uuid string, dryRun bool) (*model.DocFolder, *DocFolderDeletePreview, error) {
	folder, err := c.docFolderInRepo(repo, uuid)
	if err != nil {
		return nil, nil, err
	}
	if dryRun {
		preview, perr := c.docFolderDeletePreview(repo, folder)
		if perr != nil {
			return nil, nil, perr
		}
		return nil, preview, nil
	}
	path := c.folderDisplayPath(folder.ID)
	if err := c.store.DeleteDocFolder(folder.ID); err != nil {
		return nil, nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "doc_folder.delete", Kind: "doc_folder",
		TargetID: &folder.ID, TargetLabel: folder.Name,
		Details: "path=" + path,
	})
	return folder, nil, nil
}

func (c *localClient) MoveDocumentToFolder(ctx context.Context, repo *model.Repo, in DocumentFolderMoveInput, dryRun bool) (*model.Document, error) {
	doc, err := c.store.GetDocumentByFilename(repo.ID, in.Filename, false)
	if err != nil {
		return nil, err
	}
	var (
		folderID *int64
		scope    = store.RootFolder()
		target   = "/"
	)
	if in.FolderUUID != "" {
		folder, ferr := c.docFolderInRepo(repo, in.FolderUUID)
		if ferr != nil {
			return nil, ferr
		}
		folderID = &folder.ID
		scope = store.InFolder(folder.ID)
		target = c.folderDisplayPath(folder.ID)
	}
	position, err := c.docFolderAppendPosition(repo, scope, in.Position)
	if err != nil {
		return nil, err
	}
	if dryRun {
		projected := *doc
		projected.FolderID = folderID
		projected.FolderPosition = position
		return &projected, nil
	}
	if err := c.store.SetDocumentFolder(doc.ID, folderID, position); err != nil {
		return nil, err
	}
	updated, err := c.store.GetDocumentByID(doc.ID, false)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "document.move", Kind: "document",
		TargetID: &updated.ID, TargetLabel: updated.Filename,
		Details: "folder=" + target,
	})
	return updated, nil
}

// ---------------------------------------------------------------------
// Internals
// ---------------------------------------------------------------------

// docFolderInRepo resolves a folder uuid and asserts it belongs to repo.
// Every uuid-addressed mutator goes through this: the uuid namespace is
// global, so without the repo check a caller could reach into another
// repo's tree through this repo's prefix (and, over HTTP, past whatever
// per-repo authorisation the route applied).
func (c *localClient) docFolderInRepo(repo *model.Repo, uuid string) (*model.DocFolder, error) {
	if strings.TrimSpace(uuid) == "" {
		return nil, fmt.Errorf("folder uuid is required")
	}
	folder, err := c.store.GetDocFolderByUUID(uuid)
	if err != nil {
		return nil, err
	}
	if folder.RepoID != repo.ID {
		return nil, fmt.Errorf("%w: folder %s is not in repo %s", store.ErrDocFolderOtherRepo, uuid, repo.Prefix)
	}
	return folder, nil
}

// folderDisplayPath renders a folder's breadcrumb for the audit log.
// Best-effort: the audit detail is never worth failing a mutation over,
// so a resolution failure degrades to the bare id.
func (c *localClient) folderDisplayPath(id int64) string {
	fp, err := c.store.ResolveFolderPath(id)
	if err != nil || fp == nil {
		return fmt.Sprintf("#%d", id)
	}
	return fp.Display
}

// docFolderAppendPosition resolves the nil-able Position on a move.
// Explicit wins; nil appends after the target folder's current members.
//
// Doc-folder positions are a loose SORT KEY (not the dense 0-based index
// the Kanban uses), so "append" is max+1 rather than "the length of the
// slice" — two siblings may legitimately share a position.
func (c *localClient) docFolderAppendPosition(repo *model.Repo, scope store.DocFolderScope, want *int) (int, error) {
	if want != nil {
		if *want < 0 {
			return 0, fmt.Errorf("position must be >= 0, got %d", *want)
		}
		return *want, nil
	}
	siblings, err := c.store.ListDocuments(store.DocumentFilter{
		RepoID:          repo.ID,
		IncludeArchived: true,
		Folder:          scope,
	})
	if err != nil {
		return 0, err
	}
	next := 0
	for _, d := range siblings {
		if d.FolderPosition >= next {
			next = d.FolderPosition + 1
		}
	}
	return next, nil
}

// docFolderDeletePreview computes the blast radius of deleting a folder:
// every descendant folder goes with it, and every document anywhere in
// that subtree is re-rooted (never deleted).
func (c *localClient) docFolderDeletePreview(repo *model.Repo, folder *model.DocFolder) (*DocFolderDeletePreview, error) {
	folders, err := c.store.ListDocFolders(repo.ID)
	if err != nil {
		return nil, err
	}
	subtree := docFolderSubtreeIDs(folders, folder.ID)
	docs, err := c.store.ListDocuments(store.DocumentFilter{RepoID: repo.ID, IncludeArchived: true})
	if err != nil {
		return nil, err
	}
	reRooted := 0
	for _, d := range docs {
		if d.FolderID != nil && subtree[*d.FolderID] {
			reRooted++
		}
	}
	return &DocFolderDeletePreview{
		Folder: folder,
		Path:   c.folderDisplayPath(folder.ID),
		Cascade: DocFolderCascadeCount{
			// The folder itself is not one of its own subfolders.
			Subfolders:        len(subtree) - 1,
			DocumentsReRooted: reRooted,
		},
		WouldDelete: true,
	}, nil
}

// docFolderSubtreeIDs returns rootID plus every descendant id, walking
// the flat list breadth-first. The store's list is not guaranteed
// parent-before-child (a move can leave a child with a lower id), so
// this walks a children index rather than assuming an order.
func docFolderSubtreeIDs(folders []*model.DocFolder, rootID int64) map[int64]bool {
	children := make(map[int64][]int64, len(folders))
	for _, f := range folders {
		if f.ParentID != nil {
			children[*f.ParentID] = append(children[*f.ParentID], f.ID)
		}
	}
	out := map[int64]bool{rootID: true}
	queue := []int64{rootID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range children[cur] {
			if out[child] {
				continue
			}
			out[child] = true
			queue = append(queue, child)
		}
	}
	return out
}

// FindDocFolderByPath resolves a slash-separated display path
// ("Design/API/Auth") against the flat slice ListDocFolders returns.
//
// It is the transport-agnostic twin of store.FolderByPath: the CLI and
// the HTTP handlers both need to turn a human-typed path into a folder,
// and only the local backend can reach the store. One ListDocFolders
// round trip plus this walk works identically on both backends.
//
// The EMPTY path (or "/", or only separators) is the tree ROOT, which is
// not a folder: it returns (nil, nil), exactly as store.FolderByPath
// does, so the result feeds straight into a nil-able folder argument.
// A missing segment returns a wrapped store.ErrNotFound naming it.
func FindDocFolderByPath(folders []*model.DocFolder, path string) (*model.DocFolder, error) {
	segs := store.SplitFolderPath(path)
	if len(segs) == 0 {
		return nil, nil
	}
	byParent := make(map[int64][]*model.DocFolder, len(folders))
	var roots []*model.DocFolder
	for _, f := range folders {
		if f.ParentID == nil {
			roots = append(roots, f)
			continue
		}
		byParent[*f.ParentID] = append(byParent[*f.ParentID], f)
	}
	level := roots
	var cur *model.DocFolder
	for i, seg := range segs {
		var hit *model.DocFolder
		for _, f := range level {
			if f.Name == seg {
				hit = f
				break
			}
		}
		if hit == nil {
			return nil, fmt.Errorf("%w: no folder %q under %q", store.ErrNotFound, seg,
				strings.Join(segs[:i], store.FolderPathSeparator))
		}
		cur = hit
		level = byParent[hit.ID]
	}
	return cur, nil
}

// DocFolderDisplayPath renders one folder's breadcrumb ("Design/API/Auth")
// from the flat slice ListDocFolders returns — the read-side twin of
// FindDocFolderByPath, for surfaces that already hold the whole tree and
// must not spend a round trip per crumb. Returns "" for a nil folder
// (the tree root has no path).
func DocFolderDisplayPath(folders []*model.DocFolder, folder *model.DocFolder) string {
	if folder == nil {
		return ""
	}
	byID := make(map[int64]*model.DocFolder, len(folders))
	for _, f := range folders {
		byID[f.ID] = f
	}
	segs := []string{folder.Name}
	seen := map[int64]bool{folder.ID: true}
	for cur := folder; cur.ParentID != nil; {
		parent, ok := byID[*cur.ParentID]
		// A parent missing from the slice (or a cycle a corrupt row could
		// introduce) truncates the crumb rather than looping forever.
		if !ok || seen[parent.ID] {
			break
		}
		seen[parent.ID] = true
		segs = append([]string{parent.Name}, segs...)
		cur = parent
	}
	return strings.Join(segs, store.FolderPathSeparator)
}
