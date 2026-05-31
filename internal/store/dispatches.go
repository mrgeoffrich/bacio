package store

import (
	"database/sql"
	"errors"
	"fmt"
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

	// BACI-226: resolve base_branch BEFORE opening the tx — the issue
	// + feature rows we read are independent of the dispatch insert,
	// so a stale read at this point would just mean the value matches
	// the issue+feature state at insert-time-minus-epsilon, which is
	// exactly what BACI-227's concurrency grouping wants. Resolved
	// only when the dispatch carries an issue: setup nudges, idle
	// pings, and other issue-less dispatches leave the column NULL
	// (no PR to target, no concurrency group to participate in).
	baseBranch, err := s.resolveBaseBranchForIssueID(in.IssueID)
	if err != nil {
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
		    (repo_id, target_agent_id, target_session_id, issue_id, mode, payload, status, created_by, queued_after_dispatch_id, queued_until_blockers_clear, base_branch)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.RepoID, nullableInt(in.TargetAgentID), in.TargetSessionID,
		nullableInt(in.IssueID), string(in.Mode), in.Payload, string(status), actor,
		nullableInt(in.QueuedAfterDispatchID), blockersClear, nullableString(baseBranch),
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	// BACI-255: the dispatch row itself is now the canonical "is this
	// issue waiting?" signal — readers consult agent_dispatches via
	// WaitingDispatchForIssue. No denormalised flag to flip here.
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetDispatch(id)
}

// LatestDispatchForIssueMode returns the most recent dispatch of the
// given mode for the issue (any status), or nil when none exists. Used
// by the auto-ship ticker to decide whether to queue a ship dispatch,
// wait on one in flight, advance the card to done on ack, or leave a
// deliberately-cancelled ship alone.
func (s *Store) LatestDispatchForIssueMode(issueID int64, mode model.DispatchMode) (*model.AgentDispatch, error) {
	d, err := scanDispatch(s.DB.QueryRow(dispatchSelect+
		` WHERE d.issue_id = ? AND d.mode = ? ORDER BY d.id DESC LIMIT 1`,
		issueID, string(mode)))
	// scanDispatch returns the raw sql.ErrNoRows (it doesn't map to
	// ErrNotFound), so match that here.
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return d, nil
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
	       d.queued_until_blockers_clear,
	       d.base_branch
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

// ActiveDispatchForSession returns the most-recent non-cancelled dispatch
// targeting sessionID, or (nil, nil) when the session has none. It is the
// shared "active dispatch for a session" pick used by both the BACI-132
// SessionTodo scope (resolveActiveDispatchID in the hook, which additionally
// filters by issue) and the BACI-305 proxy-capture correlation (the recorder,
// which attributes a worktree's capture to its active dispatch). Newest by
// created_at; cancelled rows are skipped so a re-dispatch after a cancel picks
// the live one. An empty session id is a no-op.
func (s *Store) ActiveDispatchForSession(sessionID string) (*model.AgentDispatch, error) {
	if sessionID == "" {
		return nil, nil
	}
	row := s.DB.QueryRow(dispatchSelect+`
		WHERE d.target_session_id = ? AND d.status != ?
		ORDER BY d.created_at DESC, d.id DESC
		LIMIT 1`,
		sessionID, string(model.DispatchCancelled),
	)
	d, err := scanDispatch(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
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
	// BACI-255: the cancelled status on the dispatch row is the signal —
	// readers check agent_dispatches directly via WaitingDispatchForIssue,
	// no denormalised issue flag to clear.
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
// DTO. Post-BACI-255 this is also the canonical "is this issue
// waiting?" predicate — the denormalised issues.waiting_for_claim
// cache it used to mirror was removed because the cache could drift.
//
// BACI-209 / BACI-217 / BACI-255: dormant follow-ons are filtered out.
// A queued row carrying a dormant gate (`queued_after_dispatch_id`
// IS NOT NULL, or `queued_until_blockers_clear = 1`) is waiting for
// the gate, not for an agent — its surface is the BACI-180 chip on
// the card, not the spinner. Mirrors the skip in
// boardcards.activeDispatchByIssueID so the per-issue gate matches
// the kanban's per-card gate. The BACI-179 / BACI-180 follow-on
// flows (QueueFollowOnDispatch, cancel-followon, queue-followon) also
// want the parent here, not a sibling dormant row, so the filter is
// the right semantic for every caller.
//
// ORDER BY id DESC LIMIT 1 picks the newest of the non-dormant
// rows — defensive, since only one should be open at a time.
func (s *Store) WaitingDispatchForIssue(repoID, issueID int64) (*model.AgentDispatch, error) {
	row := s.DB.QueryRow(dispatchSelect+`
		WHERE d.repo_id = ? AND d.issue_id = ?
		  AND d.status IN ('queued','pending','delivered')
		  AND NOT (
		    d.status = 'queued'
		    AND (d.queued_after_dispatch_id IS NOT NULL
		         OR d.queued_until_blockers_clear = 1)
		  )
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

// CountInFlightByModeBase (BACI-227) is the per-branch sibling of
// CountInFlightByMode: same staleness gate, same creator-exclusion,
// same in-flight statuses, plus an extra
// `AND COALESCE(d.base_branch, '') = ?` clause grouping the count to
// rows targeting a specific base branch. Used by the matcher's per-
// tick walk so a `ship` cap of 1 only serialises within a single
// branch — `feat/A`, `feat/B`, and `main` each get their own slot.
//
// The COALESCE collapses NULL (legacy / setup nudge / pre-BACI-226
// rows) to the empty string. The matcher folds NULL →
// model.DefaultBaseBranch ("main") at the call site before invoking
// this helper, matching every read-site default; passing `""` here
// counts only the legacy-shape rows. Note that the count groups by
// the COALESCEd value, so a NULL row will only match a `baseBranch`
// argument of `""`, not `"main"` — callers that want to compare
// against a row's resolved branch should compute the COALESCE on
// their side too.
func (s *Store) CountInFlightByModeBase(repoID int64, mode model.DispatchMode, baseBranch string) (int, error) {
	staleWindow := fmt.Sprintf("-%d seconds", int(model.AgentIdlePingThreshold/time.Second))
	var n int
	err := s.DB.QueryRow(`
		SELECT COUNT(*)
		  FROM agent_dispatches d
		 WHERE d.repo_id = ? AND d.mode = ?
		   AND COALESCE(d.base_branch, '') = ?
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
		   )`,
		repoID, string(mode), baseBranch, model.SetupDispatchCreator, staleWindow, staleWindow,
	).Scan(&n)
	return n, err
}

// InflightKey (BACI-227) is the composite key for the bulked
// InflightByModeBaseForRepo result. A small struct rather than a
// stringly-concatenated key keeps the deriver readable and avoids
// "what separator?" questions when a mode slug or branch name
// contains an unexpected character.
type InflightKey struct {
	Mode       model.DispatchMode
	BaseBranch string
}

// InflightByModeBaseForRepo (BACI-227) is the bulked sibling of
// CountInFlightByModeBase — it returns the same per-(mode, branch)
// count for every (mode, branch) pair that has at least one in-flight
// row in `repoID`, in one query. Used by the kanban / IssueBrief
// assembler's WaitingState deriver so the per-card concurrency-cap
// label tracks the matcher's per-branch gate exactly.
//
// The BaseBranch in the returned key is COALESCE(d.base_branch, '')
// — the same shape CountInFlightByModeBase keys on, so a legacy /
// NULL row groups with the empty string. Read callers fold "" to
// model.DefaultBaseBranch before looking up against the row's
// resolved branch.
//
// Same staleness gate as the single-row form: a `delivered`
// dispatch only counts when its target identity (or named session)
// is plausibly alive (last_seen_at fresh within
// model.AgentIdlePingThreshold) so stranded orphans don't
// permanently mark a card as blocked.
func (s *Store) InflightByModeBaseForRepo(repoID int64) (map[InflightKey]int, error) {
	staleWindow := fmt.Sprintf("-%d seconds", int(model.AgentIdlePingThreshold/time.Second))
	rows, err := s.DB.Query(`
		SELECT d.mode, COALESCE(d.base_branch, ''), COUNT(*)
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
		 GROUP BY d.mode, COALESCE(d.base_branch, '')`,
		repoID, model.SetupDispatchCreator, staleWindow, staleWindow,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[InflightKey]int)
	for rows.Next() {
		var (
			mode   string
			branch string
			n      int
		)
		if err := rows.Scan(&mode, &branch, &n); err != nil {
			return nil, err
		}
		out[InflightKey{Mode: model.DispatchMode(mode), BaseBranch: branch}] = n
	}
	return out, rows.Err()
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
//
// BACI-227: superseded for the kanban/brief WaitingState deriver by
// InflightByModeBaseForRepo — the per-(mode, branch) form. Kept for
// callers that still want the per-mode count regardless of branch
// (none in tree today after the BACI-227 cutover, but the symmetry
// with CountInFlightByMode is cheap to keep).
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
// BACI-226: the bind also stamps `base_branch` with the resolved
// per-(issue, feature) base branch — the single source of truth the
// worker prompt envelope's <base_branch> tag and the BACI-227
// per-(repo, mode, base_branch) concurrency grouping both read.
// Computed via model.ResolveBaseBranch against the freshly-read
// issue + feature; a dispatch without an issue leaves the column
// NULL (no PR to target, no concurrency group to participate in).
//
// Returns ErrNotFound when 0 rows were updated. A bare `status='queued'`
// CAS miss, a `delivered_at IS NOT NULL` miss, and a re-tightened
// dormant gate miss are not distinguished at the API surface — they
// all mean "this dispatch is no longer up for grabs right now",
// which is the matcher's only decision point. The caller may emit
// an Info-level log line to surface the (rare) gate-retighten case;
// see internal/dispatcher/dispatcher.go tickMode.
func (s *Store) BindQueuedDispatch(id int64, agentID int64) (*model.AgentDispatch, error) {
	// Resolve base_branch out-of-band before the CAS — the issue row's
	// shape is independent of the dispatch's status, so a race with a
	// concurrent matcher tick doesn't corrupt the resolved value. If
	// the dispatch is no longer queued we discard the work; if it is,
	// stamping it inside the WHERE-guarded UPDATE keeps the matcher
	// committed value atomic with the queued→pending transition.
	baseBranch, err := s.resolveBaseBranchForDispatch(id)
	if err != nil {
		return nil, err
	}
	// BACI-226: re-render the payload so the freshly-resolved
	// base_branch lands in the worker's Task prompt as the
	// `<base_branch>` stub tag. The bind-time value supersedes the
	// queue-time snapshot the stub was first rendered with, matching
	// the brief's "value seen by the worker must be the value used
	// by the matcher for concurrency grouping" invariant. A nil/empty
	// payload (e.g. an untyped or setup dispatch with no template
	// rendered) skips the rewrite — there's no stub to refresh.
	newPayload, err := s.rerenderQueuedPayloadForBind(id, baseBranch)
	if err != nil {
		return nil, err
	}
	res, err := s.DB.Exec(`
		UPDATE agent_dispatches AS d
		   SET target_agent_id = ?, status = 'pending', base_branch = ?, payload = COALESCE(?, payload)
		 WHERE d.id = ?
		   AND d.status = 'queued'
		   AND d.delivered_at IS NULL
		   AND NOT `+dormantFollowOnGateSQL(),
		agentID, nullableString(baseBranch), nullableString(newPayload), id)
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

// rerenderQueuedPayloadForBind rebuilds the dispatch's payload at
// bind time so the freshly-resolved base_branch lands in the
// worker's Task prompt as the `<base_branch>` stub tag (BACI-226).
// Returns "" when no rewrite should happen — either the dispatch has
// no template/mode/issue, or the source-of-truth ComposeDispatchPayload
// can't be reconstructed without losing the original `note` (which
// today is always "" on queued dispatches but defensively we still
// pass it through). The caller then leaves `payload` untouched.
func (s *Store) rerenderQueuedPayloadForBind(dispatchID int64, baseBranch string) (string, error) {
	d, err := s.GetDispatch(dispatchID)
	if err != nil {
		return "", err
	}
	// Untyped dispatches carry no stub to refresh — payload is just
	// the freeform note (or empty). Setup/ping dispatches with no
	// mode fall here too. Leave them alone.
	if d.Mode == "" || d.IssueKey == "" {
		return "", nil
	}
	preamble, err := s.GetDispatchPreamble()
	if err != nil {
		return "", err
	}
	stub := model.DispatchStub{
		IssueKey:     d.IssueKey,
		Mode:         string(d.Mode),
		BaseBranch:   baseBranch,
		SubagentType: model.SubagentTypeForTemplate(string(d.Mode)),
	}
	// Queued dispatches today are always created with an empty note —
	// `client.AutoDispatchIssue` and friends pass "" to
	// ComposeDispatchPayload. If that ever changes we'd want to plumb
	// the note through as a separate column; for now the empty-note
	// invariant lets us re-render losslessly.
	return model.ComposeDispatchPayload(preamble, stub, ""), nil
}

// resolveBaseBranchForDispatch is the BACI-226 resolver lookup the
// matcher commit calls into: read the dispatch's issue + (optional)
// feature and feed them to model.ResolveBaseBranch. Returns "" when
// the dispatch has no issue (setup nudges, idle pings) so the
// caller can write NULL into the column — the resolver's fallback
// of "main" is for read sites, not for marking "no issue".
func (s *Store) resolveBaseBranchForDispatch(dispatchID int64) (string, error) {
	var issueID sql.NullInt64
	if err := s.DB.QueryRow(
		`SELECT issue_id FROM agent_dispatches WHERE id = ?`, dispatchID,
	).Scan(&issueID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	if !issueID.Valid {
		return "", nil
	}
	v := issueID.Int64
	return s.resolveBaseBranchForIssueID(&v)
}

// resolveBaseBranchForIssueID is the shared BACI-226 resolver entry
// point: given an optional issue PK, read the issue + (optional)
// feature and run them through model.ResolveBaseBranch. Returns ""
// when issueID is nil (the caller is dispatching against no issue —
// setup nudges, idle pings) so the caller writes NULL into the
// column rather than synthesising "main" — the resolver's fallback
// of "main" is the read-site default, not a column value.
//
// A racing hard-delete of the issue (cascade SET NULL on the
// dispatch's foreign key, between this read and the dispatch insert)
// falls through to "" via ErrNotFound — better to leave the column
// NULL than to fail the dispatch over a vanished issue.
func (s *Store) resolveBaseBranchForIssueID(issueID *int64) (string, error) {
	if issueID == nil {
		return "", nil
	}
	iss, err := s.GetIssueByID(*issueID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	var feat *model.Feature
	if iss.FeatureID != nil {
		feat, err = s.GetFeatureByID(*iss.FeatureID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return "", err
		}
	}
	return model.ResolveBaseBranch(iss, feat), nil
}

func scanDispatch(r rowScanner) (*model.AgentDispatch, error) {
	var (
		d             model.AgentDispatch
		prefix        sql.NullString
		agentID       sql.NullInt64
		agentName     sql.NullString
		issueID       sql.NullInt64
		issueKey      string
		delivered     sql.NullTime
		acked         sql.NullTime
		queuedAfter   sql.NullInt64
		blockersClear int
		baseBranch    sql.NullString
	)
	err := r.Scan(
		&d.ID, &d.RepoID, &prefix, &agentID, &agentName,
		&d.TargetSessionID, &issueID, &issueKey,
		&d.Mode, &d.Payload, &d.Status, &d.CreatedBy, &d.CreatedAt,
		&delivered, &acked, &d.AckNote, &queuedAfter, &blockersClear,
		&baseBranch,
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
	if baseBranch.Valid {
		d.BaseBranch = baseBranch.String
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
// already exists for the parent's issue. Writes no audit row.
//
// BACI-279 retired the user-facing follow-on CLI verbs / REST routes /
// client methods that used to call this; it survives as the in-package
// primitive that seeds the dormant rows the controller follow-on sweep
// (PromoteReadyFollowOns / CancelOrphanedFollowOns) operates on — the
// sweep's tests insert their fixtures through here.
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
// across both variants, no audit row.
//
// Validates: actor / mode parseable + non-empty; repo + issue exist;
// no in-flight dispatch is already on the issue (the parent-acks
// variant is the right fit there); the issue has at least one open
// blocker (no point queueing if the gate is already clear — the matcher
// would bind it on its next tick anyway). Returns the new dormant row.
//
// Like AddFollowOnDispatch, this survived BACI-279's verb retirement as
// the in-package primitive the controller follow-on sweep's tests use
// to seed blockers-clear-variant fixtures.
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
	// on its next tick. Refuse here so the call surfaces a clear "not
	// applicable" error rather than silently writing a row that the
	// very next sweep promotes.
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
	// Direct insert rather than routing through AddDispatch — kept here
	// even after BACI-255 dropped the denormalised waiting_for_claim
	// cache because the dormant variant deliberately stays invisible to
	// the matcher (the chip is the surface, not the spinner). The
	// queued-with-queued_until_blockers_clear=1 row is filtered out of
	// the "active" dispatch lookup in activeDispatchByIssueID (BACI-217),
	// so the spinner stays off until the promote sweep clears the gate.
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

// PromoteReadyFollowOns (BACI-179, BACI-217) is the controller's
// promote sweep for dormant follow-ons whose dormant gate has cleared
// AND whose issue has no open claim held by a live session.
//
// Each ready row clears its dormant flags, leaving a regular queued
// row the matcher will bind next tick. The BACI-195 fire-time
// state-gate is gone (BACI-252): the user gets to dispatch any mode
// from any state via the kanban popup, so the controller no longer
// re-evaluates a per-mode state-gate at promote time. Orphan-cancel
// (issue landed in done/cancelled) still runs in
// CancelOrphanedFollowOns above; user-cancel (chip removed) still
// runs in CancelFollowOnDispatch on the client.
//
// Leader-gated by the caller. Empty slice (no error) when no row was
// ready.
//
// The NOT EXISTS guard prevents a re-claim from racing in between
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
// already.
func (s *Store) PromoteReadyFollowOns() (promoted []*model.AgentDispatch, err error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// SELECT first (inside the tx) so we can return the post-update
	// rows with their queued_after_dispatch_id-was-NULL view to the
	// caller. UPDATE … RETURNING is supported by modernc/sqlite but
	// would still need a second query to hydrate the joined repo /
	// agent / issue columns scanDispatch expects.
	//
	// BACI-217: widen the SELECT to cover both dormant variants
	// (parent-acks and blockers-clear). A row is a candidate iff its
	// status is queued, it carries at least one of the two dormant
	// flags, the dormant gate has cleared, and no live claim is
	// currently racing on the issue. The dormant-gate cleared
	// condition is the inverse of dormantFollowOnGateSQL().
	rows, err := tx.Query(`
		SELECT d.id
		  FROM agent_dispatches d
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
		return nil, err
	}
	var promoteIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		promoteIDs = append(promoteIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(promoteIDs) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
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
	var blockerVariantIDs []int64
	for _, id := range promoteIDs {
		var blockersClear int
		if err := tx.QueryRow(
			`SELECT queued_until_blockers_clear FROM agent_dispatches WHERE id = ?`, id,
		).Scan(&blockersClear); err != nil {
			return nil, err
		}
		if blockersClear != 0 {
			blockerVariantIDs = append(blockerVariantIDs, id)
		}
	}
	if len(blockerVariantIDs) > 0 {
		snaps, err := s.blockerSnapshotsTx(tx, blockerVariantIDs)
		if err != nil {
			return nil, err
		}
		blockerSnapshots = snaps
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
			return nil, err
		}
		// BACI-255: no denormalised waiting_for_claim cache to flip — the
		// promoted dispatch row is now a regular queued dispatch (both
		// dormant flags cleared above), so activeDispatchByIssueID picks
		// it up on the next read and surfaces the spinner from the row's
		// own status. The blockers-clear variant deliberately stayed off
		// the spinner while dormant; the promote sweep above is what
		// makes it eligible, and now the row's own status is the signal.
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	promoted = make([]*model.AgentDispatch, 0, len(promoteIDs))
	for _, id := range promoteIDs {
		d, err := s.GetDispatch(id)
		if err != nil {
			return nil, err
		}
		if snap, ok := blockerSnapshots[id]; ok {
			d.BlockerSnapshot = blockerObservationsToModel(snap)
		}
		promoted = append(promoted, d)
	}
	return promoted, nil
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
