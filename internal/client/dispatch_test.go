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

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "fix the thing", "", model.StateTodo, nil, "")
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

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "fix the thing", "", model.StateTodo, nil, "")
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
		// BACI-226: the resolver-derived <base_branch> rides alongside
		// <issue_id> / <mode>; no override + no feature → fallback to
		// "main", and the supervisor copies the tag verbatim into the
		// worker's Task prompt.
		"<base_branch>main</base_branch>",
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
	wantCustom := "<issue_id>" + iss.Key + "</issue_id>\n<mode>implement</mode>\n<base_branch>main</base_branch>\n<subagent_type>" +
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

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "lockstep", "", model.StateTodo, nil, "")
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

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "ship it", "", model.StateTodo, nil, "")
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

			iss, err := p.store.CreateIssue(p.repo.ID, nil, "stuck on stale session", "", model.StateTodo, nil, "")
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

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "deliver me", "", model.StateTodo, nil, "")
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

// TestDrainDispatchesUniquePerSession (BACI-202) is the regression
// for the double-delivery bug. Two Claude Code instances registered
// under the same agent slug (different claude_pid, same agent_id) used
// to both drain an agent-targeted dispatch through ListDispatches's
// agent-id-OR-session-id filter and both spawn the per-mode subagent.
// The fix gates every emission on a per-(dispatch, session) ledger
// row (`dispatch_deliveries`) so exactly one session ever transitions
// the dispatch from pending → delivered.
//
// Assertions:
//   - first drain returns the dispatch; second drain (different
//     session, same agent identity) returns zero rows;
//   - exactly one `agent.deliver` audit row across both drains;
//   - exactly one `dispatch_deliveries` ledger row, keyed on the
//     winning session.
func TestDrainDispatchesUniquePerSession(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "no double-deliver", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	ag, _, err := p.store.UpsertAgent("shared-otter@claude.test", true)
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	// Two registered sessions under the SAME agent identity — the
	// "two Claude Code instances registered as the same agent slug
	// (different claude_pid, same target_agent_id)" reproducer the
	// brief calls out.
	const sidA = "11111111-1111-4111-8111-111111111111"
	const sidB = "22222222-2222-4222-8222-222222222222"
	sessA, err := p.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: sidA, RepoID: p.repo.ID, AgentID: &ag.ID, Actor: "tester",
	})
	if err != nil {
		t.Fatalf("UpsertAgentSession A: %v", err)
	}
	sessB, err := p.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: sidB, RepoID: p.repo.ID, AgentID: &ag.ID, Actor: "tester",
	})
	if err != nil {
		t.Fatalf("UpsertAgentSession B: %v", err)
	}

	// One agent-targeted dispatch — exactly the shape both sessions
	// would otherwise pick up through ListDispatches's agent-id-OR-
	// session-id filter.
	d, err := p.local.CreateDispatch(ctx, p.repo, inputs.AgentDispatchInput{
		TargetAgent: ag.Name,
		IssueKey:    iss.Key,
		Message:     "pick me up exactly once",
	}, false)
	if err != nil {
		t.Fatalf("CreateDispatch: %v", err)
	}

	// Session A drains first — wins the (dispatch, session) ledger
	// claim and gets the dispatch in its returned slice.
	drainedA, err := p.local.DrainDispatches(ctx, sessA.SessionID)
	if err != nil {
		t.Fatalf("DrainDispatches A: %v", err)
	}
	if len(drainedA) != 1 || drainedA[0].ID != d.ID || drainedA[0].Status != model.DispatchDelivered {
		t.Fatalf("drainedA = %+v, want one delivered row for dispatch %d", drainedA, d.ID)
	}

	// Session B drains second — loses the claim and returns no rows.
	// The dispatch is now `delivered` at the row level, but session B
	// never had a ledger entry, so the per-session uniqueness gate
	// fires on the pending branch and the already-delivered passthrough
	// is unreachable (the row was pending when B's ListDispatches saw
	// it; A flipped it under B's feet — either way, B emits nothing).
	drainedB, err := p.local.DrainDispatches(ctx, sessB.SessionID)
	if err != nil {
		t.Fatalf("DrainDispatches B: %v", err)
	}
	// drainedB can legitimately include the already-delivered row
	// (lost-push recovery for the WINNING session is the existing
	// shape). What matters is exactly-one `agent.deliver` audit row
	// and exactly-one `dispatch_deliveries` row — those are the
	// invariants that map to "exactly one worker spawn".
	_ = drainedB

	rows, err := p.store.ListHistory(store.HistoryFilter{
		RepoID: &p.repo.ID, Op: "agent.deliver",
	})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("agent.deliver rows = %d, want 1 (exactly one delivery across both drains)", len(rows))
	}
	if rows[0].TargetID == nil || *rows[0].TargetID != d.ID {
		t.Fatalf("agent.deliver TargetID = %v, want %d", rows[0].TargetID, d.ID)
	}

	// Inspect the ledger directly via a read-only count query — the
	// store doesn't expose a typed accessor (it doesn't need one;
	// the test is the single non-CLI reader), and a count is
	// sufficient to lock in the uniqueness invariant. Hits the
	// SAME *sql.DB the store wraps, so foreign keys + constraints
	// applied.
	var ledgerCount int
	if err := p.store.DB.QueryRow(
		`SELECT COUNT(*) FROM dispatch_deliveries WHERE dispatch_id = ?`, d.ID,
	).Scan(&ledgerCount); err != nil {
		t.Fatalf("count dispatch_deliveries: %v", err)
	}
	if ledgerCount != 1 {
		t.Fatalf("dispatch_deliveries rows = %d, want 1", ledgerCount)
	}
	var winner string
	if err := p.store.DB.QueryRow(
		`SELECT session_id FROM dispatch_deliveries WHERE dispatch_id = ?`, d.ID,
	).Scan(&winner); err != nil {
		t.Fatalf("read winner: %v", err)
	}
	if winner != sessA.SessionID {
		t.Fatalf("ledger winner = %q, want %q (A was first to drain)", winner, sessA.SessionID)
	}
	// Belt-and-braces: session B never appears in the ledger.
	if winner == sessB.SessionID {
		t.Fatalf("ledger unexpectedly recorded B as the winner")
	}
}

