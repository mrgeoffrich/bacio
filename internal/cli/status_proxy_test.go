package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/version"
)

// fakeVersionServer stands up an httptest server that answers GET /version
// with the supplied version string, returning the addr (host:port) the
// proxy-version probe would dial. Mirrors the standalone proxy's /version.
func fakeVersionServer(t *testing.T, ver string) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"version": ver})
	}))
	t.Cleanup(ts.Close)
	return strings.TrimPrefix(ts.URL, "http://")
}

// TestProxyVersionSkewRec covers the BACI-344 operator-visibility hook: a
// running proxy on a DIFFERENT version yields a recommendation naming both
// versions; a matching version or no server at all is silent.
func TestProxyVersionSkewRec(t *testing.T) {
	t.Run("skew_flags", func(t *testing.T) {
		addr := fakeVersionServer(t, "v0.0.0-different")
		rec := proxyVersionSkewRec(addr)
		if rec == "" {
			t.Fatal("expected a recommendation for a version-skewed proxy, got none")
		}
		for _, must := range []string{addr, "v0.0.0-different", version.String(), "bacio proxy serve"} {
			if !strings.Contains(rec, must) {
				t.Errorf("recommendation missing %q:\n%s", must, rec)
			}
		}
	})

	t.Run("matching_version_silent", func(t *testing.T) {
		addr := fakeVersionServer(t, version.String())
		if rec := proxyVersionSkewRec(addr); rec != "" {
			t.Errorf("expected silence for a matching version, got: %s", rec)
		}
	})

	t.Run("no_server_silent", func(t *testing.T) {
		// A port nothing listens on (best-effort connect failure → silent).
		if rec := proxyVersionSkewRec("127.0.0.1:0"); rec != "" {
			t.Errorf("expected silence when no proxy is running, got: %s", rec)
		}
	})

	t.Run("empty_addr_silent", func(t *testing.T) {
		if rec := proxyVersionSkewRec(""); rec != "" {
			t.Errorf("expected silence for an empty addr, got: %s", rec)
		}
	})
}
