package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// seedRepoAndIssue is a 3-line scaffold for the agent tests: returns a
// store with one repo and one issue ready to claim against.
func seedRepoAndIssue(t *testing.T) (*Store, *model.Repo, *model.Issue) {
	t.Helper()
	s := newTestStore(t)
	repo, err := s.CreateRepo("AGNT", "agent-test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "stub", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	return s, repo, iss
}

// TestUpsertAgentSessionWorktreeSlug round-trips worktree_slug (BACI-305)
// and locks in the first-write-wins update: a later heartbeat that doesn't
// resolve a slug (empty) must not clobber an established one.
func TestUpsertAgentSessionWorktreeSlug(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	first, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-slug-1", RepoID: repo.ID, Actor: "agent-claude",
		WorktreeSlug: "agent-deadbeef1234",
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if first.WorktreeSlug != "agent-deadbeef1234" {
		t.Fatalf("worktree_slug = %q, want agent-deadbeef1234", first.WorktreeSlug)
	}
	// A heartbeat with no slug must preserve the established one.
	second, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-slug-1", RepoID: repo.ID, Actor: "agent-claude",
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if second.WorktreeSlug != "agent-deadbeef1234" {
		t.Fatalf("worktree_slug clobbered by empty heartbeat: got %q", second.WorktreeSlug)
	}
}

// TestLatestActiveSessionBySlug returns the newest live session for a
// slug, skips ended sessions, and is a no-op for an empty/unknown slug
// (BACI-305 correlation lookup).
func TestLatestActiveSessionBySlug(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)

	// Unknown / empty slug → nil, no error.
	if sess, err := s.LatestActiveSessionBySlug("nope"); err != nil || sess != nil {
		t.Fatalf("unknown slug: got (%v, %v), want (nil, nil)", sess, err)
	}
	if sess, err := s.LatestActiveSessionBySlug(""); err != nil || sess != nil {
		t.Fatalf("empty slug: got (%v, %v), want (nil, nil)", sess, err)
	}

	const slug = "agent-shared-slug"
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-older", RepoID: repo.ID, Actor: "agent-claude", WorktreeSlug: slug,
	}); err != nil {
		t.Fatalf("older session: %v", err)
	}
	newer, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-newer", RepoID: repo.ID, Actor: "agent-claude", WorktreeSlug: slug,
	})
	if err != nil {
		t.Fatalf("newer session: %v", err)
	}

	got, err := s.LatestActiveSessionBySlug(slug)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got == nil || got.SessionID != newer.SessionID {
		t.Fatalf("latest = %v, want newest live session %q", got, newer.SessionID)
	}

	// End the newer one — the older live session becomes the pick.
	if _, _, _, _, _, err := s.EndAgentSession("sess-newer", string(model.EndReasonStop), model.StateTodo, DispatchCascadeCancel); err != nil {
		t.Fatalf("end newer: %v", err)
	}
	got, err = s.LatestActiveSessionBySlug(slug)
	if err != nil {
		t.Fatalf("lookup after end: %v", err)
	}
	if got == nil || got.SessionID != "sess-older" {
		t.Fatalf("latest after end = %v, want sess-older (ended skipped)", got)
	}
}

// TestUpsertAgentSessionIdempotent locks in that a repeat register on
// the same session id refreshes mutable fields and bumps last_seen_at
// rather than inserting a second row.
func TestUpsertAgentSessionIdempotent(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	first, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-1", RepoID: repo.ID, Actor: "agent-claude",
		Model: "claude-sonnet-4-6",
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if first.Model != "claude-sonnet-4-6" {
		t.Fatalf("model = %q, want claude-sonnet-4-6", first.Model)
	}
	// Force a tick so last_seen_at can move.
	time.Sleep(1100 * time.Millisecond)
	second, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-1", RepoID: repo.ID, Actor: "agent-claude",
		Model: "claude-opus-4-7",
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected same id, got %d → %d", first.ID, second.ID)
	}
	if second.Model != "claude-opus-4-7" {
		t.Fatalf("model after upsert = %q, want claude-opus-4-7", second.Model)
	}
	if !second.LastSeenAt.After(first.LastSeenAt) {
		t.Fatalf("last_seen_at did not advance: %v → %v", first.LastSeenAt, second.LastSeenAt)
	}
}

// TestSetAndClearAgentSessionErrored locks in the BACI-296 errored-state
// write/clear pair: SetAgentSessionErrored stamps the three columns and
// surfaces an "errored" liveness, ClearAgentSessionError wipes them, and
// both are no-ops on a missing session.
func TestSetAndClearAgentSessionErrored(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-err", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Write the errored state.
	if err := s.SetAgentSessionErrored("sess-err", "server_error", "529 overloaded"); err != nil {
		t.Fatalf("set errored: %v", err)
	}
	got, err := s.GetAgentSession("sess-err")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ErroredAt == nil {
		t.Fatalf("errored_at not set")
	}
	if got.ErrorType != "server_error" || got.ErrorMessage != "529 overloaded" {
		t.Fatalf("errored fields = (%q, %q), want (server_error, 529 overloaded)", got.ErrorType, got.ErrorMessage)
	}
	if live := model.SessionLiveness(got, time.Now().UTC()); live != "errored" {
		t.Fatalf("liveness = %q, want errored", live)
	}

	// Idempotent re-write refreshes the fields.
	if err := s.SetAgentSessionErrored("sess-err", "rate_limit", "429"); err != nil {
		t.Fatalf("re-set errored: %v", err)
	}
	got, _ = s.GetAgentSession("sess-err")
	if got.ErrorType != "rate_limit" {
		t.Fatalf("error_type after re-set = %q, want rate_limit", got.ErrorType)
	}

	// Clear wipes the columns.
	if err := s.ClearAgentSessionError("sess-err"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, _ = s.GetAgentSession("sess-err")
	if got.ErroredAt != nil || got.ErrorType != "" || got.ErrorMessage != "" {
		t.Fatalf("clear left residue: erroredAt=%v type=%q msg=%q", got.ErroredAt, got.ErrorType, got.ErrorMessage)
	}

	// Missing-session calls are no-ops, not errors.
	if err := s.SetAgentSessionErrored("nope", "server_error", "x"); err != nil {
		t.Fatalf("set on missing session should be no-op, got %v", err)
	}
	if err := s.ClearAgentSessionError("nope"); err != nil {
		t.Fatalf("clear on missing session should be no-op, got %v", err)
	}
}

// TestMigrateAddsErroredColumns simulates a pre-BACI-296 DB by dropping
// the three errored-state columns from a fresh store and re-running
// migrate(): the columns must come back (idempotently) so an upgrading DB
// gains them without error.
func TestMigrateAddsErroredColumns(t *testing.T) {
	s := newTestStore(t)
	for _, col := range []string{"errored_at", "error_type", "error_message"} {
		if _, err := s.DB.Exec(`ALTER TABLE agent_sessions DROP COLUMN ` + col); err != nil {
			t.Fatalf("drop %s: %v", col, err)
		}
	}
	// Two passes — migrate must be idempotent.
	for pass := 1; pass <= 2; pass++ {
		if err := migrate(s.DB); err != nil {
			t.Fatalf("pass %d migrate: %v", pass, err)
		}
		for _, col := range []string{"errored_at", "error_type", "error_message"} {
			has, err := columnExists(s.DB, "agent_sessions", col)
			if err != nil {
				t.Fatalf("pass %d columnExists %s: %v", pass, col, err)
			}
			if !has {
				t.Fatalf("pass %d: agent_sessions.%s missing after migrate", pass, col)
			}
		}
	}
}

// TestEndAgentSessionReleasesClaims locks in that ending a session
// auto-releases every open claim it holds (so a /clear at the wrong
// moment doesn't leave dangling "this agent is working on X" rows).
func TestEndAgentSessionReleasesClaims(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-2", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("sess-2", iss.ID, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, _, _, _, _, err := s.EndAgentSession("sess-2", string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel); err != nil {
		t.Fatalf("end: %v", err)
	}

	// Look up the latest claim row for (session, issue) and check
	// released_at populated.
	var released sql.NullTime
	err := s.DB.QueryRow(`
		SELECT released_at FROM agent_claims c
		JOIN agent_sessions s ON s.id = c.session_pk
		WHERE s.session_id = ? AND c.issue_id = ?
		ORDER BY c.claimed_at DESC LIMIT 1`,
		"sess-2", iss.ID,
	).Scan(&released)
	if err != nil {
		t.Fatalf("query claim: %v", err)
	}
	if !released.Valid {
		t.Fatalf("released_at is NULL — end did not auto-release the claim")
	}
}

// TestAddAgentClaimRejectsEndedSession locks in that an agent can't
// claim new issues after it's called end.
func TestAddAgentClaimRejectsEndedSession(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-3", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, _, _, _, err := s.EndAgentSession("sess-3", string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel); err != nil {
		t.Fatalf("end: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("sess-3", iss.ID, ""); err == nil {
		t.Fatalf("expected AddAgentClaim to reject ended session, got nil")
	}
}

