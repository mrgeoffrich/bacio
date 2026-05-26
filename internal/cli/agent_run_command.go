package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/agentmode"
)

// newAgentRunCommandCmd builds the `bacio agent-run-command` verb: a
// harness shim that prints the canonical one-liner you copy/paste into
// a shell to spin up an agent-mode Claude Code session against this
// repo, and nothing else. The point is shell composability:
//
//	eval "$(bacio agent-run-command)"
//	alias bacio-agent="$(bacio agent-run-command)"
//
// so the output is exactly one line on stdout — no banner, no log
// noise, no -o json wrapping. Read-only by construction: no store
// access, no .claude/ writes, no env mutation. The string comes from
// agentmode.LaunchCommand so it stays in lockstep with the post-install
// activation banner (printActivationBanner) and the install-channel
// tests that pin both.
//
// Top-level hyphenated verb (matching install-agent / install-skill)
// per the BACI-218 brief, rather than a subverb of `bacio agent` — the
// user explicitly dictated the spelling.
func newAgentRunCommandCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent-run-command",
		Short: "Print the shell one-liner that starts an agent-mode Claude Code session",
		Long: `Print the exact shell command needed to spin up an agent-mode Claude Code
session against this repo, and nothing else: stdout is a single line, no
banner, no log noise, no -o json wrapping. Designed for shell composition:

    eval "$(bacio agent-run-command)"
    alias bacio-agent="$(bacio agent-run-command)"

Read-only: never mutates the store, never touches .claude/, never calls
install-agent. This is a harness shim (like bacio tui / web), so it has
no --json / --dry-run / schema entry.

The emitted command is the same one ` + "`bacio install-agent`" + ` surfaces in its
post-install activation banner — the two strings share a single source
of truth (agentmode.LaunchCommand) so they cannot drift.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(os.Stdout, agentmode.LaunchCommand)
			return err
		},
	}
}
