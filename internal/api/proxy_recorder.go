package api

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mrgeoffrich/bacio/internal/proxy"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// anthropicHost is the upstream host a capture must target to count as an
// Anthropic message-API request (BACI-305 classification). The observation's
// Host is the post-rewrite upstream host, so it's api.anthropic.com for
// traffic that flowed through the /anthropic/* proxy to the real API.
const anthropicHost = "api.anthropic.com"

// proxyCaptureQueueDepth bounds the recorder's hand-off channel. Capture is
// off the request path, so the worker draining the channel can fall behind a
// burst of traffic; the buffer absorbs a reasonable burst, and Record drops
// (with one warning) rather than blocking the proxy when the buffer is full.
// The streamed bytes have already reached the agent by the time Record runs,
// so a dropped observation costs an index row, never a request.
const proxyCaptureQueueDepth = 256

// captureRecorder is the production proxy.Recorder. It writes the raw
// req/resp bytes to a per-day file under the resolved log dir and inserts a
// lightweight index row into proxy_requests — both off the request path,
// drained by a single worker goroutine so the proxy's streaming hot path is
// never gated on a disk flush or a DB write.
//
// Failures are swallowed (logged once, never panicked): a capture is a
// best-effort observation, so a full disk or a DB hiccup must never break the
// agent's traffic. A failed file write still inserts the index row (with an
// empty raw_log_path) so the request is at least counted.
type captureRecorder struct {
	store  *store.Store
	logDir string
	logger *slog.Logger

	ch chan proxy.RequestObservation

	startOnce sync.Once
	// closed by Close so tests can drain deterministically; nil in
	// production (the recorder lives for the process lifetime).
	done chan struct{}
}

// newCaptureRecorder constructs the recorder and starts its worker. logDir is
// the resolved per-worktree log directory (logging.Resolved.Dir); an empty
// logDir disables the raw-to-disk write but still inserts index rows. A nil
// store makes Record a no-op apart from the file write — defensive, since the
// router always passes a real store.
func newCaptureRecorder(s *store.Store, logDir string, logger *slog.Logger) *captureRecorder {
	if logger == nil {
		logger = slog.Default()
	}
	r := &captureRecorder{
		store:  s,
		logDir: logDir,
		logger: logger,
		ch:     make(chan proxy.RequestObservation, proxyCaptureQueueDepth),
		done:   make(chan struct{}),
	}
	r.start()
	return r
}

func (r *captureRecorder) start() {
	r.startOnce.Do(func() {
		go r.run()
	})
}

// Record enqueues the observation for the worker. Non-blocking: if the queue
// is full the observation is dropped with a single warning rather than
// stalling the proxy's response Close. Satisfies proxy.Recorder.
func (r *captureRecorder) Record(obs proxy.RequestObservation) {
	select {
	case r.ch <- obs:
	default:
		r.logger.Warn("proxy capture queue full — dropping observation",
			"method", obs.Method, "host", obs.Host, "path", obs.Path)
	}
}

func (r *captureRecorder) run() {
	for obs := range r.ch {
		r.persist(obs)
	}
	if r.done != nil {
		close(r.done)
	}
}

// Close stops the worker and waits for the in-flight queue to drain. Optional
// in production (the recorder lives for the process lifetime); tests call it
// to make assertions deterministic after enqueuing observations.
func (r *captureRecorder) Close() {
	close(r.ch)
	if r.done != nil {
		<-r.done
	}
}

// persist does the off-path work for one observation: write the raw req/resp
// file (best-effort) then insert the index row. Both failures are logged, not
// returned — capture must never break the proxy.
func (r *captureRecorder) persist(obs proxy.RequestObservation) {
	started := obs.Started
	if started.IsZero() {
		started = time.Now()
	}

	rawPath := r.writeRaw(obs, started)

	if r.store == nil {
		return
	}
	// BACI-305 classification (transport-level, no body parsing): the base
	// Content-Type, whether the response was an SSE stream, and whether this
	// is an Anthropic message-API capture. These let BACI-306's per-job
	// parser select exactly the parseable substrate.
	contentType := baseContentType(obs.ResponseContentType)
	sessionID, dispatchID := r.resolveCorrelation(obs.CorrelationKey)
	if _, err := r.store.AddProxyRequest(store.AddProxyRequestIn{
		Method:      obs.Method,
		Host:        obs.Host,
		Path:        obs.Path,
		Status:      obs.Status,
		BytesIn:     obs.BytesIn,
		BytesOut:    obs.BytesOut,
		Duration:    obs.Duration,
		RawLogPath:  rawPath,
		StartedAt:   obs.Started,
		EndedAt:     obs.Ended,
		ContentType: contentType,
		IsStream:    contentType == "text/event-stream",
		IsAnthropic: isAnthropicCapture(obs.Host, obs.Path),
		SessionID:   sessionID,
		DispatchID:  dispatchID,
	}); err != nil {
		r.logger.Error("proxy capture: index insert failed",
			"err", err, "method", obs.Method, "host", obs.Host, "path", obs.Path)
	}
}

// baseContentType lowercases a Content-Type header value and drops any
// parameters (e.g. "text/event-stream; charset=utf-8" → "text/event-stream"),
// so classification compares the bare media type. Empty in → empty out.
func baseContentType(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}

// isAnthropicCapture reports whether a capture targets the Anthropic message
// API: the post-rewrite upstream host is api.anthropic.com and the path is
// under /v1/. This is the predicate BACI-306's parser keys off to find its
// substrate — host+path only, no body inspection.
func isAnthropicCapture(host, path string) bool {
	return host == anthropicHost && strings.HasPrefix(path, "/v1/")
}

