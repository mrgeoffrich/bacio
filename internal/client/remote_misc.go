package client

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// ----- Comments -----

func (c *remoteClient) ListComments(ctx context.Context, repo *model.Repo, key string) ([]*model.Comment, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	var out []*model.Comment
	if err := c.do(ctx, http.MethodGet, "/repos/"+prefix+"/issues/"+canonical+"/comments", nil, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.Comment{}
	}
	return out, nil
}

func (c *remoteClient) AddComment(ctx context.Context, repo *model.Repo, in inputs.CommentAddInput, dryRun bool) (*model.Comment, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, in.IssueKey)
	if err != nil {
		return nil, err
	}
	in.IssueKey = canonical
	prefix := strings.SplitN(canonical, "-", 2)[0]
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.Comment
	if err := c.do(ctx, http.MethodPost, "/repos/"+prefix+"/issues/"+canonical+"/comments", q, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ----- Relations -----

func (c *remoteClient) LinkRelation(ctx context.Context, repo *model.Repo, in inputs.LinkInput, dryRun bool) (*model.Relation, error) {
	from, err := c.ResolveIssueKey(ctx, repo, in.From)
	if err != nil {
		return nil, err
	}
	to, err := c.ResolveIssueKey(ctx, repo, in.To)
	if err != nil {
		return nil, err
	}
	in.From = from
	in.To = to
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.Relation
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo.Prefix+"/relations", q, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) UnlinkRelation(ctx context.Context, repo *model.Repo, in inputs.UnlinkInput, dryRun bool) (*RelationDeletePreview, int64, error) {
	a, err := c.ResolveIssueKey(ctx, repo, in.A)
	if err != nil {
		return nil, 0, err
	}
	b, err := c.ResolveIssueKey(ctx, repo, in.B)
	if err != nil {
		return nil, 0, err
	}
	in.A = a
	in.B = b
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
		var preview RelationDeletePreview
		if err := c.do(ctx, http.MethodDelete, "/repos/"+repo.Prefix+"/relations", q, in, &preview); err != nil {
			return nil, 0, err
		}
		return &preview, 0, nil
	}
	var resp struct {
		Removed int64 `json:"removed"`
	}
	if err := c.do(ctx, http.MethodDelete, "/repos/"+repo.Prefix+"/relations", nil, in, &resp); err != nil {
		return nil, 0, err
	}
	return nil, resp.Removed, nil
}

// ----- PRs -----

func (c *remoteClient) ListPRs(ctx context.Context, repo *model.Repo, key string) ([]*model.PullRequest, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	var out []*model.PullRequest
	if err := c.do(ctx, http.MethodGet, "/repos/"+prefix+"/issues/"+canonical+"/pull-requests", nil, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.PullRequest{}
	}
	return out, nil
}

func (c *remoteClient) AttachPR(ctx context.Context, repo *model.Repo, key, prURL string, dryRun bool) (*model.PullRequest, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	body := inputs.PRAttachInput{IssueKey: canonical, URL: prURL}
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.PullRequest
	if err := c.do(ctx, http.MethodPost, "/repos/"+prefix+"/issues/"+canonical+"/pull-requests", q, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) DetachPR(ctx context.Context, repo *model.Repo, key, prURL string, dryRun bool) (*PRDetachPreview, int64, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, 0, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	body := inputs.PRDetachInput{IssueKey: canonical, URL: prURL}
	if dryRun {
		q := url.Values{}
		q.Set("dry_run", "true")
		var preview PRDetachPreview
		if err := c.do(ctx, http.MethodDelete, "/repos/"+prefix+"/issues/"+canonical+"/pull-requests", q, body, &preview); err != nil {
			return nil, 0, err
		}
		return &preview, 0, nil
	}
	if err := c.do(ctx, http.MethodDelete, "/repos/"+prefix+"/issues/"+canonical+"/pull-requests", nil, body, nil); err != nil {
		return nil, 0, err
	}
	return nil, 1, nil
}

// ----- Tags -----

func (c *remoteClient) AddTags(ctx context.Context, repo *model.Repo, key string, tags []string, dryRun bool) (*model.Issue, error) {
	return c.tagMutate(ctx, repo, key, tags, true, dryRun)
}

func (c *remoteClient) RemoveTags(ctx context.Context, repo *model.Repo, key string, tags []string, dryRun bool) (*model.Issue, error) {
	return c.tagMutate(ctx, repo, key, tags, false, dryRun)
}

func (c *remoteClient) tagMutate(ctx context.Context, repo *model.Repo, key string, tags []string, add, dryRun bool) (*model.Issue, error) {
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
	if add {
		body := inputs.TagAddInput{IssueKey: canonical, Tags: tags}
		if err := c.do(ctx, http.MethodPost, "/repos/"+prefix+"/issues/"+canonical+"/tags", q, body, &out); err != nil {
			return nil, err
		}
	} else {
		body := inputs.TagRmInput{IssueKey: canonical, Tags: tags}
		if err := c.do(ctx, http.MethodDelete, "/repos/"+prefix+"/issues/"+canonical+"/tags", q, body, &out); err != nil {
			return nil, err
		}
	}
	return &out, nil
}
