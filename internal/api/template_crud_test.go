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
	for _, key := range []string{"slug", "mode", "label", "body", "default", "is_builtin", "is_default", "allowed_states", "default_states", "states_are_default"} {
		if _, ok := rows[0][key]; !ok {
			t.Fatalf("missing key %q in DTO: %v", key, rows[0])
		}
	}
}

func TestPromptTemplateAddHappy(t *testing.T) {
	ts, s := newTestAPI(t, api.Options{})
	resp, body := apiPost(t, ts.URL+"/settings/templates", map[string]any{
		"slug":   "spike",
		"name":   "Spike",
		"body":   "Spike on {{issue_id}}.",
		"states": []string{"todo"},
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
		"slug":   "spike-dry",
		"name":   "Spike Dry",
		"body":   "Spike on {{issue_id}}.",
		"states": []string{"todo"},
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
		"slug":   "spike",
		"name":   "Spike",
		"body":   "Spike on {{issue_id}}.",
		"states": []string{"todo"},
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
