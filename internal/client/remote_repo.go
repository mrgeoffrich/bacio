package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/git"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

func (c *remoteClient) ListRepos(ctx context.Context) ([]*model.Repo, error) {
	var out []*model.Repo
	if err := c.do(ctx, http.MethodGet, "/repos", nil, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*model.Repo{}
	}
	return out, nil
}

func (c *remoteClient) GetRepoByPrefix(ctx context.Context, prefix string) (*model.Repo, error) {
	var out model.Repo
	path := "/repos/" + strings.ToUpper(prefix)
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetRepoByPath has no direct REST endpoint; the API addresses repos by
// prefix only. The remote backend lists every repo and filters by path
// on the client side. ErrNotFound when no row matches.
func (c *remoteClient) GetRepoByPath(ctx context.Context, path string) (*model.Repo, error) {
	repos, err := c.ListRepos(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range repos {
		if r.Path == path {
			return r, nil
		}
	}
	return nil, store.ErrNotFound
}

// EnsureRepo for remote: detect locally, then look up by path, falling
// back to POST /repos. The server (not the client) writes the audit
// row when CreateRepo fires, matching the local backend's behaviour.
func (c *remoteClient) EnsureRepo(ctx context.Context, info *git.Info) (*model.Repo, bool, error) {
	repo, err := c.GetRepoByPath(ctx, info.Root)
	if err == nil {
		return repo, false, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, false, err
	}
	body := map[string]any{
		"name":       info.Name,
		"path":       info.Root,
		"remote_url": info.RemoteURL,
	}
	var created model.Repo
	if err := c.do(ctx, http.MethodPost, "/repos", nil, body, &created); err != nil {
		return nil, false, fmt.Errorf("create repo: %w", err)
	}
	return &created, true, nil
}

func (c *remoteClient) DeleteRepo(ctx context.Context, prefix, confirm string, dryRun bool) (*model.Repo, *RepoDeletePreview, error) {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	if confirm != "" {
		q.Set("confirm", confirm)
	}
	if dryRun {
		var preview RepoDeletePreview
		if err := c.do(ctx, http.MethodDelete, "/repos/"+prefix, q, nil, &preview); err != nil {
			return nil, nil, err
		}
		return nil, &preview, nil
	}
	// For real deletes, fetch the repo first so the caller can render
	// the success message with name/path. Mirrors remote_issue.go's
	// fetch-then-delete pattern.
	repo, err := c.GetRepoByPrefix(ctx, prefix)
	if err != nil {
		return nil, nil, err
	}
	if err := c.do(ctx, http.MethodDelete, "/repos/"+prefix, q, nil, nil); err != nil {
		// Server returns 412 + Code=confirm_required when the
		// confirmation gate trips. Rehydrate the preview from
		// HTTPError.Details so the caller can show the alert.
		var he *HTTPError
		if errors.As(err, &he) && he.Status == http.StatusPreconditionFailed && he.Code == "confirm_required" {
			preview := decodePreviewFromDetails(he.Details)
			return nil, nil, &RepoConfirmError{
				Prefix:     prefix,
				GotConfirm: confirm,
				Preview:    preview,
			}
		}
		return nil, nil, err
	}
	return repo, nil, nil
}

// decodePreviewFromDetails rehydrates a RepoDeletePreview from the
// server's error envelope. Details is map[string]any decoded by
// remote.go; we round-trip it through JSON to leverage the struct
// tags rather than reach in by key.
func decodePreviewFromDetails(details map[string]any) *RepoDeletePreview {
	if details == nil {
		return nil
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return nil
	}
	var preview RepoDeletePreview
	if err := json.Unmarshal(raw, &preview); err != nil {
		return nil
	}
	return &preview
}

// LinkPhantomRepo (BACI-112) — `POST /repos/{prefix}/link` with body
// `{path: ...}` and `?dry_run=true` for the rehearsal path. Typed-error
// rehydration matches the handler's status-code mapping: 409 with a
// `kind` field in the details envelope rehydrates as a RepoLinkError so
// `errors.As` lets the CLI surface the right per-kind message.
func (c *remoteClient) LinkPhantomRepo(ctx context.Context, prefix, path string, dryRun bool) (*RepoLinkResult, error) {
	prefix = strings.ToUpper(strings.TrimSpace(prefix))
	q := url.Values{}
	if dryRun {
		q.Set("dry_run", "true")
	}
	body := map[string]any{"path": path}
	var result RepoLinkResult
	if err := c.do(ctx, http.MethodPost, "/repos/"+prefix+"/link", q, body, &result); err != nil {
		// Map the typed-error envelope back to a *RepoLinkError so the
		// CLI / desktop can branch by Kind. The handler emits 409 for
		// the "we found the row but refuse the operation" cases and 400
		// for the path-shape failures; both carry `kind` in details.
		var he *HTTPError
		if errors.As(err, &he) && he.Details != nil {
			if kindAny, ok := he.Details["kind"]; ok {
				if kind, _ := kindAny.(string); kind != "" {
					linkErr := &RepoLinkError{Kind: kind, Prefix: prefix, Path: path, Message: he.Message}
					if cp, ok := he.Details["current_path"].(string); ok {
						linkErr.CurrentPath = cp
					}
					if ep, ok := he.Details["existing_prefix"].(string); ok {
						linkErr.ExistingPrefix = ep
					}
					return nil, linkErr
				}
			}
		}
		return nil, err
	}
	return &result, nil
}
