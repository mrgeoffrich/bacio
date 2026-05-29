package api

// HTTP parity for the agent-registry verbs. Same inputs.*Input structs,
// validators, store calls, and audit log as the CLI / local backend —
// only the transport differs. Prompt templates and board preferences
// stay local-only (BACI-32 §5 #3 / §5 #5 — separate follow-ups);
// dispatch CRUD now has full REST parity here too (BACI-35), bringing
// dispatch create + list-per-repo in alongside inbox + ack.
//
// Audit actor: every mutating verb stamps history with the X-Actor
// header (falling back to defaultActor) so a remote agent's identity
// flows through the same audit chain as the local CLI (which resolves
// actor via .bacio/agents.json for agent-driven calls). Register
// additionally writes that actor into the new session row's `actor`
// field unless the JSON payload supplies its own.

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/timeparse"
)

// AgentSessionShow mirrors client.AgentSessionView so JSON consumers see
// the same shape from `bacio agent show -o json` and the API.
type AgentSessionShow struct {
	Session *model.AgentSession `json:"session"`
	Claims  []*model.AgentClaim `json:"claims"`
}

// ---------- register ----------

func (d deps) handleAgentRegister(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	parsed, _, err := inputio.DecodeStrict[inputs.AgentRegisterInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	in := *parsed
	if strings.TrimSpace(in.SessionID) == "" {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"session_id is required", map[string]any{"field": "session_id"})
		return
	}
	// BACI-100: register requires a structurally valid UUID session_id.
	// Checked here (before the dry-run branch) so a dry-run of a
	// malformed id rejects with a clean 400 too — projectAgentSession
	// doesn't validate, and the deeper RequireUUID store check would
	// otherwise surface as a 500.
	if _, err := store.ValidateSessionUUID(in.SessionID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input",
			err.Error(), map[string]any{"field": "session_id"})
		return
	}
	if in.NewIdentity && in.Agent == "" {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"new_identity requires agent", map[string]any{"field": "agent"})
		return
	}
	actor := ActorFromContext(r.Context())
	if in.Actor == "" {
		in.Actor = actor
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusCreated, projectAgentSession(repo, in))
		return
	}
	sess, err := d.registerAgent(repo, in)
	if err != nil {
		if errors.Is(err, store.ErrAgentNameTaken) {
			writeError(w, http.StatusConflict, "conflict",
				fmt.Sprintf("agent name %q already taken — generate a fresh slug and retry with new_identity", in.Agent),
				map[string]any{"field": "agent"})
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    actor,
		Op:       "agent.register",
		Kind:     "agent",
		TargetID: &sess.ID, TargetLabel: sess.SessionID,
		Details:  agentRegisterDetails(sess),
	})
	writeJSON(w, http.StatusCreated, sess)
}

// registerAgent mirrors localClient.RegisterAgent's non-dry-run path:
// upsert (or mint, on --new) the agent identity row before the session
// upsert so the FK resolves; surface ErrAgentNameTaken verbatim so the
// caller can detect the clash. Unlike the CLI's register (a manual
// fallback that leaves registered_at NULL), the REST register
// explicitly marks the session registered — a remote frontend calling
// POST /repos/{prefix}/agents/sessions is doing a deliberate "register
// me now" action and expects the session to surface in default-filtered
// list views immediately, matching the desktop's expectations. The
// MarkRegistered flag is first-mark-wins so re-runs preserve the
// original timestamp.
func (d deps) registerAgent(repo *model.Repo, in inputs.AgentRegisterInput) (*model.AgentSession, error) {
	// BACI-100 dedupe: the server can't see the caller's process tree, so
	// claude_pid must arrive in the request body. When non-zero, reap any
	// other live session row for that OS process whose session_id differs
	// from the incoming one — a single `claude` process should hold
	// exactly one live registry row. Absent (0), behave as before. The
	// UUID-shape check already ran in handleAgentRegister (before the
	// dry-run branch), and UpsertAgentSession re-checks via RequireUUID.
	if in.ClaudePID != 0 {
		if err := d.supersedeStaleSessions(repo, in.Host, in.ClaudePID, in.SessionID); err != nil {
			return nil, err
		}
	}
	var agentID *int64
	if in.Agent != "" {
		ag, created, err := d.store.UpsertAgent(in.Agent, in.NewIdentity)
		if err != nil {
			return nil, err
		}
		agentID = &ag.ID
		if created {
			recordOp(d.store, d.logger, model.HistoryEntry{
				RepoID: &repo.ID, RepoPrefix: repo.Prefix,
				Actor:    in.Actor,
				Op:       "agent.identity.create",
				Kind:     "agent",
				TargetID: &ag.ID, TargetLabel: in.Agent,
			})
		}
	}
	return d.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID:      in.SessionID,
		RepoID:         repo.ID,
		AgentID:        agentID,
		Actor:          in.Actor,
		Model:          in.Model,
		Host:           in.Host,
		Branch:         in.Branch,
		ClaudePID:      in.ClaudePID,
		MarkRegistered: true,
		RequireUUID:    true,
	})
}

