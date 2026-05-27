package client_test

import (
	"context"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestQuestionStampsUserActionReasonOnAskAndClearsOnAnswer (BACI-220)
// covers the round trip — the auto-flip-on-ask stamps
// `user_question` onto the issue alongside the state move to
// `needs_action`, and the resolve auto-flip clears it back to NULL
// when the state walks back to `in_progress`.
func TestQuestionStampsUserActionReasonOnAskAndClearsOnAnswer(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, sess := setupClaimedIssue(t, p, "sess-reason-1", model.StateInProgress)
	mustUserActionReason(t, p, iss.Key, "", "fresh in_progress issue should have no reason")

	q, err := p.local.AddSessionQuestion(ctx, client.AddSessionQuestionInput{
		SessionID: sess.SessionID,
		IssueKey:  iss.Key,
		Payload:   simplePayload(),
		AskedBy:   "agent-x@claude.test",
	})
	if err != nil {
		t.Fatalf("AddSessionQuestion: %v", err)
	}
	mustState(t, p, iss.Key, model.StateNeedsAction, "after ask")
	mustUserActionReason(t, p, iss.Key, model.UserActionReasonQuestion, "after ask")

	if _, err := p.local.AnswerSessionQuestion(ctx, q.ID, model.QuestionAnswers{
		"Pick one?": "A",
	}, false); err != nil {
		t.Fatalf("AnswerSessionQuestion: %v", err)
	}
	mustState(t, p, iss.Key, model.StateInProgress, "after answer")
	mustUserActionReason(t, p, iss.Key, "", "after answer the reason should clear")
}

// TestQuestionStampsUserActionReasonClearedOnCancel mirrors the
// answer test for the cancel path — the reason follows the state
// back out of `needs_action`.
func TestQuestionStampsUserActionReasonClearedOnCancel(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, sess := setupClaimedIssue(t, p, "sess-reason-2", model.StateInProgress)

	q, err := p.local.AddSessionQuestion(ctx, client.AddSessionQuestionInput{
		SessionID: sess.SessionID,
		IssueKey:  iss.Key,
		Payload:   simplePayload(),
		AskedBy:   "agent-x@claude.test",
	})
	if err != nil {
		t.Fatalf("AddSessionQuestion: %v", err)
	}
	mustUserActionReason(t, p, iss.Key, model.UserActionReasonQuestion, "after ask")

	if _, err := p.local.CancelSessionQuestion(ctx, q.ID, false); err != nil {
		t.Fatalf("CancelSessionQuestion: %v", err)
	}
	mustState(t, p, iss.Key, model.StateInProgress, "after cancel")
	mustUserActionReason(t, p, iss.Key, "", "after cancel the reason should clear")
}

// TestQuestionUserActionReasonPersistsAcrossSecondAsk locks the
// reason in place while a second open question is still pending on
// the same issue — the state stays in `needs_action`, and the
// reason MUST stay set rather than getting tossed by either the
// transient state writes the auto-flip skips or any subsequent
// no-op state move.
func TestQuestionUserActionReasonPersistsAcrossSecondAsk(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, sess := setupClaimedIssue(t, p, "sess-reason-3", model.StateInProgress)

	q1, err := p.local.AddSessionQuestion(ctx, client.AddSessionQuestionInput{
		SessionID: sess.SessionID, IssueKey: iss.Key,
		Payload: simplePayload(), AskedBy: "agent-x@claude.test",
	})
	if err != nil {
		t.Fatalf("AddSessionQuestion 1: %v", err)
	}
	if _, err := p.local.AddSessionQuestion(ctx, client.AddSessionQuestionInput{
		SessionID: sess.SessionID, IssueKey: iss.Key,
		Payload: simplePayload(), AskedBy: "agent-x@claude.test",
	}); err != nil {
		t.Fatalf("AddSessionQuestion 2: %v", err)
	}
	mustState(t, p, iss.Key, model.StateNeedsAction, "after two asks")
	mustUserActionReason(t, p, iss.Key, model.UserActionReasonQuestion, "after two asks")

	if _, err := p.local.AnswerSessionQuestion(ctx, q1.ID, model.QuestionAnswers{"Pick one?": "A"}, false); err != nil {
		t.Fatalf("AnswerSessionQuestion 1: %v", err)
	}
	mustState(t, p, iss.Key, model.StateNeedsAction, "after resolving 1 of 2 — state stays parked")
	mustUserActionReason(t, p, iss.Key, model.UserActionReasonQuestion, "after resolving 1 of 2 — reason stays set")
}

// TestSetIssueStateClearsUserActionReason locks in the schema-level
// invariant: a direct SetIssueState move out of `needs_action`
// clears the reason column regardless of who initiated the move.
// Mirrors the brief's "today that flip happens via the Stop /
// UserPromptSubmit idle sync, and the worker also runs `bacio issue
// state <id> in_progress`" — both reach this code path.
func TestSetIssueStateClearsUserActionReason(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "reason clear", "", model.StateInProgress, nil, "")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	// Park it manually so the test doesn't depend on the question path.
	if err := p.store.SetIssueState(iss.ID, model.StateNeedsAction); err != nil {
		t.Fatalf("SetIssueState needs_action: %v", err)
	}
	if err := p.store.SetIssueUserActionReason(iss.ID, model.UserActionReasonQuestion); err != nil {
		t.Fatalf("SetIssueUserActionReason: %v", err)
	}
	mustUserActionReason(t, p, iss.Key, model.UserActionReasonQuestion, "manually stamped")

	// Walk it back to in_progress — the bacio-issue-state-driven path.
	if err := p.store.SetIssueState(iss.ID, model.StateInProgress); err != nil {
		t.Fatalf("SetIssueState in_progress: %v", err)
	}
	mustUserActionReason(t, p, iss.Key, "", "in_progress move should clear the reason")

	// Park it again, then move into a non-needs_action terminal — the
	// release / done path also clears.
	if err := p.store.SetIssueState(iss.ID, model.StateNeedsAction); err != nil {
		t.Fatalf("SetIssueState needs_action 2: %v", err)
	}
	if err := p.store.SetIssueUserActionReason(iss.ID, model.UserActionReasonQuestion); err != nil {
		t.Fatalf("SetIssueUserActionReason 2: %v", err)
	}
	if err := p.store.SetIssueState(iss.ID, model.StateDone); err != nil {
		t.Fatalf("SetIssueState done: %v", err)
	}
	mustUserActionReason(t, p, iss.Key, "", "done move should clear the reason")
}

