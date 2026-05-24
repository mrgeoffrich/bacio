package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/agentmode"
	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/sync"
	"github.com/mrgeoffrich/bacio/internal/wtenv"
)

// skipUnlessAgentMode is the single guard every hook subcommand calls at
// the top of its RunE. When BACIO_AGENT_MODE is not set, the hook logs a
// one-line skip notice to stderr and returns true so the caller can
// `return nil` immediately — the "must NEVER fail the agent's session"
// invariant trumps every other concern, so this never returns an error.
// The name is included verbatim so a user tailing Claude Code's hook
// log can tell which subcommand bailed.
func skipUnlessAgentMode(subcommand string) bool {
	if agentmode.Enabled() {
		return false
	}
	fmt.Fprintf(os.Stderr, "bacio hook %s: %s not set — skipping\n", subcommand, agentmode.EnvVar)
	return true
}

// newHookCmd builds the hidden `bacio hook` command group — the Claude
// Code hook integration shim. Each subcommand reads a hook-event JSON
// payload on stdin (wired up by `bacio install-agent`), correlates it
// to the agent registry by session_id, and keeps the registry in sync
// without the agent calling `bacio agent …` by hand.
//
// Like `bacio tui` and `bacio api`, this is a harness-integration
// surface, not an agent-facing mutation command: it deliberately does
// NOT follow the six agent-CLI principles (no --json input, no schema
// entry, no --dry-run). It is documented in SKILL.md instead.
//
// Hard rule: a hook handler must NEVER fail the agent's session. Every
// path returns nil (exit 0); problems go to stderr, which Claude Code
// surfaces in the transcript without blocking the turn.
func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "hook",
		Short:  "Claude Code hook integration shim (reads event JSON on stdin)",
		Hidden: true,
	}
	cmd.AddCommand(
		hookSessionStartCmd(),
		hookUserPromptSubmitCmd(),
		hookStopCmd(),
		hookSessionEndCmd(),
		hookPostToolUseCmd(),
		hookPreToolUseCmd(),
	)
	return cmd
}

// hookInput is the union of the hook-event JSON fields bacio cares
// about. Claude Code sends a superset; unknown fields are ignored.
type hookInput struct {
	SessionID     string `json:"session_id"`
	CWD           string `json:"cwd"`
	HookEventName string `json:"hook_event_name"`
	Source        string `json:"source"` // SessionStart: startup|resume|clear|compact
	Reason        string `json:"reason"` // SessionEnd: clear|logout|...
}

func readHookInput() (*hookInput, error) {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}
	var in hookInput
	if len(strings.TrimSpace(string(raw))) == 0 {
		return &in, nil
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("parse hook input: %w", err)
	}
	return &in, nil
}

// hookContext is the resolved environment a hook handler operates in:
// the parsed payload, an open local client, the current repo, the agent
// identity slug, and the claude pid this hook descends from.
type hookContext struct {
	in        *hookInput
	c         client.Client
	repo      *model.Repo
	slug      string // identity slug for this claude process, "" if none
	actor     string // slug if set, else OS-user fallback
	claudePID int    // nearest `claude` ancestor pid, 0 if not found
}

func (h *hookContext) close() {
	if h != nil && h.c != nil {
		_ = h.c.Close()
	}
}