// supersedeStaleSessions is the API-side twin of
// localClient.supersedeStaleSessions (BACI-100): it ends every other
// live session row for the given (host, claude_pid) whose session_id
// differs from keepID, so one OS process can't accumulate N phantom
// "live" registry rows. Best-effort per row — a failure to end one
// phantom is logged and doesn't fail the register.
func (d deps) supersedeStaleSessions(repo *model.Repo, host string, claudePID int64, keepID string) error {
	live, err := d.store.SessionsByClaudePID(host, claudePID)
	if err != nil {
		return err
	}
	for _, s := range live {
		if s.SessionID == keepID {
			continue
		}
		// Supersede path: end the phantom row with an empty orphanState so
		// the claim cascade leaves issue state alone (BACI-126c), and
		// DispatchCascadeCancel because the phantom never owned the
		// work (BACI-133's requeue is reserved for the reaper recovery
		// path on a real session that went dark).
		ended, _, _, _, abandonedQuestions, err := d.store.EndAgentSession(s.SessionID, string(model.EndReasonSuperseded), "", store.DispatchCascadeCancel)
		if err != nil {
			if d.logger != nil {
				d.logger.Warn("superseding stale session", "session_id", s.SessionID, "err", err)
			}
			continue
		}
		if ended != nil {
			recordOp(d.store, d.logger, model.HistoryEntry{
				RepoID: &repo.ID, RepoPrefix: repo.Prefix,
				Op: "agent.end", Kind: "agent",
				TargetID: &ended.ID, TargetLabel: ended.SessionID,
				Details: fmt.Sprintf("reason=superseded (claude_pid %d re-registered as %s)", claudePID, keepID),
			})
			// BACI-253: settle any parked questions the phantom owned.
			if abandonedQuestions > 0 {
				recordOp(d.store, d.logger, model.HistoryEntry{
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

// projectAgentSession is the API-side twin of client.projectAgentSession —
// dry-run register returns this projection so the response shape matches
// a real call minus server-time fields (ID).
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

// agentRegisterDetails mirrors client.agentRegisterDetails so the audit
// row reads identically across surfaces.
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

// ---------- heartbeat ----------

func (d deps) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("session_id")
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	var in inputs.AgentHeartbeatInput
	if len(raw) > 0 {
		got, _, err := inputio.DecodeStrict[inputs.AgentHeartbeatInput](raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
			return
		}
		in = *got
	}
	// session_id in the URL is canonical; reject any mismatch with the body
	// rather than silently picking one.
	if in.SessionID != "" && in.SessionID != sid {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"session_id in body must match URL", map[string]any{"field": "session_id"})
		return
	}
	in.SessionID = sid
	existing, err := d.store.GetAgentSession(sid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("session %q is not registered", sid), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if isDryRun(r) {
		projected := *existing
		if in.Model != "" {
			projected.Model = in.Model
		}
		if in.Branch != "" {
			projected.Branch = in.Branch
		}
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	sess, err := d.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: in.SessionID,
		RepoID:    existing.RepoID,
		Actor:     existing.Actor,
		Model:     pickNonEmpty(in.Model, existing.Model),
		Host:      existing.Host,
		Branch:    pickNonEmpty(in.Branch, existing.Branch),
	})
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	// No audit row — heartbeats run hundreds of times per session and
	// would drown the audit log (matches localClient.HeartbeatAgent).
	writeJSON(w, http.StatusOK, sess)
}

// pickNonEmpty returns value if non-empty, else fallback. Mirrors
// client.orDefault — kept private to the handler so the api package
// doesn't take a client dep.
func pickNonEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// ---------- end ----------

func (d deps) handleAgentEnd(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("session_id")
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	var in inputs.AgentEndInput
	if len(raw) > 0 {
		got, _, err := inputio.DecodeStrict[inputs.AgentEndInput](raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
			return
		}
		in = *got
	}
	if in.SessionID != "" && in.SessionID != sid {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"session_id in body must match URL", map[string]any{"field": "session_id"})
		return
	}
	in.SessionID = sid
	if in.Reason == "" {
		in.Reason = string(model.EndReasonStop)
	}
	if _, err := model.ParseEndReason(in.Reason); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(),
			map[string]any{"field": "reason"})
		return
	}
	existing, err := d.store.GetAgentSession(sid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("session %q is not registered", sid), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if isDryRun(r) {
		projected := *existing
		now := time.Now().UTC()
		projected.EndedAt = &now
		projected.EndReason = in.Reason
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	// BACI-126c: state_on_orphan defaults to in_progress when unset.
	orphanState := model.StateInProgress
	if strings.TrimSpace(in.StateOnOrphan) != "" {
		parsed, perr := model.ParseState(in.StateOnOrphan)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "invalid_input",
				"state_on_orphan: "+perr.Error(),
				map[string]any{"field": "state_on_orphan"})
			return
		}
		orphanState = parsed
	}
	actor := ActorFromContext(r.Context())
	// BACI-133: derive the dispatch cascade from the end reason — only
	// the reaper-driven presumed_dead path re-queues; every other end
	// reason keeps today's cancel-on-end semantics. The audit row each
	// cascade entry produces (agent.cancel vs agent.dispatch.requeue)
	// is picked off info.NewStatus below.
	cascade := store.DispatchCascadeCancel
	if model.EndReason(in.Reason) == model.EndReasonPresumedDead {
		cascade = store.DispatchCascadeRequeue
	}
	sess, assigneeChanges, stateChanges, cascadeInfos, abandonedQuestions, err := d.store.EndAgentSession(in.SessionID, in.Reason, orphanState, cascade)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &sess.RepoID, RepoPrefix: sess.RepoPrefix,
		Actor:    actor,
		Op:       "agent.end",
		Kind:     "agent",
		TargetID: &sess.ID, TargetLabel: sess.SessionID,
		Details:  "reason=" + sess.EndReason,
	})
	// BACI-253: surface the summary `question.abandon` row when the
	// end-session cascade settled at least one parked question — same
	// shape as the client wrapper and the channel-startup janitor so
	// `bacio history --op question.abandon` is a coherent ledger
	// across every abandon path.
	if abandonedQuestions > 0 {
		recordOp(d.store, d.logger, model.HistoryEntry{
			RepoID: &sess.RepoID, RepoPrefix: sess.RepoPrefix,
			Actor:       actor,
			Op:          "question.abandon",
			Kind:        "question",
			TargetLabel: sess.SessionID,
			Details:     fmt.Sprintf("session=%s,count=%d", sess.SessionID, abandonedQuestions),
		})
	}
	for _, ch := range assigneeChanges {
		writeAssigneeChange(d.store, d.logger, actor, ch)
	}
	// BACI-126c: per-issue state move audit for every cascaded release.
	for _, sc := range stateChanges {
		writeOrphanStateChange(d.store, d.logger, actor, sc, sess.SessionID)
	}
	// BACI-58 §B / BACI-133 — write the per-row history entry for every
	// dispatch the end-session tx touched. Branch on the cascade's
	// NewStatus: cancelled rows write `agent.cancel` (today's behaviour
	// for user/hook/supersede ends), queued rows write
	// `agent.dispatch.requeue` (the BACI-133 reaper recovery path,
	// actor=bacio-channel-ping so `bacio history --user-filter
	// bacio-channel-ping` returns a coherent reaper-activity ledger).
	for _, info := range cascadeInfos {
		switch info.NewStatus {
		case model.DispatchCancelled:
			recordOp(d.store, d.logger, model.HistoryEntry{
				RepoID: &info.RepoID, RepoPrefix: info.RepoPrefix,
				Actor:    actor,
				Op:       "agent.cancel",
				Kind:     "agent",
				TargetID: &info.ID, TargetLabel: cancelledDispatchTargetLabel(info),
				Details:  cancelledDispatchDetails(info, "auto-cancel: target session ended"),
			})
		case model.DispatchQueued:
			recordOp(d.store, d.logger, model.HistoryEntry{
				RepoID: &info.RepoID, RepoPrefix: info.RepoPrefix,
				Actor:    string(model.IdlePingDispatchCreator),
				Op:       "agent.dispatch.requeue",
				Kind:     "agent",
				TargetID: &info.ID, TargetLabel: cancelledDispatchTargetLabel(info),
				Details:  cancelledDispatchDetails(info, "auto-requeue: target session reaped (presumed_dead)"),
			})
		}
	}
	writeJSON(w, http.StatusOK, sess)
}

