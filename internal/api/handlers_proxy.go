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
