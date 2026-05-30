package proxy

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
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
	return New(u, nil, nil), u
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

	front := httptest.NewServer(New(u, nil, nil))
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

// fakeRecorder collects observations for assertions. It signals each Record
// on a channel so a test can wait for the (Close-driven) capture without a
// sleep.
type fakeRecorder struct {
	mu   sync.Mutex
	obs  []RequestObservation
	recd chan struct{}
}

func newFakeRecorder() *fakeRecorder {
	return &fakeRecorder{recd: make(chan struct{}, 8)}
}

func (f *fakeRecorder) Record(o RequestObservation) {
	f.mu.Lock()
	f.obs = append(f.obs, o)
	f.mu.Unlock()
	f.recd <- struct{}{}
}

func (f *fakeRecorder) waitOne(t *testing.T) RequestObservation {
	t.Helper()
	select {
	case <-f.recd:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a recorded observation")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.obs[len(f.obs)-1]
}

// newProxyWithRecorder is newProxy plus a recorder so capture-path tests can
// assert what the proxy observed.
func newProxyWithRecorder(t *testing.T, rec Recorder, upstreamHandler http.HandlerFunc) http.Handler {
	t.Helper()
	upstream := httptest.NewServer(upstreamHandler)
	t.Cleanup(upstream.Close)
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	return New(u, nil, rec)
}

// TestProxy_RecordsObservation asserts the recorder receives the
// transport-level fields for a forwarded request: method, host, path,
// status, byte counts each way, and a non-zero duration. The raw bodies are
// captured too, with sensitive headers redacted.
func TestProxy_RecordsObservation(t *testing.T) {
	rec := newFakeRecorder()
	var upstreamHost string
	h := newProxyWithRecorder(t, rec, func(w http.ResponseWriter, r *http.Request) {
		upstreamHost = r.Host
		_, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte("response-body-1234"))
	})
	front := httptest.NewServer(h)
	t.Cleanup(front.Close)

	req, _ := http.NewRequest(http.MethodPost, front.URL+"/anthropic/v1/messages",
		strings.NewReader("request-body-xyz"))
	req.Header.Set("X-Api-Key", "sk-secret-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	// Drain + close so the capture body's Close fires the recorder.
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	obs := rec.waitOne(t)
	if obs.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", obs.Method)
	}
	if obs.Host != upstreamHost {
		t.Errorf("observed host = %q, want upstream host %q", obs.Host, upstreamHost)
	}
	if obs.Path != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages (prefix stripped)", obs.Path)
	}
	if obs.Status != http.StatusOK {
		t.Errorf("status = %d, want 200", obs.Status)
	}
	if obs.BytesIn != int64(len("request-body-xyz")) {
		t.Errorf("bytes_in = %d, want %d", obs.BytesIn, len("request-body-xyz"))
	}
	if obs.BytesOut != int64(len("response-body-1234")) {
		t.Errorf("bytes_out = %d, want %d", obs.BytesOut, len("response-body-1234"))
	}
	if obs.Duration <= 0 {
		t.Errorf("duration = %v, want > 0", obs.Duration)
	}
	if string(obs.RawRequest) != "request-body-xyz" {
		t.Errorf("raw request = %q, want %q", obs.RawRequest, "request-body-xyz")
	}
	if string(obs.RawResponse) != "response-body-1234" {
		t.Errorf("raw response = %q, want %q", obs.RawResponse, "response-body-1234")
	}
	if strings.Contains(obs.RequestHeaderBlock, "sk-secret-key") {
		t.Errorf("API key leaked into the captured request headers:\n%s", obs.RequestHeaderBlock)
	}
}

// TestProxy_RecordsUpstreamError asserts an upstream-error request records a
// status-0 observation rather than recording nothing (the request still
// shows up in the index, the proxy surfaces a 502 to the client).
func TestProxy_RecordsUpstreamError(t *testing.T) {
	rec := newFakeRecorder()
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	u, err := url.Parse(dead.URL)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dead.Close() // upstream now refuses connections

	front := httptest.NewServer(New(u, nil, rec))
	t.Cleanup(front.Close)

	resp, err := http.Get(front.URL + "/anthropic/v1/messages")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()

	obs := rec.waitOne(t)
	if obs.Status != 0 {
		t.Errorf("status = %d, want 0 for an upstream error", obs.Status)
	}
	if obs.Path != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages", obs.Path)
	}
}