// TestPruneAgentSessionsKeepsActive locks in that pruning only deletes
// ended rows whose ended_at is older than retention; active sessions
// (ended_at IS NULL) and recently-ended sessions are kept.
func TestPruneAgentSessionsKeepsActive(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("AGNT", "prune-test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	// Active session — should survive any prune.
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "active", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register active: %v", err)
	}
	// Ended session with backdated ended_at past the retention window.
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "stale", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register stale: %v", err)
	}
	if _, _, _, _, _, err := s.EndAgentSession("stale", string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel); err != nil {
		t.Fatalf("end stale: %v", err)
	}
	// Backdate to two retention windows ago.
	old := time.Now().Add(-2 * AgentSessionRetention).UTC().Format("2006-01-02 15:04:05")
	if _, err := s.DB.Exec(`UPDATE agent_sessions SET ended_at = ? WHERE session_id = 'stale'`, old); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if err := pruneAgentSessions(s.DB, AgentSessionRetention); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if _, err := s.GetAgentSession("active"); err != nil {
		t.Fatalf("active session was pruned — should have survived: %v", err)
	}
	if _, err := s.GetAgentSession("stale"); err == nil {
		t.Fatalf("stale session survived prune — should have been deleted")
	}
}

// TestPruneEndedAgentSessionsCustomRetention locks in that the exported
// PruneEndedAgentSessions honours an arbitrary retention argument — the
// controller-UI live-list prune calls it with AgentSessionLiveListRetention
// (4h), not the 60-day default.
func TestPruneEndedAgentSessionsCustomRetention(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("AGNT", "live-prune", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	for _, id := range []string{"old", "fresh"} {
		if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
			SessionID: id, RepoID: repo.ID, Actor: "agent-claude",
		}); err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
		if _, _, _, _, _, err := s.EndAgentSession(id, string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel); err != nil {
			t.Fatalf("end %s: %v", id, err)
		}
	}
	// Backdate `old` past the 4h window; leave `fresh` ended a moment ago.
	past := time.Now().Add(-5 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	if _, err := s.DB.Exec(`UPDATE agent_sessions SET ended_at = ? WHERE session_id = 'old'`, past); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	n, err := s.PruneEndedAgentSessions(AgentSessionLiveListRetention)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("deleted = %d, want 1", n)
	}
	if _, err := s.GetAgentSession("old"); err == nil {
		t.Fatalf("`old` survived the live-list prune")
	}
	if _, err := s.GetAgentSession("fresh"); err != nil {
		t.Fatalf("`fresh` was pruned but should have survived: %v", err)
	}
}

// TestPruneEndedAgentSessionsCascadesClaims pins the FK contract:
// agent_claims rows for a pruned session must disappear via ON DELETE
// CASCADE so a future schema change to that FK can't silently leave
// orphan claim rows.
func TestPruneEndedAgentSessionsCascadesClaims(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "cascade", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("cascade", iss.ID, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, _, _, _, _, err := s.EndAgentSession("cascade", string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel); err != nil {
		t.Fatalf("end: %v", err)
	}
	past := time.Now().Add(-5 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	if _, err := s.DB.Exec(`UPDATE agent_sessions SET ended_at = ? WHERE session_id = 'cascade'`, past); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if _, err := s.PruneEndedAgentSessions(AgentSessionLiveListRetention); err != nil {
		t.Fatalf("prune: %v", err)
	}
	var claims int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM agent_claims WHERE issue_id = ?`, iss.ID).Scan(&claims); err != nil {
		t.Fatalf("count claims: %v", err)
	}
	if claims != 0 {
		t.Fatalf("orphan claim rows survived prune: %d", claims)
	}
}

// TestPruneEndedAgentSessionsLeavesDispatches pins the dispatch-survival
// decision: agent_dispatches.target_session_id is not a FK, so a pruned
// session's dispatches outlive it (cleaned by the long-window
// pruneDispatches). If someone "fixes" the schema by adding an FK, this
// test fails loudly so the decision can be revisited.
func TestPruneEndedAgentSessionsLeavesDispatches(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "dispatched", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := s.AddDispatch(AddDispatchIn{
		RepoID:          repo.ID,
		TargetSessionID: "dispatched",
		IssueID:         &iss.ID,
		Payload:         "look at it",
		CreatedBy:       "agent-claude",
	}); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	if _, _, _, _, _, err := s.EndAgentSession("dispatched", string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel); err != nil {
		t.Fatalf("end: %v", err)
	}
	past := time.Now().Add(-5 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	if _, err := s.DB.Exec(`UPDATE agent_sessions SET ended_at = ? WHERE session_id = 'dispatched'`, past); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if _, err := s.PruneEndedAgentSessions(AgentSessionLiveListRetention); err != nil {
		t.Fatalf("prune: %v", err)
	}
	var dispatches int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM agent_dispatches WHERE target_session_id = ?`, "dispatched").Scan(&dispatches); err != nil {
		t.Fatalf("count dispatches: %v", err)
	}
	if dispatches != 1 {
		t.Fatalf("dispatch did not survive session prune: have %d, want 1", dispatches)
	}
}

// TestValidateSessionIDRejectsWhitespace locks in the principle-#4 rule:
// leading/trailing whitespace is an error, not trimmed silently.
func TestValidateSessionIDRejectsWhitespace(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"leading space", " 092d8907-a5ed"},
		{"trailing newline", "092d8907-a5ed\n"},
		{"middle space", "092d 8907"},
		{"tab", "abc\tdef"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateSessionID(tc.input); err == nil {
				t.Fatalf("expected error for %q, got nil", tc.input)
			}
		})
	}
}

// TestValidateSessionIDRejectsPlaceholders locks in BACI-46: the
// register MCP tool used to accept a literal "$CLAUDE_CODE_SESSION_ID"
// (and worse) because the briefing carried it as an example. The
// validator now refuses obvious placeholder strings up front, so a
// confused agent's call surfaces a clear error rather than silently
// writing a poisoned agent_sessions row.
func TestValidateSessionIDRejectsPlaceholders(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"literal env var", "$CLAUDE_CODE_SESSION_ID"},
		{"braced env var", "${CLAUDE_CODE_SESSION_ID}"},
		{"lowercase env var", "$claude_code_session_id"},
		{"double-brace template", "{{session_id}}"},
		{"single-brace template", "{session_id}"},
		{"angle-bracket template", "<session_id>"},
		{"unlisted dollar prefix", "$foo"},
		{"stray brace", "abc{def"},
		{"stray angle", "abc<def"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateSessionID(tc.input); err == nil {
				t.Fatalf("expected error for %q, got nil", tc.input)
			} else if !strings.Contains(err.Error(), "placeholder") {
				t.Fatalf("error for %q does not mention placeholder: %v", tc.input, err)
			}
		})
	}
	// Positive control: a real UUID still passes.
	if _, err := ValidateSessionID("698f641f-4df1-4880-ab89-0ab2693c115a"); err != nil {
		t.Fatalf("real UUID rejected: %v", err)
	}
}

// TestValidateSessionUUID locks in BACI-100: the register entry points
// require a structurally valid UUID, so a fat-fingered retry (the
// observed bug — a UUID with a wrong-length hex group) is rejected
// before it can land a phantom session row. The shared ValidateSessionID
// is deliberately NOT tightened — this test guards the split.
func TestValidateSessionUUID(t *testing.T) {
	good := []string{
		"698f641f-4df1-4880-ab89-0ab2693c115a", // v4
		"019e4ce7-e4c1-78f1-b547-5349910c1b9d", // v7
		"23543E26-6339-4B8A-AFF2-D8EA2013F287", // uppercase hex
	}
	for _, s := range good {
		if _, err := ValidateSessionUUID(s); err != nil {
			t.Errorf("ValidateSessionUUID(%q) = %v, want nil", s, err)
		}
	}

	bad := []struct {
		name  string
		input string
	}{
		{"short third group", "23543e26-6339-4b8-aff2-d8ea2013f287"},
		{"long second group", "23543e26-63390-4b8a-aff2-d8ea2013f287"},
		{"non-hex char", "23543e26-6339-4b8g-aff2-d8ea2013f287"},
		{"missing group", "23543e26-6339-4b8a-d8ea2013f287"},
		{"not a uuid at all", "sess-abc"},
		{"empty", ""},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateSessionUUID(tc.input); err == nil {
				t.Fatalf("ValidateSessionUUID(%q) = nil, want error", tc.input)
			}
		})
	}

	// The BACI-46 placeholder guard still fires through the stricter
	// validator (placeholder rejected before the UUID parse).
	if _, err := ValidateSessionUUID("$CLAUDE_CODE_SESSION_ID"); err == nil {
		t.Fatal("ValidateSessionUUID accepted the env-var placeholder")
	} else if !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("placeholder error not surfaced: %v", err)
	}

	// The shared validator stays permissive — non-UUID ids still pass
	// it, so dispatch/todo/claim callers that only reference an id are
	// unaffected (the no-blast-radius guarantee).
	if _, err := ValidateSessionID("sess-abc"); err != nil {
		t.Fatalf("ValidateSessionID tightened unexpectedly: %v", err)
	}
}

// TestUpsertAgentSessionRequireUUID locks in the opt-in store flag:
// RequireUUID=true rejects a malformed session_id at the register
// boundary; the default (false) keeps accepting non-UUID ids so every
// existing caller is unchanged.
func TestUpsertAgentSessionRequireUUID(t *testing.T) {
	st, repo, _ := seedRepoAndIssue(t)

	if _, err := st.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID:   "23543e26-6339-4b8-aff2-d8ea2013f287",
		RepoID:      repo.ID,
		Actor:       "agent-x",
		RequireUUID: true,
	}); err == nil {
		t.Fatal("RequireUUID=true accepted a malformed session_id")
	}

	if _, err := st.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID:   "698f641f-4df1-4880-ab89-0ab2693c115a",
		RepoID:      repo.ID,
		Actor:       "agent-x",
		RequireUUID: true,
	}); err != nil {
		t.Fatalf("RequireUUID=true rejected a valid UUID: %v", err)
	}

	// Default (RequireUUID:false) still tolerates a non-UUID id.
	if _, err := st.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-legacy",
		RepoID:    repo.ID,
		Actor:     "agent-x",
	}); err != nil {
		t.Fatalf("RequireUUID=false rejected a non-UUID id: %v", err)
	}
}

