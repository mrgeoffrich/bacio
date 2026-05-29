package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// AddNotificationIn is the validated tuple AddNotification consumes.
// RepoPrefix names the repo the notification is scoped to (resolved to
// repo_id). SessionID is the external session id of the sending channel
// session, when one had registered — optional, resolves the nullable
// session_pk FK. IssueKey is optional; when present the row deep-links to
// that issue (which must live in the named repo). Body is the message;
// SourceAgent is the persistent agent identity that sent it (or the
// "bacio-channel" fallback).
type AddNotificationIn struct {
	RepoPrefix  string
	SessionID   string
	IssueKey    string
	Body        string
	SourceAgent string
}

// NotificationFilter scopes a ListNotifications / count query. RepoID nil
// means cross-repo (the global bell's default — mirrors serveHistory's
// repo-less query). UnreadOnly drops read rows. Limit caps the result set;
// <= 0 falls back to defaultNotificationLimit.
type NotificationFilter struct {
	RepoID     *int64
	UnreadOnly bool
	Limit      int
}

// defaultNotificationLimit caps an unbounded list query so the bell can't
// pull the entire table on a busy DB. Generous for a dropdown that shows
// the most-recent notifications first.
const defaultNotificationLimit = 200

// ValidateNotificationBody enforces the BACI-287 notification body shape:
// required, multi-line free text (newlines/tabs allowed), no disallowed
// control chars, capped at model.MaxNotificationBodyLen.
func ValidateNotificationBody(s string) (string, error) {
	out, err := ValidateBody(s, "body", true)
	if err != nil {
		return "", err
	}
	if len(out) > model.MaxNotificationBodyLen {
		return "", fmt.Errorf("body too long: %d bytes, max %d", len(out), model.MaxNotificationBodyLen)
	}
	return out, nil
}

