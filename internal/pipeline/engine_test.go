package pipeline

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func newEngineStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.DB.Close() })
	return s
}

func seedPipelineCard(t *testing.T, s *store.Store, prefix, processSlug string, mode model.EngineMode) (*model.Repo, *model.Issue) {
	t.Helper()
	repo, err := s.CreateRepo(prefix, "engine-test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "card", "", model.StateInPipeline, nil, "")
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	proc, err := model.ProcessBySlug(processSlug)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if _, err := s.SetIssueProcess(iss.ID, proc); err != nil {
		t.Fatalf("set process: %v", err)
	}
	if err := s.SetIssueEngineMode(iss.ID, mode); err != nil {
		t.Fatalf("set engine mode: %v", err)
	}
	return repo, iss
}

func runningJob(t *testing.T, s *store.Store, issueID int64) *model.PipelineJob {
	t.Helper()
	jobs, err := s.ListPipelineJobs(issueID)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	for _, j := range jobs {
		if j.Status == model.JobRunning {
			return j
		}
	}
	return nil
}

// simulateWorkerAck stands in for "the matcher bound the queued dispatch
// and the worker acked it": bind (queued → pending) via raw SQL, then ack.
func simulateWorkerAck(t *testing.T, s *store.Store, dispatchID int64) {
	t.Helper()
	if _, err := s.DB.Exec(`UPDATE agent_dispatches SET status='pending' WHERE id=?`, dispatchID); err != nil {
		t.Fatalf("bind dispatch: %v", err)
	}
	if _, err := s.AckDispatch(dispatchID, ""); err != nil {
		t.Fatalf("ack dispatch: %v", err)
	}
}

// TestEngineAutoChain drives a card plan→implement→ship-handoff to
// to_be_shipped under Auto, simulating a worker ack between ticks.
func TestEngineAutoChain(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "ENG1", "plan-implement-ship", model.EngineAuto)
	eng := New(s)

	// Tick 1: starts the plan job.
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	r := runningJob(t, s, iss.ID)
	if r == nil || r.Mode != model.BuiltinTemplatePlan {
		t.Fatalf("after tick 1 running = %+v, want plan", r)
	}
	simulateWorkerAck(t, s, *r.DispatchID)

	// Tick 2: plan completes, implement starts.
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	r = runningJob(t, s, iss.ID)
	if r == nil || r.Mode != model.BuiltinTemplateImplement {
		t.Fatalf("after tick 2 running = %+v, want implement", r)
	}
	simulateWorkerAck(t, s, *r.DispatchID)

	// Tick 3: implement completes, next is the ship sentinel → hand off.
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	got, _ := s.GetIssueByID(iss.ID)
	if got.State != model.StateToBeShipped {
		t.Fatalf("state = %s, want to_be_shipped", got.State)
	}
	jobs, _ := s.ListPipelineJobs(iss.ID)
	for _, j := range jobs {
		if j.Status != model.JobComplete {
			t.Errorf("job seq=%d mode=%s status=%s, want complete", j.Sequence, j.Mode, j.Status)
		}
	}
}

// TestEngineManualModeNoAutoAdvance: with Auto off the engine never
// starts a job, but it DOES mark a manually-started job complete on ack.
func TestEngineManualModeNoAutoAdvance(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "ENG2", "plan-implement", model.EngineOff)
	eng := New(s)

	// Tick on a manual card starts nothing.
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if r := runningJob(t, s, iss.ID); r != nil {
		t.Fatalf("manual mode started a job: %+v", r)
	}

	// Simulate the manual Start of job 1 (the store CAS the API will use).
	jobs, _ := s.ListPipelineJobs(iss.ID)
	d, err := s.AddDispatch(store.AddDispatchIn{
		RepoID: iss.RepoID, IssueID: &iss.ID,
		Mode: model.DispatchMode(jobs[0].Mode), CreatedBy: model.ControllerActor,
		InitialStatus: model.DispatchQueued,
	})
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if won, err := s.StartPipelineJobWithDispatch(jobs[0].ID, d.ID); err != nil || !won {
		t.Fatalf("manual start: won=%v err=%v", won, err)
	}
	simulateWorkerAck(t, s, d.ID)

	// Tick: completion detection runs even in manual mode, but job 2 does
	// not auto-start.
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}
	jobs, _ = s.ListPipelineJobs(iss.ID)
	if jobs[0].Status != model.JobComplete {
		t.Fatalf("job 1 status = %s, want complete", jobs[0].Status)
	}
	if jobs[1].Status != model.JobPending {
		t.Fatalf("job 2 status = %s, want pending (no auto-advance)", jobs[1].Status)
	}
}

