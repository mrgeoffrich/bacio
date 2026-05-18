package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestReplaceSessionTodosRoundTrip covers the happy path: register a
// session, mirror a todo list, read it back, replace it, confirm the
// swap (no leftover rows from the previous snapshot), then empty it.
func TestReplaceSessionTodosRoundTrip(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "todos-1", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	want := []model.SessionTodo{
		{Position: 0, Content: "Read the brief", Status: model.TodoCompleted},
		{Position: 1, Content: "Write the plan", Status: model.TodoInProgress},
		{Position: 2, Content: "Open a PR", Status: model.TodoPending},
	}
	if err := s.ReplaceSessionTodos("todos-1", want); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err := s.ListSessionTodos("todos-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Content != w.Content || got[i].Status != w.Status || got[i].Position != w.Position {
			t.Fatalf("row %d = %+v, want %+v", i, got[i], w)
		}
	}

	// Swap to a shorter list — verifies the DELETE-then-INSERT atomic
	// swap really discards the rows beyond the new length.
	swap := []model.SessionTodo{
		{Position: 0, Content: "Single item", Status: model.TodoInProgress},
	}
	if err := s.ReplaceSessionTodos("todos-1", swap); err != nil {
		t.Fatalf("swap: %v", err)
	}
	got, err = s.ListSessionTodos("todos-1")
	if err != nil {
		t.Fatalf("list after swap: %v", err)
	}
	if len(got) != 1 || got[0].Content != "Single item" {
		t.Fatalf("after swap got %+v", got)
	}

	// Clearing the list with an empty slice is the agent emptying its
	// plan — exercise that path explicitly.
	if err := s.ReplaceSessionTodos("todos-1", nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, err = s.ListSessionTodos("todos-1")
	if err != nil {
		t.Fatalf("list after clear: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("after clear got %d rows, want 0", len(got))
	}
}

// TestReplaceSessionTodosCapEnforced locks in the 200-row guardrail: an
// over-cap batch is rejected wholesale rather than truncated, and the
// previous snapshot survives.
func TestReplaceSessionTodosCapEnforced(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "todos-cap", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Seed one good snapshot first so we can prove it survives a
	// rejected over-cap follow-up.
	if err := s.ReplaceSessionTodos("todos-cap", []model.SessionTodo{
		{Position: 0, Content: "keeper", Status: model.TodoPending},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	big := make([]model.SessionTodo, model.MaxSessionTodos+1)
	for i := range big {
		big[i] = model.SessionTodo{Position: i, Content: "x", Status: model.TodoPending}
	}
	err := s.ReplaceSessionTodos("todos-cap", big)
	if err == nil {
		t.Fatalf("expected over-cap batch to be rejected")
	}
	if !strings.Contains(err.Error(), "too many todos") {
		t.Fatalf("err = %v, want too-many-todos message", err)
	}
	got, err := s.ListSessionTodos("todos-cap")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Content != "keeper" {
		t.Fatalf("seed snapshot was lost: %+v", got)
	}
}

// TestReplaceSessionTodosUnknownSession returns ErrNotFound so the
// hook handler can log-and-drop rather than fall through to a SQL-error
// surface.
func TestReplaceSessionTodosUnknownSession(t *testing.T) {
	s := newTestStore(t)
	err := s.ReplaceSessionTodos("never-registered", []model.SessionTodo{
		{Position: 0, Content: "x", Status: model.TodoPending},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
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
	if err := s.ReplaceSessionTodos("todos-cascade", []model.SessionTodo{
		{Position: 0, Content: "doomed", Status: model.TodoPending},
	}); err != nil {
		t.Fatalf("replace: %v", err)
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
	if err := s.ReplaceSessionTodos("bulk-a", []model.SessionTodo{
		{Position: 0, Content: "a0", Status: model.TodoCompleted},
		{Position: 1, Content: "a1", Status: model.TodoInProgress},
	}); err != nil {
		t.Fatalf("replace a: %v", err)
	}
	if err := s.ReplaceSessionTodos("bulk-b", []model.SessionTodo{
		{Position: 0, Content: "b0", Status: model.TodoPending},
	}); err != nil {
		t.Fatalf("replace b: %v", err)
	}

	got, err := s.ListTodosBySessions([]string{"bulk-a", "bulk-b", "absent"})
	if err != nil {
		t.Fatalf("bulk: %v", err)
	}
	if len(got[a.ID]) != 2 || got[a.ID][0].Content != "a0" || got[a.ID][1].Content != "a1" {
		t.Fatalf("bulk[a] = %+v", got[a.ID])
	}
	if len(got[b.ID]) != 1 || got[b.ID][0].Content != "b0" {
		t.Fatalf("bulk[b] = %+v", got[b.ID])
	}
	if _, present := got[0]; present {
		t.Fatalf("absent session id should be missing from result map")
	}
}
