package client

import (
	"context"
	"fmt"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// Agent registry is local-only in v1. Every remote method returns
// ErrLocalOnly with a short hint so the CLI can surface a clear
// "drop --remote / unset BACIO_REMOTE" message rather than a 404.
//
// v2 follow-up: implement HTTP parity in internal/api/handlers_agent.go
// and switch these stubs to the same `c.do(...)` pattern used by
// remote_issue.go.

func remoteAgentNotSupported(verb string) error {
	return fmt.Errorf("bacio agent %s: %w (drop --remote / unset BACIO_REMOTE — the agent registry lives only in the local SQLite store in v1)", verb, ErrLocalOnly)
}

func (c *remoteClient) RegisterAgent(ctx context.Context, repo *model.Repo, in inputs.AgentRegisterInput, dryRun bool) (*model.AgentSession, error) {
	return nil, remoteAgentNotSupported("register")
}

func (c *remoteClient) HeartbeatAgent(ctx context.Context, repo *model.Repo, in inputs.AgentHeartbeatInput, dryRun bool) (*model.AgentSession, error) {
	return nil, remoteAgentNotSupported("heartbeat")
}

func (c *remoteClient) EndAgent(ctx context.Context, repo *model.Repo, in inputs.AgentEndInput, dryRun bool) (*model.AgentSession, error) {
	return nil, remoteAgentNotSupported("end")
}

func (c *remoteClient) ClaimAgent(ctx context.Context, repo *model.Repo, in inputs.AgentClaimInput, dryRun bool) (*model.AgentClaim, error) {
	return nil, remoteAgentNotSupported("claim")
}

func (c *remoteClient) ReleaseAgent(ctx context.Context, repo *model.Repo, in inputs.AgentReleaseInput, dryRun bool) (*model.AgentClaim, error) {
	return nil, remoteAgentNotSupported("release")
}

func (c *remoteClient) ListAgentSessions(ctx context.Context, f AgentSessionFilter) ([]*model.AgentSession, error) {
	return nil, remoteAgentNotSupported("list")
}

func (c *remoteClient) ShowAgentSession(ctx context.Context, sessionID string) (*AgentSessionView, error) {
	return nil, remoteAgentNotSupported("show")
}
