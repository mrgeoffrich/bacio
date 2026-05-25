package api

// BACI-187: shipping-log read endpoint. Per-repo list of recently-done
// issues, newest-first, scoped to issues.state='done' AND
// terminal_at IS NOT NULL (cancelled excluded by design; see the
// design doc's "Out of scope" block). Drives the topbar Shipped pill's
// popover on the desktop / web frontend.

import (
	"net/http"
	"strconv"
	"time"

	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/timeparse"
)

// ShippedIssue is the popover-row DTO. Lean by design — key + title +
// terminalAt + tags + (optional) featureSlug/featureEmoji/prUrl is
// everything the row renders. Full bodies / claimants / dispatch
// shape live on the issue brief, one click away.
type ShippedIssue struct {
	Key          string    `json:"key"`
	Title        string    `json:"title"`
	TerminalAt   time.Time `json:"terminalAt"`
	Tags         []string  `json:"tags"`
	FeatureSlug  string    `json:"featureSlug,omitempty"`
	FeatureEmoji string    `json:"featureEmoji,omitempty"`
	PRURL        string    `json:"prUrl,omitempty"`
}

const (
	// shippedAPIDefaultLimit is what the popover's first-open fetch
	// asks for when ?limit= is omitted. Twenty rows fits the menu
	// height comfortably and matches the store-side default.
	shippedAPIDefaultLimit = 20
	// shippedAPIMaxLimit caps caller-supplied ?limit=. Past this the
	// operator wants the History tab — the popover is for the glance.
	shippedAPIMaxLimit = 100
	// shippedAPIDefaultSinceDays is the window the popover's first-open
	// fetch asks for when ?since= is omitted. Thirty days is wider than
	// the topbar pill's seven-day count window — the popover happily
	// shows older rows so the operator can scroll back through
	// "what shipped last month".
	shippedAPIDefaultSinceDays = 30
)

func (d deps) handleShippedList(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	q := r.URL.Query()
	filter := store.ShippedFilter{RepoID: &repo.ID}

	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid_input", "limit must be a positive integer", map[string]any{"field": "limit"})
			return
		}
		if n > shippedAPIMaxLimit {
			n = shippedAPIMaxLimit
		}
		filter.Limit = n
	} else {
		filter.Limit = shippedAPIDefaultLimit
	}

	// ?since= mirrors /history's parsing: a duration like "7d" / "24h"
	// flows through timeparse.Lookback. Omit it for the default 30-day
	// window. An explicit since=0 means "no window" — pass nothing.
	if v := q.Get("since"); v != "" {
		dur, err := timeparse.Lookback(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "since"})
			return
		}
		if dur > 0 {
			cutoff := time.Now().Add(-dur)
			filter.Since = &cutoff
		}
	} else {
		cutoff := time.Now().Add(-shippedAPIDefaultSinceDays * 24 * time.Hour)
		filter.Since = &cutoff
	}

	issues, err := d.store.ListShippedIssues(filter)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}

	out := make([]ShippedIssue, 0, len(issues))
	for _, iss := range issues {
		row := ShippedIssue{
			Key:          iss.Key,
			Title:        iss.Title,
			Tags:         iss.Tags,
			FeatureSlug:  iss.FeatureSlug,
			FeatureEmoji: iss.FeatureEmoji,
		}
		if row.Tags == nil {
			row.Tags = []string{}
		}
		if iss.TerminalAt != nil {
			row.TerminalAt = *iss.TerminalAt
		}
		// First PR by insertion order — matches how IssueWorkspace's PR
		// list reads (id ASC; ListPRs orders created_at, id), and the
		// shipped row only has space for one chip. A list error is
		// non-fatal — the row still renders, just without the PR chip.
		prs, perr := d.store.ListPRs(iss.ID)
		if perr == nil && len(prs) > 0 {
			row.PRURL = prs[0].URL
		}
		out = append(out, row)
	}
	// Empty list returns `[]`, never `null` — same rule as
	// handleHistoryRepo so the JS side can iterate unconditionally.
	writeJSON(w, http.StatusOK, out)
}
