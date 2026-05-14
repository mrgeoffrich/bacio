package client

import (
	"context"
	"errors"
	"fmt"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// UpsertAgentChannel records (or heartbeats) a live `bacio channel`
// subprocess. agentName is resolved best-effort: if the identity isn't
// registered yet (transient — the session-start hook may not have run)
// the row's agent_id is just left NULL and the next poll tick fixes it.
func (c *localClient) UpsertAgentChannel(ctx context.Context, repo *model.Repo, agentName, host string, claudePID, channelPID int64) error {
	if repo == nil {
		return fmt.Errorf("UpsertAgentChannel requires a repo")
	}
	var agentID *int64
	if agentName != "" {
		ag, err := c.store.GetAgentByName(agentName)
		switch {
		case err == nil:
			agentID = &ag.ID
		case errors.Is(err, store.ErrNotFound):
			// identity not registered yet — leave agent_id NULL
		default:
			return err
		}
	}
	return c.store.UpsertAgentChannel(store.UpsertAgentChannelIn{
		RepoID:     repo.ID,
		AgentID:    agentID,
		Host:       host,
		ClaudePID:  claudePID,
		ChannelPID: channelPID,
	})
}

// LinkSessionChannel is a thin passthrough to the store — the hook side
// of the channel<->session join.
func (c *localClient) LinkSessionChannel(ctx context.Context, sessionID string, claudePID int64, host string) error {
	return c.store.LinkSessionChannel(sessionID, claudePID, host)
}
