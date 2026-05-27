package client_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestQuestionAutoFlipOpensThenAnswers covers the happy path: an
// in_progress issue auto-flips to needs_action on AddSessionQuestion,
// then back to in_progress on AnswerSessionQuestion.
func TestQuestionAutoFlipOpensThenAnswers(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, sess := setupClaimedIssue(t, p, "sess-aflip-1", model.StateInProgress)

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

	if _, err := p.local.AnswerSessionQuestion(ctx, q.ID, model.QuestionAnswers{
		"Pick one?": "A",
	}, false); err != nil {
		t.Fatalf("AnswerSessionQuestion: %v", err)
	}
	mustState(t, p, iss.Key, model.StateInProgress, "after answer")
}

// TestQuestionAutoFlipCancel covers the same path but with cancel
// instead of answer.
func TestQuestionAutoFlipCancel(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, sess := setupClaimedIssue(t, p, "sess-aflip-2", model.StateInProgress)

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

	if _, err := p.local.CancelSessionQuestion(ctx, q.ID, false); err != nil {
		t.Fatalf("CancelSessionQuestion: %v", err)
	}
	mustState(t, p, iss.Key, model.StateInProgress, "after cancel")
}

// TestQuestionAutoFlipMultipleOpenDoesNotFlipBackEarly verifies that
// answering one question while another is still open leaves the
// issue in needs_action. Only the last resolution flips back.
func TestQuestionAutoFlipMultipleOpenDoesNotFlipBackEarly(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, sess := setupClaimedIssue(t, p, "sess-aflip-3", model.StateInProgress)

	q1, err := p.local.AddSessionQuestion(ctx, client.AddSessionQuestionInput{
		SessionID: sess.SessionID, IssueKey: iss.Key,
		Payload: simplePayload(), AskedBy: "agent-x@claude.test",
	})
	if err != nil {
		t.Fatalf("AddSessionQuestion 1: %v", err)
	}
	q2, err := p.local.AddSessionQuestion(ctx, client.AddSessionQuestionInput{
		SessionID: sess.SessionID, IssueKey: iss.Key,
		Payload: simplePayload(), AskedBy: "agent-x@claude.test",
	})
	if err != nil {
		t.Fatalf("AddSessionQuestion 2: %v", err)
	}
	mustState(t, p, iss.Key, model.StateNeedsAction, "after two asks")

	if _, err := p.local.AnswerSessionQuestion(ctx, q1.ID, model.QuestionAnswers{"Pick one?": "A"}, false); err != nil {
		t.Fatalf("AnswerSessionQuestion 1: %v", err)
	}
	mustState(t, p, iss.Key, model.StateNeedsAction, "after resolving 1 of 2")

	if _, err := p.local.AnswerSessionQuestion(ctx, q2.ID, model.QuestionAnswers{"Pick one?": "B"}, false); err != nil {
		t.Fatalf("AnswerSessionQuestion 2: %v", err)
	}
	mustState(t, p, iss.Key, model.StateInProgress, "after resolving last open")
}

// TestQuestionAutoFlipSkipsWhenIssueNotInProgress asserts the open
// flip is a no-op when the issue is in any state other than
// in_progress — we don't yank a `todo` or `done` row into needs_action.
func TestQuestionAutoFlipSkipsWhenIssueNotInProgress(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, sess := setupClaimedIssue(t, p, "sess-aflip-4", model.StateTodo)

	if _, err := p.local.AddSessionQuestion(ctx, client.AddSessionQuestionInput{
		SessionID: sess.SessionID, IssueKey: iss.Key,
		Payload: simplePayload(), AskedBy: "agent-x@claude.test",
	}); err != nil {
		t.Fatalf("AddSessionQuestion: %v", err)
	}
	mustState(t, p, iss.Key, model.StateTodo, "todo issue should not flip")
}

