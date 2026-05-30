package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/pipeline"
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
	// BACI-100: validate the session_id UUID shape up front so a
	// --dry-run register of a malformed id rejects too (projectAgentSession
	// doesn't validate). The non-dry-run path re-checks via RequireUUID.
	if _, err := store.ValidateSessionUUID(in.SessionID); err != nil {
		return nil, err
	}
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
		// BACI-100: `bacio agent register` is a register entry point —
		// require a structurally valid UUID session_id here too.
		RequireUUID: true,
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
	// BACI-100: validate the session_id UUID shape up front, before any
	// identity-mint or dedupe side effects. A fat-fingered, non-UUID
	// session_id (the bug this ticket fixes) must be rejected without
	// reconciling away the genuine live row first.
	if _, err := store.ValidateSessionUUID(in.SessionID); err != nil {
		return nil, err
	}
	// BACI-100 dedupe: when the caller supplied a claude_pid, reconcile
	// any pre-existing live session rows for that OS process whose
	// session_id differs from the incoming one. These are phantom rows —
	// a single `claude` process should hold exactly one live registry
	// row. End them with reason "superseded" so they drop out of the
	// live set; the register then proceeds against the incoming id.
	if in.ClaudePID != 0 {
		if err := c.supersedeStaleSessions(in.Host, in.ClaudePID, in.SessionID, repo); err != nil {
			return nil, err
		}
	}
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
		ClaudePID:      in.ClaudePID,
		ChannelVersion: channelVersion,
		MarkRegistered: true,
		RequireUUID:    true,
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

