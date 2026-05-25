package api

// HTTP read surface for the BACI-89 background sync feature. Four
// routes:
//
//   - GET /repos/{prefix}/sync          — per-repo sync status
//   - GET /sync                         — every tracked repo's status
//   - GET /sync/repos                   — sync-repo registry (BACI-107)
//   - GET / PUT /settings/sync-preferences — the background-sync toggle
//
// "Sync status" is read straight from the shared SQLite store (the
// sync_remotes row's last_sync_at / last_sync_error) plus the project
// repo's machine-local .bacio/config.yaml. The in_progress flag is the
// one process-local bit — it reflects this process's background sync
// runner, so it's only meaningful on the leader. A non-leader bacio
// api always reports in_progress: false; last_sync_at (DB-backed) is
// the authoritative "is it mirrored" signal.
//
// The registry endpoint (BACI-107) inverts the per-repo /sync shape:
// one card per sync repo with its project members nested, plus an
// "unsynced projects" section for tracked repos that don't yet have a
// sync.remote in their .bacio/config.yaml. It composes the BACI-105
// primitives — ListSyncRemotes + DiscoverMembership + DeriveSyncLabel —
// with ReadProjectConfig; no new store calls, no new filesystem walking.

import (
	"fmt"
	"net/http"
	"time"

	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	bsync "github.com/mrgeoffrich/bacio/internal/sync"
)

// SyncStatusOut is the response shape for GET /repos/{prefix}/sync and
// each element of GET /sync. Snake-case on the wire to match the rest
// of the api; api.http.ts reshapes to camelCase.
type SyncStatusOut struct {
	// Prefix is the repo this status belongs to. Always set on the
	// cross-repo /sync list; redundant-but-harmless on the per-repo route.
	Prefix string `json:"prefix"`
	// Configured is true when the repo has a parseable
	// .bacio/config.yaml with a sync.remote AND a resolvable
	// sync_remotes row (a local clone). When false, every other field
	// below is zero — the repo simply isn't set up for sync.
	Configured bool `json:"configured"`
	// BackgroundEnabled mirrors the global sync.background_enabled
	// toggle. It is repo-independent (one global flag) but echoed on
	// every status so a single-repo client doesn't need a second call.
	BackgroundEnabled bool `json:"background_enabled"`
	// InProgress is true while this process's background sync runner
	// is mid-tick. Only meaningful on the leader process.
	InProgress bool `json:"in_progress"`
	// LastSyncAt is the time of the last successful sync, or nil.
	LastSyncAt *time.Time `json:"last_sync_at,omitempty"`
	// LastError is the failure message from the last sync run, or nil
	// when the last run succeeded.
	LastError *string `json:"last_error,omitempty"`
	// Remote is the canonical sync remote URL (empty when not configured).
	Remote string `json:"remote,omitempty"`
}

// syncStatusForRepo assembles a SyncStatusOut for one tracked repo. It
// never errors on a not-set-up repo — it returns Configured:false. The
// inProgress flag is supplied by the caller (read once from the leader
// service per request).
func syncStatusForRepo(s *store.Store, repo *model.Repo, bgEnabled, inProgress bool) SyncStatusOut {
	out := SyncStatusOut{
		Prefix:            repo.Prefix,
		BackgroundEnabled: bgEnabled,
	}
	if repo.Path == "" {
		return out
	}
	cfg, err := bsync.ReadProjectConfig(repo.Path)
	if err != nil {
		// ErrNoConfig or a broken config: not configured for sync.
		return out
	}
	if cfg.Sync.Remote == "" {
		return out
	}
	rec, err := s.GetSyncRemote(cfg.Sync.Remote)
	if err != nil {
		// A config with a remote but no local clone on this machine —
		// surface the remote but report not-configured (sync can't run).
		out.Remote = cfg.Sync.Remote
		return out
	}
	out.Configured = true
	out.Remote = cfg.Sync.Remote
	out.InProgress = inProgress
	out.LastSyncAt = rec.LastSyncAt
	out.LastError = rec.LastSyncError
	return out
}

