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
	// BACI-236: ?include_closed=1 widens the payload to include
	// done/cancelled issues and the blocker edges that touch them.
	// Default-empty / "0" / "false" / unrecognised values preserve
	// today's open-only shape — the FeaturesView graph tab is the only
	// caller that flips this on.
	includeClosed := planIncludeClosed(r)
	view, err := buildPlanView(d.store, feat, issues, includeClosed)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// planIncludeClosed reads the include_closed query param permissively
// (1 / true / yes — case-insensitive). Anything else is treated as
// false so a fat-fingered URL never silently widens the payload.
func planIncludeClosed(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("include_closed"))) {
	case "1", "true", "yes":
		return true
	}
	return false
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
// includeClosed (BACI-236) widens the issue set AND the blocker filter so the
// dependency-graph view can render closed nodes muted while keeping the open
// chain visible.
func buildPlanView(s *store.Store, f *model.Feature, all []*model.Issue, includeClosed bool) (*PlanView, error) {
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
	blockers, err := s.BlockersFor(ids)
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
			// BACI-236: skip closed blockers only on the default path
			// — when includeClosed is set, every edge between two
			// in-feature nodes is part of the graph regardless of
			// either endpoint's state. The in-feature gate (inSet)
			// stays untouched on both paths so a cross-feature
			// blocker never sneaks in.
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
