package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// The CLI's filesystem-touching verbs (`bacio doc add --from-path`, `bacio doc
// export --to-path`) are deliberately not exposed: HTTP can't safely read
// the developer's working tree. Use POST /documents with inline content
// for create-from-path, and GET /documents/{filename}/download for export.

func (d deps) handleDocumentsList(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	q := r.URL.Query()
	withContent := q.Get("with_content") == "true" || q.Get("with_content") == "1"
	f := store.DocumentFilter{RepoID: repo.ID}
	if t := q.Get("type"); t != "" {
		dt, err := model.ParseDocumentType(t)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "type"})
			return
		}
		f.Type = &dt
	}
	docs, err := d.store.ListDocuments(f)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if docs == nil {
		docs = []*model.Document{}
	}
	if withContent {
		// ListDocuments deliberately drops content for the list shape; the
		// caller asked for it, so re-fetch each row's body. Mirrors the CLI's
		// `bacio doc list -o json` deliberate-lean default.
		for i, row := range docs {
			full, err := d.store.GetDocumentByID(row.ID, true)
			if err != nil {
				status, code := statusForError(err)
				writeError(w, status, code, err.Error(), nil)
				return
			}
			docs[i] = full
		}
	}
	writeJSON(w, http.StatusOK, docs)
}

func (d deps) handleDocumentShow(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	doc, ok := resolveDocumentOnRepo(w, r, d.store, repo, true)
	if !ok {
		return
	}
	q := r.URL.Query()
	if q.Get("with_content") == "false" || q.Get("with_content") == "0" {
		doc.Content = ""
	}
	links, err := d.store.ListDocumentLinks(doc.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if links == nil {
		links = []*model.DocumentLink{}
	}
	writeJSON(w, http.StatusOK, &DocView{Document: doc, Links: links})
}

func (d deps) handleDocumentCreate(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.DocAddInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	resolved, status, code, msg, field := resolveDocCreateInput(*in)
	if msg != "" {
		writeError(w, status, code, msg, fieldDetail(field))
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusCreated, &model.Document{
			RepoID:     repo.ID,
			Filename:   resolved.Filename,
			Type:       resolved.Type,
			SizeBytes:  int64(len(resolved.Body)),
			SourcePath: resolved.SourcePath,
		})
		return
	}
	doc, err := d.store.CreateDocument(repo.ID, resolved.Filename, resolved.Type, resolved.Body, resolved.SourcePath)
	if err != nil {
		if errors.Is(err, store.ErrDocumentExists) {
			writeError(w, http.StatusConflict, "conflict", err.Error(), nil)
			return
		}
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	doc.Content = "" // mirror CLI's `bacio doc add`: don't echo body on create.
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    ActorFromContext(r.Context()),
		Op:       "document.create",
		Kind:     "document",
		TargetID: &doc.ID, TargetLabel: doc.Filename,
		Details: "type=" + string(doc.Type),
	})
	writeJSON(w, http.StatusCreated, doc)
}

func (d deps) handleDocumentUpsert(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.DocAddInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	// URL filename always wins on PUT — overwrite whatever the body claimed.
	in.Filename = r.PathValue("filename")
	resolved, status, code, msg, field := resolveDocCreateInput(*in)
	if msg != "" {
		writeError(w, status, code, msg, fieldDetail(field))
		return
	}
	existing, err := d.store.GetDocumentByFilename(repo.ID, resolved.Filename, false)
	if errors.Is(err, store.ErrNotFound) {
		if isDryRun(r) {
			writeDryRun(w, http.StatusOK, &model.Document{
				RepoID:     repo.ID,
				Filename:   resolved.Filename,
				Type:       resolved.Type,
				SizeBytes:  int64(len(resolved.Body)),
				SourcePath: resolved.SourcePath,
			})
			return
		}
		doc, err := d.store.CreateDocument(repo.ID, resolved.Filename, resolved.Type, resolved.Body, resolved.SourcePath)
		if err != nil {
			s, c := statusForError(err)
			writeError(w, s, c, err.Error(), nil)
			return
		}
		recordOp(d.store, d.logger, model.HistoryEntry{
			RepoID: &repo.ID, RepoPrefix: repo.Prefix,
			Actor:    ActorFromContext(r.Context()),
			Op:       "document.create",
			Kind:     "document",
			TargetID: &doc.ID, TargetLabel: doc.Filename,
			Details: "type=" + string(doc.Type),
		})
		writeJSON(w, http.StatusOK, doc)
		return
	}
	if err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	var newType *model.DocumentType
	if resolved.Type != existing.Type {
		t := resolved.Type
		newType = &t
	}
	body := resolved.Body
	var newSource *string
	if resolved.SourcePath != "" && resolved.SourcePath != existing.SourcePath {
		sp := resolved.SourcePath
		newSource = &sp
	}
	if isDryRun(r) {
		projected := *existing
		projected.Type = resolved.Type
		projected.SizeBytes = int64(len(body))
		if newSource != nil {
			projected.SourcePath = *newSource
		}
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	if err := d.store.UpdateDocument(existing.ID, newType, &body, newSource); err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	updated, err := d.store.GetDocumentByID(existing.ID, false)
	if err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    ActorFromContext(r.Context()),
		Op:       "document.update",
		Kind:     "document",
		TargetID: &updated.ID, TargetLabel: updated.Filename,
		Details: updatedFieldList(map[string]bool{
			"type":        newType != nil,
			"content":     true,
			"source_path": newSource != nil,
		}),
	})
	writeJSON(w, http.StatusOK, updated)
}

