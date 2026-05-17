package cli

import (
	"io"
	"os"
	"strings"
	"testing"
)

// TestSkipUnlessAgentMode pins the BACI-48 gate at the helper level:
// each hook subcommand calls this exactly once at the top of its RunE,
// so a regression here silently re-enables the auto-registration path
// the issue was filed against.
func TestSkipUnlessAgentMode(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		// t.Setenv stamps the env so Cleanup can restore it; Unsetenv
		// after makes the variable truly absent for the test body.
		t.Setenv("BACIO_AGENT_MODE", "scratch")
		if err := os.Unsetenv("BACIO_AGENT_MODE"); err != nil {
			t.Fatalf("unset: %v", err)
		}
		stderr := captureStderr(t, func() {
			if !skipUnlessAgentMode("session-start") {
				t.Fatal("expected skip=true when BACIO_AGENT_MODE is unset")
			}
		})
		if !strings.Contains(stderr, "bacio hook session-start") {
			t.Fatalf("stderr missing subcommand name: %q", stderr)
		}
		if !strings.Contains(stderr, "BACIO_AGENT_MODE not set") {
			t.Fatalf("stderr missing env-var name: %q", stderr)
		}
		if !strings.Contains(stderr, "skipping") {
			t.Fatalf("stderr missing skip verb: %q", stderr)
		}
	})

	t.Run("enabled", func(t *testing.T) {
		t.Setenv("BACIO_AGENT_MODE", "1")
		stderr := captureStderr(t, func() {
			if skipUnlessAgentMode("session-start") {
				t.Fatal("expected skip=false when BACIO_AGENT_MODE=1")
			}
		})
		if stderr != "" {
			t.Fatalf("stderr should be silent when activated: %q", stderr)
		}
	})

	t.Run("disabled_invalid_value", func(t *testing.T) {
		// "yes" is not on the restrictive enable list — must skip.
		t.Setenv("BACIO_AGENT_MODE", "yes")
		stderr := captureStderr(t, func() {
			if !skipUnlessAgentMode("user-prompt-submit") {
				t.Fatal("expected skip=true for BACIO_AGENT_MODE=yes")
			}
		})
		if !strings.Contains(stderr, "user-prompt-submit") {
			t.Fatalf("stderr missing subcommand name: %q", stderr)
		}
	})
}

// TestHookSubcommandsGatedByAgentMode runs each registered hook
// subcommand's RunE with BACIO_AGENT_MODE unset and asserts that the
// command exits nil with no side effects (it never even reads stdin or
// touches the DB). A regression that drops the gate would either error
// or hang waiting for stdin — both surface as a fatal test failure.
func TestHookSubcommandsGatedByAgentMode(t *testing.T) {
	t.Setenv("BACIO_AGENT_MODE", "scratch")
	if err := os.Unsetenv("BACIO_AGENT_MODE"); err != nil {
		t.Fatalf("unset: %v", err)
	}

	root := newHookCmd()
	subs := root.Commands()
	if len(subs) == 0 {
		t.Fatal("newHookCmd registered no subcommands")
	}
	for _, sub := range subs {
		sub := sub
		t.Run(sub.Use, func(t *testing.T) {
			stderr := captureStderr(t, func() {
				if err := sub.RunE(sub, nil); err != nil {
					t.Fatalf("RunE returned non-nil: %v", err)
				}
			})
			if !strings.Contains(stderr, "BACIO_AGENT_MODE not set") {
				t.Fatalf("expected skip notice on stderr, got %q", stderr)
			}
			if !strings.Contains(stderr, "bacio hook "+sub.Use) {
				t.Fatalf("stderr missing subcommand name %q: %q", sub.Use, stderr)
			}
		})
	}
}

// captureStderr swaps os.Stderr for a pipe over the duration of fn and
// returns whatever was written. Restores the original handle via t.Cleanup
// so a panic in fn still leaves the test runner with a working stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = orig })

	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()

	fn()
	_ = w.Close()
	os.Stderr = orig
	return <-done
}
