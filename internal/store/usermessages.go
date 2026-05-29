package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// AddUserMessageIn is the validated tuple AddUserMessage consumes.
// SessionID is the external session id (the store resolves the
// agent_sessions FK and repo from it). Body is the free-form steer
// note; CreatedBy is the actor (a user at a desktop, typically "user").
type AddUserMessageIn struct {
	SessionID string
	Body      string
	CreatedBy string
}

// ValidateUserMessageBody enforces the BACI-286 steer-message body
// shape: required, multi-line free text (newlines/tabs allowed), no
// disallowed control chars, capped at model.MaxUserMessageLen.
func ValidateUserMessageBody(s string) (string, error) {
	out, err := ValidateBody(s, "body", true)
	if err != nil {
		return "", err
	}
	if len(out) > model.MaxUserMessageLen {
		return "", fmt.Errorf("body too long: %d bytes, max %d", len(out), model.MaxUserMessageLen)
	}
	return out, nil
}

// AddUserMessage records a new un-consumed steer message for a session.
// The session is resolved by external session_id; an unknown session_id
// returns ErrNotFound. repo_id is denormalised from the resolved session
// so the row stays repo-scoped without a second lookup. The returned row
// carries the freshly-assigned id + created_at.
func (s *Store) AddUserMessage(in AddUserMessageIn) (*model.UserMessage, error) {
	if _, err := ValidateSessionID(in.SessionID); err != nil {
		return nil, err
	}
	body, err := ValidateUserMessageBody(in.Body)
	if err != nil {
		return nil, err
	}
	createdBy, err := ValidateActor(in.CreatedBy)
	if err != nil {
		return nil, fmt.Errorf("created_by: %w", err)
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var (
		sessPK int64
		repoID int64
	)
	if err := tx.QueryRow(
		`SELECT id, repo_id FROM agent_sessions WHERE session_id = ?`, in.SessionID,
	).Scan(&sessPK, &repoID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	res, err := tx.Exec(`
		INSERT INTO user_messages (session_pk, repo_id, body, created_by)
		VALUES (?, ?, ?, ?)`,
		sessPK, repoID, body, createdBy,
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetUserMessage(id)
}

// userMessageSelect is the canonical column list — the join lifts the
// external session_id alongside the row so the channel doesn't need a
// second lookup.
const userMessageSelect = `
	SELECT m.id, m.session_pk, s.session_id, m.repo_id,
	       m.body, m.created_by, m.created_at, m.consumed_at
	FROM user_messages m
	JOIN agent_sessions s ON s.id = m.session_pk`

// GetUserMessage fetches one steer message by primary key, or ErrNotFound.
func (s *Store) GetUserMessage(id int64) (*model.UserMessage, error) {
	row := s.DB.QueryRow(userMessageSelect+` WHERE m.id = ?`, id)
	m, err := scanUserMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

// DrainUserMessagesForSession returns the un-consumed steer messages for
// one session, oldest-first, and stamps consumed_at on the returned set
// in the same transaction — the same "drain marks consumed" shape the
// dispatch drain uses for `delivered`. The channel dedups per-process so
// returning a row twice across two ticks can't happen here (a consumed
// row is filtered out), but a fresh channel process never re-pushes a
// consumed row either — consumed is terminal. An unknown session id
// returns an empty slice (the channel's drain walks every session it can
// see, including ones the store no longer knows about).
func (s *Store) DrainUserMessagesForSession(sessionID string) ([]*model.UserMessage, error) {
	if _, err := ValidateSessionID(sessionID); err != nil {
		return nil, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var sessPK int64
	if err := tx.QueryRow(`SELECT id FROM agent_sessions WHERE session_id = ?`, sessionID).Scan(&sessPK); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	rows, err := tx.Query(userMessageSelect+`
		WHERE m.session_pk = ? AND m.consumed_at IS NULL
		ORDER BY m.created_at ASC, m.id ASC`, sessPK)
	if err != nil {
		return nil, err
	}
	var out []*model.UserMessage
	var ids []int64
	for rows.Next() {
		v, err := scanUserMessage(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, v)
		ids = append(ids, v.ID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(ids) == 0 {
		return nil, nil // nothing to consume; no write
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(`
		UPDATE user_messages SET consumed_at = ?
		WHERE session_pk = ? AND consumed_at IS NULL`, now, sessPK); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// Reflect the consume on the returned rows — the rows were scanned
	// before the UPDATE, so their ConsumedAt was still NULL. Callers
	// (the channel) only need ID/Body/CreatedBy, but a row that claims
	// to be un-consumed after a drain would be a contract lie.
	for _, m := range out {
		stamp := now
		m.ConsumedAt = &stamp
	}
	return out, nil
}

func scanUserMessage(r rowScanner) (*model.UserMessage, error) {
	var (
		v          model.UserMessage
		consumedAt sql.NullTime
	)
	if err := r.Scan(
		&v.ID, &v.SessionPK, &v.SessionID, &v.RepoID,
		&v.Body, &v.CreatedBy, &v.CreatedAt, &consumedAt,
	); err != nil {
		return nil, err
	}
	if consumedAt.Valid {
		v.ConsumedAt = &consumedAt.Time
	}
	return &v, nil
}
