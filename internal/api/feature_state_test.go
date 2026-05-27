package api_test

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
)

// TestAPI_PutFeatureState_FlipsState (BACI-199) round-trips the
// feature.state endpoint:
//   - PUT /repos/{p}/features/{slug}/state with {state:"done"} flips
//     the column,
//   - state_manual is NOT touched (BACI-250 decoupling),
//   - the audit log carries one feature.state row with "active → done"
//     in Details,
//   - a follow-up PUT with {state:"cancelled"} flips again.
func TestAPI_PutFeatureState_FlipsState(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")

	resp, body := apiPut(t, ts.URL+"/repos/MINI/features/auth/state", map[string]any{"slug": "auth", "state": "done"})
	if resp.StatusCode != 200 {
		t.Fatalf("PUT state=done: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"state": "done"`) && !strings.Contains(string(body), `"state":"done"`) {
		t.Fatalf("PUT response missing state:done, got %s", body)
	}
	// BACI-250: state writes no longer stamp state_manual. The row was
	// seeded with state_manual=0; it must still be 0 after a state write.
	if !strings.Contains(string(body), `"state_manual": false`) && !strings.Contains(string(body), `"state_manual":false`) {
		t.Fatalf("PUT response state_manual not false, got %s", body)
	}
	assertHistoryOps(t, s, []string{"feature.state"})

	// Verify the DB row directly — state_manual must remain false even
	// though the state column flipped.
	feat, err := s.GetFeatureBySlug(repo.ID, "auth")
	if err != nil {
		t.Fatalf("get feature: %v", err)
	}
	if feat.StateManual {
		t.Fatalf("state_manual=true after state write; BACI-250 decoupling violated")
	}

	resp, body = apiPut(t, ts.URL+"/repos/MINI/features/auth/state", map[string]any{"slug": "auth", "state": "cancelled"})
	if resp.StatusCode != 200 {
		t.Fatalf("PUT state=cancelled: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"state": "cancelled"`) && !strings.Contains(string(body), `"state":"cancelled"`) {
		t.Fatalf("PUT response missing state:cancelled, got %s", body)
	}
	assertHistoryOps(t, s, []string{"feature.state", "feature.state"})
}

// TestAPI_PutFeatureState_InvalidValue covers the 400 path: a body
// carrying an unknown state value is rejected before any DB write.
func TestAPI_PutFeatureState_InvalidValue(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")

	resp, body := apiPut(t, ts.URL+"/repos/MINI/features/auth/state", map[string]any{"slug": "auth", "state": "archived"})
	if resp.StatusCode != 400 {
		t.Fatalf("PUT state=archived: %d, body=%s", resp.StatusCode, body)
	}
	assertHistoryOps(t, s, nil)
}

// TestAPI_PutFeatureState_UnknownSlug exercises the 404 path through
// resolveFeatureOnRepo — a body whose JSON slug matches the path slug
// but no feature carries that slug.
func TestAPI_PutFeatureState_UnknownSlug(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	seedRepo(t, s)
	resp, _ := apiPut(t, ts.URL+"/repos/MINI/features/nope/state", map[string]any{"slug": "nope", "state": "done"})
	if resp.StatusCode != 404 {
		t.Fatalf("PUT unknown slug: status=%d, want 404", resp.StatusCode)
	}
	assertHistoryOps(t, s, nil)
}

// TestAPI_PutFeatureState_DryRun confirms the projection path returns
// the modified state but writes nothing. BACI-250: the projection no
// longer asserts state_manual — state writes are orthogonal to the
// auto-close pin.
func TestAPI_PutFeatureState_DryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	seedFeature(t, s, repo, "auth", "Auth")

	resp, body := apiPut(t, ts.URL+"/repos/MINI/features/auth/state?dry_run=true", map[string]any{"slug": "auth", "state": "done"})
	if resp.StatusCode != 200 {
		t.Fatalf("dry-run: %d, body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"state": "done"`) && !strings.Contains(string(body), `"state":"done"`) {
		t.Fatalf("dry-run response missing state:done projection, got %s", body)
	}
	assertHistoryOps(t, s, nil)
	feat, err := s.GetFeatureBySlug(repo.ID, "auth")
	if err != nil {
		t.Fatalf("get feature: %v", err)
	}
	if feat.StateManual {
		t.Fatalf("dry-run wrote state_manual=true to the DB")
	}
	if string(feat.State) != "active" {
		t.Fatalf("dry-run wrote state=%q to the DB", feat.State)
	}
}
