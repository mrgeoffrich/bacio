package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func (d deps) handleIssuesList(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	q := r.URL.Query()
	withDesc := q.Get("with_description")
	f := store.IssueFilter{
		RepoID:             &repo.ID,
		IncludeDescription: withDesc == "true" || withDesc == "1",
		IncludeArchived:    includeArchivedFromRequest(r, d.store),
	}
	// BACI-177: filter out issues whose feature has been hidden on the
	// board via the per-feature toggle. Loaded once per request from the
	// per-repo tui_settings KV. The board list endpoint is the canonical
	// consumer; the per-feature `?feature=` path below short-circuits
	// the filter since the user explicitly asked for that feature.
	if q.Get("feature") == "" {
		hidden, herr := d.store.LoadHiddenFeatures(repo.ID)
		if herr == nil && len(hidden) > 0 {
			slugs := make([]string, 0, len(hidden))
			for slug, on := range hidden {
				if on {
					slugs = append(slugs, slug)
				}
			}
			f.HiddenFeatureSlugs = slugs
		}
	}
	if featureSlug := q.Get("feature"); featureSlug != "" {
		feat, err := d.store.GetFeatureBySlug(repo.ID, featureSlug)
		if err != nil {
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), map[string]any{"field": "feature"})
			return
		}
		f.FeatureID = &feat.ID
	}
	if stateCSV := q.Get("state"); stateCSV != "" {
		for _, raw := range strings.Split(stateCSV, ",") {
			st, err := model.ParseState(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "state"})
				return
			}
			f.States = append(f.States, st)
		}
	}
	if tagCSV := q.Get("tag"); tagCSV != "" {
		clean, err := store.NormalizeTags(strings.Split(tagCSV, ","))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "tag"})
			return
		}
		f.Tags = clean
	}
	issues, err := d.store.ListIssues(f)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if issues == nil {
		issues = []*model.Issue{}
	}
	writeJSON(w, http.StatusOK, issues)
}

func (d deps) handleIssueShow(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	comments, err := d.store.ListComments(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if comments == nil {
		comments = []*model.Comment{}
	}
	rels, err := d.store.ListIssueRelations(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
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
	docs, err := d.store.ListDocumentsLinkedToIssue(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if docs == nil {
		docs = []*model.DocumentLink{}
	}
	claimants, err := d.store.ListClaimsForIssue(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if claimants == nil {
		claimants = []*model.AgentClaim{}
	}
	// BACI-216: surface the newest linked plan as a dedicated field so
	// the workspace can render a prominent "Open plan" link without
	// re-scanning Documents for a `plan`-typed entry.
	latestPlan, err := d.store.LatestPlanForIssue(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, &IssueView{
		Issue:        iss,
		Comments:     comments,
		Relations:    rels,
		PullRequests: prs,
		Documents:    docs,
		Claimants:    claimants,
		Taken:        model.AnyOpenClaim(claimants),
		LatestPlan:   latestPlan,
	})
}

func (d deps) handleIssueCreate(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.IssueAddInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	if in.Title == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "title is required", map[string]any{"field": "title"})
		return
	}
	state := model.StateTodo
	if in.State != "" {
		st, err := model.ParseState(in.State)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "state"})
			return
		}
		state = st
	}
	var featureID *int64
	if in.FeatureSlug != "" {
		feat, err := d.store.GetFeatureBySlug(repo.ID, in.FeatureSlug)
		if err != nil {
			status, code := statusForError(err)
			writeError(w, status, code, fmt.Sprintf("feature %q: %v", in.FeatureSlug, err), map[string]any{"field": "feature_slug"})
			return
		}
		featureID = &feat.ID
	}
	cleanTags, err := store.NormalizeTags(in.Tags)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "tags"})
		return
	}
	if isDryRun(r) {
		projected := &model.Issue{
			RepoID:      repo.ID,
			Number:      repo.NextIssueNumber,
			Key:         fmt.Sprintf("%s-%d", repo.Prefix, repo.NextIssueNumber),
			FeatureID:   featureID,
			FeatureSlug: in.FeatureSlug,
			Title:       in.Title,
			Description: in.Description,
			State:       state,
			Tags:        cleanTags,
		}
		if projected.Tags == nil {
			projected.Tags = []string{}
		}
		writeDryRun(w, http.StatusCreated, projected)
		return
	}
	iss, err := d.store.CreateIssue(repo.ID, featureID, in.Title, in.Description, state, cleanTags)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Actor:    ActorFromContext(r.Context()),
		Op:       "issue.create",
		Kind:     "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: iss.Title,
	})
	writeJSON(w, http.StatusCreated, iss)
}

func (d deps) handleIssueState(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.IssueStateInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	if in.State == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "state is required", map[string]any{"field": "state"})
		return
	}
	st, err := model.ParseState(in.State)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), map[string]any{"field": "state"})
		return
	}
	if isDryRun(r) {
		projected := *iss
		projected.State = st
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	oldState := iss.State
	if err := d.store.SetIssueState(iss.ID, st); err != nil {
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
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID:      &iss.RepoID,
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "issue.state",
		Kind:        "issue",
		TargetID:    &updated.ID,
		TargetLabel: updated.Key,
		Details:     fmt.Sprintf("%s → %s", oldState, st),
	})
	writeJSON(w, http.StatusOK, updated)
}

