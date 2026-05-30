package proxy

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// newProxy spins an httptest upstream with the given handler and returns
// a proxy pointed at it, plus the upstream's URL for assertions.
func newProxy(t *testing.T, upstreamHandler http.HandlerFunc) (http.Handler, *url.URL) {
	t.Helper()
	upstream := httptest.NewServer(upstreamHandler)
	t.Cleanup(upstream.Close)
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	return New(u, nil), u
}

// TestProxy_ForwardsAndRewritesHost asserts the proxy forwards the body
// through to the upstream and rewrites the Host header to the upstream
// host (not the proxy's own 127.0.0.1:<port>), which is load-bearing for
// the real upstream's TLS SNI / vhost routing.
func TestProxy_ForwardsAndRewritesHost(t *testing.T) {
	var gotHost string
	h, upstream := newProxy(t, func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write([]byte("echo:" + string(body)))
	})
	front := httptest.NewServer(h)
	t.Cleanup(front.Close)

	resp, err := http.Post(front.URL+"/anthropic/v1/messages", "application/json", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != "echo:hello" {
		t.Fatalf("body = %q, want %q", body, "echo:hello")
	}
	if gotHost != upstream.Host {
		t.Fatalf("upstream saw Host %q, want rewritten to %q", gotHost, upstream.Host)
	}
}

// TestProxy_StripsPrefix asserts the /anthropic prefix is stripped before
// forwarding, so the upstream sees /v1/messages — never the prefix, never
// a doubled prefix.
func TestProxy_StripsPrefix(t *testing.T) {
	var gotPath string
	h, _ := newProxy(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
	})
	front := httptest.NewServer(h)
	t.Cleanup(front.Close)

	resp, err := http.Get(front.URL + "/anthropic/v1/messages")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()

	if gotPath != "/v1/messages" {
		t.Fatalf("upstream path = %q, want %q", gotPath, "/v1/messages")
	}
}

// TestProxy_StreamsSSE asserts streamed text/event-stream chunks arrive
// at the client incrementally (FlushInterval=-1) rather than buffering to
// EOF: the second chunk is delayed, and the client must read the first
// chunk before that delay elapses.
func TestProxy_StreamsSSE(t *testing.T) {
	const delay = 300 * time.Millisecond
	h, _ := newProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("upstream ResponseWriter is not a Flusher")
			return
		}
		_, _ = io.WriteString(w, "data: first\n\n")
		flusher.Flush()
		time.Sleep(delay)
		_, _ = io.WriteString(w, "data: second\n\n")
		flusher.Flush()
	})
	front := httptest.NewServer(h)
	t.Cleanup(front.Close)

	start := time.Now()
	resp, err := http.Get(front.URL + "/anthropic/v1/messages")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	firstLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read first chunk: %v", err)
	}
	elapsed := time.Since(start)

	if !strings.Contains(firstLine, "first") {
		t.Fatalf("first chunk = %q, want it to contain %q", firstLine, "first")
	}
	// If the proxy buffered the whole response, the first read would block
	// until both chunks were written (>= delay). It arriving well before
	// the delay proves the stream is flushed incrementally.
	if elapsed >= delay {
		t.Fatalf("first chunk took %v (>= upstream's %v delay) — response was buffered, not streamed", elapsed, delay)
	}
}

// TestProxy_UpstreamError502 asserts a dead upstream yields a clean 502
// from the ErrorHandler rather than a panic or a 500.
func TestProxy_UpstreamError502(t *testing.T) {
	// Point at a URL that will fail to dial (closed upstream).
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	u, err := url.Parse(dead.URL)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dead.Close() // now the upstream refuses connections

	front := httptest.NewServer(New(u, nil))
	t.Cleanup(front.Close)

	resp, err := http.Get(front.URL + "/anthropic/v1/messages")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}
