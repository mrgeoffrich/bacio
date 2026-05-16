package client_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
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
	// substituted for the canonical key. The default text itself lives in
	// editable data files (internal/model/prompttemplates), so assert on
	// the resolve-and-substitute contract, not the exact wording.
	d, err := p.local.CreateDispatch(ctx, p.repo, inputs.AgentDispatchInput{
		TargetAgent: ag.Name,
		IssueKey:    iss.Key,
		Mode:        string(model.DispatchModeImplement),
	}, false)
	if err != nil {
		t.Fatalf("CreateDispatch (default): %v", err)
	}
	if d.Payload == "" || strings.Contains(d.Payload, "{{") {
		t.Fatalf("default payload not resolved/substituted: %q", d.Payload)
	}
	if !strings.Contains(d.Payload, iss.Key) {
		t.Fatalf("default payload = %q, want it to mention issue %s", d.Payload, iss.Key)
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

// TestBoardPreferencesLocal checks the local-backend round-trip for the
// desktop Board preferences: the default is hide-off, a real set
// persists, a dry-run set writes nothing, and the remote backend
// refuses (Board preferences are local-only).
func TestBoardPreferencesLocal(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	// Default — nothing stored yet — is hide-empty-columns off.
	prefs, err := p.local.GetBoardPreferences(ctx)
	if err != nil {
		t.Fatalf("GetBoardPreferences: %v", err)
	}
	if prefs.HideEmptyColumns {
		t.Fatalf("default HideEmptyColumns = true, want false")
	}

	// A real set persists.
	if err := p.local.SetBoardPreferences(ctx, client.BoardPreferences{HideEmptyColumns: true}, false); err != nil {
		t.Fatalf("SetBoardPreferences: %v", err)
	}
	prefs, err = p.local.GetBoardPreferences(ctx)
	if err != nil {
		t.Fatalf("GetBoardPreferences after set: %v", err)
	}
	if !prefs.HideEmptyColumns {
		t.Fatalf("HideEmptyColumns = false after set true, want true")
	}

	// A dry-run set writes nothing — the stored value stays put.
	if err := p.local.SetBoardPreferences(ctx, client.BoardPreferences{HideEmptyColumns: false}, true); err != nil {
		t.Fatalf("SetBoardPreferences dry-run: %v", err)
	}
	prefs, err = p.local.GetBoardPreferences(ctx)
	if err != nil {
		t.Fatalf("GetBoardPreferences after dry-run: %v", err)
	}
	if !prefs.HideEmptyColumns {
		t.Fatalf("dry-run set persisted: HideEmptyColumns = false, want still true")
	}

	// The remote backend refuses — Board preferences are local-only.
	if _, err := p.remote.GetBoardPreferences(ctx); !errors.Is(err, client.ErrLocalOnly) {
		t.Fatalf("remote GetBoardPreferences err = %v, want ErrLocalOnly", err)
	}
	if err := p.remote.SetBoardPreferences(ctx, client.BoardPreferences{}, false); !errors.Is(err, client.ErrLocalOnly) {
		t.Fatalf("remote SetBoardPreferences err = %v, want ErrLocalOnly", err)
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

// TestClaimReleaseKeepsAssigneeInLockstep locks in BACI-27 at the
// client layer: ClaimAgent stamps the issue's assignee with the
// claiming identity and records an issue.assign audit row alongside
// agent.claim; ReleaseAgent clears it and records the inverse.
func TestClaimReleaseKeepsAssigneeInLockstep(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "lockstep", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	ag, _, err := p.store.UpsertAgent("lockstep-fox@claude.test", true)
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	if _, err := p.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: "sess-lockstep", RepoID: p.repo.ID, AgentID: &ag.ID, Actor: "tester",
	}); err != nil {
		t.Fatalf("UpsertAgentSession: %v", err)
	}

	assignRows := func() int {
		t.Helper()
		rows, err := p.store.ListHistory(store.HistoryFilter{RepoID: &p.repo.ID, Op: "issue.assign"})
		if err != nil {
			t.Fatalf("ListHistory: %v", err)
		}
		return len(rows)
	}

	if _, err := p.local.ClaimAgent(ctx, p.repo, inputs.AgentClaimInput{
		SessionID: "sess-lockstep", IssueKey: iss.Key,
	}, false); err != nil {
		t.Fatalf("ClaimAgent: %v", err)
	}
	got, err := p.store.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("GetIssueByID: %v", err)
	}
	if got.Assignee != "lockstep-fox@claude.test" {
		t.Fatalf("after claim, assignee = %q, want lockstep-fox@claude.test", got.Assignee)
	}
	if n := assignRows(); n != 1 {
		t.Fatalf("issue.assign audit rows after claim = %d, want 1", n)
	}

	if _, err := p.local.ReleaseAgent(ctx, p.repo, inputs.AgentReleaseInput{
		SessionID: "sess-lockstep", IssueKey: iss.Key,
	}, false); err != nil {
		t.Fatalf("ReleaseAgent: %v", err)
	}
	got, err = p.store.GetIssueByID(iss.ID)
	if err != nil {
		t.Fatalf("GetIssueByID: %v", err)
	}
	if got.Assignee != "" {
		t.Fatalf("after release, assignee = %q, want empty", got.Assignee)
	}
	if n := assignRows(); n != 2 {
		t.Fatalf("issue.assign audit rows after release = %d, want 2", n)
	}
}

