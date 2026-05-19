package client

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// ResolveIssueKey accepts canonical PREFIX-N or a bare number (resolved
// against repo). Returns the canonical key.
func (c *localClient) ResolveIssueKey(ctx context.Context, repo *model.Repo, key string) (string, error) {
	key = strings.TrimSpace(key)
	if strings.Contains(key, "-") {
		prefix, num, err := store.ParseIssueKey(key)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s-%d", prefix, num), nil
	}
	if repo == nil {
		return "", fmt.Errorf("bare issue number %q requires a current repo", key)
	}
	var n int64
	if _, err := fmt.Sscanf(key, "%d", &n); err != nil {
		return "", fmt.Errorf("invalid issue reference %q", key)
	}
	return fmt.Sprintf("%s-%d", repo.Prefix, n), nil
}

func (c *localClient) ListIssues(ctx context.Context, f IssueFilter) ([]*model.Issue, error) {
	sf := store.IssueFilter{
		AllRepos:           f.AllRepos,
		IncludeDescription: f.IncludeDescription,
		States:             f.States,
		Tags:               f.Tags,
		IncludeArchived:    f.IncludeArchived,
	}
	if !f.AllRepos && f.Repo != nil {
		sf.RepoID = &f.Repo.ID
		if f.FeatureSlug != "" {
			feat, err := c.store.GetFeatureBySlug(f.Repo.ID, f.FeatureSlug)
			if err != nil {
				return nil, fmt.Errorf("feature %q: %w", f.FeatureSlug, err)
			}
			sf.FeatureID = &feat.ID
		}
	}
	issues, err := c.store.ListIssues(sf)
	if err != nil {
		return nil, err
	}
	if issues == nil {
		issues = []*model.Issue{}
	}
	return issues, nil
}

// ArchiveIssue stamps the issue's archived_at column to CURRENT_TIMESTAMP
// (BACI-68). Idempotent — archiving an already-archived row is a no-op
// (the partial-WHERE in SetIssueArchived skips it). Sticky — reopening
// an archived issue (via `bacio issue state`) does NOT auto-unarchive
// it; the user must unarchive explicitly. Records an audit row under
// the actor on --user.
func (c *localClient) ArchiveIssue(ctx context.Context, repo *model.Repo, key string, dryRun bool) (*model.Issue, error) {
	return c.setIssueArchived(ctx, repo, key, true, dryRun, "issue.archive")
}

// UnarchiveIssue clears archived_at (BACI-68). Records an audit row.
func (c *localClient) UnarchiveIssue(ctx context.Context, repo *model.Repo, key string, dryRun bool) (*model.Issue, error) {
	return c.setIssueArchived(ctx, repo, key, false, dryRun, "issue.unarchive")
}

func (c *localClient) setIssueArchived(ctx context.Context, repo *model.Repo, key string, archived, dryRun bool, op string) (*model.Issue, error) {
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	if dryRun {
		projected := *iss
		// Mirror the store's idempotent semantics: re-archiving preserves
		// the original timestamp, and unarchiving an unarchived row is a
		// no-op. Otherwise an agent comparing dry-run output to a real
		// call would see a phantom "now" timestamp that the real write
		// never produces.
		if archived && iss.ArchivedAt == nil {
			now := time.Now().UTC()
			projected.ArchivedAt = &now
		} else if !archived {
			projected.ArchivedAt = nil
		}
		return &projected, nil
	}
	if err := c.store.SetIssueArchived(iss.ID, archived); err != nil {
		return nil, err
	}
	updated, err := c.store.GetIssueByID(iss.ID)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Op: op, Kind: "issue",
		TargetID: &updated.ID, TargetLabel: updated.Key,
	})
	return updated, nil
}

func (c *localClient) GetIssueByKey(ctx context.Context, repo *model.Repo, key string) (*model.Issue, error) {
	canonical, err := c.ResolveIssueKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	prefix, num, err := store.ParseIssueKey(canonical)
	if err != nil {
		return nil, err
	}
	return c.store.GetIssueByKey(prefix, num)
}

