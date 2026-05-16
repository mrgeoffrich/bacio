package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/sync"
)

// newHookCmd builds the hidden `bacio hook` command group — the Claude
// Code hook integration shim. Each subcommand reads a hook-event JSON
// payload on stdin (wired up by `bacio install-hooks`), correlates it
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
	c, err := client.Open(context.Background(), client.Options{
		DBPath: opts.dbPath,
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
