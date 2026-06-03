package api

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/timeparse"
)

// handleProxyStats serves the BACI-303 per-FQDN proxy-capture rollup —
// the cross-cutting read surface (sibling of /history) the Monitor screen
// (BACI-304) and `bacio proxy stats` both consume. The proxy_requests
// table has no repo_id, so there is no per-repo variant. It sits outside
// the /anthropic/ auth exemption, so the bearer-token middleware protects
// it (correct — this is a UI/CLI read, not agent passthrough).
//
// `since` accepts a duration lookback (30m, 1h, 1d) and `from` an
// absolute timestamp; they're mutually exclusive, mirroring /history.
// `limit` caps the rows the store folds in (the remote CLI client sends
// `from` for an absolute cutoff and `limit` for the cap).
func (d deps) handleProxyStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var f store.ProxyStatsFilter

	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid_input", "limit must be a non-negative integer", map[string]any{"field": "limit"})
			return
		}
		f.Limit = n
	}

	since := q.Get("since")
	from := q.Get("from")
	if since != "" && from != "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "since and from are mutually exclusive", nil)
		return
	}
	if since != "" {
		dur, err := timeparse.Lookback(since)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "since"})
			return
		}
		cutoff := time.Now().Add(-dur)
		f.Since = &cutoff
	}
	if from != "" {
		t, err := timeparse.Timestamp(from)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", "from: "+err.Error(), map[string]any{"field": "from"})
			return
		}
		f.Since = &t
	}

	stats, err := d.store.ProxyStatsByFQDN(f)
	if err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	if stats == nil {
		stats = []*model.ProxyFQDNStat{}
	}
	writeJSON(w, http.StatusOK, stats)
}

// handleProxyCaptures serves the BACI-308 filtered capture list — the
// drill-down read the Monitor screen walks an FQDN stat row down into. It
// filters proxy_requests by host / dispatch_id / is_anthropic / a `since`
// lookback (or `from` absolute cutoff, mutually exclusive, mirroring
// /proxy/stats) and caps with `limit`, newest-first. Each row is best-effort
// enriched with its dispatch's issue key + mode so the sheet can show the job
// chip without a per-row fetch; a deleted dispatch leaves those empty. Behind
// the bearer-token auth like /proxy/stats (a UI/CLI read, not agent passthrough).
func (d deps) handleProxyCaptures(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var f store.ProxyRequestFilter
	f.Host = q.Get("host")

	if v := q.Get("dispatch_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_input", "dispatch_id must be a positive integer", map[string]any{"field": "dispatch_id"})
			return
		}
		f.DispatchID = &n
	}

	if v := q.Get("is_anthropic"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", "is_anthropic must be a boolean", map[string]any{"field": "is_anthropic"})
			return
		}
		f.IsAnthropic = &b
	}

	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid_input", "limit must be a non-negative integer", map[string]any{"field": "limit"})
			return
		}
		f.Limit = n
	}

	since := q.Get("since")
	from := q.Get("from")
	if since != "" && from != "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "since and from are mutually exclusive", nil)
		return
	}
	if since != "" {
		dur, err := timeparse.Lookback(since)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "since"})
			return
		}
		cutoff := time.Now().Add(-dur)
		f.Since = &cutoff
	}
	if from != "" {
		t, err := timeparse.Timestamp(from)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", "from: "+err.Error(), map[string]any{"field": "from"})
			return
		}
		f.Since = &t
	}

	out, err := d.store.ListProxyCapturesEnriched(f)
	if err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleProxySearch serves the BACI-320 content grep over the parsed
