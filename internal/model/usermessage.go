package model

import "time"

// MaxUserMessageLen caps a BACI-286 user→agent steer message body. A
// steer note is a short instruction the user types to nudge a running
// worker — not a document — so 8 KiB is generous without inviting a
// whole transcript paste. Enforced at the store boundary by
// store.ValidateUserMessageBody.
const MaxUserMessageLen = 8 << 10 // 8 KiB

// UserMessage is one BACI-286 user→agent steer message: a free-form
// note the user pushes at a specific busy session, delivered by the
// bacio channel at the worker's next turn boundary as a
// `<channel kind="message">` tag. Unlike an AgentDispatch it is NOT
// matched/bound and carries no ack — it is fire-and-forget, scoped to a
// single session.
//
// SessionPK is the agent_sessions PK; SessionID is the external session
// id lifted alongside it by the store's join so callers (the channel,
// the REST surface) don't need a second lookup. ConsumedAt is the only
// delivery signal: nil = un-consumed (not yet pushed), non-nil = the
// channel drained + pushed it. Like a dispatch's `delivered`, that is
// not proof the agent acted on the message.
type UserMessage struct {
	ID         int64      `json:"id"`
	SessionPK  int64      `json:"session_pk"`
	SessionID  string     `json:"session_id"`
	RepoID     int64      `json:"repo_id"`
	Body       string     `json:"body"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	ConsumedAt *time.Time `json:"consumed_at,omitempty"`
}