// Ensure newTestStore is reachable (declared in issues_test.go); a
// no-op test guards against import-only regressions.
var _ = filepath.Join

// TestRapidClaimReleaseClaim locks in C1: rapidly claiming, releasing,
// and re-claiming the same issue within one wall-clock second must not
// fail on a UNIQUE constraint. The original (session_pk, issue_id,
// claimed_at) UNIQUE collided because SQLite's CURRENT_TIMESTAMP is
// 1-sec granular; the partial unique index on open claims fixes it.
func TestRapidClaimReleaseClaim(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "rapid", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Three claim/release cycles back-to-back — well within a single
	// SQLite-granular second on any plausible hardware.
	for i := 0; i < 3; i++ {
		if _, _, _, _, err := s.AddAgentClaim("rapid", iss.ID, ""); err != nil {
			t.Fatalf("claim cycle %d: %v", i, err)
		}
		if _, _, _, err := s.ReleaseAgentClaim("rapid", iss.ID, model.StateInReview); err != nil {
			t.Fatalf("release cycle %d: %v", i, err)
		}
	}
}

// TestAddAgentClaimIdempotent locks in that re-claiming the same
// (session, issue) returns the existing claim row with created=false
// instead of inserting a duplicate. The local client uses the bool to
// skip writing a redundant audit row.
func TestAddAgentClaimIdempotent(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "idem", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	first, created, _, _, err := s.AddAgentClaim("idem", iss.ID, "")
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !created {
		t.Fatalf("expected created=true for the first claim")
	}
	second, created, _, _, err := s.AddAgentClaim("idem", iss.ID, "")
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if created {
		t.Fatalf("expected created=false for the re-claim")
	}
	if second.ID != first.ID {
		t.Fatalf("re-claim returned a different row: %d → %d", first.ID, second.ID)
	}
}

// TestUpsertAgentSessionRejectsEndedSession locks in C2: re-registering
// against an already-ended session id surfaces a clear error from the
// store call itself, not from a post-fetch check.
func TestUpsertAgentSessionRejectsEndedSession(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "ended-then-reregister", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if _, _, _, _, _, err := s.EndAgentSession("ended-then-reregister", string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel); err != nil {
		t.Fatalf("end: %v", err)
	}
	_, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "ended-then-reregister", RepoID: repo.ID, Actor: "agent-claude",
	})
	if err == nil {
		t.Fatalf("expected upsert against ended session to error, got nil")
	}
}

// TestReleaseClaimErrorPaths locks in that ReleaseAgentClaim returns a
// clear error for the two failure modes: unknown session, and known
// session with no open claim on that issue.
func TestReleaseClaimErrorPaths(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "rel", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, _, err := s.ReleaseAgentClaim("rel", iss.ID, model.StateInReview); err == nil {
		t.Fatalf("expected ErrNotFound releasing a non-existent claim, got nil")
	}
	if _, _, _, err := s.ReleaseAgentClaim("does-not-exist", iss.ID, model.StateInReview); err == nil {
		t.Fatalf("expected error releasing from unknown session, got nil")
	}
}

// TestUpsertAgentRequireNewClash locks in the identity-clash semantic:
// UpsertAgent(name, requireNew=true) on a taken name returns
// ErrAgentNameTaken so the agent loop can detect the clash via
// errors.Is and regenerate its slug. requireNew=false reuses silently.
func TestUpsertAgentRequireNewClash(t *testing.T) {
	s := newTestStore(t)
	first, created, err := s.UpsertAgent("cheerful-otter@claude.shiny", true)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !created || first.Name != "cheerful-otter@claude.shiny" {
		t.Fatalf("expected created=true with the slug, got created=%v name=%q", created, first.Name)
	}
	_, _, err = s.UpsertAgent("cheerful-otter@claude.shiny", true)
	if !errors.Is(err, ErrAgentNameTaken) {
		t.Fatalf("expected ErrAgentNameTaken on second --new, got: %v", err)
	}
	again, created, err := s.UpsertAgent("cheerful-otter@claude.shiny", false)
	if err != nil {
		t.Fatalf("reuse without requireNew: %v", err)
	}
	if created {
		t.Fatalf("expected created=false on reuse, got true")
	}
	if again.ID != first.ID {
		t.Fatalf("reuse returned a different row: %d → %d", first.ID, again.ID)
	}
}

// TestSessionLinksToAgent locks in the join: registering a session
// with AgentID populated stores the FK, and the LEFT JOIN in
// GetAgentSession returns the agent name back through AgentName.
func TestSessionLinksToAgent(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	ag, _, err := s.UpsertAgent("quiet-falcon@claude.shiny", true)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-link", RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register session: %v", err)
	}
	got, err := s.GetAgentSession("sess-link")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.AgentID == nil || *got.AgentID != ag.ID {
		t.Fatalf("AgentID = %v, want %d", got.AgentID, ag.ID)
	}
	if got.AgentName != "quiet-falcon@claude.shiny" {
		t.Fatalf("AgentName = %q, want quiet-falcon@claude.shiny", got.AgentName)
	}
}

// TestSessionUpsertPreservesAgentID locks in that a heartbeat-style
// upsert with AgentID=nil doesn't clobber the previously-linked
// agent_id (COALESCE in the ON CONFLICT clause).
func TestSessionUpsertPreservesAgentID(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	ag, _, err := s.UpsertAgent("bold-lynx@claude.shiny", true)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-preserve", RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	// Second upsert with AgentID=nil (mimics heartbeat / no-identity caller).
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-preserve", RepoID: repo.ID, AgentID: nil, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("second register: %v", err)
	}
	got, err := s.GetAgentSession("sess-preserve")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.AgentID == nil || *got.AgentID != ag.ID {
		t.Fatalf("AgentID was clobbered: got %v, want %d", got.AgentID, ag.ID)
	}
}

// TestClaimPromptRoundTrip locks in that a prompt passed to AddAgentClaim
// round-trips through getAgentClaimByID / ListAgentClaims, and that a
// re-claim with a fresher prompt updates it in place while still
// reporting created=false (so the audit log isn't flooded).
func TestClaimPromptRoundTrip(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "prompt-sess", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	first, created, _, _, err := s.AddAgentClaim("prompt-sess", iss.ID, "do the thing")
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !created || first.Prompt != "do the thing" {
		t.Fatalf("first claim: created=%v prompt=%q, want created=true prompt=%q", created, first.Prompt, "do the thing")
	}
	// Re-claim with a fresher prompt — no-op claim, but prompt updates.
	second, created, _, _, err := s.AddAgentClaim("prompt-sess", iss.ID, "do the other thing")
	if err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	if created {
		t.Fatalf("expected created=false on re-claim")
	}
	if second.ID != first.ID || second.Prompt != "do the other thing" {
		t.Fatalf("re-claim: id %d→%d prompt=%q, want same id and updated prompt", first.ID, second.ID, second.Prompt)
	}
	// Re-claim with an empty prompt must NOT clear the stored one.
	third, _, _, _, err := s.AddAgentClaim("prompt-sess", iss.ID, "")
	if err != nil {
		t.Fatalf("re-claim empty: %v", err)
	}
	if third.Prompt != "do the other thing" {
		t.Fatalf("empty re-claim clobbered prompt: got %q", third.Prompt)
	}
}

