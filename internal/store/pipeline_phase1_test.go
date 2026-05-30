package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestPipelineJobsLifecycle covers SetIssueProcess materialisation, the
// started-chain immutability guard, and the job status / dispatch /
// engine-field setters.
func TestPipelineJobsLifecycle(t *testing.T) {
	s, _, iss := seedRepoAndIssue(t)

	proc, err := model.ProcessBySlug("plan-implement-ship")
	if err != nil {
		t.Fatalf("ProcessBySlug: %v", err)
	}
	jobs, err := s.SetIssueProcess(iss.ID, proc)
	if err != nil {
		t.Fatalf("SetIssueProcess: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("jobs = %d, want 3", len(jobs))
	}
	wantModes := []string{model.BuiltinTemplatePlan, model.BuiltinTemplateImplement, model.ShipJobMode}
	for i, j := range jobs {
		if j.Sequence != i+1 {
			t.Errorf("job %d sequence = %d, want %d", i, j.Sequence, i+1)
		}
		if j.Mode != wantModes[i] {
			t.Errorf("job %d mode = %q, want %q", i, j.Mode, wantModes[i])
		}
		if j.Status != model.JobPending {
			t.Errorf("job %d status = %q, want pending", i, j.Status)
		}
	}
	if !jobs[2].IsShipHandoff() {
		t.Error("last job should be the ship hand-off")
	}

	// Start the first job: status → running, started_at stamped.
	if err := s.SetPipelineJobStatus(jobs[0].ID, model.JobRunning); err != nil {
		t.Fatalf("SetPipelineJobStatus running: %v", err)
	}
	got, err := s.GetPipelineJob(jobs[0].ID)
	if err != nil {
		t.Fatalf("GetPipelineJob: %v", err)
	}
	if got.Status != model.JobRunning {
		t.Fatalf("status = %q, want running", got.Status)
	}
	if got.StartedAt == nil {
		t.Fatal("started_at not stamped on running")
	}

	// Pin a dispatch id, then complete: completed_at stamped, dispatch kept.
	// The dispatch_id FK requires a real agent_dispatches row.
	res, err := s.DB.Exec(`INSERT INTO agent_dispatches (repo_id, created_by, status) VALUES (?, 'test', 'queued')`, iss.RepoID)
	if err != nil {
		t.Fatalf("seed dispatch: %v", err)
	}
	dispatchID, _ := res.LastInsertId()
	if err := s.SetPipelineJobDispatch(jobs[0].ID, &dispatchID); err != nil {
		t.Fatalf("SetPipelineJobDispatch: %v", err)
	}
	if err := s.SetPipelineJobStatus(jobs[0].ID, model.JobComplete); err != nil {
		t.Fatalf("SetPipelineJobStatus complete: %v", err)
	}
	got, _ = s.GetPipelineJob(jobs[0].ID)
	if got.CompletedAt == nil {
		t.Fatal("completed_at not stamped on complete")
	}
	if got.DispatchID == nil || *got.DispatchID != dispatchID {
		t.Fatalf("dispatch id = %v, want %d", got.DispatchID, dispatchID)
	}

	// A started chain cannot be re-set wholesale (completed-job immutability).
	if _, err := s.SetIssueProcess(iss.ID, proc); err == nil {
		t.Fatal("SetIssueProcess on a started chain succeeded, want error")
	}

	// Engine fields round-trip onto the issue.
	if err := s.SetIssueEngineMode(iss.ID, model.EngineAuto); err != nil {
		t.Fatalf("SetIssueEngineMode: %v", err)
	}
	if err := s.SetIssueEnginePauseReason(iss.ID, model.EnginePauseReasonOpenQuestion); err != nil {
		t.Fatalf("SetIssueEnginePauseReason: %v", err)
	}
	issGot, _ := s.GetIssueByID(iss.ID)
	if issGot.EngineMode != model.EngineAuto {
		t.Fatalf("engine mode = %q, want auto", issGot.EngineMode)
	}
	if issGot.EnginePauseReason != model.EnginePauseReasonOpenQuestion {
		t.Fatalf("pause reason = %q, want open_question", issGot.EnginePauseReason)
	}

	// SetIssueProcess with an invalid engine mode / unknown process is rejected.
	if _, err := model.ProcessBySlug("nope"); err == nil {
		t.Fatal("ProcessBySlug(nope) succeeded, want error")
	}
	if err := s.SetIssueEngineMode(iss.ID, model.EngineMode("bogus")); err == nil {
		t.Fatal("SetIssueEngineMode(bogus) succeeded, want error")
	}
}

// TestResetPipelineJobToPending: a terminal job carrying started_at,
// completed_at, and a dispatch_id is rewound to pending with all three
// cleared — the inverse of SetPipelineJobStatus's COALESCE stamping, which
// the BACI-291 re-run path relies on so a re-dispatch stamps fresh.
func TestResetPipelineJobToPending(t *testing.T) {
	s, _, iss := seedRepoAndIssue(t)

	proc, err := model.ProcessBySlug("plan-implement")
	if err != nil {
		t.Fatalf("ProcessBySlug: %v", err)
	}
	jobs, err := s.SetIssueProcess(iss.ID, proc)
	if err != nil {
		t.Fatalf("SetIssueProcess: %v", err)
	}

	// Run then complete the first job, pinning a real dispatch row.
	if err := s.SetPipelineJobStatus(jobs[0].ID, model.JobRunning); err != nil {
		t.Fatalf("SetPipelineJobStatus running: %v", err)
	}
	res, err := s.DB.Exec(`INSERT INTO agent_dispatches (repo_id, created_by, status) VALUES (?, 'test', 'queued')`, iss.RepoID)
	if err != nil {
		t.Fatalf("seed dispatch: %v", err)
	}
	dispatchID, _ := res.LastInsertId()
	if err := s.SetPipelineJobDispatch(jobs[0].ID, &dispatchID); err != nil {
		t.Fatalf("SetPipelineJobDispatch: %v", err)
	}
	if err := s.SetPipelineJobStatus(jobs[0].ID, model.JobComplete); err != nil {
		t.Fatalf("SetPipelineJobStatus complete: %v", err)
	}
	got, _ := s.GetPipelineJob(jobs[0].ID)
	if got.StartedAt == nil || got.CompletedAt == nil || got.DispatchID == nil {
		t.Fatalf("precondition: expected stamps set, got %+v", got)
	}

	if err := s.ResetPipelineJobToPending(jobs[0].ID); err != nil {
		t.Fatalf("ResetPipelineJobToPending: %v", err)
	}
	got, _ = s.GetPipelineJob(jobs[0].ID)
	if got.Status != model.JobPending {
		t.Fatalf("status = %q, want pending", got.Status)
	}
	if got.StartedAt != nil {
		t.Fatalf("started_at = %v, want nil", got.StartedAt)
	}
	if got.CompletedAt != nil {
		t.Fatalf("completed_at = %v, want nil", got.CompletedAt)
	}
	if got.DispatchID != nil {
		t.Fatalf("dispatch_id = %v, want nil", got.DispatchID)
	}
}

// TestSetIssueStateTearsDownPipelineRunOnLeave: dragging a card out of
// in_pipeline (back to Backlog) cancels its running job + dispatch and
// disarms Auto, so a worker isn't orphaned and a stale running row can't
// confuse a re-entry. The engine's own hand-off (→ to_be_shipped) and the
// no-op processing-state guard must NOT trigger the teardown.
func TestSetIssueStateTearsDownPipelineRunOnLeave(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)

	// Enter the pipeline with an Auto chain and a running job + dispatch.
	if err := s.SetIssueState(iss.ID, model.StateInPipeline); err != nil {
		t.Fatalf("enter pipeline: %v", err)
	}
	if err := s.SetIssueEngineMode(iss.ID, model.EngineAuto); err != nil {
		t.Fatalf("engine auto: %v", err)
	}
	proc, _ := model.ProcessBySlug("plan-implement")
	jobs, err := s.SetIssueProcess(iss.ID, proc)
	if err != nil {
		t.Fatalf("SetIssueProcess: %v", err)
	}
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID:        repo.ID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchModePlan,
		CreatedBy:     "test",
		InitialStatus: model.DispatchQueued,
	})
	if err != nil {
		t.Fatalf("AddDispatch: %v", err)
	}
	if won, err := s.StartPipelineJobWithDispatch(jobs[0].ID, d.ID); err != nil || !won {
		t.Fatalf("StartPipelineJobWithDispatch: won=%v err=%v", won, err)
	}

	// A processing-state target (in_review) on an in_pipeline card is a
	// no-op (guard) — it must NOT tear the run down.
	if err := s.SetIssueState(iss.ID, model.StateInReview); err != nil {
		t.Fatalf("guarded no-op move: %v", err)
	}
	if got, _ := s.GetPipelineJob(jobs[0].ID); got.Status != model.JobRunning {
		t.Fatalf("guarded move cancelled the job: status=%q", got.Status)
	}
	if got, _ := s.GetIssueByID(iss.ID); got.State != model.StateInPipeline {
		t.Fatalf("guarded move changed column: %q", got.State)
	}

	// Drag back to Backlog: the run is torn down.
	if err := s.SetIssueState(iss.ID, model.StateTodo); err != nil {
		t.Fatalf("leave pipeline: %v", err)
	}
	gotIss, _ := s.GetIssueByID(iss.ID)
	if gotIss.State != model.StateTodo {
		t.Fatalf("state = %q, want todo", gotIss.State)
	}
	if gotIss.EngineMode != model.EngineOff {
		t.Fatalf("engine mode = %q, want off after leaving pipeline", gotIss.EngineMode)
	}
	if gotJob, _ := s.GetPipelineJob(jobs[0].ID); gotJob.Status != model.JobCancelled {
		t.Fatalf("running job status = %q, want cancelled", gotJob.Status)
	}
	if gotD, _ := s.GetDispatch(d.ID); gotD.Status != model.DispatchCancelled {
		t.Fatalf("dispatch status = %q, want cancelled", gotD.Status)
	}
}

