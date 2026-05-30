// BACI-312 remote-side wrappers for the ui.timezone global setting.
// Mirrors remote_audio.go's shipped-sfx shape.
package client

import (
	"context"
	"net/http"
	"net/url"
)

// GetUITimezone (BACI-312) — GET /settings/timezone-preferences.
func (c *remoteClient) GetUITimezone(ctx context.Context) (string, error) {
	var out struct {
		Timezone string `json:"timezone"`
	}
	if err := c.do(ctx, http.MethodGet, "/settings/timezone-preferences", nil, nil, &out); err != nil {
		return "", err
	}
	return out.Timezone, nil
}

// SetUITimezone (BACI-312) — PUT /settings/timezone-preferences.
func (c *remoteClient) SetUITimezone(ctx context.Context, value string, dryRun bool) (string, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	body := map[string]any{"timezone": value}
	var out struct {
		Timezone string `json:"timezone"`
	}
	if err := c.do(ctx, http.MethodPut, "/settings/timezone-preferences", q, body, &out); err != nil {
		return "", err
	}
	return out.Timezone, nil
}
