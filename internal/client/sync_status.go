package client

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/sync"
)

// SyncStatus is the BACI-89 background-sync status of one tracked
// repo. Mirrors api.SyncStatusOut field-for-field so the JSON wire
// format is identical whether the backend is local or remote.
type SyncStatus struct {
	Prefix            string     `json:"prefix"`
	Configured        bool       `json:"configured"`
	BackgroundEnabled bool       `json:"background_enabled"`
	InProgress        bool       `json:"in_progress"`
	LastSyncAt        *time.Time `json:"last_sync_at,omitempty"`
	LastError         *string    `json:"last_error,omitempty"`
	Remote            string     `json:"remote,omitempty"`
}

// SyncStatuses (local) walks every tracked repo and assembles its
// background-sync status from the store + the repo's machine-local
// .bacio/config.yaml. InProgress is always false here — the localClient
// is not the controller, so it can't see the in-flight flag; the
// caller (desktop / TUI) that owns the runner reads that separately if
// it needs it. last_sync_at (DB-backed) is the authoritative signal.
func (c *localClient) SyncStatuses(ctx context.Context) ([]SyncStatus, error) {
	repos, err := c.store.ListRepos()
	if err != nil {
		return nil, err
	}
	bgEnabled, err := c.store.GetSyncBackgroundEnabled()
	if err != nil {
		return nil, err
	}
	out := make([]SyncStatus, 0, len(repos))
	for _, repo := range repos {
		st := SyncStatus{Prefix: repo.Prefix, BackgroundEnabled: bgEnabled}
		if repo.Path != "" {
			cfg, cerr := sync.ReadProjectConfig(repo.Path)
			if cerr == nil && cfg.Sync.Remote != "" {
				st.Remote = cfg.Sync.Remote
				if rec, rerr := c.store.GetSyncRemote(cfg.Sync.Remote); rerr == nil {
					st.Configured = true
					st.LastSyncAt = rec.LastSyncAt
					st.LastError = rec.LastSyncError
				}
			}
		}
		out = append(out, st)
	}
	return out, nil
}

// GetSyncBackgroundEnabled (local) reads the BACI-89 toggle.
func (c *localClient) GetSyncBackgroundEnabled(ctx context.Context) (bool, error) {
	return c.store.GetSyncBackgroundEnabled()
}

// SetSyncBackgroundEnabled (local) writes the BACI-89 toggle + an
// audit row. Returns the post-write value (or the projected value on
// dry-run). The op shape matches handlers_sync.go so `bacio history
// --kind app_setting` returns the same rows whether flipped from the
// CLI, HTTP, or desktop.
func (c *localClient) SetSyncBackgroundEnabled(ctx context.Context, value, dryRun bool) (bool, error) {
	if dryRun {
		return value, nil
	}
	if err := c.store.SetSyncBackgroundEnabled(value); err != nil {
		return false, err
	}
	c.recordOp(model.HistoryEntry{
		Op:          "sync_pref.update",
		Kind:        "app_setting",
		TargetLabel: "sync.background_enabled",
		Details:     boolDetails("background_enabled", value),
	})
	return value, nil
}

// boolDetails formats a "key=true"/"key=false" audit detail string.
func boolDetails(key string, v bool) string {
	if v {
		return key + "=true"
	}
	return key + "=false"
}

// SyncStatuses (remote) — GET /sync.
func (c *remoteClient) SyncStatuses(ctx context.Context) ([]SyncStatus, error) {
	var out []SyncStatus
	if err := c.do(ctx, http.MethodGet, "/sync", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetSyncBackgroundEnabled (remote) — GET /settings/sync-preferences.
func (c *remoteClient) GetSyncBackgroundEnabled(ctx context.Context) (bool, error) {
	var out struct {
		BackgroundEnabled bool `json:"background_enabled"`
	}
	if err := c.do(ctx, http.MethodGet, "/settings/sync-preferences", nil, nil, &out); err != nil {
		return false, err
	}
	return out.BackgroundEnabled, nil
}

// SetSyncBackgroundEnabled (remote) — PUT /settings/sync-preferences.
func (c *remoteClient) SetSyncBackgroundEnabled(ctx context.Context, value, dryRun bool) (bool, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	body := map[string]any{"background_enabled": value}
	var out struct {
		BackgroundEnabled bool `json:"background_enabled"`
	}
	if err := c.do(ctx, http.MethodPut, "/settings/sync-preferences", q, body, &out); err != nil {
		return false, err
	}
	return out.BackgroundEnabled, nil
}
