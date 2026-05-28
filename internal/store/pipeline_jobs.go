package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

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

// PipelineJobsForIssues bulk-reads the job chains for a set of issues,
// keyed by issue_id and sequence-ordered within each issue — the board
// assembler attaches the per-card chain in one query instead of N.
// Issues with no chain are simply absent from the map.
func (s *Store) PipelineJobsForIssues(issueIDs []int64) (map[int64][]*model.PipelineJob, error) {
	out := map[int64][]*model.PipelineJob{}
	if len(issueIDs) == 0 {
		return out, nil
	}
	ph := make([]string, len(issueIDs))
	args := make([]any, len(issueIDs))
	for i, id := range issueIDs {
		ph[i] = "?"
		args[i] = id
	}
	rows, err := s.DB.Query(
		`SELECT `+pipelineJobCols+` FROM pipeline_jobs WHERE issue_id IN (`+strings.Join(ph, ",")+`) ORDER BY issue_id, sequence ASC`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		j, err := scanPipelineJob(rows)
		if err != nil {
			return nil, err
		}
		out[j.IssueID] = append(out[j.IssueID], j)
	}
	return out, rows.Err()
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

// StartPipelineJobWithDispatch CAS-transitions a pending job to running
// and stamps the dispatch it will run against, in one guarded UPDATE.
// Returns true when this caller won the transition (one row affected);
// false means the job was no longer pending (a concurrent start / cancel
// raced it), so the caller should cancel the dispatch it queued to avoid
// an orphan. started_at is stamped on the first transition. The CAS is
// belt-and-suspenders over the leader gate — only one process runs the
// engine, but the manual Start API and the engine could both target the
// same job.
func (s *Store) StartPipelineJobWithDispatch(jobID, dispatchID int64) (bool, error) {
	res, err := s.DB.Exec(
		`UPDATE pipeline_jobs SET status = 'running', dispatch_id = ?, started_at = COALESCE(started_at, CURRENT_TIMESTAMP) WHERE id = ? AND status = 'pending'`,
		dispatchID, jobID,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
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

// cancelRunningPipelineJob abandons an issue's in-flight pipeline run:
// it cancels the currently-running job and the dispatch it's running
// against, turns engine Auto off, and clears the pause reason. A no-op on
// the job/dispatch when nothing is running, but it always disarms Auto so
// a card pulled out of the pipeline doesn't silently resume on re-entry.
// Mirrors pipeline.Engine.StopRunning, but lives in the store so
// SetIssueState can tear a card down as it leaves in_pipeline without an
// import cycle (the pipeline package imports the store, not the reverse).
//
// A dispatch already delivered to a worker can't be cancelled (BACI-130);
// that's tolerated — the job is still marked cancelled, so the worker's
// eventual ack is ignored because the job is terminal. Write failures on
// the job-status / engine-field updates are returned.
func (s *Store) cancelRunningPipelineJob(issueID int64) error {
	jobs, err := s.ListPipelineJobs(issueID)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.Status != model.JobRunning {
			continue
		}
		if j.DispatchID != nil {
			// Best-effort: a delivered dispatch can't be cancelled, but the
			// job is settled below regardless.
			_, _ = s.CancelDispatch(*j.DispatchID)
		}
		if err := s.SetPipelineJobStatus(j.ID, model.JobCancelled); err != nil {
			return err
		}
		break // at most one job is ever running
	}
	if err := s.SetIssueEngineMode(issueID, model.EngineOff); err != nil {
		return err
	}
	return s.SetIssueEnginePauseReason(issueID, "")
}
