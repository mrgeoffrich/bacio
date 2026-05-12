package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func (d deps) handleFeaturePlan(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	feat, ok := resolveFeatureOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	issues, err := d.store.ListIssues(store.IssueFilter{RepoID: &repo.ID, FeatureID: &feat.ID})
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	view, err := buildPlanView(d.store, feat, issues)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// isOpenState is kept in sync with internal/client/local_feature.go:isOpenState.
func isOpenState(s model.State) bool {
	switch s {
	case model.StateDone, model.StateCancelled:
		return false
	}
	return true
}

// buildPlanView is kept in sync with the local Client backend's PlanFeature
// so JSON consumers see the identical execution-order shape from CLI and HTTP.
func buildPlanView(s *store.Store, f *model.Feature, all []*model.Issue) (*PlanView, error) {
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
	blockers, err := s.BlockersFor(ids)
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
