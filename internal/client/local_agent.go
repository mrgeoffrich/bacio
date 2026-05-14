package client

import (
	"context"
	"errors"
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
// StartedAt / LastSeenAt are stamped with now so dry-run output doesn't
// surface the zero time (`0001-01-01 …`). For a refresh upsert,
// StartedAt would actually be preserved by the real write — we don't
// look that up here to keep dry-run a pure projection, and the
// LastSeenAt-equals-now case is the honest one for "what would the row
// look like immediately after this write".
func projectAgentSession(repo *model.Repo, in inputs.AgentRegisterInput) *model.AgentSession {
	now := time.Now().UTC()
	return &model.AgentSession{
		SessionID:      in.SessionID,
		RepoID:         repo.ID,
		RepoPrefix:     repo.Prefix,
		AgentName:      in.Agent,
		Actor:          in.Actor,
		Model:          in.Model,
		PermissionMode: in.PermissionMode,
		Host:           in.Host,
		Branch:         in.Branch,
		StartedAt:      now,
		LastSeenAt:     now,
	}
}

func (c *localClient) RegisterAgent(ctx context.Context, repo *model.Repo, in inputs.AgentRegisterInput, dryRun bool) (*model.AgentSession, error) {
	// Resolve agent identity (optional). When --new is set we must
	// commit the agents row *before* the session upsert so that the
	// FK on agent_sessions.agent_id resolves to a real row; dry-run
	// short-circuits before any write hits the DB. On clash the store
	// returns ErrAgentNameTaken and we surface it verbatim so the
	// agent loop can detect the case (`errors.Is(..., ErrAgentNameTaken)`)
	// and retry with a fresh slug.
	var agentID *int64
	var agentCreated bool
	if in.Agent != "" && !dryRun {
		ag, created, err := c.store.UpsertAgent(in.Agent, in.NewIdentity)
		if err != nil {
			return nil, err
		}
		agentID = &ag.ID
		agentCreated = created
	}
	if dryRun {
		return projectAgentSession(repo, in), nil
	}
	sess, err := c.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID:      in.SessionID,
		RepoID:         repo.ID,
		AgentID:        agentID,
		Actor:          in.Actor,
		Model:          in.Model,
		PermissionMode: in.PermissionMode,
		Host:           in.Host,
		Branch:         in.Branch,
	})
	if err != nil {
		return nil, err
	}
	if agentCreated {
		c.recordOp(model.HistoryEntry{
			RepoID: &repo.ID, RepoPrefix: repo.Prefix,
			Op: "agent.identity.create", Kind: "agent",
			TargetID: agentID, TargetLabel: in.Agent,
		})
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "agent.register", Kind: "agent",
		TargetID: &sess.ID, TargetLabel: sess.SessionID,
		Details: agentRegisterDetails(sess),
	})
	return sess, nil
}

// EnsureAgentIdentity mints a fresh persistent identity for a `claude`
// process that has no entry in .bacio/agents.json yet. It rerolls the
// random slug against the agents.name UNIQUE constraint (UpsertAgent
// with requireNew) until one sticks — so two agents bootstrapping on
// the same host at the same instant can't end up sharing a row. On
// success it adopts the slug as this client's audit actor, so the
// session-start hook's subsequent register/dispatch audit rows
// attribute to the agent rather than the OS user the hook runs as. The
// caller is responsible for recording the slug into .bacio/agents.json.
func (c *localClient) EnsureAgentIdentity(ctx context.Context, repo *model.Repo) (string, error) {
	const maxAttempts = 20
	for i := 0; i < maxAttempts; i++ {
		cand := model.GenerateAgentSlug()
		ag, _, err := c.store.UpsertAgent(cand, true)
		if err != nil {
			if errors.Is(err, store.ErrAgentNameTaken) {
				continue // slug clashed with a live identity — reroll
			}
			return "", err
		}
		c.actor = cand // adopt the minted identity for subsequent audit rows
		entry := model.HistoryEntry{
			Op: "agent.identity.create", Kind: "agent",
			TargetID: &ag.ID, TargetLabel: cand,
		}
		if repo != nil {
			entry.RepoID = &repo.ID
			entry.RepoPrefix = repo.Prefix
		}
		c.recordOp(entry)
		return cand, nil
	}
	return "", fmt.Errorf("could not mint a unique agent identity after %d attempts", maxAttempts)
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
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("issue %s does not exist in repo %s", in.IssueKey, repo.Prefix)
		}
		return nil, err
	}
	if _, err := c.store.GetAgentSession(in.SessionID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("session %q is not registered — run `bacio agent register` first", in.SessionID)
		}
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
	claim, created, err := c.store.AddAgentClaim(in.SessionID, iss.ID)
	if err != nil {
		return nil, err
	}
	// Re-claiming an issue this session already has open is a no-op
	// in the DB — skip the audit row too, otherwise a `claim` poll loop
	// floods the history table with duplicate entries.
	if !created {
		return claim, nil
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
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("issue %s does not exist in repo %s", in.IssueKey, repo.Prefix)
		}
		return nil, err
	}
	if _, err := c.store.GetAgentSession(in.SessionID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("session %q is not registered — run `bacio agent register` first", in.SessionID)
		}
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
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("session %q has no open claim on %s", in.SessionID, iss.Key)
		}
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
	if sess.AgentName != "" {
		parts = append(parts, "agent="+sess.AgentName)
	}
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

