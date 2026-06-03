package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
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

func (c *remoteClient) DeleteComment(ctx context.Context, repo *model.Repo, in inputs.CommentRmInput, dryRun bool) (*CommentDeletePreview, int64, error) {
	if in.CommentUUID == "" {
		return nil, 0, fmt.Errorf("comment_uuid is required")
	}
	canonical, err := c.ResolveIssueKey(ctx, repo, in.IssueKey)
	if err != nil {
		return nil, 0, err
	}
	prefix := strings.SplitN(canonical, "-", 2)[0]
	path := "/repos/" + prefix + "/issues/" + canonical + "/comments/" + in.CommentUUID
	if dryRun {
		q := url.Values{}
		q.Set("dry_run", "true")
		var preview CommentDeletePreview
		if err := c.do(ctx, http.MethodDelete, path, q, nil, &preview); err != nil {
			return nil, 0, err
		}
		return &preview, 0, nil
	}
	if err := c.do(ctx, http.MethodDelete, path, nil, nil, nil); err != nil {
		return nil, 0, err
	}
	return nil, 1, nil
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

// ----- Proxy capture (BACI-303) -----

func (c *remoteClient) ProxyStats(ctx context.Context, f store.ProxyStatsFilter) ([]*model.ProxyFQDNStat, error) {
	q := url.Values{}
	if f.Limit > 0 {
		q.Set("limit", strInt(int64(f.Limit)))
	}
	if f.Since != nil {
		q.Set("from", f.Since.UTC().Format("2006-01-02T15:04:05Z"))
	}
	var out []*model.ProxyFQDNStat
	if err := c.do(ctx, http.MethodGet, "/proxy/stats", q, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.ProxyFQDNStat{}
	}
	return out, nil
}

// ----- Per-job message detail (BACI-306) -----

func (c *remoteClient) AnthropicCapture(ctx context.Context, id int64) (*model.ProxyMessage, error) {
	var out model.ProxyMessage
	if err := c.do(ctx, http.MethodGet, "/proxy/captures/"+strInt(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) JobTranscript(ctx context.Context, dispatchID int64) (*model.AnthropicTranscript, error) {
	var out model.AnthropicTranscript
	if err := c.do(ctx, http.MethodGet, "/proxy/jobs/"+strInt(dispatchID)+"/transcript", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ----- Capture drill-down (BACI-308) -----

func (c *remoteClient) ListProxyCaptures(ctx context.Context, f store.ProxyRequestFilter) ([]*model.ProxyCaptureRow, error) {
	q := url.Values{}
	if f.Host != "" {
		q.Set("host", f.Host)
	}
	if f.DispatchID != nil {
		q.Set("dispatch_id", strInt(*f.DispatchID))
	}
	if f.IsAnthropic != nil {
		q.Set("is_anthropic", strconv.FormatBool(*f.IsAnthropic))
	}
	if f.Limit > 0 {
		q.Set("limit", strInt(int64(f.Limit)))
	}
	if f.Since != nil {
		q.Set("from", f.Since.UTC().Format("2006-01-02T15:04:05Z"))
	}
	var out []*model.ProxyCaptureRow
	if err := c.do(ctx, http.MethodGet, "/proxy/captures", q, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.ProxyCaptureRow{}
	}
	return out, nil
}

func (c *remoteClient) ProxyCaptureRaw(ctx context.Context, id int64) ([]byte, error) {
	var out []byte
	if err := c.do(ctx, http.MethodGet, "/proxy/captures/"+strInt(id)+"/raw", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ----- Content search (BACI-320) -----

func (c *remoteClient) SearchProxyMessages(ctx context.Context, f store.ProxyMessageFilter) ([]*model.ProxyMessageMatch, error) {
	q := url.Values{}
	q.Set("q", f.Query)
	if f.Role != "" {
		q.Set("role", f.Role)
	}
	if f.Block != "" {
		q.Set("block", f.Block)
	}
	if f.DispatchID != nil {
		q.Set("dispatch_id", strInt(*f.DispatchID))
	}
	if f.SessionID != "" {
		q.Set("session", f.SessionID)
	}
	if f.ClaudeAgentID != "" {
		q.Set("agent", f.ClaudeAgentID)
	}
	if f.Limit > 0 {
		q.Set("limit", strInt(int64(f.Limit)))
	}
	if f.Since != nil {
		q.Set("from", f.Since.UTC().Format("2006-01-02T15:04:05Z"))
	}
	var out []*model.ProxyMessageMatch
	if err := c.do(ctx, http.MethodGet, "/proxy/search", q, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.ProxyMessageMatch{}
	}
	return out, nil
}

// ----- Backfill (BACI-321) -----

func (c *remoteClient) ReparseProxyMessages(ctx context.Context, in ReparseProxyOpts, dryRun bool) (store.ReparseResult, error) {
	// BACI-325: refuse only a global rebuild (no --dispatch) before the round-trip
	// — the server returns the same error, but failing fast keeps the contract
	// identical to the local backend. A per-dispatch rebuild rides through.
	if in.Rebuild && in.Dispatch == nil {
		return store.ReparseResult{}, ErrRebuildNotImplemented
	}
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	if in.Dispatch != nil {
		q.Set("dispatch", strInt(*in.Dispatch))
	}
	if in.Rebuild {
		q.Set("rebuild", "true")
	}
	if in.RetryFailed {
		q.Set("retry_failed", "true")
	}
	var out store.ReparseResult
	if err := c.do(ctx, http.MethodPost, "/proxy/reparse", q, nil, &out); err != nil {
		return store.ReparseResult{}, err
	}
	return out, nil
}

// ----- Transcript browser (BACI-322) -----

func (c *remoteClient) ListJobTranscripts(ctx context.Context, f store.JobTranscriptFilter) ([]*model.JobTranscriptRow, error) {
	q := url.Values{}
	if f.RepoPrefix != "" {
		q.Set("repo", f.RepoPrefix)
	}
	if f.IssueKey != "" {
		q.Set("issue", f.IssueKey)
	}
	if f.Mode != "" {
		q.Set("mode", f.Mode)
	}
	if f.SessionID != "" {
		q.Set("session", f.SessionID)
	}
	if f.ClaudeAgentID != "" {
		q.Set("agent", f.ClaudeAgentID)
	}
	if f.Limit > 0 {
		q.Set("limit", strInt(int64(f.Limit)))
	}
	if f.Since != nil {
		q.Set("from", f.Since.UTC().Format("2006-01-02T15:04:05Z"))
	}
	var out []*model.JobTranscriptRow
	if err := c.do(ctx, http.MethodGet, "/proxy/jobs", q, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.JobTranscriptRow{}
	}
	return out, nil
}

// BlockersFor stays local-only (BACI-114). The kanban surface always
// runs against the local DB / Wails / `bacio web`, never via `bacio
// --remote`, so the bulk read has no HTTP analogue today.
func (c *remoteClient) BlockersFor(ctx context.Context, ids []int64) (map[int64][]store.IssueBlocker, error) {
	return nil, ErrLocalOnly
}

// CountEvalCommentsByIssue / CountTranscriptDocsByIssue / LatestPlanByIssue /
// LatestPRByIssue stay local-only for the same reason as BlockersFor
// (BACI-141, BACI-216, BACI-239): the board assembler is only invoked
// server-side.
func (c *remoteClient) CountEvalCommentsByIssue(ctx context.Context, ids []int64) (map[int64]int, error) {
	return nil, ErrLocalOnly
}
func (c *remoteClient) CountTranscriptDocsByIssue(ctx context.Context, ids []int64) (map[int64]int, error) {
	return nil, ErrLocalOnly
}
func (c *remoteClient) LatestPlanByIssue(ctx context.Context, ids []int64) (map[int64]*model.LatestPlan, error) {
	return nil, ErrLocalOnly
}
func (c *remoteClient) LatestPRByIssue(ctx context.Context, ids []int64) (map[int64]*model.LatestPR, error) {
	return nil, ErrLocalOnly
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
