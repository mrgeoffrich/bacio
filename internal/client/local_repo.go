package client

import (
	"context"
	"fmt"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func (c *localClient) ListRepos(ctx context.Context) ([]*model.Repo, error) {
	repos, err := c.store.ListRepos()
	if err != nil {
		return nil, err
	}
	if repos == nil {
		repos = []*model.Repo{}
	}
	return repos, nil
}

func (c *localClient) GetRepoByPrefix(ctx context.Context, prefix string) (*model.Repo, error) {
	return c.store.GetRepoByPrefix(strings.ToUpper(prefix))
}

func (c *localClient) GetRepoByPath(ctx context.Context, path string) (*model.Repo, error) {
	return c.store.GetRepoByPath(path)
}

func (c *localClient) DeleteRepo(ctx context.Context, prefix, confirm string, dryRun bool) (*model.Repo, *RepoDeletePreview, error) {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	repo, err := c.store.GetRepoByPrefix(prefix)
	if err != nil {
		return nil, nil, err
	}
	counts, err := c.store.RepoCascadeCountsForID(repo.ID)
	if err != nil {
		return nil, nil, err
	}
	preview := &RepoDeletePreview{Repo: repo, Cascade: counts, WouldDelete: true}

	// Dry-run is the agent-CLI rehearsal path: success, no changes,
	// no audit row. The error path below is the *abort* path; we
	// return it only when the caller actually intended to delete.
	if dryRun {
		return nil, preview, nil
	}

	// Confirm gate: backend-side, not CLI-side, so direct HTTP /
	// in-process callers can't bypass it by skipping a flag.
	if !strings.EqualFold(strings.TrimSpace(confirm), prefix) {
		return nil, nil, &RepoConfirmError{
			Prefix:     prefix,
			GotConfirm: confirm,
			Preview:    preview,
		}
	}

	// History rows aren't covered by the FK cascade (the schema
	// deliberately keeps `history` FK-less). Wipe them first so the
	// audit log doesn't carry dead-prefix entries forward; the final
	// `repo.delete` row written below has repo_id NULL.
	if err := c.store.DeleteHistoryByRepo(repo.ID); err != nil {
		return nil, nil, fmt.Errorf("delete history: %w", err)
	}
	if err := c.store.DeleteRepo(repo.ID); err != nil {
		return nil, nil, err
	}
	c.recordOp(model.HistoryEntry{
		// repo_id is NULL — the row no longer exists; the prefix
		// snapshot is what callers join on after a delete.
		RepoPrefix: repo.Prefix,
		Op:         "repo.delete", Kind: "repo",
		TargetLabel: repo.Prefix,
		Details:     formatCascadeDetails(repo, counts),
	})
	return repo, nil, nil
}

// formatCascadeDetails turns a RepoCascadeCounts into a one-line
// summary suitable for the audit log's `details` column.
func formatCascadeDetails(repo *model.Repo, c store.RepoCascadeCounts) string {
	return fmt.Sprintf("%s (%d issues, %d comments, %d features, %d documents, %d history)",
		repo.Name, c.Issues, c.Comments, c.Features, c.Documents, c.History)
}
