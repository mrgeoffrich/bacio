package api_test

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
)

// TestAPI_PutFeatureState_FlipsState (BACI-199) round-trips the
// feature.state endpoint:
//   - PUT /repos/{p}/features/{slug}/state with {state:"done"} flips
//     the column and stamps state_manual=true,
//   - the audit log carries one feature.state row with "active → done"
//     in Details,
//   - a follow-up PUT with {state:"cancelled"} flips again (no error
//     because the verb is unconditional — every successful call mutates).
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
	if !strings.Contains(string(body), `"state_manual": true`) && !strings.Contains(string(body), `"state_manual":true`) {
		t.Fatalf("PUT response missing state_manual:true, got %s", body)
	}
	assertHistoryOps(t, s, []string{"feature.state"})

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
// the modified state + sticky bit but writes nothing.
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