// TestListClaimsForIssue locks in that the per-issue claim list returns
// every claim (open + released) newest-first, carrying the prompt and
// the agent identity slug behind each claiming session.
func TestListClaimsForIssue(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	ag, _, err := s.UpsertAgent("merry-jackal@claude.shiny", true)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "claims-a", RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "claims-b", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register b: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("claims-a", iss.ID, "first claim"); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	if _, _, _, err := s.ReleaseAgentClaim("claims-a", iss.ID, model.StateInReview); err != nil {
		t.Fatalf("release a: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("claims-b", iss.ID, "second claim"); err != nil {
		t.Fatalf("claim b: %v", err)
	}
	claims, err := s.ListClaimsForIssue(iss.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(claims) != 2 {
		t.Fatalf("got %d claims, want 2", len(claims))
	}
	// Newest first: claims-b's claim.
	if claims[0].SessionID != "claims-b" || claims[0].Prompt != "second claim" {
		t.Fatalf("claims[0] = %q/%q, want claims-b/second claim", claims[0].SessionID, claims[0].Prompt)
	}
	if claims[0].ReleasedAt != nil {
		t.Fatalf("claims[0] should be open")
	}
	if claims[1].SessionID != "claims-a" || claims[1].Prompt != "first claim" {
		t.Fatalf("claims[1] = %q/%q, want claims-a/first claim", claims[1].SessionID, claims[1].Prompt)
	}
	if claims[1].ReleasedAt == nil {
		t.Fatalf("claims[1] should be released")
	}
	if claims[1].AgentName != "merry-jackal@claude.shiny" {
		t.Fatalf("claims[1].AgentName = %q, want merry-jackal@claude.shiny", claims[1].AgentName)
	}
}

// TestOpenClaimsBySession locks in that the repo-wide open-claim lookup
// buckets claims by session PK, includes only open claims, and excludes
// claims held by ended sessions.
func TestOpenClaimsBySession(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	iss2, err := s.CreateIssue(repo.ID, nil, "stub 2", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create issue 2: %v", err)
	}
	busy, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "busy-sess", RepoID: repo.ID, Actor: "agent-claude",
	})
	if err != nil {
		t.Fatalf("register busy: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "ended-sess", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register ended: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("busy-sess", iss.ID, "p1"); err != nil {
		t.Fatalf("claim busy: %v", err)
	}
	// A released claim must not count.
	if _, _, _, _, err := s.AddAgentClaim("busy-sess", iss2.ID, "p2"); err != nil {
		t.Fatalf("claim busy 2: %v", err)
	}
	if _, _, _, err := s.ReleaseAgentClaim("busy-sess", iss2.ID, model.StateInReview); err != nil {
		t.Fatalf("release busy 2: %v", err)
	}
	// An ended session's claim must not count (EndAgentSession releases it).
	if _, _, _, _, err := s.AddAgentClaim("ended-sess", iss2.ID, "p3"); err != nil {
		t.Fatalf("claim ended: %v", err)
	}
	if _, _, _, _, _, err := s.EndAgentSession("ended-sess", string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel); err != nil {
		t.Fatalf("end: %v", err)
	}
	bySession, err := s.OpenClaimsBySession(repo.ID)
	if err != nil {
		t.Fatalf("open claims: %v", err)
	}
	if len(bySession) != 1 {
		t.Fatalf("got %d sessions with open claims, want 1", len(bySession))
	}
	open := bySession[busy.ID]
	if len(open) != 1 || open[0].IssueID != iss.ID {
		t.Fatalf("busy session open claims = %+v, want one claim on iss", open)
	}
	gotBusy, key := model.SessionBusy(open)
	if !gotBusy || key != open[0].IssueKey {
		t.Fatalf("SessionBusy = (%v, %q), want (true, %q)", gotBusy, key, open[0].IssueKey)
	}
}

// TestResolveAgentSessionPrefix locks in C6: `agent show` accepts a
// unique-prefix match so the truncated session id printed by
// `agent list` is copy-pasteable.
func TestResolveAgentSessionPrefix(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	for _, sid := range []string{
		"deadbeef-0001-0000-0000-000000000000",
		"feedface-0002-0000-0000-000000000000",
	} {
		if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
			SessionID: sid, RepoID: repo.ID, Actor: "agent-claude",
		}); err != nil {
			t.Fatalf("register %s: %v", sid, err)
		}
	}
	got, err := s.ResolveAgentSession("deadbeef")
	if err != nil {
		t.Fatalf("unique prefix should resolve: %v", err)
	}
	if got.SessionID != "deadbeef-0001-0000-0000-000000000000" {
		t.Fatalf("resolved to %q, want deadbeef-…", got.SessionID)
	}
	// Both share the empty prefix → ambiguous.
	if _, err := s.ResolveAgentSession(""); err == nil {
		t.Fatalf("empty prefix should error")
	}
	if _, err := s.ResolveAgentSession("missing-"); err == nil {
		t.Fatalf("no-match should error")
	}
}

// assigneeOf is a 1-liner for the lockstep tests: reads an issue's
// current assignee straight from the row.
func assigneeOf(t *testing.T, s *Store, issueID int64) string {
	t.Helper()
	iss, err := s.GetIssueByID(issueID)
	if err != nil {
		t.Fatalf("read issue: %v", err)
	}
	return iss.Assignee
}

// TestAddAgentClaimAssignsIssue locks in BACI-27: a fresh claim stamps
// issues.assignee with the claiming agent's identity slug, and the
// returned AssigneeChange describes the move. A session without a
// linked identity falls back to its actor.
func TestAddAgentClaimAssignsIssue(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	ag, _, err := s.UpsertAgent("brave-otter@claude.shiny", true)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "with-id", RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, created, change, _, err := s.AddAgentClaim("with-id", iss.ID, "")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !created {
		t.Fatalf("expected created=true")
	}
	if change == nil || !change.Changed() {
		t.Fatalf("expected a changed AssigneeChange, got %+v", change)
	}
	if change.Old != "" || change.New != "brave-otter@claude.shiny" {
		t.Fatalf("AssigneeChange = %q → %q, want \"\" → brave-otter@claude.shiny", change.Old, change.New)
	}
	if change.IssueKey != iss.Key {
		t.Fatalf("AssigneeChange.IssueKey = %q, want %q", change.IssueKey, iss.Key)
	}
	if got := assigneeOf(t, s, iss.ID); got != "brave-otter@claude.shiny" {
		t.Fatalf("issue assignee = %q, want brave-otter@claude.shiny", got)
	}

	// A session with no linked identity falls back to its actor.
	iss2, err := s.CreateIssue(repo.ID, nil, "stub 2", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create issue 2: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "no-id", RepoID: repo.ID, Actor: "manual-actor",
	}); err != nil {
		t.Fatalf("register no-id: %v", err)
	}
	if _, _, change, _, err := s.AddAgentClaim("no-id", iss2.ID, ""); err != nil {
		t.Fatalf("claim no-id: %v", err)
	} else if change.New != "manual-actor" {
		t.Fatalf("identity fallback = %q, want manual-actor", change.New)
	}
	if got := assigneeOf(t, s, iss2.ID); got != "manual-actor" {
		t.Fatalf("issue 2 assignee = %q, want manual-actor", got)
	}
}

// TestAddAgentClaimOverwritesAssignee locks in the last-claim-wins rule:
// claiming an issue overwrites even a pre-set (e.g. human) assignee.
func TestAddAgentClaimOverwritesAssignee(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	if err := s.SetIssueAssignee(iss.ID, "some-human"); err != nil {
		t.Fatalf("pre-assign: %v", err)
	}
	ag, _, err := s.UpsertAgent("keen-finch@claude.shiny", true)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "overwrite", RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, _, change, _, err := s.AddAgentClaim("overwrite", iss.ID, "")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if change.Old != "some-human" || change.New != "keen-finch@claude.shiny" {
		t.Fatalf("AssigneeChange = %q → %q, want some-human → keen-finch@claude.shiny", change.Old, change.New)
	}
	if got := assigneeOf(t, s, iss.ID); got != "keen-finch@claude.shiny" {
		t.Fatalf("issue assignee = %q, want keen-finch@claude.shiny", got)
	}
}

