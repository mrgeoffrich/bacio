// Package agentmode exposes the single BACIO_AGENT_MODE env-var check
// shared by the hook subcommands and the channel server. Centralised so
// the parsing rule lives in one place and the import surface is tiny.
package agentmode

import (
	"fmt"
	"os"
)

// EnvVar is the env var bacio consults to decide whether the
// hook subcommands and the channel poller should activate.
const EnvVar = "BACIO_AGENT_MODE"

// Enabled reports whether bacio's automatic agent-supervision surfaces
// (the hook subcommands and the channel's setup-dispatch poller) should
// activate for the current process. When false, hooks no-op early and
// the channel poller stays parked — the MCP handshake and tools remain
// available regardless, so an agent that explicitly wants to opt in via
// a manual `register` call still can.
//
// Accepts "1" or "true" (case-sensitive) as enable signals; everything
// else — including unset — reads as disabled. Restrictive on purpose
// so the gate is binary and visible.
func Enabled() bool {
	switch os.Getenv(EnvVar) {
	case "1", "true":
		return true
	default:
		return false
	}
}

// DenyIfEnabled returns a non-nil error when agent mode is on, naming
// the command path so the message tells the agent which verb was
// blocked and why. CLI commands that delete shared state in a
// destructive, hard-to-reverse way (`bacio issue rm`, `bacio feature
// rm`, `bacio doc rm`, …) call this at the top of their RunE so an
// agent-driven session has to consult the human before deleting
// anything. The escape hatch is for the human to run the command
// themselves from an interactive shell with BACIO_AGENT_MODE unset —
// agents should not unset the env var themselves.
func DenyIfEnabled(command string) error {
	if !Enabled() {
		return nil
	}
	return fmt.Errorf("`bacio %s` is blocked in agent mode (%s=1): destructive deletions need human approval — ask via mcp__bacio__ask_user_question and let the user run this", command, EnvVar)
}
