package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
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

// TestExtractTaskFieldsTaskCreate parses a real-shape TaskCreate
// PostToolUse payload (one captured from a Claude Code 2.1.143
// transcript) and asserts the hook pulls the right id/content/status
// triple out of it. Locks in the field-mapping contract documented on
// postToolUseInput.
func TestExtractTaskFieldsTaskCreate(t *testing.T) {
	raw := `{
		"session_id": "sess-1",
		"hook_event_name": "PostToolUse",
		"tool_name": "TaskCreate",
		"tool_input": {
			"subject": "Map desktop UI surfaces",
			"description": "scan desktop/frontend/src for screens",
			"activeForm": "Mapping desktop UI"
		},
		"tool_response": {
			"task": {"id": "7", "subject": "Map desktop UI surfaces"}
		}
	}`
	var in postToolUseInput
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	id, content, status, ok := extractTaskFields(&in)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if id != "7" || content != "Map desktop UI surfaces" || status != string(model.TodoPending) {
		t.Fatalf("got (id=%q, content=%q, status=%q), want (7, Map desktop UI surfaces, pending)", id, content, status)
	}
}

// TestExtractTaskFieldsTaskUpdate covers the update path: the
// taskId/status come from tool_input (the agent supplied them); the
// content stays "" so the upsert leaves the existing subject alone.
// The statusChange.to fallback is exercised in the next test.
func TestExtractTaskFieldsTaskUpdate(t *testing.T) {
	raw := `{
		"session_id": "sess-1",
		"hook_event_name": "PostToolUse",
		"tool_name": "TaskUpdate",
		"tool_input": {"taskId": "2", "status": "in_progress"},
		"tool_response": {
			"success": true,
			"taskId": "2",
			"statusChange": {"from": "pending", "to": "in_progress"}
		}
	}`
	var in postToolUseInput
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	id, content, status, ok := extractTaskFields(&in)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if id != "2" || content != "" || status != "in_progress" {
		t.Fatalf("got (id=%q, content=%q, status=%q), want (2, '', in_progress)", id, content, status)
	}
}

// TestExtractTaskFieldsTaskUpdateStatusFallback covers the defensive
// fallback: if a future Claude Code variant ever omits status from
// tool_input, the hook reads statusChange.to from tool_response
// instead. Today's payloads always carry both; this protects us
// from a quiet upstream change.
func TestExtractTaskFieldsTaskUpdateStatusFallback(t *testing.T) {
	raw := `{
		"session_id": "sess-1",
		"tool_name": "TaskUpdate",
		"tool_input": {"taskId": "3"},
		"tool_response": {
			"statusChange": {"to": "completed"}
		}
	}`
	var in postToolUseInput
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	id, _, status, ok := extractTaskFields(&in)
	if !ok || id != "3" || status != "completed" {
		t.Fatalf("got (id=%q, status=%q, ok=%v), want (3, completed, true)", id, status, ok)
	}
}

// TestExtractTaskFieldsSkipsUnknownTool returns ok=false for any
// tool_name we don't model — the hook handler then silently drops
// the event. Guards against a future matcher widening that surfaces
// e.g. TaskStop (background-bash stop, unrelated to the task list).
func TestExtractTaskFieldsSkipsUnknownTool(t *testing.T) {
	for _, name := range []string{"", "TaskStop", "TaskList", "Bash", "Read"} {
		in := postToolUseInput{ToolName: name}
		if _, _, _, ok := extractTaskFields(&in); ok {
			t.Fatalf("tool_name=%q: ok=true, want false", name)
		}
	}
}

// TestExtractTaskFieldsTaskCreateMissingId rejects a TaskCreate
// payload with no auto-assigned id in tool_response — there's
// nothing to key the row by, so subsequent TaskUpdates would have
// no anchor. The hook log-and-drops.
func TestExtractTaskFieldsTaskCreateMissingId(t *testing.T) {
	in := postToolUseInput{ToolName: "TaskCreate"}
	in.ToolInput.Subject = "Has subject but no id assigned"
	if _, _, _, ok := extractTaskFields(&in); ok {
		t.Fatal("expected ok=false when TaskCreate response lacks task.id")
	}
}

