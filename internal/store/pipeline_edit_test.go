package store

import (
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestEditIssueProcessTail covers the BACI-294 locked-prefix-preserving
// edit: the completed / running jobs stay verbatim as the locked prefix
// and the pending tail is replaced + re-sequenced after them.
func TestEditIssueProcessTail(t *testing.T) {
	t.Run("preserves_locked_prefix_resequences_tail", func(t *testing.T) {
		s, _, iss := seedRepoAndIssue(t)
		proc, err := model.ProcessFromStages([]string{model.BuiltinTemplatePlan, model.BuiltinTemplateImplement, model.ShipJobMode})
		if err != nil {
			t.Fatalf("ProcessFromStages: %v", err)
		}
		jobs, err := s.SetIssueProcess(iss.ID, proc)
		if err != nil {
			t.Fatalf("SetIssueProcess: %v", err)
		}
		// Lock the first two jobs: plan complete, implement running.
		if err := s.SetPipelineJobStatus(jobs[0].ID, model.JobComplete); err != nil {
			t.Fatalf("complete job 1: %v", err)
		}
		if err := s.SetPipelineJobStatus(jobs[1].ID, model.JobRunning); err != nil {
			t.Fatalf("run job 2: %v", err)
		}

		// Edit: replace the pending tail (was just [ship]) with
		// [review, implement, ship].
		newTail := []string{model.BuiltinTemplateReview, model.BuiltinTemplateImplement, model.ShipJobMode}
		got, err := s.EditIssueProcessTail(iss.ID, newTail)
		if err != nil {
			t.Fatalf("EditIssueProcessTail: %v", err)
		}

		// Whole chain is now: plan(1,complete) implement(2,running)
		// review(3) implement(4) ship(5).
		wantModes := []string{
			model.BuiltinTemplatePlan, model.BuiltinTemplateImplement,
			model.BuiltinTemplateReview, model.BuiltinTemplateImplement, model.ShipJobMode,
		}
		if len(got) != len(wantModes) {
			t.Fatalf("chain length = %d, want %d (%v)", len(got), len(wantModes), modesOf(got))
		}
		for i, j := range got {
			if j.Sequence != i+1 {
				t.Errorf("job %d sequence = %d, want %d", i, j.Sequence, i+1)
			}
			if j.Mode != wantModes[i] {
				t.Errorf("job %d mode = %q, want %q", i, j.Mode, wantModes[i])
			}
		}
		// Locked prefix is verbatim: same row ids and statuses (the rows are
		// never touched, so dispatch_id / timestamps survive too).
		if got[0].ID != jobs[0].ID || got[0].Status != model.JobComplete {
			t.Errorf("locked job 1 changed: id=%d status=%s", got[0].ID, got[0].Status)
		}
		if got[1].ID != jobs[1].ID || got[1].Status != model.JobRunning {
			t.Errorf("locked job 2 changed: id=%d status=%s", got[1].ID, got[1].Status)
		}
		// New tail jobs are fresh pending rows.
		for _, j := range got[2:] {
			if j.Status != model.JobPending {
				t.Errorf("tail job seq %d status = %s, want pending", j.Sequence, j.Status)
			}
			if j.DispatchID != nil {
				t.Errorf("tail job seq %d has a dispatch_id, want nil", j.Sequence)
			}
		}
		// The engine's next pending job is the head of the new tail (seq 3).
		next := firstPending(got)
		if next == nil || next.Sequence != 3 || next.Mode != model.BuiltinTemplateReview {
			t.Errorf("first pending = %+v, want seq 3 review", next)
		}
	})

	t.Run("empty_prefix_replaces_whole_chain", func(t *testing.T) {
		s, _, iss := seedRepoAndIssue(t)
		proc, _ := model.ProcessFromStages([]string{model.BuiltinTemplatePlan, model.BuiltinTemplateImplement})
		if _, err := s.SetIssueProcess(iss.ID, proc); err != nil {
			t.Fatalf("SetIssueProcess: %v", err)
		}
		// Nothing started → every job is pending → the whole chain is the tail.
		got, err := s.EditIssueProcessTail(iss.ID, []string{model.BuiltinTemplateDesign, model.ShipJobMode})
		if err != nil {
			t.Fatalf("EditIssueProcessTail: %v", err)
		}
		want := []string{model.BuiltinTemplateDesign, model.ShipJobMode}
		if got := modesOf(got); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("chain = %v, want %v", got, want)
		}
		if got[0].Sequence != 1 || got[1].Sequence != 2 {
			t.Fatalf("sequences = %d,%d, want 1,2", got[0].Sequence, got[1].Sequence)
		}
	})

	t.Run("rejects_empty_chain", func(t *testing.T) {
		s, _, iss := seedRepoAndIssue(t)
		if _, err := s.EditIssueProcessTail(iss.ID, []string{model.BuiltinTemplatePlan}); err == nil {
			t.Error("EditIssueProcessTail on a card with no chain succeeded, want error")
		}
	})

	t.Run("rejects_invalid_tail", func(t *testing.T) {
		s, _, iss := seedRepoAndIssue(t)
		proc, _ := model.ProcessFromStages([]string{model.BuiltinTemplateImplement})
		if _, err := s.SetIssueProcess(iss.ID, proc); err != nil {
			t.Fatalf("SetIssueProcess: %v", err)
		}
		// Ship in the middle of the tail is rejected; the chain stays intact.
		if _, err := s.EditIssueProcessTail(iss.ID, []string{model.ShipJobMode, model.BuiltinTemplateReview}); err == nil {
			t.Error("expected error for ship-not-last tail")
		}
		after, err := s.ListPipelineJobs(iss.ID)
		if err != nil {
			t.Fatalf("ListPipelineJobs: %v", err)
		}
		if got := modesOf(after); len(got) != 1 || got[0] != model.BuiltinTemplateImplement {
			t.Fatalf("chain mutated on rejected edit: %v", got)
		}
	})
}

