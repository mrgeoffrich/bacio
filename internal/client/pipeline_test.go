package client

import (
	"context"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestPipelineClientOps covers the local client's Pipeline verbs — the
// thin layer the CLI commands delegate to.
func TestPipelineClientOps(t *testing.T) {
	c, _ := openTestLocalClient(t)
	ctx := context.Background()
	repo, err := c.store.CreateRepo("CLIP", "cli-pipe", t.TempDir(), "")
	if err != nil {
		t.Fatalf("repo: %v", err)
	}
	iss, err := c.store.CreateIssue(repo.ID, nil, "card", "", model.StateInPipeline, nil, "")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// SetIssueProcess materialises a 3-job chain.
	jobs, err := c.SetIssueProcess(ctx, repo, iss.Key, "plan-implement-ship", false)
	if err != nil {
		t.Fatalf("SetIssueProcess: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("jobs = %d, want 3", len(jobs))
	}

	// Dry-run process projects without writing.
	iss2, _ := c.store.CreateIssue(repo.ID, nil, "card2", "", model.StateInPipeline, nil, "")
	dj, err := c.SetIssueProcess(ctx, repo, iss2.Key, "plan", true)
	if err != nil {
		t.Fatalf("SetIssueProcess dry-run: %v", err)
	}
	if len(dj) != 1 {
		t.Fatalf("dry-run jobs = %d, want 1", len(dj))
	}
	if got, _ := c.store.ListPipelineJobs(iss2.ID); len(got) != 0 {
		t.Fatalf("dry-run wrote %d jobs, want 0", len(got))
	}

	// Ship hand-off → to_be_shipped.
	shipped, err := c.ShipIssue(ctx, repo, iss.Key, false)
	if err != nil {
		t.Fatalf("ShipIssue: %v", err)
	}
	if shipped.State != model.StateToBeShipped {
		t.Fatalf("shipped state = %s, want to_be_shipped", shipped.State)
	}

	// Auto-ship toggle.
	val, err := c.SetRepoAutoShip(ctx, repo, true, false)
	if err != nil || !val {
		t.Fatalf("SetRepoAutoShip: val=%v err=%v", val, err)
	}
	if rs, _ := c.store.GetRepoSettings(repo.ID); !rs.AutoShip {
		t.Fatal("auto-ship not persisted")
	}

	// Reorder a Shipping card to the top.
	iss3, _ := c.store.CreateIssue(repo.ID, nil, "card3", "", model.StateToBeShipped, nil, "")
	updated, err := c.ReorderIssue(ctx, repo, iss3.Key, 1, false)
	if err != nil {
		t.Fatalf("ReorderIssue: %v", err)
	}
	if updated.Priority != 0 {
		t.Fatalf("reordered priority = %d, want 0", updated.Priority)
	}
}
