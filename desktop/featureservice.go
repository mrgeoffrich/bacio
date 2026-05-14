package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// FeatureSummary is one feature, shaped for the desktop feature list.
type FeatureSummary struct {
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// FeatureLinkedIssue is one issue grouped under a feature, for the detail pane.
type FeatureLinkedIssue struct {
	Key        string `json:"key"`
	Title      string `json:"title"`
	State      string `json:"state"`
	StateLabel string `json:"stateLabel"`
}

// FeatureDetail is one feature with its description and linked issues — the
// payload for the desktop feature detail pane.
type FeatureDetail struct {
	Slug        string               `json:"slug"`
	Title       string               `json:"title"`
	Description string               `json:"description"`
	CreatedAt   time.Time            `json:"createdAt"`
	UpdatedAt   time.Time            `json:"updatedAt"`
	Issues      []FeatureLinkedIssue `json:"issues"`
}

// FeatureService is the Wails-bound feature API the desktop frontend talks to.
// It wraps a local bacio client.Client and reshapes its results into the DTOs
// the Features view expects. Read-only: features are created and edited via
// the CLI. Features are per-repo, so every method needs a concrete repo
// prefix (the "all repositories" pseudo-board has no feature scope).
type FeatureService struct {
	client client.Client
}

func NewFeatureService(c client.Client) *FeatureService {
	return &FeatureService{client: c}
}

// resolveRepo turns a repo prefix into a *model.Repo, rejecting the empty /
// "all" pseudo-board since features are always scoped to one repo.
func (f *FeatureService) resolveRepo(ctx context.Context, repoPrefix string) (*model.Repo, error) {
	if repoPrefix == "" || repoPrefix == "all" {
		return nil, fmt.Errorf("select a repository to view its features")
	}
	return f.client.GetRepoByPrefix(ctx, repoPrefix)
}

// ListFeatures returns every feature in one repo as a summary row.
func (f *FeatureService) ListFeatures(repoPrefix string) ([]FeatureSummary, error) {
	ctx := context.Background()
	repo, err := f.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return nil, err
	}
	feats, err := f.client.ListFeatures(ctx, repo, false)
	if err != nil {
		return nil, err
	}
	out := make([]FeatureSummary, 0, len(feats))
	for _, feat := range feats {
		out = append(out, FeatureSummary{
			Slug:      feat.Slug,
			Title:     feat.Title,
			UpdatedAt: feat.UpdatedAt,
		})
	}
	return out, nil
}

// GetFeature returns one feature with its description and the issues grouped
// under it, for the detail pane.
func (f *FeatureService) GetFeature(repoPrefix, slug string) (FeatureDetail, error) {
	ctx := context.Background()
	repo, err := f.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return FeatureDetail{}, err
	}
	view, err := f.client.ShowFeature(ctx, repo, slug)
	if err != nil {
		return FeatureDetail{}, err
	}
	feat := view.Feature
	issues := make([]FeatureLinkedIssue, 0, len(view.Issues))
	for _, iss := range view.Issues {
		issues = append(issues, FeatureLinkedIssue{
			Key:        iss.Key,
			Title:      iss.Title,
			State:      string(iss.State),
			StateLabel: stateLabel(iss.State),
		})
	}
	return FeatureDetail{
		Slug:        feat.Slug,
		Title:       feat.Title,
		Description: feat.Description,
		CreatedAt:   feat.CreatedAt,
		UpdatedAt:   feat.UpdatedAt,
		Issues:      issues,
	}, nil
}
