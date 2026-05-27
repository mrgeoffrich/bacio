package store

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
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
	// QueuedUntilBlockersClear (BACI-217) marks the row as the
	// blockers-clear follow-on variant: a dormant queued row excluded
	// from the matcher's pool until every issue on the `to` side of an
	// open `blocks` edge pointing at this dispatch's issue is
	// done/cancelled. Valid only on InitialStatus = DispatchQueued;
	// mutually exclusive with QueuedAfterDispatchID on a single row.
	QueuedUntilBlockersClear bool
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
	// BACI-217: the blockers-clear gate has the same dormant invariant,
	// and is mutually exclusive with the parent-gate variant on a
	// single row (a row can wait on a parent OR on its blockers, not
	// both — the Go-side validator is the boundary guard since SQLite
	// CHECKs aren't used on these columns).
	if in.QueuedUntilBlockersClear && status != model.DispatchQueued {
		return nil, errors.New("queued_until_blockers_clear is only valid on queued dispatches")
	}
	if in.QueuedUntilBlockersClear && in.QueuedAfterDispatchID != nil {
		return nil, errors.New("queued_after_dispatch_id and queued_until_blockers_clear are mutually exclusive")
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

	blockersClear := 0
	if in.QueuedUntilBlockersClear {
		blockersClear = 1
	}
	res, err := tx.Exec(`
		INSERT INTO agent_dispatches
		    (repo_id, target_agent_id, target_session_id, issue_id, mode, payload, status, created_by, queued_after_dispatch_id, queued_until_blockers_clear)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.RepoID, nullableInt(in.TargetAgentID), in.TargetSessionID,
		nullableInt(in.IssueID), string(in.Mode), in.Payload, string(status), actor,
		nullableInt(in.QueuedAfterDispatchID), blockersClear,
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
	       d.queued_after_dispatch_id,
	       d.queued_until_blockers_clear
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
	// BACI-179 / BACI-217: skip dormant follow-on rows whose gate has
	// not yet cleared. dormantFollowOnGateSQL() returns the OR'd
	// fragment expressing "this row is dormant" for both variants
	// (parent-acks and blockers-clear); a row not matching it is
	// either a regular queued row or a previously-dormant row whose
	// gate has just cleared, and the matcher should bind it.
	rows, err := s.DB.Query(`
		SELECT DISTINCT d.mode FROM agent_dispatches d
		 LEFT JOIN issues i ON d.issue_id = i.id
		 WHERE d.repo_id = ? AND d.status = 'queued'
		   AND (d.issue_id IS NULL OR i.archived_at IS NULL)
		   AND NOT `+dormantFollowOnGateSQL()+`
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
	// BACI-179 / BACI-217: also skip dormant follow-ons whose gate has
	// not cleared yet — both variants (parent-acks, blockers-clear)
	// are covered by dormantFollowOnGateSQL().
	rows, err := s.DB.Query(dispatchSelect+`
		WHERE d.repo_id = ? AND d.mode = ? AND d.status = 'queued'
		  AND (d.issue_id IS NULL OR i.archived_at IS NULL)
		  AND NOT `+dormantFollowOnGateSQL()+`
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
// to a worker (`delivered_at IS NOT NULL`), AND (BACI-246) if the
// row's dormant gate has re-tightened between the matcher's
// snapshot read and the bind. The latter belt-and-braces is the
// defense-in-depth gate for the blockers-clear variant: a row that
// was non-dormant in ListQueuedByRepoMode's tick-N snapshot may have
// had a blocker flipped back to `in_progress` before the tick's
// bind step runs, and a bind without this guard would fire while
// the blocker is open again. The CAS misses (zero rows affected)
// in that race, leaving the row dormant for the next promote sweep
// to re-evaluate.
//
// `delivered_at IS NOT NULL` is the sticky "this dispatch has been
// handed to an agent" gate: once a worker has the dispatch in hand,
// no later matcher tick may rebind it to a different agent — even
// if the BACI-133 reaper requeues the row after presuming the worker
// dead. (That recovery still works: the reaper's requeue branch
// resets `delivered_at = NULL` alongside status='queued', so a
// genuinely-dead worker's dispatch is rebindable while a live
// worker's is locked in.)
//
// The `d` alias on the UPDATE target lets the shared dormant-gate
// fragment from dormantFollowOnGateSQL() reuse its `d.` column
// references without a second copy. SQLite's UPDATE supports the
// aliased form natively.
//
// Returns ErrNotFound when 0 rows were updated. A bare `status='queued'`
// CAS miss, a `delivered_at IS NOT NULL` miss, and a re-tightened
// dormant gate miss are not distinguished at the API surface — they
// all mean "this dispatch is no longer up for grabs right now",
// which is the matcher's only decision point. The caller may emit
// an Info-level log line to surface the (rare) gate-retighten case;
// see internal/dispatcher/dispatcher.go tickMode.
func (s *Store) BindQueuedDispatch(id int64, agentID int64) (*model.AgentDispatch, error) {
	res, err := s.DB.Exec(`
		UPDATE agent_dispatches AS d
		   SET target_agent_id = ?, status = 'pending'
		 WHERE d.id = ?
		   AND d.status = 'queued'
		   AND d.delivered_at IS NULL
		   AND NOT `+dormantFollowOnGateSQL(),
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
		d              model.AgentDispatch
		prefix         sql.NullString
		agentID        sql.NullInt64
		agentName      sql.NullString
		issueID        sql.NullInt64
		issueKey       string
		delivered      sql.NullTime
		acked          sql.NullTime
		queuedAfter    sql.NullInt64
		blockersClear  int
	)
	err := r.Scan(
		&d.ID, &d.RepoID, &prefix, &agentID, &agentName,
		&d.TargetSessionID, &issueID, &issueKey,
		&d.Mode, &d.Payload, &d.Status, &d.CreatedBy, &d.CreatedAt,
		&delivered, &acked, &d.AckNote, &queuedAfter, &blockersClear,
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
	d.QueuedUntilBlockersClear = blockersClear != 0
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

// AddBlockerFollowOnDispatch (BACI-217) creates a dormant follow-on row
// of the second variant: a queued dispatch whose dormant gate waits
// until every issue on the `to` side of an open `blocks` edge pointing
// at the named issue is in state `done` or `cancelled`. Mirrors
// AddFollowOnDispatch's contract — issue-scoped, single-slot per issue
// across both variants, no audit row (the caller stamps
// `agent.followon.queue` with `gate=blockers` Details).
//
// Validates: actor / mode parseable + non-empty; repo + issue exist;
// no in-flight dispatch is already on the issue (the parent-acks
// variant is the right fit there — refuse silently so the caller's
// client wrapper falls through to that path); the issue has at least
// one open blocker (no point queueing if the gate is already clear —
// the matcher would bind it on its next tick anyway). Returns the new
// dormant row.
func (s *Store) AddBlockerFollowOnDispatch(repoID, issueID int64, mode model.DispatchMode, createdBy string) (*model.AgentDispatch, error) {
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
	// Resolve the issue so the error path returns a meaningful key
	// rather than a bare id. issue must live in repoID.
	iss, err := s.GetIssueByID(issueID)
	if err != nil {
		return nil, err
	}
	if iss.RepoID != repoID {
		return nil, fmt.Errorf("blocker follow-on issue %s is in a different repo", iss.Key)
	}
	// The blockers-clear variant is intended for blocked-and-idle
	// cards; an in-flight parent dispatch on the same issue means the
	// parent-acks variant is the right pick. The client wrapper's
	// branch ordering enforces this, but defend at the boundary too.
	waiting, err := s.WaitingDispatchForIssue(repoID, issueID)
	if err != nil {
		return nil, err
	}
	if waiting != nil {
		return nil, fmt.Errorf("issue %s already has an in-flight dispatch (id=%d); use the parent follow-on variant", iss.Key, waiting.ID)
	}
	// Must have at least one open blocker — otherwise the gate is
	// already clear at queue time and the matcher would bind the row
	// on its next tick. Refuse here so the client wrapper falls
	// through to the existing "no active dispatch" error rather than
	// silently writing a row that the very next sweep promotes.
	blockers, err := s.BlockersFor([]int64{issueID})
	if err != nil {
		return nil, err
	}
	openBlockers := 0
	for _, b := range blockers[issueID] {
		if isOpenBlockerState(b.BlockerState) {
			openBlockers++
		}
	}
	if openBlockers == 0 {
		return nil, fmt.Errorf("issue %s has no open blockers; the blockers-clear follow-on variant is not applicable", iss.Key)
	}
	// Single-slot per issue: only one dormant follow-on at a time,
	// across both variants.
	existing, err := s.FollowOnForIssue(repoID, issueID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("issue %s already has a follow-on dispatch (id=%d)", iss.Key, existing.ID)
	}
	// Direct insert rather than routing through AddDispatch: that path
	// flips `waiting_for_claim = 1` on the issue (parity with a
	// matcher-bound dispatch about to be picked up), but a dormant
	// blockers-clear row is not waiting for an agent — it's waiting
	// for the blocker gate. Surfacing the card as "waiting" before the
	// gate clears would render a spinner on an otherwise idle ticket;
	// the chip is the right surface here.
	actor, err := ValidateActor(createdBy)
	if err != nil {
		return nil, err
	}
	res, err := s.DB.Exec(`
		INSERT INTO agent_dispatches
		    (repo_id, target_agent_id, target_session_id, issue_id, mode, payload, status, created_by, queued_after_dispatch_id, queued_until_blockers_clear)
		VALUES (?, NULL, '', ?, ?, '', 'queued', ?, NULL, 1)`,
		repoID, issueID, string(mode), actor,
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetDispatch(id)
}

// isOpenBlockerState mirrors the "is this blocker still pending" test
// used by boardcards.cards.go's per-card `BlockedBy` filter and by the
// orphan-cancel sweep: anything that isn't `done` or `cancelled` keeps
// the blocked side waiting. Centralised here so the AddBlockerFollowOn
// guard and the matcher's NOT EXISTS predicate (expressed in SQL) read
// against the same definition of "open".
func isOpenBlockerState(s model.State) bool {
	return s != model.StateDone && s != model.StateCancelled
}

// dormantFollowOnGateSQL (BACI-217) returns the SQL fragment that
// expresses "this dispatch row is dormant and its gate has not yet
// cleared". Either the BACI-179 parent-acks gate (parent dispatch is
// still open) or the BACI-217 blockers-clear gate (the issue has at
// least one open blocker) keeps the row dormant. A row with neither
// flag set is not dormant — the matcher binds it like any other queued
// row.
//
// Centralising the fragment keeps the matcher's two query paths
// (ListQueuedModesByRepo, ListQueuedByRepoMode) and the controller's
// promote sweep (PromoteReadyFollowOns) reading from the same source —
// previous variants of this code inlined two near-identical NOT EXISTS
// snippets that drifted easily. The fragment uses `d` as the
// agent_dispatches alias (matching the existing call sites) and `p`
// and `ir`/`i2` for the correlated subqueries so the outer query's
// joins are unaffected.
func dormantFollowOnGateSQL() string {
	return `(
		(d.queued_after_dispatch_id IS NOT NULL AND EXISTS (
		    SELECT 1 FROM agent_dispatches p
		     WHERE p.id = d.queued_after_dispatch_id
		       AND p.status NOT IN ('acked','cancelled')
		))
		OR (d.queued_until_blockers_clear = 1 AND EXISTS (
		    SELECT 1 FROM issue_relations ir
		     JOIN issues i2 ON i2.id = ir.from_issue_id
		     WHERE ir.to_issue_id = d.issue_id
		       AND ir.type = 'blocks'
		       AND i2.state NOT IN ('done','cancelled')
		))
	)`
}

// BlockerObservation (BACI-246) is one row the promote sweep observed
// from a blockers-clear follow-on's `blocks` relation set at the
// moment it cleared the gate. The controller stamps these into the
// `agent.followon.promote` audit row's Details so a reader of
// `bacio history` can answer "which blockers did the gate consider
// cleared?" without rebuilding the world. A blockers-clear follow-on
// whose blocker relation rows were hard-deleted between queue and
// promote naturally produces an empty slice — that's the
// "blockers=[]" case the operator reads as "the relation was gone at
// fire time", distinct from the parent-acks variant (which simply
// has no BlockerSnapshot at all). The read helper lives on Store as
// `blockerSnapshotsTx`, scoped to PromoteReadyFollowOns's transaction
// so the snapshot describes exactly the rows the gate saw.
type BlockerObservation struct {
	BlockerKey   string
	BlockerState model.State
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

// FollowOnForIssue (BACI-179, BACI-217) returns the current dormant
// follow-on for an issue, or (nil, nil) when none exists. Used by the
// kanban board assembler to fill the chip data on a card and by
// AddFollowOnDispatch / AddBlockerFollowOnDispatch to enforce the
// single-slot invariant across both variants. NB: the returned row is
// the dormant one (either queued_after_dispatch_id IS NOT NULL or
// queued_until_blockers_clear = 1); a promoted row is no longer a
// "follow-on" — it's a regular queued dispatch heading for the matcher.
func (s *Store) FollowOnForIssue(repoID, issueID int64) (*model.AgentDispatch, error) {
	row := s.DB.QueryRow(dispatchSelect+`
		WHERE d.repo_id = ? AND d.issue_id = ?
		  AND d.status = 'queued'
		  AND (d.queued_after_dispatch_id IS NOT NULL OR d.queued_until_blockers_clear = 1)
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
	// BACI-217: widen the SELECT to cover both dormant variants
	// (parent-acks and blockers-clear). A row is a candidate iff its
	// status is queued, it carries at least one of the two dormant
	// flags, the dormant gate has cleared, and no live claim is
	// currently racing on the issue. The dormant-gate cleared
	// condition is the inverse of dormantFollowOnGateSQL().
	rows, err := tx.Query(`
		SELECT d.id, d.mode, COALESCE(i.state, '')
		  FROM agent_dispatches d
		  LEFT JOIN issues i ON i.id = d.issue_id
		 WHERE d.status = 'queued'
		   AND (d.queued_after_dispatch_id IS NOT NULL OR d.queued_until_blockers_clear = 1)
		   AND NOT ` + dormantFollowOnGateSQL() + `
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
	// BACI-246: snapshot the blockers-clear rows' live `blocks`
	// relations BEFORE the UPDATE clears queued_until_blockers_clear
	// — once that flag is cleared we lose the ability to tell which
	// promotes were blockers-clear variants vs parent-acks. Reads the
	// same source-of-truth as dormantFollowOnGateSQL so the audit row
	// describes exactly what the gate saw. Parent-acks rows produce
	// no map entry (their gate isn't blocker-based) and end up with a
	// nil BlockerSnapshot on the returned struct.
	var blockerSnapshots map[int64][]BlockerObservation
	if len(promoteIDs) > 0 {
		var blockerVariantIDs []int64
		for _, id := range promoteIDs {
			var blockersClear int
			if err := tx.QueryRow(
				`SELECT queued_until_blockers_clear FROM agent_dispatches WHERE id = ?`, id,
			).Scan(&blockersClear); err != nil {
				return nil, nil, err
			}
			if blockersClear != 0 {
				blockerVariantIDs = append(blockerVariantIDs, id)
			}
		}
		if len(blockerVariantIDs) > 0 {
			snaps, err := s.blockerSnapshotsTx(tx, blockerVariantIDs)
			if err != nil {
				return nil, nil, err
			}
			blockerSnapshots = snaps
		}
	}
	for _, id := range promoteIDs {
		// BACI-217: clear BOTH dormant flags so the promoted row is a
		// clean regular queued dispatch regardless of which variant
		// queued it. queued_after_dispatch_id may already be NULL on a
		// blockers-clear row; the SET is idempotent in that case.
		if _, err := tx.Exec(
			`UPDATE agent_dispatches
			    SET queued_after_dispatch_id = NULL,
			        queued_until_blockers_clear = 0
			  WHERE id = ?`, id,
		); err != nil {
			return nil, nil, err
		}
		// BACI-217: flip the promoted row's issue into waiting_for_claim
		// state. The blockers-clear insert deliberately left the flag
		// alone (the dormant row isn't waiting for the matcher — it's
		// waiting for the blocker gate), so the promote sweep is what
		// surfaces the spinner on the card. The parent-acks variant
		// already had the flag set by the parent's AddDispatch, so the
		// UPDATE is harmless in that case. Skip when issue_id is NULL —
		// the row will still promote into a regular queued state, but
		// there's no issue row to flip.
		if _, err := tx.Exec(
			`UPDATE issues SET waiting_for_claim = 1
			  WHERE id = (SELECT issue_id FROM agent_dispatches WHERE id = ?)
			    AND id IS NOT NULL`, id,
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
		if snap, ok := blockerSnapshots[id]; ok {
			d.BlockerSnapshot = blockerObservationsToModel(snap)
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

// blockerSnapshotsTx reads the live `blocks` relations targeting each
// blockers-clear follow-on's issue, keyed by dispatch id, inside the
// caller's transaction so the snapshot describes exactly the rows the
// gate considered. Mirrors the EXISTS subquery in
// dormantFollowOnGateSQL but materialises every row instead of just
// asserting presence. The returned map carries an entry for every
// dispatch id passed in — non-nil empty slice when no `blocks` rows
// existed (the operator's "the relations were hard-deleted" forensic
// signal), populated slice otherwise.
func (s *Store) blockerSnapshotsTx(tx *sql.Tx, dispatchIDs []int64) (map[int64][]BlockerObservation, error) {
	out := map[int64][]BlockerObservation{}
	if len(dispatchIDs) == 0 {
		return out, nil
	}
	for _, id := range dispatchIDs {
		out[id] = nil
	}
	ph := make([]string, len(dispatchIDs))
	args := make([]any, len(dispatchIDs))
	for i, id := range dispatchIDs {
		ph[i] = "?"
		args[i] = id
	}
	q := fmt.Sprintf(`
		SELECT d.id,
		       r.prefix || '-' || src.number AS blocker_key,
		       src.state
		  FROM agent_dispatches d
		  JOIN issue_relations ir ON ir.to_issue_id = d.issue_id
		  JOIN issues src ON src.id = ir.from_issue_id
		  JOIN repos r ON r.id = src.repo_id
		 WHERE d.id IN (%s)
		   AND ir.type = 'blocks'`, strings.Join(ph, ","))
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var dispatchID int64
		var key string
		var state string
		if err := rows.Scan(&dispatchID, &key, &state); err != nil {
			return nil, fmt.Errorf("scan blocker snapshot: %w", err)
		}
		out[dispatchID] = append(out[dispatchID], BlockerObservation{
			BlockerKey:   key,
			BlockerState: model.State(state),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// blockerObservationsToModel converts store-layer BlockerObservations
// (the type the SQL helpers return) into the model-layer
// DispatchBlockerObservation tag attached to AgentDispatch. Two-shape
// declaration keeps the model package independent of internal/store.
func blockerObservationsToModel(obs []BlockerObservation) []model.DispatchBlockerObservation {
	if len(obs) == 0 {
		// Return a non-nil empty slice so the caller can distinguish
		// "blockers-clear variant, all blocker rows hard-deleted" from
		// "not a blockers-clear variant" (BlockerSnapshot is nil) — the
		// audit emitter reads len()==0 as "stamp blockers=[]".
		return []model.DispatchBlockerObservation{}
	}
	out := make([]model.DispatchBlockerObservation, 0, len(obs))
	for _, o := range obs {
		out = append(out, model.DispatchBlockerObservation{
			BlockerKey:   o.BlockerKey,
			BlockerState: o.BlockerState,
		})
	}
	return out
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
	// BACI-217: orphan-cancel covers both dormant variants. A dormant
	// row whose own issue lands terminal is meaningless regardless of
	// what gate kept it dormant — the user has explicitly said the
	// work on that issue is over, so the follow-on should drop too.
	rows, err := tx.Query(`
		SELECT d.id FROM agent_dispatches d
		 WHERE d.status = 'queued'
		   AND (d.queued_after_dispatch_id IS NOT NULL OR d.queued_until_blockers_clear = 1)
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
