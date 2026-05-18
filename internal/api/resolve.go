package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// includeArchivedFromRequest resolves the per-call ?include_archived=1 query
// flag against the display.show_archived global setting. Either source can
// opt the caller in to archived rows (OR semantics); the query string wins
// when it's the only signal asking for them. Mirrors the CLI's behaviour on
// `bacio issue list --include-archived`. (BACI-68)
func includeArchivedFromRequest(r *http.Request, s *store.Store) bool {
	q := r.URL.Query().Get("include_archived")
	if q == "true" || q == "1" {
		return true
	}
	v, _ := s.GetDisplayShowArchived()
	return v
}

// resolveRepoFromPath pulls {prefix} from the URL, uppercases it, and looks
// up the repo. Writes a 404 envelope (or other statusForError) and returns
// ok=false on failure so handlers can early-return.
func resolveRepoFromPath(w http.ResponseWriter, r *http.Request, s *store.Store) (*model.Repo, bool) {
	prefix := strings.ToUpper(r.PathValue("prefix"))
	repo, err := s.GetRepoByPrefix(prefix)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return nil, false
	}
	return repo, true
}

// resolveIssueOnRepo parses {key} from the URL as PREFIX-N and looks up the
// issue. Cross-repo mismatch returns 404 (not 200, not 403) so we don't leak
// that the issue exists elsewhere.
func resolveIssueOnRepo(w http.ResponseWriter, r *http.Request, s *store.Store, repo *model.Repo) (*model.Issue, bool) {
	key := r.PathValue("key")
	prefix, num, err := store.ParseIssueKey(key)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "key"})
		return nil, false
	}
	iss, err := s.GetIssueByKey(prefix, num)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
			return nil, false
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return nil, false
	}
	if iss.RepoID != repo.ID {
		writeError(w, http.StatusNotFound, "not_found", "issue not found", nil)
		return nil, false
	}
	return iss, true
}

// resolveFeatureOnRepo pulls {slug} from the URL and looks up the feature
// scoped to the given repo. GetFeatureBySlug already filters by repo_id, so
// a slug that exists in another repo surfaces as 404 here.
func resolveFeatureOnRepo(w http.ResponseWriter, r *http.Request, s *store.Store, repo *model.Repo) (*model.Feature, bool) {
	slug := r.PathValue("slug")
	feat, err := s.GetFeatureBySlug(repo.ID, slug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
			return nil, false
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return nil, false
	}
	return feat, true
}

// resolveDocumentOnRepo pulls {filename} from the URL, validates it, and
// fetches the row scoped to repo. 404 on miss; 400 on a validation
// failure since the URL value can't satisfy any document at that point.
func resolveDocumentOnRepo(w http.ResponseWriter, r *http.Request, s *store.Store, repo *model.Repo, withContent bool) (*model.Document, bool) {
	name := r.PathValue("filename")
	clean, err := store.ValidateDocFilenameStrict(name)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "filename"})
		return nil, false
	}
	doc, err := s.GetDocumentByFilename(repo.ID, clean, withContent)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "document not found", nil)
			return nil, false
		}
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return nil, false
	}
	return doc, true
}
