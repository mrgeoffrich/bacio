package model

import "time"

// MaxNotificationBodyLen caps a BACI-287 agent→user notification body. A
// notification is a short status note an agent fires at the user — not a
// document — so 4 KiB is generous enough for a multi-line update without
// inviting a runaway transcript paste. Enforced at the store boundary by
// store.ValidateNotificationBody.
const MaxNotificationBodyLen = 4 << 10 // 4 KiB

// Notification is one BACI-287 agent→user notification: a non-blocking,
// fire-and-forget message a running agent sends the user via the bacio
// channel's `send_user_notification` MCP tool. Unlike a SessionQuestion
// (BACI-53) it carries no answer and never blocks the agent — the channel
// inserts the row and returns immediately, so there is no lifecycle beyond
// unread→read. Local-only — never synced.
//
// RepoID scopes the row; RepoPrefix is the repo's 4-char key, lifted by the
// store's join so the cross-repo bell can label each row without a second
// lookup. IssueID/IssueKey are optional — a present issue makes the row
// deep-link to that ticket. SessionPK is the agent_sessions PK of the
// sending session when one was registered; nil for a notification fired
// before the channel session registered. SourceAgent is the persistent
// agent identity that sent it (or the "bacio-channel" fallback).
//
// ReadAt is the only lifecycle signal: nil = unread (counts toward the
// bell badge), non-nil = read.
type Notification struct {
	ID          int64      `json:"id"`
	RepoID      int64      `json:"repo_id"`
	RepoPrefix  string     `json:"repo_prefix,omitempty"`
	IssueID     *int64     `json:"issue_id,omitempty"`
	IssueKey    string     `json:"issue_key,omitempty"`
	Body        string     `json:"body"`
	SourceAgent string     `json:"source_agent"`
	SessionPK   *int64     `json:"session_pk,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	ReadAt      *time.Time `json:"read_at,omitempty"`
}
