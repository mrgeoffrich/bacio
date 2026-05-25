package client

// BACI-111 — cross-transport SetupSync. The local impl mirrors the
// internal/api/handlers_sync.go handleSyncSetup body almost verbatim:
// validates the mode, acquires the cross-process sync lock so the call
// can't race a background controller tick, constructs a sync.Engine,
// and dispatches by mode. The collision case (a clone / attach that
// would renumber or rename local rows without AllowRenumber set) is
// returned as a populated *SyncSetupResult plus the typed sentinel
// ErrSetupCollision so the caller can branch on it and re-POST with
// allow_renumber=true.
//
// Wails desktop reaches this via SettingsService.SetupSync; the web
// bundle reaches it through the remote *bacio api* server. The remote
// impl lives in remote_sync_setup.go.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	bsync "github.com/mrgeoffrich/bacio/internal/sync"
)

// SyncSetupResult is the structured outcome of SetupSync. Mirrors the
// api.SyncSetupOut wire shape so the local and remote backends produce
// identical payloads. Only the field for the chosen mode is populated
// on success; PreviewCollisions is set on the collision-refused path
// (clone / attach only), in which case neither Init nor Clone is
// populated and the call wrote nothing.
type SyncSetupResult struct {
	Mode              string                  `json:"mode"`
	Init              *bsync.InitResult       `json:"init,omitempty"`
	Clone             *bsync.CloneResult      `json:"clone,omitempty"`
	PreviewCollisions *bsync.CollisionPreview `json:"preview_collisions,omitempty"`
}

// ErrSetupCollision is the typed sentinel returned alongside a
// populated *SyncSetupResult when a clone / attach is refused because
// the local DB has data that would be renumbered or renamed by the
// import. The caller renders the preview and re-submits with
// AllowRenumber=true to confirm.
var ErrSetupCollision = errors.New("sync setup: local DB has data that would be renumbered/renamed; re-submit with allow_renumber=true to confirm")

// SetupSync (local) — see Client.SetupSync. Mirrors the HTTP handler
// at internal/api/handlers_sync.go:handleSyncSetup field-for-field.
func (c *localClient) SetupSync(ctx context.Context, repo *model.Repo, in inputs.SyncSetupInput) (*SyncSetupResult, error) {
	if repo == nil {
		return nil, fmt.Errorf("SetupSync: repo is nil")
	}
	// A phantom (path=="") has no working tree; we can't write its
	// .bacio/config.yaml. Surface the explicit reason rather than
	// letting the engine fail deep with a misleading "not a git repo"
	// error. Mirrors the handler's pre-flight.
	if repo.Path == "" {
		return nil, fmt.Errorf("repo %s has no local working tree (phantom); link it first", repo.Prefix)
	}

	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	switch mode {
	case "init":
		if in.LocalPath == "" {
			return nil, fmt.Errorf(`mode="init" requires local_path`)
		}
	case "clone":
		if in.Remote == "" {
			return nil, fmt.Errorf(`mode="clone" requires remote`)
		}
	case "attach":
		if in.Remote == "" {
			return nil, fmt.Errorf(`mode="attach" requires remote (the registry key to look up)`)
		}
		if in.LocalPath != "" {
			return nil, fmt.Errorf(`mode="attach" must not set local_path — the registry's local_path is used`)
		}
	default:
		return nil, fmt.Errorf(`mode must be one of "init", "clone", "attach"`)
	}

	// Resolve the DB path for the cross-process sync lock — same source
	// the handler uses. The lock is held for the whole call so a
	// background controller tick can't race the bootstrap. localClient
	// remembers the path Open was called with; fall back to the default
	// location when the client was constructed via NewLocalFromStore
	// (which doesn't currently propagate a path — the REST handler is
	// the only caller and it has its own handler for sync setup).
	dbPath := c.dbPath
	if dbPath == "" {
		def, err := store.DefaultPath()
		if err != nil {
			return nil, fmt.Errorf("resolve db path: %w", err)
		}
		dbPath = def
	}
	release, err := bsync.AcquireSyncLock(dbPath)
	if err != nil {
		// ErrLockTimeout is the expected case; pass it through verbatim
		// so callers can errors.Is against it if they care.
		return nil, err
	}
	defer release() //nolint:errcheck — best-effort lock release

	eng := &bsync.Engine{Store: c.store, Actor: c.actor}

	switch mode {
	case "init":
		return c.runSetupInit(ctx, eng, repo, in)
	case "clone":
		return c.runSetupClone(ctx, eng, repo, in, in.Remote, "")
	case "attach":
		rec, err := c.store.GetSyncRemote(in.Remote)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, fmt.Errorf("no sync_remotes registry entry for remote %q; use mode=\"clone\" to add one", in.Remote)
			}
			return nil, err
		}
		return c.runSetupClone(ctx, eng, repo, in, in.Remote, rec.LocalPath)
	}
	// unreachable — the switch above covers every accepted mode.
	return nil, fmt.Errorf("internal: unhandled mode %q", mode)
}

