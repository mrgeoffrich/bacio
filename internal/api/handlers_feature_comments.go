package api

import (
	"fmt"
	"net/http"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// handleFeatureCommentsList — GET /repos/{prefix}/features/{slug}/comments (BACI-124).
func (d deps) handleFeatureCommentsList(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	feat, ok := resolveFeatureOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	cs, err := d.store.ListFeatureComments(feat.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if cs == nil {
		cs = []*model.FeatureComment{}
	}
	writeJSON(w, http.StatusOK, cs)
}

// handleFeatureCommentAdd — POST /repos/{prefix}/features/{slug}/comments (BACI-124).
func (d deps) handleFeatureCommentAdd(w http.ResponseWriter, r *http.Request) {
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
	in, _, err := inputio.DecodeStrict[inputs.FeatureCommentAddInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if in.Author == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "author is required", map[string]any{"field": "author"})
		return
	}
	if in.Body == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "body is required", map[string]any{"field": "body"})
		return
	}
	if isDryRun(r) {
		// Project the would-be insert, applying the same BACI-333 backstop
		// the store would so a dry-run handoff to a disabled feature
		// reports the skip rather than a phantom row.
		if in.Kind == store.FeatureCommentKindHandoff && !feat.CollectHandoffs {
			writeDryRun(w, http.StatusOK, &store.FeatureCommentResult{Skipped: true, Reason: "feature handoffs disabled"})
			return
		}
		kind := in.Kind
		if kind == "" {
			kind = store.FeatureCommentKindNote
		}
		writeDryRun(w, http.StatusCreated, &store.FeatureCommentResult{Comment: &model.FeatureComment{
			FeatureID: feat.ID,
			Author:    in.Author,
			Body:      in.Body,
			Kind:      kind,
		}})
		return
	}
	res, err := d.store.CreateFeatureComment(feat.ID, in.Author, in.Body, in.Kind)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	// BACI-333: a dropped handoff left no row and is not an error — return
	// the {skipped,reason} result with 200 and no audit row.
	if res.Skipped {
		writeJSON(w, http.StatusOK, res)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID:      &repo.ID,
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "feature_comment.add",
		Kind:        "feature",
		TargetID:    &feat.ID,
		TargetLabel: feat.Slug,
		Details:     "by " + in.Author,
	})
	writeJSON(w, http.StatusCreated, res)
}

// handleFeatureCommentDelete — DELETE /repos/{prefix}/features/{slug}/comments/{uuid} (BACI-124).
func (d deps) handleFeatureCommentDelete(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	feat, ok := resolveFeatureOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	uuid := r.PathValue("uuid")
	if uuid == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "comment uuid is required", nil)
		return
	}
	cm, err := d.store.GetFeatureCommentByUUID(uuid)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if cm.FeatureID != feat.ID {
		writeError(w, http.StatusNotFound, "not_found",
			fmt.Sprintf("comment %q does not belong to feature %s", uuid, feat.Slug), nil)
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, &FeatureCommentDeletePreview{
			FeatureSlug: feat.Slug,
			CommentUUID: cm.UUID,
			WouldRemove: 1,
		})
		return
	}
	if err := d.store.DeleteFeatureCommentByUUID(cm.UUID); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID:      &repo.ID,
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "feature_comment.delete",
		Kind:        "feature",
		TargetID:    &feat.ID,
		TargetLabel: feat.Slug,
		Details:     "by " + cm.Author,
	})
	w.WriteHeader(http.StatusNoContent)
}
