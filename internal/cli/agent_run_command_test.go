package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/agentmode"
)

// TestAgentRunCommandPrintsCanonicalOneLiner pins the BACI-218 contract:
// stdout is exactly the canonical agent-launch one-liner followed by a
// single newline — nothing more, nothing on stderr. Shells composing
// against this verb (eval "$(bacio agent-run-command)", alias=…) treat
// any byte change as a behaviour change, so the bar for editing is the
// agentmode.LaunchCommand pin in agentmode_test.go, not this test.
//
// BACI-301/344: the one-liner now injects ANTHROPIC_BASE_URL pointed at
// the standalone reverse-proxy port for this worktree (env.ProxyAddr).
// The expected endpoint is derived from the same resolveEnv chain the
// verb uses so the test stays deterministic wherever it runs.
func TestAgentRunCommandPrintsCanonicalOneLiner(t *testing.T) {
	cmd := newAgentRunCommandCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(nil)

	// The verb writes to os.Stdout directly so shell redirection works
	// the same in tests as in production — capture via the OS-level
	// helper rather than cobra's SetOut/SetErr buffers.
	got := captureStdoutText(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("agent-run-command: %v", err)
		}
	})
	env, err := resolveEnv()
	if err != nil {
		t.Fatalf("resolveEnv: %v", err)
	}
	want := agentmode.LaunchCommand(agentmode.ProxyEndpoint(env.ProxyAddr)) + "\n"
	if got != want {
		t.Fatalf("stdout mismatch:\n  got:  %q\n  want: %q", got, want)
	}
	if strings.Count(got, "\n") != 1 {
		t.Fatalf("stdout must be exactly one line, got %d newlines:\n%s", strings.Count(got, "\n"), got)
	}
	// Belt-and-braces: the verb explicitly carries no log noise on
	// stderr in the success path (no banner, no telemetry, no env-
	// resolution chatter). Anything captured here would leak into a
	// composed `eval "$(...)"` invocation in an unhelpful way.
	if stderr.Len() != 0 {
		t.Fatalf("stderr must be empty in the success path; got: %q", stderr.String())
	}
}

// TestAgentRunCommandRejectsArgs guards against a typo that would let
// the verb silently accept positional noise. `cobra.NoArgs` is wired
// into the command — keep the test so an accidental switch to
// ArbitraryArgs/MaximumNArgs doesn't slip through.
func TestAgentRunCommandRejectsArgs(t *testing.T) {
	cmd := newAgentRunCommandCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"unexpected"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error from positional arg, got nil")
	}
}

// TestAgentRunCommandMatchesActivationBanner is the cross-call drift
// guard: the post-install activation banner and `bacio agent-run-command`
// must emit the identical launch string so a user who runs install-agent
// and then copies the one-liner sees the same command the verb prints.
// Both call sites resolve through agentmode.LaunchCommand; this test
// verifies the banner — built from the same endpoint — contains it.
func TestAgentRunCommandMatchesActivationBanner(t *testing.T) {
	const endpoint = "http://127.0.0.1:5320/anthropic"
	var buf bytes.Buffer
	printActivationBanner(&buf, endpoint)
	want := agentmode.LaunchCommand(endpoint)
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("activation banner missing launch command %q; got:\n%s", want, buf.String())
	}
}
