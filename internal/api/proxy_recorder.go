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

	"github.com/mrgeoffrich/bacio/internal/anthropic"
	"github.com/mrgeoffrich/bacio/internal/model"
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

// persist does the off-path work for one observation: render the raw req/resp
// capture, write it to disk (best-effort), insert the index row, then — for a
// parseable Anthropic SSE capture — parse it into per-job message detail. Every
// failure is logged, not returned — capture must never break the proxy.
func (r *captureRecorder) persist(obs proxy.RequestObservation) {
	started := obs.Started
	if started.IsZero() {
		started = time.Now()
	}

	// Render once and reuse: the on-disk file and the BACI-306 parser both read
	// the same inflated, redacted .http bytes, so the parser sees exactly what a
	// reader of the file would.
	rendered := renderRawCapture(obs)
	rawPath := r.writeRaw(rendered, started)

	if r.store == nil {
		return
	}
	// BACI-305 classification (transport-level, no body parsing): the base
	// Content-Type, whether the response was an SSE stream, and whether this
	// is an Anthropic message-API capture. These let BACI-306's per-job
	// parser select exactly the parseable substrate.
	contentType := baseContentType(obs.ResponseContentType)
	isStream := contentType == "text/event-stream"
	isAnthropic := isAnthropicCapture(obs.Host, obs.Path)
	dispatchID := r.resolveDispatch(obs.ClaudeSessionID, obs.ClaudeAgentID)
	pr, err := r.store.AddProxyRequest(store.AddProxyRequestIn{
		Method:        obs.Method,
		Host:          obs.Host,
		Path:          obs.Path,
		Status:        obs.Status,
		BytesIn:       obs.BytesIn,
		BytesOut:      obs.BytesOut,
		Duration:      obs.Duration,
		RawLogPath:    rawPath,
		StartedAt:     obs.Started,
		EndedAt:       obs.Ended,
		ContentType:   contentType,
		IsStream:      isStream,
		IsAnthropic:   isAnthropic,
		SessionID:     obs.ClaudeSessionID,
		ClaudeAgentID: obs.ClaudeAgentID,
		DispatchID:    dispatchID,
	})
	if err != nil {
		r.logger.Error("proxy capture: index insert failed",
			"err", err, "method", obs.Method, "host", obs.Host, "path", obs.Path)
		return
	}

	// BACI-306: parse the parseable Anthropic SSE captures into per-job message
	// detail. A truncated response can't be parsed (its gzip stream — every SSE
	// response is gzipped — is incomplete and was written verbatim), so skip it;
	// the non-stream count_tokens / error JSON shapes aren't message transcripts.
	if isAnthropic && isStream && !obs.ResponseTruncated {
		r.parseMessage(pr, rendered, dispatchID, started)
	}
}

// parseMessage reconstructs one capture's assistant turn, classifies it against
// the job's last thread state (primary thread vs auxiliary probe), and persists
// it. It shares the pure parse+classify glue (anthropic.ParseAndClassify) with the
// BACI-321 backfill sweep so the live and backfill paths can't drift. Best-effort:
// a parse failure or a store hiccup is logged and swallowed so a malformed capture
// never breaks the capture path. On a parse miss it stamps proxy_requests.parse_failed_at
// — the same terminal-failure marker the backfill writes — so the sweep doesn't
// re-read an unparseable capture's file every minute.
func (r *captureRecorder) parseMessage(pr *model.ProxyRequest, rendered []byte, dispatchID *int64, started time.Time) {
	prev, err := r.store.LatestThreadState(dispatchID)
	if err != nil {
		r.logger.Debug("proxy capture: thread-state lookup failed — assuming fresh thread",
			"proxy_request_id", pr.ID, "err", err)
		prev = anthropic.ThreadState{}
	}
	pc, isPrimary, delta, err := anthropic.ParseAndClassify(rendered, prev)
	if err != nil {
		// ErrNotStream is an expected defensive miss; a real decode error is
		// worth a debug line but never fatal. Either way this capture can't yield
		// a row, so stamp the terminal-failure marker the backfill keys off.
		r.logger.Debug("proxy capture: parse failed — skipping message detail",
			"proxy_request_id", pr.ID, "err", err)
		r.markParseFailed(pr.ID)
		return
	}
	if _, err := r.store.AddProxyMessage(store.AddProxyMessageIn{
		ProxyRequestID: pr.ID,
		DispatchID:     dispatchID,
		SessionID:      pr.SessionID,
		ClaudeAgentID:  pr.ClaudeAgentID,
		Capture:        pc,
		Delta:          delta,
		IsPrimary:      isPrimary,
		StartedAt:      started,
	}); err != nil {
		r.logger.Error("proxy capture: message insert failed",
			"err", err, "proxy_request_id", pr.ID)
	}
}

// markParseFailed stamps proxy_requests.parse_failed_at on a capture the live
// parse couldn't turn into a message row (BACI-321). Best-effort like the rest of
// capture: a store hiccup is logged at debug and swallowed — the worst case is the
// backfill sweep re-attempts the same unparseable capture once.
func (r *captureRecorder) markParseFailed(proxyRequestID int64) {
	if err := r.store.MarkProxyRequestParseFailed(proxyRequestID); err != nil {
		r.logger.Debug("proxy capture: mark parse-failed failed",
			"proxy_request_id", proxyRequestID, "err", err)
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

// resolveDispatch attributes a capture to a dispatch from Claude Code's own
// request ids. The per-subagent claudeAgentID is the precise key — subagents
// share the supervisor session id, so only the agent id can pin a request to
// one dispatch; it resolves through the agent-id→dispatch binding written when
// the worker starts. Failing that (no binding yet, or a non-subagent request)
// it falls back to the session's active dispatch. Best-effort: a miss yields
// nil — the capture still lands with session_id + claude_agent_id stored raw,
// so a later read can still join. Store errors are swallowed (logged at debug)
// so a lookup hiccup never breaks the capture.
func (r *captureRecorder) resolveDispatch(sessionID, claudeAgentID string) *int64 {
	if r.store == nil {
		return nil
	}
	if claudeAgentID != "" {
		id, err := r.store.DispatchIDByClaudeAgentID(claudeAgentID)
		if err != nil {
			r.logger.Debug("proxy capture: agent-id dispatch lookup failed",
				"agent_id", claudeAgentID, "err", err)
		} else if id != nil {
			return id
		}
	}
	if sessionID == "" {
		return nil
	}
	disp, err := r.store.ActiveDispatchForSession(sessionID)
	if err != nil {
		r.logger.Debug("proxy capture: active dispatch lookup failed",
			"session", sessionID, "err", err)
		return nil
	}
	if disp == nil {
		return nil
	}
	id := disp.ID
	return &id
}

// writeRaw writes the pre-rendered raw request + response capture to a file
// under <logDir>/proxy/<date>/, returning the absolute path written (or "" on
// any failure / when no log dir is configured). The bytes are rendered by the
// caller (renderRawCapture) so the on-disk file and the BACI-306 parser share
// one render. The directory is created on demand (mkdir -p); a write failure is
// logged once and swallowed so the index row still lands without a file reference.
func (r *captureRecorder) writeRaw(rendered []byte, started time.Time) string {
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
	if err := os.WriteFile(path, rendered, 0o644); err != nil {
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
