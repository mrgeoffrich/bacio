// BACI-240 audio-preferences API test. Mirrors
// TestDisplayPreferencesRoundtrip on a single boolean — defaults to
// false, PUT round-trips, GET reflects the write.
package api_test

import (
	"net/http"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
)

func TestAudioPreferencesRoundtrip(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})

	type out struct {
		ShippedSfx bool `json:"shipped_sfx"`
	}

	// Default — false (audio is opt-in).
	resp, body := apiGet(t, ts.URL+"/settings/audio-preferences")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: status %d, body %s", resp.StatusCode, body)
	}
	var got out
	mustJSON(t, body, &got)
	if got.ShippedSfx {
		t.Fatal("default shipped_sfx must be false")
	}

	// Set true.
	resp, body = apiPut(t, ts.URL+"/settings/audio-preferences", map[string]any{"shipped_sfx": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set: status %d, body %s", resp.StatusCode, body)
	}
	mustJSON(t, body, &got)
	if !got.ShippedSfx {
		t.Fatal("after PUT true, response must reflect true")
	}

	// Re-read — persists.
	_, body = apiGet(t, ts.URL+"/settings/audio-preferences")
	mustJSON(t, body, &got)
	if !got.ShippedSfx {
		t.Fatal("after PUT true, GET must still return true")
	}

	// Dry-run reports the projected value without persisting.
	resp, body = apiPut(t, ts.URL+"/settings/audio-preferences?dry_run=true", map[string]any{"shipped_sfx": false})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dry-run set: status %d, body %s", resp.StatusCode, body)
	}
	mustJSON(t, body, &got)
	if got.ShippedSfx {
		t.Fatal("dry-run PUT false should project false in body")
	}
	// Persisted value still true.
	_, body = apiGet(t, ts.URL+"/settings/audio-preferences")
	mustJSON(t, body, &got)
	if !got.ShippedSfx {
		t.Fatal("dry-run PUT must not flip the stored value")
	}

	// Unknown JSON field is rejected by the strict decoder.
	resp, body = apiPut(t, ts.URL+"/settings/audio-preferences", map[string]any{"shipped_sfx": false, "rogue": 1})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown-field PUT: status %d, body %s; want 400", resp.StatusCode, body)
	}
}