// syncBackgroundInProgress reads this process's background sync runner
// in-flight flag, tolerating a nil leader service (tests / inert
// handler).
func (d deps) syncBackgroundInProgress() bool {
	if d.leader == nil {
		return false
	}
	return d.leader.syncInProgress()
}

func (d deps) handleSyncStatusGet(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	bgEnabled, err := d.store.GetSyncBackgroundEnabled()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	out := syncStatusForRepo(d.store, repo, bgEnabled, d.syncBackgroundInProgress())
	writeJSON(w, http.StatusOK, &out)
}

func (d deps) handleSyncStatusList(w http.ResponseWriter, r *http.Request) {
	repos, err := d.store.ListRepos()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	bgEnabled, err := d.store.GetSyncBackgroundEnabled()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	inProgress := d.syncBackgroundInProgress()
	out := make([]SyncStatusOut, 0, len(repos))
	for _, repo := range repos {
		out = append(out, syncStatusForRepo(d.store, repo, bgEnabled, inProgress))
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- sync preferences (the background-sync toggle) ----------

// SyncPreferencesOut is the response shape for GET/PUT
// /settings/sync-preferences. Mirrors BoardPreferencesOut.
type SyncPreferencesOut struct {
	BackgroundEnabled bool `json:"background_enabled"`
}

// syncPreferencesIn is the strict-decoded input for PUT.
type syncPreferencesIn struct {
	BackgroundEnabled bool `json:"background_enabled"`
}

func (d deps) handleSyncPreferencesGet(w http.ResponseWriter, r *http.Request) {
	enabled, err := d.store.GetSyncBackgroundEnabled()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, &SyncPreferencesOut{BackgroundEnabled: enabled})
}

func (d deps) handleSyncPreferencesSet(w http.ResponseWriter, r *http.Request) {
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	parsed, _, err := inputio.DecodeStrict[syncPreferencesIn](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, &SyncPreferencesOut{BackgroundEnabled: parsed.BackgroundEnabled})
		return
	}
	if err := d.store.SetSyncBackgroundEnabled(parsed.BackgroundEnabled); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		Actor:       ActorFromContext(r.Context()),
		Op:          "sync_pref.update",
		Kind:        "app_setting",
		TargetLabel: "sync.background_enabled",
		Details:     fmt.Sprintf("background_enabled=%t", parsed.BackgroundEnabled),
	})
	writeJSON(w, http.StatusOK, &SyncPreferencesOut{BackgroundEnabled: parsed.BackgroundEnabled})
}

// ---------- sync-repo registry (BACI-107) ----------

// SyncRegistryOut is the response shape for GET /sync/repos: the
// registry of sync repos this machine knows (one per `sync_remotes`
// row) plus the tracked project repos that don't yet have a sync.remote
// in their .bacio/config.yaml ("Unsynced projects" in the UI).
//
// Both slices are non-nil even when empty so the JSON encoder emits
// `[]` rather than `null` — a one-shot poll that a desktop / web view
// can re-render unconditionally.
type SyncRegistryOut struct {
	SyncRepos        []SyncRepoOut        `json:"sync_repos"`
	UnsyncedProjects []UnsyncedProjectOut `json:"unsynced_projects"`
}

// SyncRepoOut is one entry in the registry — a `sync_remotes` row
// enriched with the URL-derived label, the global in-progress flag,
// and the project prefixes the sync repo's local clone carries.
type SyncRepoOut struct {
	// RemoteURL is the canonical sync remote (the primary key of
	// sync_remotes, also what each project's .bacio/config.yaml carries).
	RemoteURL string `json:"remote_url"`
	// Label is the trailing-segment-with-.git-stripped derivation of
	// RemoteURL, via sync.DeriveSyncLabel — one source of truth shared
	// with CLI / TUI / desktop / web.
	Label string `json:"label"`
	// LocalPath is where this machine has the sync repo cloned.
	LocalPath string `json:"local_path"`
	// ClonedAt is when the local clone was first created on this machine.
	ClonedAt time.Time `json:"cloned_at"`
	// LastSyncAt is the last successful sync against this remote, or nil
	// when none has succeeded yet.
	LastSyncAt *time.Time `json:"last_sync_at,omitempty"`
	// LastError is the failure message from the last sync run, or nil
	// when the last run succeeded.
	LastError *string `json:"last_error,omitempty"`
	// InProgress is the one process-local bit — true while this
	// process's background sync runner is mid-tick. Echoed on every
	// entry because today's runner serialises across all sync-enabled
	// repos in one tick; per-row precision would need runner changes.
	InProgress bool `json:"in_progress"`
	// Projects is the list of project prefixes the sync repo's local
	// clone carries, classified against the local DB by
	// sync.DiscoverMembership. Empty when the clone is missing /
	// unreadable; never nil so the JSON encoder emits `[]`.
	Projects []MemberProjectOut `json:"projects"`
}

