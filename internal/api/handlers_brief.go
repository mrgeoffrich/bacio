package api

import (
	"fmt"
	"net/http"

	"github.com/mrgeoffrich/bacio/internal/boardcards"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func (d deps) handleIssueBrief(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveRepoFromPath(w, r, d.store)
	if !ok {
		return
	}
	iss, ok := resolveIssueOnRepo(w, r, d.store, repo)
	if !ok {
		return
	}
	q := r.URL.Query()
	noFeatureDocs := q.Get("no_feature_docs") == "true" || q.Get("no_feature_docs") == "1"
	noComments := q.Get("no_comments") == "true" || q.Get("no_comments") == "1"
	noDocContent := q.Get("no_doc_content") == "true" || q.Get("no_doc_content") == "1"

	var feat *model.Feature
	if iss.FeatureID != nil {
		var err error
		feat, err = d.store.GetFeatureByID(*iss.FeatureID)
		if err != nil {
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), nil)
			return
		}
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

	docs, warnings, err := collectBriefDocs(d.store, iss.ID, feat, !noFeatureDocs)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}
	if noDocContent {
		for _, doc := range docs {
			doc.Content = ""
		}
	}

	var comments []*model.Comment
	if !noComments {
		comments, err = d.store.ListComments(iss.ID)
		if err != nil {
			status, code := statusForError(err)
			writeError(w, status, code, err.Error(), nil)
			return
		}
	}
	if comments == nil {
		comments = []*model.Comment{}
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

	// BACI-145: derive WaitingState for the IssueLockBanner. Best-
	// effort — a failure in any of the three reads degrades to a nil
	// WaitingState (the banner falls back to the unlabelled spinner
	// rather than the inline label), so brief read latency isn't
	// gated on the dispatch / templates tables being live.
	var waitingState *boardcards.WaitingState
	if iss.WaitingForClaim {
		activeDispatch, derr := d.store.WaitingDispatchForIssue(repo.ID, iss.ID)
		if derr == nil {
			// BACI-227: per-(mode, branch) in-flight grouping so the
			// IssueLockBanner's WaitingState tracks the matcher's
			// per-branch concurrency gate exactly.
			inflight, ierr := d.store.InflightByModeBaseForRepo(repo.ID)
			if ierr != nil {
				inflight = map[store.InflightKey]int{}
			}
			templates, terr := d.store.ListPromptTemplates()
			if terr != nil {
				templates = nil
			}
			waitingState = boardcards.DeriveWaitingState(iss, activeDispatch, inflight, templates)
		}
	}

	// BACI-216: same per-issue plan lookup as the show handler so the
	// brief consumer (skill / workspace shelf) sees the same plan
	// affordance the kanban card surfaces.
	latestPlan, err := d.store.LatestPlanForIssue(iss.ID)
	if err != nil {
		status, code := statusForError(err)
		writeError(w, status, code, err.Error(), nil)
		return
	}

	writeJSON(w, http.StatusOK, &IssueBrief{
		Issue:        iss,
		Feature:      feat,
		Relations:    rels,
		PullRequests: prs,
		Documents:    docs,
		Comments:     comments,
		Claimants:    claimants,
		Taken:        model.AnyOpenClaim(claimants),
		WaitingState: waitingState,
		LatestPlan:   latestPlan,
		// BACI-226: the resolver-derived base branch. Always non-empty
		// (fallback to "main") so brief consumers can read it without
		// an extra resolver pass.
		BaseBranch: model.ResolveBaseBranch(iss, feat),
		Warnings:   warnings,
	})
}

// collectBriefDocs is kept in sync with internal/client/local_issue.go's
// (*localClient).collectBriefDocs. Issue links come first; feature links
// append to existing entries (extending linked_via) or create new ones.
// When both link rows have differing --why descriptions, the issue's wins
// and a warning is appended so nothing is silently dropped.
//
// After BACI-203 NO linked-doc body is inlined — every doc surfaces
// metadata + SizeBytes only. The LinkedDocPanel renders a link into the
// canonical /documents/<filename> page rather than embedding the body
// inline. This narrows the previous BACI-115 rule (plan + review only)
// to "never" so the brief stays slim even for issues with large plan
// docs attached.
func collectBriefDocs(s *store.Store, issueID int64, feat *model.Feature, includeFeature bool) ([]*BriefDoc, []string, error) {
	warnings := []string{}
	out := []*BriefDoc{}
	byDocID := map[int64]*BriefDoc{}

	issueLinks, err := s.ListDocumentsLinkedToIssue(issueID)
	if err != nil {
		return nil, nil, err
	}
	for _, l := range issueLinks {
		doc, err := s.GetDocumentByID(l.DocumentID, true)
		if err != nil {
			return nil, nil, err
		}
		entry := &BriefDoc{
			Filename:    doc.Filename,
			Type:        doc.Type,
			Description: l.Description,
			SourcePath:  doc.SourcePath,
			LinkedVia:   []string{"issue"},
			SizeBytes:   doc.SizeBytes,
			Content:     briefDocContent(doc),
		}
		out = append(out, entry)
		byDocID[doc.ID] = entry
	}

	if includeFeature && feat != nil {
		featLinks, err := s.ListDocumentsLinkedToFeature(feat.ID)
		if err != nil {
			return nil, nil, err
		}
		via := "feature/" + feat.Slug
		for _, l := range featLinks {
			if existing, ok := byDocID[l.DocumentID]; ok {
				existing.LinkedVia = append(existing.LinkedVia, via)
				if l.Description != "" && l.Description != existing.Description {
					if existing.Description == "" {
						existing.Description = l.Description
					} else {
						warnings = append(warnings, fmt.Sprintf(
							"document %s: feature link description differs from issue link; using issue's. Feature said: %q",
							existing.Filename, l.Description))
					}
				}
				continue
			}
			doc, err := s.GetDocumentByID(l.DocumentID, true)
			if err != nil {
				return nil, nil, err
			}
			entry := &BriefDoc{
				Filename:    doc.Filename,
				Type:        doc.Type,
				Description: l.Description,
				SourcePath:  doc.SourcePath,
				LinkedVia:   []string{via},
				SizeBytes:   doc.SizeBytes,
				Content:     briefDocContent(doc),
			}
			out = append(out, entry)
			byDocID[doc.ID] = entry
		}
	}

	return out, warnings, nil
}

// briefDocContent applied the BACI-115 plan/review inlining rule
// historically. BACI-203 narrows it to "always empty": the linked-doc
// panel is now a link to the canonical /documents/<filename> page, so
// no caller in the React tree reads the inlined body. Kept as a named
// no-op so the call site reads as an intentional strip rather than an
// always-empty literal — the symbol also keeps `model` imported, and
// the symmetry with internal/client/local_issue.go (which keeps the
// same shape for the Wails-bound brief) makes the parity obvious.
func briefDocContent(_ *model.Document) string {
	return ""
}
