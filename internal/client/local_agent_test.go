package client

import (
	"context"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/pipeline"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// seedRunningPipelineJob seeds an in_pipeline card with a process chain,
// ticks the engine once to start (and queue a dispatch for) the first job,
// and returns the running job's dispatch id. The card is left with a
// `running` job + queued dispatch — the shape a live worker leaves behind.
func seedRunningPipelineJob(t *testing.T, c *localClient, prefix string) (*model.Issue, int64) {
	t.Helper()
	repo, err := c.store.CreateRepo(prefix, "fail-dispatch", t.TempDir(), "")
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	iss, err := c.store.CreateIssue(repo.ID, nil, "card", "", model.StateInPipeline, nil, "")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	proc, err := model.ProcessBySlug("plan-implement")
	if err != nil {
		t.Fatalf("ProcessBySlug: %v", err)
	}
	if _, err := c.store.SetIssueProcess(iss.ID, proc); err != nil {
		t.Fatalf("SetIssueProcess: %v", err)
	}
	if err := c.store.SetIssueEngineMode(iss.ID, model.EngineAuto); err != nil {
		t.Fatalf("SetIssueEngineMode: %v", err)
	}
	if _, err := pipeline.New(c.store).Tick(); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	jobs, err := c.store.ListPipelineJobs(iss.ID)
	if err != nil {
		t.Fatalf("ListPipelineJobs: %v", err)
	}
	var running *model.PipelineJob
	for _, j := range jobs {
		if j.Status == model.JobRunning {
			running = j
		}
	}
	if running == nil || running.DispatchID == nil {
		t.Fatalf("no running job with dispatch after tick: %+v", jobs)
	}
	return iss, *running.DispatchID
}

// TestFailDispatch_ReconcilesJobReleasesClaimSettlesDispatch covers the
// full BACI-328 path: a soft-cancelled dispatch worker's dispatch id drives
// FailDispatch, which cancels the running job + its dispatch, stamps the
// neutral subagent_cancelled pause reason, halts Auto, releases the open
// claim, and writes an engine.advance audit row.
func TestFailDispatch_ReconcilesJobReleasesClaimSettlesDispatch(t *testing.T) {
	c, _ := openTestLocalClient(t)
	ctx := context.Background()
	iss, dispatchID := seedRunningPipelineJob(t, c, "FDIS")

	// A worker session claims the issue (the binding FailDispatch tears
	// down). The session must be registered before it can hold a claim.
	ag, _, err := c.store.UpsertAgent("swift-otter@claude.test", true)
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	if _, err := c.store.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: "sess-fail-1", RepoID: iss.RepoID, AgentID: &ag.ID, Actor: "tester",
	}); err != nil {
		t.Fatalf("UpsertAgentSession: %v", err)
	}
	if _, _, _, _, err := c.store.AddAgentClaim("sess-fail-1", iss.ID, "implement"); err != nil {
		t.Fatalf("AddAgentClaim: %v", err)
	}

	if err := c.FailDispatch(ctx, dispatchID); err != nil {
		t.Fatalf("FailDispatch: %v", err)
	}

	// Job cancelled, card paused in place with the neutral reason.
	jobs, _ := c.store.ListPipelineJobs(iss.ID)
	if jobs[0].Status != model.JobCancelled {
		t.Fatalf("job 1 status = %s, want cancelled", jobs[0].Status)
	}
	got, _ := c.store.GetIssueByID(iss.ID)
	if got.State != model.StateInPipeline {
		t.Fatalf("state = %s, want in_pipeline", got.State)
	}
	if got.EngineMode != model.EngineOff {
		t.Fatalf("engine mode = %s, want off", got.EngineMode)
	}
	if got.EnginePauseReason != model.EnginePauseReasonSubagentCancelled {
		t.Fatalf("pause reason = %q, want %q", got.EnginePauseReason, model.EnginePauseReasonSubagentCancelled)
	}

	// Dispatch cancelled.
	d, err := c.store.GetDispatch(dispatchID)
	if err != nil {
		t.Fatalf("GetDispatch: %v", err)
	}
	if d.Status != model.DispatchCancelled {
		t.Fatalf("dispatch status = %s, want cancelled", d.Status)
	}

	// Claim released.
	claims, _ := c.store.ListClaimsForIssue(iss.ID)
	for _, cl := range claims {
		if cl.ReleasedAt == nil {
			t.Fatalf("claim %d still open after FailDispatch", cl.ID)
		}
	}

	// An engine.advance audit row records the cancel.
	rows, err := c.store.ListHistory(store.HistoryFilter{Op: "engine.advance"})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no engine.advance audit row written")
	}

	// Idempotent: a second call (the supervisor double-firing) is a clean
	// no-op rather than an error.
	if err := c.FailDispatch(ctx, dispatchID); err != nil {
		t.Fatalf("second FailDispatch should be a no-op, got: %v", err)
	}
}

// TestFailDispatch_UnknownDispatchOrNoJobIsNoop locks in the no-op paths:
// an unknown dispatch id, and a dispatch with no issue (a setup/ping
// dispatch), both reconcile to nothing without error.
func TestFailDispatch_UnknownDispatchOrNoJobIsNoop(t *testing.T) {
	c, _ := openTestLocalClient(t)
	ctx := context.Background()

	// Unknown dispatch id.
	if err := c.FailDispatch(ctx, 999999); err != nil {
		t.Fatalf("FailDispatch(unknown) = %v, want nil no-op", err)
	}

	// A dispatch with no issue (no pipeline job to cancel).
	repo, err := c.store.CreateRepo("FNOP", "fail-noop", t.TempDir(), "")
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	ag, _, err := c.store.UpsertAgent("quiet-lynx@claude.test", true)
	if err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	d, err := c.store.AddDispatch(store.AddDispatchIn{
		RepoID:        repo.ID,
		TargetAgentID: &ag.ID,
		Payload:       "ping",
		CreatedBy:     "tester",
	})
	if err != nil {
		t.Fatalf("AddDispatch: %v", err)
	}
	if err := c.FailDispatch(ctx, d.ID); err != nil {
		t.Fatalf("FailDispatch(no-issue) = %v, want nil no-op", err)
	}
	// The setup dispatch is untouched (not cancelled) since there was no
	// job to reconcile.
	got, _ := c.store.GetDispatch(d.ID)
	if got.Status != model.DispatchPending {
		t.Fatalf("setup dispatch status = %s, want unchanged pending", got.Status)
	}
}
