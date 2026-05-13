package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// AgentSessionRetention bounds how long ended sessions are kept. Active
// rows (ended_at IS NULL) never expire. Mirrors HistoryRetention so the
// two prune passes share the same "what does bacio remember" window.
const AgentSessionRetention = 60 * 24 * time.Hour

// ErrAgentNameTaken signals that UpsertAgent was called with
// requireNew=true on a name that's already in use. Callers translate
// this into a "name taken, pick another" hint so the agent retries
// with a different slug.
var ErrAgentNameTaken = errors.New("agent name already taken")

// UpsertAgent inserts a new agent row or refreshes an existing one
// (matched by name). When requireNew is true, an existing name is a
// hard error (ErrAgentNameTaken) — that's the "I'm claiming this
// fresh, fail if it clashes" path the SKILL.md bootstrap uses on first
// session in a repo. When requireNew is false, an existing name is a
// no-op refresh (last_seen_at bumped) — that's the "I'm a returning
// agent reading my saved name from .bacio/agent" path.
//
// Returns the row plus a created flag so callers can decide whether to
// record an audit event for a fresh creation vs. a routine refresh.
func (s *Store) UpsertAgent(name string, requireNew bool) (*model.Agent, bool, error) {
	name, err := ValidateAgentName(name)
	if err != nil {
		return nil, false, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	var existingID int64
	err = tx.QueryRow(`SELECT id FROM agents WHERE name = ?`, name).Scan(&existingID)
	switch {
	case err == nil:
		if requireNew {
			return nil, false, ErrAgentNameTaken
		}
		if _, err := tx.Exec(`UPDATE agents SET last_seen_at = CURRENT_TIMESTAMP WHERE id = ?`, existingID); err != nil {
			return nil, false, err
		}
		if err := tx.Commit(); err != nil {
			return nil, false, err
		}
		ag, err := s.GetAgentByID(existingID)
		return ag, false, err
	case errors.Is(err, sql.ErrNoRows):
		// fresh insert below
	default:
		return nil, false, err
	}
	res, err := tx.Exec(`INSERT INTO agents (name) VALUES (?)`, name)
	if err != nil {
		return nil, false, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	ag, err := s.GetAgentByID(id)
	return ag, true, err
}

// GetAgentByName returns the agent identified by name, or ErrNotFound.
func (s *Store) GetAgentByName(name string) (*model.Agent, error) {
	row := s.DB.QueryRow(`SELECT id, name, created_at, last_seen_at FROM agents WHERE name = ?`, name)
	var ag model.Agent
	if err := row.Scan(&ag.ID, &ag.Name, &ag.CreatedAt, &ag.LastSeenAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &ag, nil
}

// GetAgentByID returns the agent identified by primary key, or
// ErrNotFound. Used by callers that already resolved name → id once
// and want to re-fetch the canonical row (e.g. after Upsert).
func (s *Store) GetAgentByID(id int64) (*model.Agent, error) {
	row := s.DB.QueryRow(`SELECT id, name, created_at, last_seen_at FROM agents WHERE id = ?`, id)
	var ag model.Agent
	if err := row.Scan(&ag.ID, &ag.Name, &ag.CreatedAt, &ag.LastSeenAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &ag, nil
}

// UpsertAgentSessionIn is the validated tuple UpsertAgentSession consumes.
// Mutable fields update on every call (so a heartbeat after a /model
// switch picks up the new model); started_at is set only on insert.
type UpsertAgentSessionIn struct {
	SessionID      string
	RepoID         int64
	AgentID        *int64 // nil leaves agent_id NULL (back-compat for callers without identity)
	Actor          string
	Model          string
	PermissionMode string
	Host           string
	Branch         string
}

// UpsertAgentSession inserts a new row or refreshes an existing one
// (matched by session_id). last_seen_at is always bumped to now;
// started_at is preserved on update. Mutable fields (actor, model, mode,
// host, branch) are overwritten on every call so a session that's
// `/model`-switched or `cd`-ed mid-life shows current state.
//
// Returns the row as it sits after the write.
func (s *Store) UpsertAgentSession(in UpsertAgentSessionIn) (*model.AgentSession, error) {
	if _, err := ValidateSessionID(in.SessionID); err != nil {
		return nil, err
	}
	actor, err := ValidateActor(in.Actor)
	if err != nil {
		return nil, err
	}
	in.Actor = actor
	if in.Model, err = validateOptionalName(in.Model, "model"); err != nil {
		return nil, err
	}
	if in.PermissionMode, err = validateOptionalName(in.PermissionMode, "permission_mode"); err != nil {
		return nil, err
	}
	if in.Host, err = validateOptionalName(in.Host, "host"); err != nil {
		return nil, err
	}
	if in.Branch, err = validateOptionalName(in.Branch, "branch"); err != nil {
		return nil, err
	}

	// Guard against re-registering an ended session inside the same
	// transaction as the write — the ON CONFLICT … WHERE clause silently
	// no-ops the conflict resolution when the row is already ended,
	// which would otherwise hide the precondition failure behind a
	// post-fetch check.
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var existingEnded sql.NullTime
	var existingReason sql.NullString
	err = tx.QueryRow(
		`SELECT ended_at, end_reason FROM agent_sessions WHERE session_id = ?`,
		in.SessionID,
	).Scan(&existingEnded, &existingReason)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if existingEnded.Valid {
		return nil, fmt.Errorf("session %q is already ended (reason=%s); start a new session", in.SessionID, existingReason.String)
	}

	// INSERT … ON CONFLICT: update mutable fields and bump last_seen_at.
	// The ended-session case is already filtered above, so this path
	// only runs for new or alive rows. agent_id is mutable on the
	// session row too — if the agent rewrites .bacio/agent mid-session
	// (rename), the next register picks up the new identity. Passing
	// nil leaves the column unchanged on update (COALESCE) so callers
	// that don't supply an agent (e.g. heartbeat) don't clobber it.
	var agentID any
	if in.AgentID != nil {
		agentID = *in.AgentID
	}
	if _, err := tx.Exec(`
		INSERT INTO agent_sessions
		    (session_id, repo_id, agent_id, actor, model, permission_mode, host, branch, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(session_id) DO UPDATE SET
		    actor           = excluded.actor,
		    agent_id        = COALESCE(excluded.agent_id, agent_sessions.agent_id),
		    model           = excluded.model,
		    permission_mode = excluded.permission_mode,
		    host            = excluded.host,
		    branch          = excluded.branch,
		    last_seen_at    = CURRENT_TIMESTAMP`,
		in.SessionID, in.RepoID, agentID, in.Actor, in.Model, in.PermissionMode, in.Host, in.Branch,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetAgentSession(in.SessionID)
}

// EndAgentSession stamps ended_at + end_reason, and auto-releases every
// open claim for that session. Idempotent: ending an already-ended
// session is a no-op (returns the row as-is) so a Stop hook firing twice
// doesn't error.
func (s *Store) EndAgentSession(sessionID, reason string) (*model.AgentSession, error) {
	if _, err := ValidateSessionID(sessionID); err != nil {
		return nil, err
	}
	parsed, err := model.ParseEndReason(reason)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var pk int64
	var alreadyEnded sql.NullString
	err = tx.QueryRow(`SELECT id, end_reason FROM agent_sessions WHERE session_id = ? AND ended_at IS NOT NULL`, sessionID).Scan(&pk, &alreadyEnded)
	if err == nil {
		// Already ended — idempotent path. The transaction did nothing
		// write-worthy, so roll it back (the defer would do this anyway,
		// but spelling it out keeps the "no commit happens here" intent
		// obvious to future readers).
		_ = tx.Rollback()
		return s.GetAgentSession(sessionID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	res, err := tx.Exec(
		`UPDATE agent_sessions SET ended_at = CURRENT_TIMESTAMP, end_reason = ? WHERE session_id = ? AND ended_at IS NULL`,
		string(parsed), sessionID,
	)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	if _, err := tx.Exec(
		`UPDATE agent_claims SET released_at = CURRENT_TIMESTAMP WHERE released_at IS NULL AND session_pk IN (SELECT id FROM agent_sessions WHERE session_id = ?)`,
		sessionID,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetAgentSession(sessionID)
}

// AddAgentClaim records a new claim. Rejects if the session is already
// ended (an ended agent has no business claiming new work). Allows
// multiple concurrent open claims by different sessions on the same
// issue (pairing/review). A session re-claiming an issue it already
// has open is a no-op (returns the existing claim with created=false).
// The `created` flag lets callers (the local client) skip writing an
// audit row for no-op re-claims — otherwise a poll loop would flood
// the history table with duplicate `agent.claim` entries.
func (s *Store) AddAgentClaim(sessionID string, issueID int64) (*model.AgentClaim, bool, error) {
	if _, err := ValidateSessionID(sessionID); err != nil {
		return nil, false, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	var sessPK int64
	var ended sql.NullTime
	err = tx.QueryRow(`SELECT id, ended_at FROM agent_sessions WHERE session_id = ?`, sessionID).Scan(&sessPK, &ended)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, fmt.Errorf("session %q is not registered", sessionID)
	}
	if err != nil {
		return nil, false, err
	}
	if ended.Valid {
		return nil, false, fmt.Errorf("session %q is already ended; cannot claim", sessionID)
	}

	var existing int64
	err = tx.QueryRow(`SELECT id FROM agent_claims WHERE session_pk = ? AND issue_id = ? AND released_at IS NULL`, sessPK, issueID).Scan(&existing)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return nil, false, err
		}
		c, err := s.getAgentClaimByID(existing)
		return c, false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}

	res, err := tx.Exec(
		`INSERT INTO agent_claims (session_pk, issue_id) VALUES (?, ?)`,
		sessPK, issueID,
	)
	if err != nil {
		return nil, false, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(
		`UPDATE agent_sessions SET last_seen_at = CURRENT_TIMESTAMP WHERE id = ?`, sessPK,
	); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	c, err := s.getAgentClaimByID(id)
	return c, true, err
}

// ReleaseAgentClaim stamps released_at on the latest open claim for
// (session, issue). Returns ErrNotFound if no open claim exists.
func (s *Store) ReleaseAgentClaim(sessionID string, issueID int64) (*model.AgentClaim, error) {
	if _, err := ValidateSessionID(sessionID); err != nil {
		return nil, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var sessPK int64
	err = tx.QueryRow(`SELECT id FROM agent_sessions WHERE session_id = ?`, sessionID).Scan(&sessPK)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("session %q is not registered", sessionID)
	}
	if err != nil {
		return nil, err
	}

	var claimID int64
	err = tx.QueryRow(
		`SELECT id FROM agent_claims WHERE session_pk = ? AND issue_id = ? AND released_at IS NULL ORDER BY claimed_at DESC LIMIT 1`,
		sessPK, issueID,
	).Scan(&claimID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE agent_claims SET released_at = CURRENT_TIMESTAMP WHERE id = ?`, claimID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE agent_sessions SET last_seen_at = CURRENT_TIMESTAMP WHERE id = ?`, sessPK); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.getAgentClaimByID(claimID)
}

// AgentSessionFilter scopes ListAgentSessions. Zero value = all
// sessions in all repos.
type AgentSessionFilter struct {
	RepoID    *int64    // nil = all repos
	OnlyAlive bool      // ended_at IS NULL
	Since     time.Time // last_seen_at >= since (zero = no filter)
}

// ListAgentSessions returns sessions matching the filter, ordered by
// last_seen_at DESC so the freshest activity surfaces first.
func (s *Store) ListAgentSessions(f AgentSessionFilter) ([]*model.AgentSession, error) {
	q := `SELECT s.id, s.session_id, s.repo_id, r.prefix, s.agent_id, a.name, s.actor, s.model, s.permission_mode, s.host, s.branch,
		s.started_at, s.last_seen_at, s.ended_at, s.end_reason
		FROM agent_sessions s
		LEFT JOIN repos r  ON r.id = s.repo_id
		LEFT JOIN agents a ON a.id = s.agent_id
		WHERE 1=1`
	var args []any
	if f.RepoID != nil {
		q += ` AND s.repo_id = ?`
		args = append(args, *f.RepoID)
	}
	if f.OnlyAlive {
		q += ` AND s.ended_at IS NULL`
	}
	if !f.Since.IsZero() {
		q += ` AND s.last_seen_at >= ?`
		args = append(args, f.Since.UTC().Format("2006-01-02 15:04:05"))
	}
	q += ` ORDER BY s.last_seen_at DESC`

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.AgentSession
	for rows.Next() {
		ag, err := scanAgentSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ag)
	}
	return out, rows.Err()
}

// GetAgentSession fetches one session by its external id.
func (s *Store) GetAgentSession(sessionID string) (*model.AgentSession, error) {
	row := s.DB.QueryRow(
		`SELECT s.id, s.session_id, s.repo_id, r.prefix, s.agent_id, a.name, s.actor, s.model, s.permission_mode, s.host, s.branch,
		s.started_at, s.last_seen_at, s.ended_at, s.end_reason
		FROM agent_sessions s
		LEFT JOIN repos r  ON r.id = s.repo_id
		LEFT JOIN agents a ON a.id = s.agent_id
		WHERE s.session_id = ?`, sessionID,
	)
	ag, err := scanAgentSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return ag, err
}

// ResolveAgentSession looks up a session by exact id first, then by
// unique-prefix match (git-style). Lets `bacio agent show` accept the
// short ids that `bacio agent list` prints without forcing the user
// to copy the full UUID. Returns ErrNotFound if no match, or a
// "matches N sessions" error on ambiguous prefixes.
func (s *Store) ResolveAgentSession(idOrPrefix string) (*model.AgentSession, error) {
	if sess, err := s.GetAgentSession(idOrPrefix); err == nil {
		return sess, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	// Try prefix match. SQLite's LIKE with no wildcard chars in the
	// supplied prefix is safe — we escape with a leading-literal pattern.
	pattern := idOrPrefix + "%"
	rows, err := s.DB.Query(
		`SELECT s.id, s.session_id, s.repo_id, r.prefix, s.agent_id, a.name, s.actor, s.model, s.permission_mode, s.host, s.branch,
		s.started_at, s.last_seen_at, s.ended_at, s.end_reason
		FROM agent_sessions s
		LEFT JOIN repos r  ON r.id = s.repo_id
		LEFT JOIN agents a ON a.id = s.agent_id
		WHERE s.session_id LIKE ? ORDER BY s.last_seen_at DESC LIMIT 2`, pattern,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var matches []*model.AgentSession
	for rows.Next() {
		ag, err := scanAgentSession(rows)
		if err != nil {
			return nil, err
		}
		matches = append(matches, ag)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch len(matches) {
	case 0:
		return nil, ErrNotFound
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("session prefix %q is ambiguous (matches at least 2 sessions); pass more characters or the full id", idOrPrefix)
	}
}

// ListAgentClaims returns every claim attached to a session (open and
// closed), claimed_at DESC. issue_id is joined back to issues so the
// caller can render the canonical PREFIX-N key without a second query.
func (s *Store) ListAgentClaims(sessionPK int64) ([]*model.AgentClaim, error) {
	rows, err := s.DB.Query(
		`SELECT c.id, c.session_pk, s.session_id, c.issue_id, r.prefix || '-' || i.number, c.claimed_at, c.released_at
		FROM agent_claims c
		JOIN agent_sessions s ON s.id = c.session_pk
		JOIN issues i ON i.id = c.issue_id
		JOIN repos r ON r.id = i.repo_id
		WHERE c.session_pk = ? ORDER BY c.claimed_at DESC`, sessionPK,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.AgentClaim
	for rows.Next() {
		var c model.AgentClaim
		var rel sql.NullTime
		if err := rows.Scan(&c.ID, &c.SessionPK, &c.SessionID, &c.IssueID, &c.IssueKey, &c.ClaimedAt, &rel); err != nil {
			return nil, err
		}
		if rel.Valid {
			c.ReleasedAt = &rel.Time
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// pruneAgentSessions drops ended sessions whose ended_at is older than
// retention. Active rows (ended_at IS NULL) are never touched.
func pruneAgentSessions(db *sql.DB, retention time.Duration) error {
	cutoff := time.Now().Add(-retention).UTC().Format("2006-01-02 15:04:05")
	_, err := db.Exec(`DELETE FROM agent_sessions WHERE ended_at IS NOT NULL AND ended_at < ?`, cutoff)
	return err
}

func (s *Store) getAgentClaimByID(id int64) (*model.AgentClaim, error) {
	var c model.AgentClaim
	var rel sql.NullTime
	err := s.DB.QueryRow(
		`SELECT c.id, c.session_pk, s.session_id, c.issue_id, r.prefix || '-' || i.number, c.claimed_at, c.released_at
		FROM agent_claims c
		JOIN agent_sessions s ON s.id = c.session_pk
		JOIN issues i ON i.id = c.issue_id
		JOIN repos r ON r.id = i.repo_id
		WHERE c.id = ?`, id,
	).Scan(&c.ID, &c.SessionPK, &c.SessionID, &c.IssueID, &c.IssueKey, &c.ClaimedAt, &rel)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if rel.Valid {
		c.ReleasedAt = &rel.Time
	}
	return &c, nil
}

func scanAgentSession(r rowScanner) (*model.AgentSession, error) {
	var ag model.AgentSession
	var prefix sql.NullString
	var agentID sql.NullInt64
	var agentName sql.NullString
	var ended sql.NullTime
	err := r.Scan(&ag.ID, &ag.SessionID, &ag.RepoID, &prefix, &agentID, &agentName,
		&ag.Actor, &ag.Model, &ag.PermissionMode,
		&ag.Host, &ag.Branch, &ag.StartedAt, &ag.LastSeenAt, &ended, &ag.EndReason)
	if err != nil {
		return nil, err
	}
	if prefix.Valid {
		ag.RepoPrefix = prefix.String
	}
	if agentID.Valid {
		v := agentID.Int64
		ag.AgentID = &v
	}
	if agentName.Valid {
		ag.AgentName = agentName.String
	}
	if ended.Valid {
		ag.EndedAt = &ended.Time
	}
	return &ag, nil
}

// validateOptionalName accepts empty (keep default-empty) or runs the
// usual single-line rules. Used for the optional session fields
// (model, permission_mode, host, branch) where "" is meaningful.
func validateOptionalName(s, field string) (string, error) {
	if s == "" {
		return "", nil
	}
	return validateSingleLine(s, field, maxNameLen, true)
}
