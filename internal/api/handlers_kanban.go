package api

// HTTP surface for the Kanban board — the human lane axis, orthogonal to
// model.State and to the Agentic Pipeline. Five routes:
//
//	GET    /repos/{prefix}/kanban/columns          -> 200 []kanbanColumnOut
//	POST   /repos/{prefix}/kanban/columns          -> 201 kanbanColumnOut
//	PATCH  /repos/{prefix}/kanban/columns/{uuid}   -> 200 kanbanColumnOut
//	DELETE /repos/{prefix}/kanban/columns/{uuid}   -> 204 (200 + preview on dry_run)
//	PUT    /repos/{prefix}/issues/{key}/kanban     -> 200 model.Issue
//
// kanbanColumnOut is model.KanbanColumn plus its ordered `cards` refs —
// see its doc comment for why membership rides on the container. All three
// lane routes answer with the same shape so Phase 5's TS contract needs one
// type, not two.
//
// The same three shape rules as the doc-folder routes (see
// handlers_docfolders.go's header) apply here, with one substitution:
//
//   - **PATCH is a presence map.** `{"name": …}` renames the lane;
//     `{"position": …}` reorders it. Position is `*int` because lane 0 —
//     the leftmost lane — is an ordinary target and must not read as
//     "unset". Exactly one key; both or neither is a 400.
//   - **An empty `column_uuid` is a VALUE.** It means "take this card OFF
//     the board" (issues.kanban_column_id back to NULL), which is the only
//     way to un-opt a git-repo card. Absent and empty are NOT the same
//     thing: `column_uuid` has no omitempty on the wire precisely so the
//     empty case survives the round trip.
//   - **Absent `position` means append** to the bottom of the target lane.
//
// PATCH .../columns/{uuid} answers with the moved lane only, never the
// whole board — the store re-densifies the siblings underneath, so a
// caller that cares about the new order re-reads GET .../kanban/columns.
// That is what lets rename and reorder share one route and one response
// shape.
//
// There is deliberately NO single-column GET, for the same reason there
// is no single-folder GET: the board is always read whole. The remote
// client depends on it — see remote_kanban.go's DeleteKanbanColumn.
//
// Every lane uuid is re-checked against the repo in the URL here AND
// again inside the client. See handlers_docfolders.go for why the
// duplication is deliberate.

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// kanbanCardRefOut is one card's placement in a lane. It is a reference,
// not the card: the Kanban view joins these against GET /repos/{prefix}/cards
// for titles, tags and assignees, exactly as the Pipeline does.
type kanbanCardRefOut struct {
	Key      string `json:"key"`
	Position int    `json:"position"`
}

// kanbanColumnOut is one lane plus its ordered card references.
//
// Membership rides on the CONTAINER, never on the card, which is the same
// shape the sync layer uses (column.yaml lists issue uuids) and the same
// shape desktop/boardservice.go's KanbanColumnDTO returns. That symmetry is
// the point: internal/boardcards.BoardCard carries no lane field, so without
// it a Kanban card would have nowhere to record its lane and the two
// transports could not satisfy one TS contract in Phase 5.
//
// Cards is always non-nil (an empty lane serialises as []) so the React side
// can map over it without a guard.
type kanbanColumnOut struct {
	*model.KanbanColumn
	Cards []kanbanCardRefOut `json:"cards"`
}