func (d deps) handleDocumentEdit(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	doc, ok := resolveDocumentOnRepo(w, r, d.store, repo, false)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, present, err := inputio.DecodeStrict[inputs.DocEditInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	_ = in.Filename // URL filename always wins; ignore body
	var (
		newType    *model.DocumentType
		newContent *string
	)
	if _, ok := present["type"]; ok {
		if in.Type == nil || *in.Type == "" {
			writeError(w, http.StatusBadRequest, "invalid_input",
				"type cannot be empty or null; omit the field to leave it unchanged",
				map[string]any{"field": "type"})
			return
		}
		t, err := model.ParseDocumentType(*in.Type)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "type"})
			return
		}
		newType = &t
	}
	if _, ok := present["content"]; ok {
		body := ""
		if in.Content != nil {
			body = *in.Content
		}
		if !utf8.ValidString(body) {
			writeError(w, http.StatusBadRequest, "invalid_input",
				"document is not valid UTF-8 text; only text documents are supported",
				map[string]any{"field": "content"})
			return
		}
		newContent = &body
	}
	if newType == nil && newContent == nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "nothing to update", nil)
		return
	}
	if isDryRun(r) {
		projected := *doc
		if newType != nil {
			projected.Type = *newType
		}
		if newContent != nil {
			projected.SizeBytes = int64(len(*newContent))
		}
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	if err := d.store.UpdateDocument(doc.ID, newType, newContent, nil); err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	updated, err := d.store.GetDocumentByID(doc.ID, false)
	if err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    ActorFromContext(r.Context()),
		Op:       "document.update",
		Kind:     "document",
		TargetID: &updated.ID, TargetLabel: updated.Filename,
		Details: updatedFieldList(map[string]bool{
			"type":    newType != nil,
			"content": newContent != nil,
		}),
	})
	writeJSON(w, http.StatusOK, updated)
}

func (d deps) handleDocumentRename(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.DocRenameInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	in.OldFilename = r.PathValue("filename") // URL wins
	if in.NewFilename == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "new_filename is required", map[string]any{"field": "new_filename"})
		return
	}
	oldName, err := store.ValidateDocFilenameStrict(in.OldFilename)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "filename"})
		return
	}
	newName, err := store.ValidateDocFilenameStrict(in.NewFilename)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "new_filename"})
		return
	}
	if oldName == newName && in.Type == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "nothing to rename: old and new filenames are identical", nil)
		return
	}
	var newType *model.DocumentType
	if in.Type != "" {
		t, err := model.ParseDocumentType(in.Type)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "type"})
			return
		}
		newType = &t
	}
	doc, err := d.store.GetDocumentByFilename(repo.ID, oldName, false)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "document not found", nil)
			return
		}
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	if isDryRun(r) {
		projected := *doc
		projected.Filename = newName
		if newType != nil {
			projected.Type = *newType
		}
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	if err := d.store.RenameDocument(doc.ID, newName, newType); err != nil {
		if errors.Is(err, store.ErrDocumentExists) {
			writeError(w, http.StatusConflict, "conflict", err.Error(), nil)
			return
		}
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	updated, err := d.store.GetDocumentByID(doc.ID, false)
	if err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	details := oldName + " → " + newName
	if newType != nil {
		details += " type=" + string(*newType)
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    ActorFromContext(r.Context()),
		Op:       "document.rename",
		Kind:     "document",
		TargetID: &updated.ID, TargetLabel: updated.Filename,
		Details: details,
	})
	writeJSON(w, http.StatusOK, updated)
}

