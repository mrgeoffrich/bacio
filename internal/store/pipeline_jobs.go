package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/mrgeoffrich/bacio/internal/model"
)

const pipelineJobCols = `id, issue_id, sequence, mode, status, dispatch_id, created_at, started_at, completed_at`

func scanPipelineJob(row rowScanner) (*model.PipelineJob, error) {
	var (
		j           model.PipelineJob
		status      string
		dispatchID  sql.NullInt64
		startedAt   sql.NullTime
		completedAt sql.NullTime
	)
	err := row.Scan(&j.ID, &j.IssueID, &j.Sequence, &j.Mode, &status, &dispatchID,
		&j.CreatedAt, &startedAt, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan pipeline job: %w", err)
	}
	j.Status = model.JobStatus(status)
	if dispatchID.Valid {
		v := dispatchID.Int64
		j.DispatchID = &v
	}
	if startedAt.Valid {
		t := startedAt.Time
		j.StartedAt = &t
	}
	if completedAt.Valid {
		t := completedAt.Time
		j.CompletedAt = &t
	}
	return &j, nil
}

// ListPipelineJobs returns the issue's process chain ordered by sequence.
// An issue with no chain returns an empty slice, not an error.
func (s *Store) ListPipelineJobs(issueID int64) ([]*model.PipelineJob, error) {
	rows, err := s.DB.Query(`SELECT `+pipelineJobCols+` FROM pipeline_jobs WHERE issue_id = ? ORDER BY sequence ASC`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.PipelineJob
	for rows.Next() {
		j, err := scanPipelineJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// GetPipelineJob reads one job by id. ErrNotFound when absent.
func (s *Store) GetPipelineJob(id int64) (*model.PipelineJob, error) {
	return scanPipelineJob(s.DB.QueryRow(`SELECT `+pipelineJobCols+` FROM pipeline_jobs WHERE id = ?`, id))
}

// SetIssueProcess materialises a preset process as the issue's pipeline
// job chain — one pending job per stage, sequenced 1..n. Valid only
// before the chain has started: if any existing job has advanced past
// pending (running / complete / cancelled) it returns an error, so a
// completed stage is never clobbered (§7 immutability). Re-picking a
// process while every job is still pending replaces the chain wholesale.
// Returns the freshly-created chain.
func (s *Store) SetIssueProcess(issueID int64, process model.Process) ([]*model.PipelineJob, error) {
	if len(process.Stages) == 0 {
		return nil, fmt.Errorf("process %q has no stages", process.Slug)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var started int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM pipeline_jobs WHERE issue_id = ? AND status != 'pending'`, issueID,
	).Scan(&started); err != nil {
		return nil, err
	}
	if started > 0 {
		return nil, fmt.Errorf("cannot set process: issue %d already has a started job chain", issueID)
	}
	if _, err := tx.Exec(`DELETE FROM pipeline_jobs WHERE issue_id = ?`, issueID); err != nil {
		return nil, err
	}
	for i, mode := range process.Stages {
		if _, err := tx.Exec(
			`INSERT INTO pipeline_jobs (issue_id, sequence, mode, status) VALUES (?, ?, ?, 'pending')`,
			issueID, i+1, mode,
		); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.ListPipelineJobs(issueID)
}

// SetPipelineJobStatus transitions a job's status, stamping started_at on
// the first move to running and completed_at on the first move to a
// terminal status (complete / cancelled). Leaves dispatch_id untouched —
// use SetPipelineJobDispatch for that. The COALESCE guards keep the
// timestamps idempotent across repeated writes of the same status.
func (s *Store) SetPipelineJobStatus(jobID int64, status model.JobStatus) error {
	if _, err := model.ParseJobStatus(string(status)); err != nil {
		return err
	}
	startedClause := "started_at"
	if status == model.JobRunning {
		startedClause = "COALESCE(started_at, CURRENT_TIMESTAMP)"
	}
	completedClause := "completed_at"
	if status.Terminal() {
		completedClause = "COALESCE(completed_at, CURRENT_TIMESTAMP)"
	}
	_, err := s.DB.Exec(
		`UPDATE pipeline_jobs SET status = ?, started_at = `+startedClause+`, completed_at = `+completedClause+` WHERE id = ?`,
		string(status), jobID,
	)
	return err
}

// SetPipelineJobDispatch pins (or clears, with nil) the agent_dispatches
// row a job is running against.
func (s *Store) SetPipelineJobDispatch(jobID int64, dispatchID *int64) error {
	_, err := s.DB.Exec(`UPDATE pipeline_jobs SET dispatch_id = ? WHERE id = ?`, nullableInt(dispatchID), jobID)
	return err
}

// SetIssueEngineMode writes the per-issue controller-engine drive mode
// (off | auto). Does not bump updated_at: this is runtime supervision
// metadata, not user-edited content — same rationale as ReorderIssue /
// SetIssueUserActionReason (a write must not churn the sync LWW gate).
func (s *Store) SetIssueEngineMode(issueID int64, mode model.EngineMode) error {
	if _, err := model.ParseEngineMode(string(mode)); err != nil {
		return err
	}
	_, err := s.DB.Exec(`UPDATE issues SET engine_mode = ? WHERE id = ?`, string(mode), issueID)
	return err
}

// SetIssueEnginePauseReason writes the engine pause reason ("" when not
// paused, "open_question" while halted on a question). Does not bump
// updated_at — same rationale as SetIssueEngineMode.
func (s *Store) SetIssueEnginePauseReason(issueID int64, reason string) error {
	_, err := s.DB.Exec(`UPDATE issues SET engine_pause_reason = ? WHERE id = ?`, reason, issueID)
	return err
}