// resolveCorrelation maps the BACI-305 correlation key (the worktree slug
// stamped on the request via X-Bacio-Corr) back to the worktree's active
// session and dispatch. Best-effort: an empty key, an unknown slug, or a
// slug with no active dispatch yields ("", nil) — the capture + classification
// still land, the correlation columns just stay empty. Any store error is
// swallowed (logged at debug) so a lookup hiccup never breaks the capture.
func (r *captureRecorder) resolveCorrelation(key string) (string, *int64) {
	if key == "" || r.store == nil {
		return "", nil
	}
	sess, err := r.store.LatestActiveSessionBySlug(key)
	if err != nil {
		r.logger.Debug("proxy capture: session lookup by slug failed", "slug", key, "err", err)
		return "", nil
	}
	if sess == nil {
		return "", nil
	}
	disp, err := r.store.ActiveDispatchForSession(sess.SessionID)
	if err != nil {
		r.logger.Debug("proxy capture: active dispatch lookup failed", "session", sess.SessionID, "err", err)
		// Still attribute to the session even without a dispatch.
		return sess.SessionID, nil
	}
	if disp == nil {
		return sess.SessionID, nil
	}
	id := disp.ID
	return sess.SessionID, &id
}

// writeRaw writes the raw request + response capture for one observation to a
// file under <logDir>/proxy/<date>/, returning the absolute path written (or
// "" on any failure / when no log dir is configured). The directory is
// created on demand (mkdir -p); a write failure is logged once and swallowed
// so the index row still lands without a file reference.
func (r *captureRecorder) writeRaw(obs proxy.RequestObservation, started time.Time) string {
	if r.logDir == "" {
		return ""
	}
	dir := filepath.Join(r.logDir, "proxy", started.Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.logger.Warn("proxy capture: create dir failed — index row without raw file",
			"dir", dir, "err", err)
		return ""
	}
	// A monotonic-enough filename: the start timestamp to nanosecond
	// precision keeps concurrent captures from colliding without needing a
	// pre-assigned id (the index row's id isn't known until after the insert).
	name := strconv.FormatInt(started.UnixNano(), 10) + ".http"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, renderRawCapture(obs), 0o644); err != nil {
		r.logger.Warn("proxy capture: write raw file failed — index row without raw file",
			"path", path, "err", err)
		return ""
	}
	return path
}

// renderRawCapture lays out one observation as a raw .http capture: the
// request header block + body, a separator, then the response header block +
// body. Truncated bodies carry an explicit marker so a reader knows the
// capture is partial.
//
// BACI-305: a gzip-encoded response body is DECODED before it's written to
// disk, so the on-disk raw copy is the readable JSON the BACI-306 parser
// consumes (Anthropic non-stream replies arrive gzipped because the Claude
// client sets its own Accept-Encoding, which the proxy forwards verbatim —
// Go's transport only auto-decompresses the gzip it requested itself). The
// bytes forwarded to the agent are untouched; only this captured copy is
// inflated. A truncated gzip body can't be inflated, so it's written verbatim
// with a marker.
func renderRawCapture(obs proxy.RequestObservation) []byte {
	var b []byte
	b = append(b, "==== REQUEST ====\r\n"...)
	b = append(b, obs.RequestHeaderBlock...)
	b = append(b, "\r\n"...)
	b = append(b, obs.RawRequest...)
	if obs.RequestTruncated {
		b = append(b, fmt.Sprintf("\r\n[... request body truncated at %d bytes ...]\r\n", proxy.MaxCapturedBody)...)
	}
	b = append(b, "\r\n==== RESPONSE ====\r\n"...)
	b = append(b, obs.ResponseHeaderBlock...)
	b = append(b, "\r\n"...)
	respBody, gzipNote := responseCaptureBody(obs)
	b = append(b, respBody...)
	if gzipNote != "" {
		b = append(b, gzipNote...)
	}
	if obs.ResponseTruncated {
		b = append(b, fmt.Sprintf("\r\n[... response body truncated at %d bytes ...]\r\n", proxy.MaxCapturedBody)...)
	}
	return b
}

// responseCaptureBody returns the response bytes to write to the .http file
// plus an optional trailing note. For a gzip-encoded, non-truncated body it
// returns the inflated bytes; for a truncated gzip body (which can't be
// inflated — the gzip stream is incomplete) it returns the raw bytes plus a
// marker so a reader knows the capture is compressed-and-partial. A non-gzip
// (or unencoded) body is returned verbatim with no note.
func responseCaptureBody(obs proxy.RequestObservation) (body []byte, note string) {
	if !strings.EqualFold(obs.ResponseContentEncoding, "gzip") {
		return obs.RawResponse, ""
	}
	if obs.ResponseTruncated {
		return obs.RawResponse, "\r\n[gzip body truncated — not decoded]\r\n"
	}
	decoded, ok := gunzipCapture(obs.RawResponse)
	if !ok {
		// Corrupt / unexpected gzip stream — fall back to verbatim rather
		// than drop the body, and flag it so a reader isn't surprised by
		// compressed bytes.
		return obs.RawResponse, "\r\n[gzip body could not be decoded — written verbatim]\r\n"
	}
	return decoded, ""
}

// gunzipCapture inflates a complete gzip stream, returning (decoded, true) on
// success or (nil, false) when the bytes aren't a valid/complete gzip stream.
// Used only for the on-disk raw copy — the streamed bytes the agent received
// were never touched.
func gunzipCapture(raw []byte) ([]byte, bool) {
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, false
	}
	defer zr.Close()
	decoded, err := io.ReadAll(zr)
	if err != nil {
		return nil, false
	}
	return decoded, true
}