// TestAddSessionQuestionRejectsEmptyIssueKey locks in BACI-128:
// the client-side guard rejects an empty issue_key at the
// boundary, so a buggy future caller (or a regressed channel)
// cannot insert an orphan row that the kanban-card surface would
// never light up on. Replaces the pre-BACI-128
// TestQuestionAutoFlipWithoutIssueKey, which exercised the
// dropped open-claim-best-effort path.
func TestAddSessionQuestionRejectsEmptyIssueKey(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	ag, _, err := p.store.UpsertAgent("agent-x@claude.test", true)
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	sess, err := p.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: "sess-aflip-5", RepoID: p.repo.ID, AgentID: &ag.ID, Actor: "tester",
	})
	if err != nil {
		t.Fatalf("UpsertAgentSession: %v", err)
	}

	_, err = p.local.AddSessionQuestion(ctx, client.AddSessionQuestionInput{
		SessionID: sess.SessionID, IssueKey: "", Payload: simplePayload(),
		AskedBy: "agent-x@claude.test",
	})
	if err == nil {
		t.Fatalf("AddSessionQuestion with empty IssueKey must error (BACI-128 boundary guard)")
	}
	if !strings.Contains(err.Error(), "issue_key is required") {
		t.Fatalf("AddSessionQuestion empty-issue error %q should mention issue_key requirement", err.Error())
	}

	// Belt-and-braces: a malformed-but-non-empty issue_key trips the
	// same boundary and never reaches the store.
	_, err = p.local.AddSessionQuestion(ctx, client.AddSessionQuestionInput{
		SessionID: sess.SessionID, IssueKey: "BACI-foo", Payload: simplePayload(),
		AskedBy: "agent-x@claude.test",
	})
	if err == nil {
		t.Fatalf("AddSessionQuestion with malformed IssueKey must error (BACI-128 boundary guard)")
	}
}

// setupClaimedIssue creates one issue + a session that has claimed it,
// returning both. The issue starts in the requested state — pass
// StateInProgress to mimic the post-claim+state-flip steady state, or
// StateTodo to test the "not-eligible-for-autoflip" branch.
func setupClaimedIssue(t *testing.T, p *pair, sessionID string, state model.State) (*model.Issue, *model.AgentSession) {
	t.Helper()
	iss, err := p.store.CreateIssue(p.repo.ID, nil, "auto-flip target", "", state, nil, "")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	ag, _, err := p.store.UpsertAgent("agent-x@claude.test", true)
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	sess, err := p.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: sessionID, RepoID: p.repo.ID, AgentID: &ag.ID, Actor: "tester",
	})
	if err != nil {
		t.Fatalf("UpsertAgentSession: %v", err)
	}
	if _, _, _, _, err := p.store.AddAgentClaim(sess.SessionID, iss.ID, ""); err != nil {
		t.Fatalf("AddAgentClaim: %v", err)
	}
	// BACI-126a: AddAgentClaim auto-moves the issue to in_progress, but
	// these tests need to verify auto-flip behaviour across the full
	// state space. Stamp the requested state back after the claim so the
	// fixture starts in the state the test asks for, regardless of the
	// claim's implicit transition.
	if state != model.StateInProgress {
		if err := p.store.SetIssueState(iss.ID, state); err != nil {
			t.Fatalf("SetIssueState: %v", err)
		}
		iss.State = state
	}
	return iss, sess
}

func simplePayload() model.QuestionPayload {
	return model.QuestionPayload{
		Questions: []model.QuestionItem{{
			Question:    "Pick one?",
			Header:      "Pick",
			MultiSelect: model.MultiSelectFlag(false),
			Options: []model.QuestionOption{
				{Label: "A", Description: "first option"},
				{Label: "B", Description: "second option"},
			},
		}},
	}
}

func mustState(t *testing.T, p *pair, key string, want model.State, msg string) {
	t.Helper()
	iss, err := p.local.GetIssueByKey(context.Background(), p.repo, key)
	if err != nil {
		t.Fatalf("%s: GetIssueByKey: %v", msg, err)
	}
	if iss.State != want {
		t.Fatalf("%s: state=%s, want %s", msg, iss.State, want)
	}
}