// ---------- claim ----------

func (d deps) handleAgentClaim(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("session_id")
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.AgentClaimInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if in.SessionID != "" && in.SessionID != sid {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"session_id in body must match URL", map[string]any{"field": "session_id"})
		return
	}
	in.SessionID = sid
	if strings.TrimSpace(in.IssueKey) == "" {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"issue_key is required", map[string]any{"field": "issue_key"})
		return
	}
	prefix, num, err := store.ParseIssueKey(in.IssueKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(),
			map[string]any{"field": "issue_key"})
		return
	}
	iss, err := d.store.GetIssueByKey(prefix, num)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("issue %s not found", in.IssueKey), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if _, err := d.store.GetAgentSession(sid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("session %q is not registered", sid), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if isDryRun(r) {
		// BACI-126a: surface the post-claim state on the dry-run so the
		// rehearsal output mirrors the live call. Auto-transition is
		// unconditional — IssueStateAfter is always in_progress.
		writeDryRun(w, http.StatusCreated, &model.AgentClaim{
			SessionID:        sid,
			IssueID:          iss.ID,
			IssueKey:         iss.Key,
			Prompt:           in.Prompt,
			ClaimedAt:        time.Now().UTC(),
			IssueStateBefore: iss.State,
			IssueStateAfter:  model.StateInProgress,
		})
		return
	}
	actor := ActorFromContext(r.Context())
	claim, created, assigneeChange, stateChange, err := d.store.AddAgentClaim(sid, iss.ID, in.Prompt)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if created {
		recordOp(d.store, d.logger, model.HistoryEntry{
			RepoID: &iss.RepoID, RepoPrefix: prefix,
			Actor:    actor,
			Op:       "agent.claim",
			Kind:     "agent",
			TargetID: &claim.ID, TargetLabel: sid,
			Details:  claimAuditDetails(iss.Key, stateChange),
		})
		if assigneeChange != nil {
			writeAssigneeChange(d.store, d.logger, actor, *assigneeChange)
		}
		// BACI-126a: surface the post-claim state on the returned object.
		if stateChange != nil {
			claim.IssueStateBefore = stateChange.Old
			claim.IssueStateAfter = stateChange.New
		}
	}
	writeJSON(w, http.StatusCreated, claim)
}

