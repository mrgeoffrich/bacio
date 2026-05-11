package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/timeparse"
)

func (d deps) handleHistoryAll(w http.ResponseWriter, r *http.Request) {
	d.serveHistory(w, r, nil)
}

func (d deps) handleHistoryRepo(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	d.serveHistory(w, r, &repo.ID)
}

func (d deps) serveHistory(w http.ResponseWriter, r *http.Request, repoID *int64) {
	q := r.URL.Query()
	f := store.HistoryFilter{
		RepoID: repoID,
		Op:     q.Get("op"),
		Kind:   q.Get("kind"),
	}
	// `actor` is the canonical name; `user_filter` is accepted as an alias
	// to match the CLI flag `--user-filter` so docs/scripts can use either.
	if a := q.Get("actor"); a != "" {
		f.Actor = a
	} else if a := q.Get("user_filter"); a != "" {
		f.Actor = a
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid_input", "limit must be a non-negative integer", map[string]any{"field": "limit"})
			return
		}
		f.Limit = n
	} else {
		f.Limit = 50
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid_input", "offset must be a non-negative integer", map[string]any{"field": "offset"})
			return
		}
		f.Offset = n
	}
	if v := q.Get("oldest_first"); v == "true" || v == "1" {
		f.OldestFirst = true
	}
	since := q.Get("since")
	from := q.Get("from")
	to := q.Get("to")
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
		f.From = &cutoff
	}
	if from != "" {
		t, err := timeparse.Timestamp(from)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", "from: "+err.Error(), map[string]any{"field": "from"})
			return
		}
		f.From = &t
	}
	if to != "" {
		t, err := timeparse.Timestamp(to)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", "to: "+err.Error(), map[string]any{"field": "to"})
			return
		}
		f.To = &t
	}
	if f.From != nil && f.To != nil && f.To.Before(*f.From) {
		writeError(w, http.StatusBadRequest, "invalid_input", "to must not be before from", nil)
		return
	}
	rows, err := d.store.ListHistory(f)
	if err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	if rows == nil {
		rows = []*model.HistoryEntry{}
	}
	writeJSON(w, http.StatusOK, rows)
}
