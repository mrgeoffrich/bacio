package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func (c *localClient) CreateDispatch(ctx context.Context, repo *model.Repo, in inputs.AgentDispatchInput, dryRun bool) (*model.AgentDispatch, error) {
	if in.TargetAgent == "" && in.TargetSession == "" {
		return nil, fmt.Errorf("dispatch requires a target — pass --to <agent> and/or --session <id>")
	}

	var agentID *int64
	if in.TargetAgent != "" {
		ag, err := c.store.GetAgentByName(in.TargetAgent)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, fmt.Errorf("no agent identity named %q is registered", in.TargetAgent)
			}
			return nil, err
		}
		agentID = &ag.ID
	}
	if in.TargetSession != "" {
		if _, err := c.store.GetAgentSession(in.TargetSession); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, fmt.Errorf("session %q is not registered", in.TargetSession)
			}
			return nil, err
		}
	}

	var issueID *int64
	var issueKey string
	if in.IssueKey != "" {
		iss, err := c.GetIssueByKey(ctx, repo, in.IssueKey)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, fmt.Errorf("issue %s does not exist in repo %s", in.IssueKey, repo.Prefix)
			}
			return nil, err
		}
		issueID = &iss.ID
		issueKey = iss.Key
	}

	mode := model.DispatchMode(in.Mode)
	payload := model.ComposeDispatchPayload(mode, in.Message)

	if dryRun {
		return &model.AgentDispatch{
			RepoID:          repo.ID,
			RepoPrefix:      repo.Prefix,
			TargetAgentID:   agentID,
			TargetAgentName: in.TargetAgent,
			TargetSessionID: in.TargetSession,
			IssueID:         issueID,
			IssueKey:        issueKey,
			Mode:            mode,
			Payload:         payload,
			Status:          model.DispatchPending,
			CreatedBy:       c.actor,
			CreatedAt:       time.Now().UTC(),
		}, nil
	}

	d, err := c.store.AddDispatch(store.AddDispatchIn{
		RepoID:          repo.ID,
		TargetAgentID:   agentID,
		TargetSessionID: in.TargetSession,
		IssueID:         issueID,
		Mode:            mode,
		Payload:         payload,
		CreatedBy:       c.actor,
	})
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "agent.dispatch", Kind: "agent",
		TargetID: &d.ID, TargetLabel: dispatchTargetLabel(d),
		Details: dispatchDetails(d),
	})
	return d, nil
}

func (c *localClient) InboxDispatches(ctx context.Context, sessionID string) ([]*model.AgentDispatch, error) {
	sess, err := c.store.GetAgentSession(sessionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("session %q is not registered — run `bacio agent register` first", sessionID)
		}
		return nil, err
	}
	// A session's inbox is everything aimed at the bare session id OR at
	// the agent identity behind it. Open items only (pending/delivered);
	// acked/cancelled are history.
	return c.store.ListDispatches(store.DispatchFilter{
		TargetAgentID:   sess.AgentID,
		TargetSessionID: sess.SessionID,
		Statuses:        []model.DispatchStatus{model.DispatchPending, model.DispatchDelivered},
	})
}

func (c *localClient) AckDispatch(ctx context.Context, in inputs.AgentAckInput, dryRun bool) (*model.AgentDispatch, error) {
	d, err := c.store.GetDispatch(in.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("no dispatch with id %d", in.ID)
		}
		return nil, err
	}
	if dryRun {
		proj := *d
		now := time.Now().UTC()
		proj.Status = model.DispatchAcked
		proj.AckedAt = &now
		proj.AckNote = in.Note
		return &proj, nil
	}
	acked, err := c.store.AckDispatch(in.ID, in.Note)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &acked.RepoID, RepoPrefix: acked.RepoPrefix,
		Op: "agent.ack", Kind: "agent",
		TargetID: &acked.ID, TargetLabel: dispatchTargetLabel(acked),
		Details: dispatchDetails(acked),
	})
	return acked, nil
}

func (c *localClient) DrainDispatches(ctx context.Context, sessionID string) ([]*model.AgentDispatch, error) {
	sess, err := c.store.GetAgentSession(sessionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("session %q is not registered", sessionID)
		}
		return nil, err
	}
	pending, err := c.store.ListDispatches(store.DispatchFilter{
		TargetAgentID:   sess.AgentID,
		TargetSessionID: sess.SessionID,
		Statuses:        []model.DispatchStatus{model.DispatchPending},
	})
	if err != nil {
		return nil, err
	}
	out := make([]*model.AgentDispatch, 0, len(pending))
	for _, d := range pending {
		delivered, err := c.store.MarkDispatchDelivered(d.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, delivered)
	}
	return out, nil
}

func (c *localClient) RepoDispatches(ctx context.Context, repo *model.Repo) ([]*model.AgentDispatch, error) {
	if repo == nil {
		return nil, fmt.Errorf("RepoDispatches requires a repo")
	}
	return c.store.ListDispatches(store.DispatchFilter{RepoID: &repo.ID})
}

func (c *localClient) DrainAgentDispatches(ctx context.Context, repo *model.Repo, agentName string) ([]*model.AgentDispatch, error) {
	// An unscoped channel (no repo / no agents.json identity yet) has
	// nothing to drain — that's not an error, the channel just idles.
	if repo == nil || agentName == "" {
		return nil, nil
	}
	ag, err := c.store.GetAgentByName(agentName)
	if err != nil {
		// The identity isn't registered yet (no hook / register has run
		// in this repo) — transient, not an error. Idle until it is.
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	pending, err := c.store.ListDispatches(store.DispatchFilter{
		RepoID:        &repo.ID,
		TargetAgentID: &ag.ID,
		Statuses:      []model.DispatchStatus{model.DispatchPending},
	})
	if err != nil {
		return nil, err
	}
	out := make([]*model.AgentDispatch, 0, len(pending))
	for _, d := range pending {
		delivered, err := c.store.MarkDispatchDelivered(d.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, delivered)
	}
	return out, nil
}

// dispatchTargetLabel picks the most specific label for audit rows:
// the agent identity slug if there is one, else the session id.
func dispatchTargetLabel(d *model.AgentDispatch) string {
	if d.TargetAgentName != "" {
		return d.TargetAgentName
	}
	return d.TargetSessionID
}

// dispatchDetails builds the audit-log Details string for a dispatch op.
func dispatchDetails(d *model.AgentDispatch) string {
	var parts []string
	if d.IssueKey != "" {
		parts = append(parts, "issue="+d.IssueKey)
	}
	if d.TargetSessionID != "" {
		parts = append(parts, "session="+d.TargetSessionID)
	}
	parts = append(parts, "status="+string(d.Status))
	return joinCSV(parts)
}