// TestEngineAutoShip: with auto-ship on, the top to_be_shipped card gets
// one ship dispatch (not duplicated while in flight) and advances to done
// once it acks.
func TestEngineAutoShip(t *testing.T) {
	s := newEngineStore(t)
	repo, err := s.CreateRepo("SHIP", "ship", t.TempDir(), "")
	if err != nil {
		t.Fatalf("repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "card", "", model.StateToBeShipped, nil, "")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	eng := New(s)

	// Auto-ship off → nothing dispatched.
	if _, err := eng.AutoShipTick(); err != nil {
		t.Fatalf("tick off: %v", err)
	}
	if d, _ := s.LatestDispatchForIssueMode(iss.ID, model.DispatchModeShip); d != nil {
		t.Fatalf("auto-ship off dispatched a ship: %+v", d)
	}

	// Turn it on → first tick queues exactly one ship dispatch.
	if err := s.SetRepoAutoShip(repo.ID, true); err != nil {
		t.Fatalf("set auto-ship: %v", err)
	}
	if _, err := eng.AutoShipTick(); err != nil {
		t.Fatalf("tick on: %v", err)
	}
	d, _ := s.LatestDispatchForIssueMode(iss.ID, model.DispatchModeShip)
	if d == nil {
		t.Fatal("expected a ship dispatch")
	}
	// In flight → no second dispatch.
	if _, err := eng.AutoShipTick(); err != nil {
		t.Fatalf("tick in-flight: %v", err)
	}
	d2, _ := s.LatestDispatchForIssueMode(iss.ID, model.DispatchModeShip)
	if d2.ID != d.ID {
		t.Fatalf("auto-ship double-dispatched: %d != %d", d2.ID, d.ID)
	}

	// Ack → next tick advances to done.
	simulateWorkerAck(t, s, d.ID)
	if _, err := eng.AutoShipTick(); err != nil {
		t.Fatalf("tick done: %v", err)
	}
	got, _ := s.GetIssueByID(iss.ID)
	if got.State != model.StateDone {
		t.Fatalf("state = %s, want done", got.State)
	}
}

// TestEngineManualShip: with auto-ship OFF the tick never queues a ship
// dispatch on its own, but once the user manually ships (a ship dispatch
// exists) the tick still advances the card to done on ack. Guards the
// "manual SHIP strands the card in to_be_shipped" regression — the
// advance-on-ack arm must not be gated on the auto-ship toggle.
func TestEngineManualShip(t *testing.T) {
	s := newEngineStore(t)
	repo, err := s.CreateRepo("MSHIP", "mship", t.TempDir(), "")
	if err != nil {
		t.Fatalf("repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "card", "", model.StateToBeShipped, nil, "")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	eng := New(s)

	// Auto-ship off → the tick does not dispatch a ship by itself.
	if _, err := eng.AutoShipTick(); err != nil {
		t.Fatalf("tick off: %v", err)
	}
	if d, _ := s.LatestDispatchForIssueMode(iss.ID, model.DispatchModeShip); d != nil {
		t.Fatalf("auto-ship off dispatched a ship: %+v", d)
	}

	// User clicks SHIP → a ship dispatch is queued (the API path:
	// dispatchIssue with mode=ship), auto-ship still off.
	d, err := s.AddDispatch(store.AddDispatchIn{
		RepoID:        repo.ID,
		IssueID:       &iss.ID,
		Mode:          model.DispatchModeShip,
		CreatedBy:     model.ControllerActor,
		InitialStatus: model.DispatchQueued,
	})
	if err != nil {
		t.Fatalf("manual ship dispatch: %v", err)
	}

	// In flight → the tick neither dispatches again nor advances.
	if _, err := eng.AutoShipTick(); err != nil {
		t.Fatalf("tick in-flight: %v", err)
	}
	if got, _ := s.GetIssueByID(iss.ID); got.State != model.StateToBeShipped {
		t.Fatalf("state = %s, want to_be_shipped while in flight", got.State)
	}

	// Ack → next tick advances to done even though auto-ship is off.
	simulateWorkerAck(t, s, d.ID)
	if _, err := eng.AutoShipTick(); err != nil {
		t.Fatalf("tick done: %v", err)
	}
	got, _ := s.GetIssueByID(iss.ID)
	if got.State != model.StateDone {
		t.Fatalf("state = %s, want done after manual ship ack", got.State)
	}
}

// TestEngineShipAdvancesBehindWedgedTop: with auto-ship OFF, an acked
// ship on a card sitting BEHIND a wedged head card still advances to
// done. The head card (first to arrive, so TopShippingIssue) has its
// ship dispatch left in cancelled — modelling BACI-295, whose worker
// session died before merging. The advance pass must sweep the whole
// Shipping column, not just the top, so the acked card behind it isn't
// stranded. Regression for the BACI-297 head-of-line bug.
func TestEngineShipAdvancesBehindWedgedTop(t *testing.T) {
	s := newEngineStore(t)
	repo, err := s.CreateRepo("WEDGE", "wedge", t.TempDir(), "")
	if err != nil {
		t.Fatalf("repo: %v", err)
	}
	// Two cards enter Shipping in order: the first becomes the top
	// (TopShippingIssue), the second sits strictly behind it (BACI-275
	// appends to the back of the FIFO with MAX(priority)+1).
	top, err := s.CreateIssue(repo.ID, nil, "wedged top", "", model.StateInPipeline, nil, "")
	if err != nil {
		t.Fatalf("create top: %v", err)
	}
	if err := s.SetIssueState(top.ID, model.StateToBeShipped); err != nil {
		t.Fatalf("ship top: %v", err)
	}
	behind, err := s.CreateIssue(repo.ID, nil, "behind", "", model.StateInPipeline, nil, "")
	if err != nil {
		t.Fatalf("create behind: %v", err)
	}
	if err := s.SetIssueState(behind.ID, model.StateToBeShipped); err != nil {
		t.Fatalf("ship behind: %v", err)
	}
	if got, _ := s.TopShippingIssue(repo.ID); got == nil || got.ID != top.ID {
		t.Fatalf("top shipping = %v, want top (%d)", got, top.ID)
	}
	eng := New(s)

	// The top card's ship dispatch dies (worker session ended) → cancelled.
	topShip, err := s.AddDispatch(store.AddDispatchIn{
		RepoID:        repo.ID,
		IssueID:       &top.ID,
		Mode:          model.DispatchModeShip,
		CreatedBy:     model.ControllerActor,
		InitialStatus: model.DispatchQueued,
	})
	if err != nil {
		t.Fatalf("top ship dispatch: %v", err)
	}
	if _, err := s.CancelDispatch(topShip.ID); err != nil {
		t.Fatalf("cancel top ship: %v", err)
	}

	// The card behind it was manually shipped and the ship acked.
	behindShip, err := s.AddDispatch(store.AddDispatchIn{
		RepoID:        repo.ID,
		IssueID:       &behind.ID,
		Mode:          model.DispatchModeShip,
		CreatedBy:     model.ControllerActor,
		InitialStatus: model.DispatchQueued,
	})
	if err != nil {
		t.Fatalf("behind ship dispatch: %v", err)
	}
	simulateWorkerAck(t, s, behindShip.ID)

	// One tick (auto-ship off): the advance pass sweeps the whole column,
	// so the acked card behind the wedged top advances to done while the
	// wedged top stays put.
	if _, err := eng.AutoShipTick(); err != nil {
		t.Fatalf("tick: %v", err)
	}
	gotBehind, _ := s.GetIssueByID(behind.ID)
	if gotBehind.State != model.StateDone {
		t.Fatalf("behind card state = %s, want done (acked ship behind a wedged top must advance)", gotBehind.State)
	}
	gotTop, _ := s.GetIssueByID(top.ID)
	if gotTop.State != model.StateToBeShipped {
		t.Fatalf("wedged top state = %s, want to_be_shipped (a cancelled ship stays parked)", gotTop.State)
	}
}

// TestEngineHaltsOnOpenQuestion: while the running job has an open
// question the engine stamps engine_pause_reason; clearing the question
// clears the pause on the next tick.
func TestEngineHaltsOnOpenQuestion(t *testing.T) {
	s := newEngineStore(t)
	repo, iss := seedPipelineCard(t, s, "ENG3", "plan-implement", model.EngineAuto)
	eng := New(s)

	// Start the plan job.
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick start: %v", err)
	}
	r := runningJob(t, s, iss.ID)
	if r == nil {
		t.Fatal("no running job after start")
	}

	// Park an open question on the running job (what the MCP path will do
	// in a later phase).
	sess, err := s.UpsertAgentSession(store.UpsertAgentSessionIn{SessionID: "q-sess", RepoID: repo.ID, Actor: "agent-q"})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	qres, err := s.DB.Exec(
		`INSERT INTO agent_session_questions (session_pk, request_uuid, payload_json, state, asked_by, pipeline_job_id) VALUES (?, 'rq-halt', '{}', 'open', 'agent-q', ?)`,
		sess.ID, r.ID,
	)
	if err != nil {
		t.Fatalf("park question: %v", err)
	}
	qid, _ := qres.LastInsertId()

	// Tick: dispatch still in flight + open question → pause stamped.
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick halt: %v", err)
	}
	got, _ := s.GetIssueByID(iss.ID)
	if got.EnginePauseReason != model.EnginePauseReasonOpenQuestion {
		t.Fatalf("pause reason = %q, want open_question", got.EnginePauseReason)
	}

	// Answer the question; next tick clears the pause.
	if _, err := s.DB.Exec(`UPDATE agent_session_questions SET state='answered' WHERE id=?`, qid); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick resume: %v", err)
	}
	got, _ = s.GetIssueByID(iss.ID)
	if got.EnginePauseReason != "" {
		t.Fatalf("pause reason = %q, want cleared", got.EnginePauseReason)
	}
}

// TestEngineManualStartDrivesChain: with Auto off, repeated StartNext
// (the manual Start control) drives the chain one job at a time, and the
// final StartNext at the ship sentinel hands off to to_be_shipped.
func TestEngineManualStartDrivesChain(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "MAN1", "plan-implement-ship", model.EngineOff)
	eng := New(s)

	// Start plan.
	if _, err := eng.StartNext(iss.ID); err != nil {
		t.Fatalf("StartNext plan: %v", err)
	}
	r := runningJob(t, s, iss.ID)
	if r == nil || r.Mode != model.BuiltinTemplatePlan {
		t.Fatalf("StartNext didn't start plan: %+v", r)
	}
	// A second StartNext is a no-op while a job runs.
	if _, err := eng.StartNext(iss.ID); err != nil {
		t.Fatalf("StartNext (no-op): %v", err)
	}
	if r2 := runningJob(t, s, iss.ID); r2 == nil || r2.ID != r.ID {
		t.Fatalf("second StartNext changed the running job")
	}

	// Ack + Tick completes plan (manual completion detection).
	simulateWorkerAck(t, s, *r.DispatchID)
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}
	// Start implement.
	if _, err := eng.StartNext(iss.ID); err != nil {
		t.Fatalf("StartNext implement: %v", err)
	}
	r = runningJob(t, s, iss.ID)
	if r == nil || r.Mode != model.BuiltinTemplateImplement {
		t.Fatalf("StartNext didn't start implement: %+v", r)
	}
	simulateWorkerAck(t, s, *r.DispatchID)
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// Next StartNext is at the ship sentinel → hand off.
	if _, err := eng.StartNext(iss.ID); err != nil {
		t.Fatalf("StartNext handoff: %v", err)
	}
	got, _ := s.GetIssueByID(iss.ID)
	if got.State != model.StateToBeShipped {
		t.Fatalf("state = %s, want to_be_shipped", got.State)
	}
}