// handleDocumentDownload is the only non-JSON response in the API. It
// streams the document body so `curl -O` can save a file directly. No
// audit row, no dry-run — read-only.
func (d deps) handleDocumentDownload(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	doc, ok := resolveDocumentOnRepo(w, r, d.store, repo, true)
	if !ok {
		return
	}
	body := []byte(doc.Content)
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	// RFC 5987 filename* is unicode-safe even if the filename contains
	// characters a quoted ASCII filename can't represent. Pair with the
	// quoted ASCII fallback for clients that don't understand filename*.
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename=%q; filename*=UTF-8''%s`,
			doc.Filename, url.PathEscape(doc.Filename)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (d deps) handleDocumentDelete(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	doc, ok := resolveDocumentOnRepo(w, r, d.store, repo, false)
	if !ok {
		return
	}
	if isDryRun(r) {
		preview, err := buildDocumentDeletePreview(d.store, doc)
		if err != nil {
			s, c := statusForError(err)
			writeError(w, s, c, err.Error(), nil)
			return
		}
		writeDryRun(w, http.StatusOK, preview)
		return
	}
	if err := d.store.DeleteDocument(doc.ID); err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    ActorFromContext(r.Context()),
		Op:       "document.delete",
		Kind:     "document",
		TargetID: &doc.ID, TargetLabel: doc.Filename,
		Details: "type=" + string(doc.Type),
	})
	w.WriteHeader(http.StatusNoContent)
}

// buildDocumentDeletePreview counts the issue/feature link rows that would
// cascade-delete with the document. Mirrors `bacio doc rm --dry-run` but splits
// the link-row count into issue vs feature so consumers can see the shape.
func buildDocumentDeletePreview(s *store.Store, doc *model.Document) (*DocumentDeletePreview, error) {
	links, err := s.ListDocumentLinks(doc.ID)
	if err != nil {
		return nil, err
	}
	var issues, features int
	for _, l := range links {
		switch {
		case l.IssueID != nil:
			issues++
		case l.FeatureID != nil:
			features++
		}
	}
	return &DocumentDeletePreview{
		Document:    doc,
		WouldDelete: true,
		Cascade: DocumentCascadeCount{
			IssueLinks:   issues,
			FeatureLinks: features,
		},
	}, nil
}

func (d deps) handleDocumentLink(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	doc, ok := resolveDocumentOnRepo(w, r, d.store, repo, false)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.DocLinkInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	in.Filename = doc.Filename // URL wins
	target, ref, status, code, msg, field := resolveDocLinkTarget(d.store, repo, in.IssueKey, in.FeatureSlug)
	if msg != "" {
		writeError(w, status, code, msg, fieldDetail(field))
		return
	}
	if isDryRun(r) {
		preview := &model.DocumentLink{
			DocumentID:       doc.ID,
			DocumentFilename: doc.Filename,
			DocumentType:     doc.Type,
			Description:      in.Description,
			IssueID:          target.IssueID,
			FeatureID:        target.FeatureID,
		}
		writeDryRun(w, http.StatusCreated, preview)
		return
	}
	link, err := d.store.LinkDocument(doc.ID, target, in.Description)
	if err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    ActorFromContext(r.Context()),
		Op:       "document.link",
		Kind:     "document",
		TargetID: &doc.ID, TargetLabel: doc.Filename,
		Details: "→ " + ref,
	})
	writeJSON(w, http.StatusCreated, link)
}

func (d deps) handleDocumentUnlink(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	doc, ok := resolveDocumentOnRepo(w, r, d.store, repo, false)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.DocUnlinkInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	in.Filename = doc.Filename
	target, ref, status, code, msg, field := resolveDocLinkTarget(d.store, repo, in.IssueKey, in.FeatureSlug)
	if msg != "" {
		writeError(w, status, code, msg, fieldDetail(field))
		return
	}
	if isDryRun(r) {
		links, err := d.store.ListDocumentLinks(doc.ID)
		if err != nil {
			s, c := statusForError(err)
			writeError(w, s, c, err.Error(), nil)
			return
		}
		matched := 0
		for _, l := range links {
			if target.IssueID != nil && l.IssueID != nil && *l.IssueID == *target.IssueID {
				matched++
			}
			if target.FeatureID != nil && l.FeatureID != nil && *l.FeatureID == *target.FeatureID {
				matched++
			}
		}
		writeDryRun(w, http.StatusOK, &DocumentUnlinkPreview{
			Filename:    doc.Filename,
			Target:      ref,
			WouldRemove: matched,
		})
		return
	}
	n, err := d.store.UnlinkDocument(doc.ID, target)
	if err != nil {
		s, c := statusForError(err)
		writeError(w, s, c, err.Error(), nil)
		return
	}
	if n > 0 {
		recordOp(d.store, d.logger, model.HistoryEntry{
			RepoID: &repo.ID, RepoPrefix: repo.Prefix,
			Actor:    ActorFromContext(r.Context()),
			Op:       "document.unlink",
			Kind:     "document",
			TargetID: &doc.ID, TargetLabel: doc.Filename,
			Details: "↛ " + ref,
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// resolveDocLinkTarget validates that exactly one of issue_key/feature_slug
// is provided and resolves it within repo. Returns the resolved LinkTarget,
// the human-readable label used in audit details, and an HTTP error tuple
// (status/code/msg/field). msg is non-empty on failure.
func resolveDocLinkTarget(s *store.Store, repo *model.Repo, issueKey, featureSlug string) (store.LinkTarget, string, int, string, string, string) {
	switch {
	case issueKey != "" && featureSlug != "":
		return store.LinkTarget{}, "", http.StatusBadRequest, "invalid_input",
			"provide issue_key OR feature_slug, not both", ""
	case issueKey == "" && featureSlug == "":
		return store.LinkTarget{}, "", http.StatusBadRequest, "invalid_input",
			"issue_key or feature_slug is required", ""
	case issueKey != "":
		prefix, num, err := store.ParseIssueKey(issueKey)
		if err != nil {
			return store.LinkTarget{}, "", http.StatusBadRequest, "invalid_input", err.Error(), "issue_key"
		}
		iss, err := s.GetIssueByKey(prefix, num)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return store.LinkTarget{}, "", http.StatusNotFound, "not_found", err.Error(), "issue_key"
			}
			st, c := statusForError(err)
			return store.LinkTarget{}, "", st, c, err.Error(), "issue_key"
		}
		if iss.RepoID != repo.ID {
			return store.LinkTarget{}, "", http.StatusNotFound, "not_found", "issue not in this repo", "issue_key"
		}
		return store.LinkTarget{IssueID: &iss.ID}, iss.Key, 0, "", "", ""
	default:
		feat, err := s.GetFeatureBySlug(repo.ID, featureSlug)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return store.LinkTarget{}, "", http.StatusNotFound, "not_found", err.Error(), "feature_slug"
			}
			st, c := statusForError(err)
			return store.LinkTarget{}, "", st, c, err.Error(), "feature_slug"
		}
		return store.LinkTarget{FeatureID: &feat.ID}, "feature/" + feat.Slug, 0, "", "", ""
	}
}

// resolvedDocCreate is the validated tuple that document create/upsert
// hand off to the store.
type resolvedDocCreate struct {
	Filename   string
	Type       model.DocumentType
	Body       string
	SourcePath string
}

// resolveDocCreateInput validates the JSON payload for create/upsert. Returns
// (resolved, status, code, msg, field). msg is non-empty on failure; the
// caller writes the error envelope so this helper stays HTTP-agnostic.
func resolveDocCreateInput(in inputs.DocAddInput) (*resolvedDocCreate, int, string, string, string) {
	if in.Filename == "" {
		return nil, http.StatusBadRequest, "invalid_input", "filename is required", "filename"
	}
	clean, err := store.ValidateDocFilenameStrict(in.Filename)
	if err != nil {
		return nil, http.StatusBadRequest, "invalid_input", err.Error(), "filename"
	}
	if in.Type == "" {
		return nil, http.StatusBadRequest, "invalid_input", "type is required", "type"
	}
	t, err := model.ParseDocumentType(in.Type)
	if err != nil {
		return nil, http.StatusBadRequest, "invalid_input", err.Error(), "type"
	}
	if in.Content == "" {
		return nil, http.StatusBadRequest, "invalid_input", "content is required", "content"
	}
	if !utf8.ValidString(in.Content) {
		return nil, http.StatusBadRequest, "invalid_input", "document is not valid UTF-8 text; only text documents are supported", "content"
	}
	return &resolvedDocCreate{
		Filename:   clean,
		Type:       t,
		Body:       in.Content,
		SourcePath: in.SourcePath,
	}, 0, "", "", ""
}

func fieldDetail(field string) map[string]any {
	if field == "" {
		return nil
	}
	return map[string]any{"field": field}
}

