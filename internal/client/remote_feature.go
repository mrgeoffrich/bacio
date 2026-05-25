package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
)

func (c *remoteClient) ListFeatures(ctx context.Context, repo *model.Repo, withDescription, includeArchived bool) ([]*model.Feature, error) {
	q := url.Values{}
	if withDescription {
		q.Set("with_description", "true")
	}
	if includeArchived {
		q.Set("include_archived", "true")
	}
	var out []*model.Feature
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo.Prefix+"/features", q, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.Feature{}
	}
	return out, nil
}

func (c *remoteClient) GetFeatureBySlug(ctx context.Context, repo *model.Repo, slug string) (*model.Feature, error) {
	var out model.Feature
	path := "/repos/" + repo.Prefix + "/features/" + slug
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetFeatureByID has no public REST endpoint (URLs are slug-keyed). The
// caller must already know the slug. The local backend supports this
// because the feature row carries its int64 id; for remote we don't
// generally need this — only the CLI's "edit issue and re-resolve
// feature slug for the projection" path does, and it has the slug.
func (c *remoteClient) GetFeatureByID(ctx context.Context, repo *model.Repo, id int64) (*model.Feature, error) {
	return nil, fmt.Errorf("GetFeatureByID is not supported in remote mode (id=%d); call GetFeatureBySlug instead", id)
}

func (c *remoteClient) CreateFeature(ctx context.Context, repo *model.Repo, in inputs.FeatureAddInput, dryRun bool) (*model.Feature, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.Feature
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo.Prefix+"/features", q, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) UpdateFeature(ctx context.Context, repo *model.Repo, slug string, title, description, emoji *string, dryRun bool) (*model.Feature, error) {
	body := map[string]any{"slug": slug}
	if title != nil {
		body["title"] = *title
	}
	if description != nil {
		body["description"] = *description
	}
	// Emoji uses presence in the JSON map to round-trip the same
	// "absent vs explicit clear" semantics the local path has — a nil
	// pointer omits the key; a non-nil empty string sends "" to clear.
	if emoji != nil {
		body["emoji"] = *emoji
	}
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.Feature
	if err := c.do(ctx, http.MethodPatch, "/repos/"+repo.Prefix+"/features/"+slug, q, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) DeleteFeature(ctx context.Context, repo *model.Repo, slug string, dryRun bool) (*model.Feature, *FeatureDeletePreview, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
		var preview FeatureDeletePreview
		if err := c.do(ctx, http.MethodDelete, "/repos/"+repo.Prefix+"/features/"+slug, q, nil, &preview); err != nil {
			return nil, nil, err
		}
		return nil, &preview, nil
	}
	// Real delete needs the feature's title/slug for the caller's
	// success message. Fetch first.
	feat, err := c.GetFeatureBySlug(ctx, repo, slug)
	if err != nil {
		return nil, nil, err
	}
	if err := c.do(ctx, http.MethodDelete, "/repos/"+repo.Prefix+"/features/"+slug, nil, nil, nil); err != nil {
		return nil, nil, err
	}
	return feat, nil, nil
}

func (c *remoteClient) ShowFeature(ctx context.Context, repo *model.Repo, slug string) (*FeatureView, error) {
	var out FeatureView
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo.Prefix+"/features/"+slug, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) PlanFeature(ctx context.Context, repo *model.Repo, slug string) (*PlanView, error) {
	var out PlanView
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo.Prefix+"/features/"+slug+"/plan", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// strInt is a tiny helper so callers can inline url.Values without
// importing strconv at every call site.
func strInt(n int64) string { return strconv.FormatInt(n, 10) }

// ArchiveFeature (BACI-68) — POST /repos/{prefix}/features/{slug}/archive.
func (c *remoteClient) ArchiveFeature(ctx context.Context, repo *model.Repo, slug string, dryRun bool) (*model.Feature, error) {
	return c.archiveFeature(ctx, repo, slug, true, dryRun)
}

// UnarchiveFeature (BACI-68) — POST /repos/{prefix}/features/{slug}/unarchive.
func (c *remoteClient) UnarchiveFeature(ctx context.Context, repo *model.Repo, slug string, dryRun bool) (*model.Feature, error) {
	return c.archiveFeature(ctx, repo, slug, false, dryRun)
}

func (c *remoteClient) archiveFeature(ctx context.Context, repo *model.Repo, slug string, archive, dryRun bool) (*model.Feature, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	verb := "archive"
	if !archive {
		verb = "unarchive"
	}
	var out model.Feature
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo.Prefix+"/features/"+slug+"/"+verb, q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListFeatureComments (BACI-124) — GET /repos/{prefix}/features/{slug}/comments.
func (c *remoteClient) ListFeatureComments(ctx context.Context, repo *model.Repo, slug string) ([]*model.FeatureComment, error) {
	var out []*model.FeatureComment
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo.Prefix+"/features/"+slug+"/comments", nil, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.FeatureComment{}
	}
	return out, nil
}

// AddFeatureComment (BACI-124) — POST /repos/{prefix}/features/{slug}/comments.
func (c *remoteClient) AddFeatureComment(ctx context.Context, repo *model.Repo, in inputs.FeatureCommentAddInput, dryRun bool) (*model.FeatureComment, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.FeatureComment
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo.Prefix+"/features/"+in.FeatureSlug+"/comments", q, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteFeatureComment (BACI-124) — DELETE /repos/{prefix}/features/{slug}/comments/{uuid}.
func (c *remoteClient) DeleteFeatureComment(ctx context.Context, repo *model.Repo, in inputs.FeatureCommentRmInput, dryRun bool) (*FeatureCommentDeletePreview, int64, error) {
	if in.CommentUUID == "" {
		return nil, 0, fmt.Errorf("comment_uuid is required")
	}
	path := "/repos/" + repo.Prefix + "/features/" + in.FeatureSlug + "/comments/" + in.CommentUUID
	if dryRun {
		q := url.Values{}
		q.Set("dry_run", "true")
		var preview FeatureCommentDeletePreview
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
