package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// ResolveIssueKey for remote: same logic as local — bare numbers
// resolve against the supplied repo's prefix; canonical keys pass
// through.
func (c *remoteClient) ResolveIssueKey(ctx context.Context, repo *model.Repo, key string) (string, error) {
	key = strings.TrimSpace(key)
	if strings.Contains(key, "-") {
		prefix, num, err := store.ParseIssueKey(key)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s-%d", prefix, num), nil
	}
	if repo == nil {
		return "", fmt.Errorf("bare issue number %q requires a current repo", key)
	}
	var n int64
	if _, err := fmt.Sscanf(key, "%d", &n); err != nil {
		return "", fmt.Errorf("invalid issue reference %q", key)
	}
	return fmt.Sprintf("%s-%d", repo.Prefix, n), nil
}

func (c *remoteClient) ListIssues(ctx context.Context, f IssueFilter) ([]*model.Issue, error) {
	if f.AllRepos {
		return c.listAllReposIssues(ctx, f)
	}
	if f.Repo == nil {
		return nil, errors.New("ListIssues requires a repo unless AllRepos is set")
	}
	q := url.Values{}
	if f.IncludeDescription {
		q.Set("with_description", "true")
	}
	if f.FeatureSlug != "" {
		q.Set("feature", f.FeatureSlug)
	}
	if len(f.States) > 0 {
		parts := make([]string, len(f.States))
		for i, s := range f.States {
			parts[i] = string(s)
		}
		q.Set("state", strings.Join(parts, ","))
	}
	if len(f.Tags) > 0 {
		q.Set("tag", strings.Join(f.Tags, ","))
	}
	var out []*model.Issue
	if err := c.do(ctx, http.MethodGet, "/repos/"+f.Repo.Prefix+"/issues", q, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.Issue{}
	}
	return out, nil
}

func (c *remoteClient) listAllReposIssues(ctx context.Context, f IssueFilter) ([]*model.Issue, error) {
	repos, err := c.ListRepos(ctx)
	if err != nil {
		return nil, err
	}
	var out []*model.Issue
	for _, r := range repos {
		sub := f
		sub.AllRepos = false
		sub.Repo = r
		issues, err := c.ListIssues(ctx, sub)
		if err != nil {
			return nil, err
		}
		out = append(out, issues...)
	}
	if out == nil {
		out = []*model.Issue{}
	}
	return out, nil
}

func (c *remoteClient) GetIssueByKey(ctx context.Context, repo *model.Repo, key string) (*model.Issue, error) {
	view, err := c.ShowIssue(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	return view.Issue, nil
}

func (c *remoteClient) ShowIssue(ctx context.Context, repo *model.Repo, key string) (*IssueView, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	var out IssueView
	if err := c.do(ctx, http.MethodGet, "/repos/"+prefix+"/issues/"+canonical, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) BriefIssue(ctx context.Context, repo *model.Repo, key string, opts BriefOptions) (*IssueBrief, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	q := url.Values{}
	if opts.NoFeatureDocs {
		q.Set("no_feature_docs", "true")
	}
	if opts.NoComments {
		q.Set("no_comments", "true")
	}
	if opts.NoDocContent {
		q.Set("no_doc_content", "true")
	}
	var out IssueBrief
	if err := c.do(ctx, http.MethodGet, "/repos/"+prefix+"/issues/"+canonical+"/brief", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) CreateIssue(ctx context.Context, repo *model.Repo, in inputs.IssueAddInput, dryRun bool) (*model.Issue, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.Issue
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo.Prefix+"/issues", q, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) UpdateIssue(ctx context.Context, repo *model.Repo, key string, edit IssueEdit, dryRun bool) (*model.Issue, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	body := map[string]any{"key": canonical}
	if edit.Title != nil {
		body["title"] = *edit.Title
	}
	if edit.Description != nil {
		body["description"] = *edit.Description
	}
	if edit.FeatureID != nil {
		// FeatureID maps to feature_slug in the body. Outer non-nil with
		// inner nil means "detach". The CLI sets edit.FeatureSlug when
		// non-detach so the remote can still send the slug.
		if edit.FeatureSlug != nil {
			body["feature_slug"] = *edit.FeatureSlug
		} else {
			body["feature_slug"] = nil
		}
	}
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.Issue
	if err := c.do(ctx, http.MethodPatch, "/repos/"+prefix+"/issues/"+canonical, q, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) SetIssueState(ctx context.Context, repo *model.Repo, key string, state model.State, dryRun bool) (*model.Issue, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	body := map[string]any{"key": canonical, "state": string(state)}
	var out model.Issue
	if err := c.do(ctx, http.MethodPut, "/repos/"+prefix+"/issues/"+canonical+"/state", q, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) AssignIssue(ctx context.Context, repo *model.Repo, key, assignee string, dryRun bool) (*model.Issue, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	body := map[string]any{"key": canonical, "assignee": assignee}
	var out model.Issue
	if err := c.do(ctx, http.MethodPut, "/repos/"+prefix+"/issues/"+canonical+"/assignee", q, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) UnassignIssue(ctx context.Context, repo *model.Repo, key string, dryRun bool) (*model.Issue, error) {
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
	if err := c.do(ctx, http.MethodDelete, "/repos/"+prefix+"/issues/"+canonical+"/assignee", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) DeleteIssue(ctx context.Context, repo *model.Repo, key string, dryRun bool) (*model.Issue, *IssueDeletePreview, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	if dryRun {
		q := url.Values{}
		q.Set("dry_run", "true")
		var preview IssueDeletePreview
		if err := c.do(ctx, http.MethodDelete, "/repos/"+prefix+"/issues/"+canonical, q, nil, &preview); err != nil {
			return nil, nil, err
		}
		return nil, &preview, nil
	}
	// For real deletes, fetch the issue first so the caller can render
	// the success message with title.
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, nil, err
	}
	if err := c.do(ctx, http.MethodDelete, "/repos/"+prefix+"/issues/"+canonical, nil, nil, nil); err != nil {
		return nil, nil, err
	}
	return iss, nil, nil
}

func (c *remoteClient) PeekNextIssue(ctx context.Context, repo *model.Repo, slug string) (*model.Issue, error) {
	var out struct {
		Issue *model.Issue `json:"issue"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo.Prefix+"/features/"+slug+"/next", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Issue, nil
}

func (c *remoteClient) ClaimNextIssue(ctx context.Context, repo *model.Repo, slug string, dryRun bool) (*model.Issue, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out struct {
		Issue *model.Issue `json:"issue"`
	}
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo.Prefix+"/features/"+slug+"/next", q, nil, &out); err != nil {
		return nil, err
	}
	return out.Issue, nil
}
