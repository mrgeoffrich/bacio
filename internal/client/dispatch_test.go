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
// composes the BACI-76 short payload: the rewritten preamble plus a
// tiny stub (ticket / mode / subagent type). The per-mode brief no
// longer lands in the payload — it is the subagent's system prompt.
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

	// A typed dispatch's payload carries the preamble + stub. No brief
	// body, no {{token}} placeholders — the stub names the ticket, mode,
	// and the subagent type the supervisor must spawn.
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
	for _, want := range []string{
		"<issue_id>" + iss.Key + "</issue_id>",
		"<mode>implement</mode>",
		"<subagent_type>" + model.SubagentTypeForTemplate(string(model.DispatchModeImplement)) + "</subagent_type>",
	} {
		if !strings.Contains(d.Payload, want) {
			t.Fatalf("default payload = %q, want it to contain %q", d.Payload, want)
		}
	}

	// The brief body never appears in the payload, even if the user
	// customised the template. Delete the preamble row so the payload
	// asserts to the bare stub; a separate test covers the
	// preamble-prepended shape (TestComposeDispatchPayload).
	if _, err := p.store.DeletePromptTemplate(model.BuiltinTemplatePreamble); err != nil {
		t.Fatalf("delete preamble: %v", err)
	}
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
	wantCustom := "<issue_id>" + iss.Key + "</issue_id>\n<mode>implement</mode>\n<subagent_type>" +
		model.SubagentTypeForTemplate(string(model.DispatchModeImplement)) + "</subagent_type>" +
		"\n\nwatch the migration"
	if d.Payload != wantCustom {
		t.Fatalf("custom payload = %q, want %q", d.Payload, wantCustom)
	}
	if strings.Contains(d.Payload, "Build "+iss.Key) {
		t.Fatalf("custom payload should NOT contain the brief body: %q", d.Payload)
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
		SessionID: "sess-lockstep", IssueKey: iss.Key, FinalState: string(model.StateInReview),
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

// TestEndAgentPresumedDeadWritesRequeueAudit (BACI-133) drives the full
// client path: register a session, attach a delivered dispatch, end the
// session with reason=presumed_dead, and assert (a) the dispatch flips
// back to queued and (b) bacio history grows an
// `agent.dispatch.requeue` row attributed to the bacio-channel-ping
// reaper actor. Locks in the audit-trail contract `bacio history --op
// agent.dispatch.requeue` (or --user-filter bacio-channel-ping) relies
// on. Runs against both the local and remote (HTTP) clients to make
// sure the api handler's cascade derivation matches the local one.
func TestEndAgentPresumedDeadWritesRequeueAudit(t *testing.T) {
	for _, mode := range []string{"local", "remote"} {
		t.Run(mode, func(t *testing.T) {
			p := newPair(t)
			defer p.cleanup()
			ctx := context.Background()
			c := p.local
			if mode == "remote" {
				c = p.remote
			}

			iss, err := p.store.CreateIssue(p.repo.ID, nil, "stuck on stale session", "", model.StateTodo, nil)
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			ag, _, err := p.store.UpsertAgent("going-dark-otter@claude.test-"+mode, true)
			if err != nil {
				t.Fatalf("UpsertAgent: %v", err)
			}
			// Use a structurally valid UUID so the BACI-100 register path
			// accepts it on the remote/HTTP side too. The mode suffix in
			// the last group keeps local/remote variants distinct.
			suffix := "1111"
			if mode == "remote" {
				suffix = "2222"
			}
			sid := "11111111-1111-4111-8111-1111111111" + suffix
			if _, err := p.store.UpsertAgentSession(store.UpsertAgentSessionIn{
				SessionID: sid, RepoID: p.repo.ID, AgentID: &ag.ID, Actor: "tester",
			}); err != nil {
				t.Fatalf("UpsertAgentSession: %v", err)
			}

			d, err := p.local.CreateDispatch(ctx, p.repo, inputs.AgentDispatchInput{
				TargetAgent: ag.Name,
				IssueKey:    iss.Key,
				Mode:        string(model.DispatchModeImplement),
				Message:     "do the thing",
			}, false)
			if err != nil {
				t.Fatalf("CreateDispatch: %v", err)
			}
			// Flip to delivered so the cascade has a non-queued source row
			// to operate on — mirrors the production state the reaper most
			// commonly catches.
			if _, err := p.store.DB.Exec(
				`UPDATE agent_dispatches SET status = 'delivered', target_session_id = ? WHERE id = ?`,
				sid, d.ID,
			); err != nil {
				t.Fatalf("flip to delivered: %v", err)
			}

			if _, err := c.EndAgent(ctx, p.repo, inputs.AgentEndInput{
				SessionID: sid,
				Reason:    string(model.EndReasonPresumedDead),
			}, false); err != nil {
				t.Fatalf("EndAgent presumed_dead: %v", err)
			}

			// Dispatch is back to queued.
			got, err := p.store.GetDispatch(d.ID)
			if err != nil {
				t.Fatalf("GetDispatch: %v", err)
			}
			if got.Status != model.DispatchQueued {
				t.Fatalf("dispatch status = %q, want queued (BACI-133 reaper recovery)", got.Status)
			}

			// Audit trail has the requeue row, attributed to the reaper.
			rows, err := p.store.ListHistory(store.HistoryFilter{
				RepoID: &p.repo.ID,
				Op:     "agent.dispatch.requeue",
			})
			if err != nil {
				t.Fatalf("ListHistory: %v", err)
			}
			if len(rows) != 1 {
				t.Fatalf("agent.dispatch.requeue rows = %d, want 1", len(rows))
			}
			row := rows[0]
			if row.Actor != model.IdlePingDispatchCreator {
				t.Fatalf("requeue row actor = %q, want %q", row.Actor, model.IdlePingDispatchCreator)
			}
			if !strings.Contains(row.Details, "auto-requeue") {
				t.Fatalf("requeue row Details = %q, missing 'auto-requeue' prefix", row.Details)
			}
			if !strings.Contains(row.Details, "issue="+iss.Key) {
				t.Fatalf("requeue row Details = %q, missing issue=%s clause", row.Details, iss.Key)
			}

			// And: no agent.cancel row for the same dispatch — the cancel
			// branch must not also fire for a reaper-driven end.
			cancelRows, err := p.store.ListHistory(store.HistoryFilter{
				RepoID: &p.repo.ID,
				Op:     "agent.cancel",
			})
			if err != nil {
				t.Fatalf("ListHistory(agent.cancel): %v", err)
			}
			for _, r := range cancelRows {
				if r.TargetID != nil && *r.TargetID == d.ID {
					t.Fatalf("unexpected agent.cancel row for requeued dispatch %d: %+v", d.ID, r)
				}
			}
		})
	}
}

// TestDrainDispatchesWritesDeliverAudit (BACI-160 gap 3) drives a
// pending dispatch through DrainDispatches and asserts the resulting
// pending→delivered transition writes an `agent.deliver` audit row.
// A second drain of the now-delivered dispatch must NOT write a
// second row — `agent.deliver` is the one-shot first-delivery
// moment, not "every drain that re-pushes a still-un-acked dispatch".
func TestDrainDispatchesWritesDeliverAudit(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "deliver me", "", model.StateTodo, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	ag, _, err := p.store.UpsertAgent("delivery-otter@claude.test", true)
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	const sid = "deeefeed-1111-4111-8111-111111111111"
	sess, err := p.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: sid, RepoID: p.repo.ID, AgentID: &ag.ID, Actor: "tester",
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

	drained, err := p.local.DrainDispatches(ctx, sess.SessionID)
	if err != nil {
		t.Fatalf("DrainDispatches: %v", err)
	}
	if len(drained) != 1 || drained[0].Status != model.DispatchDelivered {
		t.Fatalf("drained = %+v, want one delivered row", drained)
	}

	rows, err := p.store.ListHistory(store.HistoryFilter{
		RepoID: &p.repo.ID, Op: "agent.deliver",
	})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("agent.deliver rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Kind != "agent" {
		t.Fatalf("deliver row Kind = %q, want agent", row.Kind)
	}
	if row.TargetID == nil || *row.TargetID != d.ID {
		t.Fatalf("deliver row TargetID = %v, want %d", row.TargetID, d.ID)
	}
	if !strings.Contains(row.Details, "status=delivered") {
		t.Fatalf("deliver row Details = %q, missing status=delivered", row.Details)
	}

	// Second drain: dispatch is already delivered, so the loop's
	// pending-branch should not run and no new audit row should land.
	if _, err := p.local.DrainDispatches(ctx, sess.SessionID); err != nil {
		t.Fatalf("DrainDispatches second: %v", err)
	}
	rows2, err := p.store.ListHistory(store.HistoryFilter{
		RepoID: &p.repo.ID, Op: "agent.deliver",
	})
	if err != nil {
		t.Fatalf("ListHistory after second drain: %v", err)
	}
	if len(rows2) != 1 {
		t.Fatalf("agent.deliver rows after second drain = %d, want 1 (one-shot)", len(rows2))
	}
}

// TestAbandonOpenQuestionsWritesAudit (BACI-160 gap 4): a channel-
// startup sweep that finds N>0 open questions writes one summary
// `question.abandon` row carrying the session id and count; a sweep
// that finds zero rows produces no row.
func TestAbandonOpenQuestionsWritesAudit(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "parked work", "", model.StateInProgress, nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	ag, _, err := p.store.UpsertAgent("asking-otter@claude.test", true)
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	const sid = "abadaba0-1111-4111-8111-111111111111"
	if _, err := p.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: sid, RepoID: p.repo.ID, AgentID: &ag.ID, Actor: "tester",
	}); err != nil {
		t.Fatalf("UpsertAgentSession: %v", err)
	}
	// Open a question through the client wrapper so the standard
	// question.ask audit row is in place — keeps the assertions on
	// the new row honest.
	if _, err := p.local.AddSessionQuestion(ctx, client.AddSessionQuestionInput{
		SessionID: sid,
		IssueKey:  iss.Key,
		AskedBy:   "asking-otter@claude.test",
		Payload: model.QuestionPayload{
			Questions: []model.QuestionItem{{
				Question:    "should we proceed?",
				Header:      "Approval",
				MultiSelect: model.MultiSelectFlag(false),
				Options: []model.QuestionOption{
					{Label: "yes", Description: "proceed"},
					{Label: "no", Description: "stop"},
				},
			}},
		},
	}); err != nil {
		t.Fatalf("AddSessionQuestion: %v", err)
	}

	n, err := p.local.AbandonOpenQuestionsForSession(ctx, sid)
	if err != nil {
		t.Fatalf("AbandonOpenQuestionsForSession: %v", err)
	}
	if n != 1 {
		t.Fatalf("abandoned = %d, want 1", n)
	}

	rows, err := p.store.ListHistory(store.HistoryFilter{Op: "question.abandon"})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("question.abandon rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Kind != "question" {
		t.Fatalf("abandon row Kind = %q, want question", row.Kind)
	}
	if row.TargetLabel != sid {
		t.Fatalf("abandon row TargetLabel = %q, want %q", row.TargetLabel, sid)
	}
	if !strings.Contains(row.Details, "count=1") {
		t.Fatalf("abandon row Details = %q, missing count=1", row.Details)
	}

	// A second sweep finds zero open questions and must NOT write
	// another row — the no-op gate is the whole point of the N>0
	// guard.
	if _, err := p.local.AbandonOpenQuestionsForSession(ctx, sid); err != nil {
		t.Fatalf("second AbandonOpenQuestionsForSession: %v", err)
	}
	rows2, _ := p.store.ListHistory(store.HistoryFilter{Op: "question.abandon"})
	if len(rows2) != 1 {
		t.Fatalf("question.abandon rows after no-op sweep = %d, want 1", len(rows2))
	}
}
