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
	"github.com/mrgeoffrich/bacio/internal/store"
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

// OpenClaimsForSession isn't wired over HTTP — the BACI-126b gate is a
// CLI-only guard for now (the API doesn't auto-bind a session id to a
// request). Return ErrLocalOnly so an agent that's mid-conversion to
// --remote gets a clear error.
func (c *remoteClient) OpenClaimsForSession(ctx context.Context, sessionID string) ([]string, error) {
	return nil, ErrLocalOnly
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

func (c *remoteClient) CancelDispatch(ctx context.Context, in inputs.AgentCancelInput, dryRun bool) (*model.AgentDispatch, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.AgentDispatch
	path := "/agents/dispatches/" + strconv.FormatInt(in.ID, 10) + "/cancel"
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

// AutoDispatchIssue (BACI-40) targets the new REST route, sending only
// the mode in the body — the issue is in the URL and the server picks
// the free agent + re-checks the state-gate. Returns the queued
// dispatch on success, or the projected row when dryRun is set.
func (c *remoteClient) AutoDispatchIssue(ctx context.Context, repo *model.Repo, issueKey, mode string, dryRun bool) (*model.AgentDispatch, error) {
	if repo == nil {
		return nil, fmt.Errorf("AutoDispatchIssue requires a repo")
	}
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	body := inputs.IssueDispatchInput{Mode: mode}
	var out model.AgentDispatch
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo.Prefix+"/issues/"+issueKey+"/dispatch", q, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WaitingDispatchForIssue has no REST parity today — the spinner-as-
// cancel UI runs on the desktop / TUI processes that talk to the
// local store directly. A future web-mode follow-up would add the
// matching GET route; until then this returns ErrLocalOnly cleanly.
func (c *remoteClient) WaitingDispatchForIssue(ctx context.Context, repo *model.Repo, issueKey string) (*model.AgentDispatch, error) {
	return nil, remoteAgentNotSupported("waiting-dispatch-for-issue")
}

// CreateRescueDispatch (BACI-190) has no REST parity in this client
// today — the Rescue button calls the REST endpoint directly from the
// frontend via api.http.ts. A future remote-mode integration could
// add a POST helper; until then this returns ErrLocalOnly cleanly.
func (c *remoteClient) CreateRescueDispatch(ctx context.Context, dispatchID int64) (*model.AgentDispatch, error) {
	return nil, remoteAgentNotSupported("create-rescue-dispatch")
}

// QueueFollowOnDispatch (BACI-180) targets the REST follow-on route,
// sending only the mode in the body — the issue is in the URL and the
// server resolves the parent dispatch + re-checks the state-gate.
// Returns the dormant queued row on success, or the projected row when
// dryRun is set.
func (c *remoteClient) QueueFollowOnDispatch(ctx context.Context, repo *model.Repo, issueKey, mode string, dryRun bool) (*model.AgentDispatch, error) {
	if repo == nil {
		return nil, fmt.Errorf("QueueFollowOnDispatch requires a repo")
	}
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	body := inputs.AgentQueueFollowOnInput{IssueKey: issueKey, Mode: mode}
	var out model.AgentDispatch
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo.Prefix+"/issues/"+issueKey+"/followon", q, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CancelFollowOnDispatch (BACI-180) targets the REST follow-on route
// with DELETE. The server returns 200 + a JSON-null body when there
// was no dormant follow-on to cancel (idempotent no-op) — the remote
// client surfaces that as (nil, nil) to match the local client.
func (c *remoteClient) CancelFollowOnDispatch(ctx context.Context, repo *model.Repo, issueKey string, dryRun bool) (*model.AgentDispatch, error) {
	if repo == nil {
		return nil, fmt.Errorf("CancelFollowOnDispatch requires a repo")
	}
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	// Use a pointer destination so the server's JSON-null body decodes
	// as a nil *AgentDispatch (idempotent no-op) rather than the
	// zero-value struct.
	var out *model.AgentDispatch
	if err := c.do(ctx, http.MethodDelete, "/repos/"+repo.Prefix+"/issues/"+issueKey+"/followon", q, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AutoDispatchIssueWithFollowOn (BACI-209) targets the new REST route,
// sending the primary mode + follow-on mode in the body. The server
// resolves the issue from the URL, re-checks the primary's state-gate,
// and writes both rows in one transaction. Returns the parent
// dispatch on success (the follow-on is fetched via card.followOn on
// the next BoardCard refresh — mirrors how QueueFollowOnDispatch
// exposes the dormant row).
func (c *remoteClient) AutoDispatchIssueWithFollowOn(ctx context.Context, repo *model.Repo, issueKey, mode, followOnMode string, dryRun bool) (parent, followOn *model.AgentDispatch, err error) {
	if repo == nil {
		return nil, nil, fmt.Errorf("AutoDispatchIssueWithFollowOn requires a repo")
	}
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	body := inputs.AgentDispatchChainInput{
		IssueKey:     issueKey,
		Mode:         mode,
		FollowOnMode: followOnMode,
	}
	// The server returns the parent dispatch in the response body; the
	// follow-on is discoverable via the next BoardCard refresh so we
	// don't need to round-trip both rows.
	var out model.AgentDispatch
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo.Prefix+"/issues/"+issueKey+"/dispatch-chain", q, body, &out); err != nil {
		return nil, nil, err
	}
	return &out, nil, nil
}

// InflightByModeForRepo (BACI-145) has no REST parity today — the
// kanban / brief assembler runs in-process on the desktop or `bacio
// web` server, both of which talk to the local store. A future
// web-mode follow-up could surface a /repos/{prefix}/agents/inflight
// endpoint; until then this returns ErrLocalOnly cleanly.
func (c *remoteClient) InflightByModeForRepo(ctx context.Context, repo *model.Repo) (map[model.DispatchMode]int, error) {
	return nil, remoteAgentNotSupported("inflight-by-mode")
}

// InflightByModeBaseForRepo (BACI-227) shares the no-REST-parity
// shape of its per-mode sibling — same kanban-in-process consumer,
// same ErrLocalOnly surface for now.
func (c *remoteClient) InflightByModeBaseForRepo(ctx context.Context, repo *model.Repo) (map[store.InflightKey]int, error) {
	return nil, remoteAgentNotSupported("inflight-by-mode-base")
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

// SessionDispatches is local-only (used by the post-tool-use hook,
// which always talks to the local store).
func (c *remoteClient) SessionDispatches(ctx context.Context, sessionID string) ([]*model.AgentDispatch, error) {
	return nil, remoteAgentNotSupported("session-dispatches")
}

func (c *remoteClient) EnsureSetupDispatch(ctx context.Context, repo *model.Repo, sessionID string) (*model.AgentDispatch, error) {
	return nil, remoteAgentNotSupported("channel")
}

func (c *remoteClient) EnsurePingDispatch(ctx context.Context, sess *model.AgentSession) (*model.AgentDispatch, error) {
	return nil, remoteAgentNotSupported("channel-ping")
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

// Session-todo mirror methods — local-only in v1, like the rest of the
// agent registry's hook/channel-internal surface.

func (c *remoteClient) UpsertSessionTodoFromTask(ctx context.Context, sessionID, taskID, issueKey, content string, status model.TodoStatus, dispatchID *int64) error {
	return remoteAgentNotSupported("todos")
}

func (c *remoteClient) ListSessionTodos(ctx context.Context, sessionID, issueKey string) ([]model.SessionTodo, error) {
	return nil, remoteAgentNotSupported("todos")
}

func (c *remoteClient) ListTodosBySessionsAndIssue(ctx context.Context, pairs []store.SessionIssuePair) (map[int64][]model.SessionTodo, error) {
	return nil, remoteAgentNotSupported("todos")
}

// Agent questions (BACI-53) — local-only at the channel/hook layer.
// AnswerSessionQuestion / CancelSessionQuestion / ListSessionQuestions
// are reachable over REST (Phase 4) so the desktop and web bundle
// route through the typed HTTP routes; the CLI client's remote-mode
// wiring for those verbs is the typical local-first / remote-follow-up
// pattern. AddSessionQuestion / AbandonOpenQuestionsForSession /
// DrainSettledQuestionsForSession stay channel-internal and never
// get HTTP parity (the channel is per-process; no external caller
// should be enqueuing asks).

// Prompt templates landed HTTP parity in BACI-36 for the body verbs
// (Get/SetPromptTemplate(s)); these thread through the same
// `c.do(...)` pattern as the BACI-34 agent verbs. An empty body on
// SetPromptTemplate is the reset signal: the helper switches the
// HTTP verb from PUT to DELETE so the server's reset handler runs.
// The cli/settings.go applyTemplate helper sends "" to mean "revert
// to default" — this layer transparently turns that into a DELETE.
// BACI-252 retired the per-template state-gate, so the SetPromptStates
// remote shim is gone alongside the CLI verb.
//
// The newer typed CRUD methods (List/GetPromptTemplate, AddPromptTemplate,
// RenamePromptTemplate, DeletePromptTemplate, RestoreBuiltinPromptTemplates)
// remain local-only in v1 — HTTP parity for them is a follow-up.

func (c *remoteClient) ListPromptTemplates(ctx context.Context) ([]*store.PromptTemplate, error) {
	return nil, remoteAgentNotSupported("prompt-templates")
}

func (c *remoteClient) GetPromptTemplate(ctx context.Context, slug string) (*store.PromptTemplate, error) {
	return nil, remoteAgentNotSupported("prompt-templates")
}

func (c *remoteClient) AddPromptTemplate(ctx context.Context, in inputs.SettingsTemplateAddInput, dryRun bool) (*store.PromptTemplate, error) {
	return nil, remoteAgentNotSupported("prompt-templates")
}

func (c *remoteClient) RenamePromptTemplate(ctx context.Context, in inputs.SettingsTemplateRenameInput, dryRun bool) (*store.PromptTemplate, error) {
	return nil, remoteAgentNotSupported("prompt-templates")
}

func (c *remoteClient) DeletePromptTemplate(ctx context.Context, in inputs.SettingsTemplateRmInput, dryRun bool) (*store.PromptTemplate, error) {
	return nil, remoteAgentNotSupported("prompt-templates")
}

func (c *remoteClient) RestoreBuiltinPromptTemplates(ctx context.Context, dryRun bool) ([]string, error) {
	return nil, remoteAgentNotSupported("prompt-templates")
}

func (c *remoteClient) SetPromptTemplateConcurrencyLimit(ctx context.Context, in inputs.SettingsTemplateSetConcurrencyInput, dryRun bool) (*store.PromptTemplate, error) {
	// The REST surface is at PUT /settings/templates/{mode}/concurrency
	// (BACI-51), but the CLI-side typed CRUD verbs route through the
	// local store today — keep the remote stub returning the standard
	// not-supported error until HTTP parity for the typed verbs lands.
	return nil, remoteAgentNotSupported("prompt-templates")
}

func (c *remoteClient) SetPromptTemplateActionLabel(ctx context.Context, in inputs.SettingsTemplateSetActionLabelInput, dryRun bool) (*store.PromptTemplate, error) {
	// The REST surface is at PUT /settings/templates/{mode}/action-label
	// (BACI-67), but the CLI-side typed CRUD verbs route through the
	// local store today — keep the remote stub returning the standard
	// not-supported error until HTTP parity for the typed verbs lands.
	return nil, remoteAgentNotSupported("prompt-templates")
}

func (c *remoteClient) GetPromptTemplates(ctx context.Context) (map[string]string, error) {
	var out map[string]string
	if err := c.do(ctx, http.MethodGet, "/settings/templates", nil, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]string{}
	}
	return out, nil
}

func (c *remoteClient) SetPromptTemplate(ctx context.Context, mode, body string, dryRun bool) error {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	path := "/settings/templates/" + url.PathEscape(mode)
	if body == "" {
		return c.do(ctx, http.MethodDelete, path, q, nil, nil)
	}
	in := map[string]string{"slug": mode, "body": body}
	return c.do(ctx, http.MethodPut, path, q, in, nil)
}

// Agent questions (BACI-53). REST parity for the user-side verbs
// (list / show / answer / cancel) ships in Phase 4 so the desktop
// and web bundle work in remote mode. The channel-side helpers
// (AddSessionQuestion / AbandonOpenQuestionsForSession /
// DrainSettledQuestionsForSession) remain local-only — they exist
// to drive the per-machine `bacio channel` shim, which always
// talks to the local store.

func (c *remoteClient) AddSessionQuestion(ctx context.Context, in AddSessionQuestionInput) (*model.SessionQuestion, error) {
	return nil, remoteAgentNotSupported("questions")
}

func (c *remoteClient) ListSessionQuestions(ctx context.Context, sessionID string, states []model.QuestionState) ([]*model.SessionQuestion, error) {
	q := url.Values{}
	if len(states) == 0 {
		// CLI default is "open" — pass through verbatim so the REST
		// surface returns the same shape.
		q.Set("state", "open")
	} else if len(states) == 1 {
		q.Set("state", string(states[0]))
	} else {
		// Multi-state: comma-separated.
		var b string
		for i, st := range states {
			if i > 0 {
				b += ","
			}
			b += string(st)
		}
		q.Set("state", b)
	}
	var out []*model.SessionQuestion
	if err := c.do(ctx, http.MethodGet, "/agents/sessions/"+url.PathEscape(sessionID)+"/questions", q, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.SessionQuestion{}
	}
	return out, nil
}

// ListOpenQuestionsBySessions stays local-only — the REST surface
// returns per-session lists, and a remote-mode UI making N
// round-trips defeats the bulk read's purpose.
func (c *remoteClient) ListOpenQuestionsBySessions(ctx context.Context, sessionIDs []string) (map[int64][]*model.SessionQuestion, error) {
	return nil, remoteAgentNotSupported("questions")
}

func (c *remoteClient) GetSessionQuestion(ctx context.Context, id int64) (*model.SessionQuestion, error) {
	var out model.SessionQuestion
	if err := c.do(ctx, http.MethodGet, "/agents/questions/"+strconv.FormatInt(id, 10), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) AnswerSessionQuestion(ctx context.Context, id int64, answers model.QuestionAnswers, dryRun bool) (*model.SessionQuestion, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	body := map[string]any{"answers": answers}
	var out model.SessionQuestion
	if err := c.do(ctx, http.MethodPost, "/agents/questions/"+strconv.FormatInt(id, 10)+"/answer", q, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) CancelSessionQuestion(ctx context.Context, id int64, dryRun bool) (*model.SessionQuestion, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.SessionQuestion
	if err := c.do(ctx, http.MethodPost, "/agents/questions/"+strconv.FormatInt(id, 10)+"/cancel", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) AbandonOpenQuestionsForSession(ctx context.Context, sessionID string) (int, error) {
	return 0, remoteAgentNotSupported("questions")
}

func (c *remoteClient) DrainSettledQuestionsForSession(ctx context.Context, sessionID string) ([]*model.SessionQuestion, error) {
	return nil, remoteAgentNotSupported("questions")
}