// claimAuditDetails formats the agent.claim audit row's Details column
// (BACI-126a). Mirrors client.claimAuditDetails so the line reads
// identically across surfaces.
func claimAuditDetails(issueKey string, ch *store.StateChange) string {
	if ch == nil || !ch.Changed() {
		return "issue=" + issueKey
	}
	return fmt.Sprintf("issue=%s, state: %s → %s", issueKey, ch.Old, ch.New)
}

// ---------- release ----------

func (d deps) handleAgentRelease(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("session_id")
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.AgentReleaseInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if in.SessionID != "" && in.SessionID != sid {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"session_id in body must match URL", map[string]any{"field": "session_id"})
		return
	}
	in.SessionID = sid
	if strings.TrimSpace(in.IssueKey) == "" {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"issue_key is required", map[string]any{"field": "issue_key"})
		return
	}
	// Pipeline cutover: final_state is OPTIONAL — validate only when
	// present (a malformed state still 400s before any read); the
	// empty-state default to the issue's current state happens after we
	// resolve the issue below.
	if s := strings.TrimSpace(in.FinalState); s != "" {
		if _, perr := model.ParseState(s); perr != nil {
			writeError(w, http.StatusBadRequest, "invalid_input",
				"final_state: "+perr.Error(),
				map[string]any{"field": "final_state"})
			return
		}
	}
	prefix, num, err := store.ParseIssueKey(in.IssueKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(),
			map[string]any{"field": "issue_key"})
		return
	}
	iss, err := d.store.GetIssueByKey(prefix, num)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("issue %s not found", in.IssueKey), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if _, err := d.store.GetAgentSession(sid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("session %q is not registered", sid), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	// Empty final_state defaults via model.ReleaseFallbackState: a no-op
	// for engine-governed / terminal cards (the controller engine owns
	// pipeline-card progression) but in_review for an off-pipeline issue,
	// so a released claim doesn't strand the card in in_progress. A
	// non-empty state (validated above) still moves the issue.
	finalState := model.ReleaseFallbackState(iss.State)
	if s := strings.TrimSpace(in.FinalState); s != "" {
		finalState, _ = model.ParseState(s)
	}
	if isDryRun(r) {
		now := time.Now().UTC()
		// project the post-release state on the dry-run.
		writeDryRun(w, http.StatusOK, &model.AgentClaim{
			SessionID:        sid,
			IssueID:          iss.ID,
			IssueKey:         iss.Key,
			ReleasedAt:       &now,
			IssueStateBefore: iss.State,
			IssueStateAfter:  finalState,
		})
		return
	}
	actor := ActorFromContext(r.Context())
	claim, assigneeChange, stateChange, err := d.store.ReleaseAgentClaim(sid, iss.ID, finalState)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("session %q has no open claim on %s", sid, iss.Key), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: prefix,
		Actor:    actor,
		Op:       "agent.release",
		Kind:     "agent",
		TargetID: &claim.ID, TargetLabel: sid,
		Details:  releaseAuditDetails(iss.Key, stateChange),
	})
	if assigneeChange != nil {
		writeAssigneeChange(d.store, d.logger, actor, *assigneeChange)
	}
	if stateChange != nil {
		claim.IssueStateBefore = stateChange.Old
		claim.IssueStateAfter = stateChange.New
	}
	writeJSON(w, http.StatusOK, claim)
}

// releaseAuditDetails mirrors client.releaseAuditDetails (BACI-126c).
func releaseAuditDetails(issueKey string, ch *store.StateChange) string {
	if ch == nil || !ch.Changed() {
		return "issue=" + issueKey
	}
	return fmt.Sprintf("issue=%s, state: %s → %s", issueKey, ch.Old, ch.New)
}

// writeOrphanStateChange writes the issue.state audit row for a state
// move made as a side effect of a cascaded `agent end` release
// (BACI-126c). Mirrors writeAssigneeChange's shape.
func writeOrphanStateChange(s *store.Store, logger interface {
	Warn(string, ...any)
}, actor string, ch store.StateChange, sessionID string) {
	if !ch.Changed() {
		return
	}
	repoID, issueID := ch.RepoID, ch.IssueID
	entry := model.HistoryEntry{
		RepoID: &repoID, RepoPrefix: ch.RepoPrefix,
		Actor:       actor,
		Op:          "issue.state",
		Kind:        "issue",
		TargetID:    &issueID,
		TargetLabel: ch.IssueKey,
		Details:     fmt.Sprintf("%s → %s (auto: session %s ended)", ch.Old, ch.New, sessionID),
	}
	if err := s.RecordHistory(entry); err != nil {
		logger.Warn("failed to record history", "err", err, "op", entry.Op)
	}
}

