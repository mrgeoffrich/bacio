package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// SessionIssuePair targets one (session, issue) bucket in
// ListTodosBySessionsAndIssue. issueKey "" matches orphan rows the
// hook couldn't attribute to a single open claim — kept addressable
// so an explicit lookup still works.
type SessionIssuePair struct {
	SessionID string
	IssueKey  string
}

// UpsertSessionTodoFromTask records (or updates) one row from a Claude
// Code Task* tool call. The post-tool-use hook drives this on both
// TaskCreate (new row, status=pending) and TaskUpdate (update the row
// keyed by task_id, preserve position so the agent view's insertion
// order is stable). The session is resolved by external session_id;
// an unknown session_id returns ErrNotFound and the hook log-and-drops.
//
// content="" is treated as "don't touch content" on update (TaskUpdate
// payloads only carry the new status; the original subject was set on
// the matching TaskCreate). Insert paths require non-empty content.
//
// issueKey stamps the new row's issue scope on insert (BACI-62) —
// resolved by the caller from the session's single open claim at hook
// time; "" is allowed (orphan bucket, not surfaced in the per-(session,
// issue) UI). On the update path it's ignored — the row's existing
// issue_key stays put so a TaskUpdate fired after the agent flipped
// claims still lands with the job that created it.
//
// The session-level MaxSessionTodos cap is enforced on insert only —
// updates to existing rows never push the count up. Hitting the cap
// returns an error so the hook log-and-drops without writing a
// confusing partial mirror.
func (s *Store) UpsertSessionTodoFromTask(sessionID, taskID, issueKey, content string, status model.TodoStatus) error {
	if _, err := ValidateSessionID(sessionID); err != nil {
		return err
	}
	if strings.TrimSpace(taskID) == "" {
		return fmt.Errorf("task_id is required")
	}
	if issueKey != "" {
		if _, err := validateSingleLine(issueKey, "issue_key", maxNameLen, true); err != nil {
			return err
		}
	}
	switch status {
	case model.TodoPending, model.TodoInProgress, model.TodoCompleted:
		// ok
	default:
		return fmt.Errorf("unknown todo status %q", status)
	}
	if err := validateTodoContentForUpsert(content); err != nil {
		return err
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var sessPK int64
	if err := tx.QueryRow(`SELECT id FROM agent_sessions WHERE session_id = ?`, sessionID).Scan(&sessPK); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	var existingPos sql.NullInt64
	switch err := tx.QueryRow(
		`SELECT position FROM agent_session_todos WHERE session_pk = ? AND task_id = ?`,
		sessPK, taskID,
	).Scan(&existingPos); {
	case err == nil:
		// fallthrough to update branch
	case errors.Is(err, sql.ErrNoRows):
		existingPos = sql.NullInt64{}
	default:
		return err
	}

	if existingPos.Valid {
		// Update path. content="" means "leave the existing subject".
		// issue_key is left alone deliberately — the row belongs to the
		// job that created it, even if the agent has since flipped to a
		// new claim.
		if content == "" {
			if _, err := tx.Exec(
				`UPDATE agent_session_todos SET status = ?, updated_at = CURRENT_TIMESTAMP
					WHERE session_pk = ? AND position = ?`,
				string(status), sessPK, existingPos.Int64,
			); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(
				`UPDATE agent_session_todos
					SET content = ?, status = ?, updated_at = CURRENT_TIMESTAMP
					WHERE session_pk = ? AND position = ?`,
				content, string(status), sessPK, existingPos.Int64,
			); err != nil {
				return err
			}
		}
		return tx.Commit()
	}

	// Insert path — needs content + room under the cap.
	if content == "" {
		return fmt.Errorf("content is required when inserting a new todo")
	}
	var count int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM agent_session_todos WHERE session_pk = ?`, sessPK,
	).Scan(&count); err != nil {
		return err
	}
	if count >= model.MaxSessionTodos {
		return fmt.Errorf("too many todos: %d, max %d", count, model.MaxSessionTodos)
	}

	var maxPos sql.NullInt64
	if err := tx.QueryRow(
		`SELECT MAX(position) FROM agent_session_todos WHERE session_pk = ?`, sessPK,
	).Scan(&maxPos); err != nil {
		return err
	}
	nextPos := int64(0)
	if maxPos.Valid {
		nextPos = maxPos.Int64 + 1
	}
	if _, err := tx.Exec(
		`INSERT INTO agent_session_todos (session_pk, position, content, status, task_id, issue_key)
			VALUES (?, ?, ?, ?, ?, ?)`,
		sessPK, nextPos, content, string(status), taskID, issueKey,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// ListSessionTodos returns the latest snapshot for one session,
// position-ordered. issueKey == "" preserves the back-compat shape
// the REST handler exposes (every row for the session, regardless of
// issue scope); a non-empty issueKey filters to that issue's rows.
// Empty slice for an unknown session — never an error, since a UI may
// legitimately ask about a session whose todos haven't been mirrored
// yet (or are empty by design).
func (s *Store) ListSessionTodos(sessionID, issueKey string) ([]model.SessionTodo, error) {
	if _, err := ValidateSessionID(sessionID); err != nil {
		return nil, err
	}
	q := `SELECT t.position, t.content, t.status, t.task_id, t.issue_key, t.updated_at
		FROM agent_session_todos t
		JOIN agent_sessions s ON s.id = t.session_pk
		WHERE s.session_id = ?`
	args := []any{sessionID}
	if issueKey != "" {
		if _, err := validateSingleLine(issueKey, "issue_key", maxNameLen, true); err != nil {
			return nil, err
		}
		q += ` AND t.issue_key = ?`
		args = append(args, issueKey)
	}
	q += ` ORDER BY t.position ASC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SessionTodo
	for rows.Next() {
		t, err := scanSessionTodo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListTodosBySessionsAndIssue returns a session_pk → []SessionTodo
// map keyed off the (session, issue) pairs the caller asked for.
// Mirrors ListTodosBySessions' single-query shape so the desktop /
// TUI / web Agents view can hydrate every visible card in one trip;
// the per-(session, issue) scope means a session that has worked
// multiple issues only flows the current job's rows to each card.
// An empty input is a no-op (empty map, no SQL).
func (s *Store) ListTodosBySessionsAndIssue(pairs []SessionIssuePair) (map[int64][]model.SessionTodo, error) {
	out := make(map[int64][]model.SessionTodo)
	if len(pairs) == 0 {
		return out, nil
	}
	// Build a `(s.session_id, t.issue_key) IN ((?, ?), ...)` row-value
	// IN — SQLite supports it natively. Per-pair validation happens up
	// front so a bad input fails the call rather than returning a
	// partial result.
	clauses := make([]string, 0, len(pairs))
	args := make([]any, 0, len(pairs)*2)
	for _, p := range pairs {
		if _, err := ValidateSessionID(p.SessionID); err != nil {
			return nil, err
		}
		if p.IssueKey != "" {
			if _, err := validateSingleLine(p.IssueKey, "issue_key", maxNameLen, true); err != nil {
				return nil, err
			}
		}
		clauses = append(clauses, "(?, ?)")
		args = append(args, p.SessionID, p.IssueKey)
	}
	q := `SELECT t.session_pk, t.position, t.content, t.status, t.task_id, t.issue_key, t.updated_at
		FROM agent_session_todos t
		JOIN agent_sessions s ON s.id = t.session_pk
		WHERE (s.session_id, t.issue_key) IN (VALUES ` + strings.Join(clauses, ", ") + `)
		ORDER BY t.session_pk ASC, t.position ASC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pk int64
		var t model.SessionTodo
		var st string
		if err := rows.Scan(&pk, &t.Position, &t.Content, &st, &t.TaskID, &t.IssueKey, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.Status = model.TodoStatus(st)
		out[pk] = append(out[pk], t)
	}
	return out, rows.Err()
}

// ListTodosBySessions returns a session_pk → []SessionTodo map for the
// given session ids in one query, regardless of issue scope. Retained
// for back-compat (the REST `GET /agents/sessions/{id}/todos` handler
// without an `?issue_key=` filter routes through here via the
// per-session variant) — every new UI consumer should use
// ListTodosBySessionsAndIssue so it doesn't bleed prior-job rows into
// the current card.
func (s *Store) ListTodosBySessions(sessionIDs []string) (map[int64][]model.SessionTodo, error) {
	out := make(map[int64][]model.SessionTodo)
	if len(sessionIDs) == 0 {
		return out, nil
	}
	// Build a `IN (?, ?, ...)` placeholder list. sqlite has a high enough
	// host-parameter ceiling that the realistic per-repo session count
	// (tens) sails through.
	q := `SELECT t.session_pk, t.position, t.content, t.status, t.task_id, t.issue_key, t.updated_at
		FROM agent_session_todos t
		JOIN agent_sessions s ON s.id = t.session_pk
		WHERE s.session_id IN (` + placeholders(len(sessionIDs)) + `)
		ORDER BY t.session_pk ASC, t.position ASC`
	args := make([]any, len(sessionIDs))
	for i, id := range sessionIDs {
		args[i] = id
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pk int64
		var t model.SessionTodo
		var st string
		if err := rows.Scan(&pk, &t.Position, &t.Content, &st, &t.TaskID, &t.IssueKey, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.Status = model.TodoStatus(st)
		out[pk] = append(out[pk], t)
	}
	return out, rows.Err()
}

func scanSessionTodo(r rowScanner) (model.SessionTodo, error) {
	var t model.SessionTodo
	var st string
	if err := r.Scan(&t.Position, &t.Content, &st, &t.TaskID, &t.IssueKey, &t.UpdatedAt); err != nil {
		return t, err
	}
	t.Status = model.TodoStatus(st)
	return t, nil
}

// validateTodoContentForUpsert checks a single TaskCreate subject (or
// TaskUpdate replacement) before it lands in the table. Empty content
// is allowed here — the caller distinguishes "leave it alone" (update)
// from "set it" (insert). The rules otherwise mirror
// ValidateSessionTodos so the per-row guarantees stay identical
// regardless of which write path produced the row.
func validateTodoContentForUpsert(content string) error {
	if content == "" {
		return nil
	}
	if !utf8.ValidString(content) {
		return fmt.Errorf("content is not valid UTF-8")
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("content is required")
	}
	if len(content) > maxTodoContentBytes {
		return fmt.Errorf("content too long: %d bytes, max %d", len(content), maxTodoContentBytes)
	}
	for _, r := range content {
		if isDisallowedControlMulti(r) {
			return fmt.Errorf("content contains a disallowed control character (U+%04X)", r)
		}
	}
	return nil
}