// TestHookPostToolUseDropsTaskCreateWithoutClaim covers the BACI-136
// log-and-drop path: a session with no open claim fires a TaskCreate;
// the hook short-circuits before the store call, the table stays
// empty, and stderr carries the documented one-liner.
func TestHookPostToolUseDropsTaskCreateWithoutClaim(t *testing.T) {
	dbPath, c, sess, _ := setupHookTestEnv(t)

	stderr := runHookPostToolUseWith(t, dbPath, `{
		"session_id": "`+sess.SessionID+`",
		"hook_event_name": "PostToolUse",
		"tool_name": "TaskCreate",
		"tool_input": {"subject": "First task"},
		"tool_response": {"task": {"id": "1", "subject": "First task"}}
	}`)

	if !strings.Contains(stderr, "no single open claim") {
		t.Fatalf("stderr missing 'no single open claim' one-liner: %q", stderr)
	}
	if !strings.Contains(stderr, sess.SessionID) {
		t.Fatalf("stderr missing session_id %q: %q", sess.SessionID, stderr)
	}
	rows, err := c.ListSessionTodos(context.Background(), sess.SessionID, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %d, want 0 (log-and-drop must not insert)", len(rows))
	}
}

// TestHookPostToolUseDropsTaskCreateWithoutDispatch covers BACI-136's
// dispatch-resolution log-and-drop: a session with an open claim but
// no active dispatch fires a TaskCreate; the hook short-circuits
// after dispatch lookup, the table stays empty, and stderr carries
// the documented one-liner.
func TestHookPostToolUseDropsTaskCreateWithoutDispatch(t *testing.T) {
	dbPath, c, sess, repo := setupHookTestEnv(t)

	ctx := context.Background()
	iss, err := c.CreateIssue(ctx, repo, inputs.IssueAddInput{Title: "ClaimedIssue"}, false)
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	if _, err := c.ClaimAgent(ctx, repo, inputs.AgentClaimInput{
		IssueKey:  iss.Key,
		SessionID: sess.SessionID,
		Prompt:    "implement",
	}, false); err != nil {
		t.Fatalf("claim: %v", err)
	}

	stderr := runHookPostToolUseWith(t, dbPath, `{
		"session_id": "`+sess.SessionID+`",
		"hook_event_name": "PostToolUse",
		"tool_name": "TaskCreate",
		"tool_input": {"subject": "First task"},
		"tool_response": {"task": {"id": "1", "subject": "First task"}}
	}`)

	if !strings.Contains(stderr, "no active dispatch") {
		t.Fatalf("stderr missing 'no active dispatch' one-liner: %q", stderr)
	}
	if !strings.Contains(stderr, iss.Key) {
		t.Fatalf("stderr missing issue_key %q: %q", iss.Key, stderr)
	}
	rows, err := c.ListSessionTodos(ctx, sess.SessionID, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %d, want 0 (log-and-drop must not insert)", len(rows))
	}
}

