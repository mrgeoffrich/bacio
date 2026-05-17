package api_test

import (
	"encoding/json"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
)

// /leader is wired in Server.Run only; the test harness goes through
// Server.Handler() directly so the leader handler returns the empty
// state (no in-flight elector). That's good enough — the production
// path is exercised manually.
func TestLeaderEmptyWhenNotRunning(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, body := apiGet(t, ts.URL+"/leader")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Default zero state when there's no live elector.
	if v, _ := out["amLeader"].(bool); v {
		t.Fatalf("expected amLeader=false in test harness, got %v", out)
	}
	if _, ok := out["holderLabel"]; !ok {
		t.Fatalf("expected holderLabel field, got %v", out)
	}
}
