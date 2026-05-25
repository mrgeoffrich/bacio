package client

// Cross-transport sync-setup entry point (BACI-111). The HTTP layer
// already ships `POST /repos/{prefix}/sync/setup` (BACI-110); this
// file adds the matching Client.SetupSync method so the desktop's
// Wails-bound surface and any future direct caller hit the same code
// path. Both the local and remote impls produce a SyncSetupResult and,
// on a renumber-collision refusal, return ErrSetupCollision alongside
// a populated PreviewCollisions — the caller (the desktop / web
// modal) inspects the preview and re-submits with AllowRenumber=true
// to confirm.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	bsync "github.com/mrgeoffrich/bacio/internal/sync"
)

// ErrSetupCollision is the sentinel returned by SetupSync when the
// server (local or remote) refused the call because the import would
// renumber / rename local rows. The accompanying SyncSetupResult
// carries the populated PreviewCollisions; the caller renders the
// preview and re-submits with AllowRenumber=true.
//
// Wrapped via errors.Is from sync.Engine's own collision text on the
// local side, and matched against the 409 status on the remote side.
var ErrSetupCollision = errors.New("sync setup: renumber collision")

// SyncSetupResult is the cross-transport outcome of SetupSync. Mirrors
// api.SyncSetupOut field-for-field so the JSON wire format is
// identical whether the backend is local or remote — only the chosen
// mode's structured result is populated, and on a collision refusal
// neither Init nor Clone is set and PreviewCollisions carries the
// projected churn.
type SyncSetupResult struct {
	Mode              string                  `json:"mode"`
	Init              *bsync.InitResult       `json:"init,omitempty"`
	Clone             *bsync.CloneResult      `json:"clone,omitempty"`
	PreviewCollisions *bsync.CollisionPreview `json:"preview_collisions,omitempty"`
}

// setupSyncTimeout bounds one synchronous setup call. Matches the
// api.handleSyncSetup timeout (5 minutes) — the engine's clone path can
// take a while for a chunky team sync repo + import pass.
const setupSyncTimeout = 5 * time.Minute