// TestReorderIssue locks in dense (repo, state)-band renumbering: moving
// a card to position 1 puts it at priority 0 and shifts the rest down.
func TestReorderIssue(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("RORD", "reorder", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	a, _ := s.CreateIssue(repo.ID, nil, "a", "", model.StateTodo, nil, "")
	b, _ := s.CreateIssue(repo.ID, nil, "b", "", model.StateTodo, nil, "")
	c, _ := s.CreateIssue(repo.ID, nil, "c", "", model.StateTodo, nil, "")

	// Move c (number 3) to the top.
	if err := s.ReorderIssue(c.ID, 1); err != nil {
		t.Fatalf("ReorderIssue: %v", err)
	}
	priority := func(id int64) int {
		iss, err := s.GetIssueByID(id)
		if err != nil {
			t.Fatalf("get issue: %v", err)
		}
		return iss.Priority
	}
	if got := priority(c.ID); got != 0 {
		t.Fatalf("c priority = %d, want 0", got)
	}
	if got := priority(a.ID); got != 1 {
		t.Fatalf("a priority = %d, want 1", got)
	}
	if got := priority(b.ID); got != 2 {
		t.Fatalf("b priority = %d, want 2", got)
	}

	// Move a to the end (position beyond the count clamps to last).
	if err := s.ReorderIssue(a.ID, 99); err != nil {
		t.Fatalf("ReorderIssue clamp: %v", err)
	}
	if got := priority(a.ID); got != 2 {
		t.Fatalf("a priority after move-to-end = %d, want 2", got)
	}
}

// TestBootstrapRepoDefaults covers catch-all creation (auto-close off),
// the default pointer, orphan backfill, idempotency, and respecting a
// repo that already has a deliberate default.
func TestBootstrapRepoDefaults(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("BOOT", "boot", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	orphan, _ := s.CreateIssue(repo.ID, nil, "orphan", "", model.StateTodo, nil, "")

	if err := s.BootstrapRepoDefaults(repo.ID); err != nil {
		t.Fatalf("BootstrapRepoDefaults: %v", err)
	}

	maint, err := s.GetFeatureBySlug(repo.ID, CatchAllFeatureMaintenance)
	if err != nil {
		t.Fatalf("maintenance missing: %v", err)
	}
	bugs, err := s.GetFeatureBySlug(repo.ID, CatchAllFeatureBugs)
	if err != nil {
		t.Fatalf("bugs missing: %v", err)
	}
	// Auto-close OFF => state_manual set.
	if !maint.StateManual || !bugs.StateManual {
		t.Fatalf("catch-alls should have auto-close off: maint.StateManual=%v bugs.StateManual=%v", maint.StateManual, bugs.StateManual)
	}
	// Default points at maintenance.
	settings, _ := s.GetRepoSettings(repo.ID)
	if settings.DefaultFeatureID == nil || *settings.DefaultFeatureID != maint.ID {
		t.Fatalf("default feature = %v, want maintenance id %d", settings.DefaultFeatureID, maint.ID)
	}
	// Orphan backfilled onto maintenance.
	got, _ := s.GetIssueByID(orphan.ID)
	if got.FeatureID == nil || *got.FeatureID != maint.ID {
		t.Fatalf("orphan feature = %v, want maintenance id %d", got.FeatureID, maint.ID)
	}

	// Idempotent: a second call adds nothing.
	if err := s.BootstrapRepoDefaults(repo.ID); err != nil {
		t.Fatalf("BootstrapRepoDefaults (second): %v", err)
	}
	feats, _ := s.ListFeatures(repo.ID, false)
	if len(feats) != 2 {
		t.Fatalf("feature count = %d, want 2", len(feats))
	}

	// A repo with a deliberate default is left untouched (no catch-alls).
	repo2, _ := s.CreateRepo("BTW2", "boot2", t.TempDir(), "")
	custom, _ := s.CreateFeature(repo2.ID, "custom", "Custom", "", "", "")
	if err := s.SetDefaultFeatureID(repo2.ID, &custom.ID); err != nil {
		t.Fatalf("set default: %v", err)
	}
	if err := s.BootstrapRepoDefaults(repo2.ID); err != nil {
		t.Fatalf("BootstrapRepoDefaults repo2: %v", err)
	}
	feats2, _ := s.ListFeatures(repo2.ID, false)
	if len(feats2) != 1 {
		t.Fatalf("repo2 feature count = %d, want 1 (no catch-alls forced)", len(feats2))
	}
}

// TestEngineGovernedStateGuard is the keystone test: while a card sits in
// a Pipeline column, worker/agent writes that would flip it into a
// processing state are no-op'd, but deliberate column moves still apply.
func TestEngineGovernedStateGuard(t *testing.T) {
	s, repo, iss := seedRepoAndIssue(t)

	stateOf := func() model.State {
		got, err := s.GetIssueByID(iss.ID)
		if err != nil {
			t.Fatalf("get issue: %v", err)
		}
		return got.State
	}

	// Deliberate move into the pipeline.
	if err := s.SetIssueState(iss.ID, model.StateInPipeline); err != nil {
		t.Fatalf("SetIssueState in_pipeline: %v", err)
	}
	// Guard: processing-state writes are ignored. Since BACI-300 retired
	// in_progress / needs_action, in_review is the only remaining one.
	for _, target := range []model.State{model.StateInReview} {
		if err := s.SetIssueState(iss.ID, target); err != nil {
			t.Fatalf("SetIssueState %s: %v", target, err)
		}
		if stateOf() != model.StateInPipeline {
			t.Fatalf("guard failed: %s flipped in_pipeline card to %s", target, stateOf())
		}
	}

	// A claim must not move an in_pipeline card.
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{SessionID: "g1", RepoID: repo.ID, Actor: "agent-g"}); err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim("g1", iss.ID, ""); err != nil {
		t.Fatalf("AddAgentClaim: %v", err)
	}
	if stateOf() != model.StateInPipeline {
		t.Fatalf("claim flipped in_pipeline card to %s", stateOf())
	}
	// release --state in_review is ignored too (claim still drops).
	if _, _, _, err := s.ReleaseAgentClaim("g1", iss.ID, model.StateInReview); err != nil {
		t.Fatalf("ReleaseAgentClaim: %v", err)
	}
	if stateOf() != model.StateInPipeline {
		t.Fatalf("release flipped in_pipeline card to %s", stateOf())
	}

	// Deliberate column moves still work: hand-off → to_be_shipped → done.
	if err := s.SetIssueState(iss.ID, model.StateToBeShipped); err != nil {
		t.Fatalf("SetIssueState to_be_shipped: %v", err)
	}
	if stateOf() != model.StateToBeShipped {
		t.Fatalf("hand-off blocked: state = %s", stateOf())
	}
	if err := s.SetIssueState(iss.ID, model.StateDone); err != nil {
		t.Fatalf("SetIssueState done: %v", err)
	}
	if stateOf() != model.StateDone {
		t.Fatalf("ship-to-done blocked: state = %s", stateOf())
	}
}
