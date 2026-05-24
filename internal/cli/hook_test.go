package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