// TestEngineAutoChainNoShip drives a no-Ship process (plan→implement)
// under Auto: once implement acks, the card must finish in place — it
// stays in_pipeline with every job complete and is NOT handed off to
// to_be_shipped. Regression for BACI-277.
func TestEngineAutoChainNoShip(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "ENGN", "plan-implement", model.EngineAuto)
	eng := New(s)

	// Tick 1: starts the plan job.
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	r := runningJob(t, s, iss.ID)
	if r == nil || r.Mode != model.BuiltinTemplatePlan {
		t.Fatalf("after tick 1 running = %+v, want plan", r)
	}
	simulateWorkerAck(t, s, *r.DispatchID)

	// Tick 2: plan completes, implement starts.
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	r = runningJob(t, s, iss.ID)
	if r == nil || r.Mode != model.BuiltinTemplateImplement {
		t.Fatalf("after tick 2 running = %+v, want implement", r)
	}
	simulateWorkerAck(t, s, *r.DispatchID)

	// Tick 3: implement completes, chain is exhausted with no ship
	// sentinel → the card finishes in place, NOT to_be_shipped.
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	got, _ := s.GetIssueByID(iss.ID)
	if got.State != model.StateInPipeline {
		t.Fatalf("state = %s, want in_pipeline (no-Ship process must not auto-ship)", got.State)
	}
	jobs, _ := s.ListPipelineJobs(iss.ID)
	for _, j := range jobs {
		if j.Status != model.JobComplete {
			t.Errorf("job seq=%d mode=%s status=%s, want complete", j.Sequence, j.Mode, j.Status)
		}
	}

	// A further Tick must stay a no-op (idle): still in_pipeline, no job
	// started.
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick 4: %v", err)
	}
	if r := runningJob(t, s, iss.ID); r != nil {
		t.Fatalf("exhausted no-Ship chain started a job: %+v", r)
	}
	got, _ = s.GetIssueByID(iss.ID)
	if got.State != model.StateInPipeline {
		t.Fatalf("state = %s after extra tick, want in_pipeline", got.State)
	}
}

