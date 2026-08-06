package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// Kanban lanes over HTTP. Route map (Phase 4A builds the server side):
//
//	GET    /repos/{prefix}/kanban/columns
//	POST   /repos/{prefix}/kanban/columns              {name}
//	PATCH  /repos/{prefix}/kanban/columns/{uuid}       {name} | {position}
//	DELETE /repos/{prefix}/kanban/columns/{uuid}
//	PUT    /repos/{prefix}/issues/{key}/kanban         {column_uuid, position?}
//
// PATCH is a presence map like the doc-folder one: `name` present
// renames, `position` present reorders. Both PATCH shapes answer with a
// single column — that symmetry is why ReorderKanbanColumn returns the
// moved lane rather than the whole re-densified board.

// kanbanColumnPatch is the PATCH body. Position is a pointer so that
// position 0 (the leftmost lane — a perfectly ordinary target) is not
// indistinguishable from an omitted field.
type kanbanColumnPatch struct {
	Name     *string `json:"name,omitempty"`
	Position *int    `json:"position,omitempty"`
}

func (c *remoteClient) ListKanbanColumns(ctx context.Context, repo *model.Repo) ([]*model.KanbanColumn, error) {
	var out []*model.KanbanColumn
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo.Prefix+"/kanban/columns", nil, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.KanbanColumn{}
	}
	return out, nil
}

func (c *remoteClient) CreateKanbanColumn(ctx context.Context, repo *model.Repo, name string, dryRun bool) (*model.KanbanColumn, error) {
	var out model.KanbanColumn
	body := map[string]any{"name": name}
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo.Prefix+"/kanban/columns", dryRunQuery(dryRun), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) RenameKanbanColumn(ctx context.Context, repo *model.Repo, uuid, newName string, dryRun bool) (*model.KanbanColumn, error) {
	var out model.KanbanColumn
	path := "/repos/" + repo.Prefix + "/kanban/columns/" + url.PathEscape(uuid)
	if err := c.do(ctx, http.MethodPatch, path, dryRunQuery(dryRun), kanbanColumnPatch{Name: &newName}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) ReorderKanbanColumn(ctx context.Context, repo *model.Repo, uuid string, position int, dryRun bool) (*model.KanbanColumn, error) {
	var out model.KanbanColumn
	path := "/repos/" + repo.Prefix + "/kanban/columns/" + url.PathEscape(uuid)
	if err := c.do(ctx, http.MethodPatch, path, dryRunQuery(dryRun), kanbanColumnPatch{Position: &position}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) DeleteKanbanColumn(ctx context.Context, repo *model.Repo, uuid string, dryRun bool) (*model.KanbanColumn, *KanbanColumnDeletePreview, error) {
	path := "/repos/" + repo.Prefix + "/kanban/columns/" + url.PathEscape(uuid)
	if dryRun {
		var preview KanbanColumnDeletePreview
		if err := c.do(ctx, http.MethodDelete, path, dryRunQuery(true), nil, &preview); err != nil {
			return nil, nil, err
		}
		return nil, &preview, nil
	}
	// Fetch first so the caller can name the lane in its success message.
	// No single-column GET route exists (the board is always read whole),
	// so filter the list — same shape as DeleteDocFolder.
	cols, err := c.ListKanbanColumns(ctx, repo)
	if err != nil {
		return nil, nil, err
	}
	var col *model.KanbanColumn
	for _, k := range cols {
		if k.UUID == uuid {
			col = k
			break
		}
	}
	if col == nil {
		return nil, nil, store.ErrNotFound
	}
	if err := c.do(ctx, http.MethodDelete, path, nil, nil, nil); err != nil {
		return nil, nil, err
	}
	return col, nil, nil
}

func (c *remoteClient) MoveIssueToKanbanColumn(ctx context.Context, repo *model.Repo, in IssueKanbanMoveInput, dryRun bool) (*model.Issue, error) {
	key, err := c.ResolveIssueKey(ctx, repo, in.IssueKey)
	if err != nil {
		return nil, err
	}
	var out model.Issue
	path := "/repos/" + repo.Prefix + "/issues/" + url.PathEscape(key) + "/kanban"
	if err := c.do(ctx, http.MethodPut, path, dryRunQuery(dryRun), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
