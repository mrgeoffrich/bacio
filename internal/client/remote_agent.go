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

func (c *remoteClient) CreateDispatch(ctx context.Context, repo *model.Repo, in inputs.AgentDispatchInput, dryRun bool) (*model.AgentDispatch, error) {
	return nil, remoteAgentNotSupported("dispatch")
}

func (c *remoteClient) InboxDispatches(ctx context.Context, sessionID string) ([]*model.AgentDispatch, error) {
	return nil, remoteAgentNotSupported("inbox")
}

func (c *remoteClient) AckDispatch(ctx context.Context, in inputs.AgentAckInput, dryRun bool) (*model.AgentDispatch, error) {
	return nil, remoteAgentNotSupported("ack")
}

func (c *remoteClient) DrainDispatches(ctx context.Context, sessionID string) ([]*model.AgentDispatch, error) {
	return nil, remoteAgentNotSupported("inbox")
}

func (c *remoteClient) RepoDispatches(ctx context.Context, repo *model.Repo) ([]*model.AgentDispatch, error) {
	return nil, remoteAgentNotSupported("dispatches")
}

func (c *remoteClient) DrainAgentDispatches(ctx context.Context, repo *model.Repo, agentName string) ([]*model.AgentDispatch, error) {
	return nil, remoteAgentNotSupported("channel")
}

func (c *remoteClient) EnsureAgentIdentity(ctx context.Context, repo *model.Repo) (string, error) {
	return "", remoteAgentNotSupported("register")
}

func (c *remoteClient) UpsertAgentChannel(ctx context.Context, repo *model.Repo, agentName, host string, claudePID, channelPID int64) error {
	return remoteAgentNotSupported("channel")
}

func (c *remoteClient) LinkSessionChannel(ctx context.Context, sessionID string, claudePID int64, host string) error {
	return remoteAgentNotSupported("channel")
}

// Prompt templates live in the local app_settings KV — like the agent
// registry, there's no remote analogue in v1.

func (c *remoteClient) GetPromptTemplates(ctx context.Context) (map[string]string, error) {
	return nil, remoteAgentNotSupported("prompt-templates")
}

func (c *remoteClient) SetPromptTemplate(ctx context.Context, mode, body string, dryRun bool) error {
	return remoteAgentNotSupported("prompt-templates")
}

func (c *remoteClient) GetPromptStates(ctx context.Context) (map[string][]string, error) {
	return nil, remoteAgentNotSupported("prompt-states")
}

func (c *remoteClient) SetPromptStates(ctx context.Context, mode string, states []string, dryRun bool) error {
	return remoteAgentNotSupported("prompt-states")
}

// Board preferences live in the local app_settings KV — like the agent
// registry and prompt templates, there's no remote analogue in v1.

func (c *remoteClient) GetBoardPreferences(ctx context.Context) (BoardPreferences, error) {
	return BoardPreferences{}, remoteAgentNotSupported("board-preferences")
}

func (c *remoteClient) SetBoardPreferences(ctx context.Context, prefs BoardPreferences, dryRun bool) error {
	return remoteAgentNotSupported("board-preferences")
}
