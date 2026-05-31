package store

import (
	"database/sql"
	"errors"
	"os"
	"time"

	"github.com/mrgeoffrich/bacio/internal/anthropic"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// ReparseResult is the per-run summary the BACI-321 backfill returns to its two
// callers — the leader-gated controller sweep and the `bacio proxy reparse` CLI
// verb. DispatchesScanned is how many eligible dispatches the run walked;
// CapturesReparsed is how many captures yielded a fresh proxy_messages row;
// CapturesFailed is how many were stamped with the terminal-failure marker (a
// truncated / malformed capture that can never parse). The fields are snake_case
// so the CLI / REST surface can emit the result verbatim.
type ReparseResult struct {
	DispatchesScanned int `json:"dispatches_scanned"`
	CapturesReparsed  int `json:"captures_reparsed"`
	CapturesFailed    int `json:"captures_failed"`
}

// Add folds another dispatch's result into the running total. Used by the sweep
// to sum per-dispatch results into one run summary.
func (r *ReparseResult) Add(other ReparseResult) {
	r.DispatchesScanned += other.DispatchesScanned
	r.CapturesReparsed += other.CapturesReparsed
	r.CapturesFailed += other.CapturesFailed
}

// Total reports whether the run did any work — non-empty when at least one
// capture was reparsed or marked failed. The callers use it to skip a no-op audit
// row / log line on a quiet DB.
func (r ReparseResult) Total() int {
	return r.CapturesReparsed + r.CapturesFailed
}

// ReparseOpts parameterises the BACI-321 backfill sweep. QuietWindow keeps the
// sweep off any dispatch whose newest capture is younger than this, so it never
// races the live recorder on an actively-streaming job (a zero value falls back
// to ProxyReparseQuietWindow). Now is an injectable clock for the quiet-window
// cutoff (the zero value falls back to time.Now), so tests can seed captures at a
// fixed age without sleeping.
type ReparseOpts struct {
	QuietWindow time.Duration
	Now         time.Time
}

// MarkProxyRequestParseFailed stamps proxy_requests.parse_failed_at = now on one
// capture (BACI-321) — the durable terminal-failure marker the backfill keys off
// so a truncated / malformed capture is attempted once and never re-read every
// sweep. Idempotent: re-stamping just refreshes the timestamp. The live recorder
// stamps it on its own parse miss too, for consistency.
func (s *Store) MarkProxyRequestParseFailed(proxyRequestID int64) error {
	_, err := s.DB.Exec(
		`UPDATE proxy_requests SET parse_failed_at = ? WHERE id = ?`,
		time.Now().UTC(), proxyRequestID)
	return err
}

// ReparseUnparsedDispatches is the BACI-321 backfill sweep: it finds every
// dispatch the live recorder path missed (a dispatch-correlated Anthropic SSE
// capture with no proxy_messages row and no terminal-failure marker, whose newest
// such capture is older than the quiet window) and reparses each in chronological
// order. v1 is the non-destructive, fully-unparsed case — a dispatch is eligible
// only when it has NO proxy_messages rows at all, so the replay inserts ids
// monotonic in capture order and the Classify delta chain is correct without any
// delete. A dispatch with a partial gap (some captures parsed, some missing) is
// left for the destructive `--rebuild` path (out of scope for v1). Errors on one
// dispatch are returned, halting the run — the sweep is leader-gated and the
// caller logs-and-swallows, so a transient DB blip just retries next tick.
func (s *Store) ReparseUnparsedDispatches(opts ReparseOpts) (ReparseResult, error) {
	quiet := opts.QuietWindow
	if quiet <= 0 {
		quiet = ProxyReparseQuietWindow
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.Add(-quiet).UTC()

	ids, err := s.eligibleReparseDispatches(cutoff)
	if err != nil {
		return ReparseResult{}, err
	}
	var res ReparseResult
	for _, id := range ids {
		one, err := s.ReparseDispatch(id)
		if err != nil {
			return res, err
		}
		res.Add(one)
	}
	return res, nil
}

// CountUnparsedDispatchCaptures reports how many of a dispatch's Anthropic SSE
// captures a backfill would attempt: rows with no proxy_messages row and no
// terminal-failure marker. It backs the CLI dry-run for `--dispatch` (the
// projected attempt count) without mutating anything. Note it does NOT apply the
// fully-unparsed-only constraint the sweep does — the single-dispatch CLI path
// reparses whatever is missing for the named job, so the projection counts the
// same captures ReparseDispatch would touch.
func (s *Store) CountUnparsedDispatchCaptures(dispatchID int64) (int, error) {
	var n int
	err := s.DB.QueryRow(`
		SELECT COUNT(*)
		FROM proxy_requests pr
		WHERE pr.dispatch_id = ? AND pr.is_anthropic = 1 AND pr.is_stream = 1
		  AND pr.parse_failed_at IS NULL
		  AND NOT EXISTS (
		      SELECT 1 FROM proxy_messages pm WHERE pm.proxy_request_id = pr.id)`,
		dispatchID).Scan(&n)
	return n, err
}

// ProjectReparseUnparsedDispatches is the dry-run projection of the sweep: it
// runs the same eligibility query as ReparseUnparsedDispatches and reports how
// many dispatches and captures a real run would attempt, without reading a single
// .http file or writing a row. CapturesReparsed is the projected attempt count
// (the upper bound — a capture that would fail to parse can't be told apart
// without reading its file, so CapturesFailed stays 0 in the projection).
func (s *Store) ProjectReparseUnparsedDispatches(opts ReparseOpts) (ReparseResult, error) {
	quiet := opts.QuietWindow
	if quiet <= 0 {
		quiet = ProxyReparseQuietWindow
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.Add(-quiet).UTC()

	ids, err := s.eligibleReparseDispatches(cutoff)
	if err != nil {
		return ReparseResult{}, err
	}
	var res ReparseResult
	for _, id := range ids {
		n, err := s.CountUnparsedDispatchCaptures(id)
		if err != nil {
			return ReparseResult{}, err
		}
		if n > 0 {
			res.DispatchesScanned++
			res.CapturesReparsed += n
		}
	}
	return res, nil
}

// eligibleReparseDispatches returns the distinct dispatch ids the sweep should
// reparse: a dispatch that has at least one eligible unparsed capture
// (is_anthropic=1 AND is_stream=1 AND dispatch_id IS NOT NULL AND parse_failed_at
// IS NULL with no proxy_messages row), has NO proxy_messages rows at all (the
// fully-unparsed v1 constraint — a partial gap is deferred to --rebuild), and
// whose newest capture started before the quiet-window cutoff. Ordered by the
// dispatch's oldest unparsed capture so a long run makes deterministic progress.
func (s *Store) eligibleReparseDispatches(cutoff time.Time) ([]int64, error) {
	rows, err := s.DB.Query(`
		SELECT pr.dispatch_id
		FROM proxy_requests pr
		WHERE pr.is_anthropic = 1 AND pr.is_stream = 1
		  AND pr.dispatch_id IS NOT NULL
		  AND pr.parse_failed_at IS NULL
		  AND NOT EXISTS (
		      SELECT 1 FROM proxy_messages pm WHERE pm.proxy_request_id = pr.id)
		GROUP BY pr.dispatch_id
		HAVING MAX(pr.started_at) < ?
		   AND NOT EXISTS (
		      SELECT 1 FROM proxy_messages pm2 WHERE pm2.dispatch_id = pr.dispatch_id)
		ORDER BY MIN(pr.started_at)`,
		cutoff.Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ReparseDispatch reparses one dispatch's unparsed Anthropic SSE captures into
// proxy_messages rows, in chronological (started_at ASC, id ASC) order so the
// inserted ids stay monotonic in capture order and the Classify delta chain
// (threaded through LatestThreadState) is correct. It is the per-dispatch core
// shared by the sweep and `bacio proxy reparse --dispatch <id>`. For each capture
// with no existing row and no terminal-failure marker, it reads the raw .http by
// the row's absolute raw_log_path (written absolute by the recorder, so the leader
// reads it regardless of which process captured it), runs the same parse+classify
// glue the live recorder uses (anthropic.ParseAndClassify), and inserts the row.
// A capture whose file is missing/unreadable or whose bytes won't parse is stamped
// with parse_failed_at (counted in CapturesFailed) so it isn't retried every
// sweep. Captures already parsed are skipped — so re-running is idempotent and the
// dispatch's existing rows are never touched (non-destructive). DispatchesScanned
// is 1 when the dispatch had at least one eligible capture, else 0.
func (s *Store) ReparseDispatch(dispatchID int64) (ReparseResult, error) {
	caps, err := s.dispatchAnthropicCaptures(dispatchID)
	if err != nil {
		return ReparseResult{}, err
	}
	var res ReparseResult
	scanned := false
	for _, pr := range caps {
		// Already parsed → leave it; v1 never deletes, so an existing row is
		// authoritative and reparsing it would double-insert.
		has, err := s.captureHasMessage(pr.ID)
		if err != nil {
			return res, err
		}
		if has {
			continue
		}
		// A capture already given up on stays given up on.
		if pr.ParseFailedAt != nil {
			continue
		}
		scanned = true

		rendered, err := os.ReadFile(pr.RawLogPath)
		if err != nil {
			// No raw file (empty path → ReadFile errors) or it's gone from disk:
			// this capture can never be reparsed, so mark it terminal.
			if err := s.MarkProxyRequestParseFailed(pr.ID); err != nil {
				return res, err
			}
			res.CapturesFailed++
			continue
		}
		// Thread the delta chain through the most-recent primary row, exactly as
		// the live recorder does — re-read per capture so each inserted primary
		// row carries forward to the next capture's classification.
		prev, err := s.LatestThreadState(&dispatchID)
		if err != nil {
			return res, err
		}
		pc, isPrimary, delta, perr := anthropic.ParseAndClassify(rendered, prev)
		if perr != nil {
			if err := s.MarkProxyRequestParseFailed(pr.ID); err != nil {
				return res, err
			}
			res.CapturesFailed++
			continue
		}
		dispID := dispatchID
		if _, err := s.AddProxyMessage(AddProxyMessageIn{
			ProxyRequestID: pr.ID,
			DispatchID:     &dispID,
			SessionID:      pr.SessionID,
			ClaudeAgentID:  pr.ClaudeAgentID,
			Capture:        pc,
			Delta:          delta,
			IsPrimary:      isPrimary,
			StartedAt:      pr.StartedAt,
		}); err != nil {
			return res, err
		}
		res.CapturesReparsed++
	}
	if scanned {
		res.DispatchesScanned = 1
	}
	return res, nil
}

// dispatchAnthropicCaptures returns a dispatch's is_anthropic+is_stream capture
// rows in chronological order (started_at ASC, id ASC) — the order the backfill
// must replay them in so proxy_messages.id stays monotonic in capture order.
func (s *Store) dispatchAnthropicCaptures(dispatchID int64) ([]*model.ProxyRequest, error) {
	rows, err := s.DB.Query(proxyRequestSelect+`
		WHERE dispatch_id = ? AND is_anthropic = 1 AND is_stream = 1
		ORDER BY started_at ASC, id ASC`, dispatchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.ProxyRequest
	for rows.Next() {
		pr, err := scanProxyRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

// captureHasMessage reports whether a proxy_requests row already has a parsed
// proxy_messages row — the "skip already-parsed" guard that keeps reparsing
// non-destructive and idempotent.
func (s *Store) captureHasMessage(proxyRequestID int64) (bool, error) {
	var one int
	err := s.DB.QueryRow(
		`SELECT 1 FROM proxy_messages WHERE proxy_request_id = ? LIMIT 1`,
		proxyRequestID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
