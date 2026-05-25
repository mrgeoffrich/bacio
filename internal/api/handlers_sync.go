package api

// HTTP read + setup surface for the BACI-89 background sync feature.
// Six routes:
//
//   - GET /repos/{prefix}/sync          — per-repo sync status
//   - GET /sync                         — every tracked repo's status
//   - GET /sync/repos                   — sync-repo registry (BACI-107)
//   - GET / PUT /settings/sync-preferences — the background-sync toggle
//   - POST /repos/{prefix}/sync/setup   — set up sync for a repo (BACI-110)
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
//
// The setup endpoint (BACI-110) is the HTTP equivalent of the existing
// `bacio sync init` / `bacio sync clone` CLI verbs. It resolves the
// project root from the URL prefix (no cwd detection), acquires
// AcquireSyncLock so the call can't race a background controller tick,
// and dispatches to the engine's InitSyncRepo or CloneSyncRepo. v1 is
// synchronous with a generous timeout — async-with-polling is a noted
// follow-up. Renumber collisions return 409 + the preview body; the
// caller re-POSTs with allow_renumber=true to confirm.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
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

// ---------- sync setup (BACI-110) ----------

// SyncSetupOut is the response shape for POST /repos/{prefix}/sync/setup.
// Mode echoes the request so the caller can confirm which engine path
// ran without re-parsing its own request. Init/Clone carry the
// engine's structured result; only the field for the chosen mode is
// populated, and only on success. PreviewCollisions is set on the 409
// renumber-collision response (mode="clone" or "attach" only); when it
// is set, neither Init nor Clone is populated and the call wrote
// nothing.
type SyncSetupOut struct {
	Mode              string                   `json:"mode"`
	Init              *bsync.InitResult        `json:"init,omitempty"`
	Clone             *bsync.CloneResult       `json:"clone,omitempty"`
	PreviewCollisions *bsync.CollisionPreview  `json:"preview_collisions,omitempty"`
}

// syncSetupTimeout bounds one synchronous setup call. v1 is sync-with-
// generous-timeout; an async-with-polling follow-up is noted on
// BACI-110. The cap covers a fresh clone of a chunky team sync repo
// plus the import — anything longer suggests a stuck git network or a
// pathological collision-resolution pass and is better surfaced as a
// 5xx than left to drift.
const syncSetupTimeout = 5 * time.Minute

