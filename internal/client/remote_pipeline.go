package client

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/model"
)

func (c *remoteClient) ReorderIssue(ctx context.Context, repo *model.Repo, key string, position int, dryRun bool) (*model.Issue, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	body := map[string]any{"key": canonical, "position": position}
	var out model.Issue
	if err := c.do(ctx, http.MethodPut, "/repos/"+prefix+"/issues/"+canonical+"/reorder", q, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) SetIssueProcess(ctx context.Context, repo *model.Repo, key, process string, stages []string, dryRun bool) ([]*model.PipelineJob, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	// Send exactly one of process / stages so the server's mutual-exclusion
	// guard sees the same shape the local client resolves.
	body := map[string]any{"key": canonical}
	if len(stages) > 0 {
		body["stages"] = stages
	} else {
		body["process"] = process
	}
	var out []*model.PipelineJob
	if err := c.do(ctx, http.MethodPost, "/repos/"+prefix+"/issues/"+canonical+"/process", q, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *remoteClient) ShipIssue(ctx context.Context, repo *model.Repo, key string, dryRun bool) (*model.Issue, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.Issue
	if err := c.do(ctx, http.MethodPost, "/repos/"+prefix+"/issues/"+canonical+"/ship", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) StartPipelineJob(ctx context.Context, repo *model.Repo, key string) ([]*model.PipelineJob, error) {
	return c.jobControl(ctx, repo, key, "start")
}

func (c *remoteClient) StopPipelineJob(ctx context.Context, repo *model.Repo, key string) ([]*model.PipelineJob, error) {
	return c.jobControl(ctx, repo, key, "stop")
}

func (c *remoteClient) jobControl(ctx context.Context, repo *model.Repo, key, action string) ([]*model.PipelineJob, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	var out []*model.PipelineJob
	if err := c.do(ctx, http.MethodPost, "/repos/"+prefix+"/issues/"+canonical+"/jobs/"+action, nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *remoteClient) SetEngineMode(ctx context.Context, repo *model.Repo, key, mode string) (*model.Issue, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	body := map[string]any{"key": canonical, "mode": mode}
	var out model.Issue
	if err := c.do(ctx, http.MethodPut, "/repos/"+prefix+"/issues/"+canonical+"/engine-mode", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) GetPipelineJobs(ctx context.Context, repo *model.Repo, key string) ([]*model.PipelineJob, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	var out []*model.PipelineJob
	if err := c.do(ctx, http.MethodGet, "/repos/"+prefix+"/issues/"+canonical+"/jobs", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *remoteClient) SetRepoAutoShip(ctx context.Context, repo *model.Repo, enabled, dryRun bool) (bool, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	body := map[string]any{"enabled": enabled}
	var out struct {
		AutoShip bool `json:"auto_ship"`
	}
	if err := c.do(ctx, http.MethodPut, "/repos/"+repo.Prefix+"/auto-ship", q, body, &out); err != nil {
		return false, err
	}
	return out.AutoShip, nil
}

func (c *remoteClient) GetRepoAutoShip(ctx context.Context, repo *model.Repo) (bool, error) {
	var out struct {
		AutoShip bool `json:"auto_ship"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo.Prefix+"/auto-ship", nil, nil, &out); err != nil {
		return false, err
	}
	return out.AutoShip, nil
}

func (c *remoteClient) GetRepoBacklogCollapsed(ctx context.Context, repo *model.Repo) (bool, error) {
	var out struct {
		Collapsed bool `json:"backlog_collapsed"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo.Prefix+"/backlog-collapsed", nil, nil, &out); err != nil {
		return false, err
	}
	return out.Collapsed, nil
}

func (c *remoteClient) SetRepoBacklogCollapsed(ctx context.Context, repo *model.Repo, collapsed, dryRun bool) (bool, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	body := map[string]any{"collapsed": collapsed}
	var out struct {
		Collapsed bool `json:"backlog_collapsed"`
	}
	if err := c.do(ctx, http.MethodPut, "/repos/"+repo.Prefix+"/backlog-collapsed", q, body, &out); err != nil {
		return false, err
	}
	return out.Collapsed, nil
}
