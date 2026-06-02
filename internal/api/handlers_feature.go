package api

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// featureHideInput is the body shape for PUT /repos/{prefix}/features/{slug}/hide
// (BACI-177). Pointer-of-bool so the "hidden field omitted" case is
// distinguishable from "hidden: false".
type featureHideInput struct {
	Hidden *bool `json:"hidden"`
}

// featureAutoCloseInput is the body shape for PUT
// /repos/{prefix}/features/{slug}/auto-close (BACI-250). Same
// pointer-of-bool shape as featureHideInput so a missing field is
// distinguishable from `enabled: false`.
type featureAutoCloseInput struct {
	Enabled *bool `json:"enabled"`
}

// featureCollectHandoffsInput is the body shape for PUT
// /repos/{prefix}/features/{slug}/handoffs (BACI-333). Same
// pointer-of-bool shape so a missing field is distinguishable from
// `enabled: false`.
type featureCollectHandoffsInput struct {
	Enabled *bool `json:"enabled"`
}

func (d deps) handleFeaturesList(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	q := r.URL.Query()
	withDesc := q.Get("with_description")
	feats, err := d.store.ListFeaturesFiltered(store.FeatureFilter{
		RepoID:             repo.ID,
		IncludeDescription: withDesc == "true" || withDesc == "1",
		IncludeArchived:    includeArchivedFromRequest(r, d.store),
		// BACI-177: every features-list response carries the per-feature
		// "Show on board" flag so the Features screen reads it without
		// a second round-trip.
		WithHiddenOnBoard: true,
	})
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if feats == nil {
		feats = []*model.Feature{}
	}
	writeJSON(w, http.StatusOK, feats)
}

func (d deps) handleFeatureShow(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	feat, ok := resolveFeatureOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	// BACI-177: inflate HiddenOnBoard from the per-repo board-hide KV
	// so the FeaturesView toggle reads the current state without an
	// extra round-trip.
	if hidden, herr := d.store.IsFeatureHiddenOnBoard(repo.ID, feat.Slug); herr == nil {
		feat.HiddenOnBoard = hidden
	}
	issues, err := d.store.ListIssues(store.IssueFilter{RepoID: &repo.ID, FeatureID: &feat.ID})
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if issues == nil {
		issues = []*model.Issue{}
	}
	docs, err := d.store.ListDocumentsLinkedToFeature(feat.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if docs == nil {
		docs = []*model.DocumentLink{}
	}
	comments, err := d.store.ListFeatureComments(feat.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if comments == nil {
		comments = []*model.FeatureComment{}
	}
	writeJSON(w, http.StatusOK, &FeatureView{
		Feature:   feat,
		Issues:    issues,
		Documents: docs,
		Comments:  comments,
	})
}

func (d deps) handleFeatureCreate(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.FeatureAddInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if in.Title == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "title is required", map[string]any{"field": "title"})
		return
	}
	slug := in.Slug
	if slug == "" {
		slug = store.Slugify(in.Title)
	}
	if isDryRun(r) {
		projected := &model.Feature{
			RepoID:      repo.ID,
			Slug:        slug,
			Title:       in.Title,
			Description: in.Description,
			Emoji:       in.Emoji,
		}
		// BACI-225: surface branch_name on dry-run too so an agent can
		// confirm the projected payload before committing.
		if in.BranchName != "" {
			b := in.BranchName
			projected.BranchName = &b
		}
		writeDryRun(w, http.StatusCreated, projected)
		return
	}
	feat, err := d.store.CreateFeature(repo.ID, slug, in.Title, in.Description, in.Emoji, in.BranchName)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    ActorFromContext(r.Context()),
		Op:       "feature.create",
		Kind:     "feature",
		TargetID: &feat.ID, TargetLabel: feat.Slug,
		Details: feat.Title,
	})
	writeJSON(w, http.StatusCreated, feat)
}

