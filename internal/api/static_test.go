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

// TestUIHandler_DeepLinkWithExtension_FallsBackToIndex — BACI-215. A
// deep link to a route the React router owns (e.g.
// `/ui/documents/<filename>.md` or `<...>.jsonl`) must serve the SPA
// shell so the React Router can pick up the path and render the matching
// view; an earlier guard 404'd anything with a file extension, turning
// every refresh on a document deep link into a hard 404. Only paths
// under `/ui/assets/` (Vite's hashed bundle dir) should still 404 on
// miss — a miss there is a build error, not a deep link.
//
// Like the sibling tests this skips when the embed bundle isn't
// populated; the no-bundle branch is covered by
// TestUIHandler_NoBundle_Returns404WithHint.
func TestUIHandler_DeepLinkWithExtension_FallsBackToIndex(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{MountUI: true})

	// Probe /ui/ first to detect whether the embed bundle is populated;
	// if not, every path below /ui/ goes to the no-bundle hint and the
	// SPA-fallback contract isn't exercisable here.
	probe := do(t, http.MethodGet, ts.URL+"/ui/", nil, nil)
	probe.Body.Close()
	if probe.StatusCode != http.StatusOK {
		t.Skip("webui/ not populated; SPA fallback isn't reachable")
	}

	cases := []struct {
		name string
		path string
	}{
		{"markdown doc", "/ui/documents/some-plan.md"},
		{"jsonl transcript", "/ui/documents/bacio-transcript-BACI-203-agent-deadbeef.jsonl"},
		{"svg doc", "/ui/documents/some-design.svg"},
		// A deep link the router doesn't actually own still gets the
		// shell — the React router handles its own 404 from there.
		{"unknown deep route", "/ui/issues/MINI-9999"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := do(t, http.MethodGet, ts.URL+tc.path, nil, nil)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status: want 200, got %d", resp.StatusCode)
			}
			ct := resp.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "text/html") {
				t.Fatalf("content-type: want text/html*, got %q", ct)
			}
			body, _ := io.ReadAll(resp.Body)
			// index.html ships an `<div id="root">` mount point — the
			// cheapest marker that we got the SPA shell rather than a
			// random matched file.
			if !strings.Contains(string(body), "id=\"root\"") {
				t.Fatalf("body missing SPA root marker; got %q", string(body))
			}
		})
	}
}

// TestUIHandler_AssetsMiss_Returns404 — the SPA fallback deliberately
// does NOT cover `/ui/assets/*`. Vite emits hashed asset names and
// references them from index.html; a miss there is a build error, and
// serving HTML in place of a missing .js / .css would confuse the
// browser. Same skip-on-no-bundle shape as the sibling deep-link test.
func TestUIHandler_AssetsMiss_Returns404(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{MountUI: true})

	probe := do(t, http.MethodGet, ts.URL+"/ui/", nil, nil)
	probe.Body.Close()
	if probe.StatusCode != http.StatusOK {
		t.Skip("webui/ not populated; assets contract isn't reachable")
	}

	resp := do(t, http.MethodGet, ts.URL+"/ui/assets/nope-no-such-hash.js", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: want 404 on unknown asset, got %d", resp.StatusCode)
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
