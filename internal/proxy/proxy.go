// Package proxy stands up the reverse-proxy forwarding pipe that agent
// HTTP(S) traffic routes through. BACI-301 shipped the transparent
// forwarder: a single httputil.ReverseProxy that forwards everything under
// the /anthropic/ prefix to the Anthropic upstream over real TLS, streaming
// SSE token responses without buffering and rewriting the Host header so the
// upstream's TLS SNI / vhost routing sees api.anthropic.com.
//
// BACI-302 adds the observation layer: a custom http.RoundTripper tees the
// request and response bodies, times the round-trip, counts bytes each way,
// and hands a RequestObservation to a Recorder once the response stream
// closes. The capture is transport-level only (no Anthropic body parsing —
// that is BACI-305/306) and MUST NOT buffer or delay the streamed bytes: the
// tee forwards every byte downstream immediately and accumulates the raw copy
// alongside, finalising (file write + DB insert) off the request path inside
// the Recorder implementation.
package proxy

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// DefaultUpstream is the Anthropic API base the reverse proxy forwards
// to when no explicit upstream is configured. Real TLS, system roots.
const DefaultUpstream = "https://api.anthropic.com"

// PathPrefix is the route prefix the proxy is mounted at on the bacio
// api/web server. Requests arrive as /anthropic/v1/messages; the prefix
// is stripped before forwarding so the upstream sees /v1/messages.
const PathPrefix = "/anthropic"

// Claude Code stamps its own correlation ids on every Anthropic request, so
// the capture lifts them straight off the wire — no bacio header injection,
// no worktree slug. claudeSessionHeader is the supervisor session id (maps to
// agent_sessions.session_id); claudeAgentHeader is the per-subagent id, present
// only on a Task-spawned subagent's requests (subagents share the session id,
// so this is what pins a request to a specific dispatch). Both are Claude's own
// headers already bound for Anthropic — the capture reads them but does NOT
// strip them (unlike the retired X-Bacio-Corr), so the upstream request is
// untouched.
const (
	claudeSessionHeader = "X-Claude-Code-Session-Id"
	claudeAgentHeader   = "X-Claude-Code-Agent-Id"
)

// New returns an http.Handler that reverse-proxies every request to
// upstream. It strips the PathPrefix, rewrites the Host header to the
// upstream host (load-bearing for SNI/vhost — SetURL only sets
// Out.URL.Host), and flushes immediately so SSE streams aren't buffered.
// A dial/transport failure surfaces as a clean 502 rather than a panic.
//
// rec observes every request (BACI-302). A nil rec installs nopRecorder so
// the capture path is always present but inert. Capture never buffers or
// delays the streamed response — see captureTransport.
func New(upstream *url.URL, logger *slog.Logger, rec Recorder) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if rec == nil {
		rec = nopRecorder{}
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// SetURL routes to the upstream scheme/host and path-joins the
			// upstream's base path with the *outbound* request path
			// (cloned from pr.In), then rewrites pr.Out.Host to the
			// upstream host (load-bearing for SNI/vhost). It reads pr.Out,
			// not pr.In, so the /anthropic strip has to happen on
			// pr.Out.URL afterwards.
			pr.SetURL(upstream)
			// Strip the /anthropic prefix so /anthropic/v1/messages reaches
			// the upstream as /v1/messages — never doubled, never carrying
			// the prefix. RawPath is only set when the path needs escaping;
			// trim it in lockstep so an escaped path stays consistent.
			pr.Out.URL.Path = strings.TrimPrefix(pr.Out.URL.Path, PathPrefix)
			if pr.Out.URL.RawPath != "" {
				pr.Out.URL.RawPath = strings.TrimPrefix(pr.Out.URL.RawPath, PathPrefix)
			}
		},
		// -1 flushes after every write so SSE token streams arrive
		// incrementally rather than buffering to EOF.
		FlushInterval: -1,
		// BACI-302: the capture transport tees both bodies + times the
		// round-trip. Left at http.DefaultTransport semantics (TLS
		// verification on, system root CAs) for the actual HTTP exchange.
		Transport: &captureTransport{base: http.DefaultTransport, rec: rec},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Error("proxy upstream error",
				"err", err,
				"method", r.Method,
				"path", r.URL.Path,
				"upstream", upstream.String(),
			)
			http.Error(w, "bad gateway", http.StatusBadGateway)
		},
	}
	return rp
}

