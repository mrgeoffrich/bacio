package client_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestEnsureSetupDispatchEmbedsSessionID locks in the BACI-46 fix: the
// channel-emitted setup dispatch now carries the real session id
// substituted into the JSON example, so an agent copy-pasting the
// payload verbatim ends up with a valid register call instead of the
// literal "$CLAUDE_CODE_SESSION_ID" placeholder that orphaned dispatch
// #114 a day earlier.
func TestEnsureSetupDispatchEmbedsSessionID(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()

	sid := "real-uuid-9c0f7a32"
	d, err := p.local.EnsureSetupDispatch(context.Background(), p.repo, sid)
	if err != nil {
		t.Fatalf("EnsureSetupDispatch: %v", err)
	}
	if d == nil {
		t.Fatal("EnsureSetupDispatch returned nil dispatch")
	}
	if !strings.Contains(d.Payload, `"session_id": "`+sid+`"`) {
		t.Fatalf("payload missing pre-filled session id: %q", d.Payload)
	}
	if strings.Contains(d.Payload, "$CLAUDE_CODE_SESSION_ID") {
		t.Fatalf("payload still contains the literal $CLAUDE_CODE_SESSION_ID placeholder: %q", d.Payload)
	}
}

// TestCompleteRegistrationRejectsPlaceholder is the highest-value
// regression test for BACI-46: feeding the literal
// "$CLAUDE_CODE_SESSION_ID" to register surfaces the validator's
// placeholder error and writes no agent_sessions row. This is the
// exact replay of what stranded dispatch #114.
func TestCompleteRegistrationRejectsPlaceholder(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()

	// Seed a stub for a *real* session_id first so we can prove the
	// placeholder reject is what blocks the bogus write — not some
	// upstream missing-row error.
	if _, err := p.local.CreateSessionStub(context.Background(), p.repo, "real-uuid-3ab2c4d6", "smoke-host", 7777); err != nil {
		t.Fatalf("CreateSessionStub: %v", err)
	}

	in := inputs.AgentRegisterInput{
		SessionID: "$CLAUDE_CODE_SESSION_ID",
		Actor:     "tester",
		Branch:    "main",
	}
	_, err := p.local.CompleteRegistration(context.Background(), p.repo, in, "")
	if err == nil {
		t.Fatalf("expected placeholder rejection, got nil")
	}
	if !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("error does not mention placeholder: %v", err)
	}

	// The literal-placeholder row must not exist; the validator should
	// have refused before any UpsertAgentSession.
	if _, err := p.store.GetAgentSession("$CLAUDE_CODE_SESSION_ID"); err == nil {
		t.Fatalf("placeholder row was written — validator did not block the path")
	} else if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unexpected error looking up placeholder session: %v", err)
	}
}

// TestCompleteRegistrationRejectsMalformedUUID locks in BACI-100 change
// (1): register rejects a session_id that is not a structurally valid
// UUID (the fat-fingered-retry bug) and writes no agent_sessions row.
func TestCompleteRegistrationRejectsMalformedUUID(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()

	const bad = "23543e26-6339-4b8-aff2-d8ea2013f287" // 3-hex third group
	_, err := p.local.CompleteRegistration(context.Background(), p.repo, inputs.AgentRegisterInput{
		SessionID: bad,
		Actor:     "tester",
	}, "")
	if err == nil {
		t.Fatal("expected malformed-UUID rejection, got nil")
	}
	if !strings.Contains(err.Error(), "not a valid UUID") {
		t.Fatalf("error does not mention the UUID problem: %v", err)
	}
	if _, err := p.store.GetAgentSession(bad); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("malformed-UUID row was written — register did not block: %v", err)
	}
}

// TestCompleteRegistrationDedupesOnClaudePID locks in BACI-100 change
// (2): a second register for the same (host, claude_pid) under a
// different session_id reconciles the earlier live row away (reason
// "superseded") so one OS process holds exactly one live registry row.
func TestCompleteRegistrationDedupesOnClaudePID(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	const (
		host  = "smoke-host"
		pid   = int64(4242)
		first = "698f641f-4df1-4880-ab89-0ab2693c115a"
		later = "23543e26-6339-4b8a-aff2-d8ea2013f287"
	)

	if _, err := p.local.CompleteRegistration(ctx, p.repo, inputs.AgentRegisterInput{
		SessionID: first, Actor: "tester", Host: host, ClaudePID: pid,
	}, ""); err != nil {
		t.Fatalf("first register: %v", err)
	}
	// A second register for the same claude_pid, different session_id —
	// the duplicate-registration case the issue observed.
	if _, err := p.local.CompleteRegistration(ctx, p.repo, inputs.AgentRegisterInput{
		SessionID: later, Actor: "tester", Host: host, ClaudePID: pid,
	}, ""); err != nil {
		t.Fatalf("second register: %v", err)
	}

	// Exactly one live row for this claude_pid — the incoming session.
	live, err := p.store.SessionsByClaudePID(host, pid)
	if err != nil {
		t.Fatalf("SessionsByClaudePID: %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("got %d live sessions for claude_pid %d, want 1", len(live), pid)
	}
	if live[0].SessionID != later {
		t.Fatalf("surviving live session = %q, want %q", live[0].SessionID, later)
	}
	// The first row still exists but is ended with reason "superseded".
	prior, err := p.store.GetAgentSession(first)
	if err != nil {
		t.Fatalf("GetAgentSession(first): %v", err)
	}
	if prior.EndedAt == nil {
		t.Fatal("the superseded session was not ended")
	}
	if prior.EndReason != string(model.EndReasonSuperseded) {
		t.Fatalf("end_reason = %q, want superseded", prior.EndReason)
	}
}
