package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

// TestReportInstallAgentDropsDangerousFlag is the BACI-48 regression
// guard, retargeted at the consolidated install-agent reporter
// (BACI-79): the post-install success body (everything routed through
// ok(), so it round-trips through -o json) must NOT include the
// --dangerously-load-development-channels launch flag — that paragraph
// of human-facing guidance belongs in the stderr banner
// printActivationBanner produces, not the machine-parsed structured
// success body.
func TestReportInstallAgentDropsDangerousFlag(t *testing.T) {
	for _, action := range []string{"add", "update"} {
		t.Run(action, func(t *testing.T) {
			stdout := captureStdoutText(t, func() {
				if err := reportInstallAgent("/repo/.claude/settings.json", nil, "/repo/.mcp.json", action, []string{"/repo/.claude/agents/bacio-plan-worker.md"}); err != nil {
					t.Fatalf("reportInstallAgent: %v", err)
				}
			})
			if strings.Contains(stdout, "--dangerously-load-development-channels") {
				t.Fatalf("reportInstallAgent must not surface --dangerously-load-development-channels in the structured success body; got:\n%s", stdout)
			}
			// Belt-and-braces: the success body should still confirm what
			// happened, so callers grepping for the file path still match.
			if !strings.Contains(stdout, "/repo/.mcp.json") {
				t.Fatalf("reportInstallAgent output missing .mcp.json path; got:\n%s", stdout)
			}
		})
	}
}

// TestReportInstallAgentJSONShape pins the structured success body
// shape: a single object with a "message" field (the shape ok()
// produces). Asserting on the field set catches accidental drift from
// the standard ok() wrapper.
func TestReportInstallAgentJSONShape(t *testing.T) {
	prev := opts.output
	opts.output = outputJSON
	t.Cleanup(func() { opts.output = prev })

	stdout := captureStdoutText(t, func() {
		if err := reportInstallAgent("/repo/.claude/settings.json", nil, "/repo/.mcp.json", "add", []string{"/repo/.claude/agents/bacio-plan-worker.md"}); err != nil {
			t.Fatalf("reportInstallAgent: %v", err)
		}
	})
	got := map[string]any{}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal: %v\noutput=%s", err, stdout)
	}
	msg, ok := got["message"].(string)
	if !ok {
		t.Fatalf("success body missing message field: %+v", got)
	}
	if strings.Contains(msg, "--dangerously-load-development-channels") {
		t.Fatalf("JSON message must not mention --dangerously flag; got %q", msg)
	}
}

// TestPrintActivationBannerMentionsEnvVar pins the shared banner's
// content. The banner is install-agent's way of telling the user how
// to opt in; if the env-var name or the launch incantation drift the
// user will be left wondering why the supervision they just installed
// isn't firing. BACI-49 added two dangerously-* flags to the launch
// line — they're pinned here so the one-line copy-paste hint can't
// silently regress to the bare 'BACIO_AGENT_MODE=1 claude' form again.
func TestPrintActivationBannerMentionsEnvVar(t *testing.T) {
	var buf bytes.Buffer
	printActivationBanner(&buf)
	got := buf.String()
	for _, want := range []string{
		"BACIO_AGENT_MODE",
		"BACIO_AGENT_MODE=1 claude --dangerously-skip-permissions --dangerously-load-development-channels server:bacio",
		"bacio status",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("activation banner missing %q; got:\n%s", want, got)
		}
	}
}

// TestApplyBacioChannelDoesNotBakeEnv is the BACI-63 regression guard:
// the .mcp.json entry for the bacio channel must NOT carry an `env`
// block (and specifically must not bake $BACIO_ENV) — the channel
// resolves the worktree manifest at runtime from cwd, which Claude
// Code sets to the project root when spawning the MCP subprocess.
// Baking env here would freeze the manifest path at install time and
// break sibling worktrees that share a single .mcp.json.
func TestApplyBacioChannelDoesNotBakeEnv(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/.mcp.json"
	top := map[string]json.RawMessage{}
	if err := applyBacioChannel(path, top, "/usr/local/bin/bacio"); err != nil {
		t.Fatalf("applyBacioChannel: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "BACIO_ENV") {
		t.Fatalf("BACI-63 regression: .mcp.json must not bake BACIO_ENV; got %s", data)
	}
	if strings.Contains(string(data), `"env"`) {
		t.Fatalf("BACI-63 regression: .mcp.json should not carry an env block; got %s", data)
	}
}

// captureStdoutText swaps os.Stdout for a pipe over the duration of fn
// and returns what was written. Mirrors captureStderr in hook_test.go;
// kept distinct so a single test can capture both.
func captureStdoutText(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = orig })

	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()

	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}