// handleSyncSetup is the POST /repos/{prefix}/sync/setup handler. Three
// modes — init / clone / attach — dispatch into the engine's
// InitSyncRepo / CloneSyncRepo entry points, with a per-call timeout
// and the cross-process sync lock held for the duration.
func (d deps) handleSyncSetup(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	// A phantom (path=="") has no working tree; we can't write its
	// .bacio/config.yaml. Surface the explicit reason rather than
	// letting the engine fail deep with a misleading "not a git repo"
	// error.
	if repo.Path == "" {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"repo has no local working tree (phantom); link it via POST /repos/{prefix}/link first",
			map[string]any{"field": "prefix"})
		return
	}

	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	parsed, _, err := inputio.DecodeStrict[inputs.SyncSetupInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(parsed.Mode))
	if mode != "init" && mode != "clone" && mode != "attach" {
		writeError(w, http.StatusBadRequest, "invalid_input",
			`mode must be one of "init", "clone", "attach"`,
			map[string]any{"field": "mode"})
		return
	}

	// Per-mode argument validation. The engine layer also validates,
	// but surfacing a 400 here keeps the message field-anchored.
	switch mode {
	case "init":
		if parsed.LocalPath == "" {
			writeError(w, http.StatusBadRequest, "invalid_input",
				`mode="init" requires local_path`,
				map[string]any{"field": "local_path"})
			return
		}
	case "clone":
		if parsed.Remote == "" {
			writeError(w, http.StatusBadRequest, "invalid_input",
				`mode="clone" requires remote`,
				map[string]any{"field": "remote"})
			return
		}
	case "attach":
		if parsed.Remote == "" {
			writeError(w, http.StatusBadRequest, "invalid_input",
				`mode="attach" requires remote (the registry key to look up)`,
				map[string]any{"field": "remote"})
			return
		}
		if parsed.LocalPath != "" {
			writeError(w, http.StatusBadRequest, "invalid_input",
				`mode="attach" must not set local_path — the registry's local_path is used`,
				map[string]any{"field": "local_path"})
			return
		}
	}

	dryRun := isDryRun(r)
	actor := ActorFromContext(r.Context())

	// Acquire the cross-process sync lock so a background controller
	// tick can't race the bootstrap. Held for the whole call (even on
	// dry-run, so the projection matches what a live call would see).
	dbPath := d.opts.DBPath
	if dbPath == "" {
		def, err := store.DefaultPath()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
			return
		}
		dbPath = def
	}
	release, err := bsync.AcquireSyncLock(dbPath)
	if err != nil {
		// ErrLockTimeout is the only expected error here — the only
		// reason AcquireSyncLock blocks is the timeout, which means
		// another bacio sync is mid-flight. Surface 503 so the caller
		// can retry rather than treat it as a permanent failure.
		if errors.Is(err, bsync.ErrLockTimeout) {
			writeError(w, http.StatusServiceUnavailable, "sync_busy", err.Error(), nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	defer release() //nolint:errcheck — best-effort lock release

	ctx, cancel := context.WithTimeout(r.Context(), syncSetupTimeout)
	defer cancel()

	eng := &bsync.Engine{Store: d.store, Actor: actor, DryRun: dryRun}

	switch mode {
	case "init":
		d.runSyncSetupInit(ctx, w, r, eng, repo, parsed, dryRun, actor)
	case "clone":
		d.runSyncSetupClone(ctx, w, r, eng, repo, parsed, dryRun, actor)
	case "attach":
		d.runSyncSetupAttach(ctx, w, r, eng, repo, parsed, dryRun, actor)
	}
}

// runSyncSetupInit handles mode="init" — InitSyncRepo against the
// supplied local_path. Mirrors `bacio sync init` (internal/cli/sync.go).
func (d deps) runSyncSetupInit(ctx context.Context, w http.ResponseWriter, r *http.Request,
	eng *bsync.Engine, repo *model.Repo, in *inputs.SyncSetupInput, dryRun bool, actor string) {
	res, err := eng.InitSyncRepo(ctx, repo.Path, bsync.InitOptions{
		LocalPath: in.LocalPath,
		Remote:    in.Remote,
	})
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if !dryRun {
		recordOp(d.store, d.logger, model.HistoryEntry{
			Actor:      actor,
			RepoID:     &repo.ID,
			RepoPrefix: repo.Prefix,
			Op:         "sync.init",
			Kind:       "repo",
			Details: fmt.Sprintf("local=%s remote=%s commit=%s pushed=%v attached=%v",
				res.LocalPath, res.Remote, res.CommitSHA, res.Pushed, res.Attached),
		})
		if res.Import != nil {
			recordSyncImportOps(d.store, d.logger, res.Import)
		}
	}
	out := &SyncSetupOut{Mode: "init", Init: res}
	if dryRun {
		writeDryRun(w, http.StatusOK, out)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// runSyncSetupClone handles mode="clone" — CloneSyncRepo with the
// caller-supplied local_path (or the engine's default). Honours
// AllowRenumber for the BACI-4 collision-preview gate; on a refused
// clone (collisions + !AllowRenumber + !DryRun), the engine populates
// res.PreviewCollisions and we return 409 + that preview.
func (d deps) runSyncSetupClone(ctx context.Context, w http.ResponseWriter, r *http.Request,
	eng *bsync.Engine, repo *model.Repo, in *inputs.SyncSetupInput, dryRun bool, actor string) {
	res, err := eng.CloneSyncRepo(ctx, repo.Path, bsync.CloneOptions{
		LocalPath:     in.LocalPath,
		Remote:        in.Remote,
		AllowRenumber: in.AllowRenumber,
		DryRun:        dryRun,
	})
	if err != nil {
		// Collision-refused path: the engine returns a non-nil res
		// with PreviewCollisions populated AND an error. Surface the
		// preview as a 409 so the caller can render it and re-POST
		// with allow_renumber=true.
		if res != nil && res.PreviewCollisions != nil {
			writeJSON(w, http.StatusConflict, &SyncSetupOut{
				Mode:              "clone",
				PreviewCollisions: res.PreviewCollisions,
			})
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if !dryRun {
		recordOp(d.store, d.logger, model.HistoryEntry{
			Actor:      actor,
			RepoID:     &repo.ID,
			RepoPrefix: repo.Prefix,
			Op:         "sync.clone",
			Kind:       "repo",
			Details:    fmt.Sprintf("local=%s remote=%s", res.LocalPath, res.Remote),
		})
		if res.Import != nil {
			recordSyncImportOps(d.store, d.logger, res.Import)
		}
	}
	out := &SyncSetupOut{Mode: "clone", Clone: res}
	if dryRun {
		writeDryRun(w, http.StatusOK, out)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// runSyncSetupAttach handles mode="attach" — looks the remote up in
// the sync_remotes registry, then routes through CloneSyncRepo against
// the registry's local_path. The clone path short-circuits via
// openOrCloneSyncRepo when the directory exists and carries the bacio
// sentinel, so no git clone runs and the call boils down to an
// additive import + writing .bacio/config.yaml.
func (d deps) runSyncSetupAttach(ctx context.Context, w http.ResponseWriter, r *http.Request,
	eng *bsync.Engine, repo *model.Repo, in *inputs.SyncSetupInput, dryRun bool, actor string) {
	rec, err := d.store.GetSyncRemote(in.Remote)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("no sync_remotes registry entry for remote %q; use mode=\"clone\" to add one", in.Remote),
				map[string]any{"field": "remote"})
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	res, err := eng.CloneSyncRepo(ctx, repo.Path, bsync.CloneOptions{
		LocalPath:     rec.LocalPath,
		Remote:        in.Remote,
		AllowRenumber: in.AllowRenumber,
		DryRun:        dryRun,
	})
	if err != nil {
		if res != nil && res.PreviewCollisions != nil {
			writeJSON(w, http.StatusConflict, &SyncSetupOut{
				Mode:              "attach",
				PreviewCollisions: res.PreviewCollisions,
			})
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if !dryRun {
		// Same op verb as the clone path — the engine's behaviour is
		// identical (import + write config), and the audit log is
		// kept narrow (sync.init / sync.clone) to match the CLI.
		recordOp(d.store, d.logger, model.HistoryEntry{
			Actor:      actor,
			RepoID:     &repo.ID,
			RepoPrefix: repo.Prefix,
			Op:         "sync.clone",
			Kind:       "repo",
			Details:    fmt.Sprintf("local=%s remote=%s attach=true", res.LocalPath, res.Remote),
		})
		if res.Import != nil {
			recordSyncImportOps(d.store, d.logger, res.Import)
		}
	}
	out := &SyncSetupOut{Mode: "attach", Clone: res}
	if dryRun {
		writeDryRun(w, http.StatusOK, out)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// recordSyncImportOps mirrors the helper in internal/cli/sync.go: one
// sync.import row plus per-renumber / per-rename / per-deletion audit
// entries for the side effects of an import. Lives in the api package
// so the HTTP setup handler doesn't import internal/cli. Dry-run leaves
// no audit trail.
func recordSyncImportOps(s *store.Store, logger *slog.Logger, res *bsync.ImportResult) {
	if res == nil {
		return
	}
	details := fmt.Sprintf("repos=%d issues=%d features=%d documents=%d comments=%d inserted=%d updated=%d noop=%d",
		res.Repos, res.Issues, res.Features, res.Documents, res.Comments,
		res.Inserted, res.Updated, res.NoOp)
	if res.Skipped > 0 {
		details += fmt.Sprintf(" skipped=%d", res.Skipped)
	}
	recordOp(s, logger, model.HistoryEntry{
		Op:      "sync.import",
		Kind:    "repo",
		Details: details,
	})
	for _, sk := range res.SkippedStale {
		recordOp(s, logger, model.HistoryEntry{
			Op:          "sync.skip_stale_remote",
			Kind:        sk.Kind,
			TargetLabel: sk.Label,
			Details:     fmt.Sprintf("uuid=%s local=%s remote=%s", sk.UUID, sk.LocalUpdated, sk.RemoteUpdated),
		})
	}
	for _, r := range res.Renumbered {
		recordOp(s, logger, model.HistoryEntry{
			Op:          "sync.renumber",
			Kind:        "issue",
			TargetLabel: fmt.Sprintf("%s-%d", r.Prefix, r.NewNumber),
			Details:     fmt.Sprintf("from %s-%d (uuid=%s)", r.Prefix, r.OldNumber, r.UUID),
		})
	}
	for _, r := range res.Renamed {
		recordOp(s, logger, model.HistoryEntry{
			Op:          "sync.rename",
			Kind:        r.Kind,
			TargetLabel: r.New,
			Details:     fmt.Sprintf("from %s (uuid=%s)", r.Old, r.UUID),
		})
	}
	for _, d := range res.Deleted {
		recordOp(s, logger, model.HistoryEntry{
			Op:          "sync.delete",
			Kind:        d.Kind,
			TargetLabel: d.Label,
			Details:     fmt.Sprintf("uuid=%s", d.UUID),
		})
	}
}
