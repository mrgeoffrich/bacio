package api

import (
	"net/http"
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