func (c *localClient) ShowIssue(ctx context.Context, repo *model.Repo, key string) (*IssueView, error) {
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	comments, err := c.store.ListComments(iss.ID)
	if err != nil {
		return nil, err
	}
	if comments == nil {
		comments = []*model.Comment{}
	}
	rels, err := c.store.ListIssueRelations(iss.ID)
	if err != nil {
		return nil, err
	}
	prs, err := c.store.ListPRs(iss.ID)
	if err != nil {
		return nil, err
	}
	if prs == nil {
		prs = []*model.PullRequest{}
	}
	docs, err := c.store.ListDocumentsLinkedToIssue(iss.ID)
	if err != nil {
		return nil, err
	}
	if docs == nil {
		docs = []*model.DocumentLink{}
	}
	claimants, taken, err := c.issueClaimants(iss.ID)
	if err != nil {
		return nil, err
	}
	return &IssueView{
		Issue: iss, Comments: comments, Relations: rels,
		PullRequests: prs, Documents: docs,
		Claimants: claimants, Taken: taken,
	}, nil
}

// issueClaimants returns the per-issue agent-claim history (open +
// released, newest first) and the derived `taken` signal — true iff at
// least one claim is still open. taken is never stored; the open claim
// rows are the single source of truth.
func (c *localClient) issueClaimants(issueID int64) ([]*model.AgentClaim, bool, error) {
	claimants, err := c.store.ListClaimsForIssue(issueID)
	if err != nil {
		return nil, false, err
	}
	if claimants == nil {
		claimants = []*model.AgentClaim{}
	}
	return claimants, model.AnyOpenClaim(claimants), nil
}

func (c *localClient) BriefIssue(ctx context.Context, repo *model.Repo, key string, opts BriefOptions) (*IssueBrief, error) {
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	var feat *model.Feature
	if iss.FeatureID != nil {
		feat, err = c.store.GetFeatureByID(*iss.FeatureID)
		if err != nil {
			return nil, err
		}
	}
	rels, err := c.store.ListIssueRelations(iss.ID)
	if err != nil {
		return nil, err
	}
	prs, err := c.store.ListPRs(iss.ID)
	if err != nil {
		return nil, err
	}
	if prs == nil {
		prs = []*model.PullRequest{}
	}
	docs, warnings, err := c.collectBriefDocs(iss.ID, feat, !opts.NoFeatureDocs)
	if err != nil {
		return nil, err
	}
	if opts.NoDocContent {
		for _, d := range docs {
			d.Content = ""
		}
	}
	var comments []*model.Comment
	if !opts.NoComments {
		comments, err = c.store.ListComments(iss.ID)
		if err != nil {
			return nil, err
		}
	}
	if comments == nil {
		comments = []*model.Comment{}
	}
	claimants, taken, err := c.issueClaimants(iss.ID)
	if err != nil {
		return nil, err
	}
	return &IssueBrief{
		Issue:        iss,
		Feature:      feat,
		Relations:    rels,
		PullRequests: prs,
		Documents:    docs,
		Comments:     comments,
		Claimants:    claimants,
		Taken:        taken,
		Warnings:     warnings,
	}, nil
}

func (c *localClient) collectBriefDocs(issueID int64, feat *model.Feature, includeFeature bool) ([]*BriefDoc, []string, error) {
	warnings := []string{}
	out := []*BriefDoc{}
	byDocID := map[int64]*BriefDoc{}

	issueLinks, err := c.store.ListDocumentsLinkedToIssue(issueID)
	if err != nil {
		return nil, nil, err
	}
	for _, l := range issueLinks {
		d, err := c.store.GetDocumentByID(l.DocumentID, true)
		if err != nil {
			return nil, nil, err
		}
		entry := &BriefDoc{
			Filename:    d.Filename,
			Type:        d.Type,
			Description: l.Description,
			SourcePath:  d.SourcePath,
			LinkedVia:   []string{"issue"},
			Content:     d.Content,
		}
		out = append(out, entry)
		byDocID[d.ID] = entry
	}
	if includeFeature && feat != nil {
		featLinks, err := c.store.ListDocumentsLinkedToFeature(feat.ID)
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
			d, err := c.store.GetDocumentByID(l.DocumentID, true)
			if err != nil {
				return nil, nil, err
			}
			entry := &BriefDoc{
				Filename:    d.Filename,
				Type:        d.Type,
				Description: l.Description,
				SourcePath:  d.SourcePath,
				LinkedVia:   []string{via},
				Content:     d.Content,
			}
			out = append(out, entry)
			byDocID[d.ID] = entry
		}
	}
	return out, warnings, nil
}

