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

	// TaskCreate "1" → inserts at position 0 with status=pending,
	// stamped with MINI-1.
	if err := s.UpsertSessionTodoFromTask("todos-1", "1", "MINI-1", "Read the brief", model.TodoPending, nil); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	// TaskCreate "2" → inserts at position 1
	if err := s.UpsertSessionTodoFromTask("todos-1", "2", "MINI-1", "Write the plan", model.TodoPending, nil); err != nil {
		t.Fatalf("create 2: %v", err)
	}
	// TaskUpdate "1" → flip status to completed, preserve position 0
	if err := s.UpsertSessionTodoFromTask("todos-1", "1", "MINI-1", "", model.TodoCompleted, nil); err != nil {
		t.Fatalf("update 1: %v", err)
	}
	// TaskUpdate "2" → flip status to in_progress
	if err := s.UpsertSessionTodoFromTask("todos-1", "2", "MINI-1", "", model.TodoInProgress, nil); err != nil {
		t.Fatalf("update 2: %v", err)
	}

	got, err := s.ListSessionTodos("todos-1", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Position != 0 || got[0].TaskID != "1" || got[0].Status != model.TodoCompleted || got[0].Content != "Read the brief" || got[0].IssueKey != "MINI-1" {
		t.Fatalf("row 0 = %+v, want pos=0 task=1 completed/Read the brief/MINI-1", got[0])
	}
	if got[1].Position != 1 || got[1].TaskID != "2" || got[1].Status != model.TodoInProgress || got[1].Content != "Write the plan" || got[1].IssueKey != "MINI-1" {
		t.Fatalf("row 1 = %+v, want pos=1 task=2 in_progress/Write the plan/MINI-1", got[1])
	}
}