// writeAssigneeChange writes the issue.assign audit row for an
// issues.assignee mutation a claim/release/end made as a side effect of
// keeping the claim and the assignee in lockstep. No-op changes record
// nothing. Mirrors localClient.recordAssigneeChange so audit text reads
// identically across surfaces.
func writeAssigneeChange(s *store.Store, logger interface {
	Warn(string, ...any)
}, actor string, ch store.AssigneeChange) {
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
	entry := model.HistoryEntry{
		RepoID:      &repoID,
		RepoPrefix:  ch.RepoPrefix,
		Actor:       actor,
		Op:          "issue.assign",
		Kind:        "issue",
		TargetID:    &issueID,
		TargetLabel: ch.IssueKey,
		Details:     details,
	}
	if err := s.RecordHistory(entry); err != nil {
		logger.Warn("failed to record history", "err", err, "op", entry.Op)
	}
}

// ---------- list ----------

func (d deps) handleAgentSessionsList(w http.ResponseWriter, r *http.Request) {
	d.serveAgentSessions(w, r, nil)
}

func (d deps) handleAgentSessionsListRepo(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	d.serveAgentSessions(w, r, &repo.ID)
}

func (d deps) serveAgentSessions(w http.ResponseWriter, r *http.Request, repoID *int64) {
	q := r.URL.Query()
	f := store.AgentSessionFilter{RepoID: repoID}
	if v := q.Get("active"); v == "true" || v == "1" {
		f.OnlyAlive = true
	}
	// Default: registered_at IS NOT NULL — hide SessionStart stubs that
	// never made it through register. ?all=true flips it (mirrors the
	// CLI's `--all` flag on bacio agent list).
	all := q.Get("all")
	f.RegisteredOnly = !(all == "true" || all == "1")
	if since := q.Get("since"); since != "" {
		dur, err := timeparse.Lookback(since)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(),
				map[string]any{"field": "since"})
			return
		}
		f.Since = time.Now().Add(-dur).UTC()
	}
	sessions, err := d.store.ListAgentSessions(f)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if sessions == nil {
		sessions = []*model.AgentSession{}
	}
	writeJSON(w, http.StatusOK, sessions)
}

// ---------- show ----------

func (d deps) handleAgentSessionShow(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("session_id")
	sess, err := d.store.ResolveAgentSession(sid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("session %q not found", sid), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	claims, err := d.store.ListAgentClaims(sess.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if claims == nil {
		claims = []*model.AgentClaim{}
	}
	writeJSON(w, http.StatusOK, &AgentSessionShow{Session: sess, Claims: claims})
}

// ---------- todos (BACI-45) ----------

// handleAgentSessionTodos returns the agent's mirrored TodoWrite
// snapshot for one session. Read-only: writes flow through the
// `bacio hook post-tool-use` mirror, not over HTTP. Unknown session id
// returns 404; an empty list is `[]`, never null.
//
// BACI-62: an optional `?issue_key=<PREFIX-N>` filter narrows the
// returned rows to one job (the per-(session, issue) scope the
// desktop/TUI Agents view uses). The unfiltered call shape stays the
// same for back-compat — every existing caller keeps getting every
// row for the session.
//
// BACI-132: an optional `?dispatch_id=<int>` further narrows to one
// dispatch within the (session, issue), so plan-then-implement on
// the same issue can be inspected as two task lists. dispatch_id
// requires issue_key (400 otherwise) — the unfiltered shape and the
// issue_key-only shape both stay back-compat.
func (d deps) handleAgentSessionTodos(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("session_id")
	sess, err := d.store.ResolveAgentSession(sid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("session %q not found", sid), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	issueKey := r.URL.Query().Get("issue_key")
	dispatchIDStr := r.URL.Query().Get("dispatch_id")
	if dispatchIDStr != "" {
		if issueKey == "" {
			writeError(w, http.StatusBadRequest, "bad_request",
				"dispatch_id requires issue_key",
				map[string]any{"field": "dispatch_id"})
			return
		}
		dispatchID, err := strconv.ParseInt(dispatchIDStr, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request",
				fmt.Sprintf("dispatch_id %q is not an integer", dispatchIDStr),
				map[string]any{"field": "dispatch_id"})
			return
		}
		todosByPK, err := d.store.ListTodosBySessionsAndIssue([]store.SessionIssuePair{{
			SessionID:  sess.SessionID,
			IssueKey:   issueKey,
			DispatchID: &dispatchID,
		}})
		if err != nil {
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), nil)
			return
		}
		// One session, one bucket — flatten.
		var todos []model.SessionTodo
		for _, list := range todosByPK {
			todos = list
		}
		if todos == nil {
			todos = []model.SessionTodo{}
		}
		writeJSON(w, http.StatusOK, todos)
		return
	}
	todos, err := d.store.ListSessionTodos(sess.SessionID, issueKey)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if todos == nil {
		todos = []model.SessionTodo{}
	}
	writeJSON(w, http.StatusOK, todos)
}

