package client

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func (c *localClient) ListFeatures(ctx context.Context, repo *model.Repo, withDescription, includeArchived bool) ([]*model.Feature, error) {
	feats, err := c.store.ListFeaturesFiltered(store.FeatureFilter{
		RepoID:             repo.ID,
		IncludeDescription: withDescription,
		IncludeArchived:    includeArchived,
		// BACI-177: every local feature read inflates the per-feature
		// "Show on board" flag so the React component reads it without
		// a second round-trip. The lookup is one cheap KV get per repo.
		WithHiddenOnBoard: true,
	})
	if err != nil {
		return nil, err
	}
	if feats == nil {
		feats = []*model.Feature{}
	}
	return feats, nil
}

func (c *localClient) GetFeatureBySlug(ctx context.Context, repo *model.Repo, slug string) (*model.Feature, error) {
	// BACI-177: inflate HiddenOnBoard alongside the row so the
	// FeaturesView toggle reads the current state without an extra
	// fetch.
	return c.store.GetFeatureBySlugWithHidden(repo.ID, slug)
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
		projected := &model.Feature{
			RepoID:      repo.ID,
			Slug:        slug,
			Title:       in.Title,
			Description: in.Description,
			Emoji:       in.Emoji,
		}
		// BACI-225: surface branch_name on dry-run so an agent can
		// confirm the projected payload before committing.
		if in.BranchName != "" {
			b := in.BranchName
			projected.BranchName = &b
		}
		return projected, nil
	}
	f, err := c.store.CreateFeature(repo.ID, slug, in.Title, in.Description, in.Emoji, in.BranchName)
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

func (c *localClient) UpdateFeature(ctx context.Context, repo *model.Repo, slug string, title, description, emoji, branchName *string, dryRun bool) (*model.Feature, error) {
	if title == nil && description == nil && emoji == nil && branchName == nil {
		return nil, fmt.Errorf("nothing to update; pass title, description, emoji, and/or branch_name")
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
		if emoji != nil {
			projected.Emoji = *emoji
		}
		if branchName != nil {
			// BACI-225: non-nil empty string clears the column back
			// to NULL (the legacy "ship to main" default).
			if *branchName == "" {
				projected.BranchName = nil
			} else {
				b := *branchName
				projected.BranchName = &b
			}
		}
		return &projected, nil
	}
	if err := c.store.UpdateFeature(f.ID, title, description, emoji, branchName); err != nil {
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
			"emoji":       emoji != nil,
			"branch_name": branchName != nil,
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
		comments, err := c.store.CountFeatureComments(f.ID)
		if err != nil {
			return nil, nil, err
		}
		return nil, &FeatureDeletePreview{
			Feature:        f,
			WouldDelete:    true,
			IssuesUnlinked: len(issues),
			DocumentLinks:  len(docs),
			Comments:       comments,
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
	comments, err := c.store.ListFeatureComments(f.ID)
	if err != nil {
		return nil, err
	}
	if comments == nil {
		comments = []*model.FeatureComment{}
	}
	return &FeatureView{Feature: f, Issues: issues, Documents: docs, Comments: comments}, nil
}

// PlanFeature is kept in sync with internal/api/handlers_plan.go:buildPlanView.
// includeClosed (BACI-236) widens the input set and the blocker filter:
//   - false (default) — only open issues and only edges between two open
//     issues. Historical shape used by `bacio feature plan` and the
//     `bacio issue brief` assembler.
//   - true — every issue in the feature plus every `blocks` edge whose
//     endpoints are both in the feature. Closed=true is stamped on
//     done/cancelled entries so the desktop / web dependency-graph view
//     can mute them alongside the live work.
func (c *localClient) PlanFeature(ctx context.Context, repo *model.Repo, slug string, includeClosed bool) (*PlanView, error) {
	f, err := c.store.GetFeatureBySlug(repo.ID, slug)
	if err != nil {
		return nil, err
	}
	all, err := c.store.ListIssues(store.IssueFilter{RepoID: &repo.ID, FeatureID: &f.ID})
	if err != nil {
		return nil, err
	}
	considered := make([]*model.Issue, 0, len(all))
	for _, iss := range all {
		if includeClosed || isOpenState(iss.State) {
			considered = append(considered, iss)
		}
	}
	sort.SliceStable(considered, func(i, j int) bool {
		if considered[i].RepoID != considered[j].RepoID {
			return considered[i].RepoID < considered[j].RepoID
		}
		return considered[i].Number < considered[j].Number
	})
	ids := make([]int64, len(considered))
	inSet := make(map[int64]bool, len(considered))
	for i, iss := range considered {
		ids[i] = iss.ID
		inSet[iss.ID] = true
	}
	blockers, err := c.store.BlockersFor(ids)
	if err != nil {
		return nil, err
	}
	inDeg := make(map[int64]int, len(considered))
	forward := make(map[int64][]int64)
	for _, iss := range considered {
		inDeg[iss.ID] = 0
	}
	for blockedID, bs := range blockers {
		for _, b := range bs {
			// BACI-236: when includeClosed is set, accept blocker edges
			// regardless of either endpoint's state — the graph view
			// wants to see "this open ticket was blocked by that
			// delivered one". The in-feature gate stays untouched: a
			// cross-feature blocker is not a node in this graph, so
			// there's no destination to draw the edge to.
			if !includeClosed && !isOpenState(b.BlockerState) {
				continue
			}
			if !inSet[b.BlockerID] {
				continue
			}
			inDeg[blockedID]++
			forward[b.BlockerID] = append(forward[b.BlockerID], blockedID)
		}
	}
	order := make([]*model.Issue, 0, len(considered))
	remaining := considered
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
			if !includeClosed && !isOpenState(b.BlockerState) {
				continue
			}
			if !inSet[b.BlockerID] {
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
			Closed:    !isOpenState(iss.State),
		})
	}
	return view, nil
}

