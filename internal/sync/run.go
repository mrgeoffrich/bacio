package sync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// RunOptions controls a single steady-state `bacio sync` invocation.
// Each "skip" flag short-circuits one phase of the pipeline. DryRun
// is a hard skip on writes (DB rolls back, no commit, no push) but
// still produces a result the user can inspect.
type RunOptions struct {
	NoImport bool
	NoExport bool
	NoPush   bool
	DryRun   bool
}

// RunResult is the structured outcome of one `bacio sync` invocation.
// All sub-results are pointers so absence (skipped phase, no
// changes) is unambiguous in JSON.
type RunResult struct {
	Import *ImportResult       `json:"import,omitempty"`
	Export *StagedExportResult `json:"export,omitempty"`
	Commit string              `json:"commit,omitempty"`
	Pushed bool                `json:"pushed"`
	DryRun bool                `json:"dry_run,omitempty"`

	// Warnings buffer non-fatal hiccups encountered during the run
	// (e.g. last_sync_at update failure). The user-visible command
	// still succeeds; the warnings just bubble up via stderr.
	Warnings []string `json:"warnings,omitempty"`
}

// MaxPushRetries hardcodes the design doc's "retry once on
// non-fast-forward" policy. If observed in the wild we'll revisit.
const MaxPushRetries = 1

// Run executes the full sync pipeline against an already-cloned sync
// repo. The caller resolves the project root and the sync repo
// working tree before calling; this function just orchestrates the
// pull → import → export → commit → push sequence.
//
// projectRoot is the path of the project repo this user is currently
// working from. It's only used today as audit context (the actor name
// already lives on the engine); future expansions might use it for
// per-project-only sync.
//
// The lock is the caller's responsibility — see AcquireSyncLock.
// Run does not acquire it itself because the CLI layer has more
// information (the actual DB path, including the --db override).
func (e *Engine) Run(ctx context.Context, projectRoot string, syncRepo *git.Repo, opts RunOptions) (*RunResult, error) {
	if e.Store == nil {
		return nil, fmt.Errorf("sync.Run: Store is nil")
	}
	if syncRepo == nil || syncRepo.Root == "" {
		return nil, fmt.Errorf("sync.Run: syncRepo is nil or empty")
	}
	res := &RunResult{DryRun: opts.DryRun}

	// 1. Refuse to run if the working tree is dirty. Otherwise an
	// in-progress hand edit could be silently committed by the auto
	// `git add -A` in step 5.
	dirty, err := syncRepo.HasUncommittedChanges()
	if err != nil {
		return nil, fmt.Errorf("check working tree: %w", err)
	}
	if dirty {
		return nil, fmt.Errorf("sync repo at %s has uncommitted changes; commit or stash before running bacio sync", syncRepo.Root)
	}

	// 2. Pull. ff-only — non-fast-forward means a real conflict the
	// user has to resolve.
	if err := syncRepo.Pull(); err != nil {
		// If the repo has no upstream yet (new init that hasn't
		// been pushed), Pull will fail; tolerate that case so the
		// user can still bootstrap-and-sync without `init` having
		// pushed first.
		if !isNoUpstreamErr(err) {
			return nil, fmt.Errorf("git pull: %w", err)
		}
	}

	// 3. Import (if not skipped).
	if !opts.NoImport {
		importEng := &Engine{Store: e.Store, Actor: e.Actor, DryRun: opts.DryRun}
		impRes, err := importEng.Import(ctx, syncRepo.Root)
		if err != nil {
			return nil, fmt.Errorf("import: %w", err)
		}
		res.Import = impRes
	}

	// 4. Export (if not skipped). Always uses the atomic-staging
	// path so a crash mid-write doesn't corrupt the working tree.
	if !opts.NoExport {
		exportEng := &Engine{Store: e.Store, Actor: e.Actor, DryRun: opts.DryRun}
		expRes, err := exportEng.ExportStaged(ctx, syncRepo)
		if err != nil {
			return nil, fmt.Errorf("export: %w", err)
		}
		res.Export = expRes
	}

	if opts.DryRun {
		return res, nil
	}

	// 5. Stage + commit anything new. If nothing changed, Commit
	// returns ("", nil) and we skip the push.
	if err := syncRepo.Add(); err != nil {
		return nil, fmt.Errorf("git add: %w", err)
	}
	sha, err := syncRepo.Commit(buildCommitMessage(e.Actor, res))
	if err != nil {
		return nil, fmt.Errorf("git commit: %w", err)
	}
	res.Commit = sha

	if sha == "" || opts.NoPush {
		// Nothing new to push (clean tree → no commit was made), or
		// the user explicitly disabled push. Either way: bookkeep
		// and exit.
		return res, nil
	}

	// 6. Push. On non-fast-forward: pull, re-import, re-export, retry once.
	pushed, err := pushWithRetry(ctx, e, syncRepo, res)
	if err != nil {
		return nil, err
	}
	res.Pushed = pushed
	return res, nil
}

