package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestAddSessionQuestionParentsToRunningJob proves the MCP path's
// job-parenting: a question asked against an in_pipeline card whose chain
// has a running job lands on that job (activating the engine halt), while
// a question on a non-pipeline issue stays session-parented.
func TestAddSessionQuestionParentsToRunningJob(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.CreateRepo("QJOB", "qjob", t.TempDir(), "")
	if err != nil {
		t.Fatalf("repo: %v", err)
	}
	iss, err := s.CreateIssue(repo.ID, nil, "card", "", model.StateInPipeline, nil, "")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	proc, _ := model.ProcessBySlug("plan-implement")
	jobs, err := s.SetIssueProcess(iss.ID, proc)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	// Bring job 1 to running with a dispatch (what the engine StartNext does).
	d, err := s.AddDispatch(AddDispatchIn{
		RepoID: repo.ID, IssueID: &iss.ID,
		Mode: model.DispatchMode(jobs[0].Mode), CreatedBy: "agent-x",
		InitialStatus: model.DispatchQueued,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if won, err := s.StartPipelineJobWithDispatch(jobs[0].ID, d.ID); err != nil || !won {
		t.Fatalf("start job: won=%v err=%v", won, err)
	}
	if _, err := s.UpsertAgentSession(UpsertAgentSessionIn{SessionID: "qs", RepoID: repo.ID, Actor: "agent-x"}); err != nil {
		t.Fatalf("session: %v", err)
	}

	q, err := s.AddSessionQuestion(AddSessionQuestionIn{
		SessionID: "qs", IssueKey: iss.Key, Payload: validSinglePayload(), AskedBy: "agent-x",
	})
	if err != nil {
		t.Fatalf("AddSessionQuestion: %v", err)
	}
	if q.PipelineJobID == nil || *q.PipelineJobID != jobs[0].ID {
		t.Fatalf("question pipeline_job_id = %v, want %d", q.PipelineJobID, jobs[0].ID)
	}
	has, err := s.HasOpenQuestionForJob(jobs[0].ID)
	if err != nil {
		t.Fatalf("HasOpenQuestionForJob: %v", err)
	}
	if !has {
		t.Fatal("HasOpenQuestionForJob = false, want true (engine halt would not fire)")
	}

	// A question on a non-pipeline issue stays session-parented (nil job).
	iss2, err := s.CreateIssue(repo.ID, nil, "todo", "", model.StateTodo, nil, "")
	if err != nil {
		t.Fatalf("issue2: %v", err)
	}
	q2, err := s.AddSessionQuestion(AddSessionQuestionIn{
		SessionID: "qs", IssueKey: iss2.Key, Payload: validSinglePayload(), AskedBy: "agent-x",
	})
	if err != nil {
		t.Fatalf("AddSessionQuestion 2: %v", err)
	}
	if q2.PipelineJobID != nil {
		t.Fatalf("non-pipeline question parented to job %v, want nil", q2.PipelineJobID)
	}
}
