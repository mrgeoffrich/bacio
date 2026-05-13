package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/timeparse"
)

// claudeSessionEnv is the env var Claude Code exposes for the current
// session id. Other harnesses can pass --session explicitly.
const claudeSessionEnv = "CLAUDE_CODE_SESSION_ID"

func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Track live AI-agent sessions and their issue claims (local-only registry)",
		Long: `Records which agents are alive against a repo and which issues
they're focused on. Local-only — never synced to GitHub.

Typical lifecycle, driven by an agent (see SKILL.md):

    bacio agent register --user agent-claude
    bacio agent claim    MINI-42 --user agent-claude
    ...do work...
    bacio agent release  MINI-42 --user agent-claude
    bacio agent end      --reason stop --user agent-claude

` + "`--session`" + ` defaults to $CLAUDE_CODE_SESSION_ID. Claim records
intent — it does NOT move the issue or change its assignee; use
` + "`bacio issue state`" + ` / ` + "`bacio issue assign`" + ` for that.`,
	}
	cmd.AddCommand(
		agentRegisterCmd(),
		agentHeartbeatCmd(),
		agentEndCmd(),
		agentClaimCmd(),
		agentReleaseCmd(),
		agentListCmd(),
		agentShowCmd(),
	)
	return cmd
}

// ---------- register ----------