// TestReleaseAgentClaimClearsAssignee locks in that releasing the last
// open claim unassigns the issue — but a release that still leaves an
// open claim behind leaves the assignee alone.
func TestReleaseAgentClaimClearsAssignee(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	agA, _, err := s.UpsertAgent("pair-a@claude.shiny", true)
	if err != nil {
		t.Fatalf("create agent a: %v", err)
	}
	agB, _, err := s.UpsertAgent("pair-b@claude.shiny", true)
	if err != nil {
		t.Fatalf("create agent b: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-a", RepoID: repo.ID, AgentID: &agA.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-b", RepoID: repo.ID, AgentID: &agB.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register b: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("sess-a", iss.ID, ""); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("sess-b", iss.ID, ""); err != nil {
		t.Fatalf("claim b: %v", err)
	}
	// Last claim wins: assignee is B.
	if got := assigneeOf(t, s, iss.ID); got != "pair-b@claude.shiny" {
		t.Fatalf("after both claims, assignee = %q, want pair-b@claude.shiny", got)
	}
	// A releases — B still holds an open claim, so the assignee stays.
	_, change, _, err := s.ReleaseAgentClaim("sess-a", iss.ID, model.StateInReview)
	if err != nil {
		t.Fatalf("release a: %v", err)
	}
	if change.Changed() {
		t.Fatalf("release a should not have changed the assignee, got %+v", change)
	}
	if got := assigneeOf(t, s, iss.ID); got != "pair-b@claude.shiny" {
		t.Fatalf("after release a, assignee = %q, want pair-b@claude.shiny", got)
	}
	// B releases the last open claim — now the issue is unassigned.
	_, change, _, err = s.ReleaseAgentClaim("sess-b", iss.ID, model.StateInReview)
	if err != nil {
		t.Fatalf("release b: %v", err)
	}
	if !change.Changed() || change.New != "" {
		t.Fatalf("release b should have cleared the assignee, got %+v", change)
	}
	if got := assigneeOf(t, s, iss.ID); got != "" {
		t.Fatalf("after release b, assignee = %q, want empty", got)
	}
}

// TestReleaseAgentClaimKeepsForeignAssignee locks in the "don't clobber
// deliberate human action" guard: if a human reassigns the issue after
// the claim, releasing the claim leaves that assignee in place.
func TestReleaseAgentClaimKeepsForeignAssignee(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	ag, _, err := s.UpsertAgent("tidy-wren@claude.shiny", true)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "foreign", RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("foreign", iss.ID, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// A human reassigns the issue out from under the agent.
	if err := s.SetIssueAssignee(iss.ID, "a-human"); err != nil {
		t.Fatalf("human reassign: %v", err)
	}
	_, change, _, err := s.ReleaseAgentClaim("foreign", iss.ID, model.StateInReview)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if change.Changed() {
		t.Fatalf("release should not have touched the human-set assignee, got %+v", change)
	}
	if got := assigneeOf(t, s, iss.ID); got != "a-human" {
		t.Fatalf("assignee = %q, want a-human (preserved)", got)
	}
}

// TestEndAgentSessionUnassignsReleasedIssues locks in that EndAgentSession
// clears the assignee on each issue it auto-releases, and reports the
// change for the audit log.
func TestEndAgentSessionUnassignsReleasedIssues(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	iss2, err := s.CreateIssue(repo.ID, nil, "stub 2", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create issue 2: %v", err)
	}
	ag, _, err := s.UpsertAgent("busy-stoat@claude.shiny", true)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "ending", RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("ending", iss.ID, ""); err != nil {
		t.Fatalf("claim 1: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("ending", iss2.ID, ""); err != nil {
		t.Fatalf("claim 2: %v", err)
	}
	_, changes, _, _, _, err := s.EndAgentSession("ending", string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel)
	if err != nil {
		t.Fatalf("end: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("got %d assignee changes, want 2", len(changes))
	}
	for _, ch := range changes {
		if ch.Old != "busy-stoat@claude.shiny" || ch.New != "" {
			t.Fatalf("change = %q → %q, want busy-stoat@claude.shiny → \"\"", ch.Old, ch.New)
		}
	}
	if got := assigneeOf(t, s, iss.ID); got != "" {
		t.Fatalf("issue 1 assignee = %q, want empty", got)
	}
	if got := assigneeOf(t, s, iss2.ID); got != "" {
		t.Fatalf("issue 2 assignee = %q, want empty", got)
	}
}

// TestEndAgentSessionAbandonsOpenQuestions (BACI-253) locks in the
// in-tx flip: every still-open ask_user_question owned by the session
// lands as `abandoned` inside the same transaction the session ends in.
// Settled rows (answered / cancelled / abandoned) are untouched, and
// the count surfaced as the fifth return value matches the number of
// open rows the session owned at end time. Also covers the
// presumed_dead requeue path — the abandon fires regardless of cascade
// mode, because a session that's gone can't deliver to anyone.
func TestEndAgentSessionAbandonsOpenQuestions(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "parked", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register parked: %v", err)
	}

	// One open, one answered → only the open should flip.
	open1, err := s.AddSessionQuestion(AddSessionQuestionIn{
		SessionID: "parked", Payload: validSinglePayload(), AskedBy: "a",
	})
	if err != nil {
		t.Fatalf("add open1: %v", err)
	}
	answered, err := s.AddSessionQuestion(AddSessionQuestionIn{
		SessionID: "parked", Payload: validSinglePayload(), AskedBy: "a",
	})
	if err != nil {
		t.Fatalf("add answered: %v", err)
	}
	if _, err := s.AnswerSessionQuestion(answered.ID,
		model.QuestionAnswers{"Which approach should I take?": "Option A"}, "geoff"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	open2, err := s.AddSessionQuestion(AddSessionQuestionIn{
		SessionID: "parked", Payload: validMultiPayload(), AskedBy: "a",
	})
	if err != nil {
		t.Fatalf("add open2: %v", err)
	}

	_, _, _, _, abandoned, err := s.EndAgentSession("parked", string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel)
	if err != nil {
		t.Fatalf("end: %v", err)
	}
	if abandoned != 2 {
		t.Fatalf("abandoned = %d, want 2 (open1 + open2)", abandoned)
	}

	// open1 + open2 are now abandoned; the answered row is untouched.
	for _, q := range []*model.SessionQuestion{open1, open2} {
		got, err := s.GetSessionQuestion(q.ID)
		if err != nil {
			t.Fatalf("get %d: %v", q.ID, err)
		}
		if got.State != model.QuestionAbandoned {
			t.Fatalf("question %d state = %q, want abandoned", q.ID, got.State)
		}
	}
	settled, err := s.GetSessionQuestion(answered.ID)
	if err != nil {
		t.Fatalf("get answered: %v", err)
	}
	if settled.State != model.QuestionAnswered {
		t.Fatalf("answered state = %q, want answered (must not be touched)", settled.State)
	}

	// Second session with no open questions — abandoned count is 0 and
	// the end-session cascade still runs cleanly.
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "no-questions", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register no-questions: %v", err)
	}
	_, _, _, _, abandoned, err = s.EndAgentSession("no-questions", string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel)
	if err != nil {
		t.Fatalf("end (no questions): %v", err)
	}
	if abandoned != 0 {
		t.Fatalf("abandoned (no questions) = %d, want 0", abandoned)
	}

	// presumed_dead requeue path also abandons — the BACI-252 motivating
	// scenario: a reaper-driven end on a session blocked on an answer.
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "going-dark", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register going-dark: %v", err)
	}
	openDark, err := s.AddSessionQuestion(AddSessionQuestionIn{
		SessionID: "going-dark", Payload: validSinglePayload(), AskedBy: "a",
	})
	if err != nil {
		t.Fatalf("add open dark: %v", err)
	}
	_, _, _, _, abandoned, err = s.EndAgentSession("going-dark", string(model.EndReasonPresumedDead), model.StateInReview, DispatchCascadeRequeue)
	if err != nil {
		t.Fatalf("end (presumed_dead): %v", err)
	}
	if abandoned != 1 {
		t.Fatalf("abandoned (presumed_dead) = %d, want 1", abandoned)
	}
	got, err := s.GetSessionQuestion(openDark.ID)
	if err != nil {
		t.Fatalf("get dark: %v", err)
	}
	if got.State != model.QuestionAbandoned {
		t.Fatalf("presumed_dead question state = %q, want abandoned", got.State)
	}
}

// TestEndAgentSessionCancelsSessionTargetedDispatch (BACI-58 §B) locks
// in that an open dispatch targeted at the exact session is cancelled
// inside the EndAgentSession transaction, and that the
// CancelledDispatchInfo returned carries enough for the audit caller
// to write the agent.cancel row.
func TestEndAgentSessionCancelsSessionTargetedDispatch(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "sess-tgt", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:          repo.ID,
		TargetSessionID: "sess-tgt",
		IssueID:         &iss.ID,
		Mode:            model.DispatchModeShip,
		Payload:         "do it",
		CreatedBy:       "supervisor",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	_, _, _, cancelled, _, err := s.EndAgentSession("sess-tgt", string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel)
	if err != nil {
		t.Fatalf("end: %v", err)
	}
	if len(cancelled) != 1 {
		t.Fatalf("cancelled count = %d, want 1; rows = %+v", len(cancelled), cancelled)
	}
	got := cancelled[0]
	if got.ID != d.ID {
		t.Fatalf("cancelled ID = %d, want %d", got.ID, d.ID)
	}
	if got.RepoPrefix != repo.Prefix {
		t.Fatalf("RepoPrefix = %q, want %q", got.RepoPrefix, repo.Prefix)
	}
	if got.IssueKey != iss.Key {
		t.Fatalf("IssueKey = %q, want %q", got.IssueKey, iss.Key)
	}
	if got.TargetSessionID != "sess-tgt" {
		t.Fatalf("TargetSessionID = %q, want sess-tgt", got.TargetSessionID)
	}
	if got.Mode != string(model.DispatchModeShip) {
		t.Fatalf("Mode = %q, want ship", got.Mode)
	}
	// Status persisted as cancelled.
	got2, err := s.GetDispatch(d.ID)
	if err != nil {
		t.Fatalf("get dispatch: %v", err)
	}
	if got2.Status != model.DispatchCancelled {
		t.Fatalf("dispatch status after end = %q, want cancelled", got2.Status)
	}
}

// TestEndAgentSessionCancelsIdentityTargetedWhenLastLive (BACI-58 §B)
// locks in that an identity-targeted dispatch is cancelled when the
// session being ended is the identity's only live session — but a
// sibling alive session for the same identity keeps it alive.
func TestEndAgentSessionCancelsIdentityTargetedWhenLastLive(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	ag, _, err := s.UpsertAgent("lone-fox@claude.shiny", true)
	if err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "only-live", RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchModeShip,
		Payload:       "identity-targeted",
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add identity dispatch: %v", err)
	}
	_, _, _, cancelled, _, err := s.EndAgentSession("only-live", string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel)
	if err != nil {
		t.Fatalf("end: %v", err)
	}
	if len(cancelled) != 1 || cancelled[0].ID != d.ID {
		t.Fatalf("expected identity-targeted dispatch cancelled, got %+v", cancelled)
	}
}

// TestEndAgentSessionPreservesIdentityWhenSiblingAlive (BACI-58 §B) —
// pairing / review flows have two sessions sharing one identity;
// ending one must NOT cancel the identity-targeted dispatches the
// surviving sibling could still service.
func TestEndAgentSessionPreservesIdentityWhenSiblingAlive(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	ag, _, err := s.UpsertAgent("twin-lynx@claude.shiny", true)
	if err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "twin-a", RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("upsert twin-a: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "twin-b", RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("upsert twin-b: %v", err)
	}
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		Mode:          model.DispatchModeShip,
		Payload:       "pair-targeted",
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add pair dispatch: %v", err)
	}
	_, _, _, cancelled, _, err := s.EndAgentSession("twin-a", string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel)
	if err != nil {
		t.Fatalf("end twin-a: %v", err)
	}
	for _, info := range cancelled {
		if info.ID == d.ID {
			t.Fatalf("identity dispatch cancelled despite sibling-alive session: %+v", info)
		}
	}
	got, err := s.GetDispatch(d.ID)
	if err != nil {
		t.Fatalf("get dispatch: %v", err)
	}
	if got.Status == model.DispatchCancelled {
		t.Fatalf("dispatch cancelled despite sibling alive; status = %q", got.Status)
	}

	// Now end the second sibling — last live session, identity dispatch
	// should auto-cancel.
	_, _, _, cancelled, _, err = s.EndAgentSession("twin-b", string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel)
	if err != nil {
		t.Fatalf("end twin-b: %v", err)
	}
	if len(cancelled) != 1 || cancelled[0].ID != d.ID {
		t.Fatalf("end of last-live session should cancel identity dispatch, got %+v", cancelled)
	}
}

