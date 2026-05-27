package api_test

import (
	"encoding/json"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/api"
)

// BACI-50: typed prompt-template CRUD over REST.

func TestPromptTemplatesFullList(t *testing.T) {
	ts, _ := newTestAPI(t, api.Options{})
	resp, body := apiGet(t, ts.URL+"/settings/templates/full")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) < 5 {
		t.Fatalf("expected >= 5 built-in templates, got %d", len(rows))
	}
	// Spot-check the rich DTO keys the web bundle reads.
	for _, key := range []string{"slug", "mode", "label", "body", "default", "is_builtin", "is_default"} {
		if _, ok := rows[0][key]; !ok {
			t.Fatalf("missing key %q in DTO: %v", key, rows[0])
		}
	}
}

func TestPromptTemplateAddHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	resp, body := apiPost(t, ts.URL+"/settings/templates", map[string]any{
		"slug": "spike",
		"name": "Spike",
		"body": "Spike on {{issue_id}}.",
	})
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var dto map[string]any
	_ = json.Unmarshal(body, &dto)
	if dto["slug"] != "spike" {
		t.Fatalf("slug: %v", dto["slug"])
	}
	if dto["is_builtin"] != false {
		t.Fatalf("is_builtin should be false for user template: %v", dto["is_builtin"])
	}
	assertHistoryOps(t, s, []string{"template.create"})
}

func TestPromptTemplateAddDryRun(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	resp, body := apiPost(t, ts.URL+"/settings/templates?dry_run=true", map[string]any{
		"slug": "spike-dry",
		"name": "Spike Dry",
		"body": "Spike on {{issue_id}}.",
	})
	if resp.StatusCode != 201 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("expected X-Dry-Run header")
	}
	// No row should exist (LookupBySlug — use list endpoint).
	resp2, body2 := apiGet(t, ts.URL+"/settings/templates/full")
	if resp2.StatusCode != 200 {
		t.Fatalf("list status: %d", resp2.StatusCode)
	}
	var rows []map[string]any
	_ = json.Unmarshal(body2, &rows)
	for _, r := range rows {
		if r["slug"] == "spike-dry" {
			t.Fatalf("dry-run wrote template: %v", r)
		}
	}
	// No audit row either.
	assertHistoryOps(t, s, nil)
}

func TestPromptTemplateRename(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	// Seed a user template first.
	resp, _ := apiPost(t, ts.URL+"/settings/templates", map[string]any{
		"slug": "spike",
		"name": "Spike",
		"body": "Spike on {{issue_id}}.",
	})
	if resp.StatusCode != 201 {
		t.Fatalf("seed: %d", resp.StatusCode)
	}
	resp2, body := apiPost(t, ts.URL+"/settings/templates/spike/rename", map[string]any{
		"new_slug": "investigation",
		"new_name": "Investigation",
	})
	if resp2.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp2.StatusCode, body)
	}
	var dto map[string]any
	_ = json.Unmarshal(body, &dto)
	if dto["slug"] != "investigation" {
		t.Fatalf("slug: %v", dto["slug"])
	}
	assertHistoryOps(t, s, []string{"template.create", "template.rename"})
}