// loadHookContext reads the payload, moves into the agent's working
// directory, and wires up a local client + repo. It returns (nil, nil)
// — NOT an error — for the benign "nothing to do" cases (no session id,
// not a git repo, inside a sync repo) so callers can just `return nil`.
func loadHookContext() (*hookContext, error) {
	in, err := readHookInput()
	if err != nil {
		return nil, err
	}
	sid := in.SessionID
	if sid == "" {
		sid = strings.TrimSpace(os.Getenv(claudeSessionEnv))
	}
	if sid == "" {
		return nil, nil // nothing to correlate against
	}
	in.SessionID = sid

	if in.CWD != "" {
		if err := os.Chdir(in.CWD); err != nil {
			return nil, fmt.Errorf("chdir %s: %w", in.CWD, err)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	info, err := git.Detect(cwd)
	if err != nil {
		return nil, nil // not in a git repo — nothing to track
	}
	if sync.IsSyncRepo(info.Root) {
		return nil, nil // a sync repo, not a project repo
	}

	// claude_pid is keyed by the `claude` process this hook descends
	// from — the join column the channel uses to find sessions it's
	// serving. agents.json (the persistent slug-for-this-claude-pid
	// mapping) is populated by the channel's `register` tool *during*
	// register, not here at SessionStart — so this hook reads slug as
	// best-effort: empty on a fresh claude_pid, populated on subsequent
	// hooks (heartbeat / stop / end) for a claude_pid that has since
	// completed register.
	claudePID := findClaudeAncestor(os.Getpid())
	if claudePID == 0 {
		fmt.Fprintln(os.Stderr, "bacio hook: no `claude` ancestor process found — session can't be correlated to a channel")
	}
	slug := readAgentIdentity(info.Root, claudePID)
	act := slug
	if act == "" {
		act = actor()
	}

	// --remote is intentionally ignored here: the agent registry is
	// local-only, so the hook always talks to the local SQLite store.
	res, err := resolveEnv()
	if err != nil {
		return nil, err
	}
	if res.ManifestPath != "" {
		// Defensive log line — surfaces in Claude Code's hook log so a
		// misconfigured worktree (manifest in the wrong place, BACIO_ENV
		// pointing at the wrong file, etc.) shows up explicitly rather
		// than silently writing to the wrong DB.
		fmt.Fprintf(os.Stderr, "bacio hook: env source=%s db=%s manifest=%s\n", res.Source, res.DBPath, res.ManifestPath)
	}
	c, err := client.Open(context.Background(), client.Options{
		DBPath: res.DBPath,
		Actor:  act,
	})
	if err != nil {
		return nil, err
	}
	repo, _, err := c.EnsureRepo(context.Background(), info)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	return &hookContext{in: in, c: c, repo: repo, slug: slug, actor: act, claudePID: claudePID}, nil
}

// linkChannel stamps the session's claude_pid and lights up
// channel_seen_at when a live `bacio channel` is registered for the
// same (host, claude_pid). It's the hook side of the channel<->session
// join: the channel can't know its session id, but both it and this
// hook descend from the same `claude` process. Best-effort — a problem
// here is logged, never fails the hook. A claude_pid of 0 (no `claude`
// ancestor found) is passed through; the store treats it as "no
// channel" rather than erroring.
func (h *hookContext) linkChannel(sessionID string) {
	host, _ := os.Hostname()
	if err := h.c.LinkSessionChannel(context.Background(), sessionID, int64(h.claudePID), host); err != nil {
		fmt.Fprintln(os.Stderr, "bacio hook: link channel:", err)
	}
}

// ---------- session-start ----------

func hookSessionStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "session-start",
		Short:  "SessionStart hook: create a minimal session stub (channel's `register` tool enriches it)",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if skipUnlessAgentMode("session-start") {
				return nil
			}
			h, err := loadHookContext()
			if err != nil {
				fmt.Fprintln(os.Stderr, "bacio hook session-start:", err)
				return nil
			}
			if h == nil {
				return nil
			}
			defer h.close()

			// SessionStart writes only the bare minimum: session_id +
			// claude_pid + host. Identity mint, agents.json write, branch
			// detection, model — all happen later when the agent calls
			// the bacio channel's `register` tool, which has access to
			// the agent's own self-description and avoids the SQLite
			// write-contention race that lost identity mints under the
			// old "do everything at SessionStart" path.
			host, _ := os.Hostname()
			sess, err := h.c.CreateSessionStub(context.Background(), h.repo, h.in.SessionID, host, int64(h.claudePID))
			if err != nil {
				fmt.Fprintln(os.Stderr, "bacio hook session-start: create stub:", err)
				return nil
			}
			h.linkChannel(sess.SessionID)
			emitSessionStartContext(h, sess)
			emitDrainedDispatches(h, sess.SessionID)
			return nil
		},
	}
}