// TestEndAgentSessionClearsWaitingDispatch (BACI-58 §B / BACI-255) —
// when EndAgentSession cancels a session-targeted dispatch as part of
// the cascade, the row flips to status='cancelled', so
// WaitingDispatchForIssue stops returning it and the kanban spinner
// clears. Pre-BACI-255 this also tested a denormalised
// issues.waiting_for_claim 1→0 transition; that cache is gone, so the
// invariant is checked against the dispatch row directly.
func TestEndAgentSessionClearsWaitingDispatch(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "wait-sess", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := s.AddDispatch(AddDispatchIn{
		RepoID:          repo.ID,
		TargetSessionID: "wait-sess",
		IssueID:         &iss.ID,
		Payload:         "queue it",
		CreatedBy:       "supervisor",
	}); err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	// AddDispatch lands a queued row immediately visible through
	// WaitingDispatchForIssue — confirm the precondition before the
	// end-session call.
	pre, err := s.WaitingDispatchForIssue(repo.ID, iss.ID)
	if err != nil {
		t.Fatalf("WaitingDispatchForIssue before: %v", err)
	}
	if pre == nil {
		t.Fatal("WaitingDispatchForIssue = nil before end; AddDispatch should have left the row queryable")
	}
	if _, _, _, _, _, err := s.EndAgentSession("wait-sess", string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel); err != nil {
		t.Fatalf("end: %v", err)
	}
	post, err := s.WaitingDispatchForIssue(repo.ID, iss.ID)
	if err != nil {
		t.Fatalf("WaitingDispatchForIssue after: %v", err)
	}
	if post != nil {
		t.Fatalf("WaitingDispatchForIssue after end = %+v, want nil (the cascade-cancelled row should no longer be 'waiting')", post)
	}
}

// TestEndAgentSessionLeavesAckedAndCancelledAlone (BACI-58 §B) — only
// open (queued/pending/delivered) dispatches participate in the
// auto-cancel. Already-acked or already-cancelled rows are left alone.
func TestEndAgentSessionLeavesAckedAndCancelledAlone(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "settled", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	acked, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetSessionID: "settled",
		Payload: "ack me", CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add acked dispatch: %v", err)
	}
	if _, err := s.AckDispatch(acked.ID, "done"); err != nil {
		t.Fatalf("ack: %v", err)
	}
	cancelled, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, TargetSessionID: "settled",
		Payload: "cancel me", CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("add cancelled dispatch: %v", err)
	}
	if _, err := s.CancelDispatch(cancelled.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	_, _, _, info, _, err := s.EndAgentSession("settled", string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel)
	if err != nil {
		t.Fatalf("end: %v", err)
	}
	if len(info) != 0 {
		t.Fatalf("settled-dispatch end returned cancelled info = %+v, want empty", info)
	}
	got, err := s.GetDispatch(acked.ID)
	if err != nil {
		t.Fatalf("get acked: %v", err)
	}
	if got.Status != model.DispatchAcked {
		t.Fatalf("acked status = %q, want acked (must not be reverted)", got.Status)
	}
}

// TestAddAgentClaimIsStateNeutral locks in BACI-300: a freshly-created
// claim is a focus marker and never moves the issue's state, in any
// source state. The returned StateChange always reads Old == New (no
// SQL write, no issue.state audit row).
func TestAddAgentClaimIsStateNeutral(t *testing.T) {
	cases := []struct {
		name      string
		fromState model.State
	}{
		{"todo stays todo", model.StateTodo},
		{"in_review stays in_review", model.StateInReview},
		{"done stays done", model.StateDone},
		{"in_pipeline stays in_pipeline", model.StateInPipeline},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, _ := seedRepoAndIssue(t)
			iss, err := s.CreateIssue(repo.ID, nil, "claim-state", "", tc.fromState, nil, "")
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
				SessionID: "claim-state-" + string(tc.fromState), RepoID: repo.ID, Actor: "agent-claude",
			}); err != nil {
				t.Fatalf("register: %v", err)
			}
			_, _, _, sc, err := s.AddAgentClaim("claim-state-"+string(tc.fromState), iss.ID, "")
			if err != nil {
				t.Fatalf("AddAgentClaim: %v", err)
			}
			if sc == nil {
				t.Fatal("expected a StateChange on a fresh claim, got nil")
			}
			if sc.Old != tc.fromState || sc.New != tc.fromState {
				t.Errorf("StateChange: got %s → %s, want %s → %s (state-neutral)",
					sc.Old, sc.New, tc.fromState, tc.fromState)
			}
			if sc.Changed() {
				t.Errorf("Changed() = true, want false (a claim never moves state)")
			}
			// Verify the issue stayed put.
			got, err := s.GetIssueByID(iss.ID)
			if err != nil {
				t.Fatalf("GetIssueByID: %v", err)
			}
			if got.State != tc.fromState {
				t.Errorf("issue state after claim = %q, want %q (unchanged)", got.State, tc.fromState)
			}
		})
	}
}

// TestReleaseAgentClaimAppliesFinalState locks in BACI-126c: the
// release writes the caller-supplied final state atomically. Mirrors
// the claim's auto-transition test.
func TestReleaseAgentClaimAppliesFinalState(t *testing.T) {
	cases := []struct {
		name    string
		final   model.State
		wantNew model.State
	}{
		{"release as in_review", model.StateInReview, model.StateInReview},
		{"release as done", model.StateDone, model.StateDone},
		{"release as todo", model.StateTodo, model.StateTodo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, iss := seedRepoAndIssue(t)
			sid := "release-" + string(tc.final)
			if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
				SessionID: sid, RepoID: repo.ID, Actor: "agent-claude",
			}); err != nil {
				t.Fatalf("register: %v", err)
			}
			if _, _, _, _, err := s.AddAgentClaim(sid, iss.ID, ""); err != nil {
				t.Fatalf("AddAgentClaim: %v", err)
			}
			_, _, sc, err := s.ReleaseAgentClaim(sid, iss.ID, tc.final)
			if err != nil {
				t.Fatalf("ReleaseAgentClaim: %v", err)
			}
			if sc == nil {
				t.Fatal("expected a StateChange on release, got nil")
			}
			if sc.New != tc.wantNew {
				t.Errorf("StateChange.New = %s, want %s", sc.New, tc.wantNew)
			}
			got, err := s.GetIssueByID(iss.ID)
			if err != nil {
				t.Fatalf("GetIssueByID: %v", err)
			}
			if got.State != tc.wantNew {
				t.Errorf("issue state after release = %q, want %q", got.State, tc.wantNew)
			}
		})
	}
}

