package api_test

import (
	"encoding/json"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
)

// syncStatus mirrors api.SyncStatusOut for decoding test responses.
type syncStatus struct {
	Prefix            string `json:"prefix"`
	Configured        bool   `json:"configured"`
	BackgroundEnabled bool   `json:"background_enabled"`
	InProgress        bool   `json:"in_progress"`
	LastError         *string `json:"last_error"`
	Remote            string `json:"remote"`
}

// TestSyncStatusListEmptyConfig: GET /sync over a DB whose one repo has
// no .bacio/config.yaml reports configured:false but still echoes the
// global background_enabled toggle (default true).
func TestSyncStatusList(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	resp, body := apiGet(t, ts.URL+"/sync")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var out []syncStatus
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 status, got %d", len(out))
	}
	st := out[0]
	if st.Prefix != repo.Prefix {
		t.Fatalf("prefix = %q, want %q", st.Prefix, repo.Prefix)
	}
	if st.Configured {
		t.Fatal("a repo with no .bacio/config.yaml should report configured:false")
	}
	if !st.BackgroundEnabled {
		t.Fatal("background_enabled should default to true (opt-out)")
	}
}

// TestSyncStatusGetPerRepo: GET /repos/{prefix}/sync returns the same
// per-repo shape.
func TestSyncStatusGetPerRepo(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	resp, body := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/sync")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var st syncStatus
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if st.Prefix != repo.Prefix || st.Configured {
		t.Fatalf("got %+v, want prefix=%s configured=false", st, repo.Prefix)
	}
}

// TestSyncStatusGetUnknownRepo: an unknown prefix 404s.
func TestSyncStatusGetUnknownRepo(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiGet(t, ts.URL+"/repos/ZZZZ/sync")
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestSyncPreferencesRoundTrip: PUT then GET /settings/sync-preferences,
// confirming the store reflects the write and an audit row is recorded.
func TestSyncPreferencesRoundTrip(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	// Default is true; flip to false.
	resp, body := apiReq(t, "PUT", ts.URL+"/settings/sync-preferences",
		map[string]any{"background_enabled": false},
		map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 200 {
		t.Fatalf("PUT status: %d body: %s", resp.StatusCode, body)
	}
	var out struct {
		BackgroundEnabled bool `json:"background_enabled"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.BackgroundEnabled {
		t.Fatal("PUT response did not echo false")
	}
	resp2, body2 := apiGet(t, ts.URL+"/settings/sync-preferences")
	if resp2.StatusCode != 200 {
		t.Fatalf("GET status: %d body: %s", resp2.StatusCode, body2)
	}
	_ = json.Unmarshal(body2, &out)
	if out.BackgroundEnabled {
		t.Fatal("GET after PUT: background_enabled is true, want false")
	}
	stored, err := s.GetSyncBackgroundEnabled()
	if err != nil || stored {
		t.Fatalf("store reads %v err=%v, want false", stored, err)
	}
	assertHistoryOps(t, s, []string{"sync_pref.update"})
}

// TestSyncPreferencesDryRun: ?dry_run=true projects the value without
// writing.
func TestSyncPreferencesDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	resp, _ := apiReq(t, "PUT", ts.URL+"/settings/sync-preferences?dry_run=true",
		map[string]any{"background_enabled": false}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("dry-run status = %d", resp.StatusCode)
	}
	// The store must still read the default (true) — no write happened.
	if v, _ := s.GetSyncBackgroundEnabled(); !v {
		t.Fatal("dry-run must not write the toggle")
	}
}

// TestSyncPreferencesUnknownField: an unknown field is rejected.
func TestSyncPreferencesUnknownField(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiReq(t, "PUT", ts.URL+"/settings/sync-preferences",
		map[string]any{"bogus": true}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400 for unknown field", resp.StatusCode)
	}
}
