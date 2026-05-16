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

// AgentSessionLiveListRetention bounds how long ended sessions remain
// visible in the live "Agents" list. The controlling UI prunes rows
// beyond this window every UILeaderPruneInterval; the 60-day janitor
// (AgentSessionRetention) still runs on store.Open() as the safety net.
// 4h is long enough to see "this agent finished a minute ago" history,
// short enough that a busy day's churn doesn't drown out live activity.
const AgentSessionLiveListRetention = 4 * time.Hour

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
// agent reading its recorded name from .bacio/agents.json" path.
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
	SessionID string
	RepoID    int64
	AgentID   *int64 // nil leaves agent_id NULL (back-compat for callers without identity)
	Actor     string
	Model     string
	Host      string
	Branch    string
	// ClaudePID is the pid of the `claude` process driving this session.
	// Always written — the channel scopes its dispatches by (host,
	// claude_pid), so this needs to be present from the very first
	// SessionStart-stub write or the channel can't queue setup work.
	ClaudePID int64
	// ChannelVersion is the bacio binary version the agent's channel
	// reported. Only written on update when non-empty (so a heartbeat
	// from a caller that doesn't know the version doesn't clobber it).
	ChannelVersion string
	// MarkRegistered flips registered_at to CURRENT_TIMESTAMP. The
	// register tool sets it true; the SessionStart-stub path and
	// heartbeats leave it false (preserving whatever's already there).
	MarkRegistered bool
}

// UpsertAgentSession inserts a new row or refreshes an existing one
// (matched by session_id). last_seen_at is always bumped to now;
// started_at is preserved on update. Mutable fields (actor, model,
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
	// session row too — if the identity for this claude_pid changes
	// mid-session (rename), the next register picks it up. Passing
	// nil leaves the column unchanged on update (COALESCE) so callers
	// that don't supply an agent (e.g. heartbeat) don't clobber it.
	var agentID any
	if in.AgentID != nil {
		agentID = *in.AgentID
	}
	registeredAtInsert := "NULL"
	if in.MarkRegistered {
		registeredAtInsert = "CURRENT_TIMESTAMP"
	}
	registeredAtUpdate := "registered_at"
	if in.MarkRegistered {
		// Don't clobber an earlier registered_at — first-mark-wins so
		// re-runs of register report the original registration moment.
		registeredAtUpdate = "COALESCE(agent_sessions.registered_at, CURRENT_TIMESTAMP)"
	}
	if _, err := tx.Exec(`
		INSERT INTO agent_sessions
		    (session_id, repo_id, agent_id, actor, model, host, branch, claude_pid, channel_version, last_seen_at, registered_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, `+registeredAtInsert+`)
		ON CONFLICT(session_id) DO UPDATE SET
		    actor           = excluded.actor,
		    agent_id        = COALESCE(excluded.agent_id, agent_sessions.agent_id),
		    model           = excluded.model,
		    host            = excluded.host,
		    branch          = excluded.branch,
		    claude_pid      = CASE WHEN excluded.claude_pid != 0 THEN excluded.claude_pid ELSE agent_sessions.claude_pid END,
		    channel_version = CASE WHEN excluded.channel_version != '' THEN excluded.channel_version ELSE agent_sessions.channel_version END,
		    registered_at   = `+registeredAtUpdate+`,
		    last_seen_at    = CURRENT_TIMESTAMP`,
		in.SessionID, in.RepoID, agentID, in.Actor, in.Model, in.Host, in.Branch, in.ClaudePID, in.ChannelVersion,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetAgentSession(in.SessionID)
}

// AssigneeChange describes an issues.assignee mutation that
// AddAgentClaim / ReleaseAgentClaim / EndAgentSession made as a side
// effect of keeping the claim and the assignee in lockstep. The client
// layer turns a *changed* AssigneeChange into an `issue.assign` audit
// row; Old == New means the store looked but nothing moved, so the
// client skips the audit.
type AssigneeChange struct {
	IssueID    int64
	IssueKey   string
	RepoID     int64
	RepoPrefix string
	Old        string
	New        string
}

// Changed reports whether the assignee actually moved.
func (a AssigneeChange) Changed() bool { return a.Old != a.New }

