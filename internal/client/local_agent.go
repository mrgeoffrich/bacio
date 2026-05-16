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
		SessionID:  in.SessionID,
		RepoID:     repo.ID,
		RepoPrefix: repo.Prefix,
		AgentName:  in.Agent,
		Actor:      in.Actor,
		Model:      in.Model,
		Host:       in.Host,
		Branch:     in.Branch,
		StartedAt:  now,
		LastSeenAt: now,
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
		SessionID: in.SessionID,
		RepoID:    repo.ID,
		AgentID:   agentID,
		Actor:     in.Actor,
		Model:     in.Model,
		Host:      in.Host,
		Branch:    in.Branch,
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
			c.recordIdentityMintFailure(repo, err)
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
	err := fmt.Errorf("could not mint a unique agent identity after %d attempts", maxAttempts)
	c.recordIdentityMintFailure(repo, err)
	return "", err
}

// UnregisteredActor is the placeholder actor stamped on SessionStart
// stubs before the agent calls the bacio channel's `register` tool.
// register replaces it with the real agent identity slug.
const UnregisteredActor = "unregistered"

func (c *localClient) CreateSessionStub(ctx context.Context, repo *model.Repo, sessionID, host string, claudePID int64) (*model.AgentSession, error) {
	sess, err := c.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: sessionID,
		RepoID:    repo.ID,
		Actor:     UnregisteredActor,
		Host:      host,
		ClaudePID: claudePID,
		// No agent_id, model, permission_mode, branch, channel_version.
		// MarkRegistered=false: registered_at stays NULL.
	})
	if err != nil {
		return nil, err
	}
	// Stub creation deliberately does NOT emit an audit row — the
	// SessionStart hook fires for every session including ones that
	// never register (no bacio channel loaded), and flooding history
	// with stubs would drown real events. The `agent.register` audit
	// row fires from CompleteRegistration instead.
	return sess, nil
}

func (c *localClient) SessionsByClaudePID(ctx context.Context, host string, claudePID int64) ([]*model.AgentSession, error) {
	return c.store.SessionsByClaudePID(host, claudePID)
}

func (c *localClient) CompleteRegistration(ctx context.Context, repo *model.Repo, in inputs.AgentRegisterInput, channelVersion string) (*model.AgentSession, error) {
	// Resolve or mint the agent identity. Empty in.Agent means "mint a
	// fresh one"; non-empty looks up (or creates if NewIdentity is set).
	var agentID *int64
	var agentCreated bool
	if in.Agent == "" {
		slug, err := c.EnsureAgentIdentity(ctx, repo)
		if err != nil {
			return nil, err
		}
		in.Agent = slug
		// EnsureAgentIdentity recorded the create + adopted slug as
		// audit actor; resolve the id.
		ag, err := c.store.GetAgentByName(slug)
		if err != nil {
			return nil, err
		}
		agentID = &ag.ID
	} else {
		ag, created, err := c.store.UpsertAgent(in.Agent, in.NewIdentity)
		if err != nil {
			return nil, err
		}
		agentID = &ag.ID
		agentCreated = created
	}
	in.Actor = in.Agent
	c.actor = in.Agent // adopt for the subsequent audit rows
	sess, err := c.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID:      in.SessionID,
		RepoID:         repo.ID,
		AgentID:        agentID,
		Actor:          in.Actor,
		Model:          in.Model,
		Host:           in.Host,
		Branch:         in.Branch,
		ChannelVersion: channelVersion,
		MarkRegistered: true,
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

// recordIdentityMintFailure writes a paper-trail audit row when
// EnsureAgentIdentity gives up. The hook swallows the error (a hook
// MUST NOT fail the agent's session), so without this row a lost
// identity mint disappears into stderr — the way we discovered the
// SQLite-busy regression that motivated busy_timeout(5000). Best
// effort: recordOp itself logs and continues on write failure, so a
// fully-locked DB still leaves no row, but any partial degradation
// leaves a trail. Actor is "unknown" rather than the OS user so the
// row is unambiguously a system event, not user activity.
func (c *localClient) recordIdentityMintFailure(repo *model.Repo, cause error) {
	entry := model.HistoryEntry{
		Op:      "agent.identity.create_failed",
		Kind:    "agent",
		Actor:   "unknown",
		Details: cause.Error(),
	}
	if repo != nil {
		entry.RepoID = &repo.ID
		entry.RepoPrefix = repo.Prefix
	}
	c.recordOp(entry)
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
		if in.Branch != "" {
			projected.Branch = in.Branch
		}
		return &projected, nil
	}
	sess, err := c.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: in.SessionID,
		RepoID:    existing.RepoID,
		Actor:     existing.Actor,
		Model:     orDefault(in.Model, existing.Model),
		Host:      existing.Host,
		Branch:    orDefault(in.Branch, existing.Branch),
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
	sess, assigneeChanges, err := c.store.EndAgentSession(in.SessionID, in.Reason)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &sess.RepoID, RepoPrefix: sess.RepoPrefix,
		Op: "agent.end", Kind: "agent",
		TargetID: &sess.ID, TargetLabel: sess.SessionID,
		Details: "reason=" + sess.EndReason,
	})
	// Auto-releasing claims may have unassigned issues — audit each one.
	for _, ch := range assigneeChanges {
		c.recordAssigneeChange(ch)
	}
	return sess, nil
}

