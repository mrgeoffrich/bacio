package client

import (
	"context"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestCreateIssueAutoRun covers the BACI-374 toggle on the local-client
// create path (CLI + TUI + Wails). Twin of TestIssueCreateAutoRun in
// internal/api — the two create paths must agree on what the same payload
// does, so keep the cases in step.
func TestCreateIssueAutoRun(t *testing.T) {
	ctx := context.Background()

	t.Run("arms_the_full_chain", func(t *testing.T) {
		c, _ := openTestLocalClient(t)
		repo := seedRepo(t, c.store, "AUTO", t.TempDir())
		iss, err := c.CreateIssue(ctx, repo, inputs.IssueAddInput{Title: "drive it", AutoRun: true}, false)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		// The returned issue must already read in_pipeline — the composers
		// render the card off this value.
		if iss.State != model.StateInPipeline {
			t.Errorf("returned state = %q, want in_pipeline", iss.State)
		}
		if iss.EngineMode != model.EngineAuto {
			t.Errorf("engine_mode = %q, want auto", iss.EngineMode)
		}
		jobs, err := c.store.ListPipelineJobs(iss.ID)
		if err != nil {
			t.Fatalf("list jobs: %v", err)
		}
		want := []string{
			model.BuiltinTemplateScope, model.BuiltinTemplatePlan,
			model.BuiltinTemplateImplement, model.ShipJobMode,
		}
		if len(jobs) != len(want) {
			t.Fatalf("chain length = %d, want %d", len(jobs), len(want))
		}
		for i, j := range jobs {
			if j.Mode != want[i] {
				t.Errorf("job %d mode = %q, want %q", i, j.Mode, want[i])
			}
			if j.Status != model.JobPending {
				t.Errorf("job %d status = %s, want pending", i, j.Status)
			}
		}
	})

	t.Run("defaults_off", func(t *testing.T) {
		c, _ := openTestLocalClient(t)
		repo := seedRepo(t, c.store, "AUTO", t.TempDir())
		iss, err := c.CreateIssue(ctx, repo, inputs.IssueAddInput{Title: "inert"}, false)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if iss.State != model.StateTodo {
			t.Errorf("state = %q, want todo", iss.State)
		}
		jobs, err := c.store.ListPipelineJobs(iss.ID)
		if err != nil {
			t.Fatalf("list jobs: %v", err)
		}
		if len(jobs) != 0 {
			t.Errorf("chain length = %d, want 0", len(jobs))
		}
	})

	t.Run("explicit_state_wins", func(t *testing.T) {
		c, _ := openTestLocalClient(t)
		repo := seedRepo(t, c.store, "AUTO", t.TempDir())
		iss, err := c.CreateIssue(ctx, repo, inputs.IssueAddInput{
			Title: "already done", AutoRun: true, State: "done",
		}, false)
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if iss.State != model.StateDone {
			t.Errorf("state = %q, want done", iss.State)
		}
		jobs, err := c.store.ListPipelineJobs(iss.ID)
		if err != nil {
			t.Fatalf("list jobs: %v", err)
		}
		if len(jobs) != 0 {
			t.Errorf("chain length = %d, want 0", len(jobs))
		}
	})

	t.Run("dry_run_projects_without_writing", func(t *testing.T) {
		c, _ := openTestLocalClient(t)
		repo := seedRepo(t, c.store, "AUTO", t.TempDir())
		projected, err := c.CreateIssue(ctx, repo, inputs.IssueAddInput{Title: "rehearse", AutoRun: true}, true)
		if err != nil {
			t.Fatalf("CreateIssue dry-run: %v", err)
		}
		if projected.State != model.StateInPipeline {
			t.Errorf("projected state = %q, want in_pipeline", projected.State)
		}
		issues, err := c.ListIssues(ctx, IssueFilter{Repo: repo})
		if err != nil {
			t.Fatalf("list issues: %v", err)
		}
		if len(issues) != 0 {
			t.Errorf("dry-run wrote %d issues, want 0", len(issues))
		}
	})
}
