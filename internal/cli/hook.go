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
		hookStopFailureCmd(),
		hookSessionEndCmd(),
		hookPostToolUseCmd(),
		hookPostToolUseHeartbeatCmd(),
		hookPreToolUseCmd(),
		hookSetTitleCmd(),
	)
	return cmd
}

// hookInput is the union of the hook-event JSON fields bacio cares
// about. Claude Code sends a superset; unknown fields are ignored.
type hookInput struct {
	SessionID     string `json:"session_id"`
	CWD           string `json:"cwd"`
	HookEventName string `json:"hook_event_name"`
	Source        string `json:"source"`        // SessionStart: startup|resume|clear|compact
	Reason        string `json:"reason"`        // SessionEnd: clear|logout|...
	ErrorType     string `json:"error_type"`    // StopFailure: rate_limit|server_error|...
	ErrorMessage  string `json:"error_message"` // StopFailure: human blurb
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
	in           *hookInput
	c            client.Client
	repo         *model.Repo
	slug         string // identity slug for this claude process, "" if none
	worktreeSlug string // resolved wtenv manifest slug (BACI-305), "" outside a worktree env
	actor        string // slug if set, else OS-user fallback
	claudePID    int    // nearest `claude` ancestor pid, 0 if not found
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
		// Restore the original working directory on return so that
		// the hookContext's lifetime doesn't bleed into the caller's
		// process — only matters in tests (which share a process), but
		// good hygiene in production too.
		origCWD, cwdErr := os.Getwd()
		if cwdErr == nil {
			defer func() { _ = os.Chdir(origCWD) }()
		}
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
	return &hookContext{in: in, c: c, repo: repo, slug: slug, worktreeSlug: res.ManifestSlug(), actor: act, claudePID: claudePID}, nil
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
			sess, err := h.c.CreateSessionStub(context.Background(), h.repo, h.in.SessionID, host, h.worktreeSlug, int64(h.claudePID))
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
			h.clearErrorOnRecovery(sess)
			emitClaimNudge(h, sess)
			emitDrainedDispatches(h, sess.SessionID)
			return nil
		},
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
		"[bacio] You still hold open claims on %s — when that work is done, run `bacio agent release` on the claim.\n",
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
				h.clearErrorOnRecovery(sess)
			}
			return nil
		},
	}
}

// ---------- stop-failure ----------

