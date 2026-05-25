package main

import (
	"context"
	"fmt"
	"time"

	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/model"
)

// DocSummary is one document, shaped for the desktop document list.
//
// BACI-204 adds Links + Snippet + ArchivedAt + CreatedAt so the
// redesigned Documents page can render the linked-issue / linked-feature
// chips, a per-row snippet under each title, sort by created date,
// and mute archived rows — all without a second round trip per row.
// Existing fields stay first so the wire shape is purely additive.
type DocSummary struct {
	Filename   string       `json:"filename"`
	Type       string       `json:"type"`
	SizeBytes  int64        `json:"sizeBytes"`
	UpdatedAt  time.Time    `json:"updatedAt"`
	CreatedAt  time.Time    `json:"createdAt"`
	ArchivedAt *time.Time   `json:"archivedAt,omitempty"`
	Snippet    string       `json:"snippet,omitempty"`
	Links      []DocSummaryLinkDTO `json:"links,omitempty"`
}

// DocSummaryLinkDTO is one link row on a DocSummary — issue or feature plus
// the per-link description. Same fields as the brief endpoint's
// LinkedDocDTO so the React rendering stays consistent across the
// document-detail and document-list surfaces.
type DocSummaryLinkDTO struct {
	IssueKey    string `json:"issueKey,omitempty"`
	FeatureSlug string `json:"featureSlug,omitempty"`
	Description string `json:"description,omitempty"`
}

// DocContent is one document with its markdown body, for the editor pane.
type DocContent struct {
	Filename  string    `json:"filename"`
	Type      string    `json:"type"`
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// DocService is the Wails-bound document API the desktop frontend talks to.
// It wraps a local bacio client.Client and reshapes its results into the DTOs
// the editor UI expects. Documents are per-repo: every method needs a
// concrete repo prefix (the "all repositories" pseudo-board has no document
// scope).
type DocService struct {
	client client.Client
}

func NewDocService(c client.Client) *DocService {
	return &DocService{client: c}
}

// resolveRepo turns a repo prefix into a *model.Repo, rejecting the empty /
// "all" pseudo-board since documents are always scoped to one repo.
func (d *DocService) resolveRepo(ctx context.Context, repoPrefix string) (*model.Repo, error) {
	if repoPrefix == "" || repoPrefix == "all" {
		return nil, fmt.Errorf("select a repository to view its documents")
	}
	return d.client.GetRepoByPrefix(ctx, repoPrefix)
}

// ListDocs returns every document in one repo as a summary row. typeFilter is
// a document-type string ("architecture", "designs", …) or "" for all types.
func (d *DocService) ListDocs(repoPrefix, typeFilter string) ([]DocSummary, error) {
	ctx := context.Background()
	repo, err := d.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return nil, err
	}
	// BACI-68: archived docs follow the display.show_archived global
	// setting. When on, the Docs view surfaces archived rows rendered
	// visibly muted; when off (the default) they're hidden.
	showArchived, _ := d.client.GetDisplayShowArchived(ctx)
	docs, err := d.client.ListDocuments(ctx, repo, typeFilter, showArchived)
	if err != nil {
		return nil, err
	}
	out := make([]DocSummary, 0, len(docs))
	for _, doc := range docs {
		row := DocSummary{
			Filename:   doc.Filename,
			Type:       string(doc.Type),
			SizeBytes:  doc.SizeBytes,
			UpdatedAt:  doc.UpdatedAt,
			CreatedAt:  doc.CreatedAt,
			ArchivedAt: doc.ArchivedAt,
			Snippet:    doc.Snippet,
		}
		// BACI-204: reshape link rows into the desktop DTO. A doc with
		// no links keeps Links nil so omitempty drops it from the wire
		// JSON.
		if len(doc.Links) > 0 {
			row.Links = make([]DocSummaryLinkDTO, 0, len(doc.Links))
			for _, l := range doc.Links {
				row.Links = append(row.Links, DocSummaryLinkDTO{
					IssueKey:    l.IssueKey,
					FeatureSlug: l.FeatureSlug,
					Description: l.Description,
				})
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// ArchiveDoc / UnarchiveDoc are the Wails parity for the existing
// HTTP /repos/{prefix}/documents/{filename}/{archive,unarchive}
// routes — surfaced through the same client.SetDocumentArchived path
// the CLI uses (BACI-68). They power the redesigned Documents page's
// per-row archive toggle (BACI-204); the desktop binding had to grow
// these so the React surface stays transport-agnostic.
func (d *DocService) ArchiveDoc(repoPrefix, filename string) error {
	ctx := context.Background()
	repo, err := d.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return err
	}
	_, err = d.client.ArchiveDocument(ctx, repo, filename, false)
	return err
}

func (d *DocService) UnarchiveDoc(repoPrefix, filename string) error {
	ctx := context.Background()
	repo, err := d.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return err
	}
	_, err = d.client.UnarchiveDocument(ctx, repo, filename, false)
	return err
}

// GetDoc returns one document with its markdown content for editing.
func (d *DocService) GetDoc(repoPrefix, filename string) (DocContent, error) {
	ctx := context.Background()
	repo, err := d.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return DocContent{}, err
	}
	view, err := d.client.ShowDocument(ctx, repo, filename, true)
	if err != nil {
		return DocContent{}, err
	}
	doc := view.Document
	return DocContent{
		Filename:  doc.Filename,
		Type:      string(doc.Type),
		Content:   doc.Content,
		UpdatedAt: doc.UpdatedAt,
	}, nil
}

// SaveDoc persists new markdown content for an existing document and returns
// the refreshed document. The underlying client records an audit entry.
func (d *DocService) SaveDoc(repoPrefix, filename, content string) (DocContent, error) {
	ctx := context.Background()
	repo, err := d.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return DocContent{}, err
	}
	// EditDocument returns the refreshed row without its content body, so
	// echo back the content we just persisted rather than its empty field.
	doc, err := d.client.EditDocument(ctx, repo, filename, nil, &content, false)
	if err != nil {
		return DocContent{}, err
	}
	return DocContent{
		Filename:  doc.Filename,
		Type:      string(doc.Type),
		Content:   content,
		UpdatedAt: doc.UpdatedAt,
	}, nil
}
