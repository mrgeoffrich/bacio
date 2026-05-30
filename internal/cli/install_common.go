package cli

import (
	"fmt"
	"io"

	"github.com/mrgeoffrich/bacio/internal/agentmode"
)

// printActivationBanner writes the shared post-install reminder that
// bacio's hooks + channel are inert until BACIO_AGENT_MODE=1 is set in
// the launching Claude environment. Used by install-agent; it surfaces
// the banner on stderr (rather than via the structured ok() success
// body) because it is human guidance — folding a paragraph of prose
// into the JSON success payload would clutter machine consumers' parse
// path.
//
// endpoint is the resolved ANTHROPIC_BASE_URL the launch one-liner
// injects (BACI-301) — passed in so the banner stays byte-identical to
// the `bacio agent-run-command` verb's output. correlationKey is the
// resolved worktree slug (BACI-305) the launch one-liner stamps as the
// X-Bacio-Corr header value; empty outside a worktree env (the header is
// then omitted).
func printActivationBanner(w io.Writer, endpoint, correlationKey string) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Activation:")
	fmt.Fprintf(w, "  bacio's hooks + channel are inert unless %s=1 is set in the\n", agentmode.EnvVar)
	fmt.Fprintln(w, "  environment of the Claude session that loads them.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  To launch this directory as a registered bacio agent:")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "    %s\n", agentmode.LaunchCommand(endpoint, correlationKey))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  --dangerously-skip-permissions waives the per-tool approval prompt")
	fmt.Fprintln(w, "  for the agent session; --dangerously-load-development-channels")
	fmt.Fprintln(w, "  server:bacio opts in to the experimental native-channels transport")
	fmt.Fprintln(w, "  so dispatches stream live (the regular .mcp.json transport also")
	fmt.Fprintln(w, "  works without that flag — see 'bacio channel --help').")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  ANTHROPIC_BASE_URL routes the session's Anthropic traffic through")
	fmt.Fprintln(w, "  bacio's local reverse proxy, so a `bacio web` (or `bacio api`)")
	fmt.Fprintln(w, "  must be running on that port for the agent to reach the API.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  For normal interactive Claude sessions, launch without the env")
	fmt.Fprintln(w, "  var: bacio's hooks and channel detect, log, and exit cleanly, and")
	fmt.Fprintln(w, "  bacio CLI calls attribute to your OS user as usual.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  `bacio status` reports the current value.")
}