// TestEngineManualStartNoShip: a manual Start at the end of a no-Ship
// chain is a no-op — the card finishes in place rather than handing off
// to Shipping. Regression for BACI-277 on the shared manual-Start path.
func TestEngineManualStartNoShip(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "MANN", "plan-implement", model.EngineOff)
	eng := New(s)

	// Start plan, ack, Tick to complete it.
	if _, err := eng.StartNext(iss.ID); err != nil {
		t.Fatalf("StartNext plan: %v", err)
	}
	r := runningJob(t, s, iss.ID)
	if r == nil || r.Mode != model.BuiltinTemplatePlan {
		t.Fatalf("StartNext didn't start plan: %+v", r)
	}
	simulateWorkerAck(t, s, *r.DispatchID)
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// Start implement, ack, Tick to complete it.
	if _, err := eng.StartNext(iss.ID); err != nil {
		t.Fatalf("StartNext implement: %v", err)
	}
	r = runningJob(t, s, iss.ID)
	if r == nil || r.Mode != model.BuiltinTemplateImplement {
		t.Fatalf("StartNext didn't start implement: %+v", r)
	}
	simulateWorkerAck(t, s, *r.DispatchID)
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// Chain exhausted (no ship sentinel) → a final StartNext is a no-op.
	if _, err := eng.StartNext(iss.ID); err != nil {
		t.Fatalf("StartNext at chain end: %v", err)
	}
	got, _ := s.GetIssueByID(iss.ID)
	if got.State != model.StateInPipeline {
		t.Fatalf("state = %s, want in_pipeline (manual Start must not ship a no-Ship card)", got.State)
	}
	if r := runningJob(t, s, iss.ID); r != nil {
		t.Fatalf("StartNext at chain end started a job: %+v", r)
	}
}

// TestEngineShelveReturnsToBacklog: an Auto scope-shelve card runs scope,
// then on reaching the shelve sentinel demotes back to todo (Backlog) and
// out of the Pipeline (BACI-332) — the demoting counterpart to the ship
// hand-off.
func TestEngineShelveReturnsToBacklog(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "SHLV", "scope-shelve", model.EngineAuto)
	eng := New(s)

	// Tick 1: starts the scope job.
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	r := runningJob(t, s, iss.ID)
	if r == nil || r.Mode != model.BuiltinTemplateScope {
		t.Fatalf("after tick 1 running = %+v, want scope", r)
	}
	simulateWorkerAck(t, s, *r.DispatchID)

	// Tick 2: scope completes, next is the shelve sentinel → demote to todo.
	advances, err := eng.Tick()
	if err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	got, _ := s.GetIssueByID(iss.ID)
	if got.State != model.StateTodo {
		t.Fatalf("state = %s, want todo", got.State)
	}
	var sawShelve bool
	for _, a := range advances {
		if a.Kind == "shelve" {
			sawShelve = true
		}
	}
	if !sawShelve {
		t.Fatalf("advances = %+v, want one shelve", advances)
	}
}

// TestEngineManualStartShelve: a manual (Auto off) scope-shelve card
// reaches the shelve sentinel via StartNext and demotes to todo —
// manual and Auto behave identically (mirrors TestEngineManualStartNoShip).
func TestEngineManualStartShelve(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "SHLM", "scope-shelve", model.EngineOff)
	eng := New(s)

	// Start scope, ack, Tick to complete it.
	if _, err := eng.StartNext(iss.ID); err != nil {
		t.Fatalf("StartNext scope: %v", err)
	}
	r := runningJob(t, s, iss.ID)
	if r == nil || r.Mode != model.BuiltinTemplateScope {
		t.Fatalf("StartNext didn't start scope: %+v", r)
	}
	simulateWorkerAck(t, s, *r.DispatchID)
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// Start again → reaches the shelve sentinel and demotes to todo.
	if _, err := eng.StartNext(iss.ID); err != nil {
		t.Fatalf("StartNext shelve: %v", err)
	}
	got, _ := s.GetIssueByID(iss.ID)
	if got.State != model.StateTodo {
		t.Fatalf("state = %s, want todo (manual Start must shelve)", got.State)
	}
}

