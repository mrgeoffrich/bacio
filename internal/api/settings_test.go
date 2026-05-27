package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// ---------- list (templates) ----------

func TestPromptTemplatesListAllDefaults(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, body := apiGet(t, ts.URL+"/settings/templates")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var out map[string]string
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Every dispatch mode should be present and equal to its built-in
	// default since nothing has been set yet.
	for _, slug := range model.BuiltinTemplateSlugs() {
		got, ok := out[slug]
		if !ok {
			t.Fatalf("missing mode %q in list", slug)
		}
		if got != model.DefaultPromptTemplate(model.DispatchMode(slug)) {
			t.Fatalf("mode %q: body deviates from default (default-only state)", slug)
		}
	}
}

func TestPromptTemplatesListAfterSet(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	if err := s.SetPromptTemplate(model.DispatchModePlan, "custom plan body"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	resp, body := apiGet(t, ts.URL+"/settings/templates")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out map[string]string
	_ = json.Unmarshal(body, &out)
	if out[string(model.DispatchModePlan)] != "custom plan body" {
		t.Fatalf("plan template: %q", out[string(model.DispatchModePlan)])
	}
}

// ---------- set body ----------

func TestPromptTemplateSetHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	resp, body := apiReq(t, "PUT",
		ts.URL+"/settings/templates/plan",
		map[string]any{"slug": "plan", "body": "review {{issue_id}}"},
		map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	stored, _ := s.GetPromptTemplate(model.DispatchModePlan)
	if stored != "review {{issue_id}}" {
		t.Fatalf("body not persisted: %q", stored)
	}
	assertHistoryOps(t, s, []string{"prompt_template.update"})
}

func TestPromptTemplateSetBodyRequired(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiReq(t, "PUT", ts.URL+"/settings/templates/plan",
		map[string]any{"body": ""}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestPromptTemplateSetModeMismatch(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiReq(t, "PUT", ts.URL+"/settings/templates/plan",
		map[string]any{"slug": "review", "body": "x"}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestPromptTemplateSetUnknownMode(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	// Capitals fail the slug-shape check in ParseDispatchMode.
	resp, _ := apiReq(t, "PUT", ts.URL+"/settings/templates/BOGUS",
		map[string]any{"body": "x"}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestPromptTemplateSetDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	resp, body := apiReq(t, "PUT",
		ts.URL+"/settings/templates/plan?dry_run=true",
		map[string]any{"body": "would-set"},
		map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d header=%q body=%s", resp.StatusCode, resp.Header.Get("X-Dry-Run"), body)
	}
	// Stored value should still be the default (no write happened).
	stored, _ := s.GetPromptTemplate(model.DispatchModePlan)
	if stored != model.DefaultPromptTemplate(model.DispatchModePlan) {
		t.Fatalf("dry-run wrote body: %q", stored)
	}
	assertHistoryOps(t, s, nil)
}

func TestPromptTemplateSetUnknownField(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiReq(t, "PUT", ts.URL+"/settings/templates/plan",
		map[string]any{"body": "x", "bogus": true}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

// ---------- reset body ----------

func TestPromptTemplateResetHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	if err := s.SetPromptTemplate(model.DispatchModePlan, "custom"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	resp, body := apiReq(t, "DELETE", ts.URL+"/settings/templates/plan",
		nil, map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	stored, _ := s.GetPromptTemplate(model.DispatchModePlan)
	if stored != model.DefaultPromptTemplate(model.DispatchModePlan) {
		t.Fatalf("reset did not revert: %q", stored)
	}
	// Audit row uses prompt_template.reset (the local backend's convention
	// for an empty-body Set).
	rows, _ := s.ListHistory(store.HistoryFilter{})
	want := false
	for _, r := range rows {
		if r.Op == "prompt_template.reset" {
			want = true
		}
	}
	if !want {
		t.Fatalf("expected a prompt_template.reset audit row, got %+v", rows)
	}
}

func TestPromptTemplateResetUnknownMode(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	// Capitals fail the slug-shape check in ParseDispatchMode.
	resp, _ := apiReq(t, "DELETE", ts.URL+"/settings/templates/BOGUS", nil, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestPromptTemplateResetDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	if err := s.SetPromptTemplate(model.DispatchModePlan, "custom"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	resp, _ := apiReq(t, "DELETE",
		ts.URL+"/settings/templates/plan?dry_run=true", nil, nil)
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	// Stored value should still be the custom one (no write happened).
	stored, _ := s.GetPromptTemplate(model.DispatchModePlan)
	if stored != "custom" {
		t.Fatalf("dry-run reset persisted: %q", stored)
	}
}

// ---------- BACI-252 regression: deleted endpoints stay 404 ----------

// TestRetiredStatesEndpointsAreUnreachable guards against accidental
// re-introduction of the BACI-241 / BACI-245 state-gate plumbing.
// Each of the four routes was deleted in BACI-252; the router serves
// no handler at any of these paths. The exact status code depends on
// whether the deleted path collides with a surviving `{mode}` template
// route — 404 (no route at all) and 405 (method-not-allowed because
// the path matches a different verb's wildcard) are both acceptable
// "this endpoint is gone" signals; 200/201 is the regression we are
// guarding against.
func TestRetiredStatesEndpointsAreUnreachable(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	gone := []struct {
		method string
		path   string
	}{
		{"GET", "/settings/state-graph"},
		{"GET", "/settings/templates/states"},
		{"PUT", "/settings/templates/plan/states"},
		{"DELETE", "/settings/templates/plan/states"},
	}
	for _, g := range gone {
		resp, body := apiReq(t, g.method, ts.URL+g.path, nil, nil)
		if resp.StatusCode != 404 && resp.StatusCode != 405 {
			t.Errorf("%s %s: status=%d body=%s, want 404 or 405", g.method, g.path, resp.StatusCode, body)
		}
	}
}

// ---------- BACI-235 per-repo default_feature ----------

func TestDefaultFeature_RoundTrip(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "catchall", "Catch-all")

	// GET on a fresh repo => {feature: null}.
	resp, body := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/settings/default-feature")
	if resp.StatusCode != 200 {
		t.Fatalf("GET (fresh) status: %d body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"feature": null`) {
		t.Fatalf("GET (fresh) body: %s, want feature:null", body)
	}

	// PUT sets the default.
	resp, body = apiPut(t,
		ts.URL+"/repos/"+repo.Prefix+"/settings/default-feature",
		map[string]any{"slug": "catchall"})
	if resp.StatusCode != 200 {
		t.Fatalf("PUT status: %d body: %s", resp.StatusCode, body)
	}
	var out struct {
		Feature *model.Feature `json:"feature"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("PUT decode: %v", err)
	}
	if out.Feature == nil || out.Feature.Slug != "catchall" {
		t.Fatalf("PUT body: %+v, want feature.slug=catchall", out.Feature)
	}

	// GET observes the set value.
	resp, body = apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/settings/default-feature")
	if resp.StatusCode != 200 {
		t.Fatalf("GET (after set) status: %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("GET decode: %v", err)
	}
	if out.Feature == nil || out.Feature.Slug != "catchall" {
		t.Fatalf("GET (after set) body: %+v", out.Feature)
	}

	// POST /repos/{prefix}/issues with no feature_slug picks up the
	// default (the core acceptance bullet).
	resp, body = apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/issues",
		map[string]any{"title": "implicit default"})
	if resp.StatusCode != 201 {
		t.Fatalf("POST issue status: %d body: %s", resp.StatusCode, body)
	}
	var iss model.Issue
	if err := json.Unmarshal(body, &iss); err != nil {
		t.Fatalf("POST issue decode: %v", err)
	}
	if iss.FeatureSlug != "catchall" {
		t.Fatalf("POST issue: FeatureSlug=%q, want catchall", iss.FeatureSlug)
	}

	// Explicit slug wins over the default.
	resp, body = apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/issues",
		map[string]any{"title": "explicit", "feature_slug": ""})
	if resp.StatusCode != 201 {
		t.Fatalf("POST explicit-empty status: %d body: %s", resp.StatusCode, body)
	}
	// (Empty explicit feature_slug is the same as "absent" — REST
	// transport collapses the two; the default still applies.)
	if err := json.Unmarshal(body, &iss); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if iss.FeatureSlug != "catchall" {
		t.Fatalf("empty explicit: FeatureSlug=%q, want catchall", iss.FeatureSlug)
	}

	// DELETE clears the default.
	resp, body = apiDelete(t, ts.URL+"/repos/"+repo.Prefix+"/settings/default-feature", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("DELETE status: %d body: %s", resp.StatusCode, body)
	}
	resp, body = apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/settings/default-feature")
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"feature": null`) {
		t.Fatalf("GET (after clear) status=%d body=%s", resp.StatusCode, body)
	}

	// Post-clear, a featureless POST stays featureless.
	resp, body = apiPost(t, ts.URL+"/repos/"+repo.Prefix+"/issues",
		map[string]any{"title": "post-clear"})
	if resp.StatusCode != 201 {
		t.Fatalf("POST post-clear status: %d body: %s", resp.StatusCode, body)
	}
	var iss2 model.Issue
	if err := json.Unmarshal(body, &iss2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if iss2.FeatureSlug != "" {
		t.Fatalf("post-clear: FeatureSlug=%q, want empty (body=%s)", iss2.FeatureSlug, body)
	}

	_ = feat
}

func TestDefaultFeature_SetUnknownFeature(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)

	resp, body := apiPut(t,
		ts.URL+"/repos/"+repo.Prefix+"/settings/default-feature",
		map[string]any{"slug": "no-such-feature"})
	if resp.StatusCode == 200 {
		t.Fatalf("expected non-200 for unknown feature, got 200 body=%s", body)
	}
}

func TestDefaultFeature_ClearsOnFeatureDelete(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	repo := seedRepo(t, s)
	feat := seedFeature(t, s, repo, "catchall", "Catch-all")

	// Set the default.
	if _, body := apiPut(t,
		ts.URL+"/repos/"+repo.Prefix+"/settings/default-feature",
		map[string]any{"slug": "catchall"}); !strings.Contains(string(body), "catchall") {
		t.Fatalf("set: body=%s", body)
	}

	// Delete the feature directly at the store boundary (mirrors
	// `bacio feature rm` reaching the same SQL).
	if err := s.DeleteFeature(feat.ID); err != nil {
		t.Fatalf("delete feature: %v", err)
	}

	// GET sees the FK-cascaded NULL — {feature: null}, no error.
	resp, body := apiGet(t, ts.URL+"/repos/"+repo.Prefix+"/settings/default-feature")
	if resp.StatusCode != 200 {
		t.Fatalf("GET status: %d body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"feature": null`) {
		t.Fatalf("GET body: %s, want feature:null after FK cascade", body)
	}
}

