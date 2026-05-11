package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func (c *remoteClient) ListDocuments(ctx context.Context, repo *model.Repo, typeStr string) ([]*model.Document, error) {
	q := url.Values{}
	if typeStr != "" {
		q.Set("type", typeStr)
	}
	var out []*model.Document
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo.Prefix+"/documents", q, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.Document{}
	}
	return out, nil
}

func (c *remoteClient) ShowDocument(ctx context.Context, repo *model.Repo, filename string, withContent bool) (*DocView, error) {
	q := url.Values{}
	if !withContent {
		q.Set("with_content", "false")
	}
	var out DocView
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo.Prefix+"/documents/"+url.PathEscape(filename), q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) GetDocumentRaw(ctx context.Context, repo *model.Repo, filename string) (*model.Document, error) {
	view, err := c.ShowDocument(ctx, repo, filename, true)
	if err != nil {
		return nil, err
	}
	return view.Document, nil
}

func (c *remoteClient) DownloadDocument(ctx context.Context, repo *model.Repo, filename string) ([]byte, error) {
	var raw []byte
	if err := c.do(ctx, http.MethodGet, "/repos/"+repo.Prefix+"/documents/"+url.PathEscape(filename)+"/download", nil, nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *remoteClient) CreateDocument(ctx context.Context, repo *model.Repo, in DocCreateInput, dryRun bool) (*model.Document, error) {
	body := inputs.DocAddInput{
		Filename:   in.Filename,
		Type:       string(in.Type),
		Content:    in.Body,
		SourcePath: in.SourcePath,
	}
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.Document
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo.Prefix+"/documents", q, body, &out); err != nil {
		return nil, err
	}
	out.Content = "" // mirror CLI's add behaviour: don't echo body
	return &out, nil
}

func (c *remoteClient) UpsertDocument(ctx context.Context, repo *model.Repo, in DocCreateInput, dryRun bool) (*model.Document, error) {
	body := inputs.DocAddInput{
		Filename:   in.Filename,
		Type:       string(in.Type),
		Content:    in.Body,
		SourcePath: in.SourcePath,
	}
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.Document
	if err := c.do(ctx, http.MethodPut, "/repos/"+repo.Prefix+"/documents/"+url.PathEscape(in.Filename), q, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) EditDocument(ctx context.Context, repo *model.Repo, filename string, newType *string, newContent *string, dryRun bool) (*model.Document, error) {
	body := map[string]any{"filename": filename}
	if newType != nil {
		body["type"] = *newType
	}
	if newContent != nil {
		body["content"] = *newContent
	}
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.Document
	if err := c.do(ctx, http.MethodPatch, "/repos/"+repo.Prefix+"/documents/"+url.PathEscape(filename), q, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) RenameDocument(ctx context.Context, repo *model.Repo, oldName, newName, typeStr string, dryRun bool) (*model.Document, error) {
	body := inputs.DocRenameInput{OldFilename: oldName, NewFilename: newName, Type: typeStr}
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.Document
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo.Prefix+"/documents/"+url.PathEscape(oldName)+"/rename", q, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) DeleteDocument(ctx context.Context, repo *model.Repo, filename string, dryRun bool) (*model.Document, *DocumentDeletePreview, error) {
	if dryRun {
		q := url.Values{}
		q.Set("dry_run", "true")
		var preview DocumentDeletePreview
		if err := c.do(ctx, http.MethodDelete, "/repos/"+repo.Prefix+"/documents/"+url.PathEscape(filename), q, nil, &preview); err != nil {
			return nil, nil, err
		}
		return nil, &preview, nil
	}
	doc, err := c.GetDocumentRaw(ctx, repo, filename)
	if err != nil {
		return nil, nil, err
	}
	if err := c.do(ctx, http.MethodDelete, "/repos/"+repo.Prefix+"/documents/"+url.PathEscape(filename), nil, nil, nil); err != nil {
		return nil, nil, err
	}
	return doc, nil, nil
}

func (c *remoteClient) LinkDocument(ctx context.Context, repo *model.Repo, in inputs.DocLinkInput, dryRun bool) (*model.DocumentLink, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	var out model.DocumentLink
	if err := c.do(ctx, http.MethodPost, "/repos/"+repo.Prefix+"/documents/"+url.PathEscape(in.Filename)+"/links", q, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) UnlinkDocument(ctx context.Context, repo *model.Repo, in inputs.DocUnlinkInput, dryRun bool) (*DocumentUnlinkPreview, int64, error) {
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
		var preview DocumentUnlinkPreview
		if err := c.do(ctx, http.MethodDelete, "/repos/"+repo.Prefix+"/documents/"+url.PathEscape(in.Filename)+"/links", q, in, &preview); err != nil {
			return nil, 0, err
		}
		return &preview, 0, nil
	}
	if err := c.do(ctx, http.MethodDelete, "/repos/"+repo.Prefix+"/documents/"+url.PathEscape(in.Filename)+"/links", nil, in, nil); err != nil {
		return nil, 0, err
	}
	return nil, 1, nil
}

func (c *remoteClient) ListHistory(ctx context.Context, repo *model.Repo, f store.HistoryFilter) ([]*model.HistoryEntry, error) {
	q := url.Values{}
	if f.Limit > 0 {
		q.Set("limit", strInt(int64(f.Limit)))
	}
	if f.Offset > 0 {
		q.Set("offset", strInt(int64(f.Offset)))
	}
	if f.Actor != "" {
		q.Set("actor", f.Actor)
	}
	if f.Op != "" {
		q.Set("op", f.Op)
	}
	if f.Kind != "" {
		q.Set("kind", f.Kind)
	}
	if f.From != nil {
		q.Set("from", f.From.UTC().Format("2006-01-02T15:04:05Z"))
	}
	if f.To != nil {
		q.Set("to", f.To.UTC().Format("2006-01-02T15:04:05Z"))
	}
	if f.OldestFirst {
		q.Set("oldest_first", "true")
	}
	path := "/history"
	if repo != nil {
		path = "/repos/" + repo.Prefix + "/history"
	}
	var out []*model.HistoryEntry
	if err := c.do(ctx, http.MethodGet, path, q, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.HistoryEntry{}
	}
	return out, nil
}
