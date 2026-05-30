package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

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
	if f.IncludeArchived {
		q.Set("include_archived", "true")
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
	// BACI-232: base_branch follows the same shape as feature_slug — non-nil
	// empty string clears (sent as ""), non-nil non-empty sets, nil omits.
	if edit.BaseBranch != nil {
		body["base_branch"] = *edit.BaseBranch
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

// ArchiveIssue (BACI-68) — POST /repos/{prefix}/issues/{key}/archive.
func (c *remoteClient) ArchiveIssue(ctx context.Context, repo *model.Repo, key string, dryRun bool) (*model.Issue, error) {
	return c.archiveIssue(ctx, repo, key, true, dryRun)
}

// UnarchiveIssue (BACI-68) — POST /repos/{prefix}/issues/{key}/unarchive.
func (c *remoteClient) UnarchiveIssue(ctx context.Context, repo *model.Repo, key string, dryRun bool) (*model.Issue, error) {
	return c.archiveIssue(ctx, repo, key, false, dryRun)
}

func (c *remoteClient) archiveIssue(ctx context.Context, repo *model.Repo, key string, archive, dryRun bool) (*model.Issue, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	verb := "archive"
	if !archive {
		verb = "unarchive"
	}
	var out model.Issue
	if err := c.do(ctx, http.MethodPost, "/repos/"+prefix+"/issues/"+canonical+"/"+verb, q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListShippedIssues (BACI-187) — GET /repos/{prefix}/shipped. Hits the
// REST endpoint with the popover's typical (since, limit) shape and
// decodes the lean DTO list into sparse *model.Issue rows. PR URLs,
// feature emoji, etc. that ride the DTO but not the Issue struct are
// dropped on this path — the remote consumer today is the CLI client,
// which doesn't render a PR chip. The desktop / web consumers always
// run against the local backend (or talk directly to the HTTP endpoint
// from the browser via api.http.ts) so they keep the full DTO shape.
func (c *remoteClient) ListShippedIssues(ctx context.Context, repo *model.Repo, f store.ShippedFilter) ([]*model.Issue, error) {
	if repo == nil {
		return nil, fmt.Errorf("ListShippedIssues requires a repo")
	}
	q := url.Values{}
	if f.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", f.Limit))
	}
	if f.Since != nil {
		// BACI-312: send the cutoff as an absolute ?since_ts= RFC3339
		// timestamp rather than the pre-BACI-312 lossy `%dh` (whole-hour)
		// ?since= rounding. f.Since is already an absolute lower bound, so
		// passing it verbatim is both simpler and exact — it preserves a
		// midnight cutoff to the second instead of drifting it by up to an
		// hour.
		q.Set("since_ts", f.Since.UTC().Format(time.RFC3339))
	}
	type shippedDTO struct {
		Key          string    `json:"key"`
		Title        string    `json:"title"`
		TerminalAt   time.Time `json:"terminalAt"`
		Tags         []string  `json:"tags"`
		FeatureSlug  string    `json:"featureSlug"`
		FeatureEmoji string    `json:"featureEmoji"`
	}
	// BACI-221 reshaped the response from a bare list to {rows, total}.
	// The remote client only needs the rows; total is the Pipeline
	// Shipping-column pill's concern and the dedicated
	// CountShippedIssues call covers it.
	var raw struct {
		Rows  []shippedDTO `json:"rows"`
		Total int          `json:"total"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo.Prefix+"/shipped", q, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]*model.Issue, 0, len(raw.Rows))
	for _, d := range raw.Rows {
		prefix, num, err := store.ParseIssueKey(d.Key)
		if err != nil {
			return nil, fmt.Errorf("ListShippedIssues: bad key %q from remote: %w", d.Key, err)
		}
		t := d.TerminalAt
		iss := &model.Issue{
			Key:          d.Key,
			Number:       num,
			Title:        d.Title,
			State:        model.StateDone,
			Tags:         d.Tags,
			FeatureSlug:  d.FeatureSlug,
			FeatureEmoji: d.FeatureEmoji,
			TerminalAt:   &t,
		}
		_ = prefix // prefix is implicit in repo.Prefix; Issue.RepoID stays zero on the remote path.
		out = append(out, iss)
	}
	return out, nil
}

// CountShippedIssues (BACI-221) — GET /repos/{prefix}/shipped/count.
// Mirrors ListShippedIssues' absolute ?since_ts= shape (BACI-312); the
// count endpoint deliberately has no ?limit= parameter — the count is
// total under the scope, independent of any per-fetch row cap.
func (c *remoteClient) CountShippedIssues(ctx context.Context, repo *model.Repo, f store.ShippedFilter) (int, error) {
	if repo == nil {
		return 0, fmt.Errorf("CountShippedIssues requires a repo")
	}
	q := url.Values{}
	if f.Since != nil {
		q.Set("since_ts", f.Since.UTC().Format(time.RFC3339))
	}
	var out struct {
		Total int `json:"total"`
	}
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo.Prefix+"/shipped/count", q, nil, &out); err != nil {
		return 0, err
	}
	return out.Total, nil
}
