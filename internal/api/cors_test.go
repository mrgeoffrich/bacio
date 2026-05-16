package api_test

import (
	"net/http"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
)

func TestCORS_AbsentAllowList_NoHeaders(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp := do(t, http.MethodGet, ts.URL+"/healthz", nil, map[string]string{
		"Origin": "http://example.com",
	})
	defer resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO should be empty with no allow-list, got %q", got)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
}

func TestCORS_AllowedOrigin_EchoesHeaders(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{
		CORSOrigins: []string{"http://localhost:5174"},
	})
	resp := do(t, http.MethodGet, ts.URL+"/healthz", nil, map[string]string{
		"Origin": "http://localhost:5174",
	})
	defer resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:5174" {
		t.Fatalf("ACAO: want allowed origin, got %q", got)
	}
	if got := resp.Header.Get("Vary"); got != "Origin" {
		t.Fatalf("Vary: want Origin, got %q", got)
	}
}

func TestCORS_DisallowedOrigin_NoHeaders(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{
		CORSOrigins: []string{"http://localhost:5174"},
	})
	resp := do(t, http.MethodGet, ts.URL+"/healthz", nil, map[string]string{
		"Origin": "http://evil.example",
	})
	defer resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO should be empty for disallowed origin, got %q", got)
	}
}

// Preflight on an allowed origin must short-circuit with 204 + CORS
// headers — and crucially must NOT pass through the auth middleware,
// which would 401 a cross-origin OPTIONS even with the wrong token.
func TestCORS_Preflight_AllowedOrigin_ShortCircuitsAuth(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{
		CORSOrigins: []string{"http://localhost:5174"},
		Token:       "secret",
	})
	resp := do(t, http.MethodOptions, ts.URL+"/repos", nil, map[string]string{
		"Origin":                         "http://localhost:5174",
		"Access-Control-Request-Method":  "GET",
		"Access-Control-Request-Headers": "Authorization",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight status: want 204, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:5174" {
		t.Fatalf("preflight ACAO: want allowed origin, got %q", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatalf("preflight ACAM should be set")
	}
}

func TestCORS_Wildcard_EchoesStar(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{
		CORSOrigins: []string{"*"},
	})
	resp := do(t, http.MethodGet, ts.URL+"/healthz", nil, map[string]string{
		"Origin": "http://anywhere.example",
	})
	defer resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("wildcard ACAO: want '*', got %q", got)
	}
}
