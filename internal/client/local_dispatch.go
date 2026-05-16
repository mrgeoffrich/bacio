package client

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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
	var issueKey, issueTitle string
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
		issueTitle = iss.Title
	}

	mode := model.DispatchMode(in.Mode)
	// Resolve the stage's prompt template (the user's custom override or
	// the built-in default), render its placeholders against this
	// issue's context, then append the free-form note. An untyped mode
	// has no template, so the payload is just the note.
	template, err := c.store.GetPromptTemplate(mode)
	if err != nil {
		return nil, err
	}
	payload := model.ComposeDispatchPayload(template, map[string]string{
		"issue_id":    issueKey,
		"issue_title": issueTitle,
		"repo_prefix": repo.Prefix,
	}, in.Message)

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

// agentCandidate is the small projection AutoPickFreeAgent works on —
// just the bits the picker decides from. Keeping it raw (no DTO shape)
// lets REST, CLI, and desktop share the picker without pulling each
// other's render types into client.
type agentCandidate struct {
	AgentName       string
	Ended           bool
	Busy            bool
	HasChannel      bool
	HasOpenDispatch bool
}

// autoPickFreeAgent returns the first candidate eligible to take
// auto-dispatched work: live, not busy, has a channel, no un-acked
// dispatch already queued against it, and a persistent identity slug
// (CreateDispatch routes by slug). Candidates are expected in
// last-seen-DESC order; the first match wins so the freshest free
// agent gets the work.
func autoPickFreeAgent(cands []agentCandidate) string {
	for _, c := range cands {
		if c.Ended || c.Busy || c.AgentName == "" || !c.HasChannel || c.HasOpenDispatch {
			continue
		}
		return c.AgentName
	}
	return ""
}

// AutoDispatchIssue (BACI-40) is the state-gated auto-pick dispatch
// verb shared by the desktop per-card action button, the REST
// `POST /repos/{prefix}/issues/{key}/dispatch` route, and the CLI's
// target-less `bacio agent dispatch <key> --mode <stage>`. It mirrors
// what desktop's BoardService.DispatchIssue used to do inline.
func (c *localClient) AutoDispatchIssue(ctx context.Context, repo *model.Repo, issueKey, mode string, dryRun bool) (*model.AgentDispatch, error) {
	if repo == nil {
		return nil, fmt.Errorf("AutoDispatchIssue requires a repo")
	}
	parsedMode, err := model.ParseDispatchMode(mode)
	if err != nil {
		return nil, err
	}
	if parsedMode == "" {
		return nil, fmt.Errorf("a dispatch mode is required")
	}

	// State-gate: the prompt for this stage must be valid to run from
	// the issue's current state. The desktop UI already gates the
	// per-card action button on this; re-check here so a stale UI or
	// a CLI/REST caller can't queue a prompt the state doesn't allow.
	iss, err := c.GetIssueByKey(ctx, repo, issueKey)
	if err != nil {
		return nil, err
	}
	gates, err := c.GetPromptStates(ctx)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(gates[mode], string(iss.State)) {
		return nil, fmt.Errorf("the %s prompt can't run from a %s issue", mode, iss.State)
	}

	// Auto-pick: build a candidate list (last-seen-DESC, since
	// ListAgentSessions orders that way) and let autoPickFreeAgent
	// choose the freshest free agent.
	sessions, err := c.store.ListAgentSessions(store.AgentSessionFilter{
		RepoID:         &repo.ID,
		RegisteredOnly: true,
	})
	if err != nil {
		return nil, err
	}
	openClaims, err := c.store.OpenClaimsBySession(repo.ID)
	if err != nil {
		return nil, err
	}
	dispatches, err := c.store.ListDispatches(store.DispatchFilter{RepoID: &repo.ID})
	if err != nil {
		return nil, err
	}
	occupiedAgentID := map[int64]bool{}
	occupiedSession := map[string]bool{}
	for _, d := range dispatches {
		if d.Status != model.DispatchPending && d.Status != model.DispatchDelivered {
			continue
		}
		if d.TargetAgentID != nil {
			occupiedAgentID[*d.TargetAgentID] = true
		}
		if d.TargetSessionID != "" {
			occupiedSession[d.TargetSessionID] = true
		}
	}
	now := time.Now()
	cands := make([]agentCandidate, 0, len(sessions))
	for _, s := range sessions {
		cand := agentCandidate{
			AgentName:  s.AgentName,
			Ended:      model.SessionLiveness(s, now) == "ended",
			HasChannel: s.ChannelSeenAt != nil,
		}
		if !cand.Ended {
			cand.Busy, _ = model.SessionBusy(openClaims[s.ID])
		}
		if s.AgentID != nil && occupiedAgentID[*s.AgentID] {
			cand.HasOpenDispatch = true
		}
		if occupiedSession[s.SessionID] {
			cand.HasOpenDispatch = true
		}
		cands = append(cands, cand)
	}
	agentName := autoPickFreeAgent(cands)
	if agentName == "" {
		return nil, fmt.Errorf("no free agent available — every agent is busy or offline")
	}

	return c.CreateDispatch(ctx, repo, inputs.AgentDispatchInput{
		TargetAgent: agentName,
		IssueKey:    iss.Key,
		Mode:        mode,
	}, dryRun)
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
	// Drain pending AND delivered — anything not yet acked. `delivered`
	// only means "we tried to hand it over once"; if that push was lost
	// (a dead channel process, a notification the session never surfaced)
	// the dispatch would be silently stranded. Re-surfacing un-acked
	// dispatches on every hook drain is the reliable recovery path — only
	// an ack retires a dispatch.
	open, err := c.store.ListDispatches(store.DispatchFilter{
		TargetAgentID:   sess.AgentID,
		TargetSessionID: sess.SessionID,
		Statuses:        []model.DispatchStatus{model.DispatchPending, model.DispatchDelivered},
	})
	if err != nil {
		return nil, err
	}
	return c.markDrained(open)
}

