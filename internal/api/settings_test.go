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
	for _, slug := range model.BuiltinTemplateSlugs() {
		got, ok := out[slug]
		if !ok {
			t.Fatalf("missing mode %q in list", slug)
		}
		want := model.DefaultPromptStates(model.DispatchMode(slug))
		if len(got) != len(want) {
			t.Fatalf("mode %q states len: got %d want %d", slug, len(got), len(want))
		}
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

// ---------- set states ----------

func TestPromptStatesSetHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	resp, body := apiReq(t, "PUT",
		ts.URL+"/settings/templates/review/states",
		map[string]any{"slug": "review", "states": []string{"in_review", "needs_action"}},
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

// ---------- board preferences (BACI-47/D) ----------

func TestBoardPreferencesGetDefault(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, body := apiGet(t, ts.URL+"/settings/board-preferences")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var out struct {
		HideEmptyColumns bool `json:"hide_empty_columns"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.HideEmptyColumns {
		t.Fatalf("expected default false, got true")
	}
}

func TestBoardPreferencesSetRoundTrip(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	resp, body := apiReq(t, "PUT", ts.URL+"/settings/board-preferences",
		map[string]any{"hide_empty_columns": true},
		map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 200 {
		t.Fatalf("PUT status: %d body: %s", resp.StatusCode, body)
	}
	var out struct {
		HideEmptyColumns bool `json:"hide_empty_columns"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.HideEmptyColumns {
		t.Fatalf("PUT response did not echo true")
	}
	// GET should now reflect the write.
	resp2, body2 := apiGet(t, ts.URL+"/settings/board-preferences")
	if resp2.StatusCode != 200 {
		t.Fatalf("GET status: %d body: %s", resp2.StatusCode, body2)
	}
	_ = json.Unmarshal(body2, &out)
	if !out.HideEmptyColumns {
		t.Fatalf("GET after PUT: hide_empty_columns is false")
	}
	stored, err := s.GetBoardHideEmptyColumns()
	if err != nil || !stored {
		t.Fatalf("store reads %v err=%v", stored, err)
	}
	assertHistoryOps(t, s, []string{"board_pref.update"})
}

func TestBoardPreferencesSetDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	resp, _ := apiReq(t, "PUT", ts.URL+"/settings/board-preferences?dry_run=true",
		map[string]any{"hide_empty_columns": true},
		map[string]string{"X-Actor": "agent-alice"})
	if resp.StatusCode != 200 || resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("status: %d header=%q", resp.StatusCode, resp.Header.Get("X-Dry-Run"))
	}
	stored, _ := s.GetBoardHideEmptyColumns()
	if stored {
		t.Fatalf("dry-run wrote pref")
	}
	assertHistoryOps(t, s, nil)
}

func TestBoardPreferencesSetUnknownField(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, _ := apiReq(t, "PUT", ts.URL+"/settings/board-preferences",
		map[string]any{"hide_empty_columns": true, "bogus": 1}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestBoardPreferencesAuthRequired(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{Token: "secret"})
	resp, _ := apiGet(t, ts.URL+"/settings/board-preferences")
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}
