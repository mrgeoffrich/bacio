package store

import (
	"database/sql"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// RepoActivity (BACI-369) is the per-repo activity summary the topbar's
// repository picker orders itself by: when anything last happened in the
// repo, and how many agent jobs are in flight right now.
//
// LastActivityAt is nil for a repo nothing has ever happened in — a
// freshly-registered repo still appears in the list, it just sorts last.
type RepoActivity struct {
	RepoID         int64
	Prefix         string
	LastActivityAt *time.Time
	ActiveJobs     int
}

// ListRepoActivity returns one RepoActivity per tracked repo, in prefix
// order (the caller does the activity ranking — the CLI and every other
// repo listing stay alphabetical).
//
// The recency signal is the audit log: every mutation from every surface
// lands a history row, so MAX(history.created_at) is the broadest "when
// did something last happen here" the store has. History is pruned at 60
// days, so it's floored by MAX(issues.updated_at) — a repo whose audit
// rows have aged out still orders sensibly against its peers.
//
// The in-flight signal is pipeline_jobs.status = 'running', the same
// truth the Pipeline board renders as a running stage.
func (s *Store) ListRepoActivity() ([]RepoActivity, error) {
	rows, err := s.DB.Query(`
		SELECT r.id, r.prefix,
		       (SELECT MAX(h.created_at) FROM history h WHERE h.repo_id = r.id),
		       (SELECT MAX(i.updated_at) FROM issues i WHERE i.repo_id = r.id),
		       (SELECT COUNT(*) FROM pipeline_jobs j
		          JOIN issues i2 ON i2.id = j.issue_id
		         WHERE i2.repo_id = r.id AND j.status = ?)
		FROM repos r ORDER BY r.prefix`, string(model.JobRunning))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RepoActivity{}
	for rows.Next() {
		var (
			a           RepoActivity
			lastHistory sql.NullString
			lastIssue   sql.NullString
			activeJobs  int
		)
		if err := rows.Scan(&a.RepoID, &a.Prefix, &lastHistory, &lastIssue, &activeJobs); err != nil {
			return nil, err
		}
		a.ActiveJobs = activeJobs
		a.LastActivityAt = latestActivity(lastHistory, lastIssue)
		out = append(out, a)
	}
	return out, rows.Err()
}

// latestActivity picks the later of the two aggregate timestamps, or nil
// when neither parses. Both arrive as TEXT: MAX() over a DATETIME column
// strips the affinity the driver would otherwise use to hand back a
// time.Time (same trap as the proxy-message aggregates), so they go
// through parseSQLiteTime rather than a sql.NullTime scan.
func latestActivity(vals ...sql.NullString) *time.Time {
	var latest time.Time
	for _, v := range vals {
		if !v.Valid {
			continue
		}
		t := parseSQLiteTime(v.String)
		if t.IsZero() {
			continue
		}
		if t.After(latest) {
			latest = t
		}
	}
	if latest.IsZero() {
		return nil
	}
	return &latest
}
