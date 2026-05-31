package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// NormalizeClaudeAgentID is the single normalization seam shared by the write
// side (the worker-start hook that records the binding) and the read side (the
// reverse-proxy recorder that resolves a captured request's X-Claude-Code-Agent-Id
// to a dispatch). The hook field `agent_id` and the HTTP header
// X-Claude-Code-Agent-Id are assumed to be the same identifier modulo
// formatting, so both sides funnel through here rather than comparing raw: it
// lowercases, trims, and strips a leading "agent-" prefix (the worktree-slug
// shaping). Pin the exact transform here once the two forms are verified
// end-to-end; nothing else compares agent ids directly.
func NormalizeClaudeAgentID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "agent-")
	return s
}

// BindSubagentDispatch records that the Claude Code subagent identified by
// claudeAgentID is working dispatchID (under sessionID). The key is normalized
// so the recorder's lookup matches regardless of the header's formatting.
// INSERT OR REPLACE: a subagent re-binding (re-run, or a later, better signal)
// overwrites in place — one binding per subagent. now is passed in so the
// caller controls the clock (tests, and to keep the store time-source-free).
func (s *Store) BindSubagentDispatch(claudeAgentID string, dispatchID int64, sessionID string, now time.Time) error {
	key := NormalizeClaudeAgentID(claudeAgentID)
	if key == "" {
		return nil
	}
	sessionID = clampProxyField(sessionID, 64)
	_, err := s.DB.Exec(`
		INSERT INTO subagent_dispatches (claude_agent_id, dispatch_id, session_id, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(claude_agent_id) DO UPDATE SET
		    dispatch_id = excluded.dispatch_id,
		    session_id  = excluded.session_id,
		    created_at  = excluded.created_at`,
		key, dispatchID, sessionID, now.UTC())
	return err
}

// DispatchIDByClaudeAgentID returns the dispatch a captured request's
// X-Claude-Code-Agent-Id is bound to, or nil when there is no binding (an
// empty/unknown agent id). The key is normalized through the same seam as the
// write side. A missing row is not an error — it just means the binding hasn't
// been recorded (yet), so the recorder falls back to the session's active
// dispatch.
func (s *Store) DispatchIDByClaudeAgentID(claudeAgentID string) (*int64, error) {
	key := NormalizeClaudeAgentID(claudeAgentID)
	if key == "" {
		return nil, nil
	}
	var id int64
	err := s.DB.QueryRow(
		`SELECT dispatch_id FROM subagent_dispatches WHERE claude_agent_id = ?`, key,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}