// sessionIdentity resolves the assignee string for a session: the
// linked agent identity slug if the session has one, else the session's
// actor (itself validated at register time). Never the OS user.
func sessionIdentity(tx *sql.Tx, sessPK int64) (string, error) {
	var name sql.NullString
	var actor string
	err := tx.QueryRow(
		`SELECT a.name, s.actor FROM agent_sessions s LEFT JOIN agents a ON a.id = s.agent_id WHERE s.id = ?`,
		sessPK,
	).Scan(&name, &actor)
	if err != nil {
		return "", err
	}
	if name.Valid && name.String != "" {
		return name.String, nil
	}
	return actor, nil
}

// issueAssigneeContext reads the fields the assignee-lockstep helpers
// need inside a tx: the current assignee plus the issue's repo/key for
// the audit row.
func issueAssigneeContext(tx *sql.Tx, issueID int64) (assignee string, repoID int64, prefix string, number int64, err error) {
	err = tx.QueryRow(
		`SELECT i.assignee, i.repo_id, r.prefix, i.number FROM issues i JOIN repos r ON r.id = i.repo_id WHERE i.id = ?`,
		issueID,
	).Scan(&assignee, &repoID, &prefix, &number)
	return
}

// applyClaimAssignee stamps issues.assignee with the claiming identity,
// overwriting whatever was there — claiming is a deliberate ownership
// signal, so last claim wins even over a human-set assignee. Returns the
// before/after for the audit row.
func applyClaimAssignee(tx *sql.Tx, issueID int64, identity string) (*AssigneeChange, error) {
	clean, err := ValidateName(identity, "assignee")
	if err != nil {
		return nil, err
	}
	old, repoID, prefix, number, err := issueAssigneeContext(tx, issueID)
	if err != nil {
		return nil, err
	}
	ch := &AssigneeChange{
		IssueID: issueID, IssueKey: fmt.Sprintf("%s-%d", prefix, number),
		RepoID: repoID, RepoPrefix: prefix, Old: old, New: clean,
	}
	if old == clean {
		return ch, nil // already assigned to this identity — nothing to write
	}
	if _, err := tx.Exec(
		`UPDATE issues SET assignee = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		clean, issueID,
	); err != nil {
		return nil, err
	}
	return ch, nil
}

// clearAssigneeIfOwned clears issues.assignee iff (a) the issue has no
// remaining open claims and (b) the current assignee still equals the
// releasing identity — so a human's deliberate reassignment after the
// claim is preserved. Must run *after* the releasing claim is stamped so
// the open-claim count is accurate. Returns a changed AssigneeChange
// only when it actually cleared the field.
func clearAssigneeIfOwned(tx *sql.Tx, issueID int64, identity string) (*AssigneeChange, error) {
	var openClaims int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM agent_claims WHERE issue_id = ? AND released_at IS NULL`,
		issueID,
	).Scan(&openClaims); err != nil {
		return nil, err
	}
	old, repoID, prefix, number, err := issueAssigneeContext(tx, issueID)
	if err != nil {
		return nil, err
	}
	ch := &AssigneeChange{
		IssueID: issueID, IssueKey: fmt.Sprintf("%s-%d", prefix, number),
		RepoID: repoID, RepoPrefix: prefix, Old: old, New: old,
	}
	if openClaims > 0 || old == "" || old != identity {
		return ch, nil // claims remain, nothing to clear, or a human owns it now
	}
	if _, err := tx.Exec(
		`UPDATE issues SET assignee = '', updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		issueID,
	); err != nil {
		return nil, err
	}
	ch.New = ""
	return ch, nil
}

// EndAgentSession stamps ended_at + end_reason, and auto-releases every
// open claim for that session. Idempotent: ending an already-ended
// session is a no-op (returns the row as-is) so a Stop hook firing twice
// doesn't error. Each issue left with no open claims is also unassigned
// (subject to clearAssigneeIfOwned's guard) — the returned slice carries
// one entry per issue whose assignee actually changed, for the audit log.
func (s *Store) EndAgentSession(sessionID, reason string) (*model.AgentSession, []AssigneeChange, error) {
	if _, err := ValidateSessionID(sessionID); err != nil {
		return nil, nil, err
	}
	parsed, err := model.ParseEndReason(reason)
	if err != nil {
		return nil, nil, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, nil, err
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
		sess, err := s.GetAgentSession(sessionID)
		return sess, nil, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, err
	}

	var sessPK int64
	if err := tx.QueryRow(`SELECT id FROM agent_sessions WHERE session_id = ?`, sessionID).Scan(&sessPK); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}

	// Capture the issues this session is about to auto-release *before*
	// the bulk update, so the post-release assignee sweep knows which
	// issues to revisit.
	claimedRows, err := tx.Query(
		`SELECT DISTINCT issue_id FROM agent_claims WHERE released_at IS NULL AND session_pk = ?`, sessPK,
	)
	if err != nil {
		return nil, nil, err
	}
	var issueIDs []int64
	for claimedRows.Next() {
		var id int64
		if err := claimedRows.Scan(&id); err != nil {
			claimedRows.Close()
			return nil, nil, err
		}
		issueIDs = append(issueIDs, id)
	}
	if err := claimedRows.Err(); err != nil {
		claimedRows.Close()
		return nil, nil, err
	}
	claimedRows.Close()

	res, err := tx.Exec(
		`UPDATE agent_sessions SET ended_at = CURRENT_TIMESTAMP, end_reason = ? WHERE session_id = ? AND ended_at IS NULL`,
		string(parsed), sessionID,
	)
	if err != nil {
		return nil, nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, nil, ErrNotFound
	}
	if _, err := tx.Exec(
		`UPDATE agent_claims SET released_at = CURRENT_TIMESTAMP WHERE released_at IS NULL AND session_pk = ?`,
		sessPK,
	); err != nil {
		return nil, nil, err
	}

	identity, err := sessionIdentity(tx, sessPK)
	if err != nil {
		return nil, nil, err
	}
	var changes []AssigneeChange
	for _, issueID := range issueIDs {
		ch, err := clearAssigneeIfOwned(tx, issueID, identity)
		if err != nil {
			return nil, nil, err
		}
		if ch.Changed() {
			changes = append(changes, *ch)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	sess, err := s.GetAgentSession(sessionID)
	return sess, changes, err
}

// AddAgentClaim records a new claim. Rejects if the session is already
// ended (an ended agent has no business claiming new work). Allows
// multiple concurrent open claims by different sessions on the same
// issue (pairing/review). A session re-claiming an issue it already
// has open is a no-op (returns the existing claim with created=false) —
// except that a non-empty prompt on the re-claim overwrites the stored
// prompt in place, so a re-claim with a fresher instruction is
// recorded without flooding the audit log.
// The `created` flag lets callers (the local client) skip writing an
// audit row for no-op re-claims — otherwise a poll loop would flood
// the history table with duplicate `agent.claim` entries.
//
// A freshly-created claim also stamps issues.assignee with the claiming
// identity (last claim wins, even over a human-set assignee) inside the
// same transaction, so the claim and the assignee can never drift. The
// returned *AssigneeChange describes that mutation for the audit log; it
// is nil on the no-op re-claim path.
func (s *Store) AddAgentClaim(sessionID string, issueID int64, prompt string) (*model.AgentClaim, bool, *AssigneeChange, error) {
	if _, err := ValidateSessionID(sessionID); err != nil {
		return nil, false, nil, err
	}
	prompt, err := ValidateBody(prompt, "prompt", false)
	if err != nil {
		return nil, false, nil, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, false, nil, err
	}
	defer tx.Rollback()

	var sessPK int64
	var ended sql.NullTime
	err = tx.QueryRow(`SELECT id, ended_at FROM agent_sessions WHERE session_id = ?`, sessionID).Scan(&sessPK, &ended)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil, fmt.Errorf("session %q is not registered", sessionID)
	}
	if err != nil {
		return nil, false, nil, err
	}
	if ended.Valid {
		return nil, false, nil, fmt.Errorf("session %q is already ended; cannot claim", sessionID)
	}

	var existing int64
	err = tx.QueryRow(`SELECT id FROM agent_claims WHERE session_pk = ? AND issue_id = ? AND released_at IS NULL`, sessPK, issueID).Scan(&existing)
	if err == nil {
		// No-op re-claim. If the caller supplied a fresher prompt, update
		// it in place — the claim row still counts as "not created" so the
		// audit log isn't flooded by a poll loop. Ownership is unchanged,
		// so the assignee is left alone (nil AssigneeChange).
		if prompt != "" {
			if _, err := tx.Exec(`UPDATE agent_claims SET prompt = ? WHERE id = ?`, prompt, existing); err != nil {
				return nil, false, nil, err
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, false, nil, err
		}
		c, err := s.getAgentClaimByID(existing)
		return c, false, nil, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil, err
	}

	res, err := tx.Exec(
		`INSERT INTO agent_claims (session_pk, issue_id, prompt) VALUES (?, ?, ?)`,
		sessPK, issueID, prompt,
	)
	if err != nil {
		return nil, false, nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, false, nil, err
	}
	if _, err := tx.Exec(
		`UPDATE agent_sessions SET last_seen_at = CURRENT_TIMESTAMP WHERE id = ?`, sessPK,
	); err != nil {
		return nil, false, nil, err
	}
	identity, err := sessionIdentity(tx, sessPK)
	if err != nil {
		return nil, false, nil, err
	}
	change, err := applyClaimAssignee(tx, issueID, identity)
	if err != nil {
		return nil, false, nil, err
	}
	// A fresh open claim clears the issue's waiting_for_claim flag — an
	// agent has picked the work up, so the dispatch→claim gap is closed.
	// Only on this new-claim path, not the no-op re-claim above.
	if _, err := tx.Exec(
		`UPDATE issues SET waiting_for_claim = 0 WHERE id = ?`, issueID,
	); err != nil {
		return nil, false, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, nil, err
	}
	c, err := s.getAgentClaimByID(id)
	return c, true, change, err
}

// ReleaseAgentClaim stamps released_at on the latest open claim for
// (session, issue). Returns ErrNotFound if no open claim exists.
//
// Once the claim is released, the issue is unassigned if it has no
// remaining open claims and its assignee still equals the releasing
// identity (clearAssigneeIfOwned's guard preserves a human's deliberate
// reassignment). The returned *AssigneeChange describes that mutation
// for the audit log — Old == New when nothing was cleared.
func (s *Store) ReleaseAgentClaim(sessionID string, issueID int64) (*model.AgentClaim, *AssigneeChange, error) {
	if _, err := ValidateSessionID(sessionID); err != nil {
		return nil, nil, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	var sessPK int64
	err = tx.QueryRow(`SELECT id FROM agent_sessions WHERE session_id = ?`, sessionID).Scan(&sessPK)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("session %q is not registered", sessionID)
	}
	if err != nil {
		return nil, nil, err
	}

	var claimID int64
	err = tx.QueryRow(
		`SELECT id FROM agent_claims WHERE session_pk = ? AND issue_id = ? AND released_at IS NULL ORDER BY claimed_at DESC LIMIT 1`,
		sessPK, issueID,
	).Scan(&claimID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(`UPDATE agent_claims SET released_at = CURRENT_TIMESTAMP WHERE id = ?`, claimID); err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(`UPDATE agent_sessions SET last_seen_at = CURRENT_TIMESTAMP WHERE id = ?`, sessPK); err != nil {
		return nil, nil, err
	}
	identity, err := sessionIdentity(tx, sessPK)
	if err != nil {
		return nil, nil, err
	}
	change, err := clearAssigneeIfOwned(tx, issueID, identity)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	claim, err := s.getAgentClaimByID(claimID)
	return claim, change, err
}

// AgentSessionFilter scopes ListAgentSessions. Zero value = all
// sessions in all repos.
type AgentSessionFilter struct {
	RepoID         *int64    // nil = all repos
	OnlyAlive      bool      // ended_at IS NULL
	RegisteredOnly bool      // registered_at IS NOT NULL — hide SessionStart stubs that never made it through register
	Since          time.Time // last_seen_at >= since (zero = no filter)
}

// ListAgentSessions returns sessions matching the filter, ordered by
// last_seen_at DESC so the freshest activity surfaces first.
func (s *Store) ListAgentSessions(f AgentSessionFilter) ([]*model.AgentSession, error) {
	q := `SELECT ` + agentSessionSelect + `
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
	if f.RegisteredOnly {
		q += ` AND s.registered_at IS NOT NULL`
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

// SessionsByClaudePID returns the open sessions (ended_at IS NULL)
// matching the given (host, claude_pid) — the channel's own coordinates.
// The bacio channel MCP server doesn't know its session id (Claude Code
// doesn't pass it), so it uses this to find which sessions it's serving
// and target setup dispatches at them. claudePID of 0 returns nothing
// (the channel couldn't walk to its `claude` ancestor — no correlation
// possible). Newest-first by started_at.
func (s *Store) SessionsByClaudePID(host string, claudePID int64) ([]*model.AgentSession, error) {
	if claudePID == 0 {
		return nil, nil
	}
	rows, err := s.DB.Query(
		`SELECT `+agentSessionSelect+`
		FROM agent_sessions s
		LEFT JOIN repos r  ON r.id = s.repo_id
		LEFT JOIN agents a ON a.id = s.agent_id
		WHERE s.host = ? AND s.claude_pid = ? AND s.ended_at IS NULL
		ORDER BY s.started_at DESC`,
		host, claudePID,
	)
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
		`SELECT `+agentSessionSelect+`
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
		`SELECT `+agentSessionSelect+`
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
		`SELECT c.id, c.session_pk, s.session_id, a.name, c.issue_id, r.prefix || '-' || i.number, c.prompt, c.claimed_at, c.released_at
		FROM agent_claims c
		JOIN agent_sessions s ON s.id = c.session_pk
		LEFT JOIN agents a ON a.id = s.agent_id
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
		c, err := scanAgentClaim(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListClaimsForIssue returns every claim against an issue — open and
// released — newest claim first, joined to the session id and the
// agent identity slug behind it. This is the "session list against
// each issue": the history of who has worked the issue and the prompt
// they ran.
func (s *Store) ListClaimsForIssue(issueID int64) ([]*model.AgentClaim, error) {
	rows, err := s.DB.Query(
		`SELECT c.id, c.session_pk, s.session_id, a.name, c.issue_id, r.prefix || '-' || i.number, c.prompt, c.claimed_at, c.released_at
		FROM agent_claims c
		JOIN agent_sessions s ON s.id = c.session_pk
		LEFT JOIN agents a ON a.id = s.agent_id
		JOIN issues i ON i.id = c.issue_id
		JOIN repos r ON r.id = i.repo_id
		WHERE c.issue_id = ? ORDER BY c.claimed_at DESC`, issueID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.AgentClaim
	for rows.Next() {
		c, err := scanAgentClaim(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// OpenClaimsBySession returns the open (unreleased) claims for every
// alive session in a repo, keyed by session PK. Lets the TUI/desktop
// agent views and dispatch pickers compute "busy" for a whole repo in
// one query instead of an N+1 ListAgentClaims loop. Claims held by
// ended sessions are excluded — an ended session isn't busy.
func (s *Store) OpenClaimsBySession(repoID int64) (map[int64][]*model.AgentClaim, error) {
	rows, err := s.DB.Query(
		`SELECT c.id, c.session_pk, s.session_id, a.name, c.issue_id, r.prefix || '-' || i.number, c.prompt, c.claimed_at, c.released_at
		FROM agent_claims c
		JOIN agent_sessions s ON s.id = c.session_pk
		LEFT JOIN agents a ON a.id = s.agent_id
		JOIN issues i ON i.id = c.issue_id
		JOIN repos r ON r.id = i.repo_id
		WHERE c.released_at IS NULL AND s.ended_at IS NULL AND s.repo_id = ?
		ORDER BY c.claimed_at DESC`, repoID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64][]*model.AgentClaim)
	for rows.Next() {
		c, err := scanAgentClaim(rows)
		if err != nil {
			return nil, err
		}
		out[c.SessionPK] = append(out[c.SessionPK], c)
	}
	return out, rows.Err()
}

// PruneEndedAgentSessions deletes ended sessions whose ended_at is
// older than retention and returns the row count. Active rows
// (ended_at IS NULL) are never touched. Shared by the store.Open()
// 60-day janitor (AgentSessionRetention) and the controller-UI
// 5-minute live-list prune (AgentSessionLiveListRetention) — same SQL,
// only the retention argument differs. `agent_claims` rows referencing
// pruned sessions are removed transitively by the FK's ON DELETE
// CASCADE; `agent_dispatches.target_session_id` is a free-form text
// column (not a FK), so dispatches survive — left to pruneDispatches.
func (s *Store) PruneEndedAgentSessions(retention time.Duration) (int64, error) {
	cutoff := time.Now().Add(-retention).UTC().Format("2006-01-02 15:04:05")
	res, err := s.DB.Exec(`DELETE FROM agent_sessions WHERE ended_at IS NOT NULL AND ended_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// pruneAgentSessions is the thin shim store.Open() uses to invoke the
// exported PruneEndedAgentSessions at startup. Keeping the package-
// private name avoids touching the Open() call site every time the
// implementation moves.
func pruneAgentSessions(db *sql.DB, retention time.Duration) error {
	_, err := (&Store{DB: db}).PruneEndedAgentSessions(retention)
	return err
}

func (s *Store) getAgentClaimByID(id int64) (*model.AgentClaim, error) {
	row := s.DB.QueryRow(
		`SELECT c.id, c.session_pk, s.session_id, a.name, c.issue_id, r.prefix || '-' || i.number, c.prompt, c.claimed_at, c.released_at
		FROM agent_claims c
		JOIN agent_sessions s ON s.id = c.session_pk
		LEFT JOIN agents a ON a.id = s.agent_id
		JOIN issues i ON i.id = c.issue_id
		JOIN repos r ON r.id = i.repo_id
		WHERE c.id = ?`, id,
	)
	c, err := scanAgentClaim(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// scanAgentClaim reads one agent_claims row in the canonical column
// order shared by every claim query: id, session_pk, session_id,
// agent_name, issue_id, issue_key, prompt, claimed_at, released_at.
func scanAgentClaim(r rowScanner) (*model.AgentClaim, error) {
	var c model.AgentClaim
	var agentName sql.NullString
	var rel sql.NullTime
	if err := r.Scan(&c.ID, &c.SessionPK, &c.SessionID, &agentName, &c.IssueID, &c.IssueKey, &c.Prompt, &c.ClaimedAt, &rel); err != nil {
		return nil, err
	}
	if agentName.Valid {
		c.AgentName = agentName.String
	}
	if rel.Valid {
		c.ReleasedAt = &rel.Time
	}
	return &c, nil
}

// agentSessionSelect is the canonical column list for selecting an
// AgentSession joined to its repo prefix + agent name. Centralised so
// new columns (registered_at, channel_version) only need to land in
// one place; scanAgentSession's column order mirrors it 1:1.
const agentSessionSelect = `s.id, s.session_id, s.repo_id, r.prefix, s.agent_id, a.name, s.actor, s.model,
	s.host, s.branch, s.started_at, s.last_seen_at, s.ended_at, s.end_reason,
	s.claude_pid, s.channel_seen_at, s.registered_at, s.channel_version`

func scanAgentSession(r rowScanner) (*model.AgentSession, error) {
	var ag model.AgentSession
	var prefix sql.NullString
	var agentID sql.NullInt64
	var agentName sql.NullString
	var ended sql.NullTime
	var channelSeen sql.NullTime
	var registered sql.NullTime
	err := r.Scan(&ag.ID, &ag.SessionID, &ag.RepoID, &prefix, &agentID, &agentName,
		&ag.Actor, &ag.Model,
		&ag.Host, &ag.Branch, &ag.StartedAt, &ag.LastSeenAt, &ended, &ag.EndReason,
		&ag.ClaudePID, &channelSeen, &registered, &ag.ChannelVersion)
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
	if channelSeen.Valid {
		ag.ChannelSeenAt = &channelSeen.Time
	}
	if registered.Valid {
		ag.RegisteredAt = &registered.Time
	}
	return &ag, nil
}

// validateOptionalName accepts empty (keep default-empty) or runs the
// usual single-line rules. Used for the optional session fields
// (model, host, branch) where "" is meaningful.
func validateOptionalName(s, field string) (string, error) {
	if s == "" {
		return "", nil
	}
	return validateSingleLine(s, field, maxNameLen, true)
}