// captureTransport wraps a base RoundTripper to observe each request without
// disturbing the stream. It tees the outbound request body, times the
// round-trip, and wraps the response body in a counting + teeing reader whose
// Close finalises the observation and hands it to the recorder. SSE-safe:
// every response byte is forwarded downstream immediately; the raw copy is
// accumulated alongside and the recorder does its disk/DB work off the
// request path.
type captureTransport struct {
	base http.RoundTripper
	rec  Recorder
}

func (t *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	started := time.Now()

	// Lift Claude Code's own correlation ids off the inbound request. Unlike
	// the retired X-Bacio-Corr these are Claude's own headers already bound
	// for Anthropic, so we read but never strip them — the upstream request is
	// untouched and they stay visible in the raw .http capture.
	claudeSessionID := req.Header.Get(claudeSessionHeader)
	claudeAgentID := req.Header.Get(claudeAgentHeader)

	// Tee the request body as it's read by the transport: the captured copy
	// accumulates into reqBuf (capped) while every byte still reaches the
	// upstream. Requests with no body (GET) skip the tee.
	var reqBuf *cappedBuffer
	if req.Body != nil {
		reqBuf = newCappedBuffer(MaxCapturedBody)
		req.Body = &teeReadCloser{rc: req.Body, tee: reqBuf}
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		// The round-trip failed before any response — record a status-0
		// observation so a failed upstream still shows up in the index.
		// The ReverseProxy's ErrorHandler surfaces the 502 to the client.
		obs := RequestObservation{
			Method:             req.Method,
			Host:               req.URL.Host,
			Path:               req.URL.Path,
			Status:             0,
			Started:            started,
			Ended:              time.Now(),
			Duration:           time.Since(started),
			RequestHeaderBlock: headerBlock(req.Method+" "+req.URL.RequestURI()+" "+req.Proto, req.Header),
			ClaudeSessionID:    claudeSessionID,
			ClaudeAgentID:      claudeAgentID,
		}
		if reqBuf != nil {
			obs.BytesIn = reqBuf.total
			obs.RawRequest = reqBuf.bytes()
			obs.RequestTruncated = reqBuf.truncated
		}
		t.rec.Record(obs)
		return nil, err
	}

	// Wrap the response body so byte counting + raw teeing happen as the
	// stream is read downstream, and the recorder fires exactly once on
	// Close. The original response body is closed by the wrapper.
	respBuf := newCappedBuffer(MaxCapturedBody)
	statusLine := resp.Proto + " " + resp.Status
	resp.Body = &captureBody{
		rc:              resp.Body,
		tee:             respBuf,
		onClose:         t.rec.Record,
		started:         started,
		method:          req.Method,
		host:            req.URL.Host,
		path:            req.URL.Path,
		status:          resp.StatusCode,
		bytesIn:         reqBufTotal(reqBuf),
		rawRequest:      reqBufBytes(reqBuf),
		reqTrunc:        reqBufTruncated(reqBuf),
		reqHeaders:      headerBlock(req.Method+" "+req.URL.RequestURI()+" "+req.Proto, req.Header),
		respHeaders:     headerBlock(statusLine, resp.Header),
		respContentType: resp.Header.Get("Content-Type"),
		respEncoding:    resp.Header.Get("Content-Encoding"),
		claudeSessionID: claudeSessionID,
		claudeAgentID:   claudeAgentID,
	}
	return resp, nil
}

// reqBufTotal / reqBufBytes / reqBufTruncated are nil-safe accessors so the
// response-path observation can fold in the request-body capture without a
// nil check at every call site (GET requests have no request buffer).
func reqBufTotal(b *cappedBuffer) int64 {
	if b == nil {
		return 0
	}
	return b.total
}

func reqBufBytes(b *cappedBuffer) []byte {
	if b == nil {
		return nil
	}
	return b.bytes()
}

func reqBufTruncated(b *cappedBuffer) bool {
	if b == nil {
		return false
	}
	return b.truncated
}

// captureBody wraps the upstream response body. Read forwards every byte
// downstream and accumulates a capped raw copy + a running byte count. Close
// closes the upstream body, then fires the recorder exactly once with the
// finalised observation. The recorder is responsible for doing its work off
// the request path (the proxy's reason to exist is low-latency streaming).
type captureBody struct {
	rc      io.ReadCloser
	tee     *cappedBuffer
	onClose func(RequestObservation)

	started time.Time
	method  string
	host    string
	path    string
	status  int

	bytesIn    int64
	rawRequest []byte
	reqTrunc   bool
	reqHeaders string

	respHeaders     string
	respContentType string
	respEncoding    string
	claudeSessionID string
	claudeAgentID   string

	bytesOut int64
	fired    bool
}