// SetupSync (local) dispatches by mode into the same sync.Engine
// entry points the HTTP handler uses (handleSyncSetup). It acquires
// the cross-process sync lock so a background controller tick can't
// race the bootstrap, then runs init / clone / attach against the
// resolved project root. On a collision refusal the returned result
// carries PreviewCollisions and the error is ErrSetupCollision.
//
// Local-only invariants mirror the HTTP handler:
//
//   - The repo must have a working tree (path != ""). Phantoms are
//     refused at this layer; the engine would otherwise fail deep with
//     a misleading "not a git repo" error.
//   - mode is one of "init" / "clone" / "attach" — anything else is a
//     400-equivalent error.
//   - mode="attach" looks the remote up in sync_remotes and reuses the
//     clone path against the registry's local_path. local_path on the
//     input must be empty; passing one is rejected so the caller can't
//     accidentally point at a stale directory.
func (c *localClient) SetupSync(ctx context.Context, repo *model.Repo, in inputs.SyncSetupInput) (*SyncSetupResult, error) {
	if repo == nil {
		return nil, errors.New("SetupSync: repo is nil")
	}
	if repo.Path == "" {
		return nil, fmt.Errorf("repo %s has no local working tree (phantom); link it first", repo.Prefix)
	}
	mode := in.Mode
	switch mode {
	case "init":
		if in.LocalPath == "" {
			return nil, errors.New(`mode="init" requires local_path`)
		}
	case "clone":
		if in.Remote == "" {
			return nil, errors.New(`mode="clone" requires remote`)
		}
	case "attach":
		if in.Remote == "" {
			return nil, errors.New(`mode="attach" requires remote (the registry key to look up)`)
		}
		if in.LocalPath != "" {
			return nil, errors.New(`mode="attach" must not set local_path — the registry's local_path is used`)
		}
	default:
		return nil, fmt.Errorf(`mode must be one of "init", "clone", "attach"; got %q`, mode)
	}

	// Acquire the cross-process sync lock for the duration of the call.
	// Mirrors the HTTP handler's serialisation against the background
	// controller tick.
	dbPath, err := store.DefaultPath()
	if err != nil {
		return nil, err
	}
	release, err := bsync.AcquireSyncLock(dbPath)
	if err != nil {
		return nil, err
	}
	defer release() //nolint:errcheck — best-effort lock release

	ctx, cancel := context.WithTimeout(ctx, setupSyncTimeout)
	defer cancel()

	eng := &bsync.Engine{Store: c.store, Actor: c.actor}

	switch mode {
	case "init":
		res, err := eng.InitSyncRepo(ctx, repo.Path, bsync.InitOptions{
			LocalPath: in.LocalPath,
			Remote:    in.Remote,
		})
		if err != nil {
			return nil, err
		}
		c.recordSetupOps(repo, "sync.init", fmt.Sprintf("local=%s remote=%s commit=%s pushed=%v attached=%v",
			res.LocalPath, res.Remote, res.CommitSHA, res.Pushed, res.Attached), res.Import)
		return &SyncSetupResult{Mode: "init", Init: res}, nil

	case "clone":
		res, err := eng.CloneSyncRepo(ctx, repo.Path, bsync.CloneOptions{
			LocalPath:     in.LocalPath,
			Remote:        in.Remote,
			AllowRenumber: in.AllowRenumber,
		})
		if err != nil {
			// Collision-refused path: the engine returns a non-nil
			// res with PreviewCollisions populated AND an error.
			// Surface the preview alongside ErrSetupCollision so the
			// caller can render it and re-submit with AllowRenumber=true.
			if res != nil && res.PreviewCollisions != nil {
				return &SyncSetupResult{Mode: "clone", PreviewCollisions: res.PreviewCollisions}, ErrSetupCollision
			}
			return nil, err
		}
		c.recordSetupOps(repo, "sync.clone", fmt.Sprintf("local=%s remote=%s", res.LocalPath, res.Remote), res.Import)
		return &SyncSetupResult{Mode: "clone", Clone: res}, nil

	case "attach":
		rec, err := c.store.GetSyncRemote(in.Remote)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, fmt.Errorf("no sync_remotes registry entry for remote %q; use mode=\"clone\" to add one", in.Remote)
			}
			return nil, err
		}
		res, err := eng.CloneSyncRepo(ctx, repo.Path, bsync.CloneOptions{
			LocalPath:     rec.LocalPath,
			Remote:        in.Remote,
			AllowRenumber: in.AllowRenumber,
		})
		if err != nil {
			if res != nil && res.PreviewCollisions != nil {
				return &SyncSetupResult{Mode: "attach", PreviewCollisions: res.PreviewCollisions}, ErrSetupCollision
			}
			return nil, err
		}
		// Same op verb as the clone path — the engine's behaviour is
		// identical (import + write config), and the audit log is kept
		// narrow (sync.init / sync.clone) to match the CLI / HTTP layer.
		c.recordSetupOps(repo, "sync.clone", fmt.Sprintf("local=%s remote=%s attach=true", res.LocalPath, res.Remote), res.Import)
		return &SyncSetupResult{Mode: "attach", Clone: res}, nil
	}

	// Unreachable — the switch above covers every validated mode.
	return nil, fmt.Errorf("setup sync: unhandled mode %q", mode)
}

