package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// BackgroundRunner drives the BACI-89 continual-mirror sync loop. The
// leader-elected controller calls Tick on store.SyncTickInterval; the
// runner enumerates every sync-enabled tracked repo and runs the same
// pull → import → export → commit → push pipeline a manual `bacio sync`
// runs, recording per-run status (last_sync_at / last_sync_error) so
// the desktop + web UIs can surface a live mirror state.
//
// The runner is cheap to construct unconditionally — it self-gates on
// the sync.background_enabled toggle and on whether any repo is
// actually sync-enabled, so a non-sync user pays only an empty
// ListRepos loop every five minutes.
type BackgroundRunner struct {
	store *store.Store
	// dbPath is the resolved SQLite path; needed for AcquireSyncLock,
	// whose lock file sits beside the DB. Empty falls back to
	// store.DefaultPath() at Tick time.
	dbPath string
	actor  string
	log    *slog.Logger

	// inFlight is the overlap guard — Tick sets it before doing work
	// and clears it after, so a slow git run that outlasts the tick
	// interval causes the next tick to skip rather than stack.
	inFlight atomic.Bool

	// backoff state. mu guards failCount + nextEligible. After a tick
	// in which any repo errored, failCount is bumped and nextEligible
	// pushed out exponentially (capped at backoffMax). A clean tick
	// resets both.
	mu           sync.Mutex
	failCount    int
	nextEligible time.Time
}

// backoffBase / backoffMax bound the exponential backoff applied after
// repeated failing ticks. The first failure waits one base interval,
// the second two, then four… capped at backoffMax. This keeps an
// unattended loop from hammering a remote that is rejecting pushes
// (a real conflict needing human resolution).
const (
	backoffBase = store.SyncTickInterval
	backoffMax  = time.Hour
)

// NewBackgroundRunner builds a runner. dbPath is the resolved SQLite
// path (for the sync lock); pass "" to fall back to store.DefaultPath.
// actor labels the audit rows the runner writes. log may be nil.
func NewBackgroundRunner(s *store.Store, dbPath, actor string, log *slog.Logger) *BackgroundRunner {
	if log == nil {
		log = slog.Default()
	}
	if actor == "" {
		actor = "bacio-background-sync"
	}
	return &BackgroundRunner{store: s, dbPath: dbPath, actor: actor, log: log}
}

// InProgress reports whether a Tick is currently running. Used by the
// HTTP sync-status handler to surface a live "syncing now" flag. Only
// meaningful on the leader process — a non-leader never runs Tick, so
// it always reports false there.
func (r *BackgroundRunner) InProgress() bool {
	if r == nil {
		return false
	}
	return r.inFlight.Load()
}

// Tick runs one background-sync pass. It is safe to call from a
// controller goroutine: it never panics out (the body is recover-
// wrapped by the caller's …IfLeader helper, but Tick also tolerates a
// nil store), and it self-gates on the toggle, the in-flight guard,
// and the failure backoff.
//
// Tick returns nil on a skip (toggle off / overlap / backoff) and on a
// fully-clean run; it returns an error only to let a caller log the
// aggregate failure — the per-repo failures are already recorded on
// the sync_remotes rows and the backoff is already advanced.
func (r *BackgroundRunner) Tick(ctx context.Context) error {
	if r == nil || r.store == nil {
		return nil
	}

	// 1. Toggle gate.
	enabled, err := r.store.GetSyncBackgroundEnabled()
	if err != nil {
		return fmt.Errorf("read sync.background_enabled: %w", err)
	}
	if !enabled {
		return nil
	}

	// 2. Overlap guard — skip if a previous tick is still running.
	if !r.inFlight.CompareAndSwap(false, true) {
		r.log.Debug("bacio: background sync tick skipped — previous run still in flight")
		return nil
	}
	defer r.inFlight.Store(false)

	// 3. Backoff gate.
	r.mu.Lock()
	if !r.nextEligible.IsZero() && time.Now().Before(r.nextEligible) {
		next := r.nextEligible
		r.mu.Unlock()
		r.log.Debug("bacio: background sync tick skipped — backing off", "next_eligible", next)
		return nil
	}
	r.mu.Unlock()

	// 4. Enumerate sync-enabled repos and sync each.
	repos, err := r.syncEnabledRepos()
	if err != nil {
		r.recordFailure()
		return fmt.Errorf("enumerate sync-enabled repos: %w", err)
	}

	var firstErr error
	synced := 0
	for _, sr := range repos {
		if err := r.syncOne(ctx, sr); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			r.log.Warn("bacio: background sync failed for repo",
				"repo", sr.repo.Prefix, "remote", sr.remote, "err", err)
			continue
		}
		synced++
	}

	if firstErr != nil {
		r.recordFailure()
		return firstErr
	}
	// A fully-clean tick (including the no-repos case) resets backoff.
	r.recordSuccess()
	if synced > 0 {
		r.log.Info("bacio: background sync completed", "repos", synced)
	}
	return nil
}