// runSetupInit dispatches mode="init" and stamps the audit row.
// Returns the engine's result wrapped in a SyncSetupResult.
func (c *localClient) runSetupInit(ctx context.Context, eng *bsync.Engine, repo *model.Repo, in inputs.SyncSetupInput) (*SyncSetupResult, error) {
	res, err := eng.InitSyncRepo(ctx, repo.Path, bsync.InitOptions{
		LocalPath: in.LocalPath,
		Remote:    in.Remote,
	})
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op:   "sync.init",
		Kind: "repo",
		Details: fmt.Sprintf("local=%s remote=%s commit=%s pushed=%v attached=%v",
			res.LocalPath, res.Remote, res.CommitSHA, res.Pushed, res.Attached),
	})
	if res.Import != nil {
		c.recordSyncImportOps(res.Import)
	}
	return &SyncSetupResult{Mode: "init", Init: res}, nil
}

// runSetupClone dispatches the shared CloneSyncRepo path used by
// mode="clone" (caller-supplied LocalPath) and mode="attach"
// (registry-resolved LocalPath). The mode string is preserved on the
// result so the caller can branch — attach writes a slightly different
// audit-row detail. attachLocalPath != "" => attach mode; otherwise
// clone mode.
func (c *localClient) runSetupClone(ctx context.Context, eng *bsync.Engine, repo *model.Repo, in inputs.SyncSetupInput, remote, attachLocalPath string) (*SyncSetupResult, error) {
	localPath := in.LocalPath
	mode := "clone"
	if attachLocalPath != "" {
		localPath = attachLocalPath
		mode = "attach"
	}
	res, err := eng.CloneSyncRepo(ctx, repo.Path, bsync.CloneOptions{
		LocalPath:     localPath,
		Remote:        remote,
		AllowRenumber: in.AllowRenumber,
	})
	if err != nil {
		// Collision-refused path: the engine returns a non-nil res with
		// PreviewCollisions populated AND an error. Surface the preview
		// as the typed sentinel so the caller can re-submit with
		// AllowRenumber=true.
		if res != nil && res.PreviewCollisions != nil {
			return &SyncSetupResult{
				Mode:              mode,
				PreviewCollisions: res.PreviewCollisions,
			}, ErrSetupCollision
		}
		return nil, err
	}
	// Audit. The clone + attach paths share the sync.clone op — same
	// shape the handler uses; attach gets an extra detail token.
	details := fmt.Sprintf("local=%s remote=%s", res.LocalPath, res.Remote)
	if mode == "attach" {
		details += " attach=true"
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op:      "sync.clone",
		Kind:    "repo",
		Details: details,
	})
	if res.Import != nil {
		c.recordSyncImportOps(res.Import)
	}
	return &SyncSetupResult{Mode: mode, Clone: res}, nil
}

// recordSyncImportOps mirrors internal/api/handlers_sync.go's helper
// of the same name — one sync.import row plus per-renumber /
// per-rename / per-deletion audit entries for the side effects of an
// import. Lives on localClient so the audit actor is the client's
// resolved actor (same as every other recordOp call).
func (c *localClient) recordSyncImportOps(res *bsync.ImportResult) {
	if res == nil {
		return
	}
	details := fmt.Sprintf("repos=%d issues=%d features=%d documents=%d comments=%d inserted=%d updated=%d noop=%d",
		res.Repos, res.Issues, res.Features, res.Documents, res.Comments,
		res.Inserted, res.Updated, res.NoOp)
	if res.Skipped > 0 {
		details += fmt.Sprintf(" skipped=%d", res.Skipped)
	}
	c.recordOp(model.HistoryEntry{
		Op:      "sync.import",
		Kind:    "repo",
		Details: details,
	})
	for _, sk := range res.SkippedStale {
		c.recordOp(model.HistoryEntry{
			Op:          "sync.skip_stale_remote",
			Kind:        sk.Kind,
			TargetLabel: sk.Label,
			Details:     fmt.Sprintf("uuid=%s local=%s remote=%s", sk.UUID, sk.LocalUpdated, sk.RemoteUpdated),
		})
	}
	for _, r := range res.Renumbered {
		c.recordOp(model.HistoryEntry{
			Op:          "sync.renumber",
			Kind:        "issue",
			TargetLabel: fmt.Sprintf("%s-%d", r.Prefix, r.NewNumber),
			Details:     fmt.Sprintf("from %s-%d (uuid=%s)", r.Prefix, r.OldNumber, r.UUID),
		})
	}
	for _, r := range res.Renamed {
		c.recordOp(model.HistoryEntry{
			Op:          "sync.rename",
			Kind:        r.Kind,
			TargetLabel: r.New,
			Details:     fmt.Sprintf("from %s (uuid=%s)", r.Old, r.UUID),
		})
	}
	for _, d := range res.Deleted {
		c.recordOp(model.HistoryEntry{
			Op:          "sync.delete",
			Kind:        d.Kind,
			TargetLabel: d.Label,
			Details:     fmt.Sprintf("uuid=%s", d.UUID),
		})
	}
}
