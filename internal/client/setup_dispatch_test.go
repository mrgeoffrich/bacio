package client_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
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