// ---------- inbox ----------

func (d deps) handleAgentInbox(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("session_id")
	sess, err := d.store.GetAgentSession(sid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("session %q is not registered", sid), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	// A session's inbox is everything aimed at the bare session id OR at
	// the agent identity behind it; open items only.
	ds, err := d.store.ListDispatches(store.DispatchFilter{
		TargetAgentID:   sess.AgentID,
		TargetSessionID: sess.SessionID,
		Statuses:        []model.DispatchStatus{model.DispatchPending, model.DispatchDelivered},
	})
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if ds == nil {
		ds = []*model.AgentDispatch{}
	}
	writeJSON(w, http.StatusOK, ds)
}

// ---------- ack ----------

func (d deps) handleAgentDispatchAck(w http.ResponseWriter, r *http.Request) {
	rawID := r.PathValue("id")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_input",
			fmt.Sprintf("invalid dispatch id %q", rawID), map[string]any{"field": "id"})
		return
	}
	body, ok := readBody(r, w)
	if !ok {
		return
	}
	in := inputs.AgentAckInput{ID: id}
	if len(body) > 0 {
		got, _, err := inputio.DecodeStrict[inputs.AgentAckInput](body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
			return
		}
		// The URL id is canonical; reject a mismatch in the body rather than
		// silently picking one.
		if got.ID != 0 && got.ID != id {
			writeError(w, http.StatusBadRequest, "invalid_input",
				"id in body must match URL", map[string]any{"field": "id"})
			return
		}
		in = *got
		in.ID = id
	}
	existing, err := d.store.GetDispatch(in.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("no dispatch with id %d", in.ID), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if isDryRun(r) {
		projected := *existing
		now := time.Now().UTC()
		projected.Status = model.DispatchAcked
		projected.AckedAt = &now
		projected.AckNote = in.Note
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	actor := ActorFromContext(r.Context())
	acked, err := d.store.AckDispatch(in.ID, in.Note)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &acked.RepoID, RepoPrefix: acked.RepoPrefix,
		Actor:    actor,
		Op:       "agent.ack",
		Kind:     "agent",
		TargetID: &acked.ID, TargetLabel: dispatchTargetLabel(acked),
		Details:  dispatchDetails(acked),
	})
	writeJSON(w, http.StatusOK, acked)
}

// ---------- cancel ----------

func (d deps) handleAgentDispatchCancel(w http.ResponseWriter, r *http.Request) {
	rawID := r.PathValue("id")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_input",
			fmt.Sprintf("invalid dispatch id %q", rawID), map[string]any{"field": "id"})
		return
	}
	body, ok := readBody(r, w)
	if !ok {
		return
	}
	in := inputs.AgentCancelInput{ID: id}
	if len(body) > 0 {
		got, _, err := inputio.DecodeStrict[inputs.AgentCancelInput](body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
			return
		}
		if got.ID != 0 && got.ID != id {
			writeError(w, http.StatusBadRequest, "invalid_input",
				"id in body must match URL", map[string]any{"field": "id"})
			return
		}
		in = *got
		in.ID = id
	}
	existing, err := d.store.GetDispatch(in.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("no dispatch with id %d", in.ID), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if existing.Status == model.DispatchAcked {
		writeError(w, http.StatusConflict, "conflict",
			fmt.Sprintf("dispatch %d is already acked; cannot cancel", in.ID), nil)
		return
	}
	if existing.Status == model.DispatchDelivered {
		// BACI-130: a delivered dispatch means the worker has taken the
		// Task call and is doing the work — cancelling the row would
		// just drop the kanban activity pill while the work continues.
		// Return 409 (matching the already-acked arm) and short-circuit
		// the dry-run projection — projecting "would-be cancelled" for
		// a row that's about to reject is misleading.
		writeError(w, http.StatusConflict, "conflict",
			fmt.Sprintf("dispatch %d has been delivered; cannot cancel", in.ID), nil)
		return
	}
	if isDryRun(r) {
		projected := *existing
		projected.Status = model.DispatchCancelled
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	actor := ActorFromContext(r.Context())
	cancelled, err := d.store.CancelDispatch(in.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &cancelled.RepoID, RepoPrefix: cancelled.RepoPrefix,
		Actor:    actor,
		Op:       "agent.cancel",
		Kind:     "agent",
		TargetID: &cancelled.ID, TargetLabel: dispatchTargetLabel(cancelled),
		Details:  dispatchDetails(cancelled),
	})
	writeJSON(w, http.StatusOK, cancelled)
}

// ---------- rescue (BACI-190) ----------