// TestEngineShelveClearsChainForRepipe: after a shelve the card's job
// chain is wiped, and a fresh process can be set on the re-piped card —
// proving a shelved card carries no permanent penalty and is re-pipeable
// (AC #5).
func TestEngineShelveClearsChainForRepipe(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "SHLR", "scope-shelve", model.EngineAuto)
	eng := New(s)

	// Drive scope → shelve.
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	r := runningJob(t, s, iss.ID)
	if r == nil {
		t.Fatal("no running scope job after tick 1")
	}
	simulateWorkerAck(t, s, *r.DispatchID)
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick 2: %v", err)
	}

	// The whole chain is gone — indistinguishable from a card never piped.
	jobs, err := s.ListPipelineJobs(iss.ID)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("chain after shelve has %d jobs, want empty", len(jobs))
	}

	// Re-pipe: move back to in_pipeline and set a fresh process.
	if err := s.SetIssueState(iss.ID, model.StateInPipeline); err != nil {
		t.Fatalf("re-pipe state: %v", err)
	}
	proc, _ := model.ProcessBySlug("plan-implement")
	if _, err := s.SetIssueProcess(iss.ID, proc); err != nil {
		t.Fatalf("re-pipe set process: %v", err)
	}
	jobs, _ = s.ListPipelineJobs(iss.ID)
	if len(jobs) != 2 {
		t.Fatalf("re-piped chain has %d jobs, want 2 (plan-implement)", len(jobs))
	}
}

// TestEngineStopRunning: Stop cancels the running job and halts Auto.
// TestEngineHandoffAppendsToShippingBack — BACI-275: two cards handed
// off through the engine into to_be_shipped must preserve arrival order.
// The first card to reach to_be_shipped is the TopShippingIssue; the
// second sits strictly behind it, never jumping the queue.
func TestEngineHandoffAppendsToShippingBack(t *testing.T) {
	s := newEngineStore(t)
	repo, first := seedPipelineCard(t, s, "SHIP", "implement-ship", model.EngineAuto)
	eng := New(s)

	// drive runs a single implement-ship card from in_pipeline to
	// to_be_shipped: tick starts implement, the worker acks, the next
	// tick completes implement and hands off to the ship sentinel.
	drive := func(iss *model.Issue) {
		t.Helper()
		if _, err := eng.Tick(); err != nil {
			t.Fatalf("tick (start implement) for %d: %v", iss.ID, err)
		}
		r := runningJob(t, s, iss.ID)
		if r == nil || r.Mode != model.BuiltinTemplateImplement {
			t.Fatalf("running job for %d = %+v, want implement", iss.ID, r)
		}
		simulateWorkerAck(t, s, *r.DispatchID)
		if _, err := eng.Tick(); err != nil {
			t.Fatalf("tick (handoff) for %d: %v", iss.ID, err)
		}
		got, _ := s.GetIssueByID(iss.ID)
		if got.State != model.StateToBeShipped {
			t.Fatalf("issue %d state = %s, want to_be_shipped", iss.ID, got.State)
		}
	}

	drive(first)

	// Second card arrives in the same repo and is handed off after the
	// first is already queued.
	second, err := s.CreateIssue(repo.ID, nil, "second", "", model.StateInPipeline, nil, "")
	if err != nil {
		t.Fatalf("create second issue: %v", err)
	}
	proc, _ := model.ProcessBySlug("implement-ship")
	if _, err := s.SetIssueProcess(second.ID, proc); err != nil {
		t.Fatalf("set process: %v", err)
	}
	if err := s.SetIssueEngineMode(second.ID, model.EngineAuto); err != nil {
		t.Fatalf("set engine mode: %v", err)
	}
	drive(second)

	top, err := s.TopShippingIssue(repo.ID)
	if err != nil {
		t.Fatalf("top shipping: %v", err)
	}
	if top == nil || top.ID != first.ID {
		t.Fatalf("top shipping = %v, want first (%d)", top, first.ID)
	}
	gotFirst, _ := s.GetIssueByID(first.ID)
	gotSecond, _ := s.GetIssueByID(second.ID)
	if gotFirst.Priority >= gotSecond.Priority {
		t.Fatalf("first priority %d must be < second priority %d (second sits behind)", gotFirst.Priority, gotSecond.Priority)
	}
}

func TestEngineStopRunning(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "STOP", "plan-implement", model.EngineAuto)
	eng := New(s)

	// Auto starts the plan job.
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if runningJob(t, s, iss.ID) == nil {
		t.Fatal("no running job to stop")
	}
	if _, err := eng.StopRunning(iss.ID); err != nil {
		t.Fatalf("StopRunning: %v", err)
	}
	jobs, _ := s.ListPipelineJobs(iss.ID)
	if jobs[0].Status != model.JobCancelled {
		t.Fatalf("job 1 status = %s, want cancelled", jobs[0].Status)
	}
	got, _ := s.GetIssueByID(iss.ID)
	if got.EngineMode != model.EngineOff {
		t.Fatalf("engine mode = %s, want off (Auto halted)", got.EngineMode)
	}
	// A follow-up Tick must NOT auto-advance (Auto is now off).
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("post-stop tick: %v", err)
	}
	if runningJob(t, s, iss.ID) != nil {
		t.Fatal("post-stop tick started a new job despite Auto off")
	}
}

