package client

import (
	"context"
	"fmt"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// GetDefaultFeature (BACI-235) returns the per-repo default_feature
// row, or nil when the setting is unset. The FK ON DELETE SET NULL
// means a dangling reference can't survive a feature delete, so this
// reader never returns a non-nil feature that has since been removed.
func (c *localClient) GetDefaultFeature(ctx context.Context, repo *model.Repo) (*model.Feature, error) {
	settings, err := c.store.GetRepoSettings(repo.ID)
	if err != nil {
		return nil, err
	}
	if settings.DefaultFeatureID == nil {
		return nil, nil
	}
	feat, err := c.store.GetFeatureByID(*settings.DefaultFeatureID)
	if err != nil {
		return nil, err
	}
	return feat, nil
}

// SetDefaultFeature (BACI-235) resolves slug against the same repo,
// then writes its id into repo_settings.default_feature_id. Records a
// `repo_setting.update` audit row with target_label `default_feature`
// and details `slug=<value>`. dryRun short-circuits before the write
// but still does the slug existence check so the rehearsal output
// matches the real call.
func (c *localClient) SetDefaultFeature(ctx context.Context, repo *model.Repo, slug string, dryRun bool) (*model.Feature, error) {
	if slug == "" {
		return nil, fmt.Errorf("slug is required (use ClearDefaultFeature to unset)")
	}
	feat, err := c.store.GetFeatureBySlug(repo.ID, slug)
	if err != nil {
		return nil, fmt.Errorf("feature %q: %w", slug, err)
	}
	if dryRun {
		return feat, nil
	}
	if err := c.store.SetDefaultFeatureID(repo.ID, &feat.ID); err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "repo_setting.update", Kind: "repo_setting",
		TargetID: &feat.ID, TargetLabel: "default_feature",
		Details: fmt.Sprintf("slug=%s", feat.Slug),
	})
	return feat, nil
}

// ClearDefaultFeature (BACI-235) unsets the per-repo default_feature.
// Idempotent: clearing an already-unset value still records the audit
// op for symmetry with the set path, but skips the SQL write when
// possible (a fresh repo never has a row, so the upsert is a no-op).
// Records `repo_setting.update` with details `cleared`.
func (c *localClient) ClearDefaultFeature(ctx context.Context, repo *model.Repo, dryRun bool) error {
	if dryRun {
		return nil
	}
	if err := c.store.ClearDefaultFeature(repo.ID); err != nil {
		return err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "repo_setting.update", Kind: "repo_setting",
		TargetLabel: "default_feature",
		Details:     "cleared",
	})
	return nil
}
