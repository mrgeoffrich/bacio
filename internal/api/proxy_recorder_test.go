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

// TestCaptureRecorder_ResolvesDispatch asserts the recorder maps a correlation
// key (worktree slug) → active session → active dispatch and stamps session_id
// + dispatch_id; an unknown/empty key leaves them empty/NULL (BACI-305).
func TestCaptureRecorder_ResolvesDispatch(t *testing.T) {
	s := recorderTestStore(t)
	repo, err := s.CreateRepo("PROX", "proxy-test", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	const slug = "agent-corr-slug"
	sess, err := s.UpsertAgentSession(store.UpsertAgentSessionIn{
		SessionID: "sess-corr-resolve", RepoID: repo.ID, Actor: "agent-claude", WorktreeSlug: slug,
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	disp, err := s.AddDispatch(store.AddDispatchIn{
		RepoID: repo.ID, TargetSessionID: sess.SessionID, Payload: "work", CreatedBy: "supervisor",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	rec := newCaptureRecorder(s, t.TempDir(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	// Resolved capture: the slug maps back to the session + active dispatch.
	rec.Record(proxy.RequestObservation{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages", Status: 200,
		Started: time.Now(), Ended: time.Now(), CorrelationKey: slug,
	})
	// Unknown key: correlation columns stay empty.
	rec.Record(proxy.RequestObservation{
		Method: "POST", Host: "api.anthropic.com", Path: "/v1/messages", Status: 200,
		Started: time.Now(), Ended: time.Now(), CorrelationKey: "agent-does-not-exist",
	})
	rec.Close()

	rows, err := s.ListProxyRequests(0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var resolved, unresolved *model.ProxyRequest
	for _, r := range rows {
		if r.SessionID == sess.SessionID {
			resolved = r
		} else {
			unresolved = r
		}
	}
	if resolved == nil {
		t.Fatalf("no row carried the resolved session_id; rows=%+v", rows)
	}
	if resolved.DispatchID == nil || *resolved.DispatchID != disp.ID {
		t.Errorf("resolved dispatch_id = %v, want %d", resolved.DispatchID, disp.ID)
	}
	if unresolved == nil || unresolved.SessionID != "" || unresolved.DispatchID != nil {
		t.Errorf("unknown key should leave correlation empty: %+v", unresolved)
	}
}
