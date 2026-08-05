package store

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestArmIssuePipeline covers the BACI-374 store composite behind the
// auto-run toggle: the card lands in_pipeline with the preset's chain
// materialised and engine Auto armed, and a failure from any of the three
// writes is surfaced rather than swallowed.
func TestArmIssuePipeline(t *testing.T) {
	proc, err := model.ProcessBySlug("scope-plan-implement-ship")
	if err != nil {
		t.Fatalf("ProcessBySlug: %v", err)
	}

	t.Run("arms_state_chain_and_auto", func(t *testing.T) {
		s, _, iss := seedRepoAndIssue(t)
		if err := s.ArmIssuePipeline(iss.ID, proc); err != nil {
			t.Fatalf("ArmIssuePipeline: %v", err)
		}

		got, err := s.GetIssueByID(iss.ID)
		if err != nil {
			t.Fatalf("GetIssueByID: %v", err)
		}
		if got.State != model.StateInPipeline {
			t.Errorf("state = %q, want %q", got.State, model.StateInPipeline)
		}
		if got.EngineMode != model.EngineAuto {
			t.Errorf("engine_mode = %q, want %q", got.EngineMode, model.EngineAuto)
		}

		jobs, err := s.ListPipelineJobs(iss.ID)
		if err != nil {
			t.Fatalf("ListPipelineJobs: %v", err)
		}
		wantModes := []string{
			model.BuiltinTemplateScope, model.BuiltinTemplatePlan,
			model.BuiltinTemplateImplement, model.ShipJobMode,
		}
		if len(jobs) != len(wantModes) {
			t.Fatalf("chain length = %d, want %d (%v)", len(jobs), len(wantModes), modesOf(jobs))
		}
		for i, j := range jobs {
			if j.Mode != wantModes[i] {
				t.Errorf("job %d mode = %q, want %q", i, j.Mode, wantModes[i])
			}
			if j.Sequence != i+1 {
				t.Errorf("job %d sequence = %d, want %d", i, j.Sequence, i+1)
			}
			if j.Status != model.JobPending {
				t.Errorf("job %d status = %s, want pending", i, j.Status)
			}
		}
	})

	t.Run("propagates_chain_error", func(t *testing.T) {
		// A started chain makes SetIssueProcess refuse; the composite must
		// return that error rather than leaving a silently chainless card
		// running under Auto.
		s, _, iss := seedRepoAndIssue(t)
		seeded, err := s.SetIssueProcess(iss.ID, proc)
		if err != nil {
			t.Fatalf("seed SetIssueProcess: %v", err)
		}
		if err := s.SetPipelineJobStatus(seeded[0].ID, model.JobRunning); err != nil {
			t.Fatalf("start job 1: %v", err)
		}

		err = s.ArmIssuePipeline(iss.ID, proc)
		if err == nil {
			t.Fatal("expected an error arming over a started chain")
		}
		if !strings.Contains(err.Error(), "started job chain") {
			t.Errorf("error = %v, want the started-chain refusal", err)
		}
		got, err := s.GetIssueByID(iss.ID)
		if err != nil {
			t.Fatalf("GetIssueByID: %v", err)
		}
		if got.EngineMode == model.EngineAuto {
			t.Error("engine_mode = auto after a failed arm; Auto must not be armed past the error")
		}
	})
}
