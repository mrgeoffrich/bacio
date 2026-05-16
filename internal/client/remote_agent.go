package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// HTTP parity for the agent-registry verbs landed in BACI-34. The
// methods in scope are register / heartbeat / end / claim / release /
// list / show / inbox / ack / ListOpenClaims — every other agent-related
// method (dispatch creation, prompt templates, board prefs, the
// channel-side primitives) still has no HTTP analogue and returns
// ErrLocalOnly via remoteAgentNotSupported.

func remoteAgentNotSupported(verb string) error {
	return fmt.Errorf("bacio agent %s: %w (drop --remote / unset BACIO_REMOTE — this agent verb is local-only)", verb, ErrLocalOnly)
}

func (c *remoteClient) RegisterAgent(ctx context.Context, repo *model.Repo, in inputs.AgentRegisterInput, dryRun bool) (*model.AgentSession, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.AgentSession
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo.Prefix+"/agents/sessions", q, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) HeartbeatAgent(ctx context.Context, repo *model.Repo, in inputs.AgentHeartbeatInput, dryRun bool) (*model.AgentSession, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.AgentSession
	if err := c.do(ctx, http.MethodPost, "/agents/sessions/"+url.PathEscape(in.SessionID)+"/heartbeat", q, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) EndAgent(ctx context.Context, repo *model.Repo, in inputs.AgentEndInput, dryRun bool) (*model.AgentSession, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.AgentSession
	if err := c.do(ctx, http.MethodPost, "/agents/sessions/"+url.PathEscape(in.SessionID)+"/end", q, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) ClaimAgent(ctx context.Context, repo *model.Repo, in inputs.AgentClaimInput, dryRun bool) (*model.AgentClaim, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.AgentClaim
	if err := c.do(ctx, http.MethodPost, "/agents/sessions/"+url.PathEscape(in.SessionID)+"/claims", q, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) ReleaseAgent(ctx context.Context, repo *model.Repo, in inputs.AgentReleaseInput, dryRun bool) (*model.AgentClaim, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.AgentClaim
	if err := c.do(ctx, http.MethodDelete, "/agents/sessions/"+url.PathEscape(in.SessionID)+"/claims", q, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) ListAgentSessions(ctx context.Context, f AgentSessionFilter) ([]*model.AgentSession, error) {
	q := url.Values{}
	if f.OnlyAlive {
		q.Set("active", "true")
	}
	// The server defaults to registered-only; flip to "include stubs" via
	// ?all=true so the wire flag mirrors the CLI's `--all`. The flag
	// carries the opposite polarity from f.RegisteredOnly precisely
	// because a remote client opting into stubs should be explicit.
	if !f.RegisteredOnly {
		q.Set("all", "true")
	}
	if !f.Since.IsZero() {
		// Express the cutoff as a relative duration string the server
		// re-resolves against its own clock — avoids drift between two
		// machines with slightly different wall times.
		q.Set("since", time.Since(f.Since).Round(time.Second).String())
	}
	path := "/agents/sessions"
	if f.Repo != nil {
		path = "/repos/" + f.Repo.Prefix + "/agents/sessions"
	}
	var out []*model.AgentSession
	if err := c.do(ctx, http.MethodGet, path, q, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.AgentSession{}
	}
	return out, nil
}

func (c *remoteClient) ShowAgentSession(ctx context.Context, sessionID string) (*AgentSessionView, error) {
	var out AgentSessionView
	if err := c.do(ctx, http.MethodGet, "/agents/sessions/"+url.PathEscape(sessionID), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) ListOpenClaims(ctx context.Context, repo *model.Repo) ([]*model.AgentClaim, error) {
	path := "/agents/claims/open"
	if repo != nil {
		path = "/repos/" + repo.Prefix + "/agents/claims/open"
	}
	var out []*model.AgentClaim
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.AgentClaim{}
	}
	return out, nil
}

func (c *remoteClient) InboxDispatches(ctx context.Context, sessionID string) ([]*model.AgentDispatch, error) {
	var out []*model.AgentDispatch
	if err := c.do(ctx, http.MethodGet, "/agents/sessions/"+url.PathEscape(sessionID)+"/inbox", nil, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.AgentDispatch{}
	}
	return out, nil
}

func (c *remoteClient) AckDispatch(ctx context.Context, in inputs.AgentAckInput, dryRun bool) (*model.AgentDispatch, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.AgentDispatch
	path := "/agents/dispatches/" + strconv.FormatInt(in.ID, 10) + "/ack"
	if err := c.do(ctx, http.MethodPost, path, q, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------- the rest stays local-only ----------
// CreateDispatch, prompt templates, board prefs, and every channel /
// hook-internal primitive don't have HTTP analogues in v1. Most exist
// to drive the per-machine `bacio channel` / `bacio hook` shims (which
// always talk to the local store) or to mutate the global app_settings
// KV (no remote analogue in v1).

// CreateDispatch queues a dispatch via the REST API (BACI-35).
// Mirrors the CLI flag path through the inputs.AgentDispatchInput
// shape; the server resolves agent / session / issue / prompt
// template and stamps the audit log itself.
func (c *remoteClient) CreateDispatch(ctx context.Context, repo *model.Repo, in inputs.AgentDispatchInput, dryRun bool) (*model.AgentDispatch, error) {
	if repo == nil {
		return nil, fmt.Errorf("CreateDispatch requires a repo")
	}
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.AgentDispatch
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo.Prefix+"/agents/dispatches", q, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DrainDispatches is the side-effect-bearing "list pending+delivered
// AND mark pending → delivered" call used by the bacio hook to feed an
// agent's context. The hook talks to the local store directly (it
// doesn't go through --remote), so this is genuinely not needed over
// HTTP today — keep it local-only and clearly labelled.
func (c *remoteClient) DrainDispatches(ctx context.Context, sessionID string) ([]*model.AgentDispatch, error) {
	return nil, remoteAgentNotSupported("inbox-drain")
}

// RepoDispatches lists every dispatch queued against a repo, newest
// first — the read backing the desktop Agents view's per-repo bucket
// (BACI-35).
func (c *remoteClient) RepoDispatches(ctx context.Context, repo *model.Repo) ([]*model.AgentDispatch, error) {
	if repo == nil {
		return nil, fmt.Errorf("RepoDispatches requires a repo")
	}
	var out []*model.AgentDispatch
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo.Prefix+"/agents/dispatches", nil, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.AgentDispatch{}
	}
	return out, nil
}

func (c *remoteClient) EnsureSetupDispatch(ctx context.Context, repo *model.Repo, sessionID string) (*model.AgentDispatch, error) {
	return nil, remoteAgentNotSupported("channel")
}

func (c *remoteClient) DrainAgentDispatches(ctx context.Context, repo *model.Repo, agentName string) ([]*model.AgentDispatch, error) {
	return nil, remoteAgentNotSupported("channel")
}

func (c *remoteClient) EnsureAgentIdentity(ctx context.Context, repo *model.Repo) (string, error) {
	return "", remoteAgentNotSupported("register")
}

func (c *remoteClient) CreateSessionStub(ctx context.Context, repo *model.Repo, sessionID, host string, claudePID int64) (*model.AgentSession, error) {
	return nil, remoteAgentNotSupported("session-stub")
}

func (c *remoteClient) SessionsByClaudePID(ctx context.Context, host string, claudePID int64) ([]*model.AgentSession, error) {
	return nil, remoteAgentNotSupported("sessions-by-claude-pid")
}

func (c *remoteClient) CompleteRegistration(ctx context.Context, repo *model.Repo, in inputs.AgentRegisterInput, channelVersion string) (*model.AgentSession, error) {
	return nil, remoteAgentNotSupported("register")
}

func (c *remoteClient) UpsertAgentChannel(ctx context.Context, repo *model.Repo, agentName, host string, claudePID, channelPID int64) error {
	return remoteAgentNotSupported("channel")
}

func (c *remoteClient) LinkSessionChannel(ctx context.Context, sessionID string, claudePID int64, host string) error {
	return remoteAgentNotSupported("channel")
}

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

func (c *remoteClient) GetBoardPreferences(ctx context.Context) (BoardPreferences, error) {
	return BoardPreferences{}, remoteAgentNotSupported("board-preferences")
}

func (c *remoteClient) SetBoardPreferences(ctx context.Context, prefs BoardPreferences, dryRun bool) error {
	return remoteAgentNotSupported("board-preferences")
}
