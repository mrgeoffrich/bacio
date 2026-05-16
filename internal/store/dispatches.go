package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// AgentDispatchRetention bounds how long settled dispatches (acked or
// cancelled) are kept. Open dispatches (pending/delivered) never expire.
// Mirrors HistoryRetention / AgentSessionRetention so the prune passes
// share one "what does bacio remember" window.
const AgentDispatchRetention = 60 * 24 * time.Hour

// maxDispatchPayload bounds the free-form instruction body. Generous
// enough for a paragraph or two of context; a cap stops a runaway
// caller filling the local DB.
const maxDispatchPayload = 8192

// AddDispatchIn is the validated tuple AddDispatch consumes. A dispatch
// must name a target: TargetAgentID, TargetSessionID, or both. RepoID
// and CreatedBy are always required.
type AddDispatchIn struct {
	RepoID          int64
	TargetAgentID   *int64
	TargetSessionID string
	IssueID         *int64
	Mode            model.DispatchMode
	Payload         string
	CreatedBy       string
}

// AddDispatch records a new pending dispatch. Existence of the target
// agent / session / issue is enforced by foreign keys — a bad id
// surfaces as a constraint error rather than a silent no-op.
func (s *Store) AddDispatch(in AddDispatchIn) (*model.AgentDispatch, error) {
	if in.RepoID == 0 {
		return nil, errors.New("dispatch requires a repo")
	}
	actor, err := ValidateActor(in.CreatedBy)
	if err != nil {
		return nil, err
	}
	if in.TargetAgentID == nil && in.TargetSessionID == "" {
		return nil, errors.New("dispatch requires a target: an agent identity, a session, or both")
	}
	if in.TargetSessionID != "" {
		if _, err := ValidateSessionID(in.TargetSessionID); err != nil {
			return nil, err
		}
	}
	if len(in.Payload) > maxDispatchPayload {
		return nil, fmt.Errorf("dispatch payload too long (%d bytes; max %d)", len(in.Payload), maxDispatchPayload)
	}
	if _, err := model.ParseDispatchMode(string(in.Mode)); err != nil {
		return nil, err
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`
		INSERT INTO agent_dispatches
		    (repo_id, target_agent_id, target_session_id, issue_id, mode, payload, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		in.RepoID, nullableInt(in.TargetAgentID), in.TargetSessionID,
		nullableInt(in.IssueID), string(in.Mode), in.Payload, actor,
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	// A dispatch against a concrete issue flips its waiting_for_claim
	// flag — transactional with the dispatch insert so a dispatch is
	// never recorded without its issue being flagged. Cleared by
	// AddAgentClaim when an agent claims, or by CancelDispatch.
	if in.IssueID != nil {
		if _, err := tx.Exec(
			`UPDATE issues SET waiting_for_claim = 1 WHERE id = ?`, *in.IssueID,
		); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetDispatch(id)
}

// DispatchFilter scopes ListDispatches. When both TargetAgentID and
// TargetSessionID are set, rows matching EITHER target are returned —
// that's the "everything aimed at me" drain/inbox query, since a
// session both is itself a target and belongs to an agent identity.
// RepoID and Statuses are always AND-ed.
type DispatchFilter struct {
	RepoID          *int64
	TargetAgentID   *int64
	TargetSessionID string
	Statuses        []model.DispatchStatus // empty = any status
	CreatedBy       string                 // "" = any creator
}

const dispatchSelect = `
	SELECT d.id, d.repo_id, r.prefix, d.target_agent_id, a.name,
	       d.target_session_id, d.issue_id,
	       COALESCE(r2.prefix || '-' || i.number, ''),
	       d.mode, d.payload, d.status, d.created_by, d.created_at,
	       d.delivered_at, d.acked_at, d.ack_note
	FROM agent_dispatches d
	LEFT JOIN repos  r  ON r.id  = d.repo_id
	LEFT JOIN agents a  ON a.id  = d.target_agent_id
	LEFT JOIN issues i  ON i.id  = d.issue_id
	LEFT JOIN repos  r2 ON r2.id = i.repo_id`

// ListDispatches returns dispatches matching the filter, newest first.
func (s *Store) ListDispatches(f DispatchFilter) ([]*model.AgentDispatch, error) {
	q := dispatchSelect + ` WHERE 1=1`
	var args []any
	if f.RepoID != nil {
		q += ` AND d.repo_id = ?`
		args = append(args, *f.RepoID)
	}
	switch {
	case f.TargetAgentID != nil && f.TargetSessionID != "":
		q += ` AND (d.target_agent_id = ? OR d.target_session_id = ?)`
		args = append(args, *f.TargetAgentID, f.TargetSessionID)
	case f.TargetAgentID != nil:
		q += ` AND d.target_agent_id = ?`
		args = append(args, *f.TargetAgentID)
	case f.TargetSessionID != "":
		q += ` AND d.target_session_id = ?`
		args = append(args, f.TargetSessionID)
	}
	if len(f.Statuses) > 0 {
		q += ` AND d.status IN (`
		for i, st := range f.Statuses {
			if i > 0 {
				q += `,`
			}
			q += `?`
			args = append(args, string(st))
		}
		q += `)`
	}
	if f.CreatedBy != "" {
		q += ` AND d.created_by = ?`
		args = append(args, f.CreatedBy)
	}
	q += ` ORDER BY d.created_at DESC, d.id DESC`

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.AgentDispatch
	for rows.Next() {
		d, err := scanDispatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDispatch fetches one dispatch by primary key, or ErrNotFound.
func (s *Store) GetDispatch(id int64) (*model.AgentDispatch, error) {
	row := s.DB.QueryRow(dispatchSelect+` WHERE d.id = ?`, id)
	d, err := scanDispatch(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// MarkDispatchDelivered moves a pending dispatch to delivered and stamps
// delivered_at. Idempotent: a dispatch that's already delivered or acked
// is returned unchanged; a cancelled one is also returned as-is (a
// withdrawn dispatch is never resurrected). Returns ErrNotFound for an
// unknown id.
func (s *Store) MarkDispatchDelivered(id int64) (*model.AgentDispatch, error) {
	if _, err := s.GetDispatch(id); err != nil {
		return nil, err
	}
	if _, err := s.DB.Exec(
		`UPDATE agent_dispatches
		    SET status = 'delivered', delivered_at = CURRENT_TIMESTAMP
		  WHERE id = ? AND status = 'pending'`, id,
	); err != nil {
		return nil, err
	}
	return s.GetDispatch(id)
}

// AckDispatch records the agent's acknowledgement of a dispatch and an
// optional free-form note. Pending or delivered dispatches move to
// acked; an already-acked dispatch is returned unchanged (the first
// ack's note wins). Acking a cancelled dispatch is an error.
func (s *Store) AckDispatch(id int64, note string) (*model.AgentDispatch, error) {
	d, err := s.GetDispatch(id)
	if err != nil {
		return nil, err
	}
	switch d.Status {
	case model.DispatchAcked:
		return d, nil
	case model.DispatchCancelled:
		return nil, fmt.Errorf("dispatch %d is cancelled; nothing to ack", id)
	}
	if len(note) > maxDispatchPayload {
		return nil, fmt.Errorf("ack note too long (%d bytes; max %d)", len(note), maxDispatchPayload)
	}
	if _, err := s.DB.Exec(
		`UPDATE agent_dispatches
		    SET status = 'acked', acked_at = CURRENT_TIMESTAMP, ack_note = ?
		  WHERE id = ? AND status IN ('pending','delivered')`, note, id,
	); err != nil {
		return nil, err
	}
	return s.GetDispatch(id)
}

// CancelDispatch withdraws a pending or delivered dispatch. An
// already-cancelled dispatch is returned unchanged; cancelling an acked
// dispatch is an error (the work was already acknowledged).
func (s *Store) CancelDispatch(id int64) (*model.AgentDispatch, error) {
	d, err := s.GetDispatch(id)
	if err != nil {
		return nil, err
	}
	switch d.Status {
	case model.DispatchCancelled:
		return d, nil
	case model.DispatchAcked:
		return nil, fmt.Errorf("dispatch %d is already acked; cannot cancel", id)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`UPDATE agent_dispatches
		    SET status = 'cancelled'
		  WHERE id = ? AND status IN ('pending','delivered')`, id,
	); err != nil {
		return nil, err
	}
	// A cancelled dispatch is no longer "waiting" — clear the issue's
	// flag. Harmless if another open dispatch still targets the issue:
	// the next dispatch/claim re-establishes the correct value, and the
	// flag is only an "is anything happening" hint, not a counter.
	if d.IssueID != nil {
		if _, err := tx.Exec(
			`UPDATE issues SET waiting_for_claim = 0 WHERE id = ?`, *d.IssueID,
		); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetDispatch(id)
}

// pruneDispatches drops settled dispatches (acked or cancelled) whose
// created_at is older than retention. Open dispatches (pending /
// delivered) are never touched.
func pruneDispatches(db *sql.DB, retention time.Duration) error {
	cutoff := time.Now().Add(-retention).UTC().Format("2006-01-02 15:04:05")
	_, err := db.Exec(
		`DELETE FROM agent_dispatches
		  WHERE status IN ('acked','cancelled') AND created_at < ?`, cutoff,
	)
	return err
}

func scanDispatch(r rowScanner) (*model.AgentDispatch, error) {
	var (
		d         model.AgentDispatch
		prefix    sql.NullString
		agentID   sql.NullInt64
		agentName sql.NullString
		issueID   sql.NullInt64
		issueKey  string
		delivered sql.NullTime
		acked     sql.NullTime
	)
	err := r.Scan(
		&d.ID, &d.RepoID, &prefix, &agentID, &agentName,
		&d.TargetSessionID, &issueID, &issueKey,
		&d.Mode, &d.Payload, &d.Status, &d.CreatedBy, &d.CreatedAt,
		&delivered, &acked, &d.AckNote,
	)
	if err != nil {
		return nil, err
	}
	if prefix.Valid {
		d.RepoPrefix = prefix.String
	}
	if agentID.Valid {
		v := agentID.Int64
		d.TargetAgentID = &v
	}
	if agentName.Valid {
		d.TargetAgentName = agentName.String
	}
	if issueID.Valid {
		v := issueID.Int64
		d.IssueID = &v
	}
	d.IssueKey = issueKey
	if delivered.Valid {
		d.DeliveredAt = &delivered.Time
	}
	if acked.Valid {
		d.AckedAt = &acked.Time
	}
	return &d, nil
}
