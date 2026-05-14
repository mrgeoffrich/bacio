package client

import (
	"context"
	"errors"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// TestDispatchLifecycleLocal walks the full local-backend dispatch
// path: create -> inbox -> ack, asserting the audit-relevant fields
// land and the inbox drains the right rows.
func TestDispatchLifecycleLocal(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "fix the thing", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	ag, _, err := p.store.UpsertAgent("swift-otter@claude.test", true)
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	sess, err := p.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: "sess-disp-1", RepoID: p.repo.ID, AgentID: &ag.ID, Actor: "tester",
	})
	if err != nil {
		t.Fatalf("UpsertAgentSession: %v", err)
	}

	d, err := p.local.CreateDispatch(ctx, p.repo, inputs.AgentDispatchInput{
		TargetAgent: ag.Name,
		IssueKey:    iss.Key,
		Message:     "please pick this up",
	}, false)
	if err != nil {
		t.Fatalf("CreateDispatch: %v", err)
	}
	if d.Status != model.DispatchPending || d.IssueKey != iss.Key {
		t.Fatalf("dispatch = %+v", d)
	}

	inbox, err := p.local.InboxDispatches(ctx, sess.SessionID)
	if err != nil {
		t.Fatalf("InboxDispatches: %v", err)
	}
	if len(inbox) != 1 || inbox[0].ID != d.ID {
		t.Fatalf("inbox = %+v, want the one dispatch", inbox)
	}

	acked, err := p.local.AckDispatch(ctx, inputs.AgentAckInput{ID: d.ID, Note: "done"}, false)
	if err != nil {
		t.Fatalf("AckDispatch: %v", err)
	}
	if acked.Status != model.DispatchAcked || acked.AckNote != "done" {
		t.Fatalf("acked = %+v", acked)
	}

	// Acked dispatches drop out of the inbox.
	inbox, err = p.local.InboxDispatches(ctx, sess.SessionID)
	if err != nil {
		t.Fatalf("InboxDispatches after ack: %v", err)
	}
	if len(inbox) != 0 {
		t.Fatalf("inbox after ack = %+v, want empty", inbox)
	}
}

// TestDispatchDryRunLocal checks that a dry-run create projects a
// pending dispatch without persisting it.
func TestDispatchDryRunLocal(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	ag, _, err := p.store.UpsertAgent("quiet-lynx@claude.test", true)
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	d, err := p.local.CreateDispatch(ctx, p.repo, inputs.AgentDispatchInput{
		TargetAgent: ag.Name,
		Message:     "dry run",
	}, true)
	if err != nil {
		t.Fatalf("CreateDispatch dry-run: %v", err)
	}
	if d.Status != model.DispatchPending {
		t.Fatalf("dry-run status = %q", d.Status)
	}
	if d.ID != 0 {
		t.Fatalf("dry-run dispatch has id %d, want 0 (not persisted)", d.ID)
	}
	if got, _ := p.store.ListDispatches(store.DispatchFilter{}); len(got) != 0 {
		t.Fatalf("dry-run persisted %d dispatches, want 0", len(got))
	}
}