// handleAgentDispatchRescue posts a `from="bacio-rescue"` channel
// event to an idle supervisor session, asking it to handle a dead
// worker's stranded worktree INLINE. Eligibility (status, creator,
// dead-session state, idle-supervisor availability) is enforced by
// client.CreateRescueDispatch — the handler just translates the
// returned errors into clean HTTP statuses (404 / 409 / 500) and
// shapes the response.
func (d deps) handleAgentDispatchRescue(w http.ResponseWriter, r *http.Request) {
	rawID := r.PathValue("id")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_input",
			fmt.Sprintf("invalid dispatch id %q", rawID), map[string]any{"field": "id"})
		return
	}
	who := ActorFromContext(r.Context())
	c := client.NewLocalFromStore(d.store, who)
	defer c.Close()
	dsp, err := c.CreateRescueDispatch(r.Context(), id)
	if err != nil {
		msg := err.Error()
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("no dispatch with id %d", id), nil)
			return
		}
		// Eligibility misses are 409s (the caller can fix the state and
		// retry, but the request itself isn't malformed).
		if strings.Contains(msg, "rescuable") ||
			strings.Contains(msg, "is still alive") ||
			strings.Contains(msg, "no target session") ||
			strings.Contains(msg, "no idle supervisor") {
			writeError(w, http.StatusConflict, "conflict", msg, nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, msg, nil)
		return
	}
	writeJSON(w, http.StatusCreated, dsp)
}

// dispatchTargetLabel / dispatchDetails mirror their client-side twins so
// the audit row reads identically across surfaces.
func dispatchTargetLabel(d *model.AgentDispatch) string {
	if d.TargetAgentName != "" {
		return d.TargetAgentName
	}
	return d.TargetSessionID
}

func dispatchDetails(d *model.AgentDispatch) string {
	var parts []string
	if d.IssueKey != "" {
		parts = append(parts, "issue="+d.IssueKey)
	}
	if d.TargetSessionID != "" {
		parts = append(parts, "session="+d.TargetSessionID)
	}
	parts = append(parts, "status="+string(d.Status))
	return strings.Join(parts, ",")
}

// cancelledDispatchTargetLabel / cancelledDispatchDetails (BACI-58 §B)
// shape the audit row for an auto-cancelled dispatch the same way the
// regular agent.cancel path does, but starting from the lighter-weight
// store.CancelledDispatchInfo (no full AgentDispatch fetch needed).
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
	return strings.Join(parts, ",")
}

// ---------- open claims ----------

func (d deps) handleAgentClaimsOpen(w http.ResponseWriter, r *http.Request) {
	d.serveOpenClaims(w, nil)
}

func (d deps) handleAgentClaimsOpenRepo(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	d.serveOpenClaims(w, &repo.ID)
}

