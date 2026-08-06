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
	Filename   string              `json:"filename"`
	Type       string              `json:"type"`
	SizeBytes  int64               `json:"sizeBytes"`
	UpdatedAt  time.Time           `json:"updatedAt"`
	CreatedAt  time.Time           `json:"createdAt"`
	ArchivedAt *time.Time          `json:"archivedAt,omitempty"`
	Snippet    string              `json:"snippet,omitempty"`
	Links      []DocSummaryLinkDTO `json:"links,omitempty"`
	// FolderUUID is the doc-folder this page is filed under, or "" for
	// the tree root. Addressed by **uuid**, never the folder's int64 id:
	// uuid is the only folder identity that survives a sync round trip,
	// and every folder mutator on this service takes a uuid. "" is a
	// meaningful value (the root is not itself a folder), so the field
	// carries no omitempty.
	FolderUUID string `json:"folderUuid"`
	// FolderPosition is the page's sort key inside its folder. It is a
	// SORT KEY, not a dense index — siblings may share one and the
	// listing tie-breaks on filename, so every pre-pivot document sitting
	// at 0 keeps its historical alphabetical order.
	FolderPosition int `json:"folderPosition"`
}

// DocFolderDTO is one node of the Confluence-style document tree, shaped
// for the desktop docs rail. The tree is flat on the wire — every folder
// in the repo, each naming its parent — and the React side assembles it;
// that is exactly what ListDocFolders on the client returns.
//
// ParentUUID == "" means the tree ROOT. The root is not itself a folder,
// so "" is unambiguous rather than a missing value, and the field
// deliberately carries no omitempty so the distinction survives JSON.
type DocFolderDTO struct {
	UUID       string    `json:"uuid"`
	Name       string    `json:"name"`
	ParentUUID string    `json:"parentUuid"`
	Position   int       `json:"position"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// DocFolderDeletePreviewDTO is the blast radius of deleting a folder,
// for the confirmation dialog. Subfolders is the number of DESCENDANT
// folders that go with it; DocumentsReRooted is the number of pages in
// that whole subtree that get moved back to the tree root — a folder
// delete never destroys a page.
type DocFolderDeletePreviewDTO struct {
	UUID              string `json:"uuid"`
	Name              string `json:"name"`
	Path              string `json:"path"`
	Subfolders        int    `json:"subfolders"`
	DocumentsReRooted int    `json:"documentsReRooted"`
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
	// One extra round trip resolves documents.folder_id (an int64 the
	// frontend must never see) into the folder uuid every mutator on this
	// service is addressed by. Cheap: folders are a handful of rows per
	// repo, and a repo with no folders returns an empty slice.
	_, uuidByFolderID, err := d.folderTree(ctx, repo)
	if err != nil {
		return nil, err
	}
	out := make([]DocSummary, 0, len(docs))
	for _, doc := range docs {
		row := DocSummary{
			Filename:       doc.Filename,
			Type:           string(doc.Type),
			SizeBytes:      doc.SizeBytes,
			UpdatedAt:      doc.UpdatedAt,
			CreatedAt:      doc.CreatedAt,
			ArchivedAt:     doc.ArchivedAt,
			Snippet:        doc.Snippet,
			FolderPosition: doc.FolderPosition,
		}
		if doc.FolderID != nil {
			row.FolderUUID = uuidByFolderID[*doc.FolderID]
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

// defaultNewDocType is the document type a page created from the docs
// tree lands on when the caller doesn't name one. `user_docs` is the
// generic "a human wrote this page" bucket — the agent-produced types
// (plan / review / transcript / session_retro) are written by the
// pipeline, never by the New-page button.
const defaultNewDocType = model.DocTypeUserDocs

// CreateDoc creates a document and returns it with its (possibly empty)
// body, ready for the editor to open on. filename must be a single flat
// segment — document filenames stay globally unique per repo and folders
// are purely organisational, so '/' is rejected at the store boundary.
//
// docType is optional: "" falls back to user_docs. folderUUID files the
// new page under a folder; "" leaves it at the tree root. The folder is
// applied as a second write (the document create path has no folder
// argument), so a bad folder uuid surfaces after the page already
// exists — the caller sees the error and the page is at the root.
func (d *DocService) CreateDoc(repoPrefix, filename, docType, content, folderUUID string) (DocContent, error) {
	ctx := context.Background()
	repo, err := d.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return DocContent{}, err
	}
	t := defaultNewDocType
	if docType != "" {
		parsed, perr := model.ParseDocumentType(docType)
		if perr != nil {
			return DocContent{}, perr
		}
		t = parsed
	}
	if _, err := d.client.CreateDocument(ctx, repo, client.DocCreateInput{
		Filename: filename,
		Type:     t,
		Body:     content,
	}, false); err != nil {
		return DocContent{}, err
	}
	if folderUUID != "" {
		if _, err := d.client.MoveDocumentToFolder(ctx, repo, client.DocumentFolderMoveInput{
			Filename:   filename,
			FolderUUID: folderUUID,
		}, false); err != nil {
			return DocContent{}, err
		}
	}
	return d.GetDoc(repoPrefix, filename)
}

// RenameDoc renames a document in place and returns the refreshed
// content payload under its new name. The folder membership, links and
// body are untouched — a rename is purely the filename.
func (d *DocService) RenameDoc(repoPrefix, filename, newFilename string) (DocContent, error) {
	ctx := context.Background()
	repo, err := d.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return DocContent{}, err
	}
	// Empty typeStr = "leave the type alone"; the rename affordance in
	// the docs tree never re-types a page.
	if _, err := d.client.RenameDocument(ctx, repo, filename, newFilename, "", false); err != nil {
		return DocContent{}, err
	}
	return d.GetDoc(repoPrefix, newFilename)
}

// DeleteDoc permanently removes a document and every link pointing at
// it. Unlike ArchiveDoc this is not reversible — the caller is expected
// to have confirmed with the user first.
func (d *DocService) DeleteDoc(repoPrefix, filename string) error {
	ctx := context.Background()
	repo, err := d.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return err
	}
	_, _, err = d.client.DeleteDocument(ctx, repo, filename, false)
	return err
}

// MoveDocToFolder files a document under a folder. folderUUID == ""
// moves it back to the tree root — the root is not a folder, so "" is a
// meaningful destination rather than a missing argument.
//
// position is the sort key inside the target folder; pass null to append
// after the folder's current members. It is a sort key, NOT a dense
// index — siblings may share one and the listing tie-breaks on filename.
func (d *DocService) MoveDocToFolder(repoPrefix, filename, folderUUID string, position *int) error {
	ctx := context.Background()
	repo, err := d.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return err
	}
	_, err = d.client.MoveDocumentToFolder(ctx, repo, client.DocumentFolderMoveInput{
		Filename:   filename,
		FolderUUID: folderUUID,
		Position:   position,
	}, false)
	return err
}

// folderTree returns every folder in the repo plus an id → uuid index.
// The index is the one place documents.folder_id gets translated into
// the uuid the wire shape uses; nothing outside this file should see a
// folder's int64 id.
func (d *DocService) folderTree(ctx context.Context, repo *model.Repo) ([]*model.DocFolder, map[int64]string, error) {
	folders, err := d.client.ListDocFolders(ctx, repo)
	if err != nil {
		return nil, nil, err
	}
	uuidByID := make(map[int64]string, len(folders))
	for _, f := range folders {
		uuidByID[f.ID] = f.UUID
	}
	return folders, uuidByID, nil
}

func docFolderDTO(f *model.DocFolder, uuidByID map[int64]string) DocFolderDTO {
	dto := DocFolderDTO{
		UUID:      f.UUID,
		Name:      f.Name,
		Position:  f.Position,
		CreatedAt: f.CreatedAt,
		UpdatedAt: f.UpdatedAt,
	}
	if f.ParentID != nil {
		dto.ParentUUID = uuidByID[*f.ParentID]
	}
	return dto
}

// folderDTOAfterWrite re-reads the tree and reshapes one folder from it.
// Mutators return a *model.DocFolder carrying a parent_id, and the DTO
// needs the parent's uuid — resolving it from the freshly-read tree also
// guarantees the returned row reflects what actually landed.
func (d *DocService) folderDTOAfterWrite(ctx context.Context, repo *model.Repo, uuid string) (DocFolderDTO, error) {
	folders, uuidByID, err := d.folderTree(ctx, repo)
	if err != nil {
		return DocFolderDTO{}, err
	}
	for _, f := range folders {
		if f.UUID == uuid {
			return docFolderDTO(f, uuidByID), nil
		}
	}
	return DocFolderDTO{}, fmt.Errorf("doc folder %s not found after write", uuid)
}

// ListDocFolders returns EVERY folder in the repo, not just the roots —
// the docs rail assembles the tree client-side from the flat list, and a
// full list is also what makes a display path ("Design/API/Auth")
// derivable without a round trip per level.
func (d *DocService) ListDocFolders(repoPrefix string) ([]DocFolderDTO, error) {
	ctx := context.Background()
	repo, err := d.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return nil, err
	}
	folders, uuidByID, err := d.folderTree(ctx, repo)
	if err != nil {
		return nil, err
	}
	out := make([]DocFolderDTO, 0, len(folders))
	for _, f := range folders {
		out = append(out, docFolderDTO(f, uuidByID))
	}
	return out, nil
}

// CreateDocFolder adds a folder. parentUUID == "" creates it at the tree
// root. Sibling names must be unique within one parent; the store
// rejects a duplicate rather than silently de-duplicating.
func (d *DocService) CreateDocFolder(repoPrefix, name, parentUUID string) (DocFolderDTO, error) {
	ctx := context.Background()
	repo, err := d.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return DocFolderDTO{}, err
	}
	folder, err := d.client.CreateDocFolder(ctx, repo, client.DocFolderCreateInput{
		Name:       name,
		ParentUUID: parentUUID,
	}, false)
	if err != nil {
		return DocFolderDTO{}, err
	}
	return d.folderDTOAfterWrite(ctx, repo, folder.UUID)
}

// RenameDocFolder renames a folder in place; its parent, children and
// documents are all untouched. Because record folders on disk are keyed
// by uuid, a rename is a pure content change to the sync layer — no
// rename detection, no redirects.
func (d *DocService) RenameDocFolder(repoPrefix, uuid, newName string) (DocFolderDTO, error) {
	ctx := context.Background()
	repo, err := d.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return DocFolderDTO{}, err
	}
	if _, err := d.client.RenameDocFolder(ctx, repo, uuid, newName, false); err != nil {
		return DocFolderDTO{}, err
	}
	return d.folderDTOAfterWrite(ctx, repo, uuid)
}

// MoveDocFolder re-parents a folder and its whole subtree.
// newParentUUID == "" re-roots it. The store refuses a move into the
// folder's own descendant and a move that would breach the depth cap.
func (d *DocService) MoveDocFolder(repoPrefix, uuid, newParentUUID string) (DocFolderDTO, error) {
	ctx := context.Background()
	repo, err := d.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return DocFolderDTO{}, err
	}
	if _, err := d.client.MoveDocFolder(ctx, repo, client.DocFolderMoveInput{
		UUID:          uuid,
		NewParentUUID: newParentUUID,
	}, false); err != nil {
		return DocFolderDTO{}, err
	}
	return d.folderDTOAfterWrite(ctx, repo, uuid)
}

// PreviewDeleteDocFolder reports what a delete would take with it,
// WITHOUT deleting anything — the dry-run behind the confirmation
// dialog. Pairs with DeleteDocFolder, which does the real write.
func (d *DocService) PreviewDeleteDocFolder(repoPrefix, uuid string) (DocFolderDeletePreviewDTO, error) {
	ctx := context.Background()
	repo, err := d.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return DocFolderDeletePreviewDTO{}, err
	}
	_, preview, err := d.client.DeleteDocFolder(ctx, repo, uuid, true)
	if err != nil {
		return DocFolderDeletePreviewDTO{}, err
	}
	if preview == nil || preview.Folder == nil {
		return DocFolderDeletePreviewDTO{}, fmt.Errorf("doc folder %s: no delete preview returned", uuid)
	}
	return DocFolderDeletePreviewDTO{
		UUID:              preview.Folder.UUID,
		Name:              preview.Folder.Name,
		Path:              preview.Path,
		Subfolders:        preview.Cascade.Subfolders,
		DocumentsReRooted: preview.Cascade.DocumentsReRooted,
	}, nil
}

// DeleteDocFolder removes a folder for real. Descendant folders go with
// it; every document in that subtree is re-rooted rather than deleted.
func (d *DocService) DeleteDocFolder(repoPrefix, uuid string) error {
	ctx := context.Background()
	repo, err := d.resolveRepo(ctx, repoPrefix)
	if err != nil {
		return err
	}
	_, _, err = d.client.DeleteDocFolder(ctx, repo, uuid, false)
	return err
}