// proxy_messages bodies — the search→drill-in read `bacio proxy grep` consumes
// (and the web Monitor could reuse). It greps delta_json / turn_json for the
// required `q` substring (case-insensitive), optionally narrowed by `role`
// (assistant | user), `block` (text | thinking | tool_use | tool_result), the
// dispatch_id / session / agent correlation, and a `since` lookback (or `from`
// absolute cutoff, mutually exclusive, mirroring /proxy/captures). `limit` caps
// the match lines. Each match carries the proxy_requests id so the caller can
// drill into /proxy/captures/{id}. Behind the bearer-token auth like
// /proxy/stats (a UI/CLI read, not agent passthrough).
func (d deps) handleProxySearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var f store.ProxyMessageFilter

	f.Query = q.Get("q")
	if f.Query == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "q is required", map[string]any{"field": "q"})
		return
	}

	if v := q.Get("role"); v != "" {
		if v != "assistant" && v != "user" {
			writeError(w, http.StatusBadRequest, "invalid_input", "role must be assistant or user", map[string]any{"field": "role"})
			return
		}
		f.Role = v
	}

	if v := q.Get("block"); v != "" {
		switch v {
		case "text", "thinking", "tool_use", "tool_result":
			f.Block = v
		default:
			writeError(w, http.StatusBadRequest, "invalid_input", "block must be one of text, thinking, tool_use, tool_result", map[string]any{"field": "block"})
			return
		}
	}

	if v := q.Get("dispatch_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_input", "dispatch_id must be a positive integer", map[string]any{"field": "dispatch_id"})
			return
		}
		f.DispatchID = &n
	}

	f.SessionID = q.Get("session")
	f.ClaudeAgentID = q.Get("agent")

	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid_input", "limit must be a non-negative integer", map[string]any{"field": "limit"})
			return
		}
		f.Limit = n
	}

	since := q.Get("since")
	from := q.Get("from")
	if since != "" && from != "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "since and from are mutually exclusive", nil)
		return
	}
	if since != "" {
		dur, err := timeparse.Lookback(since)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "since"})
			return
		}
		cutoff := time.Now().Add(-dur)
		f.Since = &cutoff
	}
	if from != "" {
		t, err := timeparse.Timestamp(from)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", "from: "+err.Error(), map[string]any{"field": "from"})
			return
		}
		f.Since = &t
	}

	out, err := d.store.SearchProxyMessages(f)
	if err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	if out == nil {
		out = []*model.ProxyMessageMatch{}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleProxyJobs serves the BACI-322 transcript browser list — one summary
// row per distinct dispatch that has parsed captures, the row-per-dispatch
// list the Monitor Transcript page renders. It scopes to the active repo
// (`repo`), one issue (`issue`), one job mode (`mode`), one supervisor session
// (`session`) / subagent identity (`agent`) (BACI-348), and a `since` lookback
// (or `from` absolute cutoff, mutually exclusive, mirroring /proxy/captures),
// capped with `limit`, newest-first. Each row carries the issue-key / mode /
// agent / repo-prefix enrichment lifted off its dispatch. Behind the
// bearer-token auth like /proxy/stats (a UI/CLI read, not agent passthrough).
func (d deps) handleProxyJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var f store.JobTranscriptFilter
	f.RepoPrefix = q.Get("repo")
	f.IssueKey = q.Get("issue")
	f.Mode = q.Get("mode")
	f.SessionID = q.Get("session")
	f.ClaudeAgentID = q.Get("agent")

	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid_input", "limit must be a non-negative integer", map[string]any{"field": "limit"})
			return
		}
		f.Limit = n
	}

	since := q.Get("since")
	from := q.Get("from")
	if since != "" && from != "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "since and from are mutually exclusive", nil)
		return
	}
	if since != "" {
		dur, err := timeparse.Lookback(since)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "since"})
			return
		}
		cutoff := time.Now().Add(-dur)
		f.Since = &cutoff
	}
	if from != "" {
		t, err := timeparse.Timestamp(from)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", "from: "+err.Error(), map[string]any{"field": "from"})
			return
		}
		f.Since = &t
	}

	out, err := d.store.ListJobTranscripts(f)
	if err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	if out == nil {
		out = []*model.JobTranscriptRow{}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleProxyCapture serves the BACI-306 parsed detail of one captured
// Anthropic SSE turn, keyed on the proxy_requests id (the id the raw .http file
// and `proxy stats` reference). 404 when that capture wasn't parseable
// (non-stream / truncated) and so has no proxy_messages row. Behind the
// bearer-token auth like /proxy/stats (a UI/CLI read, not agent passthrough).
func (d deps) handleProxyCapture(w http.ResponseWriter, r *http.Request) {
	id, ok := readProxyID(r, w, "id")
	if !ok {
		return
	}
	msg, err := d.store.CaptureMessage(id)
	if err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, msg)
}

