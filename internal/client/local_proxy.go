// BACI-321: client-side wrapper for the proxy_messages backfill — the manual
// escape hatch over the leader-gated controller sweep. The store holds the
// reparse core (shared with the sweep); this layer maps the CLI/REST options
// onto it and records the audit row on a non-empty wet run.
package client

import (
	"context"
	"errors"
	"fmt"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// ReparseProxyOpts is the cross-transport option set for the BACI-321 backfill.
// Dispatch nil sweeps every eligible dispatch; Dispatch set scopes the run to one
// job's captures. Rebuild requests the destructive partial-gap rebuild — reserved
// on the surface but not implemented in v1 (the call errors with
// ErrRebuildNotImplemented rather than silently doing the non-destructive thing).
// RetryFailed (BACI-323) clears parse_failed_at on the still-unparsed captures in
// scope before the reparse, so dispatches the parser previously gave up on (e.g.
// the pre-fix marker-collision failures) backfill once the parser is fixed. The
// dry-run path counts the captures it would clear without mutating.
type ReparseProxyOpts struct {
	Dispatch    *int64
	Rebuild     bool
	RetryFailed bool
}

// ErrRebuildNotImplemented is returned when a caller passes the destructive
// partial-gap rebuild flag (`--rebuild` / "rebuild": true). The flag is reserved
// on the CLI / schema surface so the destructive path can land later without a
// surface change, but v1 only ships the non-destructive fully-unparsed backfill.
var ErrRebuildNotImplemented = errors.New("proxy reparse: --rebuild (destructive partial-gap rebuild) is not implemented in v1")

// ReparseProxyMessages runs the BACI-321 backfill: reparse one dispatch's
// unparsed captures (Dispatch set) or sweep every eligible dispatch (Dispatch
// nil). On dry-run it projects the counts without writing — for the
// single-dispatch case the store's ReparseDispatch is mutating, so the dry-run
// path reports the projected work (the eligible-but-unparsed capture count)
// without invoking it; for the all-dispatch sweep it counts the eligible
// dispatches the same way. On a non-empty wet run it records a `proxy.reparse`
// audit row with the per-run counts (mirroring archive.sweep's no-noise-on-empty
// contract).
func (c *localClient) ReparseProxyMessages(ctx context.Context, in ReparseProxyOpts, dryRun bool) (store.ReparseResult, error) {
	if in.Rebuild {
		return store.ReparseResult{}, ErrRebuildNotImplemented
	}

	if dryRun {
		return c.projectReparse(in)
	}

	var (
		res store.ReparseResult
		err error
	)
	if in.Dispatch != nil {
		// BACI-323: for the scoped path, clear the dispatch's terminal-failure
		// markers first when retrying failures — ReparseDispatch itself skips a
		// stamped capture (keeping its non-destructive contract), so the clear has
		// to happen one layer up.
		if in.RetryFailed {
			if _, cerr := c.store.ClearProxyParseFailed(store.ClearParseFailedOpts{Dispatch: in.Dispatch}); cerr != nil {
				return store.ReparseResult{}, cerr
			}
		}
		res, err = c.store.ReparseDispatch(*in.Dispatch)
	} else {
		res, err = c.store.ReparseUnparsedDispatches(store.ReparseOpts{RetryFailed: in.RetryFailed})
	}
	if err != nil {
		return store.ReparseResult{}, err
	}
	if res.Total() > 0 {
		c.recordOp(model.HistoryEntry{
			Op:      "proxy.reparse",
			Kind:    "sweep",
			Details: detailsForReparse(res),
		})
	}
	return res, nil
}

// projectReparse computes the dry-run preview without mutating: how many
// captures a real run would attempt. The actual reparse is per-capture mutating
// (each insert threads the next capture's classification), so a faithful dry-run
// can't run the store core; instead it counts the eligible-but-unparsed captures
// the run would touch (CapturesReparsed is the projected attempt count, the same
// shape a wet run returns). A truncated capture that would fail can't be told
// apart without reading the file, so CapturesFailed stays 0 in the projection —
// the preview reports the upper bound of work, which is what a reader wants.
func (c *localClient) projectReparse(in ReparseProxyOpts) (store.ReparseResult, error) {
	if in.Dispatch != nil {
		// BACI-323: with --retry-failed, the wet run clears this dispatch's
		// terminal-failure markers first; the count drops the parse_failed_at
		// predicate to mirror that, so the preview reflects the recovery work.
		n, err := c.store.CountUnparsedDispatchCaptures(*in.Dispatch, in.RetryFailed)
		if err != nil {
			return store.ReparseResult{}, err
		}
		res := store.ReparseResult{CapturesReparsed: n}
		if n > 0 {
			res.DispatchesScanned = 1
		}
		return res, nil
	}
	return c.store.ProjectReparseUnparsedDispatches(store.ReparseOpts{RetryFailed: in.RetryFailed})
}

// detailsForReparse is the compact, parseable Details string the `proxy.reparse`
// audit row carries — readable in `bacio history` without the JSON formatter, and
// the same shape the leader-driven controller's ReparseProxyMessagesIfLeader
// emits so a reader sees identical rows regardless of which surface kicked the
// backfill.
func detailsForReparse(r store.ReparseResult) string {
	return fmt.Sprintf(`{"dispatches_scanned":%d,"captures_reparsed":%d,"captures_failed":%d}`,
		r.DispatchesScanned, r.CapturesReparsed, r.CapturesFailed)
}
