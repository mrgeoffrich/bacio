package client

import (
	"context"
	"fmt"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// The local (direct-store) half of the per-space nav-surface gates.
// Shapes copied from local_pipeline.go's SetRepoAutoShip: dry-run
// short-circuits before the write, the audit row names the setting, and
// the written value is echoed back.
//
// The audit op is `repo_setting.update` with the column as
// TargetLabel — matching how backlog-collapsed and impact-primary
// record themselves — rather than a bespoke `repo.*` op like auto-ship
// uses. These are display preferences, not behaviour changes.

// RepoSurfaces resolves every space's gates in one query, keyed by
// prefix. Read-only, no audit row.
func (c *localClient) RepoSurfaces(ctx context.Context) (map[string]model.RepoSurfaces, error) {
	return c.store.ListRepoSurfaces()
}

// SetRepoShowAgentSurfaces writes the per-space "Agent Mode" gate and
// returns the resulting value.
func (c *localClient) SetRepoShowAgentSurfaces(ctx context.Context, repo *model.Repo, enabled, dryRun bool) (bool, error) {
	return c.setRepoSurface(repo, "show_agent_surfaces", enabled, dryRun)
}

// SetRepoShowKanban writes the per-space "Show Kanban Board" gate.
func (c *localClient) SetRepoShowKanban(ctx context.Context, repo *model.Repo, enabled, dryRun bool) (bool, error) {
	return c.setRepoSurface(repo, "show_kanban", enabled, dryRun)
}

func (c *localClient) setRepoSurface(repo *model.Repo, field string, enabled, dryRun bool) (bool, error) {
	if dryRun {
		return enabled, nil
	}
	// Compare against the RESOLVED previous value: on a space nobody has
	// touched the column is NULL, and writing the kind default
	// explicitly isn't a change worth auditing.
	prev, err := c.store.GetRepoSurfaces(repo.ID)
	if err != nil {
		return false, err
	}
	var was bool
	if field == "show_kanban" {
		was = prev.ShowKanban
		err = c.store.SetRepoShowKanban(repo.ID, enabled)
	} else {
		was = prev.ShowAgentSurfaces
		err = c.store.SetRepoShowAgentSurfaces(repo.ID, enabled)
	}
	if err != nil {
		return false, err
	}
	if was != enabled {
		c.recordOp(model.HistoryEntry{
			RepoID: &repo.ID, RepoPrefix: repo.Prefix,
			Op:          "repo_setting.update",
			Kind:        "repo_setting",
			TargetLabel: field,
			Details:     fmt.Sprintf("enabled=%v", enabled),
		})
	}
	return enabled, nil
}
