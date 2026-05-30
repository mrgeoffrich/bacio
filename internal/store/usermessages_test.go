package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// seedMessageSession registers a session against the seed repo, ready
// for the user_messages tests.
func seedMessageSession(t *testing.T, sessionID string) (*Store, *model.Repo) {
	t.Helper()
	s, repo, _ := seedRepoAndIssue(t)
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{
		SessionID: sessionID, RepoID: repo.ID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register session: %v", err)
	}
	return s, repo
}

// TestAddUserMessage covers the happy path: enqueue → row carries the
// resolved session id + repo id, an un-consumed timestamp, and the body.
func TestAddUserMessage(t *testing.T) {
	s, repo := seedMessageSession(t, "um-1")
	m, err := s.AddUserMessage(AddUserMessageIn{
		SessionID: "um-1",
		Body:      "please write ACKSTEER in your next message",
		CreatedBy: "user",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if m.ID == 0 {
		t.Fatalf("expected populated id, got %+v", m)
	}
	if m.SessionID != "um-1" {
		t.Fatalf("session id = %q, want um-1", m.SessionID)
	}
	if m.RepoID != repo.ID {
		t.Fatalf("repo id = %d, want %d", m.RepoID, repo.ID)
	}
	if m.ConsumedAt != nil {
		t.Fatalf("fresh message should be un-consumed, got consumed_at=%v", m.ConsumedAt)
	}
	if m.Body != "please write ACKSTEER in your next message" {
		t.Fatalf("body round-trip mismatch: %q", m.Body)
	}
}

// TestAddUserMessageUnknownSession returns ErrNotFound — there's no
// session FK to hang the message off.
func TestAddUserMessageUnknownSession(t *testing.T) {
	s, _ := seedMessageSession(t, "um-real")
	_, err := s.AddUserMessage(AddUserMessageIn{
		SessionID: "um-ghost",
		Body:      "hello?",
		CreatedBy: "user",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown session, got %v", err)
	}
}

// TestAddUserMessageValidation rejects an empty body, an over-cap body,
// and an empty actor.
func TestAddUserMessageValidation(t *testing.T) {
	s, _ := seedMessageSession(t, "um-val")
	if _, err := s.AddUserMessage(AddUserMessageIn{SessionID: "um-val", Body: "  ", CreatedBy: "user"}); err == nil {
		t.Fatalf("empty body should be rejected")
	}
	tooLong := strings.Repeat("x", model.MaxUserMessageLen+1)
	if _, err := s.AddUserMessage(AddUserMessageIn{SessionID: "um-val", Body: tooLong, CreatedBy: "user"}); err == nil {
		t.Fatalf("over-cap body should be rejected")
	}
	if _, err := s.AddUserMessage(AddUserMessageIn{SessionID: "um-val", Body: "ok", CreatedBy: ""}); err == nil {
		t.Fatalf("empty created_by should be rejected")
	}
}

// TestDrainUserMessagesForSessionConsumesOnce locks in the
// "drain marks consumed" contract: the first drain returns the rows
// oldest-first, a second drain returns nothing, and the rows are stamped
// consumed.
func TestDrainUserMessagesForSessionConsumesOnce(t *testing.T) {
	s, _ := seedMessageSession(t, "um-drain")
	for _, body := range []string{"first", "second", "third"} {
		if _, err := s.AddUserMessage(AddUserMessageIn{SessionID: "um-drain", Body: body, CreatedBy: "user"}); err != nil {
			t.Fatalf("add %q: %v", body, err)
		}
		// Nudge created_at apart so the ASC ordering is deterministic
		// even when the clock granularity is coarse.
		time.Sleep(2 * time.Millisecond)
	}
	got, err := s.DrainUserMessagesForSession("um-drain")
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("first drain returned %d rows, want 3", len(got))
	}
	if got[0].Body != "first" || got[2].Body != "third" {
		t.Fatalf("drain not oldest-first: %q ... %q", got[0].Body, got[2].Body)
	}
	for _, m := range got {
		if m.ConsumedAt == nil {
			t.Fatalf("drained row %d not stamped consumed", m.ID)
		}
	}
	// Second drain: nothing left.
	again, err := s.DrainUserMessagesForSession("um-drain")
	if err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second drain returned %d rows, want 0", len(again))
	}
}

// TestDrainUserMessagesForSessionUnknown returns an empty slice, not an
// error — the channel walks every session it can see, including ones the
// store no longer knows about.
func TestDrainUserMessagesForSessionUnknown(t *testing.T) {
	s, _ := seedMessageSession(t, "um-known")
	got, err := s.DrainUserMessagesForSession("um-unknown")
	if err != nil {
		t.Fatalf("drain unknown: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("drain unknown returned %d rows, want 0", len(got))
	}
}

// TestUserMessageCascadeOnSessionDelete locks in that hard-deleting the
// owning session cascades the user_messages rows out (ON DELETE CASCADE),
// so a pruned session leaves no orphans.
func TestUserMessageCascadeOnSessionDelete(t *testing.T) {
	s, _ := seedMessageSession(t, "um-cascade")
	m, err := s.AddUserMessage(AddUserMessageIn{SessionID: "um-cascade", Body: "steer", CreatedBy: "user"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// End + backdate so the session prune deletes the row.
	if _, _, _, _, _, err := s.EndAgentSession("um-cascade", string(model.EndReasonStop), model.StateInReview, DispatchCascadeCancel); err != nil {
		t.Fatalf("end: %v", err)
	}
	old := time.Now().Add(-2 * AgentSessionRetention).UTC().Format("2006-01-02 15:04:05")
	if _, err := s.DB.Exec(`UPDATE agent_sessions SET ended_at = ? WHERE session_id = 'um-cascade'`, old); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if err := pruneAgentSessions(s.DB, AgentSessionRetention); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if _, err := s.GetUserMessage(m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected message to cascade out, got %v", err)
	}
}
