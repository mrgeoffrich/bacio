// Package agentmode exposes the single BACIO_AGENT_MODE env-var check
// shared by the hook subcommands and the channel server. Centralised so
// the parsing rule lives in one place and the import surface is tiny.
package agentmode

import (
	"fmt"
	"net"
	"os"
)

// EnvVar is the env var bacio consults to decide whether the
// hook subcommands and the channel poller should activate.
const EnvVar = "BACIO_AGENT_MODE"

// claudeInvocation is the fixed `claude …` half of the launch one-liner:
// per-tool approval prompt waived, native-channels transport opted in so
// dispatches stream live. LaunchCommand prepends the env-var assignments.
const claudeInvocation = "claude --dangerously-skip-permissions --dangerously-load-development-channels server:bacio"

// LaunchCommand returns the canonical shell one-liner that spins up a
// Claude Code session wired to bacio's agent-mode supervision. It sets
// BACIO_AGENT_MODE=1, points ANTHROPIC_BASE_URL at the BACI-301 reverse
// proxy (proxyEndpoint, e.g. http://127.0.0.1:5320/anthropic) so all
// Anthropic traffic routes through the local bacio web/api server, and
// flips ENABLE_TOOL_SEARCH=true. No bacio correlation header is injected:
// the reverse-proxy capture correlates off Claude Code's own request
// headers (X-Claude-Code-Session-Id / -Agent-Id), so the launch one-liner
// needs nothing extra. Kept here so the post-install activation banner
// (printActivationBanner) and the `bacio agent-run-command` verb emit the
// exact same string and never drift apart.
//
// Consequence (intended, per the reverse-proxy-monitor feature): a
// running `bacio web`/`bacio api` is a hard dependency of an agent
// session — without it the worker gets connection-refused on
// ANTHROPIC_BASE_URL.
func LaunchCommand(proxyEndpoint string) string {
	return fmt.Sprintf("%s=1 ANTHROPIC_BASE_URL=%s ENABLE_TOOL_SEARCH=true %s",
		EnvVar, proxyEndpoint, claudeInvocation)
}

// ProxyEndpoint builds the ANTHROPIC_BASE_URL value from a resolved
// host:port bind address (env.APIAddr), e.g. "127.0.0.1:5320" →
// "http://127.0.0.1:5320/anthropic". A bind address that doesn't parse
// as host:port falls back to the default 127.0.0.1 host so the launch
// one-liner always emits a usable URL rather than a malformed one.
func ProxyEndpoint(apiAddr string) string {
	host, port, err := net.SplitHostPort(apiAddr)
	if err != nil || host == "" || port == "" {
		host, port = "127.0.0.1", "5320"
	}
	return fmt.Sprintf("http://%s/anthropic", net.JoinHostPort(host, port))
}

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