func (d deps) handleFeatureEdit(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, present, err := inputio.DecodeStrict[inputs.FeatureEditInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	feat, ok := resolveFeatureOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	_ = in.Slug
	var tPtr, dPtr, ePtr, bPtr *string
	if _, ok := present["title"]; ok {
		if in.Title == nil || *in.Title == "" {
			writeError(w, http.StatusBadRequest, "invalid_input",
				"title cannot be empty or null; omit the field to leave it unchanged",
				map[string]any{"field": "title"})
			return
		}
		tPtr = in.Title
	}
	if _, ok := present["description"]; ok {
		if in.Description == nil {
			empty := ""
			dPtr = &empty
		} else {
			dPtr = in.Description
		}
	}
	// BACI-172: emoji absent = no change; present (even null / "") = update
	// (null and explicit empty both clear the glyph).
	if _, ok := present["emoji"]; ok {
		if in.Emoji == nil {
			empty := ""
			ePtr = &empty
		} else {
			ePtr = in.Emoji
		}
	}
	// BACI-225: branch_name absent = no change; present (even null / "")
	// = clear the column back to NULL (ship to main). Same shape as
	// emoji above.
	if _, ok := present["branch_name"]; ok {
		if in.BranchName == nil {
			empty := ""
			bPtr = &empty
		} else {
			bPtr = in.BranchName
		}
	}
	if tPtr == nil && dPtr == nil && ePtr == nil && bPtr == nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "nothing to update", nil)
		return
	}
	if isDryRun(r) {
		projected := *feat
		if tPtr != nil {
			projected.Title = *tPtr
		}
		if dPtr != nil {
			projected.Description = *dPtr
		}
		if ePtr != nil {
			projected.Emoji = *ePtr
		}
		if bPtr != nil {
			if *bPtr == "" {
				projected.BranchName = nil
			} else {
				b := *bPtr
				projected.BranchName = &b
			}
		}
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	if err := d.store.UpdateFeature(feat.ID, tPtr, dPtr, ePtr, bPtr); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	updated, err := d.store.GetFeatureByID(feat.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    ActorFromContext(r.Context()),
		Op:       "feature.update",
		Kind:     "feature",
		TargetID: &updated.ID, TargetLabel: updated.Slug,
		Details: updatedFieldList(map[string]bool{
			"title":       tPtr != nil,
			"description": dPtr != nil,
			"emoji":       ePtr != nil,
			"branch_name": bPtr != nil,
		}),
	})
	writeJSON(w, http.StatusOK, updated)
}

func (d deps) handleFeatureDelete(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	feat, ok := resolveFeatureOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	if isDryRun(r) {
		preview, err := buildFeatureDeletePreview(d.store, repo, feat)
		if err != nil {
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), nil)
			return
		}
		writeDryRun(w, http.StatusOK, preview)
		return
	}
	if err := d.store.DeleteFeature(feat.ID); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    ActorFromContext(r.Context()),
		Op:       "feature.delete",
		Kind:     "feature",
		TargetID: &feat.ID, TargetLabel: feat.Slug,
		Details: feat.Title,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleFeaturesHidden (BACI-177) returns the set of feature slugs the
// user has hidden on the kanban board. Backs the per-feature toggle on
// the Features screen so the React component reads the current state
// without iterating ListFeatures looking for the flag.
func (d deps) handleFeaturesHidden(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	hidden, err := d.store.LoadHiddenFeatures(repo.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	slugs := make([]string, 0, len(hidden))
	for slug, on := range hidden {
		if on {
			slugs = append(slugs, slug)
		}
	}
	sort.Strings(slugs)
	writeJSON(w, http.StatusOK, map[string]any{"slugs": slugs})
}

// handleFeatureHide (BACI-177) flips the per-feature "Show on board"
// toggle. Body: {"hidden": bool}. Idempotent — flipping to the same
// state is a no-op write and skips the audit row. Audits as
// feature.hide / feature.unhide so `bacio history --op feature.hide`
// works out of the box.
func (d deps) handleFeatureHide(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	feat, ok := resolveFeatureOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	body, _, err := inputio.DecodeStrict[featureHideInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if body.Hidden == nil {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"hidden is required", map[string]any{"field": "hidden"})
		return
	}
	want := *body.Hidden
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, map[string]any{"slug": feat.Slug, "hidden": want})
		return
	}
	current, err := d.store.IsFeatureHiddenOnBoard(repo.ID, feat.Slug)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if current != want {
		if err := d.store.SetFeatureHiddenOnBoard(repo.ID, feat.Slug, want); err != nil {
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), nil)
			return
		}
		op := "feature.unhide"
		if want {
			op = "feature.hide"
		}
		recordOp(d.store, d.logger, model.HistoryEntry{
			Actor:       ActorFromContext(r.Context()),
			RepoID:      &repo.ID,
			RepoPrefix:  repo.Prefix,
			Op:          op,
			Kind:        "feature",
			TargetID:    &feat.ID,
			TargetLabel: feat.Slug,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"slug": feat.Slug, "hidden": want})
}