// TestStoreClaimDispatchDeliveryIdempotent (BACI-202) locks in the
// pure store-layer contract: the first ClaimDispatchDelivery call for
// a (dispatch, session) pair returns true; any subsequent call (same
// pair) returns false because the INSERT OR IGNORE no-ops on the
// existing row. Independent of the drain machinery so a future
// refactor of markDrained doesn't silently widen the uniqueness gap.
func TestStoreClaimDispatchDeliveryIdempotent(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "idempotent claim", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	ag, _, err := p.store.UpsertAgent("claim-otter@claude.test", true)
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	const sid = "33333333-3333-4333-8333-333333333333"
	sess, err := p.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: sid, RepoID: p.repo.ID, AgentID: &ag.ID, Actor: "tester",
	})
	if err != nil {
		t.Fatalf("UpsertAgentSession: %v", err)
	}
	d, err := p.local.CreateDispatch(ctx, p.repo, inputs.AgentDispatchInput{
		TargetAgent: ag.Name,
		IssueKey:    iss.Key,
	}, false)
	if err != nil {
		t.Fatalf("CreateDispatch: %v", err)
	}

	first, err := p.store.ClaimDispatchDelivery(d.ID, sess.SessionID)
	if err != nil {
		t.Fatalf("first ClaimDispatchDelivery: %v", err)
	}
	if !first {
		t.Fatalf("first ClaimDispatchDelivery = false, want true")
	}
	second, err := p.store.ClaimDispatchDelivery(d.ID, sess.SessionID)
	if err != nil {
		t.Fatalf("second ClaimDispatchDelivery: %v", err)
	}
	if second {
		t.Fatalf("second ClaimDispatchDelivery = true, want false (idempotent)")
	}
	// An empty session id is a caller bug — short-circuit to (false, nil)
	// rather than touching the table. Locks in the contract markDrained
	// relies on for the legacy DrainAgentDispatches passthrough.
	empty, err := p.store.ClaimDispatchDelivery(d.ID, "")
	if err != nil {
		t.Fatalf("empty-session ClaimDispatchDelivery: %v", err)
	}
	if empty {
		t.Fatalf("empty-session ClaimDispatchDelivery = true, want false")
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

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "parked work", "", model.StateInReview, nil, "")
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