// emitSessionStartContext writes a short briefing to stdout. For a
// SessionStart hook, stdout is injected into the agent's context — so
// the agent starts the session already knowing what it owns.
func emitSessionStartContext(h *hookContext, sess *model.AgentSession) {
	var b strings.Builder
	if h.slug != "" {
		// Repeat-session for a claude_pid that previously registered:
		// agents.json has the slug so the briefing can name the agent
		// and surface any issues already assigned to it.
		fmt.Fprintf(&b, "[bacio] Session opened against repo %s as %s (waiting for `register` from the bacio channel to complete).\n", h.repo.Prefix, h.slug)
		// BACI-53 soft-nudge: point the agent at the bacio
		// MCP ask_user_question tool. The user sees the
		// question in their TUI / desktop / web window where
		// the issue context is already in front of them, and
		// the exchange is recorded in bacio's audit log.
		fmt.Fprintln(&b, "Use `mcp__bacio__ask_user_question` for any clarification — it surfaces in bacio's UI rather than Claude Code's own terminal modal.")

		if view, err := h.c.ShowAgentSession(context.Background(), sess.SessionID); err == nil {
			var open []string
			for _, cl := range view.Claims {
				if cl.ReleasedAt == nil {
					open = append(open, cl.IssueKey)
				}
			}
			if len(open) > 0 {
				fmt.Fprintf(&b, "Open claims carried into this session: %s\n", strings.Join(open, ", "))
			}
		}

		if assigned := h.assignedIssues(); len(assigned) > 0 {
			fmt.Fprintf(&b, "Issues assigned to %s:\n", h.slug)
			for _, i := range assigned {
				fmt.Fprintf(&b, "  %-12s %-12s %s\n", i.Key, i.State, i.Title)
			}
		}
	} else {
		// Fresh claude_pid — identity hasn't been minted yet (register
		// does that). Keep the briefing minimal; the channel will queue
		// the setup dispatch with full instructions for the agent.
		fmt.Fprintf(&b, "[bacio] Session opened against repo %s (stub %s — bacio channel's `register` tool will complete the registration).\n", h.repo.Prefix, shortID(sess.SessionID))
	}
	fmt.Fprint(os.Stdout, b.String())
}

// ---------- user-prompt-submit ----------

func hookUserPromptSubmitCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "user-prompt-submit",
		Short:  "UserPromptSubmit hook: heartbeat the session + nudge on open claims",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if skipUnlessAgentMode("user-prompt-submit") {
				return nil
			}
			h, err := loadHookContext()
			if err != nil {
				fmt.Fprintln(os.Stderr, "bacio hook user-prompt-submit:", err)
				return nil
			}
			if h == nil {
				return nil
			}
			defer h.close()

			sess := h.heartbeatOrRegister()
			if sess == nil {
				return nil
			}
			h.linkChannel(sess.SessionID)
			h.syncClaimedIssueStates(sess.SessionID, false)
			emitClaimNudge(h, sess)
			emitDrainedDispatches(h, sess.SessionID)
			return nil
		},
	}
}

// syncClaimedIssueStates keeps every issue this session holds an open
// claim on in lock-step with whether the agent is currently working it.
// When idle is true (Stop hook — the agent's turn ended), in_progress
// claims flip to needs_action; when idle is false (UserPromptSubmit
// hook — a new prompt arrived), the inverse fires. Only issues already
// in the matching "from" state are touched, so a no-op turn writes
// nothing — no SetIssueState call, no audit row, no updated_at bump.
// Best-effort: every error goes to stderr, never fails the hook.
func (h *hookContext) syncClaimedIssueStates(sessionID string, idle bool) {
	view, err := h.c.ShowAgentSession(context.Background(), sessionID)
	if err != nil {
		return
	}
	from, to := model.StateNeedsAction, model.StateInProgress
	if idle {
		from, to = model.StateInProgress, model.StateNeedsAction
	}
	for _, cl := range view.Claims {
		if cl.ReleasedAt != nil {
			continue
		}
		iss, err := h.c.GetIssueByKey(context.Background(), h.repo, cl.IssueKey)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bacio hook: lookup", cl.IssueKey+":", err)
			continue
		}
		if iss.State != from {
			continue
		}
		if _, err := h.c.SetIssueState(context.Background(), h.repo, cl.IssueKey, to, false); err != nil {
			fmt.Fprintln(os.Stderr, "bacio hook: flip", cl.IssueKey+":", err)
		}
	}
}

// emitClaimNudge writes a gentle reminder to stdout (injected into the
// agent's context on UserPromptSubmit) when the session is holding open
// claims. It never blocks — the agent reads it and carries on. This is
// the Phase 1 "soft-nudge" supervision gate.
func emitClaimNudge(h *hookContext, sess *model.AgentSession) {
	view, err := h.c.ShowAgentSession(context.Background(), sess.SessionID)
	if err != nil {
		return
	}
	var open []string
	for _, cl := range view.Claims {
		if cl.ReleasedAt == nil {
			open = append(open, cl.IssueKey)
		}
	}
	if len(open) == 0 {
		return
	}
	fmt.Fprintf(os.Stdout,
		"[bacio] You still hold open claims on %s — when that work is done, move the issue to in_review/done and run `bacio agent release` on the claim.\n",
		strings.Join(open, ", "))
}

