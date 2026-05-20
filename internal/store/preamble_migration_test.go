package store

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestRefreshDispatchPreambleNewDefault: a fresh DB seeds the
// _dispatch_preamble row with the BACI-76 default already (the embedded
// default is the new one), so GetDispatchPreamble returns the
// spawn-the-per-mode-subagent text and never the retired
// `general-purpose` wording.
func TestRefreshDispatchPreambleNewDefault(t *testing.T) {
	s := newTestStore(t)
	body, err := s.GetDispatchPreamble()
	if err != nil {
		t.Fatalf("GetDispatchPreamble: %v", err)
	}
	if strings.Contains(body, `subagent_type = "general-purpose"`) {
		t.Fatalf("fresh-DB preamble still spawns general-purpose:\n%s", body)
	}
	if !strings.Contains(body, "Subagent:") {
		t.Fatalf("fresh-DB preamble does not reference the Subagent stub line:\n%s", body)
	}
}

// TestRefreshDispatchPreambleUpgradesOldDefault: simulate a pre-BACI-76
// DB by writing the old embedded default into the row, then re-run the
// refresh — it must replace the body with the new default.
func TestRefreshDispatchPreambleUpgradesOldDefault(t *testing.T) {
	s := newTestStore(t)
	old := strings.TrimRight(oldDispatchPreambleBACI52, "\r\n")
	if _, err := s.DB.Exec(
		`UPDATE prompt_templates SET body = ? WHERE slug = ?`,
		old, model.BuiltinTemplatePreamble,
	); err != nil {
		t.Fatalf("seed old preamble: %v", err)
	}
	if err := refreshDispatchPreamble(s.DB); err != nil {
		t.Fatalf("refreshDispatchPreamble: %v", err)
	}
	body, err := s.GetDispatchPreamble()
	if err != nil {
		t.Fatalf("GetDispatchPreamble: %v", err)
	}
	if body != model.DefaultPromptBodyForBuiltinSlug(model.BuiltinTemplatePreamble) {
		t.Fatalf("old default was not refreshed to the new default:\n%s", body)
	}
}

// TestRefreshDispatchPreambleLeavesCustomBody: a user-customised
// preamble body is left untouched by the refresh.
func TestRefreshDispatchPreambleLeavesCustomBody(t *testing.T) {
	s := newTestStore(t)
	const custom = "my own preamble — do whatever I say"
	if _, err := s.DB.Exec(
		`UPDATE prompt_templates SET body = ? WHERE slug = ?`,
		custom, model.BuiltinTemplatePreamble,
	); err != nil {
		t.Fatalf("seed custom preamble: %v", err)
	}
	if err := refreshDispatchPreamble(s.DB); err != nil {
		t.Fatalf("refreshDispatchPreamble: %v", err)
	}
	body, err := s.GetDispatchPreamble()
	if err != nil {
		t.Fatalf("GetDispatchPreamble: %v", err)
	}
	if body != custom {
		t.Fatalf("customised preamble was modified: got %q, want %q", body, custom)
	}
}
