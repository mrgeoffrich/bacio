package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mrgeoffrich/bacio/internal/model"
)

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
// The session-level MaxSessionTodos cap is enforced on insert only —
// updates to existing rows never push the count up. Hitting the cap
// returns an error so the hook log-and-drops without writing a
// confusing partial mirror.
func (s *Store) UpsertSessionTodoFromTask(sessionID, taskID, content string, status model.TodoStatus) error {
	if _, err := ValidateSessionID(sessionID); err != nil {
		return err
	}
	if strings.TrimSpace(taskID) == "" {
		return fmt.Errorf("task_id is required")
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
		`INSERT INTO agent_session_todos (session_pk, position, content, status, task_id)
			VALUES (?, ?, ?, ?, ?)`,
		sessPK, nextPos, content, string(status), taskID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// ListSessionTodos returns the latest snapshot for one session,
// position-ordered. Empty slice for an unknown session — never an
// error, since a UI may legitimately ask about a session whose todos
// haven't been mirrored yet (or are empty by design).
func (s *Store) ListSessionTodos(sessionID string) ([]model.SessionTodo, error) {
	if _, err := ValidateSessionID(sessionID); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(
		`SELECT t.position, t.content, t.status, t.task_id, t.updated_at
		FROM agent_session_todos t
		JOIN agent_sessions s ON s.id = t.session_pk
		WHERE s.session_id = ?
		ORDER BY t.position ASC`, sessionID,
	)
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

// ListTodosBySessions returns a session_pk → []SessionTodo map for the
// given session ids in one query. Mirrors OpenClaimsBySession so the
// agent views can hydrate every row in one trip instead of N+1. Sessions
// with no todos are absent from the map (zero-value rendering on the
// caller side). An empty input is a no-op (empty map, no SQL).
func (s *Store) ListTodosBySessions(sessionIDs []string) (map[int64][]model.SessionTodo, error) {
	out := make(map[int64][]model.SessionTodo)
	if len(sessionIDs) == 0 {
		return out, nil
	}
	// Build a `IN (?, ?, ...)` placeholder list. sqlite has a high enough
	// host-parameter ceiling that the realistic per-repo session count
	// (tens) sails through.
	q := `SELECT t.session_pk, t.position, t.content, t.status, t.task_id, t.updated_at
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
		if err := rows.Scan(&pk, &t.Position, &t.Content, &st, &t.TaskID, &t.UpdatedAt); err != nil {
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
	if err := r.Scan(&t.Position, &t.Content, &st, &t.TaskID, &t.UpdatedAt); err != nil {
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