// TestClearIssueProcess covers the BACI-314 Reset write: ClearIssueProcess
// deletes EVERY job regardless of status — the deliberate bypass of
// SetIssueProcess's started>0 guard that Reset needs to wipe a chain that
// has already run.
func TestClearIssueProcess(t *testing.T) {
	s, _, iss := seedRepoAndIssue(t)
	proc, err := model.ProcessFromStages([]string{model.BuiltinTemplatePlan, model.BuiltinTemplateImplement, model.ShipJobMode})
	if err != nil {
		t.Fatalf("ProcessFromStages: %v", err)
	}
	jobs, err := s.SetIssueProcess(iss.ID, proc)
	if err != nil {
		t.Fatalf("SetIssueProcess: %v", err)
	}
	// Mixed statuses: complete + cancelled + pending — exactly the chain
	// SetIssueProcess would refuse to clobber. ClearIssueProcess must not.
	if err := s.SetPipelineJobStatus(jobs[0].ID, model.JobComplete); err != nil {
		t.Fatalf("complete job 1: %v", err)
	}
	if err := s.SetPipelineJobStatus(jobs[1].ID, model.JobCancelled); err != nil {
		t.Fatalf("cancel job 2: %v", err)
	}

	if err := s.ClearIssueProcess(iss.ID); err != nil {
		t.Fatalf("ClearIssueProcess: %v", err)
	}
	after, err := s.ListPipelineJobs(iss.ID)
	if err != nil {
		t.Fatalf("ListPipelineJobs: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("chain after clear = %v, want empty", modesOf(after))
	}

	// Idempotent: clearing a card with no chain is a no-op.
	if err := s.ClearIssueProcess(iss.ID); err != nil {
		t.Fatalf("ClearIssueProcess on empty chain: %v", err)
	}
}

func modesOf(jobs []*model.PipelineJob) []string {
	out := make([]string, len(jobs))
	for i, j := range jobs {
		out[i] = j.Mode
	}
	return out
}

func firstPending(jobs []*model.PipelineJob) *model.PipelineJob {
	for _, j := range jobs {
		if j.Status == model.JobPending {
			return j
		}
	}
	return nil
}
