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
			}
			srv := channel.New(src, "bacio", os.Stdin, os.Stdout, logf)

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return srv.Run(ctx)
		},
	}
}

// channelSource adapts the bacio dispatch queue to channel.Source. It
// is NOT scoped to a session id — Claude Code never tells the channel
// its session. Instead it holds the repo (stable for the process's
// life) and re-reads its agent identity from .bacio/agents.json keyed
// on claudePID on every call: that entry is written by the session-start
// hook, which routinely runs *after* the channel subprocess is spawned,
// so caching the identity once at startup froze an empty identity for
// the whole session — the original delivery bug.
//
// claudePID is the `claude` process this channel descends from: both
// the agents.json key and what the hooks join on to correlate this
// channel back to a session.
type channelSource struct {
	c          client.Client
	repo       *model.Repo // stable for the channel's lifetime
	repoRoot   string      // repo root — .bacio/agents.json lives here
	host       string
	claudePID  int64
	channelPID int64
}

// identity re-reads this channel's agent slug from .bacio/agents.json.
// Empty when the repo couldn't be resolved or no hook has recorded an
// entry for this claude_pid yet — both transient, both leave the
// channel running idle until they resolve.
func (s *channelSource) identity() string {
	if s.repoRoot == "" || s.claudePID == 0 {
		return ""
	}
	return readAgentIdentity(s.repoRoot, int(s.claudePID))
}

func (s *channelSource) Drain(ctx context.Context) ([]channel.Event, error) {
	ds, err := s.c.DrainAgentDispatches(ctx, s.repo, s.identity())
	if err != nil {
		return nil, err
	}
	out := make([]channel.Event, 0, len(ds))
	for _, d := range ds {
		out = append(out, channel.Event{
			ID:       d.ID,
			IssueKey: d.IssueKey,
			From:     d.CreatedBy,
			Mode:     string(d.Mode),
			Payload:  d.Payload,
		})
	}
	return out, nil
}

// Heartbeat records this channel as live in agent_channels every poll
// tick. It keys on (host, claude_pid) — the hooks correlate a session
// to a channel on exactly that pair — so it needs a repo and a resolved
// claude_pid, but NOT an identity: an identity-less channel still
// records presence (agent_id NULL) and the next tick fills the identity
// in once the session-start hook records it in .bacio/agents.json.
func (s *channelSource) Heartbeat(ctx context.Context) error {
	if s.repo == nil || s.claudePID == 0 {
		return nil
	}
	return s.c.UpsertAgentChannel(ctx, s.repo, s.identity(), s.host, s.claudePID, s.channelPID)
}

func (s *channelSource) Ack(ctx context.Context, eventID int64, note string) error {
	_, err := s.c.AckDispatch(ctx, inputs.AgentAckInput{ID: eventID, Note: note}, false)
	return err
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
	env := os.Environ()
	sort.Strings(env)
	logf("environ (%d vars):", len(env))
	for _, kv := range env {
		logf("  %s", kv)
	}
	logf("--- end channel diagnostics ---")
}
