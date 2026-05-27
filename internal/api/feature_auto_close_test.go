package api_test

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
)

// TestAPI_PutFeatureAutoClose_FlipsBit (BACI-250) round-trips the
// feature.auto-close endpoint:
//   - PUT with {enabled:false} sets state_manual=true,
//   - PUT with {enabled:true} clears it back to false,
//   - each real flip emits exactly one feature.auto-close audit row,
//   - the state column is never touched.
func TestAPI_PutFeatureAutoClose_FlipsBit(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")

	// Flip OFF — sticky bit goes on.
	resp, body := apiPut(t, ts.URL+"/repos/MINI/features/auth/auto-close", map[string]any{"enabled": false})
	if resp.StatusCode != 200 {
		t.Fatalf("PUT enabled=false: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"state_manual": true`) && !strings.Contains(string(body), `"state_manual":true`) {
		t.Fatalf("PUT response missing state_manual:true, got %s", body)
	}
	if !strings.Contains(string(body), `"state": "active"`) && !strings.Contains(string(body), `"state":"active"`) {
		t.Fatalf("PUT response should leave state=active, got %s", body)
	}
	assertHistoryOps(t, s, []string{"feature.auto-close"})

	// Confirm the DB row.
	feat, err := s.GetFeatureBySlug(repo.ID, "auth")
	if err != nil {
		t.Fatalf("get feature: %v", err)
	}
	if !feat.StateManual {
		t.Fatalf("state_manual=false after auto-close OFF, want true")
	}
	if string(feat.State) != "active" {
		t.Fatalf("state=%q after auto-close OFF; auto-close must not touch state", feat.State)
	}

	// Flip back ON — sticky bit clears.
	resp, body = apiPut(t, ts.URL+"/repos/MINI/features/auth/auto-close", map[string]any{"enabled": true})
	if resp.StatusCode != 200 {
		t.Fatalf("PUT enabled=true: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"state_manual": false`) && !strings.Contains(string(body), `"state_manual":false`) {
		t.Fatalf("PUT response missing state_manual:false, got %s", body)
	}
	assertHistoryOps(t, s, []string{"feature.auto-close", "feature.auto-close"})
}

// TestAPI_PutFeatureAutoClose_DryRun confirms the projection path
// returns the projected state_manual without writing the DB.
func TestAPI_PutFeatureAutoClose_DryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")

	resp, body := apiPut(t, ts.URL+"/repos/MINI/features/auth/auto-close?dry_run=true", map[string]any{"enabled": false})
	if resp.StatusCode != 200 {
		t.Fatalf("dry-run: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"state_manual": true`) && !strings.Contains(string(body), `"state_manual":true`) {
		t.Fatalf("dry-run response missing state_manual:true projection, got %s", body)
	}
	assertHistoryOps(t, s, nil)
	feat, err := s.GetFeatureBySlug(repo.ID, "auth")
	if err != nil {
		t.Fatalf("get feature: %v", err)
	}
	if feat.StateManual {
		t.Fatalf("dry-run wrote state_manual=true to the DB")
	}
}

// TestAPI_PutFeatureAutoClose_MissingEnabledField rejects a body whose
// `enabled` field is absent (pointer-of-bool sees a nil and the handler
// returns 400) so a caller can't silently get the false-default.
func TestAPI_PutFeatureAutoClose_MissingEnabledField(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")

	resp, body := apiPut(t, ts.URL+"/repos/MINI/features/auth/auto-close", map[string]any{})
	if resp.StatusCode != 400 {
		t.Fatalf("missing enabled: status=%d, want 400, body=%s", resp.StatusCode, body)
	}
	assertHistoryOps(t, s, nil)
}

// TestAPI_PutFeatureAutoClose_UnknownSlug exercises the 404 path
// through resolveFeatureOnRepo.
func TestAPI_PutFeatureAutoClose_UnknownSlug(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiPut(t, ts.URL+"/repos/MINI/features/nope/auto-close", map[string]any{"enabled": false})
	if resp.StatusCode != 404 {
		t.Fatalf("PUT unknown slug: status=%d, want 404", resp.StatusCode)
	}
	assertHistoryOps(t, s, nil)
}

// TestAPI_PutFeatureAutoClose_Idempotent confirms that flipping to the
// already-current value returns 200 but does NOT emit an extra audit
// row — mirrors feature.hide's idempotency semantics.
func TestAPI_PutFeatureAutoClose_Idempotent(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")

	// First real flip — OFF.
	resp, _ := apiPut(t, ts.URL+"/repos/MINI/features/auth/auto-close", map[string]any{"enabled": false})
	if resp.StatusCode != 200 {
		t.Fatalf("first flip OFF: %d", resp.StatusCode)
	}
	assertHistoryOps(t, s, []string{"feature.auto-close"})

	// Idempotent re-flip — still OFF.
	resp, _ = apiPut(t, ts.URL+"/repos/MINI/features/auth/auto-close", map[string]any{"enabled": false})
	if resp.StatusCode != 200 {
		t.Fatalf("idempotent re-flip OFF: %d", resp.StatusCode)
	}
	assertHistoryOps(t, s, []string{"feature.auto-close"})

	// Flip back ON — adds one new row.
	resp, _ = apiPut(t, ts.URL+"/repos/MINI/features/auth/auto-close", map[string]any{"enabled": true})
	if resp.StatusCode != 200 {
		t.Fatalf("flip ON: %d", resp.StatusCode)
	}
	assertHistoryOps(t, s, []string{"feature.auto-close", "feature.auto-close"})
}
