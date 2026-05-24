package client

import (
	"context"
	"fmt"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// ListFeatureComments returns every feature_comments row for one
// feature, oldest first (BACI-124).
func (c *localClient) ListFeatureComments(ctx context.Context, repo *model.Repo, slug string) ([]*model.FeatureComment, error) {
	f, err := c.store.GetFeatureBySlug(repo.ID, slug)
	if err != nil {
		return nil, err
	}
	cs, err := c.store.ListFeatureComments(f.ID)
	if err != nil {
		return nil, err
	}
	if cs == nil {
		cs = []*model.FeatureComment{}
	}
	return cs, nil
}

// AddFeatureComment inserts one row into feature_comments and emits a
// feature_comment.add audit row (BACI-124).
func (c *localClient) AddFeatureComment(ctx context.Context, repo *model.Repo, in inputs.FeatureCommentAddInput, dryRun bool) (*model.FeatureComment, error) {
	if in.FeatureSlug == "" || in.Author == "" || in.Body == "" {
		return nil, fmt.Errorf("feature_slug, author, and body are required")
	}
	f, err := c.store.GetFeatureBySlug(repo.ID, in.FeatureSlug)
	if err != nil {
		return nil, err
	}
	if dryRun {
		return &model.FeatureComment{
			FeatureID: f.ID,
			Author:    in.Author,
			Body:      in.Body,
		}, nil
	}
	cm, err := c.store.CreateFeatureComment(f.ID, in.Author, in.Body)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "feature_comment.add", Kind: "feature",
		TargetID: &f.ID, TargetLabel: f.Slug,
		Details: "by " + in.Author,
	})
	return cm, nil
}

// DeleteFeatureComment removes a feature_comments row by uuid and emits
// a feature_comment.delete audit row (BACI-124).
func (c *localClient) DeleteFeatureComment(ctx context.Context, repo *model.Repo, in inputs.FeatureCommentRmInput, dryRun bool) (*FeatureCommentDeletePreview, int64, error) {
	if in.FeatureSlug == "" || in.CommentUUID == "" {
		return nil, 0, fmt.Errorf("feature_slug and comment_uuid are required")
	}
	f, err := c.store.GetFeatureBySlug(repo.ID, in.FeatureSlug)
	if err != nil {
		return nil, 0, err
	}
	cm, err := c.store.GetFeatureCommentByUUID(in.CommentUUID)
	if err != nil {
		return nil, 0, err
	}
	if cm.FeatureID != f.ID {
		return nil, 0, fmt.Errorf("comment %q does not belong to feature %s", in.CommentUUID, f.Slug)
	}
	if dryRun {
		return &FeatureCommentDeletePreview{
			FeatureSlug: f.Slug,
			CommentUUID: cm.UUID,
			WouldRemove: 1,
		}, 0, nil
	}
	if err := c.store.DeleteFeatureCommentByUUID(cm.UUID); err != nil {
		return nil, 0, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "feature_comment.delete", Kind: "feature",
		TargetID: &f.ID, TargetLabel: f.Slug,
		Details: "by " + cm.Author,
	})
	return nil, 1, nil
}
