package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func (c *localClient) ListDocuments(ctx context.Context, repo *model.Repo, typeStr string, includeArchived bool) ([]*model.Document, error) {
	f := store.DocumentFilter{RepoID: repo.ID, IncludeArchived: includeArchived}
	if typeStr != "" {
		t, err := model.ParseDocumentType(typeStr)
		if err != nil {
			return nil, err
		}
		f.Type = &t
	}
	docs, err := c.store.ListDocuments(f)
	if err != nil {
		return nil, err
	}
	if docs == nil {
		docs = []*model.Document{}
	}
	return docs, nil
}

func (c *localClient) ShowDocument(ctx context.Context, repo *model.Repo, filename string, withContent bool) (*DocView, error) {
	d, err := c.store.GetDocumentByFilename(repo.ID, filename, withContent)
	if err != nil {
		return nil, err
	}
	links, err := c.store.ListDocumentLinks(d.ID)
	if err != nil {
		return nil, err
	}
	if links == nil {
		links = []*model.DocumentLink{}
	}
	return &DocView{Document: d, Links: links}, nil
}

func (c *localClient) GetDocumentRaw(ctx context.Context, repo *model.Repo, filename string) (*model.Document, error) {
	return c.store.GetDocumentByFilename(repo.ID, filename, true)
}

func (c *localClient) DownloadDocument(ctx context.Context, repo *model.Repo, filename string) ([]byte, error) {
	d, err := c.store.GetDocumentByFilename(repo.ID, filename, true)
	if err != nil {
		return nil, err
	}
	return []byte(d.Content), nil
}

func (c *localClient) CreateDocument(ctx context.Context, repo *model.Repo, in DocCreateInput, dryRun bool) (*model.Document, error) {
	if dryRun {
		return &model.Document{
			RepoID:     repo.ID,
			Filename:   in.Filename,
			Type:       in.Type,
			SizeBytes:  int64(len(in.Body)),
			SourcePath: in.SourcePath,
		}, nil
	}
	d, err := c.store.CreateDocument(repo.ID, in.Filename, in.Type, in.Body, in.SourcePath)
	if err != nil {
		return nil, err
	}
	d.Content = ""
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "document.create", Kind: "document",
		TargetID: &d.ID, TargetLabel: d.Filename,
		Details: "type=" + string(d.Type),
	})
	return d, nil
}

func (c *localClient) UpsertDocument(ctx context.Context, repo *model.Repo, in DocCreateInput, dryRun bool) (*model.Document, error) {
	existing, err := c.store.GetDocumentByFilename(repo.ID, in.Filename, false)
	if errors.Is(err, store.ErrNotFound) {
		return c.CreateDocument(ctx, repo, in, dryRun)
	}
	if err != nil {
		return nil, err
	}
	var newType *model.DocumentType
	if in.Type != existing.Type {
		t := in.Type
		newType = &t
	}
	body := in.Body
	var newSource *string
	if in.SourcePath != "" && in.SourcePath != existing.SourcePath {
		sp := in.SourcePath
		newSource = &sp
	}
	if dryRun {
		projected := *existing
		projected.Type = in.Type
		projected.SizeBytes = int64(len(body))
		if newSource != nil {
			projected.SourcePath = *newSource
		}
		return &projected, nil
	}
	if err := c.store.UpdateDocument(existing.ID, newType, &body, newSource); err != nil {
		return nil, err
	}
	updated, err := c.store.GetDocumentByID(existing.ID, false)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "document.update", Kind: "document",
		TargetID: &updated.ID, TargetLabel: updated.Filename,
		Details: updatedFieldList(map[string]bool{
			"type":        newType != nil,
			"content":     true,
			"source_path": newSource != nil,
		}),
	})
	return updated, nil
}

