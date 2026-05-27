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
	if !strings.Contains(body, "<subagent_type>") {
		t.Fatalf("fresh-DB preamble does not reference the <subagent_type> stub tag:\n%s", body)
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

// TestRefreshDispatchPreambleUpgradesBACI76TypoDefault: simulate a
// post-BACI-76 / pre-BACI-80 DB by writing the typo'd default (the one
// that told the supervisor to run the retired `bacio install-agents`
// plural verb) into the row, then re-run the refresh — it must replace
// the body with the corrected default that says `bacio install-agent`.
func TestRefreshDispatchPreambleUpgradesBACI76TypoDefault(t *testing.T) {
	s := newTestStore(t)
	typo := strings.TrimRight(oldDispatchPreambleBACI76Typo, "\r\n")
	if _, err := s.DB.Exec(
		`UPDATE prompt_templates SET body = ? WHERE slug = ?`,
		typo, model.BuiltinTemplatePreamble,
	); err != nil {
		t.Fatalf("seed typo'd preamble: %v", err)
	}
	if err := refreshDispatchPreamble(s.DB); err != nil {
		t.Fatalf("refreshDispatchPreamble: %v", err)
	}
	body, err := s.GetDispatchPreamble()
	if err != nil {
		t.Fatalf("GetDispatchPreamble: %v", err)
	}
	if body != model.DefaultPromptBodyForBuiltinSlug(model.BuiltinTemplatePreamble) {
		t.Fatalf("typo'd default was not refreshed to the new default:\n%s", body)
	}
	if strings.Contains(body, "install-agents") {
		t.Fatalf("refreshed preamble still contains the `install-agents` plural typo:\n%s", body)
	}
}

// TestRefreshDispatchPreambleUpgradesBACI80Default: simulate a
// post-BACI-80 / pre-BACI-85 DB by writing the BACI-80 default (which
// did not yet mention attach_transcript) into the row, then re-run the
// refresh — it must replace the body with the BACI-85 default that
// tells the supervisor to call mcp__bacio__attach_transcript.
func TestRefreshDispatchPreambleUpgradesBACI80Default(t *testing.T) {
	s := newTestStore(t)
	old := strings.TrimRight(oldDispatchPreambleBACI80, "\r\n")
	if _, err := s.DB.Exec(
		`UPDATE prompt_templates SET body = ? WHERE slug = ?`,
		old, model.BuiltinTemplatePreamble,
	); err != nil {
		t.Fatalf("seed BACI-80 preamble: %v", err)
	}
	if err := refreshDispatchPreamble(s.DB); err != nil {
		t.Fatalf("refreshDispatchPreamble: %v", err)
	}
	body, err := s.GetDispatchPreamble()
	if err != nil {
		t.Fatalf("GetDispatchPreamble: %v", err)
	}
	if body != model.DefaultPromptBodyForBuiltinSlug(model.BuiltinTemplatePreamble) {
		t.Fatalf("BACI-80 default was not refreshed to the new default:\n%s", body)
	}
	if !strings.Contains(body, "attach_transcript") {
		t.Fatalf("refreshed preamble does not mention attach_transcript:\n%s", body)
	}
}

// TestRefreshDispatchPreambleUpgradesBACI85Default: simulate a
// post-BACI-85 / pre-BACI-103 DB by writing the BACI-85 default (which
// told the supervisor to compose the Task `prompt` argument free-form)
// into the row, then re-run the refresh — it must replace the body with
// the BACI-103 default that passes a fixed verbatim Task-prompt stub.
func TestRefreshDispatchPreambleUpgradesBACI85Default(t *testing.T) {
	s := newTestStore(t)
	old := strings.TrimRight(oldDispatchPreambleBACI85, "\r\n")
	if _, err := s.DB.Exec(
		`UPDATE prompt_templates SET body = ? WHERE slug = ?`,
		old, model.BuiltinTemplatePreamble,
	); err != nil {
		t.Fatalf("seed BACI-85 preamble: %v", err)
	}
	if err := refreshDispatchPreamble(s.DB); err != nil {
		t.Fatalf("refreshDispatchPreamble: %v", err)
	}
	body, err := s.GetDispatchPreamble()
	if err != nil {
		t.Fatalf("GetDispatchPreamble: %v", err)
	}
	if body != model.DefaultPromptBodyForBuiltinSlug(model.BuiltinTemplatePreamble) {
		t.Fatalf("BACI-85 default was not refreshed to the new default:\n%s", body)
	}
	if strings.Contains(body, "a few lines giving the worker its job") {
		t.Fatalf("refreshed preamble still tells the supervisor to compose a free-form prompt:\n%s", body)
	}
	if !strings.Contains(body, "<dispatch_id>") {
		t.Fatalf("refreshed preamble does not reference the <dispatch_id> stub tag:\n%s", body)
	}
}

// TestRefreshDispatchPreambleUpgradesBACI103Default: simulate a
// post-BACI-103 / pre-XML-stub DB by writing the BACI-103 default
// (which spelled the stub as `Ticket: …`/`Mode: …`/`Subagent: …`
// colon-lines plus an appended `Dispatch ID: …` line) into the row,
// then re-run the refresh — it must replace the body with the
// XML-stub default that uses <issue_id>/<mode>/<subagent_type> tags.
func TestRefreshDispatchPreambleUpgradesBACI103Default(t *testing.T) {
	s := newTestStore(t)
	old := strings.TrimRight(oldDispatchPreambleBACI103, "\r\n")
	if _, err := s.DB.Exec(
		`UPDATE prompt_templates SET body = ? WHERE slug = ?`,
		old, model.BuiltinTemplatePreamble,
	); err != nil {
		t.Fatalf("seed BACI-103 preamble: %v", err)
	}
	if err := refreshDispatchPreamble(s.DB); err != nil {
		t.Fatalf("refreshDispatchPreamble: %v", err)
	}
	body, err := s.GetDispatchPreamble()
	if err != nil {
		t.Fatalf("GetDispatchPreamble: %v", err)
	}
	if body != model.DefaultPromptBodyForBuiltinSlug(model.BuiltinTemplatePreamble) {
		t.Fatalf("BACI-103 default was not refreshed to the XML-stub default:\n%s", body)
	}
	if !strings.Contains(body, "<issue_id>") || !strings.Contains(body, "<mode>") || !strings.Contains(body, "<dispatch_id>") {
		t.Fatalf("refreshed preamble does not reference the XML stub tags:\n%s", body)
	}
	if strings.Contains(body, "Ticket: ") {
		t.Fatalf("refreshed preamble still spells the stub as `Ticket: …`:\n%s", body)
	}
}

// TestRefreshDispatchPreambleUpgradesBACI225Default (BACI-226):
// simulate a post-XML-stub / pre-BACI-226 DB by writing the BACI-225
// default (which made no mention of the <base_branch> stub tag) into
// the row, then re-run the refresh — it must replace the body with the
// BACI-226 default that tells the supervisor to copy <base_branch>
// verbatim into the worker's Task prompt.
func TestRefreshDispatchPreambleUpgradesBACI225Default(t *testing.T) {
	s := newTestStore(t)
	old := strings.TrimRight(oldDispatchPreambleBACI225, "\r\n")
	if _, err := s.DB.Exec(
		`UPDATE prompt_templates SET body = ? WHERE slug = ?`,
		old, model.BuiltinTemplatePreamble,
	); err != nil {
		t.Fatalf("seed BACI-225 preamble: %v", err)
	}
	if err := refreshDispatchPreamble(s.DB); err != nil {
		t.Fatalf("refreshDispatchPreamble: %v", err)
	}
	body, err := s.GetDispatchPreamble()
	if err != nil {
		t.Fatalf("GetDispatchPreamble: %v", err)
	}
	if body != model.DefaultPromptBodyForBuiltinSlug(model.BuiltinTemplatePreamble) {
		t.Fatalf("BACI-225 default was not refreshed to the BACI-226 default:\n%s", body)
	}
	if !strings.Contains(body, "<base_branch>") {
		t.Fatalf("refreshed preamble does not reference the <base_branch> stub tag:\n%s", body)
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
