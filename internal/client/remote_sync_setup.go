package client

// BACI-111 — remote SetupSync. POSTs to /repos/{prefix}/sync/setup and,
// uniquely among the remote verbs, decodes the response body even on a
// non-2xx status: a 409 carries the renumber-collision preview the
// caller needs to show the user. The existing `do` helper throws on
// !ok and would lose the structured body — this verb uses a small
// per-call helper that mirrors `do` field-for-field but inspects the
// status before deciding whether the body is the success shape or the
// `{error, code, details}` envelope.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/model"
	bsync "github.com/mrgeoffrich/bacio/internal/sync"
)

// remoteSyncSetupOut mirrors api.SyncSetupOut on the wire. Kept
// package-local — the public client.SyncSetupResult is the cross-
// transport shape callers see.
type remoteSyncSetupOut struct {
	Mode              string                  `json:"mode"`
	Init              *bsync.InitResult       `json:"init,omitempty"`
	Clone             *bsync.CloneResult      `json:"clone,omitempty"`
	PreviewCollisions *bsync.CollisionPreview `json:"preview_collisions,omitempty"`
}

// SetupSync (remote) — POST /repos/{prefix}/sync/setup. On 200 the
// body decodes into the success shape; on 409 the body decodes into
// the same shape with `preview_collisions` populated, and we return
// (result, ErrSetupCollision) so the caller can branch. Any other
// non-2xx is the standard {error, code, details} envelope, wrapped as
// an HTTPError.
func (c *remoteClient) SetupSync(ctx context.Context, repo *model.Repo, in inputs.SyncSetupInput) (*SyncSetupResult, error) {
	if repo == nil {
		return nil, fmt.Errorf("SetupSync: repo is nil")
	}
	path := "/repos/" + repo.Prefix + "/sync/setup"
	body, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	u := *c.base
	setURLPath(&u, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.actor != "" {
		req.Header.Set("X-Actor", c.actor)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusOK:
		var out remoteSyncSetupOut
		if len(raw) > 0 {
			if jerr := json.Unmarshal(raw, &out); jerr != nil {
				return nil, fmt.Errorf("decode response: %w", jerr)
			}
		}
		return &SyncSetupResult{
			Mode:              out.Mode,
			Init:              out.Init,
			Clone:             out.Clone,
			PreviewCollisions: out.PreviewCollisions,
		}, nil
	case resp.StatusCode == http.StatusConflict:
		// 409: the server returns the SyncSetupOut shape with
		// preview_collisions populated; surface the typed sentinel
		// alongside the populated result.
		var out remoteSyncSetupOut
		if len(raw) > 0 {
			if jerr := json.Unmarshal(raw, &out); jerr != nil {
				// Fall back to the error-envelope decode path — a 409
				// without a parseable preview body is still a hard error.
				return nil, wrapHTTPEnvelopeError(resp.StatusCode, raw)
			}
		}
		return &SyncSetupResult{
			Mode:              out.Mode,
			Init:              out.Init,
			Clone:             out.Clone,
			PreviewCollisions: out.PreviewCollisions,
		}, ErrSetupCollision
	default:
		return nil, wrapHTTPEnvelopeError(resp.StatusCode, raw)
	}
}

// wrapHTTPEnvelopeError parses the standard {error, code, details}
// envelope into an *HTTPError; falls back to the raw body when the
// envelope doesn't parse. Matches the tail of `remoteClient.do`.
func wrapHTTPEnvelopeError(status int, raw []byte) error {
	herr := &HTTPError{Status: status}
	if len(raw) > 0 {
		var env errorBody
		if jerr := json.Unmarshal(raw, &env); jerr == nil {
			herr.Code = env.Code
			herr.Message = env.Error
			herr.Details = env.Details
		} else {
			herr.Message = strings.TrimSpace(string(raw))
		}
	}
	return wrapStoreError(herr)
}