func (c *localClient) CreateIssue(ctx context.Context, repo *model.Repo, in inputs.IssueAddInput, dryRun bool) (*model.Issue, error) {
	if in.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	state := model.StateTodo
	if in.State != "" {
		st, err := model.ParseState(in.State)
		if err != nil {
			return nil, err
		}
		state = st
	}
	cleanTags, err := store.NormalizeTags(in.Tags)
	if err != nil {
		return nil, err
	}
	var featureID *int64
	if in.FeatureSlug != "" {
		feat, err := c.store.GetFeatureBySlug(repo.ID, in.FeatureSlug)
		if err != nil {
			return nil, fmt.Errorf("feature %q: %w", in.FeatureSlug, err)
		}
		featureID = &feat.ID
	}
	if dryRun {
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
		return projected, nil
	}
	iss, err := c.store.CreateIssue(repo.ID, featureID, in.Title, in.Description, state, cleanTags)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "issue.create", Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: iss.Title,
	})
	return iss, nil
}

func (c *localClient) UpdateIssue(ctx context.Context, repo *model.Repo, key string, edit IssueEdit, dryRun bool) (*model.Issue, error) {
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	if edit.Title == nil && edit.Description == nil && edit.FeatureID == nil {
		return nil, fmt.Errorf("nothing to update")
	}
	if dryRun {
		projected := *iss
		if edit.Title != nil {
			projected.Title = *edit.Title
		}
		if edit.Description != nil {
			projected.Description = *edit.Description
		}
		if edit.FeatureID != nil {
			projected.FeatureID = *edit.FeatureID
			if *edit.FeatureID == nil {
				projected.FeatureSlug = ""
			} else {
				feat, err := c.store.GetFeatureByID(**edit.FeatureID)
				if err != nil {
					return nil, err
				}
				projected.FeatureSlug = feat.Slug
			}
		}
		return &projected, nil
	}
	if err := c.store.UpdateIssue(iss.ID, edit.Title, edit.Description, edit.FeatureID); err != nil {
		return nil, err
	}
	updated, err := c.store.GetIssueByID(iss.ID)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Op: "issue.update", Kind: "issue",
		TargetID: &updated.ID, TargetLabel: updated.Key,
		Details: updatedFieldList(map[string]bool{
			"title":       edit.Title != nil,
			"description": edit.Description != nil,
			"feature":     edit.FeatureID != nil,
		}),
	})
	return updated, nil
}

func (c *localClient) SetIssueState(ctx context.Context, repo *model.Repo, key string, state model.State, dryRun bool) (*model.Issue, error) {
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	if dryRun {
		projected := *iss
		projected.State = state
		return &projected, nil
	}
	oldState := iss.State
	if err := c.store.SetIssueState(iss.ID, state); err != nil {
		return nil, err
	}
	updated, err := c.store.GetIssueByID(iss.ID)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Op: "issue.state", Kind: "issue",
		TargetID: &updated.ID, TargetLabel: updated.Key,
		Details: fmt.Sprintf("%s → %s", oldState, state),
	})
	return updated, nil
}