// TestDispatchPromptTemplateRendering checks that CreateDispatch
// resolves the stage's prompt template, substitutes the issue context
// into its placeholders, and appends the free-form note — both for the
// built-in default and a user's custom override.
func TestDispatchPromptTemplateRendering(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "fix the thing", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	ag, _, err := p.store.UpsertAgent("swift-otter@claude.test", true)
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}

	// No custom template stored → the built-in default, with {{issue_id}}
	// substituted for the canonical key.
	d, err := p.local.CreateDispatch(ctx, p.repo, inputs.AgentDispatchInput{
		TargetAgent: ag.Name,
		IssueKey:    iss.Key,
		Mode:        string(model.DispatchModeImplement),
	}, false)
	if err != nil {
		t.Fatalf("CreateDispatch (default): %v", err)
	}
	wantDefault := "Implement " + iss.Key + " end-to-end."
	if d.Payload != wantDefault {
		t.Fatalf("default payload = %q, want %q", d.Payload, wantDefault)
	}

	// Store a custom template, then dispatch with a note: the custom
	// body is rendered and the note is appended after a blank line.
	if err := p.local.SetPromptTemplate(ctx, string(model.DispatchModeImplement), "Build {{issue_id}} for {{repo_prefix}}.", false); err != nil {
		t.Fatalf("SetPromptTemplate: %v", err)
	}
	d, err = p.local.CreateDispatch(ctx, p.repo, inputs.AgentDispatchInput{
		TargetAgent: ag.Name,
		IssueKey:    iss.Key,
		Mode:        string(model.DispatchModeImplement),
		Message:     "watch the migration",
	}, false)
	if err != nil {
		t.Fatalf("CreateDispatch (custom): %v", err)
	}
	wantCustom := "Build " + iss.Key + " for " + p.repo.Prefix + ".\n\nwatch the migration"
	if d.Payload != wantCustom {
		t.Fatalf("custom payload = %q, want %q", d.Payload, wantCustom)
	}

	// GetPromptTemplates reflects the override; the other stages stay
	// on their defaults.
	tmpls, err := p.local.GetPromptTemplates(ctx)
	if err != nil {
		t.Fatalf("GetPromptTemplates: %v", err)
	}
	if tmpls[string(model.DispatchModeImplement)] != "Build {{issue_id}} for {{repo_prefix}}." {
		t.Fatalf("implement template = %q, want the custom body", tmpls[string(model.DispatchModeImplement)])
	}
	if tmpls[string(model.DispatchModeReview)] != model.DefaultPromptTemplate(model.DispatchModeReview) {
		t.Fatalf("review template = %q, want the built-in default", tmpls[string(model.DispatchModeReview)])
	}
}

// TestSetPromptTemplateDryRun checks that a dry-run set validates the
// mode and body but writes nothing, while a bad body still errors.
func TestSetPromptTemplateDryRun(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	// A valid dry-run set returns nil and persists nothing.
	if err := p.local.SetPromptTemplate(ctx, string(model.DispatchModePlan), "Plan {{issue_id}} carefully.", true); err != nil {
		t.Fatalf("SetPromptTemplate dry-run: %v", err)
	}
	tmpls, err := p.local.GetPromptTemplates(ctx)
	if err != nil {
		t.Fatalf("GetPromptTemplates: %v", err)
	}
	if tmpls[string(model.DispatchModePlan)] != model.DefaultPromptTemplate(model.DispatchModePlan) {
		t.Fatalf("dry-run persisted the plan template: %q", tmpls[string(model.DispatchModePlan)])
	}

	// Validation still runs under dry-run — a control-char body errors.
	if err := p.local.SetPromptTemplate(ctx, string(model.DispatchModePlan), "bad\x00body", true); err == nil {
		t.Fatal("SetPromptTemplate dry-run with control char = nil, want error")
	}
}

// TestDispatchRemoteNotSupported locks in that the remote backend
// refuses dispatch verbs with ErrLocalOnly (the registry is local-only
// in v1).
func TestDispatchRemoteNotSupported(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	_, err := p.remote.CreateDispatch(ctx, p.repo, inputs.AgentDispatchInput{TargetAgent: "x"}, false)
	if !errors.Is(err, ErrLocalOnly) {
		t.Fatalf("remote CreateDispatch err = %v, want ErrLocalOnly", err)
	}
	if _, err := p.remote.InboxDispatches(ctx, "sess-x"); !errors.Is(err, ErrLocalOnly) {
		t.Fatalf("remote InboxDispatches err = %v, want ErrLocalOnly", err)
	}
	if _, err := p.remote.AckDispatch(ctx, inputs.AgentAckInput{ID: 1}, false); !errors.Is(err, ErrLocalOnly) {
		t.Fatalf("remote AckDispatch err = %v, want ErrLocalOnly", err)
	}
}