// supersedeStaleSessions reaps the phantom registry rows the BACI-100
// dedupe targets: any *other* live session (ended_at IS NULL) for the
// same (host, claude_pid) whose session_id differs from keepID. A single
// `claude` process should hold exactly one live registry row — the
// genuine session that keeps heartbeating. The extras are typically a
// fat-fingered register retry (a different, wrong UUID) that minted a
// row no one will ever heartbeat or end. End each with reason
// "superseded" so it drops out of the live set and can't be picked as a
// dispatch target; the caller then proceeds with the register against
// keepID. Best-effort per row: a failure to end one phantom is logged
// and doesn't fail the register.
func (c *localClient) supersedeStaleSessions(host string, claudePID int64, keepID string, repo *model.Repo) error {
	live, err := c.store.SessionsByClaudePID(host, claudePID)
	if err != nil {
		return err
	}
	for _, s := range live {
		if s.SessionID == keepID {
			continue
		}
		// Supersede path: end the phantom row with an empty orphanState so
		// the claim cascade leaves issue state alone. The phantom session
		// only exists because a non-UUID register clobbered it; its
		// claims (if any) should not be retroactively state-mutated, and
		// the dispatch cascade must NOT re-queue (BACI-133's requeue is
		// reserved for the reaper on a real session that went dark — a
		// phantom never owned the work).
		ended, _, _, _, abandonedQuestions, err := c.store.EndAgentSession(s.SessionID, string(model.EndReasonSuperseded), "", store.DispatchCascadeCancel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bacio: superseding stale session %s: %v\n", s.SessionID, err)
			continue
		}
		if ended != nil {
			c.recordOp(model.HistoryEntry{
				RepoID: &repo.ID, RepoPrefix: repo.Prefix,
				Op: "agent.end", Kind: "agent",
				TargetID: &ended.ID, TargetLabel: ended.SessionID,
				Details: fmt.Sprintf("reason=superseded (claude_pid %d re-registered as %s)", claudePID, keepID),
			})
			// BACI-253: any open questions the phantom owned are now
			// abandoned in the same tx — emit the matching summary row
			// so the audit log mirrors the channel-startup janitor.
			if abandonedQuestions > 0 {
				c.recordOp(model.HistoryEntry{
					RepoID: &repo.ID, RepoPrefix: repo.Prefix,
					Op: "question.abandon", Kind: "question",
					TargetLabel: ended.SessionID,
					Details:     fmt.Sprintf("session=%s,count=%d", ended.SessionID, abandonedQuestions),
				})
			}
		}
	}
	return nil
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

// MarkAgentErrored stamps the StopFailure error class + message on the
// session row (BACI-296). No recordOp — the StopFailure hook is a
// fail-open observe-only path and the errored state is transient
// supervision metadata, not an audit-worthy mutation; the engine's
// job.failed advance (FailPipelineForSession below) is what lands in the
// history log.
func (c *localClient) MarkAgentErrored(ctx context.Context, sessionID, errType, errMsg string) error {
	return c.store.SetAgentSessionErrored(sessionID, errType, errMsg)
}

// ClearAgentError wipes the errored-state columns on the next successful
// heartbeat (recovery). No recordOp for the same reason as the heartbeat
// path it rides on.
func (c *localClient) ClearAgentError(ctx context.Context, sessionID string) error {
	return c.store.ClearAgentSessionError(sessionID)
}

// FailPipelineForSession reconciles the in-flight Pipeline job of the
// session that just took an Anthropic API failure (BACI-296). It walks
// the session's open claims, and for each whose issue is in_pipeline with
// a running job, drives the engine's FailRunning branch (transient →
// pause the chain in place; terminal → move the card to needs_action).
// Each committed engine transition is mirrored into the audit log as an
// `engine.advance` row, matching the controller's own advance-writing
// shape. Best-effort and bounded: a session normally holds exactly one
// open pipeline claim, but the loop tolerates the rare multi-claim case.
func (c *localClient) FailPipelineForSession(ctx context.Context, sessionID, errType, errMsg string) error {
	claims, err := c.store.OpenClaimsForSession(sessionID)
	if err != nil {
		return err
	}
	eng := pipeline.New(c.store)
	for _, cl := range claims {
		if cl == nil {
			continue
		}
		advances, err := eng.FailRunning(cl.IssueID, errType, errMsg)
		if err != nil {
			return err
		}
		for _, adv := range advances {
			repoID := adv.RepoID
			c.recordOp(model.HistoryEntry{
				RepoID: &repoID, RepoPrefix: adv.RepoPrefix,
				Op: "engine.advance", Kind: "engine",
				TargetID: &adv.IssueID, TargetLabel: adv.IssueKey,
				Details: strings.TrimSpace(adv.Kind + " " + adv.Detail),
			})
		}
	}
	return nil
}

func (c *localClient) EndAgent(ctx context.Context, repo *model.Repo, in inputs.AgentEndInput, dryRun bool) (*model.AgentSession, error) {
	if _, err := model.ParseEndReason(in.Reason); err != nil {
		return nil, err
	}
	// BACI-300: state_on_orphan defaults to "" (leave the state alone) —
	// a claim is a focus marker now, so an abandoned claim's issue stays
	// exactly where it was rather than being flipped to the retired
	// in_progress. A caller can still pass an explicit override (validated
	// via ParseState so dash/space variants work).
	var orphanState model.State
	if strings.TrimSpace(in.StateOnOrphan) != "" {
		parsed, err := model.ParseState(in.StateOnOrphan)
		if err != nil {
			return nil, fmt.Errorf("state_on_orphan: %w", err)
		}
		orphanState = parsed
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
	// BACI-133: derive the dispatch cascade from the end reason — only
	// the reaper-driven presumed_dead path re-queues; every other end
	// reason (hook-driven SessionEnd via mapEndReason, operator-driven
	// `bacio agent end`, supersede via the dedicated call site above)
	// keeps today's cancel-on-end semantics. The store validates the
	// pairing — Requeue with any other reason is rejected at the
	// boundary, so this two-line derivation is the only place that
	// decides.
	cascade := store.DispatchCascadeCancel
	if model.EndReason(in.Reason) == model.EndReasonPresumedDead {
		cascade = store.DispatchCascadeRequeue
	}
	sess, assigneeChanges, stateChanges, cascadeInfos, abandonedQuestions, err := c.store.EndAgentSession(in.SessionID, in.Reason, orphanState, cascade)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &sess.RepoID, RepoPrefix: sess.RepoPrefix,
		Op: "agent.end", Kind: "agent",
		TargetID: &sess.ID, TargetLabel: sess.SessionID,
		Details: "reason=" + sess.EndReason,
	})
	// BACI-253: emit the summary `question.abandon` row when the
	// end-session cascade settled at least one parked question. The
	// store flipped the rows inside its tx so there's no orphan-row
	// window between the agent end and the abandon; here we just
	// record the audit trail mirroring the channel-startup janitor.
	if abandonedQuestions > 0 {
		c.recordOp(model.HistoryEntry{
			RepoID: &sess.RepoID, RepoPrefix: sess.RepoPrefix,
			Op: "question.abandon", Kind: "question",
			TargetLabel: sess.SessionID,
			Details:     fmt.Sprintf("session=%s,count=%d", sess.SessionID, abandonedQuestions),
		})
	}
	// Auto-releasing claims may have unassigned issues — audit each one.
	for _, ch := range assigneeChanges {
		c.recordAssigneeChange(ch)
	}
	// BACI-126c: a cascaded release that moved an issue's state writes a
	// per-issue `issue.state` audit row tagged with the auto-release
	// reason so a reader can distinguish it from a deliberate state move.
	for _, sc := range stateChanges {
		c.recordOrphanStateChange(sc, sess.SessionID)
	}
	// BACI-58 §B / BACI-133 — record one per-row history entry for every
	// dispatch the end-session tx touched. Branch on the cascade's
	// NewStatus: cancelled rows write `agent.cancel` (the "auto-cancel:"
	// prefix lets `bacio history --op agent.cancel` distinguish
	// reaper-driven cancels from manual `bacio agent cancel`s), queued
	// rows write `agent.dispatch.requeue` (BACI-133 reaper recovery —
	// actor=bacio-channel-ping so `bacio history --user-filter
	// bacio-channel-ping` returns a coherent reaper-activity ledger).
	for _, info := range cascadeInfos {
		switch info.NewStatus {
		case model.DispatchCancelled:
			c.recordOp(model.HistoryEntry{
				RepoID: &info.RepoID, RepoPrefix: info.RepoPrefix,
				Op: "agent.cancel", Kind: "agent",
				TargetID: &info.ID, TargetLabel: cancelledDispatchTargetLabel(info),
				Details:  cancelledDispatchDetails(info, "auto-cancel: target session ended"),
			})
		case model.DispatchQueued:
			c.recordOp(model.HistoryEntry{
				RepoID: &info.RepoID, RepoPrefix: info.RepoPrefix,
				Actor:  string(model.IdlePingDispatchCreator),
				Op:     "agent.dispatch.requeue", Kind: "agent",
				TargetID: &info.ID, TargetLabel: cancelledDispatchTargetLabel(info),
				Details:  cancelledDispatchDetails(info, "auto-requeue: target session reaped (presumed_dead)"),
			})
		}
	}
	return sess, nil
}

