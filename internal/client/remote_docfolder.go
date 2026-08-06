package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// Doc folders over HTTP. Route map (Phase 4A builds the server side):
//
//	GET    /repos/{prefix}/doc-folders
//	POST   /repos/{prefix}/doc-folders                       {name, parent_uuid}
//	PATCH  /repos/{prefix}/doc-folders/{uuid}                {name} | {parent_uuid}
//	DELETE /repos/{prefix}/doc-folders/{uuid}
//	PUT    /repos/{prefix}/documents/{filename}/folder       {folder_uuid, position?}
//
// The PATCH body is a presence map: `name` present renames, `parent_uuid`
// present re-parents, and a *present but empty* `parent_uuid` means the
// tree root. Both fields are pointers so `omitempty` distinguishes
// "absent" from "explicitly empty" — the same idiom EditDocument uses.

// docFolderPatch is the PATCH body. Pointers, not strings: an empty
// parent_uuid is a meaningful value (the tree root), so it must be
// distinguishable from an omitted one.
type docFolderPatch struct {
	Name       *string `json:"name,omitempty"`
	ParentUUID *string `json:"parent_uuid,omitempty"`
}

func (c *remoteClient) ListDocFolders(ctx context.Context, repo *model.Repo) ([]*model.DocFolder, error) {
	var out []*model.DocFolder
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo.Prefix+"/doc-folders", nil, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.DocFolder{}
	}
	return out, nil
}

func (c *remoteClient) CreateDocFolder(ctx context.Context, repo *model.Repo, in DocFolderCreateInput, dryRun bool) (*model.DocFolder, error) {
	var out model.DocFolder
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo.Prefix+"/doc-folders", dryRunQuery(dryRun), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) RenameDocFolder(ctx context.Context, repo *model.Repo, uuid, newName string, dryRun bool) (*model.DocFolder, error) {
	var out model.DocFolder
	body := docFolderPatch{Name: &newName}
	path := "/repos/" + repo.Prefix + "/doc-folders/" + url.PathEscape(uuid)
	if err := c.do(ctx, http.MethodPatch, path, dryRunQuery(dryRun), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) MoveDocFolder(ctx context.Context, repo *model.Repo, in DocFolderMoveInput, dryRun bool) (*model.DocFolder, error) {
	var out model.DocFolder
	parent := in.NewParentUUID
	body := docFolderPatch{ParentUUID: &parent}
	path := "/repos/" + repo.Prefix + "/doc-folders/" + url.PathEscape(in.UUID)
	if err := c.do(ctx, http.MethodPatch, path, dryRunQuery(dryRun), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) DeleteDocFolder(ctx context.Context, repo *model.Repo, uuid string, dryRun bool) (*model.DocFolder, *DocFolderDeletePreview, error) {
	path := "/repos/" + repo.Prefix + "/doc-folders/" + url.PathEscape(uuid)
	if dryRun {
		var preview DocFolderDeletePreview
		if err := c.do(ctx, http.MethodDelete, path, dryRunQuery(true), nil, &preview); err != nil {
			return nil, nil, err
		}
		return nil, &preview, nil
	}
	// Fetch first so the caller can render the success message with the
	// folder's name. There is no single-folder GET route (the tree is
	// always read whole), so filter the list. Mirrors remote_repo.go's
	// GetRepoByPath doing the same for a route the API doesn't expose.
	folders, err := c.ListDocFolders(ctx, repo)
	if err != nil {
		return nil, nil, err
	}
	var folder *model.DocFolder
	for _, f := range folders {
		if f.UUID == uuid {
			folder = f
			break
		}
	}
	if folder == nil {
		return nil, nil, store.ErrNotFound
	}
	if err := c.do(ctx, http.MethodDelete, path, nil, nil, nil); err != nil {
		return nil, nil, err
	}
	return folder, nil, nil
}

func (c *remoteClient) MoveDocumentToFolder(ctx context.Context, repo *model.Repo, in DocumentFolderMoveInput, dryRun bool) (*model.Document, error) {
	var out model.Document
	path := "/repos/" + repo.Prefix + "/documents/" + url.PathEscape(in.Filename) + "/folder"
	if err := c.do(ctx, http.MethodPut, path, dryRunQuery(dryRun), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// dryRunQuery is the one-liner every pivot remote mutator uses for the
// `?dry_run=true` rehearsal flag. nil when the call is for real, so the
// URL stays clean.
func dryRunQuery(dryRun bool) url.Values {
	if !dryRun {
		return nil
	}
	q := url.Values{}
	q.Set("dry_run", "true")
	return q
}
