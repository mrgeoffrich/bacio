package agentmode

import (
	"os"
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