// recordOrphanStateChange writes the issue.state audit row for a state
// move made as a side effect of a cascaded `agent end` release
// (BACI-126c). Mirrors recordAssigneeChange's shape; the details
// string names the originating session and the auto-release reason so
// `bacio history --op issue.state` makes the cascade visible.
func (c *localClient) recordOrphanStateChange(ch store.StateChange, sessionID string) {
	if !ch.Changed() {
		return
	}
	repoID, issueID := ch.RepoID, ch.IssueID
	c.recordOp(model.HistoryEntry{
		RepoID: &repoID, RepoPrefix: ch.RepoPrefix,
		Op: "issue.state", Kind: "issue",
		TargetID: &issueID, TargetLabel: ch.IssueKey,
		Details: fmt.Sprintf("%s → %s (auto: session %s ended)", ch.Old, ch.New, sessionID),
	})
}

// cancelledDispatchTargetLabel / cancelledDispatchDetails shape an
// agent.cancel audit row for a dispatch the store auto-cancelled
// (BACI-58 §B) starting from the lightweight store.CancelledDispatchInfo,
// without round-tripping through GetDispatch. Output mirrors the
// dispatchTargetLabel / dispatchDetails helpers above so manual and
// auto-cancels are visually consistent in `bacio history`.
func cancelledDispatchTargetLabel(info store.CancelledDispatchInfo) string {
	if info.TargetAgentName != "" {
		return info.TargetAgentName
	}
	return info.TargetSessionID
}

