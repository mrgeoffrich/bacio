package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/mrgeoffrich/bacio/internal/proxy"
	"github.com/mrgeoffrich/bacio/internal/store"
)

type ctxKey string

const (
	ctxKeyActor ctxKey = "actor"
)

const defaultActor = "api"

// statusRecorder lets requestLog capture the response status without
// breaking handlers that write directly to the underlying ResponseWriter.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Unwrap exposes the wrapped ResponseWriter so http.ResponseController can
// reach the underlying connection's Flush / SetWriteDeadline / SetReadDeadline
// through this wrapper (Go 1.20+ unwraps via this method). Without it, the
// streaming reverse proxy can neither flush SSE incrementally nor lift the
// server's WriteTimeout for a long Anthropic turn — see clearStreamDeadline.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func recoverPanic(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				// http.ErrAbortHandler is the stdlib sentinel a handler
				// panics with to abort a request silently — ReverseProxy
				// raises it when it can't finish streaming the response,
				// almost always because the client (the agent) hung up
				// mid-stream. net/http's own conn.serve recovers it
				// without logging and just drops the connection; re-panic
				// so that path runs. Logging it as an ERROR with a stack
				// and trying to writeError to a dead connection is noise.
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}
				logger.Error("panic in handler",
					"err", rec,
					"path", r.URL.Path,
					"method", r.Method,
					"stack", string(debug.Stack()),
				)
				writeError(w, http.StatusInternalServerError, "internal", "internal server error", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func requestLog(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"actor", ActorFromContext(r.Context()),
			"remote_addr", r.RemoteAddr,
		)
	})
}

// proxyStreamTimeout is the per-connection read/write deadline
// clearStreamDeadline installs for the /anthropic/* reverse-proxy route. It
// is deliberately well above the Claude client's own 600s request cap
// (X-Stainless-Timeout) so the client's timeout — never the bacio server's —
// is what bounds a turn, while still backstopping a wedged/half-open
// connection rather than pinning it forever.
const proxyStreamTimeout = 15 * time.Minute

// clearStreamDeadline lifts the API server's short Read/WriteTimeout
// (server.go: ReadTimeout 15s, WriteTimeout 30s) for the streaming
// reverse-proxy route. Those timeouts are right for the JSON API but fatal to
// the proxy: the connection's write deadline is set when the request is read,
// so a slow or rate-limited Anthropic turn whose time-to-first-byte (or total
// stream) crosses 30s has its downstream write deadline expire before the
// proxy can relay the response — the agent then receives a truncated SSE
// stream (no message_stop) and retries, which is the "responses take forever /
// keep failing" symptom. We push the deadline out to proxyStreamTimeout for
// this exchange only; the rest of the API keeps the protective 30s.
//
// Best-effort: a ResponseWriter that doesn't support deadline control leaves
// the server default in place (no worse than before). statusRecorder.Unwrap
// is what lets the controller reach the real connection through the log
// wrapper.
func clearStreamDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		deadline := time.Now().Add(proxyStreamTimeout)
		_ = rc.SetWriteDeadline(deadline)
		_ = rc.SetReadDeadline(deadline)
		next.ServeHTTP(w, r)
	})
}

func auth(next http.Handler, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /healthz is unauthenticated, and the BACI-301 reverse-proxy
		// route (/anthropic/*) is auth-exempt by prefix: agent traffic
		// carries its own Anthropic auth, not bacio's bearer token, so a
		// configured token must never block it.
		if token == "" || r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, proxy.PathPrefix+"/") {
			next.ServeHTTP(w, r)
			return
		}
		got := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(got, prefix) ||
			subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(got, prefix)), []byte(token)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func actorMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("X-Actor")
		actor := defaultActor
		if raw != "" {
			clean, err := store.ValidateActor(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_input", err.Error(),
					map[string]any{"field": "X-Actor"})
				return
			}
			actor = clean
		}
		ctx := context.WithValue(r.Context(), ctxKeyActor, actor)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func bodyCap(next http.Handler, max int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			r.Body = http.MaxBytesReader(w, r.Body, max)
		}
		next.ServeHTTP(w, r)
	})
}

// cors handles cross-origin requests from a browser-hosted bundle,
// using a strict allow-list rather than a wildcard. Origins are
// matched case-sensitively against the Origin header; "*" entries
// are honoured but discouraged (incompatible with credentialed
// requests). When the allow-list is empty the middleware is a no-op
// — same-origin browsers don't send an Origin header on simple
// fetches and don't preflight at all, so omitting headers in the
// default deployment keeps the response surface tight.
//
// Sits outermost in the chain so a CORS preflight is answered
// before auth runs — otherwise a cross-origin OPTIONS would always
// 401 when a bearer token is configured, which is the wrong shape.
func cors(next http.Handler, allowed []string) http.Handler {
	if len(allowed) == 0 {
		return next
	}
	allow := make(map[string]struct{}, len(allowed))
	wildcard := false
	for _, o := range allowed {
		if o == "*" {
			wildcard = true
			continue
		}
		allow[o] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			_, ok := allow[origin]
			if ok || wildcard {
				echo := origin
				if wildcard && !ok {
					echo = "*"
				}
				w.Header().Set("Access-Control-Allow-Origin", echo)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Actor, X-Dry-Run")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Max-Age", "600")
			}
		}
		if r.Method == http.MethodOptions {
			// Preflight short-circuits — even when the origin doesn't
			// match the allow-list, return 204 (with no CORS headers)
			// rather than fall through to the auth middleware, which
			// would 401 and confuse the browser. The missing
			// Access-Control-Allow-Origin tells the browser the
			// preflight failed.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ActorFromContext retrieves the resolved actor stamped onto the request
// context by actorMiddleware. Returns the default if missing so callers
// don't have to nil-check.
func ActorFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyActor).(string); ok && v != "" {
		return v
	}
	return defaultActor
}

// readBody pulls the request body, surfacing 413 for the body cap and
// 400 for any other read error. Returns ok=false if a response was
// already written.
func readBody(r *http.Request, w http.ResponseWriter) ([]byte, bool) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds 4 MiB", nil)
		} else {
			writeError(w, http.StatusBadRequest, "invalid_input", err.Error(), nil)
		}
		return nil, false
	}
	return raw, true
}