// handleFeatureState (BACI-199) flips the feature's three-state
// column to the value carried in the JSON body. Body shape is
// `{"slug": "<slug>", "state": "active|done|cancelled"}` — the slug
// in the body is required by the strict decoder but the path-resolved
// feature is the authoritative target.
//
// BACI-250 decoupled this endpoint from the auto-close pin: a state
// write no longer touches `state_manual`. Use
// PUT /repos/{prefix}/features/{slug}/auto-close to flip the pin.
func (d deps) handleFeatureState(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.FeatureStateInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	feat, ok := resolveFeatureOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	if in.State == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "state is required", map[string]any{"field": "state"})
		return
	}
	st, err := model.ParseFeatureState(in.State)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "state"})
		return
	}
	if isDryRun(r) {
		projected := *feat
		projected.State = st
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	oldState := feat.State
	if err := d.store.SetFeatureState(feat.ID, st); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	updated, err := d.store.GetFeatureByID(feat.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID:      &feat.RepoID,
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "feature.state",
		Kind:        "feature",
		TargetID:    &updated.ID,
		TargetLabel: updated.Slug,
		Details:     fmt.Sprintf("%s → %s", oldState, st),
	})
	writeJSON(w, http.StatusOK, updated)
}

// handleFeatureAutoClose (BACI-250) flips the per-feature auto-close
// toggle — the sticky-bit `state_manual` column that gates the
// BACI-199 auto-completion sweep. Body: `{"enabled": bool}`. Mirrors
// handleFeatureHide's shape: pointer-of-bool to distinguish absent
// from `false`, idempotent no-ops skip the audit row, audits as
// `feature.auto-close` with Details `off → on` / `on → off`.
func (d deps) handleFeatureAutoClose(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	feat, ok := resolveFeatureOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	body, _, err := inputio.DecodeStrict[featureAutoCloseInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if body.Enabled == nil {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"enabled is required", map[string]any{"field": "enabled"})
		return
	}
	want := *body.Enabled
	if isDryRun(r) {
		projected := *feat
		projected.StateManual = !want
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	current := !feat.StateManual // auto-close ON iff state_manual is false
	if current == want {
		// Idempotent — return the row unchanged and skip the audit row.
		writeJSON(w, http.StatusOK, feat)
		return
	}
	if err := d.store.SetFeatureAutoClose(feat.ID, want); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	updated, err := d.store.GetFeatureByID(feat.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	from, to := "on", "off"
	if want {
		from, to = "off", "on"
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID:      &feat.RepoID,
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "feature.auto-close",
		Kind:        "feature",
		TargetID:    &updated.ID,
		TargetLabel: updated.Slug,
		Details:     fmt.Sprintf("%s → %s", from, to),
	})
	writeJSON(w, http.StatusOK, updated)
}

// handleFeatureCollectHandoffs (BACI-333) flips the per-feature
// `collect_handoffs` toggle that gates whether worker close-outs append
// handoff comments to this feature. Body: `{"enabled": bool}`. Mirrors
// handleFeatureAutoClose 1:1 except there is no polarity inversion —
// `enabled` maps straight to the column; idempotent no-ops skip the
// audit row; audits as `feature.handoffs` with Details `off → on` / `on → off`.
func (d deps) handleFeatureCollectHandoffs(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	feat, ok := resolveFeatureOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	body, _, err := inputio.DecodeStrict[featureCollectHandoffsInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if body.Enabled == nil {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"enabled is required", map[string]any{"field": "enabled"})
		return
	}
	want := *body.Enabled
	if isDryRun(r) {
		projected := *feat
		projected.CollectHandoffs = want
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	if feat.CollectHandoffs == want {
		// Idempotent — return the row unchanged and skip the audit row.
		writeJSON(w, http.StatusOK, feat)
		return
	}
	if err := d.store.SetFeatureCollectHandoffs(feat.ID, want); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	updated, err := d.store.GetFeatureByID(feat.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	from, to := "on", "off"
	if want {
		from, to = "off", "on"
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID:      &feat.RepoID,
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "feature.handoffs",
		Kind:        "feature",
		TargetID:    &updated.ID,
		TargetLabel: updated.Slug,
		Details:     fmt.Sprintf("%s → %s", from, to),
	})
	writeJSON(w, http.StatusOK, updated)
}

// buildFeatureDeletePreview is kept in sync with the local Client backend's
// DeleteFeature dry-run branch.
func buildFeatureDeletePreview(s *store.Store, repo *model.Repo, feat *model.Feature) (*FeatureDeletePreview, error) {
	issues, err := s.ListIssues(store.IssueFilter{RepoID: &repo.ID, FeatureID: &feat.ID})
	if err != nil {
		return nil, err
	}
	docs, err := s.ListDocumentsLinkedToFeature(feat.ID)
	if err != nil {
		return nil, err
	}
	comments, err := s.CountFeatureComments(feat.ID)
	if err != nil {
		return nil, err
	}
	return &FeatureDeletePreview{
		Feature:        feat,
		WouldDelete:    true,
		IssuesUnlinked: len(issues),
		DocumentLinks:  len(docs),
		Comments:       comments,
	}, nil
}