// markDrained flips any still-pending dispatches to delivered and returns
// the list unchanged otherwise. Already-delivered rows are passed through
// as-is (re-emitted by the caller until acked).
func (c *localClient) markDrained(open []*model.AgentDispatch) ([]*model.AgentDispatch, error) {
	out := make([]*model.AgentDispatch, 0, len(open))
	for _, d := range open {
		if d.Status == model.DispatchPending {
			delivered, err := c.store.MarkDispatchDelivered(d.ID)
			if err != nil {
				return nil, err
			}
			out = append(out, delivered)
			continue
		}
		out = append(out, d)
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
	// Same pending-AND-delivered drain as DrainDispatches: an un-acked
	// dispatch stays drainable so a lost push can still be recovered. The
	// channel caller dedups within a process so it doesn't re-push every
	// poll tick; a fresh channel process re-pushes un-acked work.
	open, err := c.store.ListDispatches(store.DispatchFilter{
		RepoID:        &repo.ID,
		TargetAgentID: &ag.ID,
		Statuses:      []model.DispatchStatus{model.DispatchPending, model.DispatchDelivered},
	})
	if err != nil {
		return nil, err
	}
	return c.markDrained(open)
}

func (c *localClient) ListPromptTemplates(ctx context.Context) ([]*store.PromptTemplate, error) {
	return c.store.ListPromptTemplates()
}

func (c *localClient) GetPromptTemplate(ctx context.Context, slug string) (*store.PromptTemplate, error) {
	return c.store.GetPromptTemplateBySlug(slug)
}

func (c *localClient) AddPromptTemplate(ctx context.Context, in inputs.SettingsTemplateAddInput, dryRun bool) (*store.PromptTemplate, error) {
	addIn := store.AddPromptTemplateIn{
		Slug:          in.Slug,
		Name:          in.Name,
		Body:          in.Body,
		AllowedStates: stringsToStates(in.States),
		// IsBuiltin is never set by an agent-facing add — only the
		// migration's seed step and RestoreBuiltinPromptTemplates flip
		// it on.
	}
	if dryRun {
		return c.store.ValidateAddPromptTemplate(addIn)
	}
	t, err := c.store.AddPromptTemplate(addIn)
	if err != nil {
		return nil, err
	}
	id := t.ID
	c.recordOp(model.HistoryEntry{
		Op: "template.create", Kind: "app_setting",
		TargetID: &id, TargetLabel: "prompt_template:" + t.Slug,
		Details: fmt.Sprintf("slug=%s, name=%s", t.Slug, t.Name),
	})
	return t, nil
}

func (c *localClient) RenamePromptTemplate(ctx context.Context, in inputs.SettingsTemplateRenameInput, dryRun bool) (*store.PromptTemplate, error) {
	// A rename with empty NewSlug means "keep slug, change name"; copy
	// the existing slug in so the validator + store don't need to
	// special-case it.
	existing, err := c.store.GetPromptTemplateBySlug(in.Slug)
	if err != nil {
		return nil, err
	}
	target := store.RenamePromptTemplateIn{
		OldSlug: in.Slug,
		NewSlug: in.NewSlug,
		NewName: in.NewName,
	}
	if target.NewSlug == "" {
		target.NewSlug = in.Slug
	}
	if dryRun {
		// Project the would-be row without writing. Validate both slug
		// shapes; surface the existing row's other fields untouched.
		if _, err := store.ValidatePromptTemplateSlug(target.OldSlug); err != nil {
			return nil, fmt.Errorf("old slug: %w", err)
		}
		if _, err := store.ValidatePromptTemplateSlug(target.NewSlug); err != nil {
			return nil, fmt.Errorf("new slug: %w", err)
		}
		projected := *existing
		projected.Slug = target.NewSlug
		if target.NewName != "" {
			name, err := store.ValidatePromptTemplateName(target.NewName)
			if err != nil {
				return nil, err
			}
			projected.Name = name
		}
		return &projected, nil
	}
	renamed, err := c.store.RenamePromptTemplate(target)
	if err != nil {
		return nil, err
	}
	id := renamed.ID
	details := fmt.Sprintf("from=%s, to=%s, name=%s", in.Slug, renamed.Slug, renamed.Name)
	c.recordOp(model.HistoryEntry{
		Op: "template.rename", Kind: "app_setting",
		TargetID: &id, TargetLabel: "prompt_template:" + renamed.Slug,
		Details: details,
	})
	return renamed, nil
}

func (c *localClient) DeletePromptTemplate(ctx context.Context, in inputs.SettingsTemplateRmInput, dryRun bool) (*store.PromptTemplate, error) {
	existing, err := c.store.GetPromptTemplateBySlug(in.Slug)
	if err != nil {
		return nil, err
	}
	if dryRun {
		return existing, nil
	}
	removed, err := c.store.DeletePromptTemplate(in.Slug)
	if err != nil {
		return nil, err
	}
	id := removed.ID
	c.recordOp(model.HistoryEntry{
		Op: "template.delete", Kind: "app_setting",
		TargetID: &id, TargetLabel: "prompt_template:" + removed.Slug,
		Details: "slug=" + removed.Slug,
	})
	return removed, nil
}

func (c *localClient) RestoreBuiltinPromptTemplates(ctx context.Context, dryRun bool) ([]string, error) {
	if dryRun {
		// Compute the would-be list without touching the table.
		existing, err := c.store.ListPromptTemplates()
		if err != nil {
			return nil, err
		}
		present := make(map[string]bool, len(existing))
		for _, t := range existing {
			present[t.Slug] = true
		}
		var would []string
		for _, slug := range model.BuiltinTemplateSlugs() {
			if !present[slug] {
				would = append(would, slug)
			}
		}
		return would, nil
	}
	created, err := c.store.RestoreBuiltinPromptTemplates()
	if err != nil {
		return nil, err
	}
	if len(created) > 0 {
		c.recordOp(model.HistoryEntry{
			Op: "template.restore_defaults", Kind: "app_setting",
			TargetLabel: "prompt_template",
			Details:     "slugs=" + strings.Join(created, ","),
		})
	}
	return created, nil
}

func (c *localClient) GetPromptTemplates(ctx context.Context) (map[string]string, error) {
	all, err := c.store.AllPromptTemplates()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(all))
	for m, t := range all {
		out[string(m)] = t
	}
	return out, nil
}