// syncTarget bundles a tracked repo with its resolved sync remote +
// local clone path.
type syncTarget struct {
	repo      *model.Repo
	remote    string
	localPath string
}

// syncEnabledRepos walks every tracked repo and returns those that are
// fully set up for sync: the on-disk path exists, .bacio/config.yaml
// parses with a non-empty sync.remote, and a sync_remotes row resolves
// the remote to a local clone. A repo failing any check is silently
// skipped — it simply isn't set up for sync, which is not an error.
func (r *BackgroundRunner) syncEnabledRepos() ([]syncTarget, error) {
	repos, err := r.store.ListRepos()
	if err != nil {
		return nil, err
	}
	var out []syncTarget
	for _, repo := range repos {
		if repo.Path == "" {
			continue
		}
		cfg, err := ReadProjectConfig(repo.Path)
		if err != nil {
			// ErrNoConfig (no sync here) and a broken config are both
			// "skip this repo" — the background loop must not abort
			// the whole tick over one mis-set-up repo.
			if !errors.Is(err, ErrNoConfig) {
				r.log.Debug("bacio: background sync skipping repo with unreadable config",
					"repo", repo.Prefix, "err", err)
			}
			continue
		}
		if cfg.Sync.Remote == "" {
			continue
		}
		rec, err := r.store.GetSyncRemote(cfg.Sync.Remote)
		if err != nil {
			// No local clone of the remote — not set up on this
			// machine. Skip silently.
			continue
		}
		out = append(out, syncTarget{repo: repo, remote: cfg.Sync.Remote, localPath: rec.LocalPath})
	}
	return out, nil
}

// syncOne runs the full pipeline for a single sync-enabled repo. It
// acquires the cross-process sync lock (so a concurrent manual
// `bacio sync` and the background ticker cannot interleave), runs
// Engine.Run, and records last_sync_at / last_sync_error plus a
// sync.run audit row. The error it returns (if any) is also persisted
// on the sync_remotes row before returning.
func (r *BackgroundRunner) syncOne(ctx context.Context, t syncTarget) error {
	syncRepo, err := git.Open(t.localPath)
	if err != nil {
		err = fmt.Errorf("open local sync clone %s: %w", t.localPath, err)
		_ = r.store.MarkSyncFailed(t.remote, err.Error())
		return err
	}

	dbPath := r.dbPath
	if dbPath == "" {
		if def, derr := store.DefaultPath(); derr == nil {
			dbPath = def
		}
	}
	release, err := AcquireSyncLock(dbPath)
	if err != nil {
		err = fmt.Errorf("acquire sync lock: %w", err)
		_ = r.store.MarkSyncFailed(t.remote, err.Error())
		return err
	}
	defer release() //nolint:errcheck — best-effort lock release

	eng := &Engine{Store: r.store, Actor: r.actor}
	res, runErr := eng.Run(ctx, t.repo.Path, syncRepo, RunOptions{})
	if runErr != nil {
		_ = r.store.MarkSyncFailed(t.remote, runErr.Error())
		_ = r.store.RecordHistory(model.HistoryEntry{
			Actor:   r.actor,
			Op:      "sync.run",
			Kind:    "repo",
			Details: "background sync failed: " + runErr.Error(),
		})
		return runErr
	}

	if err := r.store.MarkSyncCompleted(t.remote); err != nil {
		r.log.Warn("bacio: background sync failed to update last_sync_at",
			"repo", t.repo.Prefix, "err", err)
	}
	_ = r.store.RecordHistory(model.HistoryEntry{
		Actor:   r.actor,
		Op:      "sync.run",
		Kind:    "repo",
		Details: "background sync: " + backgroundRunDetails(res),
	})
	return nil
}

// backgroundRunDetails formats a one-line summary of a RunResult for
// the audit log — a trimmed-down sibling of cli.syncRunDetails kept in
// this package so the background path doesn't depend on internal/cli.
func backgroundRunDetails(res *RunResult) string {
	if res == nil {
		return "no result"
	}
	s := ""
	if res.Import != nil {
		s += fmt.Sprintf("inserted=%d updated=%d ", res.Import.Inserted, res.Import.Updated)
	}
	if res.Export != nil {
		s += fmt.Sprintf("writes=%d deletes=%d ", res.Export.Writes, res.Export.Deletes)
	}
	if res.Commit != "" {
		s += "commit=" + res.Commit
	}
	if res.Pushed {
		s += " pushed=true"
	}
	if s == "" {
		s = "no changes"
	}
	return s
}

// recordFailure bumps the failure counter and pushes nextEligible out
// exponentially (capped at backoffMax).
func (r *BackgroundRunner) recordFailure() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failCount++
	// 2^(failCount-1) base intervals, capped.
	delay := backoffBase
	for i := 1; i < r.failCount && delay < backoffMax; i++ {
		delay *= 2
	}
	if delay > backoffMax {
		delay = backoffMax
	}
	r.nextEligible = time.Now().Add(delay)
}

// recordSuccess clears the backoff state after a clean tick.
func (r *BackgroundRunner) recordSuccess() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failCount = 0
	r.nextEligible = time.Time{}
}
