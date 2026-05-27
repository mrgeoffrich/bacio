// BACI-240 remote-side wrappers for the ui.shipped_sfx global toggle.
// Mirrors remote_archive.go's display-preferences shape.
package client

import (
	"context"
	"net/http"
	"net/url"
)

// GetUIShippedSfx (BACI-240) — GET /settings/audio-preferences.
func (c *remoteClient) GetUIShippedSfx(ctx context.Context) (bool, error) {
	var out struct {
		ShippedSfx bool `json:"shipped_sfx"`
	}
	if err := c.do(ctx, http.MethodGet, "/settings/audio-preferences", nil, nil, &out); err != nil {
		return false, err
	}
	return out.ShippedSfx, nil
}

// SetUIShippedSfx (BACI-240) — PUT /settings/audio-preferences.
func (c *remoteClient) SetUIShippedSfx(ctx context.Context, value, dryRun bool) (bool, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	body := map[string]any{"shipped_sfx": value}
	var out struct {
		ShippedSfx bool `json:"shipped_sfx"`
	}
	if err := c.do(ctx, http.MethodPut, "/settings/audio-preferences", q, body, &out); err != nil {
		return false, err
	}
	return out.ShippedSfx, nil
}
