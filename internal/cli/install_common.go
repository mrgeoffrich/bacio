package cli

import (
	"fmt"
	"io"

	"github.com/mrgeoffrich/bacio/internal/agentmode"
)

// printActivationBanner writes the shared post-install reminder that
// bacio's hooks + channel are inert until BACIO_AGENT_MODE=1 is set in
// the launching Claude environment. Used by install-hooks and
// install-channel; both surface it on stderr (rather than via the
// structured ok() success body) because it is human guidance — folding
// a paragraph of prose into the JSON success payload would clutter
// machine consumers' parse path.
func printActivationBanner(w io.Writer) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Activation:")
	fmt.Fprintf(w, "  bacio's hooks + channel are inert unless %s=1 is set in the\n", agentmode.EnvVar)
	fmt.Fprintln(w, "  environment of the Claude session that loads them.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  To launch this directory as a registered bacio agent:")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "    %s=1 claude\n", agentmode.EnvVar)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  For normal interactive Claude sessions, launch without the env")
	fmt.Fprintln(w, "  var: bacio's hooks and channel detect, log, and exit cleanly, and")
	fmt.Fprintln(w, "  bacio CLI calls attribute to your OS user as usual.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  `bacio status` reports the current value.")
}