// TestOpenClaimsForSession backs the BACI-126b gate's session lookup:
// returns the open claim keys for one session, ignoring released
// claims and claims on other sessions.
func TestOpenClaimsForSession(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "open-a", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "open-b", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register b: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("open-a", iss.ID, "p1"); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	iss2, err := s.CreateIssue(repo.ID, nil, "second", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("open-a", iss2.ID, "p2"); err != nil {
		t.Fatalf("claim a 2: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("open-b", iss.ID, "p3"); err != nil {
		t.Fatalf("claim b: %v", err)
	}
	// Session A holds two open claims; B holds one.
	gotA, err := s.OpenClaimsForSession("open-a")
	if err != nil {
		t.Fatalf("OpenClaimsForSession a: %v", err)
	}
	if len(gotA) != 2 {
		t.Errorf("open-a: got %d claims, want 2", len(gotA))
	}
	gotB, err := s.OpenClaimsForSession("open-b")
	if err != nil {
		t.Fatalf("OpenClaimsForSession b: %v", err)
	}
	if len(gotB) != 1 {
		t.Errorf("open-b: got %d claims, want 1", len(gotB))
	}
	// Released claims are excluded.
	if _, _, _, err := s.ReleaseAgentClaim("open-a", iss.ID, model.StateInReview); err != nil {
		t.Fatalf("release: %v", err)
	}
	gotA2, _ := s.OpenClaimsForSession("open-a")
	if len(gotA2) != 1 {
		t.Errorf("after release, open-a: got %d claims, want 1", len(gotA2))
	}
	// Unknown session yields empty, no error.
	gotUnknown, err := s.OpenClaimsForSession("nobody")
	if err != nil {
		t.Fatalf("OpenClaimsForSession unknown: %v", err)
	}
	if len(gotUnknown) != 0 {
		t.Errorf("unknown session: got %d claims, want 0", len(gotUnknown))
	}
}

// TestEndAgentSessionAppliesOrphanState locks in BACI-126c's cascaded
// release: every claim auto-released by EndAgentSession lands the
// issue in the caller-supplied orphanState. The returned StateChange
// slice carries one entry per issue that actually moved.
func TestEndAgentSessionAppliesOrphanState(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	iss2, err := s.CreateIssue(repo.ID, nil, "second", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	sid := "orphan-end"
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: sid, RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim(sid, iss.ID, ""); err != nil {
		t.Fatalf("claim 1: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim(sid, iss2.ID, ""); err != nil {
		t.Fatalf("claim 2: %v", err)
	}
	// End the session with an explicit state_on_orphan=in_review; both
	// issues should land at in_review. (The claim itself is state-neutral
	// since BACI-300, so both started in todo.)
	_, _, stateChanges, _, _, err := s.EndAgentSession(sid, string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel)
	if err != nil {
		t.Fatalf("end: %v", err)
	}
	if len(stateChanges) != 2 {
		t.Errorf("got %d state changes, want 2", len(stateChanges))
	}
	for _, sc := range stateChanges {
		if sc.New != model.StateInReview {
			t.Errorf("state change for %s: New = %s, want in_review", sc.IssueKey, sc.New)
		}
	}
	for _, id := range []int64{iss.ID, iss2.ID} {
		got, err := s.GetIssueByID(id)
		if err != nil {
			t.Fatalf("GetIssueByID: %v", err)
		}
		if got.State != model.StateInReview {
			t.Errorf("issue %d state after end = %q, want in_review", id, got.State)
		}
	}
}

// TestEndAgentSession_PresumedDeadRequeuesDispatches (BACI-133) locks in
// the reaper-recovery path: a session-targeted dispatch on a session
// the reaper force-ends with reason=presumed_dead and cascade=Requeue
// comes back out queued (not cancelled), with target_session_id=''
// and target_agent_id=NULL so the BACI-51 matcher can rebind it. The
// requeued row is still visible to WaitingDispatchForIssue (BACI-255
// — the row IS the signal), so the kanban spinner keeps spinning
// while the matcher rebinds.
func TestEndAgentSession_PresumedDeadRequeuesDispatches(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "going-dark", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:          repo.ID,
		TargetSessionID: "going-dark",
		IssueID:         &iss.ID,
		Mode:            model.DispatchModeImplement,
		Payload:         "do it",
		CreatedBy:       "supervisor",
	})
	if err != nil {
		t.Fatalf("add dispatch: %v", err)
	}
	// Bump the dispatch to delivered to mirror the production state the
	// reaper most commonly catches: target session received the row, then
	// went silent before acking.
	if _, err := s.DB.Exec(`UPDATE agent_dispatches SET status = 'delivered' WHERE id = ?`, d.ID); err != nil {
		t.Fatalf("flip to delivered: %v", err)
	}

	_, _, _, cascade, _, err := s.EndAgentSession("going-dark",
		string(model.EndReasonPresumedDead), model.StateInReview, DispatchCascadeRequeue)
	if err != nil {
		t.Fatalf("end presumed_dead: %v", err)
	}
	if len(cascade) != 1 {
		t.Fatalf("cascade count = %d, want 1; rows = %+v", len(cascade), cascade)
	}
	if cascade[0].NewStatus != model.DispatchQueued {
		t.Fatalf("cascade[0].NewStatus = %q, want %q", cascade[0].NewStatus, model.DispatchQueued)
	}

	got, err := s.GetDispatch(d.ID)
	if err != nil {
		t.Fatalf("get dispatch: %v", err)
	}
	if got.Status != model.DispatchQueued {
		t.Fatalf("dispatch status = %q, want queued (BACI-133 reaper recovery)", got.Status)
	}
	if got.TargetSessionID != "" {
		t.Fatalf("target_session_id = %q, want empty (matcher must be free to rebind)", got.TargetSessionID)
	}
	if got.TargetAgentID != nil {
		t.Fatalf("target_agent_id = %v, want nil", *got.TargetAgentID)
	}

	// BACI-255: the requeued row is still active (status=queued), so
	// WaitingDispatchForIssue keeps returning it — the spinner stays
	// lit while the matcher rebinds to a fresh agent. The next claim
	// is what stops it from being "waiting".
	wd, err := s.WaitingDispatchForIssue(repo.ID, iss.ID)
	if err != nil {
		t.Fatalf("WaitingDispatchForIssue: %v", err)
	}
	if wd == nil {
		t.Fatal("WaitingDispatchForIssue = nil after requeue; queued row should still satisfy the predicate")
	}
	if wd.ID != d.ID {
		t.Fatalf("WaitingDispatchForIssue id = %d, want %d (the requeued row)", wd.ID, d.ID)
	}
}

// TestEndAgentSession_PresumedDeadRequeuesIdentityWhenLastLive (BACI-133)
// mirrors the BACI-58 §B identity-cancel test on the requeue branch:
// when the dying session is the identity's only live one, an
// identity-targeted dispatch loses its identity binding and goes back
// to queued so a fresh agent can pick it up.
func TestEndAgentSession_PresumedDeadRequeuesIdentityWhenLastLive(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	ag, _, err := s.UpsertAgent("solo-otter@claude.shiny", true)
	if err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "only-live-reap", RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchModeImplement,
		Payload:       "identity-targeted",
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add identity dispatch: %v", err)
	}
	_, _, _, cascade, _, err := s.EndAgentSession("only-live-reap",
		string(model.EndReasonPresumedDead), model.StateInReview, DispatchCascadeRequeue)
	if err != nil {
		t.Fatalf("end: %v", err)
	}
	if len(cascade) != 1 || cascade[0].ID != d.ID {
		t.Fatalf("expected identity dispatch in cascade, got %+v", cascade)
	}
	if cascade[0].NewStatus != model.DispatchQueued {
		t.Fatalf("NewStatus = %q, want queued", cascade[0].NewStatus)
	}
	got, err := s.GetDispatch(d.ID)
	if err != nil {
		t.Fatalf("get dispatch: %v", err)
	}
	if got.Status != model.DispatchQueued {
		t.Fatalf("status = %q, want queued", got.Status)
	}
	if got.TargetAgentID != nil {
		t.Fatalf("target_agent_id = %v, want nil — identity has no live sessions, matcher must re-pick", *got.TargetAgentID)
	}
}

// TestEndAgentSession_PresumedDeadPreservesIdentityWhenSiblingAlive
// (BACI-133) — the sibling-alive guard ports correctly to the requeue
// branch: two sessions sharing one identity, end one presumed_dead,
// the identity-targeted dispatch must stay pending/delivered (untouched)
// because the sibling can still service it.
func TestEndAgentSession_PresumedDeadPreservesIdentityWhenSiblingAlive(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	ag, _, err := s.UpsertAgent("pair-finch@claude.shiny", true)
	if err != nil {
		t.Fatalf("upsert agent: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "pair-a", RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("upsert pair-a: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "pair-b", RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("upsert pair-b: %v", err)
	}
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		Mode:          model.DispatchModeImplement,
		Payload:       "pair-targeted",
		CreatedBy:     "supervisor",
	})
	if err != nil {
		t.Fatalf("add pair dispatch: %v", err)
	}
	_, _, _, cascade, _, err := s.EndAgentSession("pair-a",
		string(model.EndReasonPresumedDead), model.StateInReview, DispatchCascadeRequeue)
	if err != nil {
		t.Fatalf("end pair-a: %v", err)
	}
	for _, info := range cascade {
		if info.ID == d.ID {
			t.Fatalf("identity dispatch requeued despite sibling-alive session: %+v", info)
		}
	}
	got, err := s.GetDispatch(d.ID)
	if err != nil {
		t.Fatalf("get dispatch: %v", err)
	}
	if got.Status == model.DispatchQueued {
		t.Fatalf("dispatch unexpectedly requeued despite sibling alive; status = %q", got.Status)
	}
	if got.TargetAgentID == nil || *got.TargetAgentID != ag.ID {
		t.Fatalf("identity binding lost despite sibling alive; target_agent_id = %v", got.TargetAgentID)
	}
}

// TestEndAgentSession_RequeueRejectsNonPresumedDeadReason (BACI-133)
// locks in the store-boundary defence: DispatchCascadeRequeue is only
// valid when paired with reason=presumed_dead. A caller bug that pairs
// it with any other reason errors before any write — no silent
// re-queue on a user-driven end.
func TestEndAgentSession_RequeueRejectsNonPresumedDeadReason(t *testing.T) {
	s, repo, _ := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "user-stop", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, _, _, _, _, err := s.EndAgentSession("user-stop",
		string(model.EndReasonStop), model.StateInReview, DispatchCascadeRequeue)
	if err == nil {
		t.Fatalf("expected store-boundary error pairing Requeue with reason=stop, got nil")
	}
	// And the session must still be alive — the rejection fires before
	// any write.
	sess, gerr := s.GetAgentSession("user-stop")
	if gerr != nil {
		t.Fatalf("get session: %v", gerr)
	}
	if sess.EndedAt != nil {
		t.Fatalf("session ended despite store-boundary rejection")
	}
}

