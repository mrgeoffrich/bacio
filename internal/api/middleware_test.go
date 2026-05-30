package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRecoverPanic_AbortHandlerReraisedSilently asserts that a handler
// panicking with http.ErrAbortHandler — the stdlib sentinel ReverseProxy
// raises when a client hangs up mid-stream — is re-panicked untouched and
// neither logged nor turned into a 500. net/http's own conn.serve recovers
// ErrAbortHandler without logging; our middleware must not pre-empt that.
func TestRecoverPanic_AbortHandlerReraisedSilently(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	h := recoverPanic(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}), logger)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/anthropic/v1/messages", nil)

	var got any
	func() {
		defer func() { got = recover() }()
		h.ServeHTTP(rec, req)
	}()

	if got != http.ErrAbortHandler {
		t.Fatalf("recovered value = %v, want http.ErrAbortHandler re-panicked so net/http handles it", got)
	}
	if rec.Code != http.StatusOK { // NewRecorder defaults to 200; we want nothing written
		t.Errorf("status = %d, want no write (default 200) — must not writeError on an aborted connection", rec.Code)
	}
	if logBuf.Len() != 0 {
		t.Errorf("logged %q, want nothing — ErrAbortHandler is an expected client disconnect, not an error", logBuf.String())
	}
}

// TestRecoverPanic_GenericPanicLogsAnd500 asserts the ordinary path is
// unchanged: a real panic is logged and surfaced to the client as a 500.
func TestRecoverPanic_GenericPanicLogsAnd500(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	h := recoverPanic(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}), logger)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/issues", nil)

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("generic panic escaped the middleware: %v", r)
			}
		}()
		h.ServeHTTP(rec, req)
	}()

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(logBuf.String(), "panic in handler") {
		t.Errorf("log = %q, want it to record the panic", logBuf.String())
	}
}
