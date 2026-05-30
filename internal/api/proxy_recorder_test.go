package api

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mrgeoffrich/bacio/internal/proxy"
	"github.com/mrgeoffrich/bacio/internal/store"
)

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