// TestUpsertSessionTodoFromTaskUpdatePreservesIssueKey locks in the
// BACI-62 rule that TaskUpdate keeps the row's original issue_key
// regardless of whatever issueKey the caller passes — the row belongs
// with the job that created it, even if the agent has since flipped
// to a new claim.
func TestUpsertSessionTodoFromTaskUpdatePreservesIssueKey(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "todos-reflip", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := s.UpsertSessionTodoFromTask("todos-reflip", "1", "MINI-1", "first job", model.TodoPending, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Caller now claims a different issue and updates the same task_id.
	// The update path ignores the new issueKey and keeps MINI-1.
	if err := s.UpsertSessionTodoFromTask("todos-reflip", "1", "MINI-2", "", model.TodoCompleted, nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := s.ListSessionTodos("todos-reflip", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].IssueKey != "MINI-1" {
		t.Fatalf("IssueKey = %q, want MINI-1 (update must not re-stamp)", got[0].IssueKey)
	}
	if got[0].Status != model.TodoCompleted {
		t.Fatalf("Status = %q, want completed", got[0].Status)
	}
}

// TestUpsertSessionTodoFromTaskRejectsEmptyIssueKey locks in the
// BACI-136 contract: an insert with an empty issueKey is rejected at
// the store boundary, and the table stays untouched. The hook short-
// circuits to log-and-drop before this check fires in production —
// but the belt is here either way for any future caller that bypasses
// the hook.
func TestUpsertSessionTodoFromTaskRejectsEmptyIssueKey(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "todos-empty-issue", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	err := s.UpsertSessionTodoFromTask("todos-empty-issue", "1", "", "no claim resolved", model.TodoPending, nil)
	if err == nil || !strings.Contains(err.Error(), "issue_key is required") {
		t.Fatalf("err = %v, want issue_key is required", err)
	}
	got, listErr := s.ListSessionTodos("todos-empty-issue", "")
	if listErr != nil {
		t.Fatalf("list: %v", listErr)
	}
	if len(got) != 0 {
		t.Fatalf("rows = %d, want 0 (reject must leave the table empty)", len(got))
	}
}

// TestUpsertSessionTodoFromTaskUpdateAcceptsEmptyIssueKey locks in
// the BACI-136 "insert-only" scope of the new reject: a TaskUpdate-
// shape call (existing row + content="") with issueKey="" still
// succeeds, preserving the row's original issue_key. This is the
// behaviour the hook depends on so a TaskUpdate fired after the
// agent's claim window closed still lands cleanly.
func TestUpsertSessionTodoFromTaskUpdateAcceptsEmptyIssueKey(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "todos-update-empty", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := s.UpsertSessionTodoFromTask("todos-update-empty", "1", "MINI-1", "seeded", model.TodoPending, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Update with issueKey="" must succeed — the update branch ignores
	// the supplied issueKey and keeps MINI-1.
	if err := s.UpsertSessionTodoFromTask("todos-update-empty", "1", "", "", model.TodoCompleted, nil); err != nil {
		t.Fatalf("update with empty issueKey: %v", err)
	}
	got, err := s.ListSessionTodos("todos-update-empty", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].IssueKey != "MINI-1" {
		t.Fatalf("got = %+v, want one row scoped to MINI-1", got)
	}
	if got[0].Status != model.TodoCompleted {
		t.Fatalf("Status = %q, want completed", got[0].Status)
	}
}

// TestUpsertSessionTodoFromTaskCapEnforced locks in the 200-row
// guardrail on the insert path. The cap applies only to TaskCreate
// (a TaskUpdate to an existing row never grows the count); attempting
// to insert beyond the cap returns an error and leaves the table
// untouched. The cap is per-session, not per-(session, issue) — a
// session juggling many small jobs hits it on raw row count.
func TestUpsertSessionTodoFromTaskCapEnforced(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "todos-cap", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	for i := 0; i < model.MaxSessionTodos; i++ {
		if err := s.UpsertSessionTodoFromTask("todos-cap", taskIDFor(i), "MINI-1", "x", model.TodoPending, nil); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	err := s.UpsertSessionTodoFromTask("todos-cap", taskIDFor(model.MaxSessionTodos), "MINI-2", "overflow", model.TodoPending, nil)
	if err == nil {
		t.Fatalf("expected over-cap insert to be rejected")
	}
	if !strings.Contains(err.Error(), "too many todos") {
		t.Fatalf("err = %v, want too-many-todos message", err)
	}
	got, err := s.ListSessionTodos("todos-cap", "")
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
	err := s.UpsertSessionTodoFromTask("never-registered", "1", "MINI-1", "x", model.TodoPending, nil)
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
	err := s.UpsertSessionTodoFromTask("todos-noid", "", "MINI-1", "x", model.TodoPending, nil)
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
	if err := s.UpsertSessionTodoFromTask("todos-cascade", "1", "MINI-1", "doomed", model.TodoPending, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// End the session, then artificially age its ended_at past the
	// retention window so the prune sweep collects it.
	if _, _, _, _, err := s.EndAgentSession("todos-cascade", string(model.EndReasonStop), model.StateInProgress, DispatchCascadeCancel); err != nil {
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

// TestListTodosBySessionsBulk covers the back-compat bulk-read shape
// used by the REST `?issue_key=` unset path — one query per refresh,
// results keyed by session PK. The TaskID and IssueKey round-trip
// through the bulk read.
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
	if err := s.UpsertSessionTodoFromTask("bulk-a", "1", "MINI-1", "a0", model.TodoCompleted, nil); err != nil {
		t.Fatalf("create a0: %v", err)
	}
	if err := s.UpsertSessionTodoFromTask("bulk-a", "2", "MINI-1", "a1", model.TodoInProgress, nil); err != nil {
		t.Fatalf("create a1: %v", err)
	}
	if err := s.UpsertSessionTodoFromTask("bulk-b", "1", "MINI-2", "b0", model.TodoPending, nil); err != nil {
		t.Fatalf("create b0: %v", err)
	}

	got, err := s.ListTodosBySessions([]string{"bulk-a", "bulk-b", "absent"})
	if err != nil {
		t.Fatalf("bulk: %v", err)
	}
	if len(got[a.ID]) != 2 || got[a.ID][0].Content != "a0" || got[a.ID][0].TaskID != "1" || got[a.ID][0].IssueKey != "MINI-1" || got[a.ID][1].Content != "a1" || got[a.ID][1].TaskID != "2" {
		t.Fatalf("bulk[a] = %+v", got[a.ID])
	}
	if len(got[b.ID]) != 1 || got[b.ID][0].Content != "b0" || got[b.ID][0].TaskID != "1" || got[b.ID][0].IssueKey != "MINI-2" {
		t.Fatalf("bulk[b] = %+v", got[b.ID])
	}
	if _, present := got[0]; present {
		t.Fatalf("absent session id should be missing from result map")
	}
}

// TestListTodosBySessionsAndIssue covers the BACI-62 per-(session,
// issue) bulk reader: one session may have rows under multiple
// issue keys; the assembler only flows the asked-for pair's rows
// onto a card, and rows tied to a different issue stay invisible.
func TestListTodosBySessionsAndIssue(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	a, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "pair-a", RepoID: repo.ID, Actor: "agent-claude",
	})
	if err != nil {
		t.Fatalf("register a: %v", err)
	}
	b, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "pair-b", RepoID: repo.ID, Actor: "agent-claude",
	})
	if err != nil {
		t.Fatalf("register b: %v", err)
	}
	// pair-a worked MINI-1 then MINI-2; pair-b worked only MINI-1.
	if err := s.UpsertSessionTodoFromTask("pair-a", "a1", "MINI-1", "a/mini-1/first", model.TodoCompleted, nil); err != nil {
		t.Fatalf("a1: %v", err)
	}
	if err := s.UpsertSessionTodoFromTask("pair-a", "a2", "MINI-1", "a/mini-1/second", model.TodoCompleted, nil); err != nil {
		t.Fatalf("a2: %v", err)
	}
	if err := s.UpsertSessionTodoFromTask("pair-a", "a3", "MINI-2", "a/mini-2/first", model.TodoInProgress, nil); err != nil {
		t.Fatalf("a3: %v", err)
	}
	if err := s.UpsertSessionTodoFromTask("pair-b", "b1", "MINI-1", "b/mini-1/only", model.TodoPending, nil); err != nil {
		t.Fatalf("b1: %v", err)
	}
	// Ask for: (pair-a, MINI-2) and (pair-b, MINI-1). pair-a's MINI-1
	// rows must NOT appear; pair-b's only row must come back.
	got, err := s.ListTodosBySessionsAndIssue([]SessionIssuePair{
		{SessionID: "pair-a", IssueKey: "MINI-2"},
		{SessionID: "pair-b", IssueKey: "MINI-1"},
	})
	if err != nil {
		t.Fatalf("pair-keyed bulk: %v", err)
	}
	if len(got[a.ID]) != 1 || got[a.ID][0].Content != "a/mini-2/first" {
		t.Fatalf("pair-a@MINI-2 = %+v, want one row a/mini-2/first", got[a.ID])
	}
	if len(got[b.ID]) != 1 || got[b.ID][0].Content != "b/mini-1/only" {
		t.Fatalf("pair-b@MINI-1 = %+v, want one row b/mini-1/only", got[b.ID])
	}
}

// TestUpsertSessionTodoFromTaskStampsDispatchID covers BACI-132's
// per-dispatch scope on the insert path: a non-nil dispatchID is
// stamped on the new row and round-trips through both the unfiltered
// list and the triple-keyed bulk read.
func TestUpsertSessionTodoFromTaskStampsDispatchID(t *testing.T) {
	s, repo, issue := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "todos-dispatch", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	dispatchID := seedDispatch(t, s, repo.ID, &issue.ID, "")

	if err := s.UpsertSessionTodoFromTask("todos-dispatch", "1", issue.Key, "stamped", model.TodoPending, &dispatchID); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.ListSessionTodos("todos-dispatch", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].DispatchID == nil || *got[0].DispatchID != dispatchID {
		t.Fatalf("DispatchID = %v, want %d", got[0].DispatchID, dispatchID)
	}
}

// TestUpsertSessionTodoFromTaskUpdatePreservesDispatchID locks in
// the BACI-132 rule that TaskUpdate keeps the row's original
// dispatch_id, regardless of which dispatch the caller passes —
// mirroring the BACI-62 issue_key behaviour.
func TestUpsertSessionTodoFromTaskUpdatePreservesDispatchID(t *testing.T) {
	s, repo, issue := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "todos-disp-update", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	dispatchA := seedDispatch(t, s, repo.ID, &issue.ID, "")
	dispatchB := seedDispatch(t, s, repo.ID, &issue.ID, "")

	if err := s.UpsertSessionTodoFromTask("todos-disp-update", "1", issue.Key, "row", model.TodoPending, &dispatchA); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Update with dispatchB — store must keep dispatchA.
	if err := s.UpsertSessionTodoFromTask("todos-disp-update", "1", issue.Key, "", model.TodoCompleted, &dispatchB); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := s.ListSessionTodos("todos-disp-update", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].DispatchID == nil || *got[0].DispatchID != dispatchA {
		t.Fatalf("DispatchID after update = %v, want %d (must not re-stamp)", got[0].DispatchID, dispatchA)
	}
	if got[0].Status != model.TodoCompleted {
		t.Fatalf("Status = %q, want completed", got[0].Status)
	}
}

// TestUpsertSessionTodoFromTaskRejectsDispatchWithoutIssue is the
// defensive belt: a non-nil dispatchID paired with an empty issueKey
// is a hook-bug shape and the store rejects it loud. The hook never
// produces this pairing in practice (it nullifies dispatchID when
// dropping to the orphan bucket) — this lock keeps a future
// refactor from quietly inserting bad rows.
func TestUpsertSessionTodoFromTaskRejectsDispatchWithoutIssue(t *testing.T) {
	s, repo, issue := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "todos-disp-no-issue", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	dispatchID := seedDispatch(t, s, repo.ID, &issue.ID, "")
	err := s.UpsertSessionTodoFromTask("todos-disp-no-issue", "1", "", "bad", model.TodoPending, &dispatchID)
	if err == nil || !strings.Contains(err.Error(), "dispatch_id requires issue_key") {
		t.Fatalf("err = %v, want dispatch_id requires issue_key", err)
	}
}

// TestListTodosBySessionsAndIssueFiltersByDispatchID covers the
// BACI-132 triple-key bulk read: two dispatches on one (session,
// issue), each with their own rows, return only the dispatch
// asked for.
func TestListTodosBySessionsAndIssueFiltersByDispatchID(t *testing.T) {
	s, repo, issue := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "todos-two-disp", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	dispatchA := seedDispatch(t, s, repo.ID, &issue.ID, "")
	dispatchB := seedDispatch(t, s, repo.ID, &issue.ID, "")

	if err := s.UpsertSessionTodoFromTask("todos-two-disp", "a1", issue.Key, "a/first", model.TodoCompleted, &dispatchA); err != nil {
		t.Fatalf("a1: %v", err)
	}
	if err := s.UpsertSessionTodoFromTask("todos-two-disp", "a2", issue.Key, "a/second", model.TodoCompleted, &dispatchA); err != nil {
		t.Fatalf("a2: %v", err)
	}
	if err := s.UpsertSessionTodoFromTask("todos-two-disp", "b1", issue.Key, "b/first", model.TodoPending, &dispatchB); err != nil {
		t.Fatalf("b1: %v", err)
	}

	gotA, err := s.ListTodosBySessionsAndIssue([]SessionIssuePair{{
		SessionID: "todos-two-disp", IssueKey: issue.Key, DispatchID: &dispatchA,
	}})
	if err != nil {
		t.Fatalf("triple bulk A: %v", err)
	}
	flatA := []model.SessionTodo(nil)
	for _, list := range gotA {
		flatA = append(flatA, list...)
	}
	if len(flatA) != 2 || flatA[0].Content != "a/first" || flatA[1].Content != "a/second" {
		t.Fatalf("dispatch A = %+v, want two rows a/first, a/second", flatA)
	}
	gotB, err := s.ListTodosBySessionsAndIssue([]SessionIssuePair{{
		SessionID: "todos-two-disp", IssueKey: issue.Key, DispatchID: &dispatchB,
	}})
	if err != nil {
		t.Fatalf("triple bulk B: %v", err)
	}
	flatB := []model.SessionTodo(nil)
	for _, list := range gotB {
		flatB = append(flatB, list...)
	}
	if len(flatB) != 1 || flatB[0].Content != "b/first" {
		t.Fatalf("dispatch B = %+v, want one row b/first", flatB)
	}
}

// TestListTodosBySessionsAndIssueIgnoresPreMigrationNULLRows covers
// the BACI-132 rule that a pre-migration row (dispatch_id IS NULL)
// falls out of the triple-keyed filter even though it still matches
// on (session, issue). The pair-keyed BACI-62 lookup (no dispatch
// filter) still sees it — back-compat for callers that don't care.
func TestListTodosBySessionsAndIssueIgnoresPreMigrationNULLRows(t *testing.T) {
	s, repo, issue := seedRepoAndIssue(t)
	sess, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "todos-legacy", RepoID: repo.ID, Actor: "agent-claude",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// Insert a legacy row directly — NULL dispatch_id.
	if _, err := s.DB.Exec(
		`INSERT INTO agent_session_todos (session_pk, position, content, status, task_id, issue_key, dispatch_id)
			VALUES (?, 0, 'legacy', 'pending', 'legacy-1', ?, NULL)`,
		sess.ID, issue.Key,
	); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	dispatchID := seedDispatch(t, s, repo.ID, &issue.ID, "")
	if err := s.UpsertSessionTodoFromTask("todos-legacy", "fresh-1", issue.Key, "fresh", model.TodoPending, &dispatchID); err != nil {
		t.Fatalf("fresh insert: %v", err)
	}

	// Triple-keyed read excludes the legacy row.
	got, err := s.ListTodosBySessionsAndIssue([]SessionIssuePair{{
		SessionID: "todos-legacy", IssueKey: issue.Key, DispatchID: &dispatchID,
	}})
	if err != nil {
		t.Fatalf("triple bulk: %v", err)
	}
	totalRows := 0
	for _, list := range got {
		totalRows += len(list)
	}
	if totalRows != 1 {
		t.Fatalf("triple total rows = %d, want 1 (legacy NULL row excluded)", totalRows)
	}

	// Pair-keyed read (no dispatch filter) still sees both.
	pair, err := s.ListTodosBySessionsAndIssue([]SessionIssuePair{{
		SessionID: "todos-legacy", IssueKey: issue.Key,
	}})
	if err != nil {
		t.Fatalf("pair bulk: %v", err)
	}
	pairRows := 0
	for _, list := range pair {
		pairRows += len(list)
	}
	if pairRows != 2 {
		t.Fatalf("pair total rows = %d, want 2 (back-compat shape includes NULL)", pairRows)
	}
}

// seedDispatch inserts one dispatch row targeting the given issue
// (the test helper passes the seeded issue id directly to avoid the
// (prefix, number) split GetIssueByKey wants) and returns the new
// row's id. Defaults to mode="implement" when blank.
func seedDispatch(t *testing.T, s *Store, repoID int64, issueID *int64, mode string) int64 {
	t.Helper()
	if mode == "" {
		mode = "implement"
	}
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:          repoID,
		TargetSessionID: "todos-dispatch-target",
		IssueID:         issueID,
		Mode:            model.DispatchMode(mode),
		Payload:         "stub",
		CreatedBy:       "test-seeder",
	})
	if err != nil {
		t.Fatalf("seedDispatch: %v", err)
	}
	return d.ID
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