// TestDispatchRemoteNotSupported locks in which dispatch-side verbs
// are still local-only after BACI-34 landed inbox/ack and BACI-35
// landed create + list-per-repo over HTTP. Only the side-effect-bearing
// drain path (used by the hook against the local store directly) is
// left without a REST analogue.
func TestDispatchRemoteNotSupported(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	if _, err := p.remote.DrainDispatches(ctx, "sess-x"); !errors.Is(err, client.ErrLocalOnly) {
		t.Fatalf("remote DrainDispatches err = %v, want ErrLocalOnly", err)
	}
}

// TestRoundTripDispatchCreate exercises the BACI-35 additions —
// CreateDispatch and RepoDispatches — through the remote backend. The
// inbox/ack round-trip is covered separately by BACI-34's
// TestRoundTripAgentLifecycle.
func TestRoundTripDispatchCreate(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "ship it", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	ag, _, err := p.store.UpsertAgent("swift-otter@claude.test", true)
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}

	// Dry-run via REST: get a projected dispatch, write nothing.
	proj, err := p.remote.CreateDispatch(ctx, p.repo, inputs.AgentDispatchInput{
		TargetAgent: ag.Name,
		IssueKey:    iss.Key,
		Mode:        string(model.DispatchModeImplement),
		Message:     "preview",
	}, true)
	if err != nil {
		t.Fatalf("remote CreateDispatch dry-run: %v", err)
	}
	if proj.ID != 0 {
		t.Errorf("dry-run dispatch id = %d, want 0 (server-time field)", proj.ID)
	}
	if list, _ := p.remote.RepoDispatches(ctx, p.repo); len(list) != 0 {
		t.Fatalf("dry-run wrote %d dispatch(es), want 0", len(list))
	}

	// Real create via REST.
	d, err := p.remote.CreateDispatch(ctx, p.repo, inputs.AgentDispatchInput{
		TargetAgent: ag.Name,
		IssueKey:    iss.Key,
		Mode:        string(model.DispatchModeImplement),
		Message:     "go",
	}, false)
	if err != nil {
		t.Fatalf("remote CreateDispatch: %v", err)
	}
	if d.ID == 0 || d.Status != model.DispatchPending {
		t.Fatalf("created dispatch = %+v", d)
	}
	if d.TargetAgentName != ag.Name || d.IssueKey != iss.Key {
		t.Fatalf("created dispatch = %+v", d)
	}

	// RepoDispatches sees it.
	list, err := p.remote.RepoDispatches(ctx, p.repo)
	if err != nil {
		t.Fatalf("remote RepoDispatches: %v", err)
	}
	if len(list) != 1 || list[0].ID != d.ID {
		t.Fatalf("RepoDispatches = %+v, want [%d]", list, d.ID)
	}
}
