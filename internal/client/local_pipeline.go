package client

import (
	"context"
	"fmt"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/pipeline"
)

// ReorderIssue moves the card to a 1-based position within its
// (repo, state) ordering band and returns the refreshed issue.
func (c *localClient) ReorderIssue(ctx context.Context, repo *model.Repo, key string, position int, dryRun bool) (*model.Issue, error) {
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	if position < 1 {
		return nil, fmt.Errorf("position must be >= 1")
	}
	if dryRun {
		return iss, nil
	}
	if err := c.store.ReorderIssue(iss.ID, position); err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Op: "issue.reorder", Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: fmt.Sprintf("position=%d", position),
	})
	return c.store.GetIssueByID(iss.ID)
}

// SetIssueProcess assigns a process — from either a preset slug or an
// explicit ordered stage list — and returns the materialised job chain.
// The dry-run projection mirrors the rows that would be created without
// writing them.
func (c *localClient) SetIssueProcess(ctx context.Context, repo *model.Repo, key, process string, stages []string, dryRun bool) ([]*model.PipelineJob, error) {
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	proc, err := model.ResolveProcess(process, stages)
	if err != nil {
		return nil, err
	}
	if dryRun {
		out := make([]*model.PipelineJob, len(proc.Stages))
		for i, mode := range proc.Stages {
			out[i] = &model.PipelineJob{IssueID: iss.ID, Sequence: i + 1, Mode: mode, Status: model.JobPending}
		}
		return out, nil
	}
	jobs, err := c.store.SetIssueProcess(iss.ID, proc)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Op: "issue.process", Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: proc.Slug,
	})
	return jobs, nil
}

// ShipIssue is the in_pipeline → to_be_shipped hand-off (the manual Ship
// control / in-process Ship stage). Returns the refreshed issue.
func (c *localClient) ShipIssue(ctx context.Context, repo *model.Repo, key string, dryRun bool) (*model.Issue, error) {
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	if dryRun {
		projected := *iss
		projected.State = model.StateToBeShipped
		return &projected, nil
	}
	if _, err := pipeline.New(c.store).Handoff(iss.ID); err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Op: "issue.ship", Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
	})
	return c.store.GetIssueByID(iss.ID)
}

// StartPipelineJob is the manual Start control: advance the card one
// step (start next job / hand off). Returns the refreshed chain.
func (c *localClient) StartPipelineJob(ctx context.Context, repo *model.Repo, key string) ([]*model.PipelineJob, error) {
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	if _, err := pipeline.New(c.store).StartNext(iss.ID); err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Op: "issue.job.start", Kind: "issue", TargetID: &iss.ID, TargetLabel: iss.Key,
	})
	return c.store.ListPipelineJobs(iss.ID)
}

// StopPipelineJob is the manual Stop control: cancel the running job, halt
// Auto, and signal the running worker to wind down (the engine enqueues a
// BACI-291 steer message at the worker's open claim session). Returns the
// refreshed chain.
func (c *localClient) StopPipelineJob(ctx context.Context, repo *model.Repo, key string) ([]*model.PipelineJob, error) {
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	if _, err := pipeline.New(c.store).StopRunning(iss.ID); err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Op: "issue.job.stop", Kind: "issue", TargetID: &iss.ID, TargetLabel: iss.Key,
	})
	return c.store.ListPipelineJobs(iss.ID)
}

// RerunPipelineJob is the per-job Re-run control on an aborted step
// (BACI-291): resolve the cancelled job at the 1-based seq, reset it to
// pending, and re-dispatch it. Returns the refreshed chain.
func (c *localClient) RerunPipelineJob(ctx context.Context, repo *model.Repo, key string, seq int) ([]*model.PipelineJob, error) {
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	jobs, err := c.store.ListPipelineJobs(iss.ID)
	if err != nil {
		return nil, err
	}
	var target *model.PipelineJob
	for _, j := range jobs {
		if j.Sequence == seq {
			target = j
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("no job at sequence %d", seq)
	}
	if _, err := pipeline.New(c.store).RerunJob(iss.ID, target.ID); err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Op: "issue.job.rerun", Kind: "issue", TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: fmt.Sprintf("seq=%d mode=%s", target.Sequence, target.Mode),
	})
	return c.store.ListPipelineJobs(iss.ID)
}

// SetEngineMode sets the controller engine's drive mode for the card
// ("off" | "auto"). Returns the refreshed issue.
func (c *localClient) SetEngineMode(ctx context.Context, repo *model.Repo, key, mode string) (*model.Issue, error) {
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	em, err := model.ParseEngineMode(mode)
	if err != nil {
		return nil, err
	}
	if err := c.store.SetIssueEngineMode(iss.ID, em); err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Op: "issue.engine_mode", Kind: "issue", TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: string(em),
	})
	return c.store.GetIssueByID(iss.ID)
}

// GetPipelineJobs returns a card's process chain (sequence-ordered).
func (c *localClient) GetPipelineJobs(ctx context.Context, repo *model.Repo, key string) ([]*model.PipelineJob, error) {
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	return c.store.ListPipelineJobs(iss.ID)
}

// SetRepoAutoShip toggles the per-repo Shipping-column auto-ship setting
// and returns the resulting value.
func (c *localClient) SetRepoAutoShip(ctx context.Context, repo *model.Repo, enabled, dryRun bool) (bool, error) {
	if dryRun {
		return enabled, nil
	}
	if err := c.store.SetRepoAutoShip(repo.ID, enabled); err != nil {
		return false, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "repo.auto_ship", Kind: "repo",
		TargetID: &repo.ID, TargetLabel: repo.Prefix,
		Details: fmt.Sprintf("enabled=%v", enabled),
	})
	return enabled, nil
}

// GetRepoAutoShip reads the per-repo Shipping-column auto-ship toggle
// straight off RepoSettings — a read-only path, no audit row.
func (c *localClient) GetRepoAutoShip(ctx context.Context, repo *model.Repo) (bool, error) {
	settings, err := c.store.GetRepoSettings(repo.ID)
	if err != nil {
		return false, err
	}
	return settings.AutoShip, nil
}

// GetRepoBacklogCollapsed reads the per-repo Pipeline Backlog-column
// collapse preference from the tui_settings KV — read-only, no audit row.
func (c *localClient) GetRepoBacklogCollapsed(ctx context.Context, repo *model.Repo) (bool, error) {
	return c.store.IsBacklogCollapsed(repo.ID)
}

// SetRepoBacklogCollapsed persists the per-repo Pipeline Backlog-column
// collapse preference and returns the resulting value. Records a
// `repo_setting.update` audit row only when the value actually changes —
// the idempotent-no-op precedent shared with feature.hide and
// board.hidden_states.
func (c *localClient) SetRepoBacklogCollapsed(ctx context.Context, repo *model.Repo, collapsed, dryRun bool) (bool, error) {
	if dryRun {
		return collapsed, nil
	}
	prev, err := c.store.IsBacklogCollapsed(repo.ID)
	if err != nil {
		return false, err
	}
	if err := c.store.SetBacklogCollapsed(repo.ID, collapsed); err != nil {
		return false, err
	}
	if prev != collapsed {
		c.recordOp(model.HistoryEntry{
			RepoID: &repo.ID, RepoPrefix: repo.Prefix,
			Op: "repo_setting.update", Kind: "repo_setting",
			TargetID: &repo.ID, TargetLabel: "pipeline.backlog_collapsed",
			Details: fmt.Sprintf("collapsed=%v", collapsed),
		})
	}
	return collapsed, nil
}
