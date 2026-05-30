// BACI-312 timezone-preferences API test. Mirrors
// TestAudioPreferencesRoundtrip on a single string — defaults to "" (the
// auto-detect sentinel), PUT round-trips, GET reflects the write, a
// malformed IANA name is a 400, and dry-run touches nothing.
package api_test

import (
	"net/http"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
)

func TestTimezonePreferencesRoundtrip(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})

	type out struct {
		Timezone string `json:"timezone"`
	}

	// Default — empty (the React layer auto-detects on first run).
	resp, body := apiGet(t, ts.URL+"/settings/timezone-preferences")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: status %d, body %s", resp.StatusCode, body)
	}
	var got out
	mustJSON(t, body, &got)
	if got.Timezone != "" {
		t.Fatalf("default timezone = %q, want empty", got.Timezone)
	}

	// Set a valid zone.
	resp, body = apiPut(t, ts.URL+"/settings/timezone-preferences", map[string]any{"timezone": "Australia/Sydney"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set: status %d, body %s", resp.StatusCode, body)
	}
	mustJSON(t, body, &got)
	if got.Timezone != "Australia/Sydney" {
		t.Fatalf("after PUT, response timezone = %q, want Australia/Sydney", got.Timezone)
	}

	// Re-read — persists.
	_, body = apiGet(t, ts.URL+"/settings/timezone-preferences")
	mustJSON(t, body, &got)
	if got.Timezone != "Australia/Sydney" {
		t.Fatalf("after PUT, GET timezone = %q, want Australia/Sydney", got.Timezone)
	}

	// A malformed IANA name is a 400 and leaves the stored value intact.
	resp, body = apiPut(t, ts.URL+"/settings/timezone-preferences", map[string]any{"timezone": "not a zone!"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad timezone PUT: status %d, body %s; want 400", resp.StatusCode, body)
	}
	_, body = apiGet(t, ts.URL+"/settings/timezone-preferences")
	mustJSON(t, body, &got)
	if got.Timezone != "Australia/Sydney" {
		t.Fatalf("after rejected PUT, GET timezone = %q, want Australia/Sydney unchanged", got.Timezone)
	}

	// Dry-run reports the projected value without persisting.
	resp, body = apiPut(t, ts.URL+"/settings/timezone-preferences?dry_run=true", map[string]any{"timezone": "UTC"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dry-run set: status %d, body %s", resp.StatusCode, body)
	}
	mustJSON(t, body, &got)
	if got.Timezone != "UTC" {
		t.Fatalf("dry-run PUT should project UTC in body, got %q", got.Timezone)
	}
	_, body = apiGet(t, ts.URL+"/settings/timezone-preferences")
	mustJSON(t, body, &got)
	if got.Timezone != "Australia/Sydney" {
		t.Fatalf("dry-run PUT must not change the stored value, got %q", got.Timezone)
	}

	// Unknown JSON field is rejected by the strict decoder.
	resp, body = apiPut(t, ts.URL+"/settings/timezone-preferences", map[string]any{"timezone": "UTC", "rogue": 1})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown-field PUT: status %d, body %s; want 400", resp.StatusCode, body)
	}
}
