package store

import (
	"database/sql"
	"errors"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// ReplaceSessionTodos swaps a session's todo list atomically. The
// session is resolved by external session_id, so the post-tool-use hook
// can call this without first looking up the PK. A nil/empty slice
// clears the list entirely (the agent emptied its plan). An unknown
// session_id returns ErrNotFound — the hook handler logs and drops the
// snapshot; the next TodoWrite after the session registers mirrors fine.
func (s *Store) ReplaceSessionTodos(sessionID string, todos []model.SessionTodo) error {
	if _, err := ValidateSessionID(sessionID); err != nil {
		return err
	}
	if err := ValidateSessionTodos(todos); err != nil {
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
	if _, err := tx.Exec(`DELETE FROM agent_session_todos WHERE session_pk = ?`, sessPK); err != nil {
		return err
	}
	if len(todos) > 0 {
		stmt, err := tx.Prepare(`INSERT INTO agent_session_todos (session_pk, position, content, status) VALUES (?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, t := range todos {
			if _, err := stmt.Exec(sessPK, t.Position, t.Content, string(t.Status)); err != nil {
				return err
			}
		}
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
		`SELECT t.position, t.content, t.status, t.updated_at
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
	q := `SELECT t.session_pk, t.position, t.content, t.status, t.updated_at
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
		if err := rows.Scan(&pk, &t.Position, &t.Content, &st, &t.UpdatedAt); err != nil {
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
	if err := r.Scan(&t.Position, &t.Content, &st, &t.UpdatedAt); err != nil {
		return t, err
	}
	t.Status = model.TodoStatus(st)
	return t, nil
}