// AddNotification records a new unread agent→user notification. The repo is
// resolved by prefix (an unknown prefix returns ErrNotFound). An optional
// SessionID resolves the nullable session_pk; an unknown session id is
// tolerated (the row records source_agent without a session FK). An
// optional IssueKey resolves issue_id + denormalises the key; the issue
// must belong to the resolved repo. The returned row carries the
// freshly-assigned id + created_at.
func (s *Store) AddNotification(in AddNotificationIn) (*model.Notification, error) {
	body, err := ValidateNotificationBody(in.Body)
	if err != nil {
		return nil, err
	}
	sourceAgent, err := ValidateAgentName(in.SourceAgent)
	if err != nil {
		return nil, fmt.Errorf("source_agent: %w", err)
	}
	if in.SessionID != "" {
		if _, err := ValidateSessionID(in.SessionID); err != nil {
			return nil, err
		}
	}

	repo, err := s.GetRepoByPrefix(in.RepoPrefix)
	if err != nil {
		return nil, err
	}

	// Resolve the optional issue: it must parse and must live in the named
	// repo — a notification can't deep-link to a ticket in another repo.
	var (
		issueID  sql.NullInt64
		issueKey string
	)
	if in.IssueKey != "" {
		prefix, num, perr := ParseIssueKey(in.IssueKey)
		if perr != nil {
			return nil, perr
		}
		iss, gerr := s.GetIssueByKey(prefix, num)
		if gerr != nil {
			return nil, gerr
		}
		if iss.RepoID != repo.ID {
			return nil, fmt.Errorf("issue %s is not in repo %s", in.IssueKey, repo.Prefix)
		}
		issueID = sql.NullInt64{Int64: iss.ID, Valid: true}
		issueKey = iss.Key
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Resolve the optional session_pk. An unknown session id leaves the FK
	// NULL — a notification fired before the channel session registered
	// still records its source_agent.
	var sessPK sql.NullInt64
	if in.SessionID != "" {
		var pk int64
		if qerr := tx.QueryRow(
			`SELECT id FROM agent_sessions WHERE session_id = ?`, in.SessionID,
		).Scan(&pk); qerr != nil {
			if !errors.Is(qerr, sql.ErrNoRows) {
				return nil, qerr
			}
		} else {
			sessPK = sql.NullInt64{Int64: pk, Valid: true}
		}
	}

	res, err := tx.Exec(`
		INSERT INTO notifications
		    (repo_id, issue_id, issue_key, body, source_agent, session_pk)
		VALUES (?, ?, ?, ?, ?, ?)`,
		repo.ID, issueID, issueKey, body, sourceAgent, sessPK,
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
	return s.GetNotification(id)
}

// notificationSelect is the canonical column list — the join lifts the
// repo prefix alongside the row so the cross-repo bell can label each
// notification without a second lookup.
const notificationSelect = `
	SELECT n.id, n.repo_id, r.prefix, n.issue_id, n.issue_key,
	       n.body, n.source_agent, n.session_pk, n.created_at, n.read_at
	FROM notifications n
	JOIN repos r ON r.id = n.repo_id`

// GetNotification fetches one notification by primary key, or ErrNotFound.
func (s *Store) GetNotification(id int64) (*model.Notification, error) {
	row := s.DB.QueryRow(notificationSelect+` WHERE n.id = ?`, id)
	n, err := scanNotification(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}

// ListNotifications returns notifications matching the filter, newest-first.
// A nil filter.RepoID lists across every repo (the global bell default);
// filter.UnreadOnly drops read rows.
func (s *Store) ListNotifications(filter NotificationFilter) ([]*model.Notification, error) {
	q := notificationSelect
	var (
		args   []any
		wheres []string
	)
	if filter.RepoID != nil {
		wheres = append(wheres, `n.repo_id = ?`)
		args = append(args, *filter.RepoID)
	}
	if filter.UnreadOnly {
		wheres = append(wheres, `n.read_at IS NULL`)
	}
	for i, w := range wheres {
		if i == 0 {
			q += ` WHERE ` + w
		} else {
			q += ` AND ` + w
		}
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultNotificationLimit
	}
	q += ` ORDER BY n.created_at DESC, n.id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Notification
	for rows.Next() {
		v, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CountUnreadNotifications returns the number of unread rows. A nil repoID
// counts across every repo (the global bell badge).
func (s *Store) CountUnreadNotifications(repoID *int64) (int, error) {
	q := `SELECT COUNT(*) FROM notifications WHERE read_at IS NULL`
	var args []any
	if repoID != nil {
		q += ` AND repo_id = ?`
		args = append(args, *repoID)
	}
	var n int
	if err := s.DB.QueryRow(q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// MarkNotificationRead stamps read_at on one notification. Idempotent —
// marking an already-read row leaves read_at untouched (the WHERE clause
// guards it) and is not an error; the returned row carries the existing
// read_at. An unknown id returns ErrNotFound.
func (s *Store) MarkNotificationRead(id int64) (*model.Notification, error) {
	if _, err := s.GetNotification(id); err != nil {
		return nil, err
	}
	if _, err := s.DB.Exec(`
		UPDATE notifications
		   SET read_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND read_at IS NULL`, id); err != nil {
		return nil, err
	}
	return s.GetNotification(id)
}

// MarkAllNotificationsRead stamps read_at on every unread row, returning the
// number of rows flipped. A nil repoID marks across every repo (the bell's
// "mark all read" footer); a non-nil repoID scopes the sweep.
func (s *Store) MarkAllNotificationsRead(repoID *int64) (int, error) {
	q := `UPDATE notifications SET read_at = ? WHERE read_at IS NULL`
	args := []any{time.Now().UTC()}
	if repoID != nil {
		q += ` AND repo_id = ?`
		args = append(args, *repoID)
	}
	res, err := s.DB.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func scanNotification(r rowScanner) (*model.Notification, error) {
	var (
		v         model.Notification
		issueID   sql.NullInt64
		issueKey  sql.NullString
		sessionPK sql.NullInt64
		readAt    sql.NullTime
	)
	if err := r.Scan(
		&v.ID, &v.RepoID, &v.RepoPrefix, &issueID, &issueKey,
		&v.Body, &v.SourceAgent, &sessionPK, &v.CreatedAt, &readAt,
	); err != nil {
		return nil, err
	}
	if issueID.Valid {
		id := issueID.Int64
		v.IssueID = &id
	}
	if issueKey.Valid {
		v.IssueKey = issueKey.String
	}
	if sessionPK.Valid {
		pk := sessionPK.Int64
		v.SessionPK = &pk
	}
	if readAt.Valid {
		v.ReadAt = &readAt.Time
	}
	return &v, nil
}
