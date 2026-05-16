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

func recoverPanic(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
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

func auth(next http.Handler, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" || r.URL.Path == "/healthz" {
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