// hookStopFailureCmd services the Claude Code StopFailure hook — fired
// when a turn ends because of an Anthropic API error (529 overloaded,
// 5xx, 429 rate_limit, auth/billing failures, …) rather than a normal
// stop (BACI-296). StopFailure is observe-only: Claude Code ignores the
// hook's stdout and exit code, so this can only RECORD state, never retry
// in place — recovery (re-arm / re-dispatch) is a supervisor/controller
// concern.
//
// Two best-effort halves, both fail-open:
//
//  1. Mark the agent errored. Stamp error_type / error_message / errored_at
//     on the session row so the kanban / Agents view shows the failure and
//     the dispatch picker stops handing it fresh work until it recovers.
//
//  2. Reconcile the in-flight Pipeline job. If the session held an open
//     claim on an in_pipeline card with a running job, drive the engine's
//     FailRunning branch — the card stays in_pipeline, paused with a
//     transient/terminal engine_pause_reason so the user can re-arm.
//
// Unlike the Stop hook this path deliberately does NOT clear the errored
// state — this turn *is* the failure. Recovery clearing happens on the
// next Stop / UserPromptSubmit, when the session takes a fresh turn.
func hookStopFailureCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "stop-failure",
		Short:  "StopFailure hook: record the API error on the agent + fail its in-flight pipeline job",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if skipUnlessAgentMode("stop-failure") {
				return nil
			}
			h, err := loadHookContext()
			if err != nil {
				fmt.Fprintln(os.Stderr, "bacio hook stop-failure:", err)
				return nil
			}
			if h == nil {
				return nil
			}
			defer h.close()

			// Resolve the session (registering a stub if SessionStart never
			// ran), but do NOT clear the error — see the doc comment.
			sess := h.heartbeatOrRegister()
			if sess == nil {
				return nil
			}
			h.linkChannel(sess.SessionID)

			// Half 1: always record the agent-errored state, regardless of
			// whether a pipeline job is in flight.
			if err := h.c.MarkAgentErrored(context.Background(), sess.SessionID, h.in.ErrorType, h.in.ErrorMessage); err != nil {
				fmt.Fprintln(os.Stderr, "bacio hook stop-failure: mark errored:", err)
			}

			// Half 2: reconcile the session's in-flight pipeline job per the
			// error class. A session with no in-flight pipeline claim is a
			// clean no-op inside FailPipelineForSession.
			if err := h.c.FailPipelineForSession(context.Background(), sess.SessionID, h.in.ErrorType, h.in.ErrorMessage); err != nil {
				fmt.Fprintln(os.Stderr, "bacio hook stop-failure: fail pipeline job:", err)
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
			// BACI-136: empty issueKey on TaskCreate (zero or many open
			// claims) is log-and-drop. The store rejects the orphan
			// insert at the boundary; pre-empting the call here keeps
			// the stderr line a clean one-liner instead of the bare
			// "upsert: issue_key is required" surface a TaskCreate
			// fired before `bacio agent claim` would otherwise produce.
			// TaskUpdate is exempt — the update branch in the store
			// ignores the supplied issueKey and keeps the row's
			// existing scope, so an empty issueKey is harmless there.
			if in.ToolName == "TaskCreate" && issueKey == "" {
				fmt.Fprintf(os.Stderr,
					"bacio hook post-tool-use: skipping TaskCreate todo: no single open claim for session %q\n",
					in.SessionID)
				return nil
			}
			// BACI-132: resolve the active dispatch for the (session,
			// issue) at TaskCreate time so the row carries the dispatch
			// scope. Only matters for inserts — TaskUpdate keeps the
			// row's original dispatch_id, so spending the lookup on the
			// update path is wasted work. If the dispatch can't be
			// resolved, log-and-drop (BACI-136 symmetry with the
			// no-claim path) rather than landing a row without dispatch
			// attribution.
			var dispatchID *int64
			if in.ToolName == "TaskCreate" {
				dispatchID = resolveActiveDispatchID(context.Background(), c, in.SessionID, issueKey)
				if dispatchID == nil {
					fmt.Fprintf(os.Stderr,
						"bacio hook post-tool-use: skipping TaskCreate todo: no active dispatch for session %q issue %q\n",
						in.SessionID, issueKey)
					return nil
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

// ---------- set-title ----------

// setTitleMatcher is the matcher installed in .claude/settings.json for
// the BACI-147 terminal-title hook — Claude Code fires this PostToolUse
// entry only when the agent calls the bacio channel's `register` tool,
// which is the precise moment the session's identity slug becomes
// resolvable from .bacio/agents.json (`register` writes the entry). The
// literal lives next to the handler so the install-side wiring and the
// hook can't drift, matching the postToolUseMatcher / preToolUseMatcher
// convention.
const setTitleMatcher = "mcp__bacio__register"

// titleWriter is the side-effect surface for writeTerminalTitle. Tests
// substitute a captured *bytes.Buffer; production opens /dev/tty inside
// openTerminalTTY. Pulled out so the OSC emission is unit-testable
// without ever touching the host terminal.
var titleWriter = openTerminalTTY

// openTerminalTTY opens /dev/tty for write-only output. Going via
// /dev/tty (not os.Stdout) keeps the escape sequence out of Claude
// Code's stdout-injection path, so the OSC never reaches the model's
// context. A missing or non-writable /dev/tty (CI host, headless pipe)
// surfaces as the open error and is swallowed by the caller — the
// fail-open invariant.
func openTerminalTTY() (io.WriteCloser, error) {
	return os.OpenFile("/dev/tty", os.O_WRONLY, 0)
}

// writeTerminalTitle emits the `ESC ] 0 ; <title> BEL` OSC sequence to
// /dev/tty (via titleWriter so tests can capture it). The sequence is
// honoured by every mainstream terminal emulator on macOS and Linux
// (iTerm2, Apple Terminal, GNOME Terminal, tmux, Alacritty, kitty,
// Ghostty, Windows Terminal). Returns the underlying error so the
// caller can log it; the hook itself swallows after one stderr line.
func writeTerminalTitle(title string) error {
	w, err := titleWriter()
	if err != nil {
		return err
	}
	defer w.Close()
	_, err = io.WriteString(w, "\x1b]0;"+title+"\x07")
	return err
}

func hookSetTitleCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "set-title",
		Short:  "PostToolUse hook (matcher: mcp__bacio__register): set the terminal window title to the agent slug",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if skipUnlessAgentMode("set-title") {
				return nil
			}
			h, err := loadHookContext()
			if err != nil {
				fmt.Fprintln(os.Stderr, "bacio hook set-title:", err)
				return nil
			}
			if h == nil {
				return nil
			}
			defer h.close()
			if h.slug == "" {
				// register has fired but agents.json doesn't yet carry an
				// entry for this claude_pid — rare race, no slug to set.
				// One-line skip notice keeps a tailing user informed.
				fmt.Fprintln(os.Stderr, "bacio hook set-title: no agent slug for this claude_pid yet — skipping")
				return nil
			}
			if err := writeTerminalTitle(h.slug); err != nil {
				// Fail-open: /dev/tty unavailable (CI, headless pipe),
				// write failed, etc. Log one line and carry on — the
				// hook must NEVER fail the agent's session.
				fmt.Fprintln(os.Stderr, "bacio hook set-title: write /dev/tty:", err)
			}
			return nil
		},
	}
}