func (b *captureBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.bytesOut += int64(n)
		b.tee.Write(p[:n])
	}
	return n, err
}

func (b *captureBody) Close() error {
	err := b.rc.Close()
	// Guard against a double-Close firing the recorder twice (the http
	// stack can call Close more than once on the body).
	if !b.fired {
		b.fired = true
		ended := time.Now()
		b.onClose(RequestObservation{
			Method:                  b.method,
			Host:                    b.host,
			Path:                    b.path,
			Status:                  b.status,
			BytesIn:                 b.bytesIn,
			BytesOut:                b.bytesOut,
			Started:                 b.started,
			Ended:                   ended,
			Duration:                ended.Sub(b.started),
			RawRequest:              b.rawRequest,
			RawResponse:             b.tee.bytes(),
			RequestTruncated:        b.reqTrunc,
			ResponseTruncated:       b.tee.truncated,
			RequestHeaderBlock:      b.reqHeaders,
			ResponseHeaderBlock:     b.respHeaders,
			ResponseContentType:     b.respContentType,
			ResponseContentEncoding: b.respEncoding,
			ClaudeSessionID:         b.claudeSessionID,
			ClaudeAgentID:           b.claudeAgentID,
		})
	}
	return err
}

// teeReadCloser forwards reads from rc while copying the bytes into tee. Used
// for the request body so the captured copy accumulates as the transport
// streams the body upstream — never buffering ahead, never delaying the send.
type teeReadCloser struct {
	rc  io.ReadCloser
	tee *cappedBuffer
}

func (t *teeReadCloser) Read(p []byte) (int, error) {
	n, err := t.rc.Read(p)
	if n > 0 {
		t.tee.Write(p[:n])
	}
	return n, err
}

func (t *teeReadCloser) Close() error { return t.rc.Close() }

// cappedBuffer accumulates bytes up to cap while tracking the TOTAL bytes
// seen (so a truncated capture still reports the true streamed size) and a
// truncated flag. It never errors and never blocks — Write always "succeeds"
// from the caller's view; bytes past the cap are simply not retained.
type cappedBuffer struct {
	buf       bytes.Buffer
	cap       int
	total     int64
	truncated bool
}

func newCappedBuffer(cap int) *cappedBuffer {
	return &cappedBuffer{cap: cap}
}

func (c *cappedBuffer) Write(p []byte) {
	c.total += int64(len(p))
	remaining := c.cap - c.buf.Len()
	if remaining <= 0 {
		if len(p) > 0 {
			c.truncated = true
		}
		return
	}
	if len(p) > remaining {
		c.buf.Write(p[:remaining])
		c.truncated = true
		return
	}
	c.buf.Write(p)
}

func (c *cappedBuffer) bytes() []byte { return c.buf.Bytes() }

// headerBlock renders an HTTP start line plus headers as a raw text block for
// the on-disk capture — the request line / status line followed by one
// "Key: value" per header. Auth-bearing headers are redacted so the raw
// capture file never persists an Anthropic API key.
func headerBlock(startLine string, h http.Header) string {
	var sb strings.Builder
	sb.WriteString(startLine)
	sb.WriteString("\r\n")
	for k, vs := range h {
		for _, v := range vs {
			if isSensitiveHeader(k) {
				v = "[redacted]"
			}
			sb.WriteString(k)
			sb.WriteString(": ")
			sb.WriteString(v)
			sb.WriteString("\r\n")
		}
	}
	return sb.String()
}

// isSensitiveHeader reports whether a header carries a secret that must not
// land in the raw capture file. Agent traffic carries its own Anthropic auth
// (x-api-key / authorization), which the capture has no business persisting.
func isSensitiveHeader(k string) bool {
	switch strings.ToLower(k) {
	case "authorization", "x-api-key", "proxy-authorization", "cookie", "set-cookie":
		return true
	}
	// Claude Code's own correlation headers (X-Claude-Code-Session-Id /
	// -Agent-Id) are NOT secrets — they're the substrate the recorder reads
	// to attribute a capture, and they're already sent to Anthropic — so they
	// stay visible in the raw .http block.
	return false
}