func (c *localClient) AssignIssue(ctx context.Context, repo *model.Repo, key, assignee string, dryRun bool) (*model.Issue, error) {
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return nil, fmt.Errorf("assignee name must be non-empty (use `bacio issue unassign` to clear)")
	}
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	if dryRun {
		projected := *iss
		projected.Assignee = assignee
		return &projected, nil
	}
	old := iss.Assignee
	if err := c.store.SetIssueAssignee(iss.ID, assignee); err != nil {
		return nil, err
	}
	updated, err := c.store.GetIssueByID(iss.ID)
	if err != nil {
		return nil, err
	}
	details := "assigned to " + updated.Assignee
	if old != "" {
		details = fmt.Sprintf("%s → %s", old, updated.Assignee)
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Op: "issue.assign", Kind: "issue",
		TargetID: &updated.ID, TargetLabel: updated.Key,
		Details: details,
	})
	return updated, nil
}

func (c *localClient) UnassignIssue(ctx context.Context, repo *model.Repo, key string, dryRun bool) (*model.Issue, error) {
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, err
	}
	if iss.Assignee == "" {
		return iss, nil
	}
	if dryRun {
		projected := *iss
		projected.Assignee = ""
		return &projected, nil
	}
	old := iss.Assignee
	if err := c.store.SetIssueAssignee(iss.ID, ""); err != nil {
		return nil, err
	}
	updated, err := c.store.GetIssueByID(iss.ID)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Op: "issue.assign", Kind: "issue",
		TargetID: &updated.ID, TargetLabel: updated.Key,
		Details: fmt.Sprintf("%s → (unassigned)", old),
	})
	return updated, nil
}

func (c *localClient) DeleteIssue(ctx context.Context, repo *model.Repo, key string, dryRun bool) (*model.Issue, *IssueDeletePreview, error) {
	iss, err := c.GetIssueByKey(ctx, repo, key)
	if err != nil {
		return nil, nil, err
	}
	if dryRun {
		comments, err := c.store.ListComments(iss.ID)
		if err != nil {
			return nil, nil, err
		}
		relations, err := c.store.ListIssueRelations(iss.ID)
		if err != nil {
			return nil, nil, err
		}
		prs, err := c.store.ListPRs(iss.ID)
		if err != nil {
			return nil, nil, err
		}
		docs, err := c.store.ListDocumentsLinkedToIssue(iss.ID)
		if err != nil {
			return nil, nil, err
		}
		relCount := 0
		if relations != nil {
			relCount = len(relations.Outgoing) + len(relations.Incoming)
		}
		return nil, &IssueDeletePreview{
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
	if err := c.store.DeleteIssue(iss.ID); err != nil {
		return nil, nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &iss.RepoID, RepoPrefix: repo.Prefix,
		Op: "issue.delete", Kind: "issue",
		TargetID: &iss.ID, TargetLabel: iss.Key,
		Details: iss.Title,
	})
	return iss, nil, nil
}

func (c *localClient) PeekNextIssue(ctx context.Context, repo *model.Repo, slug string) (*model.Issue, error) {
	feat, err := c.store.GetFeatureBySlug(repo.ID, slug)
	if err != nil {
		return nil, fmt.Errorf("feature %q: %w", slug, err)
	}
	return c.store.PeekNextIssue(repo.ID, feat.ID)
}

func (c *localClient) ClaimNextIssue(ctx context.Context, repo *model.Repo, slug string, dryRun bool) (*model.Issue, error) {
	feat, err := c.store.GetFeatureBySlug(repo.ID, slug)
	if err != nil {
		return nil, fmt.Errorf("feature %q: %w", slug, err)
	}
	if dryRun {
		return c.store.PeekNextIssue(repo.ID, feat.ID)
	}
	who := c.actor
	if who == "" {
		who = "unknown"
	}
	iss, err := c.store.ClaimNextIssue(repo.ID, feat.ID, who)
	if err != nil {
		return nil, err
	}
	if iss != nil {
		c.recordOp(model.HistoryEntry{
			RepoID: &repo.ID, RepoPrefix: repo.Prefix,
			Op: "issue.claim", Kind: "issue",
			TargetID: &iss.ID, TargetLabel: iss.Key,
			Details: fmt.Sprintf("claimed by %s (todo → in_progress)", who),
		})
	}
	return iss, nil
}
