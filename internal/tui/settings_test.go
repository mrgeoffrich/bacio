//go:build !js

package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

// settingsTestRepo spins up an in-memory store with one repo, the
// fixture every settings test builds on.
func settingsTestRepo(t *testing.T) (*store.Store, *model.Repo) {
	t.Helper()
	s, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	repo, err := s.CreateRepo("MINI", "mini", t.TempDir(), "")
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	return s, repo
}

// hasHistoryOp reports whether the audit log contains a row with the
// given op — used to assert a settings mutation recorded itself.
func hasHistoryOp(t *testing.T, s *store.Store, op string) bool {
	t.Helper()
	rows, err := s.ListHistory(store.HistoryFilter{Op: op, Limit: 100})
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	return len(rows) > 0
}

// TestSettingsLoadsAllStages: the Settings view loads every registered
// template (the seeded built-ins on a fresh store) in stable iteration
// order with its effective template body.
func TestSettingsLoadsAllStages(t *testing.T) {
	s, repo := settingsTestRepo(t)
	sv := newSettingsView(s, repo)

	slugs := model.BuiltinTemplateSlugs()
	if len(sv.stages) != len(slugs) {
		t.Fatalf("expected %d stages, got %d", len(slugs), len(sv.stages))
	}
	for i, st := range sv.stages {
		if st.slug != slugs[i] {
			t.Fatalf("stage %d: expected slug %q, got %q", i, slugs[i], st.slug)
		}
		if st.body == "" {
			t.Fatalf("stage %q: expected a non-empty effective body", st.slug)
		}
		if !st.bodyIsDefault {
			t.Fatalf("stage %q: a fresh store should report the body as default", st.slug)
		}
	}
}