// ---------- post-tool-use-heartbeat ----------

// hookPostToolUseHeartbeatCmd is the BACI-159 supervisor heartbeat
// hook. The bacio idle-pinger relies on `agent_sessions.last_seen_at`
// as the sole liveness signal, but Claude Code only fires the
// supervisor-side `UserPromptSubmit` / `Stop` hooks at turn
// boundaries — a long Task-spawned subagent run has no parent-side
// turns until it returns. Without a heartbeat the supervisor's
// last_seen_at can fall past `AgentIdlePingThreshold` while the
// subagent is still doing real work, and the reaper force-ends the
// session.
//
// This hook fires on every supervisor tool call (matcher empty in
// install_hooks.go) — `Task`'s eventual PostToolUse when the
// subagent returns, every `mcp__bacio__*` call, every Bash, every
// Read/Edit/Write. Each one bumps last_seen_at via the shared
// `heartbeatOrRegister` helper. The cost is one cheap indexed
// UPDATE on a single SQLite row inside the connection
// `loadHookContext` already opens; measured well sub-millisecond on
// existing high-frequency hook paths.
//
// **Stdout discipline.** Claude Code merges PostToolUse hook stdout
// into the supervisor's context, so this hook MUST stay silent on
// stdout. Every failure goes to stderr (logged, not surfaced) per
// the standard hook fail-open invariant.
func hookPostToolUseHeartbeatCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "post-tool-use-heartbeat",
		Short:  "PostToolUse hook (no matcher): heartbeat the supervisor session on every tool call",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if skipUnlessAgentMode("post-tool-use-heartbeat") {
				return nil
			}
			h, err := loadHookContext()
			if err != nil {
				fmt.Fprintln(os.Stderr, "bacio hook post-tool-use-heartbeat:", err)
				return nil
			}
			if h == nil {
				return nil
			}
			defer h.close()
			// heartbeatOrRegister logs its own register-fallback failure
			// path; a transient bump failure that fell through is silent
			// here by design — failing the hook would propagate to the
			// supervisor's tool call result, and the BACI-148 fresh-
			// dispatch gate plus the existing UserPromptSubmit / Stop
			// heartbeats give the reaper plenty of redundant signal if
			// this one tick goes missing.
			_ = h.heartbeatOrRegister()
			return nil
		},
	}
}

// ---------- pre-tool-use ----------

