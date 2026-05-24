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

// maxDispatchPayload bounds the free-form instruction body. Comfortably
// fits a multi-phase prompt template (the built-in `design` template is
// ~22 KB) with headroom; a cap still stops a runaway caller filling
// the local DB. Shared with the ack-note path — same reasoning.
const maxDispatchPayload = 256 * 1024

// AddDispatchIn is the validated tuple AddDispatch consumes. RepoID
// and CreatedBy are always required.
//
// Target rules:
//   - Targeted dispatch (the default, InitialStatus = "" or
//     DispatchPending) requires a target: TargetAgentID,
//     TargetSessionID, or both.
//   - Queued dispatch (BACI-51: InitialStatus = DispatchQueued) is
//     target-less by construction — the background matcher binds the
//     target later via BindQueuedDispatch.
//
// InitialStatus selects between the two paths. Empty defaults to
// DispatchPending so every existing caller stays correct.
type AddDispatchIn struct {
	RepoID          int64
	TargetAgentID   *int64
	TargetSessionID string
	IssueID         *int64
	Mode            model.DispatchMode
	Payload         string
	CreatedBy       string
	InitialStatus   model.DispatchStatus
}

// AddDispatch records a new dispatch. Defaults to status='pending'
// targeting a named agent/session; pass InitialStatus = DispatchQueued
// for the BACI-51 enqueue path (no target — the matcher binds later).
// Existence of the target agent / session / issue is enforced by foreign
// keys — a bad id surfaces as a constraint error rather than a silent
// no-op.
func (s *Store) AddDispatch(in AddDispatchIn) (*model.AgentDispatch, error) {
	if in.RepoID == 0 {
		return nil, errors.New("dispatch requires a repo")
	}
	actor, err := ValidateActor(in.CreatedBy)
	if err != nil {
		return nil, err
	}
	status := in.InitialStatus
	if status == "" {
		status = model.DispatchPending
	}
	switch status {
	case model.DispatchPending:
		if in.TargetAgentID == nil && in.TargetSessionID == "" {
			return nil, errors.New("dispatch requires a target: an agent identity, a session, or both")
		}
	case model.DispatchQueued:
		// Queued rows are target-less by construction (the matcher binds
		// the agent later). A queued insert that also carries a target
		// is a caller bug — refuse it rather than silently dropping the
		// target.
		if in.TargetAgentID != nil || in.TargetSessionID != "" {
			return nil, errors.New("queued dispatches must not carry a target — the matcher will bind one")
		}
	default:
		return nil, fmt.Errorf("dispatch InitialStatus %q not supported (use queued or pending)", status)
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
		    (repo_id, target_agent_id, target_session_id, issue_id, mode, payload, status, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		in.RepoID, nullableInt(in.TargetAgentID), in.TargetSessionID,
		nullableInt(in.IssueID), string(in.Mode), in.Payload, string(status), actor,
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
//
// BACI-57: when the dispatch carries a target_session_id, the ack
// also bumps agent_sessions.last_seen_at for that session inside the
// same transaction — the ack is proof the session is alive (only the
// bacio channel running inside the agent's Claude process can
// produce one), so it counts as a heartbeat. This keeps the
// idle-pinger's "last seen" clock honest without re-querying for
// "latest acked ping" on every reaper tick.
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
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		`UPDATE agent_dispatches
		    SET status = 'acked', acked_at = CURRENT_TIMESTAMP, ack_note = ?
		  WHERE id = ? AND status IN ('pending','delivered')`, note, id,
	)
	if err != nil {
		return nil, err
	}
	rows, _ := res.RowsAffected()
	if rows > 0 && d.TargetSessionID != "" {
		if _, err := tx.Exec(
			`UPDATE agent_sessions SET last_seen_at = CURRENT_TIMESTAMP
			  WHERE session_id = ? AND ended_at IS NULL`,
			d.TargetSessionID,
		); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetDispatch(id)
}

// CancelDispatch withdraws a queued or pending dispatch.
// An already-cancelled dispatch is returned unchanged; cancelling an
// acked dispatch is an error (the work was already acknowledged).
// BACI-130: cancelling a delivered dispatch is also an error — once
// delivered_at is set the worker has taken the Task call and is doing
// the real work; cancelling the row at that point doesn't stop the
// worker, it just lies in the model. The right way to stop a worker
// mid-flight is to interrupt the agent itself.
// BACI-51: queued cancellation rides the same path as pending so the
// UI's spinner-as-cancel button can clear an un-matched item.
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
	case model.DispatchDelivered:
		// BACI-130: the worker has taken the Task — cancelling the row
		// would drop the kanban activity pill while the work continues.
		// Reject before the transaction so nothing in the issues table
		// is touched.
		ts := ""
		if d.DeliveredAt != nil {
			ts = d.DeliveredAt.UTC().Format(time.RFC3339)
		}
		return nil, fmt.Errorf("dispatch %d has been delivered; cannot cancel (delivered_at %s)", id, ts)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`UPDATE agent_dispatches
		    SET status = 'cancelled'
		  WHERE id = ? AND status IN ('queued','pending')`, id,
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

// WaitingDispatchForIssue returns the active (queued / pending /
// delivered) dispatch targeting an issue, or (nil, nil) when none
// exists. Used by the BACI-51 spinner-as-cancel button to resolve the
// dispatch id without exposing dispatch internals through the card
// DTO. ORDER BY id DESC LIMIT 1 picks the newest if multiple open
// rows exist — defensive, since only one should be open while
// `waiting_for_claim = 1`.
func (s *Store) WaitingDispatchForIssue(repoID, issueID int64) (*model.AgentDispatch, error) {
	row := s.DB.QueryRow(dispatchSelect+`
		WHERE d.repo_id = ? AND d.issue_id = ?
		  AND d.status IN ('queued','pending','delivered')
		ORDER BY d.id DESC LIMIT 1`, repoID, issueID)
	d, err := scanDispatch(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return d, err
}

// ListQueuedModesByRepo returns the distinct slugs of queued dispatches
// for a repo, in lifecycle-friendly order (mode ASC) so the matcher's
// per-tick iteration is deterministic across runs. An empty result is
// the "nothing waiting" case.
//
// Dispatches whose issue_id has since been archived (BACI-68) are
// excluded — the issue's been manually hidden from the board, so the
// matcher must not silently bind a fresh agent to it. AutoDispatchIssue
// already refuses to create such queued rows, but a row created before
// the archive (or a hand-inserted one) still needs the guard.
func (s *Store) ListQueuedModesByRepo(repoID int64) ([]model.DispatchMode, error) {
	rows, err := s.DB.Query(`
		SELECT DISTINCT d.mode FROM agent_dispatches d
		 LEFT JOIN issues i ON d.issue_id = i.id
		 WHERE d.repo_id = ? AND d.status = 'queued'
		   AND (d.issue_id IS NULL OR i.archived_at IS NULL)
		 ORDER BY d.mode ASC`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DispatchMode
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, model.DispatchMode(m))
	}
	return out, rows.Err()
}

// ListQueuedByRepoMode returns queued dispatches for a (repo, mode)
// pair, oldest first. The FIFO order is what the BACI-51 matcher uses
// to pick the next dispatch to bind. created_at is the primary order
// key (matching SQLite's default 1-sec timestamp granularity); id ASC
// tiebreaks so two dispatches inserted in the same second pick the
// numerically older one first.
func (s *Store) ListQueuedByRepoMode(repoID int64, mode model.DispatchMode) ([]*model.AgentDispatch, error) {
	// BACI-68: skip queued dispatches whose target issue has been
	// archived. Same rationale as ListQueuedModesByRepo's guard. The
	// `i` alias is already joined in dispatchSelect; reuse it.
	rows, err := s.DB.Query(dispatchSelect+`
		WHERE d.repo_id = ? AND d.mode = ? AND d.status = 'queued'
		  AND (d.issue_id IS NULL OR i.archived_at IS NULL)
		ORDER BY d.created_at ASC, d.id ASC`, repoID, string(mode))
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

// CountInFlightByMode counts the dispatches currently consuming a
// (repo, mode) slot — pending and delivered, excluding the
// bacio-channel setup-dispatch creator so the channel's own register
// nudges never block real dispatches from binding. This is the query
// the BACI-51 matcher uses to enforce a template's concurrency_limit.
//
// BACI-58 — staleness gate. A `delivered` dispatch whose target agent
// never returns used to permanently strand a slot in the per-(repo,
// mode) count. Now a row only counts when its target is plausibly
// alive — either the targeted identity has a session whose
// `last_seen_at` is within `model.AgentIdlePingThreshold` (20m after
// BACI-133, aligned with the BACI-57 reaper) and isn't ended, or the
// targeted session itself satisfies the same. Pure exclusion (no
// write); the orphan rows are tidied (cancelled or BACI-133-requeued)
// by EndAgentSession's BACI-58 §B cascade once the reaper (or a clean
// SessionEnd hook) stamps `ended_at`.
//
// The threshold is rendered as a SQLite duration string so the live
// `datetime('now', '-3600 seconds')` evaluation matches the row's
// `last_seen_at` exactly.
func (s *Store) CountInFlightByMode(repoID int64, mode model.DispatchMode) (int, error) {
	staleWindow := fmt.Sprintf("-%d seconds", int(model.AgentIdlePingThreshold/time.Second))
	var n int
	err := s.DB.QueryRow(`
		SELECT COUNT(*)
		  FROM agent_dispatches d
		 WHERE d.repo_id = ? AND d.mode = ?
		   AND d.status IN ('pending','delivered')
		   AND d.created_by != ?
		   AND (
		     -- Identity-targeted: at least one alive session for this
		     -- identity is fresh enough to plausibly be working it.
		     (d.target_agent_id IS NOT NULL AND EXISTS (
		       SELECT 1 FROM agent_sessions s
		        WHERE s.agent_id = d.target_agent_id
		          AND s.ended_at IS NULL
		          AND s.last_seen_at > datetime('now', ?)
		     ))
		     OR
		     -- Session-targeted: the named session itself is alive and
		     -- recently seen. Rare for matcher-bound rows (matcher binds
		     -- by identity) but covers the directly-targeted dispatch
		     -- shape too.
		     (d.target_session_id != '' AND EXISTS (
		       SELECT 1 FROM agent_sessions s
		        WHERE s.session_id = d.target_session_id
		          AND s.ended_at IS NULL
		          AND s.last_seen_at > datetime('now', ?)
		     ))
		   )`,
		repoID, string(mode), model.SetupDispatchCreator, staleWindow, staleWindow,
	).Scan(&n)
	return n, err
}

// BindQueuedDispatch atomically binds a queued dispatch to a target
// agent and flips it to pending — the matcher's commit step. The
// WHERE status='queued' guard makes the bind a no-op if a concurrent
// process already matched the row (leader gating makes this near-
// impossible, but the guard is defence in depth). Returns ErrNotFound
// when 0 rows were updated.
func (s *Store) BindQueuedDispatch(id int64, agentID int64) (*model.AgentDispatch, error) {
	res, err := s.DB.Exec(`
		UPDATE agent_dispatches
		   SET target_agent_id = ?, status = 'pending'
		 WHERE id = ? AND status = 'queued'`,
		agentID, id)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrNotFound
	}
	return s.GetDispatch(id)
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
