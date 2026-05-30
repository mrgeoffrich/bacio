package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/timeparse"
	"github.com/mrgeoffrich/bacio/internal/version"
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

    bacio agent register
    bacio agent claim    MINI-42
    ...do work...
    bacio agent release  MINI-42
    bacio agent end      --reason stop

` + "`--session`" + ` defaults to $CLAUDE_CODE_SESSION_ID. Claiming an
issue also stamps its assignee with the claiming agent's identity (and
releasing the last open claim clears it again). As of BACI-126a, claim
auto-transitions the issue to in_progress (unconditionally — claim from
any state is valid). Release takes a required ` + "`--state`" + ` flag
(BACI-126c) so the issue lands in a declared final state rather than
drifting.`,
	}
	cmd.AddCommand(
		agentRegisterCmd(),
		agentHeartbeatCmd(),
		agentEndCmd(),
		agentClaimCmd(),
		agentReleaseCmd(),
		agentInboxCmd(),
		agentAckCmd(),
		agentCancelCmd(),
		agentListCmd(),
		agentShowCmd(),
		agentQuestionsCmd(),
	)
	return cmd
}

// ---------- register ----------

func agentRegisterCmd() *cobra.Command {
	var (
		sessionID, agentName, modelStr, host, branch, rawInput string
		newIdentity                                            bool
	)
	cmd := &cobra.Command{
		Use:   "register",
		Short: "Register (or refresh) this agent session against the current repo",
		Long: `Register or refresh a live agent session.

The optional --agent flag attaches a persistent identity (e.g.
"cheerful-otter@claude.shiny") so cross-session activity correlates
back to one logical agent rather than dissolving on every /clear.

