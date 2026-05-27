package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

var ErrPRAlreadyAttached = errors.New("PR already attached to this issue")

// AttachPR inserts the PR row. The bump_issue_updated_on_pr_insert
// schema trigger advances issues.updated_at so the sync importer's
// last-writer-wins gate (which keys on issues.updated_at) preserves
// the new row through the next round-trip — see BACI-144 (and
// BACI-142, which fixed the same drift at the call site before the
// invariant was promoted into the schema).
func (s *Store) AttachPR(issueID int64, url string) (*model.PullRequest, error) {
	url, err := ValidatePRURLStrict(url)
	if err != nil {
		return nil, err
	}
	res, err := s.DB.Exec(`INSERT INTO issue_pull_requests (issue_id, url) VALUES (?, ?)`, issueID, url)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, ErrPRAlreadyAttached
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.getPRByID(id)
}

// DetachPR deletes the PR row. The bump_issue_updated_on_pr_delete
// schema trigger advances issues.updated_at when a row went away so
// the LWW gate preserves the deletion through the next sync — see
// BACI-144.
func (s *Store) DetachPR(issueID int64, url string) (int64, error) {
	res, err := s.DB.Exec(`DELETE FROM issue_pull_requests WHERE issue_id = ? AND url = ?`, issueID, url)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ReplacePRsForIssue clears every PR row for issueID and re-attaches
// the supplied URLs. Used by the sync importer to apply the `prs:`
// list from issue.yaml in one shot. Each URL is validated; the
// transaction rolls back on any validator failure.
func (s *Store) ReplacePRsForIssue(issueID int64, urls []string) error {
	cleaned := make([]string, 0, len(urls))
	for _, u := range urls {
		c, err := ValidatePRURLStrict(u)
		if err != nil {
			return err
		}
		cleaned = append(cleaned, c)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM issue_pull_requests WHERE issue_id = ?`, issueID); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO issue_pull_requests (issue_id, url) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, u := range cleaned {
		if _, err := stmt.Exec(issueID, u); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListPRs(issueID int64) ([]*model.PullRequest, error) {
	rows, err := s.DB.Query(`SELECT id, issue_id, url, created_at FROM issue_pull_requests WHERE issue_id = ? ORDER BY created_at, id`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.PullRequest
	for rows.Next() {
		pr, err := scanPR(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

// LatestPRByIssue (BACI-239) is the bulk per-issue PR denorm consumed
// by boardcards.Assemble — sibling of LatestPlanByIssue. Returns a map
// keyed by issue id; issues with no attached PR are absent from the
// map. For each issue with PRs the value carries the
// most-recently-attached row (URL + created_at) plus the total PR
// count so the kanban card chip can surface "N attached" in its
// tooltip without a second round-trip.
//
// Per-issue "latest" uses `(created_at, id) DESC` — newest-attached
// wins, then larger id as the deterministic tiebreaker. Going
// newest-first because a freshly-opened PR after a review iteration is
// the link the user wants to click; if a future ticket disagrees the
// rule lives in this one place and is cheap to flip.
func (s *Store) LatestPRByIssue(issueIDs []int64) (map[int64]*model.LatestPR, error) {
	out := make(map[int64]*model.LatestPR, len(issueIDs))
	if len(issueIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(issueIDs))
	args := make([]any, 0, len(issueIDs))
	for i, id := range issueIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}
	q := `SELECT id, issue_id, url, created_at
	       FROM issue_pull_requests
	       WHERE issue_id IN (` + strings.Join(placeholders, ", ") + `)`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// Track each issue's best (newest) candidate plus a running count.
	// Per-issue PR count is tiny in practice; the Go-side argmax keeps
	// the SQL simple and avoids a correlated subquery.
	type candidate struct {
		created time.Time
		id      int64
		latest  *model.LatestPR
	}
	best := make(map[int64]candidate, len(issueIDs))
	counts := make(map[int64]int, len(issueIDs))
	for rows.Next() {
		var pr model.PullRequest
		if err := rows.Scan(&pr.ID, &pr.IssueID, &pr.URL, &pr.CreatedAt); err != nil {
			return nil, err
		}
		counts[pr.IssueID]++
		cur := candidate{
			created: pr.CreatedAt,
			id:      pr.ID,
			latest: &model.LatestPR{
				URL:       pr.URL,
				CreatedAt: pr.CreatedAt,
			},
		}
		prev, ok := best[pr.IssueID]
		if !ok || prCandidateLess(prev, cur) {
			best[pr.IssueID] = cur
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for issueID, c := range best {
		c.latest.Count = counts[issueID]
		out[issueID] = c.latest
	}
	return out, nil
}

// prCandidateLess reports whether `a` is strictly less recent than `b`
// under the LatestPR ordering — newer created_at wins, then larger id
// as the deterministic tiebreaker. Strict-less semantics mean "first
// seen at the same key wins" when two rows are exactly equal; fine
// here because we just need determinism and the per-issue candidate
// set is tiny.
func prCandidateLess(a, b struct {
	created time.Time
	id      int64
	latest  *model.LatestPR
}) bool {
	if !a.created.Equal(b.created) {
		return a.created.Before(b.created)
	}
	return a.id < b.id
}

func (s *Store) getPRByID(id int64) (*model.PullRequest, error) {
	return scanPR(s.DB.QueryRow(`SELECT id, issue_id, url, created_at FROM issue_pull_requests WHERE id = ?`, id))
}

func scanPR(row rowScanner) (*model.PullRequest, error) {
	var pr model.PullRequest
	err := row.Scan(&pr.ID, &pr.IssueID, &pr.URL, &pr.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan pr: %w", err)
	}
	return &pr, nil
}