// handleJobTranscript serves the BACI-306 assembled per-job transcript for a
// dispatch: the ordered primary-thread messages, summed token usage, and the
// auxiliary turns. 404 when the dispatch has no parsed captures. Behind the
// bearer-token auth like /proxy/stats.
func (d deps) handleJobTranscript(w http.ResponseWriter, r *http.Request) {
	id, ok := readProxyID(r, w, "dispatch_id")
	if !ok {
		return
	}
	tr, err := d.store.JobTranscript(id)
	if err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, tr)
}

// handleProxyRaw serves the BACI-308 raw .http capture for one proxy_requests
// id: the inflated, auth-redacted req/resp bytes the recorder wrote to disk
// (renderRawCapture redacts auth-bearing headers at capture time, so this is
// safe to surface). It is the escape hatch the Monitor sheet falls back to when
// a capture isn't a parseable Anthropic turn. 404 — not 500 — when the row has
// no raw file (RawLogPath empty because the write failed) or the file is gone
// from disk (pruned / log dir wiped), so the sheet treats "raw unavailable" as
// a clean miss rather than a transport error. Behind the bearer-token auth like
// /proxy/stats.
func (d deps) handleProxyRaw(w http.ResponseWriter, r *http.Request) {
	id, ok := readProxyID(r, w, "id")
	if !ok {
		return
	}
	pr, err := d.store.GetProxyRequest(id)
	if err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	if pr.RawLogPath == "" {
		writeError(w, http.StatusNotFound, "not_found", "no raw capture file for this request", nil)
		return
	}
	body, err := os.ReadFile(pr.RawLogPath)
	if err != nil {
		// File pruned / log dir wiped — a clean miss, not a server error.
		writeError(w, http.StatusNotFound, "not_found", "raw capture file unavailable", nil)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// handleProxyReparse serves the BACI-321 proxy_messages backfill — the first
// MUTATING proxy verb, the manual escape hatch over the leader-gated controller
// sweep. POST /proxy/reparse?dispatch=&dry_run= reparses dispatch-correlated
// Anthropic captures the live recorder path missed. With no `dispatch` it sweeps
// every eligible dispatch (the same work the controller does once a minute); with
// `dispatch` it scopes to one job. `retry_failed` (BACI-323) clears parse_failed_at
// on the still-unparsed captures in scope first, so dispatches the parser gave up
// on backfill once the parser is fixed. `rebuild` (BACI-325) is the destructive
// per-dispatch rebuild — delete the dispatch's proxy_messages rows and replay all
// its captures through the corrected parser — and REQUIRES `dispatch`; a global
// rebuild (rebuild without dispatch) 400s as not_implemented. Dry-run projects the
// counts without writing; a non-empty wet run records a `proxy.reparse` audit row.
// Behind the bearer-token auth like /proxy/stats (a UI/CLI mutation, not agent
// passthrough).
func (d deps) handleProxyReparse(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var dispatchID *int64
	if v := q.Get("dispatch"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_input", "dispatch must be a positive integer", map[string]any{"field": "dispatch"})
			return
		}
		dispatchID = &n
	}

	var rebuild bool
	if v := q.Get("rebuild"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", "rebuild must be a boolean", map[string]any{"field": "rebuild"})
			return
		}
		rebuild = b
	}
	// BACI-325: a per-dispatch rebuild ships; a global rebuild (no dispatch) is the
	// unbounded destructive write that stays out of scope.
	if rebuild && dispatchID == nil {
		writeError(w, http.StatusBadRequest, "not_implemented",
			"a global rebuild (without dispatch) is not implemented; pass dispatch to rebuild one dispatch", map[string]any{"field": "rebuild"})
		return
	}

	var retryFailed bool
	if v := q.Get("retry_failed"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", "retry_failed must be a boolean", map[string]any{"field": "retry_failed"})
			return
		}
		retryFailed = b
	}

	dryRun := isDryRun(r)
	if dryRun {
		var (
			res store.ReparseResult
			err error
		)
		switch {
		case rebuild:
			// BACI-325: a rebuild deletes and replays ALL the dispatch's Anthropic
			// captures, so its projection counts all of them (dispatch is required
			// here — the global rebuild was rejected above).
			n, cerr := d.store.CountDispatchAnthropicCaptures(*dispatchID)
			if cerr != nil {
				err = cerr
			} else {
				res.CapturesReparsed = n
				if n > 0 {
					res.DispatchesScanned = 1
				}
			}
		case dispatchID != nil:
			// BACI-323: --retry-failed drops the parse_failed_at predicate so the
			// count mirrors the wet run (which clears the dispatch's markers first).
			n, cerr := d.store.CountUnparsedDispatchCaptures(*dispatchID, retryFailed)
			if cerr != nil {
				err = cerr
			} else {
				res.CapturesReparsed = n
				if n > 0 {
					res.DispatchesScanned = 1
				}
			}
		default:
			res, err = d.store.ProjectReparseUnparsedDispatches(store.ReparseOpts{RetryFailed: retryFailed})
		}
		if err != nil {
			s, c := statusForError(err)
			writeError(w, s, c, err.Error(), nil)
			return
		}
		writeDryRun(w, http.StatusOK, &res)
		return
	}

	var (
		res store.ReparseResult
		err error
	)
	switch {
	case rebuild:
		// BACI-325: per-dispatch rebuild — RebuildDispatch deletes the dispatch's
		// rows and clears its parse_failed_at markers itself, so the BACI-323
		// retry-failed clear is redundant here and skipped (dispatch is required).
		res, err = d.store.RebuildDispatch(*dispatchID)
	case dispatchID != nil:
		// BACI-323: clear the scoped dispatch's markers first when retrying
		// failures — ReparseDispatch skips a stamped capture, so the clear is one
		// layer up (mirrors the local client).
		if retryFailed {
			if _, cerr := d.store.ClearProxyParseFailed(store.ClearParseFailedOpts{Dispatch: dispatchID}); cerr != nil {
				s, c := statusForError(cerr)
				writeError(w, s, c, cerr.Error(), nil)
				return
			}
		}
		res, err = d.store.ReparseDispatch(*dispatchID)
	default:
		res, err = d.store.ReparseUnparsedDispatches(store.ReparseOpts{RetryFailed: retryFailed})
	}
	if err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	if res.Total() > 0 {
		recordOp(d.store, d.logger, model.HistoryEntry{
			Actor:   ActorFromContext(r.Context()),
			Op:      "proxy.reparse",
			Kind:    "sweep",
			Details: fmt.Sprintf(`{"dispatches_scanned":%d,"captures_reparsed":%d,"captures_failed":%d}`,
				res.DispatchesScanned, res.CapturesReparsed, res.CapturesFailed),
		})
	}
	writeJSON(w, http.StatusOK, &res)
}

// readProxyID parses a positive int64 path value (the proxy_requests id or a
// dispatch id), writing a 400 and returning ok=false on a bad value.
func readProxyID(r *http.Request, w http.ResponseWriter, field string) (int64, bool) {
	raw := r.PathValue(field)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"invalid "+field+" "+raw, map[string]any{"field": field})
		return 0, false
	}
	return id, true
}
