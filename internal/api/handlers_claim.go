package api

import (
	"fmt"
	"net/http"

	"github.com/mrgeoffrich/bacio/internal/model"
)

func (d deps) handleFeatureNextPeek(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	feat, ok := resolveFeatureOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	iss, err := d.store.PeekNextIssue(repo.ID, feat.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, &ClaimResult{Issue: iss})
}

func (d deps) handleFeatureNextClaim(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	feat, ok := resolveFeatureOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	if r.Header.Get("X-Actor") == "" {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"X-Actor is required to claim work", map[string]any{"field": "X-Actor"})
		return
	}
	who := ActorFromContext(r.Context())
	if isDryRun(r) {
		iss, err := d.store.PeekNextIssue(repo.ID, feat.ID)
		if err != nil {
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), nil)
			return
		}
		writeDryRun(w, http.StatusOK, &ClaimResult{Issue: iss})
		return
	}
	iss, err := d.store.ClaimNextIssue(repo.ID, feat.ID, who)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if iss != nil {
		recordOp(d.store, d.logger, model.HistoryEntry{
			RepoID: &repo.ID, RepoPrefix: repo.Prefix,
			Actor:    who,
			Op:       "issue.claim",
			Kind:     "issue",
			TargetID: &iss.ID, TargetLabel: iss.Key,
			Details: fmt.Sprintf("claimed by %s (todo → in_progress)", who),
		})
	}
	writeJSON(w, http.StatusOK, &ClaimResult{Issue: iss})
}