// preToolUseInput is the slice of the Claude Code PreToolUse payload
// the worktree-confinement guard and the BACI-134 sqlite3 confinement
// guard care about. The matcher is `Write|Edit|Bash`: the Write/Edit
// branch reads `file_path`, the Bash branch reads `command`. The decoder
// ignores unknown fields, so this is a strict subset and needs no
// knowledge of any other tool's input shape.
type preToolUseInput struct {
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath string `json:"file_path"` // Write / Edit
		Command  string `json:"command"`   // Bash (BACI-134)
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
// hook code can't drift (same convention as postToolUseMatcher).
//
// The matcher covers two distinct guards that share the same hook entry:
//
//   - Write|Edit — the BACI-116 / BACI-129 worktree confinement guard.
//     Denies edits that resolve outside a linked-worktree root, and any
//     edit at all when cwd is the primary checkout. The original BACI-116
//     reasoning to exclude Bash held: parsing tool_input.command for
//     paths in general is fragile, and confining Write/Edit defuses the
//     Bash escape — with the parent checkout kept clean by the write
//     denial, a stray `cd <main> && git commit` has nothing to commit.
//
//   - Bash — the BACI-134 sqlite3 confinement guard. Different escape
//     class: `sqlite3 ~/.bacio/db.sqlite "DELETE ..."` mutates the live
//     store directly, bypassing every internal/store/* path that writes
//     the audit-log `history` row. The Write/Edit confinement above
//     doesn't catch it because no file_path is involved. Denies only the
//     specific shape `sqlite3 <path>` where `<path>` resolves to the
//     default `~/.bacio/db.sqlite`; everything else (including a
//     worktree-isolated `.bacio/db.sqlite`) fails open.
const preToolUseMatcher = "Write|Edit|Bash"

// preToolUseDecision is the verdict the confinement guard reaches for
// one tool call. allow=true emits nothing (the call proceeds); allow=
// false emits the PreToolUse deny JSON naming the worktree root so the
// model self-corrects. reason is non-empty only on a deny.
type preToolUseDecision struct {
	allow  bool
	reason string
}

// worktreeRootResolver classifies a given cwd into one of three
// PreToolUse outcomes the guard cares about:
//
//   - linked == true,  root != ""  : cwd is in a *linked* worktree —
//     edits under root are allowed; edits outside it are denied.
//   - linked == false, root != ""  : cwd is in the *primary* worktree
//     of a git repo — every edit is denied (the main-checkout case
//     closed by BACI-129).
//   - linked == false, root == ""  : cwd is not in a git repo (or the
//     classification failed) — fail-open, edits are allowed.
//
// The production implementation is gitLinkedWorktreeRoot; tests
// substitute their own.
type worktreeRootResolver func(cwd string) (root string, linked bool)

// gitLinkedWorktreeRoot asks git directly whether cwd is in the primary
// or a linked worktree (BACI-129), replacing the previous wtenv-backed
// resolver that engaged only when an environment-config.yaml manifest
// was present. Collapses any classification error to the
// (root="", linked=false) outcome so the decision function's fail-open
// invariant has a single error path to handle.
func gitLinkedWorktreeRoot(cwd string) (string, bool) {
	root, linked, err := git.LinkedWorktreeRoot(cwd)
	if err != nil {
		return "", false
	}
	return root, linked
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
// confinement guard — no stdin, no stdout, directly unit-testable.
//
// Confinement engages whenever BACIO_AGENT_MODE=1 is set (the outer
// gate in hookPreToolUseCmd) and cwd sits inside any git repository.
// Inside the primary worktree (the main checkout) every Write/Edit is
// denied — this is the BACI-129 widening of the BACI-116 guard, which
// previously short-circuited to allow when no worktree manifest was
// present. Inside a linked worktree the call is allowed only if its
// file_path resolves under that worktree's root (the original BACI-116
// containment check). Outside any git repository the call is allowed
// (fail-open).
//
// Every ambiguous or error case allows; a deny is emitted only on a
// positive "this edit must not land here" determination, and the
// deny reason names the relevant root so the model self-corrects.
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

	root, linked := resolveRoot(in.CWD)

	// Outside any git repo → fail-open. A non-dispatch agent run
	// against arbitrary scratch paths is not in scope for confinement.
	if root == "" {
		return allow
	}

	// Inside the *primary* worktree (the main checkout): deny every
	// edit, regardless of where file_path points. This is the BACI-129
	// case — a supervisor with BACIO_AGENT_MODE=1 set must not write to
	// the parent checkout. The reason names the primary root so the
	// model is told what to do next (move into a linked worktree, or
	// drop BACIO_AGENT_MODE for this shell).
	if !linked {
		root = evalSymlinksLenient(root)
		return preToolUseDecision{
			allow: false,
			reason: fmt.Sprintf(
				"bacio: this agent-mode session must edit files inside a linked git worktree, "+
					"not the primary worktree %s. The %s file_path %s resolves into the primary "+
					"checkout. Run `bacio worktree init` to create a linked worktree (or move into "+
					"an existing one) and re-issue the edit there.",
				root, in.ToolName, in.ToolInput.FilePath),
		}
	}

	// Linked worktree: BACI-116 containment check. Normalise the
	// target — a Write/Edit file_path is normally already absolute, but
	// resolve it against cwd defensively. Then eval symlinks on both
	// sides so a symlink can't slip the check.
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
			"bacio: this dispatched-worker session is confined to its linked git worktree %s. "+
				"The %s file_path %s resolves outside it (into the primary checkout or a "+
				"sibling worktree). Re-issue the edit with a path under %s.",
			root, in.ToolName, in.ToolInput.FilePath, root),
	}
}

