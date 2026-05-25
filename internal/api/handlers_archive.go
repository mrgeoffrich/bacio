// BACI-68 archive lifecycle HTTP handlers. Six per-entity verbs
// (issue/feature/document × archive/unarchive), one ad-hoc sweep
// trigger, and a get/set pair for the display.show_archived global
// toggle. The per-entity verbs follow the same shape as
// handleIssueState — resolve the row from the path, project the
// updated row on dry-run, audit the mutation on the real path.
package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// nowUTC matches the time.Now().UTC() pattern used throughout
// handlers_agent.go / handlers_questions.go — used by the dry-run
// projections of the archive verbs so the output carries a realistic
// timestamp instead of a zero value.
func nowUTC() time.Time { return time.Now().UTC() }

// projectArchivedAt mirrors the store's idempotent semantics for the
// archive dry-run projections (BACI-68 nit #10): re-archiving an
// already-archived row preserves the original timestamp, unarchiving
// an unarchived row is a no-op. Without this, an agent comparing
// dry-run output to a real call would see a phantom `now` timestamp
// that the real write never produces.
func projectArchivedAt(current *time.Time, archive bool) *time.Time {
	if !archive {
		return nil
	}
	if current != nil {
		return current
	}
	now := nowUTC()
	return &now
}

// ------------ issue archive / unarchive ------------

func (d deps) handleIssueArchive(w http.ResponseWriter, r *http.Request) {
	d.runIssueArchive(w, r, true)
}

func (d deps) handleIssueUnarchive(w http.ResponseWriter, r *http.Request) {
	d.runIssueArchive(w, r, false)
}

func (d deps) runIssueArchive(w http.ResponseWriter, r *http.Request, archive bool) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	if isDryRun(r) {
		projected := *iss
		projected.ArchivedAt = projectArchivedAt(iss.ArchivedAt, archive)
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	if err := d.store.SetIssueArchived(iss.ID, archive); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	updated, err := d.store.GetIssueByID(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	op := "issue.archive"
	if !archive {
		op = "issue.unarchive"
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		Actor:       ActorFromContext(r.Context()),
		RepoID:      &iss.RepoID,
		RepoPrefix:  repo.Prefix,
		Op:          op,
		Kind:        "issue",
		TargetID:    &updated.ID,
		TargetLabel: updated.Key,
	})
	writeJSON(w, http.StatusOK, updated)
}

// ------------ feature archive / unarchive ------------

func (d deps) handleFeatureArchive(w http.ResponseWriter, r *http.Request) {
	d.runFeatureArchive(w, r, true)
}

func (d deps) handleFeatureUnarchive(w http.ResponseWriter, r *http.Request) {
	d.runFeatureArchive(w, r, false)
}

func (d deps) runFeatureArchive(w http.ResponseWriter, r *http.Request, archive bool) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	slug := r.PathValue("slug")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "slug is required", map[string]any{"field": "slug"})
		return
	}
	feat, err := d.store.GetFeatureBySlug(repo.ID, slug)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), map[string]any{"field": "slug"})
		return
	}
	if isDryRun(r) {
		projected := *feat
		projected.ArchivedAt = projectArchivedAt(feat.ArchivedAt, archive)
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	if err := d.store.SetFeatureArchived(feat.ID, archive); err != nil {
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
	op := "feature.archive"
	if !archive {
		op = "feature.unarchive"
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		Actor:       ActorFromContext(r.Context()),
		RepoID:      &feat.RepoID,
		RepoPrefix:  repo.Prefix,
		Op:          op,
		Kind:        "feature",
		TargetID:    &updated.ID,
		TargetLabel: updated.Slug,
	})
	writeJSON(w, http.StatusOK, updated)
}

// ------------ document archive / unarchive ------------

func (d deps) handleDocumentArchive(w http.ResponseWriter, r *http.Request) {
	d.runDocumentArchive(w, r, true)
}

func (d deps) handleDocumentUnarchive(w http.ResponseWriter, r *http.Request) {
	d.runDocumentArchive(w, r, false)
}

func (d deps) runDocumentArchive(w http.ResponseWriter, r *http.Request, archive bool) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	filename := r.PathValue("filename")
	if filename == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "filename is required", map[string]any{"field": "filename"})
		return
	}
	doc, err := d.store.GetDocumentByFilename(repo.ID, filename, false)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), map[string]any{"field": "filename"})
		return
	}
	if isDryRun(r) {
		projected := *doc
		projected.ArchivedAt = projectArchivedAt(doc.ArchivedAt, archive)
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	if err := d.store.SetDocumentArchived(doc.ID, archive); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	updated, err := d.store.GetDocumentByID(doc.ID, false)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	op := "doc.archive"
	if !archive {
		op = "doc.unarchive"
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		Actor:       ActorFromContext(r.Context()),
		RepoID:      &doc.RepoID,
		RepoPrefix:  repo.Prefix,
		Op:          op,
		Kind:        "document",
		TargetID:    &updated.ID,
		TargetLabel: updated.Filename,
	})
	writeJSON(w, http.StatusOK, updated)
}

