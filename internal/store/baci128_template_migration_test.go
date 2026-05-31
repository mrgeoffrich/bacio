package store

import (
	"strings"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/model"
)

// TestRefreshAskUserQuestionTemplatesNewDefault: a fresh DB seeds
// every per-mode template body with the post-BACI-128 default
// already, so each carries the `Pass issue_id` instruction.
func TestRefreshAskUserQuestionTemplatesNewDefault(t *testing.T) {
	s := newTestStore(t)
	for _, slug := range []string{
		model.BuiltinTemplatePlan,
		model.BuiltinTemplateDesign,
		model.BuiltinTemplateImplement,
		model.BuiltinTemplateReview,
		model.BuiltinTemplateShip,
		model.BuiltinTemplateFixReview,
	} {
		var body string
		if err := s.DB.QueryRow(`SELECT body FROM prompt_templates WHERE slug = ?`, slug).Scan(&body); err != nil {
			t.Fatalf("read body for %s: %v", slug, err)
		}
		if !strings.Contains(body, "Pass `issue_id: <issue_id>`") {
			t.Errorf("fresh-DB %s template missing BACI-128 instruction:\n%s", slug, body)
		}
	}
}

// TestRefreshAskUserQuestionTemplatesUpgradesPreBACI128: simulate
// a pre-BACI-128 DB by writing each frozen previous-default body
// into the matching row, then re-run the refresh — it must
// replace every body with the new default. Covers all six
// per-mode templates plus the dispatch preamble.
func TestRefreshAskUserQuestionTemplatesUpgradesPreBACI128(t *testing.T) {
	s := newTestStore(t)
	for slug, oldDefault := range baci128PrevDefaults {
		if _, err := s.DB.Exec(
			`UPDATE prompt_templates SET body = ? WHERE slug = ?`,
			oldDefault, slug,
		); err != nil {
			t.Fatalf("seed old body for %s: %v", slug, err)
		}
	}
	if err := refreshAskUserQuestionTemplates(s.DB); err != nil {
		t.Fatalf("refreshAskUserQuestionTemplates: %v", err)
	}
	for slug := range baci128PrevDefaults {
		var body string
		if err := s.DB.QueryRow(`SELECT body FROM prompt_templates WHERE slug = ?`, slug).Scan(&body); err != nil {
			t.Fatalf("read body for %s: %v", slug, err)
		}
		want := model.DefaultPromptBodyForBuiltinSlug(slug)
		if body != want {
			t.Errorf("BACI-128 refresh of %s did not replace the body in place", slug)
		}
	}
}

// TestRefreshAskUserQuestionTemplatesLeavesCustomBodiesAlone: a
// stored body that doesn't byte-match the frozen previous default
// is treated as user-customised and not rewritten.
func TestRefreshAskUserQuestionTemplatesLeavesCustomBodiesAlone(t *testing.T) {
	s := newTestStore(t)
	const custom = "This is a deliberately-customised plan template body."
	if _, err := s.DB.Exec(
		`UPDATE prompt_templates SET body = ? WHERE slug = ?`,
		custom, model.BuiltinTemplatePlan,
	); err != nil {
		t.Fatalf("seed custom body: %v", err)
	}
	if err := refreshAskUserQuestionTemplates(s.DB); err != nil {
		t.Fatalf("refreshAskUserQuestionTemplates: %v", err)
	}
	var body string
	if err := s.DB.QueryRow(
		`SELECT body FROM prompt_templates WHERE slug = ?`, model.BuiltinTemplatePlan,
	).Scan(&body); err != nil {
		t.Fatalf("read plan body: %v", err)
	}
	if body != custom {
		t.Fatalf("customised plan body was rewritten by BACI-128 migration:\n  got:  %q\n  want: %q", body, custom)
	}
}

// TestRefreshAskUserQuestionTemplatesIdempotent: a second run is a
// no-op — once the new default is stored, the equality check
// against the OLD default fails and no UPDATE fires.
func TestRefreshAskUserQuestionTemplatesIdempotent(t *testing.T) {
	s := newTestStore(t)
	// First call: already on the new default (fresh DB).
	if err := refreshAskUserQuestionTemplates(s.DB); err != nil {
		t.Fatalf("refreshAskUserQuestionTemplates #1: %v", err)
	}
	var firstUpdatedAt string
	if err := s.DB.QueryRow(
		`SELECT updated_at FROM prompt_templates WHERE slug = ?`, model.BuiltinTemplatePlan,
	).Scan(&firstUpdatedAt); err != nil {
		t.Fatalf("read plan updated_at: %v", err)
	}
	// Second call: must not touch the row.
	if err := refreshAskUserQuestionTemplates(s.DB); err != nil {
		t.Fatalf("refreshAskUserQuestionTemplates #2: %v", err)
	}
	var secondUpdatedAt string
	if err := s.DB.QueryRow(
		`SELECT updated_at FROM prompt_templates WHERE slug = ?`, model.BuiltinTemplatePlan,
	).Scan(&secondUpdatedAt); err != nil {
		t.Fatalf("read plan updated_at again: %v", err)
	}
	if firstUpdatedAt != secondUpdatedAt {
		t.Fatalf("BACI-128 migration was not idempotent: updated_at changed from %q to %q",
			firstUpdatedAt, secondUpdatedAt)
	}
}

// TestRefreshAskUserQuestionTemplatesUpgradesPreamble locks in the
// dispatch-preamble rewrite: a stored body that byte-matches the
// pre-BACI-128 frozen default (the user never customised it) is replaced
// in place with the current default. The BACI-128 rename carried
// `issue_key = ...` → `issue_id = ...` inside the attach_transcript
// example; BACI-307 removed that example entirely, so the assertion is
// now body-equality against the current default rather than a substring
// check on the (deleted) example.
func TestRefreshAskUserQuestionTemplatesUpgradesPreamble(t *testing.T) {
	s := newTestStore(t)
	old := baci128PrevDefaults[model.BuiltinTemplatePreamble]
	if _, err := s.DB.Exec(
		`UPDATE prompt_templates SET body = ? WHERE slug = ?`,
		old, model.BuiltinTemplatePreamble,
	); err != nil {
		t.Fatalf("seed old preamble: %v", err)
	}
	if err := refreshAskUserQuestionTemplates(s.DB); err != nil {
		t.Fatalf("refreshAskUserQuestionTemplates: %v", err)
	}
	body, err := s.GetDispatchPreamble()
	if err != nil {
		t.Fatalf("GetDispatchPreamble: %v", err)
	}
	if body != model.DefaultPromptBodyForBuiltinSlug(model.BuiltinTemplatePreamble) {
		t.Fatalf("pre-BACI-128 preamble was not refreshed to the current default:\n%s", body)
	}
	if strings.Contains(body, "attach_transcript") {
		t.Fatalf("refreshed preamble still mentions the retired attach_transcript step:\n%s", body)
	}
}
