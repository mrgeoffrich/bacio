package client

import (
	"context"
	"net/http"
	"time"
)

// RepoActivity (BACI-369) is the per-repo activity summary the topbar's
// repository picker orders itself by. Mirrors api.RepoActivityOut
// field-for-field so the JSON wire format is identical whether the
// backend is local or remote.
//
// LastActivityAt is omitted for a repo nothing has happened in yet; the
// picker sorts those last.
type RepoActivity struct {
	Prefix         string     `json:"prefix"`
	LastActivityAt *time.Time `json:"last_activity_at,omitempty"`
	ActiveJobs     int        `json:"active_jobs"`
}

// RepoActivities (local) reads the store's single cross-repo aggregate.
// The store's RepoID is dropped — callers key on prefix.
func (c *localClient) RepoActivities(ctx context.Context) ([]RepoActivity, error) {
	rows, err := c.store.ListRepoActivity()
	if err != nil {
		return nil, err
	}
	out := make([]RepoActivity, 0, len(rows))
	for _, r := range rows {
		out = append(out, RepoActivity{
			Prefix:         r.Prefix,
			LastActivityAt: r.LastActivityAt,
			ActiveJobs:     r.ActiveJobs,
		})
	}
	return out, nil
}

// RepoActivities (remote) — GET /repos/activity.
func (c *remoteClient) RepoActivities(ctx context.Context) ([]RepoActivity, error) {
	var out []RepoActivity
	if err := c.do(ctx, http.MethodGet, "/repos/activity", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}
