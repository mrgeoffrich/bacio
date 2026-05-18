package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestUpsertSessionTodoFromTaskRoundTrip covers the happy path: a
// session registers, the hook records a TaskCreate (insert at the
// next position with status=pending), then a TaskUpdate (preserve
// position, flip status). List reads back the latest state with
// task_id populated.
func TestUpsertSessionTodoFromTaskRoundTrip(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "todos-1", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	// TaskCreate "1" → inserts at position 0 with status=pending
	if err := s.UpsertSessionTodoFromTask("todos-1", "1", "Read the brief", model.TodoPending); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	// TaskCreate "2" → inserts at position 1
	if err := s.UpsertSessionTodoFromTask("todos-1", "2", "Write the plan", model.TodoPending); err != nil {
		t.Fatalf("create 2: %v", err)
	}
	// TaskUpdate "1" → flip status to completed, preserve position 0
	if err := s.UpsertSessionTodoFromTask("todos-1", "1", "", model.TodoCompleted); err != nil {
		t.Fatalf("update 1: %v", err)
	}
	// TaskUpdate "2" → flip status to in_progress
	if err := s.UpsertSessionTodoFromTask("todos-1", "2", "", model.TodoInProgress); err != nil {
		t.Fatalf("update 2: %v", err)
	}

	got, err := s.ListSessionTodos("todos-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Position != 0 || got[0].TaskID != "1" || got[0].Status != model.TodoCompleted || got[0].Content != "Read the brief" {
		t.Fatalf("row 0 = %+v, want pos=0 task=1 completed/Read the brief", got[0])
	}
	if got[1].Position != 1 || got[1].TaskID != "2" || got[1].Status != model.TodoInProgress || got[1].Content != "Write the plan" {
		t.Fatalf("row 1 = %+v, want pos=1 task=2 in_progress/Write the plan", got[1])
	}
}

// TestUpsertSessionTodoFromTaskCapEnforced locks in the 200-row
// guardrail on the insert path. The cap applies only to TaskCreate
// (a TaskUpdate to an existing row never grows the count); attempting
// to insert beyond the cap returns an error and leaves the table
// untouched.
func TestUpsertSessionTodoFromTaskCapEnforced(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "todos-cap", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	for i := 0; i < model.MaxSessionTodos; i++ {
		if err := s.UpsertSessionTodoFromTask("todos-cap", taskIDFor(i), "x", model.TodoPending); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	err := s.UpsertSessionTodoFromTask("todos-cap", taskIDFor(model.MaxSessionTodos), "overflow", model.TodoPending)
	if err == nil {
		t.Fatalf("expected over-cap insert to be rejected")
	}
	if !strings.Contains(err.Error(), "too many todos") {
		t.Fatalf("err = %v, want too-many-todos message", err)
	}
	got, err := s.ListSessionTodos("todos-cap")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != model.MaxSessionTodos {
		t.Fatalf("len(got) = %d, want %d (cap should not have grown)", len(got), model.MaxSessionTodos)
	}
}

// TestUpsertSessionTodoFromTaskUnknownSession returns ErrNotFound so
// the hook handler can log-and-drop rather than fall through to a
// SQL-error surface.
func TestUpsertSessionTodoFromTaskUnknownSession(t *testing.T) {
	s := newTestStore(t)
	err := s.UpsertSessionTodoFromTask("never-registered", "1", "x", model.TodoPending)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestUpsertSessionTodoFromTaskMissingTaskID rejects a call with no
// task_id — without it the upsert can't key future updates. The
// hook's extractTaskFields already guards this, but the store-side
// check is the belt to the hook's braces.
func TestUpsertSessionTodoFromTaskMissingTaskID(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "todos-noid", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	err := s.UpsertSessionTodoFromTask("todos-noid", "", "x", model.TodoPending)
	if err == nil || !strings.Contains(err.Error(), "task_id is required") {
		t.Fatalf("err = %v, want task_id is required", err)
	}
}

// TestSessionTodosCascadeOnSessionDelete proves that pruning an ended
// session via PruneEndedAgentSessions cascades through the FK to
// agent_session_todos — no extra prune sweep needed, like the agent_claims
// cascade.
func TestSessionTodosCascadeOnSessionDelete(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "todos-cascade", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := s.UpsertSessionTodoFromTask("todos-cascade", "1", "doomed", model.TodoPending); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// End the session, then artificially age its ended_at past the
	// retention window so the prune sweep collects it.
	if _, _, _, err := s.EndAgentSession("todos-cascade", string(model.EndReasonStop)); err != nil {
		t.Fatalf("end: %v", err)
	}
	if _, err := s.DB.Exec(
		`UPDATE agent_sessions SET ended_at = datetime('now', '-100 days') WHERE session_id = ?`,
		"todos-cascade",
	); err != nil {
		t.Fatalf("age: %v", err)
	}
	if _, err := s.PruneEndedAgentSessions(AgentSessionRetention); err != nil {
		t.Fatalf("prune: %v", err)
	}

	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM agent_session_todos`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("agent_session_todos has %d rows after prune, want 0", n)
	}
}

// TestListTodosBySessionsBulk covers the bulk-read shape used by the
// agent views — one query per refresh, results keyed by session PK.
// The TaskID round-trips through the bulk read.
func TestListTodosBySessionsBulk(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	a, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "bulk-a", RepoID: repo.ID, Actor: "agent-claude",
	})
	if err != nil {
		t.Fatalf("register a: %v", err)
	}
	b, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "bulk-b", RepoID: repo.ID, Actor: "agent-claude",
	})
	if err != nil {
		t.Fatalf("register b: %v", err)
	}
	if err := s.UpsertSessionTodoFromTask("bulk-a", "1", "a0", model.TodoCompleted); err != nil {
		t.Fatalf("create a0: %v", err)
	}
	if err := s.UpsertSessionTodoFromTask("bulk-a", "2", "a1", model.TodoInProgress); err != nil {
		t.Fatalf("create a1: %v", err)
	}
	if err := s.UpsertSessionTodoFromTask("bulk-b", "1", "b0", model.TodoPending); err != nil {
		t.Fatalf("create b0: %v", err)
	}

	got, err := s.ListTodosBySessions([]string{"bulk-a", "bulk-b", "absent"})
	if err != nil {
		t.Fatalf("bulk: %v", err)
	}
	if len(got[a.ID]) != 2 || got[a.ID][0].Content != "a0" || got[a.ID][0].TaskID != "1" || got[a.ID][1].Content != "a1" || got[a.ID][1].TaskID != "2" {
		t.Fatalf("bulk[a] = %+v", got[a.ID])
	}
	if len(got[b.ID]) != 1 || got[b.ID][0].Content != "b0" || got[b.ID][0].TaskID != "1" {
		t.Fatalf("bulk[b] = %+v", got[b.ID])
	}
	if _, present := got[0]; present {
		t.Fatalf("absent session id should be missing from result map")
	}
}

// taskIDFor turns an integer counter into a Claude Code-style
// task_id string ("1", "2", ...). Test helper only — production
// task_ids come from Claude Code's mint, not this function.
func taskIDFor(n int) string {
	// Avoid strconv import noise for a tiny helper — sprintf is plenty.
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}
