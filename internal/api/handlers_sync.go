package api

// HTTP read surface for the BACI-89 background sync feature. Three
// routes:
//
//   - GET /repos/{prefix}/sync          — per-repo sync status
//   - GET /sync                         — every tracked repo's status
//   - GET / PUT /settings/sync-preferences — the background-sync toggle
//
// "Sync status" is read straight from the shared SQLite store (the
// sync_remotes row's last_sync_at / last_sync_error) plus the project
// repo's machine-local .bacio/config.yaml. The in_progress flag is the
// one process-local bit — it reflects this process's background sync
// runner, so it's only meaningful on the leader. A non-leader bacio
// api always reports in_progress: false; last_sync_at (DB-backed) is
// the authoritative "is it mirrored" signal.

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
