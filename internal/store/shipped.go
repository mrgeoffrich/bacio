package store

import (
	"fmt"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// ShippedFilter scopes the BACI-187 shipping-log read. RepoID nil
// means every repo — the BACI-371 default for the Shipped pill; set it
// to narrow to one repo. Since clamps the window to a lower bound on
// `terminal_at` (omit for "as far back as Limit allows"); Limit caps
// the result count.
type ShippedFilter struct {
	RepoID *int64
	Since  *time.Time
	Limit  int
}

const (
	// shippedDefaultLimit is the popover's default row count when the
	// caller passes Limit=0. Matches the desktop popover's first-open
	// fetch — twenty rows is enough for the "what landed this week"
	// glance the ticket asked for without overflowing the menu.
	shippedDefaultLimit = 20
	// shippedMaxLimit caps the result count regardless of caller
	// request. Past this the operator wants the History tab — keeps the
	// SQL bounded and the JSON payload modest.
	shippedMaxLimit = 200
)

// ListShippedIssues returns issues that landed in the Done column —
// `state = 'done' AND terminal_at IS NOT NULL` — newest-first by
// `terminal_at`. The popover that drives this read deliberately
// excludes cancelled rows: this is a "what shipped" view, not a
// "what closed" view (see BACI-187 plan, Out of scope).
//
// terminal_at is the BACI-138 timestamp stamped on every transition
// into a terminal state. The migration backfill seeds the column from
// updated_at for pre-BACI-138 rows; LWW edits on a closed issue after
// the backfill can drift the column forward of the actual close time,
// which is an acceptable quirk for a momentum-tracking surface
// (documented in the design doc).
//
// shippedWhere builds the WHERE clause + args bag shared between
// ListShippedIssues (the row read) and CountShippedIssues (the BACI-221
// scope-count read). Keeping the two on one helper means the count and
// the list can never silently drift on their state / repo / since
// predicates — a regression on one side would surface immediately as a
// count vs row-count mismatch in the popover.
func shippedWhere(f ShippedFilter) (where []string, args []any) {
	where = []string{
		"i.state = ?",
		"i.terminal_at IS NOT NULL",
	}
	args = []any{string(model.StateDone)}
	if f.RepoID != nil {
		where = append(where, "i.repo_id = ?")
		args = append(args, *f.RepoID)
	}
	if f.Since != nil {
		where = append(where, "i.terminal_at >= ?")
		args = append(args, f.Since.UTC().Format("2006-01-02 15:04:05"))
	}
	return where, args
}

// Uses idx_issues_terminal_at; the EXPLAIN QUERY PLAN assertion in
// the store test pins the index against a future regression.
func (s *Store) ListShippedIssues(f ShippedFilter) ([]*model.Issue, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = shippedDefaultLimit
	}
	if limit > shippedMaxLimit {
		limit = shippedMaxLimit
	}
	where, args := shippedWhere(f)
	q := issueSelect + ` WHERE `
	for i, w := range where {
		if i > 0 {
			q += " AND "
		}
		q += w
	}
	q += fmt.Sprintf(` ORDER BY i.terminal_at DESC, i.id DESC LIMIT %d`, limit)

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Issue
	var ids []int64
	for rows.Next() {
		iss, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		// Strip the heavy description column — the popover only renders
		// key + title + relative date + tags + (optional) PR chip. Same
		// rule the default IssueFilter applies.
		iss.Description = ""
		out = append(out, iss)
		ids = append(ids, iss.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tagMap, err := s.loadTagsForIssues(ids)
	if err != nil {
		return nil, err
	}
	for _, iss := range out {
		iss.Tags = tagMap[iss.ID]
		if iss.Tags == nil {
			iss.Tags = []string{}
		}
	}
	return out, nil
}

// CountShippedIssues returns the total number of shipped (done +
// non-NULL terminal_at) issues matching the same WHERE that
// ListShippedIssues applies. f.Limit is ignored — the count is the
// total under the scope, independent of how many rows the popover
// would inflate per fetch.
//
// BACI-221: backs the Pipeline Shipping-column "Shipped · N" pill so
// the count always agrees with the popover's list under the active
// Today / Last Week / Forever scope. The pre-BACI-221 pill derived its
// count client-side from the polled cards array — that path
// undercounted because cards are filtered by show_archived and the
// per-feature board-hide set,
// and it couldn't represent "Forever" at all. Moving the count
// server-side fixes both.
//
// Uses the same idx_issues_terminal_at-driven plan as
// ListShippedIssues; a COUNT(*) over the existing predicates is an
// index scan, not a table scan.
func (s *Store) CountShippedIssues(f ShippedFilter) (int, error) {
	where, args := shippedWhere(f)
	q := `SELECT COUNT(*) FROM issues i WHERE `
	for i, w := range where {
		if i > 0 {
			q += " AND "
		}
		q += w
	}
	var n int
	if err := s.DB.QueryRow(q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
