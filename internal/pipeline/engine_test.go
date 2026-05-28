package pipeline

import (
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
