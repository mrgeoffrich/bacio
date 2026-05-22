package api

import (
	"fmt"
	"net/http"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
)

func (d deps) handleCommentsList(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	cs, err := d.store.ListComments(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if cs == nil {
		cs = []*model.Comment{}
	}
	writeJSON(w, http.StatusOK, cs)
}

func (d deps) handleCommentAdd(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.CommentAddInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
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
		writeDryRun(w, http.StatusCreated, &model.Comment{
			IssueID: iss.ID,
			Author:  in.Author,
			Body:    in.Body,
		})
		return
	}
	c, err := d.store.CreateComment(iss.ID, in.Author, in.Body)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID:      &iss.RepoID,
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "comment.add",
		Kind:        "issue",
		TargetID:    &iss.ID,
		TargetLabel: iss.Key,
		Details:     "by " + in.Author,
	})
	writeJSON(w, http.StatusCreated, c)
}

func (d deps) handleCommentDelete(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	uuid := r.PathValue("uuid")
	if uuid == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "comment uuid is required", nil)
		return
	}
	cm, err := d.store.GetCommentByUUID(uuid)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if cm.IssueID != iss.ID {
		writeError(w, http.StatusNotFound, "not_found",
			fmt.Sprintf("comment %q does not belong to %s", uuid, iss.Key), nil)
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, &CommentDeletePreview{
			IssueKey:    iss.Key,
			CommentUUID: cm.UUID,
			WouldRemove: 1,
		})
		return
	}
	if err := d.store.DeleteCommentByUUID(cm.UUID); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID:      &iss.RepoID,
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "comment.delete",
		Kind:        "issue",
		TargetID:    &iss.ID,
		TargetLabel: iss.Key,
		Details:     "by " + cm.Author,
	})
	w.WriteHeader(http.StatusNoContent)
}