// emitDrainedDispatches drains the session's open dispatches and writes
// them to stdout — injected into the agent's context. Called by the
// SessionStart and UserPromptSubmit hooks: this is the pull-delivery
// path. It drains every un-acked dispatch (pending AND delivered), so a
// dispatch whose push was lost is re-surfaced on the next prompt — only
// an ack retires it. A pending dispatch is flipped to "delivered" as it
// drains.
func emitDrainedDispatches(h *hookContext, sessionID string) {
	ds, err := h.c.DrainDispatches(context.Background(), sessionID)
	if err != nil || len(ds) == 0 {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[bacio] %d open dispatch(es) queued for you:\n", len(ds))
	for _, d := range ds {
		issue := ""
		if d.IssueKey != "" {
			issue = " [" + d.IssueKey + "]"
		}
		mode := ""
		if d.Mode != "" {
			mode = " (" + string(d.Mode) + ")"
		}
		fmt.Fprintf(&b, "  #%d%s%s from %s", d.ID, issue, mode, d.CreatedBy)
		if d.Payload != "" {
			fmt.Fprintf(&b, ": %s", d.Payload)
		}
		b.WriteByte('\n')
	}
	fmt.Fprint(&b, "Ack each once handled: `bacio agent ack <id> --note \"...\"`.\n")
	fmt.Fprint(os.Stdout, b.String())
}

// ---------- stop ----------

func hookStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "stop",
		Short:  "Stop hook: heartbeat the session",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if skipUnlessAgentMode("stop") {
				return nil
			}
			h, err := loadHookContext()
			if err != nil {
				fmt.Fprintln(os.Stderr, "bacio hook stop:", err)
				return nil
			}
			if h == nil {
				return nil
			}
			defer h.close()
			if sess := h.heartbeatOrRegister(); sess != nil {
				h.linkChannel(sess.SessionID)
				h.syncClaimedIssueStates(sess.SessionID, true)
			}
			return nil
		},
	}
}

// ---------- post-tool-use ----------

// postToolUseInput is the slice of the Claude Code PostToolUse payload
// the task-list mirror cares about. Both TaskCreate and TaskUpdate go
// through this single struct; tool_input + tool_response together
// carry every field the upsert path needs. The decoder ignores
// unknown fields, so this is a strict subset (no need to teach it
// about every other tool's input shape).
//
// TaskCreate:
//   tool_input  = {subject, description?, activeForm?}
//   tool_response = {task: {id, subject}}
//   → use tool_response.task.id (Claude Code-assigned) as the key,
//     tool_input.subject as the content, status defaults to pending.
//
// TaskUpdate:
//   tool_input  = {taskId, status?}
//   tool_response = {success, taskId, updatedFields, statusChange: {from, to}}
//   → use tool_input.taskId as the key, tool_input.status (or
//     tool_response.statusChange.to as a fallback) as the new status,
//     leave content alone (the original subject was set on create).
type postToolUseInput struct {
	SessionID     string `json:"session_id"`
	CWD           string `json:"cwd"`
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	ToolInput     struct {
		Subject string `json:"subject"` // TaskCreate
		TaskID  string `json:"taskId"`  // TaskUpdate
		Status  string `json:"status"`  // TaskUpdate (sometimes)
	} `json:"tool_input"`
	ToolResponse struct {
		Task struct {
			ID      string `json:"id"`
			Subject string `json:"subject"`
		} `json:"task"` // TaskCreate
		StatusChange struct {
			To string `json:"to"`
		} `json:"statusChange"` // TaskUpdate
	} `json:"tool_response"`
}

func readPostToolUseInput() (*postToolUseInput, error) {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}
	var in postToolUseInput
	if len(strings.TrimSpace(string(raw))) == 0 {
		return &in, nil
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("parse hook input: %w", err)
	}
	return &in, nil
}

// postToolUseMatcher is the regex installed in .claude/settings.json's
// PostToolUse matcher field — pipe-alternation per Claude Code's
// matcher syntax. Keeping the literal here so the install-agent plan
// and the hook code can't drift.
const postToolUseMatcher = "TaskCreate|TaskUpdate"

func hookPostToolUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "post-tool-use",
		Short:  "PostToolUse hook (matcher: TaskCreate|TaskUpdate): mirror the agent's task list",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if skipUnlessAgentMode("post-tool-use") {
				return nil
			}
			in, err := readPostToolUseInput()
			if err != nil {
				fmt.Fprintln(os.Stderr, "bacio hook post-tool-use:", err)
				return nil
			}
			if in.SessionID == "" {
				return nil // nothing to correlate against
			}

			taskID, content, statusRaw, ok := extractTaskFields(in)
			if !ok {
				// Unknown tool_name (e.g. matcher widened, or a future
				// Task* variant we don't model) — log a one-liner so
				// the user can notice the drift, but don't fail the
				// session.
				if in.ToolName != "" && in.ToolName != "TaskCreate" && in.ToolName != "TaskUpdate" {
					fmt.Fprintf(os.Stderr, "bacio hook post-tool-use: unhandled tool_name %q; skipping\n", in.ToolName)
				}
				return nil
			}
			status, err := model.ParseTodoStatus(statusRaw)
			if err != nil {
				fmt.Fprintf(os.Stderr, "bacio hook post-tool-use: skip bad status %q: %v\n", statusRaw, err)
				return nil
			}

			// The agent registry is local-only, so the hook always talks
			// to the local SQLite store — --remote is intentionally
			// ignored (matches the other four hooks). Unlike them we
			// skip the git.Detect + EnsureRepo dance: the FK is to
			// agent_sessions, not repos, so no working-directory context
			// is needed. Less plumbing, less startup latency on a
			// high-frequency hook, no spurious "not in a git repo"
			// stderr noise if the agent runs Task* from a non-git
			// scratch dir.
			c, err := client.Open(context.Background(), client.Options{
				DBPath: opts.dbPath,
				Actor:  actor(),
			})
			if err != nil {
				fmt.Fprintln(os.Stderr, "bacio hook post-tool-use: open:", err)
				return nil
			}
			defer c.Close()

			issueKey := resolveOpenClaimIssueKey(context.Background(), c, in.SessionID)
			// BACI-132: resolve the active dispatch for the (session,
			// issue) at TaskCreate time so the row carries the dispatch
			// scope. Only matters for inserts — TaskUpdate keeps the
			// row's original dispatch_id, so spending the lookup on the
			// update path is wasted work. A non-nil dispatchID paired
			// with an empty issueKey is rejected at the store boundary
			// (defence-in-depth); if the dispatch can't be resolved we
			// drop the row to the orphan bucket (empty issueKey, nil
			// dispatchID) the same way the zero/many-claims path
			// already does.
			var dispatchID *int64
			if issueKey != "" && in.ToolName == "TaskCreate" {
				dispatchID = resolveActiveDispatchID(context.Background(), c, in.SessionID, issueKey)
				if dispatchID == nil {
					issueKey = ""
				}
			}
			if err := c.UpsertSessionTodoFromTask(context.Background(), in.SessionID, taskID, issueKey, content, status, dispatchID); err != nil {
				fmt.Fprintln(os.Stderr, "bacio hook post-tool-use: upsert:", err)
			}
			return nil
		},
	}
}

// resolveActiveDispatchID returns the id of the most-recent
// non-cancelled dispatch targeting (session, issue) — the BACI-132
// per-dispatch scope for a freshly-inserted SessionTodo row. Mirrors
// boardcards.pickActiveDispatch (newest non-cancelled match on the
// (session, issue) pair, agent-identity-fallback for targeting) but
// returns just the id since that's all the store layer needs.
// Best-effort: any lookup error returns nil so the caller drops the
// row to the orphan bucket instead of failing the hook.
func resolveActiveDispatchID(ctx context.Context, c client.Client, sessionID, issueKey string) *int64 {
	if sessionID == "" || issueKey == "" {
		return nil
	}
	dispatches, err := c.SessionDispatches(ctx, sessionID)
	if err != nil {
		return nil
	}
	// SessionDispatches returns newest-first already, but be explicit
	// rather than relying on the contract — pick the latest by
	// CreatedAt, skipping cancelled rows and anything not targeting
	// this issue. A multi-issue session that's stamped TaskCreates
	// against the old issue while a new dispatch is in flight on
	// another issue still gets the right dispatch picked, since we
	// filter on IssueKey first.
	var best *model.AgentDispatch
	for _, d := range dispatches {
		if d == nil || d.IssueKey != issueKey {
			continue
		}
		if d.Status == model.DispatchCancelled {
			continue
		}
		if best == nil || d.CreatedAt.After(best.CreatedAt) {
			best = d
		}
	}
	if best == nil {
		return nil
	}
	id := best.ID
	return &id
}