// setupHookTestEnv wires up the bits the post-tool-use hook needs to
// run end-to-end against a throwaway sqlite db: BACIO_AGENT_MODE on,
// opts.dbPath pointing at a temp DB, a repo row, and a registered
// session. Returns the dbPath, an open client (closed via t.Cleanup),
// the registered session, and the repo so callers can seed claims /
// dispatches if they need to.
func setupHookTestEnv(t *testing.T) (string, client.Client, *model.AgentSession, *model.Repo) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	t.Setenv("BACIO_AGENT_MODE", "1")
	t.Setenv("HOME", t.TempDir()) // keep client.Open away from ~/.bacio

	// Hook RunE reads opts.dbPath; patch it for the test duration.
	prev := opts.dbPath
	opts.dbPath = dbPath
	t.Cleanup(func() { opts.dbPath = prev })

	ctx := context.Background()
	c, err := client.Open(ctx, client.Options{DBPath: dbPath, Actor: "tester"})
	if err != nil {
		t.Fatalf("open client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	repo, _, err := c.EnsureRepo(ctx, &git.Info{Root: t.TempDir(), Name: "proj"})
	if err != nil {
		t.Fatalf("ensure repo: %v", err)
	}
	sess, err := c.RegisterAgent(ctx, repo, inputs.AgentRegisterInput{
		SessionID: "f4cef4ce-f4ce-f4ce-f4ce-f4cef4cef4ce",
		Actor:     "tester",
	}, false)
	if err != nil {
		t.Fatalf("register session: %v", err)
	}
	return dbPath, c, sess, repo
}

// runHookPostToolUseWith feeds the given JSON payload to
// hookPostToolUseCmd's RunE via os.Stdin and returns whatever the hook
// writes to stderr. Wraps the stdin / stderr swap so the test bodies
// stay focused on the assertions.
func runHookPostToolUseWith(t *testing.T, _ , payload string) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	origStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = origStdin })
	if _, err := w.WriteString(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close payload writer: %v", err)
	}

	cmd := hookPostToolUseCmd()
	return captureStderr(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatalf("RunE: %v", err)
		}
	})
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

// captureTitleWriter swaps the package-level titleWriter for one that
// captures into the returned buffer. The cleanup restores the original
// so a failing test can't leak state into a later test.
//
// If openErr is non-nil, the substitute writer returns that error
// instead of a writer — used by tests that exercise the fail-open path
// (no /dev/tty) without touching the real filesystem.
func captureTitleWriter(t *testing.T, openErr error) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := titleWriter
	titleWriter = func() (io.WriteCloser, error) {
		if openErr != nil {
			return nil, openErr
		}
		return nopCloser{buf}, nil
	}
	t.Cleanup(func() { titleWriter = prev })
	return buf
}

// nopCloser wraps a bytes.Buffer so it satisfies io.WriteCloser without
// turning Close into anything that could fail a test.
type nopCloser struct{ *bytes.Buffer }

func (nopCloser) Close() error { return nil }

// TestWriteTerminalTitleEmitsOSC pins the wire format: the helper writes
// the standard `ESC ] 0 ; <title> BEL` OSC sequence to the injected
// writer — no trailing newline, no other bytes. Every mainstream
// terminal emulator on macOS / Linux honours this exact byte sequence.
func TestWriteTerminalTitleEmitsOSC(t *testing.T) {
	buf := captureTitleWriter(t, nil)
	if err := writeTerminalTitle("brave-koala@claude.shiny"); err != nil {
		t.Fatalf("writeTerminalTitle: %v", err)
	}
	want := "\x1b]0;brave-koala@claude.shiny\x07"
	if got := buf.String(); got != want {
		t.Fatalf("title bytes = %q, want %q", got, want)
	}
}

// TestWriteTerminalTitleSurfacesOpenError covers the fail-open invariant
// the hook caller relies on: a /dev/tty open failure (CI host, headless
// pipe) surfaces as a non-nil error from writeTerminalTitle and writes
// nothing. The hook's RunE then logs one stderr line and carries on
// without failing the agent's session.
func TestWriteTerminalTitleSurfacesOpenError(t *testing.T) {
	captureTitleWriter(t, errors.New("no /dev/tty here"))
	if err := writeTerminalTitle("brave-koala@claude.shiny"); err == nil {
		t.Fatal("expected error when titleWriter open fails")
	}
}

