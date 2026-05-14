package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
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
id). The channel resolves the repo from that directory and the agent
identity from its .bacio/agent file, and pushes the dispatches queued
for that identity. If it can't resolve a repo or identity it still
starts — it just runs idle — because a failed MCP server is worse than
one that delivers nothing. It also exposes a 'reply' tool so the agent
can acknowledge a dispatch without shelling out to 'bacio agent ack'.

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

			// Resolve the repo + agent identity this channel serves. Both
			// are best-effort: a channel that can't resolve them still
			// starts and serves a valid (idle) MCP server.
			var info *git.Info
			var agentName string
			if gi, err := git.Detect(projectDir); err == nil {
				info = gi
				agentName = readAgentSlug(gi.Root)
				if agentName == "" {
					logf("no .bacio/agent in %s — channel will run idle (no identity to scope dispatches to)", gi.Root)
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

			src := &channelSource{c: c, repo: repo, agentName: agentName}
			srv := channel.New(src, "bacio", os.Stdin, os.Stdout, logf)

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return srv.Run(ctx)
		},
	}
}

// channelSource adapts the bacio dispatch queue to channel.Source,
// scoped to a repo + agent identity (not a session id — the channel is
// never told its session). A nil repo or empty agentName means the
// channel couldn't scope itself: Drain returns nothing and the channel
// runs idle. Ack still works regardless — it's keyed on dispatch id.
type channelSource struct {
	c         client.Client
	repo      *model.Repo
	agentName string
}

func (s *channelSource) Drain(ctx context.Context) ([]channel.Event, error) {
	ds, err := s.c.DrainAgentDispatches(ctx, s.repo, s.agentName)
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

func (s *channelSource) Ack(ctx context.Context, eventID int64, note string) error {
	_, err := s.c.AckDispatch(ctx, inputs.AgentAckInput{ID: eventID, Note: note}, false)
	return err
}