// pushWithRetry runs `git push`; on non-fast-forward it pulls + re-
// imports + re-exports + retries once. If the second push also
// fails, we surface the original error so the user knows to
// investigate (probably a real conflict that requires manual
// resolution).
func pushWithRetry(ctx context.Context, e *Engine, syncRepo *git.Repo, res *RunResult) (bool, error) {
	for attempt := 0; attempt <= MaxPushRetries; attempt++ {
		if err := syncRepo.Push(); err == nil {
			return true, nil
		} else if !isNonFastForward(err) {
			return false, fmt.Errorf("git push: %w", err)
		} else if attempt == MaxPushRetries {
			return false, fmt.Errorf("git push: non-fast-forward and retry exhausted: %w", err)
		}

		// Non-fast-forward: another client pushed between our
		// pull and our push. Bring their changes in, re-run import +
		// export, and try again.
		res.Warnings = append(res.Warnings,
			fmt.Sprintf("push attempt %d rejected non-fast-forward; pulling and retrying", attempt+1))
		if err := syncRepo.Pull(); err != nil {
			return false, fmt.Errorf("git pull on retry: %w", err)
		}
		// Re-import.
		importEng := &Engine{Store: e.Store, Actor: e.Actor}
		impRes, err := importEng.Import(ctx, syncRepo.Root)
		if err != nil {
			return false, fmt.Errorf("import on retry: %w", err)
		}
		res.Import = impRes
		// Re-export.
		exportEng := &Engine{Store: e.Store, Actor: e.Actor}
		expRes, err := exportEng.ExportStaged(ctx, syncRepo)
		if err != nil {
			return false, fmt.Errorf("export on retry: %w", err)
		}
		res.Export = expRes
		if err := syncRepo.Add(); err != nil {
			return false, fmt.Errorf("git add on retry: %w", err)
		}
		sha, err := syncRepo.Commit(buildCommitMessage(e.Actor, res))
		if err != nil {
			return false, fmt.Errorf("git commit on retry: %w", err)
		}
		// `Commit` returns "" if the index is clean — meaning their
		// changes already covered everything we wanted to push.
		// Try push anyway; if there's nothing to push git will say
		// so and we treat that as success.
		res.Commit = sha
	}
	return false, nil
}

// buildCommitMessage produces the commit message used by `bacio sync`
// runs. Subject line carries the actor + timestamp; body lists
// per-kind counts so `git log -1` is informative.
//
// Format example:
//
//   bacio sync: geoff @ 2026-05-09T14:22:00Z
//
//   issues: +2 ~1 -0
//   features: +0 ~0 -0
//   ...
func buildCommitMessage(actor string, res *RunResult) string {
	if actor == "" {
		actor = "unknown"
	}
	stamp := time.Now().UTC().Format(time.RFC3339)
	subject := fmt.Sprintf("bacio sync: %s @ %s", actor, stamp)
	if res == nil || res.Export == nil || res.Export.ExportResult == nil {
		return subject
	}
	er := res.Export.ExportResult
	body := fmt.Sprintf(
		"issues: %d  features: %d  documents: %d  comments: %d\nrenames: %d  writes: %d  deletes: %d",
		er.Issues, er.Features, er.Documents, er.Comments,
		res.Export.Renames, res.Export.Writes, res.Export.Deletes,
	)
	return subject + "\n\n" + body
}

// isNoUpstreamErr reports whether the error from `git pull` says
// "there is no upstream branch configured". We tolerate this case
// during init/post-init so the user can run bacio sync immediately
// after init even if push --set-upstream hasn't propagated yet.
func isNoUpstreamErr(err error) bool {
	var ge *git.Error
	if !errors.As(err, &ge) {
		return false
	}
	combined := strings.ToLower(ge.Stderr)
	return strings.Contains(combined, "no upstream branch") ||
		strings.Contains(combined, "no remote configured") ||
		strings.Contains(combined, "does not appear to be a git repository") ||
		strings.Contains(combined, "no tracking information")
}

// isNonFastForward reports whether the error from `git push` is the
// "non-fast-forward" rejection we want to retry once after a pull.
// String-match on the well-known stderr — the alternative is to
// parse the porcelain output, which is more brittle than this.
func isNonFastForward(err error) bool {
	var ge *git.Error
	if !errors.As(err, &ge) {
		return false
	}
	combined := strings.ToLower(ge.Stderr)
	return strings.Contains(combined, "non-fast-forward") ||
		strings.Contains(combined, "fetch first") ||
		strings.Contains(combined, "rejected") && strings.Contains(combined, "updates were rejected")
}

// keep store imported for future helpers.
var _ = store.SyncKindRepo