func cancelledDispatchDetails(info store.CancelledDispatchInfo, reason string) string {
	var parts []string
	parts = append(parts, reason)
	if info.IssueKey != "" {
		parts = append(parts, "issue="+info.IssueKey)
	}
	if info.TargetSessionID != "" {
		parts = append(parts, "session="+info.TargetSessionID)
	}
	if info.Mode != "" {
		parts = append(parts, "mode="+info.Mode)
	}
	parts = append(parts, "status=cancelled")
	return joinCSV(parts)
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
		// BACI-300: a claim is state-neutral — it no longer moves the
		// issue. Project before == after == the issue's current state so
		// the rehearsal mirrors the live no-move claim.
		return &model.AgentClaim{
			SessionID:        in.SessionID,
			IssueID:          iss.ID,
			IssueKey:         iss.Key,
			Prompt:           in.Prompt,
			ClaimedAt:        now,
			IssueStateBefore: iss.State,
			IssueStateAfter:  iss.State,
		}, nil
	}
	claim, created, assigneeChange, stateChange, err := c.store.AddAgentClaim(in.SessionID, iss.ID, in.Prompt)
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
		Details: claimAuditDetails(iss.Key, stateChange),
	})
	// A fresh claim also stamps the assignee — audit that move too.
	if assigneeChange != nil {
		c.recordAssigneeChange(*assigneeChange)
	}
	// BACI-126a: surface the post-claim state on the returned object so
	// callers have it without a second read. JSON-only — not a column on
	// the claim row. Since BACI-300 a claim never moves the state, so
	// before == after here.
	if stateChange != nil {
		claim.IssueStateBefore = stateChange.Old
		claim.IssueStateAfter = stateChange.New
	}
	return claim, nil
}

// claimAuditDetails formats the agent.claim audit row's Details column
// (BACI-126a). Since BACI-300 a claim is state-neutral, so ch.Changed()
// is always false and the line is just `issue=<KEY>`; the state-move
// branch is retained for any caller that still passes a moving change.
func claimAuditDetails(issueKey string, ch *store.StateChange) string {
	if ch == nil || !ch.Changed() {
		return "issue=" + issueKey
	}
	return fmt.Sprintf("issue=%s, state: %s → %s", issueKey, ch.Old, ch.New)
}

func (c *localClient) ReleaseAgent(ctx context.Context, repo *model.Repo, in inputs.AgentReleaseInput, dryRun bool) (*model.AgentClaim, error) {
	// Pipeline cutover: final_state is OPTIONAL. The controller engine
	// owns the state of pipeline cards, so a pipeline-stage worker
	// releases the claim only — it no longer declares a final state. We
	// resolve the issue first, then default an empty final_state via
	// model.ReleaseFallbackState, which since BACI-300 is a no-op for
	// every card (a release never moves the state — the card stays exactly
	// where it was). A non-empty final_state still moves the issue — a
	// caller that genuinely wants to land the card somewhere (e.g. a
	// triage pass marking a card done) keeps declaring it.
	iss, err := c.GetIssueByKey(ctx, repo, in.IssueKey)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("issue %s does not exist in repo %s", in.IssueKey, repo.Prefix)
		}
		return nil, err
	}
	finalState := model.ReleaseFallbackState(iss.State)
	if s := strings.TrimSpace(in.FinalState); s != "" {
		parsed, perr := model.ParseState(s)
		if perr != nil {
			return nil, fmt.Errorf("final_state: %w", perr)
		}
		finalState = parsed
	}
	if _, err := c.store.GetAgentSession(in.SessionID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("session %q is not registered — run `bacio agent register` first", in.SessionID)
		}
		return nil, err
	}
	if dryRun {
		now := time.Now().UTC()
		// BACI-126c: project the post-release state so the rehearsal
		// mirrors the live call.
		return &model.AgentClaim{
			SessionID:        in.SessionID,
			IssueID:          iss.ID,
			IssueKey:         iss.Key,
			ReleasedAt:       &now,
			IssueStateBefore: iss.State,
			IssueStateAfter:  finalState,
		}, nil
	}
	claim, assigneeChange, stateChange, err := c.store.ReleaseAgentClaim(in.SessionID, iss.ID, finalState)
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
		Details: releaseAuditDetails(iss.Key, stateChange),
	})
	// Releasing the last open claim unassigns the issue — audit that.
	if assigneeChange != nil {
		c.recordAssigneeChange(*assigneeChange)
	}
	// BACI-126c: surface the post-release state on the returned object so
	// callers can render `release BACI-42 (todo → todo)` without a second
	// read. Since BACI-300 an undeclared release leaves the state put.
	if stateChange != nil {
		claim.IssueStateBefore = stateChange.Old
		claim.IssueStateAfter = stateChange.New
	}
	return claim, nil
}

