package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// The remote (HTTP) half of the per-space nav-surface gates. Shapes
// copied from remote_pipeline.go's SetRepoAutoShip.

// RepoSurfaces reads the gates off GET /repos, which carries them on
// every repo payload (api.RepoOut). There is deliberately no dedicated
// read route: a second source of truth for the same values could drift
// from the one the board list already uses.
func (c *remoteClient) RepoSurfaces(ctx context.Context) (map[string]model.RepoSurfaces, error) {
	var rows []struct {
		Prefix            string `json:"prefix"`
		ShowAgentSurfaces bool   `json:"show_agent_surfaces"`
		ShowKanban        bool   `json:"show_kanban"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos", nil, nil, &rows); err != nil {
		return nil, err
	}
	out := make(map[string]model.RepoSurfaces, len(rows))
	for _, row := range rows {
		out[row.Prefix] = model.RepoSurfaces{
			ShowAgentSurfaces: row.ShowAgentSurfaces,
			ShowKanban:        row.ShowKanban,
		}
	}
	return out, nil
}

func (c *remoteClient) SetRepoShowAgentSurfaces(ctx context.Context, repo *model.Repo, enabled, dryRun bool) (bool, error) {
	return c.putRepoSurface(ctx, repo, "show-agent-surfaces", "show_agent_surfaces", enabled, dryRun)
}

func (c *remoteClient) SetRepoShowKanban(ctx context.Context, repo *model.Repo, enabled, dryRun bool) (bool, error) {
	return c.putRepoSurface(ctx, repo, "show-kanban", "show_kanban", enabled, dryRun)
}

// putRepoSurface PUTs one gate. The route segment is kebab-case and the
// response key is the snake_case column name, matching the handler.
func (c *remoteClient) putRepoSurface(ctx context.Context, repo *model.Repo, route, key string, enabled, dryRun bool) (bool, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	body := map[string]any{"enabled": enabled}
	var out map[string]bool
	if err := c.do(ctx, http.MethodPut, "/repos/"+repo.Prefix+"/"+route, q, body, &out); err != nil {
		return false, err
	}
	return out[key], nil
}
