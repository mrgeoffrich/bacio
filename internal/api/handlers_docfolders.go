package api

// HTTP surface for the document folder tree (the pivot's doc_folders
// table). Five routes:
//
//	GET    /repos/{prefix}/doc-folders                  -> 200 []model.DocFolder
//	POST   /repos/{prefix}/doc-folders                  -> 201 model.DocFolder
//	PATCH  /repos/{prefix}/doc-folders/{uuid}           -> 200 model.DocFolder
//	DELETE /repos/{prefix}/doc-folders/{uuid}           -> 204 (200 + preview on dry_run)
//	PUT    /repos/{prefix}/documents/{filename}/folder  -> 200 model.Document
//
// Three shapes carry the whole contract, and all three are easy to get
// subtly wrong:
//
//   - **PATCH is a presence map, not a patch of values.** `{"name": …}`
//     renames; `{"parent_uuid": …}` re-parents. Which key is PRESENT
//     selects the operation — its value never does. Both fields are
//     pointers so a JSON `null`-free absence is distinguishable from an
//     explicitly-empty string. Sending both, or neither, is a 400: there
//     is no combined rename-and-move verb because the store applies them
//     in separate transactions and a half-applied pair has no sensible
//     response shape.
//   - **An empty uuid is a VALUE, not a missing one.** `parent_uuid: ""`
//     (and `folder_uuid: ""`) mean the tree ROOT — the only way to pull a
//     folder or a page back out to the top level. Absent `parent_uuid` on
//     CREATE also means root, because a create has nothing to preserve.
//   - **Absent `position` means append.** `position` is `*int` on the
//     wire for the same reason: position 0 (the top of a folder) is an
//     ordinary target and must not read as "unset".
//
// There is deliberately NO single-folder GET. The tree is always read
// whole (it is small, and every consumer needs the parent chain to
// render a breadcrumb), so a per-folder route would only ever be a
// slower way to filter the list. The remote client relies on this — see
// remote_docfolder.go's DeleteDocFolder.
//
// Every uuid is re-checked against the repo in the URL before it is
// touched: the uuid namespace is global, so without that check a caller
// holding any folder uuid could mutate another repo's tree through this
// repo's prefix. The check happens twice on purpose — once here (so a
// cross-repo uuid surfaces as a clean 404 that does not leak the row's
// existence, matching resolveIssueOnRepo) and again inside the client,
// which is the boundary the CLI and desktop share.

import (
	"errors"
	"net/http"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// docFolderCreateIn is the POST body — the wire twin of
// client.DocFolderCreateInput. `parent_uuid` absent or empty both mean
// the tree root.
type docFolderCreateIn struct {
	Name       string `json:"name"`
	ParentUUID string `json:"parent_uuid,omitempty"`
}

// docFolderPatchIn is the PATCH body. Pointers, not strings: see the
// presence-map note in the file header.
type docFolderPatchIn struct {
	Name       *string `json:"name,omitempty"`
	ParentUUID *string `json:"parent_uuid,omitempty"`
}

// documentFolderMoveIn is the PUT .../documents/{filename}/folder body —
// the wire twin of client.DocumentFolderMoveInput.
//
// `filename` is accepted (the shared input struct carries it, so a strict
// decode would reject the payload without it) and then IGNORED: the URL
// path segment is authoritative. Same rule as the Kanban issue-move
// route, where the body's key can legitimately differ from the URL's.
type documentFolderMoveIn struct {
	Filename   string `json:"filename,omitempty"`
	FolderUUID string `json:"folder_uuid"`
	Position   *int   `json:"position,omitempty"`
}

func (d deps) handleDocFoldersList(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	c := client.NewLocalFromStore(d.store, ActorFromContext(r.Context()))
	folders, err := c.ListDocFolders(r.Context(), repo)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, folders)
}

func (d deps) handleDocFolderCreate(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[docFolderCreateIn](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	// Empty parent_uuid == the tree root, so only a non-empty one names a
	// row that has to exist and has to be ours.
	if in.ParentUUID != "" {
		if _, ok := resolveDocFolderInRepo(w, d.store, repo, in.ParentUUID, "parent_uuid"); !ok {
			return
		}
	}
	c := client.NewLocalFromStore(d.store, ActorFromContext(r.Context()))
	dryRun := isDryRun(r)
	folder, err := c.CreateDocFolder(r.Context(), repo, client.DocFolderCreateInput{
		Name:       in.Name,
		ParentUUID: in.ParentUUID,
	}, dryRun)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), docFolderErrorField(err))
		return
	}
	if dryRun {
		writeDryRun(w, http.StatusCreated, folder)
		return
	}
	writeJSON(w, http.StatusCreated, folder)
}