func TestPromptTemplateDelete(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	// Delete a built-in by slug (`fix_review`).
	resp, body := apiDelete(t, ts.URL+"/settings/templates/fix_review/row", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var dto map[string]any
	_ = json.Unmarshal(body, &dto)
	if dto["slug"] != "fix_review" {
		t.Fatalf("returned slug: %v", dto["slug"])
	}
	assertHistoryOps(t, s, []string{"template.delete"})
}

// TestPromptTemplateActionLabel locks the BACI-67 REST surface: set
// the imperative override via PUT, clear via DELETE, dry-run leaves
// the row untouched, validator rejects control characters.
func TestPromptTemplateActionLabel(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})

	// Built-in `plan` ships with an action_label of "Plan".
	resp, body := apiGet(t, ts.URL+"/settings/templates/full")
	if resp.StatusCode != 200 {
		t.Fatalf("list: %d body: %s", resp.StatusCode, body)
	}
	var rows []map[string]any
	_ = json.Unmarshal(body, &rows)
	var foundPlan map[string]any
	for _, r := range rows {
		if r["slug"] == "plan" {
			foundPlan = r
			break
		}
	}
	if foundPlan == nil {
		t.Fatalf("plan template missing from list")
	}
	if foundPlan["action_label"] != "Plan" {
		t.Fatalf("action_label for plan: %v, want %q", foundPlan["action_label"], "Plan")
	}
	if foundPlan["default_action_label"] != "Plan" {
		t.Fatalf("default_action_label for plan: %v, want %q", foundPlan["default_action_label"], "Plan")
	}
	if foundPlan["action_label_is_default"] != true {
		t.Fatalf("action_label_is_default for plan: %v, want true", foundPlan["action_label_is_default"])
	}

	// PUT to override.
	resp2, body2 := apiPut(t, ts.URL+"/settings/templates/plan/action-label", map[string]any{
		"action_label": "Plan it",
	})
	if resp2.StatusCode != 200 {
		t.Fatalf("put status: %d body: %s", resp2.StatusCode, body2)
	}
	var got map[string]any
	_ = json.Unmarshal(body2, &got)
	if got["action_label"] != "Plan it" {
		t.Fatalf("post-put action_label: %v, want %q", got["action_label"], "Plan it")
	}
	if got["mode"] != "plan" {
		t.Fatalf("post-put mode: %v, want \"plan\"", got["mode"])
	}

	// Dry-run PUT projects without writing.
	resp3, _ := apiPut(t, ts.URL+"/settings/templates/plan/action-label?dry_run=1", map[string]any{
		"action_label": "Plan v2",
	})
	if resp3.StatusCode != 200 {
		t.Fatalf("dry-run put: %d", resp3.StatusCode)
	}
	if resp3.Header.Get("X-Dry-Run") != "applied" {
		t.Fatalf("dry-run missing X-Dry-Run header")
	}
	// Real value should still be "Plan it".
	respCheck, bodyCheck := apiGet(t, ts.URL+"/settings/templates/full")
	if respCheck.StatusCode != 200 {
		t.Fatalf("recheck list: %d", respCheck.StatusCode)
	}
	_ = json.Unmarshal(bodyCheck, &rows)
	for _, r := range rows {
		if r["slug"] == "plan" {
			if r["action_label"] != "Plan it" {
				t.Fatalf("dry-run leaked: action_label = %v", r["action_label"])
			}
		}
	}

	// Validator: control characters rejected.
	respBad, _ := apiPut(t, ts.URL+"/settings/templates/plan/action-label", map[string]any{
		"action_label": "Bad\x01label",
	})
	if respBad.StatusCode != 400 {
		t.Fatalf("expected 400 on control chars, got %d", respBad.StatusCode)
	}

	// DELETE clears the override (UI then derives from Name).
	respDel, bodyDel := apiDelete(t, ts.URL+"/settings/templates/plan/action-label", nil)
	if respDel.StatusCode != 200 {
		t.Fatalf("delete status: %d body: %s", respDel.StatusCode, bodyDel)
	}
	var gotDel map[string]any
	_ = json.Unmarshal(bodyDel, &gotDel)
	if gotDel["action_label"] != "" {
		t.Fatalf("post-delete action_label: %v, want \"\"", gotDel["action_label"])
	}

	// History should have two action-label updates (PUT + DELETE).
	assertHistoryOps(t, s, []string{"template.set_action_label", "template.set_action_label"})
}

func TestPromptTemplateRestoreBuiltins(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	// Delete one built-in.
	if resp, body := apiDelete(t, ts.URL+"/settings/templates/fix_review/row", nil); resp.StatusCode != 200 {
		t.Fatalf("delete: %d body: %s", resp.StatusCode, body)
	}
	// Restore.
	resp, body := apiPost(t, ts.URL+"/settings/templates/restore-builtins", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	restored, _ := out["restored"].([]any)
	if len(restored) != 1 || restored[0] != "fix_review" {
		t.Fatalf("restored: %v", out["restored"])
	}
	templates, _ := out["templates"].([]any)
	if len(templates) < 5 {
		t.Fatalf("expected post-state list >= 5: %d", len(templates))
	}
	assertHistoryOps(t, s, []string{"template.delete", "template.restore_defaults"})
}