// TestSetIssueStateNeedsActionPreservesPreviousReason verifies the
// SQL clause keeps the column untouched on a needs_action→needs_action
// no-op move. The auto-flip writes the reason via a SECOND statement
// after SetIssueState; the SQL fragment for the state move itself
// must not stomp it.
func TestSetIssueStateNeedsActionPreservesPreviousReason(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "preserve reason", "", model.StateInProgress, nil, "")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := p.store.SetIssueState(iss.ID, model.StateNeedsAction); err != nil {
		t.Fatalf("SetIssueState needs_action: %v", err)
	}
	if err := p.store.SetIssueUserActionReason(iss.ID, model.UserActionReasonQuestion); err != nil {
		t.Fatalf("SetIssueUserActionReason: %v", err)
	}
	// Re-issue the same state — common when a second auto-flip path
	// fires while the issue is already parked.
	if err := p.store.SetIssueState(iss.ID, model.StateNeedsAction); err != nil {
		t.Fatalf("SetIssueState needs_action again: %v", err)
	}
	mustUserActionReason(t, p, iss.Key, model.UserActionReasonQuestion, "re-issued needs_action must preserve the reason")
}

func mustUserActionReason(t *testing.T, p *pair, key string, want model.UserActionReasonType, msg string) {
	t.Helper()
	iss, err := p.local.GetIssueByKey(context.Background(), p.repo, key)
	if err != nil {
		t.Fatalf("%s: GetIssueByKey: %v", msg, err)
	}
	if iss.UserActionReasonType != want {
		t.Fatalf("%s: user_action_reason_type=%q, want %q", msg, iss.UserActionReasonType, want)
	}
}
