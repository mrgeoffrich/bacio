package store

import (
	"database/sql"
	"path/filepath"
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
	iss, err := s.CreateIssue(repo.ID, nil, "stub", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	return s, repo, iss
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
	if _, err := s.AddAgentClaim("sess-2", iss.ID); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.EndAgentSession("sess-2", string(model.EndReasonStop)); err != nil {
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
	if _, err := s.EndAgentSession("sess-3", string(model.EndReasonStop)); err != nil {
		t.Fatalf("end: %v", err)
	}
	if _, err := s.AddAgentClaim("sess-3", iss.ID); err == nil {
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
	if _, err := s.EndAgentSession("stale", string(model.EndReasonStop)); err != nil {
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

// Ensure newTestStore is reachable (declared in issues_test.go); a
// no-op test guards against import-only regressions.
var _ = filepath.Join
