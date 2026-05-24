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