// TestProxy_CaptureDoesNotBufferSSE re-asserts the BACI-301 streaming
// guarantee with a recorder installed: the capture wraps the stream, it must
// not buffer it. The second chunk is delayed; the client must still read the
// first chunk before that delay elapses.
func TestProxy_CaptureDoesNotBufferSSE(t *testing.T) {
	const delay = 300 * time.Millisecond
	rec := newFakeRecorder()
	h := newProxyWithRecorder(t, rec, func(w http.ResponseWriter, r *http.Request) {
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
	if elapsed >= delay {
		t.Fatalf("first chunk took %v (>= upstream's %v delay) — capture buffered the stream", elapsed, delay)
	}
}

// TestProxy_CapturesResponseEncoding asserts a gzip-Content-Encoding upstream
// reply records ResponseContentEncoding == "gzip" on the observation (so the
// recorder can decode the on-disk copy), and that the RAW captured response is
// the compressed gzip bytes — the proxy tees the wire bytes verbatim; decoding
// happens later in the recorder, never on the forwarded stream (BACI-305).
func TestProxy_CapturesResponseEncoding(t *testing.T) {
	rec := newFakeRecorder()
	gzipped := gzipBytes(t, []byte(`{"ok":true}`))
	h := newProxyWithRecorder(t, rec, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(gzipped)
	})
	front := httptest.NewServer(h)
	t.Cleanup(front.Close)

	// Set Accept-Encoding explicitly so the round-trip mirrors production: the
	// Claude client sets its own Accept-Encoding, which the proxy forwards, so
	// Go's transport sees a pre-set header and does NOT transparently decode
	// the reply (it only auto-decodes the gzip it added itself). That keeps the
	// upstream's Content-Encoding header — and the compressed body — intact
	// through the proxy. DisableCompression on the client transport stops the
	// outer client from also auto-decoding what it receives.
	req, _ := http.NewRequest(http.MethodGet, front.URL+"/anthropic/v1/messages", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	obs := rec.waitOne(t)
	if obs.ResponseContentEncoding != "gzip" {
		t.Errorf("ResponseContentEncoding = %q, want %q", obs.ResponseContentEncoding, "gzip")
	}
	if obs.ResponseContentType != "application/json" {
		t.Errorf("ResponseContentType = %q, want %q", obs.ResponseContentType, "application/json")
	}
	// The tee captured the wire (compressed) bytes verbatim — the recorder is
	// what inflates them for the on-disk copy.
	if !bytes.Equal(obs.RawResponse, gzipped) {
		t.Errorf("RawResponse should be the raw gzip bytes (%d), got %d bytes", len(gzipped), len(obs.RawResponse))
	}
}

// TestProxy_CapturesCorrelationHeader asserts an inbound X-Bacio-Corr header
// lands on the observation's CorrelationKey, is NOT forwarded upstream, and is
// redacted from the captured request header block (BACI-305).
func TestProxy_CapturesCorrelationHeader(t *testing.T) {
	rec := newFakeRecorder()
	var upstreamSawCorr string
	h := newProxyWithRecorder(t, rec, func(w http.ResponseWriter, r *http.Request) {
		upstreamSawCorr = r.Header.Get("X-Bacio-Corr")
		_, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte("ok"))
	})
	front := httptest.NewServer(h)
	t.Cleanup(front.Close)

	req, _ := http.NewRequest(http.MethodPost, front.URL+"/anthropic/v1/messages", strings.NewReader("body"))
	req.Header.Set("X-Bacio-Corr", "agent-deadbeef1234")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	obs := rec.waitOne(t)
	if obs.CorrelationKey != "agent-deadbeef1234" {
		t.Errorf("CorrelationKey = %q, want %q", obs.CorrelationKey, "agent-deadbeef1234")
	}
	if upstreamSawCorr != "" {
		t.Errorf("X-Bacio-Corr leaked to upstream: %q", upstreamSawCorr)
	}
	if strings.Contains(obs.RequestHeaderBlock, "agent-deadbeef1234") {
		t.Errorf("correlation key leaked into the captured request headers:\n%s", obs.RequestHeaderBlock)
	}
}

// TestProxy_ClassifiesContentType asserts the response Content-Type surfaces
// on the observation for both an SSE stream and a JSON reply, so the recorder
// can classify is_stream without re-deriving it (BACI-305).
func TestProxy_ClassifiesContentType(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
	}{
		{name: "sse", contentType: "text/event-stream"},
		{name: "json", contentType: "application/json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := newFakeRecorder()
			h := newProxyWithRecorder(t, rec, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", c.contentType)
				_, _ = w.Write([]byte("x"))
			})
			front := httptest.NewServer(h)
			t.Cleanup(front.Close)

			resp, err := http.Get(front.URL + "/anthropic/v1/messages")
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			_, _ = io.ReadAll(resp.Body)
			resp.Body.Close()

			obs := rec.waitOne(t)
			if obs.ResponseContentType != c.contentType {
				t.Errorf("ResponseContentType = %q, want %q", obs.ResponseContentType, c.contentType)
			}
		})
	}
}

// gzipBytes returns the gzip-compressed form of in — a test fixture for the
// gzip-encoding capture/decode coverage.
func gzipBytes(t *testing.T, in []byte) []byte {
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
