package api

import (
	"net/http"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

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
		writeDryRun(w, http.StatusCreated, projected)
		return
	}
	feat, err := d.store.CreateFeature(repo.ID, slug, in.Title, in.Description, in.Emoji)
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
	var tPtr, dPtr, ePtr *string
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
	if tPtr == nil && dPtr == nil && ePtr == nil {
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
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	if err := d.store.UpdateFeature(feat.ID, tPtr, dPtr, ePtr); err != nil {
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
