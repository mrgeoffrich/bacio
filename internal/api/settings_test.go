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
	for _, m := range model.AllDispatchModes() {
		got, ok := out[string(m)]
		if !ok {
			t.Fatalf("missing mode %q in list", m)
		}
		if got != model.DefaultPromptTemplate(m) {
			t.Fatalf("mode %q: body deviates from default (default-only state)", m)
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

// ---------- list (states) ----------

func TestPromptStatesListAllDefaults(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, body := apiGet(t, ts.URL+"/settings/templates/states")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var out map[string][]string
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, m := range model.AllDispatchModes() {
		got, ok := out[string(m)]
		if !ok {
			t.Fatalf("missing mode %q in list", m)
		}
		want := model.DefaultPromptStates(m)
		if len(got) != len(want) {
			t.Fatalf("mode %q states len: got %d want %d", m, len(got), len(want))
		}
	}
}

// ---------- set body ----------

func TestPromptTemplateSetHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	resp, body := apiReq(t, "PUT",
		ts.URL+"/settings/templates/plan",
		map[string]any{"mode": "plan", "body": "review {{issue_id}}"},
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
		map[string]any{"mode": "review", "body": "x"}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestPromptTemplateSetUnknownMode(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiReq(t, "PUT", ts.URL+"/settings/templates/bogus",
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
	resp, _ := apiReq(t, "DELETE", ts.URL+"/settings/templates/bogus", nil, nil)
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

// ---------- set states ----------

func TestPromptStatesSetHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	resp, body := apiReq(t, "PUT",
		ts.URL+"/settings/templates/review/states",
		map[string]any{"mode": "review", "states": []string{"in_review", "needs_action"}},
		map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	stored, _ := s.GetPromptStates(model.DispatchModeReview)
	got := make([]string, len(stored))
	for i, st := range stored {
		got[i] = string(st)
	}
	if strings.Join(got, ",") != "in_review,needs_action" {
		t.Fatalf("states not persisted: %v", got)
	}
}

func TestPromptStatesSetRejectsEmpty(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiReq(t, "PUT", ts.URL+"/settings/templates/plan/states",
		map[string]any{"states": []string{}}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestPromptStatesSetRejectsInvalidState(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiReq(t, "PUT", ts.URL+"/settings/templates/plan/states",
		map[string]any{"states": []string{"todo", "bogus"}}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestPromptStatesSetDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	resp, _ := apiReq(t, "PUT",
		ts.URL+"/settings/templates/plan/states?dry_run=true",
		map[string]any{"states": []string{"todo", "in_progress"}}, nil)
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	stored, _ := s.GetPromptStates(model.DispatchModePlan)
	want := model.DefaultPromptStates(model.DispatchModePlan)
	if len(stored) != len(want) {
		t.Fatalf("dry-run wrote states: got %v, want default %v", stored, want)
	}
}

// ---------- reset states ----------

func TestPromptStatesResetHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	if err := s.SetPromptStates(model.DispatchModeReview, []model.State{model.StateDone}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	resp, _ := apiReq(t, "DELETE", ts.URL+"/settings/templates/review/states",
		nil, map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	stored, _ := s.GetPromptStates(model.DispatchModeReview)
	want := model.DefaultPromptStates(model.DispatchModeReview)
	if len(stored) != len(want) {
		t.Fatalf("reset did not revert: got %v want %v", stored, want)
	}
}