func (c *localClient) EditDocument(ctx context.Context, repo *model.Repo, filename string, newType *string, newContent *string, dryRun bool) (*model.Document, error) {
	d, err := c.store.GetDocumentByFilename(repo.ID, filename, false)
	if err != nil {
		return nil, err
	}
	var typed *model.DocumentType
	if newType != nil {
		t, err := model.ParseDocumentType(*newType)
		if err != nil {
			return nil, err
		}
		typed = &t
	}
	if dryRun {
		projected := *d
		if typed != nil {
			projected.Type = *typed
		}
		if newContent != nil {
			projected.SizeBytes = int64(len(*newContent))
		}
		return &projected, nil
	}
	if err := c.store.UpdateDocument(d.ID, typed, newContent, nil); err != nil {
		return nil, err
	}
	updated, err := c.store.GetDocumentByID(d.ID, false)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "document.update", Kind: "document",
		TargetID: &updated.ID, TargetLabel: updated.Filename,
		Details: updatedFieldList(map[string]bool{
			"type":    typed != nil,
			"content": newContent != nil,
		}),
	})
	return updated, nil
}

func (c *localClient) RenameDocument(ctx context.Context, repo *model.Repo, oldName, newName, typeStr string, dryRun bool) (*model.Document, error) {
	var newType *model.DocumentType
	if typeStr != "" {
		t, err := model.ParseDocumentType(typeStr)
		if err != nil {
			return nil, err
		}
		newType = &t
	}
	d, err := c.store.GetDocumentByFilename(repo.ID, oldName, false)
	if err != nil {
		return nil, err
	}
	if dryRun {
		projected := *d
		projected.Filename = newName
		if newType != nil {
			projected.Type = *newType
		}
		return &projected, nil
	}
	if err := c.store.RenameDocument(d.ID, newName, newType); err != nil {
		return nil, err
	}
	updated, err := c.store.GetDocumentByID(d.ID, false)
	if err != nil {
		return nil, err
	}
	details := fmt.Sprintf("%s → %s", oldName, newName)
	if newType != nil {
		details += " type=" + string(*newType)
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "document.rename", Kind: "document",
		TargetID: &updated.ID, TargetLabel: updated.Filename,
		Details: details,
	})
	return updated, nil
}

func (c *localClient) DeleteDocument(ctx context.Context, repo *model.Repo, filename string, dryRun bool) (*model.Document, *DocumentDeletePreview, error) {
	d, err := c.store.GetDocumentByFilename(repo.ID, filename, false)
	if err != nil {
		return nil, nil, err
	}
	if dryRun {
		links, err := c.store.ListDocumentLinks(d.ID)
		if err != nil {
			return nil, nil, err
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
		return nil, &DocumentDeletePreview{
			Document: d, WouldDelete: true,
			Cascade: DocumentCascadeCount{
				IssueLinks:   issues,
				FeatureLinks: features,
			},
		}, nil
	}
	if err := c.store.DeleteDocument(d.ID); err != nil {
		return nil, nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "document.delete", Kind: "document",
		TargetID: &d.ID, TargetLabel: d.Filename,
		Details: "type=" + string(d.Type),
	})
	return d, nil, nil
}

func (c *localClient) LinkDocument(ctx context.Context, repo *model.Repo, in inputs.DocLinkInput, dryRun bool) (*model.DocumentLink, error) {
	d, err := c.store.GetDocumentByFilename(repo.ID, in.Filename, false)
	if err != nil {
		return nil, err
	}
	target, ref, err := c.resolveDocLinkTarget(repo, in.IssueKey, in.FeatureSlug)
	if err != nil {
		return nil, err
	}
	desc := strings.TrimSpace(in.Description)
	if dryRun {
		preview := &model.DocumentLink{
			DocumentID:       d.ID,
			DocumentFilename: d.Filename,
			DocumentType:     d.Type,
			Description:      desc,
		}
		if target.IssueID != nil {
			preview.IssueID = target.IssueID
		}
		if target.FeatureID != nil {
			preview.FeatureID = target.FeatureID
		}
		return preview, nil
	}
	link, err := c.store.LinkDocument(d.ID, target, desc)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "document.link", Kind: "document",
		TargetID: &d.ID, TargetLabel: d.Filename,
		Details: "→ " + ref,
	})
	return link, nil
}

