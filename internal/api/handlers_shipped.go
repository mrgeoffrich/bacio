package api

// BACI-187: shipping-log read endpoint. Per-repo list of recently-done
// issues, newest-first, scoped to issues.state='done' AND
// terminal_at IS NOT NULL (cancelled excluded by design; see the
// design doc's "Out of scope" block). Drives the Pipeline Shipping
// column's Shipped pill's popover on the desktop / web frontend.

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

// ShippedListResponse (BACI-221) wraps the popover's per-fetch rows
// alongside the total count under the same scope. The popover renders
// "N rows of TOTAL" and uses Total to seed the Pipeline Shipping-column
// pill without an extra round trip on first open. The pre-BACI-221 response shape was
// a bare []ShippedIssue; the wrapper is a breaking change, but the
// only consumers are the desktop / web ShippedPopover (which lands
// in lockstep with this) and the cross-transport remote client
// (which decodes via its own private type).
type ShippedListResponse struct {
	Rows  []ShippedIssue `json:"rows"`
	Total int            `json:"total"`
}

// ShippedCountResponse is the body of GET /repos/{prefix}/shipped/count
// — total shipped under the current ?since= scope, polled on the same
// 10s cadence as the leader / agents endpoints so the Pipeline
// Shipping-column pill reflects "Forever" and includes archived /
// board-hidden rows. No
// limit parameter — the count is total under the scope.
type ShippedCountResponse struct {
	Total int `json:"total"`
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
	// the Pipeline Shipping-column pill's seven-day count window — the
	// popover happily shows older rows so the operator can scroll back
	// through "what shipped last month".
	shippedAPIDefaultSinceDays = 30
)

// parseShippedSince reads the shared ?since= parsing for both the list
// and count handlers. Returns (cutoff, ok) — ok=false means the handler
// already wrote a 400 envelope and the caller should bail. When
// useDefault is true, an absent ?since= falls back to the
// shippedAPIDefaultSinceDays cutoff (the list handler's behaviour);
// when false, an absent ?since= returns a nil pointer = no window
// (the count handler's behaviour, so a caller asking for "Forever"
// can omit the parameter entirely).
func parseShippedSince(w http.ResponseWriter, r *http.Request, useDefault bool) (*time.Time, bool) {
	if v := r.URL.Query().Get("since"); v != "" {
		dur, err := timeparse.Lookback(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "since"})
			return nil, false
		}
		if dur > 0 {
			cutoff := time.Now().Add(-dur)
			return &cutoff, true
		}
		return nil, true
	}
	if useDefault {
		cutoff := time.Now().Add(-shippedAPIDefaultSinceDays * 24 * time.Hour)
		return &cutoff, true
	}
	return nil, true
}

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

	since, ok := parseShippedSince(w, r, true)
	if !ok {
		return
	}
	filter.Since = since

	issues, err := d.store.ListShippedIssues(filter)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}

	rows := make([]ShippedIssue, 0, len(issues))
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
		rows = append(rows, row)
	}

	// BACI-221: count the same scope server-side so the popover's
	// header can render "showing N of TOTAL" and the Pipeline
	// Shipping-column pill can seed off the same number without an extra
	// round trip on first open. Count ignores filter.Limit by design —
	// total under the scope, not per-fetch.
	total, err := d.store.CountShippedIssues(filter)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, ShippedListResponse{Rows: rows, Total: total})
}

// handleShippedCount (BACI-221) is the lean count-only sibling of
// handleShippedList — the Pipeline Shipping-column Shipped pill polls
// this on the same 10s cadence as the other live read endpoints so its
// number reflects the active Today / Last Week / Forever scope even
// when the popover isn't open. No ?limit= parameter: count is total
// under the scope.
func (d deps) handleShippedCount(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	// useDefault=false: the pill's "Forever" scope intentionally omits
	// ?since=, so the default 30-day cutoff would silently undercount.
	since, ok := parseShippedSince(w, r, false)
	if !ok {
		return
	}
	filter := store.ShippedFilter{RepoID: &repo.ID, Since: since}
	total, err := d.store.CountShippedIssues(filter)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, ShippedCountResponse{Total: total})
}
