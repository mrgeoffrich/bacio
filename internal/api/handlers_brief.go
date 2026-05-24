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
			inflight, ierr := d.store.InflightByModeForRepo(repo.ID)
			if ierr != nil {
				inflight = map[model.DispatchMode]int{}
			}
			templates, terr := d.store.ListPromptTemplates()
			if terr != nil {
				templates = nil
			}
			waitingState = boardcards.DeriveWaitingState(iss, activeDispatch, inflight, templates)
		}
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
		Warnings:     warnings,
	})
}

// collectBriefDocs is kept in sync with internal/client/local_issue.go's
// (*localClient).collectBriefDocs. Issue links come first; feature links
// append to existing entries (extending linked_via) or create new ones.
// When both link rows have differing --why descriptions, the issue's wins
// and a warning is appended so nothing is silently dropped.
//
// After BACI-115 the body is inlined only for `plan` and `review` doc
// types; transcripts and everything else carry SizeBytes + metadata
// with an empty Content string.
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

// briefDocContent applies the BACI-115 inlining rule: a doc's body is
// inlined in the brief only if its type is `plan` or `review`. Mirrors
// the client-side briefDocContent in internal/client/local_issue.go so
// both code paths produce identical bodies. The transcript-filename
// fallback catches `attach_transcript` docs created before the
// `transcript` type existed (still typed `project_complete`).
func briefDocContent(d *model.Document) string {
	if d == nil {
		return ""
	}
	if model.IsBacioTranscriptFilename(d.Filename) {
		return ""
	}
	if !model.DocTypeInlinedInBrief(d.Type) {
		return ""
	}
	return d.Content
}
