package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/channel"
	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/version"
)

func newChannelCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "channel",
		Short:  "Run bacio as a Claude Code channel (MCP server over stdio)",
		Hidden: true,
		Long: `Run bacio as a Claude Code "channel": an MCP server, spoken over
stdio, that pushes queued dispatches into THIS Claude Code session the
moment they're created — no waiting for the next prompt the way the
'bacio hook' pull path does.

Scoping: Claude Code spawns one channel subprocess per session and sets
$CLAUDE_PROJECT_DIR in its environment (it does NOT pass the session
id). The channel resolves the repo from that directory, walks its
process tree to the owning 'claude' pid, and looks that pid up in
.bacio/agents.json to find its agent identity — then pushes the
dispatches queued for that identity. If it can't resolve a repo or
identity it still starts — it just runs idle — because a failed MCP
server is worse than one that delivers nothing. It also exposes a
'reply' tool so the agent can acknowledge a dispatch without shelling
out to 'bacio agent ack'.

Research-preview caveats: Claude Code channels are a research preview.
A custom channel like bacio is not on the Anthropic allowlist, so it
must be launched with --dangerously-load-development-channels, e.g.:

    claude --dangerously-load-development-channels server:bacio

with a .mcp.json entry running 'bacio channel' (see 'bacio
install-channel'). Requires Claude Code v2.1.80 or later, and the
protocol contract may still change.

Like 'bacio hook' and 'bacio tui', this is a harness-integration shim,
not an agent-facing mutation command: it does not follow the six
agent-CLI principles. All output on stdout is JSON-RPC; diagnostics go
to stderr.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio channel: not supported in remote mode — the agent registry is local-only")
			}
			logf := func(format string, a ...any) {
				fmt.Fprintf(os.Stderr, "bacio channel: "+format+"\n", a...)
			}

			// Claude Code sets CLAUDE_PROJECT_DIR in a stdio MCP server's
			// environment; the session id is NOT passed (only hooks get
			// it, via their JSON payload). Fall back to the process cwd
			// when CLAUDE_PROJECT_DIR is absent — e.g. running by hand.
			projectDir := strings.TrimSpace(os.Getenv("CLAUDE_PROJECT_DIR"))
			if projectDir == "" {
				if cwd, err := os.Getwd(); err == nil {
					projectDir = cwd
				}
			}

			// The `claude` process this channel descends from is the key
			// to everything: .bacio/agents.json maps claude_pid -> agent
			// identity, and the hooks join on the same pid. Resolved once.
			claudePID := findClaudeAncestor(os.Getpid())

			// Resolve the repo + agent identity this channel serves. Both
			// are best-effort: a channel that can't resolve them still
			// starts and serves a valid (idle) MCP server.
			var info *git.Info
			var agentName string
			if gi, err := git.Detect(projectDir); err == nil {
				info = gi
				agentName = readAgentIdentity(gi.Root, claudePID)
				if agentName == "" {
					logf("no agents.json entry for claude_pid=%d in %s — channel will run idle until a hook records one", claudePID, gi.Root)
				}
			} else {
				logf("%s is not a git repo — channel will run idle", projectDir)
			}

			actorName := agentName
			if actorName == "" {
				actorName = actor()
			}
			c, err := client.Open(context.Background(), client.Options{
				DBPath: opts.dbPath,
				Actor:  actorName,
			})
			if err != nil {
				return err
			}
			defer c.Close()

			var repo *model.Repo
			if info != nil {
				if r, _, err := c.EnsureRepo(context.Background(), info); err == nil {
					repo = r
				} else {
					logf("could not resolve repo for %s: %v — channel will run idle", info.Root, err)
				}
			}

			dumpChannelDiagnostics(logf, projectDir, info, agentName, repo)

			repoRoot := ""
			if info != nil {
				repoRoot = info.Root
			}
			host, _ := os.Hostname()

			src := &channelSource{
				c:          c,
				repo:       repo,
				repoRoot:   repoRoot,
				host:       host,
				claudePID:  int64(claudePID),
				channelPID: int64(os.Getpid()),
				pushed:     map[int64]bool{},
			}
			srv := channel.New(src, "bacio", version.String(), os.Stdin, os.Stdout, logf)

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return srv.Run(ctx)
		},
	}
}

// channelSource adapts the bacio dispatch queue to channel.Source. It
// is NOT scoped to a session id — Claude Code never tells the channel
// its session. Instead it scopes by (host, claudePID): on each tick it
// walks the open sessions matching its own (host, claudePID) and
// targets dispatches at those session_ids directly. Agent identity is
// orthogonal — it only exists post-register, and the channel doesn't
// need it for drain/setup.
//
// claudePID is the `claude` process this channel descends from: the
// session row's claude_pid column is the join key.
type channelSource struct {
	c          client.Client
	repo       *model.Repo // stable for the channel's lifetime
	repoRoot   string      // repo root — agents.json still lives here (read-only here; register writes it)
	host       string
	claudePID  int64
	channelPID int64

	// pushed is the set of dispatch ids this channel process has already
	// emitted. DrainDispatches returns un-acked dispatches (pending AND
	// delivered) so a lost push can be recovered — but without this
	// guard the channel would re-push every still-un-acked dispatch on
	// every 3s poll tick. A fresh channel process starts with an empty
	// set, so a restart still re-pushes work the previous process's push
	// may not have landed. Only touched from the single poller goroutine.
	pushed map[int64]bool
}

// hintedAgentName is a best-effort lookup of the agent identity slug
// associated with this channel's claude_pid, used purely as a hint for
// the agent_channels row (so `bacio agent channels` can show the slug).
// Empty pre-register; populated after register writes agents.json.
func (s *channelSource) hintedAgentName() string {
	if s.repoRoot == "" || s.claudePID == 0 {
		return ""
	}
	return readAgentIdentity(s.repoRoot, int(s.claudePID))
}

func (s *channelSource) Drain(ctx context.Context) ([]channel.Event, error) {
	if s.repo == nil || s.claudePID == 0 {
		return nil, nil
	}
	sessions, err := s.c.SessionsByClaudePID(ctx, s.host, s.claudePID)
	if err != nil {
		return nil, err
	}
	var out []channel.Event
	for _, sess := range sessions {
		ds, derr := s.c.DrainDispatches(ctx, sess.SessionID)
		if derr != nil {
			fmt.Fprintln(os.Stderr, "bacio channel: drain session", sess.SessionID, ":", derr)
			continue
		}
		for _, d := range ds {
			if s.pushed[d.ID] {
				continue
			}
			s.pushed[d.ID] = true
			out = append(out, channel.Event{
				ID:       d.ID,
				IssueKey: d.IssueKey,
				From:     d.CreatedBy,
				Mode:     string(d.Mode),
				Payload:  d.Payload,
			})
		}
	}
	return out, nil
}

// Heartbeat records this channel as live in agent_channels every poll
// tick. It keys on (host, claude_pid) — the hooks correlate a session
// to a channel on exactly that pair — so it needs a repo and a resolved
// claude_pid. The agent name is a hint (read from agents.json if it's
// been written yet — empty pre-register), used for human-readable
// display only.
func (s *channelSource) Heartbeat(ctx context.Context) error {
	if s.repo == nil || s.claudePID == 0 {
		return nil
	}
	return s.c.UpsertAgentChannel(ctx, s.repo, s.hintedAgentName(), s.host, s.claudePID, s.channelPID)
}

func (s *channelSource) Ack(ctx context.Context, eventID int64, note string) error {
	_, err := s.c.AckDispatch(ctx, inputs.AgentAckInput{ID: eventID, Note: note}, false)
	return err
}

// Register is the agent-driven side of the channel<->session join.
// The agent calls the bacio MCP `register` tool with its own session
// id (and optionally model, branch); we resolve/mint its persistent
// identity, write agents.json so future hook invocations can name the
// agent in their briefings, enrich the session row (CompleteRegistration
// sets agent_id, actor, model, branch, channel_version,
// registered_at), and stamp channel_seen_at via LinkSessionChannel.
//
// Identity preservation across /clear: we look up the existing slug
// for this claude_pid in agents.json and pass it through, so the new
// post-/clear session inherits the previous identity instead of being
// minted a fresh one (which would orphan the previous slug — the
// SessionEnd hook just marked its last session ended:clear). An empty
// hint (truly new claude_pid, no agents.json entry) still mints fresh.
//
// The bacio binary version stamped onto agent_sessions.channel_version
// comes from internal/version.String() inside this channel process —
// NOT from anything the agent passes. The agent doesn't reliably know
// it, and the whole point of recording it is to detect stale channel
// processes vs. the binary the UI is running. String() (not bare
// Version) so dev builds get commit-level resolution.
//
// claudePID may legitimately be 0 (no `claude` ancestor walkable);
// LinkSessionChannel treats a 0 join key as "no channel" and leaves
// channel_seen_at untouched — register's other side-effects still apply.
func (s *channelSource) Register(ctx context.Context, sessionID, modelID, branch string) error {
	if s.repo == nil {
		return fmt.Errorf("channel has no resolved repo — cannot register")
	}
	if err := s.Heartbeat(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "bacio channel: heartbeat before register:", err)
	}
	sess, err := s.c.CompleteRegistration(ctx, s.repo, inputs.AgentRegisterInput{
		SessionID: sessionID,
		Agent:     s.hintedAgentName(),
		Host:      s.host,
		Model:     modelID,
		Branch:    branch,
	}, version.String())
	if err != nil {
		return err
	}
	// Persist (claude_pid -> agent identity) in .bacio/agents.json so
	// subsequent hooks for the same claude_pid (heartbeat, end, future
	// sessions after /clear) can name the agent in their briefings.
	// Best-effort: a write failure doesn't fail register.
	if sess.AgentName != "" && s.repoRoot != "" && s.claudePID != 0 {
		if rerr := recordAgentSession(s.repoRoot, int(s.claudePID), s.host, sess.AgentName, sessionID); rerr != nil {
			fmt.Fprintln(os.Stderr, "bacio channel: update agents.json:", rerr)
		}
	}
	if err := s.c.LinkSessionChannel(ctx, sessionID, s.claudePID, s.host); err != nil {
		fmt.Fprintln(os.Stderr, "bacio channel: link session channel:", err)
	}
	return nil
}

// EnsureSetup walks the open sessions for this channel's (host,
// claudePID) and queues a setup dispatch for each one that hasn't
// completed register yet. Idempotent — EnsureSetupDispatch dedupes via
// (target_session_id, created_by=bacio-channel, status open). No-ops
// when the channel has no resolved repo / claude_pid (idle channel) or
// when no open unregistered session matches.
func (s *channelSource) EnsureSetup(ctx context.Context) error {
	if s.repo == nil || s.claudePID == 0 {
		return nil
	}
	sessions, err := s.c.SessionsByClaudePID(ctx, s.host, s.claudePID)
	if err != nil {
		return err
	}
	for _, sess := range sessions {
		if sess.RegisteredAt != nil {
			continue // already registered — no nudge needed
		}
		if _, err := s.c.EnsureSetupDispatch(ctx, s.repo, sess.SessionID); err != nil {
			fmt.Fprintln(os.Stderr, "bacio channel: ensure setup dispatch for", sess.SessionID, ":", err)
		}
	}
	return nil
}

// dumpChannelDiagnostics logs everything the channel process can see at
// startup: pid/ppid, process ancestry, the resolved project dir / git
// root / agent identity / repo, and the full environment. It's a
// discovery aid for wiring up session<->channel correlation — Claude
// Code does NOT hand the channel its session id, so we need to know
// exactly what IS reachable (notably: is the `claude` process a
// walkable ancestor?). All of it goes to stderr, which Claude Code
// surfaces in its MCP server logs.
func dumpChannelDiagnostics(logf func(string, ...any), projectDir string, info *git.Info, agentName string, repo *model.Repo) {
	logf("--- channel diagnostics ---")
	logf("pid=%d ppid=%d", os.Getpid(), os.Getppid())
	if cwd, err := os.Getwd(); err == nil {
		logf("cwd=%s", cwd)
	}
	logf("CLAUDE_PROJECT_DIR=%q resolved projectDir=%q", os.Getenv("CLAUDE_PROJECT_DIR"), projectDir)
	if info != nil {
		logf("git root=%s remote=%q", info.Root, info.RemoteURL)
	} else {
		logf("git root=<unresolved>")
	}
	logf("agent identity (.bacio/agents.json)=%q", agentName)
	if repo != nil {
		logf("repo prefix=%s id=%d", repo.Prefix, repo.ID)
	} else {
		logf("repo=<unresolved>")
	}
	if claudePID := findClaudeAncestor(os.Getpid()); claudePID != 0 {
		logf("resolved claude_pid=%d (nearest `claude` ancestor)", claudePID)
	} else {
		logf("resolved claude_pid=0 — no `claude` ancestor found; session<->channel correlation unavailable")
	}
	for _, line := range processAncestry(os.Getpid()) {
		logf("ancestry: %s", line)
	}
	// The full environment can carry tokens/secrets and lands in Claude
	// Code's MCP logs, so the dump is gated behind BACIO_CHANNEL_DEBUG.
	// The pid / ancestry / repo / identity / claude_pid lines above —
	// the actually-useful correlation diagnostics — stay always-on.
	if os.Getenv("BACIO_CHANNEL_DEBUG") != "" {
		env := os.Environ()
		sort.Strings(env)
		logf("environ (%d vars):", len(env))
		for _, kv := range env {
			logf("  %s", kv)
		}
	} else {
		logf("environ: %d vars (set BACIO_CHANNEL_DEBUG=1 to dump)", len(os.Environ()))
	}
	logf("--- end channel diagnostics ---")
}