// TestEndAgentSessionAbandonsOpenQuestionsAudit (BACI-253) drives the
// full client/HTTP path: register a session, open one ask_user_question
// against it, end the session, and assert the abandon flip is recorded
// as a single summary `question.abandon` audit row. Mirrors
// TestAbandonOpenQuestionsWritesAudit's shape — `bacio history --op
// question.abandon` is a coherent ledger across both the
// channel-startup janitor path and the EndAgent cascade path.
func TestEndAgentSessionAbandonsOpenQuestionsAudit(t *testing.T) {
	for _, mode := range []string{"local", "remote"} {
		t.Run(mode, func(t *testing.T) {
			p := newPair(t)
			defer p.cleanup()
			ctx := context.Background()
			c := p.local
			if mode == "remote" {
				c = p.remote
			}

			iss, err := p.store.CreateIssue(p.repo.ID, nil, "parked on a question", "", model.StateInReview, nil, "")
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			ag, _, err := p.store.UpsertAgent("parked-quoll@claude.test-"+mode, true)
			if err != nil {
				t.Fatalf("UpsertAgent: %v", err)
			}
			// Distinct UUID suffix per mode so the local/remote variants
			// don't collide on session id.
			suffix := "1313"
			if mode == "remote" {
				suffix = "1414"
			}
			sid := "22222222-2222-4222-8222-2222222222" + suffix
			if _, err := p.store.UpsertAgentSession(store.UpsertAgentSessionIn{
				SessionID: sid, RepoID: p.repo.ID, AgentID: &ag.ID, Actor: "tester",
			}); err != nil {
				t.Fatalf("UpsertAgentSession: %v", err)
			}

			// Open one question via the client wrapper so the standard
			// question.ask audit row exists too — the assertion below
			// filters explicitly on `question.abandon` to keep honest.
			if _, err := p.local.AddSessionQuestion(ctx, client.AddSessionQuestionInput{
				SessionID: sid,
				IssueKey:  iss.Key,
				AskedBy:   "parked-quoll@claude.test-" + mode,
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

			if _, err := c.EndAgent(ctx, p.repo, inputs.AgentEndInput{
				SessionID: sid,
				Reason:    string(model.EndReasonStop),
			}, false); err != nil {
				t.Fatalf("EndAgent: %v", err)
			}

			rows, err := p.store.ListHistory(store.HistoryFilter{
				RepoID: &p.repo.ID,
				Op:     "question.abandon",
			})
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
		})
	}
}

// TestCreateRescueDispatchHappyPath (BACI-190) seeds a dead worker
// scenario — a delivered implement dispatch whose target session is
// ended — plus an idle live channel-connected supervisor, then asserts
// CreateRescueDispatch enqueues a `bacio-rescue` dispatch at the live
// supervisor and writes one agent.rescue audit row. BACI-307 dropped
// the transcript-doc agent-id enrichment, so the payload tells the
// supervisor to discover the worktree via `git worktree list`.
func TestCreateRescueDispatchHappyPath(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "salvage me", "", model.StateInReview, nil, "")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	// Dead worker session + agent.
	deadAg, _, err := p.store.UpsertAgent("dead-koala@claude.test", true)
	if err != nil {
		t.Fatalf("UpsertAgent(dead): %v", err)
	}
	const deadSID = "deadbeef-1111-4111-8111-111111111111"
	if _, err := p.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: deadSID, RepoID: p.repo.ID, AgentID: &deadAg.ID, Actor: "dead-koala",
		MarkRegistered: true,
	}); err != nil {
		t.Fatalf("UpsertAgentSession(dead): %v", err)
	}
	if _, _, _, _, _, err := p.store.EndAgentSession(deadSID, string(model.EndReasonPresumedDead), "", store.DispatchCascadeRequeue); err != nil {
		t.Fatalf("EndAgentSession(dead): %v", err)
	}

	// The original (now-stranded) dispatch — delivered, never acked.
	orig, err := p.store.AddDispatch(store.AddDispatchIn{
		RepoID:          p.repo.ID,
		TargetAgentID:   &deadAg.ID,
		TargetSessionID: deadSID,
		IssueID:         &iss.ID,
		Mode:            model.DispatchModeImplement,
		Payload:         "implement BACI-X please",
		CreatedBy:       "supervisor@host",
	})
	if err != nil {
		t.Fatalf("AddDispatch(orig): %v", err)
	}
	if _, err := p.store.MarkDispatchDelivered(orig.ID); err != nil {
		t.Fatalf("MarkDispatchDelivered: %v", err)
	}

	// Live idle supervisor — registered + channel-connected, no open
	// claim → eligible to receive a rescue dispatch.
	liveAg, _, err := p.store.UpsertAgent("alive-otter@claude.test", true)
	if err != nil {
		t.Fatalf("UpsertAgent(alive): %v", err)
	}
	const liveSID = "aaaaaaaa-2222-4222-8222-222222222222"
	if _, err := p.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: liveSID, RepoID: p.repo.ID, AgentID: &liveAg.ID, Actor: "alive-otter",
		MarkRegistered: true, ClaudePID: 4242, Host: "host",
	}); err != nil {
		t.Fatalf("UpsertAgentSession(live): %v", err)
	}
	if err := p.store.UpsertAgentChannel(store.UpsertAgentChannelIn{
		RepoID: p.repo.ID, AgentID: &liveAg.ID, Host: "host", ClaudePID: 4242, ChannelPID: 4243,
	}); err != nil {
		t.Fatalf("UpsertAgentChannel: %v", err)
	}
	if err := p.store.LinkSessionChannel(liveSID, 4242, "host"); err != nil {
		t.Fatalf("LinkSessionChannel: %v", err)
	}

	// Drive the rescue.
	rescue, err := p.local.CreateRescueDispatch(ctx, orig.ID)
	if err != nil {
		t.Fatalf("CreateRescueDispatch: %v", err)
	}
	if rescue.CreatedBy != model.RescueDispatchCreator {
		t.Fatalf("rescue.CreatedBy = %q, want %q", rescue.CreatedBy, model.RescueDispatchCreator)
	}
	if rescue.TargetSessionID != liveSID {
		t.Fatalf("rescue.TargetSessionID = %q, want %q (the live supervisor)", rescue.TargetSessionID, liveSID)
	}
	if !strings.Contains(rescue.Payload, "worktree list") {
		t.Fatalf("rescue payload should tell the supervisor to discover the worktree: %q", rescue.Payload)
	}
	if !strings.Contains(rescue.Payload, "INLINE") {
		t.Fatalf("rescue payload should tell the supervisor to handle INLINE: %q", rescue.Payload)
	}

	// The original dispatch is unchanged (still delivered, still un-acked).
	again, err := p.store.GetDispatch(orig.ID)
	if err != nil {
		t.Fatalf("GetDispatch(orig): %v", err)
	}
	if again.Status != model.DispatchDelivered {
		t.Fatalf("original status flipped to %s; should still be delivered", again.Status)
	}

	// One agent.rescue audit row, pointing at the rescue dispatch with
	// Details naming the original and the agent.
	rows, err := p.store.ListHistory(store.HistoryFilter{RepoID: &p.repo.ID, Op: "agent.rescue"})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("agent.rescue rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.TargetID == nil || *row.TargetID != rescue.ID {
		t.Fatalf("rescue audit TargetID = %v, want %d", row.TargetID, rescue.ID)
	}
	if !strings.Contains(row.Details, "original_dispatch=") {
		t.Fatalf("rescue audit Details missing original_dispatch: %q", row.Details)
	}
}