// resolveOpenClaimIssueKey returns the issue key the post-tool-use
// hook should stamp on a freshly-inserted SessionTodo row. When the
// session has exactly one open claim, that claim's IssueKey is the
// scope for the new row. Zero or many open claims fall back to "" —
// the orphan bucket the per-(session, issue) UI lookups deliberately
// skip, since neither a no-claim session nor a paired session can
// disambiguate which job a new task belongs to. Best-effort: any
// transient lookup error returns "" so the upsert still proceeds
// (the issue stamp is a UI-grouping signal, not a correctness one).
func resolveOpenClaimIssueKey(ctx context.Context, c client.Client, sessionID string) string {
	view, err := c.ShowAgentSession(ctx, sessionID)
	if err != nil {
		return ""
	}
	var only *model.AgentClaim
	for _, cl := range view.Claims {
		if cl == nil || cl.ReleasedAt != nil {
			continue
		}
		if only != nil {
			return "" // more than one open claim — orphan
		}
		only = cl
	}
	if only == nil {
		return ""
	}
	return only.IssueKey
}

// extractTaskFields pulls (task_id, content, status) from the
// PostToolUse payload based on the tool_name. Returns ok=false for
// any tool_name bacio doesn't model — the caller silently drops
// those events.
//
// TaskCreate: id comes from tool_response.task.id (Claude Code mints
// it); content from tool_input.subject (the agent's planned-task
// title); status defaults to "pending" because every newly created
// task starts pending and the response carries no status field.
//
// TaskUpdate: id and status both come from tool_input — the agent
// supplied them. statusChange.to in tool_response is a fallback only
// in case Claude Code starts omitting status from tool_input in some
// future variant. Empty content signals the upsert path to leave
// the existing subject alone.
func extractTaskFields(in *postToolUseInput) (taskID, content, status string, ok bool) {
	switch in.ToolName {
	case "TaskCreate":
		taskID = in.ToolResponse.Task.ID
		content = strings.TrimSpace(in.ToolInput.Subject)
		if content == "" {
			content = strings.TrimSpace(in.ToolResponse.Task.Subject)
		}
		status = string(model.TodoPending)
		return taskID, content, status, taskID != "" && content != ""
	case "TaskUpdate":
		taskID = in.ToolInput.TaskID
		status = in.ToolInput.Status
		if status == "" {
			status = in.ToolResponse.StatusChange.To
		}
		// content stays "" — the upsert path treats that as "leave the
		// existing subject alone" so a TaskUpdate doesn't blank it.
		return taskID, "", status, taskID != "" && status != ""
	}
	return "", "", "", false
}

// ---------- pre-tool-use ----------

// preToolUseInput is the slice of the Claude Code PreToolUse payload
// the worktree-confinement guard cares about. The matcher is exactly
// `Write|Edit`, both of which carry a `file_path` in tool_input; the
// decoder ignores unknown fields, so this is a strict subset and needs
// no knowledge of any other tool's input shape.
type preToolUseInput struct {
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath string `json:"file_path"` // Write / Edit
	} `json:"tool_input"`
}

func readPreToolUseInput() (*preToolUseInput, error) {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}
	var in preToolUseInput
	if len(strings.TrimSpace(string(raw))) == 0 {
		return &in, nil
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("parse hook input: %w", err)
	}
	return &in, nil
}

// preToolUseMatcher is the regex installed in .claude/settings.json's
// PreToolUse matcher field — pipe-alternation per Claude Code's matcher
// syntax. Keeping the literal here so the install-agent plan and the
// hook code can't drift (same convention as postToolUseMatcher). Bash
// is deliberately NOT in the matcher: tool_input.command is a raw
// string and parsing it for paths is fragile and easy to bypass.
// Confining the write tools defuses the Bash escape for free — with the
// parent checkout kept clean by the write-tool denial, a stray
// `cd <main> && git commit` has nothing to commit.
const preToolUseMatcher = "Write|Edit"

// preToolUseDecision is the verdict the confinement guard reaches for
// one tool call. allow=true emits nothing (the call proceeds); allow=
// false emits the PreToolUse deny JSON naming the worktree root so the
// model self-corrects. reason is non-empty only on a deny.
type preToolUseDecision struct {
	allow  bool
	reason string
}