// releaseAuditDetails formats the agent.release audit row's Details
// column (BACI-126c). Same shape as claimAuditDetails — the line is
// `issue=<KEY>, state: <old> → <new>` when the release moved state,
// or `issue=<KEY>` when state was already at the target.
func releaseAuditDetails(issueKey string, ch *store.StateChange) string {
	if ch == nil || !ch.Changed() {
		return "issue=" + issueKey
	}
	return fmt.Sprintf("issue=%s, state: %s → %s", issueKey, ch.Old, ch.New)
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

// OpenClaimsForSession returns the canonical issue keys of every open
// claim held by sessionID, for the BACI-126b issue-group gate. Empty
// slice when the session is unknown / ended / claimless. Single store
// query.
func (c *localClient) OpenClaimsForSession(ctx context.Context, sessionID string) ([]string, error) {
	claims, err := c.store.OpenClaimsForSession(sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(claims))
	for _, cl := range claims {
		if cl == nil || cl.IssueKey == "" {
			continue
		}
		out = append(out, cl.IssueKey)
	}
	return out, nil
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

// UpsertSessionTodoFromTask / ListSessionTodos /
// ListTodosBySessionsAndIssue are thin pass-throughs to the store. The
// post-tool-use hook drives the write side (one row per TaskCreate /
// TaskUpdate event); the desktop/TUI agent views drive the read side.
// No audit row — Task* is high-frequency, like heartbeats; flooding
// `bacio history` would drown the audit log.
func (c *localClient) UpsertSessionTodoFromTask(ctx context.Context, sessionID, taskID, issueKey, content string, status model.TodoStatus, dispatchID *int64) error {
	return c.store.UpsertSessionTodoFromTask(sessionID, taskID, issueKey, content, status, dispatchID)
}

func (c *localClient) ListSessionTodos(ctx context.Context, sessionID, issueKey string) ([]model.SessionTodo, error) {
	return c.store.ListSessionTodos(sessionID, issueKey)
}

func (c *localClient) ListTodosBySessionsAndIssue(ctx context.Context, pairs []store.SessionIssuePair) (map[int64][]model.SessionTodo, error) {
	return c.store.ListTodosBySessionsAndIssue(pairs)
}

// AddSessionQuestion is the channel's entry point for the BACI-53
// ask_user_question MCP tool. The channel resolves session_id /
// agent identity / issue id on its side (BACI-128 — issue_id is a
// required, validated MCP tool arg), then hands the validated
// payload to the store. Writes a question.ask audit row so `bacio
// history --op question.ask` surfaces every ask. BACI-300 retired the
// off-pipeline in_progress→needs_action auto-flip — the kanban-card
// question pill (and, for a pipeline card, the engine's open_question
// pause) surfaces "agent is blocked" without a state move.
func (c *localClient) AddSessionQuestion(ctx context.Context, in AddSessionQuestionInput) (*model.SessionQuestion, error) {
	// BACI-128 boundary guard: every legitimate caller now threads a
	// validated canonical key through from the channel. Reject empty
	// here defensively so a future caller that regresses to the old
	// "best-effort open-claim lookup" behaviour fails loud instead of
	// quietly inserting an orphan row the kanban surface can't see.
	if strings.TrimSpace(in.IssueKey) == "" {
		return nil, fmt.Errorf("AddSessionQuestion: issue_key is required (the BACI-128 validator caller must thread it through)")
	}
	if _, _, err := store.ParseIssueKey(in.IssueKey); err != nil {
		return nil, fmt.Errorf("AddSessionQuestion: %w", err)
	}
	q, err := c.store.AddSessionQuestion(store.AddSessionQuestionIn{
		SessionID: in.SessionID,
		IssueKey:  in.IssueKey,
		Payload:   in.Payload,
		AskedBy:   in.AskedBy,
	})
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		Actor: in.AskedBy, Op: "question.ask", Kind: "question",
		TargetID: &q.ID, TargetLabel: q.RequestUUID,
		Details: questionAuditDetails(q),
	})
	return q, nil
}

func (c *localClient) ListSessionQuestions(ctx context.Context, sessionID string, states []model.QuestionState) ([]*model.SessionQuestion, error) {
	return c.store.ListSessionQuestions(sessionID, states)
}

func (c *localClient) ListOpenQuestionsBySessions(ctx context.Context, sessionIDs []string) (map[int64][]*model.SessionQuestion, error) {
	return c.store.ListOpenQuestionsBySessions(sessionIDs)
}

func (c *localClient) PipelineJobsForIssues(ctx context.Context, issueIDs []int64) (map[int64][]*model.PipelineJob, error) {
	return c.store.PipelineJobsForIssues(issueIDs)
}