// serveOpenClaims flattens store.OpenClaimsBySession (which buckets open
// claims by session PK) into a flat list. With repoID == nil it
// concatenates across every tracked repo — matches localClient.ListOpenClaims.
func (d deps) serveOpenClaims(w http.ResponseWriter, repoID *int64) {
	collect := func(id int64) ([]*model.AgentClaim, error) {
		bySession, err := d.store.OpenClaimsBySession(id)
		if err != nil {
			return nil, err
		}
		var out []*model.AgentClaim
		for _, claims := range bySession {
			out = append(out, claims...)
		}
		return out, nil
	}
	var (
		out []*model.AgentClaim
		err error
	)
	if repoID != nil {
		out, err = collect(*repoID)
	} else {
		repos, lerr := d.store.ListRepos()
		if lerr != nil {
			status, code := statusForError(lerr)
			writeError(w, status, code, lerr.Error(), nil)
			return
		}
		for _, r := range repos {
			claims, cerr := collect(r.ID)
			if cerr != nil {
				err = cerr
				break
			}
			out = append(out, claims...)
		}
	}
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if out == nil {
		out = []*model.AgentClaim{}
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- repo-scoped dispatch CRUD (BACI-35) ----------

// handleAgentDispatchesList returns every dispatch queued against this
// repo, newest first — the read backing the desktop Agents view's
// per-repo bucket. Read-only — no audit row.
func (d deps) handleAgentDispatchesList(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	ds, err := d.store.ListDispatches(store.DispatchFilter{RepoID: &repo.ID})
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if ds == nil {
		ds = []*model.AgentDispatch{}
	}
	writeJSON(w, http.StatusOK, ds)
}

// handleAgentDispatchCreate queues a dispatch against this repo,
// mirroring localClient.CreateDispatch: validate target_agent and/or
// target_session, resolve the issue (if any), render the stage's
// prompt template with the issue's context, then write the row and
// stamp the audit log. 201 with the queued dispatch, or a 201 dry-run
// projection (no row, no audit) with X-Dry-Run: applied.
func (d deps) handleAgentDispatchCreate(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.AgentDispatchInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if in.TargetAgent == "" && in.TargetSession == "" {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"dispatch requires a target — pass target_agent and/or target_session", nil)
		return
	}

	var agentID *int64
	if in.TargetAgent != "" {
		ag, err := d.store.GetAgentByName(in.TargetAgent)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found",
					fmt.Sprintf("no agent identity named %q is registered", in.TargetAgent),
					map[string]any{"field": "target_agent"})
				return
			}
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), nil)
			return
		}
		agentID = &ag.ID
	}
	if in.TargetSession != "" {
		if _, err := d.store.GetAgentSession(in.TargetSession); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found",
					fmt.Sprintf("session %q is not registered", in.TargetSession),
					map[string]any{"field": "target_session"})
				return
			}
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), nil)
			return
		}
	}

	var issueID *int64
	var issueKey string
	if in.IssueKey != "" {
		prefix, num, err := store.ParseIssueKey(in.IssueKey)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(),
				map[string]any{"field": "issue_key"})
			return
		}
		iss, err := d.store.GetIssueByKey(prefix, num)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found",
					fmt.Sprintf("issue %s does not exist", in.IssueKey),
					map[string]any{"field": "issue_key"})
				return
			}
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), nil)
			return
		}
		if iss.RepoID != repo.ID {
			writeError(w, http.StatusNotFound, "not_found",
				"issue not found in this repo", map[string]any{"field": "issue_key"})
			return
		}
		issueID = &iss.ID
		issueKey = iss.Key
	}

	mode := model.DispatchMode(in.Mode)
	// BACI-76: the payload is the rewritten preamble plus a tiny stub
	// (ticket / mode / subagent type); the per-mode brief is the
	// subagent's system prompt now.
	preamble, err := d.store.GetDispatchPreamble()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	// BACI-226: resolve the base branch up front so the stub carries
	// the <base_branch> tag the worker reads from its Task prompt.
	// Mirror local_dispatch.go's targeted-pending path — no bind step
	// for a direct REST/API dispatch, so the value must be correct at
	// creation time.
	var baseBranch string
	if issueID != nil {
		iss, ierr := d.store.GetIssueByID(*issueID)
		if ierr != nil {
			status, code := statusForError(ierr)
			writeError(w, status, code, ierr.Error(), nil)
			return
		}
		var feat *model.Feature
		if iss.FeatureID != nil {
			feat, ierr = d.store.GetFeatureByID(*iss.FeatureID)
			if ierr != nil {
				status, code := statusForError(ierr)
				writeError(w, status, code, ierr.Error(), nil)
				return
			}
		}
		baseBranch = model.ResolveBaseBranch(iss, feat)
	}
	stub := model.DispatchStub{IssueKey: issueKey, Mode: string(mode), BaseBranch: baseBranch}
	if mode != "" {
		stub.SubagentType = model.SubagentTypeForTemplate(string(mode))
	}
	payload := model.ComposeDispatchPayload(preamble, stub, in.Message)

	who := ActorFromContext(r.Context())
	if isDryRun(r) {
		writeDryRun(w, http.StatusCreated, &model.AgentDispatch{
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
			CreatedBy:       who,
			CreatedAt:       time.Now().UTC(),
		})
		return
	}

	dsp, err := d.store.AddDispatch(store.AddDispatchIn{
		RepoID:          repo.ID,
		TargetAgentID:   agentID,
		TargetSessionID: in.TargetSession,
		IssueID:         issueID,
		Mode:            mode,
		Payload:         payload,
		CreatedBy:       who,
	})
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    who,
		Op:       "agent.dispatch",
		Kind:     "agent",
		TargetID: &dsp.ID, TargetLabel: dispatchTargetLabel(dsp),
		Details: dispatchDetails(dsp),
	})
	writeJSON(w, http.StatusCreated, dsp)
}

// (dispatchTargetLabel and dispatchDetails live above — defined for the
// ack handler and reused by create.)

// ---------- state-gated auto-pick dispatch (BACI-40) ----------

// handleIssueDispatch is the REST entry point for the state-gated
// enqueue verb (BACI-40 + BACI-51): re-check the stage's state-gate
// against the issue's current state, then insert a queued dispatch
// the background matcher will bind to a free agent. Mirrors the
// desktop per-card action button and the CLI's target-less
// `bacio agent dispatch <key> --mode <stage>`; all three routes
// share client.AutoDispatchIssue so the gate + enqueue path only
// lives in one place. Post-BACI-51 there is no "no free agent" 400
// — that case is now expressed as a queued row.
func (d deps) handleIssueDispatch(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.IssueDispatchInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if in.Mode == "" {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"a dispatch mode is required", map[string]any{"field": "mode"})
		return
	}

	who := ActorFromContext(r.Context())
	c := client.NewLocalFromStore(d.store, who)
	defer c.Close()
	dsp, err := c.AutoDispatchIssue(r.Context(), repo, iss.Key, in.Mode, isDryRun(r))
	if err != nil {
		// State-gate misses are 400s (caller's choice to retry / pick
		// a different mode) rather than 500s.
		msg := err.Error()
		if strings.Contains(msg, "can't run from") {
			writeError(w, http.StatusBadRequest, "invalid_input", msg, nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, msg, nil)
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusCreated, dsp)
		return
	}
	writeJSON(w, http.StatusCreated, dsp)
}

// handleIssueWaitingDispatch is the BACI-51 read backing the spinner-
// as-cancel UI: returns the active (queued / pending / delivered)
// dispatch targeting an issue, or 404 when none. Read-only; no audit
// row.
func (d deps) handleIssueWaitingDispatch(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	dsp, err := d.store.WaitingDispatchForIssue(repo.ID, iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if dsp == nil {
		writeError(w, http.StatusNotFound, "not_found",
			"no waiting dispatch for "+iss.Key, nil)
		return
	}
	writeJSON(w, http.StatusOK, dsp)
}