// kanbanBoardOut assembles lanes-with-membership. It mirrors
// desktop/boardservice.go's kanbanBoard exactly, INCLUDING both visibility
// filters — a lane must never reference a card the board itself didn't ship,
// or the UI renders a placement for a card it cannot draw.
func kanbanBoardOut(ctx context.Context, c client.Client, repo *model.Repo, cols []*model.KanbanColumn) ([]kanbanColumnOut, error) {
	showArchived, _ := c.GetDisplayShowArchived(ctx)
	var hiddenSlugs []string
	if slugs, err := c.ListHiddenFeatureSlugs(ctx, repo); err == nil {
		hiddenSlugs = slugs
	}
	issues, err := c.ListIssues(ctx, client.IssueFilter{
		Repo:               repo,
		IncludeArchived:    showArchived,
		HiddenFeatureSlugs: hiddenSlugs,
	})
	if err != nil {
		return nil, err
	}
	byColumn := make(map[int64][]kanbanCardRefOut, len(cols))
	for _, iss := range issues {
		if iss.KanbanColumnID == nil {
			continue // not on the board
		}
		id := *iss.KanbanColumnID
		byColumn[id] = append(byColumn[id], kanbanCardRefOut{Key: iss.Key, Position: iss.KanbanPosition})
	}
	out := make([]kanbanColumnOut, 0, len(cols))
	for _, col := range cols {
		refs := byColumn[col.ID]
		// Positions are a dense 0-based index maintained by the store, but
		// sort anyway so a gap left by an older binary degrades to a stable
		// order rather than a scrambled lane.
		sort.SliceStable(refs, func(i, j int) bool { return refs[i].Position < refs[j].Position })
		if refs == nil {
			refs = []kanbanCardRefOut{}
		}
		out = append(out, kanbanColumnOut{KanbanColumn: col, Cards: refs})
	}
	return out, nil
}

// kanbanColumnOutOne shapes a single lane the same way, so create, rename
// and reorder answer with the identical type the list route does and Phase 5
// needs only one TS shape.
func kanbanColumnOutOne(ctx context.Context, c client.Client, repo *model.Repo, col *model.KanbanColumn) (kanbanColumnOut, error) {
	board, err := kanbanBoardOut(ctx, c, repo, []*model.KanbanColumn{col})
	if err != nil {
		return kanbanColumnOut{}, err
	}
	return board[0], nil
}

// kanbanColumnCreateIn is the POST body. A new lane always appends to the
// right of the board, so there is no position field.
type kanbanColumnCreateIn struct {
	Name string `json:"name"`
}

// kanbanColumnPatchIn is the PATCH body — the wire twin of the client's
// kanbanColumnPatch. Pointers: see the presence-map note above.
type kanbanColumnPatchIn struct {
	Name     *string `json:"name,omitempty"`
	Position *int    `json:"position,omitempty"`
}

// issueKanbanMoveIn is the PUT .../issues/{key}/kanban body — the wire
// twin of client.IssueKanbanMoveInput.
//
// `issue_key` is accepted and then IGNORED. This is not tidiness, it is
// required: the remote client resolves a short reference ("42") to a full
// key ("BACI-42") for the URL but sends the *unresolved* value in the
// body, so the two legitimately differ. The URL is authoritative.
type issueKanbanMoveIn struct {
	IssueKey   string `json:"issue_key,omitempty"`
	ColumnUUID string `json:"column_uuid"`
	Position   *int   `json:"position,omitempty"`
}

