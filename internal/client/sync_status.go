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
	MirroredBy        string     `json:"mirrored_by,omitempty"`
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
	coverage, err := sync.MirrorCoverage(c.store)
	if err != nil {
		return nil, err
	}
	out := make([]SyncStatus, 0, len(repos))
	for _, repo := range repos {
		st := SyncStatus{Prefix: repo.Prefix, BackgroundEnabled: bgEnabled}
		// BACI-376: mirror coverage is independent of this repo's own
		// config — the export is whole-DB, so a project with no sync
		// config is mirrored all the same. Applied first; a repo that
		// also drives its own tick overwrites the timestamps below.
		if src, ok := coverage[repo.Prefix]; ok {
			st.MirroredBy = src.Label
			st.LastSyncAt = src.LastSyncAt
			st.LastError = src.LastError
		}
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

// ---------- sync-repo registry (BACI-107 / BACI-108) ----------

// SyncRegistry is the BACI-108 desktop / web Sync view payload — the
// registry of sync repos this machine knows (one per `sync_remotes`
// row) plus the tracked project repos that don't yet have a sync.remote
// in their .bacio/config.yaml ("Unsynced projects" in the UI).
//
// Mirrors api.SyncRegistryOut field-for-field so the JSON wire format
// is identical whether the backend is local or remote. Snake-case JSON
// tags match every other api.http.ts wire DTO; the Wails service in
// desktop/settingsservice.go re-shapes to camelCase for the React tree.
type SyncRegistry struct {
	SyncRepos        []SyncRepoEntry   `json:"sync_repos"`
	UnsyncedProjects []UnsyncedProject `json:"unsynced_projects"`
}

// SyncRepoEntry is one entry in the registry — a `sync_remotes` row
// enriched with the URL-derived label, the global in-progress flag,
// and the project prefixes the sync repo's local clone carries.
// Mirrors api.SyncRepoOut.
type SyncRepoEntry struct {
	RemoteURL  string          `json:"remote_url"`
	Label      string          `json:"label"`
	LocalPath  string          `json:"local_path"`
	ClonedAt   time.Time       `json:"cloned_at"`
	LastSyncAt *time.Time      `json:"last_sync_at,omitempty"`
	LastError  *string         `json:"last_error,omitempty"`
	InProgress bool            `json:"in_progress"`
	Projects   []MemberProject `json:"projects"`
}

// MemberProject is one project entry inside SyncRepoEntry.Projects —
// classified against the local DB as linked / phantom / absent.
// Mirrors api.MemberProjectOut.
type MemberProject struct {
	Prefix string `json:"prefix"`
	Name   string `json:"name"`
	UUID   string `json:"uuid,omitempty"`
	Status string `json:"status"`
}

// UnsyncedProject is one entry in SyncRegistry.UnsyncedProjects: a
// tracked project repo (non-empty path) that doesn't yet have a
// sync.remote in its .bacio/config.yaml, or whose remote isn't in the
// registry. Mirrors api.UnsyncedProjectOut.
type UnsyncedProject struct {
	Prefix string `json:"prefix"`
	Name   string `json:"name"`
	UUID   string `json:"uuid"`
	Path   string `json:"path"`
}

// SyncRegistry (local) composes the BACI-105 primitives —
// ListSyncRemotes + DiscoverMembership + DeriveSyncLabel — with
// ReadProjectConfig in the same shape as internal/api/handlers_sync.go's
// handleSyncRegistryList. No new store / filesystem code; the handler's
// pass over the registry + the unsynced residual is the reference. The
// in-progress bit is the localClient's process-local "is a sync runner
// mid-tick" flag; the local client isn't the controller, so it always
// reports false (matches SyncStatuses' precedent above).
func (c *localClient) SyncRegistry(ctx context.Context) (*SyncRegistry, error) {
	remotes, err := c.store.ListSyncRemotes()
	if err != nil {
		return nil, err
	}
	repos, err := c.store.ListRepos()
	if err != nil {
		return nil, err
	}
	// First pass: assemble the registry list AND remember every prefix
	// the registry already accounts for (linked or phantom under any
	// sync repo). Absent prefixes don't disqualify a tracked repo from
	// unsynced_projects.
	accountedPrefixes := make(map[string]bool)
	registryURLs := make(map[string]bool, len(remotes))
	syncRepos := make([]SyncRepoEntry, 0, len(remotes))
	for _, rec := range remotes {
		registryURLs[rec.RemoteURL] = true
		members, err := sync.DiscoverMembership(rec.LocalPath, c.store)
		if err != nil {
			// Membership failure on one row shouldn't sink the whole
			// payload — drop to an empty membership list so the UI still
			// renders the registry card. (Matches handler precedent.)
			members = nil
		}
		entries := make([]MemberProject, 0, len(members))
		for _, m := range members {
			if m.Status == sync.StatusLinked || m.Status == sync.StatusPhantom {
				accountedPrefixes[m.Prefix] = true
			}
			entry := MemberProject{
				Prefix: m.Prefix,
				Status: string(m.Status),
			}
			if m.Repo != nil {
				entry.Name = m.Repo.Name
				entry.UUID = m.Repo.UUID
			}
			entries = append(entries, entry)
		}
		syncRepos = append(syncRepos, SyncRepoEntry{
			RemoteURL:  rec.RemoteURL,
			Label:      sync.DeriveSyncLabel(rec.RemoteURL),
			LocalPath:  rec.LocalPath,
			ClonedAt:   rec.ClonedAt,
			LastSyncAt: rec.LastSyncAt,
			LastError:  rec.LastSyncError,
			InProgress: false,
			Projects:   entries,
		})
	}
	// Second pass: the unsynced-projects residual. A repo qualifies iff
	// it has a real working tree AND it isn't already surfaced via the
	// registry AND its .bacio/config.yaml either is missing, empty, or
	// points at a remote the registry doesn't carry.
	unsynced := make([]UnsyncedProject, 0)
	for _, repo := range repos {
		if repo.Path == "" {
			continue
		}
		if accountedPrefixes[repo.Prefix] {
			continue
		}
		if isRegisteredRemote(repo.Path, registryURLs) {
			continue
		}
		unsynced = append(unsynced, UnsyncedProject{
			Prefix: repo.Prefix,
			Name:   repo.Name,
			UUID:   repo.UUID,
			Path:   repo.Path,
		})
	}
	return &SyncRegistry{SyncRepos: syncRepos, UnsyncedProjects: unsynced}, nil
}

// isRegisteredRemote reports whether the project repo at path has a
// .bacio/config.yaml whose sync.remote is in the registry. A missing
// or broken config returns false — the repo just isn't set up for
// sync, and the user still sees it in "Unsynced projects" so they can
// fix it. Mirrors handlers_sync.go's syncedRepoIsRegistered minus the
// logger plumbing (localClient has no slog handle and the handler does
// the broken-config logging server-side).
func isRegisteredRemote(path string, registryURLs map[string]bool) bool {
	cfg, err := sync.ReadProjectConfig(path)
	if err != nil {
		return false
	}
	if cfg.Sync.Remote == "" {
		return false
	}
	return registryURLs[cfg.Sync.Remote]
}

// SyncRegistry (remote) — GET /sync/repos.
func (c *remoteClient) SyncRegistry(ctx context.Context) (*SyncRegistry, error) {
	var out SyncRegistry
	if err := c.do(ctx, http.MethodGet, "/sync/repos", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