func (d deps) handleIssueAssign(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, _, err := inputio.DecodeStrict[inputs.IssueAssignInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	name := strings.TrimSpace(in.Assignee)
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid_input",
			"assignee must be non-empty (use DELETE /issues/{key}/assignee to clear)",
			map[string]any{"field": "assignee"})
		return
	}
	if isDryRun(r) {
		projected := *iss
		projected.Assignee = name
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	old := iss.Assignee
	if err := d.store.SetIssueAssignee(iss.ID, name); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), map[string]any{"field": "assignee"})
		return
	}
	updated, err := d.store.GetIssueByID(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	details := "assigned to " + updated.Assignee
	if old != "" {
		details = fmt.Sprintf("%s → %s", old, updated.Assignee)
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID:      &iss.RepoID,
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "issue.assign",
		Kind:        "issue",
		TargetID:    &updated.ID,
		TargetLabel: updated.Key,
		Details:     details,
	})
	writeJSON(w, http.StatusOK, updated)
}

func (d deps) handleIssueEdit(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	raw, ok := readBody(r, w)
	if !ok {
		return
	}
	in, present, err := inputio.DecodeStrict[inputs.IssueEditInput](raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	var tPtr, dPtr *string
	var fPtr **int64
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
	if _, ok := present["feature_slug"]; ok {
		if in.FeatureSlug == nil || *in.FeatureSlug == "" {
			var none *int64
			fPtr = &none
		} else {
			feat, err := d.store.GetFeatureBySlug(iss.RepoID, *in.FeatureSlug)
			if err != nil {
				status, code := statusForError(err)
				writeError(w, status, code, fmt.Sprintf("feature %q: %v", *in.FeatureSlug, err), map[string]any{"field": "feature_slug"})
				return
			}
			p := &feat.ID
			fPtr = &p
		}
	}
	if tPtr == nil && dPtr == nil && fPtr == nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "nothing to update", nil)
		return
	}
	if isDryRun(r) {
		projected := *iss
		if tPtr != nil {
			projected.Title = *tPtr
		}
		if dPtr != nil {
			projected.Description = *dPtr
		}
		if fPtr != nil {
			projected.FeatureID = *fPtr
			if *fPtr == nil {
				projected.FeatureSlug = ""
			} else {
				feat, err := d.store.GetFeatureByID(**fPtr)
				if err != nil {
					status, code := statusForError(err)
					writeError(w, status, code, err.Error(), nil)
					return
				}
				projected.FeatureSlug = feat.Slug
			}
		}
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	if err := d.store.UpdateIssue(iss.ID, tPtr, dPtr, fPtr); err != nil {
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
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID:      &iss.RepoID,
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "issue.update",
		Kind:        "issue",
		TargetID:    &updated.ID,
		TargetLabel: updated.Key,
		Details: updatedFieldList(map[string]bool{
			"title":       tPtr != nil,
			"description": dPtr != nil,
			"feature":     fPtr != nil,
		}),
	})
	writeJSON(w, http.StatusOK, updated)
}

func (d deps) handleIssueDelete(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	if isDryRun(r) {
		preview, err := buildIssueDeletePreview(d.store, iss)
		if err != nil {
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), nil)
			return
		}
		writeDryRun(w, http.StatusOK, preview)
		return
	}
	if err := d.store.DeleteIssue(iss.ID); err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID:      &iss.RepoID,
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "issue.delete",
		Kind:        "issue",
		TargetID:    &iss.ID,
		TargetLabel: iss.Key,
		Details:     iss.Title,
	})
	w.WriteHeader(http.StatusNoContent)
}

// updatedFieldList mirrors internal/cli/audit.go:updatedFieldList exactly so
// audit messages from CLI and HTTP edits read identically. Copied verbatim
// per CLAUDE.md "no internal/api → internal/cli imports" rule.
func updatedFieldList(fields map[string]bool) string {
	var parts []string
	for name, touched := range fields {
		if touched {
			parts = append(parts, name)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "updated " + strings.Join(parts, ",")
}

// buildIssueDeletePreview is kept in sync with the local Client backend's
// DeleteIssue dry-run branch.
func buildIssueDeletePreview(s *store.Store, iss *model.Issue) (*IssueDeletePreview, error) {
	comments, err := s.ListComments(iss.ID)
	if err != nil {
		return nil, err
	}
	relations, err := s.ListIssueRelations(iss.ID)
	if err != nil {
		return nil, err
	}
	prs, err := s.ListPRs(iss.ID)
	if err != nil {
		return nil, err
	}
	docs, err := s.ListDocumentsLinkedToIssue(iss.ID)
	if err != nil {
		return nil, err
	}
	relCount := 0
	if relations != nil {
		relCount = len(relations.Outgoing) + len(relations.Incoming)
	}
	return &IssueDeletePreview{
		Issue:       iss,
		WouldDelete: true,
		Cascade: CascadeCount{
			Comments:      len(comments),
			Relations:     relCount,
			PullRequests:  len(prs),
			DocumentLinks: len(docs),
			Tags:          len(iss.Tags),
		},
	}, nil
}

func (d deps) handleIssueUnassign(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	if iss.Assignee == "" {
		// Matches CLI behaviour: no-op writes no audit row but still returns
		// the (unchanged) issue.
		writeJSON(w, http.StatusOK, iss)
		return
	}
	if isDryRun(r) {
		projected := *iss
		projected.Assignee = ""
		writeDryRun(w, http.StatusOK, &projected)
		return
	}
	old := iss.Assignee
	if err := d.store.SetIssueAssignee(iss.ID, ""); err != nil {
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
	recordOp(d.store, d.logger, model.HistoryEntry{
		RepoID:      &iss.RepoID,
		RepoPrefix:  repo.Prefix,
		Actor:       ActorFromContext(r.Context()),
		Op:          "issue.assign",
		Kind:        "issue",
		TargetID:    &updated.ID,
		TargetLabel: updated.Key,
		Details:     fmt.Sprintf("%s → (unassigned)", old),
	})
	writeJSON(w, http.StatusOK, updated)
}
