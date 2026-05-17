// Package agentmode exposes the single BACIO_AGENT_MODE env-var check
// shared by the hook subcommands and the channel server. Centralised so
// the parsing rule lives in one place and the import surface is tiny.
package agentmode

import "os"

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
