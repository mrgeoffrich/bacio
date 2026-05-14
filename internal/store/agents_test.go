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
	if _, _, err := s.AddAgentClaim("sess-2", iss.ID, ""); err != nil {
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
	if _, _, err := s.AddAgentClaim("sess-3", iss.ID, ""); err == nil {
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
		if _, _, err := s.AddAgentClaim("rapid", iss.ID, ""); err != nil {
			t.Fatalf("claim cycle %d: %v", i, err)
		}
		if _, err := s.ReleaseAgentClaim("rapid", iss.ID); err != nil {
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
	first, created, err := s.AddAgentClaim("idem", iss.ID, "")
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !created {
		t.Fatalf("expected created=true for the first claim")
	}
	second, created, err := s.AddAgentClaim("idem", iss.ID, "")
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
	if _, err := s.EndAgentSession("ended-then-reregister", string(model.EndReasonStop)); err != nil {
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
	if _, err := s.ReleaseAgentClaim("rel", iss.ID); err == nil {
		t.Fatalf("expected ErrNotFound releasing a non-existent claim, got nil")
	}
	if _, err := s.ReleaseAgentClaim("does-not-exist", iss.ID); err == nil {
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
	first, created, err := s.AddAgentClaim("prompt-sess", iss.ID, "do the thing")
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !created || first.Prompt != "do the thing" {
		t.Fatalf("first claim: created=%v prompt=%q, want created=true prompt=%q", created, first.Prompt, "do the thing")
	}
	// Re-claim with a fresher prompt — no-op claim, but prompt updates.
	second, created, err := s.AddAgentClaim("prompt-sess", iss.ID, "do the other thing")
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
	third, _, err := s.AddAgentClaim("prompt-sess", iss.ID, "")
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
	if _, _, err := s.AddAgentClaim("claims-a", iss.ID, "first claim"); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	if _, err := s.ReleaseAgentClaim("claims-a", iss.ID); err != nil {
		t.Fatalf("release a: %v", err)
	}
	if _, _, err := s.AddAgentClaim("claims-b", iss.ID, "second claim"); err != nil {
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
	if _, _, err := s.AddAgentClaim("busy-sess", iss.ID, "p1"); err != nil {
		t.Fatalf("claim busy: %v", err)
	}
	// A released claim must not count.
	if _, _, err := s.AddAgentClaim("busy-sess", iss2.ID, "p2"); err != nil {
		t.Fatalf("claim busy 2: %v", err)
	}
	if _, err := s.ReleaseAgentClaim("busy-sess", iss2.ID); err != nil {
		t.Fatalf("release busy 2: %v", err)
	}
	// An ended session's claim must not count (EndAgentSession releases it).
	if _, _, err := s.AddAgentClaim("ended-sess", iss2.ID, "p3"); err != nil {
		t.Fatalf("claim ended: %v", err)
	}
	if _, err := s.EndAgentSession("ended-sess", string(model.EndReasonStop)); err != nil {
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
