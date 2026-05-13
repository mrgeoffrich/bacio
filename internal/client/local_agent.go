package client

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// projectAgentSession builds the in-memory session object the local
// client returns from --dry-run paths. Mirrors what UpsertAgentSession
// would store, minus server-time fields (id, created_at-style stamps).
func projectAgentSession(repo *model.Repo, in inputs.AgentRegisterInput) *model.AgentSession {
	return &model.AgentSession{
		SessionID:      in.SessionID,
		RepoID:         repo.ID,
		RepoPrefix:     repo.Prefix,
		Actor:          in.Actor,
		Model:          in.Model,
		PermissionMode: in.PermissionMode,
		Host:           in.Host,
		Branch:         in.Branch,
	}
}

func (c *localClient) RegisterAgent(ctx context.Context, repo *model.Repo, in inputs.AgentRegisterInput, dryRun bool) (*model.AgentSession, error) {
	if dryRun {
		return projectAgentSession(repo, in), nil
	}
	sess, err := c.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID:      in.SessionID,
		RepoID:         repo.ID,
		Actor:          in.Actor,
		Model:          in.Model,
		PermissionMode: in.PermissionMode,
		Host:           in.Host,
		Branch:         in.Branch,
	})
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "agent.register", Kind: "agent",
		TargetID: &sess.ID, TargetLabel: sess.SessionID,
		Details: agentRegisterDetails(sess),
	})
	return sess, nil
}

// HeartbeatAgent is the same store call as RegisterAgent but with
// fewer required fields and no audit row. session_id must already
// exist; we don't allow a heartbeat to implicitly create a session
// (an agent that never registered is a bug worth surfacing).
func (c *localClient) HeartbeatAgent(ctx context.Context, repo *model.Repo, in inputs.AgentHeartbeatInput, dryRun bool) (*model.AgentSession, error) {
	existing, err := c.store.GetAgentSession(in.SessionID)
	if err != nil {
		return nil, fmt.Errorf("heartbeat: %w (register first)", err)
	}
	if dryRun {
		// Reflect the heartbeat fields onto the existing row so the dry-run
		// output matches what the next write would land.
		projected := *existing
		if in.Model != "" {
			projected.Model = in.Model
		}
		if in.PermissionMode != "" {
			projected.PermissionMode = in.PermissionMode
		}
		if in.Branch != "" {
			projected.Branch = in.Branch
		}
		return &projected, nil
	}
	sess, err := c.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID:      in.SessionID,
		RepoID:         existing.RepoID,
		Actor:          existing.Actor,
		Model:          orDefault(in.Model, existing.Model),
		PermissionMode: orDefault(in.PermissionMode, existing.PermissionMode),
		Host:           existing.Host,
		Branch:         orDefault(in.Branch, existing.Branch),
	})
	if err != nil {
		return nil, err
	}
	// No recordOp — heartbeats run hundreds of times per session and
	// would flood the audit log.
	return sess, nil
}

func (c *localClient) EndAgent(ctx context.Context, repo *model.Repo, in inputs.AgentEndInput, dryRun bool) (*model.AgentSession, error) {
	if _, err := model.ParseEndReason(in.Reason); err != nil {
		return nil, err
	}
	existing, err := c.store.GetAgentSession(in.SessionID)
	if err != nil {
		return nil, err
	}
	if dryRun {
		projected := *existing
		now := time.Now().UTC()
		projected.EndedAt = &now
		projected.EndReason = in.Reason
		return &projected, nil
	}
	sess, err := c.store.EndAgentSession(in.SessionID, in.Reason)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &sess.RepoID, RepoPrefix: sess.RepoPrefix,
		Op: "agent.end", Kind: "agent",
		TargetID: &sess.ID, TargetLabel: sess.SessionID,
		Details: "reason=" + sess.EndReason,
	})
	return sess, nil
}

