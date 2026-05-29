package client

import (
	"context"
	"fmt"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// AddUserMessage enqueues a BACI-286 user→agent steer message against a
// session. The store resolves the agent_sessions FK + repo from the
// session id and validates the body. Writes an agent.message audit row
// so `bacio history --op agent.message` surfaces every steer note —
// unlike a question there is no answer/abandon lifecycle, so the enqueue
// is the only audited event.
func (c *localClient) AddUserMessage(ctx context.Context, in AddUserMessageInput) (*model.UserMessage, error) {
	// Default the sender to the client's actor (e.g. "desktop", or the
	// REST request's X-Actor) when a caller doesn't thread one through —
	// mirrors how the question path resolves answered_by from c.actor.
	createdBy := in.CreatedBy
	if createdBy == "" {
		createdBy = c.actor
	}
	m, err := c.store.AddUserMessage(store.AddUserMessageIn{
		SessionID: in.SessionID,
		Body:      in.Body,
		CreatedBy: createdBy,
	})
	if err != nil {
		return nil, err
	}
	repoID := m.RepoID
	c.recordOp(model.HistoryEntry{
		Actor:  createdBy,
		RepoID: &repoID,
		Op:     "agent.message", Kind: "message",
		TargetID:    &m.ID,
		TargetLabel: m.SessionID,
		Details:     fmt.Sprintf("session=%s,bytes=%d", m.SessionID, len(m.Body)),
	})
	return m, nil
}

// DrainUserMessagesForSession returns + consumes a session's un-consumed
// steer messages — the channel's push path. No audit row (mechanical
// drain, matching the question-drain precedent).
func (c *localClient) DrainUserMessagesForSession(ctx context.Context, sessionID string) ([]*model.UserMessage, error) {
	return c.store.DrainUserMessagesForSession(sessionID)
}