// ------------ ad-hoc sweep trigger ------------

func (d deps) handleArchiveSweep(w http.ResponseWriter, r *http.Request) {
	dryRun := isDryRun(r)
	res, err := d.store.ArchiveSweep(dryRun)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if dryRun {
		// Counts reflect what a real sweep would have archived; the tx
		// was rolled back so no rows changed and no audit row fires.
		writeDryRun(w, http.StatusOK, &res)
		return
	}
	if res.Total() > 0 {
		recordOp(d.store, d.logger, model.HistoryEntry{
			Actor:   ActorFromContext(r.Context()),
			Op:      "archive.sweep",
			Kind:    "sweep",
			Details: fmt.Sprintf(`{"issues":%d,"features":%d,"documents":%d}`, res.IssuesArchived, res.FeaturesArchived, res.DocumentsArchived),
		})
	}
	writeJSON(w, http.StatusOK, &res)
}

// ------------ display.show_archived toggle ------------

// DisplayPreferencesOut is the response shape for
// /settings/display-preferences. Underscore on the wire to match every
// other JSON field in this package.
type DisplayPreferencesOut struct {
	ShowArchived bool `json:"show_archived"`
}

// displayPreferencesIn is the strict-decoded input for PUT.
type displayPreferencesIn struct {
	ShowArchived bool `json:"show_archived"`
}

func (d deps) handleDisplayPreferencesGet(w http.ResponseWriter, r *http.Request) {
	v, err := d.store.GetDisplayShowArchived()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, &DisplayPreferencesOut{ShowArchived: v})
}

func (d deps) handleDisplayPreferencesSet(w http.ResponseWriter, r *http.Request) {
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	parsed, _, err := inputio.DecodeStrict[displayPreferencesIn](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, &DisplayPreferencesOut{ShowArchived: parsed.ShowArchived})
		return
	}
	if err := d.store.SetDisplayShowArchived(parsed.ShowArchived); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		Actor:       ActorFromContext(r.Context()),
		Op:          "display.update",
		Kind:        "app_setting",
		TargetLabel: "display.show_archived",
		Details:     fmt.Sprintf("show_archived=%t", parsed.ShowArchived),
	})
	writeJSON(w, http.StatusOK, &DisplayPreferencesOut{ShowArchived: parsed.ShowArchived})
}

// ------------ BACI-162 archive.auto_enabled + retention_days ------------

// ArchivePreferencesOut is the response shape for
// /settings/archive-preferences. Underscore on the wire to match
// every other JSON field in this package.
type ArchivePreferencesOut struct {
	AutoEnabled   bool `json:"auto_enabled"`
	RetentionDays int  `json:"retention_days"`
}

// archivePreferencesIn is the strict-decoded input for PUT. Both
// fields are required — the handler writes the pair atomically
// rather than juggling a partial update; the meaning of one field
// depends on the other (auto_enabled gates whether retention_days
// matters at all).
type archivePreferencesIn struct {
	AutoEnabled   bool `json:"auto_enabled"`
	RetentionDays int  `json:"retention_days"`
}

func (d deps) handleArchivePreferencesGet(w http.ResponseWriter, r *http.Request) {
	autoEnabled, err := d.store.GetArchiveAutoEnabled()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	retention, err := d.store.GetArchiveRetentionDays()
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, &ArchivePreferencesOut{
		AutoEnabled:   autoEnabled,
		RetentionDays: retention,
	})
}

func (d deps) handleArchivePreferencesSet(w http.ResponseWriter, r *http.Request) {
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	parsed, _, err := inputio.DecodeStrict[archivePreferencesIn](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	retention, err := store.ValidateArchiveRetentionDays(parsed.RetentionDays)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	out := &ArchivePreferencesOut{AutoEnabled: parsed.AutoEnabled, RetentionDays: retention}
	if isDryRun(r) {
		writeDryRun(w, http.StatusOK, out)
		return
	}
	if err := d.store.SetArchiveAutoEnabled(parsed.AutoEnabled); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if err := d.store.SetArchiveRetentionDays(retention); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		Actor:       ActorFromContext(r.Context()),
		Op:          "archive.update",
		Kind:        "app_setting",
		TargetLabel: "archive.preferences",
		Details:     fmt.Sprintf("auto_enabled=%t retention_days=%d", parsed.AutoEnabled, retention),
	})
	writeJSON(w, http.StatusOK, out)
}
