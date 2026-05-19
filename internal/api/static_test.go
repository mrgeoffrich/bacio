package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
)

// /ui/ and the embed setup depend on whether `webui/` is populated at
// compile time. On a CI runner without `./build.sh` (or `--skip-web`
// passed) having populated webui/, only the placeholder `.gitkeep` is
// present — every path below /ui/ falls into the "no bundle" branch
// (404 with hint).
// When the bundle IS present, the handler serves real files and these
// tests skip; the presence/absence branch is the contract.

func TestUIHandler_NoBundle_Returns404WithHint(t *testing.T) {
	// BACI-72: opt in to MountUI=true to keep the no-bundle-hint
	// behaviour reachable. The default `bacio api` shape (MountUI=false)
	// is covered by TestUIHandler_NotMounted_404 below.
	ts, _ := newTestAPI(t, api.Options{MountUI: true})
	resp := do(t, http.MethodGet, ts.URL+"/ui/", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: want 200 or 404, got %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusOK {
		t.Skip("webui/ is populated; this test exercises the absent-bundle path only")
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "web UI bundle hasn't been built") {
		t.Fatalf("hint missing from body: %q", string(body))
	}
}

func TestUIHandler_BareUI_RedirectsToSlashedForm(t *testing.T) {
	// BACI-72: the bare-/ui redirect only registers under MountUI=true.
	ts, _ := newTestAPI(t, api.Options{MountUI: true})
	// Disable redirect-follow so we can assert on the 301 itself.
	c := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/ui", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("status: want 301, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/ui/" {
		t.Fatalf("location: want /ui/, got %q", got)
	}
}

// TestUIHandler_NotMounted_404 — when MountUI is false (the default for
// `bacio api` after BACI-72) the /ui and /ui/ routes aren't registered.
// Both paths fall through to the ServeMux 404 with no special bundle
// hint body, and the bare /ui doesn't 301 either.
func TestUIHandler_NotMounted_404(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{}) // MountUI = false
	// /ui/ — prefix path.
	resp := do(t, http.MethodGet, ts.URL+"/ui/", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/ui/ status: want 404, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "web UI bundle hasn't been built") {
		t.Fatalf("/ui/ hint should NOT appear when route is unmounted; body=%q", string(body))
	}

	// /ui — bare. Same 404, no 301.
	c := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/ui", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	bareResp, err := c.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer bareResp.Body.Close()
	if bareResp.StatusCode != http.StatusNotFound {
		t.Fatalf("/ui status: want 404, got %d", bareResp.StatusCode)
	}
}
