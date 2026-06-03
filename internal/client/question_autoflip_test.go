package client_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestQuestionDoesNotMoveIssueState locks in the BACI-300 retirement of
// the legacy in_progress→needs_action auto-flip: opening, answering, and
// cancelling a question leave the claimed issue's state exactly where it
// was. The "agent is blocked on a question" signal now lives on the
// kanban-card question pill (and, for a pipeline card, the engine's
// open_question pause), not in a state move.
func TestQuestionDoesNotMoveIssueState(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, sess := setupClaimedIssue(t, p, "sess-aflip-1", model.StateTodo)

	q, err := p.local.AddSessionQuestion(ctx, client.AddSessionQuestionInput{
		SessionID: sess.SessionID,
		IssueKey:  iss.Key,
		Payload:   simplePayload(),
		AskedBy:   "agent-x@claude.test",
	})
	if err != nil {
		t.Fatalf("AddSessionQuestion: %v", err)
	}
	mustState(t, p, iss.Key, model.StateTodo, "after ask")

	if _, err := p.local.AnswerSessionQuestion(ctx, q.ID, model.QuestionAnswers{
		"Pick one?": "A",
	}, false); err != nil {
		t.Fatalf("AnswerSessionQuestion: %v", err)
	}
	mustState(t, p, iss.Key, model.StateTodo, "after answer")
}

// TestQuestionCancelDoesNotMoveIssueState covers the same no-move
// guarantee on the cancel path.
func TestQuestionCancelDoesNotMoveIssueState(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, sess := setupClaimedIssue(t, p, "sess-aflip-2", model.StateInReview)

	q, err := p.local.AddSessionQuestion(ctx, client.AddSessionQuestionInput{
		SessionID: sess.SessionID,
		IssueKey:  iss.Key,
		Payload:   simplePayload(),
		AskedBy:   "agent-x@claude.test",
	})
	if err != nil {
		t.Fatalf("AddSessionQuestion: %v", err)
	}
	mustState(t, p, iss.Key, model.StateInReview, "after ask")

	if _, err := p.local.CancelSessionQuestion(ctx, q.ID, false); err != nil {
		t.Fatalf("CancelSessionQuestion: %v", err)
	}
	mustState(t, p, iss.Key, model.StateInReview, "after cancel")
}

// TestAddSessionQuestionRejectsEmptyIssueKey locks in BACI-128:
// the client-side guard rejects an empty issue_key at the
// boundary, so a buggy future caller (or a regressed channel)
// cannot insert an orphan row that the kanban-card surface would
// never light up on.
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
// returning both. The issue starts (and, since BACI-300's state-neutral
// claim, stays) in the requested state.
func setupClaimedIssue(t *testing.T, p *pair, sessionID string, state model.State) (*model.Issue, *model.AgentSession) {
	t.Helper()
	iss, err := p.store.CreateIssue(p.repo.ID, nil, "auto-flip target", "", state, nil, "", "")
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
	// BACI-300: AddAgentClaim is state-neutral, so the issue stays in the
	// requested state after the claim — no need to stamp it back.
	if _, _, _, _, err := p.store.AddAgentClaim(sess.SessionID, iss.ID, ""); err != nil {
		t.Fatalf("AddAgentClaim: %v", err)
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