// worktreeRootResolver finds the dispatch worktree root that confines a
// given cwd, or "" when cwd is not inside a manifest-bearing worktree.
// Defaulted to the wtenv-backed resolver; swapped in tests.
type worktreeRootResolver func(cwd string) string

// resolveWorktreeRoot walks up from cwd via the wtenv resolver and
// returns the directory of the worktree manifest (environment-config.yaml)
// — the allowed worktree root. Returns "" when no manifest fed
// resolution (Source default), i.e. this is not a dispatch worktree, so
// confinement does not engage. Any resolver error returns "" too:
// fail-open is the invariant, a guard that can't resolve must not deny.
func resolveWorktreeRoot(cwd string) string {
	res, err := wtenv.Resolve(wtenv.ResolveOpts{Cwd: cwd})
	if err != nil || res.ManifestPath == "" {
		return ""
	}
	return filepath.Dir(res.ManifestPath)
}

// pathWithin reports whether target is the directory root itself or a
// path strictly underneath it. It is boundary-safe: a plain
// strings.HasPrefix(target, root) would wrongly accept a sibling like
// `…/agent-abc-evil` for root `…/agent-abc`, so the under-root case
// requires the separator. Both inputs are expected to be cleaned
// absolute paths.
func pathWithin(root, target string) bool {
	if root == "" || target == "" {
		return false
	}
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if target == root {
		return true
	}
	return strings.HasPrefix(target, root+string(os.PathSeparator))
}

// evalSymlinksLenient resolves symlinks in path. A path component that
// does not exist yet (a brand-new file the worker is about to Write,
// possibly in a not-yet-created directory) is not an error: it walks up
// to the deepest existing ancestor, resolves symlinks there, then
// rejoins the missing tail — so a Write to a not-yet-created path still
// gets a symlink-safe answer that shares a prefix with a resolved
// worktree root. Returns the cleaned absolute path when nothing on the
// chain exists rather than failing — fail-open.
func evalSymlinksLenient(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	// Walk up to the deepest existing ancestor, resolve it, then rejoin
	// the non-existent tail. filepath.Dir eventually yields the root
	// ("/" or a volume) which always exists, so this terminates.
	var tail []string
	cur := path
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return path // reached the root without finding anything
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(append([]string{resolved}, tail...)...)
		}
		cur = parent
	}
}

// decidePreToolUse is the pure decision function for the PreToolUse
// confinement guard — no stdin, no stdout, directly unit-testable. It
// allows in every ambiguous or error case (fail-open) and denies ONLY
// on a positive "the file_path resolves outside a known dispatch
// worktree root" determination.
func decidePreToolUse(in *preToolUseInput, resolveRoot worktreeRootResolver) preToolUseDecision {
	allow := preToolUseDecision{allow: true}

	// Defends against a matcher widening or a tool variant we don't
	// model — only Write/Edit carry a file_path we can confine.
	if in.ToolName != "Write" && in.ToolName != "Edit" {
		return allow
	}
	if strings.TrimSpace(in.ToolInput.FilePath) == "" {
		return allow
	}

	// Confinement engages only when cwd sits inside a manifest-bearing
	// worktree — exactly the dispatched-worker case (`bacio worktree
	// init` is in every brief's Setup). No manifest → not a dispatch
	// worktree → allow.
	root := resolveRoot(in.CWD)
	if root == "" {
		return allow
	}

	// Normalise the target: a Write/Edit file_path is normally already
	// absolute, but resolve it against cwd defensively. Then eval
	// symlinks on both sides so a symlink can't slip the check.
	target := in.ToolInput.FilePath
	if !filepath.IsAbs(target) {
		base := in.CWD
		if base == "" {
			if wd, err := os.Getwd(); err == nil {
				base = wd
			}
		}
		target = filepath.Join(base, target)
	}
	target = evalSymlinksLenient(target)
	root = evalSymlinksLenient(root)

	if pathWithin(root, target) {
		return allow
	}
	return preToolUseDecision{
		allow: false,
		reason: fmt.Sprintf(
			"bacio: this dispatched-worker session is confined to its git worktree %s. "+
				"The %s file_path %s resolves outside it (the parent checkout). "+
				"Re-issue the edit with a path under %s.",
			root, in.ToolName, in.ToolInput.FilePath, root),
	}
}

