package client

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func (c *localClient) ListFeatures(ctx context.Context, repo *model.Repo, withDescription bool) ([]*model.Feature, error) {
	feats, err := c.store.ListFeatures(repo.ID, withDescription)
	if err != nil {
		return nil, err
	}
	if feats == nil {
		feats = []*model.Feature{}
	}
	return feats, nil
}

func (c *localClient) GetFeatureBySlug(ctx context.Context, repo *model.Repo, slug string) (*model.Feature, error) {
	return c.store.GetFeatureBySlug(repo.ID, slug)
}

func (c *localClient) GetFeatureByID(ctx context.Context, repo *model.Repo, id int64) (*model.Feature, error) {
	return c.store.GetFeatureByID(id)
}

func (c *localClient) CreateFeature(ctx context.Context, repo *model.Repo, in inputs.FeatureAddInput, dryRun bool) (*model.Feature, error) {
	if in.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	slug := in.Slug
	if slug == "" {
		slug = store.Slugify(in.Title)
	}
	if dryRun {
		return &model.Feature{
			RepoID:      repo.ID,
			Slug:        slug,
			Title:       in.Title,
			Description: in.Description,
		}, nil
	}
	f, err := c.store.CreateFeature(repo.ID, slug, in.Title, in.Description)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "feature.create", Kind: "feature",
		TargetID: &f.ID, TargetLabel: f.Slug,
		Details: f.Title,
	})
	return f, nil
}

func (c *localClient) UpdateFeature(ctx context.Context, repo *model.Repo, slug string, title, description *string, dryRun bool) (*model.Feature, error) {
	if title == nil && description == nil {
		return nil, fmt.Errorf("nothing to update; pass title and/or description")
	}
	f, err := c.store.GetFeatureBySlug(repo.ID, slug)
	if err != nil {
		return nil, err
	}
	if dryRun {
		projected := *f
		if title != nil {
			projected.Title = *title
		}
		if description != nil {
			projected.Description = *description
		}
		return &projected, nil
	}
	if err := c.store.UpdateFeature(f.ID, title, description); err != nil {
		return nil, err
	}
	updated, err := c.store.GetFeatureByID(f.ID)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "feature.update", Kind: "feature",
		TargetID: &updated.ID, TargetLabel: updated.Slug,
		Details: updatedFieldList(map[string]bool{
			"title":       title != nil,
			"description": description != nil,
		}),
	})
	return updated, nil
}

func (c *localClient) DeleteFeature(ctx context.Context, repo *model.Repo, slug string, dryRun bool) (*model.Feature, *FeatureDeletePreview, error) {
	f, err := c.store.GetFeatureBySlug(repo.ID, slug)
	if err != nil {
		return nil, nil, err
	}
	if dryRun {
		issues, err := c.store.ListIssues(store.IssueFilter{RepoID: &repo.ID, FeatureID: &f.ID})
		if err != nil {
			return nil, nil, err
		}
		docs, err := c.store.ListDocumentsLinkedToFeature(f.ID)
		if err != nil {
			return nil, nil, err
		}
		return nil, &FeatureDeletePreview{
			Feature:        f,
			WouldDelete:    true,
			IssuesUnlinked: len(issues),
			DocumentLinks:  len(docs),
		}, nil
	}
	if err := c.store.DeleteFeature(f.ID); err != nil {
		return nil, nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "feature.delete", Kind: "feature",
		TargetID: &f.ID, TargetLabel: f.Slug,
		Details: f.Title,
	})
	return f, nil, nil
}

func (c *localClient) ShowFeature(ctx context.Context, repo *model.Repo, slug string) (*FeatureView, error) {
	f, err := c.store.GetFeatureBySlug(repo.ID, slug)
	if err != nil {
		return nil, err
	}
	issues, err := c.store.ListIssues(store.IssueFilter{RepoID: &repo.ID, FeatureID: &f.ID})
	if err != nil {
		return nil, err
	}
	if issues == nil {
		issues = []*model.Issue{}
	}
	docs, err := c.store.ListDocumentsLinkedToFeature(f.ID)
	if err != nil {
		return nil, err
	}
	if docs == nil {
		docs = []*model.DocumentLink{}
	}
	return &FeatureView{Feature: f, Issues: issues, Documents: docs}, nil
}

// PlanFeature is kept in sync with internal/api/handlers_plan.go:buildPlanView.
func (c *localClient) PlanFeature(ctx context.Context, repo *model.Repo, slug string) (*PlanView, error) {
	f, err := c.store.GetFeatureBySlug(repo.ID, slug)
	if err != nil {
		return nil, err
	}
	all, err := c.store.ListIssues(store.IssueFilter{RepoID: &repo.ID, FeatureID: &f.ID})
	if err != nil {
		return nil, err
	}
	open := make([]*model.Issue, 0, len(all))
	for _, iss := range all {
		if isOpenState(iss.State) {
			open = append(open, iss)
		}
	}
	sort.SliceStable(open, func(i, j int) bool {
		if open[i].RepoID != open[j].RepoID {
			return open[i].RepoID < open[j].RepoID
		}
		return open[i].Number < open[j].Number
	})
	ids := make([]int64, len(open))
	inSet := make(map[int64]bool, len(open))
	for i, iss := range open {
		ids[i] = iss.ID
		inSet[iss.ID] = true
	}
	blockers, err := c.store.BlockersFor(ids)
	if err != nil {
		return nil, err
	}
	inDeg := make(map[int64]int, len(open))
	forward := make(map[int64][]int64)
	for _, iss := range open {
		inDeg[iss.ID] = 0
	}
	for blockedID, bs := range blockers {
		for _, b := range bs {
			if !isOpenState(b.BlockerState) {
				continue
			}
			if !inSet[b.BlockerID] {
				continue
			}
			inDeg[blockedID]++
			forward[b.BlockerID] = append(forward[b.BlockerID], blockedID)
		}
	}
	order := make([]*model.Issue, 0, len(open))
	remaining := open
	for len(remaining) > 0 {
		var next []*model.Issue
		var processed []int64
		for _, iss := range remaining {
			if inDeg[iss.ID] == 0 {
				order = append(order, iss)
				processed = append(processed, iss.ID)
			} else {
				next = append(next, iss)
			}
		}
		if len(processed) == 0 {
			keys := make([]string, len(remaining))
			for i, iss := range remaining {
				keys[i] = iss.Key
			}
			return nil, fmt.Errorf("dependency cycle among: %s", strings.Join(keys, ", "))
		}
		for _, id := range processed {
			for _, b := range forward[id] {
				inDeg[b]--
			}
		}
		remaining = next
	}
	view := &PlanView{Feature: f.Slug, Order: make([]PlanEntry, 0, len(order))}
	for _, iss := range order {
		var by []string
		for _, b := range blockers[iss.ID] {
			if !isOpenState(b.BlockerState) {
				continue
			}
			by = append(by, b.BlockerKey)
		}
		sort.Strings(by)
		view.Order = append(view.Order, PlanEntry{
			Key:       iss.Key,
			Title:     iss.Title,
			State:     iss.State,
			Assignee:  iss.Assignee,
			BlockedBy: by,
		})
	}
	return view, nil
}

// isOpenState is kept in sync with internal/api/handlers_plan.go:isOpenState.
func isOpenState(s model.State) bool {
	switch s {
	case model.StateDone, model.StateCancelled, model.StateDuplicate:
		return false
	}
	return true
}