// TestHookSetTitleSkipsWithoutAgentMode is a focused version of the
// table-driven TestHookSubcommandsGatedByAgentMode for the new
// subcommand: BACIO_AGENT_MODE unset → one stderr skip line, RunE
// returns nil, the title writer is never invoked.
func TestHookSetTitleSkipsWithoutAgentMode(t *testing.T) {
	t.Setenv("BACIO_AGENT_MODE", "scratch")
	if err := os.Unsetenv("BACIO_AGENT_MODE"); err != nil {
		t.Fatalf("unset: %v", err)
	}
	buf := captureTitleWriter(t, nil)

	cmd := hookSetTitleCmd()
	stderr := captureStderr(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatalf("RunE: %v", err)
		}
	})
	if !strings.Contains(stderr, "bacio hook set-title") {
		t.Fatalf("stderr missing subcommand name: %q", stderr)
	}
	if buf.Len() != 0 {
		t.Fatalf("title writer should not run when gated; got %q", buf.String())
	}
}

// TestHookSetTitleSkipsWithoutSlug feeds the hook a payload whose
// session_id has no resolvable claude_pid → empty slug → the hook logs
// one stderr line and writes nothing. Exercises the "register fired but
// agents.json doesn't yet carry an entry" race documented in RunE.
func TestHookSetTitleSkipsWithoutSlug(t *testing.T) {
	// BACIO_AGENT_MODE on; loadHookContext needs a real git repo to walk
	// out of so it doesn't bail on git.Detect. Same shape as the
	// post-tool-use happy-path setup, minus the agents.json seed —
	// readAgentIdentity returns "" when the file is absent.
	tmp := initGitRepo(t)
	t.Setenv("BACIO_AGENT_MODE", "1")
	t.Setenv("HOME", t.TempDir())

	prev := opts.dbPath
	opts.dbPath = filepath.Join(t.TempDir(), "db.sqlite")
	t.Cleanup(func() { opts.dbPath = prev })

	buf := captureTitleWriter(t, nil)

	payload := `{"session_id": "f4cef4ce-f4ce-f4ce-f4ce-f4cef4cef4ce", "cwd": "` + tmp + `", "hook_event_name": "PostToolUse", "tool_name": "mcp__bacio__register"}`
	stderr := runHookSetTitleWith(t, payload)

	if !strings.Contains(stderr, "no agent slug") {
		t.Fatalf("stderr missing 'no agent slug' one-liner: %q", stderr)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no OSC write when slug is empty; got %q", buf.String())
	}
}

// TestHookPostToolUseHeartbeatBumpsLastSeen is the BACI-159 positive
// path. A registered session whose last_seen_at is comfortably stale
// (set to ~30 min ago via a direct store reach-in) must have it
// bumped to ~now by the heartbeat hook. The hook's RunE writes
// nothing to stdout — that's enforced separately, the test here pins
// the actual store mutation.
func TestHookPostToolUseHeartbeatBumpsLastSeen(t *testing.T) {
	// Reuses the post-tool-use happy-path scaffolding: temp DB, repo
	// row, registered session, opts.dbPath patched, BACIO_AGENT_MODE
	// on. The hook's loadHookContext will open a *second* client
	// against the same DB and reach the same session row.
	tmp := initGitRepo(t)
	dbPath, c, sess, _ := setupHookTestEnv(t)

	// Reach back into the DB to set last_seen_at = 30 min ago — the
	// register call above stamped it to ~now. We don't need raw SQL:
	// HeartbeatAgent itself updates last_seen_at to "now", and the
	// pre/post comparison below catches any forward motion regardless
	// of the starting value. But to make the test resilient against
	// a clock that drifts mere microseconds (the register and the
	// post-heartbeat call would otherwise sit inside the same
	// millisecond on fast hardware), stamp a known-stale value first.
	ctx := context.Background()
	view, err := c.ShowAgentSession(ctx, sess.SessionID)
	if err != nil {
		t.Fatalf("show session: %v", err)
	}
	before := view.Session.LastSeenAt
	// SQLite stamps last_seen_at via CURRENT_TIMESTAMP which is
	// second-resolution. Sleep a full second so the post-heartbeat
	// value is unambiguously after `before` — cheaper than reach-in
	// clock injection for one assertion.
	time.Sleep(1100 * time.Millisecond)

	payload := `{"session_id": "` + sess.SessionID + `", "cwd": "` + tmp + `", "hook_event_name": "PostToolUse", "tool_name": "Bash"}`
	stderr, stdout := runHookPostToolUseHeartbeatWith(t, payload)

	if stdout != "" {
		// Critical: Claude Code merges PostToolUse stdout into the
		// supervisor's context. Any byte leaked here lands in the
		// transcript — make the regression loud.
		t.Fatalf("heartbeat hook must be stdout-silent, got %q", stdout)
	}
	// stderr is allowed to carry the env-resolver debug line (worktree
	// manifest detection) and the no-claude-ancestor warning under
	// `go test`. Just make sure no error from heartbeatOrRegister
	// leaked through.
	if strings.Contains(stderr, "register fallback") {
		t.Fatalf("unexpected register-fallback error in stderr: %q", stderr)
	}

	view, err = c.ShowAgentSession(ctx, sess.SessionID)
	if err != nil {
		t.Fatalf("show session post-heartbeat: %v", err)
	}
	if !view.Session.LastSeenAt.After(before) {
		t.Fatalf("last_seen_at = %v, want > %v (heartbeat did not bump)",
			view.Session.LastSeenAt, before)
	}
	// Use dbPath so the unused-import linter stays happy on the helper
	// return value.
	_ = dbPath
}

