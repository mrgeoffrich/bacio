package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// defaultFeatureOut is the shared response shape for the per-repo
// default-feature endpoints. The HTTP handlers encode nil as Feature=nil
// (the "no default set" response) and the resolved feature otherwise.
type defaultFeatureOut struct {
	Feature *model.Feature `json:"feature"`
}

// GetDefaultFeature (BACI-235) — GET /repos/{prefix}/settings/default-feature.
// 200 returns {feature: <Feature|null>}; 404 from the server is
// translated to (nil, nil) — same shape the local backend returns when
// the setting is unset.
func (c *remoteClient) GetDefaultFeature(ctx context.Context, repo *model.Repo) (*model.Feature, error) {
	var out defaultFeatureOut
	err := c.do(ctx, http.MethodGet, "/repos/"+repo.Prefix+"/settings/default-feature", nil, nil, &out)
	if err != nil {
		var herr *HTTPError
		if errors.As(err, &herr) && herr.Status == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	return out.Feature, nil
}

// SetDefaultFeature (BACI-235) — PUT /repos/{prefix}/settings/default-feature
// with body {slug: <slug>}. Server resolves the slug and writes the FK;
// records a `repo_setting.update` audit row. Returns the resolved
// feature.
func (c *remoteClient) SetDefaultFeature(ctx context.Context, repo *model.Repo, slug string, dryRun bool) (*model.Feature, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	body := map[string]any{"slug": slug}
	var out defaultFeatureOut
	if err := c.do(ctx, http.MethodPut, "/repos/"+repo.Prefix+"/settings/default-feature", q, body, &out); err != nil {
		return nil, err
	}
	return out.Feature, nil
}

// ClearDefaultFeature (BACI-235) — DELETE /repos/{prefix}/settings/default-feature.
// Idempotent: a 200 / 204 on an already-unset setting is treated as
// success, no audit op stamped server-side in that case.
func (c *remoteClient) ClearDefaultFeature(ctx context.Context, repo *model.Repo, dryRun bool) error {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	return c.do(ctx, http.MethodDelete, "/repos/"+repo.Prefix+"/settings/default-feature", q, nil, nil)
}