With bacio's hooks installed, the SessionStart hook does all of this
for you — minting the identity, recording it in .bacio/agents.json,
and registering the session. This command is the manual fallback for
repos without hooks: generate a fresh slug and register with --agent
<slug> --new. The --new flag asserts "this name MUST be new" — bacio
errors with "agent name already taken" if the slug clashes, so the
loop can retry; subsequent registers of a known slug drop --new.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput,
				"session", "agent", "new", "model", "host", "branch")
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
				SessionID:   sid,
				Actor:       actor(),
				Agent:       agentName,
				NewIdentity: newIdentity,
				Model:       modelStr,
				Host:        orDefault(host, detectedHost),
				Branch:      orDefault(branch, detectedBranch),
			})
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "session id (default: $CLAUDE_CODE_SESSION_ID)")
	cmd.Flags().StringVar(&agentName, "agent", "", "persistent identity slug (e.g. cheerful-otter@claude.shiny)")
	cmd.Flags().BoolVar(&newIdentity, "new", false, "assert --agent is a fresh slug; errors if it clashes (loop with a new slug)")
	cmd.Flags().StringVar(&modelStr, "model", "", "model identifier (e.g. claude-sonnet-4-6)")
	cmd.Flags().StringVar(&host, "host", "", "hostname (default: os.Hostname())")
	cmd.Flags().StringVar(&branch, "branch", "", "git branch (default: detected from current repo)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runAgentRegister(in inputs.AgentRegisterInput) error {
	if in.NewIdentity && in.Agent == "" {
		return fmt.Errorf("--new requires --agent <name>")
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
		if errors.Is(err, store.ErrAgentNameTaken) {
			// Stable phrasing so agent-loop code can grep stderr for
			// "agent name already taken" and decide to regenerate.
			return fmt.Errorf("agent name %q already taken — generate a fresh slug and retry with --new", in.Agent)
		}
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
		sessionID, modelStr, branch, rawInput string
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
				"session", "model", "branch")
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
				SessionID: sid,
				Model:     modelStr,
				Branch:    branch,
			})
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "session id (default: $CLAUDE_CODE_SESSION_ID)")
	cmd.Flags().StringVar(&modelStr, "model", "", "model identifier (overwrite previous if different)")
	cmd.Flags().StringVar(&branch, "branch", "", "git branch")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runAgentHeartbeat(in inputs.AgentHeartbeatInput) error {
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
		sessionID, reason, stateOnOrphan, rawInput string
	)
	cmd := &cobra.Command{
		Use:   "end",
		Short: "End an agent session and auto-release every open claim",
		Long: `End an agent session and auto-release every open claim it holds.

BACI-126c: every cascaded release moves its issue to ` + "`--state-on-orphan`" + `
(default: in_progress). Use the default when the harness is stopping and
the work is abandoned, not finished — leaving the issue at in_progress
lets the next agent (or the user) pick the ticket up.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput,
				"session", "reason", "state-on-orphan")
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
			return runAgentEnd(inputs.AgentEndInput{
				SessionID:     sid,
				Reason:        reason,
				StateOnOrphan: stateOnOrphan,
			})
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "session id (default: $CLAUDE_CODE_SESSION_ID)")
	cmd.Flags().StringVar(&reason, "reason", "", "end_reason: stop|clear|logout|crash|other (default: stop)")
	cmd.Flags().StringVar(&stateOnOrphan, "state-on-orphan", "", "state every auto-released claim's issue lands in (default: in_progress)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runAgentEnd(in inputs.AgentEndInput) error {
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
		sessionID, prompt, rawInput string
	)
	cmd := &cobra.Command{
		Use:   "claim [issue-key]",
		Short: "Focus this session on an issue — records the claim, stamps the assignee, and auto-moves the issue to in_progress (BACI-126a)",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput, "session", "prompt")
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
			return runAgentClaim(inputs.AgentClaimInput{SessionID: sid, IssueKey: args[0], Prompt: prompt})
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "session id (default: $CLAUDE_CODE_SESSION_ID)")
	cmd.Flags().StringVar(&prompt, "prompt", "", "instruction/dispatch text this session is working from")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runAgentClaim(in inputs.AgentClaimInput) error {
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
		sessionID, finalState, rawInput string
	)
	cmd := &cobra.Command{
		Use:   "release [issue-key]",
		Short: "Release this session's claim on an issue (--state optional)",
		Long: `Release this session's claim on an issue.

` + "`--state`" + ` is OPTIONAL. Omit it to drop the claim only and leave
the issue's state untouched — this is the default for pipeline-stage
workers, whose state is owned by the controller engine (the Pipeline
cutover removed the worker's state authority). Pass ` + "`--state`" + `
to also move the issue atomically — used by the pre-pipeline triage
passes (scope / research) that hand a fresh ` + "`todo`" + ` ticket on.
Allowed values: todo, in_progress, needs_action, in_review, done,
cancelled.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput, "session", "state")
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
			return runAgentRelease(inputs.AgentReleaseInput{
				SessionID:  sid,
				IssueKey:   args[0],
				FinalState: finalState,
			})
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "session id (default: $CLAUDE_CODE_SESSION_ID)")
	cmd.Flags().StringVar(&finalState, "state", "", "final issue state — optional; omit to drop the claim only (todo|in_progress|needs_action|in_review|done|cancelled)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runAgentRelease(in inputs.AgentReleaseInput) error {
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

// The legacy `bacio agent dispatch` CLI verb was retired in BACI-300
// alongside the manual state-driven workflow — work now flows through
// the Pipeline, whose controller engine queues each stage's dispatch.
// The shared dispatch machinery it used (client.CreateDispatch /
// AutoDispatchIssue, the agent_dispatches table, the matcher + channel)
// stays load-bearing for the Pipeline and the REST dispatch route.

// ---------- inbox ----------

func agentInboxCmd() *cobra.Command {
	var sessionID string
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "List dispatches queued for this session (and its agent identity)",
		Long: `Show the open dispatches (pending or delivered) aimed at this session
or at the agent identity behind it. --session defaults to
$CLAUDE_CODE_SESSION_ID. Ack a dispatch with ` + "`bacio agent ack <id>`" + `.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			sid, err := resolveSessionID(sessionID)
			if err != nil {
				return err
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			ds, err := c.InboxDispatches(context.Background(), sid)
			if err != nil {
				return err
			}
			if opts.output == outputJSON && ds == nil {
				ds = []*model.AgentDispatch{}
			}
			return emit(ds)
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "session id (default: $CLAUDE_CODE_SESSION_ID)")
	return cmd
}

// ---------- ack ----------

func agentAckCmd() *cobra.Command {
	var (
		note, rawInput string
	)
	cmd := &cobra.Command{
		Use:   "ack <dispatch-id>",
		Short: "Acknowledge a dispatch and record an optional reply note",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput, "note")
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.AgentAckInput](raw)
				if err != nil {
					return err
				}
				return runAgentAck(*in)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <dispatch-id> positional or --json")
			}
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid dispatch id %q: %w", args[0], err)
			}
			return runAgentAck(inputs.AgentAckInput{ID: id, Note: note})
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "free-form reply recorded against the dispatch")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runAgentAck(in inputs.AgentAckInput) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	d, err := c.AckDispatch(context.Background(), in, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(d)
	}
	return emit(d)
}

// ---------- cancel ----------

func agentCancelCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "cancel <dispatch-id>",
		Short: "Cancel a queued or pending dispatch (the row stops appearing as 'waiting' on the issue's card)",
		Long: `Withdraw a dispatch that hasn't been delivered to its worker yet —
the dispatcher's side of ack.

Use when a queued dispatch was orphaned (target session ended before
acking, or the dispatch is simply no longer wanted). Cancelling flips
the row to status='cancelled' so it no longer satisfies
WaitingDispatchForIssue (BACI-255), unsticking the desktop/TUI spinner.

Cancelling a dispatch that has already been delivered to the worker is
an error (BACI-130): the worker is past the point where it can be
stopped by mutating the dispatch row. Interrupt the agent itself
instead. Cancelling an acked dispatch is an error too (the work was
acknowledged). Cancelling an already-cancelled dispatch is a no-op.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.AgentCancelInput](raw)
				if err != nil {
					return err
				}
				return runAgentCancel(*in)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <dispatch-id> positional or --json")
			}
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid dispatch id %q: %w", args[0], err)
			}
			return runAgentCancel(inputs.AgentCancelInput{ID: id})
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func runAgentCancel(in inputs.AgentCancelInput) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	d, err := c.CancelDispatch(context.Background(), in, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(d)
	}
	return emit(d)
}