// isOpenState is kept in sync with internal/api/handlers_plan.go:isOpenState.
func isOpenState(s model.State) bool {
	switch s {
	case model.StateDone, model.StateCancelled:
		return false
	}
	return true
}

// ArchiveFeature stamps the feature's archived_at column (BACI-68).
// Records a feature.archive audit row under the resolved actor.
func (c *localClient) ArchiveFeature(ctx context.Context, repo *model.Repo, slug string, dryRun bool) (*model.Feature, error) {
	return c.setFeatureArchived(ctx, repo, slug, true, dryRun, "feature.archive")
}

// UnarchiveFeature clears archived_at (BACI-68).
func (c *localClient) UnarchiveFeature(ctx context.Context, repo *model.Repo, slug string, dryRun bool) (*model.Feature, error) {
	return c.setFeatureArchived(ctx, repo, slug, false, dryRun, "feature.unarchive")
}

// IsFeatureHiddenOnBoard (BACI-177) returns the per-feature board-hide
// flag for slug in repo. Defaults to false on an unknown slug — the
// caller's existence check is the feature read upstream of this.
func (c *localClient) IsFeatureHiddenOnBoard(ctx context.Context, repo *model.Repo, slug string) (bool, error) {
	return c.store.IsFeatureHiddenOnBoard(repo.ID, slug)
}

// SetFeatureHiddenOnBoard (BACI-177) flips the per-feature board-hide
// flag and returns the resulting state. Records an audit row under
// feature.hide / feature.unhide so the history surface tracks who
// toggled it; idempotent flips skip the audit (the store-side
// SetFeatureHiddenOnBoard is a no-op for unchanged state, but we still
// want the audit row only on a real flip).
func (c *localClient) SetFeatureHiddenOnBoard(ctx context.Context, repo *model.Repo, slug string, hidden, dryRun bool) (bool, error) {
	// Resolve the feature first so the audit row carries a real
	// target id + slug, and so a typo (unknown slug) errors here
	// instead of silently writing an orphan KV entry.
	feat, err := c.store.GetFeatureBySlug(repo.ID, slug)
	if err != nil {
		return false, err
	}
	current, err := c.store.IsFeatureHiddenOnBoard(repo.ID, slug)
	if err != nil {
		return false, err
	}
	if dryRun {
		return hidden, nil
	}
	if current == hidden {
		// No-op — don't churn the audit log on idempotent flips.
		return current, nil
	}
	if err := c.store.SetFeatureHiddenOnBoard(repo.ID, slug, hidden); err != nil {
		return false, err
	}
	op := "feature.unhide"
	if hidden {
		op = "feature.hide"
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: op, Kind: "feature",
		TargetID: &feat.ID, TargetLabel: feat.Slug,
	})
	return hidden, nil
}

// ListHiddenFeatureSlugs (BACI-177) returns every slug currently
// hidden in repo, sorted alphabetically for stable JSON output.
func (c *localClient) ListHiddenFeatureSlugs(ctx context.Context, repo *model.Repo) ([]string, error) {
	hidden, err := c.store.LoadHiddenFeatures(repo.ID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(hidden))
	for slug, on := range hidden {
		if on {
			out = append(out, slug)
		}
	}
	sort.Strings(out)
	return out, nil
}

// SetFeatureState (BACI-199) flips the feature's three-state column
// and stamps the sticky bit so the auto-completion sweep leaves the
// row alone. Records a `feature.state` audit row with Details of the
// form "old → new" — same shape as SetIssueState.
func (c *localClient) SetFeatureState(ctx context.Context, repo *model.Repo, slug string, state model.FeatureState, dryRun bool) (*model.Feature, error) {
	feat, err := c.store.GetFeatureBySlug(repo.ID, slug)
	if err != nil {
		return nil, err
	}
	if dryRun {
		projected := *feat
		projected.State = state
		projected.StateManual = true
		return &projected, nil
	}
	oldState := feat.State
	if err := c.store.SetFeatureState(feat.ID, state, true); err != nil {
		return nil, err
	}
	updated, err := c.store.GetFeatureByID(feat.ID)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &feat.RepoID, RepoPrefix: repo.Prefix,
		Op: "feature.state", Kind: "feature",
		TargetID: &updated.ID, TargetLabel: updated.Slug,
		Details: fmt.Sprintf("%s → %s", oldState, state),
	})
	return updated, nil
}

func (c *localClient) setFeatureArchived(ctx context.Context, repo *model.Repo, slug string, archived, dryRun bool, op string) (*model.Feature, error) {
	feat, err := c.store.GetFeatureBySlug(repo.ID, slug)
	if err != nil {
		return nil, err
	}
	if dryRun {
		projected := *feat
		// Mirror the store's idempotent semantics — see local_issue.go's
		// setIssueArchived for the rationale.
		if archived && feat.ArchivedAt == nil {
			now := time.Now().UTC()
			projected.ArchivedAt = &now
		} else if !archived {
			projected.ArchivedAt = nil
		}
		return &projected, nil
	}
	if err := c.store.SetFeatureArchived(feat.ID, archived); err != nil {
		return nil, err
	}
	updated, err := c.store.GetFeatureByID(feat.ID)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &feat.RepoID, RepoPrefix: repo.Prefix,
		Op: op, Kind: "feature",
		TargetID: &updated.ID, TargetLabel: updated.Slug,
	})
	return updated, nil
}
