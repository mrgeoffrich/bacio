package api

import (
	"bytes"
	"compress/gzip"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/proxy"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// gzipFixture returns the gzip-compressed form of in for the decode tests.
func gzipFixture(t *testing.T, in []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(in); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func recorderTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestCaptureRecorder_WritesRawAndIndexRow asserts that after Record drains,
// a raw .http file exists under <logDir>/proxy/<date>/ and a proxy_requests
// index row landed pointing at it.
func TestCaptureRecorder_WritesRawAndIndexRow(t *testing.T) {
	s := recorderTestStore(t)
	logDir := t.TempDir()
	rec := newCaptureRecorder(s, logDir, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	started := time.Now()
	rec.Record(proxy.RequestObservation{
		Method:              "POST",
		Host:                "api.anthropic.com",
		Path:                "/v1/messages",
		Status:              200,
		BytesIn:             10,
		BytesOut:            20,
		Started:             started,
		Ended:               started.Add(50 * time.Millisecond),
		Duration:            50 * time.Millisecond,
		RawRequest:          []byte("req-body"),
		RawResponse:         []byte("resp-body"),
		RequestHeaderBlock:  "POST /v1/messages HTTP/1.1\r\n",
		ResponseHeaderBlock: "HTTP/1.1 200 OK\r\n",
	})
	rec.Close() // drains the queue and stops the worker

	// Index row landed.
	rows, err := s.ListProxyRequests(0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d index rows, want 1", len(rows))
	}
	row := rows[0]
	if row.Method != "POST" || row.Path != "/v1/messages" || row.Status != 200 {
		t.Fatalf("index row fields wrong: %+v", row)
	}
	if row.RawLogPath == "" {
		t.Fatalf("index row has no raw_log_path")
	}

	// The raw file exists under the date dir and contains the bodies.
	wantDir := filepath.Join(logDir, "proxy", started.Format("2006-01-02"))
	if filepath.Dir(row.RawLogPath) != wantDir {
		t.Fatalf("raw file dir = %q, want %q", filepath.Dir(row.RawLogPath), wantDir)
	}
	raw, err := os.ReadFile(row.RawLogPath)
	if err != nil {
		t.Fatalf("read raw file: %v", err)
	}
	for _, want := range []string{"req-body", "resp-body", "==== REQUEST ====", "==== RESPONSE ===="} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Fatalf("raw file missing %q:\n%s", want, raw)
		}
	}
}

// TestCaptureRecorder_SwallowsWriteError asserts a raw-file write failure is
// swallowed (no panic) and the index row still lands without a raw_log_path —
// capture must never break the proxy. We force the failure by pointing logDir
// at a path that already exists as a FILE, so MkdirAll under it fails.
func TestCaptureRecorder_SwallowsWriteError(t *testing.T) {
	s := recorderTestStore(t)
	// A regular file standing where the log dir should be: MkdirAll of a
	// subpath under it must fail.
	notADir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	rec := newCaptureRecorder(s, notADir, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	rec.Record(proxy.RequestObservation{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages",
		Status: 200, Started: time.Now(), Ended: time.Now(),
	})
	rec.Close()

	rows, err := s.ListProxyRequests(0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d index rows, want 1 (the row must land even when the file write fails)", len(rows))
	}
	if rows[0].RawLogPath != "" {
		t.Fatalf("raw_log_path = %q, want empty after a write failure", rows[0].RawLogPath)
	}
}

// TestCaptureRecorder_EmptyLogDirSkipsFile asserts an empty log dir disables
// the raw-to-disk write while the index row still lands.
func TestCaptureRecorder_EmptyLogDirSkipsFile(t *testing.T) {
	s := recorderTestStore(t)
	rec := newCaptureRecorder(s, "", slog.New(slog.NewTextHandler(os.Stderr, nil)))
	rec.Record(proxy.RequestObservation{
		Method: "GET", Host: "api.anthropic.com", Path: "/v1/messages",
		Status: 200, Started: time.Now(), Ended: time.Now(),
	})
	rec.Close()

	rows, err := s.ListProxyRequests(0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d index rows, want 1", len(rows))
	}
	if rows[0].RawLogPath != "" {
		t.Fatalf("raw_log_path = %q, want empty when no log dir is configured", rows[0].RawLogPath)
	}
}

// TestCaptureRecorder_DecodesGzipResponse asserts a gzip-Content-Encoding
// response body is written DECODED to the on-disk .http file, so the BACI-306
// parser reads plain JSON rather than compressed bytes (BACI-305).
func TestCaptureRecorder_DecodesGzipResponse(t *testing.T) {
	s := recorderTestStore(t)
	logDir := t.TempDir()
	rec := newCaptureRecorder(s, logDir, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	const plain = `{"type":"message","role":"assistant"}`
	rec.Record(proxy.RequestObservation{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages", Status: 200,
		Started: time.Now(), Ended: time.Now(),
		RawResponse:             gzipFixture(t, []byte(plain)),
		ResponseContentType:     "application/json",
		ResponseContentEncoding: "gzip",
	})
	rec.Close()

	rows, err := s.ListProxyRequests(0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	raw, err := os.ReadFile(rows[0].RawLogPath)
	if err != nil {
		t.Fatalf("read raw file: %v", err)
	}
	if !bytes.Contains(raw, []byte(plain)) {
		t.Fatalf("decoded JSON not found in raw file (gzip not inflated):\n%s", raw)
	}
}

// TestCaptureRecorder_TruncatedGzipVerbatim asserts a truncated gzip body is
// written verbatim with the marker rather than inflated — the gzip stream is
// incomplete and can't be safely decoded (BACI-305).
func TestCaptureRecorder_TruncatedGzipVerbatim(t *testing.T) {
	s := recorderTestStore(t)
	logDir := t.TempDir()
	rec := newCaptureRecorder(s, logDir, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	// A real gzip stream chopped in half — inflating it would error.
	full := gzipFixture(t, []byte(`{"big":"payload"}`))
	partial := full[:len(full)/2]
	rec.Record(proxy.RequestObservation{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages", Status: 200,
		Started: time.Now(), Ended: time.Now(),
		RawResponse:             partial,
		ResponseTruncated:       true,
		ResponseContentType:     "application/json",
		ResponseContentEncoding: "gzip",
	})
	rec.Close()

	rows, err := s.ListProxyRequests(0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	raw, err := os.ReadFile(rows[0].RawLogPath)
	if err != nil {
		t.Fatalf("read raw file: %v", err)
	}
	if !bytes.Contains(raw, []byte("[gzip body truncated — not decoded]")) {
		t.Fatalf("truncated-gzip marker missing:\n%s", raw)
	}
	if !bytes.Contains(raw, partial) {
		t.Fatalf("truncated gzip bytes should be written verbatim")
	}
}

// TestCaptureRecorder_ClassifiesAnthropic asserts the index row carries the
// BACI-305 classification for an api.anthropic.com /v1/messages SSE capture:
// is_anthropic=1, is_stream=1, content_type populated (base type only).
func TestCaptureRecorder_ClassifiesAnthropic(t *testing.T) {
	s := recorderTestStore(t)
	rec := newCaptureRecorder(s, t.TempDir(), slog.New(slog.NewTextHandler(os.Stderr, nil)))

	rec.Record(proxy.RequestObservation{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages", Status: 200,
		Started: time.Now(), Ended: time.Now(),
		ResponseContentType: "text/event-stream; charset=utf-8",
	})
	// A non-Anthropic host on a /v1/ path must NOT classify as Anthropic.
	rec.Record(proxy.RequestObservation{
		Method: "GET", Host: "example.com", Path: "/v1/messages", Status: 200,
		Started: time.Now(), Ended: time.Now(),
		ResponseContentType: "application/json",
	})
	rec.Close()

	rows, err := s.ListProxyRequests(0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var anthropic, other *model.ProxyRequest
	for _, r := range rows {
		switch r.Host {
		case "api.anthropic.com":
			anthropic = r
		case "example.com":
			other = r
		}
	}
	if anthropic == nil || other == nil {
		t.Fatalf("expected both rows, got %+v", rows)
	}
	if !anthropic.IsAnthropic {
		t.Errorf("anthropic capture is_anthropic = false, want true")
	}
	if !anthropic.IsStream {
		t.Errorf("SSE capture is_stream = false, want true")
	}
	if anthropic.ContentType != "text/event-stream" {
		t.Errorf("content_type = %q, want base type text/event-stream (params stripped)", anthropic.ContentType)
	}
	if other.IsAnthropic {
		t.Errorf("non-anthropic host classified as anthropic")
	}
}

// TestCaptureRecorder_ParsesAnthropicMessage asserts a parseable Anthropic SSE
// capture (is_anthropic + is_stream + not-truncated) is parsed into a
// proxy_messages row, while a non-stream JSON capture and a truncated SSE
// capture both leave proxy_messages empty (BACI-306). The SSE response is
// gzipped on the wire — exercising the recorder's inflate-then-parse path the
// way real Anthropic streams arrive.
func TestCaptureRecorder_ParsesAnthropicMessage(t *testing.T) {
	s := recorderTestStore(t)
	rec := newCaptureRecorder(s, t.TempDir(), slog.New(slog.NewTextHandler(os.Stderr, nil)))

	const reqBody = `{"model":"claude-opus-4-8","system":[{"type":"text","text":"sys"}],` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"stream":true}`
	const sse = "event: message_start\n" +
		`data: {"type":"message_start","message":{"model":"claude-opus-4-8","usage":{"input_tokens":50}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello there"}}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":9}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n"

	// (1) Parseable SSE turn.
	rec.Record(proxy.RequestObservation{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages", Status: 200,
		Started: time.Now(), Ended: time.Now(),
		RawRequest:              []byte(reqBody),
		RequestHeaderBlock:      "POST /v1/messages HTTP/1.1\r\nContent-Type: application/json\r\n",
		RawResponse:             gzipFixture(t, []byte(sse)),
		ResponseHeaderBlock:     "HTTP/2.0 200 OK\r\nContent-Type: text/event-stream; charset=utf-8\r\nContent-Encoding: gzip\r\n",
		ResponseContentType:     "text/event-stream; charset=utf-8",
		ResponseContentEncoding: "gzip",
	})
	// (2) Non-stream JSON (count_tokens) — no message row.
	rec.Record(proxy.RequestObservation{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages/count_tokens", Status: 200,
		Started: time.Now(), Ended: time.Now(),
		RawResponse:         []byte(`{"input_tokens":5}`),
		ResponseHeaderBlock: "HTTP/2.0 200 OK\r\nContent-Type: application/json\r\n",
		ResponseContentType: "application/json",
	})
	// (3) Truncated SSE — un-inflatable, must be skipped.
	rec.Record(proxy.RequestObservation{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages", Status: 200,
		Started: time.Now(), Ended: time.Now(),
		RawRequest:              []byte(reqBody),
		RawResponse:             gzipFixture(t, []byte(sse))[:10],
		ResponseTruncated:       true,
		ResponseHeaderBlock:     "HTTP/2.0 200 OK\r\nContent-Type: text/event-stream\r\nContent-Encoding: gzip\r\n",
		ResponseContentType:     "text/event-stream",
		ResponseContentEncoding: "gzip",
	})
	rec.Close()

	// Exactly one proxy_messages row: the parseable SSE turn.
	pm, err := s.CaptureMessage(1) // proxy_request_id 1 = the first capture.
	if err != nil {
		t.Fatalf("CaptureMessage(1): %v (the parseable SSE turn should have a row)", err)
	}
	if !pm.IsPrimary {
		t.Errorf("first capture should classify primary (establishes the thread)")
	}
	if pm.Usage.InputTokens != 50 || pm.Usage.OutputTokens != 9 {
		t.Errorf("merged usage = %+v, want input 50 / output 9", pm.Usage)
	}
	if pm.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q, want end_turn", pm.StopReason)
	}
	// The non-stream and truncated captures (proxy_request_id 2, 3) have no row.
	if _, err := s.CaptureMessage(2); err != store.ErrNotFound {
		t.Errorf("non-stream JSON capture should have no message row, err = %v", err)
	}
	if _, err := s.CaptureMessage(3); err != store.ErrNotFound {
		t.Errorf("truncated SSE capture should have no message row, err = %v", err)
	}
}

// TestCaptureRecorder_ResolvesDispatch asserts the recorder attributes a
// capture from Claude Code's own request ids: the per-subagent
// X-Claude-Code-Agent-Id resolves through the agent-id→dispatch binding
// (precise — and wins over the session's newer active dispatch), and absent a
// binding it falls back to the session's active dispatch. session_id is stored
// raw from the header; an unknown session leaves dispatch_id NULL.
func TestCaptureRecorder_ResolvesDispatch(t *testing.T) {
	s := recorderTestStore(t)
	repo, err := s.CreateRepo("PROX", "proxy-test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	const sessionID = "sess-corr-resolve"
	sess, err := s.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: sessionID, RepoID: repo.ID, Actor: "agent-claude",
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	// Two dispatches on the same session. dispB is created later, so it is the
	// session's "active" dispatch (ActiveDispatchForSession orders newest-first).
	dispA, err := s.AddDispatch(store.AddDispatchIn{
		RepoID: repo.ID, TargetSessionID: sess.SessionID, Payload: "older", CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("dispatch A: %v", err)
	}
	dispB, err := s.AddDispatch(store.AddDispatchIn{
		RepoID: repo.ID, TargetSessionID: sess.SessionID, Payload: "newer", CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("dispatch B: %v", err)
	}
	// Bind the subagent agent-id to the OLDER dispatch, so a hit proves the
	// binding wins over the session's newer active dispatch (the fallback).
	if err := s.BindSubagentDispatch("a79b9dc411ab02ff6", dispA.ID, sessionID, time.Now()); err != nil {
		t.Fatalf("bind: %v", err)
	}

	rec := newCaptureRecorder(s, t.TempDir(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	// (1) Precise: the agent id resolves through the binding to dispA.
	rec.Record(proxy.RequestObservation{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages", Status: 200,
		Started: time.Now(), Ended: time.Now(),
		ClaudeSessionID: sessionID, ClaudeAgentID: "a79b9dc411ab02ff6",
	})
	// (2) Fallback: no agent id, so the session's active dispatch (dispB) wins.
	rec.Record(proxy.RequestObservation{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages", Status: 200,
		Started: time.Now(), Ended: time.Now(),
		ClaudeSessionID: sessionID,
	})
	// (3) Unknown session: stored raw, no dispatch resolved.
	rec.Record(proxy.RequestObservation{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages", Status: 200,
		Started: time.Now(), Ended: time.Now(),
		ClaudeSessionID: "sess-unknown",
	})
	rec.Close()

	rows, err := s.ListProxyRequests(0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var bound, fallback, unknown int
	for _, r := range rows {
		switch {
		case r.ClaudeAgentID == "a79b9dc411ab02ff6":
			bound++
			if r.SessionID != sessionID {
				t.Errorf("bound row session_id = %q, want %q", r.SessionID, sessionID)
			}
			if r.DispatchID == nil || *r.DispatchID != dispA.ID {
				t.Errorf("bound row dispatch_id = %v, want dispA %d", r.DispatchID, dispA.ID)
			}
		case r.SessionID == sessionID:
			fallback++
			if r.DispatchID == nil || *r.DispatchID != dispB.ID {
				t.Errorf("fallback row dispatch_id = %v, want dispB %d", r.DispatchID, dispB.ID)
			}
		case r.SessionID == "sess-unknown":
			unknown++
			if r.DispatchID != nil {
				t.Errorf("unknown session should leave dispatch_id NULL: %+v", r)
			}
		default:
			t.Errorf("unexpected row: %+v", r)
		}
	}
	if bound != 1 || fallback != 1 || unknown != 1 {
		t.Errorf("row counts bound=%d fallback=%d unknown=%d, want 1/1/1", bound, fallback, unknown)
	}
}
