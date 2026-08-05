// BACI-368: GET /launch-repo echoes the prefix the cobra command
// resolved from its cwd, and reports "" when there wasn't one.
package api_test

import (
	"net/http"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
)

func TestLaunchRepoEndpoint(t *testing.T) {
	type out struct {
		Prefix string `json:"prefix"`
	}

	t.Run("configured", func(t *testing.T) {
		ts, _ := newTestAPI(t, api.Options{LaunchRepoPrefix: "MINI"})
		resp, body := apiGet(t, ts.URL+"/launch-repo")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d, body %s", resp.StatusCode, body)
		}
		var got out
		mustJSON(t, body, &got)
		if got.Prefix != "MINI" {
			t.Fatalf("prefix = %q, want MINI", got.Prefix)
		}
	})

	t.Run("unset", func(t *testing.T) {
		ts, _ := newTestAPI(t, api.Options{})
		resp, body := apiGet(t, ts.URL+"/launch-repo")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d, body %s", resp.StatusCode, body)
		}
		var got out
		mustJSON(t, body, &got)
		if got.Prefix != "" {
			t.Fatalf("prefix = %q, want empty", got.Prefix)
		}
	})
}