// TestSettingsEditBodySaves: typing into the editor and pressing ctrl+s
// persists the new body and records a prompt_template.update audit row.
func TestSettingsEditBodySaves(t *testing.T) {
	s, repo := settingsTestRepo(t)
	sv := newSettingsView(s, repo)

	sv.openEditor(0)
	mode := model.DispatchMode(sv.stages[0].slug)

	sv.updateEditor(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ZZZ-custom")})
	sv.updateEditor(tea.KeyMsg{Type: tea.KeyCtrlS})

	got, err := s.GetPromptTemplate(mode)
	if err != nil {
		t.Fatalf("get template: %v", err)
	}
	if !strings.Contains(got, "ZZZ-custom") {
		t.Fatalf("expected saved body to contain the typed marker, got %q", got)
	}
	if !hasHistoryOp(t, s, "prompt_template.update") {
		t.Fatal("expected a prompt_template.update history row")
	}
	if sv.stages[0].bodyIsDefault {
		t.Fatal("stage should no longer report its body as default after a custom save")
	}
}

// TestSettingsResetBody: ctrl+r in the body pane reverts a built-in to
// its embedded default and records a prompt_template.reset row.
func TestSettingsResetBody(t *testing.T) {
	s, repo := settingsTestRepo(t)
	mode := model.DispatchMode(model.BuiltinTemplateSlugs()[0])
	if err := s.SetPromptTemplate(mode, "a custom body"); err != nil {
		t.Fatalf("seed custom template: %v", err)
	}

	sv := newSettingsView(s, repo)
	if sv.stages[0].bodyIsDefault {
		t.Fatal("precondition: stage 0 should start as custom")
	}
	sv.openEditor(0)
	sv.updateEditor(tea.KeyMsg{Type: tea.KeyCtrlR})

	got, err := s.GetPromptTemplate(mode)
	if err != nil {
		t.Fatalf("get template: %v", err)
	}
	if got != model.DefaultPromptBodyForBuiltinSlug(string(mode)) {
		t.Fatalf("expected reset to the built-in default, got %q", got)
	}
	if !hasHistoryOp(t, s, "prompt_template.reset") {
		t.Fatal("expected a prompt_template.reset history row")
	}
	if !sv.stages[0].bodyIsDefault {
		t.Fatal("stage should report its body as default after reset")
	}
}

// TestSettingsToggleStateSaves: in the state-gate pane, space toggles
// the focused state and writes a prompt_states.update audit row.
func TestSettingsToggleStateSaves(t *testing.T) {
	s, repo := settingsTestRepo(t)
	sv := newSettingsView(s, repo)

	sv.openEditor(0)
	mode := model.DispatchMode(sv.stages[0].slug)
	before, err := s.GetPromptStates(mode)
	if err != nil {
		t.Fatalf("get states: %v", err)
	}

	sv.updateEditor(tea.KeyMsg{Type: tea.KeyTab})
	if sv.editPane != paneGate {
		t.Fatal("expected the state-gate pane to be focused after tab")
	}
	sv.updateEditor(tea.KeyMsg{Type: tea.KeyRight})
	sv.updateEditor(tea.KeyMsg{Type: tea.KeySpace})

	after, err := s.GetPromptStates(mode)
	if err != nil {
		t.Fatalf("get states: %v", err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("expected the state-gate to gain one state on toggle (before=%v after=%v)", before, after)
	}
	if !hasHistoryOp(t, s, "prompt_states.update") {
		t.Fatal("expected a prompt_states.update history row")
	}
}

// TestSettingsCapturesInput is the headline regression test: while the
// body textarea is focused the shell must NOT treat `q` as quit — it
// must reach the textarea as a literal keystroke. Once focus moves to
// the state-gate pane, `q` quits again.
func TestSettingsCapturesInput(t *testing.T) {
	s, repo := settingsTestRepo(t)
	m, err := NewModel(s, repo, nil)
	if err != nil {
		t.Fatalf("new model: %v", err)
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// Switch to the Settings tab (index 5) and open the editor.
	settingsIdx := len(m.tabs) - 1
	if m.tabs[settingsIdx].name != "Settings" {
		t.Fatalf("expected the last tab to be Settings, got %q", m.tabs[settingsIdx].name)
	}
	m.active = settingsIdx
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open the editor on stage 0

	sv := m.tabs[settingsIdx].v.(*settingsView)
	if !sv.CapturesInput() {
		t.Fatal("expected CapturesInput to be true with the body pane focused")
	}

	// `q` through the shell must NOT quit — it must land in the textarea.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd != nil {
		if _, isQuit := cmd().(tea.QuitMsg); isQuit {
			t.Fatal("`q` quit the program while the body textarea was focused")
		}
	}
	if !strings.Contains(sv.ta.Value(), "q") {
		t.Fatalf("expected `q` to be typed into the textarea, value=%q", sv.ta.Value())
	}

	// tab to the state-gate pane — CapturesInput should now be false, so
	// `q` would quit again.
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if sv.CapturesInput() {
		t.Fatal("expected CapturesInput to be false with the state-gate pane focused")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("expected `q` to quit once the state-gate pane is focused")
	}
	if _, isQuit := cmd().(tea.QuitMsg); !isQuit {
		t.Fatal("expected a tea.QuitMsg from `q` with the state-gate pane focused")
	}
}

// TestSettingsValidationErrorSurfaced: a body the store rejects (over
// the 1 MiB cap) surfaces the error in the editor without losing the
// draft or writing anything. An over-long body is used rather than a
// control char because the textarea sanitises control runes on input,
// so a too-long body is the reliable way to drive a store rejection.
func TestSettingsValidationErrorSurfaced(t *testing.T) {
	s, repo := settingsTestRepo(t)
	sv := newSettingsView(s, repo)
	sv.openEditor(0)
	mode := model.DispatchMode(sv.stages[0].slug)

	tooLong := strings.Repeat("x", (1<<20)+1)
	sv.ta.SetValue(tooLong)
	sv.saveBody()

	if sv.editErr == nil {
		t.Fatal("expected a validation error to be surfaced in the editor")
	}
	if sv.ta.Value() != tooLong {
		t.Fatal("expected the rejected draft to be preserved in the textarea")
	}
	got, err := s.GetPromptTemplate(mode)
	if err != nil {
		t.Fatalf("get template: %v", err)
	}
	if got != model.DefaultPromptBodyForBuiltinSlug(string(mode)) {
		t.Fatalf("expected no write on a rejected body, store has %q", got)
	}
}