func (c *localClient) GetSessionQuestion(ctx context.Context, id int64) (*model.SessionQuestion, error) {
	return c.store.GetSessionQuestion(id)
}

// AnswerSessionQuestion submits the user's answer. dryRun runs
// every validator (payload-shape, state-guard) but writes nothing
// — the returned row is the existing open row, unchanged.
func (c *localClient) AnswerSessionQuestion(ctx context.Context, id int64, answers model.QuestionAnswers, dryRun bool) (*model.SessionQuestion, error) {
	current, err := c.store.GetSessionQuestion(id)
	if err != nil {
		return nil, err
	}
	if current.State != model.QuestionOpen {
		return nil, fmt.Errorf("question %d is %s; only open questions can be answered", id, current.State)
	}
	if err := model.ValidateQuestionAnswers(current.Payload, answers); err != nil {
		return nil, err
	}
	if dryRun {
		// Project the post-write shape so the caller sees what a real
		// answer would look like, without touching the row.
		projected := *current
		projected.State = model.QuestionAnswered
		projected.Answers = answers
		now := time.Now().UTC()
		projected.AnsweredAt = &now
		projected.AnsweredBy = c.actor
		return &projected, nil
	}
	updated, err := c.store.AnswerSessionQuestion(id, answers, c.actor)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		Op: "question.answer", Kind: "question",
		TargetID: &updated.ID, TargetLabel: updated.RequestUUID,
		Details: questionAuditDetails(updated),
	})
	return updated, nil
}

// CancelSessionQuestion dismisses an open question. dryRun runs
// the state guard but writes nothing.
func (c *localClient) CancelSessionQuestion(ctx context.Context, id int64, dryRun bool) (*model.SessionQuestion, error) {
	current, err := c.store.GetSessionQuestion(id)
	if err != nil {
		return nil, err
	}
	if current.State != model.QuestionOpen {
		return nil, fmt.Errorf("question %d is %s; only open questions can be cancelled", id, current.State)
	}
	if dryRun {
		projected := *current
		projected.State = model.QuestionCancelled
		now := time.Now().UTC()
		projected.AnsweredAt = &now
		projected.AnsweredBy = c.actor
		return &projected, nil
	}
	updated, err := c.store.CancelSessionQuestion(id, c.actor)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		Op: "question.cancel", Kind: "question",
		TargetID: &updated.ID, TargetLabel: updated.RequestUUID,
		Details: questionAuditDetails(updated),
	})
	return updated, nil
}

// AbandonOpenQuestionsForSession flips every open question owned by
// sessionID to `abandoned` — janitor work the channel runs at startup
// (BACI-160 gap 4). Writes ONE summary `question.abandon` history row
// when N>0; a sweep that found no rows produces no audit noise. The
// summary row matches the channel-restart cadence (rare, high-signal):
// per-question rows would drown the audit log when a channel restarts
// after a long parked-question backlog. Actor falls through to
// recordOp's normal resolution (the channel's actor / "bacio-channel").
func (c *localClient) AbandonOpenQuestionsForSession(ctx context.Context, sessionID string) (int, error) {
	n, err := c.store.AbandonOpenQuestionsForSession(sessionID)
	if err != nil {
		return n, err
	}
	if n > 0 {
		c.recordOp(model.HistoryEntry{
			Op: "question.abandon", Kind: "question",
			TargetLabel: sessionID,
			Details:     fmt.Sprintf("session=%s,count=%d", sessionID, n),
		})
	}
	return n, nil
}

func (c *localClient) DrainSettledQuestionsForSession(ctx context.Context, sessionID string) ([]*model.SessionQuestion, error) {
	return c.store.DrainSettledQuestionsForSession(sessionID)
}

// questionAuditDetails composes a short human-readable line for
// the audit-log Details column. Mirrors the dispatch convention —
// pick out the high-signal fields without dumping the full payload.
func questionAuditDetails(q *model.SessionQuestion) string {
	parts := []string{"session=" + q.SessionID}
	if q.IssueKey != "" {
		parts = append(parts, "issue="+q.IssueKey)
	}
	parts = append(parts, fmt.Sprintf("state=%s", q.State))
	if n := len(q.Payload.Questions); n > 0 {
		parts = append(parts, fmt.Sprintf("questions=%d", n))
	}
	return strings.Join(parts, " ")
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