// TestResetProcessClearsAndDisarms: Reset on a card whose chain has started
// (a completed prefix + pending tail, nothing running) wipes every job and
// disarms Auto so the freshly-emptied card can't silently resume.
func TestResetProcessClearsAndDisarms(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "RST", "plan-implement", model.EngineAuto)
	eng := New(s)

	// Auto starts the plan job; ack it so plan completes and implement runs.
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}
	job := runningJob(t, s, iss.ID)
	if job == nil {
		t.Fatal("no running job after first tick")
	}
	simulateWorkerAck(t, s, *job.DispatchID)
	if _, err := eng.Tick(); err != nil { // plan completes, implement starts
		t.Fatalf("tick: %v", err)
	}
	// Stop the now-running implement job so the chain has a started prefix
	// but nothing running (Reset requires no running job).
	if _, err := eng.StopRunning(iss.ID); err != nil {
		t.Fatalf("StopRunning: %v", err)
	}
	if jobs, _ := s.ListPipelineJobs(iss.ID); len(jobs) == 0 {
		t.Fatal("expected a started chain to reset")
	}

	if err := eng.ResetProcess(iss.ID); err != nil {
		t.Fatalf("ResetProcess: %v", err)
	}
	after, err := s.ListPipelineJobs(iss.ID)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("chain after reset has %d jobs, want empty", len(after))
	}
	got, _ := s.GetIssueByID(iss.ID)
	if got.EngineMode != model.EngineOff {
		t.Fatalf("engine mode = %s, want off (Auto disarmed)", got.EngineMode)
	}
	if got.EnginePauseReason != "" {
		t.Fatalf("pause reason = %q, want cleared", got.EnginePauseReason)
	}
}

// TestResetProcessRejectsRunning: Reset refuses with ErrJobRunning while a
// job is running and leaves the chain intact — the user must Stop first.
func TestResetProcessRejectsRunning(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "RSR", "plan-implement", model.EngineAuto)
	eng := New(s)

	if _, err := eng.Tick(); err != nil { // Auto starts the plan job
		t.Fatalf("tick: %v", err)
	}
	if runningJob(t, s, iss.ID) == nil {
		t.Fatal("no running job to guard against")
	}
	before, _ := s.ListPipelineJobs(iss.ID)

	if err := eng.ResetProcess(iss.ID); !errors.Is(err, ErrJobRunning) {
		t.Fatalf("ResetProcess err = %v, want ErrJobRunning", err)
	}
	after, _ := s.ListPipelineJobs(iss.ID)
	if len(after) != len(before) {
		t.Fatalf("chain length after rejected reset = %d, want %d (no rows deleted)", len(after), len(before))
	}
}

// claimRunningJob stands in for "a worker opened its claim on the card and
// is running the in-flight job": register a session and open a claim on
// the issue, returning the external session id the steer message targets.
func claimRunningJob(t *testing.T, s *store.Store, iss *model.Issue, sessionID string) string {
	t.Helper()
	if _, err := s.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: sessionID, RepoID: iss.RepoID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register session: %v", err)
	}
	if _, _, _, _, err := s.AddAgentClaim(sessionID, iss.ID, "implement"); err != nil {
		t.Fatalf("add claim: %v", err)
	}
	return sessionID
}

// TestStopRunningSteersWorker: when a worker holds an open claim on the
// stopped card, Stop enqueues the canned wind-down steer message at that
// session (alongside the existing cancel + Auto-halt bookkeeping).
func TestStopRunningSteersWorker(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "STR", "plan-implement", model.EngineAuto)
	eng := New(s)

	if _, err := eng.Tick(); err != nil { // Auto starts the plan job
		t.Fatalf("tick: %v", err)
	}
	if runningJob(t, s, iss.ID) == nil {
		t.Fatal("no running job to stop")
	}
	sessionID := claimRunningJob(t, s, iss, "11111111-1111-1111-1111-111111111111")

	if _, err := eng.StopRunning(iss.ID); err != nil {
		t.Fatalf("StopRunning: %v", err)
	}

	// Bookkeeping still happened.
	jobs, _ := s.ListPipelineJobs(iss.ID)
	if jobs[0].Status != model.JobCancelled {
		t.Fatalf("job 1 status = %s, want cancelled", jobs[0].Status)
	}
	if got, _ := s.GetIssueByID(iss.ID); got.EngineMode != model.EngineOff {
		t.Fatalf("engine mode = %s, want off", got.EngineMode)
	}

	// AND the worker got the steer message.
	msgs, err := s.DrainUserMessagesForSession(sessionID)
	if err != nil {
		t.Fatalf("drain messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("steer messages = %d, want 1", len(msgs))
	}
	if msgs[0].Body != stopWorkerSteerBody {
		t.Fatalf("steer body = %q, want the canned stop body", msgs[0].Body)
	}
}

// TestStopRunningNoClaimNoMessage: a running job with no open claim/session
// (dispatch never delivered, or worker already released) is still cancelled
// and Auto halted, but no steer message is written and Stop doesn't error.
func TestStopRunningNoClaimNoMessage(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "NCM", "plan-implement", model.EngineAuto)
	eng := New(s)

	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if runningJob(t, s, iss.ID) == nil {
		t.Fatal("no running job to stop")
	}
	// Register a session but DON'T claim the issue — no open claim to steer.
	otherSession := "22222222-2222-2222-2222-222222222222"
	if _, err := s.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: otherSession, RepoID: iss.RepoID, Actor: "agent-claude",
	}); err != nil {
		t.Fatalf("register session: %v", err)
	}

	if _, err := eng.StopRunning(iss.ID); err != nil {
		t.Fatalf("StopRunning: %v", err)
	}
	jobs, _ := s.ListPipelineJobs(iss.ID)
	if jobs[0].Status != model.JobCancelled {
		t.Fatalf("job 1 status = %s, want cancelled", jobs[0].Status)
	}
	msgs, err := s.DrainUserMessagesForSession(otherSession)
	if err != nil {
		t.Fatalf("drain messages: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("steer messages = %d, want 0 (no open claim)", len(msgs))
	}
}