func agentRegisterCmd() *cobra.Command {
	var (
		sessionID, modelStr, mode, host, branch, rawInput string
	)
	cmd := &cobra.Command{
		Use:   "register",
		Short: "Register (or refresh) this agent session against the current repo",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput,
				"session", "model", "mode", "host", "branch")
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.AgentRegisterInput](raw)
				if err != nil {
					return err
				}
				return runAgentRegister(*in)
			}
			sid, err := resolveSessionID(sessionID)
			if err != nil {
				return err
			}
			detectedBranch, _ := detectBranch()
			detectedHost, _ := os.Hostname()
			return runAgentRegister(inputs.AgentRegisterInput{
				SessionID:      sid,
				Actor:          actor(),
				Model:          modelStr,
				PermissionMode: mode,
				Host:           orDefault(host, detectedHost),
				Branch:         orDefault(branch, detectedBranch),
			})
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "session id (default: $CLAUDE_CODE_SESSION_ID)")
	cmd.Flags().StringVar(&modelStr, "model", "", "model identifier (e.g. claude-sonnet-4-6)")
	cmd.Flags().StringVar(&mode, "mode", "", "permission mode (plan/acceptEdits/bypass/...)")
	cmd.Flags().StringVar(&host, "host", "", "hostname (default: os.Hostname())")
	cmd.Flags().StringVar(&branch, "branch", "", "git branch (default: detected from current repo)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runAgentRegister(in inputs.AgentRegisterInput) error {
	if err := requireLocalForAgent("register"); err != nil {
		return err
	}
	if in.Actor == "" {
		in.Actor = actor()
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	sess, err := c.RegisterAgent(context.Background(), repo, in, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(sess)
	}
	return emit(sess)
}

// ---------- heartbeat ----------

func agentHeartbeatCmd() *cobra.Command {
	var (
		sessionID, modelStr, mode, branch, rawInput string
	)
	cmd := &cobra.Command{
		Use:   "heartbeat",
		Short: "Bump last_seen_at on an already-registered session",
		Long: `Optional — register, claim, and release already bump last_seen_at.
Useful when a long-running session has no other mutations to make but
wants to stay flagged as alive.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput,
				"session", "model", "mode", "branch")
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.AgentHeartbeatInput](raw)
				if err != nil {
					return err
				}
				return runAgentHeartbeat(*in)
			}
			sid, err := resolveSessionID(sessionID)
			if err != nil {
				return err
			}
			return runAgentHeartbeat(inputs.AgentHeartbeatInput{
				SessionID:      sid,
				Model:          modelStr,
				PermissionMode: mode,
				Branch:         branch,
			})
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "session id (default: $CLAUDE_CODE_SESSION_ID)")
	cmd.Flags().StringVar(&modelStr, "model", "", "model identifier (overwrite previous if different)")
	cmd.Flags().StringVar(&mode, "mode", "", "permission mode")
	cmd.Flags().StringVar(&branch, "branch", "", "git branch")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runAgentHeartbeat(in inputs.AgentHeartbeatInput) error {
	if err := requireLocalForAgent("heartbeat"); err != nil {
		return err
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	sess, err := c.HeartbeatAgent(context.Background(), repo, in, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(sess)
	}
	return emit(sess)
}

// ---------- end ----------

func agentEndCmd() *cobra.Command {
	var (
		sessionID, reason, rawInput string
	)
	cmd := &cobra.Command{
		Use:   "end",
		Short: "End an agent session and auto-release every open claim",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput,
				"session", "reason")
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.AgentEndInput](raw)
				if err != nil {
					return err
				}
				return runAgentEnd(*in)
			}
			sid, err := resolveSessionID(sessionID)
			if err != nil {
				return err
			}
			if reason == "" {
				reason = string(model.EndReasonStop)
			}
			return runAgentEnd(inputs.AgentEndInput{SessionID: sid, Reason: reason})
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "session id (default: $CLAUDE_CODE_SESSION_ID)")
	cmd.Flags().StringVar(&reason, "reason", "", "end_reason: stop|clear|logout|crash|other (default: stop)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runAgentEnd(in inputs.AgentEndInput) error {
	if err := requireLocalForAgent("end"); err != nil {
		return err
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	sess, err := c.EndAgent(context.Background(), repo, in, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(sess)
	}
	return emit(sess)
}

// ---------- claim ----------

func agentClaimCmd() *cobra.Command {
	var (
		sessionID, rawInput string
	)
	cmd := &cobra.Command{
		Use:   "claim [issue-key]",
		Short: "Record that this session is focused on an issue (intent, not state)",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput, "session")
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.AgentClaimInput](raw)
				if err != nil {
					return err
				}
				return runAgentClaim(*in)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <issue-key> positional or --json")
			}
			sid, err := resolveSessionID(sessionID)
			if err != nil {
				return err
			}
			return runAgentClaim(inputs.AgentClaimInput{SessionID: sid, IssueKey: args[0]})
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "session id (default: $CLAUDE_CODE_SESSION_ID)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runAgentClaim(in inputs.AgentClaimInput) error {
	if err := requireLocalForAgent("claim"); err != nil {
		return err
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	claim, err := c.ClaimAgent(context.Background(), repo, in, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(claim)
	}
	return emit(claim)
}

// ---------- release ----------

func agentReleaseCmd() *cobra.Command {
	var (
		sessionID, rawInput string
	)
	cmd := &cobra.Command{
		Use:   "release [issue-key]",
		Short: "Release this session's claim on an issue",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput, "session")
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.AgentReleaseInput](raw)
				if err != nil {
					return err
				}
				return runAgentRelease(*in)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <issue-key> positional or --json")
			}
			sid, err := resolveSessionID(sessionID)
			if err != nil {
				return err
			}
			return runAgentRelease(inputs.AgentReleaseInput{SessionID: sid, IssueKey: args[0]})
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "session id (default: $CLAUDE_CODE_SESSION_ID)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runAgentRelease(in inputs.AgentReleaseInput) error {
	if err := requireLocalForAgent("release"); err != nil {
		return err
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	claim, err := c.ReleaseAgent(context.Background(), repo, in, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(claim)
	}
	return emit(claim)
}

// ---------- list ----------

func agentListCmd() *cobra.Command {
	var (
		allRepos, alive bool
		since           string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List agent sessions in this repo (lean output; use `agent show` for claim history)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireLocalForAgent("list"); err != nil {
				return err
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()

			f := client.AgentSessionFilter{OnlyAlive: alive}
			if !allRepos {
				repo, err := resolveRepoC(c)
				if err != nil {
					return err
				}
				f.Repo = repo
			}
			if since != "" {
				dur, err := timeparse.Lookback(since)
				if err != nil {
					return fmt.Errorf("--since: %w", err)
				}
				f.Since = time.Now().Add(-dur).UTC()
			}
			sessions, err := c.ListAgentSessions(context.Background(), f)
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				if sessions == nil {
					sessions = []*model.AgentSession{}
				}
				return emit(sessions)
			}
			return emitAgentSessionTable(sessions)
		},
	}
	cmd.Flags().BoolVar(&allRepos, "all-repos", false, "include sessions across every repo (default: just this one)")
	cmd.Flags().BoolVar(&alive, "active", false, "only sessions that haven't been ended")
	cmd.Flags().StringVar(&since, "since", "", "only sessions seen within this duration (e.g. 30m, 4h)")
	return cmd
}

// ---------- show ----------

func agentShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <session-id>",
		Short: "Show one session with full claim history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireLocalForAgent("show"); err != nil {
				return err
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			view, err := c.ShowAgentSession(context.Background(), args[0])
			if err != nil {
				return err
			}
			if opts.output == outputJSON {
				return emit(view)
			}
			return emitAgentSessionDetail(view)
		},
	}
	return cmd
}

// ---------- helpers ----------

// requireLocalForAgent short-circuits any agent verb when --remote /
// BACIO_REMOTE is set. The registry is local-only in v1; rather than
// have agents see a confusing "dial tcp" or 404 from the remote call,
// we surface a clear "drop --remote" message before any network I/O.
func requireLocalForAgent(verb string) error {
	if inRemoteMode() {
		return fmt.Errorf("bacio agent %s is local-only in v1 — drop --remote / unset BACIO_REMOTE (the agent registry lives only in the local SQLite store)", verb)
	}
	return nil
}

// resolveSessionID falls back to $CLAUDE_CODE_SESSION_ID when --session
// is empty. Errors out explicitly if neither source provides a value
// rather than guessing — silently registering a wrong session id is
// worse than a clear "set --session or export CLAUDE_CODE_SESSION_ID".
func resolveSessionID(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if v := strings.TrimSpace(os.Getenv(claudeSessionEnv)); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("--session not set and $%s is empty", claudeSessionEnv)
}

// detectBranch returns the current git branch via a shellout.
// Returns ("", nil) when not in a git tree — callers fall back to
// whatever --branch the user supplied (or empty).
func detectBranch() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	out, err := exec.Command("git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

// orDefault returns value if non-empty, else fallback.
func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func emitAgentSessionTable(sessions []*model.AgentSession) error {
	if len(sessions) == 0 {
		fmt.Fprintln(os.Stdout, "(no agent sessions)")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION\tACTOR\tREPO\tMODEL\tBRANCH\tLAST-SEEN\tSTATUS")
	now := time.Now().UTC()
	for _, s := range sessions {
		status := "alive"
		if s.EndedAt != nil {
			status = "ended:" + s.EndReason
		}
		seen := humanAgo(now.Sub(s.LastSeenAt.UTC()))
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			shortID(s.SessionID), s.Actor, s.RepoPrefix, dashIfEmpty(s.Model),
			dashIfEmpty(s.Branch), seen, status)
	}
	return w.Flush()
}

func emitAgentSessionDetail(view *client.AgentSessionView) error {
	s := view.Session
	fmt.Fprintf(os.Stdout, "Session:  %s\n", s.SessionID)
	fmt.Fprintf(os.Stdout, "Actor:    %s\n", s.Actor)
	fmt.Fprintf(os.Stdout, "Repo:     %s\n", s.RepoPrefix)
	fmt.Fprintf(os.Stdout, "Model:    %s\n", dashIfEmpty(s.Model))
	fmt.Fprintf(os.Stdout, "Mode:     %s\n", dashIfEmpty(s.PermissionMode))
	fmt.Fprintf(os.Stdout, "Host:     %s\n", dashIfEmpty(s.Host))
	fmt.Fprintf(os.Stdout, "Branch:   %s\n", dashIfEmpty(s.Branch))
	fmt.Fprintf(os.Stdout, "Started:  %s\n", s.StartedAt.Local().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(os.Stdout, "LastSeen: %s\n", s.LastSeenAt.Local().Format("2006-01-02 15:04:05"))
	if s.EndedAt != nil {
		fmt.Fprintf(os.Stdout, "Ended:    %s (reason=%s)\n", s.EndedAt.Local().Format("2006-01-02 15:04:05"), s.EndReason)
	} else {
		fmt.Fprintf(os.Stdout, "Ended:    -\n")
	}
	if len(view.Claims) == 0 {
		fmt.Fprintln(os.Stdout, "\nClaims:   (none)")
		return nil
	}
	fmt.Fprintln(os.Stdout, "\nClaims:")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  ISSUE\tCLAIMED\tRELEASED")
	for _, cl := range view.Claims {
		rel := "-"
		if cl.ReleasedAt != nil {
			rel = cl.ReleasedAt.Local().Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\n", cl.IssueKey,
			cl.ClaimedAt.Local().Format("2006-01-02 15:04:05"), rel)
	}
	return w.Flush()
}

// shortID truncates a session id for table display. 12 chars covers
// well past UUID-v7's deterministic timestamp prefix into the random
// half, so collisions in a busy repo are vanishingly unlikely while
// the column still fits comfortably. `agent show` accepts any unique
// prefix, so list output stays copy-pasteable.
func shortID(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func humanAgo(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
