package store

import (
	"database/sql"
	"errors"
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
	if _, _, _, err := s.AddAgentClaim("sess-2", iss.ID, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, _, err := s.EndAgentSession("sess-2", string(model.EndReasonStop)); err != nil {
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
	if _, _, err := s.EndAgentSession("sess-3", string(model.EndReasonStop)); err != nil {
		t.Fatalf("end: %v", err)
	}
	if _, _, _, err := s.AddAgentClaim("sess-3", iss.ID, ""); err == nil {
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
	if _, _, err := s.EndAgentSession("stale", string(model.EndReasonStop)); err != nil {
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
		if _, _, err := s.EndAgentSession(id, string(model.EndReasonStop)); err != nil {
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
	if _, _, _, err := s.AddAgentClaim("cascade", iss.ID, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, _, err := s.EndAgentSession("cascade", string(model.EndReasonStop)); err != nil {
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
	if _, _, err := s.EndAgentSession("dispatched", string(model.EndReasonStop)); err != nil {
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
		if _, _, _, err := s.AddAgentClaim("rapid", iss.ID, ""); err != nil {
			t.Fatalf("claim cycle %d: %v", i, err)
		}
		if _, _, err := s.ReleaseAgentClaim("rapid", iss.ID); err != nil {
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
	first, created, _, err := s.AddAgentClaim("idem", iss.ID, "")
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !created {
		t.Fatalf("expected created=true for the first claim")
	}
	second, created, _, err := s.AddAgentClaim("idem", iss.ID, "")
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
	if _, _, err := s.EndAgentSession("ended-then-reregister", string(model.EndReasonStop)); err != nil {
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
	if _, _, err := s.ReleaseAgentClaim("rel", iss.ID); err == nil {
		t.Fatalf("expected ErrNotFound releasing a non-existent claim, got nil")
	}
	if _, _, err := s.ReleaseAgentClaim("does-not-exist", iss.ID); err == nil {
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
	first, created, _, err := s.AddAgentClaim("prompt-sess", iss.ID, "do the thing")
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !created || first.Prompt != "do the thing" {
		t.Fatalf("first claim: created=%v prompt=%q, want created=true prompt=%q", created, first.Prompt, "do the thing")
	}
	// Re-claim with a fresher prompt — no-op claim, but prompt updates.
	second, created, _, err := s.AddAgentClaim("prompt-sess", iss.ID, "do the other thing")
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
	third, _, _, err := s.AddAgentClaim("prompt-sess", iss.ID, "")
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
	if _, _, _, err := s.AddAgentClaim("claims-a", iss.ID, "first claim"); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	if _, _, err := s.ReleaseAgentClaim("claims-a", iss.ID); err != nil {
		t.Fatalf("release a: %v", err)
	}
	if _, _, _, err := s.AddAgentClaim("claims-b", iss.ID, "second claim"); err != nil {
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
	iss2, err := s.CreateIssue(repo.ID, nil, "stub 2", "", model.StateTodo, nil)
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
	if _, _, _, err := s.AddAgentClaim("busy-sess", iss.ID, "p1"); err != nil {
		t.Fatalf("claim busy: %v", err)
	}
	// A released claim must not count.
	if _, _, _, err := s.AddAgentClaim("busy-sess", iss2.ID, "p2"); err != nil {
		t.Fatalf("claim busy 2: %v", err)
	}
	if _, _, err := s.ReleaseAgentClaim("busy-sess", iss2.ID); err != nil {
		t.Fatalf("release busy 2: %v", err)
	}
	// An ended session's claim must not count (EndAgentSession releases it).
	if _, _, _, err := s.AddAgentClaim("ended-sess", iss2.ID, "p3"); err != nil {
		t.Fatalf("claim ended: %v", err)
	}
	if _, _, err := s.EndAgentSession("ended-sess", string(model.EndReasonStop)); err != nil {
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
	_, created, change, err := s.AddAgentClaim("with-id", iss.ID, "")
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
	iss2, err := s.CreateIssue(repo.ID, nil, "stub 2", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("create issue 2: %v", err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: "no-id", RepoID: repo.ID, Actor: "manual-actor",
	}); err != nil {
		t.Fatalf("register no-id: %v", err)
	}
	if _, _, change, err := s.AddAgentClaim("no-id", iss2.ID, ""); err != nil {
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
	_, _, change, err := s.AddAgentClaim("overwrite", iss.ID, "")
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
	if _, _, _, err := s.AddAgentClaim("sess-a", iss.ID, ""); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	if _, _, _, err := s.AddAgentClaim("sess-b", iss.ID, ""); err != nil {
		t.Fatalf("claim b: %v", err)
	}
	// Last claim wins: assignee is B.
	if got := assigneeOf(t, s, iss.ID); got != "pair-b@claude.shiny" {
		t.Fatalf("after both claims, assignee = %q, want pair-b@claude.shiny", got)
	}
	// A releases — B still holds an open claim, so the assignee stays.
	_, change, err := s.ReleaseAgentClaim("sess-a", iss.ID)
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
	_, change, err = s.ReleaseAgentClaim("sess-b", iss.ID)
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
	if _, _, _, err := s.AddAgentClaim("foreign", iss.ID, ""); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// A human reassigns the issue out from under the agent.
	if err := s.SetIssueAssignee(iss.ID, "a-human"); err != nil {
		t.Fatalf("human reassign: %v", err)
	}
	_, change, err := s.ReleaseAgentClaim("foreign", iss.ID)
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
	iss2, err := s.CreateIssue(repo.ID, nil, "stub 2", "", model.StateTodo, nil)
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
	if _, _, _, err := s.AddAgentClaim("ending", iss.ID, ""); err != nil {
		t.Fatalf("claim 1: %v", err)
	}
	if _, _, _, err := s.AddAgentClaim("ending", iss2.ID, ""); err != nil {
		t.Fatalf("claim 2: %v", err)
	}
	_, changes, err := s.EndAgentSession("ending", string(model.EndReasonStop))
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