func (d deps) handleKanbanColumnsList(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	c := client.NewLocalFromStore(d.store, ActorFromContext(r.Context()))
	cols, err := c.ListKanbanColumns(r.Context(), repo)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	out, err := kanbanBoardOut(r.Context(), c, repo, cols)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (d deps) handleKanbanColumnCreate(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[kanbanColumnCreateIn](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	c := client.NewLocalFromStore(d.store, ActorFromContext(r.Context()))
	dryRun := isDryRun(r)
	col, err := c.CreateKanbanColumn(r.Context(), repo, in.Name, dryRun)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), kanbanErrorField(err))
		return
	}
	out, err := kanbanColumnOutOne(r.Context(), c, repo, col)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if dryRun {
		writeDryRun(w, http.StatusCreated, out)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// handleKanbanColumnPatch is the shared rename/reorder route.
func (d deps) handleKanbanColumnPatch(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	col, ok := resolveKanbanColumnInRepo(w, d.store, repo, r.PathValue("uuid"), "uuid")
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[kanbanColumnPatchIn](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	switch {
	case in.Name != nil && in.Position != nil:
		writeError(w, http.StatusBadRequest, "invalid_input",
			"send exactly one of name (rename) or position (reorder), not both", nil)
		return
	case in.Name == nil && in.Position == nil:
		writeError(w, http.StatusBadRequest, "invalid_input",
			"send one of name (rename) or position (reorder)", nil)
		return
	}

	c := client.NewLocalFromStore(d.store, ActorFromContext(r.Context()))
	dryRun := isDryRun(r)
	var updated *model.KanbanColumn
	if in.Name != nil {
		updated, err = c.RenameKanbanColumn(r.Context(), repo, col.UUID, *in.Name, dryRun)
	} else {
		if *in.Position < 0 {
			writeError(w, http.StatusBadRequest, "invalid_input",
				"position must be >= 0", fieldDetail("position"))
			return
		}
		updated, err = c.ReorderKanbanColumn(r.Context(), repo, col.UUID, *in.Position, dryRun)
	}
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), kanbanErrorField(err))
		return
	}
	out, err := kanbanColumnOutOne(r.Context(), c, repo, updated)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if dryRun {
		writeDryRun(w, http.StatusOK, out)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleKanbanColumnDelete removes a lane. Cards are never deleted — the
// FK is ON DELETE SET NULL, so every card in the lane simply comes off
// the board — which is what the preview's cascade count reports.
//
// `?dry_run=true` answers 200 with the KanbanColumnDeletePreview body; a
// real delete answers 204 with no body.
func (d deps) handleKanbanColumnDelete(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	col, ok := resolveKanbanColumnInRepo(w, d.store, repo, r.PathValue("uuid"), "uuid")
	if !ok {
		return
	}
	c := client.NewLocalFromStore(d.store, ActorFromContext(r.Context()))
	dryRun := isDryRun(r)
	_, preview, err := c.DeleteKanbanColumn(r.Context(), repo, col.UUID, dryRun)
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

// handleIssueKanbanSet implements PUT /repos/{prefix}/issues/{key}/kanban
// — putting a card on a lane, moving it between lanes, or (with an empty
// column_uuid) taking it off the board entirely.
//
// PUT, not PATCH: the body replaces the card's whole board placement
// (lane + position) and is idempotent.
func (d deps) handleIssueKanbanSet(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[issueKanbanMoveIn](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	// "" is "off the board" — an explicit destination, not a missing one.
	if in.ColumnUUID != "" {
		if _, ok := resolveKanbanColumnInRepo(w, d.store, repo, in.ColumnUUID, "column_uuid"); !ok {
			return
		}
	}
	c := client.NewLocalFromStore(d.store, ActorFromContext(r.Context()))
	dryRun := isDryRun(r)
	updated, err := c.MoveIssueToKanbanColumn(r.Context(), repo, client.IssueKanbanMoveInput{
		IssueKey:   iss.Key,
		ColumnUUID: in.ColumnUUID,
		Position:   in.Position,
	}, dryRun)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), kanbanErrorField(err))
		return
	}
	if dryRun {
		writeDryRun(w, http.StatusOK, updated)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// ---------------------------------------------------------------------
// Internals
// ---------------------------------------------------------------------

// resolveKanbanColumnInRepo looks a lane up by uuid and asserts it
// belongs to repo. Unknown uuid and other-repo uuid both answer the same
// 404 — see resolveDocFolderInRepo for why.
func resolveKanbanColumnInRepo(w http.ResponseWriter, s *store.Store, repo *model.Repo, uuid, field string) (*model.KanbanColumn, bool) {
	if uuid == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "kanban column uuid is required", fieldDetail(field))
		return nil, false
	}
	col, err := s.GetKanbanColumnByUUID(uuid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "kanban column not found", fieldDetail(field))
			return nil, false
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return nil, false
	}
	if col.RepoID != repo.ID {
		writeError(w, http.StatusNotFound, "not_found", "kanban column not found", fieldDetail(field))
		return nil, false
	}
	return col, true
}

// kanbanErrorField attributes a refusal to the field responsible. The
// store's duplicate-lane refusal is a plain fmt.Errorf (it replaces the
// raw UNIQUE error with a friendlier one), so this matches the phrase it
// owns rather than a sentinel.
func kanbanErrorField(err error) map[string]any {
	if err == nil {
		return nil
	}
	if kanbanNameCollision(err) {
		return fieldDetail("name")
	}
	return nil
}
