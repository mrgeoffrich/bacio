package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func (d deps) handleReposList(w http.ResponseWriter, r *http.Request) {
	repos, err := d.store.ListRepos()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if repos == nil {
		repos = []*model.Repo{}
	}
	writeJSON(w, http.StatusOK, repos)
}

func (d deps) handleReposShow(w http.ResponseWriter, r *http.Request) {
	prefix := strings.ToUpper(r.PathValue("prefix"))
	repo, err := d.store.GetRepoByPrefix(prefix)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, repo)
}

func (d deps) handleReposCreate(w http.ResponseWriter, r *http.Request) {
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.RepoCreateInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "name is required", map[string]any{"field": "name"})
		return
	}
	if strings.TrimSpace(in.Path) == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "path is required", map[string]any{"field": "path"})
		return
	}

	var prefix string
	if in.Prefix != "" {
		p, err := store.ValidatePrefix(in.Prefix)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "prefix"})
			return
		}
		prefix = p
	}

	if existing, err := d.store.GetRepoByPath(in.Path); err == nil {
		writeError(w, http.StatusConflict, "conflict",
			"repo already registered for this path",
			map[string]any{"prefix": existing.Prefix, "path": existing.Path})
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}

	if prefix != "" {
		if existing, err := d.store.GetRepoByPrefix(prefix); err == nil {
			writeError(w, http.StatusConflict, "conflict",
				"prefix already in use",
				map[string]any{"prefix": existing.Prefix, "path": existing.Path})
			return
		} else if !errors.Is(err, store.ErrNotFound) {
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), nil)
			return
		}
	} else {
		p, err := d.store.AllocatePrefix(in.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
			return
		}
		prefix = p
	}

	if isDryRun(r) {
		writeDryRun(w, http.StatusCreated, &model.Repo{
			Prefix:          prefix,
			Name:            in.Name,
			Path:            in.Path,
			RemoteURL:       in.RemoteURL,
			NextIssueNumber: 1,
		})
		return
	}

	repo, err := d.store.CreateRepo(prefix, in.Name, in.Path, in.RemoteURL)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    ActorFromContext(r.Context()),
		Op:       "repo.create",
		Kind:     "repo",
		TargetID: &repo.ID, TargetLabel: repo.Prefix,
		Details: "api init (" + repo.Name + ")",
	})
	writeJSON(w, http.StatusCreated, repo)
}

// handleReposDelete implements `DELETE /repos/{prefix}` — the
// destructive end of `bacio repo rm`. The destruction is gated on a
// `confirm=<prefix>` query parameter that must equal the path prefix
// (case-insensitive); without it the server returns 412 Precondition
// Failed with the impact preview embedded in the error envelope's
// `details`. `?dry_run=true` short-circuits to the same preview as a
// 200 OK with no changes.
//
// The gate lives here (not just in the CLI) so that a direct
// curl -XDELETE without the confirmation token gets the same safety
// as the CLI — no agent / proxy can bypass the alert by skipping a
// flag.
func (d deps) handleReposDelete(w http.ResponseWriter, r *http.Request) {
	prefix := strings.ToUpper(r.PathValue("prefix"))
	repo, err := d.store.GetRepoByPrefix(prefix)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	counts, err := d.store.RepoCascadeCountsForID(repo.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	preview := struct {
		Repo        *model.Repo             `json:"repo"`
		Cascade     store.RepoCascadeCounts `json:"cascade"`
		WouldDelete bool                    `json:"would_delete"`
	}{Repo: repo, Cascade: counts, WouldDelete: true}

	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, preview)
		return
	}
	confirm := strings.TrimSpace(r.URL.Query().Get("confirm"))
	if !strings.EqualFold(confirm, prefix) {
		// 412 because we have a target but the precondition (matching
		// confirmation token) failed; not 400 (input is well-formed)
		// nor 403 (auth is fine).
		details := map[string]any{
			"repo":         preview.Repo,
			"cascade":      preview.Cascade,
			"would_delete": true,
		}
		msg := "destructive operation requires ?confirm=" + prefix + " (or --confirm " + prefix + " from the CLI); ask the user before proceeding"
		if confirm != "" {
			msg = "confirm value " + confirm + " does not match repo prefix " + prefix
		}
		writeError(w, http.StatusPreconditionFailed, "confirm_required", msg, details)
		return
	}
	if err := d.store.DeleteHistoryByRepo(repo.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	if err := d.store.DeleteRepo(repo.ID); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		// repo_id NULL — the row is gone; only the prefix snapshot
		// remains for callers querying history afterwards.
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "repo.delete",
		Kind:        "repo",
		TargetLabel: repo.Prefix,
		Details:     repoCascadeDetails(repo, counts),
	})
	w.WriteHeader(http.StatusNoContent)
}

// repoCascadeDetails mirrors local_repo.go:formatCascadeDetails so
// audit messages from CLI and HTTP deletes read identically.
func repoCascadeDetails(repo *model.Repo, c store.RepoCascadeCounts) string {
	return fmt.Sprintf("%s (%d issues, %d comments, %d features, %d documents, %d history)",
		repo.Name, c.Issues, c.Comments, c.Features, c.Documents, c.History)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(body)
}
