package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

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
