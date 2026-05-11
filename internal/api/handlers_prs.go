package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func (d deps) handlePRsList(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	prs, err := d.store.ListPRs(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if prs == nil {
		prs = []*model.PullRequest{}
	}
	writeJSON(w, http.StatusOK, prs)
}

func (d deps) handlePRAttach(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.PRAttachInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	if in.URL == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "url is required", map[string]any{"field": "url"})
		return
	}
	clean, err := store.ValidatePRURLStrict(in.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "url"})
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusCreated, &model.PullRequest{
			IssueID: iss.ID,
			URL:     clean,
		})
		return
	}
	pr, err := d.store.AttachPR(iss.ID, clean)
	if err != nil {
		if errors.Is(err, store.ErrPRAlreadyAttached) {
			writeError(w, http.StatusConflict, "conflict", err.Error(), nil)
			return
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID:      &iss.RepoID,
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "pr.attach",
		Kind:        "issue",
		TargetID:    &iss.ID,
		TargetLabel: iss.Key,
		Details:     clean,
	})
	writeJSON(w, http.StatusCreated, pr)
}

func (d deps) handlePRDetach(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	url, ok := prURLFromRequest(w, r)
	if !ok {
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	if isDryRun(r) {
		prs, err := d.store.ListPRs(iss.ID)
		if err != nil {
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), nil)
			return
		}
		matched := false
		for _, p := range prs {
			if p.URL == url {
				matched = true
				break
			}
		}
		if !matched {
			writeError(w, http.StatusNotFound, "not_found",
				fmt.Sprintf("no PR matching %q on %s", url, iss.Key), nil)
			return
		}
		writeDryRun(w, http.StatusOK, &PRDetachPreview{
			IssueKey:    iss.Key,
			URL:         url,
			WouldRemove: 1,
		})
		return
	}
	n, err := d.store.DetachPR(iss.ID, url)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "not_found",
			fmt.Sprintf("no PR matching %q on %s", url, iss.Key), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID:      &iss.RepoID,
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "pr.detach",
		Kind:        "issue",
		TargetID:    &iss.ID,
		TargetLabel: iss.Key,
		Details:     url,
	})
	w.WriteHeader(http.StatusNoContent)
}

// prURLFromRequest accepts the URL from a JSON body (preferred when one is
// supplied) or falls back to the ?url= query parameter. The body's
// issue_key field is ignored — the URL path is the source of truth for
// which issue this detach applies to.
func prURLFromRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.ContentLength > 0 {
		raw, ok := readBody(r, w)
		if !ok {
			return "", false
		}
		in, _, err := inputio.DecodeStrict[inputs.PRDetachInput](raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
			return "", false
		}
		clean := strings.TrimSpace(in.URL)
		if clean == "" {
			writeError(w, http.StatusBadRequest, "invalid_input", "url is required", map[string]any{"field": "url"})
			return "", false
		}
		return clean, true
	}
	clean := strings.TrimSpace(r.URL.Query().Get("url"))
	if clean == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "url is required (body or ?url=)", map[string]any{"field": "url"})
		return "", false
	}
	return clean, true
}