func (c *localClient) ClaimAgent(ctx context.Context, repo *model.Repo, in inputs.AgentClaimInput, dryRun bool) (*model.AgentClaim, error) {
	iss, err := c.GetIssueByKey(ctx, repo, in.IssueKey)
	if err != nil {
		return nil, err
	}
	if _, err := c.store.GetAgentSession(in.SessionID); err != nil {
		return nil, err
	}
	if dryRun {
		now := time.Now().UTC()
		return &model.AgentClaim{
			SessionID: in.SessionID,
			IssueID:   iss.ID,
			IssueKey:  iss.Key,
			ClaimedAt: now,
		}, nil
	}
	claim, err := c.store.AddAgentClaim(in.SessionID, iss.ID)
	if err != nil {
		return nil, err
	}
	// Audit against the *issue's* repo, not the cwd repo. Cross-repo
	// claims (working in BACI but claiming a DEMO-* issue) are valid;
	// recording them under the cwd repo would mis-attribute the audit
	// and make `bacio history --repo DEMO` miss the entry.
	issRepoID, issRepoPrefix := iss.RepoID, prefixFromIssueKey(iss.Key)
	c.recordOp(model.HistoryEntry{
		RepoID: &issRepoID, RepoPrefix: issRepoPrefix,
		Op: "agent.claim", Kind: "agent",
		TargetID: &claim.ID, TargetLabel: in.SessionID,
		Details: "issue=" + iss.Key,
	})
	return claim, nil
}

func (c *localClient) ReleaseAgent(ctx context.Context, repo *model.Repo, in inputs.AgentReleaseInput, dryRun bool) (*model.AgentClaim, error) {
	iss, err := c.GetIssueByKey(ctx, repo, in.IssueKey)
	if err != nil {
		return nil, err
	}
	if _, err := c.store.GetAgentSession(in.SessionID); err != nil {
		return nil, err
	}
	if dryRun {
		now := time.Now().UTC()
		return &model.AgentClaim{
			SessionID:  in.SessionID,
			IssueID:    iss.ID,
			IssueKey:   iss.Key,
			ReleasedAt: &now,
		}, nil
	}
	claim, err := c.store.ReleaseAgentClaim(in.SessionID, iss.ID)
	if err != nil {
		return nil, err
	}
	issRepoID, issRepoPrefix := iss.RepoID, prefixFromIssueKey(iss.Key)
	c.recordOp(model.HistoryEntry{
		RepoID: &issRepoID, RepoPrefix: issRepoPrefix,
		Op: "agent.release", Kind: "agent",
		TargetID: &claim.ID, TargetLabel: in.SessionID,
		Details: "issue=" + iss.Key,
	})
	return claim, nil
}

// prefixFromIssueKey returns the PREFIX portion of a canonical issue
// key. Falls back to "" on malformed input; recordOp tolerates an
// empty prefix.
func prefixFromIssueKey(key string) string {
	if i := strings.Index(key, "-"); i > 0 {
		return key[:i]
	}
	return ""
}

func (c *localClient) ListAgentSessions(ctx context.Context, f AgentSessionFilter) ([]*model.AgentSession, error) {
	sf := store.AgentSessionFilter{OnlyAlive: f.OnlyAlive, Since: f.Since}
	if f.Repo != nil {
		id := f.Repo.ID
		sf.RepoID = &id
	}
	return c.store.ListAgentSessions(sf)
}

func (c *localClient) ShowAgentSession(ctx context.Context, sessionID string) (*AgentSessionView, error) {
	sess, err := c.store.ResolveAgentSession(sessionID)
	if err != nil {
		return nil, err
	}
	claims, err := c.store.ListAgentClaims(sess.ID)
	if err != nil {
		return nil, err
	}
	return &AgentSessionView{Session: sess, Claims: claims}, nil
}

func agentRegisterDetails(sess *model.AgentSession) string {
	var parts []string
	if sess.Model != "" {
		parts = append(parts, "model="+sess.Model)
	}
	if sess.PermissionMode != "" {
		parts = append(parts, "mode="+sess.PermissionMode)
	}
	if sess.Branch != "" {
		parts = append(parts, "branch="+sess.Branch)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

