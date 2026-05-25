package store

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
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
	// QueuedAfterDispatchID (BACI-179), when non-nil, links this dispatch
	// as a follow-on to the named parent dispatch. Valid only on
	// InitialStatus = DispatchQueued; rejected otherwise. The BACI-51
	// matcher skips queued rows whose parent isn't yet acked/cancelled;
	// the controller's promote sweep clears the column once the
	// predecessor settles and no open claim races in on the issue.
	QueuedAfterDispatchID *int64
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
	// BACI-179: a follow-on link is only valid on dormant queued rows —
	// a dispatch already targeted at an agent / session can't sensibly
	// wait for a predecessor. The matcher's dormant gate keys on
	// status='queued' AND queued_after_dispatch_id IS NOT NULL.
	if in.QueuedAfterDispatchID != nil && status != model.DispatchQueued {
		return nil, errors.New("queued_after_dispatch_id is only valid on queued dispatches")
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
		    (repo_id, target_agent_id, target_session_id, issue_id, mode, payload, status, created_by, queued_after_dispatch_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.RepoID, nullableInt(in.TargetAgentID), in.TargetSessionID,
		nullableInt(in.IssueID), string(in.Mode), in.Payload, string(status), actor,
		nullableInt(in.QueuedAfterDispatchID),
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
	       d.delivered_at, d.acked_at, d.ack_note,
	       d.queued_after_dispatch_id
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

// ClaimDispatchDelivery (BACI-202) inserts a (dispatch_id, session_id)
// row in dispatch_deliveries and reports whether THIS call won the row.
// Two sessions can share one agent identity (two Claude Code instances
// registered under the same slug, different claude_pid) and both will
// see the same agent-targeted dispatch through ListDispatches's
// agent-id-OR-session-id filter; the drain path calls this before
// emitting the <channel> event and only proceeds when claimed == true.
// The losing session's drain falls through silently — no
// MarkDispatchDelivered call, no agent.deliver audit row, no channel
// emit. INSERT OR IGNORE is the storage-layer source of truth for the
// uniqueness invariant; the FK to agent_sessions(session_id) keeps the
// ledger from carrying rows for sessions that never existed.
//
// An empty session id is a caller bug (callers must thread a real
// session id through from the drain entrypoint) and short-circuits to
// (false, nil) without touching the table — better than a silent
// "everyone wins" mode that would re-open the double-delivery race the
// table exists to close.
func (s *Store) ClaimDispatchDelivery(dispatchID int64, sessionID string) (bool, error) {
	if sessionID == "" {
		return false, nil
	}
	res, err := s.DB.Exec(
		`INSERT OR IGNORE INTO dispatch_deliveries (dispatch_id, session_id) VALUES (?, ?)`,
		dispatchID, sessionID,
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
	// BACI-179: skip dormant follow-on rows whose predecessor isn't yet
	// settled (acked/cancelled). Correlated NOT EXISTS — design §1
	// picked it over a LEFT JOIN because rows with
	// queued_after_dispatch_id IS NULL short-circuit to constant-true
	// (no probe), and the subquery is a PK-equality lookup on the
	// referenced parent. No new index for this gate; the sweep index
	// (idx_dispatches_queued_after) is for the opposite-direction walk.
	rows, err := s.DB.Query(`
		SELECT DISTINCT d.mode FROM agent_dispatches d
		 LEFT JOIN issues i ON d.issue_id = i.id
		 WHERE d.repo_id = ? AND d.status = 'queued'
		   AND (d.issue_id IS NULL OR i.archived_at IS NULL)
		   AND NOT EXISTS (
		     SELECT 1 FROM agent_dispatches p
		      WHERE p.id = d.queued_after_dispatch_id
		        AND p.status NOT IN ('acked','cancelled')
		   )
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
	// BACI-179: also skip dormant follow-ons whose predecessor isn't
	// yet settled (see ListQueuedModesByRepo for the design choice).
	rows, err := s.DB.Query(dispatchSelect+`
		WHERE d.repo_id = ? AND d.mode = ? AND d.status = 'queued'
		  AND (d.issue_id IS NULL OR i.archived_at IS NULL)
		  AND NOT EXISTS (
		    SELECT 1 FROM agent_dispatches p
		     WHERE p.id = d.queued_after_dispatch_id
		       AND p.status NOT IN ('acked','cancelled')
		  )
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

// InflightByModeForRepo is the bulked sibling of CountInFlightByMode —
// it returns the same count for every mode that has at least one
// in-flight row in `repoID`, in one query. Used by the kanban /
// IssueBrief assembler (BACI-145) so the per-card WaitingState deriver
// can spot a concurrency-cap-blocked queued dispatch without fanning
// out an N-mode CountInFlightByMode walk per repo. Modes with zero
// in-flight rows are omitted from the map — the deriver treats a
// missing entry as zero.
//
// Same staleness gate as the single-row form: a `delivered` dispatch
// only counts when its target identity (or named session) is plausibly
// alive (last_seen_at fresh within model.AgentIdlePingThreshold) so
// stranded orphans don't permanently mark a card as blocked.
func (s *Store) InflightByModeForRepo(repoID int64) (map[model.DispatchMode]int, error) {
	staleWindow := fmt.Sprintf("-%d seconds", int(model.AgentIdlePingThreshold/time.Second))
	rows, err := s.DB.Query(`
		SELECT d.mode, COUNT(*)
		  FROM agent_dispatches d
		 WHERE d.repo_id = ?
		   AND d.status IN ('pending','delivered')
		   AND d.created_by != ?
		   AND (
		     (d.target_agent_id IS NOT NULL AND EXISTS (
		       SELECT 1 FROM agent_sessions s
		        WHERE s.agent_id = d.target_agent_id
		          AND s.ended_at IS NULL
		          AND s.last_seen_at > datetime('now', ?)
		     ))
		     OR
		     (d.target_session_id != '' AND EXISTS (
		       SELECT 1 FROM agent_sessions s
		        WHERE s.session_id = d.target_session_id
		          AND s.ended_at IS NULL
		          AND s.last_seen_at > datetime('now', ?)
		     ))
		   )
		 GROUP BY d.mode`,
		repoID, model.SetupDispatchCreator, staleWindow, staleWindow,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[model.DispatchMode]int)
	for rows.Next() {
		var mode string
		var n int
		if err := rows.Scan(&mode, &n); err != nil {
			return nil, err
		}
		out[model.DispatchMode(mode)] = n
	}
	return out, rows.Err()
}

// BindQueuedDispatch atomically binds a queued dispatch to a target
// agent and flips it to pending — the matcher's commit step. The
// WHERE guard makes the bind a no-op if a concurrent process already
// matched the row, AND (BACI-200) if the row has ever been delivered
// to a worker (`delivered_at IS NOT NULL`). The latter is the sticky
// "this dispatch has been handed to an agent" gate: once a worker has
// the dispatch in hand, no later matcher tick may rebind it to a
// different agent — even if the BACI-133 reaper requeues the row
// after presuming the worker dead. (That recovery still works: the
// reaper's requeue branch resets `delivered_at = NULL` alongside
// status='queued', so a genuinely-dead worker's dispatch is rebindable
// while a live worker's is locked in.)
//
// Returns ErrNotFound when 0 rows were updated. A bare `status='queued'`
// CAS miss and a `delivered_at IS NOT NULL` miss are not distinguished
// at the API surface — both mean "this dispatch is no longer up for
// grabs", which is the matcher's only decision point.
func (s *Store) BindQueuedDispatch(id int64, agentID int64) (*model.AgentDispatch, error) {
	res, err := s.DB.Exec(`
		UPDATE agent_dispatches
		   SET target_agent_id = ?, status = 'pending'
		 WHERE id = ?
		   AND status = 'queued'
		   AND delivered_at IS NULL`,
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
		d            model.AgentDispatch
		prefix       sql.NullString
		agentID      sql.NullInt64
		agentName    sql.NullString
		issueID      sql.NullInt64
		issueKey     string
		delivered    sql.NullTime
		acked        sql.NullTime
		queuedAfter  sql.NullInt64
	)
	err := r.Scan(
		&d.ID, &d.RepoID, &prefix, &agentID, &agentName,
		&d.TargetSessionID, &issueID, &issueKey,
		&d.Mode, &d.Payload, &d.Status, &d.CreatedBy, &d.CreatedAt,
		&delivered, &acked, &d.AckNote, &queuedAfter,
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
	if queuedAfter.Valid {
		v := queuedAfter.Int64
		d.QueuedAfterDispatchID = &v
	}
	return &d, nil
}

// AddFollowOnDispatch creates a dormant follow-on row (BACI-179) linked
// to an open parent dispatch on the same repo. The parent must still be
// open (queued, pending, or delivered) AND targeted at an issue —
// follow-ons are issue-scoped, since the matcher's eventual bind
// derives "free agent for this mode" without an issue context anyway,
// but the controller's promote sweep needs the issue id to gate on
// "no open claim races in". At most one dormant follow-on per issue at
// a time (single-slot v1 — multi-step chains are explicitly out of
// scope per the parent plan).
//
// Returns ErrNotFound when the parent dispatch doesn't exist; an
// explicit error when the parent isn't open, when it has no issue,
// when the supplied mode is unparseable, or when a dormant follow-on
// already exists for the parent's issue. Writes no audit row — the
// caller (typically the client wrapper QueueFollowOnDispatch) records
// the `agent.followon.queue` entry so the audit log carries the
// requesting actor rather than a generic store-layer attribution.
func (s *Store) AddFollowOnDispatch(repoID, parentDispatchID int64, mode model.DispatchMode, createdBy string) (*model.AgentDispatch, error) {
	if repoID == 0 {
		return nil, errors.New("follow-on dispatch requires a repo")
	}
	if _, err := ValidateActor(createdBy); err != nil {
		return nil, err
	}
	if _, err := model.ParseDispatchMode(string(mode)); err != nil {
		return nil, err
	}
	if mode == "" {
		return nil, errors.New("follow-on dispatch requires a mode")
	}
	parent, err := s.GetDispatch(parentDispatchID)
	if err != nil {
		return nil, err
	}
	if parent.RepoID != repoID {
		return nil, fmt.Errorf("follow-on parent dispatch %d is in a different repo", parentDispatchID)
	}
	switch parent.Status {
	case model.DispatchQueued, model.DispatchPending, model.DispatchDelivered:
		// OK — predecessor still open. Note: queued is allowed so a
		// supervisor can chain follow-ons onto a freshly-queued
		// dispatch that hasn't yet bound. The matcher's NOT EXISTS
		// gate handles both queued and pending parents identically:
		// neither is "settled".
	default:
		return nil, fmt.Errorf("follow-on parent dispatch %d is %s; predecessor must still be open", parentDispatchID, parent.Status)
	}
	if parent.IssueID == nil {
		return nil, fmt.Errorf("follow-on parent dispatch %d has no issue; follow-ons are issue-scoped", parentDispatchID)
	}
	// Single-slot per issue: only one dormant follow-on at a time.
	existing, err := s.FollowOnForIssue(repoID, *parent.IssueID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("issue %s already has a follow-on dispatch (id=%d)", parent.IssueKey, existing.ID)
	}
	return s.AddDispatch(AddDispatchIn{
		RepoID:                repoID,
		IssueID:               parent.IssueID,
		Mode:                  mode,
		CreatedBy:             createdBy,
		InitialStatus:         model.DispatchQueued,
		QueuedAfterDispatchID: &parent.ID,
	})
}

// AddDispatchWithFollowOnIn is the validated tuple AddDispatchWithFollowOn
// consumes — the parent's fields plus the follow-on mode. The parent
// is always inserted as a target-less queued row (the BACI-51 enqueue
// path AutoDispatchIssue uses); the follow-on rides as a dormant
// queued row linked to the just-inserted parent via
// queued_after_dispatch_id. Both rows share the same RepoID, IssueID,
// and CreatedBy actor — there's no scenario where the chain spans
// repos or issues.
type AddDispatchWithFollowOnIn struct {
	RepoID       int64
	IssueID      *int64
	Mode         model.DispatchMode
	Payload      string
	CreatedBy    string
	FollowOnMode model.DispatchMode
}

// AddDispatchWithFollowOn (BACI-209) inserts a queued parent dispatch
// plus a dormant follow-on linked to it, both in one transaction. The
// parent is target-less (matcher binds it later), the follow-on
// carries queued_after_dispatch_id = parent.ID so the matcher's
// dormant gate (NOT EXISTS … parent not settled) skips it until the
// parent acks / cancels.
//
// Mirrors AddDispatch + AddFollowOnDispatch's individual contracts:
//   - parent fields validated as for a queued AddDispatch call;
//   - follow-on mode parsed + non-empty;
//   - single-slot per issue (the parent's issue) — if a dormant
//     follow-on already exists on the same issue the call errors
//     before any INSERT runs.
//
// Sharing one transaction matters: if we wrote two sequential
// AddDispatch + AddFollowOnDispatch calls and the second failed, the
// parent would land queued but with no follow-on attached — a
// half-queued state the UI would have to detect and clean up.
//
// Writes no audit row — the caller (typically
// client.AutoDispatchIssueWithFollowOn) records agent.queue for the
// parent + agent.followon.queue for the follow-on so the actor
// attribution comes from the requesting caller rather than the store.
func (s *Store) AddDispatchWithFollowOn(in AddDispatchWithFollowOnIn) (parent, followOn *model.AgentDispatch, err error) {
	if in.RepoID == 0 {
		return nil, nil, errors.New("dispatch requires a repo")
	}
	if in.IssueID == nil {
		// Follow-ons are issue-scoped (the orphan-cancel sweep keys on
		// issue state). A chain dispatch without an issue would land a
		// queued parent + an undeletable orphan follow-on — refuse it
		// here so the caller surfaces a clear error.
		return nil, nil, errors.New("dispatch-with-follow-on requires an issue")
	}
	actor, err := ValidateActor(in.CreatedBy)
	if err != nil {
		return nil, nil, err
	}
	if _, err := model.ParseDispatchMode(string(in.Mode)); err != nil {
		return nil, nil, err
	}
	if in.Mode == "" {
		return nil, nil, errors.New("dispatch-with-follow-on requires a primary mode")
	}
	if _, err := model.ParseDispatchMode(string(in.FollowOnMode)); err != nil {
		return nil, nil, err
	}
	if in.FollowOnMode == "" {
		return nil, nil, errors.New("dispatch-with-follow-on requires a follow-on mode")
	}
	if len(in.Payload) > maxDispatchPayload {
		return nil, nil, fmt.Errorf("dispatch payload too long (%d bytes; max %d)", len(in.Payload), maxDispatchPayload)
	}
	// Single-slot per issue — match AddFollowOnDispatch's invariant. A
	// pre-existing dormant follow-on on the issue would mean the user
	// is double-queueing two next-mode chains on the same ticket.
	existing, err := s.FollowOnForIssue(in.RepoID, *in.IssueID)
	if err != nil {
		return nil, nil, err
	}
	if existing != nil {
		return nil, nil, fmt.Errorf("issue id=%d already has a follow-on dispatch (id=%d)", *in.IssueID, existing.ID)
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	// Parent: target-less queued row, identical shape to the BACI-51
	// AutoDispatchIssue enqueue path.
	res, err := tx.Exec(`
		INSERT INTO agent_dispatches
		    (repo_id, target_agent_id, target_session_id, issue_id, mode, payload, status, created_by, queued_after_dispatch_id)
		VALUES (?, NULL, '', ?, ?, ?, 'queued', ?, NULL)`,
		in.RepoID, nullableInt(in.IssueID), string(in.Mode), in.Payload, actor,
	)
	if err != nil {
		return nil, nil, err
	}
	parentID, err := res.LastInsertId()
	if err != nil {
		return nil, nil, err
	}
	// Follow-on: dormant queued row, linked to the just-inserted parent.
	if _, err := tx.Exec(`
		INSERT INTO agent_dispatches
		    (repo_id, target_agent_id, target_session_id, issue_id, mode, payload, status, created_by, queued_after_dispatch_id)
		VALUES (?, NULL, '', ?, ?, '', 'queued', ?, ?)`,
		in.RepoID, nullableInt(in.IssueID), string(in.FollowOnMode), actor, parentID,
	); err != nil {
		return nil, nil, err
	}
	// A dispatch against a concrete issue flips its waiting_for_claim
	// flag — same as AddDispatch. One UPDATE covers both rows since
	// both target the same issue.
	if _, err := tx.Exec(
		`UPDATE issues SET waiting_for_claim = 1 WHERE id = ?`, *in.IssueID,
	); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	// Reload both rows with their joined columns populated.
	parent, err = s.GetDispatch(parentID)
	if err != nil {
		return nil, nil, err
	}
	// The follow-on is the one row with QueuedAfterDispatchID == parent.ID;
	// load it via FollowOnForIssue rather than tracking the second
	// LastInsertId so the scan goes through the same dispatchSelect
	// projection.
	followOn, err = s.FollowOnForIssue(in.RepoID, *in.IssueID)
	if err != nil {
		return nil, nil, err
	}
	return parent, followOn, nil
}

// CancelFollowOnDispatch cancels the current dormant follow-on for an
// issue (BACI-179) — user-driven removal of a chip from the kanban.
// Idempotent: returns (nil, nil) when there is no dormant row to
// cancel (e.g. already promoted, already cancelled, never existed).
// Returns the cancelled row when the cancel actually flipped one.
// Writes no audit row — the caller records the
// `agent.followon.cancel` entry. The orphan-cancel sweep uses
// CancelOrphanedFollowOns instead so the per-row audit attribution
// stays distinct.
func (s *Store) CancelFollowOnDispatch(repoID, issueID int64) (*model.AgentDispatch, error) {
	d, err := s.FollowOnForIssue(repoID, issueID)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, nil
	}
	return s.CancelDispatch(d.ID)
}

// FollowOnForIssue (BACI-179) returns the current dormant follow-on
// for an issue, or (nil, nil) when none exists. Used by the kanban
// board assembler to fill the chip data on a card (Phase 3) and by
// AddFollowOnDispatch to enforce the single-slot invariant. NB: the
// returned row is the dormant one (queued_after_dispatch_id IS NOT
// NULL); a promoted row is no longer a "follow-on" — it's a regular
// queued dispatch heading for the matcher.
func (s *Store) FollowOnForIssue(repoID, issueID int64) (*model.AgentDispatch, error) {
	row := s.DB.QueryRow(dispatchSelect+`
		WHERE d.repo_id = ? AND d.issue_id = ?
		  AND d.status = 'queued'
		  AND d.queued_after_dispatch_id IS NOT NULL
		ORDER BY d.id DESC LIMIT 1`, repoID, issueID)
	d, err := scanDispatch(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return d, err
}

// PromoteReadyFollowOns (BACI-179, BACI-195) is the controller's
// promote-or-cancel sweep for dormant follow-ons whose predecessor has
// settled AND whose issue has no open claim held by a live session.
//
// For each ready row the per-mode state-gate is re-evaluated against
// the issue's *current* state (BACI-195) — the gate at queue time was
// dropped because the issue is almost always in a state the next-mode
// gate doesn't admit while the parent is still running. Each row goes
// to one of two outcomes:
//
//   - **Promote** — gate passes (or the row has no issue_id, so no
//     gate to check): clears queued_after_dispatch_id, leaving a
//     regular queued row the matcher will bind next tick. Returned in
//     `promoted`.
//   - **Gate-fail cancel** — gate doesn't admit the issue's current
//     state: flips status to 'cancelled', leaving queued_after_dispatch_id
//     intact for debugging. Returned in `gateFailed`. The caller writes
//     a distinguishing audit op (agent.followon.gate_fail) for these so
//     `bacio history` separates them from orphan-cancel / user-cancel.
//
// `gates` carries the loaded state-gates keyed by mode; nil/missing
// entries fail the gate (matches the queue-time semantics that
// AutoDispatchIssue still uses — a mode with no gate registered is
// not runnable from any state). Pass `nil` only in tests that
// pre-load every mode you care about; the controller always loads
// from Store.AllPromptStates so production behaviour is consistent.
//
// Both partitions commit in a single transaction so a sweep is
// atomic — concurrent matcher ticks see either the pre-sweep state
// (every row still dormant) or the post-sweep state (every row
// promoted or cancelled), never a half-applied mix.
//
// Leader-gated by the caller. Empty slices (no error) when no row was
// ready.
//
// The second NOT EXISTS guards against a re-claim racing in between
// predecessor-ack and the promote tick: if the same issue is already
// being worked again, the new claim should service whatever the
// holder needs and a follow-on bind would step on it. The
// agent_sessions.ended_at = NULL join keeps a reaper-killed-but-not-
// yet-released claim from blocking the promote indefinitely (mirrors
// the live-claim check in OpenClaimsForSession).
//
// When issue_id IS NULL the claim EXISTS subquery short-circuits to
// false (no claim is keyed on a NULL issue), so a follow-on whose
// issue was hard-deleted gets promoted on the next tick — which is
// fine: the matcher's existing path handles issue-less queued rows
// already, and there's nothing to gate on (the gate check is skipped
// for rows with no issue_id).
func (s *Store) PromoteReadyFollowOns(gates map[model.DispatchMode][]model.State) (promoted []*model.AgentDispatch, gateFailed []*model.AgentDispatch, err error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	// SELECT first (inside the tx) so we can return the post-update
	// rows with their queued_after_dispatch_id-was-NULL view to the
	// caller. UPDATE … RETURNING is supported by modernc/sqlite but
	// would still need a second query to hydrate the joined repo /
	// agent / issue columns scanDispatch expects.
	//
	// BACI-195: pull mode + the issue's current state on the same row
	// so the per-row gate check below runs without a per-row second
	// query. NULL issue_state (no issue_id) is fine — handled below
	// by skipping the gate check for that row.
	rows, err := tx.Query(`
		SELECT d.id, d.mode, COALESCE(i.state, '')
		  FROM agent_dispatches d
		  LEFT JOIN issues i ON i.id = d.issue_id
		 WHERE d.status = 'queued'
		   AND d.queued_after_dispatch_id IS NOT NULL
		   AND NOT EXISTS (
		     SELECT 1 FROM agent_dispatches p
		      WHERE p.id = d.queued_after_dispatch_id
		        AND p.status NOT IN ('acked','cancelled')
		   )
		   AND NOT EXISTS (
		     SELECT 1 FROM agent_claims c
		       JOIN agent_sessions s ON s.id = c.session_pk
		      WHERE c.issue_id = d.issue_id
		        AND c.released_at IS NULL
		        AND s.ended_at IS NULL
		   )`)
	if err != nil {
		return nil, nil, err
	}
	type readyRow struct {
		id    int64
		mode  string
		state string // empty when issue_id IS NULL
	}
	var ready []readyRow
	for rows.Next() {
		var r readyRow
		if err := rows.Scan(&r.id, &r.mode, &r.state); err != nil {
			rows.Close()
			return nil, nil, err
		}
		ready = append(ready, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(ready) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, nil, err
		}
		return nil, nil, nil
	}
	// Partition into promote vs cancel. Gate check uses pure helper
	// followOnGatePasses so the same logic is unit-testable
	// independently of the DB.
	var promoteIDs, cancelIDs []int64
	for _, r := range ready {
		if followOnGatePasses(gates, model.DispatchMode(r.mode), model.State(r.state)) {
			promoteIDs = append(promoteIDs, r.id)
		} else {
			cancelIDs = append(cancelIDs, r.id)
		}
	}
	for _, id := range promoteIDs {
		if _, err := tx.Exec(
			`UPDATE agent_dispatches SET queued_after_dispatch_id = NULL WHERE id = ?`, id,
		); err != nil {
			return nil, nil, err
		}
	}
	for _, id := range cancelIDs {
		// Leave queued_after_dispatch_id intact on a gate-fail cancel
		// so the audit Details line can still reference the parent
		// dispatch id (followOnDetails reads it). The row is settled
		// — its dormant-ness is moot.
		if _, err := tx.Exec(
			`UPDATE agent_dispatches SET status = 'cancelled' WHERE id = ?`, id,
		); err != nil {
			return nil, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	promoted = make([]*model.AgentDispatch, 0, len(promoteIDs))
	for _, id := range promoteIDs {
		d, err := s.GetDispatch(id)
		if err != nil {
			return nil, nil, err
		}
		promoted = append(promoted, d)
	}
	gateFailed = make([]*model.AgentDispatch, 0, len(cancelIDs))
	for _, id := range cancelIDs {
		d, err := s.GetDispatch(id)
		if err != nil {
			return nil, nil, err
		}
		gateFailed = append(gateFailed, d)
	}
	return promoted, gateFailed, nil
}

// followOnGatePasses (BACI-195) is the pure fire-time gate check
// extracted so PromoteReadyFollowOns and its tests share the same
// semantics. A row with no issue (state == "") always passes — the
// matcher's existing path already handles issue-less queued rows and
// there's nothing meaningful to gate on. Otherwise the issue state
// must appear in gates[mode] (slices.Contains; nil gates[mode] is
// false, matching the AutoDispatchIssue queue-time check semantics).
func followOnGatePasses(gates map[model.DispatchMode][]model.State, mode model.DispatchMode, state model.State) bool {
	if state == "" {
		return true
	}
	return slices.Contains(gates[mode], state)
}

// CancelOrphanedFollowOns (BACI-179) is the controller's orphan-cancel
// sweep: flips every dormant follow-on whose issue has landed in
// `done` or `cancelled` to status='cancelled'. Returns the slice of
// cancelled rows so the caller can write per-row audit history; empty
// slice (no error) when no row was orphan-cancellable. Leader-gated
// by the caller.
//
// Archive (archived_at IS NOT NULL) alone does NOT trigger cancel —
// archive is a visibility flag, not a lifecycle terminal. An
// archived-but-still-in_progress issue could be unarchived and
// worked; we don't want a passing archive sweep to cascade-cancel
// follow-ons on it. The matcher's existing BACI-68 archive guard
// already prevents binding a fresh agent to an archived issue, which
// is the correct safety boundary.
//
// Rows with issue_id IS NULL are skipped: a follow-on whose issue was
// hard-deleted (FK ON DELETE SET NULL nulled the column) has nothing
// to be orphaned against. The promote sweep handles that case
// instead (predecessor settles → column clears → matcher binds).
func (s *Store) CancelOrphanedFollowOns() ([]*model.AgentDispatch, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`
		SELECT d.id FROM agent_dispatches d
		 WHERE d.status = 'queued'
		   AND d.queued_after_dispatch_id IS NOT NULL
		   AND d.issue_id IS NOT NULL
		   AND EXISTS (
		     SELECT 1 FROM issues i
		      WHERE i.id = d.issue_id
		        AND i.state IN ('done','cancelled')
		   )`)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	for _, id := range ids {
		if _, err := tx.Exec(
			`UPDATE agent_dispatches SET status = 'cancelled' WHERE id = ?`, id,
		); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	out := make([]*model.AgentDispatch, 0, len(ids))
	for _, id := range ids {
		d, err := s.GetDispatch(id)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}