// TestRerunJobRedispatches: Stop a running job (now cancelled), then
// RerunJob on it → the job is running again with a fresh dispatch, and the
// reset cleared then re-stamped started_at.
func TestRerunJobRedispatches(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "RRN", "plan-implement", model.EngineAuto)
	eng := New(s)

	if _, err := eng.Tick(); err != nil { // Auto starts plan
		t.Fatalf("tick: %v", err)
	}
	job := runningJob(t, s, iss.ID)
	if job == nil {
		t.Fatal("no running job")
	}
	firstDispatch := *job.DispatchID

	if _, err := eng.StopRunning(iss.ID); err != nil {
		t.Fatalf("StopRunning: %v", err)
	}

	adv, err := eng.RerunJob(iss.ID, job.ID)
	if err != nil {
		t.Fatalf("RerunJob: %v", err)
	}
	if len(adv) != 1 || adv[0].Kind != "job.start" {
		t.Fatalf("advances = %+v, want one job.start", adv)
	}
	got, _ := s.GetPipelineJob(job.ID)
	if got.Status != model.JobRunning {
		t.Fatalf("re-run job status = %s, want running", got.Status)
	}
	if got.DispatchID == nil || *got.DispatchID == firstDispatch {
		t.Fatalf("re-run dispatch = %v, want a fresh one (not %d)", got.DispatchID, firstDispatch)
	}
	if got.StartedAt == nil {
		t.Fatal("re-run job started_at not re-stamped")
	}
	if got.CompletedAt != nil {
		t.Fatalf("re-run job completed_at = %v, want nil (reset cleared it)", got.CompletedAt)
	}
}

// TestRerunJobRejectsNonCancelled: RerunJob on a complete job returns
// ErrJobNotCancelled; on a running job (one already in flight) it's a
// no-op (nil) so it never double-dispatches.
func TestRerunJobRejectsNonCancelled(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "RRJ", "plan-implement", model.EngineAuto)
	eng := New(s)

	if _, err := eng.Tick(); err != nil { // Auto starts plan
		t.Fatalf("tick: %v", err)
	}
	job := runningJob(t, s, iss.ID)
	if job == nil {
		t.Fatal("no running job")
	}

	// A running job → no-op (a job is in flight), not an error.
	adv, err := eng.RerunJob(iss.ID, job.ID)
	if err != nil {
		t.Fatalf("RerunJob on running: unexpected err %v", err)
	}
	if adv != nil {
		t.Fatalf("RerunJob on running returned %+v, want nil (no-op)", adv)
	}

	// Complete the plan job (ack), then re-run it → ErrJobNotCancelled.
	simulateWorkerAck(t, s, *job.DispatchID)
	if _, err := eng.Tick(); err != nil { // reconcile → complete, advance starts implement
		t.Fatalf("tick: %v", err)
	}
	// Stop the now-running implement so no job is in flight for the guard test.
	if _, err := eng.StopRunning(iss.ID); err != nil {
		t.Fatalf("StopRunning: %v", err)
	}
	if _, err := eng.RerunJob(iss.ID, job.ID); !errors.Is(err, ErrJobNotCancelled) {
		t.Fatalf("RerunJob on complete job err = %v, want ErrJobNotCancelled", err)
	}
}

// TestFailRunning_Transient locks in the transient branch: a
// server_error cancels the running job, halts Auto, sets the transient
// agent_error pause reason, and leaves the card in_pipeline (no
// auto-retry, user re-arms).
func TestFailRunning_Transient(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "FAIL", "plan-implement", model.EngineAuto)
	eng := New(s)

	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if runningJob(t, s, iss.ID) == nil {
		t.Fatal("no running job to fail")
	}

	advances, err := eng.FailRunning(iss.ID, "server_error", "529 overloaded")
	if err != nil {
		t.Fatalf("FailRunning: %v", err)
	}
	if len(advances) != 1 || advances[0].Kind != "job.failed" {
		t.Fatalf("advances = %+v, want one job.failed", advances)
	}
	jobs, _ := s.ListPipelineJobs(iss.ID)
	if jobs[0].Status != model.JobCancelled {
		t.Fatalf("job 1 status = %s, want cancelled", jobs[0].Status)
	}
	got, _ := s.GetIssueByID(iss.ID)
	if got.State != model.StateInPipeline {
		t.Fatalf("state = %s, want in_pipeline (transient pauses in place)", got.State)
	}
	if got.EngineMode != model.EngineOff {
		t.Fatalf("engine mode = %s, want off", got.EngineMode)
	}
	if got.EnginePauseReason != model.EnginePauseReasonAgentErrorTransient {
		t.Fatalf("pause reason = %q, want %q", got.EnginePauseReason, model.EnginePauseReasonAgentErrorTransient)
	}
	// A follow-up Tick must NOT auto-advance (Auto is off).
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("post-fail tick: %v", err)
	}
	if runningJob(t, s, iss.ID) != nil {
		t.Fatal("post-fail tick started a new job despite Auto off")
	}
}

