package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/store"
)

// remoteClient drives a *bacio api* server over HTTP. The server stamps
// audit-log rows itself, so this client never calls RecordHistory.
type remoteClient struct {
	httpClient *http.Client
	base       *url.URL
	token      string
	actor      string
}

func newRemoteClient(opts Options) (*remoteClient, error) {
	raw := strings.TrimSpace(opts.Remote)
	if raw == "" {
		return nil, errors.New("remote URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid remote URL %q: %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid remote URL %q: must include scheme and host", raw)
	}
	return &remoteClient{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		base:       u,
		token:      opts.Token,
		actor:      opts.Actor,
	}, nil
}

func (c *remoteClient) Mode() string { return ModeRemote }
func (c *remoteClient) Close() error { return nil }

// errorBody is the API error envelope shape.
type errorBody struct {
	Error   string         `json:"error"`
	Code    string         `json:"code"`
	Details map[string]any `json:"details,omitempty"`
}

// HTTPError wraps a non-2xx response. Status preserves the HTTP code so
// callers can branch on 404 vs 409 etc.
type HTTPError struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// IsNotFound reports whether err wraps an HTTP 404. Used by the local
// auto-register flow's remote analogue.
func IsNotFound(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusNotFound
	}
	return false
}

// do issues a request, applies auth + actor headers, and parses the
// envelope into either body (on 2xx) or *HTTPError (otherwise). The
// caller writes the body to a streaming response by passing a non-nil
// rawSink instead of body.
func (c *remoteClient) do(ctx context.Context, method, path string, query url.Values, in any, out any) error {
	u := *c.base
	setURLPath(&u, path)
	if query != nil {
		u.RawQuery = query.Encode()
	}
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.actor != "" {
		req.Header.Set("X-Actor", c.actor)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out == nil || len(raw) == 0 {
			return nil
		}
		// Allow assigning the raw bytes directly when out is *[]byte.
		if sink, ok := out.(*[]byte); ok {
			*sink = raw
			return nil
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
		return nil
	}
	herr := &HTTPError{Status: resp.StatusCode}
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

// wrapStoreError keeps callers compatible with errors.Is(err, store.ErrNotFound)
// even when the underlying error is an HTTPError. Returned error wraps the
// HTTPError so callers that branch on HTTP status still can.
func wrapStoreError(he *HTTPError) error {
	if he.Status == http.StatusNotFound {
		return &notFoundError{HTTPError: *he}
	}
	return he
}

type notFoundError struct {
	HTTPError
}

func (e *notFoundError) Unwrap() error { return store.ErrNotFound }

// setURLPath sets u.Path AND u.RawPath so that callers may pre-escape path
// segments with url.PathEscape without Go's url package double-encoding the
// literal '%' on the way out. Path is the unescaped form (decoded for
// callers' convenience); RawPath is the on-the-wire form. We always set
// both to keep them consistent — Go's URL.String() honours RawPath only
// when it is a valid encoding of Path.
func setURLPath(u *url.URL, escaped string) {
	full := strings.TrimRight(u.Path, "/") + escaped
	if unescaped, err := url.PathUnescape(full); err == nil {
		u.Path = unescaped
		u.RawPath = full
	} else {
		u.Path = full
	}
}