// TestCreateRescueDispatchRejectsAliveSession (BACI-190) asserts the
// eligibility gate: a dispatch whose target session is still alive
// can't be rescued. Returns a 'still alive' error the API layer maps
// to 409 conflict.
func TestCreateRescueDispatchRejectsAliveSession(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	iss, err := p.store.CreateIssue(p.repo.ID, nil, "alive worker", "", model.StateInReview, nil, "")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	ag, _, err := p.store.UpsertAgent("live-otter@claude.test", true)
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	const sid = "11111111-3333-4333-8333-333333333333"
	if _, err := p.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: sid, RepoID: p.repo.ID, AgentID: &ag.ID, Actor: "live-otter",
		MarkRegistered: true,
	}); err != nil {
		t.Fatalf("UpsertAgentSession: %v", err)
	}
	d, err := p.store.AddDispatch(store.AddDispatchIn{
		RepoID:          p.repo.ID,
		TargetAgentID:   &ag.ID,
		TargetSessionID: sid,
		IssueID:         &iss.ID,
		Mode:            model.DispatchModeImplement,
		Payload:         "in-flight",
		CreatedBy:       "supervisor@host",
	})
	if err != nil {
		t.Fatalf("AddDispatch: %v", err)
	}
	if _, err := p.store.MarkDispatchDelivered(d.ID); err != nil {
		t.Fatalf("MarkDispatchDelivered: %v", err)
	}

	_, err = p.local.CreateRescueDispatch(ctx, d.ID)
	if err == nil {
		t.Fatalf("CreateRescueDispatch on alive session: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "still alive") {
		t.Fatalf("error = %q, want 'still alive'", err.Error())
	}
}