// seedOrphanIssueDelete drops the named issue row from the DB while
// leaving any agent_claims rows that reference it in place — the
// pre-BACI-134 corruption shape that BACI-140 has to tolerate. The FK on
// agent_claims.issue_id declares ON DELETE CASCADE, so we have to bypass
// FK enforcement for the DELETE; modernc/sqlite's `PRAGMA foreign_keys`
// is connection-scoped and the database/sql pool would otherwise hand
// us a fresh connection between the pragma and the DELETE, so the whole
// sequence pins a single *sql.Conn.
func seedOrphanIssueDelete(t *testing.T, s *Store, issueID int64) {
	t.Helper()
	ctx := context.Background()
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatalf("pragma off: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM issues WHERE id = ?`, issueID); err != nil {
		t.Fatalf("orphan delete: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("pragma on: %v", err)
	}
}

// TestEndAgentSession_TolerantOfOrphanClaim (BACI-140) locks in the
// crash fix: an agent_claims row whose issue_id no longer points at a
// row in `issues` must not block EndAgentSession. The session ends
// cleanly (ended_at stamped), the orphan claim is released, the idle
// pinger stops looping on `sql: no rows in result set`.
func TestEndAgentSession_TolerantOfOrphanClaim(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "orphan-end", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("orphan-end", iss.ID, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// Corrupt the store: delete the issue out from under the claim
	// without firing the cascade. Mirrors the BACI-140 ticket repro.
	seedOrphanIssueDelete(t, s, iss.ID)

	sess, assigneeChanges, stateChanges, _, _, err := s.EndAgentSession(
		"orphan-end", string(model.EndReasonPresumedDead),
		model.StateInReview, DispatchCascadeCancel,
	)
	if err != nil {
		t.Fatalf("end with orphan claim: %v", err)
	}
	if sess.EndedAt == nil {
		t.Fatalf("ended_at not stamped — session is still alive after EndAgentSession")
	}
	if len(assigneeChanges) != 0 {
		t.Fatalf("got %d assignee changes from orphan claim, want 0", len(assigneeChanges))
	}
	if len(stateChanges) != 0 {
		t.Fatalf("got %d state changes from orphan claim, want 0", len(stateChanges))
	}
	// Claim row exists and is released — the bulk UPDATE doesn't read
	// issues, so the orphan never blocks it.
	var released sql.NullTime
	if err := s.DB.QueryRow(`
		SELECT released_at FROM agent_claims c
		JOIN agent_sessions s ON s.id = c.session_pk
		WHERE s.session_id = ?
		ORDER BY c.claimed_at DESC LIMIT 1`,
		"orphan-end",
	).Scan(&released); err != nil {
		t.Fatalf("query claim: %v", err)
	}
	if !released.Valid {
		t.Fatalf("orphan claim was not released")
	}
}

// TestEndAgentSession_TolerantOfMixedOrphanAndLiveClaims (BACI-140)
// guards the per-issue release loop: a session holding both a live
// claim and an orphan claim must still process the live one cleanly
// (assignee cleared, state cascaded) — the orphan is dropped by the
// upfront filter and never reaches the per-issue helpers.
func TestEndAgentSession_TolerantOfMixedOrphanAndLiveClaims(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	live, err := s.CreateIssue(repo.ID, nil, "live", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create live issue: %v", err)
	}
	ag, _, err := s.UpsertAgent("orphan-pair@claude.shiny", true)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "mixed", RepoID: repo.ID, AgentID: &ag.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("mixed", iss.ID, ""); err != nil {
		t.Fatalf("claim orphan: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("mixed", live.ID, ""); err != nil {
		t.Fatalf("claim live: %v", err)
	}
	// Vanish the first issue out from under its claim.
	seedOrphanIssueDelete(t, s, iss.ID)

	sess, changes, states, _, _, err := s.EndAgentSession(
		"mixed", string(model.EndReasonStop),
		"", DispatchCascadeCancel,
	)
	if err != nil {
		t.Fatalf("end mixed: %v", err)
	}
	if sess.EndedAt == nil {
		t.Fatalf("ended_at not stamped")
	}
	// Live issue is the only one that should surface an assignee change.
	if len(changes) != 1 {
		t.Fatalf("got %d assignee changes, want 1 (live issue only)", len(changes))
	}
	if changes[0].IssueID != live.ID || changes[0].New != "" {
		t.Fatalf("unexpected assignee change: %+v", changes[0])
	}
	// BACI-300: an empty orphanState leaves every issue's state alone, so
	// no state change row is produced.
	for _, sc := range states {
		if sc.Changed() {
			t.Fatalf("unexpected state change for live issue: %+v", sc)
		}
	}
}

// TestReleaseAgentClaim_TolerantOfOrphanIssue (BACI-140) protects the
// single-claim release path: a human (or future caller) deleting an
// issue with a live claim attached used to crash ReleaseAgentClaim with
// a naked sql.ErrNoRows from the per-issue helpers. The hardened
// helpers turn that crash into a logged no-op so the release
// transaction commits and the claim row is stamped released_at.
//
// The post-commit getAgentClaimByID re-fetch INNER JOINs `issues`, so
// the function still returns ErrNotFound for an orphan claim — the
// release just can't be re-rendered with an issue key. The test
// asserts on the DB-side outcome (released_at present) rather than the
// returned struct, which is what the caller in EndAgentSession's loop
// actually relies on.
func TestReleaseAgentClaim_TolerantOfOrphanIssue(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "orphan-rel", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("orphan-rel", iss.ID, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	seedOrphanIssueDelete(t, s, iss.ID)

	_, _, _, err := s.ReleaseAgentClaim("orphan-rel", iss.ID, model.StateInReview)
	// The post-commit re-fetch can't see the orphan claim (its
	// getAgentClaimByID INNER JOIN drops it), so the function returns
	// ErrNotFound — but the underlying tx must still have committed.
	if err != nil && !errors.Is(err, ErrNotFound) {
		t.Fatalf("release with orphan issue: unexpected %v", err)
	}
	var released sql.NullTime
	if err := s.DB.QueryRow(`
		SELECT released_at FROM agent_claims c
		JOIN agent_sessions s ON s.id = c.session_pk
		WHERE s.session_id = ?
		ORDER BY c.claimed_at DESC LIMIT 1`,
		"orphan-rel",
	).Scan(&released); err != nil {
		t.Fatalf("query claim: %v", err)
	}
	if !released.Valid {
		t.Fatalf("orphan claim was not released — release tx did not commit")
	}
}

// TestOpenPrunesOrphanAgentClaims (BACI-140) locks in the startup-time
// janitor: an orphan agent_claims row left in the DB by a pre-BACI-134
// raw `sqlite3` write is swept by the next store.Open. Both the
// historical corruption + future safety net rely on this.
func TestOpenPrunesOrphanAgentClaims(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	repo, err := s.CreateRepo("AGNT", "orphan-prune", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "stub", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "to-orphan", RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("to-orphan", iss.ID, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	seedOrphanIssueDelete(t, s, iss.ID)

	// Sanity: the orphan claim is in the DB before Close/re-Open.
	var before int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM agent_claims WHERE issue_id = ?`, iss.ID).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}
	if before != 1 {
		t.Fatalf("orphan claim was not seeded, count = %d", before)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })

	var after int
	if err := s2.DB.QueryRow(`SELECT COUNT(*) FROM agent_claims WHERE issue_id = ?`, iss.ID).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != 0 {
		t.Fatalf("orphan claim survived Open() janitor: have %d, want 0", after)
	}
}

// TestReleaseAgentClaim_TerminalStateNoop (BACI-200, Bug 2) locks in
// that a release against an issue that has already reached a terminal
// state (`done` / `cancelled`) leaves the state unchanged. The claim
// is still dropped — the worker's bookkeeping completes — but a
// parallel worker finishing late cannot drag a shipped ticket
// backwards into `in_review`. This is the BACI-197 regression
// (`done → in_review`) the issue calls out.
func TestReleaseAgentClaim_TerminalStateNoop(t *testing.T) {
	cases := []struct {
		name     string
		terminal model.State
	}{
		{"done stays done", model.StateDone},
		{"cancelled stays cancelled", model.StateCancelled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, iss := seedRepoAndIssue(t)
			sid := "term-" + string(tc.terminal)
			if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
				SessionID: sid, RepoID: repo.ID, Actor: "agent-claude",
			}); err != nil {
				t.Fatalf("register: %v", err)
			}
			if _, _, _, _, err := s.AddAgentClaim(sid, iss.ID, ""); err != nil {
				t.Fatalf("AddAgentClaim: %v", err)
			}
			// Drive the issue to the terminal state out-of-band (the
			// BACI-197 scenario: the sibling worker shipped first and the
			// issue is already `done` by the time this worker's release
			// lands).
			if err := s.SetIssueState(iss.ID, tc.terminal); err != nil {
				t.Fatalf("set terminal state: %v", err)
			}
			claim, _, sc, err := s.ReleaseAgentClaim(sid, iss.ID, model.StateInReview)
			if err != nil {
				t.Fatalf("ReleaseAgentClaim: %v", err)
			}
			if claim == nil {
				t.Fatal("expected a released claim, got nil")
			}
			// Release bookkeeping still completes — the claim row is
			// stamped released_at.
			if claim.ReleasedAt == nil {
				t.Fatalf("claim released_at = nil, want a timestamp")
			}
			// StateChange shows Old == New == terminal (no movement).
			if sc == nil {
				t.Fatal("expected a StateChange on release, got nil")
			}
			if sc.Old != tc.terminal || sc.New != tc.terminal {
				t.Errorf("StateChange = (%s → %s), want (%s → %s) (terminal no-op)",
					sc.Old, sc.New, tc.terminal, tc.terminal)
			}
			// Issue itself is unmoved.
			got, err := s.GetIssueByID(iss.ID)
			if err != nil {
				t.Fatalf("GetIssueByID: %v", err)
			}
			if got.State != tc.terminal {
				t.Errorf("issue state after release = %q, want %q (BACI-200 terminal gate)",
					got.State, tc.terminal)
			}
		})
	}
}