func (c *localClient) UnlinkDocument(ctx context.Context, repo *model.Repo, in inputs.DocUnlinkInput, dryRun bool) (*DocumentUnlinkPreview, int64, error) {
	d, err := c.store.GetDocumentByFilename(repo.ID, in.Filename, false)
	if err != nil {
		return nil, 0, err
	}
	target, ref, err := c.resolveDocLinkTarget(repo, in.IssueKey, in.FeatureSlug)
	if err != nil {
		return nil, 0, err
	}
	if dryRun {
		links, err := c.store.ListDocumentLinks(d.ID)
		if err != nil {
			return nil, 0, err
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
		if matched == 0 {
			return nil, 0, fmt.Errorf("no link from %s to %s", d.Filename, ref)
		}
		return &DocumentUnlinkPreview{
			Filename:    d.Filename,
			Target:      ref,
			WouldRemove: matched,
		}, 0, nil
	}
	n, err := c.store.UnlinkDocument(d.ID, target)
	if err != nil {
		return nil, 0, err
	}
	if n == 0 {
		return nil, 0, fmt.Errorf("no link from %s to %s", d.Filename, ref)
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &repo.ID, RepoPrefix: repo.Prefix,
		Op: "document.unlink", Kind: "document",
		TargetID: &d.ID, TargetLabel: d.Filename,
		Details: "↛ " + ref,
	})
	return nil, n, nil
}

// ArchiveDocument stamps the document's archived_at column (BACI-68).
// Records a doc.archive audit row under the resolved actor.
func (c *localClient) ArchiveDocument(ctx context.Context, repo *model.Repo, filename string, dryRun bool) (*model.Document, error) {
	return c.setDocumentArchived(ctx, repo, filename, true, dryRun, "doc.archive")
}

// UnarchiveDocument clears archived_at (BACI-68).
func (c *localClient) UnarchiveDocument(ctx context.Context, repo *model.Repo, filename string, dryRun bool) (*model.Document, error) {
	return c.setDocumentArchived(ctx, repo, filename, false, dryRun, "doc.unarchive")
}

func (c *localClient) setDocumentArchived(ctx context.Context, repo *model.Repo, filename string, archived, dryRun bool, op string) (*model.Document, error) {
	d, err := c.store.GetDocumentByFilename(repo.ID, filename, false)
	if err != nil {
		return nil, err
	}
	if dryRun {
		projected := *d
		// Mirror the store's idempotent semantics — see local_issue.go's
		// setIssueArchived for the rationale.
		if archived && d.ArchivedAt == nil {
			now := time.Now().UTC()
			projected.ArchivedAt = &now
		} else if !archived {
			projected.ArchivedAt = nil
		}
		return &projected, nil
	}
	if err := c.store.SetDocumentArchived(d.ID, archived); err != nil {
		return nil, err
	}
	updated, err := c.store.GetDocumentByID(d.ID, false)
	if err != nil {
		return nil, err
	}
	c.recordOp(model.HistoryEntry{
		RepoID: &d.RepoID, RepoPrefix: repo.Prefix,
		Op: op, Kind: "document",
		TargetID: &updated.ID, TargetLabel: updated.Filename,
	})
	return updated, nil
}

// resolveDocLinkTarget mirrors the API's helper but without the HTTP
// status tuple — failures come back as plain errors.
func (c *localClient) resolveDocLinkTarget(repo *model.Repo, issueKey, featureSlug string) (store.LinkTarget, string, error) {
	switch {
	case issueKey != "" && featureSlug != "":
		return store.LinkTarget{}, "", fmt.Errorf("provide issue_key OR feature_slug, not both")
	case issueKey == "" && featureSlug == "":
		return store.LinkTarget{}, "", fmt.Errorf("issue_key or feature_slug is required")
	case issueKey != "":
		prefix, num, err := store.ParseIssueKey(issueKey)
		if err != nil {
			return store.LinkTarget{}, "", err
		}
		iss, err := c.store.GetIssueByKey(prefix, num)
		if err != nil {
			return store.LinkTarget{}, "", err
		}
		if iss.RepoID != repo.ID {
			return store.LinkTarget{}, "", fmt.Errorf("issue %s not in this repo", iss.Key)
		}
		return store.LinkTarget{IssueID: &iss.ID}, iss.Key, nil
	default:
		feat, err := c.store.GetFeatureBySlug(repo.ID, featureSlug)
		if err != nil {
			return store.LinkTarget{}, "", err
		}
		return store.LinkTarget{FeatureID: &feat.ID}, "feature/" + feat.Slug, nil
	}
}