// recordSetupOps writes the sync.{init,clone} audit row plus any
// per-renumber / per-rename / per-deletion side-effects from the
// import pass. Mirrors api.recordSyncImportOps but lives here so the
// local client doesn't import internal/api.
func (c *localClient) recordSetupOps(repo *model.Repo, op, details string, imp *bsync.ImportResult) {
	c.recordOp(model.HistoryEntry{
		RepoID:     &repo.ID,
		RepoPrefix: repo.Prefix,
		Op:         op,
		Kind:       "repo",
		Details:    details,
	})
	if imp == nil {
		return
	}
	importDetails := fmt.Sprintf("repos=%d issues=%d features=%d documents=%d comments=%d inserted=%d updated=%d noop=%d",
		imp.Repos, imp.Issues, imp.Features, imp.Documents, imp.Comments,
		imp.Inserted, imp.Updated, imp.NoOp)
	if imp.Skipped > 0 {
		importDetails += fmt.Sprintf(" skipped=%d", imp.Skipped)
	}
	c.recordOp(model.HistoryEntry{
		Op:      "sync.import",
		Kind:    "repo",
		Details: importDetails,
	})
	for _, sk := range imp.SkippedStale {
		c.recordOp(model.HistoryEntry{
			Op:          "sync.skip_stale_remote",
			Kind:        sk.Kind,
			TargetLabel: sk.Label,
			Details:     fmt.Sprintf("uuid=%s local=%s remote=%s", sk.UUID, sk.LocalUpdated, sk.RemoteUpdated),
		})
	}
	for _, r := range imp.Renumbered {
		c.recordOp(model.HistoryEntry{
			Op:          "sync.renumber",
			Kind:        "issue",
			TargetLabel: fmt.Sprintf("%s-%d", r.Prefix, r.NewNumber),
			Details:     fmt.Sprintf("from %s-%d (uuid=%s)", r.Prefix, r.OldNumber, r.UUID),
		})
	}
	for _, r := range imp.Renamed {
		c.recordOp(model.HistoryEntry{
			Op:          "sync.rename",
			Kind:        r.Kind,
			TargetLabel: r.New,
			Details:     fmt.Sprintf("from %s (uuid=%s)", r.Old, r.UUID),
		})
	}
	for _, d := range imp.Deleted {
		c.recordOp(model.HistoryEntry{
			Op:          "sync.delete",
			Kind:        d.Kind,
			TargetLabel: d.Label,
			Details:     fmt.Sprintf("uuid=%s", d.UUID),
		})
	}
}

// SetupSync (remote) POSTs to /repos/{prefix}/sync/setup. The endpoint
// returns the standard SyncSetupOut on 200 and the same shape (with
// preview_collisions populated, init/clone empty) on 409 — which the
// generic c.do helper can't unwrap, since it would treat any non-2xx
// as an error envelope. We send the request directly here so the 409
// body decodes into a SyncSetupResult and surfaces ErrSetupCollision
// to the caller.
func (c *remoteClient) SetupSync(ctx context.Context, repo *model.Repo, in inputs.SyncSetupInput) (*SyncSetupResult, error) {
	if repo == nil {
		return nil, errors.New("SetupSync: repo is nil")
	}
	u := *c.base
	setURLPath(&u, "/repos/"+url.PathEscape(repo.Prefix)+"/sync/setup")
	buf, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.actor != "" {
		req.Header.Set("X-Actor", c.actor)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		var out SyncSetupResult
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		return &out, nil
	case http.StatusConflict:
		// The 409 body is a SyncSetupOut with preview_collisions
		// populated; decode the same target struct and return the
		// sentinel so the caller branches on errors.Is.
		var out SyncSetupResult
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("decode 409 response: %w", err)
		}
		return &out, ErrSetupCollision
	default:
		// Any other non-2xx is the standard error-envelope path; map
		// it through the same HTTPError shape c.do would have produced.
		herr := &HTTPError{Status: resp.StatusCode}
		if len(raw) > 0 {
			var env errorBody
			if jerr := json.Unmarshal(raw, &env); jerr == nil {
				herr.Code = env.Code
				herr.Message = env.Error
				herr.Details = env.Details
			} else {
				herr.Message = string(raw)
			}
		}
		return nil, wrapStoreError(herr)
	}
}
