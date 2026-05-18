// BACI-68 remote-side wrappers for the archive sweep and the
// display.show_archived global toggle.
package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/mrgeoffrich/bacio/internal/store"
)

// ArchiveSweep (BACI-68) — POST /archive/sweep. Mirrors the local
// shape (counts in the response body).
func (c *remoteClient) ArchiveSweep(ctx context.Context, dryRun bool) (store.ArchiveSweepResult, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out store.ArchiveSweepResult
	if err := c.do(ctx, http.MethodPost, "/archive/sweep", q, nil, &out); err != nil {
		return store.ArchiveSweepResult{}, err
	}
	return out, nil
}

// GetDisplayShowArchived (BACI-68) — GET /settings/display-preferences.
func (c *remoteClient) GetDisplayShowArchived(ctx context.Context) (bool, error) {
	var out struct {
		ShowArchived bool `json:"show_archived"`
	}
	if err := c.do(ctx, http.MethodGet, "/settings/display-preferences", nil, nil, &out); err != nil {
		return false, err
	}
	return out.ShowArchived, nil
}

// SetDisplayShowArchived (BACI-68) — PUT /settings/display-preferences.
func (c *remoteClient) SetDisplayShowArchived(ctx context.Context, value, dryRun bool) (bool, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	body := map[string]any{"show_archived": value}
	var out struct {
		ShowArchived bool `json:"show_archived"`
	}
	if err := c.do(ctx, http.MethodPut, "/settings/display-preferences", q, body, &out); err != nil {
		return false, err
	}
	return out.ShowArchived, nil
}
