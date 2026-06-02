package agentmode

import (
	"os"
	"strings"
	"testing"
)

// TestEnabled locks in the restrictive parse: only "1" and "true"
// (lower-case, exact) enable the gate. Anything else — including
// unset, "0", "false", "yes", and case variants — must read as
// disabled, so the activation signal is binary and visible.
func TestEnabled(t *testing.T) {
	cases := []struct {
		name    string
		set     bool
		value   string
		enabled bool
	}{
		{name: "unset", set: false, enabled: false},
		{name: "empty", set: true, value: "", enabled: false},
		{name: "one", set: true, value: "1", enabled: true},
		{name: "true_lower", set: true, value: "true", enabled: true},
		{name: "zero", set: true, value: "0", enabled: false},
		{name: "false_lower", set: true, value: "false", enabled: false},
		{name: "yes", set: true, value: "yes", enabled: false},
		{name: "TRUE_upper", set: true, value: "TRUE", enabled: false},
		{name: "True_mixed", set: true, value: "True", enabled: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.set {
				t.Setenv(EnvVar, c.value)
			} else {
				// t.Setenv stamps the current value once on first use so
				// it can restore it at cleanup; pair it with an immediate
				// Unsetenv so the test sees a truly absent env var.
				t.Setenv(EnvVar, "scratch")
				if err := os.Unsetenv(EnvVar); err != nil {
					t.Fatalf("unset: %v", err)
				}
			}
			if got := Enabled(); got != c.enabled {
				t.Fatalf("Enabled() = %v, want %v (value=%q set=%v)", got, c.enabled, c.value, c.set)
			}
		})
	}
}

// TestLaunchCommand pins the canonical one-liner for a sample proxy
// endpoint. Two callers depend on the exact string — the install-agent
// activation banner and the `bacio agent-run-command` verb — and shells /
// aliases people write against it (eval, alias=…) treat any byte change
// as a behaviour change, so the bar for editing is higher than the rest
// of the file. Belt-and-braces against an accidental refactor that drops
// a flag or the injected proxy env (BACI-301). No bacio correlation header
// is injected — correlation reads Claude Code's own X-Claude-Code-* headers.
func TestLaunchCommand(t *testing.T) {
	const endpoint = "http://127.0.0.1:5320/anthropic"
	const want = "BACIO_AGENT_MODE=1 ANTHROPIC_BASE_URL=http://127.0.0.1:5320/anthropic ENABLE_TOOL_SEARCH=true claude --dangerously-skip-permissions --dangerously-load-development-channels server:bacio"
	got := LaunchCommand(endpoint)
	if got != want {
		t.Fatalf("LaunchCommand drift:\n  got:  %q\n  want: %q", got, want)
	}
	if !strings.HasPrefix(got, EnvVar+"=1 ") {
		t.Fatalf("LaunchCommand %q must lead with %s=1", got, EnvVar)
	}
	for _, must := range []string{"ANTHROPIC_BASE_URL=" + endpoint, "ENABLE_TOOL_SEARCH=true"} {
		if !strings.Contains(got, must) {
			t.Fatalf("LaunchCommand %q missing %q", got, must)
		}
	}
	// No bacio correlation header is injected — the reverse-proxy capture
	// correlates off Claude Code's own X-Claude-Code-* request headers.
	if strings.Contains(got, "ANTHROPIC_CUSTOM_HEADERS") {
		t.Fatalf("launch one-liner must not inject ANTHROPIC_CUSTOM_HEADERS; got %q", got)
	}
}

// TestProxyEndpoint covers the host:port → ANTHROPIC_BASE_URL mapping.
// BACI-344 retargeted the launch endpoint at the standalone proxy port
// (env.ProxyAddr, the API port minus one), so the fallback when the bind
// address doesn't parse is now 127.0.0.1:5319.
func TestProxyEndpoint(t *testing.T) {
	cases := []struct {
		name      string
		proxyAddr string
		want      string
	}{
		{name: "default_addr", proxyAddr: "127.0.0.1:5319", want: "http://127.0.0.1:5319/anthropic"},
		{name: "worktree_port", proxyAddr: "127.0.0.1:5349", want: "http://127.0.0.1:5349/anthropic"},
		{name: "non_loopback_host", proxyAddr: "0.0.0.0:8079", want: "http://0.0.0.0:8079/anthropic"},
		{name: "empty_falls_back", proxyAddr: "", want: "http://127.0.0.1:5319/anthropic"},
		{name: "no_port_falls_back", proxyAddr: "127.0.0.1", want: "http://127.0.0.1:5319/anthropic"},
		{name: "empty_port_falls_back", proxyAddr: "127.0.0.1:", want: "http://127.0.0.1:5319/anthropic"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ProxyEndpoint(c.proxyAddr); got != c.want {
				t.Fatalf("ProxyEndpoint(%q) = %q, want %q", c.proxyAddr, got, c.want)
			}
		})
	}
}

// TestDenyIfEnabled covers the two states that matter: nil in normal
// (non-agent) sessions so the command runs through, and a non-nil
// error in agent sessions whose message names the blocked command and
// points the agent at the human-approval escape hatch.
func TestDenyIfEnabled(t *testing.T) {
	t.Run("agent_mode_off_returns_nil", func(t *testing.T) {
		t.Setenv(EnvVar, "scratch")
		if err := os.Unsetenv(EnvVar); err != nil {
			t.Fatalf("unset: %v", err)
		}
		if err := DenyIfEnabled("issue rm"); err != nil {
			t.Fatalf("DenyIfEnabled() with agent mode off = %v, want nil", err)
		}
	})
	t.Run("agent_mode_on_returns_typed_error", func(t *testing.T) {
		t.Setenv(EnvVar, "1")
		err := DenyIfEnabled("issue rm")
		if err == nil {
			t.Fatal("DenyIfEnabled() with agent mode on returned nil, want error")
		}
		msg := err.Error()
		for _, want := range []string{"issue rm", EnvVar, "mcp__bacio__ask_user_question"} {
			if !strings.Contains(msg, want) {
				t.Errorf("error message %q missing %q", msg, want)
			}
		}
	})
}