// TestHookPostToolUseHeartbeatSkipsWhenAgentModeDisabled mirrors the
// BACI-48 gate at the heartbeat-hook layer: BACIO_AGENT_MODE unset →
// one stderr skip line, RunE returns nil, the store is never
// touched. This is the canonical "human at the terminal" path and
// must stay a no-op so a non-agent shell doesn't pay the per-tool
// DB-write cost.
func TestHookPostToolUseHeartbeatSkipsWhenAgentModeDisabled(t *testing.T) {
	t.Setenv("BACIO_AGENT_MODE", "scratch")
	if err := os.Unsetenv("BACIO_AGENT_MODE"); err != nil {
		t.Fatalf("unset: %v", err)
	}

	cmd := hookPostToolUseHeartbeatCmd()
	stderr := captureStderr(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatalf("RunE: %v", err)
		}
	})
	if !strings.Contains(stderr, "bacio hook post-tool-use-heartbeat") {
		t.Fatalf("stderr missing subcommand name: %q", stderr)
	}
	if !strings.Contains(stderr, "BACIO_AGENT_MODE not set") {
		t.Fatalf("stderr missing env-var name: %q", stderr)
	}
}

// runHookPostToolUseHeartbeatWith feeds the given JSON payload to
// hookPostToolUseHeartbeatCmd's RunE via os.Stdin and returns
// whatever the hook writes to stderr AND stdout. Stdout discipline
// is load-bearing for this hook (Claude Code merges PostToolUse
// stdout into the supervisor's context), so the test surface
// captures both streams.
func runHookPostToolUseHeartbeatWith(t *testing.T, payload string) (string, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	origStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = origStdin })
	if _, err := w.WriteString(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close payload writer: %v", err)
	}
	cmd := hookPostToolUseHeartbeatCmd()

	// Capture stdout in parallel with stderr via a pipe pair, same
	// shape as captureStderr.
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = outW
	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(outR)
		done <- string(data)
	}()

	stderr := captureStderr(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			_ = outW.Close()
			os.Stdout = origStdout
			t.Fatalf("RunE: %v", err)
		}
	})
	_ = outW.Close()
	os.Stdout = origStdout
	stdout := <-done
	return stderr, stdout
}

// runHookSetTitleWith feeds the given JSON payload to hookSetTitleCmd's
// RunE via os.Stdin and returns whatever the hook writes to stderr.
// Mirrors runHookPostToolUseWith so the test bodies stay focused on
// the assertions.
func runHookSetTitleWith(t *testing.T, payload string) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	origStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = origStdin })
	if _, err := w.WriteString(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close payload writer: %v", err)
	}
	cmd := hookSetTitleCmd()
	return captureStderr(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatalf("RunE: %v", err)
		}
	})
}

