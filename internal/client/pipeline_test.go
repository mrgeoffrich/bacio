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
	iss, err := c.store.CreateIssue(repo.ID, nil, "card", "", model.StateInPipeline, nil, "", "")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// SetIssueProcess (slug path) materialises a 3-job chain.
	jobs, err := c.SetIssueProcess(ctx, repo, iss.Key, "plan-implement-ship", nil, false)
	if err != nil {
		t.Fatalf("SetIssueProcess: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("jobs = %d, want 3", len(jobs))
	}

	// SetIssueProcess (explicit stage-list path) round-trips an arbitrary
	// chain the named presets don't enumerate.
	issS, _ := c.store.CreateIssue(repo.ID, nil, "card-stages", "", model.StateInPipeline, nil, "", "")
	sj, err := c.SetIssueProcess(ctx, repo, issS.Key, "", []string{"design", "plan_large", "implement", "ship"}, false)
	if err != nil {
		t.Fatalf("SetIssueProcess stages: %v", err)
	}
	wantModes := []string{"design", "plan_large", "implement", "ship"}
	if len(sj) != len(wantModes) {
		t.Fatalf("stage-list jobs = %d, want %d", len(sj), len(wantModes))
	}
	for i, m := range wantModes {
		if sj[i].Mode != m {
			t.Fatalf("stage-list job[%d].Mode = %q, want %q", i, sj[i].Mode, m)
		}
	}

	// Dry-run process projects without writing — including the stage-list
	// projection path.
	iss2, _ := c.store.CreateIssue(repo.ID, nil, "card2", "", model.StateInPipeline, nil, "", "")
	dj, err := c.SetIssueProcess(ctx, repo, iss2.Key, "", []string{"design", "implement"}, true)
	if err != nil {
		t.Fatalf("SetIssueProcess dry-run: %v", err)
	}
	if len(dj) != 2 {
		t.Fatalf("dry-run jobs = %d, want 2", len(dj))
	}
	if got, _ := c.store.ListPipelineJobs(iss2.ID); len(got) != 0 {
		t.Fatalf("dry-run wrote %d jobs, want 0", len(got))
	}

	// EditIssueProcessTail keeps the locked prefix and replaces the
	// pending tail. Seed a card with a started chain (job 1 complete),
	// then edit the tail.
	issE, _ := c.store.CreateIssue(repo.ID, nil, "card-edit", "", model.StateInPipeline, nil, "", "")
	ej, err := c.SetIssueProcess(ctx, repo, issE.Key, "plan-implement-ship", nil, false)
	if err != nil {
		t.Fatalf("SetIssueProcess for edit: %v", err)
	}
	if err := c.store.SetPipelineJobStatus(ej[0].ID, model.JobComplete); err != nil {
		t.Fatalf("complete job 1: %v", err)
	}
	// Dry-run projects the locked prefix + the re-sequenced tail without writing.
	proj, err := c.EditIssueProcessTail(ctx, repo, issE.Key, []string{"review", "implement", "ship"}, true)
	if err != nil {
		t.Fatalf("EditIssueProcessTail dry-run: %v", err)
	}
	wantProj := []string{"plan", "review", "implement", "ship"}
	if len(proj) != len(wantProj) {
		t.Fatalf("dry-run chain = %d, want %d", len(proj), len(wantProj))
	}
	for i, m := range wantProj {
		if proj[i].Mode != m || proj[i].Sequence != i+1 {
			t.Fatalf("dry-run job[%d] = {%s,%d}, want {%s,%d}", i, proj[i].Mode, proj[i].Sequence, m, i+1)
		}
	}
	if got, _ := c.store.ListPipelineJobs(issE.ID); len(got) != 3 {
		t.Fatalf("dry-run mutated the chain to %d jobs, want 3", len(got))
	}
	// Real write.
	edited, err := c.EditIssueProcessTail(ctx, repo, issE.Key, []string{"review", "implement", "ship"}, false)
	if err != nil {
		t.Fatalf("EditIssueProcessTail: %v", err)
	}
	if len(edited) != 4 || edited[0].Mode != "plan" || edited[0].Status != model.JobComplete {
		t.Fatalf("edited chain = %v, locked prefix not preserved", edited)
	}

	// Ship hand-off → to_be_shipped.
	shipped, err := c.ShipIssue(ctx, repo, iss.Key, false)
	if err != nil {
		t.Fatalf("ShipIssue: %v", err)
	}
	if shipped.State != model.StateToBeShipped {
		t.Fatalf("shipped state = %s, want to_be_shipped", shipped.State)
	}

	// Mark-done hand-off → done (BACI-352). Seed a card with a mark_done-
	// terminal chain; dry-run projects done without writing, the real call
	// lands the card in done (not to_be_shipped).
	issD, _ := c.store.CreateIssue(repo.ID, nil, "card-done", "", model.StateInPipeline, nil, "", "")
	if _, err := c.SetIssueProcess(ctx, repo, issD.Key, "", []string{"implement", "mark_done"}, false); err != nil {
		t.Fatalf("SetIssueProcess mark_done: %v", err)
	}
	projDone, err := c.MarkDoneIssue(ctx, repo, issD.Key, true)
	if err != nil {
		t.Fatalf("MarkDoneIssue dry-run: %v", err)
	}
	if projDone.State != model.StateDone {
		t.Fatalf("dry-run mark-done state = %s, want done", projDone.State)
	}
	if got, _ := c.store.GetIssueByID(issD.ID); got.State != model.StateInPipeline {
		t.Fatalf("dry-run mark-done wrote state = %s, want in_pipeline (no write)", got.State)
	}
	done, err := c.MarkDoneIssue(ctx, repo, issD.Key, false)
	if err != nil {
		t.Fatalf("MarkDoneIssue: %v", err)
	}
	if done.State != model.StateDone {
		t.Fatalf("mark-done state = %s, want done", done.State)
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
	iss3, _ := c.store.CreateIssue(repo.ID, nil, "card3", "", model.StateToBeShipped, nil, "", "")
	updated, err := c.ReorderIssue(ctx, repo, iss3.Key, 1, false)
	if err != nil {
		t.Fatalf("ReorderIssue: %v", err)
	}
	if updated.Priority != 0 {
		t.Fatalf("reordered priority = %d, want 0", updated.Priority)
	}
}