// liveDBResolver returns the absolute, symlink-resolved path of the
// "live" bacio SQLite store — the default shared DB under the user's
// home (~/.bacio/db.sqlite). Returns "" when the path cannot be
// resolved, which collapses to fail-open inside decideBashSqlite3.
//
// Deliberately narrow: this is *not* the per-worktree wtenv resolver.
// A worktree-isolated DB (BACI-87) sits at <worktree>/.bacio/db.sqlite
// and is therefore a different absolute path — it never matches this
// resolver, so the BACI-134 guard naturally allows sqlite3 calls against
// it without consulting the wtenv layer.
type liveDBResolver func() string

func defaultLiveDBResolver() string {
	p, err := wtenv.DefaultDBPath("")
	if err != nil {
		return ""
	}
	return evalSymlinksLenient(p)
}

// decideBashSqlite3 is the pure decision function for the BACI-134
// sqlite3 confinement guard — no stdin, no stdout, directly
// unit-testable. The matcher (`Write|Edit|Bash`) hands every Bash call
// to this function; it denies only the specific shape
// `sqlite3 <path> [...]` where `<path>` resolves to the live shared DB.
//
// Path-based, not verb-based: the goal is to keep every mutation
// audit-logged via the store boundary. Even a `SELECT 1` against the
// live DB is denied — a dispatched worker has no business reading the
// shared store with raw SQL (the `bacio` read verbs and `bacio history`
// cover legitimate diagnostics). A worktree-isolated DB
// (`<worktree>/.bacio/db.sqlite`) is a different absolute path and is
// therefore allowed by virtue of not matching.
//
// Fail-open invariant: every ambiguous case (shell substitution,
// pipes, env-var-referenced paths, the basename not being a literal
// `sqlite3`, no candidate path token, an unreadable resolver) allows
// the call and logs nothing. A deny is emitted only on a positive
// "this command targets the live DB" determination, and the deny
// reason names the live DB path so the model self-corrects.
func decideBashSqlite3(in *preToolUseInput, resolveLiveDB liveDBResolver) preToolUseDecision {
	allow := preToolUseDecision{allow: true}

	if in.ToolName != "Bash" {
		return allow
	}
	cmd := strings.TrimSpace(in.ToolInput.Command)
	if cmd == "" {
		return allow
	}

	// Fail-open on any shell construct that would let the actual sqlite3
	// invocation hide from a plain strings.Fields tokenisation: pipes,
	// command substitution, backticks, sub-shells. The guard's job is to
	// make the obvious bypass loud, not to be a general bash sandbox.
	if strings.ContainsAny(cmd, "|`") || strings.Contains(cmd, "$(") {
		return allow
	}

	tokens := strings.Fields(cmd)
	idx := -1
	for i, tok := range tokens {
		// Strip leading quotes the same way the path candidate below
		// does, so `"sqlite3"` is recognised but `bash` (with sqlite3
		// only inside a -c string) is not.
		stripped := strings.Trim(tok, `"'`)
		if filepath.Base(stripped) == "sqlite3" {
			idx = i
			break
		}
	}
	if idx == -1 {
		return allow
	}

	// Walk tokens after the sqlite3 binary: skip flags (anything starting
	// with `-`), pick the first plain token as the DB path argument.
	var candidate string
	for _, tok := range tokens[idx+1:] {
		if strings.HasPrefix(tok, "-") {
			continue
		}
		candidate = tok
		break
	}
	if candidate == "" {
		return allow
	}

	// Strip a single layer of surrounding quotes so
	// `"~/.bacio/db.sqlite"` and `'~/.bacio/db.sqlite'` resolve the same
	// as a bare path. Anything fancier (escaped quotes inside, multi-word
	// expansion) is left as-is and naturally drops below to fail-open via
	// the env-var / glob check.
	candidate = strings.Trim(candidate, `"'`)

	// Fail-open on shell-evaluated path tokens: env vars, command
	// substitution, globs, parameter expansion. We can't statically
	// resolve them at hook time, and a worker that genuinely wants to
	// bypass via `$BACIO_DB` is a different (harder) problem the guard
	// deliberately doesn't try to solve.
	if strings.ContainsAny(candidate, "$*?(){}") {
		return allow
	}

	// Expand a leading `~/` against the user's home so
	// `sqlite3 ~/.bacio/db.sqlite "..."` resolves to the same absolute
	// path as `sqlite3 /Users/<u>/.bacio/db.sqlite "..."`. Anything else
	// (`~user/...`) falls through to fail-open.
	if strings.HasPrefix(candidate, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return allow
		}
		candidate = filepath.Join(home, candidate[2:])
	} else if candidate == "~" {
		return allow
	}

	// Resolve relative paths against cwd (the worker's worktree). A
	// `sqlite3 .bacio/db.sqlite` from inside a worktree resolves to that
	// worktree's isolated DB and is therefore *not* the live DB.
	if !filepath.IsAbs(candidate) {
		base := in.CWD
		if base == "" {
			if wd, err := os.Getwd(); err == nil {
				base = wd
			}
		}
		if base == "" {
			return allow
		}
		candidate = filepath.Join(base, candidate)
	}

	liveDB := resolveLiveDB()
	if liveDB == "" {
		return allow
	}
	resolved := evalSymlinksLenient(candidate)

	if resolved != liveDB {
		return allow
	}

	return preToolUseDecision{
		allow: false,
		reason: fmt.Sprintf(
			"bacio: this agent-mode session must not invoke sqlite3 against the live shared "+
				"bacio store %s. Every mutation must go through a `bacio` CLI verb so the audit "+
				"log records it. For read-only diagnostics use the `bacio` read verbs (e.g. "+
				"`bacio issue show`, `bacio history`); for throwaway state, re-run "+
				"`bacio worktree init --isolate-db` so the worker's DB is its own isolated "+
				"file. If the legitimate verb refuses you, ask the user via "+
				"`mcp__bacio__ask_user_question` rather than reaching for raw SQL.",
			liveDB),
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
		Short:  "PreToolUse hook (matcher: Write|Edit|Bash): confine writes to a linked worktree and block raw sqlite3 against the live store",
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
			// Two sibling deciders share the hook entry. Write/Edit goes
			// through the worktree-confinement guard (BACI-116 / BACI-129);
			// Bash goes through the sqlite3 confinement guard (BACI-134).
			// Only one deny ever leaves the hook so Claude Code's surface
			// stays uncluttered — Write/Edit checked first, then Bash.
			if d := decidePreToolUse(in, gitLinkedWorktreeRoot); !d.allow {
				emitPreToolUseDeny(d.reason)
				return nil
			}
			if d := decideBashSqlite3(in, defaultLiveDBResolver); !d.allow {
				emitPreToolUseDeny(d.reason)
				return nil
			}
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

// clearErrorOnRecovery wipes the errored-state columns when a session
// that previously took an Anthropic API failure (BACI-296) takes a fresh
// turn — the recovery signal. Edge-only: a session that isn't errored
// writes nothing, so a normal heartbeat doesn't churn the row. Best-
// effort — a clear failure goes to stderr and never fails the hook.
func (h *hookContext) clearErrorOnRecovery(sess *model.AgentSession) {
	if sess == nil || sess.ErroredAt == nil {
		return
	}
	if err := h.c.ClearAgentError(context.Background(), sess.SessionID); err != nil {
		fmt.Fprintln(os.Stderr, "bacio hook: clear errored state:", err)
	}
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
			model.StateTodo, model.StateInReview,
			model.StateInPipeline, model.StateToBeShipped,
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
