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

The channel is scoped to one session via $CLAUDE_CODE_SESSION_ID and
exposes a 'reply' tool so the agent can acknowledge a dispatch without
shelling out to 'bacio agent ack'.

Research-preview caveats: Claude Code channels are a research preview.
A custom channel like bacio is not on the Anthropic allowlist, so it
must be launched with --dangerously-load-development-channels, e.g.:

    claude --dangerously-load-development-channels server:bacio

with a .mcp.json entry running 'bacio channel'. Requires Claude Code
v2.1.80 or later, and the protocol contract may still change.

Like 'bacio hook' and 'bacio tui', this is a harness-integration shim,
not an agent-facing mutation command: it does not follow the six
agent-CLI principles. All output on stdout is JSON-RPC; diagnostics go
to stderr.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if inRemoteMode() {
				return fmt.Errorf("bacio channel: not supported in remote mode — the agent registry is local-only")
			}
			sid := strings.TrimSpace(os.Getenv(claudeSessionEnv))
			if sid == "" {
				return fmt.Errorf("bacio channel: $%s is empty — a channel is scoped to one session", claudeSessionEnv)
			}

			// Attribute acks to the agent identity when .bacio/agent is
			// present, mirroring the hook shim; fall back to the OS user.
			act := actor()
			if cwd, err := os.Getwd(); err == nil {
				if info, err := git.Detect(cwd); err == nil {
					if slug := readAgentSlug(info.Root); slug != "" {
						act = slug
					}
				}
			}

			c, err := client.Open(context.Background(), client.Options{
				DBPath: opts.dbPath,
				Actor:  act,
			})
			if err != nil {
				return err
			}
			defer c.Close()

			src := &channelSource{c: c, sessionID: sid}
			logf := func(format string, a ...any) {
				fmt.Fprintf(os.Stderr, format+"\n", a...)
			}
			srv := channel.New(src, "bacio", os.Stdin, os.Stdout, logf)

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return srv.Run(ctx)
		},
	}
}

// channelSource adapts the bacio dispatch queue to channel.Source,
// scoped to one session. Drain hands the channel this session's pending
// dispatches (marking them delivered); Ack records the agent's reply.
type channelSource struct {
	c         client.Client
	sessionID string
}

func (s *channelSource) Drain(ctx context.Context) ([]channel.Event, error) {
	ds, err := s.c.DrainDispatches(ctx, s.sessionID)
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