// handleDocFolderPatch is the shared rename/move route. Exactly one of
// `name` / `parent_uuid` must be present — see the file header.
func (d deps) handleDocFolderPatch(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	folder, ok := resolveDocFolderInRepo(w, d.store, repo, r.PathValue("uuid"), "uuid")
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[docFolderPatchIn](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	switch {
	case in.Name != nil && in.ParentUUID != nil:
		writeError(w, http.StatusBadRequest, "invalid_input",
			"send exactly one of name (rename) or parent_uuid (move), not both", nil)
		return
	case in.Name == nil && in.ParentUUID == nil:
		writeError(w, http.StatusBadRequest, "invalid_input",
			"send one of name (rename) or parent_uuid (move)", nil)
		return
	}

	c := client.NewLocalFromStore(d.store, ActorFromContext(r.Context()))
	dryRun := isDryRun(r)
	var updated *model.DocFolder
	if in.Name != nil {
		updated, err = c.RenameDocFolder(r.Context(), repo, folder.UUID, *in.Name, dryRun)
	} else {
		// Present-but-empty parent_uuid is the tree root; anything else
		// must resolve to a folder in this repo.
		if *in.ParentUUID != "" {
			if _, ok := resolveDocFolderInRepo(w, d.store, repo, *in.ParentUUID, "parent_uuid"); !ok {
				return
			}
		}
		updated, err = c.MoveDocFolder(r.Context(), repo, client.DocFolderMoveInput{
			UUID:          folder.UUID,
			NewParentUUID: *in.ParentUUID,
		}, dryRun)
	}
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), docFolderErrorField(err))
		return
	}
	if dryRun {
		writeDryRun(w, http.StatusOK, updated)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleDocFolderDelete removes a folder and everything under it.
// Documents are never deleted — the whole subtree's pages are re-rooted —
// so the preview counts them separately from the subfolders that go.
//
// `?dry_run=true` answers 200 with the DocFolderDeletePreview body; a
// real delete answers 204 with no body, mirroring DELETE /repos/{prefix}.
func (d deps) handleDocFolderDelete(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	folder, ok := resolveDocFolderInRepo(w, d.store, repo, r.PathValue("uuid"), "uuid")
	if !ok {
		return
	}
	c := client.NewLocalFromStore(d.store, ActorFromContext(r.Context()))
	dryRun := isDryRun(r)
	_, preview, err := c.DeleteDocFolder(r.Context(), repo, folder.UUID, dryRun)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if dryRun {
		writeDryRun(w, http.StatusOK, preview)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDocumentFolderSet implements PUT
// /repos/{prefix}/documents/{filename}/folder — moving one page between
// folders. PUT rather than PATCH because the body replaces the document's
// whole placement (folder + position), and folder membership is a single
// idempotent fact rather than a partial edit.
func (d deps) handleDocumentFolderSet(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	doc, ok := resolveDocumentOnRepo(w, r, d.store, repo, false)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[documentFolderMoveIn](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	// "" is the tree root — an explicit destination, not a missing one.
	if in.FolderUUID != "" {
		if _, ok := resolveDocFolderInRepo(w, d.store, repo, in.FolderUUID, "folder_uuid"); !ok {
			return
		}
	}
	c := client.NewLocalFromStore(d.store, ActorFromContext(r.Context()))
	dryRun := isDryRun(r)
	updated, err := c.MoveDocumentToFolder(r.Context(), repo, client.DocumentFolderMoveInput{
		Filename:   doc.Filename,
		FolderUUID: in.FolderUUID,
		Position:   in.Position,
	}, dryRun)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), docFolderErrorField(err))
		return
	}
	// The list shape never carries a body; neither should a move.
	updated.Content = ""
	if dryRun {
		writeDryRun(w, http.StatusOK, updated)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// ---------------------------------------------------------------------
// Internals
// ---------------------------------------------------------------------

// resolveDocFolderInRepo looks a folder up by uuid and asserts it belongs
// to repo. A uuid that does not exist AND a uuid that belongs to another
// repo both answer 404 with the same message — the same non-leaking rule
// resolveIssueOnRepo applies, and the reason the store's global uuid
// namespace is safe to expose on a per-repo route.
func resolveDocFolderInRepo(w http.ResponseWriter, s *store.Store, repo *model.Repo, uuid, field string) (*model.DocFolder, bool) {
	if uuid == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "folder uuid is required", fieldDetail(field))
		return nil, false
	}
	folder, err := s.GetDocFolderByUUID(uuid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "folder not found", fieldDetail(field))
			return nil, false
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return nil, false
	}
	if folder.RepoID != repo.ID {
		writeError(w, http.StatusNotFound, "not_found", "folder not found", fieldDetail(field))
		return nil, false
	}
	return folder, true
}

// docFolderErrorField attributes a typed store refusal to the request
// field responsible, so a UI can highlight the offending input instead of
// string-matching the message.
func docFolderErrorField(err error) map[string]any {
	switch {
	case errors.Is(err, store.ErrDocFolderExists):
		return fieldDetail("name")
	case errors.Is(err, store.ErrDocFolderCycle), errors.Is(err, store.ErrDocFolderTooDeep):
		return fieldDetail("parent_uuid")
	}
	return nil
}