// recordAssigneeChange writes the issue.assign audit row for an
// issues.assignee mutation a claim / release / end made as a side
// effect of keeping the claim and the assignee in lockstep. A no-op
// change (Old == New) records nothing. Detail strings mirror the
// AssignIssue / UnassignIssue conventions so `bacio history` reads
// consistently regardless of which path moved the assignee.
func (c *localClient) recordAssigneeChange(ch store.AssigneeChange) {
	if !ch.Changed() {
		return
	}
	var details string
	switch {
	case ch.New == "":
		details = fmt.Sprintf("%s → (unassigned)", ch.Old)
	case ch.Old == "":
		details = "assigned to " + ch.New
	default:
		details = fmt.Sprintf("%s → %s", ch.Old, ch.New)
	}
	repoID, issueID := ch.RepoID, ch.IssueID
	c.recordOp(model.HistoryEntry{
		RepoID: &repoID, RepoPrefix: ch.RepoPrefix,
		Op: "issue.assign", Kind: "issue",
		TargetID: &issueID, TargetLabel: ch.IssueKey,
		Details: details,
	})
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
			Prompt:    in.Prompt,
			ClaimedAt: now,
		}, nil
	}
	claim, created, assigneeChange, err := c.store.AddAgentClaim(in.SessionID, iss.ID, in.Prompt)
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
	// A fresh claim also stamps the assignee — audit that move too.
	if assigneeChange != nil {
		c.recordAssigneeChange(*assigneeChange)
	}
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
	claim, assigneeChange, err := c.store.ReleaseAgentClaim(in.SessionID, iss.ID)
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
	// Releasing the last open claim unassigns the issue — audit that.
	if assigneeChange != nil {
		c.recordAssigneeChange(*assigneeChange)
	}
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
	sf := store.AgentSessionFilter{
		OnlyAlive:      f.OnlyAlive,
		RegisteredOnly: f.RegisteredOnly,
		Since:          f.Since,
	}
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

// ListOpenClaims flattens store.OpenClaimsBySession (which buckets open
// claims by session) into a flat slice. With repo == nil it concatenates
// across every tracked repo. One query per repo — no N+1.
func (c *localClient) ListOpenClaims(ctx context.Context, repo *model.Repo) ([]*model.AgentClaim, error) {
	collect := func(repoID int64) ([]*model.AgentClaim, error) {
		bySession, err := c.store.OpenClaimsBySession(repoID)
		if err != nil {
			return nil, err
		}
		var out []*model.AgentClaim
		for _, claims := range bySession {
			out = append(out, claims...)
		}
		return out, nil
	}
	if repo != nil {
		return collect(repo.ID)
	}
	repos, err := c.store.ListRepos()
	if err != nil {
		return nil, err
	}
	var out []*model.AgentClaim
	for _, r := range repos {
		claims, err := collect(r.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, claims...)
	}
	return out, nil
}

func agentRegisterDetails(sess *model.AgentSession) string {
	var parts []string
	if sess.AgentName != "" {
		parts = append(parts, "agent="+sess.AgentName)
	}
	if sess.Model != "" {
		parts = append(parts, "model="+sess.Model)
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