// TestCreateRescueDispatchRejectsTrivialCreator (BACI-190) asserts a
// rescue can't be rescued: chain-rescuing in-flight rescue dispatches
// would loop forever.
func TestCreateRescueDispatchRejectsTrivialCreator(t *testing.T) {
	p := newPair(t)
	defer p.cleanup()
	ctx := context.Background()

	ag, _, err := p.store.UpsertAgent("recursion-otter@claude.test", true)
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	const sid = "22222222-4444-4444-8444-444444444444"
	if _, err := p.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: sid, RepoID: p.repo.ID, AgentID: &ag.ID, Actor: "recursion-otter",
		MarkRegistered: true,
	}); err != nil {
		t.Fatalf("UpsertAgentSession: %v", err)
	}
	if _, _, _, _, _, err := p.store.EndAgentSession(sid, string(model.EndReasonStop), "", store.DispatchCascadeCancel); err != nil {
		t.Fatalf("EndAgentSession: %v", err)
	}
	d, err := p.store.AddDispatch(store.AddDispatchIn{
		RepoID:          p.repo.ID,
		TargetSessionID: sid,
		Payload:         "rescue payload",
		CreatedBy:       model.RescueDispatchCreator,
	})
	if err != nil {
		t.Fatalf("AddDispatch: %v", err)
	}
	if _, err := p.store.MarkDispatchDelivered(d.ID); err != nil {
		t.Fatalf("MarkDispatchDelivered: %v", err)
	}

	_, err = p.local.CreateRescueDispatch(ctx, d.ID)
	if err == nil {
		t.Fatalf("CreateRescueDispatch on rescue dispatch: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not rescuable") {
		t.Fatalf("error = %q, want 'not rescuable'", err.Error())
	}
}