// TestFailRunning_Terminal locks in the BACI-300 terminal branch: an
// authentication_failed cancels the running job, halts Auto, and pauses
// the card IN PLACE with the terminal agent_error pause reason — the
// card stays in_pipeline (pre-BACI-300 this yanked it out to
// needs_action, a state that no longer exists).
func TestFailRunning_Terminal(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "TERM", "plan-implement", model.EngineAuto)
	eng := New(s)

	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if runningJob(t, s, iss.ID) == nil {
		t.Fatal("no running job to fail")
	}

	advances, err := eng.FailRunning(iss.ID, "authentication_failed", "401")
	if err != nil {
		t.Fatalf("FailRunning: %v", err)
	}
	if len(advances) != 1 || advances[0].Kind != "job.failed" {
		t.Fatalf("advances = %+v, want one job.failed", advances)
	}
	got, _ := s.GetIssueByID(iss.ID)
	if got.State != model.StateInPipeline {
		t.Fatalf("state = %s, want in_pipeline (terminal pauses in place)", got.State)
	}
	if got.EnginePauseReason != model.EnginePauseReasonAgentErrorTerminal {
		t.Fatalf("pause reason = %q, want %q", got.EnginePauseReason, model.EnginePauseReasonAgentErrorTerminal)
	}
	if got.EngineMode != model.EngineOff {
		t.Fatalf("engine mode = %s, want off", got.EngineMode)
	}
	jobs, _ := s.ListPipelineJobs(iss.ID)
	if jobs[0].Status != model.JobCancelled {
		t.Fatalf("job 1 status = %s, want cancelled", jobs[0].Status)
	}
}

// TestFailRunning_NoRunningJobIsNoop locks in that a card with no running
// job (and a card not in_pipeline) is a clean no-op — a worker that died
// before claiming has nothing to reconcile.
func TestFailRunning_NoRunningJobIsNoop(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "NOOP", "plan-implement", model.EngineAuto)
	eng := New(s)
	// No Tick — no job is running yet.
	advances, err := eng.FailRunning(iss.ID, "server_error", "x")
	if err != nil {
		t.Fatalf("FailRunning: %v", err)
	}
	if len(advances) != 0 {
		t.Fatalf("advances = %+v, want none", advances)
	}
	got, _ := s.GetIssueByID(iss.ID)
	if got.State != model.StateInPipeline {
		t.Fatalf("state = %s, want unchanged in_pipeline", got.State)
	}
}

// TestCancelRunning_PausesNeutral locks in the BACI-328 user-cancel path:
// a soft cancel cancels the running job + its dispatch, halts Auto, and
// pauses the card IN PLACE with the neutral subagent_cancelled pause reason
// — the card stays in_pipeline (a deliberate cancel, not a failure), and a
// follow-up Tick must not auto-advance.
func TestCancelRunning_PausesNeutral(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "CANC", "plan-implement", model.EngineAuto)
	eng := New(s)

	if _, err := eng.Tick(); err != nil {
		t.Fatalf("tick: %v", err)
	}
	running := runningJob(t, s, iss.ID)
	if running == nil {
		t.Fatal("no running job to cancel")
	}
	dispatchID := running.DispatchID

	advances, err := eng.CancelRunning(iss.ID)
	if err != nil {
		t.Fatalf("CancelRunning: %v", err)
	}
	if len(advances) != 1 || advances[0].Kind != "job.cancelled.subagent" {
		t.Fatalf("advances = %+v, want one job.cancelled.subagent", advances)
	}
	jobs, _ := s.ListPipelineJobs(iss.ID)
	if jobs[0].Status != model.JobCancelled {
		t.Fatalf("job 1 status = %s, want cancelled", jobs[0].Status)
	}
	got, _ := s.GetIssueByID(iss.ID)
	if got.State != model.StateInPipeline {
		t.Fatalf("state = %s, want in_pipeline (cancel pauses in place)", got.State)
	}
	if got.EngineMode != model.EngineOff {
		t.Fatalf("engine mode = %s, want off", got.EngineMode)
	}
	if got.EnginePauseReason != model.EnginePauseReasonSubagentCancelled {
		t.Fatalf("pause reason = %q, want %q", got.EnginePauseReason, model.EnginePauseReasonSubagentCancelled)
	}
	if dispatchID != nil {
		d, err := s.GetDispatch(*dispatchID)
		if err != nil {
			t.Fatalf("GetDispatch: %v", err)
		}
		if d.Status != model.DispatchCancelled {
			t.Fatalf("dispatch status = %s, want cancelled", d.Status)
		}
	}
	// A follow-up Tick must NOT auto-advance (Auto is off).
	if _, err := eng.Tick(); err != nil {
		t.Fatalf("post-cancel tick: %v", err)
	}
	if runningJob(t, s, iss.ID) != nil {
		t.Fatal("post-cancel tick started a new job despite Auto off")
	}
}

// TestCancelRunning_NoRunningJobIsNoop locks in idempotency: a card with no
// running job (a too-early cancel, or a job that already settled) is a
// clean no-op, so the report_subagent_incomplete tool is safe to call more
// than once.
func TestCancelRunning_NoRunningJobIsNoop(t *testing.T) {
	s := newEngineStore(t)
	_, iss := seedPipelineCard(t, s, "CNOP", "plan-implement", model.EngineAuto)
	eng := New(s)
	// No Tick — no job is running yet.
	advances, err := eng.CancelRunning(iss.ID)
	if err != nil {
		t.Fatalf("CancelRunning: %v", err)
	}
	if len(advances) != 0 {
		t.Fatalf("advances = %+v, want none", advances)
	}
	got, _ := s.GetIssueByID(iss.ID)
	if got.State != model.StateInPipeline {
		t.Fatalf("state = %s, want unchanged in_pipeline", got.State)
	}
	if got.EnginePauseReason != "" {
		t.Fatalf("pause reason = %q, want empty (no-op stamps nothing)", got.EnginePauseReason)
	}
}

