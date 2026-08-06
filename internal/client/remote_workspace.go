package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// CreateWorkspace (remote) — POST /workspaces with `?dry_run=true` for
// the rehearsal path. Deliberately a route of its own rather than
// POST /repos with a kind field: registering a git repo takes a path and
// a remote and refuses without them, and a workspace takes neither.
func (c *remoteClient) CreateWorkspace(ctx context.Context, in WorkspaceCreateInput, dryRun bool) (*model.Repo, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.Repo
	if err := c.do(ctx, http.MethodPost, "/workspaces", q, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