func (c *localClient) SetPromptTemplate(ctx context.Context, mode, body string, dryRun bool) error {
	m, err := model.ParseDispatchMode(mode)
	if err != nil {
		return err
	}
	if m == "" {
		return fmt.Errorf("prompt template requires a slug")
	}
	if dryRun {
		// Validate the body at the store boundary, then stop before the
		// write — same shape as every other --dry-run mutation.
		_, err := c.store.ValidatePromptTemplate(m, body)
		return err
	}
	if err := c.store.SetPromptTemplate(m, body); err != nil {
		return err
	}
	// Prompt templates are global, not repo-scoped — the audit row
	// carries no RepoID. recordOp never fails the user-visible action.
	op := "prompt_template.update"
	if strings.TrimSpace(body) == "" {
		op = "prompt_template.reset"
	}
	c.recordOp(model.HistoryEntry{
		Op: op, Kind: "app_setting",
		TargetLabel: "prompt_template:" + string(m),
		Details:     "slug=" + string(m),
	})
	return nil
}

func (c *localClient) GetPromptStates(ctx context.Context) (map[string][]string, error) {
	all, err := c.store.AllPromptStates()
	if err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(all))
	for m, states := range all {
		ss := make([]string, len(states))
		for i, st := range states {
			ss[i] = string(st)
		}
		out[string(m)] = ss
	}
	return out, nil
}