// ---------- list ----------

func agentListCmd() *cobra.Command {
	var (
		allRepos, alive, allStates bool
		since                      string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List agent sessions in this repo (registered only by default — use --all to include SessionStart stubs)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()

			// Default: registered_at IS NOT NULL — hide stubs from
			// sessions that never completed register. --all flips it.
			f := client.AgentSessionFilter{
				OnlyAlive:      alive,
				RegisteredOnly: !allStates,
			}
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
	cmd.Flags().BoolVar(&allStates, "all", false, "include SessionStart stubs that never completed register (default: registered only)")
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

// resolveSessionID falls back to $CLAUDE_CODE_SESSION_ID, then to this
// process's newest session in .bacio/agents.json (resolved via the
// claude pid), when --session is empty. Errors out explicitly if none
// of them provide a value rather than guessing — silently registering a
// wrong session id is worse than a clear failure.
func resolveSessionID(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if v := strings.TrimSpace(os.Getenv(claudeSessionEnv)); v != "" {
		return v, nil
	}
	if v := sessionIDForProcess(); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("--session not set, $%s is empty, and no .bacio/agents.json entry for this process", claudeSessionEnv)
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
	fmt.Fprintln(w, "SESSION\tAGENT\tACTOR\tREPO\tMODEL\tBRANCH\tLAST-SEEN\tCHANNEL\tBACIO\tSTATUS")
	now := time.Now().UTC()
	current := version.String()
	for _, s := range sessions {
		status := "alive"
		if s.EndedAt != nil {
			status = "ended:" + s.EndReason
		}
		seen := humanAgo(now.Sub(s.LastSeenAt.UTC()))
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			shortID(s.SessionID), dashIfEmpty(s.AgentName), s.Actor, s.RepoPrefix,
			dashIfEmpty(s.Model), dashIfEmpty(s.Branch), seen,
			channelStatus(s, now), bacioVersionDisplay(s.ChannelVersion, current), status)
	}
	return w.Flush()
}

// bacioVersionDisplay renders the BACIO column (the bacio binary version
// the channel was running at register time). "-" for sessions that
// pre-date version reporting (or never registered). A trailing "!stale"
// annotates a non-matching version so you can spot a long-lived channel
// still running an old bacio binary after an upgrade. A bare "dev"
// current (no VCS info — debug.ReadBuildInfo failed) suppresses the
// stale flag because we can't meaningfully compare; once VCS info is
// present (the usual case), two different commits will surface as
// stale even between two dev builds.
func bacioVersionDisplay(seen, current string) string {
	if seen == "" {
		return "-"
	}
	if current == "" || current == "dev" || seen == current {
		return seen
	}
	return seen + "!stale"
}

// channelStatus reports whether a session currently has a live `bacio
// channel` behind it — "live" when the hooks last saw a fresh
// agent_channels row for the session's (host, claude_pid), "-" when
// none was ever linked or its heartbeat went stale. A stale link reads
// the same as none: the channel process is gone either way.
func channelStatus(s *model.AgentSession, now time.Time) string {
	if s.ChannelSeenAt == nil {
		return "-"
	}
	if now.Sub(s.ChannelSeenAt.UTC()) <= model.AgentLivenessThreshold {
		return "live"
	}
	return "-"
}

func emitAgentSessionDetail(view *client.AgentSessionView) error {
	s := view.Session
	fmt.Fprintf(os.Stdout, "Session:  %s\n", s.SessionID)
	fmt.Fprintf(os.Stdout, "Agent:    %s\n", dashIfEmpty(s.AgentName))
	fmt.Fprintf(os.Stdout, "Actor:    %s\n", s.Actor)
	fmt.Fprintf(os.Stdout, "Repo:     %s\n", s.RepoPrefix)
	fmt.Fprintf(os.Stdout, "Model:    %s\n", dashIfEmpty(s.Model))
	fmt.Fprintf(os.Stdout, "Host:     %s\n", dashIfEmpty(s.Host))
	fmt.Fprintf(os.Stdout, "Branch:   %s\n", dashIfEmpty(s.Branch))
	if s.ClaudePID != 0 {
		fmt.Fprintf(os.Stdout, "Channel:  %s (claude_pid=%d)\n", channelStatus(s, time.Now().UTC()), s.ClaudePID)
	} else {
		fmt.Fprintf(os.Stdout, "Channel:  %s\n", channelStatus(s, time.Now().UTC()))
	}
	fmt.Fprintf(os.Stdout, "Bacio Version: %s\n", bacioVersionDisplay(s.ChannelVersion, version.String()))
	if s.RegisteredAt != nil {
		fmt.Fprintf(os.Stdout, "Registered: %s\n", s.RegisteredAt.Local().Format("2006-01-02 15:04:05"))
	} else {
		fmt.Fprintf(os.Stdout, "Registered: - (SessionStart stub, register tool never called)\n")
	}
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
