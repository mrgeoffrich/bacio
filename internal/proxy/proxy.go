// Package proxy stands up the reverse-proxy forwarding pipe that agent
// HTTP(S) traffic routes through. BACI-301 ships only the transparent
// forwarder: a single httputil.ReverseProxy that forwards everything
// under the /anthropic/ prefix to the Anthropic upstream over real TLS,
// streaming SSE token responses without buffering and rewriting the Host
// header so the upstream's TLS SNI / vhost routing sees api.anthropic.com.
//
// Monitoring, per-FQDN aggregation, and request/response capture (the
// rest of the reverse-proxy-monitor feature) hang off this package in
// later tickets — they are deliberately not here yet.
package proxy

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// DefaultUpstream is the Anthropic API base the reverse proxy forwards
// to when no explicit upstream is configured. Real TLS, system roots.
const DefaultUpstream = "https://api.anthropic.com"

// PathPrefix is the route prefix the proxy is mounted at on the bacio
// api/web server. Requests arrive as /anthropic/v1/messages; the prefix
// is stripped before forwarding so the upstream sees /v1/messages.
const PathPrefix = "/anthropic"

// New returns an http.Handler that reverse-proxies every request to
// upstream. It strips the PathPrefix, rewrites the Host header to the
// upstream host (load-bearing for SNI/vhost — SetURL only sets
// Out.URL.Host), and flushes immediately so SSE streams aren't buffered.
// A dial/transport failure surfaces as a clean 502 rather than a panic.
func New(upstream *url.URL, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
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
	// Transport left nil → http.DefaultTransport (TLS verification on,
	// system root CAs) handles the upstream HTTPS connection.
	return rp
}