func (c *localClient) SetPromptStates(ctx context.Context, mode string, states []string, dryRun bool) error {
	m, err := model.ParseDispatchMode(mode)
	if err != nil {
		return err
	}
	if m == "" {
		return fmt.Errorf("prompt state-gate requires a slug")
	}
	parsed := stringsToStates(states)
	if dryRun {
		// Validate the state set at the store boundary, then stop before
		// the write — same shape as every other --dry-run mutation.
		_, err := c.store.ValidatePromptStates(m, parsed)
		return err
	}
	if err := c.store.SetPromptStates(m, parsed); err != nil {
		return err
	}
	// Prompt state-gates are global, not repo-scoped — the audit row
	// carries no RepoID. recordOp never fails the user-visible action.
	op := "prompt_states.update"
	if len(states) == 0 {
		op = "prompt_states.reset"
	}
	c.recordOp(model.HistoryEntry{
		Op: op, Kind: "app_setting",
		TargetLabel: "prompt_states:" + string(m),
		Details:     "slug=" + string(m),
	})
	return nil
}

// stringsToStates is the wire-to-model helper for state-gate inputs.
// Each element is a model.State string cast directly; the validator on
// the store boundary rejects unknown values.
func stringsToStates(states []string) []model.State {
	out := make([]model.State, len(states))
	for i, s := range states {
		out[i] = model.State(s)
	}
	return out
}

func (c *localClient) GetBoardPreferences(ctx context.Context) (BoardPreferences, error) {
	hide, err := c.store.GetBoardHideEmptyColumns()
	if err != nil {
		return BoardPreferences{}, err
	}
	return BoardPreferences{HideEmptyColumns: hide}, nil
}

func (c *localClient) SetBoardPreferences(ctx context.Context, prefs BoardPreferences, dryRun bool) error {
	if dryRun {
		// A bool can't be malformed — there's nothing to validate, so a
		// dry-run is a no-op: returns nil, writes nothing, same shape as
		// every other --dry-run mutation.
		return nil
	}
	if err := c.store.SetBoardHideEmptyColumns(prefs.HideEmptyColumns); err != nil {
		return err
	}
	// Board preferences are global, not repo-scoped — the audit row
	// carries no RepoID, same as the prompt-template rows. recordOp
	// never fails the user-visible action.
	c.recordOp(model.HistoryEntry{
		Op: "board_prefs.update", Kind: "app_setting",
		TargetLabel: "board.hide_empty_columns",
		Details:     fmt.Sprintf("hide_empty_columns=%t", prefs.HideEmptyColumns),
	})
	return nil
}

// dispatchTargetLabel picks the most specific label for audit rows:
// the agent identity slug if there is one, else the session id.
func dispatchTargetLabel(d *model.AgentDispatch) string {
	if d.TargetAgentName != "" {
		return d.TargetAgentName
	}
	return d.TargetSessionID
}

// SetupDispatchCreator marks dispatches that the bacio channel itself
// enqueued to ask the agent to call the `register` tool — distinct from
// any human or supervisor creator. The channel filters on this value to
// keep EnsureSetupDispatch idempotent across restarts.
const SetupDispatchCreator = "bacio-channel"

// setupDispatchPayload is the content of the channel-emitted setup
// dispatch. The agent sees this as a regular dispatch (so it lands via
// the proven push path, not a synthetic notification) and acks it via
// the normal reply tool. $CLAUDE_CODE_SESSION_ID is a literal — the
// agent substitutes it from its own env.
const setupDispatchPayload = "Call the bacio MCP `register` tool now with " +
	"{\"session_id\": \"$CLAUDE_CODE_SESSION_ID\", \"model\": \"<your model id>\", " +
	"\"branch\": \"<your current git branch>\"} " +
	"(session_id is the only required field — find it in your env as $CLAUDE_CODE_SESSION_ID; " +
	"model + branch are optional but worth passing — " +
	"the model identifier looks like \"claude-opus-4-7\" or \"claude-sonnet-4-6\"). " +
	"This completes the registration; ack this dispatch via `reply` once you've called register."

func (c *localClient) EnsureSetupDispatch(ctx context.Context, repo *model.Repo, sessionID string) (*model.AgentDispatch, error) {
	if repo == nil || sessionID == "" {
		return nil, nil // channel runs idle until a session for its (host, claude_pid) exists
	}
	existing, err := c.store.ListDispatches(store.DispatchFilter{
		RepoID:          &repo.ID,
		TargetSessionID: sessionID,
		Statuses:        []model.DispatchStatus{model.DispatchPending, model.DispatchDelivered},
		CreatedBy:       SetupDispatchCreator,
	})
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return existing[0], nil // idempotent — drain will re-push it next tick
	}
	d, err := c.store.AddDispatch(store.AddDispatchIn{
		RepoID:          repo.ID,
		TargetSessionID: sessionID,
		Payload:         setupDispatchPayload,
		CreatedBy:       SetupDispatchCreator,
	})
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "agent.dispatch", Kind: "agent", Actor: SetupDispatchCreator,
		TargetID: &d.ID, TargetLabel: dispatchTargetLabel(d),
		Details: dispatchDetails(d),
	})
	return d, nil
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