// emitPreToolUseDeny writes the PreToolUse deny decision JSON to stdout.
// Claude Code reads this on a hook exit 0 and blocks the tool call,
// surfacing permissionDecisionReason to the model so it self-corrects.
func emitPreToolUseDeny(reason string) {
	out := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": reason,
		},
	}
	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "bacio hook pre-tool-use: encode decision:", err)
	}
}

func hookPreToolUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "pre-tool-use",
		Short:  "PreToolUse hook (matcher: Write|Edit): confine a dispatched worker to its git worktree",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// skipUnlessAgentMode: an interactive (non-agent) session is
			// never confined — only dispatched workers run agent mode.
			if skipUnlessAgentMode("pre-tool-use") {
				return nil
			}
			in, err := readPreToolUseInput()
			if err != nil {
				// Fail-open: a guard that can't read its input must not
				// wedge a legitimate session. Log and allow.
				fmt.Fprintln(os.Stderr, "bacio hook pre-tool-use:", err)
				return nil
			}
			d := decidePreToolUse(in, resolveWorktreeRoot)
			if d.allow {
				return nil
			}
			emitPreToolUseDeny(d.reason)
			return nil
		},
	}
}

// ---------- session-end ----------

func hookSessionEndCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "session-end",
		Short:  "SessionEnd hook: end the session and auto-release its claims",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if skipUnlessAgentMode("session-end") {
				return nil
			}
			h, err := loadHookContext()
			if err != nil {
				fmt.Fprintln(os.Stderr, "bacio hook session-end:", err)
				return nil
			}
			if h == nil {
				return nil
			}
			defer h.close()

			_, err = h.c.EndAgent(context.Background(), h.repo, inputs.AgentEndInput{
				SessionID: h.in.SessionID,
				Reason:    mapEndReason(h.in.Reason),
			}, false)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				fmt.Fprintln(os.Stderr, "bacio hook session-end:", err)
			}
			return nil
		},
	}
}

// ---------- helpers ----------

// heartbeatOrRegister bumps last_seen_at on the session, falling back
// to a full register when the session isn't in the registry yet — the
// SessionStart hook may not have run (e.g. hooks installed mid-session,
// or a harness that doesn't fire SessionStart).
func (h *hookContext) heartbeatOrRegister() *model.AgentSession {
	br, _ := detectBranch()
	sess, err := h.c.HeartbeatAgent(context.Background(), h.repo, inputs.AgentHeartbeatInput{
		SessionID: h.in.SessionID,
		Branch:    br,
	}, false)
	if err == nil {
		return sess
	}
	reg := inputs.AgentRegisterInput{
		SessionID: h.in.SessionID,
		Actor:     h.actor,
		Agent:     h.slug,
		Branch:    br,
	}
	if hn, err := os.Hostname(); err == nil {
		reg.Host = hn
	}
	sess, err = h.c.RegisterAgent(context.Background(), h.repo, reg, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bacio hook: register fallback:", err)
		return nil
	}
	return sess
}

// assignedIssues returns the non-terminal issues in this repo assigned
// to the agent's identity slug. client.IssueFilter has no assignee
// field, so we list and filter in memory — fine for the per-repo
// volumes bacio targets.
func (h *hookContext) assignedIssues() []*model.Issue {
	if h.slug == "" {
		return nil
	}
	issues, err := h.c.ListIssues(context.Background(), client.IssueFilter{
		Repo: h.repo,
		States: []model.State{
			model.StateTodo, model.StateInProgress,
			model.StateNeedsAction, model.StateInReview,
		},
	})
	if err != nil {
		return nil
	}
	var out []*model.Issue
	for _, i := range issues {
		if i.Assignee == h.slug {
			out = append(out, i)
		}
	}
	return out
}

// mapEndReason translates Claude Code's SessionEnd reason values to the
// bacio EndReason vocabulary. Claude sends clear|resume|logout|
// prompt_input_exit|bypass_permissions_disabled|other; bacio knows
// stop|clear|logout|crash|other. Anything unrecognised becomes "other".
func mapEndReason(r string) string {
	switch strings.TrimSpace(r) {
	case "clear":
		return string(model.EndReasonClear)
	case "logout":
		return string(model.EndReasonLogout)
	case "prompt_input_exit":
		return string(model.EndReasonStop)
	default:
		return string(model.EndReasonOther)
	}
}