// MemberProjectOut is one project entry inside a SyncRepoOut.Projects.
// Mirrors sync.MemberProject's three-status enum but flattens the
// embedded *model.Repo into the leaf string fields the UI actually
// renders.
type MemberProjectOut struct {
	// Prefix is the project's 4-letter key prefix (always set).
	Prefix string `json:"prefix"`
	// Name is the local repo's display name. Empty for "absent" status
	// (the prefix exists on disk in the sync repo, but the local DB has
	// no row for it) and empty for phantoms that pre-date Name capture.
	Name string `json:"name"`
	// UUID is the local repo's UUID, empty for absent entries.
	UUID string `json:"uuid,omitempty"`
	// Status is one of "linked", "phantom", "absent" — mirrors
	// sync.MembershipStatus.
	Status string `json:"status"`
}

// UnsyncedProjectOut is one entry in SyncRegistryOut.UnsyncedProjects:
// a tracked project repo (non-empty path) that doesn't yet have a
// `sync.remote` in its .bacio/config.yaml, or whose remote isn't in the
// registry. Phantom rows (path == "") are excluded — they're already
// surfaced under their sync repo's Projects.
type UnsyncedProjectOut struct {
	Prefix string `json:"prefix"`
	Name   string `json:"name"`
	UUID   string `json:"uuid"`
	Path   string `json:"path"`
}

// handleSyncRegistryList is the GET /sync/repos handler. Composes the
// registry via sync.BuildRegistry (BACI-109 lifted the three-pass body
// into internal/sync so the TUI shares the same code) and maps the
// resulting Registry into the snake-case wire DTOs declared above.
//
// The in-progress bit lives only here — it's process-local (only
// meaningful on the leader) so the shared sync.Registry deliberately
// does NOT carry it; the handler stitches it onto each SyncRepoOut.
func (d deps) handleSyncRegistryList(w http.ResponseWriter, r *http.Request) {
	reg, err := bsync.BuildRegistry(d.store, d.logger)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	inProgress := d.syncBackgroundInProgress()
	syncRepos := make([]SyncRepoOut, 0, len(reg.SyncRepos))
	for _, e := range reg.SyncRepos {
		syncRepos = append(syncRepos, SyncRepoOut{
			RemoteURL:  e.RemoteURL,
			Label:      e.Label,
			LocalPath:  e.LocalPath,
			ClonedAt:   e.ClonedAt,
			LastSyncAt: e.LastSyncAt,
			LastError:  e.LastError,
			InProgress: inProgress,
			Projects:   memberProjectsOutFor(e.Members),
		})
	}
	unsynced := make([]UnsyncedProjectOut, 0, len(reg.UnsyncedProjects))
	for _, u := range reg.UnsyncedProjects {
		unsynced = append(unsynced, UnsyncedProjectOut{
			Prefix: u.Prefix,
			Name:   u.Name,
			UUID:   u.UUID,
			Path:   u.Path,
		})
	}
	writeJSON(w, http.StatusOK, &SyncRegistryOut{
		SyncRepos:        syncRepos,
		UnsyncedProjects: unsynced,
	})
}

// memberProjectsOutFor maps the registry's leaf MemberProjectEntry into
// the wire DTO with snake-case JSON tags.
func memberProjectsOutFor(members []bsync.MemberProjectEntry) []MemberProjectOut {
	out := make([]MemberProjectOut, 0, len(members))
	for _, m := range members {
		out = append(out, MemberProjectOut{
			Prefix: m.Prefix,
			Name:   m.Name,
			UUID:   m.UUID,
			Status: string(m.Status),
		})
	}
	return out
}
